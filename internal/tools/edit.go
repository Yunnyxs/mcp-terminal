package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/fsutil"
	"terminal-mcp/internal/result"
)

const maxEditBytes = 10 << 20

type editInput struct {
	Path       string `json:"path" jsonschema:"file path, relative to root"`
	Old        string `json:"old" jsonschema:"exact text to replace; must match exactly once unless replace_all"`
	New        string `json:"new" jsonschema:"replacement text (may be empty)"`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"replace every match"`
	Sudo       bool   `json:"sudo,omitempty" jsonschema:"run as root (sudo -n)"`
}

func (in editInput) wantSudo() bool { return in.Sudo }

var editTool = fileTool[editInput, editInput]{
	name:    "edit",
	desc:    "Replace exact text in an existing file. Fails if the text is missing or matches more than once (unless replace_all).",
	timeout: sudoTimeout,
	prepare: func(s config.Settings, in editInput) (editInput, error) {
		p, err := resolve(s.Root, in.Path)
		in.Path, in.Sudo = p, false
		return in, err
	},
	exec: editFile,
}

func editFile(s config.Settings, in editInput) (*mcp.CallToolResult, error) {
	if in.Old == "" {
		return nil, errors.New("old is empty")
	}
	if in.Old == in.New {
		return nil, errors.New("old and new are identical")
	}
	p, err := resolve(s.Root, in.Path)
	if err != nil {
		return nil, err
	}
	target, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", target)
	}
	if info.Size() > maxEditBytes {
		return nil, fmt.Errorf("file too large to edit (%d bytes, max %d)", info.Size(), maxEditBytes)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}

	content := string(data)
	count := strings.Count(content, in.Old)
	switch {
	case count == 0:
		return nil, errors.New("old text not found (check exact whitespace and line breaks)")
	case count > 1 && !in.ReplaceAll:
		return nil, fmt.Errorf("old text matches %d times; add surrounding context or set replace_all", count)
	}
	replaced := 1
	if in.ReplaceAll {
		replaced = count
	}
	updated := strings.Replace(content, in.Old, in.New, replaced)
	if err := fsutil.WriteFileLike(target, []byte(updated), info); err != nil {
		return nil, err
	}
	return result.Text(fmt.Sprintf("replaced %d occurrence(s) in %s", replaced, p)), nil
}
