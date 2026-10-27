main() {
  check_prerequisites
  mkdir -p "$BIN" "$CONF" "$UNITS"
  step_path
  step_build
  step_config
  step_cloudflared
  step_root_access "$@"
  step_services
  step_verify
}
