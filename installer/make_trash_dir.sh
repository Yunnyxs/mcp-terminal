# shellcheck shell=bash
make_trash_dir() {
  mkdir -p "$TRASHDIR"
  chmod 700 "$TRASHDIR"
  echo "   $TRASHDIR (mode 700, Trash)"
}
