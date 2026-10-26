package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/mcp/proto"
)

func (h *host) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-h.stopCh:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			h.shutdown("listener stopped: " + err.Error())
			return
		}
		go h.handle(conn)
	}
}

func (h *host) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var req proto.Request
	if err := json.NewDecoder(io.LimitReader(conn, proto.MaxRequestBytes)).Decode(&req); err != nil {
		reply(conn, errResp("invalid request: "+err.Error()))
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	switch req.Op {
	case proto.OpPing:
		reply(conn, proto.Response{OK: true, Info: h.info()})
	case proto.OpStop:
		reply(conn, proto.Response{OK: true})
		h.shutdown("unload requested")
	case proto.OpTools:
		reply(conn, h.doTools(req))
	case proto.OpCall:
		reply(conn, h.doCall(req))
	default:
		reply(conn, errResp(fmt.Sprintf("unknown op: %q", req.Op)))
	}
}

func reply(conn net.Conn, r proto.Response) {
	_ = conn.SetWriteDeadline(time.Now().Add(60 * time.Second))
	_ = json.NewEncoder(conn).Encode(r)
}

func errResp(msg string) proto.Response { return proto.Response{Error: msg} }

func (h *host) info() *proto.Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	return &proto.Info{
		Name: h.name, Kind: h.entry.Kind(), PID: os.Getpid(), Started: h.started, LastUsed: h.last,
		IdleMS: h.idle.Milliseconds(), Active: h.active, Server: h.server, Version: h.version, Tools: h.nTools,
	}
}

func (h *host) begin() {
	h.mu.Lock()
	h.active++
	h.last = time.Now()
	h.mu.Unlock()
}

func (h *host) end() {
	h.mu.Lock()
	h.active--
	h.last = time.Now()
	h.mu.Unlock()
}

func (h *host) doTools(req proto.Request) proto.Response {
	h.begin()
	defer h.end()
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	var out []proto.ToolInfo
	for t, err := range h.cs.Tools(ctx, nil) {
		if err != nil {
			return errResp("failed to fetch the tool list: " + err.Error())
		}
		if req.Tool != "" && t.Name != req.Tool {
			continue
		}
		schema, _ := json.Marshal(t.InputSchema)
		out = append(out, proto.ToolInfo{Name: t.Name, Description: t.Description, Schema: schema})
	}
	if req.Tool != "" && len(out) == 0 {
		return errResp(fmt.Sprintf("tool %q is not in MCP %q", req.Tool, h.name))
	}
	return proto.Response{OK: true, Tools: out}
}

func (h *host) doCall(req proto.Request) proto.Response {
	if req.Tool == "" {
		return errResp("tool name is empty")
	}
	var args any = map[string]any{}
	if a := bytes.TrimSpace(req.Args); len(a) > 0 && string(a) != "null" {
		var m map[string]any
		if err := json.Unmarshal(a, &m); err != nil {
			return errResp("arguments must be a JSON object: " + err.Error())
		}
		args = m
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultCallTimeout
	}
	h.begin()
	defer h.end()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	res, err := h.cs.CallTool(ctx, &sdk.CallToolParams{Name: req.Tool, Arguments: args})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errResp(fmt.Sprintf("call %q exceeded the time limit of %s", req.Tool, timeout))
		}
		return errResp(err.Error())
	}
	return proto.Response{OK: true, Result: convertResult(res)}
}
