# shellcheck shell=bash
step_root_access() {
  local a ans="" stmp root_ok=0
  say "4/6 Root access for Claude (optional)"
  for a in "$@"; do
    if [ "$a" = "--yes-root" ]; then root_ok=1; fi
  done
  if [ "$root_ok" = 0 ] && [ -t 0 ]; then
    read -rp "Give Claude passwordless root access (sudo NOPASSWD ALL)? [y/N] " ans || ans=""
    if [[ "$ans" =~ ^[Yy]$ ]]; then root_ok=1; fi
  fi
  if [ "$root_ok" = 1 ]; then
    stmp="$(mktemp)"
    echo "$(id -un) ALL=(ALL) NOPASSWD: ALL" > "$stmp"
    sudo visudo -cf "$stmp" >/dev/null || { rm -f "$stmp"; die "sudoers validation failed"; }
    sudo install -m 440 -o root -g root "$stmp" /etc/sudoers.d/terminal-mcp
    rm -f "$stmp"
    warn "Anyone who has the connector URL = root on this machine. Keep the URL secret."
  else
    echo "   skipped (sudo still requires a password)"
  fi
}
