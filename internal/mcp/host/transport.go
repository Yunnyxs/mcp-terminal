package host

import (
	"io"
	"net/http"
	"os/exec"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/mcp/registry"
)

func newTransport(e registry.Entry, stderr io.Writer) (t sdk.Transport, cmd *exec.Cmd) {
	if e.URL != "" {
		return &sdk.StreamableClientTransport{
			Endpoint:             e.URL,
			HTTPClient:           &http.Client{Transport: &headerTransport{headers: e.Headers, base: http.DefaultTransport}},
			DisableStandaloneSSE: true,
		}, nil
	}
	cmd = exec.Command(e.Command, e.Args...)
	cmd.Env = e.ChildEnv()
	cmd.Dir = e.Cwd
	cmd.Stderr = stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	return &sdk.CommandTransport{Command: cmd, TerminateDuration: childTermWait}, cmd
}

type headerTransport struct {
	headers map[string]string
	base    http.RoundTripper
}

func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.headers {
		r.Header.Set(k, v)
	}
	return t.base.RoundTrip(r)
}
