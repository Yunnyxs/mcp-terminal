package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"terminal-mcp/internal/clierr"
	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/client"
	"terminal-mcp/internal/mcp/host"
	"terminal-mcp/internal/mcp/mcptest"
	"terminal-mcp/internal/mcp/registry"
)

type harness struct {
	*Runner
	out, err *bytes.Buffer
	p        config.Paths
}

func newHarness(t *testing.T, cfg registry.Config, stdin string) *harness {
	t.Helper()
	dir, err := os.MkdirTemp("", "tm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p := config.Paths{Dir: dir, RunDir: dir + "/run", LogDir: dir + "/mcp", OutDir: dir + "/mcp/out", MCPs: dir + "/mcps.json"}
	if err := registry.Save(p.MCPs, cfg); err != nil {
		t.Fatal(err)
	}
	h := &harness{out: &bytes.Buffer{}, err: &bytes.Buffer{}, p: p}
	h.Runner = newRunner(h.out, h.err, strings.NewReader(stdin), p, config.DefaultLimits(), false)
	return h
}

func (h *harness) reset() { h.out.Reset(); h.err.Reset() }

func isUsage(err error) bool {
	var u clierr.Usage
	return errors.As(err, &u)
}

func startHost(t *testing.T, h *harness, name string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- host.Run(ctx, name, h.p, host.Options{OpenLog: func(path string, _ int64) (io.Writer, error) {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return nil, err
			}
			return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		}})
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	c := client.New(h.p)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("host stopped before it was ready: %v", err)
		default:
		}
		if _, err := c.Ping(name); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("host not ready")
}

func TestMcpAddLsRm(t *testing.T) {
	h := newHarness(t, registry.Config{}, "")

	if err := h.mcp([]string{"ls"}); err != nil || !strings.Contains(h.out.String(), "(empty; add with: mcp add") {
		t.Errorf("ls (empty): %q err=%v", h.out, err)
	}
	h.reset()

	err := h.mcp([]string{"add", "fake", "--env", "SECRET=supersecret", "--cwd", "/tmp", "--idle", "10m", "--", "npx", "-y", "pkg", "--flag"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "fake: added to "+h.p.MCPs) || !strings.Contains(h.err.String(), "warning: env/header passed as arguments") {
		t.Errorf("add stdio: out=%q err=%q", h.out, h.err)
	}
	h.reset()
	if err := h.mcp([]string{"add", "web", "--url", "https://user:pw@host.example/mcp?token=abc", "--header", "Authorization=Bearer supersecret"}); err != nil {
		t.Fatal(err)
	}
	h.reset()

	cfg, err := registry.Load(h.p.MCPs)
	if err != nil {
		t.Fatal(err)
	}
	e := cfg["fake"]
	if e.Command != "npx" || strings.Join(e.Args, " ") != "-y pkg --flag" || e.Cwd != "/tmp" || e.IdleTimeout != "10m" || e.Env["SECRET"] != "supersecret" {
		t.Errorf("entry fake = %+v", e)
	}
	if fi, _ := os.Stat(h.p.MCPs); fi.Mode().Perm() != 0o600 {
		t.Errorf("mcps.json mode = %o, want 600", fi.Mode().Perm())
	}

	if err := h.mcp([]string{"ls"}); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	for _, want := range []string{"NAME", "fake", "stdio", "10m", "npx -y pkg --flag", "SECRET", "web", "http", "host.example/mcp", "Authorization", "(default)"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls does not contain %q:\n%s", want, out)
		}
	}
	for _, leaked := range []string{"supersecret", "pw@", "abc", "Bearer"} {
		if strings.Contains(out, leaked) {
			t.Errorf("ls leaked %q:\n%s", leaked, out)
		}
	}
	h.reset()

	if err := h.mcp([]string{"add", "fake", "--", "echo"}); err == nil || !strings.Contains(err.Error(), "already exists") || isUsage(err) {
		t.Errorf("duplicate: err = %v", err)
	}
	if err := h.mcp([]string{"add", "fake", "--force", "--", "echo"}); err != nil {
		t.Errorf("--force: %v", err)
	}
	if cfg, _ := registry.Load(h.p.MCPs); cfg["fake"].Command != "echo" || len(cfg["fake"].Env) != 0 {
		t.Errorf("--force did not overwrite cleanly: %+v", cfg["fake"])
	}
	h.reset()

	if err := h.mcp([]string{"rm", "nope"}); err == nil || !strings.Contains(err.Error(), "is not in") {
		t.Errorf("rm of a missing entry: err = %v", err)
	}
	if err := h.mcp([]string{"rm", "fake"}); err != nil || !strings.Contains(h.out.String(), "fake: removed from") {
		t.Errorf("rm: %q err=%v", h.out, err)
	}
	if cfg, _ := registry.Load(h.p.MCPs); len(cfg) != 1 || cfg["web"].URL == "" {
		t.Errorf("after rm: %+v", cfg)
	}
}

