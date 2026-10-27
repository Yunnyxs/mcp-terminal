step_path() {
  local rc="$HOME/.bashrc"
  say "PATH for terminal-mcp"
  if [ -f "$rc" ] && grep -qF '.local/bin' "$rc"; then
    echo "   already present in $rc"
    return
  fi
  touch "$rc"
  {
    printf '# terminal-mcp: add ~/.local/bin to PATH (must be at the top, before the non-interactive return)\n'
    printf 'export PATH="$HOME/.local/bin:$PATH"\n\n'
    cat "$rc"
  } > "$rc.tmp.$$"
  mv -f "$rc.tmp.$$" "$rc"
  echo "   added to $rc (open a new terminal or run: source ~/.bashrc)"
}
