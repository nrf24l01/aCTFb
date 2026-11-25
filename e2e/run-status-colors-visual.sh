#!/usr/bin/env bash
# Bounded real-browser check for status badge colours.
#
# Starts exactly one owned Vite dev server through e2e/lib/service-lifecycle.sh
# (process-group leader + recorded /proc start time), runs a Playwright Chromium
# pass against the test-only visual fixture, then stops only that owned group.
# No backend, no database, no Docker.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_PATH="/fixtures/status-colors/index.html"
set -m
# shellcheck source=e2e/lib/service-lifecycle.sh
source "$ROOT/e2e/lib/service-lifecycle.sh"

OUT="${OUT:-/tmp/opencode/status-colors}"
LOG_DIR="${LOG_DIR:-/tmp/opencode/status-colors-logs}"
mkdir -p "$OUT" "$LOG_DIR"

PORT="$(python3 - <<'PY'
import socket
s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()
PY
)"

cleanup() {
  services_stop_all
}
lifecycle_traps cleanup

echo "vite port: $PORT"
# The Vite project root is frontend/, so the owned leader execs Bun there.
# Called without command substitution on purpose: `$(...)` runs a subshell that
# becomes the process-group leader, so the recorded PID would not own the group.
service_start vite "$LOG_DIR/vite.log" \
  bash -c "cd '$ROOT/frontend' && exec bun run dev --host 127.0.0.1 --port '$PORT' --strictPort" >/dev/null
VITE_PID="$SERVICE_LAST_PID"
VITE_STARTTIME="$(service_proc_starttime "$VITE_PID")"
echo "vite owned pid: $VITE_PID starttime: $VITE_STARTTIME"

ready=0
for _ in $(seq 1 120); do
  if curl -qfsS "http://127.0.0.1:${PORT}${FIXTURE_PATH}" -o /dev/null 2>/dev/null; then
    ready=1
    break
  fi
  sleep 0.5
done
if [[ "$ready" != "1" ]]; then
  echo "vite did not become ready; last log lines:" >&2
  tail -30 "$LOG_DIR/vite.log" >&2 || true
  exit 1
fi
echo "vite ready"

BASE_URL="http://127.0.0.1:${PORT}${FIXTURE_PATH}" \
  OUT="$OUT" \
  node "$ROOT/e2e/status-colors-visual.mjs"

# Prove the owned group is gone before the trap runs.
services_stop_all
sleep 0.3
if kill -0 -- "-${VITE_PID}" 2>/dev/null; then
  echo "FAIL: owned vite group ${VITE_PID} still alive" >&2
  exit 1
fi
echo "owned vite group ${VITE_PID} stopped (no survivors)"
