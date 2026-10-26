package main

import (
	"context"
	"encoding/base64"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Text string `json:"text" jsonschema:"text to return"`
}
type sleepIn struct {
	MS int `json:"ms" jsonschema:"sleep duration in milliseconds"`
}
type bigIn struct {
	N int `json:"n" jsonschema:"number of bytes"`
}
type envIn struct {
	Name string `json:"name" jsonschema:"name of the environment variable"`
}
type none struct{}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func main() {
	srv := mcp.NewServer(&mcp.Implementation{Name: "fakemcp", Version: "1.0.0"}, nil)

	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "Return the given text.\nSecond line of description."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
			return text("echo: " + in.Text), nil, nil
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
	mcp.AddTool(srv, &mcp.Tool{Name: "big", Description: "Return n bytes of text (multi-byte runes included)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in bigIn) (*mcp.CallToolResult, any, error) {
			return text(strings.Repeat("é", in.N/2)), nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "env", Description: "Return the value of an environment variable of this process."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in envIn) (*mcp.CallToolResult, any, error) {
			v, ok := os.LookupEnv(in.Name)
			if !ok {
				return text("<unset>"), nil, nil
			}
			return text(v), nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "image", Description: "Return a tiny PNG image."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
			png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: png, MIMEType: "image/png"}}}, nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "fail", Description: "Always report a tool error."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
			r := text("this tool always fails")
			r.IsError = true
			return r, nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "crash", Description: "Exit the whole server shortly after replying."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
			go func() { time.Sleep(200 * time.Millisecond); os.Exit(3) }()
			return text("bye"), nil, nil
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "spawn", Description: "Start a long-lived grandchild process and return its pid."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
			c := exec.Command("sleep", "300")
			if err := c.Start(); err != nil {
				return nil, nil, err
			}
			return text(strconv.Itoa(c.Process.Pid)), nil, nil
		})

	log.Println("fakemcp: ready (this stderr must end up in the host log)")
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
