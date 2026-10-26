package host

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/mcp/registry"
)

func TestHeaderTransportAddsHeadersWithoutMutatingRequest(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Clone() }))
	defer srv.Close()

	hc := &http.Client{Transport: &headerTransport{
		headers: map[string]string{"Authorization": "Bearer x", "X-Key": "y"},
		base:    http.DefaultTransport,
	}}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if got.Get("Authorization") != "Bearer x" || got.Get("X-Key") != "y" {
		t.Errorf("header di server = %v", got)
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("the original request was modified by the transport")
	}
}

func TestNewTransportHTTP(t *testing.T) {
	tr, cmd := newTransport(registry.Entry{URL: "http://127.0.0.1:1/mcp"}, io.Discard)
	st, ok := tr.(*sdk.StreamableClientTransport)
	if !ok || cmd != nil {
		t.Fatalf("transport = %T, cmd = %v", tr, cmd)
	}
	if st.Endpoint != "http://127.0.0.1:1/mcp" || !st.DisableStandaloneSSE {
		t.Errorf("transport = %+v", st)
	}
}

func TestNewTransportStdio(t *testing.T) {
	t.Setenv("TERMINAL_MCP_TOKEN", "secret-token")
	var sink strings.Builder
	tr, cmd := newTransport(registry.Entry{Command: "npx", Args: []string{"-y", "x"}, Cwd: "/tmp", Env: map[string]string{"MINE": "1"}}, &sink)
	if _, ok := tr.(*sdk.CommandTransport); !ok || cmd == nil {
		t.Fatalf("transport = %T, cmd = %v", tr, cmd)
	}
	if cmd.Dir != "/tmp" || len(cmd.Args) != 3 || cmd.Args[2] != "x" {
		t.Errorf("cmd = dir %q args %v", cmd.Dir, cmd.Args)
	}
	if cmd.Stderr != &sink {
		t.Error("child stderr was not redirected to the host log")
	}

	if sa := cmd.SysProcAttr; sa == nil || !sa.Setpgid || sa.Pdeathsig != syscall.SIGTERM {
		t.Errorf("SysProcAttr = %+v", sa)
	}
	if env := strings.Join(cmd.Env, "\n"); strings.Contains(env, "secret-token") || !strings.Contains(env, "MINE=1") {
		t.Errorf("wrong child env:\n%s", env)
	}
}

func TestKillGroup(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()

	killGroup(cmd.Process.Pid)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("process is still alive after killGroup")
	}

	done := make(chan struct{})
	go func() { killGroup(cmd.Process.Pid); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("killGroup hangs on an empty group")
	}
}
