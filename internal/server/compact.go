package server

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/compact"
	"terminal-mcp/internal/config"
	"terminal-mcp/internal/rawstore"
)

func compaction(s config.Settings) mcp.Middleware {
	if !s.Compact {
		return nil
	}
	budget := s.LLMOutput
	if budget <= 0 {
		budget = config.DefaultLLMOutput
	}
	store := rawstore.New(s.Paths.OutDir)
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if err != nil || method != "tools/call" {
				return res, err
			}
			p, ok := req.GetParams().(*mcp.CallToolParamsRaw)
			if !ok || p.Name != "run" {
				return res, nil
			}
			args := parseRunArgs(p.Arguments)
			if args.Raw {
				return res, nil
			}
			if cr, ok := res.(*mcp.CallToolResult); ok {
				if short := compactRun(cr, args.Command, budget, store); short != nil {
					return short, nil
				}
			}
			return res, nil
		}
	}
}

type runArgs struct {
	Command string `json:"command"`
	Raw     bool   `json:"raw"`
}

func parseRunArgs(args json.RawMessage) runArgs {
	var a runArgs
	_ = json.Unmarshal(args, &a)
	return a
}

func compactRun(cr *mcp.CallToolResult, command string, budget int, store *rawstore.Store) *mcp.CallToolResult {
	if len(cr.Content) != 1 {
		return nil
	}
	tc, ok := cr.Content[0].(*mcp.TextContent)
	if !ok {
		return nil
	}
	head, body, _ := strings.Cut(tc.Text, "\n")
	status, failed, ok := parseHeader(head)
	if !ok {
		return nil
	}
	if body == "(no output)" {
		body = ""
	}
	short, lossy := compact.Body(body, compact.Options{Budget: budget, Failed: failed, Command: command})
	text := compact.Join(status, short)
	if lossy {

		if len(text)+noteEstimate >= len(tc.Text) {
			return nil
		}
		text += "\n" + rawNote(store, tc.Text)
	}
	out := *cr
	out.Content = []mcp.Content{&mcp.TextContent{Text: text}}
	return &out
}

func parseHeader(head string) (status string, failed, ok bool) {
	if strings.HasPrefix(head, "timeout after ") {
		return head, true, true
	}
	rest, found := strings.CutPrefix(head, "exit ")
	if !found || !strings.HasSuffix(rest, ")") {
		return "", false, false
	}
	i := strings.LastIndex(rest, " (")
	if i < 0 {
		return "", false, false
	}
	code := rest[:i]
	dur, err := time.ParseDuration(rest[i+2 : len(rest)-1])
	if err != nil {
		return "", false, false
	}
	return compact.Status(code, dur), code != "0", true
}

const noteEstimate = 100

var captureCut = regexp.MustCompile(`\n\.\.\.\[\d+ bytes truncated\]\.\.\.\n`)

func rawNote(store *rawstore.Store, stored string) string {
	path, err := store.Save([]byte(stored))
	if err != nil {
		return "[summarized; rerun with raw=true for unsummarized output]"
	}
	if captureCut.MatchString(stored) {
		return "[summarized; stored output (already cut at the capture limit): " + path + "]"
	}
	return "[summarized; stored output: " + path + "]"
}
