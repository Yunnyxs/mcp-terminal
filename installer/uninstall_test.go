package installer

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func uninstallSandbox(t *testing.T) *sandbox {
	t.Helper()
	sb := newSandbox(t)
	for _, name := range []string{"rmdir", "cut", "du"} {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("tool %q is not available: %v", name, err)
		}
		if err := os.Symlink(p, filepath.Join(sb.tools, name)); err != nil {
			t.Fatal(err)
		}
	}

	sb.env["UNINSTALL_CGROUP_FILE"] = filepath.Join(sb.home, "cgroup")
	if err := os.WriteFile(sb.env["UNINSTALL_CGROUP_FILE"], []byte("0::/user.slice/session-1.scope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return sb
}

func (sb *sandbox) runUninstall(args ...string) (int, string) {
	sb.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, sb.bash, append([]string{filepath.Join(sb.root, "uninstall.sh")}, args...)...)
	cmd.Env = sb.environ()
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			sb.t.Fatalf("uninstall.sh: %v", err)
		}
		code = ee.ExitCode()
	}
	return code, buf.String()
}

func (sb *sandbox) mustUninstall(args ...string) string {
	sb.t.Helper()
	code, out := sb.runUninstall(args...)
	if code != 0 {
		sb.t.Fatalf("uninstall.sh failed (exit %d):\n%s", code, out)
	}
	return out
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func snapshot(t *testing.T, home string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(home, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := rel + " " + info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			dest, _ := os.Readlink(p)
			line += " -> " + dest
		case info.Mode().IsRegular():
			b, _ := os.ReadFile(p)
			line += " " + string(b)
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

const userBashrc = "# user's own\nalias ll='ls -l'\n\nexport EDITOR=vim\n"

func (sb *sandbox) installFresh(args ...string) {
	sb.t.Helper()
	if err := os.WriteFile(sb.path(".bashrc"), []byte(userBashrc), 0o644); err != nil {
		sb.t.Fatal(err)
	}
	sb.mustInstall(args...)
	data := sb.path(".mcp-terminal")
	for path, content := range map[string]string{
		filepath.Join(data, "activity_terminal_mcp.log"): "{}\n",
		filepath.Join(data, "terminal_mcp.log"):          "log\n",
		filepath.Join(data, "notes", "00-read-first.md"): "user notes\n",
		filepath.Join(data, "Trash", "files", "old.txt"): "deleted by user\n",
		filepath.Join(data, "mcp", "out", "run-1.txt"):   "result\n",
		filepath.Join(data, "run", "demo.lock"):          "",
		sb.path(".config", "terminal-mcp", "mcps.json"):  "{}\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			sb.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			sb.t.Fatal(err)
		}
	}
}

func TestUninstallScriptParsesAndIsExecutable(t *testing.T) {
	sb := newSandbox(t)
	f := filepath.Join(sb.root, "uninstall.sh")
	if m := mode(t, f); m&0o100 == 0 {
		t.Errorf("uninstall.sh is not executable (%o)", m)
	}
	if out, err := exec.Command(sb.bash, "-n", f).CombinedOutput(); err != nil {
		t.Errorf("bash -n: %v\n%s", err, out)
	}
}

func TestUninstallRevertsInstall(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh("--yes-root")
	if !exists(sb.path(".local", "bin", "terminal-mcp")) || !exists(sb.configEnv()) {
		t.Fatal("precondition: the installer must have installed first")
	}

	sb.writeTool("systemctl", `echo "systemctl $*" >> "$STUB_LOG"
case "$*" in
  *show-environment*) exit 0 ;;
  *list-units*) echo "tmcp-mcp-demo.service loaded active running demo" ;;
esac
exit 0`)

	out := sb.mustUninstall("--yes")

	for _, p := range []string{
		sb.path(".config", "systemd", "user", "terminal-mcp.service"),
		sb.path(".config", "systemd", "user", "terminal-mcp-tunnel.service"),
		sb.path(".local", "bin", "terminal-mcp"),
		sb.path(".local", "bin", "tmcp"),
		sb.path(".config", "terminal-mcp"),
		sb.path(".mcp-terminal"),
	} {
		if exists(p) {
			t.Errorf("still present after uninstall: %s", p)
		}
	}

	if !exists(sb.path(".local", "bin", "cloudflared")) {
		t.Error("cloudflared was deleted although --remove-cloudflared was not given")
	}
	if got := sb.read(sb.path(".bashrc")); got != userBashrc {
		t.Errorf(".bashrc was not restored to its original state:\n%q\ninstead of\n%q", got, userBashrc)
	}
	for _, c := range []string{
		"systemctl --user disable --now terminal-mcp.service",
		"systemctl --user disable --now terminal-mcp-tunnel.service",
		"systemctl --user stop tmcp-mcp-demo.service",
		"systemctl --user daemon-reload",
		"sudo -n rm -f /etc/sudoers.d/terminal-mcp",
	} {
		if !sb.called(c) {
			t.Errorf("call missing: %q\nfound: %v", c, sb.calls())
		}
	}
	if !strings.Contains(out, "has been removed") || !strings.Contains(out, "linger") {
		t.Errorf("final summary is incomplete:\n%s", out)
	}
}

func TestUninstallRemoveCloudflaredLeavesNoBinDir(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	sb.mustUninstall("--yes", "--remove-cloudflared")
	if exists(sb.path(".local", "bin")) {
		t.Errorf("~/.local/bin should be gone (empty): %v", snapshot(t, sb.path(".local")))
	}
}

func TestUninstallKeepsUnrelatedFilesInBin(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	other := sb.path(".local", "bin", "user-owned")
	if err := os.WriteFile(other, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	sb.mustUninstall("--yes", "--remove-cloudflared")
	if !exists(other) {
		t.Error("the user's file in ~/.local/bin was deleted too")
	}
}

func TestUninstallDryRunChangesNothing(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh("--yes-root")
	before := snapshot(t, sb.home)
	n := len(sb.calls())

	out := sb.mustUninstall("--dry-run", "--remove-cloudflared")

	if after := snapshot(t, sb.home); after != before {
		t.Errorf("dry run changed HOME")
	}
	if !strings.Contains(out, "[dry-run]") || !strings.Contains(out, "nothing was changed") {
		t.Errorf("dry-run output is incomplete:\n%s", out)
	}
	for _, c := range sb.calls()[n:] {
		if strings.Contains(c, "disable") || strings.Contains(c, "daemon-reload") || strings.Contains(c, " rm ") {
			t.Errorf("dry run called commands that change the system: %q", c)
		}
	}
}

func TestUninstallNeedsYesWithoutTerminal(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	before := snapshot(t, sb.home)
	code, out := sb.runUninstall()
	if code == 0 || !strings.Contains(out, "--yes") {
		t.Errorf("must refuse without --yes (exit %d):\n%s", code, out)
	}
	if snapshot(t, sb.home) != before {
		t.Error("the rejection still changed HOME")
	}
}

func TestUninstallKeepNotesAndTrash(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	sb.mustUninstall("--yes", "--keep-notes", "--keep-trash")
	data := sb.path(".mcp-terminal")
	if sb.read(filepath.Join(data, "notes", "00-read-first.md")) != "user notes\n" {
		t.Error("notes changed")
	}
	if sb.read(filepath.Join(data, "Trash", "files", "old.txt")) != "deleted by user\n" {
		t.Error("Trash contents changed")
	}
	for _, gone := range []string{"activity_terminal_mcp.log", "terminal_mcp.log", "mcp", "run"} {
		if exists(filepath.Join(data, gone)) {
			t.Errorf("should have been deleted: %s", gone)
		}
	}
	if exists(sb.path(".config", "terminal-mcp")) || exists(sb.path(".local", "bin", "terminal-mcp")) {
		t.Error("configuration/binaries were not deleted")
	}
}

func TestUninstallKeepNotesOnly(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	sb.mustUninstall("--yes", "--keep-notes")
	data := sb.path(".mcp-terminal")
	if !exists(filepath.Join(data, "notes", "00-read-first.md")) {
		t.Error("notes were deleted")
	}
	if exists(filepath.Join(data, "Trash")) {
		t.Error("Trash should have been deleted")
	}
}

func TestUninstallLeavesBashrcAloneWithoutInstallerLines(t *testing.T) {
	sb := uninstallSandbox(t)
	content := "export PATH=\"$HOME/.local/bin:$PATH\"\nalias a=b"
	if err := os.WriteFile(sb.path(".bashrc"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sb.mustUninstall("--yes")
	if got := sb.read(sb.path(".bashrc")); got != content {
		t.Errorf("the user's .bashrc changed: %q", got)
	}
}

func TestUninstallBashrcKeepsPermissionsAndLinesAround(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	if err := os.Chmod(sb.path(".bashrc"), 0o640); err != nil {
		t.Fatal(err)
	}
	sb.mustUninstall("--yes")
	if m := mode(t, sb.path(".bashrc")); m != 0o640 {
		t.Errorf(".bashrc mode changed: %o", m)
	}
}

func TestUninstallLeavesForeignTmcpAlone(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	tmcp := sb.path(".local", "bin", "tmcp")
	_ = os.Remove(tmcp)
	if err := os.WriteFile(tmcp, []byte("not an alias\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := sb.mustUninstall("--yes")
	if !exists(tmcp) {
		t.Error("the user's tmcp was deleted")
	}
	if !strings.Contains(out, "not a symlink") {
		t.Errorf("no warning:\n%s", out)
	}
}

func TestUninstallReportsCustomPathsOutsideDataDir(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	custom := sb.path("my-junk")
	if err := os.MkdirAll(custom, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(sb.configEnv(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("TERMINAL_MCP_TRASH=" + custom + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	out := sb.mustUninstall("--yes")
	if !exists(custom) {
		t.Error("a directory outside ~/.mcp-terminal was deleted too")
	}
	if !strings.Contains(out, "NOT deleted") || !strings.Contains(out, custom) {
		t.Errorf("not reported:\n%s", out)
	}
}

func TestUninstallWarnsWhenNotesAndTrashWillBeDeleted(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	out := sb.mustUninstall("--dry-run")
	if !strings.Contains(out, "notes will be DELETED") || !strings.Contains(out, "Trash will be PERMANENTLY DELETED") {
		t.Errorf("the plan does not warn about notes/Trash:\n%s", out)
	}
}

func TestUninstallIsIdempotentOnCleanMachine(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	sb.mustUninstall("--yes", "--remove-cloudflared")
	out := sb.mustUninstall("--yes", "--remove-cloudflared")
	if !strings.Contains(out, "has been removed") {
		t.Errorf("a second uninstall must still succeed:\n%s", out)
	}
}

func TestUninstallWorksWithoutSystemd(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	sb.env["STUB_NO_SYSTEMD"] = "1"
	n := len(sb.calls())
	out := sb.mustUninstall("--yes")
	if !strings.Contains(out, "systemd user session not active") {
		t.Errorf("no systemd warning:\n%s", out)
	}
	if exists(sb.path(".local", "bin", "terminal-mcp")) || exists(sb.path(".mcp-terminal")) {
		t.Error("files were not deleted while systemd was down")
	}
	for _, c := range sb.calls()[n:] {
		if strings.Contains(c, "disable") || strings.Contains(c, "daemon-reload") {
			t.Errorf("systemd was down but was still called: %q", c)
		}
	}
}

func TestUninstallFromInsideServerDetaches(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	sb.writeTool("systemd-run", `echo "systemd-run $*" >> "$STUB_LOG"; exit 0`)
	if err := os.WriteFile(sb.env["UNINSTALL_CGROUP_FILE"],
		[]byte("0::/user.slice/user-1000.slice/user@1000.service/app.slice/terminal-mcp.service\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, sb.home)

	if code, out := sb.runUninstall(); code == 0 || !strings.Contains(out, "inside the terminal-mcp server") {
		t.Errorf("must refuse without --yes from inside the server (exit %d):\n%s", code, out)
	}
	out := sb.mustUninstall("--yes", "--keep-notes")
	if !strings.Contains(out, "3 seconds") {
		t.Errorf("does not explain the scheduling:\n%s", out)
	}
	var run string
	for _, c := range sb.calls() {
		if strings.HasPrefix(c, "systemd-run ") {
			run = c
		}
	}
	if run == "" {
		t.Fatalf("systemd-run was not called: %v", sb.calls())
	}
	for _, want := range []string{"--user", "--unit=tmcp-uninstall-", "UNINSTALL_DETACHED=1", "uninstall.sh", "--yes", "--keep-notes"} {
		if !strings.Contains(run, want) {
			t.Errorf("systemd-run is missing %q: %s", want, run)
		}
	}

	logFile := sb.path("tmp", "terminal-mcp-uninstall.log")
	if !exists(logFile) {
		t.Error("scheduling log was not created")
	}
	_ = os.Remove(logFile)
	if snapshot(t, sb.home) != before {
		t.Error("scheduling changed HOME")
	}
	for _, c := range sb.calls() {
		if strings.Contains(c, "disable") {
			t.Errorf("service stopped by a process that would be killed: %q", c)
		}
	}
}

func TestUninstallDetachedRunDoesNotDetachAgain(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.installFresh()
	sb.writeTool("systemd-run", `echo "systemd-run $*" >> "$STUB_LOG"; exit 0`)
	if err := os.WriteFile(sb.env["UNINSTALL_CGROUP_FILE"], []byte("0::/x/terminal-mcp.service\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sb.env["UNINSTALL_DETACHED"] = "1"
	sb.mustUninstall("--yes")
	for _, c := range sb.calls() {
		if strings.HasPrefix(c, "systemd-run") {
			t.Errorf("rescheduling endlessly: %q", c)
		}
	}
	if exists(sb.path(".local", "bin", "terminal-mcp")) {
		t.Error("does not delete")
	}
}

func TestUninstallRefusesPathsOutsideHome(t *testing.T) {
	sb := uninstallSandbox(t)
	sb.env["HOME"] = "/"
	code, out := sb.runUninstall("--dry-run")
	if code == 0 || !strings.Contains(out, "invalid HOME") {
		t.Errorf("HOME=/ must be rejected (exit %d):\n%s", code, out)
	}
}

func TestUninstallRejectsUnknownArgument(t *testing.T) {
	sb := uninstallSandbox(t)
	code, out := sb.runUninstall("--delete-all")
	if code != 2 || !strings.Contains(out, "unknown argument") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}
