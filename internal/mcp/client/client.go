package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"syscall"
	"time"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/proto"
)

var ErrNotLoaded = errors.New("MCP is not loaded")

type Client struct{ paths config.Paths }

func New(p config.Paths) *Client { return &Client{paths: p} }

func (c *Client) roundTrip(name string, req proto.Request, wait time.Duration) (*proto.Response, error) {
	sock, err := proto.Socket(c.paths, name)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, ErrNotLoaded
		}
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(wait))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}
	var resp proto.Response
	if err := json.NewDecoder(io.LimitReader(conn, proto.MaxResponseBytes)).Decode(&resp); err != nil {
		return nil, fmt.Errorf("could not read the host reply: %w", err)
	}
	return &resp, nil
}

func (c *Client) Ping(name string) (*proto.Info, error) {
	resp, err := c.roundTrip(name, proto.Request{Op: proto.OpPing}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if !resp.OK || resp.Info == nil {
		return nil, fmt.Errorf("ping rejected: %s", resp.Error)
	}
	return resp.Info, nil
}

func (c *Client) Tools(name, tool string) ([]proto.ToolInfo, error) {
	resp, err := c.roundTrip(name, proto.Request{Op: proto.OpTools, Tool: tool}, 60*time.Second)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Error)
	}
	return resp.Tools, nil
}

func (c *Client) Call(name, tool string, args json.RawMessage, timeout time.Duration) (*proto.CallResult, error) {
	req := proto.Request{Op: proto.OpCall, Tool: tool, Args: args, TimeoutMS: timeout.Milliseconds()}
	resp, err := c.roundTrip(name, req, timeout+15*time.Second)
	if err != nil {
		return nil, err
	}
	if !resp.OK || resp.Result == nil {
		return nil, errors.New(resp.Error)
	}
	return resp.Result, nil
}

func (c *Client) Stop(name string) error {
	resp, err := c.roundTrip(name, proto.Request{Op: proto.OpStop}, 15*time.Second)
	if err != nil {
		return fmt.Errorf("stop command failed: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("stop command rejected: %s", resp.Error)
	}
	return nil
}
