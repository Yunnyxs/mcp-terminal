package tools

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/result"
)

type readInput struct {
	Path   string `json:"path" jsonschema:"file path, relative to root"`
	Offset int64  `json:"offset,omitempty" jsonschema:"start byte. Default: 0"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max bytes. Default and max: server setting"`
	Sudo   bool   `json:"sudo,omitempty" jsonschema:"run as root (sudo -n)"`
}

func (in readInput) wantSudo() bool { return in.Sudo }

var readTool = fileTool[readInput, readInput]{
	name:    "read",
	desc:    "Read a UTF-8 text file by byte range. Output ends with next_offset when truncated.",
	timeout: sudoTimeout,
	prepare: func(s config.Settings, in readInput) (readInput, error) {
		p, err := resolve(s.Root, in.Path)
		in.Path, in.Sudo = p, false
		return in, err
	},
	exec: readFile,
}

func readFile(s config.Settings, in readInput) (*mcp.CallToolResult, error) {
	if in.Offset < 0 || in.Limit < 0 {
		return nil, errors.New("offset and limit must be >= 0")
	}
	p, err := resolve(s.Root, in.Path)
	if err != nil {
		return nil, err
	}

	limit := max(utf8.UTFMax, s.MaxOutput)
	if in.Limit > 0 && in.Limit < limit {
		limit = max(utf8.UTFMax, in.Limit)
	}

	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", p)
	}
	if _, err := f.Seek(in.Offset, io.SeekStart); err != nil {
		return nil, err
	}

	buf := make([]byte, limit+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	more := n > limit
	chunk := buf[:min(n, limit)]
	if more {
		chunk = trimPartialRune(chunk)
	}
	if bytes.IndexByte(chunk, 0) >= 0 {
		return nil, fmt.Errorf("binary file: %s", p)
	}
	if len(chunk) == 0 {
		return result.Text("(empty)"), nil
	}
	out := strings.ToValidUTF8(string(chunk), "\uFFFD")
	if more {
		out += fmt.Sprintf("\n[next_offset: %d, file size: %d]", in.Offset+int64(len(chunk)), info.Size())
	}
	return result.Text(out), nil
}

func trimPartialRune(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			return b
		}
	}
	return b
}
