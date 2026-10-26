# shellcheck shell=bash
wait_server_ready() {
  local code=""
  for _ in $(seq 1 20); do
    code="$(curl -s -o /dev/null -w '%{http_code}' -X POST \
      -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
      -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"install","version":"1"}}}' \
      "http://127.0.0.1:$PORT/$TOKEN/mcp" || true)"
    [ "$code" = 200 ] && break
    sleep 1
  done
  [ "$code" = 200 ] || die "the server is not responding. Check the log: journalctl --user -u terminal-mcp -n 30"
  echo "   server responded with HTTP 200"
}
