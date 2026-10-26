package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(kv map[string]string) Lookup { return func(k string) string { return kv[k] } }

func TestLoadDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s, err := Load(env(map[string]string{"SHELL": "/bin/zsh"}))
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, ".mcp-terminal")
	want := map[string][2]string{
		"Host":        {s.Host, "127.0.0.1"},
		"Shell":       {s.Shell, "/bin/zsh"},
		"Root":        {s.Root, home},
		"ActivityLog": {s.ActivityLog, filepath.Join(data, "activity_terminal_mcp.log")},
		"ServerLog":   {s.ServerLog, filepath.Join(data, "terminal_mcp.log")},
		"TrashDir":    {s.TrashDir, filepath.Join(data, "Trash")},
		"NotesDir":    {s.NotesDir, filepath.Join(data, "notes")},
		"Paths.Dir":   {s.Paths.Dir, data},
		"Paths.MCPs":  {s.Paths.MCPs, filepath.Join(home, ".config", "terminal-mcp", "mcps.json")},
	}
	for name, p := range want {
		if p[0] != p[1] {
			t.Errorf("%s = %q, want %q", name, p[0], p[1])
		}
	}
	if s.Port != 8000 || s.Timeout != 120*time.Second || s.MaxOutput != 30000 || s.LogMaxBytes != 400<<20 {
		t.Errorf("wrong default numbers: %+v", s)
	}
}

func TestLoadShellFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Shell != "/bin/bash" {
		t.Errorf("Shell = %q, want /bin/bash", s.Shell)
	}
}

func TestLoadOverrides(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(t.TempDir(), "a.log")
	s, err := Load(env(map[string]string{
		"TMCP_DIR": t.TempDir(), "TMCP_MCPS": filepath.Join(root, "m.json"),
		"TERMINAL_MCP_ROOT": root, "TERMINAL_MCP_PORT": "9001", "TERMINAL_MCP_TIMEOUT": "5",
		"TERMINAL_MCP_MAX_OUTPUT": "2000", "TERMINAL_MCP_LOG_MAX_MB": "7",
		"TERMINAL_MCP_ACTIVITY_LOG": log, "TERMINAL_MCP_TOKEN": "tok", "TERMINAL_MCP_HOST": "0.0.0.0",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Root != root || s.Port != 9001 || s.Timeout != 5*time.Second || s.MaxOutput != 2000 ||
		s.LogMaxBytes != 7<<20 || s.ActivityLog != log || s.Token != "tok" || s.Host != "0.0.0.0" {
		t.Errorf("override not read: %+v", s)
	}

	if !strings.HasPrefix(s.ServerLog, s.Paths.Dir) {
		t.Errorf("ServerLog %q not under %q", s.ServerLog, s.Paths.Dir)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	root := t.TempDir()
	notDir := filepath.Join(root, "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		kv   map[string]string
		want string
	}{
		{"port not an integer", map[string]string{"TERMINAL_MCP_PORT": "abc"}, "TERMINAL_MCP_PORT must be an integer"},
		{"port out of range", map[string]string{"TERMINAL_MCP_PORT": "70000"}, "TERMINAL_MCP_PORT must be 1-65535"},
		{"timeout zero", map[string]string{"TERMINAL_MCP_TIMEOUT": "0"}, "TERMINAL_MCP_TIMEOUT must be 1-"},
		{"max output too small", map[string]string{"TERMINAL_MCP_MAX_OUTPUT": "999"}, "TERMINAL_MCP_MAX_OUTPUT must be 1000-10000000"},
		{"log mb zero", map[string]string{"TERMINAL_MCP_LOG_MAX_MB": "0"}, "TERMINAL_MCP_LOG_MAX_MB must be 1-"},
		{"root not a directory", map[string]string{"TERMINAL_MCP_ROOT": notDir}, "TERMINAL_MCP_ROOT is not a directory"},
		{"root missing", map[string]string{"TERMINAL_MCP_ROOT": filepath.Join(root, "nope")}, "TERMINAL_MCP_ROOT is not a directory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", root)
			_, err := Load(env(c.kv))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want containing %q", err, c.want)
			}
		})
	}
}

func TestLimitsFrom(t *testing.T) {
	l, err := LimitsFrom(env(nil))
	if err != nil || l.Timeout != 120*time.Second || l.MaxOutput != 30000 {
		t.Errorf("wrong default: %+v err=%v", l, err)
	}
	l, err = LimitsFrom(env(map[string]string{"TERMINAL_MCP_TIMEOUT": "3", "TERMINAL_MCP_MAX_OUTPUT": "5000"}))
	if err != nil || l.Timeout != 3*time.Second || l.MaxOutput != 5000 {
		t.Errorf("wrong override: %+v err=%v", l, err)
	}
	if _, err = LimitsFrom(env(map[string]string{"TERMINAL_MCP_TIMEOUT": "x"})); err == nil {
		t.Error("invalid value must fail")
	}
}

func TestPathsFrom(t *testing.T) {
	dir := t.TempDir()
	p, err := PathsFrom(env(map[string]string{"TMCP_DIR": dir, "TMCP_MCPS": filepath.Join(dir, "m.json")}))
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Dir: dir, RunDir: filepath.Join(dir, "run"), LogDir: filepath.Join(dir, "mcp"),
		OutDir: filepath.Join(dir, "mcp", "out"), MCPs: filepath.Join(dir, "m.json"),
	}
	if p != want {
		t.Errorf("Paths = %+v, want %+v", p, want)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	p, err = PathsFrom(env(map[string]string{"TMCP_DIR": dir}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != dir || p.MCPs != filepath.Join(home, ".config", "terminal-mcp", "mcps.json") {
		t.Errorf("mixed default/wrong override: %+v", p)
	}
}

func TestParseEnv(t *testing.T) {
	m := ParseEnv("# comment\n; also a comment\n\nA=1\nB = \"two words\"\nC='x'\nD=\nline-without-equals\nE=a=b\n")
	want := map[string]string{"A": "1", "B": "two words", "C": "x", "D": "", "E": "a=b"}
	if len(m) != len(want) {
		t.Fatalf("m = %v, want %v", m, want)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q, want %q", k, m[k], v)
		}
	}
}

func TestFromEnvFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMCP_DIR", "")
	t.Setenv("TMCP_MCPS", "")

	if _, err := FromEnvFile(); err == nil || !strings.Contains(err.Error(), "config not found") {
		t.Fatalf("without config.env: err = %v", err)
	}

	cfgDir := filepath.Join(home, ".config", "terminal-mcp")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "TERMINAL_MCP_TOKEN=from-file\nTERMINAL_MCP_PORT=8123\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TERMINAL_MCP_PORT", "9999")
	t.Setenv("TERMINAL_MCP_TIMEOUT", "5")
	tokenBefore := os.Getenv("TERMINAL_MCP_TOKEN")

	s, err := FromEnvFile()
	if err != nil {
		t.Fatal(err)
	}
	if s.Token != "from-file" || s.Port != 8123 {
		t.Errorf("file values not read: %+v", s)
	}
	if s.Timeout != 120*time.Second {
		t.Errorf("TERMINAL_MCP_TIMEOUT from shell leaked: %v", s.Timeout)
	}

	if os.Getenv("TERMINAL_MCP_PORT") != "9999" || os.Getenv("TERMINAL_MCP_TOKEN") != tokenBefore {
		t.Error("FromEnvFile modified the process environment")
	}
}
