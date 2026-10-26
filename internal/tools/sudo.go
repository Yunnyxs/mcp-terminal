package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/result"
)

const (
	sudoBin     = "/usr/bin/sudo"
	sudoTimeout = 30 * time.Second

	deleteSudoTimeout = 10 * time.Minute
)

type fileOpRequest struct {
	Op        string          `json:"op"`
	MaxOutput int             `json:"max_output"`
	Args      json.RawMessage `json:"args"`
}

type fileOpResponse struct {
	Text    string `json:"text,omitempty"`
	Error   string `json:"error,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}

func sudoCall(ctx context.Context, s config.Settings, op string, timeout time.Duration, payload any) (*mcp.CallToolResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe = strings.TrimSuffix(exe, " (deleted)")

	args, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(fileOpRequest{Op: op, MaxOutput: s.MaxOutput, Args: args})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sudoBin, "-n", "--", exe, "fileop")
	cmd.Stdin = bytes.NewReader(body)
	var stdout bytes.Buffer
	stderr := newCapture(2000)
	cmd.Stdout, cmd.Stderr = &stdout, stderr

	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		hint := ""
		if strings.Contains(msg, "password") {
			hint = " (sudo=true needs passwordless sudo: NOPASSWD in sudoers)"
		}
		return nil, fmt.Errorf("sudo failed: %v: %s%s", err, msg, hint)
	}

	var resp fileOpResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("unexpected helper output: %w", err)
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	res := result.Text(resp.Text)
	res.IsError = resp.IsError
	return res, nil
}

func ServeFileOp(r io.Reader, w io.Writer) error {
	var req fileOpRequest
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return fmt.Errorf("bad request: %w", err)
	}
	s := config.Settings{Root: "/", MaxOutput: req.MaxOutput}

	var resp fileOpResponse
	op, ok := helperOps[req.Op]
	if !ok {
		resp.Error = fmt.Sprintf("unknown op %q", req.Op)
	} else if res, err := op(s, req.Args); err != nil {
		resp.Error = err.Error()
	} else {
		resp.Text, resp.IsError = result.String(res), res.IsError
	}
	return json.NewEncoder(w).Encode(resp)
}
