step_build() {
  say "1/6 Build terminal-mcp"
  (cd "$KIT" && go build -o "$BIN/terminal-mcp" ./cmd/terminal-mcp)
  echo "   $BIN/terminal-mcp"
  ln -sf terminal-mcp "$BIN/tmcp"
  echo "   $BIN/tmcp (short alias)"
}
