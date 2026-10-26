package host

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/client"
	"terminal-mcp/internal/mcp/mcptest"
	"terminal-mcp/internal/mcp/proto"
	"terminal-mcp/internal/mcp/registry"
)

func testPaths(t *testing.T, cfg registry.Config) config.Paths {
	t.Helper()
	dir, err := os.MkdirTemp("", "tm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p := config.Paths{Dir: dir, RunDir: dir + "/run", LogDir: dir + "/mcp", OutDir: dir + "/mcp/out", MCPs: dir + "/mcps.json"}
	if err := registry.Save(p.MCPs, cfg); err != nil {
		t.Fatal(err)
	}
	return p
}

func openFileLog(path string, _ int64) (io.Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", what)
}

func start(t *testing.T, p config.Paths, name string) (cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)

	go func() { ch <- Run(ctx, name, p, Options{OpenLog: openFileLog}); close(ch) }()
	t.Cleanup(func() { cancel(); <-ch })
	c := client.New(p)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-ch:
			t.Fatalf("host stopped before it was ready: %v", err)
		default:
		}
		if _, err := c.Ping(name); err == nil {
			return cancel, ch
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("host was not ready within 15 seconds")
	return nil, nil
}

func finished(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("host did not stop within 15 seconds")
		return nil
	}
}

