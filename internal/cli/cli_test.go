package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"terminal-mcp/internal/clierr"
	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/command"
)

func run(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = Run(args, &o, &e)
	return code, o.String(), e.String()
}

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(config.EnvDir, filepath.Join(home, "data"))
	t.Setenv(config.EnvMCPs, filepath.Join(home, "mcps.json"))
	return home
}

func TestRunNoArgsAndHelpPrintUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		code, out, errOut := run(t, args...)
		if code != 0 || out != usage || errOut != "" {
			t.Errorf("%v: code=%d, usage equal=%v, stderr=%q", args, code, out == usage, errOut)
		}
	}
	for _, want := range []string{"serve [--stdio]", "status", "restart [server|tunnel|all]", "rotate", "tmcp mcp-help"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage does not contain %q", want)
		}
	}
}

func TestRunVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version"} {
		code, out, _ := run(t, arg)
		if code != 0 || out != "terminal-mcp "+config.Version+"\n" {
			t.Errorf("%s: code=%d out=%q", arg, code, out)
		}
	}
}

func TestRunUnknownCommandIsExit2WithUsage(t *testing.T) {
	code, out, errOut := run(t, "bogus")
	if code != 2 || out != "" || !strings.HasPrefix(errOut, `unknown command: "bogus"`) || !strings.Contains(errOut, usage) {
		t.Errorf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestNoArgsCommandsRejectExtraArguments(t *testing.T) {

	for _, name := range []string{"status", "url", "start", "stop", "rotate"} {
		code, _, errOut := run(t, name, "extra", "args")
		if code != 1 || errOut != "Error: "+name+" takes no arguments: extra args\n" {
			t.Errorf("%s: code=%d err=%q", name, code, errOut)
		}
	}
}

func TestRestartValidatesTargetBeforeTouchingSystemd(t *testing.T) {
	isolate(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"restart", "a", "b"}, "usage: terminal-mcp restart [server|tunnel|all]"},
		{[]string{"restart", "bogus"}, `unknown target "bogus" (choose: server, tunnel, all)`},
	}
	for _, c := range cases {
		code, _, errOut := run(t, c.args...)
		if code != 2 || errOut != "Error: "+c.want+"\n" {
			t.Errorf("%v: code=%d err=%q", c.args, code, errOut)
		}
	}
	for target, units := range restartTargets {
		if len(units) == 0 {
			t.Errorf("target %s has no units", target)
		}
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		code     int
		wantText string
	}{
		{"success", nil, 0, ""},
		{"usage", clierr.Usage("misuse"), 2, "Error: misuse\n"},
		{"wrapped usage", errors.Join(errors.New("x"), clierr.Usage("inner")), 2, "Error: inner\n"},
		{"reported is not reprinted", clierr.Reported, 1, ""},
		{"plain error", errors.New("failed"), 1, "Error: failed\n"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		if code := exitCode(c.err, &buf); code != c.code || buf.String() != c.wantText {
			t.Errorf("%s: code=%d text=%q, want %d %q", c.name, code, buf.String(), c.code, c.wantText)
		}
	}
}

func TestRunDelegatesMCPCommands(t *testing.T) {
	isolate(t)

	if code, out, _ := run(t, "mcp-help"); code != 0 || out != command.Usage {
		t.Errorf("mcp-help: code=%d", code)
	}
	if code, out, _ := run(t, "mcp", "ls"); code != 0 || !strings.Contains(out, "(empty; add with: mcp add") {
		t.Errorf("mcp ls: code=%d out=%q", code, out)
	}
	if code, out, _ := run(t, "mcp", "add", "x", "--", "echo", "hi"); code != 0 || !strings.Contains(out, "x: added to") {
		t.Errorf("mcp add: code=%d out=%q", code, out)
	}
	if code, _, errOut := run(t, "mcp", "add"); code != 2 || !strings.HasPrefix(errOut, "Error: usage: mcp add") {
		t.Errorf("misuse must exit 2: code=%d err=%q", code, errOut)
	}
	if code, _, errOut := run(t, "call", "x"); code != 2 || !strings.Contains(errOut, "usage: call") {
		t.Errorf("call misuse: code=%d err=%q", code, errOut)
	}

	code, _, errOut := run(t, "load", "missing")
	if code != 1 || !strings.Contains(errOut, "missing: FAILED:") || strings.Contains(errOut, "Error:") {
		t.Errorf("load failed: code=%d err=%q", code, errOut)
	}
}

func TestSettingsMissingConfigIsActionable(t *testing.T) {
	isolate(t)
	code, _, errOut := run(t, "logs", "activity")
	if code != 1 || !strings.Contains(errOut, "config not found") || !strings.Contains(errOut, "run install.sh") {
		t.Errorf("code=%d err=%q", code, errOut)
	}
}

func writeEnv(t *testing.T, home, body string) string {
	t.Helper()
	dir := filepath.Join(home, ".config", "terminal-mcp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
