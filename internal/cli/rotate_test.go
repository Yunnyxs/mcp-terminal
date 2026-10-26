package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"terminal-mcp/internal/auth"
	"terminal-mcp/internal/config"
)

func TestNewToken(t *testing.T) {
	a, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newToken()
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a) {
		t.Errorf("format token = %q, want 64 hex", a)
	}
	if a == b {
		t.Error("two consecutive tokens are equal")
	}
	if err := auth.ValidateToken(a); err != nil {
		t.Errorf("new token fails auth.ValidateToken: %v", err)
	}
}

func TestReplaceToken(t *testing.T) {
	const tok = "NEW"
	cases := []struct{ name, in, want string }{
		{"replaces, other lines intact",
			"TERMINAL_MCP_HOST=127.0.0.1\nTERMINAL_MCP_TOKEN=old\nTERMINAL_MCP_PORT=8000\n",
			"TERMINAL_MCP_HOST=127.0.0.1\nTERMINAL_MCP_TOKEN=NEW\nTERMINAL_MCP_PORT=8000\n"},
		{"comments and blank lines kept", "# note\n\nTERMINAL_MCP_TOKEN=old\n", "# note\n\nTERMINAL_MCP_TOKEN=NEW\n"},
		{"line with leading spaces", "  TERMINAL_MCP_TOKEN=old\n", "TERMINAL_MCP_TOKEN=NEW\n"},
		{"absent: appended at the end", "A=1\n", "A=1\nTERMINAL_MCP_TOKEN=NEW\n"},
		{"absent, no trailing newline", "A=1", "A=1\nTERMINAL_MCP_TOKEN=NEW\n"},
		{"empty file", "", "\nTERMINAL_MCP_TOKEN=NEW\n"},
		{"similar name not replaced", "TERMINAL_MCP_TOKEN_OTHER=x\nTERMINAL_MCP_TOKEN=old\n", "TERMINAL_MCP_TOKEN_OTHER=x\nTERMINAL_MCP_TOKEN=NEW\n"},
	}
	for _, c := range cases {
		got := replaceToken(c.in, tok)

		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestRewriteTokenAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.env")
	original := "TERMINAL_MCP_HOST=127.0.0.1\nTERMINAL_MCP_TOKEN=" + strings.Repeat("a", 64) + "\nTERMINAL_MCP_PORT=8123\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	token, err := rewriteToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.ValidateToken(token); err != nil || token == strings.Repeat("a", 64) {
		t.Fatalf("token = %q, %v", token, err)
	}
	got, _ := os.ReadFile(path)
	want := "TERMINAL_MCP_HOST=127.0.0.1\nTERMINAL_MCP_TOKEN=" + token + "\nTERMINAL_MCP_PORT=8123\n"
	if string(got) != want {
		t.Errorf("content:\n%s\nwant:\n%s", got, want)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 600 (contains the token)", fi.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temp file left over: %d entries", len(entries))
	}

	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".config", "terminal-mcp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(dir, ".config", "terminal-mcp", "config.env")); err != nil {
		t.Fatal(err)
	}
	s, err := config.FromEnvFile()
	if err != nil || s.Token != token || s.Port != 8123 {
		t.Errorf("FromEnvFile = %+v, %v", s, err)
	}
}

func TestRewriteTokenMissingFile(t *testing.T) {
	if _, err := rewriteToken(filepath.Join(t.TempDir(), "missing.env")); err == nil {
		t.Error("missing file must error, not create a new config")
	}
}
