# shellcheck shell=bash
write_config() {
  umask 077
  {
    echo "TERMINAL_MCP_TOKEN=$TOKEN"
    echo "TERMINAL_MCP_HOST=$HOST_CUR"
    echo "TERMINAL_MCP_PORT=$PORT"
    if [ -n "$EXTRA" ]; then printf '%s\n' "$EXTRA"; fi
  } > "$CONF/config.env.tmp"
  mv -f "$CONF/config.env.tmp" "$CONF/config.env"
  chmod 600 "$CONF/config.env"
  echo "   $CONF/config.env (mode 600)"
}
