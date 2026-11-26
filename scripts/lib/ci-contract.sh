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
# Labels of the checks that failed, so a fixture can assert that *its* invariant
# fired rather than "some check somewhere failed".
CI_CONTRACT_FAILED_LABELS=()

# Every image the integration job must load locally and the publish job must
# push. One list so a target can never be added to one half only.
CI_CONTRACT_TARGETS="backend notification retention events-dispatcher file-scanner storage-init plugin-runner ad-worker instance-worker frontend"

_ci_reset() { CI_CONTRACT_PASSES=0; CI_CONTRACT_FAILURES=0; CI_CONTRACT_FAILED_LABELS=(); }

_ci_pass() { printf 'PASS %s\n' "$1"; CI_CONTRACT_PASSES=$((CI_CONTRACT_PASSES + 1)); }

# _ci_fail LABEL [DETAIL]
# Only the stable LABEL is recorded for the self-test, so an expected label never
# embeds an interpolated value. DETAIL carries the pattern or the actual value.
_ci_fail() {
  local label="$1" detail="${2:-}"
  if [[ -n "$detail" ]]; then
    printf 'FAIL %s (%s)\n' "$label" "$detail" >&2
  else
    printf 'FAIL %s\n' "$label" >&2
  fi
  CI_CONTRACT_FAILURES=$((CI_CONTRACT_FAILURES + 1))
  CI_CONTRACT_FAILED_LABELS+=("$label")
}

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
  if [[ "$1" == *"$3"* ]]; then _ci_pass "$2"; else _ci_fail "$2" "missing: $3"; fi
}

_ci_forbid_in() { # block label literal
  if [[ "$1" == *"$3"* ]]; then _ci_fail "$2" "unexpected: $3"; else _ci_pass "$2"; fi
}

# Line-anchored variants. These exist because a substring check can be satisfied
# by prose: a comment mentioning `push: false` would otherwise let a step that
# actually pushes pass the gate. Indentation is part of the pattern on purpose.
_ci_require_line() { # block label ere
  if printf '%s\n' "$1" | grep -Eq -- "$3"; then _ci_pass "$2"; else _ci_fail "$2" "no line matches: $3"; fi
}

_ci_forbid_line() { # block label ere
  if printf '%s\n' "$1" | grep -Eq -- "$3"; then _ci_fail "$2" "line matches: $3"; else _ci_pass "$2"; fi
}

# --- one workflow/bake pair -------------------------------------------------