func TestMcpUsageErrors(t *testing.T) {
	h := newHarness(t, registry.Config{}, "")
	cases := map[string][]string{
		"no subcommand":              {},
		"wrong subcommand":           {"bogus"},
		"add without a name":         {"add", "--", "x"},
		"add with an invalid name":   {"add", "Bad Name", "--", "x"},
		"add without a target":       {"add", "x"},
		"add command and url":        {"add", "x", "--url", "http://y", "--", "z"},
		"add with a non-http url":    {"add", "x", "--url", "ftp://y"},
		"add with a wrong env":       {"add", "x", "--env", "no-equals", "--", "z"},
		"add with an unknown option": {"add", "x", "--bogus", "--", "z"},
		"rm without a name":          {"rm"},
		"rm extra":                   {"rm", "a", "b"},
	}
	for name, args := range cases {
		if err := h.mcp(args); !isUsage(err) {
			t.Errorf("%s: err = %v, want clierr.Usage", name, err)
		}
	}
	if cfg, _ := registry.Load(h.p.MCPs); len(cfg) != 0 {
		t.Errorf("a failed command must not change the config: %+v", cfg)
	}
}

func TestMcpRmUnloadsRunningHost(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	h := newHarness(t, registry.Config{"web": {URL: srv.URL}}, "")
	startHost(t, h, "web")

	if err := h.mcp([]string{"rm", "web"}); err != nil {
		t.Fatal(err)
	}
	if out := h.out.String(); !strings.Contains(out, "web: stopped") || !strings.Contains(out, "web: removed from") {
		t.Errorf("out = %q", out)
	}
	if _, err := client.New(h.p).Ping("web"); !errors.Is(err, client.ErrNotLoaded) {
		t.Errorf("host is still alive after rm: %v", err)
	}
}

func TestLoadAndLoaded(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	h := newHarness(t, registry.Config{"web": {URL: srv.URL}}, "")

	if err := h.loaded(nil); err != nil || !strings.Contains(h.out.String(), "no MCP servers loaded") {
		t.Errorf("loaded (empty): %q err=%v", h.out, err)
	}
	h.reset()

	startHost(t, h, "web")
	if err := h.load([]string{"web"}); err != nil {
		t.Fatal(err)
	}
	if out := h.out.String(); !strings.Contains(out, "web: already loaded (fakehttp 1.0.0, 4 tools, pid ") {
		t.Errorf("load = %q", out)
	}
	h.reset()

	if err := h.loaded(nil); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	for _, want := range []string{"NAME", "TYPE", "LEFT", "web", "http", "fakehttp 1.0.0", "30m0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("loaded does not contain %q:\n%s", want, out)
		}
	}

	if err := h.loaded([]string{"x"}); !isUsage(err) {
		t.Errorf("loaded with arguments: err = %v", err)
	}
	if err := h.load(nil); !isUsage(err) {
		t.Errorf("load without a name: err = %v", err)
	}
}

func TestLoadReportsPerNameFailures(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	h := newHarness(t, registry.Config{"web": {URL: srv.URL}}, "")
	startHost(t, h, "web")

	err := h.load([]string{"absent", "web"})
	if !errors.Is(err, clierr.Reported) {
		t.Errorf("err = %v, want clierr.Reported", err)
	}
	if !strings.Contains(h.err.String(), "absent: FAILED:") || !strings.Contains(h.out.String(), "web: already loaded") {
		t.Errorf("one failure must not stop the others: out=%q err=%q", h.out, h.err)
	}
}

