package tools

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/result"
)

func testSettings(t *testing.T) config.Settings {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "root")
	data := filepath.Join(home, "data")
	for _, d := range []string{root, data} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return config.Settings{
		Root:        root,
		Shell:       "/bin/sh",
		Timeout:     10 * time.Second,
		MaxOutput:   30000,
		TrashDir:    filepath.Join(data, "Trash"),
		NotesDir:    filepath.Join(data, "notes"),
		ActivityLog: filepath.Join(data, "activity.log"),
		ServerLog:   filepath.Join(data, "server.log"),
	}
}

func callTool[In sudoInput, P any](t *testing.T, ft fileTool[In, P], s config.Settings, in In) (*mcp.CallToolResult, error) {
	t.Helper()
	p, err := ft.prepare(s, in)
	if err != nil {
		return nil, err
	}
	res, err := ft.exec(s, p)
	return res, withHint(err)
}

func textOf(res *mcp.CallToolResult) string { return result.String(res) }

func mustWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
