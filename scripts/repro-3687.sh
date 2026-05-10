#!/usr/bin/env bash
# repro-3687.sh — reproduce GH#3687 on the current bd binary.
#
# Background: when the dolt sql-server is running but the PID file
# .beads/dolt-server.pid is missing or stale, older bd builds reported
# "Dolt server: not running" from `bd dolt status` and "server not
# running" from `bd dolt stop`, even though the server process was
# alive on the configured port. The fix in this branch surfaces an
# actionable diagnostic message instead, with a copy-pasteable cleanup
# command and a pointer to `bd dolt killall --force-port`.
#
# This script:
#   1. Initializes a throwaway bd workspace.
#   2. Starts the local Dolt server.
#   3. Captures the running PID/port.
#   4. Removes the PID file to simulate the GH#3687 condition.
#   5. Verifies that `bd dolt status` and `bd dolt stop` both surface
#      the diagnostic instead of the silent false-negative.
#   6. Verifies that `bd dolt killall --force-port <PORT>` cleans up.
#   7. Re-runs `bd dolt status` to confirm the workspace is clean.
#
# Exit code 0 = fix is working. Non-zero = regression.
#
# Requirements: dolt in PATH, lsof in PATH (Unix), kill in PATH.

set -euo pipefail

BD="${BD:-bd}"
WORK="$(mktemp -d -t bd-repro-3687.XXXXXX)"
trap 'cleanup' EXIT

cleanup() {
    set +e
    if [[ -n "${PORT:-}" ]]; then
        # Best-effort: stop anything still listening on PORT.
        if command -v lsof >/dev/null 2>&1; then
            local pids
            pids=$(lsof -ti ":$PORT" -sTCP:LISTEN 2>/dev/null || true)
            if [[ -n "$pids" ]]; then
                kill $pids 2>/dev/null || true
                sleep 1
                kill -9 $pids 2>/dev/null || true
            fi
        fi
    fi
    rm -rf "$WORK"
}

step() {
    printf '\n=== %s ===\n' "$*"
}

fail() {
    printf '\n[FAIL] %s\n' "$*" >&2
    exit 1
}

ok() {
    printf '[ ok ] %s\n' "$*"
}

cd "$WORK"

step "1. Initialize bd workspace (server mode)"
"$BD" init --server --reinit-local --non-interactive --prefix repro >/dev/null 2>&1

step "2. Start the local Dolt server"
"$BD" dolt start >/dev/null 2>&1 || true

PID_FILE=".beads/dolt-server.pid"
PORT_FILE=".beads/dolt-server.port"
if [[ ! -f "$PID_FILE" ]]; then
    fail "PID file $PID_FILE was not created after 'bd dolt start'"
fi
PID=$(cat "$PID_FILE")
PORT=$(cat "$PORT_FILE")
ok "server up: PID=$PID, port=$PORT"

step "3. Confirm server is responding (sanity)"
if ! "$BD" dolt status 2>&1 | grep -q "Dolt server: running"; then
    fail "expected baseline 'Dolt server: running' before removing PID file"
fi

step "4. Simulate GH#3687: remove PID file while server keeps running"
rm -f "$PID_FILE"
if ! kill -0 "$PID" 2>/dev/null; then
    fail "test prereq broken: server PID $PID died unexpectedly"
fi
ok "PID file removed; PID $PID still alive on port $PORT"

step "5a. 'bd dolt status' must surface the diagnostic"
STATUS_OUT=$("$BD" dolt status 2>&1 || true)
echo "$STATUS_OUT"
if ! grep -q "bd dolt killall --force-port $PORT" <<<"$STATUS_OUT"; then
    fail "bd dolt status did not include the missing-PID-file diagnostic"
fi
ok "status surfaced the diagnostic"

step "5b. 'bd dolt stop' must surface the diagnostic and exit non-zero"
set +e
STOP_OUT=$("$BD" dolt stop 2>&1)
STOP_RC=$?
set -e
echo "$STOP_OUT"
if [[ $STOP_RC -eq 0 ]]; then
    fail "bd dolt stop returned 0 in the GH#3687 case; expected non-zero"
fi
if ! grep -q "bd dolt killall --force-port $PORT" <<<"$STOP_OUT"; then
    fail "bd dolt stop did not include the missing-PID-file diagnostic"
fi
ok "stop surfaced the diagnostic and exited non-zero"

step "6. 'bd dolt killall --force-port' should clean up"
KILLALL_OUT=$("$BD" dolt killall --force-port "$PORT" 2>&1)
echo "$KILLALL_OUT"
if ! grep -q "Killed dolt sql-server on port $PORT" <<<"$KILLALL_OUT"; then
    fail "bd dolt killall --force-port did not report killing the server"
fi
sleep 1
if kill -0 "$PID" 2>/dev/null; then
    fail "PID $PID still alive after 'bd dolt killall --force-port $PORT'"
fi
ok "killall freed the port"

step "7. Final 'bd dolt status' should report not running cleanly"
FINAL_OUT=$("$BD" dolt status 2>&1)
echo "$FINAL_OUT"
if ! grep -q "Dolt server: not running" <<<"$FINAL_OUT"; then
    fail "expected 'not running' after killall, got: $FINAL_OUT"
fi
if grep -q "bd dolt killall --force-port" <<<"$FINAL_OUT"; then
    fail "diagnostic still appearing after cleanup; port may still be held"
fi
ok "workspace is clean"

printf '\nAll checks passed. GH#3687 diagnostic flow is healthy.\n'
