#!/usr/bin/env bash
# Docker cleanup policy for the isolated e2e harnesses.
#
# Sourced, never executed. Two rules, because a test harness must not destroy
# resources it does not own:
#
#   1. Containers and volumes it created by exact name are always safe to remove.
#   2. Anything else that appeared during the run is only removed when this is a
#      hosted, ephemeral GitHub runner, where every container on the host belongs
#      to this job. On a shared or manual host the difference set may contain a
#      concurrent `docker compose up`, a recreated builder, or an operator
#      container, so removal is refused and the leak assertion in the caller
#      reports it instead of silently deleting someone else's work.
#
# GITHUB_ACTIONS + RUNNER_ENVIRONMENT are the markers GitHub sets. `${CI:-}` is
# deliberately not used: a developer machine or a shared CI box can have CI=true
# without being disposable.

# run_cleanup_is_hosted_ephemeral -> 0 when broad removal is safe.
run_cleanup_is_hosted_ephemeral() {
  [[ "${GITHUB_ACTIONS:-}" == "true" && "${RUNNER_ENVIRONMENT:-}" == "github-hosted" ]]
}

# run_cleanup_owned_containers NAME... -> remove exact, known-owned containers.
# Idempotent and safe on any host; a missing name is not an error.
run_cleanup_owned_containers() {
  [[ "$#" -gt 0 ]] || return 0
  docker rm -f "$@" >/dev/null 2>&1 || true
}

# run_cleanup_owned_volumes NAME... -> remove exact, known-owned volumes.
run_cleanup_owned_volumes() {
  [[ "$#" -gt 0 ]] || return 0
  docker volume rm "$@" >/dev/null 2>&1 || true
}

# run_cleanup_unclassified BEFORE_CONTAINERS BEFORE_VOLUMES
#   Removes resources that appeared after the before-snapshot, but only on a
#   hosted ephemeral runner. Otherwise it reports them and changes nothing.
#   Returns 1 when unclassified resources were left in place.
run_cleanup_unclassified() {
  local before_containers="$1" before_volumes="$2"
  local new_containers new_volumes

  new_containers="$(comm -13 "$before_containers" <(docker ps -aq | sort) 2>/dev/null || true)"
  # Volumes stay filtered to the project namespace even on a hosted runner.
  new_volumes="$(comm -13 "$before_volumes" <(docker volume ls -q | sort) 2>/dev/null | grep -E '^actfb' || true)"

  [[ -n "$new_containers" || -n "$new_volumes" ]] || return 0

  if ! run_cleanup_is_hosted_ephemeral; then
    printf 'WARNING: refusing broad Docker cleanup outside a hosted ephemeral runner\n' >&2
    printf 'WARNING: containers left in place: %s\n' "${new_containers//$'\n'/ }" >&2
    printf 'WARNING: volumes left in place: %s\n' "${new_volumes//$'\n'/ }" >&2
    printf 'WARNING: remove them by hand if they belong to this run; the leak assertion will fail\n' >&2
    return 1
  fi

  if [[ -n "$new_containers" ]]; then
    # shellcheck disable=SC2086
    docker rm -f $new_containers >/dev/null 2>&1 || true
  fi
  if [[ -n "$new_volumes" ]]; then
    # shellcheck disable=SC2086
    docker volume rm $new_volumes >/dev/null 2>&1 || true
  fi
  return 0
}

# run_cleanup_remove_workdir DIR
#   Removes the harness's own scratch directory, and only that. The path is
#   resolved first and must live under the shared temp root, so a bad
#   E2E_WORKDIR cannot recursively delete a checkout.
run_cleanup_remove_workdir() {
  local dir="${1:-}"
  [[ -n "$dir" ]] || return 0
  [[ -d "$dir" ]] || return 0

  local resolved temp_root
  resolved="$(cd "$dir" 2>/dev/null && pwd -P)" || return 0
  temp_root="$(cd "${TMPDIR:-/tmp}/opencode" 2>/dev/null && pwd -P)" || temp_root="/tmp/opencode"

  case "$resolved" in
    "$temp_root"/*) ;;
    *)
      printf 'WARNING: refusing to delete a non-temp workdir: %s\n' "$resolved" >&2
      return 1
      ;;
  esac

  rm -rf -- "$resolved"
}
