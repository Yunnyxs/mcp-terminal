package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
)

func Register(srv *mcp.Server, s config.Settings) {
	addRun(srv, s)
	readTool.register(srv, s)
	writeTool.register(srv, s)
	editTool.register(srv, s)
	deleteTool.register(srv, s)
	checkpointTool.register(srv, s)
}

type sudoInput interface{ wantSudo() bool }

type fileTool[In sudoInput, P any] struct {
	name, desc string
	timeout    time.Duration
	prepare    func(config.Settings, In) (P, error)
	exec       func(config.Settings, P) (*mcp.CallToolResult, error)
}

func (t fileTool[In, P]) register(srv *mcp.Server, s config.Settings) {
	mcp.AddTool(srv, &mcp.Tool{Name: t.name, Description: t.desc},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			p, err := t.prepare(s, in)
			if err != nil {
				return nil, nil, err
			}
			if in.wantSudo() {
				res, err := sudoCall(ctx, s, t.name, t.timeout, p)
				return res, nil, err
			}
			res, err := t.exec(s, p)
			return res, nil, withHint(err)
		})
}

func (t fileTool[In, P]) helper() func(config.Settings, json.RawMessage) (*mcp.CallToolResult, error) {
	return func(s config.Settings, raw json.RawMessage) (*mcp.CallToolResult, error) {
		var p P
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return t.exec(s, p)
	}
}

var helperOps = map[string]func(config.Settings, json.RawMessage) (*mcp.CallToolResult, error){
	readTool.name:       readTool.helper(),
	writeTool.name:      writeTool.helper(),
	editTool.name:       editTool.helper(),
	deleteTool.name:     deleteTool.helper(),
	checkpointTool.name: checkpointTool.helper(),
}

func resolve(root, raw string) (string, error) {
	if raw == "" {
		return "", errors.New("path is empty")
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		raw = filepath.Join(home, raw[1:])
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(root, raw)
	}
	return filepath.Clean(raw), nil
}

func withHint(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w (retry with sudo=true)", err)
	}
	return err
}
