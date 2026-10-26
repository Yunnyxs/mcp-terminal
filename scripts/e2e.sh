#!/usr/bin/env bash
set -u
umask 077

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
REAL_HOME=$HOME
BASE=${E2E_TMP:-${TMPDIR:-/tmp}}
W=$(mktemp -d "$BASE/tmcp-e2e.XXXXXX") || { echo "failed to create the working directory in $BASE"; exit 2; }
PASS=0; FAIL=0; SKIP=0
HTTP_PID=""

ok()   { PASS=$((PASS+1)); echo "  ok   $1"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL $1"; [ -n "${2:-}" ] && echo "       $2" | head -8; }
skip() { SKIP=$((SKIP+1)); echo "  skip $1"; }
check() {
  local name=$1 want=$2 sub=$3; shift 3
  local out rc
  out=$("$@" 2>&1); rc=$?
  if [ "$rc" != "$want" ]; then bad "$name (exit $rc, expected $want)" "$out"; return; fi
  if [ -n "$sub" ] && ! grep -qF -- "$sub" <<<"$out"; then bad "$name (does not contain: $sub)" "$out"; return; fi
  ok "$name"
}
has()   { if grep -qF -- "$3" "$2"; then ok "$1"; else bad "$1 (does not contain: $3)" "$(head -c 400 "$2")"; fi; }
lacks() { if grep -qF -- "$3" "$2"; then bad "$1 (contains: $3)" "$(head -c 400 "$2")"; else ok "$1"; fi; }
is()    { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "got: $2 | expected: $3"; fi; }

cleanup() {
  [ -n "$HTTP_PID" ] && kill "$HTTP_PID" 2>/dev/null
  [ -x "${T:-}" ] && "$T" unload --all >/dev/null 2>&1
  systemctl --user stop 'tmcp-mcp-e2e-*' >/dev/null 2>&1
  systemctl --user reset-failed 'tmcp-mcp-e2e-*' >/dev/null 2>&1
  if [ "${E2E_KEEP:-0}" = 1 ]; then echo "working directory kept: $W"; else rm -rf "$W"; fi
}
trap cleanup EXIT

command -v python3 >/dev/null || { echo "python3 is required"; exit 2; }
command -v curl >/dev/null || { echo "curl is required"; exit 2; }
command -v go >/dev/null || { echo "go is required"; exit 2; }

echo "== build"
GOCACHE_REAL=$(go env GOCACHE); GOMODCACHE_REAL=$(go env GOMODCACHE); GOPATH_REAL=$(go env GOPATH)
mkdir -p "$W/bin"
touch "$W/marker"
( cd "$ROOT" && go build -o "$W/bin/terminal-mcp" ./cmd/terminal-mcp && go build -o "$W/bin/fakemcp" ./cmd/fakemcp ) \
  || { echo "build failed"; exit 2; }
T=$W/bin/terminal-mcp
FAKE=$W/bin/fakemcp
ok "build terminal-mcp and fakemcp"

sys_snapshot() {
  {
    sha256sum "$REAL_HOME/.local/bin/terminal-mcp" 2>/dev/null
    sha256sum "$REAL_HOME/.config/terminal-mcp/mcps.json" "$REAL_HOME/.config/terminal-mcp/config.env" 2>/dev/null
    ls -1 "$REAL_HOME/.mcp-terminal/run" 2>/dev/null
    ls -1 "$REAL_HOME/.mcp-terminal/mcp/out" 2>/dev/null
    systemctl --user show -p MainPID --value terminal-mcp.service 2>/dev/null
  } | sha256sum | cut -c1-16
}
SNAP_BEFORE=$(sys_snapshot)

export HOME=$W/home TMCP_DIR=$W/data TMCP_MCPS=$W/data/mcps.json
mkdir -p "$HOME" "$TMCP_DIR"
for v in $(env | grep -o '^TERMINAL_MCP_[A-Za-z_]*'); do unset "$v"; done
unset TMCP_SPAWN TMCP_AUTOLOAD
export TERMINAL_MCP_SHELL=/bin/bash
export GOCACHE=$GOCACHE_REAL GOMODCACHE=$GOMODCACHE_REAL GOPATH=$GOPATH_REAL GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-buildvcs=false
export SECRET_IN_HOST=leak-should-not-reach-child
ME=$(id -un)

stdio() {
  local prefix=$1; shift
  timeout 120 python3 -I - "$prefix" "$T" "$@" <<'PY'
import json, os, subprocess, sys
prefix, exe, *rest = sys.argv[1:]
calls = list(zip(rest[0::2], rest[1::2]))
par = os.environ.get("STDIO_PAR") == "1"
p = subprocess.Popen([exe, "serve", "--stdio"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                     stderr=subprocess.DEVNULL, text=True, bufsize=1)
def send(o):
    p.stdin.write(json.dumps(o) + "\n"); p.stdin.flush()
pending = {}
def recv(i):
    while i not in pending:
        line = p.stdout.readline()
        if not line:
            raise SystemExit("server closed stdout before answering id %d" % i)
        m = json.loads(line)
        if "id" in m:
            pending[m["id"]] = m
    return pending.pop(i)
send({"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": {
    "protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "e2e", "version": "1"}}})
recv(0)
send({"jsonrpc": "2.0", "method": "notifications/initialized"})
def req(i, tool, args):
    send({"jsonrpc": "2.0", "id": i, "method": "tools/call", "params": {"name": tool, "arguments": json.loads(args)}})
def save(i, r):
    if "error" in r:
        text, err = r["error"].get("message", ""), "1"
    else:
        res = r.get("result") or {}
        text = "\n".join(c.get("text", "") for c in res.get("content", []))
        err = "1" if res.get("isError") else "0"
    with open("%s.%d" % (prefix, i), "w") as f: f.write(text)
    with open("%s.%d.err" % (prefix, i), "w") as f: f.write(err)
if par:
    for i, (tool, args) in enumerate(calls, 1): req(i, tool, args)
    for i in range(1, len(calls) + 1): save(i, recv(i))
else:
    for i, (tool, args) in enumerate(calls, 1):
        req(i, tool, args); save(i, recv(i))
p.stdin.close()
try: p.wait(timeout=15)
except subprocess.TimeoutExpired: p.kill()
PY
}
stored_path() { sed -n 's/.*stored output[^:]*: \(\/[^]]*\)\]$/\1/p' "$1" | tail -1; }

echo "== clean state (no MCP servers registered)"
check "version" 0 "terminal-mcp" "$T" version
check "unknown command" 2 "unknown command" "$T" bogus
check "mcp ls without config" 0 "(empty" "$T" mcp ls
check "loaded without MCP servers" 0 "no MCP servers" "$T" loaded
check "unload --all without MCP servers" 0 "no MCP servers" "$T" unload --all
[ ! -e "$TMCP_MCPS" ] && ok "mcps.json is not created on its own" || bad "mcps.json appeared unasked"

echo "== run: summary for the client, raw log"
S=$W/s1
TERMINAL_MCP_LLM_OUTPUT=1000 stdio "$S" \
  run '{"command":"echo hello"}' \
  run '{"command":"echo boom >&2; exit 3"}' \
  run '{"command":"seq 1 3000"}' \
  run '{"command":"seq 1 3000","raw":true}' \
  run '{"command":"seq 1 20000"}' \
  run '{"command":"sleep 30","timeout":1}' \
  run '{"command":"printf \"a\\r\\nb\\r\\n\""}' \
  run '{"command":"printf \"\\377\\376 bad\\n\""}' \
  run '{"command":"head -c 5000 /dev/zero | tr \"\\0\" x"}' \
  || bad "stdio session failed"
is "short exit 0: no header" "$(cat "$S.1" 2>/dev/null)" "hello"
is "failure: first line is exit N" "$(head -1 "$S.2" 2>/dev/null)" "exit 3"
has "failure: stderr is kept" "$S.2" "boom"
has "long output is summarized + marker" "$S.3" "[summarized; stored output:"
[ "$(wc -c < "$S.3")" -lt 1500 ] && ok "summary <= budget (+ status/marker)" || bad "summary too large: $(wc -c < "$S.3") bytes"
P=$(stored_path "$S.3")
if [ -n "$P" ] && [ -f "$P" ]; then
  ok "output file is stored"
  is "output file mode 600" "$(stat -c %a "$P")" "600"
  is "output file contains all 3000 lines" "$(grep -cx '[0-9]*' "$P")" "3000"
  has "output file contains the raw header" "$P" "exit 0 ("
else bad "output file is stored" "path: '$P'"; fi
is "output directory mode 700" "$(stat -c %a "$TMCP_DIR/mcp/out" 2>/dev/null)" "700"
is "raw=true: raw header" "$(head -1 "$S.4" | cut -c1-8)" "exit 0 ("
has "raw=true: output intact" "$S.4" "3000"
lacks "raw=true: no summary marker" "$S.4" "summarized"
has "output > capture limit: capture-cut marker" "$S.5" "already cut at the capture limit"
is "timeout: flagged as isError" "$(cat "$S.6.err")" "1"
has "timeout: message" "$S.6" "timeout after 1s"
is "CRLF is normalized" "$(cat "$S.7")" "$(printf 'a\nb')"
has "non-UTF8 bytes do not break the response" "$S.8" "bad"
[ "$(wc -c < "$S.9")" -lt 1500 ] && ok "very long line is truncated" || bad "5000-byte line was not truncated: $(wc -c < "$S.9")"

echo "== the activity log records the RAW result"
AL=$TMCP_DIR/activity_terminal_mcp.log
is "activity log mode 600" "$(stat -c %a "$AL" 2>/dev/null)" "600"
is "server log mode 600" "$(stat -c %a "$TMCP_DIR/terminal_mcp.log" 2>/dev/null)" "600"
python3 -I - "$AL" "$S.3" "$S.4" <<'PY' && ok "log: valid JSON per line, summarized for the client but raw in the log" || bad "activity log content"
import json, sys
log, client_summary, client_raw = sys.argv[1:]
entries = [json.loads(l) for l in open(log) if l.strip()]
assert len(entries) == 9, "entries = %d, want 9" % len(entries)
for e in entries:
    assert e["tool"] == "run" and "time" in e and "duration_ms" in e and "is_error" in e
seq = [e for e in entries if e["args"].get("command") == "seq 1 3000" and not e["args"].get("raw")][0]
assert seq["result"].startswith("exit 0 ("), seq["result"][:30]
assert seq["result"].rstrip("\n").endswith("\n3000"), "the log must contain the raw output up to the last line"
assert len(seq["result"]) > 10000 and len(open(client_summary).read()) < 1500
raw = [e for e in entries if e["args"].get("raw")][0]
assert raw["result"] == open(client_raw).read(), "raw=true: the client must match the log"
assert [e for e in entries if e["args"].get("command", "").startswith("sleep")][0]["is_error"] is True
PY

echo "== concurrent run (rawstore)"
P2=$W/s2
STDIO_PAR=1 TERMINAL_MCP_LLM_OUTPUT=1000 stdio "$P2" \
  run '{"command":"seq 1 3000"}' run '{"command":"seq 1 3000"}' run '{"command":"seq 1 3000"}' \
  run '{"command":"seq 1 3000"}' run '{"command":"seq 1 3000"}' run '{"command":"seq 1 3000"}' \
  || bad "parallel session failed"
paths=""; good=0
for i in 1 2 3 4 5 6; do
  p=$(stored_path "$P2.$i"); paths="$paths$p"$'\n'
  [ -f "$p" ] && [ "$(grep -cx '[0-9]*' "$p")" = 3000 ] && good=$((good+1))
done
is "6 concurrent calls: every file is stored intact" "$good" "6"
is "all stored file paths are unique" "$(printf '%s' "$paths" | sort -u | grep -c .)" "6"

echo "== real go test (go test filter)"
if HOME=$W/home bash -lc 'command -v go' >/dev/null 2>&1; then
  G=$W/gomod
  mkdir -p "$G/pass" "$G/fail" "$G/notest" "$G/broken"
  printf 'module e2emod\n\ngo 1.21\n' > "$G/go.mod"
  cat > "$G/pass/pass_test.go" <<'GO'
package pass

import (
	"testing"
	"time"
)

func TestA(t *testing.T) { t.Log("log from A") }

func TestB(t *testing.T) {
	t.Parallel()
	time.Sleep(10 * time.Millisecond)
	t.Run("sub1", func(t *testing.T) { t.Parallel() })
	t.Run("sub2", func(t *testing.T) { t.Parallel() })
}
GO
  cat > "$G/fail/fail_test.go" <<'GO'
package fail

import "testing"

func TestFine(t *testing.T) {}

func TestBoom(t *testing.T) {
	t.Log("context before failure")
	t.Fatalf("want 1, got 2")
}
GO
  printf 'package notest\n' > "$G/notest/x.go"
  mkdir -p "$G/bigtest"
  {
    printf 'package bigtest\n\nimport "testing"\n\n'
    for i in $(seq 1 30); do printf 'func TestP%02d(t *testing.T) { t.Log("test log %02d with extra text") }\n' "$i" "$i"; done
    printf '\nfunc TestBoom(t *testing.T) {\n\tt.Log("context before failure")\n\tt.Fatalf("want 1, got 2")\n}\n'
  } > "$G/bigtest/bigtest_test.go"
  for n in 1 2 3 4 5 6; do mkdir -p "$G/nt$n"; printf 'package nt%s\n' "$n" > "$G/nt$n/x.go"; done
  printf 'package broken\n\nfunc {\n' > "$G/broken/broken.go"
  printf 'package broken\n' > "$G/broken/broken_test.go"

  G1=$W/g1
  TERMINAL_MCP_LLM_OUTPUT=8000 stdio "$G1" \
    run "{\"command\":\"go test -v ./pass/\",\"cwd\":\"$G\"}" \
    run "{\"command\":\"go test -v ./fail/\",\"cwd\":\"$G\"}" \
    run "{\"command\":\"go test -cover ./pass/\",\"cwd\":\"$G\"}" \
    run "{\"command\":\"go test ./broken/\",\"cwd\":\"$G\"}" \
    run "{\"command\":\"go test -v ./notest/\",\"cwd\":\"$G\"}" \
    run "{\"command\":\"go test -run NoSuchTest ./pass/\",\"cwd\":\"$G\"}" \
    run "{\"command\":\"go test -v ./pass/ ./fail/\",\"cwd\":\"$G\"}" \
    run "{\"command\":\"go test -v ./pass/ | cat\",\"cwd\":\"$G\"}" \
    run "{\"command\":\"go test -v ./bigtest/\",\"cwd\":\"$G\"}" \
    run "{\"command\":\"go test ./nt1/ ./nt2/ ./nt3/ ./nt4/ ./nt5/ ./nt6/\",\"cwd\":\"$G\"}" \
    || bad "go test session failed"
  has   "go test -v passes: test count summary" "$G1.1" "[2 tests passed]"
  has   "go test -v passes: the ok line is kept" "$G1.1" "ok  "
  lacks "go test -v passes: === RUN is dropped (subtest, Parallel)" "$G1.1" "=== RUN"
  lacks "go test -v passes: --- PASS is dropped" "$G1.1" "--- PASS"
  is    "go test fails: first line is exit 1" "$(head -1 "$G1.2")" "exit 1"
  has   "go test fails: --- FAIL is kept" "$G1.2" "--- FAIL: TestBoom"
  has   "go test fails: the failure message is kept" "$G1.2" "want 1, got 2"
  has   "go test fails: the package FAIL line is kept" "$G1.2" "FAIL"
  has   "go test fails, small output: left intact (saving < minFilterGain)" "$G1.2" "=== RUN"
  has   "go test -cover: ok + coverage" "$G1.3" "coverage"
  has   "build fails: the compiler message is kept" "$G1.4" "syntax error"
  has   "build fails: [build failed] marker" "$G1.4" "build failed"
  is    "build fails: exit 1" "$(head -1 "$G1.4")" "exit 1"
  has   "one package without tests: the original line is kept (small saving)" "$G1.5" "[no test files]"
  has   "no tests to run stays readable" "$G1.6" "no tests to run"
  has   "two packages, one fails: FAIL is readable" "$G1.7" "--- FAIL: TestBoom"
  has   "two packages, one fails: the passing package is still reported" "$G1.7" "ok  "
  lacks "with a pipe: filter not used (=== RUN intact)" "$G1.8" "[2 tests passed]"
  is    "fails, large output: first line is exit 1" "$(head -1 "$G1.9")" "exit 1"
  has   "large failure: --- FAIL is kept" "$G1.9" "--- FAIL: TestBoom"
  has   "large failure: the failure message is kept" "$G1.9" "want 1, got 2"
  has   "large failure: the failing test log is kept" "$G1.9" "context before failure"
  has   "large failure: the package FAIL line is kept" "$G1.9" "FAIL	e2emod/bigtest"
  has   "large failure: passing tests are summarized" "$G1.9" "[30 tests passed]"
  lacks "large failure: === RUN is dropped" "$G1.9" "=== RUN"
  lacks "large failure: passing test logs are dropped" "$G1.9" "test log 07"
  has   "large failure: stored-file marker" "$G1.9" "[summarized; stored output:"
  gp=$(stored_path "$G1.9"); [ -f "$gp" ] && grep -q "=== RUN   TestP07" "$gp" && ok "large failure: the stored file contains the original output" || bad "large failure: the stored file lacks the original output" "$gp"
  has   "many packages without tests are summarized" "$G1.10" "[6 packages without test files]"
else
  skip "go is not on the login shell PATH: go test section skipped"
fi

echo "== read / write / edit / delete"
F=$W/f1
stdio "$F" \
  write '{"path":"notes/a.txt","content":"one\ntwo\n"}' \
  write '{"path":"notes/a.txt","content":"x"}' \
  write '{"path":"notes/a.txt","content":"new\n","overwrite":true}' \
  edit  '{"path":"notes/a.txt","old":"new","new":"edit"}' \
  read  '{"path":"notes/a.txt"}' \
  read  '{"path":"notes/a.txt","limit":2}' \
  delete '{"paths":["notes/a.txt"]}' \
  delete '{"paths":["."]}' \
  delete '{"paths":["notes/*"]}' \
  read  '{"path":"notes/missing.txt"}' \
  || bad "file tool session failed"
has "write a new file: reply" "$F.1" "wrote"
is  "write+overwrite+edit: the final content is correct (read from Trash)" "$(cat "$TMCP_DIR/Trash/files/a.txt" 2>/dev/null)" "edit"
is  "write without overwrite is rejected" "$(cat "$F.2.err")" "1"
has "write without overwrite: message" "$F.2" "already exists"
has "read: content" "$F.5" "edit"
has "read with limit: next_offset" "$F.6" "next_offset"
has "delete: moved to Trash" "$F.7" "Moved to Trash"
[ ! -e "$HOME/notes/a.txt" ] && [ -f "$TMCP_DIR/Trash/files/a.txt" ] && [ -f "$TMCP_DIR/Trash/info/a.txt.trashinfo" ] \
  && ok "delete: file + .trashinfo are in Trash" || bad "delete: Trash is incomplete"
is  "delete of the root directory is rejected" "$(cat "$F.8.err")" "1"
has "delete of the root directory: reason" "$F.8" "protected"
has "delete wildcard is not expanded" "$F.9" "wildcards are not expanded"
is  "read of a missing file: isError" "$(cat "$F.10.err")" "1"
grep -q 'one' "$AL" && ok "the log records the full write content" || bad "the write content is missing from the activity log"

echo "== sudo=true (helper fileop)"
if [ "${E2E_SUDO:-1}" = 0 ]; then
  skip "E2E_SUDO=0"
elif sudo -n -- "$T" version >/dev/null 2>&1; then
  U=$W/u1
  mkdir -p "$HOME/sudo"; printf 'start\n' > "$HOME/sudo/s.txt"
  chmod 640 "$HOME/sudo/s.txt"
  stdio "$U" \
    write '{"path":"sudo/s.txt","content":"root-write\n","overwrite":true,"sudo":true}' \
    edit  '{"path":"sudo/s.txt","old":"root","new":"edit","sudo":true}' \
    read  '{"path":"sudo/s.txt","sudo":true}' \
    read  '{"path":"/etc/sudoers"}' \
    read  '{"path":"/etc/sudoers","sudo":true}' \
    || bad "sudo session failed"
  is  "sudo write: content changed" "$(cat "$HOME/sudo/s.txt")" "edit-write"
  is  "sudo write: the owner stays the user" "$(stat -c %U "$HOME/sudo/s.txt")" "$ME"
  is  "sudo write: mode stays 640" "$(stat -c %a "$HOME/sudo/s.txt")" "640"
  has "sudo read" "$U.3" "edit-write"
  is  "without sudo: the root file is rejected" "$(cat "$U.4.err")" "1"
  has "without sudo: hint to retry with sudo=true" "$U.4" "retry with sudo=true"
  is  "sudo=true: the root file is readable" "$(cat "$U.5.err")" "0"
  has "sudo=true: sudoers content" "$U.5" "ALL"
else
  skip "passwordless sudo is not available for the test binary"
fi

echo "== HTTP server (like claude.ai)"
PORT=$(python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
TOKEN=$(python3 -I -c 'import secrets;print(secrets.token_hex(32))')
HL=$W/http.log
TERMINAL_MCP_TOKEN=$TOKEN TERMINAL_MCP_PORT=$PORT TERMINAL_MCP_HOST=127.0.0.1 TERMINAL_MCP_LLM_OUTPUT=1000 \
  "$T" serve >"$HL" 2>&1 &
HTTP_PID=$!
http() {
  curl -s -m 20 -o "$W/http.out" -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' -d "$2" "http://127.0.0.1:$PORT/$1/mcp"
}
INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}'
up=0; for _ in $(seq 1 50); do [ "$(http "$TOKEN" "$INIT")" = 200 ] && { up=1; break; }; sleep 0.2; done
[ $up = 1 ] && ok "HTTP server is up" || bad "HTTP server did not come up" "$(head -c 300 "$HL")"
has "initialize: serverInfo" "$W/http.out" "terminal-mcp"
is  "wrong token: 404" "$(http "wrong$TOKEN" "$INIT")" "404"
is  "no token: 404" "$(curl -s -m 5 -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/mcp")" "404"
is  "GET without a token: 404" "$(curl -s -m 5 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/")" "404"
code=$(http "$TOKEN" '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run","arguments":{"command":"echo http-ok"}}}')
is  "tools/call over HTTP: 200" "$code" "200"
has "tools/call over HTTP: short result" "$W/http.out" "http-ok"
lacks "tools/call over HTTP: no exit header" "$W/http.out" "exit 0"
http "$TOKEN" '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run","arguments":{"command":"seq 1 3000"}}}' >/dev/null
has "HTTP: long output is summarized" "$W/http.out" "summarized; stored output"
http "$TOKEN" '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"run","arguments":{"command":"seq 1 3000","raw":true}}}' >/dev/null
has "HTTP: raw=true intact" "$W/http.out" "3000"
lacks "HTTP: the token is not in the server log" "$HL" "$TOKEN"
lacks "HTTP: the token is not in the server log file" "$TMCP_DIR/terminal_mcp.log" "$TOKEN"
has "HTTP: request is logged without a path" "$HL" "http POST status=200"
grep -q 'http-ok' "$AL" && ok "HTTP: the call is in the activity log" || bad "the HTTP call is missing from the activity log"
kill -TERM "$HTTP_PID"; ended=""; for _ in $(seq 1 70); do kill -0 "$HTTP_PID" 2>/dev/null || { ended=1; break; }; sleep 0.1; done
if [ -n "$ended" ]; then wait "$HTTP_PID"; is "SIGTERM: exit 0" "$?" "0"; else bad "the server did not stop within 7 s after SIGTERM"; kill -9 "$HTTP_PID" 2>/dev/null; fi
HTTP_PID=""
has "shutdown is logged" "$HL" "shutdown: server stopped"

echo "== separate MCP servers: config"
M=e2e-fake; MI=e2e-idle; MB=e2e-broken
cat > "$TMCP_MCPS" <<EOF
{
  "$M":  { "command": "$FAKE", "env": { "FAKE_SECRET": "s3cret-from-config" } },
  "$MI": { "command": "$FAKE", "idle_timeout": "4s" },
  "$MB": { "command": "/nonexistent/binary" }
}
EOF
chmod 644 "$TMCP_MCPS"
check "loose permissions are rejected" 1 "too open" "$T" mcp ls
chmod 600 "$TMCP_MCPS"
check "mcp ls" 0 "$MI" "$T" mcp ls
check "mcp ls shows the env name" 0 "FAKE_SECRET" "$T" mcp ls
"$T" mcp ls 2>&1 | grep -q "s3cret-from-config" && bad "env value leaked in mcp ls" || ok "env value is not shown in mcp ls"
check "mcp add stdio" 0 "added" "$T" mcp add e2e-extra -- /bin/true
check "mcp add duplicate is rejected" 1 "already exists" "$T" mcp add e2e-extra -- /bin/true
check "mcp add --force" 0 "added" "$T" mcp add e2e-extra --force -- /bin/false
check "mcp add bad name" 2 "invalid" "$T" mcp add "../x" -- /bin/true
check "mcp add command+url is rejected" 2 "exactly one" "$T" mcp add e2e-y --url http://localhost:1/ -- /bin/true
check "mcp rm" 0 "removed" "$T" mcp rm e2e-extra
is "mcps.json stays 600 after add/rm" "$(stat -c %a "$TMCP_MCPS")" "600"

echo "== load / loaded"
check "load" 0 "loaded" "$T" load $M
check "load again = already loaded" 0 "already loaded" "$T" load $M
check "loaded" 0 "fakemcp" "$T" loaded
check "load unknown name" 1 "is not in" "$T" load e2e-nope
check "load broken binary" 1 "FAILED" "$T" load $MB
check "load bad name" 1 "invalid" "$T" load "A/b"
PID=$("$T" loaded | awk -v n=$M '$1==n{print $3}')
CG=$(cat /proc/$PID/cgroup 2>/dev/null)
if grep -q "terminal-mcp.service" <<<"$CG"; then bad "host is still in the terminal-mcp.service cgroup (it would die on restart)"; else ok "host is outside the terminal-mcp.service cgroup"; fi
is "run directory 700" "$(stat -c %a "$TMCP_DIR/run")" "700"
is "socket 600" "$(stat -c %a "$TMCP_DIR/run/$M.sock")" "600"

echo "== tools"
check "tools short" 0 "echo(text*)" "$T" tools $M
check "tools short lists 8 tools" 0 "$M: 8 tools" "$T" tools $M
check "tools detail" 0 "inputSchema" "$T" tools $M echo
check "tools detail full description" 0 "Second line of description." "$T" tools $M echo
check "tools missing tool" 1 "is not in" "$T" tools $M nope

echo "== call"
check "call echo" 0 "echo: hello" "$T" call $M echo '{"text":"hello"}'
out=$(echo '{"text":"from stdin"}' | "$T" call $M echo - 2>&1); grep -qF "echo: from stdin" <<<"$out" && ok "call via stdin" || bad "call via stdin" "$out"
check "call tool without params" 0 "" "$T" call $M image
check "call JSON non-object" 2 "JSON object" "$T" call $M echo '[1,2]'
check "call broken JSON" 2 "JSON object" "$T" call $M echo '{oops'
check "call missing tool" 1 "" "$T" call $M nope '{}'
check "call isError -> exit 1" 1 "this tool always fails" "$T" call $M fail
check "call wrong option" 2 "unknown option" "$T" call $M echo '{}' --bogus
check "call timeout" 1 "time limit" "$T" call $M sleep '{"ms":4000}' --timeout 1
check "call after a timeout is still healthy" 0 "echo: ok" "$T" call $M echo '{"text":"ok"}'
out=$("$T" call $M image 2>&1); f=$(grep -o '/[^ ]*\.png' <<<"$out" | head -1)
if [ -n "$f" ] && [ -f "$f" ] && head -c 4 "$f" | grep -q PNG; then
  ok "image is saved as a valid PNG"; is "result file 600" "$(stat -c %a "$f")" "600"
else bad "image was not saved" "$out"; fi
out=$(TERMINAL_MCP_MAX_OUTPUT=5000 "$T" call $M big '{"n":100000}' 2>&1); f=$(grep -o 'full result: [^]]*' <<<"$out" | sed 's/full result: //')
if grep -q "output truncated" <<<"$out" && [ -f "$f" ] && [ "$(stat -c %s "$f")" -ge 100000 ]; then ok "large output is truncated + the full file is stored"; else bad "output truncation" "${out:0:300}"; fi
python3 -I - "$out" <<'PY' && ok "the truncation is valid UTF-8 (no rune is cut)" || bad "the truncation is not valid UTF-8"
import sys
sys.argv[1].encode("utf-8", "surrogateescape").decode("utf-8")
PY

echo "== env isolation"
check "env from the config reaches the child" 0 "s3cret-from-config" "$T" call $M env '{"name":"FAKE_SECRET"}'
check "host env does not leak to the child" 0 "<unset>" "$T" call $M env '{"name":"SECRET_IN_HOST"}'
check "PATH is inherited" 0 "/" "$T" call $M env '{"name":"PATH"}'

echo "== parallel"
for i in 1 2 3 4 5 6; do ( "$T" call $M echo "{\"text\":\"p$i\"}" > "$W/par.$i" 2>&1 ) & done; wait
okc=0; for i in 1 2 3 4 5 6; do grep -qF "echo: p$i" "$W/par.$i" && okc=$((okc+1)); done
is "6 parallel calls all correct" "$okc" "6"

echo "== host log"
grep -q "fakemcp: ready" "$TMCP_DIR/mcp/$M.log" && ok "child stderr reaches the host log" || bad "child stderr is missing from the log"
grep -q "s3cret-from-config" "$TMCP_DIR/mcp/$M.log" "$TMCP_DIR/run"/* 2>/dev/null && bad "config secret is in the log/run" || ok "config secret is not in the log"
grep -rq "hello" "$TMCP_DIR/mcp/$M.log" && bad "call arguments are recorded in the host log" || ok "call arguments are not recorded in the host log"
is "host log 600" "$(stat -c %a "$TMCP_DIR/mcp/$M.log")" "600"

echo "== process group"
GC=$("$T" call $M spawn 2>&1 | tr -d '\n')
if kill -0 "$GC" 2>/dev/null; then ok "grandchild process is alive (pid $GC)"; else bad "grandchild is not alive" "$GC"; fi
check "unload" 0 "stopped" "$T" unload $M
sleep 1
if kill -0 "$GC" 2>/dev/null; then bad "grandchild process did NOT die after unload (pid $GC)"; kill "$GC" 2>/dev/null; else ok "grandchild process died after unload"; fi
check "unload again" 0 "was not loaded" "$T" unload $M
[ ! -e "$TMCP_DIR/run/$M.sock" ] && [ ! -e "$TMCP_DIR/run/$M.pid" ] && ok "socket+pid are cleaned up" || bad "leftover socket/pid"

echo "== autoload"
check "call loads automatically" 0 "loaded automatically" "$T" call $M echo '{"text":"auto"}'
"$T" unload $M >/dev/null 2>&1
check "--no-autoload refuses" 1 "not loaded" "$T" call $M echo '{"text":"x"}' --no-autoload
TMCP_AUTOLOAD=0 check "TMCP_AUTOLOAD=0 refuses" 1 "not loaded" "$T" call $M echo '{"text":"x"}'

echo "== child crash"
"$T" load $M >/dev/null 2>&1
"$T" call $M crash >/dev/null 2>&1
sleep 2
check "host stops when the child dies" 0 "no MCP servers" "$T" loaded
grep -q "connection to MCP ended" "$TMCP_DIR/mcp/$M.log" && ok "the stop reason is recorded in the log" || bad "the crash reason is not recorded"
check "call recovers after a crash (autoload)" 0 "echo: recovered" "$T" call $M echo '{"text":"recovered"}'

echo "== host killed forcibly (kill -9)"
HP=$("$T" loaded | awk -v n=$M '$1==n{print $3}')
CP=$(pgrep -f "$FAKE" | head -1)
GC=$("$T" call $M spawn 2>&1 | tr -d '\n')
kill -9 "$HP"; sleep 2
if [ -n "$CP" ] && kill -0 "$CP" 2>/dev/null; then bad "orphaned child MCP after the host was killed with -9 (pid $CP)"; kill "$CP" 2>/dev/null; else ok "child MCP died too"; fi
if kill -0 "$GC" 2>/dev/null; then bad "orphaned grandchild process after the host was killed with -9 (pid $GC)"; kill "$GC" 2>/dev/null; else ok "grandchild process is cleaned up too (systemd unit cgroup)"; fi
systemctl --user reset-failed 'tmcp-mcp-e2e-*' 2>/dev/null
check "recovers from a stale socket" 0 "echo: stale" "$T" call $M echo '{"text":"stale"}'

echo "== idle timeout"
check "load $MI" 0 "loaded" "$T" load $MI
sleep 7
out=$("$T" loaded 2>&1)
grep -q "^$MI" <<<"$out" && bad "$MI is still alive after idle" "$out" || ok "$MI stopped by itself after idle"
grep -q "idle exceeded" "$TMCP_DIR/mcp/$MI.log" && ok "the idle reason is recorded" || bad "the idle reason is not recorded"
"$T" load $MI >/dev/null 2>&1
for _ in 1 2 3; do sleep 2; "$T" call $MI echo '{"text":"keep"}' >/dev/null 2>&1; done
"$T" loaded 2>&1 | grep -q "^$MI" && ok "periodic calls hold off idle" || bad "$MI died although it was in use"
"$T" call $MI sleep '{"ms":7000}' --timeout 20 >/dev/null 2>&1 &
sleep 5.5
"$T" loaded 2>&1 | grep -q "^$MI" && ok "host is not idle while a long call runs" || bad "host died in the middle of a call"
wait

echo "== unload --all"
"$T" load $M $MI >/dev/null 2>&1
check "unload --all" 0 "stopped" "$T" unload --all
check "empty after unload --all" 0 "no MCP servers" "$T" loaded
sleep 1
systemctl --user list-units 'tmcp-mcp-e2e-*' --no-legend 2>/dev/null | grep -q . \
  && bad "transient systemd unit left over" "$(systemctl --user list-units 'tmcp-mcp-e2e-*' --no-legend)" \
  || ok "no transient units left"

echo "== cleanliness"
is "the real system is unchanged (installed binary, config, run, out, service PID)" "$(sys_snapshot)" "$SNAP_BEFORE"
changed=$(cd "$ROOT" && find . -newer "$W/marker" -type f -not -path './.git/*' 2>/dev/null | head -5)
[ -z "$changed" ] && ok "no file in the project directory changed" || bad "the project directory changed" "$changed"
[ -z "$(find "$HOME" "$TMCP_DIR" -user root 2>/dev/null | head -1)" ] && ok "no root-owned file left behind" || bad "root-owned files in the working directory"

echo
echo "RESULT: $PASS passed, $FAIL failed, $SKIP skipped"
[ "$FAIL" = 0 ]