ci_contract_check() {
  local wf="$1" bake="$2"
  _ci_reset

  if [[ ! -f "$wf" ]]; then _ci_fail "ci contract: missing workflow $wf"; return 1; fi
  if [[ ! -f "$bake" ]]; then _ci_fail "ci contract: missing bake definition $bake"; return 1; fi

  local jobs
  jobs="$(awk '/^jobs:/{inside=1; next} inside && /^  [a-zA-Z0-9_-]+:/{gsub(/:.*/, "", $1); print $1}' "$wf")"
  if [[ "$jobs" == $'backend\nfrontend\nintegration\npublish' ]]; then
    _ci_pass 'workflow has exactly backend/frontend/integration/publish jobs'
  else
    _ci_fail 'workflow has exactly backend/frontend/integration/publish jobs' \
      "got: $(printf '%s' "$jobs" | tr '\n' ' ')"
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
  # Any job-level permissions block at all: the job must inherit the global
  # read-only default. A `write-all` here would grant packages: write even though
  # the literal `packages: write` is absent.
  _ci_forbid_line "$integration" 'integration declares no job permissions' '^    permissions:'
  _ci_forbid_line "$integration" 'integration cannot write packages' '^      packages: write$'
  _ci_forbid_line "$integration" 'integration does not log in to a registry' '^        uses: docker/login-action@'
  _ci_forbid_line "$integration" 'integration never pushes images' '^ *docker push'
  # Any `docker buildx` subcommand, not just `buildx build`. This matches the
  # invocation shapes the workflow actually uses (bare, `sudo`, or with leading
  # `VAR=value` assignments); it is a pattern guard for known forms, not a shell
  # parser, so an obfuscated call (for example `bash -c '…'`) is out of scope.
  # The bake shape checks below remain the primary guard.
  _ci_forbid_line "$integration" 'integration has no hand-written buildx invocation' '^ *([A-Za-z_][A-Za-z0-9_]*=[^ ]+ +)*(sudo +)?docker +buildx'
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
    'scripts/test-ci-contract.sh' \
    'e2e/test-run-cleanup.sh' \
    'run-e2e.sh' \
    'run-full-e2e.sh' \
    'run-default-profile-e2e.sh'; do
    _ci_require_in "$integration" "integration still runs ${gate}" "$gate"
  done

  # The two regression guards must be real steps with their published names and
  # commands, not merely a mention somewhere in the job.
  _ci_require_line "$integration" 'contract guard step is named and wired' \
    '^        run: \./scripts/test-ci-contract\.sh$'
  _ci_require_in "$integration" 'contract guard step keeps its name' 'Test CI contract regression guards'
  _ci_require_line "$integration" 'cleanup guard step is named and wired' \
    '^        run: \./e2e/test-run-cleanup\.sh$'
  _ci_require_in "$integration" 'cleanup guard step keeps its name' 'Test test-resource cleanup guards'

  # --- publish keeps the tests-before-push invariant -----------------------
  local publish_needs
  publish_needs="$(printf '%s\n' "$publish" | grep -E '^    needs:' || true)"
  if [[ "$publish_needs" == *backend* && "$publish_needs" == *frontend* && "$publish_needs" == *integration* ]]; then
    _ci_pass 'publish needs backend, frontend and integration'
  else
    _ci_fail 'publish needs backend, frontend and integration' \
      "got: ${publish_needs:-none}"
  fi
  _ci_require_in "$publish" 'publish may write packages' 'packages: write'
  _ci_require_in "$publish" 'publish is main-only' "github.ref == 'refs/heads/main'"
  _ci_require_line "$publish" 'publish pushes through bake' '^          push: true$'
  _ci_require_in "$publish" 'publish publishes the immutable full-SHA tag' 'sha-${{ github.sha }}'

  # --- the bake definition carries platform, scopes and labels -------------
  # Workflow-level permissions must be exactly the read-only default; a
  # `write-all` there would silently grant integration package write.
  local global_perms
  global_perms="$(awk '
    /^permissions:[[:space:]]*$/ { inside = 1; print; next }
    inside && /^[^[:space:]]/ { inside = 0 }
    inside { print }
  ' "$wf")"
  if [[ "$global_perms" == $'permissions:\n  contents: read' ]]; then
    _ci_pass 'workflow permissions are exactly contents: read'
  else
    _ci_fail 'workflow permissions are exactly contents: read' \
      "got: $(printf '%s' "$global_perms" | tr '\n' ' ')"
  fi
  _ci_forbid_line "$(printf '%s\n' "$global_perms")" 'workflow never grants write-all' 'write-all'

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
      _ci_fail "bake keeps a separate GHA cache scope for ${target}"
    fi
    if grep -q "mode=max,scope=actfb-${target}\"" "$bake"; then
      _ci_pass "bake exports max-mode cache for ${target}"
    else
      _ci_fail "bake exports max-mode cache for ${target}"
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

# Replace a line only when the entire line equals `from`. Needed for fixtures
# that must hit one specific block: a substring replace on `  contents: read`
# also matches publish's more deeply indented copy.
_ci_mutate_replace_line() { # file from to (whole-line, literal)
  awk -v from="$2" -v to="$3" '{ if ($0 == from) print to; else print }' "$1"
}

_ci_mutate_insert_after_job() { # file job literal-line
  awk -v job="$2" -v ins="$3" '
    { print }
    /^jobs:[[:space:]]*$/ { injobs = 1 }
    injobs && $0 ~ "^  " job ":[[:space:]]*$" && !done { print ins; done = 1 }
  ' "$1"
}

# Assert one fixture's verdict. For `reject`, the expected failure label must be
# present: proving that *this* invariant fired, not merely that some check did.
# A mutation that breaks an unrelated check therefore fails the self-test.
_ci_expect() { # expect(accept|reject) label mutated-workflow log bake [expected-fail-label]
  local want="$1" label="$2" mutated="$3" log="$4" bake="$5" expected="${6:-}"
  # ci_contract_check resets the counters and the recorded labels, so preserve
  # the running selftest tally (including the label snapshot) and only add this
  # fixture's own verdict to it. Restoring the labels matters as much as the
  # counters: a stale label left behind by an earlier fixture would satisfy a
  # later expectation that never actually fired.
  local saved_passes="$CI_CONTRACT_PASSES" saved_failures="$CI_CONTRACT_FAILURES"
  local -a saved_labels=("${CI_CONTRACT_FAILED_LABELS[@]+"${CI_CONTRACT_FAILED_LABELS[@]}"}")
  ci_contract_check "$mutated" "$bake" >"$log" 2>&1 || true
  local fails="$CI_CONTRACT_FAILURES"
  local labels
  labels="$(printf '%s\n' "${CI_CONTRACT_FAILED_LABELS[@]+"${CI_CONTRACT_FAILED_LABELS[@]}"}")"
  CI_CONTRACT_PASSES="$saved_passes"
  CI_CONTRACT_FAILURES="$saved_failures"
  CI_CONTRACT_FAILED_LABELS=("${saved_labels[@]+"${saved_labels[@]}"}")

  if [[ "$want" == reject ]]; then
    if [[ -z "$expected" ]]; then
      _ci_fail "self-test fixture '${label}' has no expected failure label"
      return 1
    fi
    if [[ "$fails" -eq 0 ]]; then
      _ci_fail "contract accepts ${label}: that unsafe edit is not detected"
      sed 's/^/    /' "$log" >&2 || true
      return 1
    fi
    # Fixed-string, whole-line match against the recorded labels.
    if printf '%s\n' "$labels" | grep -qxF -- "$expected"; then
      _ci_pass "contract rejects ${label}"
      return 0
    fi
    _ci_fail "contract rejects ${label} but not for the expected reason"
    printf '    expected failure label: %s\n' "$expected" >&2
    printf '    actual failure labels:\n' >&2
    printf '%s\n' "$labels" | sed 's/^/      /' >&2
    return 1
  fi

  if [[ "$fails" -eq 0 ]]; then
    _ci_pass "contract accepts ${label}"
    return 0
  fi
  _ci_fail "contract rejects ${label}, which should be valid"
  printf '%s\n' "$labels" | sed 's/^/    /' >&2 || true
  return 1
}

# Meta-fixture: prove the expected-label check itself is live. Running the same
# mutant with a label no check emits must FAIL; otherwise every fixture above
# would pass on "some check fired". The probe runs in a subshell so its
# deliberate failure cannot leak into the selftest totals.
_ci_expect_label_check_is_live() { # mutated log bake
  local mutated="$1" log="$2" bake="$3"
  if ( _ci_expect reject 'a wrong expected label' "$mutated" "$log" "$bake" \
        'this label is never emitted by any check' ) >/dev/null 2>&1; then
    _ci_fail 'self-test detects a wrong expected label'
  else
    _ci_pass 'self-test detects a wrong expected label'
  fi
}

ci_contract_selftest() {
  local wf="$1" bake="$2"
  _ci_reset

  local dir
  dir="$(mktemp -d "${TMPDIR:-/tmp}/actfb-ci-contract.XXXXXX")"
  local log="$dir/contract.log"

  # Positive control: the shipped workflow must pass. This also proves the
  # mutation helpers below do not corrupt a file they are not asked to touch.
  _ci_expect accept 'the shipped workflow' "$wf" "$log" "$bake"

  _ci_mutate_replace "$wf" 'needs: [backend, frontend, integration]' 'needs: [backend, frontend]' >"$dir/no-publish-dep.yml"
  _ci_expect reject 'publish that dropped integration' "$dir/no-publish-dep.yml" "$log" "$bake" \
    'publish needs backend, frontend and integration'

  _ci_mutate_replace "$wf" '          push: false' '          push: true' >"$dir/integration-push.yml"
  _ci_expect reject 'an integration step that pushes' "$dir/integration-push.yml" "$log" "$bake" \
    'integration bake never pushes'

  _ci_mutate_replace "$wf" '          load: true' '          load: false' >"$dir/integration-noload.yml"
  _ci_expect reject 'an integration build that keeps no image' "$dir/integration-noload.yml" "$log" "$bake" \
    'integration bake loads images locally'

  _ci_mutate_drop "$wf" '            retention.tags=actfb-retention:ci' >"$dir/missing-tag.yml"
  _ci_expect reject 'a build that lost one image tag' "$dir/missing-tag.yml" "$log" "$bake" \
    'integration tags actfb-retention:ci'

  _ci_mutate_drop "$wf" '            max-parallelism = 2' >"$dir/unbounded.yml"
  _ci_expect reject 'an unbounded build' "$dir/unbounded.yml" "$log" "$bake" \
    'integration bake is bounded'

  _ci_mutate_insert_after_job "$wf" integration '    needs: [backend, frontend]' >"$dir/restored-needs.yml"
  _ci_expect reject 'integration waiting on the quality jobs again' "$dir/restored-needs.yml" "$log" "$bake" \
    'integration does not wait on the quality jobs'

  # Valid job-level permissions block, correctly indented (4 spaces for the key,
  # 6 for its child) so the fixture is syntactically real YAML.
  _ci_mutate_insert_after_job "$wf" integration $'    permissions:\n      packages: write' >"$dir/integration-packages.yml"
  _ci_expect reject 'an integration job with package write' "$dir/integration-packages.yml" "$log" "$bake" \
    'integration declares no job permissions'

  _ci_mutate_insert_after_job "$wf" integration '        uses: docker/login-action@v4' >"$dir/integration-login.yml"
  _ci_expect reject 'an integration job that logs in to a registry' "$dir/integration-login.yml" "$log" "$bake" \
    'integration does not log in to a registry'

  _ci_mutate_insert_after_job "$wf" integration '          docker buildx build \' >"$dir/manual-buildx.yml"
  _ci_expect reject 'a hand-written buildx loop' "$dir/manual-buildx.yml" "$log" "$bake" \
    'integration has no hand-written buildx invocation'

  _ci_mutate_drop "$wf" 'TestRustFSFullStackIntegration' >"$dir/dropped-gate.yml"
  _ci_expect reject 'a workflow that dropped a gate step' "$dir/dropped-gate.yml" "$log" "$bake" \
    'integration still runs TestRustFSFullStackIntegration'

  # The two regression-guard steps are load-bearing: dropping either one must be
  # rejected, and each must be reported by its own assertion.
  _ci_mutate_drop "$wf" 'scripts/test-ci-contract.sh' >"$dir/dropped-contract-guard.yml"
  _ci_expect reject 'a workflow without the CI contract guard step' "$dir/dropped-contract-guard.yml" "$log" "$bake" \
    'integration still runs scripts/test-ci-contract.sh'

  _ci_mutate_drop "$wf" 'e2e/test-run-cleanup.sh' >"$dir/dropped-cleanup-guard.yml"
  _ci_expect reject 'a workflow without the cleanup guard step' "$dir/dropped-cleanup-guard.yml" "$log" "$bake" \
    'integration still runs e2e/test-run-cleanup.sh'

  # Workflow-level privilege escalation: write-all must be rejected even though
  # no job declares its own permissions block. Whole-line replacement, so only
  # the root `permissions:` child is rewritten and publish's own read-only line
  # is left alone.
  _ci_mutate_replace_line "$wf" '  contents: read' '  write-all' >"$dir/global-write-all.yml"
  _ci_expect reject 'a workflow-level write-all' "$dir/global-write-all.yml" "$log" "$bake" \
    'workflow never grants write-all'

  # Meta-fixture: the expected-label check itself must be live. Claiming a label
  # that no check emits has to fail, otherwise every fixture above is vacuous.
  _ci_expect_label_check_is_live "$dir/integration-push.yml" "$log" "$bake"

  rm -rf "$dir"
  [[ "$CI_CONTRACT_FAILURES" -eq 0 ]]
}
