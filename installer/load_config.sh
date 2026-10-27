load_config() {
  local h
  TOKEN=""
  PORT="$DEFAULT_PORT"
  HOST_CUR="127.0.0.1"
  EXTRA=""
  [ -f "$CONF/config.env" ] || return 0

  TOKEN="$(sed -n 's/^TERMINAL_MCP_TOKEN=//p' "$CONF/config.env" | head -1)"
  PORT="$(sed -n 's/^TERMINAL_MCP_PORT=//p' "$CONF/config.env" | head -1)"
  PORT="${PORT:-$DEFAULT_PORT}"
  h="$(sed -n 's/^TERMINAL_MCP_HOST=//p' "$CONF/config.env" | head -1)"
  HOST_CUR="${h:-127.0.0.1}"
  EXTRA="$(grep -vE '^(TERMINAL_MCP_TOKEN|TERMINAL_MCP_HOST|TERMINAL_MCP_PORT)=' "$CONF/config.env" || true)"
}
