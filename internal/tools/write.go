package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/fsutil"
	"terminal-mcp/internal/result"
)

type writeInput struct {
	Path      string `json:"path" jsonschema:"file path, relative to root. Parent dirs are created"`
	Content   string `json:"content" jsonschema:"full file content"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"required to replace an existing file"`
	Sudo      bool   `json:"sudo,omitempty" jsonschema:"run as root (sudo -n)"`
}

func (in writeInput) wantSudo() bool { return in.Sudo }

var writeTool = fileTool[writeInput, writeInput]{
	name:    "write",
	desc:    "Write a whole file atomically. Refuses to replace an existing file unless overwrite=true.",
	timeout: sudoTimeout,
	prepare: func(s config.Settings, in writeInput) (writeInput, error) {
		p, err := resolve(s.Root, in.Path)
		in.Path, in.Sudo = p, false
		return in, err
	},
	exec: writeFile,
}

func writeFile(s config.Settings, in writeInput) (*mcp.CallToolResult, error) {
	p, err := resolve(s.Root, in.Path)
	if err != nil {
		return nil, err
	}

	target, err := filepath.EvalSymlinks(p)
	switch {
	case err == nil:
		if !in.Overwrite {
			return nil, fmt.Errorf("%s already exists; set overwrite=true to replace it", p)
		}
		info, statErr := os.Stat(target)
		if statErr != nil {
			return nil, statErr
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("not a regular file: %s", target)
		}
		if err := fsutil.WriteFileLike(target, []byte(in.Content), info); err != nil {
			return nil, err
		}
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := createNew(p, in.Content); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return result.Text(fmt.Sprintf("wrote %d bytes to %s", len(in.Content), p)), nil
}

func createNew(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
