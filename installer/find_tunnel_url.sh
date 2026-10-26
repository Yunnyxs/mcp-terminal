# shellcheck shell=bash
find_tunnel_url() {
  URL=""
  for _ in $(seq 1 90); do
    URL="$(journalctl --user -u terminal-mcp-tunnel -I --no-pager 2>/dev/null \
      | grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' | tail -1 || true)"
    [ -n "$URL" ] && break
    sleep 1
  done
}
