#!/usr/bin/env bash
set -euo pipefail

KIT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$HOME/.local/bin"
CONF="$HOME/.config/terminal-mcp"
UNITS="$HOME/.config/systemd/user"
LOGDIR="$HOME/.mcp-terminal"
TRASHDIR="$LOGDIR/Trash"
NOTESDIR="$LOGDIR/notes"
DEFAULT_PORT=8000

for f in "$KIT"/installer/*.sh; do
  # shellcheck source=/dev/null
  source "$f"
done

main "$@"
