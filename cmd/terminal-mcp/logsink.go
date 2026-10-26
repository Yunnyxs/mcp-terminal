package main

import (
	"io"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/logging"
)

func openLog(path string, maxBytes int64) (io.Writer, error) {
	return logging.Open(path, maxBytes)
}

func activityMiddleware(path string, maxBytes int64) (mcp.Middleware, error) {
	w, err := logging.Open(path, maxBytes)
	if err != nil {
		return nil, err
	}
	return logging.Activity(w, log.Default()), nil
}
