package host

import (
	"encoding/json"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/mcp/proto"
)

func convertResult(res *sdk.CallToolResult) *proto.CallResult {
	out := &proto.CallResult{IsError: res.IsError}
	for _, c := range res.Content {
		switch v := c.(type) {
		case *sdk.TextContent:
			out.Parts = append(out.Parts, proto.Part{Kind: "text", Text: v.Text})
		case *sdk.ImageContent:
			out.Parts = append(out.Parts, proto.Part{Kind: "image", MIME: v.MIMEType, Data: v.Data})
		case *sdk.AudioContent:
			out.Parts = append(out.Parts, proto.Part{Kind: "audio", MIME: v.MIMEType, Data: v.Data})
		case *sdk.ResourceLink:
			out.Parts = append(out.Parts, proto.Part{Kind: "link", URI: v.URI, Text: v.Name, MIME: v.MIMEType})
		case *sdk.EmbeddedResource:
			if r := v.Resource; r != nil {
				out.Parts = append(out.Parts, proto.Part{Kind: "resource", URI: r.URI, MIME: r.MIMEType, Text: r.Text, Data: r.Blob})
			}
		default:
			if b, err := json.Marshal(c); err == nil {
				out.Parts = append(out.Parts, proto.Part{Kind: "text", Text: string(b)})
			}
		}
	}
	if res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			out.Structured = b
		}
	}
	return out
}
