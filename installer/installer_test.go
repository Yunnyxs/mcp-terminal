package installer

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"terminal-mcp/internal/auth"
	"terminal-mcp/internal/config"
)

var realTools = []string{
	"bash", "env", "go", "mkdir", "chmod", "ln", "mv", "cp", "cat", "sed", "grep", "head", "tail", "od", "tr",
	"seq", "mktemp", "install", "id", "touch", "uname", "rm", "dirname", "basename", "tee", "date", "sort", "wc", "readlink",
}

var stubs = map[string]string{
	"systemctl": `echo "systemctl $*" >> "$STUB_LOG"
case "$*" in
  *show-environment*) [ "${STUB_NO_SYSTEMD:-0}" = 1 ] && exit 1 ;;
  *is-active*) [ "${STUB_SERVICE_ACTIVE:-0}" = 1 ] && exit 0 || exit 3 ;;
esac
exit 0`,
	"loginctl": `echo "loginctl $*" >> "$STUB_LOG"; exit 0`,
	"journalctl": `echo "journalctl $*" >> "$STUB_LOG"
[ -n "${STUB_TUNNEL_URL:-}" ] && echo "INF |  $STUB_TUNNEL_URL  |"
exit 0`,

	"curl": `echo "curl $*" >> "$STUB_LOG"
out=""; wfmt=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -w) wfmt="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -n "$wfmt" ]; then printf '%s' "${STUB_HTTP_CODE:-200}"; exit 0; fi
[ "${STUB_CURL_FAIL:-0}" = 1 ] && exit 22
printf '#!/bin/sh\necho fake-cloudflared\n' > "$out"
exit 0`,

	"sudo": `echo "sudo $*" >> "$STUB_LOG"
if [ "$1" = visudo ]; then cp "$3" "$STUB_DIR/sudoers.copy"; fi
exit 0`,
	"sleep": `exit 0`,
}

