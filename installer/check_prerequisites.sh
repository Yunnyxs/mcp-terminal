check_prerequisites() {
  [ "$(id -u)" -ne 0 ] || die "Do not run as root. Run as a regular user."
  command -v go >/dev/null 2>&1 || die "Go is not installed. Arch: sudo pacman -S go"
  command -v curl >/dev/null 2>&1 || die "curl is not installed."
  systemctl --user show-environment >/dev/null 2>&1 || die "The systemd user session is not active (check: systemctl --user status)."
}
