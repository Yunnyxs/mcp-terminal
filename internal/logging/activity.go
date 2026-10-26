package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/result"
)

type entry struct {
	Time    string          `json:"time"`
	Tool    string          `json:"tool"`
	Args    json.RawMessage `json:"args,omitempty"`
	Ms      int64           `json:"duration_ms"`
	IsError bool            `json:"is_error"`
	Result  string          `json:"result,omitempty"`
	Err     string          `json:"error,omitempty"`
}

func Activity(w io.Writer, errLog *log.Logger) mcp.Middleware {
	if errLog == nil {
		errLog = log.Default()
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, ok := req.(*mcp.CallToolRequest)
			if method != "tools/call" || !ok || call.Params == nil {
				return next(ctx, method, req)
			}
			start := time.Now()
			res, err := next(ctx, method, req)

			e := entry{
				Time: start.Format("2006-01-02T15:04:05.000Z07:00"),
				Tool: call.Params.Name,
				Args: call.Params.Arguments,
				Ms:   time.Since(start).Milliseconds(),
			}
			if err != nil {
				e.IsError, e.Err = true, err.Error()
			}
			if r, ok := res.(*mcp.CallToolResult); ok && r != nil {
				e.IsError = e.IsError || r.IsError
				e.Result = result.String(r)
			}
			writeEntry(w, e, errLog)
			return res, err
		}
	}
}

func writeEntry(w io.Writer, e entry, errLog *log.Logger) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		errLog.Printf("activity log: encode failed: %v", err)
		return
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		errLog.Printf("activity log: write failed: %v", err)
	}
}
