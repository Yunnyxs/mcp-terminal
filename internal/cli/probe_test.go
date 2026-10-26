package cli

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"terminal-mcp/internal/config"
)

const probeToken = "0123456789abcdef0123456789abcdef"

func settingsFor(t *testing.T, ts *httptest.Server, token string) config.Settings {
	t.Helper()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)
	return config.Settings{Host: host, Port: port, Token: token}
}

func TestProbeAddr(t *testing.T) {
	cases := []struct {
		host string
		port int
		want string
	}{
		{"127.0.0.1", 8000, "127.0.0.1:8000"},
		{"", 8000, "127.0.0.1:8000"},
		{"0.0.0.0", 9001, "127.0.0.1:9001"},
		{"::", 9001, "127.0.0.1:9001"},
		{"::1", 80, "[::1]:80"},
		{"localhost", 1, "localhost:1"},
	}
	for _, c := range cases {
		if got := probeAddr(config.Settings{Host: c.host, Port: c.port}); got != c.want {
			t.Errorf("probeAddr(%q,%d) = %q, want %q", c.host, c.port, got, c.want)
		}
	}
}

func TestProbeSendsInitializeToTokenPath(t *testing.T) {
	var gotPath, gotBody, gotAccept string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAccept = r.URL.Path, r.Header.Get("Accept")
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	d, err := probe(settingsFor(t, ts, probeToken))
	if err != nil || d < 0 {
		t.Fatalf("probe = %v, %v", d, err)
	}
	if gotPath != "/"+probeToken+"/mcp" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"method":"initialize"`) || !strings.Contains(gotAccept, "application/json") {
		t.Errorf("body=%q accept=%q", gotBody, gotAccept)
	}
}

func TestProbeFailures(t *testing.T) {
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	if _, err := probe(settingsFor(t, notFound, probeToken)); err == nil || err.Error() != "HTTP 404" {
		t.Errorf("404: err = %v", err)
	}
	if _, err := probe(config.Settings{Host: "127.0.0.1", Port: 1}); err == nil || !strings.Contains(err.Error(), "TERMINAL_MCP_TOKEN is empty") {
		t.Errorf("without token: err = %v", err)
	}
}

func TestProbeConnectionErrorNeverLeaksToken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	_, err = probe(config.Settings{Host: "127.0.0.1", Port: port, Token: probeToken})
	if err == nil {
		t.Fatal("must fail")
	}
	if strings.Contains(err.Error(), probeToken) || strings.Contains(err.Error(), "/mcp") {
		t.Errorf("error leaks URL/token: %v", err)
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("cause not visible: %v", err)
	}
}

func TestWaitReady(t *testing.T) {

	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer ts.Close()
	if _, err := waitReady(settingsFor(t, ts, probeToken), 10*time.Second); err != nil || calls != 3 {
		t.Errorf("waitReady err=%v calls=%d, want nil and 3", err, calls)
	}

	down := httptest.NewServer(http.NotFoundHandler())
	defer down.Close()
	_, err := waitReady(settingsFor(t, down, probeToken), 600*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not respond") || !strings.Contains(err.Error(), "terminal-mcp logs") || strings.Contains(err.Error(), probeToken) {
		t.Errorf("err = %v", err)
	}
}
