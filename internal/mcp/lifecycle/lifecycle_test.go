package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/client"
	"terminal-mcp/internal/mcp/host"
	"terminal-mcp/internal/mcp/proto"
	"terminal-mcp/internal/mcp/registry"
)

var fakeBin string

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "mcphost" {
		os.Exit(runAsHost(os.Args[2]))
	}
	dir, err := os.MkdirTemp("", "fakemcp")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := exec.LookPath("go"); err == nil {
		bin := filepath.Join(dir, "fakemcp")
		if out, err := exec.Command("go", "build", "-o", bin, "../../../cmd/fakemcp").CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "building fakemcp failed: %v\n%s", err, out)
			os.Exit(1)
		}
		fakeBin = bin
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func runAsHost(name string) int {
	p, err := config.PathsFromEnv()
	if err == nil {
		err = host.Serve(name, p, host.Options{OpenLog: openFileLog})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return 0
}

func openFileLog(path string, _ int64) (io.Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}

type testEnv struct {
	m  *Manager
	cl *client.Client
	p  config.Paths
}

func newEnv(t *testing.T, cfg registry.Config) *testEnv {
	t.Helper()
	if fakeBin == "" {
		t.Skip("go is not available to build fakemcp")
	}
	dir, err := os.MkdirTemp("", "tm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv(config.EnvDir, dir)
	t.Setenv(config.EnvMCPs, filepath.Join(dir, "mcps.json"))
	t.Setenv(EnvSpawn, "setsid")
	p, err := config.PathsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Save(p.MCPs, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(p)
	t.Cleanup(func() { _, _ = m.UnloadAll() })
	return &testEnv{m: m, cl: client.New(p), p: p}
}

func fakeCfg(e registry.Entry) registry.Config {
	e.Command = fakeBin
	return registry.Config{"fake": e}
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", what)
}

func procAlive(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i >= 0 && i+2 < len(s) && s[i+2] != 'Z'
}

func callText(t *testing.T, cl *client.Client, tool string, args string) (string, bool) {
	t.Helper()
	res, err := cl.Call("fake", tool, []byte(args), 20*time.Second)
	if err != nil {
		t.Fatalf("call %s: %v", tool, err)
	}
	var b strings.Builder
	for _, p := range res.Parts {
		b.WriteString(p.Text)
	}
	return b.String(), res.IsError
}

func TestLoadCallUnload(t *testing.T) {
	env := newEnv(t, fakeCfg(registry.Entry{}))

	res, err := env.m.Load("fake")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Started || res.Method != "setsid" || res.Warn != "" {
		t.Errorf("first Load = %+v", res)
	}
	if res.Info.Server != "fakemcp" || res.Info.Tools != 8 || res.Info.Kind != "stdio" || res.Info.Name != "fake" {
		t.Errorf("Info = %+v", res.Info)
	}
	hostPID := res.Info.PID

	res, err = env.m.Load("fake")
	if err != nil || res.Started || res.Info.PID != hostPID {
		t.Errorf("second Load = %+v, %v", res, err)
	}
	if list := env.m.Loaded(); len(list) != 1 || list[0].Name != "fake" {
		t.Errorf("Loaded = %+v", list)
	}

	sock, _ := proto.Socket(env.p, "fake")
	for path, want := range map[string]os.FileMode{sock: 0o600, proto.PIDFile(env.p, "fake"): 0o600, env.p.RunDir: 0o700} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: %v %v, want %o", path, fi, err, want)
		}
	}

	if out, bad := callText(t, env.cl, "echo", `{"text":"hi"}`); out != "echo: hi" || bad {
		t.Errorf("echo = %q bad=%v", out, bad)
	}
	if out, bad := callText(t, env.cl, "fail", ``); out != "this tool always fails" || !bad {
		t.Errorf("fail = %q bad=%v", out, bad)
	}
	if _, err := env.cl.Call("fake", "absent", nil, 10*time.Second); err == nil {
		t.Error("an unknown tool must be an error")
	}

	was, err := env.m.Unload("fake")
	if err != nil || !was {
		t.Fatalf("Unload = %v, %v", was, err)
	}
	if _, err := env.cl.Ping("fake"); !errors.Is(err, client.ErrNotLoaded) {
		t.Errorf("after unload: err = %v", err)
	}
	for _, f := range []string{sock, proto.PIDFile(env.p, "fake")} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s was not cleaned up", f)
		}
	}
	waitFor(t, 10*time.Second, "host process died", func() bool { return !procAlive(hostPID) })

	if was, err := env.m.Unload("fake"); err != nil || was {
		t.Errorf("second Unload = %v, %v; want false, nil", was, err)
	}
}

