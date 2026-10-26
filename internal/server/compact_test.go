package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/rawstore"
	"terminal-mcp/internal/result"
)

func compactSettings(t *testing.T) config.Settings {
	t.Helper()
	s := testSettings(t)
	s.Compact = true
	s.LLMOutput = 8000
	s.Paths.OutDir = filepath.Join(filepath.Dir(s.NotesDir), "out")
	return s
}

func callRun(t *testing.T, cs *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "run", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCompactionOffKeepsHandlerOutput(t *testing.T) {
	cs := connect(t, New(testSettings(t), Options{}))
	got := result.String(callRun(t, cs, map[string]any{"command": "echo hi"}))
	if !strings.HasPrefix(got, "exit 0 (") || !strings.HasSuffix(got, "\nhi\n") && !strings.HasSuffix(got, "\nhi") {
		t.Errorf("got %q", got)
	}
}

func TestCompactionShortResults(t *testing.T) {
	cs := connect(t, New(compactSettings(t), Options{}))
	for _, c := range []struct{ command, want string }{
		{"echo hi", "hi"},
		{"true", "ok"},
		{"echo boom; exit 3", "exit 3\nboom"},
		{"exit 4", "exit 4"},
	} {
		got := strings.TrimRight(result.String(callRun(t, cs, map[string]any{"command": c.command})), "\n")
		if got != c.want {
			t.Errorf("%q: got %q, want %q", c.command, got, c.want)
		}
	}
}

func TestCompactionLongOutputSavesRaw(t *testing.T) {
	s := compactSettings(t)
	s.LLMOutput = 500
	cs := connect(t, New(s, Options{}))

	got := result.String(callRun(t, cs, map[string]any{"command": "seq 1 5000"}))
	const prefix = "[summarized; stored output: "
	i := strings.Index(got, prefix)
	if i < 0 || !strings.HasSuffix(got, "]") {
		t.Fatalf("raw marker is missing:\n%s", got)
	}
	if len(got) > 400 {
		t.Errorf("summarized result is %d bytes, too large:\n%s", len(got), got)
	}
	path := strings.TrimSuffix(got[i+len(prefix):], "]")
	if filepath.Dir(path) != s.Paths.OutDir {
		t.Errorf("raw stored in %s, want in %s", path, s.Paths.OutDir)
	}
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(full), "exit 0 (") || !strings.Contains(string(full), "\n2500\n") || !strings.HasSuffix(strings.TrimSpace(string(full)), "5000") {
		t.Errorf("raw file is incomplete (%d bytes)", len(full))
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("raw file mode = %v", fi.Mode().Perm())
	}
}

func TestCompactionWithoutStoreStillSays(t *testing.T) {
	s := compactSettings(t)
	s.LLMOutput = 500
	s.Paths.OutDir = ""
	cs := connect(t, New(s, Options{}))
	got := result.String(callRun(t, cs, map[string]any{"command": "seq 1 5000"}))
	if !strings.Contains(got, "[summarized; rerun with raw=true for unsummarized output]") {
		t.Errorf("got %q", got)
	}
}

func TestCompactionRawBypass(t *testing.T) {
	s := compactSettings(t)
	s.LLMOutput = 500
	cs := connect(t, New(s, Options{}))
	got := result.String(callRun(t, cs, map[string]any{"command": "seq 1 5000", "raw": true}))
	if !strings.HasPrefix(got, "exit 0 (") || !strings.Contains(got, "\n2500\n") || strings.Contains(got, "summarized") {
		t.Errorf("raw=true must return the handler result as is, got %d bytes: %.80q", len(got), got)
	}
	if entries, _ := os.ReadDir(s.Paths.OutDir); len(entries) != 0 {
		t.Errorf("raw=true must not store a file, found %d", len(entries))
	}
}

func TestCompactionIsOutermost(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	inner := func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if cr, ok := res.(*mcp.CallToolResult); ok && method == "tools/call" {
				mu.Lock()
				seen = append(seen, result.String(cr))
				mu.Unlock()
			}
			return res, err
		}
	}
	cs := connect(t, New(compactSettings(t), Options{Middleware: []mcp.Middleware{inner}}))
	client := result.String(callRun(t, cs, map[string]any{"command": "echo hi"}))

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "exit 0 (") {
		t.Errorf("the inner middleware must see the raw result, seen = %q", seen)
	}
	if strings.TrimSpace(client) != "hi" {
		t.Errorf("the client must receive the short version, got %q", client)
	}
}

func TestCompactionTimeoutKeepsMessageAndErrorFlag(t *testing.T) {
	cs := connect(t, New(compactSettings(t), Options{}))
	res := callRun(t, cs, map[string]any{"command": "sleep 5", "timeout": 1})
	if got := strings.TrimSpace(result.String(res)); got != "timeout after 1s, killed" {
		t.Errorf("got %q", got)
	}
	if !res.IsError {
		t.Error("IsError must stay true for a timeout")
	}
}

