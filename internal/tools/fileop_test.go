package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func fileOp(t *testing.T, op string, args any) fileOpResponse {
	t.Helper()
	a, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(fileOpRequest{Op: op, MaxOutput: 1000, Args: a})
	var out bytes.Buffer
	if err := ServeFileOp(bytes.NewReader(req), &out); err != nil {
		t.Fatalf("ServeFileOp: %v", err)
	}
	var resp fileOpResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %q", out.String())
	}
	return resp
}

func TestServeFileOpRoundTrip(t *testing.T) {
	s := testSettings(t)
	p := filepath.Join(s.Root, "f.txt")

	if r := fileOp(t, "write", map[string]any{"path": p, "content": "hi, world!"}); r.Error != "" || r.Text != "wrote 10 bytes to "+p {
		t.Errorf("write: %+v", r)
	}
	if r := fileOp(t, "read", map[string]any{"path": p}); r.Error != "" || r.Text != "hi, world!" {
		t.Errorf("read: %+v", r)
	}
	if r := fileOp(t, "edit", map[string]any{"path": p, "old": "world!", "new": "root"}); r.Error != "" || !strings.HasPrefix(r.Text, "replaced 1 occurrence(s)") {
		t.Errorf("edit: %+v", r)
	}
	if mustRead(t, p) != "hi, root" {
		t.Error("an edit through the helper did not change the file")
	}

	for _, sub := range []string{"files", "info"} {
		mustWrite(t, filepath.Join(s.TrashDir, sub, ".keep"), "", 0o600)
	}
	plan := deletePlan{Paths: []string{p}, TrashDir: s.TrashDir, Protected: []string{s.Root}}
	if r := fileOp(t, "delete", plan); r.Error != "" || r.IsError || !strings.Contains(r.Text, "[ok] "+p+" -> Trash") || exists(p) {
		t.Errorf("delete: %+v", r)
	}
}

func TestServeFileOpReportsFailuresInJSON(t *testing.T) {
	s := testSettings(t)
	if r := fileOp(t, "bogus", map[string]any{}); r.Error != `unknown op "bogus"` {
		t.Errorf("unknown op: %+v", r)
	}
	if r := fileOp(t, "read", map[string]any{"path": filepath.Join(s.Root, "nope")}); !strings.Contains(r.Error, "no such file") {
		t.Errorf("read failed: %+v", r)
	}

	plan := deletePlan{Paths: []string{s.Root}, TrashDir: s.TrashDir, Protected: []string{s.Root}}
	if r := fileOp(t, "delete", plan); r.Error != "" || !r.IsError {
		t.Errorf("delete rejected: %+v", r)
	}

	var out bytes.Buffer
	if err := ServeFileOp(strings.NewReader("not json"), &out); err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Errorf("broken JSON: err = %v", err)
	}
}

func TestHelperOpsMatchRegisteredTools(t *testing.T) {
	s := testSettings(t)
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(srv, s)
	cs := connect(t, srv)

	list, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)

		if tool.Annotations != nil {
			t.Errorf("tool %s has Annotations: %+v", tool.Name, tool.Annotations)
		}
	}
	if want := []string{"delete", "edit", "read", "run", "write"}; !slices.Equal(sorted(names), want) {
		t.Errorf("registered tools = %v, want %v", sorted(names), want)
	}
	for _, name := range names {
		_, inTable := helperOps[name]
		if name == "run" && inTable || name != "run" && !inTable {
			t.Errorf("the helperOps table is out of sync for tool %q (present=%v)", name, inTable)
		}
	}
}

func TestRegisteredToolsEndToEnd(t *testing.T) {
	s := testSettings(t)
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(srv, s)
	cs := connect(t, srv)

	call := func(name string, args map[string]any) (string, bool) {
		t.Helper()
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return textOf(res), res.IsError
	}

	if out, bad := call("write", map[string]any{"path": "n.txt", "content": "txt"}); bad || !strings.HasPrefix(out, "wrote 3 bytes to ") {
		t.Errorf("write: %q bad=%v", out, bad)
	}
	if out, bad := call("read", map[string]any{"path": "n.txt"}); bad || out != "txt" {
		t.Errorf("read: %q bad=%v", out, bad)
	}
	if out, bad := call("run", map[string]any{"command": "cat n.txt"}); bad || !strings.HasSuffix(out, "\ntxt") {
		t.Errorf("run: %q bad=%v", out, bad)
	}

	if out, bad := call("read", map[string]any{"path": "nope"}); !bad || !strings.Contains(out, "no such file") {
		t.Errorf("read failed: %q bad=%v", out, bad)
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
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func sorted(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
