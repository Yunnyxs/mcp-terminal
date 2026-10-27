#!/usr/bin/env bash
set -euo pipefail

KIT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$HOME/.local/bin"
CONF="$HOME/.config/terminal-mcp"
UNITS="$HOME/.config/systemd/user"
DATA="$HOME/.mcp-terminal"
SUDOERS="/etc/sudoers.d/terminal-mcp"
RC="$HOME/.bashrc"
RC_MARK='# terminal-mcp: add ~/.local/bin to PATH (must be at the top, before the non-interactive return)'
RC_LINE='export PATH="$HOME/.local/bin:$PATH"'
SERVICES=(terminal-mcp-tunnel.service terminal-mcp.service)
CGROUP_FILE="${UNINSTALL_CGROUP_FILE:-/proc/self/cgroup}"

for f in say die warn; do source "$KIT/installer/$f.sh"; done

DRY=0 YES=0 KEEP_NOTES=0 KEEP_TRASH=0 RM_CF=0
LEFT=()

usage() {
  cat <<'EOF'
terminal-mcp uninstaller: the reverse of install.sh. Removes everything the installer and the server created,
so the machine looks as if terminal-mcp had never been installed.

  ./uninstall.sh --dry-run    show the plan without changing anything
  ./uninstall.sh              show the plan, then ask for confirmation
  ./uninstall.sh --yes        do not ask (required when run from inside the terminal-mcp server)

Options:
  --keep-notes          do not delete ~/.mcp-terminal/notes (the user's notes)
  --keep-trash          do not delete ~/.mcp-terminal/Trash (files "deleted" through the delete tool)
  --remove-cloudflared  also delete ~/.local/bin/cloudflared (the installer cannot tell whether it downloaded
                        it or it was already there, so it is kept by default)

Removed: the systemd services (server, tunnel, separate MCP hosts tmcp-mcp-*), the terminal-mcp and tmcp
binaries, ~/.config/terminal-mcp (config.env holds the token, mcps.json), ~/.mcp-terminal (logs, run, MCP output,
Trash, notes), the two PATH lines the installer added to ~/.bashrc, and /etc/sudoers.d/terminal-mcp.
NOT touched: this source folder, the Go build cache, loginctl linger (see the note at the end), and anything
outside the list above.

When run from inside the terminal-mcp server itself (for example through the run tool), stopping the service
would kill this script. It therefore moves itself into a temporary systemd unit (tmcp-uninstall-*), waits
3 seconds, and does the work there. The result is written to ${TMPDIR:-/tmp}/terminal-mcp-uninstall.log.
EOF
}

act() {
  if [ "$DRY" = 1 ]; then printf '   [dry-run] %s\n' "$*"; else "$@"; fi
}

safe_rm() {
  local p="$1"
  case "$p" in
    "$HOME"/?*) ;;
    *) die "refusing to delete outside \$HOME: $p" ;;
  esac
  case "$KIT/" in
    "$p"/*) die "refusing to delete a directory that contains the uninstaller source: $p" ;;
  esac
  { [ -e "$p" ] || [ -L "$p" ]; } || return 0
  act rm -rf -- "$p"
  if [ "$DRY" = 0 ]; then echo "   removed: $p"; fi
}

exists() { [ -e "$1" ] || [ -L "$1" ]; }

for a in "$@"; do
  case "$a" in
    --dry-run) DRY=1 ;;
    --yes|-y) YES=1 ;;
    --keep-notes) KEEP_NOTES=1 ;;
    --keep-trash) KEEP_TRASH=1 ;;
    --remove-cloudflared) RM_CF=1 ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'unknown argument: %s\n\n' "$a" >&2; usage >&2; exit 2 ;;
  esac
done

[ "$(id -u)" -ne 0 ] || die "Do not run as root. Run as a regular user."
case "$HOME" in /?*) ;; *) die "invalid HOME: '$HOME'" ;; esac
[ "$HOME" != "/" ] || die "invalid HOME"

HAVE_SYSTEMD=1
if ! command -v systemctl >/dev/null 2>&1 || ! systemctl --user show-environment >/dev/null 2>&1; then
  HAVE_SYSTEMD=0
fi

if [ -z "${UNINSTALL_DETACHED:-}" ] && grep -q '/terminal-mcp.service' "$CGROUP_FILE" 2>/dev/null; then
  [ "$DRY" = 1 ] || [ "$YES" = 1 ] || die "Running from inside the terminal-mcp server: add --yes (there is no terminal to ask on)."
  if [ "$DRY" = 0 ]; then
    command -v systemd-run >/dev/null 2>&1 || die "systemd-run not found; run this script from a regular terminal, not from inside the server."
    LOG="${TMPDIR:-/tmp}/terminal-mcp-uninstall.log"
    : > "$LOG"
    systemd-run --user --collect --quiet --unit="tmcp-uninstall-$$" --setenv=UNINSTALL_DETACHED=1 \
      /usr/bin/env bash -c 'sleep 3; exec "$0" "$@" >>"$LOG" 2>&1' "$KIT/uninstall.sh" "$@"
    say "Uninstall scheduled in 3 seconds outside the server (this session will drop)."
    echo "   result: $LOG"
    exit 0
  fi
fi

CFG_PATHS=()
if [ -f "$CONF/config.env" ]; then
  for key in TERMINAL_MCP_ACTIVITY_LOG TERMINAL_MCP_LOG TERMINAL_MCP_TRASH TERMINAL_MCP_NOTES; do
    v="$(sed -n "s/^$key=//p" "$CONF/config.env" | head -1)"
    case "$v" in
      "") ;;
      "$DATA"|"$DATA"/*) ;;
      *) if exists "$v"; then CFG_PATHS+=("$key=$v"); fi ;;
    esac
  done
fi

size_of() { if command -v du >/dev/null 2>&1; then du -sh -- "$1" 2>/dev/null | cut -f1; fi; }
mark() { if exists "$1"; then printf '   [present]   %s %s\n' "$1" "${2:-}"; else printf '   [absent]    %s\n' "$1"; fi; }

say "terminal-mcp uninstall plan$( [ "$DRY" = 1 ] && echo ' (dry-run)')"
echo "systemd services:"
for u in "${SERVICES[@]}"; do mark "$UNITS/$u"; done
echo "Binaries:"
mark "$BIN/terminal-mcp"; mark "$BIN/tmcp"
if [ "$RM_CF" = 1 ]; then mark "$BIN/cloudflared" "(--remove-cloudflared)"; else echo "   [kept]      $BIN/cloudflared (use --remove-cloudflared to delete it)"; fi
echo "Configuration (config.env holds the token, mcps.json may hold secrets):"
mark "$CONF" "$(size_of "$CONF")"
echo "Data:"
mark "$DATA" "$(size_of "$DATA")"
if [ -d "$DATA/notes" ]; then
  if [ "$KEEP_NOTES" = 1 ]; then echo "   [kept]      $DATA/notes (--keep-notes)"; else warn "notes will be DELETED: $DATA/notes (use --keep-notes to keep them)"; fi
fi
if [ -d "$DATA/Trash" ]; then
  if [ "$KEEP_TRASH" = 1 ]; then echo "   [kept]      $DATA/Trash (--keep-trash)"; else warn "Trash will be PERMANENTLY DELETED, its files cannot be recovered: $DATA/Trash (use --keep-trash)"; fi
fi
echo "Other:"
if [ -f "$RC" ] && grep -qxF "$RC_MARK" "$RC"; then echo "   [present]   two PATH lines in $RC"; else echo "   [absent]    installer PATH lines in $RC"; fi
echo "   /etc/sudoers.d/terminal-mcp: checked and removed through sudo if present"
for p in "${CFG_PATHS[@]}"; do warn "outside $DATA, NOT deleted: $p"; done

if [ "$DRY" = 0 ] && [ "$YES" = 0 ]; then
  [ -t 0 ] || die "No terminal available for confirmation. Add --yes (or --dry-run to see the plan only)."
  read -rp "Type 'delete' to continue: " ans || ans=""
  [ "$ans" = "delete" ] || die "Cancelled."
fi

say "1/6 Stop the services"
if [ "$HAVE_SYSTEMD" = 1 ]; then
  for u in "${SERVICES[@]}"; do
    act systemctl --user disable --now "$u" >/dev/null 2>&1 || true
  done
  while read -r unit _; do
    [ -n "$unit" ] || continue
    act systemctl --user stop "$unit" >/dev/null 2>&1 || true
    echo "   stopped: $unit"
  done < <(systemctl --user list-units 'tmcp-mcp-*' --all --plain --no-legend 2>/dev/null || true)
else
  warn "systemd user session not active: service step skipped"
fi
for pf in "$DATA"/run/*.pid; do
  [ -f "$pf" ] || continue
  pid="$(head -1 "$pf" 2>/dev/null || true)"
  case "$pid" in ''|*[!0-9]*) continue ;; esac
  if [ -r "/proc/$pid/cmdline" ] && tr '\0' ' ' < "/proc/$pid/cmdline" | grep -q ' mcphost '; then
    act kill "$pid" 2>/dev/null || true
    echo "   stopped: host pid $pid"
  fi
done

say "2/6 Remove the systemd units"
for u in "${SERVICES[@]}"; do
  safe_rm "$UNITS/$u"
  safe_rm "$UNITS/default.target.wants/$u"
done
if [ "$HAVE_SYSTEMD" = 1 ]; then
  act systemctl --user daemon-reload || true
  act systemctl --user reset-failed >/dev/null 2>&1 || true
fi

say "3/6 Remove the binaries"
safe_rm "$BIN/terminal-mcp"
if [ -L "$BIN/tmcp" ]; then
  case "$(readlink "$BIN/tmcp")" in
    terminal-mcp|*/terminal-mcp) safe_rm "$BIN/tmcp" ;;
    *) warn "$BIN/tmcp is not an alias of terminal-mcp, left alone" ;;
  esac
