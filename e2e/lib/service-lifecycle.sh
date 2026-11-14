#!/usr/bin/env bash
# Owned service lifecycle for test harnesses.
#
# Every service is started as its own process-group leader (the caller must
# enable Bash job control with `set -m`). Cleanup signals only that exact group,
# records the leader's /proc start time to avoid PID-reuse kills, waits a
# bounded time, and escalates to KILL only for survivors inside the owned group.
# It never uses broad pkill/killall and never signals the harness process group.
#
# Source this file; do not execute it directly.

SERVICE_PIDS=()
SERVICE_STARTTIMES=()
SERVICE_NAMES=()
SERVICE_LAST_PID=""

# service_proc_starttime PID -> field 22 of /proc/PID/stat (start time).
service_proc_starttime() {
  local pid="$1"
  [[ -n "$pid" && -r "/proc/${pid}/stat" ]] || return 0
  # The comm field may contain spaces/parentheses; strip through the final ') '.
  local stat
  stat="$(cat "/proc/${pid}/stat" 2>/dev/null)" || return 0
  stat="${stat##*) }"
  # After stripping, field 3 (state) is now field 1; starttime is field 20.
  awk '{print $20}' <<<"$stat"
}

# service_start NAME LOG COMMAND... -> prints the owned PID.
# The caller must have run `set -m` so this background job gets a fresh PGID.
service_start() {
  local name="$1"
  local log="$2"
  shift 2
  mkdir -p "$(dirname "$log")"
  "$@" >"$log" 2>&1 &
  local pid=$!
  local starttime
  starttime="$(service_proc_starttime "$pid")"
  SERVICE_PIDS+=("$pid")
  SERVICE_STARTTIMES+=("$starttime")
  SERVICE_NAMES+=("$name")
  SERVICE_LAST_PID="$pid"
  printf '%s' "$pid"
}

# service_stop PID STARTTIME
service_stop() {
  local pid="${1:-}"
  local recorded_start="${2:-}"
  [[ -n "$pid" ]] || return 0

  local live_start
  live_start="$(service_proc_starttime "$pid")"
  if [[ -n "$live_start" && -n "$recorded_start" && "$live_start" != "$recorded_start" ]]; then
    # The PID was reused by an unrelated process. Never signal it.
    return 0
  fi

  # The group leader may have exited while descendants remain in the owned
  # PGID; `kill -0 -- -PGID` checks the group, not just the leader.
  if [[ -n "$live_start" ]]; then
    kill -TERM -- "-${pid}" 2>/dev/null || true
  elif kill -0 -- "-${pid}" 2>/dev/null; then
    kill -TERM -- "-${pid}" 2>/dev/null || true
  else
    wait "$pid" 2>/dev/null || true
    return 0
  fi

  local attempt
  for attempt in $(seq 1 50); do
    if ! kill -0 -- "-${pid}" 2>/dev/null; then
      wait "$pid" 2>/dev/null || true
      return 0
    fi
    sleep 0.1
  done

  # Bounded escalation: only exact survivors in the recorded owned group.
  kill -KILL -- "-${pid}" 2>/dev/null || true
  for attempt in $(seq 1 20); do
    kill -0 -- "-${pid}" 2>/dev/null || break
    sleep 0.05
  done
  wait "$pid" 2>/dev/null || true
}

# services_stop_all stops in reverse start order (frontend/children first when
# the caller registers them last).
services_stop_all() {
  local index
  for ((index=${#SERVICE_PIDS[@]}-1; index>=0; index--)); do
    service_stop "${SERVICE_PIDS[$index]}" "${SERVICE_STARTTIMES[$index]}"
  done
  SERVICE_PIDS=()
  SERVICE_STARTTIMES=()
  SERVICE_NAMES=()
  SERVICE_LAST_PID=""
}

# lifecycle_traps CLEANUP_FUNCTION installs status-preserving EXIT/INT/TERM
# handlers. INT/TERM exit with the conventional 128+signal status, then EXIT
# runs cleanup exactly once and re-exits with that status.
lifecycle_traps() {
  local cleanup_fn="$1"
  trap 'status=$?; trap - EXIT INT TERM; '"${cleanup_fn}"'; exit "$status"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
}