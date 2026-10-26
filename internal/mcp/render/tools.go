package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"terminal-mcp/internal/mcp/proto"
)

func SummarizeParams(schema json.RawMessage) string {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil || len(s.Properties) == 0 {
		return ""
	}
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if req[names[i]] != req[names[j]] {
			return req[names[i]]
		}
		return names[i] < names[j]
	})
	for i, n := range names {
		if req[n] {
			names[i] = n + "*"
		}
	}
	return strings.Join(names, ", ")
}

func FirstLine(s string, max int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = string(r[:max-1]) + "…"
	}
	return s
}

func Tools(tools []proto.ToolInfo) string {
	var b strings.Builder
	for _, t := range tools {
		fmt.Fprintf(&b, "- %s(%s)", t.Name, SummarizeParams(t.Schema))
		if d := FirstLine(t.Description, 110); d != "" {
			b.WriteString(": " + d)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func ToolDetail(t proto.ToolInfo) string {
	var b strings.Builder
	b.WriteString(t.Name + "\n")
	if d := strings.TrimSpace(t.Description); d != "" {
		b.WriteString("\n" + d + "\n")
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, t.Schema, "", "  ") == nil {
		b.WriteString("\ninputSchema:\n" + pretty.String() + "\n")
	}
	return b.String()
}
