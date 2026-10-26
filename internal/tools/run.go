package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/result"
)

const maxTimeoutSec = 3600

type runInput struct {
	Command string `json:"command" jsonschema:"shell command"`
	Cwd     string `json:"cwd,omitempty" jsonschema:"working dir, relative to root. Default: root"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"seconds, 1-3600. Default: server setting"`
	Raw     bool   `json:"raw,omitempty" jsonschema:"true = unsummarized output. Default: long output is summarized"`
}

func addRun(srv *mcp.Server, s config.Settings) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run",
		Description: "Run a shell command in a login shell (no stdin, not interactive). Returns exit code and combined stdout+stderr, truncated if long. Prefix with `sudo -n` for root.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runInput) (*mcp.CallToolResult, any, error) {
		res, err := runCommand(ctx, s, in)
		return res, nil, err
	})
}

func runCommand(ctx context.Context, s config.Settings, in runInput) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(in.Command) == "" {
		return nil, errors.New("command is empty")
	}

	dir := s.Root
	if in.Cwd != "" {
		var err error
		if dir, err = resolve(s.Root, in.Cwd); err != nil {
			return nil, err
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("cwd is not a directory: %s", dir)
		}
	}

	timeout := s.Timeout
	if in.Timeout != 0 {
		if in.Timeout < 1 || in.Timeout > maxTimeoutSec {
			return nil, fmt.Errorf("timeout must be 1-%d seconds", maxTimeoutSec)
		}
		timeout = time.Duration(in.Timeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.Shell, "-lc", in.Command)
	cmd.Dir = dir

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }

	cmd.WaitDelay = 3 * time.Second
	out := newCapture(s.MaxOutput)
	cmd.Stdout, cmd.Stderr = out, out

	start := time.Now()
	err := cmd.Run()
	took := time.Since(start).Round(10 * time.Millisecond)

	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if ctx.Err() != nil && cmd.Process != nil {

		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	code := "0"
	var exitErr *exec.ExitError
	switch {
	case timedOut:

	case errors.As(err, &exitErr):
		code = strconv.Itoa(exitErr.ExitCode())
		if exitErr.ExitCode() < 0 {
			code = exitErr.ProcessState.String()
		}
	case err != nil && !errors.Is(err, exec.ErrWaitDelay):
		return nil, err
	}

	header := fmt.Sprintf("exit %s (%s)", code, took)
	if timedOut {
		header = fmt.Sprintf("timeout after %s, killed", timeout)
	}
	body := out.String()
	if body == "" {
		body = "(no output)"
	}
	res := result.Text(header + "\n" + body)
	res.IsError = timedOut
	return res, nil
}
