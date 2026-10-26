package mcptest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Text string `json:"text" jsonschema:"text to return"`
}
type bigIn struct {
	N int `json:"n" jsonschema:"number of bytes"`
}
type sleepIn struct {
	MS int `json:"ms" jsonschema:"sleep duration in milliseconds"`
}
type none struct{}

type Server struct {
	*httptest.Server

	mu   sync.Mutex
	auth string
}

func (s *Server) LastAuth() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.auth
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func NewHTTP(t testing.TB) *Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "fakehttp", Version: "1.0.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "Return the given text.\nSecond line of description."},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
			return text("echo: " + in.Text), nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "fail", Description: "Always report a tool error."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
			r := text("this tool always fails")
			r.IsError = true
			return r, nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "big", Description: "Return n bytes of text (multi-byte runes included)."},
		func(_ context.Context, _ *mcp.CallToolRequest, in bigIn) (*mcp.CallToolResult, any, error) {
			return text(strings.Repeat("é", in.N/2)), nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "sleep", Description: "Sleep for ms milliseconds."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sleepIn) (*mcp.CallToolResult, any, error) {
			select {
			case <-time.After(time.Duration(in.MS) * time.Millisecond):
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
			return text("slept"), nil, nil
		})

	s := &Server{}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auth = r.Header.Get("Authorization")
		s.mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}