func TestRunHTTPEndToEnd(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	p := testPaths(t, registry.Config{"web": {URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer secret"}}})
	cancel, done := start(t, p, "web")
	c := client.New(p)

	info, err := c.Ping("web")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "web" || info.Kind != "http" || info.Server != "fakehttp" || info.PID != os.Getpid() || info.Tools != 4 || info.Active != 0 {
		t.Errorf("Info = %+v", info)
	}

	list, err := c.Tools("web", "")
	if err != nil || len(list) != 4 {
		t.Fatalf("Tools = %+v, %v", list, err)
	}
	one, err := c.Tools("web", "echo")
	if err != nil || len(one) != 1 || one[0].Name != "echo" || !strings.Contains(string(one[0].Schema), `"text"`) {
		t.Errorf("Tools(echo) = %+v, %v", one, err)
	}
	if _, err := c.Tools("web", "absent"); err == nil || !strings.Contains(err.Error(), `tool "absent" is not in MCP "web"`) {
		t.Errorf("unknown tool: err = %v", err)
	}

	res, err := c.Call("web", "echo", json.RawMessage(`{"text":"hi"}`), 10*time.Second)
	if err != nil || res.IsError || len(res.Parts) != 1 || res.Parts[0].Text != "echo: hi" {
		t.Errorf("Call echo = %+v, %v", res, err)
	}
	if got := srv.LastAuth(); got != "Bearer secret" {
		t.Errorf("Authorization header on the server = %q, want %q", got, "Bearer secret")
	}
	res, err = c.Call("web", "fail", nil, 10*time.Second)
	if err != nil || !res.IsError || res.Parts[0].Text != "this tool always fails" {
		t.Errorf("Call fail = %+v, %v", res, err)
	}

	cancel()
	if err := finished(t, done); err != nil {
		t.Errorf("Run after cancel: %v", err)
	}
	if _, err := c.Ping("web"); err == nil {
		t.Error("host still answers after stopping")
	}
	for _, f := range []string{"run/web.sock", "run/web.pid"} {
		if _, err := os.Stat(filepath.Join(p.Dir, f)); err == nil {
			t.Errorf("%s was not cleaned up", f)
		}
	}
	if log, _ := os.ReadFile(proto.LogFile(p, "web")); !strings.Contains(string(log), "ready: fakehttp 1.0.0 (http), 4 tools") || !strings.Contains(string(log), "done") {
		t.Errorf("host log is incomplete:\n%s", log)
	}
}

func TestStopViaSocket(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	p := testPaths(t, registry.Config{"web": {URL: srv.URL}})
	_, done := start(t, p, "web")
	if err := client.New(p).Stop("web"); err != nil {
		t.Fatal(err)
	}
	if err := finished(t, done); err != nil {
		t.Errorf("Run after stop: %v", err)
	}
	if log, _ := os.ReadFile(proto.LogFile(p, "web")); !strings.Contains(string(log), "unload requested") {
		t.Errorf("the stop reason was not recorded:\n%s", log)
	}
}

func TestIdleTimeoutStopsHost(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	p := testPaths(t, registry.Config{"web": {URL: srv.URL, IdleTimeout: "1s"}})
	begin := time.Now()
	_, done := start(t, p, "web")
	if err := finished(t, done); err != nil {
		t.Errorf("Run: %v", err)
	}
	if took := time.Since(begin); took < time.Second {
		t.Errorf("host stopped too quickly: %s", took)
	}
	if log, _ := os.ReadFile(proto.LogFile(p, "web")); !strings.Contains(string(log), "idle exceeded 1s") {
		t.Errorf("the idle reason was not recorded:\n%s", log)
	}
}

func TestSecondHostForSameNameRefused(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	p := testPaths(t, registry.Config{"web": {URL: srv.URL}})
	start(t, p, "web")

	err := Run(context.Background(), "web", p, Options{})
	if err == nil || !strings.Contains(err.Error(), `host "web" is already running or starting`) {
		t.Errorf("err = %v", err)
	}
	if _, err := client.New(p).Ping("web"); err != nil {
		t.Errorf("the first host was disturbed by the second attempt: %v", err)
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	p := testPaths(t, registry.Config{"web": {URL: "http://127.0.0.1:1"}})
	cases := []struct{ name, want string }{
		{"Invalid", "MCP name"},
		{"absent", `"absent" is not in`},
	}
	for _, c := range cases {
		if err := Run(context.Background(), c.name, p, Options{}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err = %v, want it to contain %q", c.name, err, c.want)
		}
	}
	bad := testPaths(t, registry.Config{})
	if err := os.WriteFile(bad.MCPs, []byte(`{"x":{"command":"a","idle_timeout":"old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), "x", bad, Options{}); err == nil || !strings.Contains(err.Error(), "idle_timeout") {
		t.Errorf("wrong idle: err = %v", err)
	}
}

func TestConnectFailureIsLogged(t *testing.T) {
	p := testPaths(t, registry.Config{"dead": {URL: "http://127.0.0.1:1"}})
	err := Run(context.Background(), "dead", p, Options{OpenLog: openFileLog})
	if err == nil || !strings.Contains(err.Error(), "failed to connect to MCP") {
		t.Fatalf("err = %v", err)
	}
	if log, _ := os.ReadFile(proto.LogFile(p, "dead")); !strings.Contains(string(log), "Error: failed to connect to MCP") {
		t.Errorf("the error was not recorded in the host log:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(p.RunDir, "dead.sock")); err == nil {
		t.Error("socket left behind after a failed start")
	}

	if err := Run(context.Background(), "dead", p, Options{}); err == nil || strings.Contains(err.Error(), "already running") {
		t.Errorf("lock was not released: %v", err)
	}
}

func TestOpenLogFailureAborts(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	p := testPaths(t, registry.Config{"web": {URL: srv.URL}})
	opts := Options{OpenLog: func(string, int64) (io.Writer, error) { return nil, os.ErrPermission }}
	if err := Run(context.Background(), "web", p, opts); err == nil || !strings.Contains(err.Error(), "log host:") {
		t.Errorf("err = %v", err)
	}
}

func raw(t *testing.T, p config.Paths, name, payload string) proto.Response {
	t.Helper()
	sock, _ := proto.Socket(p, name)
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatal(err)
	}
	var resp proto.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("response not readable: %v", err)
	}
	return resp
}

func TestBadRequestsGetErrorResponses(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	p := testPaths(t, registry.Config{"web": {URL: srv.URL}})
	start(t, p, "web")

	cases := []struct{ name, payload, want string }{
		{"not JSON", "not json", "invalid request"},
		{"unknown op", `{"op":"bogus"}`, `unknown op: "bogus"`},
		{"empty tool", `{"op":"call"}`, "tool name is empty"},
		{"args is not an object", `{"op":"call","tool":"echo","args":[1]}`, "arguments must be a JSON object"},
	}
	for _, c := range cases {
		resp := raw(t, p, "web", c.payload)
		if resp.OK || !strings.Contains(resp.Error, c.want) {
			t.Errorf("%s: %+v, want an error containing %q", c.name, resp, c.want)
		}
	}

	if _, err := client.New(p).Ping("web"); err != nil {
		t.Errorf("host broke after a bad request: %v", err)
	}
}

func TestCallTimeout(t *testing.T) {
	srv := mcptest.NewHTTP(t)
	p := testPaths(t, registry.Config{"web": {URL: srv.URL}})
	start(t, p, "web")
	c := client.New(p)

	begin := time.Now()
	_, err := c.Call("web", "sleep", json.RawMessage(`{"ms":5000}`), time.Second)
	if err == nil || !strings.Contains(err.Error(), `call "sleep" exceeded the time limit of 1s`) {
		t.Errorf("err = %v", err)
	}
	if took := time.Since(begin); took > 4*time.Second {
		t.Errorf("timeout was not honored: %s", took)
	}
	if _, err := c.Ping("web"); err != nil {
		t.Errorf("host broke after a timeout: %v", err)
	}
}