func TestCompactionLeavesToolErrorsAndOtherToolsAlone(t *testing.T) {
	s := compactSettings(t)
	cs := connect(t, New(s, Options{}))

	res := callRun(t, cs, map[string]any{"command": "  "})
	if got := result.String(res); !res.IsError || !strings.Contains(got, "command is empty") || strings.Contains(got, "summarized") {
		t.Errorf("tool errors must be left alone: IsError=%v %q", res.IsError, got)
	}

	if err := os.WriteFile(filepath.Join(s.Root, "a.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rd, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "read", Arguments: map[string]any{"path": "a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.String(rd); !strings.Contains(got, "hello world") {
		t.Errorf("read: %q", got)
	}
}

func TestParseHeader(t *testing.T) {
	cases := []struct {
		in         string
		status     string
		failed, ok bool
	}{
		{"exit 0 (12ms)", "", false, true},
		{"exit 0 (7.5s)", "(8s)", false, true},
		{"exit 2 (7.5s)", "exit 2 (8s)", true, true},
		{"exit 1 (10ms)", "exit 1", true, true},
		{"exit signal: killed (1.2s)", "exit signal: killed", true, true},
		{"timeout after 2m0s, killed", "timeout after 2m0s, killed", true, true},
		{"command is empty", "", false, false},
		{"exit x (bad)", "", false, false},
		{"exit 3", "", false, false},
	}
	for _, c := range cases {
		status, failed, ok := parseHeader(c.in)
		if status != c.status || failed != c.failed || ok != c.ok {
			t.Errorf("parseHeader(%q) = (%q, %v, %v), want (%q, %v, %v)", c.in, status, failed, ok, c.status, c.failed, c.ok)
		}
	}
}

func TestParseRunArgs(t *testing.T) {
	a := parseRunArgs([]byte(`{"command":"go test ./...","raw":true,"cwd":"/x"}`))
	if a.Command != "go test ./..." || !a.Raw {
		t.Errorf("got %+v", a)
	}
	if a := parseRunArgs([]byte(`not json`)); a.Command != "" || a.Raw {
		t.Errorf("broken arguments must be treated as empty: %+v", a)
	}
}

func TestCompactRunUsesCommandFilter(t *testing.T) {
	dir := t.TempDir()
	store := rawstore.New(dir)
	var sb strings.Builder
	sb.WriteString("exit 0 (30ms)\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&sb, "=== RUN   TestCase%02d\n--- PASS: TestCase%02d (0.00s)\n", i, i)
	}
	sb.WriteString("PASS\nok  \texample.com/a\t0.1s")
	full := sb.String()
	mk := func() *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: full}}}
	}

	got := result.String(compactRun(mk(), "go test -v ./...", 8000, store))
	if !strings.HasPrefix(got, "ok  \texample.com/a\t0.1s\n[20 tests passed]\n[summarized; stored output: "+dir) {
		t.Errorf("the go test filter was not used:\n%s", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("a filtered result must store the full file, found %d files", len(entries))
	}

	got = result.String(compactRun(mk(), "echo whatever", 8000, store))
	if !strings.Contains(got, "=== RUN   TestCase00") || strings.Contains(got, "summarized") {
		t.Errorf("without a filter the content must stay intact:\n%s", got)
	}
}

func TestRawNoteSaysWhenCaptureWasCut(t *testing.T) {
	store := rawstore.New(t.TempDir())
	if got := rawNote(store, "exit 0 (1ms)\nabc\n...[500 bytes truncated]...\nxyz"); !strings.Contains(got, "already cut at the capture limit") {
		t.Errorf("a truncated capture must be reported: %s", got)
	}
	if got := rawNote(store, "exit 0 (1ms)\nabc"); strings.Contains(got, "cut") || !strings.HasPrefix(got, "[summarized; stored output: ") {
		t.Errorf("without truncation: %s", got)
	}
	if got := rawNote(rawstore.New(""), "x"); got != "[summarized; rerun with raw=true for unsummarized output]" {
		t.Errorf("storage disabled: %s", got)
	}
}

func TestCompactRunNeverGrowsOutput(t *testing.T) {
	store := rawstore.New(t.TempDir())

	var rows []string
	for i := 0; i < 8; i++ {
		rows = append(rows, fmt.Sprintf("row %02d xxxx", i))
	}
	cr := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "exit 0 (1ms)\n" + strings.Join(rows, "\n")}}}
	if got := compactRun(cr, "", 60, store); got != nil {
		t.Errorf("a result that did not get shorter must be returned as is (nil), got: %s", result.String(got))
	}
}
