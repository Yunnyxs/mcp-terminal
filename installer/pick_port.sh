# shellcheck shell=bash
pick_port() {
  local p
  if systemctl --user is-active -q terminal-mcp.service; then
    :
  elif port_busy "$PORT"; then
    PORT=""
  fi
  if [ -z "$PORT" ]; then
    p=$DEFAULT_PORT
    while port_busy "$p"; do
      p=$((p + 1))
      [ "$p" -le 65535 ] || die "no free port"
    done
    PORT="$p"
    echo "   port chosen: $PORT"
  else
    echo "   port: $PORT"
  fi
}
