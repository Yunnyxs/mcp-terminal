package host

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/mcp/proto"
	"terminal-mcp/internal/mcp/registry"
)

const (
	handshakeTimeout   = 60 * time.Second
	listTimeout        = 30 * time.Second
	defaultCallTimeout = 120 * time.Second
	logMax             = 5 << 20
	childTermWait      = 3 * time.Second
)

type Options struct {
	OpenLog func(path string, maxBytes int64) (io.Writer, error)
}

type host struct {
	name  string
	entry registry.Entry
	idle  time.Duration
	log   *log.Logger
	logw  io.Writer

	cs      *sdk.ClientSession
	cmd     *exec.Cmd
	started time.Time
	server  string
	version string
	nTools  int

	mu     sync.Mutex
	last   time.Time
	active int

	stopOnce sync.Once
	stopCh   chan struct{}
}

func Serve(name string, p config.Paths, opts Options) error {
	syscall.Umask(0o077)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return Run(ctx, name, p, opts)
}

func Run(ctx context.Context, name string, p config.Paths, opts Options) (err error) {

	runtime.LockOSThread()

	if err := registry.ValidName(name); err != nil {
		return err
	}
	cfg, err := registry.Load(p.MCPs)
	if err != nil {
		return err
	}
	entry, ok := cfg[name]
	if !ok {
		return fmt.Errorf("%q is not in %s", name, p.MCPs)
	}
	idle, err := entry.IdleDuration()
	if err != nil {
		return err
	}
	sock, err := proto.Socket(p, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.RunDir, 0o700); err != nil {
		return err
	}

	var logw, out io.Writer = os.Stderr, os.Stderr
	if opts.OpenLog != nil {
		w, err := opts.OpenLog(proto.LogFile(p, name), logMax)
		if err != nil {
			return fmt.Errorf("log host: %w", err)
		}
		logw, out = w, io.MultiWriter(os.Stderr, w)
	}
	lg := log.New(out, "[mcphost "+name+"] ", log.LstdFlags)
	defer func() {
		if err != nil {
			lg.Printf("Error: %v", err)
		}
	}()

	lock, err := os.OpenFile(proto.LockFile(p, name), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("host %q is already running or starting", name)
	}
	_ = os.Remove(sock)

	h := &host{name: name, entry: entry, idle: idle, log: lg, logw: logw, stopCh: make(chan struct{})}
	if err := h.connect(ctx); err != nil {
		h.teardown()
		return err
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		h.teardown()
		return err
	}
	_ = os.Chmod(sock, 0o600)
	defer os.Remove(sock)
	pid := proto.PIDFile(p, name)
	if err := os.WriteFile(pid, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		_ = ln.Close()
		h.teardown()
		return err
	}
	defer os.Remove(pid)

	lg.Printf("ready: %s %s (%s), %d tools, idle=%s, pid=%d", h.server, h.version, entry.Kind(), h.nTools, idleLabel(idle), os.Getpid())
	go h.accept(ln)
	go h.watchConn()
	go h.idleLoop()

	select {
	case <-ctx.Done():
		lg.Printf("stopping: signal received")
	case <-h.stopCh:
	}
	_ = ln.Close()
	h.teardown()
	lg.Printf("done")
	return nil
}

func idleLabel(d time.Duration) string {
	if d == 0 {
		return "off"
	}
	return d.String()
}

func (h *host) connect(ctx context.Context) error {
	t, cmd := newTransport(h.entry, h.logw)
	h.cmd = cmd

	client := sdk.NewClient(&sdk.Implementation{Name: "terminal-mcp-lite", Version: config.Version}, nil)

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := time.AfterFunc(handshakeTimeout, cancel)
	cs, err := client.Connect(cctx, t, nil)
	timer.Stop()
	if err != nil {
		if cctx.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf("MCP handshake exceeded %s", handshakeTimeout)
		}
		return fmt.Errorf("failed to connect to MCP: %w", err)
	}
	h.cs = cs
	h.started = time.Now()
	h.last = h.started
	if ir := cs.InitializeResult(); ir != nil && ir.ServerInfo != nil {
		h.server, h.version = ir.ServerInfo.Name, ir.ServerInfo.Version
	}
	lctx, lcancel := context.WithTimeout(ctx, listTimeout)
	defer lcancel()
	n := 0
	for _, err := range cs.Tools(lctx, nil) {
		if err != nil {
			return fmt.Errorf("MCP connected but fetching the tool list failed: %w", err)
		}
		n++
	}
	h.nTools = n
	return nil
}

func (h *host) watchConn() {
	err := h.cs.Wait()
	if err != nil {
		h.shutdown("connection to MCP ended: " + err.Error())
		return
	}
	h.shutdown("connection to MCP ended")
}

func (h *host) shutdown(reason string) {
	h.stopOnce.Do(func() {
		h.log.Printf("stopping: %s", reason)
		close(h.stopCh)
	})
}

func (h *host) idleLoop() {
	if h.idle <= 0 {
		return
	}
	tick := min(h.idle/4, time.Second)
	tick = max(tick, 50*time.Millisecond)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-h.stopCh:
			return
		case <-t.C:
			h.mu.Lock()
			expired := h.active == 0 && time.Since(h.last) > h.idle
			h.mu.Unlock()
			if expired {
				h.shutdown("idle exceeded " + h.idle.String())
				return
			}
		}
	}
}

func (h *host) teardown() {
	if h.cs != nil {
		done := make(chan struct{})
		go func() { _ = h.cs.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(childTermWait + 5*time.Second):
		}
	}
	if h.cmd != nil && h.cmd.Process != nil {
		killGroup(h.cmd.Process.Pid)
	}
}

func killGroup(pgid int) {
	if syscall.Kill(-pgid, 0) != nil {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	time.Sleep(300 * time.Millisecond)
	if syscall.Kill(-pgid, 0) == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
