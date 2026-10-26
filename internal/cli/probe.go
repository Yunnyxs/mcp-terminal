package cli

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"terminal-mcp/internal/config"
)

func probeAddr(s config.Settings) string {
	host := s.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(s.Port))
}

func probe(s config.Settings) (time.Duration, error) {
	if s.Token == "" {
		return 0, errors.New("TERMINAL_MCP_TOKEN is empty in config.env")
	}
	const body = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"terminal-mcp-cli","version":"1"}}}`
	req, err := http.NewRequest(http.MethodPost, "http://"+probeAddr(s)+"/"+s.Token+"/mcp", strings.NewReader(body))
	if err != nil {
		return 0, errors.New("invalid request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	start := time.Now()
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {

		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return time.Since(start).Round(time.Millisecond), nil
}

func waitReady(s config.Settings, timeout time.Duration) (time.Duration, error) {
	deadline := time.Now().Add(timeout)
	for {
		d, err := probe(s)
		if err == nil {
			return d, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("server did not respond within %s (%v); check: terminal-mcp logs", timeout, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
