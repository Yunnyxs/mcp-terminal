package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "github", "my-mcp_2", "0abc"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-a", "_a", "A", "a b", "a/b", "../x", "a.b", strings.Repeat("a", 33)} {
		if err := ValidName(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestEntryValidate(t *testing.T) {
	cases := []struct {
		name string
		e    Entry
		ok   bool
	}{
		{"stdio", Entry{Command: "npx", Args: []string{"-y", "x"}}, true},
		{"http", Entry{URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "Bearer x"}}, true},
		{"empty", Entry{}, false},
		{"both", Entry{Command: "a", URL: "http://x"}, false},
		{"url not http", Entry{URL: "ftp://x"}, false},
		{"url without host", Entry{URL: "http://"}, false},
		{"args for url", Entry{URL: "http://x", Args: []string{"a"}}, false},
		{"env for url", Entry{URL: "http://x", Env: map[string]string{"A": "b"}}, false},
		{"header for stdio", Entry{Command: "a", Headers: map[string]string{"X": "y"}}, false},
		{"relative cwd", Entry{Command: "a", Cwd: "rel"}, false},
		{"absolute cwd", Entry{Command: "a", Cwd: "/tmp"}, true},
		{"env key contains =", Entry{Command: "a", Env: map[string]string{"A=B": "c"}}, false},
		{"header key contains :", Entry{URL: "http://x", Headers: map[string]string{"A:B": "c"}}, false},
		{"invalid idle", Entry{Command: "a", IdleTimeout: "long"}, false},
		{"idle too short", Entry{Command: "a", IdleTimeout: "10ms"}, false},
	}
	for _, c := range cases {
		if err := c.e.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err=%v, ok expected=%v", c.name, err, c.ok)
		}
	}
}

func TestKind(t *testing.T) {
	if (Entry{Command: "x"}).Kind() != "stdio" || (Entry{URL: "http://x"}).Kind() != "http" {
		t.Error("wrong Kind")
	}
}

func TestIdleDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"": DefaultIdle, "0": 0, "off": 0, "NEVER": 0, "90s": 90 * time.Second, "2h": 2 * time.Hour} {
		got, err := Entry{IdleTimeout: in}.IdleDuration()
		if err != nil || got != want {
			t.Errorf("IdleDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestNamesSorted(t *testing.T) {
	got := Config{"b": {}, "a": {}, "c": {}}.Names()
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("Names = %v", got)
	}
}

func TestSaveLoadRoundTripAndPerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "mcps.json")
	cfg := Config{"a": {Command: "x", Env: map[string]string{"K": "secret"}}}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("file permissions = %o, want 600", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Errorf("directory permissions = %o, want 700", fi.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil || got["a"].Env["K"] != "secret" {
		t.Fatalf("load: %v %+v", err, got)
	}

	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("directory has %d entries, want 1", len(entries))
	}
	_ = os.Chmod(path, 0o640)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "too open") {
		t.Errorf("permissions 640 must be rejected, got: %v", err)
	}
}

func TestLoadStrict(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "c.json")
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if c, err := Load(filepath.Join(dir, "missing.json")); err != nil || len(c) != 0 {
		t.Errorf("missing file = empty config, got %v %v", c, err)
	}
	if c, err := Load(write("  \n")); err != nil || len(c) != 0 {
		t.Errorf("blank file = empty config, got %v %v", c, err)
	}
	for name, body := range map[string]string{
		"misspelled field": `{"a":{"comand":"x"}}`,
		"invalid name":     `{"A/b":{"command":"x"}}`,
		"empty entry":      `{"a":{}}`,
		"broken JSON":      `{not json`,
		"invalid entry":    `{"a":{"command":"x","url":"http://y"}}`,
		"trailing data":    `{"a":{"command":"x"}} 1`,
	} {
		if _, err := Load(write(body)); err == nil {
			t.Errorf("%s: must fail", name)
		}
	}
}

func TestChildEnvDoesNotLeak(t *testing.T) {
	t.Setenv("TERMINAL_MCP_TOKEN", "secrettoken")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "x")
	t.Setenv("PATH", "/usr/bin")
	e := Entry{Command: "x", Env: map[string]string{"PATH": "/custom", "MINE": "1"}}
	env := strings.Join(e.ChildEnv(), "\n")
	for _, leaked := range []string{"secrettoken", "AWS_SECRET", "TERMINAL_MCP_TOKEN"} {
		if strings.Contains(env, leaked) {
			t.Errorf("child env contains %q:\n%s", leaked, env)
		}
	}
	if !strings.Contains(env, "PATH=/custom") || strings.Contains(env, "PATH=/usr/bin") || !strings.Contains(env, "MINE=1") {
		t.Errorf("explicit env must win over inherited env:\n%s", env)
	}
}

func TestPassEnvReturnsCopy(t *testing.T) {
	a := PassEnv()
	if len(a) == 0 || a[0] != "PATH" {
		t.Fatalf("PassEnv = %v", a)
	}
	a[0] = "CHANGED"
	if PassEnv()[0] != "PATH" {
		t.Error("modifying the PassEnv result affects the original list")
	}
}
