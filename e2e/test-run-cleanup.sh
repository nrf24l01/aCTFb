#!/usr/bin/env bash
# Deterministic tests for e2e/lib/run-cleanup.sh.
#
# No Docker daemon is used: a stub `docker` on PATH keeps container and volume
# state in files and records every call, so each ownership rule can be asserted
# exactly. Nothing here touches real containers.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=e2e/lib/run-cleanup.sh
source "$ROOT/e2e/lib/run-cleanup.sh"

passes=0
failures=0
pass() { printf 'PASS %s\n' "$1"; passes=$((passes + 1)); }
fail() { printf 'FAIL %s\n' "$1" >&2; failures=$((failures + 1)); }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/actfb-cleanup-test.XXXXXX")"
# Cleanup is extended once the outside fixture exists; both paths are removed
# exactly, never by pattern.
OUTSIDE_ROOT=""
trap 'rm -rf -- "$WORK"; [[ -n "${OUTSIDE_ROOT:-}" ]] && rm -rf -- "$OUTSIDE_ROOT"' EXIT

# --- stub docker ------------------------------------------------------------
mkdir -p "$WORK/bin"
cat >"$WORK/bin/docker" <<'STUB'
#!/usr/bin/env bash
# State lives in $MOCK_STATE: containers, volumes, calls.
set -uo pipefail
state="${MOCK_STATE:?}"
cmd="${1:-}"
shift || true
case "$cmd" in
  ps)
    # ps -aq
    [[ "${1:-}" == "-aq" ]] || { echo "stub docker: unsupported ps args" >&2; exit 2; }
    cat "$state/containers"
    ;;
  volume)
    case "${1:-}" in
      ls) cat "$state/volumes" ;;
      rm) shift; printf 'volume rm %s\n' "$*" >>"$state/calls"
          for v in "$@"; do grep -vxF -- "$v" "$state/volumes" >"$state/volumes.tmp" 2>/dev/null || true; mv "$state/volumes.tmp" "$state/volumes"; done ;;
      *) echo "stub docker: unsupported volume $1" >&2; exit 2 ;;
    esac
    ;;
  rm)
    # rm -f ID...
    [[ "${1:-}" == "-f" ]] && shift
    printf 'rm %s\n' "$*" >>"$state/calls"
    for c in "$@"; do grep -vxF -- "$c" "$state/containers" >"$state/containers.tmp" 2>/dev/null || true; mv "$state/containers.tmp" "$state/containers"; done
    ;;
  *) echo "stub docker: unsupported command $cmd" >&2; exit 2 ;;
esac
STUB
chmod +x "$WORK/bin/docker"
export PATH="$WORK/bin:$PATH"

new_state() {
  rm -rf "$WORK/state"
  mkdir -p "$WORK/state"
  : >"$WORK/state/calls"
  export MOCK_STATE="$WORK/state"
}

set_containers() { printf '%s\n' "$@" >"$WORK/state/containers"; }
set_volumes() { printf '%s\n' "$@" >"$WORK/state/volumes"; }
snapshot() { printf '%s\n' "$@" >"$WORK/before-containers.txt"; printf '' >"$WORK/before-volumes.txt"; }

containers_now() { cat "$WORK/state/containers"; }
calls_now() { cat "$WORK/state/calls"; }

# --- 1. manual host: bystander survives ------------------------------------
new_state
snapshot pre1
set_containers pre1 own-svc bystander
(
  unset GITHUB_ACTIONS RUNNER_ENVIRONMENT CI
  run_cleanup_unclassified "$WORK/before-containers.txt" "$WORK/before-volumes.txt"
) >/dev/null 2>"$WORK/err1"
rc=$?
if [[ "$rc" -ne 0 ]]; then pass 'manual host: unclassified cleanup reports refusal'; else fail 'manual host: cleanup claimed success while leaving resources'; fi
if containers_now | grep -qx 'bystander'; then pass 'manual host: bystander container survives'; else fail 'manual host: bystander container was deleted'; fi
if containers_now | grep -qx 'pre1'; then pass 'manual host: pre-existing container survives'; else fail 'manual host: pre-existing container was deleted'; fi
if grep -q 'rm ' "$WORK/state/calls"; then fail 'manual host: docker rm was invoked'; else pass 'manual host: no docker rm invoked'; fi

# --- 2. CI=true alone must not authorise deletion -------------------------
new_state
snapshot pre1
set_containers pre1 own-svc bystander
(
  unset GITHUB_ACTIONS RUNNER_ENVIRONMENT
  CI=true run_cleanup_unclassified "$WORK/before-containers.txt" "$WORK/before-volumes.txt"
) >/dev/null 2>&1
if containers_now | grep -qx 'bystander'; then pass 'CI=true without GITHUB_ACTIONS: bystander survives'; else fail 'CI=true alone authorised deletion'; fi

# --- 3. self-hosted runner marker denies ----------------------------------
new_state
snapshot pre1
set_containers pre1 own-svc bystander
(
  GITHUB_ACTIONS=true RUNNER_ENVIRONMENT=self-hosted \
    run_cleanup_unclassified "$WORK/before-containers.txt" "$WORK/before-volumes.txt"
) >/dev/null 2>&1
if containers_now | grep -qx 'bystander'; then pass 'self-hosted runner: bystander survives'; else fail 'self-hosted runner authorised deletion'; fi

