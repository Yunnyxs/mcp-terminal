package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Text string `json:"text"`
}

func newClient(t *testing.T, w *bytes.Buffer, lg *log.Logger) *mcp.ClientSession {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + in.Text}}}, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "boom"}, func(context.Context, *mcp.CallToolRequest, echoIn) (*mcp.CallToolResult, any, error) {
		return nil, nil, errors.New("exploded")
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "pic"}, func(context.Context, *mcp.CallToolRequest, echoIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: []byte{1, 2}, MIMEType: "image/png"}}}, nil, nil
	})
	srv.AddReceivingMiddleware(Activity(w, lg))

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

func call(t *testing.T, cs *mcp.ClientSession, tool, text string) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: map[string]any{"text": text}})
	if err != nil {
		t.Fatalf("CallTool %s: %v", tool, err)
	}
	return res
}

func entries(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line is not JSON: %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestActivityRecordsToolCall(t *testing.T) {
	var buf bytes.Buffer
	cs := newClient(t, &buf, nil)
	call(t, cs, "echo", "hello <&>")

	es := entries(t, &buf)
	if len(es) != 1 {
		t.Fatalf("entries = %d, want 1: %s", len(es), buf.String())
	}
	e := es[0]
	if e["tool"] != "echo" || e["is_error"] != false || e["result"] != "echo: hello <&>" {
		t.Errorf("entries = %v", e)
	}
	args, _ := e["args"].(map[string]any)
	if args["text"] != "hello <&>" {
		t.Errorf("args = %v", e["args"])
	}
	if _, ok := e["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms missing: %v", e)
	}
	if ts, _ := e["time"].(string); len(ts) < len("2006-01-02T15:04:05.000+00:00") {
		t.Errorf("time = %q", ts)
	}

	if strings.Contains(buf.String(), "\\u003c") || strings.Contains(buf.String(), "\\u0026") {
		t.Errorf("HTML escaped: %s", buf.String())
	}
	if !strings.HasSuffix(buf.String(), "\n") || strings.Count(buf.String(), "\n") != 1 {
		t.Errorf("must be exactly one line: %q", buf.String())
	}
}

func TestActivityRecordsToolError(t *testing.T) {
	var buf bytes.Buffer
	cs := newClient(t, &buf, nil)
	res := call(t, cs, "boom", "x")
	if !res.IsError {
		t.Fatal("SDK should wrap tool errors as IsError")
	}
	es := entries(t, &buf)
	if len(es) != 1 || es[0]["is_error"] != true || !strings.Contains(es[0]["result"].(string), "exploded") {
		t.Errorf("entries = %v", es)
	}
}

func TestActivityIgnoresOtherMethods(t *testing.T) {
	var buf bytes.Buffer
	cs := newClient(t, &buf, nil)
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := cs.Ping(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("only tools/call should be logged, but the log contains: %s", buf.String())
	}
}

func TestActivityNonTextContent(t *testing.T) {
	var buf bytes.Buffer
	cs := newClient(t, &buf, nil)
	call(t, cs, "pic", "x")
	es := entries(t, &buf)
	if len(es) != 1 || es[0]["result"] != "[*mcp.ImageContent]" {
		t.Errorf("entries = %v", es)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestActivityWriteFailureDoesNotFailTool(t *testing.T) {
	var errBuf bytes.Buffer
	lg := log.New(&errBuf, "", 0)
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok " + in.Text}}}, nil, nil
	})
	srv.AddReceivingMiddleware(Activity(failWriter{}, lg))
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	res := call(t, cs, "echo", "a")
	if res.IsError || res.Content[0].(*mcp.TextContent).Text != "ok a" {
		t.Errorf("tool must still succeed: %+v", res)
	}
	if !strings.Contains(errBuf.String(), "write failed") || !strings.Contains(errBuf.String(), "disk full") {
		t.Errorf("write failure must be reported to errLog, got: %q", errBuf.String())
	}
}
