# shellcheck shell=bash
step_verify() {
  say "6/6 Verification and connector URL"
  wait_server_ready
  find_tunnel_url
  print_summary
}
