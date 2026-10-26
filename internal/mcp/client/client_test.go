package client

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/proto"
)

func shortPaths(t *testing.T) config.Paths {
	t.Helper()
	dir, err := os.MkdirTemp("", "tm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p := config.Paths{Dir: dir, RunDir: dir + "/run", LogDir: dir + "/mcp", OutDir: dir + "/mcp/out"}
	if err := os.MkdirAll(p.RunDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func fakeHost(t *testing.T, p config.Paths, name string, handler func(proto.Request) any) {
	t.Helper()
	sock, err := proto.Socket(p, name)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var req proto.Request
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				_ = json.NewEncoder(conn).Encode(handler(req))
			}()
		}
	}()
}

func TestNotLoaded(t *testing.T) {
	p := shortPaths(t)
	c := New(p)

	if _, err := c.Ping("x"); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("no socket: err = %v, want ErrNotLoaded", err)
	}

	sock, _ := proto.Socket(p, "x")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Tools("x", ""); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("dead socket: err = %v, want ErrNotLoaded", err)
	}
	if _, err := c.Call("x", "t", nil, time.Second); !errors.Is(err, ErrNotLoaded) {
		t.Errorf("call: err = %v, want ErrNotLoaded", err)
	}
}

func TestSocketPathTooLong(t *testing.T) {
	c := New(config.Paths{RunDir: "/tmp/" + strings.Repeat("x", 120)})
	if _, err := c.Ping("x"); err == nil || errors.Is(err, ErrNotLoaded) || !strings.Contains(err.Error(), "too long") {
		t.Errorf("err = %v", err)
	}
}

func TestPing(t *testing.T) {
	p := shortPaths(t)
	fakeHost(t, p, "ok", func(req proto.Request) any {
		if req.Op != proto.OpPing {
			t.Errorf("op = %q", req.Op)
		}
		return proto.Response{OK: true, Info: &proto.Info{Name: "ok", PID: 42, Tools: 3}}
	})
	info, err := New(p).Ping("ok")
	if err != nil || info.Name != "ok" || info.PID != 42 || info.Tools != 3 {
		t.Errorf("Ping = %+v, %v", info, err)
	}

	fakeHost(t, p, "rejected", func(proto.Request) any { return proto.Response{Error: "busy right now"} })
	if _, err := New(p).Ping("rejected"); err == nil || !strings.Contains(err.Error(), "ping rejected: busy right now") {
		t.Errorf("err = %v", err)
	}
}

func TestTools(t *testing.T) {
	p := shortPaths(t)
	fakeHost(t, p, "m", func(req proto.Request) any {
		if req.Op != proto.OpTools || req.Tool != "echo" {
			return proto.Response{Error: `tool "x" is not in MCP "m"`}
		}
		return proto.Response{OK: true, Tools: []proto.ToolInfo{{Name: "echo"}}}
	})
	c := New(p)
	list, err := c.Tools("m", "echo")
	if err != nil || len(list) != 1 || list[0].Name != "echo" {
		t.Errorf("Tools = %+v, %v", list, err)
	}
	if _, err := c.Tools("m", "x"); err == nil || err.Error() != `tool "x" is not in MCP "m"` {
		t.Errorf("host error message must be passed through unchanged, got: %v", err)
	}
}

func TestCallPassesArgsAndTimeout(t *testing.T) {
	p := shortPaths(t)
	fakeHost(t, p, "m", func(req proto.Request) any {
		if req.Op != proto.OpCall || req.Tool != "echo" || string(req.Args) != `{"text":"hi"}` || req.TimeoutMS != 2000 {
			return proto.Response{Error: "wrong request: " + req.Op + " " + req.Tool + " " + string(req.Args)}
		}
		return proto.Response{OK: true, Result: &proto.CallResult{Parts: []proto.Part{{Kind: "text", Text: "echo: hi"}}}}
	})
	res, err := New(p).Call("m", "echo", json.RawMessage(`{"text":"hi"}`), 2*time.Second)
	if err != nil || len(res.Parts) != 1 || res.Parts[0].Text != "echo: hi" {
		t.Errorf("Call = %+v, %v", res, err)
	}

	fakeHost(t, p, "failing", func(proto.Request) any { return proto.Response{Error: "tool not found"} })
	if _, err := New(p).Call("failing", "x", nil, time.Second); err == nil || err.Error() != "tool not found" {
		t.Errorf("err = %v", err)
	}
}

func TestStop(t *testing.T) {
	p := shortPaths(t)
	fakeHost(t, p, "ok", func(req proto.Request) any {
		if req.Op != proto.OpStop {
			t.Errorf("op = %q", req.Op)
		}
		return proto.Response{OK: true}
	})
	if err := New(p).Stop("ok"); err != nil {
		t.Errorf("Stop: %v", err)
	}

	fakeHost(t, p, "deny", func(proto.Request) any { return proto.Response{Error: "not allowed"} })
	if err := New(p).Stop("deny"); err == nil || !strings.Contains(err.Error(), "stop command rejected: not allowed") {
		t.Errorf("err = %v", err)
	}
	if err := New(p).Stop("missing"); err == nil || !strings.Contains(err.Error(), "stop command failed") || !errors.Is(err, ErrNotLoaded) {
		t.Errorf("host missing: err = %v", err)
	}
}

func TestUnreadableResponse(t *testing.T) {
	p := shortPaths(t)
	fakeHost(t, p, "broken", func(proto.Request) any { return "not a response" })
	if _, err := New(p).Ping("broken"); err == nil || !strings.Contains(err.Error(), "could not read the host reply") {
		t.Errorf("err = %v", err)
	}
}
