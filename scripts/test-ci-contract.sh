#!/usr/bin/env bash
# Standalone tests for scripts/lib/ci-contract.sh.
#
# Pure text: no Docker, no Go, no network. Covers the positive check, the mutated
# fixtures, the negative control against the pre-change workflow, and the
# "a contract failure must not swallow the summary" behaviour the packaging
# script relies on.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

passes=0
failures=0
pass() { printf 'PASS %s\n' "$1"; passes=$((passes + 1)); }
fail() { printf 'FAIL %s\n' "$1" >&2; failures=$((failures + 1)); }

# shellcheck source=scripts/lib/ci-contract.sh
source scripts/lib/ci-contract.sh

WORK="$(mktemp -d "${TMPDIR:-/tmp}/actfb-contract-test.XXXXXX")"
trap 'rm -rf -- "$WORK"' EXIT

# --- 1. the shipped workflow passes ---------------------------------------
ci_contract_check .github/workflows/go.yml docker-bake.hcl >"$WORK/positive.log" 2>&1
rc=$?
if [[ "$rc" -eq 0 && "$CI_CONTRACT_FAILURES" -eq 0 ]]; then
  pass "shipped workflow passes (${CI_CONTRACT_PASSES} checks)"
else
  fail "shipped workflow failed: $(grep -c '^FAIL' "$WORK/positive.log" || true) failures"
fi
if [[ "$CI_CONTRACT_PASSES" -ge 60 ]]; then
  pass "shipped workflow runs a substantial check set (${CI_CONTRACT_PASSES})"
else
  fail "check set shrank unexpectedly (${CI_CONTRACT_PASSES})"
fi

# --- 2. counters reset between invocations --------------------------------
first="$CI_CONTRACT_PASSES"
ci_contract_check .github/workflows/go.yml docker-bake.hcl >/dev/null 2>&1
if [[ "$CI_CONTRACT_PASSES" -eq "$first" ]]; then
  pass "counters reset between invocations (no accumulation)"
else
  fail "counters accumulated: $first then $CI_CONTRACT_PASSES"
fi

# Recorded failure labels must reset too. A stale label would let a later
# expectation be satisfied by a failure that never happened.
ci_contract_check "$WORK/nonexistent-workflow.yml" docker-bake.hcl >/dev/null 2>&1
stale_labels="${#CI_CONTRACT_FAILED_LABELS[@]}"
ci_contract_check .github/workflows/go.yml docker-bake.hcl >/dev/null 2>&1
if [[ "$stale_labels" -gt 0 && "${#CI_CONTRACT_FAILED_LABELS[@]}" -eq 0 ]]; then
  pass "failure labels reset between invocations (no stale match)"
else
  fail "failure labels leaked: $stale_labels then ${#CI_CONTRACT_FAILED_LABELS[@]}"
fi

# --- 2b. the two regression guards are wired into the workflow -----------
for guard in 'Test CI contract regression guards' 'Test test-resource cleanup guards'; do
  if grep -qF -- "$guard" .github/workflows/go.yml; then
    pass "workflow declares the step '${guard}'"
  else
    fail "workflow is missing the step '${guard}'"
  fi
done
if grep -qE '^        run: \./scripts/test-ci-contract\.sh$' .github/workflows/go.yml; then
  pass 'contract guard step runs scripts/test-ci-contract.sh'
else
  fail 'contract guard step does not run scripts/test-ci-contract.sh'
fi
if grep -qE '^        run: \./e2e/test-run-cleanup\.sh$' .github/workflows/go.yml; then
  pass 'cleanup guard step runs e2e/test-run-cleanup.sh'
else
  fail 'cleanup guard step does not run e2e/test-run-cleanup.sh'
fi

# --- 3. the self-test passes, with one fixture per invariant --------------
ci_contract_selftest .github/workflows/go.yml docker-bake.hcl >"$WORK/selftest.log" 2>&1
rc=$?
if [[ "$rc" -eq 0 && "$CI_CONTRACT_FAILURES" -eq 0 ]]; then
  pass "self-test passes (${CI_CONTRACT_PASSES} fixtures)"
else
  fail "self-test failed: $(grep -c '^FAIL' "$WORK/selftest.log" || true) failures"
  grep '^FAIL' "$WORK/selftest.log" >&2 || true
fi
if [[ "$CI_CONTRACT_PASSES" -ge 12 ]]; then
  pass "self-test covers the expected fixture count (${CI_CONTRACT_PASSES})"
else
  fail "self-test fixture count dropped (${CI_CONTRACT_PASSES})"
fi

# --- 4. negative control: the pre-change workflow must be rejected --------
# Prefer the real baseline from git; fall back to reconstructing the old job
# graph so the test still works in a shallow clone.
BASE_SHA="0da549e35ba0fa4aab811abc96a0411a8482e966"
if git cat-file -e "${BASE_SHA}:.github/workflows/go.yml" 2>/dev/null; then
  git show "${BASE_SHA}:.github/workflows/go.yml" >"$WORK/old-graph.yml"
  negative_source="baseline ${BASE_SHA:0:7}"
else
  awk '
    /^    # Independent of the quality jobs on purpose/ { skip = 1 }
    skip && /^    runs-on: ubuntu-latest$/ { skip = 0; print; next }
    skip { next }
    { print }
  ' .github/workflows/go.yml |
  awk '{ print; if ($0 == "  integration:") { getline; print; print "    needs: [backend, frontend]" } }' \
    >"$WORK/old-graph.yml"
  negative_source="reconstructed job graph"
fi

ci_contract_check "$WORK/old-graph.yml" docker-bake.hcl >"$WORK/negative.log" 2>&1
negative_fails="$CI_CONTRACT_FAILURES"
if [[ "$negative_fails" -gt 0 ]]; then
  pass "negative control is rejected (${negative_fails} failures, ${negative_source})"
else
  fail "negative control passed: the gate is not live"
fi
if grep -q 'integration does not wait on the quality jobs' "$WORK/negative.log"; then
  pass "negative control trips the independence assertion"
else
  fail "negative control did not trip the independence assertion"
fi

# --- 5. a contract failure must not swallow the summary -------------------
# Mirrors the packaging script's fold: FAIL lines print, counts are added, and a
# non-zero exit still happens at the end.
(
  set -euo pipefail
  p=0
  f=0
  ci_contract_check "$WORK/old-graph.yml" docker-bake.hcl || true
  p=$((p + CI_CONTRACT_PASSES))
  f=$((f + CI_CONTRACT_FAILURES))
  printf 'SUMMARY %d passed, %d failed\n' "$p" "$f"
  [[ "$f" -eq 0 ]]
) >"$WORK/fold.log" 2>&1
fold_rc=$?
if grep -q '^SUMMARY' "$WORK/fold.log"; then
  pass "summary still prints after a contract failure"
else
  fail "summary was swallowed by a contract failure"
fi
if [[ "$fold_rc" -ne 0 ]]; then
  pass "fold still exits non-zero on failure"
else
  fail "fold exited zero despite failures"
fi

printf '\n%d checks passed, %d failed\n' "$passes" "$failures"
[[ "$failures" -eq 0 ]]
