#!/usr/bin/env bash
# Regression tests for e2e/lib/service-lifecycle.sh.
#
# Proves that successful, failed and interrupted harness exits leave zero owned
# child processes, including a descendant that ignores TERM (KILL fallback).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LIB="$ROOT/e2e/lib/service-lifecycle.sh"
WORK="$(mktemp -d /tmp/opencode/service-lifecycle.XXXXXX)"
TOKEN="actfb-lifecycle-$$"
FAILED=0

cleanup() {
  rm -rf "$WORK"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  FAILED=1
}

assert_gone() {
  local pid="$1" label="$2"
  if [[ -n "$pid" && -d "/proc/${pid}" ]]; then
    fail "${label}: pid ${pid} survived"
  fi
}

wait_for_file() {
  local file="$1" attempt
  for attempt in $(seq 1 50); do
    [[ -s "$file" ]] && return 0
    sleep 0.05
  done
  return 1
}

run_case() {
  local name="$1" expected_status="$2"
  local child_file="$WORK/${name}.child"
  local script="$WORK/${name}.sh"
  cat >"$script" <<EOF
#!/usr/bin/env bash
set -euo pipefail
set -m
source "$LIB"
cleanup() {
  services_stop_all
}
lifecycle_traps cleanup
service_start probe "$WORK/${name}.log" \\
  bash -c 'sleep 300 & echo \$! > "\$1"; wait' _ "$child_file" >/dev/null
if [[ "$name" == "signal" ]]; then
  service_start hold "$WORK/${name}-hold.log" sleep 300 >/dev/null
  wait "\$SERVICE_LAST_PID"
fi
if [[ "$name" == "failure" ]]; then
  exit 9
fi
exit "$expected_status"
EOF
  chmod +x "$script"

  if [[ "$name" == "signal" ]]; then
    "$script" &
    local harness_pid=$!
    wait_for_file "$child_file" || fail "${name}: child did not start"
    kill -TERM "$harness_pid"
    local status=0
    wait "$harness_pid" || status=$?
    if [[ "$status" -ne 143 ]]; then
      fail "${name}: harness exit ${status}, want 143"
    fi
  else
    local status=0
    "$script" || status=$?
    if [[ "$status" -ne "$expected_status" ]]; then
      fail "${name}: harness exit ${status}, want ${expected_status}"
    fi
  fi

  local child_pid=""
  [[ -s "$child_file" ]] && child_pid="$(cat "$child_file")"
  assert_gone "$child_pid" "$name child"
  if pgrep -f -- "$child_file" >/dev/null 2>&1; then
    fail "${name}: matching child process remains"
  fi
}

# 1. Successful harness exit preserves 0 and stops the service tree.
run_case success 0
# 2. Failed harness exit preserves the exact failure status.
run_case failure 9
# 3. SIGTERM interruption preserves 143 and stops the service tree.
run_case signal 0

# 4. A TERM-ignoring descendant forces the bounded KILL fallback.
kill_probe="$WORK/killprobe"
kill_script="$WORK/kill.sh"
cat >"$kill_script" <<EOF
#!/usr/bin/env bash
set -euo pipefail
set -m
source "$LIB"
cleanup() { services_stop_all; }
lifecycle_traps cleanup
service_start stubborn "$WORK/kill.log" bash -c 'trap "" TERM; sleep 300 & echo \$! > "$kill_probe"; wait' >/dev/null
exit 0
EOF
chmod +x "$kill_script"
kill_status=0
"$kill_script" || kill_status=$?
if [[ "$kill_status" -ne 0 ]]; then
  fail "kill fallback: harness exit ${kill_status}, want 0"
fi
kill_child=""
[[ -s "$kill_probe" ]] && kill_child="$(cat "$kill_probe")"
assert_gone "$kill_child" "kill fallback child"

# 5. PID-reuse protection must not signal a mismatched start time.
bash -c '
set -euo pipefail
source "$1"
sleep 300 &
pid=$!
SERVICE_PIDS=("$pid")
SERVICE_STARTTIMES=("999999999")
service_stop "$pid" "999999999"
kill -0 "$pid"
kill "$pid" 2>/dev/null || true
' _ "$LIB" >/dev/null || fail "PID-reuse guard signalled a live probe"

if [[ "$FAILED" -ne 0 ]]; then
  echo "service-lifecycle regression FAILED" >&2
  exit 1
fi
echo "service-lifecycle regression PASS (success/failure/signal/KILL/PID-reuse)"