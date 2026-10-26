package result

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTextRoundTrip(t *testing.T) {
	if got := String(Text("hello\nworld")); got != "hello\nworld" {
		t.Errorf("String(Text(x)) = %q", got)
	}
	if r := Text("x"); r.IsError || len(r.Content) != 1 {
		t.Errorf("Text produced an unexpected result: %+v", r)
	}
}

func TestString(t *testing.T) {
	cases := []struct {
		name string
		in   *mcp.CallToolResult
		want string
	}{
		{"nil", nil, ""},
		{"empty", &mcp.CallToolResult{}, ""},
		{"two texts joined", &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "a"}, &mcp.TextContent{Text: "b"},
		}}, "a\nb"},
		{"non-text is given a type marker", &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "a"}, &mcp.ImageContent{},
		}}, "a\n[*mcp.ImageContent]"},
	}
	for _, c := range cases {
		if got := String(c.in); got != c.want {
			t.Errorf("%s: String = %q, want %q", c.name, got, c.want)
		}
	}
}
