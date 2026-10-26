package lifecycle

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/proto"
	"terminal-mcp/internal/mcp/registry"
)

const EnvSpawn = "TMCP_SPAWN"

const logTailRows = 15

func UnitName(name string) string { return "tmcp-mcp-" + name }

func spawn(exe, name string) (method, warn string, alive func() bool, err error) {
	mode := os.Getenv(EnvSpawn)
	if mode != "" && mode != "systemd" && mode != "setsid" {
		return "", "", nil, fmt.Errorf("%s=%q is not recognized (use systemd or setsid)", EnvSpawn, mode)
	}
	if mode != "setsid" {
		alive, serr := spawnSystemd(exe, name)
		if serr == nil {
			return "systemd", "", alive, nil
		}
		if mode == "systemd" {
			return "", "", nil, serr
		}
		warn = "systemd-run failed (" + serr.Error() + "); host started with setsid and will stop if the terminal-mcp service restarts"
	}
	alive, err = spawnDetached(exe, name)
	if err != nil {
		return "", "", nil, err
	}
	return "setsid", warn, alive, nil
}

func spawnSystemd(exe, name string) (func() bool, error) {
	sr, err := exec.LookPath("systemd-run")
	if err != nil {
		return nil, errors.New("systemd-run not found")
	}
	unit := UnitName(name)
	args := []string{"--user", "--quiet", "--collect", "--unit=" + unit, "--description=terminal-mcp-lite host: " + name}

	keys := append(registry.PassEnv(), config.EnvDir, config.EnvMCPs)
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			args = append(args, "--setenv="+k+"="+v)
		}
	}
	args = append(args, "--", exe, "mcphost", name)
	if out, err := exec.Command(sr, args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	var lastCheck time.Time
	state := true
	return func() bool {
		if time.Since(lastCheck) < time.Second {
			return state
		}
		lastCheck = time.Now()
		state = exec.Command("systemctl", "--user", "is-active", "--quiet", unit).Run() == nil
		return state
	}, nil
}

func spawnDetached(exe, name string) (func() bool, error) {
	cmd := exec.Command(exe, "mcphost", name)
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	return func() bool {
		select {
		case <-done:
			return false
		default:
			return true
		}
	}, nil
}

func logTail(p config.Paths, name string) string {
	path := proto.LogFile(p, name)
	f, err := os.Open(path)
	if err != nil {
		return "(host log missing: " + path + ")"
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > 64<<10 {
		_, _ = f.Seek(info.Size()-64<<10, io.SeekStart)
	}
	var rows []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		rows = append(rows, sc.Text())
		if len(rows) > logTailRows {
			rows = rows[1:]
		}
	}
	return "log " + path + ":\n  " + strings.Join(rows, "\n  ")
}
