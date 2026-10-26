package server

import (
	"log"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/auth"
)

func NewHandler(srv *mcp.Server, token string, lg *log.Logger) (http.Handler, error) {
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{

			Stateless:    true,
			JSONResponse: true,

			DisableLocalhostProtection: true,
		},
	)
	h, err := auth.WithToken(token, mcpHandler)
	if err != nil {
		return nil, err
	}
	if lg == nil {
		lg = log.Default()
	}
	return logRequests(h, lg), nil
}

func logRequests(next http.Handler, lg *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		via, ip := "local", r.RemoteAddr
		if r.Header.Get("Cf-Ray") != "" {
			via, ip = "cf", r.Header.Get("Cf-Connecting-Ip")
		}
		lg.Printf("http %s status=%d %s via=%s ip=%s", r.Method, rec.status, time.Since(start).Round(time.Millisecond), via, ip)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