# --- 4. hosted ephemeral: new IDs removed, pre-existing kept --------------
new_state
snapshot pre1
set_containers pre1 own-svc bystander
(
  GITHUB_ACTIONS=true RUNNER_ENVIRONMENT=github-hosted \
    run_cleanup_unclassified "$WORK/before-containers.txt" "$WORK/before-volumes.txt"
) >/dev/null 2>&1
rc=$?
if [[ "$rc" -eq 0 ]]; then pass 'hosted runner: cleanup succeeds'; else fail 'hosted runner: cleanup returned non-zero'; fi
if containers_now | grep -qx 'own-svc'; then fail 'hosted runner: own service container left behind'; else pass 'hosted runner: own service container removed'; fi
if containers_now | grep -qx 'bystander'; then fail 'hosted runner: new container left behind'; else pass 'hosted runner: new container removed'; fi
if containers_now | grep -qx 'pre1'; then pass 'hosted runner: pre-existing container preserved'; else fail 'hosted runner: pre-existing container removed'; fi

# --- 5. hosted runner: volumes stay namespace-filtered --------------------
new_state
snapshot pre1
set_containers pre1
set_volumes actfb-run-vol other-vol
(
  GITHUB_ACTIONS=true RUNNER_ENVIRONMENT=github-hosted \
    run_cleanup_unclassified "$WORK/before-containers.txt" "$WORK/before-volumes.txt"
) >/dev/null 2>&1
if grep -qx 'actfb-run-vol' "$WORK/state/volumes" 2>/dev/null; then fail 'hosted runner: own actfb volume left behind'; else pass 'hosted runner: own actfb volume removed'; fi
if grep -qx 'other-vol' "$WORK/state/volumes" 2>/dev/null; then pass 'hosted runner: non-actfb volume untouched'; else fail 'hosted runner: non-actfb volume deleted'; fi

# --- 6. exact owned removal is safe on a manual host ----------------------
new_state
set_containers own-clamav own-pg bystander
(
  unset GITHUB_ACTIONS RUNNER_ENVIRONMENT
  run_cleanup_owned_containers own-clamav own-pg
) >/dev/null 2>&1
if containers_now | grep -qx 'own-clamav'; then fail 'owned removal: named container not removed on manual host'; else pass 'owned removal: named containers removed on manual host'; fi
if containers_now | grep -qx 'bystander'; then pass 'owned removal: unrelated container untouched'; else fail 'owned removal: unrelated container deleted'; fi

# --- 7. workdir removal only under a harness temp root -------------------
# The negative fixture must be a path that is genuinely outside every accepted
# temp root, but must never be the repository or a broad user directory: the
# guard's whole purpose is to refuse `rm -rf`, and a test that points at real
# data would be the very hazard it is checking for. So create a dedicated,
# empty, throwaway directory under a base that is outside the temp roots and
# remove exactly that directory on exit.
fixture_base="${HOME:-/var/tmp}"
if ! mkdir -p "$fixture_base" 2>/dev/null || [[ ! -w "$fixture_base" ]]; then
  fixture_base="/var/tmp"
fi
OUTSIDE_ROOT="$(mktemp -d "$fixture_base/.actfb-cleanup-fixture.XXXXXX")"

if run_cleanup_path_is_temp_owned "$(cd "$OUTSIDE_ROOT" && pwd -P)"; then
  fail "fixture base ${fixture_base} is inside a harness temp root; the negative case cannot be trusted"
else
  pass "negative fixture is outside every harness temp root (${fixture_base})"
fi

mkdir -p "$OUTSIDE_ROOT/docs"
printf 'keep me\n' >"$OUTSIDE_ROOT/docs/owned.txt"
if ( unset GITHUB_ACTIONS; run_cleanup_remove_workdir "$OUTSIDE_ROOT" ) >/dev/null 2>&1; then
  fail 'workdir: non-temp path was accepted for deletion'
else
  pass 'workdir: non-temp path refused'
fi
if [[ -f "$OUTSIDE_ROOT/docs/owned.txt" ]]; then
  pass 'workdir: non-temp path and its contents left intact'
else
  fail 'workdir: non-temp path contents were deleted'
fi
rm -rf -- "$OUTSIDE_ROOT"
OUTSIDE_ROOT=""

# A workdir that does not exist is a no-op, not an error.
if run_cleanup_remove_workdir "$WORK/definitely-absent" >/dev/null 2>&1; then
  pass 'workdir: absent path is a no-op'
else
  fail 'workdir: absent path returned an error'
fi

# The harness's own temp workdir is still removed, under both temp conventions.
tempdir="$(run_cleanup_temp_root)/actfb-cleanup-owned-$$"
mkdir -p "$tempdir/nested"
run_cleanup_remove_workdir "$tempdir" >/dev/null 2>&1
if [[ -d "$tempdir" ]]; then fail 'workdir: own temp dir not removed'; else pass 'workdir: own temp dir removed'; fi

# The fixed /tmp/opencode default must be accepted even when TMPDIR points
# somewhere else, because the runners default their workdir there.
fixed_default="/tmp/opencode/actfb-cleanup-fixed-$$"
mkdir -p "$fixed_default"
run_cleanup_remove_workdir "$fixed_default" >/dev/null 2>&1
if [[ -d "$fixed_default" ]]; then fail 'workdir: /tmp/opencode default not removed'; else pass 'workdir: /tmp/opencode default removed'; fi

# --- 8. empty difference set is a no-op on any host ----------------------
new_state
snapshot pre1
set_containers pre1
(
  unset GITHUB_ACTIONS RUNNER_ENVIRONMENT
  run_cleanup_unclassified "$WORK/before-containers.txt" "$WORK/before-volumes.txt"
) >/dev/null 2>&1
rc=$?
if [[ "$rc" -eq 0 ]]; then pass 'no new resources: cleanup succeeds everywhere'; else fail 'no new resources: cleanup returned non-zero'; fi

printf '\n%d checks passed, %d failed\n' "$passes" "$failures"
[[ "$failures" -eq 0 ]]