func TestHostLogHasChildStderr(t *testing.T) {
	env := newEnv(t, fakeCfg(registry.Entry{}))
	if _, err := env.m.Load("fake"); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(proto.LogFile(env.p, "fake"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fakemcp: ready", "[mcphost fake]", "ready: fakemcp 1.0.0 (stdio), 8 tools"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log does not contain %q:\n%s", want, log)
		}
	}
	if fi, _ := os.Stat(proto.LogFile(env.p, "fake")); fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode = %o, want 600", fi.Mode().Perm())
	}
}

func TestEnsure(t *testing.T) {
	env := newEnv(t, fakeCfg(registry.Entry{}))

	if _, err := env.m.Ensure("fake", false); !errors.Is(err, client.ErrNotLoaded) || !strings.Contains(err.Error(), "tmcp load fake") {
		t.Errorf("without autoload: err = %v", err)
	}
	res, err := env.m.Ensure("fake", true)
	if err != nil || !res.Started {
		t.Fatalf("autoload = %+v, %v", res, err)
	}
	res, err = env.m.Ensure("fake", false)
	if err != nil || res.Started {
		t.Errorf("Ensure while already running = %+v, %v", res, err)
	}
	if _, err := env.m.Ensure("Invalid", true); err == nil {
		t.Error("an invalid name must be rejected")
	}
}

func TestChildEnvironmentIsolated(t *testing.T) {
	t.Setenv("TERMINAL_MCP_TOKEN", "secret-token")
	env := newEnv(t, fakeCfg(registry.Entry{Env: map[string]string{"MINE": "1"}}))
	if _, err := env.m.Load("fake"); err != nil {
		t.Fatal(err)
	}
	if out, _ := callText(t, env.cl, "env", `{"name":"TERMINAL_MCP_TOKEN"}`); out != "<unset>" {
		t.Errorf("TERMINAL_MCP_TOKEN leaked to the child MCP: %q", out)
	}
	if out, _ := callText(t, env.cl, "env", `{"name":"MINE"}`); out != "1" {
		t.Errorf("env from the config did not arrive: %q", out)
	}
	if out, _ := callText(t, env.cl, "env", `{"name":"PATH"}`); out == "<unset>" || out == "" {
		t.Errorf("PATH was not inherited: %q", out)
	}
}

func TestChildCrashStopsHost(t *testing.T) {
	env := newEnv(t, fakeCfg(registry.Entry{}))
	if _, err := env.m.Load("fake"); err != nil {
		t.Fatal(err)
	}
	if out, _ := callText(t, env.cl, "crash", ``); out != "bye" {
		t.Errorf("crash = %q", out)
	}
	waitFor(t, 15*time.Second, "host stopped after the child MCP died", func() bool {
		_, err := env.cl.Ping("fake")
		return errors.Is(err, client.ErrNotLoaded)
	})
	waitFor(t, 5*time.Second, "log records the reason", func() bool {
		log, _ := os.ReadFile(proto.LogFile(env.p, "fake"))
		return strings.Contains(string(log), "connection to MCP ended")
	})
}

