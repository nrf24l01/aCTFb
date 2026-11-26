#!/usr/bin/env bash
# CI contract for .github/workflows/go.yml and docker-bake.hcl.
#
# Sourced by scripts/test-production-packaging.sh. Deliberately restricted to
# bash + awk + grep so it runs in the same minimal environments as the rest of
# the packaging checks (no PyYAML, no yq, no python).
#
# Two entry points:
#   ci_contract_check <workflow> <bake>     validates one workflow/bake pair
#   ci_contract_selftest <workflow> <bake>  proves every invariant is live by
#                                           mutating a temporary copy and
#                                           asserting the mutation is rejected
#
# Both set CI_CONTRACT_PASSES / CI_CONTRACT_FAILURES instead of printing a
# summary, so the caller folds the counts into its own totals.

CI_CONTRACT_PASSES=0
CI_CONTRACT_FAILURES=0

# Every image the integration job must load locally and the publish job must
# push. One list so a target can never be added to one half only.
CI_CONTRACT_TARGETS="backend notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker frontend"

_ci_pass() { printf 'PASS %s\n' "$1"; CI_CONTRACT_PASSES=$((CI_CONTRACT_PASSES + 1)); }
_ci_fail() { printf 'FAIL %s\n' "$1" >&2; CI_CONTRACT_FAILURES=$((CI_CONTRACT_FAILURES + 1)); }

# Print one job block from a workflow. Jobs live at two-space indent under
# `jobs:`; a following two-space key ends the block.
ci_job_block() {
  awk -v job="$2" '
    /^jobs:[[:space:]]*$/ { injobs = 1; next }
    injobs && $0 ~ "^  " job ":[[:space:]]*$" { inside = 1; print; next }
    inside && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { inside = 0 }
    inside { print }
  ' "$1"
}

_ci_require_in() { # block label literal
  if [[ "$1" == *"$3"* ]]; then _ci_pass "$2"; else _ci_fail "$2 (missing: $3)"; fi
}

_ci_forbid_in() { # block label literal
  if [[ "$1" == *"$3"* ]]; then _ci_fail "$2 (unexpected: $3)"; else _ci_pass "$2"; fi
}

# Line-anchored variants. These exist because a substring check can be satisfied
# by prose: a comment mentioning `push: false` would otherwise let a step that
# actually pushes pass the gate. Indentation is part of the pattern on purpose.
_ci_require_line() { # block label ere
  if printf '%s\n' "$1" | grep -Eq -- "$3"; then _ci_pass "$2"; else _ci_fail "$2 (no line matches: $3)"; fi
}

_ci_forbid_line() { # block label ere
  if printf '%s\n' "$1" | grep -Eq -- "$3"; then _ci_fail "$2 (line matches: $3)"; else _ci_pass "$2"; fi
}

# --- one workflow/bake pair -------------------------------------------------

