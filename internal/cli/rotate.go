package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"terminal-mcp/internal/config"
	"terminal-mcp/internal/fsutil"
)

func (c *cmd) rotate() error {
	path, err := config.EnvFilePath()
	if err != nil {
		return err
	}
	if _, err := rewriteToken(path); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "New token saved to %s\n", path)

	s, err := config.FromEnvFile()
	if err != nil {
		return err
	}

	if insideServer() {

		resetFailed(serverUnit)
		if err := scheduleSystemctl("restart", serverUnit); err != nil {
			return fmt.Errorf("token was replaced but the restart could not be scheduled (%w); run: terminal-mcp restart", err)
		}
		fmt.Fprintf(c.out, "Running from inside the server. Restart scheduled in %s; the connection will drop briefly.\n", laterDelay)
		if host := tunnelHost(); host != "" {
			fmt.Fprintf(c.out, "New URL:\n%s/%s/mcp\n", host, s.Token)
		}
		fmt.Fprintln(c.out, "The old URL stops working after the restart.")
		return nil
	}

	resetFailed(serverUnit)
	if err := systemctl("restart", serverUnit); err != nil {
		return fmt.Errorf("new token saved but the restart failed: %w", err)
	}
	took, err := waitReady(s, 20*time.Second)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Server restarted (HTTP 200 within %s).\n", took)
	if !isActive(tunnelUnit) {
		fmt.Fprintln(c.err, "Tunnel is not running, so there is no URL yet. Run: terminal-mcp start")
		return nil
	}
	host, err := c.waitTunnelHost(90 * time.Second)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "New URL:\n%s/%s/mcp\n", host, s.Token)
	fmt.Fprintln(c.out, "The old URL no longer works.")
	return nil
}

func rewriteToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	if err := fsutil.WriteFileAtomic(path, []byte(replaceToken(string(data), token)), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func replaceToken(content, token string) string {
	const prefix = "TERMINAL_MCP_TOKEN="
	lines := strings.Split(content, "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			lines[i] = prefix + token
			found = true
		}
	}
	if !found {
		return strings.TrimRight(content, "\n") + "\n" + prefix + token + "\n"
	}
	return strings.Join(lines, "\n")
}
