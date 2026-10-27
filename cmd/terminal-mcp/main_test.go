package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tmcpbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "terminal-mcp")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type sandbox struct {
	home, root, data string
	env              []string
}

func newSandbox(t *testing.T, extra ...string) *sandbox {
	t.Helper()
	home := t.TempDir()
	sb := &sandbox{home: home, root: filepath.Join(home, "root"), data: filepath.Join(home, "data")}
	if err := os.MkdirAll(sb.root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "TERMINAL_MCP_") || strings.HasPrefix(k, "TMCP_") || k == "HOME" {
			continue
		}
		sb.env = append(sb.env, kv)
	}
	sb.env = append(sb.env,
		"HOME="+home,
		"TMCP_DIR="+sb.data,
		"TMCP_MCPS="+filepath.Join(home, "mcps.json"),
		"TERMINAL_MCP_ROOT="+sb.root,
		"TERMINAL_MCP_SHELL=/bin/sh",
	)
	sb.env = append(sb.env, extra...)
	return sb
}

func (sb *sandbox) exec(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Env = sb.env
	cmd.Stdin = strings.NewReader(stdin)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	code = 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("exec %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return code, o.String(), e.String()
}

const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestVersionHelpAndUnknownCommand(t *testing.T) {
	sb := newSandbox(t)
	if code, out, _ := sb.exec(t, "", "version"); code != 0 || out != "terminal-mcp 0.3.0-lite\n" {
		t.Errorf("version: code=%d out=%q", code, out)
	}
	code, out, _ := sb.exec(t, "")
	if code != 0 || !strings.Contains(out, "Usage: terminal-mcp <command>") {
		t.Errorf("no arguments: code=%d out=%q", code, out)
	}
	code, _, errOut := sb.exec(t, "", "bogus")
	if code != 2 || !strings.Contains(errOut, `unknown command: "bogus"`) {
		t.Errorf("unknown command: code=%d err=%q", code, errOut)
	}

	code, out, _ = sb.exec(t, "", "mcp", "ls")
	if code != 0 || !strings.Contains(out, "(empty; add with: mcp add") {
		t.Errorf("mcp ls: code=%d out=%q", code, out)
	}
}

func TestFileOpHiddenSubcommand(t *testing.T) {
	sb := newSandbox(t)
	file := filepath.Join(sb.root, "from-fileop.txt")
	req := fmt.Sprintf(`{"op":"write","max_output":1000,"args":{"path":%q,"content":"txt"}}`, file)
	code, out, errOut := sb.exec(t, req, "fileop")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	var resp struct{ Text, Error string }
	if err := json.Unmarshal([]byte(out), &resp); err != nil || resp.Error != "" || resp.Text != "wrote 3 bytes to "+file {
		t.Errorf("response = %q (%v)", out, err)
	}
	if b, _ := os.ReadFile(file); string(b) != "txt" {
		t.Errorf("file content = %q", b)
	}

	if code, _, errOut := sb.exec(t, "not json", "fileop"); code != 1 || !strings.Contains(errOut, "Error: bad request") {
		t.Errorf("malformed request: code=%d err=%q", code, errOut)
	}
}

func TestMCPHostHiddenSubcommandFailsCleanly(t *testing.T) {
	sb := newSandbox(t)
	code, _, errOut := sb.exec(t, "", "mcphost", "missing")
	if code != 1 || !strings.Contains(errOut, `Error: "missing" is not in`) {
		t.Errorf("code=%d err=%q", code, errOut)
	}
}

func TestServeRejectsBadSettings(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
		want  string
	}{
		{"no token", nil, "TERMINAL_MCP_TOKEN is not set"},
		{"short token", []string{"TERMINAL_MCP_TOKEN=short"}, "token must be at least 32 characters"},
		{"port not an integer", []string{"TERMINAL_MCP_TOKEN=" + token, "TERMINAL_MCP_PORT=abc"}, "TERMINAL_MCP_PORT must be an integer"},
		{"root not a directory", []string{"TERMINAL_MCP_TOKEN=" + token, "TERMINAL_MCP_ROOT=/does/not/exist"}, "TERMINAL_MCP_ROOT is not a directory"},
	}
	for _, c := range cases {
		sb := newSandbox(t, c.extra...)
		code, _, errOut := sb.exec(t, "", "serve")
		if code != 1 || !strings.Contains(errOut, "Error: ") || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: code=%d err=%q, want containing %q", c.name, code, errOut, c.want)
		}
		if strings.Contains(errOut, token) {
			t.Errorf("%s: stderr leaks the token", c.name)
		}
	}
}

type httpServer struct {
	sb     *sandbox
	cmd    *exec.Cmd
	exited chan error
	stderr *bytes.Buffer
	port   int
}

