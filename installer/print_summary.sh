print_summary() {
  echo
  if [ -n "$URL" ]; then
    say "Done. Connector URL for claude.ai:"
    echo "$URL/$TOKEN/mcp"
    warn "Quick tunnel: DNS needs about 1 minute before it is reachable. If claude.ai fails to connect, wait and try again."
    warn "The URL changes every time the tunnel restarts (restarting only the server does not change it). Check the latest URL: terminal-mcp url"
  else
    warn "The tunnel has not provided a URL yet. Check: terminal-mcp logs tunnel -n 30"
  fi
  warn "The token is in $CONF/config.env. Anyone who has this URL can run commands on this machine, so keep it safe."
  echo "Short commands: tmcp status | url | restart | logs | rotate | help"
  warn "Terminals that are already open have not picked up the new PATH. Run: source ~/.bashrc (or open a new terminal)"
}