func TestTools(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	h := newHarness(t, registry.Config{"web": {URL: srv.URL}}, "")
	startHost(t, h, "web")

	if err := h.tools([]string{"web"}); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	for _, want := range []string{"web: 4 tools (* = required parameter; detail: tools web <tool>)", "- echo(text*): Return the given text.", "- sleep(ms*)"} {
		if !strings.Contains(out, want) {
			t.Errorf("tools does not contain %q:\n%s", want, out)
		}
	}
	h.reset()

	if err := h.tools([]string{"web", "echo"}); err != nil {
		t.Fatal(err)
	}
	out = h.out.String()
	for _, want := range []string{"echo\n", "Second line of description.", "inputSchema:", `"text"`} {
		if !strings.Contains(out, want) {
			t.Errorf("details do not contain %q:\n%s", want, out)
		}
	}

	if err := h.tools([]string{"web", "absent"}); err == nil || !strings.Contains(err.Error(), `tool "absent" is not in`) {
		t.Errorf("tool does not exist: err = %v", err)
	}
	for _, args := range [][]string{{}, {"a", "b", "c"}} {
		if err := h.tools(args); !isUsage(err) {
			t.Errorf("tools %v: err = %v, want usage", args, err)
		}
	}
}

func TestToolsNoAutoloadOnUnloaded(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	h := newHarness(t, registry.Config{"web": {URL: srv.URL}}, "")

	for _, args := range [][]string{{"web"}, {"web", "--no-autoload"}} {
		err := h.tools(args)
		if !errors.Is(err, client.ErrNotLoaded) || !strings.Contains(err.Error(), "tmcp load web") {
			t.Errorf("tools %v: err = %v", args, err)
		}
	}
}

func TestCall(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	h := newHarness(t, registry.Config{"web": {URL: srv.URL}}, "")
	startHost(t, h, "web")

	if err := h.call([]string{"web", "echo", `{"text":"hi"}`}); err != nil || h.out.String() != "echo: hi\n" {
		t.Errorf("call: %q err=%v", h.out, err)
	}
	h.reset()

	err := h.call([]string{"web", "fail"})
	if !errors.Is(err, clierr.Reported) || h.out.String() != "this tool always fails\n" || !strings.Contains(h.err.String(), "isError=true") {
		t.Errorf("call fail: out=%q err=%q %v", h.out, h.err, err)
	}
	h.reset()

	if err := h.call([]string{"web", "echo", "--timeout", "30", `{"text":"x"}`}); err != nil || h.out.String() != "echo: x\n" {
		t.Errorf("--timeout: %q err=%v", h.out, err)
	}
}

func TestCallArgsFromStdin(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	h := newHarness(t, registry.Config{"web": {URL: srv.URL}}, `{"text":"from stdin"}`)
	startHost(t, h, "web")
	if err := h.call([]string{"web", "echo", "-"}); err != nil || h.out.String() != "echo: from stdin\n" {
		t.Errorf("call -: %q err=%v", h.out, err)
	}

	big := newHarness(t, registry.Config{"web": {URL: srv.URL}}, strings.Repeat("a", maxStdinArgs+1))
	if err := big.call([]string{"web", "echo", "-"}); !isUsage(err) || !strings.Contains(err.Error(), "too large") {
		t.Errorf("stdin too large: err = %v", err)
	}
}

func TestCallValidatesBeforeTouchingHost(t *testing.T) {
	h := newHarness(t, registry.Config{}, "")
	cases := map[string][]string{
		"no tool":                 {"web"},
		"extra":                   {"web", "echo", "{}", "x"},
		"JSON is not an object":   {"web", "echo", `[1,2]`},
		"broken JSON":             {"web", "echo", `{notjson`},
		"timeout is not a number": {"web", "echo", "--timeout", "abc"},
		"zero timeout":            {"web", "echo", "--timeout", "0"},
		"unknown option":          {"web", "echo", "--bogus"},
	}
	for name, args := range cases {
		if err := h.call(args); !isUsage(err) {
			t.Errorf("%s: err = %v, want clierr.Usage", name, err)
		}
	}
	if h.out.Len() != 0 {
		t.Errorf("there must be no output: %q", h.out)
	}
}

