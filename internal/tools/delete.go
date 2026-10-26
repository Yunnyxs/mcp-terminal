package tools

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/result"
)

const maxDeletePaths = 50

type deleteInput struct {
	Paths     []string `json:"paths" jsonschema:"explicit paths, no wildcards, max 50"`
	Recursive bool     `json:"recursive,omitempty" jsonschema:"required for non-empty directories"`
	Permanent bool     `json:"permanent,omitempty" jsonschema:"delete for good instead of moving to Trash"`
	Sudo      bool     `json:"sudo,omitempty" jsonschema:"run as root (sudo -n)"`
}

func (in deleteInput) wantSudo() bool { return in.Sudo }

type deletePlan struct {
	Paths     []string `json:"paths"`
	Recursive bool     `json:"recursive"`
	Permanent bool     `json:"permanent"`
	TrashDir  string   `json:"trash_dir"`
	Protected []string `json:"protected"`
}

var deleteTool = fileTool[deleteInput, deletePlan]{
	name:    "delete",
	desc:    "Delete files or directories by explicit path. Moves to Trash (restorable) unless permanent=true. Refuses protected paths. Prefer this over `rm`.",
	timeout: deleteSudoTimeout,
	prepare: planDelete,
	exec: func(_ config.Settings, plan deletePlan) (*mcp.CallToolResult, error) {
		return deleteAll(plan), nil
	},
}

type outcome int

const (
	outDone outcome = iota
	outSkipped
	outFailed
)

func planDelete(s config.Settings, in deleteInput) (deletePlan, error) {
	if len(in.Paths) == 0 || len(in.Paths) > maxDeletePaths {
		return deletePlan{}, fmt.Errorf("paths must have 1-%d entries", maxDeletePaths)
	}
	plan := deletePlan{
		Recursive: in.Recursive,
		Permanent: in.Permanent,
		TrashDir:  s.TrashDir,
		Protected: protectedPaths(s),
	}
	for _, raw := range in.Paths {
		p, err := resolve(s.Root, raw)
		if err != nil {
			return deletePlan{}, fmt.Errorf("%q: %w", raw, err)
		}
		plan.Paths = append(plan.Paths, p)
	}
	if !in.Permanent {

		for _, sub := range []string{"files", "info"} {
			if err := os.MkdirAll(filepath.Join(s.TrashDir, sub), 0o700); err != nil {
				return deletePlan{}, withHint(err)
			}
		}
	}
	return plan, nil
}

func protectedPaths(s config.Settings) []string {
	var out []string
	add := func(p string) {
		if p != "" {
			out = append(out, p)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home)
		add(filepath.Join(home, ".config", "terminal-mcp"))
	}
	add(s.Root)
	add(s.TrashDir)
	add(s.NotesDir)
	add(s.ActivityLog)
	add(s.ServerLog)
	if exe, err := os.Executable(); err == nil {
		add(strings.TrimSuffix(exe, " (deleted)"))
	}
	return out
}

func protectedReason(p string, protected []string) string {
	if p == "/" || filepath.Dir(p) == "/" {
		return "system path"
	}
	for _, q := range protected {
		if p == q {
			return "protected path"
		}
		if strings.HasPrefix(q, p+"/") {
			return "contains protected path " + q
		}
	}
	return ""
}

func canon(p string) string {
	p = filepath.Clean(p)
	if p == "/" {
		return p
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return p
	}
	return filepath.Join(dir, filepath.Base(p))
}

func deleteAll(plan deletePlan) *mcp.CallToolResult {
	protected := make([]string, len(plan.Protected))
	for i, q := range plan.Protected {
		protected[i] = canon(q)
	}
	trashDir := canon(plan.TrashDir)

	seen := map[string]bool{}
	var lines []string
	var ok, skipped, bad int
	for _, raw := range plan.Paths {
		line, out := deleteOne(plan, canon(raw), protected, trashDir, seen)
		lines = append(lines, line)
		switch out {
		case outDone:
			ok++
		case outSkipped:
			skipped++
		default:
			bad++
		}
	}

	verb := "Moved to Trash"
	if plan.Permanent {
		verb = "Deleted permanently"
	}
	header := fmt.Sprintf("%s: %d ok, %d rejected/failed", verb, ok, bad)
	if skipped > 0 {
		header += fmt.Sprintf(", %d skipped", skipped)
	}
	res := result.Text(header + "\n" + strings.Join(lines, "\n"))
	res.IsError = ok == 0 && bad > 0
	return res
}

func deleteOne(plan deletePlan, p string, protected []string, trashDir string, seen map[string]bool) (string, outcome) {
	if seen[p] {
		return fmt.Sprintf("[skipped] %s: duplicate", p), outSkipped
	}
	seen[p] = true

	if why := protectedReason(p, protected); why != "" {
		return fmt.Sprintf("[rejected] %s: %s", p, why), outFailed
	}
	if strings.HasPrefix(p, trashDir+"/") && !plan.Permanent {
		return fmt.Sprintf("[rejected] %s: already in Trash; use permanent=true to delete for good", p), outFailed
	}

	info, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		msg := "not found"
		if strings.ContainsAny(p, "*?[") {
			msg += " (wildcards are not expanded; list explicit paths)"
		}
		return fmt.Sprintf("[skipped] %s: %s", p, msg), outSkipped
	}
	if err != nil {
		return fmt.Sprintf("[failed] %s: %s", p, failReason(err)), outFailed
	}

	isDir := info.IsDir()
	if isDir && !plan.Recursive {
		empty, err := dirEmpty(p)
		if err != nil {
			return fmt.Sprintf("[failed] %s: %s", p, failReason(err)), outFailed
		}
		if !empty {
			return fmt.Sprintf("[rejected] %s: non-empty directory; set recursive=true", p), outFailed
		}
	}

	if plan.Permanent {
		if isDir && plan.Recursive {
			err = os.RemoveAll(p)
		} else {
			err = os.Remove(p)
		}
		if err != nil {
			return fmt.Sprintf("[failed] %s: %s", p, failReason(err)), outFailed
		}

		if filepath.Dir(p) == filepath.Join(trashDir, "files") {
			_ = os.Remove(filepath.Join(trashDir, "info", filepath.Base(p)+".trashinfo"))
		}
		return fmt.Sprintf("[ok] %s (deleted permanently)", p), outDone
	}

	name, err := moveToTrash(trashDir, p)
	if err != nil {
		return fmt.Sprintf("[failed] %s: %s", p, failReason(err)), outFailed
	}
	return fmt.Sprintf("[ok] %s -> Trash (%s)", p, name), outDone
}

func failReason(err error) string {
	msg := err.Error()
	if errors.Is(err, fs.ErrPermission) && os.Geteuid() != 0 {
		msg += " (retry with sudo=true)"
	}
	return msg
}

func dirEmpty(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}
