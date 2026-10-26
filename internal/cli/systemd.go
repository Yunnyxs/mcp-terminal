package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	serverUnit = "terminal-mcp.service"
	tunnelUnit = "terminal-mcp-tunnel.service"

	laterDelay = 3 * time.Second
)

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("systemctl --user %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}

func isActive(unit string) bool {
	return exec.Command("systemctl", "--user", "is-active", "--quiet", unit).Run() == nil
}

func resetFailed(units ...string) {
	_ = exec.Command("systemctl", append([]string{"--user", "reset-failed"}, units...)...).Run()
}

func unitProp(unit, prop string) string {
	out, err := exec.Command("systemctl", "--user", "--timestamp=unix", "show", "-p", prop, "--value", unit).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func activeFor(unit string) (time.Duration, bool) {
	secs, err := strconv.ParseInt(strings.TrimPrefix(unitProp(unit, "ActiveEnterTimestamp"), "@"), 10, 64)
	if err != nil || secs <= 0 {
		return 0, false
	}
	return time.Since(time.Unix(secs, 0)), true
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d sec", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr %d min", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d d %d hr", int(d.Hours())/24, int(d.Hours())%24)
}

func cgroupHasUnit(cgroup, unit string) bool {
	return strings.Contains(cgroup, "/"+unit)
}

func insideServer() bool {
	b, err := os.ReadFile("/proc/self/cgroup")
	return err == nil && cgroupHasUnit(string(b), serverUnit)
}

func scheduleSystemctl(args ...string) error {
	sc, err := exec.LookPath("systemctl")
	if err != nil {
		return err
	}
	argv := []string{
		"--user", "--quiet", "--collect",
		"--unit=terminal-mcp-later-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		fmt.Sprintf("--on-active=%ds", int(laterDelay.Seconds())),
		sc, "--user",
	}
	out, err := exec.Command("systemd-run", append(argv, args...)...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("systemd-run: %s", msg)
	}
	return nil
}

func sudoActive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "sudo", "-n", "true").Run() == nil
}
