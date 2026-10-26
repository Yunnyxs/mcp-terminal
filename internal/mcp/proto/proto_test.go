package proto

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"terminal-mcp/internal/config"
)

func TestSocketPathLimit(t *testing.T) {
	long := config.Paths{RunDir: "/tmp/" + strings.Repeat("x", 120)}
	if _, err := Socket(long, "a"); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("path that is too long must be rejected, got: %v", err)
	}
	short := config.Paths{RunDir: "/tmp/run"}
	if s, err := Socket(short, "a"); err != nil || s != "/tmp/run/a.sock" {
		t.Errorf("Socket = %q, %v", s, err)
	}
}

func TestFileLayout(t *testing.T) {
	p := config.Paths{RunDir: "/d/run", LogDir: "/d/mcp"}
	want := map[string]string{
		"pid":  "/d/run/x.pid",
		"lock": "/d/run/x.lock",
		"log":  "/d/mcp/x.log",
	}
	got := map[string]string{"pid": PIDFile(p, "x"), "lock": LockFile(p, "x"), "log": LogFile(p, "x")}
	for k, v := range want {
		if got[k] != filepath.FromSlash(v) {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestWireFormat(t *testing.T) {
	b, err := json.Marshal(Request{Op: OpCall, Tool: "t", Args: json.RawMessage(`{"a":1}`), TimeoutMS: 5})
	if err != nil || string(b) != `{"op":"call","tool":"t","args":{"a":1},"timeout_ms":5}` {
		t.Errorf("Request = %s err=%v", b, err)
	}
	b, _ = json.Marshal(Response{OK: true, Result: &CallResult{IsError: true, Parts: []Part{{Kind: "text", Text: "x"}}}})
	if string(b) != `{"ok":true,"result":{"is_error":true,"parts":[{"kind":"text","text":"x"}]}}` {
		t.Errorf("Response = %s", b)
	}
	b, _ = json.Marshal(Part{Kind: "image", MIME: "image/png", Data: []byte{1, 2, 3}})
	if string(b) != `{"kind":"image","mime":"image/png","data":"AQID"}` {
		t.Errorf("Part binary = %s", b)
	}
	b, _ = json.Marshal(Request{Op: OpPing})
	if string(b) != `{"op":"ping"}` {
		t.Errorf("ping = %s", b)
	}
}
