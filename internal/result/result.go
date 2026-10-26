package result

import (
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func Text(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func String(r *mcp.CallToolResult) string {
	if r == nil {
		return ""
	}
	parts := make([]string, 0, len(r.Content))
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		} else {
			parts = append(parts, fmt.Sprintf("[%T]", c))
		}
	}
	return strings.Join(parts, "\n")
}
