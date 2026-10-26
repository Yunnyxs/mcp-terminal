package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/client"
	"terminal-mcp/internal/mcp/proto"
	"terminal-mcp/internal/mcp/registry"
)

const loadWait = 90 * time.Second

type Manager struct {
	Paths config.Paths

	Exe string

	cl *client.Client
}

func New(p config.Paths) *Manager { return &Manager{Paths: p, cl: client.New(p)} }

func (m *Manager) exe() (string, error) {
	if m.Exe != "" {
		return m.Exe, nil
	}
	return os.Executable()
}

type LoadResult struct {
	Info    *proto.Info
	Started bool
	Method  string
	Warn    string
}

func (m *Manager) Load(name string) (*LoadResult, error) {
	if err := registry.ValidName(name); err != nil {
		return nil, err
	}
	cfg, err := registry.Load(m.Paths.MCPs)
	if err != nil {
		return nil, err
	}
	entry, ok := cfg[name]
	if !ok {
		return nil, fmt.Errorf("%q is not in %s (add it with: tmcp mcp add)", name, m.Paths.MCPs)
	}
	if err := entry.Validate(); err != nil {
		return nil, fmt.Errorf("%q: %w", name, err)
	}
	if info, err := m.cl.Ping(name); err == nil {
		return &LoadResult{Info: info}, nil
	} else if !errors.Is(err, client.ErrNotLoaded) {
		return nil, err
	}
	if _, err := proto.Socket(m.Paths, name); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.Paths.RunDir, 0o700); err != nil {
		return nil, err
	}

	exe, err := m.exe()
	if err != nil {
		return nil, err
	}
	method, warn, alive, err := spawn(exe, name)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(loadWait)
	for time.Now().Before(deadline) {
		if info, err := m.cl.Ping(name); err == nil {
			return &LoadResult{Info: info, Started: true, Method: method, Warn: warn}, nil
		}
		if !alive() {
			time.Sleep(400 * time.Millisecond)
			if info, err := m.cl.Ping(name); err == nil {
				return &LoadResult{Info: info, Started: true, Method: method, Warn: warn}, nil
			}
			return nil, fmt.Errorf("host %q stopped before it was ready.\n%s", name, logTail(m.Paths, name))
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("host %q not ready after %s.\n%s", name, loadWait, logTail(m.Paths, name))
}

func (m *Manager) Ensure(name string, autoload bool) (*LoadResult, error) {
	if err := registry.ValidName(name); err != nil {
		return nil, err
	}
	if info, err := m.cl.Ping(name); err == nil {
		return &LoadResult{Info: info}, nil
	} else if !errors.Is(err, client.ErrNotLoaded) {
		return nil, err
	}
	if !autoload {
		return nil, fmt.Errorf("%w: run: tmcp load %s", client.ErrNotLoaded, name)
	}
	return m.Load(name)
}

func (m *Manager) Unload(name string) (wasLoaded bool, err error) {
	if err := registry.ValidName(name); err != nil {
		return false, err
	}
	if _, perr := m.cl.Ping(name); perr == nil {
		wasLoaded = true
		if err := m.cl.Stop(name); err != nil {
			return true, err
		}
		if m.waitGone(name, 20*time.Second) {
			return true, nil
		}
	}

	if pid, ok := m.livePID(name); ok {
		wasLoaded = true
		_ = syscall.Kill(pid, syscall.SIGTERM)
		if m.waitGone(name, 10*time.Second) && !pidAlive(pid) {
			return true, nil
		}
		return true, fmt.Errorf("host pid %d did not stop after SIGTERM", pid)
	}
	m.cleanStale(name)
	return wasLoaded, nil
}

func (m *Manager) UnloadAll() (stopped []string, err error) {
	var errs []string
	for _, name := range m.runNames() {
		was, uerr := m.Unload(name)
		if uerr != nil {
			errs = append(errs, name+": "+uerr.Error())
			continue
		}
		if was {
			stopped = append(stopped, name)
		}
	}
	if len(errs) > 0 {
		return stopped, errors.New(strings.Join(errs, "; "))
	}
	return stopped, nil
}

func (m *Manager) Loaded() []*proto.Info {
	var out []*proto.Info
	for _, name := range m.runNames() {
		if info, err := m.cl.Ping(name); err == nil {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) waitGone(name string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := m.cl.Ping(name); errors.Is(err, client.ErrNotLoaded) {
			if pid, ok := m.livePID(name); !ok || !pidAlive(pid) {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func (m *Manager) livePID(name string) (int, bool) {
	data, err := os.ReadFile(proto.PIDFile(m.Paths, name))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 || !pidAlive(pid) {
		return 0, false
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return 0, false
	}
	args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	if len(args) >= 3 && args[len(args)-2] == "mcphost" && args[len(args)-1] == name {
		return pid, true
	}
	return 0, false
}

func (m *Manager) cleanStale(name string) {
	lock, err := os.OpenFile(proto.LockFile(m.Paths, name), os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return
	}
	if sock, err := proto.Socket(m.Paths, name); err == nil {
		_ = os.Remove(sock)
	}
	_ = os.Remove(proto.PIDFile(m.Paths, name))
}

func (m *Manager) runNames() []string {
	entries, err := os.ReadDir(m.Paths.RunDir)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, e := range entries {
		n := e.Name()
		for _, ext := range []string{".sock", ".pid"} {
			if base, ok := strings.CutSuffix(n, ext); ok && registry.ValidName(base) == nil {
				set[base] = true
			}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