var probeClient = &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

func (s *httpServer) url(path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", s.port, path)
}

func (s *httpServer) initialize(path string) int {
	const body = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	req, _ := http.NewRequest(http.MethodPost, s.url(path), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := probeClient.Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

func startServer(t *testing.T) *httpServer {
	t.Helper()
	port := freePort(t)
	sb := newSandbox(t, "TERMINAL_MCP_TOKEN="+token, "TERMINAL_MCP_HOST=127.0.0.1", fmt.Sprintf("TERMINAL_MCP_PORT=%d", port))
	s := &httpServer{sb: sb, port: port, exited: make(chan error, 1), stderr: &bytes.Buffer{}}
	s.cmd = exec.Command(binPath, "serve")
	s.cmd.Env = sb.env
	s.cmd.Stderr = s.stderr
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { s.exited <- s.cmd.Wait() }()
	t.Cleanup(func() { _ = s.cmd.Process.Kill() })

	deadline := time.Now().Add(15 * time.Second)
	for s.initialize("/"+token+"/mcp") != http.StatusOK {
		select {
		case err := <-s.exited:
			t.Fatalf("server exited before ready: %v\n%s", err, s.stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready within 15 seconds\n%s", s.stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	return s
}

func (s *httpServer) stop(t *testing.T) (time.Duration, error) {
	t.Helper()
	start := time.Now()
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-s.exited:
		return time.Since(start), err
	case <-time.After(20 * time.Second):
		t.Fatalf("server did not stop within 20 seconds after SIGTERM\n%s", s.stderr.String())
		return 0, nil
	}
}

func TestServeHTTPLifecycle(t *testing.T) {
	s := startServer(t)

	for _, path := range []string{"/" + strings.Repeat("0", 64) + "/mcp", "/mcp", "/"} {
		if got := s.initialize(path); got != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, got)
		}
	}

	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             s.url("/" + token + "/mcp"),
		HTTPClient:           &http.Client{Transport: &http.Transport{DisableKeepAlives: true}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "run", Arguments: map[string]any{"command": "echo through-binary"}})
	if err != nil || res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "through-binary") {
		t.Errorf("CallTool = %+v, %v", res, err)
	}
	_ = cs.Close()

	took, err := s.stop(t)
	if err != nil || took > 3*time.Second {
		t.Errorf("shutdown: err=%v took=%s, want nil and < 3s\n%s", err, took, s.stderr.String())
	}
	out := s.stderr.String()
	for _, want := range []string{fmt.Sprintf("listening on http://127.0.0.1:%d/<TOKEN>/mcp", s.port), "shutdown: signal received", "shutdown: server stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, out)
		}
	}

	logFile, err := os.ReadFile(filepath.Join(s.sb.data, "terminal_mcp.log"))
	if err != nil {
		t.Fatalf("server log not written: %v", err)
	}
	for name, content := range map[string]string{"stderr": out, "server log": string(logFile)} {
		if strings.Contains(content, token) {
			t.Errorf("%s leaks the token", name)
		}
	}
	if !strings.Contains(string(logFile), "listening on") {
		t.Errorf("server log does not contain the start event:\n%s", logFile)
	}
}

func TestServeShutdownIsInstantWithIdleKeepAliveConnection(t *testing.T) {
	s := startServer(t)
	c := &http.Client{}
	resp, err := c.Post(s.url("/"+strings.Repeat("0", 64)+"/mcp"), "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = new(bytes.Buffer).ReadFrom(resp.Body)
	resp.Body.Close()

	took, err := s.stop(t)
	if err != nil || took > 3*time.Second || !strings.Contains(s.stderr.String(), "shutdown: server stopped") {
		t.Errorf("err=%v took=%s\n%s", err, took, s.stderr.String())
	}
}

func TestServeShutdownWaitsAtMostFiveSecondsForUnusedConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("takes about 5 seconds")
	}
	s := startServer(t)
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	took, err := s.stop(t)
	if err != nil {
		t.Errorf("exit must be clean (0) even with a hanging connection: %v", err)
	}
	if took < 4*time.Second || took > 10*time.Second {
		t.Errorf("shutdown took %s, want about 5s (shutdownTimeout)", took)
	}
	if out := s.stderr.String(); !strings.Contains(out, "shutdown exceeded 5s, remaining requests are forcibly stopped") {
		t.Errorf("timeout message missing:\n%s", out)
	}
}

