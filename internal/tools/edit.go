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

type editOp struct {
	Old        string  `json:"old,omitempty" jsonschema:"exact text to replace; must match exactly once unless replace_all"`
	New        *string `json:"new,omitempty" jsonschema:"replacement text (may be empty)"`
	ReplaceAll bool    `json:"replace_all,omitempty" jsonschema:"replace every match"`
}

type editInput struct {
	Path       string   `json:"path" jsonschema:"file path, relative to root"`
	Old        string   `json:"old,omitempty" jsonschema:"exact text to replace; must match exactly once unless replace_all"`
	New        *string  `json:"new,omitempty" jsonschema:"replacement text (may be empty)"`
	Edits      []editOp `json:"edits,omitempty" jsonschema:"several {old,new,replace_all} edits, instead of old/new"`
	ReplaceAll bool     `json:"replace_all,omitempty" jsonschema:"replace every match"`
	Sudo       bool     `json:"sudo,omitempty" jsonschema:"run as root (sudo -n)"`
}

func (in editInput) wantSudo() bool { return in.Sudo }

var editTool = fileTool[editInput, editInput]{
	name:    "edit",
	desc:    "Replace exact text in an existing file. Fails if the text is missing or matches more than once (unless replace_all). Use edits=[{old,new}] for several replacements, applied in order, all-or-nothing.",
	timeout: sudoTimeout,
	prepare: func(s config.Settings, in editInput) (editInput, error) {
		p, err := resolve(s.Root, in.Path)
		in.Path, in.Sudo = p, false
		if err != nil {
			return in, err
		}
		if in.Edits != nil {
			switch n := len(in.Edits); {
			case in.Old != "" || in.New != nil:
				return in, errors.New("use either old/new or edits, not both")
			case in.ReplaceAll:
				return in, errors.New("replace_all goes inside each edit, not at top level")
			case n == 0:
				return in, errors.New("edits is empty")
			case n > s.MaxEdits:
				return in, fmt.Errorf("too many edits (%d, max %d): split into several calls or raise TERMINAL_MCP_MAX_EDITS", n, s.MaxEdits)
			}
		}
		return in, nil
	},
	exec: editFile,
}

func opErr(idx int, err error) error {
	if idx == 0 {
		return err
	}
	return fmt.Errorf("edit #%d: %w", idx, err)
}

func checkEdit(op editOp, idx int) error {
	switch {
	case op.Old == "":
		return opErr(idx, errors.New("old is empty"))
	case op.New == nil:
		return opErr(idx, errors.New("new is required"))
	case op.Old == *op.New:
		return opErr(idx, errors.New("old and new are identical"))
	}
	return nil
}

// applyEdit applies one edit; idx is the 1-based position in a batch, 0 for the single mode.
func applyEdit(content string, op editOp, idx int) (string, int, error) {
	if err := checkEdit(op, idx); err != nil {
		return "", 0, err
	}
	count := strings.Count(content, op.Old)
	switch {
	case count == 0:
		return "", 0, opErr(idx, errors.New("old text not found (check exact whitespace and line breaks)"))
	case count > 1 && !op.ReplaceAll:
		return "", 0, opErr(idx, fmt.Errorf("old text matches %d times; add surrounding context or set replace_all", count))
	}
	replaced := 1
	if op.ReplaceAll {
		replaced = count
	}
	return strings.Replace(content, op.Old, *op.New, replaced), replaced, nil
}

func editFile(s config.Settings, in editInput) (*mcp.CallToolResult, error) {
	batch := in.Edits != nil
	ops := in.Edits
	if !batch {
		ops = []editOp{{Old: in.Old, New: in.New, ReplaceAll: in.ReplaceAll}}
	}
	if len(ops) == 0 {
		return nil, errors.New("edits is empty")
	}
	for i, op := range ops {
		idx := 0
		if batch {
			idx = i + 1
		}
		if err := checkEdit(op, idx); err != nil {
			return nil, err
		}
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

	content, total := string(data), 0
	for i, op := range ops {
		idx := 0
		if batch {
			idx = i + 1
		}
		var n int
		if content, n, err = applyEdit(content, op, idx); err != nil {
			return nil, err
		}
		total += n
	}
	if err := fsutil.WriteFileLike(target, []byte(content), info); err != nil {
		return nil, err
	}
	if batch {
		return result.Text(fmt.Sprintf("applied %d edits (%d replacements) in %s", len(ops), total, p)), nil
	}
	return result.Text(fmt.Sprintf("replaced %d occurrence(s) in %s", total, p)), nil
}
