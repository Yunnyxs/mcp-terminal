# shellcheck shell=bash
step_cloudflared() {
  local arch
  say "3/6 cloudflared"
  if command -v cloudflared >/dev/null 2>&1; then
    CF="$(command -v cloudflared)"
  elif [ -x "$BIN/cloudflared" ]; then
    CF="$BIN/cloudflared"
  else
    case "$(uname -m)" in
      x86_64|amd64) arch=amd64 ;;
      aarch64|arm64) arch=arm64 ;;
      *) die "Architecture $(uname -m) is not supported automatically. Install cloudflared manually." ;;
    esac
    echo "   downloading cloudflared ($arch) to $BIN ..."
    curl -fsSL -o "$BIN/cloudflared.tmp" \
      "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-$arch" \
      || die "Failed to download cloudflared. Check your internet connection."
    chmod 755 "$BIN/cloudflared.tmp"
    mv -f "$BIN/cloudflared.tmp" "$BIN/cloudflared"
    CF="$BIN/cloudflared"
  fi
  echo "   $CF"
}