func TestGrandchildKilledOnUnload(t *testing.T) {
	env := newEnv(t, fakeCfg(registry.Entry{}))
	if _, err := env.m.Load("fake"); err != nil {
		t.Fatal(err)
	}
	out, _ := callText(t, env.cl, "spawn", ``)
	pid, err := strconv.Atoi(out)
	if err != nil || !procAlive(pid) {
		t.Fatalf("grandchild process is not running: %q, %v", out, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	if _, err := env.m.Unload("fake"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "grandchild process died too", func() bool { return !procAlive(pid) })
}

func TestLoadErrors(t *testing.T) {
	env := newEnv(t, registry.Config{
		"broken": {Command: "/no/such/binary"},
		"fake":   {Command: fakeBin},
	})

	if _, err := env.m.Load("Invalid"); err == nil || !strings.Contains(err.Error(), "MCP name") {
		t.Errorf("invalid name: err = %v", err)
	}
	if _, err := env.m.Load("absent"); err == nil || !strings.Contains(err.Error(), `"absent" is not in`) {
		t.Errorf("not in the config: err = %v", err)
	}

	_, err := env.m.Load("broken")
	if err == nil || !strings.Contains(err.Error(), `host "broken" stopped before it was ready`) || !strings.Contains(err.Error(), "failed to connect to MCP") {
		t.Errorf("broken command: err = %v", err)
	}

	t.Setenv(EnvSpawn, "bogus")
	if _, err := env.m.Load("fake"); err == nil || !strings.Contains(err.Error(), `TMCP_SPAWN="bogus" is not recognized`) {
		t.Errorf("wrong spawn mode: err = %v", err)
	}
}

func TestLoadRejectsInvalidConfigFile(t *testing.T) {
	env := newEnv(t, registry.Config{})
	if err := os.WriteFile(env.p.MCPs, []byte(`{"fake":{"comand":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Load("fake"); err == nil {
		t.Error("a config with a misspelled field must be rejected")
	}
}

func TestLoadedAndUnloadAll(t *testing.T) {
	env := newEnv(t, registry.Config{"b": {Command: fakeBin}, "a": {Command: fakeBin}})
	if list := env.m.Loaded(); len(list) != 0 {
		t.Fatalf("initial Loaded = %+v", list)
	}
	for _, n := range []string{"b", "a"} {
		if _, err := env.m.Load(n); err != nil {
			t.Fatal(err)
		}
	}
	list := env.m.Loaded()
	if len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" {
		t.Errorf("Loaded must be sorted by name: %+v", list)
	}
	stopped, err := env.m.UnloadAll()
	if err != nil || strings.Join(stopped, ",") != "a,b" {
		t.Errorf("UnloadAll = %v, %v", stopped, err)
	}
	if list := env.m.Loaded(); len(list) != 0 {
		t.Errorf("Loaded after UnloadAll = %+v", list)
	}
}

func TestStaleFilesAreCleaned(t *testing.T) {
	env := newEnv(t, registry.Config{})
	if err := os.MkdirAll(env.p.RunDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock, _ := proto.Socket(env.p, "ghost")
	files := []string{sock, proto.PIDFile(env.p, "ghost"), proto.LockFile(env.p, "ghost")}
	for _, f := range files {
		body := ""
		if strings.HasSuffix(f, ".pid") {
			body = "999999\n"
		}
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if was, err := env.m.Unload("ghost"); err != nil || was {
		t.Errorf("Unload = %v, %v; want false, nil", was, err)
	}
	for _, f := range files[:2] {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s leftovers were not cleaned up", f)
		}
	}
}

func TestUnloadNeverSignalsUnrelatedProcess(t *testing.T) {
	env := newEnv(t, registry.Config{})
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })

	if err := os.MkdirAll(env.p.RunDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proto.PIDFile(env.p, "x"), []byte(strconv.Itoa(other.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if was, err := env.m.Unload("x"); err != nil || was {
		t.Errorf("Unload = %v, %v; want false, nil", was, err)
	}
	if !procAlive(other.Process.Pid) {
		t.Error("Unload killed a process that is not a host")
	}
}

func TestUnitName(t *testing.T) {
	if got := UnitName("github"); got != "tmcp-mcp-github" {
		t.Errorf("UnitName = %q", got)
	}
}
