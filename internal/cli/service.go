package cli

import (
	"errors"
	"fmt"
	"time"

	"terminal-mcp/internal/clierr"
	"terminal-mcp/internal/config"
)

var restartTargets = map[string][]string{
	"server": {serverUnit},
	"tunnel": {tunnelUnit},
	"all":    {serverUnit, tunnelUnit},
}

func (c *cmd) restart(args []string) error {
	target := "server"
	switch {
	case len(args) > 1:
		return clierr.Usage("usage: terminal-mcp restart [server|tunnel|all]")
	case len(args) == 1:
		target = args[0]
	}
	units, ok := restartTargets[target]
	if !ok {
		return clierr.Usagef("unknown target %q (choose: server, tunnel, all)", target)
	}

	if insideServer() {
		resetFailed(units...)
		if err := scheduleSystemctl(append([]string{"restart"}, units...)...); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "Running from inside the server. Restart scheduled in %s; the connection will drop briefly.\n", laterDelay)
		if target != "server" {
			fmt.Fprintln(c.out, "The tunnel URL will change; get the new one with: terminal-mcp url (from your own terminal).")
		}
		return nil
	}

	s, err := config.FromEnvFile()
	if err != nil {
		return err
	}
	resetFailed(units...)
	tunnelBefore := unitProp(tunnelUnit, "MainPID")
	if target != "tunnel" {
		if err := systemctl("restart", serverUnit); err != nil {
			return err
		}
		took, err := waitReady(s, 20*time.Second)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "Server restarted (HTTP 200 within %s).\n", took)
	}
	if target != "server" {
		if err := systemctl("restart", tunnelUnit); err != nil {
			return err
		}
	}
	return c.reportTunnel(s, tunnelBefore, target)
}

func (c *cmd) reportTunnel(s config.Settings, pidBefore, target string) error {
	if !isActive(tunnelUnit) {
		fmt.Fprintln(c.err, "Tunnel is not running, so the connector cannot be used. Run: terminal-mcp start")
		return nil
	}
	if unitProp(tunnelUnit, "MainPID") == pidBefore {
		fmt.Fprintln(c.out, "Connector URL unchanged.")
		return nil
	}
	host, err := c.waitTunnelHost(90 * time.Second)
	if err != nil {
		return err
	}
	if target == "server" {
		fmt.Fprintln(c.err, "Tunnel restarted too: the tunnel unit still uses Requires=. Re-run install.sh so a server restart does not change the URL.")
	}
	fmt.Fprintln(c.out, "New URL:")
	fmt.Fprintln(c.out, host+"/"+s.Token+"/mcp")
	fmt.Fprintln(c.out, "The old URL no longer works: update the connector in claude.ai.")
	return nil
}

func (c *cmd) start() error {
	s, err := config.FromEnvFile()
	if err != nil {
		return err
	}
	resetFailed(serverUnit, tunnelUnit)
	if err := systemctl("start", serverUnit, tunnelUnit); err != nil {
		return err
	}
	took, err := waitReady(s, 20*time.Second)
	if err != nil {
		return err
	}
	host, err := c.waitTunnelHost(90 * time.Second)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Server and tunnel running (HTTP 200 within %s). URL:\n%s/%s/mcp\n", took, host, s.Token)
	return nil
}

func (c *cmd) stop() error {
	if insideServer() {
		return errors.New("refused: this would stop the server that is running this command. Run it from your own terminal")
	}
	if err := systemctl("stop", tunnelUnit, serverUnit); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "Server and tunnel stopped.")
	return nil
}

func (c *cmd) status() error {
	healthy := true
	row := func(name, state, detail string) {
		fmt.Fprintf(c.out, "%-8s %-11s %s\n", name, state, detail)
	}

	for _, u := range []struct{ name, unit string }{{"server", serverUnit}, {"tunnel", tunnelUnit}} {
		if !isActive(u.unit) {
			row(u.name, "DOWN", "run: terminal-mcp start")
			healthy = false
			continue
		}
		detail := ""
		if d, ok := activeFor(u.unit); ok {
			detail = "running for " + humanDuration(d)
		}
		row(u.name, "active", detail)
	}

	s, err := config.FromEnvFile()
	if err != nil {
		row("HTTP", "FAILED", err.Error())
		healthy = false
	} else if d, err := probe(s); err != nil {
		row("HTTP", "FAILED", probeAddr(s)+": "+err.Error())
		healthy = false
	} else {
		row("HTTP", "local OK", fmt.Sprintf("%s (%d ms)", probeAddr(s), d.Milliseconds()))
	}

	switch {
	case !isActive(tunnelUnit):
		row("URL", "-", "tunnel down")
	case tunnelHost() == "":
		row("URL", "not yet", "tunnel just started? try again shortly")
		healthy = false
	default:
		row("URL", "available", "run: terminal-mcp url")
	}

	if sudoActive() {
		row("sudo", "active", "passwordless (sudo=true can be used)")
	} else {
		row("sudo", "inactive", "sudo=true will fail (use install.sh --yes-root to enable)")
	}

	if !healthy {
		return clierr.Reported
	}
	return nil
}
