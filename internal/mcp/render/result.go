package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/proto"
)

func Result(p config.Paths, name, tool string, r *proto.CallResult, maxOut int) (string, error) {
	stamp := time.Now().Format("20060102-150405")
	var lines []string
	for i, part := range r.Parts {
		switch part.Kind {
		case "text":
			lines = append(lines, part.Text)
		case "link":
			lines = append(lines, fmt.Sprintf("[link: %s %s]", part.URI, strings.TrimSpace(part.Text)))
		case "resource":
			if part.Text != "" {
				lines = append(lines, fmt.Sprintf("[resource: %s]\n%s", part.URI, part.Text))
				continue
			}
			fallthrough
		default:
			if len(part.Data) == 0 {
				lines = append(lines, fmt.Sprintf("[%s empty]", part.Kind))
				continue
			}
			path, err := saveOut(p, fmt.Sprintf("%s-%s-%s-%d%s", name, safeTool(tool), stamp, i+1, extFor(part.MIME)), part.Data)
			if err != nil {
				return "", err
			}
			lines = append(lines, fmt.Sprintf("[%s saved: %s (%s, %d bytes)]", part.Kind, path, part.MIME, len(part.Data)))
		}
	}
	if len(r.Parts) == 0 && len(r.Structured) > 0 {
		var pretty bytes.Buffer
		if json.Indent(&pretty, r.Structured, "", "  ") == nil {
			lines = append(lines, pretty.String())
		} else {
			lines = append(lines, string(r.Structured))
		}
	}
	text := strings.Join(lines, "\n")
	if len(text) <= maxOut {
		return text, nil
	}
	path, err := saveOut(p, fmt.Sprintf("%s-%s-%s.txt", name, safeTool(tool), stamp), []byte(text))
	if err != nil {
		return "", err
	}
	cut := maxOut
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n[output truncated: %d of %d bytes shown; full result: %s]", text[:cut], cut, len(text), path), nil
}
