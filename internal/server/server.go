package server

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/tools"
)

type Options struct {
	Middleware []mcp.Middleware
}

func New(s config.Settings, opts Options) *mcp.Server {
	srv := mcp.NewServer(
		&mcp.Implementation{Name: "terminal-mcp", Version: config.Version},
		&mcp.ServerOptions{Instructions: instructions(s.NotesDir)},
	)
	for _, m := range opts.Middleware {
		if m != nil {
			srv.AddReceivingMiddleware(m)
		}
	}

	if m := compaction(s); m != nil {
		srv.AddReceivingMiddleware(m)
	}
	tools.Register(srv, s)
	return srv
}

func instructions(notesDir string) string {
	return fmt.Sprintf("Notes about this machine are in %s. At the start of a session, list it (run: ls) and read the relevant files, "+
		"beginning with 00-read-first.md if present. Treat the notes as more reliable than default assumptions. "+
		"Never delete or overwrite notes unless the user asks. If the folder is missing or empty, ignore this.", notesDir)
}