func (sb *sandbox) stdioClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	cmd := exec.Command(binPath, "--stdio")
	cmd.Env = sb.env
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (text string, isErr bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestStdioMode(t *testing.T) {
	sb := newSandbox(t)
	cs := sb.stdioClient(t)

	ir := cs.InitializeResult()
	if ir.ServerInfo.Name != "terminal-mcp" || ir.ServerInfo.Version != "0.3.0-lite" {
		t.Errorf("ServerInfo = %+v", ir.ServerInfo)
	}
	if want := filepath.Join(sb.data, "notes"); !strings.Contains(ir.Instructions, want) {
		t.Errorf("Instructions do not point to the notes folder %q:\n%s", want, ir.Instructions)
	}

	list, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Annotations != nil {
			t.Errorf("tool %s has Annotations", tool.Name)
		}
	}
	if got := strings.Join(sortedStrings(names), ","); got != "checkpoint,delete,edit,read,run,write" {
		t.Errorf("tool = %s", got)
	}

	if out, bad := callTool(t, cs, "run", map[string]any{"command": "echo hello; pwd"}); bad || !strings.Contains(out, "hello") || !strings.Contains(out, sb.root) {
		t.Errorf("run = %q bad=%v (cwd must be root %s)", out, bad, sb.root)
	}
	if out, bad := callTool(t, cs, "write", map[string]any{"path": "n/a.txt", "content": "txt"}); bad || !strings.HasPrefix(out, "wrote 3 bytes to ") {
		t.Errorf("write = %q bad=%v", out, bad)
	}
	if out, bad := callTool(t, cs, "read", map[string]any{"path": "n/a.txt"}); bad || out != "txt" {
		t.Errorf("read = %q bad=%v", out, bad)
	}
	if out, bad := callTool(t, cs, "read", map[string]any{"path": "missing"}); !bad || !strings.Contains(out, "no such file") {
		t.Errorf("read failed = %q bad=%v", out, bad)
	}
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func requireSudo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Skip("sudo not available")
	}
	cmd := exec.Command("sudo", "-n", "--", binPath, "fileop")
	cmd.Stdin = strings.NewReader(`{"op":"bogus"}`)
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "unknown op") {
		t.Skipf("sudo -n is not available for this binary (needs NOPASSWD): %v", err)
	}
}

func ownerUID(t *testing.T, path string) uint32 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Uid
}

func TestSudoToolsThroughRealBinary(t *testing.T) {
	requireSudo(t)
	sb := newSandbox(t)
	cs := sb.stdioClient(t)
	file := filepath.Join(sb.root, "root-owned.txt")

	t.Cleanup(func() { _ = exec.Command("sudo", "-n", "rm", "-f", file).Run() })

	if out, bad := callTool(t, cs, "write", map[string]any{"path": file, "content": "data", "sudo": true}); bad || out != "wrote 4 bytes to "+file {
		t.Fatalf("write sudo = %q bad=%v", out, bad)
	}
	if uid := ownerUID(t, file); uid != 0 {
		t.Errorf("file created by uid %d, want 0 (root): sudo was not actually used", uid)
	}
	if out, bad := callTool(t, cs, "read", map[string]any{"path": file, "sudo": true}); bad || out != "data" {
		t.Errorf("read sudo = %q bad=%v", out, bad)
	}

	before, _ := os.Stat(file)
	if out, bad := callTool(t, cs, "edit", map[string]any{"path": file, "old": "data", "new": "edit", "sudo": true}); bad || !strings.HasPrefix(out, "replaced 1 occurrence(s) in ") {
		t.Fatalf("edit sudo = %q bad=%v", out, bad)
	}
	after, _ := os.Stat(file)
	if string(mustRead(t, file)) != "edit" || ownerUID(t, file) != 0 || after.Mode() != before.Mode() {
		t.Errorf("edit sudo must preserve owner and permissions: content=%q uid=%d mode %v -> %v", mustRead(t, file), ownerUID(t, file), before.Mode(), after.Mode())
	}

	out, bad := callTool(t, cs, "delete", map[string]any{"paths": []string{file}, "permanent": true, "sudo": true})
	if bad || !strings.HasPrefix(out, "Deleted permanently: 1 ok") {
		t.Errorf("delete sudo = %q bad=%v", out, bad)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("file still exists after sudo delete")
	}
}

func TestSudoDeleteStillRefusesProtectedPaths(t *testing.T) {
	requireSudo(t)
	sb := newSandbox(t)
	cs := sb.stdioClient(t)

	out, bad := callTool(t, cs, "delete", map[string]any{"paths": []string{sb.root, sb.home}, "recursive": true, "permanent": true, "sudo": true})
	if !bad || strings.Count(out, "[rejected]") != 2 {
		t.Errorf("delete sudo on protected paths = %q bad=%v", out, bad)
	}
	if _, err := os.Stat(sb.root); err != nil {
		t.Errorf("root deleted via sudo: %v", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