elif exists "$BIN/tmcp"; then
  warn "$BIN/tmcp is not a symlink made by the installer, left alone"
fi
safe_rm "$BIN/cloudflared.tmp"
if [ "$RM_CF" = 1 ]; then safe_rm "$BIN/cloudflared"; fi
if [ "$DRY" = 0 ]; then rmdir "$BIN" 2>/dev/null || true; fi

say "4/6 Remove the configuration"
safe_rm "$CONF"

say "5/6 Remove the data"
if exists "$DATA"; then
  shopt -s dotglob nullglob
  for e in "$DATA"/*; do
    case "$e" in
      "$DATA/notes") if [ "$KEEP_NOTES" = 1 ]; then echo "   kept: $e"; continue; fi ;;
      "$DATA/Trash") if [ "$KEEP_TRASH" = 1 ]; then echo "   kept: $e"; continue; fi ;;
    esac
    safe_rm "$e"
  done
  shopt -u dotglob nullglob
  if [ "$DRY" = 0 ]; then rmdir "$DATA" 2>/dev/null || true; fi
else
  echo "   absent: $DATA"
fi

say "6/6 Clean up .bashrc and sudoers"
if [ -f "$RC" ] && grep -qxF "$RC_MARK" "$RC"; then
  mapfile -t L < "$RC"
  OUT=() i=0 n=${#L[@]} removed=0
  while [ "$i" -lt "$n" ]; do
    if [ "${L[$i]}" = "$RC_MARK" ] && [ "$((i + 1))" -lt "$n" ] && [ "${L[$((i + 1))]}" = "$RC_LINE" ]; then
      i=$((i + 2))
      if [ "$i" -lt "$n" ] && [ -z "${L[$i]}" ]; then i=$((i + 1)); fi
      removed=1
      continue
    fi
    OUT+=("${L[$i]}")
    i=$((i + 1))
  done
  if [ "$removed" = 1 ]; then
    if [ "$DRY" = 1 ]; then
      echo "   [dry-run] remove two PATH lines from $RC"
    else
      tmp="$(mktemp)"
      if [ "${#OUT[@]}" -gt 0 ]; then printf '%s\n' "${OUT[@]}" > "$tmp"; else : > "$tmp"; fi
      cat "$tmp" > "$RC"
      rm -f "$tmp"
      echo "   PATH lines removed from $RC"
    fi
  else
    echo "   $RC: marker present but its form was changed, left alone"
  fi
else
  echo "   $RC: no installer lines"
fi

if command -v sudo >/dev/null 2>&1; then
  if sudo -n test -e "$SUDOERS" 2>/dev/null; then
    act sudo -n rm -f "$SUDOERS"
    if [ "$DRY" = 0 ]; then echo "   removed: $SUDOERS"; fi
  elif sudo -n true 2>/dev/null; then
    echo "   absent: $SUDOERS"
  elif [ -t 0 ] && [ "$DRY" = 0 ]; then
    echo "   sudo needs a password to check $SUDOERS"
    sudo rm -f "$SUDOERS" && echo "   removed (if present): $SUDOERS"
  else
    warn "cannot check $SUDOERS without a password. Run manually: sudo rm -f $SUDOERS"
    LEFT+=("$SUDOERS (not checked)")
  fi
else
  warn "sudo not found; check $SUDOERS manually"
fi

if [ "$DRY" = 1 ]; then
  say "Dry run finished: nothing was changed."
  exit 0
fi
for u in "${SERVICES[@]}"; do if exists "$UNITS/$u"; then LEFT+=("$UNITS/$u"); fi; done
for p in "$BIN/terminal-mcp" "$CONF"; do if exists "$p"; then LEFT+=("$p"); fi; done
if [ "$KEEP_NOTES" = 0 ] && [ "$KEEP_TRASH" = 0 ] && exists "$DATA"; then LEFT+=("$DATA"); fi

echo
if [ "${#LEFT[@]}" -gt 0 ]; then
  warn "Not fully clean:"
  for p in "${LEFT[@]}"; do echo "   - $p" >&2; done
  exit 1
fi
say "terminal-mcp has been removed."
echo "Notes:"
echo "   - Linger (loginctl enable-linger) was enabled by the installer and is not revoked, because it may have been active before."
echo "     To revoke it: loginctl disable-linger \"\$USER\""
echo "   - PATH in shells that are already open still contains ~/.local/bin until the terminal is closed."
echo "   - This source folder ($KIT) is not deleted."
