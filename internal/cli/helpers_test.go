package cli

import (
	"strings"
	"testing"
	"time"
)

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                               "0 sec",
		59 * time.Second:                "59 sec",
		time.Minute:                     "1 min",
		59*time.Minute + 59*time.Second: "59 min",
		time.Hour:                       "1 hr 0 min",
		3*time.Hour + 25*time.Minute:    "3 hr 25 min",
		23*time.Hour + 59*time.Minute:   "23 hr 59 min",
		24 * time.Hour:                  "1 d 0 hr",
		50*time.Hour + 30*time.Minute:   "2 d 2 hr",
	}
	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestCgroupHasUnit(t *testing.T) {
	inServer := "0::/user.slice/user-1000.slice/user@1000.service/app.slice/terminal-mcp.service\n"
	inTunnel := "0::/user.slice/user-1000.slice/user@1000.service/app.slice/terminal-mcp-tunnel.service\n"
	inShell := "0::/user.slice/user-1000.slice/session-3.scope\n"
	if !cgroupHasUnit(inServer, serverUnit) {
		t.Error("process inside the server not detected")
	}
	if cgroupHasUnit(inTunnel, serverUnit) {
		t.Error("tunnel unit mistaken for server (same name prefix)")
	}
	if cgroupHasUnit(inShell, serverUnit) {
		t.Error("plain shell mistaken for inside the server")
	}
}

func TestLastTunnelURLPicksNewest(t *testing.T) {
	journal := `2026-10-20 INF Requesting new quick Tunnel on trycloudflare.com...
2026-10-20 INF |  https://old-one-abc.trycloudflare.com  |
2026-10-20 INF Registered tunnel connection
2026-10-20 INF |  https://new-two-xyz.trycloudflare.com  |
`
	if got := lastTunnelURL([]byte(journal)); got != "https://new-two-xyz.trycloudflare.com" {
		t.Errorf("got %q", got)
	}
	for _, empty := range []string{"", "no url", "https://example.com not a tunnel", "http://x.trycloudflare.com"} {
		if got := lastTunnelURL([]byte(empty)); got != "" {
			t.Errorf("lastTunnelURL(%q) = %q, want empty", empty, got)
		}
	}
}

func TestUnitNamesMatchInstaller(t *testing.T) {

	if serverUnit != "terminal-mcp.service" || tunnelUnit != "terminal-mcp-tunnel.service" {
		t.Errorf("unit = %q / %q", serverUnit, tunnelUnit)
	}
	if !strings.HasSuffix(serverUnit, ".service") || !strings.HasSuffix(tunnelUnit, ".service") {
		t.Error("unit name must end with .service")
	}
}
