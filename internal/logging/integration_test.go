package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/logging"
	"terminal-mcp/internal/server"
)

func newServer(t *testing.T, buf *bytes.Buffer, llmBudget int) *mcp.ClientSession {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, "data")
	s := config.Settings{
		Root: root, Shell: "/bin/sh", Timeout: 20 * time.Second, MaxOutput: 30000,
		Compact: true, LLMOutput: llmBudget,
		TrashDir: filepath.Join(data, "Trash"), NotesDir: filepath.Join(data, "notes"),
		Paths: config.Paths{OutDir: filepath.Join(data, "mcp", "out")},
	}
	srv := server.New(s, server.Options{Middleware: []mcp.Middleware{logging.Activity(buf, nil)}})

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func run(t *testing.T, cs *mcp.ClientSession, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "run", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("content = %d", len(res.Content))
	}
	return res.Content[0].(*mcp.TextContent).Text
}

func loggedResult(t *testing.T, buf *bytes.Buffer) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("log entries = %d, want 1: %q", len(lines), buf.String())
	}
	var e struct {
		Tool   string `json:"tool"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("log is not JSON: %v", err)
	}
	if e.Tool != "run" {
		t.Fatalf("tool = %q", e.Tool)
	}
	return e.Result
}

func TestLogKeepsRawOutputWhileClientGetsSummary(t *testing.T) {
	var buf bytes.Buffer
	cs := newServer(t, &buf, 600)

	got := run(t, cs, map[string]any{"command": "seq 1 3000"})
	logged := loggedResult(t, &buf)

	if !strings.HasPrefix(logged, "exit 0 (") {
		t.Errorf("log must contain the raw header, got start: %.40q", logged)
	}
	for _, want := range []string{"\n1\n", "\n1500\n", "\n3000"} {
		if !strings.Contains(logged, want) {
			t.Errorf("log does not contain %q: raw output must be intact", want)
		}
	}

	if strings.HasPrefix(got, "exit 0") {
		t.Errorf("client must not receive the raw header: %.40q", got)
	}
	if !strings.Contains(got, "[summarized; stored output:") {
		t.Errorf("client must be told the content was summarized: %q", got)
	}
	if len(got) >= len(logged)/2 {
		t.Errorf("client result (%d bytes) is not more concise than the log (%d bytes)", len(got), len(logged))
	}
}

func TestLogAndClientAgreeOnShortOutput(t *testing.T) {
	var buf bytes.Buffer
	cs := newServer(t, &buf, 8000)

	got := run(t, cs, map[string]any{"command": "echo hello"})
	if got != "hello" {
		t.Errorf("client = %q, want %q (exit 0 without header)", got, "hello")
	}
	logged := loggedResult(t, &buf)
	if !strings.HasPrefix(logged, "exit 0 (") || !strings.HasSuffix(logged, "\nhello\n") {
		t.Errorf("log = %q: must be raw header + output", logged)
	}
}

func TestLogRecordsFailureAndRawFlag(t *testing.T) {
	var buf bytes.Buffer
	cs := newServer(t, &buf, 8000)

	got := run(t, cs, map[string]any{"command": "echo failed >&2; exit 3"})
	if !strings.HasPrefix(got, "exit 3\n") || !strings.Contains(got, "failed") {
		t.Errorf("client = %q", got)
	}
	if logged := loggedResult(t, &buf); !strings.HasPrefix(logged, "exit 3 (") {
		t.Errorf("log = %q", logged)
	}

	buf.Reset()
	raw := run(t, cs, map[string]any{"command": "echo raw", "raw": true})
	if logged := loggedResult(t, &buf); raw != logged {
		t.Errorf("raw=true: client %q != log %q", raw, logged)
	}
}

func TestLogRecordsFileToolsToo(t *testing.T) {
	var buf bytes.Buffer
	cs := newServer(t, &buf, 8000)

	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "write", Arguments: map[string]any{"path": "notes.txt", "content": "secret content <&>"},
	}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), `"tool":"write"`) || !strings.Contains(buf.String(), "secret content <&>") {
		t.Errorf("write entry incomplete: %s", buf.String())
	}
}
