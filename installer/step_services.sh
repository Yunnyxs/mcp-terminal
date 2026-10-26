# shellcheck shell=bash
step_services() {
  say "5/6 Install and start the services"
  install -m 644 "$KIT/units/terminal-mcp.service" "$UNITS/terminal-mcp.service"
  write_tunnel_unit
  systemctl --user daemon-reload
  systemctl --user enable terminal-mcp.service terminal-mcp-tunnel.service >/dev/null
  systemctl --user restart terminal-mcp.service
  systemctl --user restart terminal-mcp-tunnel.service
  loginctl enable-linger "$USER" 2>/dev/null || warn "enabling linger failed: the services only start after login."
}
