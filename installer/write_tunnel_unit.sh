# shellcheck shell=bash
write_tunnel_unit() {
  cat > "$UNITS/terminal-mcp-tunnel.service" <<EOF
[Unit]
Description=terminal-mcp - Cloudflare quick tunnel
Wants=terminal-mcp.service
After=terminal-mcp.service

[Service]
ExecStart=$CF tunnel --no-autoupdate --protocol http2 --url http://127.0.0.1:$PORT
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF
}
