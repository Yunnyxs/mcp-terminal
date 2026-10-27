make_notes_dir() {
  mkdir -p "$NOTESDIR"
  chmod 700 "$NOTESDIR"
  echo "   $NOTESDIR (mode 700, notes)"
}