func TestCallTruncatesLargeOutputToFile(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	h := newHarness(t, registry.Config{"web": {URL: srv.URL}}, "")
	h.limits.MaxOutput = 1000
	startHost(t, h, "web")

	if err := h.call([]string{"web", "big", `{"n":5000}`}); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if !strings.Contains(out, "[output truncated: ") || !strings.Contains(out, h.p.OutDir) {
		t.Errorf("out = %.200q", out)
	}
	files, _ := os.ReadDir(h.p.OutDir)
	if len(files) != 1 {
		t.Fatalf("full-result files = %d, want 1", len(files))
	}
	if b, _ := os.ReadFile(filepath.Join(h.p.OutDir, files[0].Name())); len(b) != 5000 {
		t.Errorf("full result is %d bytes, want 5000", len(b))
	}
}

func TestUnload(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	h := newHarness(t, registry.Config{"web": {URL: srv.URL}}, "")
	startHost(t, h, "web")

	if err := h.unload([]string{"web"}); err != nil || h.out.String() != "web: stopped\n" {
		t.Errorf("unload: %q err=%v", h.out, err)
	}
	h.reset()
	if err := h.unload([]string{"web"}); err != nil || h.out.String() != "web: was not loaded\n" {
		t.Errorf("unload again: %q err=%v", h.out, err)
	}
	h.reset()
	if err := h.unload([]string{"--all"}); err != nil || !strings.Contains(h.out.String(), "no MCP servers loaded") {
		t.Errorf("unload --all (empty): %q err=%v", h.out, err)
	}

	for name, args := range map[string][]string{
		"no arguments":    {},
		"names and --all": {"web", "--all"},
		"unknown option":  {"--bogus"},
	} {
		if err := h.unload(args); !isUsage(err) {
			t.Errorf("%s: err = %v, want usage", name, err)
		}
	}
}

func TestUnloadAllStopsEveryHost(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	h := newHarness(t, registry.Config{"a": {URL: srv.URL}, "b": {URL: srv.URL}}, "")
	startHost(t, h, "a")
	startHost(t, h, "b")

	if err := h.unload([]string{"--all"}); err != nil {
		t.Fatal(err)
	}
	if out := h.out.String(); out != "a: stopped\nb: stopped\n" {
		t.Errorf("out = %q", out)
	}
	if list := h.mgr.Loaded(); len(list) != 0 {
		t.Errorf("something is still alive: %+v", list)
	}
}

func TestRunHelpAndDispatch(t *testing.T) {
	var out, errw bytes.Buffer
	if err := Run("mcp-help", nil, &out, &errw); err != nil || out.String() != Usage {
		t.Errorf("mcp-help: err=%v, the help text differs from Usage", err)
	}
	for _, want := range []string{"load <name>...", "call <name> <tool>", "mcp add <name>", "TMCP_AUTOLOAD=0"} {
		if !strings.Contains(Usage, want) {
			t.Errorf("Usage does not contain %q", want)
		}
	}

	dir := t.TempDir()
	t.Setenv(config.EnvDir, dir)
	t.Setenv(config.EnvMCPs, filepath.Join(dir, "mcps.json"))
	if err := Run("bogus", nil, &out, &errw); !isUsage(err) {
		t.Errorf("unknown command: err = %v", err)
	}
}

func TestRunUsesEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvDir, dir)
	t.Setenv(config.EnvMCPs, filepath.Join(dir, "mcps.json"))
	var out, errw bytes.Buffer
	if err := Run("mcp", []string{"add", "x", "--", "echo", "hi"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	cfg, err := registry.Load(filepath.Join(dir, "mcps.json"))
	if err != nil || cfg["x"].Command != "echo" {
		t.Errorf("config = %+v, %v", cfg, err)
	}
}