type sandbox struct {
	t     *testing.T
	root  string
	home  string
	tools string
	bash  string
	log   string
	env   map[string]string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}

	if k := os.Getenv("TMCP_KIT"); k != "" {
		root = k
	}
	sb := &sandbox{t: t, root: root, home: t.TempDir(), tools: t.TempDir(), env: map[string]string{}}
	if err := os.MkdirAll(filepath.Join(sb.home, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range realTools {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("tool %q is not available: %v", name, err)
		}
		if name == "bash" {
			sb.bash = p
		}
		if err := os.Symlink(p, filepath.Join(sb.tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range stubs {
		sb.writeTool(name, body)
	}
	sb.log = filepath.Join(sb.tools, "calls.log")
	return sb
}

func (sb *sandbox) writeTool(name, body string) {
	sb.t.Helper()
	path := filepath.Join(sb.tools, name)
	_ = os.Remove(path)
	if err := os.WriteFile(path, []byte("#!"+sb.bash+"\n"+body+"\n"), 0o755); err != nil {
		sb.t.Fatal(err)
	}
}

func (sb *sandbox) removeTool(name string) {
	sb.t.Helper()
	if err := os.Remove(filepath.Join(sb.tools, name)); err != nil {
		sb.t.Fatal(err)
	}
}

func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Skipf("go env %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

func (sb *sandbox) environ() []string {
	cur, _ := user.Current()
	m := map[string]string{
		"PATH": sb.tools, "HOME": sb.home, "USER": cur.Username, "LOGNAME": cur.Username,
		"TMPDIR": filepath.Join(sb.home, "tmp"), "LANG": "C",
		"STUB_LOG": sb.log, "STUB_DIR": sb.tools,
		"STUB_TUNNEL_URL": "https://fake-test.trycloudflare.com",

		"GOROOT": goEnv(sb.t, "GOROOT"), "GOMODCACHE": goEnv(sb.t, "GOMODCACHE"), "GOCACHE": goEnv(sb.t, "GOCACHE"),
		"GOPATH": goEnv(sb.t, "GOPATH"), "GOPROXY": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-buildvcs=false", "CGO_ENABLED": "0",
	}
	for k, v := range sb.env {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	var out []string
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func (sb *sandbox) run(args ...string) (int, string) {
	sb.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, sb.bash, append([]string{filepath.Join(sb.root, "install.sh")}, args...)...)
	cmd.Env = sb.environ()
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			sb.t.Fatalf("install.sh: %v", err)
		}
		code = ee.ExitCode()
	}
	return code, buf.String()
}

func (sb *sandbox) path(parts ...string) string {
	return filepath.Join(append([]string{sb.home}, parts...)...)
}

func (sb *sandbox) configEnv() string { return sb.path(".config", "terminal-mcp", "config.env") }

func (sb *sandbox) read(path string) string {
	sb.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		sb.t.Fatal(err)
	}
	return string(b)
}

func (sb *sandbox) calls() []string {
	b, _ := os.ReadFile(sb.log)
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (sb *sandbox) called(line string) bool {
	for _, c := range sb.calls() {
		if c == line {
			return true
		}
	}
	return false
}

func (sb *sandbox) mustInstall(args ...string) string {
	sb.t.Helper()
	code, out := sb.run(args...)
	if code != 0 {
		sb.t.Fatalf("install.sh failed (exit %d):\n%s", code, out)
	}
	return out
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return fi.Mode().Perm()
}

func setting(content, key string) string {
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=(.*)$`).FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestScriptsParseAndFollowConventions(t *testing.T) {
	sb := newSandbox(t)
	if m := mode(t, filepath.Join(sb.root, "install.sh")); m&0o100 == 0 {
		t.Errorf("install.sh is not executable (%o), although the instructions say ./install.sh", m)
	}
	files, err := filepath.Glob(filepath.Join(sb.root, "installer", "*.sh"))
	if err != nil || len(files) == 0 {
		t.Fatalf("installer script not found: %v", err)
	}
	for _, f := range append(files, filepath.Join(sb.root, "install.sh")) {
		if out, err := exec.Command(sb.bash, "-n", f).CombinedOutput(); err != nil {
			t.Errorf("bash -n %s: %v\n%s", f, err, out)
		}
	}

	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".sh")
		if !regexp.MustCompile(`(?m)^` + name + `\(\) `).MatchString(sb.read(f)) {
			t.Errorf("%s does not define function %s()", filepath.Base(f), name)
		}
	}
}

func TestShellcheckWhenAvailable(t *testing.T) {
	sc, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skip("shellcheck is not installed")
	}
	root, _ := filepath.Abs("..")
	files, _ := filepath.Glob(filepath.Join(root, "installer", "*.sh"))
	files = append(files, filepath.Join(root, "install.sh"))
	if out, err := exec.Command(sc, append([]string{"-x", "-s", "bash"}, files...)...).CombinedOutput(); err != nil {
		t.Errorf("shellcheck:\n%s", out)
	}
}

func TestFreshInstall(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	out := sb.mustInstall()

	bin := sb.path(".local", "bin", "terminal-mcp")
	if mode(t, bin)&0o111 == 0 {
		t.Errorf("%s is not executable", bin)
	}
	if v, err := exec.Command(bin, "version").Output(); err != nil || string(v) != "terminal-mcp "+config.Version+"\n" {
		t.Errorf("built binary: version = %q, %v", v, err)
	}
	if dest, err := os.Readlink(sb.path(".local", "bin", "tmcp")); err != nil || dest != "terminal-mcp" {
		t.Errorf("alias tmcp -> %q, %v", dest, err)
	}

	cfg := sb.read(sb.configEnv())
	if m := mode(t, sb.configEnv()); m != 0o600 {
		t.Errorf("mode of config.env = %o, want 600 (it holds the token)", m)
	}
	token := setting(cfg, "TERMINAL_MCP_TOKEN")
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(token) || auth.ValidateToken(token) != nil {
		t.Errorf("token = %q", token)
	}
	port, err := strconv.Atoi(setting(cfg, "TERMINAL_MCP_PORT"))
	if err != nil || port < 1 || port > 65535 {
		t.Errorf("port = %q", setting(cfg, "TERMINAL_MCP_PORT"))
	}
	if setting(cfg, "TERMINAL_MCP_HOST") != "127.0.0.1" {
		t.Errorf("host = %q, want 127.0.0.1", setting(cfg, "TERMINAL_MCP_HOST"))
	}

	for _, d := range []string{".mcp-terminal", ".mcp-terminal/Trash", ".mcp-terminal/notes"} {
		if m := mode(t, sb.path(d)); m != 0o700 {
			t.Errorf("mode of %s = %o, want 700", d, m)
		}
	}

	srvUnit := sb.read(sb.path(".config", "systemd", "user", "terminal-mcp.service"))
	if srvUnit != sb.read(filepath.Join(sb.root, "units", "terminal-mcp.service")) {
		t.Error("the server unit differs from units/terminal-mcp.service")
	}
	if !strings.Contains(srvUnit, "ExecStart=%h/.local/bin/terminal-mcp serve\n") {
		t.Errorf("the server unit must use 'serve' (without arguments it only shows help):\n%s", srvUnit)
	}
	tunnel := sb.read(sb.path(".config", "systemd", "user", "terminal-mcp-tunnel.service"))
	cf := filepath.Join(sb.tools, "cloudflared")
	if !strings.Contains(tunnel, "ExecStart="+cf+" tunnel --no-autoupdate --protocol http2 --url http://127.0.0.1:"+strconv.Itoa(port)+"\n") {
		t.Errorf("wrong tunnel ExecStart:\n%s", tunnel)
	}

	if !strings.Contains(tunnel, "Wants=terminal-mcp.service") || strings.Contains(tunnel, "Requires=") {
		t.Errorf("the tunnel unit must use Wants=, not Requires=:\n%s", tunnel)
	}

	var sysctl []string
	for _, c := range sb.calls() {
		if strings.HasPrefix(c, "systemctl") && !strings.Contains(c, "is-active") && !strings.Contains(c, "show-environment") {
			sysctl = append(sysctl, c)
		}
	}
	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable terminal-mcp.service terminal-mcp-tunnel.service",
		"systemctl --user restart terminal-mcp.service",
		"systemctl --user restart terminal-mcp-tunnel.service",
	}
	if strings.Join(sysctl, "|") != strings.Join(want, "|") {
		t.Errorf("systemctl commands:\n got %q\nwant %q", sysctl, want)
	}
	if !sb.called("loginctl enable-linger "+os.Getenv("USER")) && !hasPrefix(sb.calls(), "loginctl enable-linger ") {
		t.Errorf("linger was not enabled: %q", sb.calls())
	}

	if hasPrefix(sb.calls(), "sudo ") {
		t.Errorf("sudo was called without being asked: %q", sb.calls())
	}
	if _, err := os.Stat(sb.configEnv() + ".tmp"); err == nil {
		t.Error("config.env.tmp left over")
	}

	if !strings.Contains(out, "https://fake-test.trycloudflare.com/"+token+"/mcp") {
		t.Errorf("connector URL was not printed:\n%s", out)
	}
	for _, w := range []string{"skipped (sudo still requires a password)", "Anyone who has this URL", "tmcp status | url | restart | logs | rotate | help"} {
		if !strings.Contains(out, w) {
			t.Errorf("output does not contain %q", w)
		}
	}
}

func hasPrefix(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func TestInstallerAgreesWithGoConfig(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	sb.mustInstall()

	t.Setenv("HOME", sb.home)
	t.Setenv(config.EnvDir, "")
	t.Setenv(config.EnvMCPs, "")
	if want, err := config.EnvFilePath(); err != nil || want != sb.configEnv() {
		t.Errorf("config.EnvFilePath() = %q (%v), the installer wrote to %q", want, err, sb.configEnv())
	}
	s, err := config.FromEnvFile()
	if err != nil {
		t.Fatalf("config.FromEnvFile failed to read the installer output: %v", err)
	}
	if s.Token != setting(sb.read(sb.configEnv()), "TERMINAL_MCP_TOKEN") || s.Host != "127.0.0.1" {
		t.Errorf("Settings = %+v", s)
	}
	if s.Paths.Dir != sb.path(".mcp-terminal") || s.TrashDir != sb.path(".mcp-terminal", "Trash") || s.NotesDir != sb.path(".mcp-terminal", "notes") {
		t.Errorf("data locations in Go (%s, %s, %s) differ from the directories created by the installer", s.Paths.Dir, s.TrashDir, s.NotesDir)
	}
	for _, d := range []string{s.Paths.Dir, s.TrashDir, s.NotesDir} {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("directory %s was not created by the installer", d)
		}
	}
}

func TestRerunKeepsTokenPortAndExtraConfig(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	sb.mustInstall()
	first := sb.read(sb.configEnv())

	extra := "# my notes\nTERMINAL_MCP_TIMEOUT=77\nTERMINAL_MCP_ROOT=/tmp\n"
	if err := os.WriteFile(sb.configEnv(), []byte(first+extra), 0o600); err != nil {
		t.Fatal(err)
	}

	sb.env["STUB_SERVICE_ACTIVE"] = "1"
	out := sb.mustInstall()

	second := sb.read(sb.configEnv())
	for _, k := range []string{"TERMINAL_MCP_TOKEN", "TERMINAL_MCP_PORT", "TERMINAL_MCP_HOST"} {
		if setting(first, k) != setting(second, k) {
			t.Errorf("%s changed after reinstall: %q -> %q", k, setting(first, k), setting(second, k))
		}
	}
	for _, want := range []string{"# my notes", "TERMINAL_MCP_TIMEOUT=77", "TERMINAL_MCP_ROOT=/tmp"} {
		if !strings.Contains(second, want) {
			t.Errorf("config.env lost %q:\n%s", want, second)
		}
	}
	if !strings.Contains(out, "existing token kept (not replaced)") {
		t.Errorf("output does not say the existing token was kept:\n%s", out)
	}
	if m := mode(t, sb.configEnv()); m != 0o600 {
		t.Errorf("mode of config.env = %o, want 600", m)
	}
}

func TestYesRootInstallsValidatedSudoers(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	out := sb.mustInstall("--yes-root")

	calls := sb.calls()
	var visudo, install string
	for _, c := range calls {
		switch {
		case strings.HasPrefix(c, "sudo visudo -cf "):
			visudo = c
		case strings.HasPrefix(c, "sudo install "):
			install = c
		}
	}
	if visudo == "" || install == "" {
		t.Fatalf("sudo visudo/install was not called: %q", calls)
	}

	if strings.Index(strings.Join(calls, "\n"), visudo) > strings.Index(strings.Join(calls, "\n"), install) {
		t.Error("sudoers was installed before visudo validated it")
	}
	if !strings.Contains(install, "-m 440 -o root -g root ") || !strings.HasSuffix(install, " /etc/sudoers.d/terminal-mcp") {
		t.Errorf("sudoers install command = %q", install)
	}
	who, _ := exec.Command("id", "-un").Output()
	if got := sb.read(filepath.Join(sb.tools, "sudoers.copy")); got != strings.TrimSpace(string(who))+" ALL=(ALL) NOPASSWD: ALL\n" {
		t.Errorf("sudoers content = %q", got)
	}
	if !strings.Contains(out, "Anyone who has the connector URL = root on this machine") {
		t.Errorf("root warning was not printed:\n%s", out)
	}

	if entries, _ := os.ReadDir(sb.path("tmp")); len(entries) != 0 {
		t.Errorf("temporary files left in TMPDIR: %v", entries)
	}
}

func TestInvalidSudoersAbortsWithoutInstalling(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	sb.writeTool("sudo", `echo "sudo $*" >> "$STUB_LOG"; [ "$1" = visudo ] && exit 1; exit 0`)
	code, out := sb.run("--yes-root")
	if code != 1 || !strings.Contains(out, "sudoers validation failed") {
		t.Errorf("exit=%d, output:\n%s", code, out)
	}
	if hasPrefix(sb.calls(), "sudo install ") {
		t.Error("sudoers was installed even though validation failed")
	}
}

func busyPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func seedConfig(t *testing.T, sb *sandbox, port int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sb.configEnv()), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "TERMINAL_MCP_TOKEN=" + strings.Repeat("ab", 32) + "\nTERMINAL_MCP_HOST=127.0.0.1\nTERMINAL_MCP_PORT=" + strconv.Itoa(port) + "\n"
	if err := os.WriteFile(sb.configEnv(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBusyPortFromConfigIsReplaced(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	busy := busyPort(t)
	seedConfig(t, sb, busy)

	out := sb.mustInstall()
	got := setting(sb.read(sb.configEnv()), "TERMINAL_MCP_PORT")
	if got == strconv.Itoa(busy) || got == "" {
		t.Errorf("the port in use (%d) was not replaced: %q", busy, got)
	}
	if !strings.Contains(out, "port chosen: "+got) {
		t.Errorf("output does not mention the new port %s:\n%s", got, out)
	}

	if tunnel := sb.read(sb.path(".config", "systemd", "user", "terminal-mcp-tunnel.service")); !strings.Contains(tunnel, "http://127.0.0.1:"+got+"\n") {
		t.Errorf("the tunnel unit does not use port %s:\n%s", got, tunnel)
	}
	if setting(sb.read(sb.configEnv()), "TERMINAL_MCP_TOKEN") != strings.Repeat("ab", 32) {
		t.Error("the existing token changed when the port was replaced")
	}
}

func TestActiveServiceKeepsItsPort(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	busy := busyPort(t)
	seedConfig(t, sb, busy)
	sb.env["STUB_SERVICE_ACTIVE"] = "1"

	sb.mustInstall()
	if got := setting(sb.read(sb.configEnv()), "TERMINAL_MCP_PORT"); got != strconv.Itoa(busy) {
		t.Errorf("port = %s, want it kept at %d (our own service is using it)", got, busy)
	}
}

func TestCloudflaredDownloadedWhenMissing(t *testing.T) {
	sb := newSandbox(t)
	out := sb.mustInstall()

	cf := sb.path(".local", "bin", "cloudflared")
	if mode(t, cf)&0o111 == 0 {
		t.Errorf("%s is not executable", cf)
	}
	if !strings.Contains(sb.read(sb.path(".config", "systemd", "user", "terminal-mcp-tunnel.service")), "ExecStart="+cf+" tunnel") {
		t.Error("the tunnel unit does not use the downloaded cloudflared")
	}
	var dl string
	for _, c := range sb.calls() {
		if strings.HasPrefix(c, "curl -fsSL") {
			dl = c
		}
	}
	if !regexp.MustCompile(`https://github\.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-(amd64|arm64)$`).MatchString(dl) {
		t.Errorf("download command = %q", dl)
	}
	if !strings.Contains(out, "downloading cloudflared") {
		t.Errorf("output does not mention the download:\n%s", out)
	}
	if _, err := os.Stat(cf + ".tmp"); err == nil {
		t.Error("cloudflared.tmp left over")
	}

	if err := os.Truncate(sb.log, 0); err != nil {
		t.Fatal(err)
	}
	sb.mustInstall()
	if hasPrefix(sb.calls(), "curl -fsSL") {
		t.Error("cloudflared was downloaded again although it already exists in ~/.local/bin")
	}
}

func TestCloudflaredFailures(t *testing.T) {
	sb := newSandbox(t)
	sb.env["STUB_CURL_FAIL"] = "1"
	if code, out := sb.run(); code != 1 || !strings.Contains(out, "Failed to download cloudflared") {
		t.Errorf("download failed: exit=%d\n%s", code, out)
	}
	if _, err := os.Stat(sb.path(".local", "bin", "cloudflared")); err == nil {
		t.Error("partial cloudflared file left behind after the download failed")
	}

	sb2 := newSandbox(t)
	sb2.writeTool("uname", `echo riscv64`)
	if code, out := sb2.run(); code != 1 || !strings.Contains(out, "Architecture riscv64 is not supported automatically") {
		t.Errorf("unsupported architecture: exit=%d\n%s", code, out)
	}
}

func TestPrerequisites(t *testing.T) {
	cases := []struct {
		name  string
		setup func(sb *sandbox)
		want  string
	}{
		{"run as root", func(sb *sandbox) { sb.writeTool("id", `echo 0`) }, "Do not run as root"},
		{"go missing", func(sb *sandbox) { sb.removeTool("go") }, "Go is not installed"},
		{"curl missing", func(sb *sandbox) { sb.removeTool("curl") }, "curl is not installed"},
		{"systemd user session down", func(sb *sandbox) { sb.env["STUB_NO_SYSTEMD"] = "1" }, "The systemd user session is not active"},
	}
	for _, c := range cases {
		sb := newSandbox(t)
		c.setup(sb)
		code, out := sb.run()
		if code != 1 || !strings.Contains(out, c.want) {
			t.Errorf("%s: exit=%d, want it to contain %q:\n%s", c.name, code, c.want, out)
		}

		if _, err := os.Stat(sb.path(".local")); err == nil {
			t.Errorf("%s: ~/.local was created although the prerequisites failed", c.name)
		}
		if _, err := os.Stat(sb.path(".mcp-terminal")); err == nil {
			t.Errorf("%s: data directory was created although the prerequisites failed", c.name)
		}
	}
}

func TestServerNotReadyAborts(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	sb.env["STUB_HTTP_CODE"] = "500"
	code, out := sb.run()
	if code != 1 || !strings.Contains(out, "the server is not responding") || !strings.Contains(out, "journalctl --user -u terminal-mcp") {
		t.Errorf("exit=%d:\n%s", code, out)
	}
}

func TestMissingTunnelURLIsWarningNotFailure(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	sb.env["STUB_TUNNEL_URL"] = ""
	code, out := sb.run()
	if code != 0 || !strings.Contains(out, "The tunnel has not provided a URL yet") || strings.Contains(out, "Connector URL for claude.ai") {
		t.Errorf("exit=%d:\n%s", code, out)
	}
}

func TestReadinessProbeUsesConfiguredPortAndToken(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	sb.mustInstall()
	cfg := sb.read(sb.configEnv())
	want := "http://127.0.0.1:" + setting(cfg, "TERMINAL_MCP_PORT") + "/" + setting(cfg, "TERMINAL_MCP_TOKEN") + "/mcp"
	found := false
	for _, c := range sb.calls() {
		if strings.HasPrefix(c, "curl ") && strings.HasSuffix(c, " "+want) {
			found = true
		}
	}
	if !found {
		t.Errorf("no probe to %s:\n%s", want, strings.Join(sb.calls(), "\n"))
	}
}

func TestBashrcPathLineAddedOnTopAndOnlyOnce(t *testing.T) {
	sb := newSandbox(t)
	sb.writeTool("cloudflared", "echo fake")
	rc := sb.path(".bashrc")
	original := "# my own\ncase $- in *i*) ;; *) return ;; esac\nalias ll='ls -l'\n"
	if err := os.WriteFile(rc, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	sb.mustInstall()
	got := sb.read(rc)

	if !strings.HasPrefix(got, "# terminal-mcp:") || !strings.Contains(got, "export PATH=\"$HOME/.local/bin:$PATH\"\n\n"+original) {
		t.Errorf(".bashrc:\n%s", got)
	}
	sb.mustInstall()

	if again := sb.read(rc); again != got || strings.Count(again, `export PATH="$HOME/.local/bin:$PATH"`) != 1 {
		t.Errorf("reinstall changed or duplicated the PATH lines:\n%s", again)
	}

	sb2 := newSandbox(t)
	sb2.writeTool("cloudflared", "echo fake")
	mine := "export PATH=$PATH:$HOME/.local/bin\n"
	if err := os.WriteFile(sb2.path(".bashrc"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	sb2.mustInstall()
	if got := sb2.read(sb2.path(".bashrc")); got != mine {
		t.Errorf(".bashrc that already contains .local/bin was modified:\n%s", got)
	}
}
