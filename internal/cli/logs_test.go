package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogsRejectsBadInputBeforeRunningAnything(t *testing.T) {
	isolate(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown source", []string{"logs", "bogus"}, `unknown log source "bogus" (choose: server, tunnel, activity)`},
		{"leftover argument", []string{"logs", "server", "extra"}, "unknown argument: extra"},
		{"-n zero", []string{"logs", "-n", "0"}, "-n must be >= 1"},
		{"-n negative", []string{"logs", "server", "-n", "-5"}, "-n must be >= 1"},
	}
	for _, c := range cases {
		code, _, errOut := run(t, c.args...)
		if code != 1 || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: code=%d err=%q, want containing %q", c.name, code, errOut, c.want)
		}
	}

	code, _, errOut := run(t, "logs", "--bogus")
	if code != 1 || !strings.Contains(errOut, "flag provided but not defined") || strings.Contains(errOut, "Error:") {
		t.Errorf("bad flag: code=%d err=%q", code, errOut)
	}
}

func TestLogCommand(t *testing.T) {
	argv, err := logCommand("server", 20, false)
	if err != nil || strings.Join(argv, " ") != "journalctl --user -u terminal-mcp.service -n 20 --no-pager" {
		t.Errorf("server: %v %v", argv, err)
	}
	argv, _ = logCommand("tunnel", 5, true)
	if strings.Join(argv, " ") != "journalctl --user -u terminal-mcp-tunnel.service -n 5 --no-pager -f" {
		t.Errorf("tunnel -f: %v", argv)
	}
}

func TestLogsActivityReadsConfiguredFile(t *testing.T) {
	home := isolate(t)
	actLog := filepath.Join(home, "activity.log")
	writeEnv(t, home, "TERMINAL_MCP_ACTIVITY_LOG="+actLog+"\n")

	code, _, errOut := run(t, "logs", "activity")
	if code != 1 || !strings.Contains(errOut, "no activity log yet: "+actLog) {
		t.Errorf("without file: code=%d err=%q", code, errOut)
	}

	if err := os.WriteFile(actLog, []byte("one\ntwo\nthree\nfour\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run(t, "logs", "activity", "-n", "2")
	if code != 0 || out != "three\nfour\n" || errOut != "" {
		t.Errorf("code=%d out=%q err=%q", code, out, errOut)
	}

	argv, _ := logCommand("activity", 3, true)
	if strings.Join(argv, " ") != "tail -n 3 -f "+actLog {
		t.Errorf("argv = %v", argv)
	}
}
