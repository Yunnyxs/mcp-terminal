package host

import (
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/mcp/proto"
)

func TestConvertResult(t *testing.T) {
	res := &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{
			&sdk.TextContent{Text: "text"},
			&sdk.ImageContent{Data: []byte{1}, MIMEType: "image/png"},
			&sdk.AudioContent{Data: []byte{2}, MIMEType: "audio/wav"},
			&sdk.ResourceLink{URI: "file:///a", Name: "a", MIMEType: "text/plain"},
			&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///b", MIMEType: "text/plain", Text: "content"}},
			&sdk.EmbeddedResource{},
		},
		StructuredContent: map[string]any{"a": 1},
	}
	got := convertResult(res)
	if !got.IsError {
		t.Error("IsError is missing")
	}
	want := []proto.Part{
		{Kind: "text", Text: "text"},
		{Kind: "image", MIME: "image/png", Data: []byte{1}},
		{Kind: "audio", MIME: "audio/wav", Data: []byte{2}},
		{Kind: "link", URI: "file:///a", Text: "a", MIME: "text/plain"},
		{Kind: "resource", URI: "file:///b", MIME: "text/plain", Text: "content"},
	}
	if len(got.Parts) != len(want) {
		t.Fatalf("Parts = %+v, want %d parts", got.Parts, len(want))
	}
	for i, w := range want {
		g := got.Parts[i]
		if g.Kind != w.Kind || g.Text != w.Text || g.MIME != w.MIME || g.URI != w.URI || string(g.Data) != string(w.Data) {
			t.Errorf("Parts[%d] = %+v, want %+v", i, g, w)
		}
	}
	if string(got.Structured) != `{"a":1}` {
		t.Errorf("Structured = %s", got.Structured)
	}
}

func TestConvertResultEmpty(t *testing.T) {
	got := convertResult(&sdk.CallToolResult{})
	if got.IsError || len(got.Parts) != 0 || got.Structured != nil {
		t.Errorf("empty result = %+v", got)
	}
}