ci_contract_check() {
  local wf="$1" bake="$2"
  CI_CONTRACT_PASSES=0
  CI_CONTRACT_FAILURES=0

  if [[ ! -f "$wf" ]]; then _ci_fail "ci contract: missing workflow $wf"; return 1; fi
  if [[ ! -f "$bake" ]]; then _ci_fail "ci contract: missing bake definition $bake"; return 1; fi

  local jobs
  jobs="$(awk '/^jobs:/{inside=1; next} inside && /^  [a-zA-Z0-9_-]+:/{gsub(/:.*/, "", $1); print $1}' "$wf")"
  if [[ "$jobs" == $'backend\nfrontend\nintegration\npublish' ]]; then
    _ci_pass 'workflow has exactly backend/frontend/integration/publish jobs'
  else
    _ci_fail "workflow jobs are not the approved four: $(printf '%s' "$jobs" | tr '\n' ' ')"
  fi

  local backend frontend integration publish
  backend="$(ci_job_block "$wf" backend)"
  frontend="$(ci_job_block "$wf" frontend)"
  integration="$(ci_job_block "$wf" integration)"
  publish="$(ci_job_block "$wf" publish)"

  # --- quality gates must keep doing their own work -------------------------
  _ci_require_in "$backend" 'backend runs the full test suite' 'go test ./... -count=1'
  _ci_require_in "$backend" 'backend runs go vet' 'go vet ./...'
  _ci_require_in "$backend" 'backend compiles every package' 'go build ./...'
  _ci_require_in "$backend" 'backend runs the race detector' 'go test -race ./... -count=1'
  _ci_require_in "$frontend" 'frontend typechecks' 'bun run typecheck'
  _ci_require_in "$frontend" 'frontend runs unit tests' 'bun run test'
  _ci_require_in "$frontend" 'frontend builds the bundle' 'bun run build'

  # --- integration is independent, and stays unprivileged ------------------
  # It checks out, installs its own toolchains and builds the images itself, so
  # it consumes nothing from the quality jobs. If a future change introduces a
  # real artifact dependency this assertion must be changed deliberately; it is
  # not meant to forbid such a design, only to make it explicit.
  _ci_forbid_line "$integration" 'integration does not wait on the quality jobs' '^    needs:'
  _ci_forbid_line "$integration" 'integration cannot write packages' '^      packages: write$'
  _ci_forbid_line "$integration" 'integration does not log in to a registry' '^        uses: docker/login-action@'
  _ci_forbid_line "$integration" 'integration never pushes images' '^ *docker push'
  _ci_forbid_line "$integration" 'integration has no hand-written buildx loop' '^ *docker buildx build'
  _ci_forbid_line "$integration" 'integration does not pass its own cache flags' '^ *--cache-from'

  # --- the integration build must be the official, bounded, load-only bake --
  _ci_require_line "$integration" 'integration builds through the official bake action' '^        uses: docker/bake-action@v7$'
  _ci_require_line "$integration" 'integration bake never pushes' '^          push: false$'
  _ci_require_line "$integration" 'integration bake loads images locally' '^          load: true$'
  _ci_require_line "$integration" 'integration buildx takes an inline daemon config' '^          buildkitd-config-inline: |$'
  _ci_require_line "$integration" 'integration bake is bounded' '^              max-parallelism = 2$'
  _ci_require_line "$integration" 'integration bake reads the shared definition' '^          files: docker-bake\.hcl$'

  local target
  for target in $CI_CONTRACT_TARGETS; do
    _ci_require_line "$integration" "integration tags actfb-${target}:ci" \
      "^            ${target}\\.tags=actfb-${target}:ci\$"
  done

  # --- the gates themselves are not allowed to disappear -------------------
  local gate
  for gate in \
    'TestMigrationsCleanSchema' \
    'TestP0AuthEmailIntegration' \
    'TestP1CoreIntegration' \
    'TestRustFSFullStackIntegration' \
    'test-production-packaging.sh' \
    'test-production-auth-proxy.sh' \
    'run-e2e.sh' \
    'run-full-e2e.sh' \
    'run-default-profile-e2e.sh'; do
    _ci_require_in "$integration" "integration still runs ${gate}" "$gate"
  done

  # --- publish keeps the tests-before-push invariant -----------------------
  local publish_needs
  publish_needs="$(printf '%s\n' "$publish" | grep -E '^    needs:' || true)"
  if [[ "$publish_needs" == *backend* && "$publish_needs" == *frontend* && "$publish_needs" == *integration* ]]; then
    _ci_pass 'publish needs backend, frontend and integration'
  else
    _ci_fail "publish does not need all three prior jobs (got: ${publish_needs:-none})"
  fi
  _ci_require_in "$publish" 'publish may write packages' 'packages: write'
  _ci_require_in "$publish" 'publish is main-only' "github.ref == 'refs/heads/main'"
  _ci_require_line "$publish" 'publish pushes through bake' '^          push: true$'
  _ci_require_in "$publish" 'publish publishes the immutable full-SHA tag' 'sha-${{ github.sha }}'

  # --- the bake definition carries platform, scopes and labels -------------
  if grep -Eq 'platforms[[:space:]]*=[[:space:]]*\["linux/amd64"\]' "$bake"; then
    _ci_pass 'bake builds only linux/amd64'
  else
    _ci_fail 'bake does not pin linux/amd64'
  fi
  if [[ "$(grep -c 'provenance = false' "$bake")" -ge 2 ]]; then
    _ci_pass 'bake disables provenance for every target'
  else
    _ci_fail 'bake does not disable provenance for every target'
  fi
  for target in $CI_CONTRACT_TARGETS; do
    if [[ "$(grep -c "scope=actfb-${target}\"" "$bake")" -ge 2 ]]; then
      _ci_pass "bake keeps a separate GHA cache scope for ${target}"
    else
      _ci_fail "bake is missing the GHA cache scope for ${target}"
    fi
    if grep -q "mode=max,scope=actfb-${target}\"" "$bake"; then
      _ci_pass "bake exports max-mode cache for ${target}"
    else
      _ci_fail "bake does not export max-mode cache for ${target}"
    fi
  done

  [[ "$CI_CONTRACT_FAILURES" -eq 0 ]]
}

# --- mutated fixtures -------------------------------------------------------

_ci_mutate_replace() { # file from to   (literal, awk index avoids regex)
  awk -v from="$2" -v to="$3" '
    { line = $0; at = index(line, from)
      if (at > 0) line = substr(line, 1, at - 1) to substr(line, at + length(from))
      print line }
  ' "$1"
}

_ci_mutate_drop() { # file literal
  awk -v lit="$2" 'index($0, lit) == 0' "$1"
}

_ci_mutate_insert_after_job() { # file job literal-line
  awk -v job="$2" -v ins="$3" '
    { print }
    /^jobs:[[:space:]]*$/ { injobs = 1 }
    injobs && $0 ~ "^  " job ":[[:space:]]*$" && !done { print ins; done = 1 }
  ' "$1"
}

_ci_expect() { # expect(accept|reject) label mutated-workflow log bake
  local want="$1" label="$2" mutated="$3" log="$4" bake="$5"
  # ci_contract_check resets the counters, so preserve the running selftest
  # tally and only add this fixture's own verdict to it.
  local saved_passes="$CI_CONTRACT_PASSES" saved_failures="$CI_CONTRACT_FAILURES"
  ci_contract_check "$mutated" "$bake" >"$log" 2>&1 || true
  local fails="$CI_CONTRACT_FAILURES"
  CI_CONTRACT_PASSES="$saved_passes"
  CI_CONTRACT_FAILURES="$saved_failures"
  if [[ "$want" == reject ]]; then
    if [[ "$fails" -gt 0 ]]; then
      _ci_pass "contract rejects ${label}"
    else
      _ci_fail "contract accepts ${label}: that unsafe edit is not detected"
      sed 's/^/    /' "$log" >&2 || true
    fi
  else
    if [[ "$fails" -eq 0 ]]; then
      _ci_pass "contract accepts ${label}"
    else
      _ci_fail "contract rejects ${label}, which should be valid"
      sed 's/^/    /' "$log" >&2 || true
    fi
  fi
}

ci_contract_selftest() {
  local wf="$1" bake="$2"
  CI_CONTRACT_PASSES=0
  CI_CONTRACT_FAILURES=0

  local dir
  dir="$(mktemp -d "${TMPDIR:-/tmp}/actfb-ci-contract.XXXXXX")"
  local log="$dir/contract.log"

  # Positive control: the shipped workflow must pass. This also proves the
  # mutation helpers below do not corrupt a file they are not asked to touch.
  _ci_expect accept 'the shipped workflow' "$wf" "$log" "$bake"

  _ci_mutate_replace "$wf" 'needs: [backend, frontend, integration]' 'needs: [backend, frontend]' >"$dir/no-publish-dep.yml"
  _ci_expect reject 'publish that dropped integration' "$dir/no-publish-dep.yml" "$log" "$bake"

  _ci_mutate_replace "$wf" '          push: false' '          push: true' >"$dir/integration-push.yml"
  _ci_expect reject 'an integration step that pushes' "$dir/integration-push.yml" "$log" "$bake"

  _ci_mutate_replace "$wf" '          load: true' '          load: false' >"$dir/integration-noload.yml"
  _ci_expect reject 'an integration build that keeps no image' "$dir/integration-noload.yml" "$log" "$bake"

  _ci_mutate_drop "$wf" '            retention.tags=actfb-retention:ci' >"$dir/missing-tag.yml"
  _ci_expect reject 'a build that lost one image tag' "$dir/missing-tag.yml" "$log" "$bake"

  _ci_mutate_drop "$wf" '            max-parallelism = 2' >"$dir/unbounded.yml"
  _ci_expect reject 'an unbounded build' "$dir/unbounded.yml" "$log" "$bake"

  _ci_mutate_insert_after_job "$wf" integration '    needs: [backend, frontend]' >"$dir/restored-needs.yml"
  _ci_expect reject 'integration waiting on the quality jobs again' "$dir/restored-needs.yml" "$log" "$bake"

  _ci_mutate_insert_after_job "$wf" integration '      packages: write' >"$dir/integration-packages.yml"
  _ci_expect reject 'an integration job with package write' "$dir/integration-packages.yml" "$log" "$bake"

  _ci_mutate_insert_after_job "$wf" integration '        uses: docker/login-action@v4' >"$dir/integration-login.yml"
  _ci_expect reject 'an integration job that logs in to a registry' "$dir/integration-login.yml" "$log" "$bake"

  _ci_mutate_insert_after_job "$wf" integration '          docker buildx build \' >"$dir/manual-buildx.yml"
  _ci_expect reject 'a hand-written buildx loop' "$dir/manual-buildx.yml" "$log" "$bake"

  _ci_mutate_drop "$wf" 'TestRustFSFullStackIntegration' >"$dir/dropped-gate.yml"
  _ci_expect reject 'a workflow that dropped a gate step' "$dir/dropped-gate.yml" "$log" "$bake"

  rm -rf "$dir"
  [[ "$CI_CONTRACT_FAILURES" -eq 0 ]]
}
