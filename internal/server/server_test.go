package server

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
)

func testSettings(t *testing.T) config.Settings {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, "data")
	return config.Settings{
		Root: root, Shell: "/bin/sh", Timeout: 10 * time.Second, MaxOutput: 30000,
		TrashDir: filepath.Join(data, "Trash"), NotesDir: filepath.Join(data, "notes"),
		ActivityLog: filepath.Join(data, "a.log"), ServerLog: filepath.Join(data, "s.log"),
	}
}

func connect(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
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

func TestInstructions(t *testing.T) {
	got := instructions("/data/notes")
	for _, want := range []string{"/data/notes", "00-read-first.md", "list it (run: ls)", "Never delete or overwrite notes"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions do not contain %q:\n%s", want, got)
		}
	}

	if len(got) > 500 {
		t.Errorf("instructions are %d bytes, too long to send in every session", len(got))
	}
}

func TestNewAdvertisesIdentityInstructionsAndTools(t *testing.T) {
	s := testSettings(t)
	cs := connect(t, New(s, Options{}))

	ir := cs.InitializeResult()
	if ir.ServerInfo.Name != "terminal-mcp" || ir.ServerInfo.Version != config.Version {
		t.Errorf("ServerInfo = %+v", ir.ServerInfo)
	}
	if ir.Instructions != instructions(s.NotesDir) || !strings.Contains(ir.Instructions, s.NotesDir) {
		t.Errorf("Instructions = %q", ir.Instructions)
	}

	list, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	if got := strings.Join(sortedCopy(names), ","); got != "delete,edit,read,run,write" {
		t.Errorf("tool = %s", got)
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestNewAppliesMiddlewareInOrderAndSkipsNil(t *testing.T) {
	s := testSettings(t)
	var order []string
	mk := func(name string) mcp.Middleware {
		return func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				if method == "tools/call" {
					order = append(order, name)
				}
				return next(ctx, method, req)
			}
		}
	}
	cs := connect(t, New(s, Options{Middleware: []mcp.Middleware{mk("a"), nil, mk("b")}}))

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "run", Arguments: map[string]any{"command": "echo ok"}})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}

	if got := strings.Join(order, ","); got != "b,a" && got != "a,b" {
		t.Errorf("order = %q", got)
	}
	if len(order) != 2 {
		t.Errorf("middleware was called %d times, want 2", len(order))
	}
}

const testToken = "0123456789abcdef0123456789abcdef"

func newHTTP(t *testing.T) (*httptest.Server, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	h, err := NewHandler(New(testSettings(t), Options{}), testToken, log.New(&logs, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, &logs
}

func TestNewHandlerRejectsWeakToken(t *testing.T) {
	if h, err := NewHandler(New(testSettings(t), Options{}), "short", nil); err == nil || h != nil {
		t.Errorf("a weak token must be rejected: %v %v", h, err)
	}
}

func TestHTTPEndToEndWithRealClient(t *testing.T) {
	ts, logs := newHTTP(t)
	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: ts.URL + "/" + testToken + "/mcp", DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	if cs.InitializeResult().ServerInfo.Name != "terminal-mcp" {
		t.Errorf("ServerInfo = %+v", cs.InitializeResult().ServerInfo)
	}
	list, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(list.Tools) != 5 {
		t.Fatalf("ListTools = %d tool, %v", len(list.Tools), err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "run", Arguments: map[string]any{"command": "echo via-http"}})
	if err != nil || res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "via-http") {
		t.Errorf("CallTool = %+v, %v", res, err)
	}
	if !strings.Contains(logs.String(), "http POST status=200") {
		t.Errorf("request was not recorded:\n%s", logs)
	}
}

func TestHTTPWrongTokenIs404AndNeverLogsToken(t *testing.T) {
	ts, logs := newHTTP(t)
	wrong := strings.Repeat("f", len(testToken))

	for _, path := range []string{"/" + wrong + "/mcp", "/mcp", "/", "/" + testToken + "/other"} {
		resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, resp.StatusCode)
		}
	}
	out := logs.String()
	if strings.Count(out, "status=404") != 4 {
		t.Errorf("every rejected request must be recorded:\n%s", out)
	}
	for _, secret := range []string{testToken, wrong, "/mcp"} {
		if strings.Contains(out, secret) {
			t.Errorf("log contains %q (paths/tokens must not be written):\n%s", secret, out)
		}
	}
}

func TestLogRequestsViaCloudflareUsesRealIP(t *testing.T) {
	var logs bytes.Buffer
	h := logRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }), log.New(&logs, "", 0))

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Cf-Ray", "abc")
	req.Header.Set("Cf-Connecting-Ip", "203.0.113.7")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got := logs.String(); !strings.Contains(got, "http POST status=202") || !strings.Contains(got, "via=cf ip=203.0.113.7") {
		t.Errorf("log = %q", got)
	}

	logs.Reset()
	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got := logs.String(); !strings.Contains(got, "via=local ip=127.0.0.1:5555") {
		t.Errorf("log = %q", got)
	}
}

func TestStatusRecorder(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: inner, status: http.StatusOK}

	rec.WriteHeader(http.StatusTeapot)
	rec.Flush()
	if rec.status != http.StatusTeapot || inner.Code != http.StatusTeapot {
		t.Errorf("status = %d / %d", rec.status, inner.Code)
	}
	if !inner.Flushed {
		t.Error("Flush was not forwarded (streaming would break)")
	}
	if rec.Unwrap() != http.ResponseWriter(inner) {
		t.Error("Unwrap did not return the original writer")
	}

	(&statusRecorder{ResponseWriter: noFlush{}}).Flush()
}

type noFlush struct{ http.ResponseWriter }
