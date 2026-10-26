package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"time"

	"terminal-mcp/internal/config"
)

var tunnelURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func lastTunnelURL(journal []byte) string {
	matches := tunnelURLRe.FindAll(journal, -1)
	if len(matches) == 0 {
		return ""
	}
	return string(matches[len(matches)-1])
}

func tunnelHost() string {
	out, err := exec.Command("journalctl", "--user", "-u", tunnelUnit, "-I", "--no-pager", "-o", "cat").Output()
	if err != nil {
		return ""
	}
	return lastTunnelURL(out)
}

func (c *cmd) waitTunnelHost(timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	announced := false
	for {
		if !isActive(tunnelUnit) {
			return "", errors.New("tunnel is not running; run: terminal-mcp start")
		}
		if h := tunnelHost(); h != "" {
			return h, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("tunnel URL did not appear within %s; check: terminal-mcp logs tunnel", timeout)
		}
		if !announced {
			fmt.Fprintln(c.err, "Waiting for the tunnel...")
			announced = true
		}
		time.Sleep(time.Second)
	}
}

func (c *cmd) url() error {
	s, err := config.FromEnvFile()
	if err != nil {
		return err
	}
	if s.Token == "" {
		return errors.New("TERMINAL_MCP_TOKEN is empty in config.env")
	}
	host, err := c.waitTunnelHost(90 * time.Second)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, host+"/"+s.Token+"/mcp")
	return nil
}
