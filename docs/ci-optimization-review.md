# Independent CI review — `07b0be8..6257347` (2 commits)

Reviewer scope: the two local commits that change the CI pipeline and its contract. **Read-only on product code.**
This file is the only thing written; nothing was staged, committed or pushed.

| Commit | Subject |
| --- | --- |
| `15bf7fa` | `ci: run integration in parallel and cache the image build` |
| `6257347` | `docs: record the CI optimisation baseline and verification` |

Baseline under review: run [45889089999](https://github.com/nrf24l01/aCTFb/actions/runs/45889089999) at
`0da549e` (remote `main`), success, 990 s, 4/4 jobs. Local `main` is `ahead 12` of `origin/main` (these two
commits plus ten desktop-UI commits); nothing from either set is published.

---

## Verdict

**APPROVED — no Critical finding. Merge is safe for the CI host; 3 Important and 5 Minor items, none of which
can make a broken pipeline look green.**

The pipeline shape is correct and the safety properties the change claims all hold under independent check:
`publish` still needs all three jobs and is still main-only, integration has no `needs:`, no `permissions:`,
no registry login, no `docker push`, and its bake is `push: false` / `load: true`. The contract that guards
those properties is live (66 assertions, 11 self-test fixtures, 19 failures against the pre-change workflow).

The Important items are about **operability, not correctness**: the first-ever execution of 18 browser specs
will leave no traces if it fails (I1), the promoted full-stack runner force-removes any container created
during its window — correct on an ephemeral VM, unsafe next to an operator stack (I2), and the self-test proves
"some assertion fired" rather than "this assertion fired" (I3). All three are cheap to close in the single fix
wave MAIN plans.

Nothing here justifies weakening a gate to reach green. If the first GitHub run fails, that is a product or
harness finding.

---

## What I actually executed

Pure-text checks only. **No** `go test`, no `bun` test run, no Docker build/run, no server, no subagent.

| Check | Command | Result |
| --- | --- | --- |
| Contract, positive | `source scripts/lib/ci-contract.sh; ci_contract_check .github/workflows/go.yml docker-bake.hcl` | **66 passed, 0 failed** (matches the doc) |
| Contract self-test | `ci_contract_selftest …` | **11 passed, 0 failed** (1 positive control + 10 mutants) |
| Contract, negative control | `ci_contract_check /tmp/opencode/old-go.yml docker-bake.hcl` (`old-go.yml` = `git show 07b0be8:.github/workflows/go.yml`) | **47 passed, 19 failed** — gate is live, and the 19 are exactly the expected ones (`needs:`, manual buildx, `--cache-from`, bake step, `push`, `load`, `max-parallelism`, `files`, all 10 tags, `run-full-e2e.sh`) |
| Workflow YAML | PyYAML `safe_load` | 4 jobs; `integration.needs = None`, `integration.permissions = None`; `publish.needs = [backend, frontend, integration]`; global `permissions = {contents: read}`; triggers `push`/`pull_request`/`workflow_dispatch` intact |
| Spec collection | `bun run test --list` / `… p1-core.spec.ts --list` / `… full-stage.spec.ts organizer-ux.spec.ts --list` | **24 / 1 / 18** — matches the doc, and proves `bun run test <spec>` really forwards the filter (see “verified correct”, item 5) |
| Shell syntax | `bash -n` on `scripts/lib/ci-contract.sh`, `scripts/test-production-packaging.sh`, `e2e/run-e2e.sh`, `e2e/run-full-e2e.sh` | 4/4 OK |
| Baseline arithmetic | `/tmp/opencode/run-45889089999-jobs.json` | Frontend 36 s, Backend 188 s, Integration 738 s, Publish 54 s, wall 20:10:35→20:27:04 ≈ 990 s — the doc's table is exact |

Not re-run by me (already established by the author, and out of my read-only/pure-text budget): the full
`./scripts/test-production-packaging.sh` (337/0/1 SKIP — it shells out to `docker compose config`),
`docker buildx bake --print`, and the full-stack runner itself. I did verify the *evidence file* for the bake
claim (`/tmp/opencode/bake-print.json`): 10 targets, exactly one tag each (`actfb-<target>:ci`), `context: .`
(`frontend` for the frontend), `linux/amd64`, `cache-to gha mode=max scope=actfb-<target>`, and
`VITE_API_BASE_URL=/api` preserved.

---

## Spec conformance

Every requirement in the brief, with the line that satisfies it.

| Requirement | Evidence | ✓ |
| --- | --- | --- |
| Independent backend/frontend/integration, 3 jobs in parallel | `go.yml:132-147` — no `needs:`; contract forbids one (`ci-contract.sh:95`) | ✓ |
| Publish needs **all three**, exactly 4 jobs | `go.yml:359`; `ci-contract.sh:69-73, 132-138` | ✓ |
| Tests-before-push | `publish` has no `always()`/`if: always()`, so a failed need blocks it; `go.yml:360` | ✓ |
| Official `docker/bake-action@v7`, `load: true`, `push: false` | `go.yml:305-326`; inputs `files/source/push/load/provenance/set` all exist in the v7 `action.yml` (verified upstream) | ✓ |
| Ten local `actfb-<target>:ci` tags, same platform/args/scopes | `go.yml:313-323` + `docker-bake.hcl:24,41-127`; `bake-print.json` | ✓ |
| Stable gha cache scopes, credentials automatic | `docker-bake.hcl:42-127` unchanged; credentials come from the action, not from a `run:` step | ✓ |
| BuildKit bounded | `go.yml:291-297` (`buildkitd-config-inline`, `[worker.oci] max-parallelism = 2`); `buildkitd-config-inline` is a real setup-buildx@v4 input | ✓ |
| Minimal permissions; `packages: write` only in publish | `go.yml:65-66, 363-365`; integration has no `permissions:` | ✓ |
| Publish main-only, `latest` + full-SHA, never on PR | `go.yml:360, 388-395`; `sha-${{ github.sha }}` | ✓ |
| Triggers / `workflow_dispatch` / self-path preserved | `go.yml:6-63` (paths include `.github/workflows/go.yml`, `scripts/**`, `e2e/**`) | ✓ |
| Contract: positive + self-test mutants rejecting unsafe publish / missing gate | 66/0, 11/0, 19 negative-control failures (run above) | ✓ |
| No Dockerfile change; `GOARCH` already correct | `git diff --stat` has no `Dockerfile`; `Dockerfile:31` is `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` (byte-verified with `od -c`) | ✓ |
| 23 skips → 18 in a new required gate; default 5 unchanged; p1-core not duplicated | `go.yml:265-287`; collection 24/1/18; `run-default-profile-e2e.sh:286` sets `E2E_DEFAULT_PROFILE=1` and runs the 5 | ✓ |
| Positional args forwarded quoted, no `eval` | `run-e2e.sh:114-119`, `run-full-e2e.sh:310-316` — `spec_args=("$@")`, `${spec_args[@]+"${spec_args[@]}"}` | ✓ |
| No new artifact dependency | No `download-artifact` anywhere; only `upload-artifact` under `if: failure()` | ✓ |
| Frontend API base `/api` (contracts are `/api/v2`) preserved, not invented | `docker-bake.hcl:115` + `frontend/src/shared/config/env.ts:8-14` (`apiV2BaseUrl = ${apiBaseUrl}/v2`, and a `/api/v2` input is normalised back to `/api`) | ✓ |
| Honest local disclosure (stale images skipped, full runner not run) | `docs/ci-optimization-verification.md:149-167` (not run locally), `:168-190` (validations), `:185` (the SKIP); `test-production-packaging.sh:560` SKIP text | ✓ |

---

## Findings

### I1 — Important: a failing full-stack gate leaves no Playwright evidence

`e2e/run-full-e2e.sh:57,60` assign the result directories **unconditionally** into `$WORKDIR`
(`/tmp/opencode/<project>`), and `cleanup()` deletes `$WORKDIR` on **every** exit including failure
(`:89-91`, `lifecycle_traps cleanup` at `:94`). The workflow's artifact step only collects workspace paths:

```yaml
# .github/workflows/go.yml:346-355
        with:
          path: |
            e2e/test-results/**
            e2e/playwright-report/**
```

So the 18 specs that have never run in CI — the highest-risk part of this change — will fail with a log line
and no trace, no screenshot, no video, no HTML report. `playwright.config.ts:17-19` sets
`trace/screenshot/video: …-on-failure`, i.e. the artefacts are produced and then deleted.

This is a pre-existing pattern (`run-e2e.sh:108-109` does the same; `run-default-profile-e2e.sh` sets only
`E2E_REPORT_DIR`, so its traces do survive), but the new 45-minute gate makes it acute.

**Fix (2 lines + 2 env lines):** honour a pre-set value instead of overwriting —

```bash
# e2e/run-full-e2e.sh:57 and :60
export E2E_OUTPUT_DIR="${E2E_OUTPUT_DIR:-$WORKDIR/test-results}"
export E2E_REPORT_DIR="${E2E_REPORT_DIR:-$WORKDIR/playwright-report}"
```

```yaml
# .github/workflows/go.yml:285-287
      - name: Run full-stack browser acceptance
        timeout-minutes: 45
        env:
          E2E_OUTPUT_DIR: ${{ github.workspace }}/e2e/test-results/full-stack
          E2E_REPORT_DIR: ${{ github.workspace }}/e2e/playwright-report/full-stack
        run: ./e2e/run-full-e2e.sh full-stage.spec.ts organizer-ux.spec.ts
```

`E2E_KEEP=1` alone does **not** fix it: `$WORKDIR` is random and outside the upload glob. Do the same for
`run-e2e.sh` in the same wave so both gates behave alike.

**Test:** run the gate with a deliberately failing spec locally (`--grep` a non-existent title is not enough;
mutate one assertion) and confirm `e2e/test-results/full-stack/` contains a `trace.zip`.

### I2 — Important (local only, not a CI blocker): the promoted runner force-removes *every* container created during its window

`e2e/run-full-e2e.sh:66-81`:

```bash
new_containers="$(comm -13 "$WORKDIR/containers-before.txt" <(docker ps -aq | sort) …)"
[[ -n "$new_containers" ]] && docker rm -f $new_containers
new_volumes="$(… | grep -E '^actfb' || true)"     # volumes are filtered, containers are not
```

**On the CI VM this is provably safe** and I confirm the reasoning: the only containers alive before the
snapshot are the job's own service containers (`go.yml:149-171`, `postgres` + `redis`), which are inside
`containers-before.txt` and therefore survive; nothing else on an ephemeral runner creates containers
concurrently. The step also runs **before** `docker/setup-buildx-action` (`go.yml:285` vs `:289`), so it cannot
delete the builder container — that ordering is right and should stay.

**Locally it is unsafe**, and this change turns the script into a documented, routine command. A concurrent
`docker compose up`, a builder recreated mid-run, or any operator container started during the window is
destroyed. The verification doc discloses the hazard honestly but the code still exposes it.

**Fix, preserving CI behaviour exactly:** gate the broad removal on the CI marker and fail loudly instead of
deleting when it is absent —

```bash
if [[ -n "$new_containers" ]]; then
  if [[ -n "${CI:-}" ]]; then
    docker rm -f $new_containers >/dev/null 2>&1 || true
  else
    echo "refusing to remove unowned containers outside CI: $new_containers" >&2
    return 1            # cleanup() ignores this; the assertion at :337 still reports them
  fi
fi
```

The assertion at `:336-342` already fails on leftovers, so narrowing the *removal* cannot hide a leak — the
resource gate stays as strong as it is today.

### I3 — Important: the self-test proves “an assertion fired”, not “this assertion fired”

`scripts/lib/ci-contract.sh:193-217`:

```bash
ci_contract_check "$mutated" "$bake" >"$log" 2>&1 || true
local fails="$CI_CONTRACT_FAILURES"
…
if [[ "$fails" -gt 0 ]]; then _ci_pass "contract rejects ${label}"
```

Any single failure makes a mutant “rejected”, so a fixture whose mutation accidentally broke an unrelated
check (typo, bad indentation, truncated line) would still pass. The doc's claim — “proves each assertion
rejects an unsafe edit” (`docs/ci-optimization-verification.md:20`, and §“Contract hardening” `:193-210`) — is therefore stronger than what
the code enforces. It is not vacuous (the positive control and the 19-failure negative control are real), but
the per-invariant guarantee is weaker than stated.

Related, same file: the two `_ci_mutate_insert_after_job` fixtures (`:250-256`) inject `      packages: write`
and `        uses: docker/login-action@v4` at 6/8-space indent directly under `  integration:`, which is not
valid YAML at that position. A text checker does not care, but the fixture then does not prove the check fires
on a *syntactically valid* job-level `permissions:`/`uses:`.

**Fix:** pass the expected failure text into `_ci_expect` and require it —

```bash
_ci_expect reject 'publish that dropped integration' "$dir/no-publish-dep.yml" "$log" "$bake" \
  'publish does not need all three prior jobs'
```

with `grep -qF -- "$expected" "$log"` inside `_ci_expect`. Ten one-line additions; each mutant then pins its
own invariant.

### M1 — Minor: `permissions: write-all` is not caught

`ci-contract.sh:96` forbids only the literal `^      packages: write$` inside the integration block, and the
packaging script's `require_pattern … 'contents: read'` can be satisfied by publish's own block
(`go.yml:364`). A workflow-level `permissions: write-all` would grant integration `packages: write` and pass
both gates. Residual risk is low — `push: false`, no login action and no `docker push` are independently
enforced, so the privilege would still be unusable — but the stated invariant (“integration cannot write
packages”) is not fully enforced.

**Fix:** add `_ci_forbid_line "$integration" 'integration declares no job permissions' '^    permissions:'`
and assert the workflow-level block is exactly `contents: read`.

### M2 — Minor: a contract failure aborts the packaging script before its summary

`test-production-packaging.sh:2` is `set -euo pipefail` and `:321` calls `ci_contract_check` bare, so a
non-zero return exits the script at that point. The gate still fails (correct), but `passes`/`failures` are
never folded and the “N passed / M failed” summary never prints — the CI log shows only the `FAIL …` lines.
**Fix:** `ci_contract_check … || true` plus the existing fold, or `|| { failures=$((failures+1)); }`.

### M3 — Minor: integration build-path forbids are bare-command anchored

`ci-contract.sh:99-100` (`^ *docker buildx build`, `^ *--cache-from`) miss `env X=1 docker buildx build`,
`bash -c '…'` and `xargs …`. Defence-in-depth only: the shape requirements at `:103-108`
(`uses: docker/bake-action@v7`, `push: false`, `load: true`, `files: docker-bake.hcl`) mean an alternative
build path cannot both exist and pass. Optional: forbid `docker buildx` (any subcommand) in the block.

### M4 — Minor: `docs/ci-optimization-verification.md:224` states the opposite of its own point

Byte-exact check of the committed line (`od -c`):

```
T h e   f i l e   c o n t a i n s   ` G O A R m d 6 4 ` .
```

The Dockerfile is correct (`Dockerfile:31`, byte-verified `G O A R C H = a m d 6 4`), so the paragraph whose
purpose is to record “no defect” currently records a defect. Change that token to `GOARCH`.

### M5 — Minor: measurement wording will be read as a net prediction

- `docs/implementation-plan.md:1158` — “roughly 13 minutes once warm” nets H1+H2 and omits the new gate, while
  `:1159` and `docs/ci-optimization-verification.md:146` correctly say the gate **adds** time. Suggest
  “≈13 minutes before the new full-stack gate, whose cost is unknown”.
- `docs/ci-optimization-verification.md:47-55` says the first pipeline is “still cold”. Two things are worth
  saying explicitly so the first measurement is not misread as a regression:
  1. the integration build now **pays a real `mode=max` cache export** that it previously skipped (the gha
     exporter silently no-oped without credentials), so `Build all production images` may be **slower** than
     the 72 s baseline on run 1;
  2. `scripts/lib/ci-contract.sh` is new and `.dockerignore` does **not** exclude `scripts/`, so `COPY . .`
     changes and the 37.6 s compile is cold for that reason too (`.github/`, `e2e/`, `docs/` are excluded —
     good, future CI-only tweaks will not bust the image cache).
- `:3` “The five-file change” — the diff is 5 code files **plus** 2 docs. Say “five code files”.

---

## Verified correct (checked because the brief asked)

1. **`source: .` and the bake inputs are real, not invented.** `docker/bake-action@v7/action.yml` declares
   `files`, `source`, `push`, `load`, `provenance`, `set`, `vars`, `targets`. `docker/setup-buildx-action@v4`
   declares `buildkitd-config-inline`. No “unexpected input” warning, no silent no-op. Publish already used
   `source: .` at baseline, so the context resolution is proven in production.
2. **Tag overrides replace, they do not append.** `bake-print.json` shows exactly one tag per target
   (`actfb-<target>:ci`), `context: .` (frontend `frontend`), `linux/amd64`, and
   `args = {GIT_SHA, IMAGE_TAG, VITE_API_BASE_URL=/api}` — so the runtime/auth-proxy smokes that inspect
   `actfb-*:ci` keep working, and `SHA_TAG`/`GIT_SHA` reach the HCL through the step `env:` (the same
   mechanism publish already relies on for `LATEST_TAG`).
3. **`GIT_SHA` cannot invalidate the compile.** `Dockerfile:50-51` declares the ARGs inside `runtime`, after
   the `build` stage; `-ldflags` carries no `-X`. The cache scopes are content-addressed, so a warm read from
   publish's previous main run is the right expectation — and because both jobs run on `main`, they share the
   same GitHub cache ref scope.
4. **Double-publish privilege is genuinely absent.** Integration: no `permissions:`, no
   `docker/login-action`, `push: false`, no `docker push` (also globally forbidden at
   `test-production-packaging.sh:313`). `docker/bake-action`'s default `github-token` is used only for remote
   definitions (`source: .` is local).
5. **Argument forwarding works end-to-end.** `bun run test p1-core.spec.ts --list` printed
   `$ playwright test "p1-core.spec.ts" --list` → 1 test, i.e. Bun forwards positionals without `--`, and the
   `${spec_args[@]+"${spec_args[@]}"}` idiom is safe under `set -u` with zero arguments (no-arg run still
   collects 24). No `eval`, no re-splitting of quoted arguments.
6. **The 18 specs will actually execute, not silently skip.** Every per-test guard is satisfied by what
   `run-full-e2e.sh` exports: `E2E_FULL_STACK=1` (`:61`), `E2E_DOCKER_SOCKET=/var/run/docker.sock` (`:59`),
   `E2E_DOCKER_IMAGE_REF` (`:167`), `E2E_INSTANCE_PORT` (`:58`), `E2E_DB_CONTAINER`/`E2E_PG_*` (`:47-49`),
   admin/student/reviewer/scorer credentials (`:294-299`). `default-profile.spec.ts:3` needs
   `E2E_DEFAULT_PROFILE`, which this runner never sets — and it is not collected anyway.
7. **The contract rewrite was necessary, not cosmetic.** The old `grep -A4 '^  integration:'` window would
   already fail against the new comment block, so keeping the grep was not an option.
8. **`bake` uses the same 10 targets as the old shell loop**, including `storage-init` and the untargeted
   `frontend` final stage, and the `common` target is not built (not in `group "default"`).

---

## First-GitHub-run watch list (nothing to change now)

| What | Why it matters | Failure meaning |
| --- | --- | --- |
| `Build all production images` duration, runs 1 and 2 | run 1 pays cache export for the first time; run 2 is the first real H2 signal | run 2 not faster ⇒ the credential theory was wrong for this job, not a code bug |
| Integration job duration vs. 120 min | 738 s baseline + unknown gate | a timeout is a budget fix (`timeout-minutes`), not a test failure |
| ClamAV `healthy` wait (`run-full-e2e.sh:156-162`, 300 s, fresh volume every run) | network-bound, known flake source, first-ever CI execution | a `ClamAV did not become healthy` line is an infrastructure flake — do **not** weaken the wait; raise the bound |
| 18 specs collected *and executed* | a silent `-N skipped` would mean a guard is unmet | investigate the guard, never delete it |
| `docker ps -aq` diff at `:337` | asserts the harness owns nothing | a real leak, not a false positive |

Because integration no longer waits for the quality jobs, a red commit now burns ~12 minutes of extra runner
time before the verdict. That is the documented, accepted trade-off (`go.yml:134-141`) and does not delay a
green run.

---

## Explicitly not verified by me

- The full-stack gate end to end (never executed anywhere, and it must not be run on this host — see I2).
- `./scripts/test-production-packaging.sh` in full (needs `docker compose config`); the 337/0/1 result is the
  author's, and I independently reproduced the contract and self-test portions it now includes.
- `docker buildx bake --print` on this host; I read the author's captured `bake-print.json` instead.
- `actionlint` (not installed here either).
- Anything about the desktop-UI work in the same push window — out of scope, and no source merge in these two
  commits touches `frontend/` or the e2e specs.

## Suggested fix wave (single pass, no gate weakened)

1. I1 — output-dir defaults + step `env:` (4 lines).
2. I2 — CI-gated broad removal, assertion unchanged (~6 lines).
3. I3 — expected-label assertion in `_ci_expect` (10 fixtures + helper).
4. M1–M5 — contract forbids, `|| true` fold, doc tokens/wording.

Then one `workflow_dispatch` run, and record the two `Build all production images` durations plus the gate
duration separately before quoting any saving.


---

# Round 2 — scoped re-review of the fix wave (`6257347..78a95fd`)

Scope: **only** I1, I2, I3, M1–M5 from the review above, plus anything the fix itself breaks. Single commit
`78a95fd` ("ci: keep browser evidence and stop deleting unowned containers"), 9 files, no UI commit
interleaved. Read-only on product code; this section is the only write.

Re-ran (pure text / stub, no Docker daemon, no Go, no Bun, no server): `bash -n` on all 7 shell files (7/7 OK),
one targeted probe of the workdir guard under the repo's own temp convention (see N2), and static reads.
The author's stated numbers (contract 69/0, self-test 13/0, cleanup 18/0, contract-test 9/0, packaging 342/0/1,
PyYAML 4 jobs, bake 10 tags) are **taken as given and not repeated**.

## Verdict

**Not a clean pass: one open Important (N1).** The pipeline change itself is safe and needs no further work —
every guard is stronger than before and no gate was weakened. N1 is that the two new regression suites are
not invoked by anything, so the proof for I2 and part of I3 exists but never runs. Closing it is additive
(two `run:` steps + two entries in the contract's gate list); nothing else in this round needs to change
before a GitHub run.

| Item | Status |
| --- | --- |
| I1 — a failing browser gate keeps its evidence | **ADDRESSED** |
| I2 — cleanup no longer deletes unowned containers | **ADDRESSED** (code correct; proof unwired → N1) |
| I3 — self-test pins its own failure label | **ADDRESSED** |
| M1 — `write-all` / job-level permissions | **ADDRESSED** |
| M2 — a contract failure no longer swallows the summary | **ADDRESSED** |
| M3 — buildx forbids | **PARTIAL** (`env X=…`/`sudo`/any subcommand covered; `bash -c '…'` is not — N4) |
| M4 — mangled architecture token in the verification doc | **ADDRESSED** |
| M5 — measurement wording | **PARTIAL** (verification doc fixed; `docs/implementation-plan.md:1160` still says "roughly 13 minutes once warm" and is not in this commit) |

## I1 — ADDRESSED

- Both runners honour a caller-supplied directory: `e2e/run-e2e.sh:112-113`, `e2e/run-full-e2e.sh:62,65`
  (`${E2E_OUTPUT_DIR:-…}`, `${E2E_REPORT_DIR:-…}`). Manual runs keep the old behaviour.
- Workflow writes into the workspace for **both** browser steps: `go.yml:267-269` (`…/e2e/test-results/p1-core`,
  `…/e2e/playwright-report/p1-core`) and `go.yml:294-295` (`…/full-stack`), and the existing upload step
  (`go.yml:355-364`, `e2e/test-results/**` + `e2e/playwright-report/**`) covers both without change.
- The scratch workdir is still deleted, and the new guard makes that provably narrow: `run-cleanup.sh:74-92`
  resolves the path with `pwd -P`, requires it to be **strictly under** `${TMPDIR:-/tmp}/opencode`, and refuses
  the temp root itself, `/`, a symlink that resolves outside, and an empty argument. A configured
  `E2E_OUTPUT_DIR` in the workspace therefore cannot be removed by `cleanup()`.
- Secret exposure: unchanged and correct. Generated PostgreSQL credentials live only in `$WORKDIR` (deleted);
  the artefact paths receive Playwright traces/report only. Traces do contain the harness's own per-run
  synthetic bearer tokens — ephemeral, never persisted, no operator credential, no `deploy/config.yaml`, no
  `.env`. Accepted, and stated in the verification doc.
- `e2e/.gitignore:2-3` already ignores `playwright-report/` and `test-results/`, so a workspace-targeted run
  cannot dirty the checkout.
- Out of scope but worth one line: `run-default-profile-e2e.sh:289` still points its report into its own
  workdir and `:47` deletes it, so that step's traces are still lost on failure. Not changed by design.

## I2 — ADDRESSED (code)

`e2e/lib/run-cleanup.sh` is the right shape:

- `:20-22` `run_cleanup_is_hosted_ephemeral` requires `GITHUB_ACTIONS == true` **and**
  `RUNNER_ENVIRONMENT == github-hosted`. Both are GitHub-provided (`RUNNER_ENVIRONMENT` is documented with
  exactly those two values), so a hosted job always qualifies and a self-hosted runner never does.
  `${CI:-}` is deliberately not accepted — correct, since a developer box or a shared CI host can set it.
- `:26-35` exact-name removal is unconditional and idempotent (`"$#" -gt 0` guard, missing name is not an
  error), so the harness's own PG/NATS/RustFS/ClamAV containers and the clamav volume are always cleaned —
  on any host.
- `:41-68` the difference set is removed only on a hosted ephemeral runner; otherwise it is reported and
  returns 1. Volumes keep the `^actfb` filter (`:47`) **even on a hosted runner**, so a concurrently created
  operator volume is never deleted. Owned names run first (`run-full-e2e.sh:74-75`), so they never appear in
  the diff.
- The leak assertion is unchanged (`run-full-e2e.sh:338-345`): it re-diffs after cleanup and `exit 1`s with the
  container list. `run_cleanup_unclassified … || true` at `:79` cannot mask a leak — the assertion is what
  fails the run. **No tolerance to green.**
- Failure propagation is intact: `lifecycle_traps` still captures `$?` and re-exits with it, so a Playwright
  failure is not converted into a cleanup failure. `run-e2e.sh` uses a plain `trap cleanup EXIT`, and its
  `cleanup()` ends on a construct that returns 0 in both `E2E_KEEP` branches (verified by construction and with
  a two-case probe of bash's EXIT-trap status semantics: a trap whose last command succeeds preserves the
  original status).

Accepted trade-off, stated in the doc and confirmed here: on a manual host the broad removal now refuses, so a
run that leaves a worker-created (not exact-named) container ends **red** on the assertion and needs manual
cleanup. That is the intended direction — protect the operator, never hide a leak.

## I3 — ADDRESSED

- `_ci_fail` (`ci-contract.sh:34-43`) records the **stable label** and carries the interpolated value in a
  separate detail, so expected labels never embed values.
- `_ci_expect` (`:238-279`) now requires an expected label for every `reject` fixture, matches it as a
  whole-line fixed string against `CI_CONTRACT_FAILED_LABELS` (`:261`, `grep -qxF`), and fails with
  "rejects … but not for the expected reason" plus the actual label list. A mutation that breaks an unrelated
  check can no longer pass.
- Every one of the 11 mutants names its own label (`:307-353`), and the permissions fixture is now real YAML
  (`:331-335`, `    permissions:` / `      packages: write`).
- The meta-fixture (`:285-293`) runs the same mutant with a label no check emits inside a **subshell**, so the
  deliberate failure cannot leak into the final counters, and asserts that this is rejected. The final tally
  (`:360`) therefore cannot be inflated or poisoned by it.
- 13 = 1 positive control + 11 mutants + 1 meta-check, matching the stated count.

## M1–M5

- **M1 ADDRESSED.** `:117` forbids *any* job-level `permissions:` in the integration block; `:173-185` requires
  the workflow-level block to be exactly `contents: read` and forbids `write-all` there; `:351-353` adds the
  `write-all` mutant. Publish's own `permissions:` is at 4-space indent and is correctly ignored by the
  workflow-level awk extraction.
- **M2 ADDRESSED.** `test-production-packaging.sh:321-331` uses `|| true` plus the count fold for both the
  check and the self-test, so the summary prints and the script still exits non-zero;
  `scripts/test-ci-contract.sh:94-117` asserts exactly that (summary line present **and** non-zero exit).
  `ci_contract_check` can only return non-zero after recording a failure, so `|| true` cannot hide one.
- **M3 PARTIAL.** `:124` now catches `docker buildx` with any subcommand plus `VAR=…` prefixes and `sudo`
  (`grep -E` group syntax is valid ERE). It does **not** catch `bash -c 'docker buildx …'`, while the comment at
  `:121-123` says "regardless of a `env X=1` / `bash -c` prefix". The claim is stronger than the regex; the
  primary shape guards (`:128-133`) still make an alternative build path impossible to pass, so this is
  comment-vs-code only — see N4.
- **M4 ADDRESSED.** `docs/ci-optimization-verification.md` now describes the earlier report as a display
  artifact and shows the correct token in the `od -c` block; the mangled spelling no longer appears in that
  document. (`docs/instance-lifetime-verification.md:162-174` still contains it as historical prose — different
  file, out of scope, and the same display artifact; do not reopen it.)
- **M5 PARTIAL.** The verification doc gained the run-1-is-slower explanation (first real `mode=max` export,
  new `scripts/` file busting `COPY . .`), dropped any net claim, and now says "five code files". The one-line
  wording in `docs/implementation-plan.md:1160` ("roughly 13 minutes once warm") is unchanged because that file
  is not part of this commit and is currently modified in the working tree by the desktop-UI agent. Fold it into
  that agent's next commit or a separate docs commit — do not interleave it here.

## New findings

### N1 — Important: the two new regression suites are invoked by nothing

`e2e/test-run-cleanup.sh` (18 checks) and `scripts/test-ci-contract.sh` (9 checks) exist, pass, and are recorded
in the verification doc as manual validations — but no workflow, no packaging script, no `Makefile` target and
no glob references either path:

```
$ grep -rn 'test-run-cleanup\|test-ci-contract' . --include=*   # excluding docs/ci-optimization*
(no matches)
```

`.github/workflows/go.yml` runs only `test-production-packaging.sh`, `test-production-runtime.sh`,
`test-production-auth-proxy.sh` (`:341,348,353`); `scripts/test-production-packaging.sh` `require_file`s only the
runtime and auth-proxy scripts (`:44-45`). The packaging total confirms it: 337 + 3 (contract 66→69) + 2
(self-test 11→13) = 342, i.e. the two new suites are not folded in.

Consequence: the hosted path is still exercised on every CI run (via `run-full-e2e.sh`), but the **refusal**
path — manual host, `CI=true` alone, self-hosted runner, non-temp workdir — is protected only by a test nobody
runs. A future refactor could re-broaden the deletion and CI would stay green. That is the exact failure mode
I2 was meant to prevent, and the I3 meta-check survives only because it lives inside `ci_contract_selftest`,
which *is* wired.

**Fix (additive, ~1 s of runtime, no gate weakened):** add two steps next to the packaging step in the
integration job, and add both paths to the contract's gate list so they cannot be deleted silently.

```yaml
# .github/workflows/go.yml, after the "Packaging contract checks" step (currently :335-341)
      - name: CI contract self-tests
        run: ./scripts/test-ci-contract.sh

      - name: Cleanup ownership tests
        run: ./e2e/test-run-cleanup.sh
```

```bash
# scripts/lib/ci-contract.sh:143-152, extend the existing gate loop
    'test-ci-contract.sh' \
    'test-run-cleanup.sh' \
```

Both are pure text plus a stub `docker` on `PATH`; neither needs the daemon, the built images or network. If
they are folded into `test-production-packaging.sh` instead, the recorded total becomes 342 + 9 + 18 = 369 and
the verification doc's row must be updated in the same commit.

### N2 — Minor (fix with N1): the cleanup test fails under this repo's own temp convention

`e2e/test-run-cleanup.sh:18` puts its scratch tree under `${TMPDIR:-/tmp}`, and `:141-147` asserts that a path
outside the temp root is refused. When `TMPDIR=/tmp/opencode` — the convention this repository uses everywhere
(`/tmp/opencode/<project>` in both runners) — the scratch tree lands **inside** the temp root, so the "non-temp"
fixture is actually a legitimate path and the guard correctly accepts it. Observed:

```
$ TMPDIR=/tmp/opencode ./e2e/test-run-cleanup.sh
FAIL workdir: non-temp path was accepted for deletion
FAIL workdir: non-temp path was deleted
16 checks passed, 2 failed
```

CI is unaffected (GitHub leaves `TMPDIR` unset → `/tmp`, so the fixture is genuinely outside the root), but a
developer following the repo convention sees a false red in the suite that is supposed to prove I2.

**Fix:** make the fixture path independent of `TMPDIR`, e.g. build it under a directory that is provably outside
the temp root and assert the precondition first:

```bash
temp_root="$(cd "${TMPDIR:-/tmp}/opencode" 2>/dev/null && pwd -P || printf '/tmp/opencode')"
outside="$ROOT/.cleanup-test-outside-$$"          # the checkout is never under the temp root
mkdir -p "$outside/docs"
# precondition: assert the fixture really is outside, so the refusal case is meaningful
[[ "$outside" != "$temp_root"/* ]] || fail 'workdir: fixture is inside the temp root; refusal cannot be tested'
if ( unset GITHUB_ACTIONS; run_cleanup_remove_workdir "$outside" ) >/dev/null 2>&1; then
  fail 'workdir: non-temp path was accepted for deletion'
else
  pass 'workdir: non-temp path refused'
fi
```

### N3 — Minor: the verification doc's packaging row is stale

`docs/ci-optimization-verification.md` still records `./scripts/test-production-packaging.sh` as
**337 passed**. With contract 69 + self-test 13 the fold is 342 (the author's own measurement). The row is the
table MAIN will read before the GitHub run; change 337 → 342. (I did not re-run packaging, per scope.)

### N4 — Minor: the `bash -c` claim in the M3 comment

`scripts/lib/ci-contract.sh:121-123` states the forbid covers a `bash -c` prefix. Verified it does not:

```
$ printf '          run: bash -c '\''docker buildx build .'\''\n' | grep -Eq '^ *([A-Za-z_][A-Za-z0-9_]*=[^ ]+ +)*(sudo +)?docker +buildx'
(no match)
```

Either reword to "any `VAR=…`/`sudo`-prefixed `docker buildx` invocation" or extend the pattern with an optional
`(bash|sh) +-c +['\"]?` prefix. Low risk either way: `:128-133` already make the bake the only way to pass.

### N5 — Minor: `CI_CONTRACT_FAILED_LABELS` is not restored by `_ci_expect`

`ci-contract.sh:242-248` saves and restores `CI_CONTRACT_PASSES`/`CI_CONTRACT_FAILURES` but leaves the label
array holding the mutant's labels. Harmless today — `ci_contract_check` calls `_ci_reset` (`:79`) on entry, so
every fixture starts clean and the final tally (`:360`) does not read labels. Add
`local saved_labels=(…); CI_CONTRACT_FAILED_LABELS=("${saved_labels[@]}")` if you want the invariant to be
local rather than incidental.

### N6 — Minor: the `write-all` fixture is not surgical

`ci-contract.sh:351` replaces the substring `  contents: read`, which also matches publish's `      contents: read`,
so the mutant rewrites both blocks. The fixture still proves what it must (the expected label
`workflow never grants write-all` fires), but it does not isolate the workflow-level case. Anchor the mutation
to column 0 (`_ci_mutate_replace … $'\n  contents: read' …`) if you want the fixture to be exact.

## Still open by design (no action)

- The full-stack gate has never executed on any host; runtime and the ClamAV health wait remain unknown and
  network-bound. First GitHub run is still the first signal — and per the doc, run 1 may be **slower** than the
  72 s baseline.
- `e2e/lib/run-cleanup.sh:42` takes `"$1"`/`"$2"` unguarded under `set -u`; both call sites pass two
  arguments, so this is cosmetic.
- The manual-host behaviour (leftovers reported, run red, cleanup by hand) is documented and accepted.

## What I did not review

Desktop UI, `frontend/e2e/*`, `docs/desktop-workspaces*`, and the concurrent uncommitted working-tree changes
(`docs/implementation-plan.md`, `docs/frontend.md`, `frontend/e2e/desktop-workspaces.spec.ts`) — owned by the UI
agent and explicitly out of scope. No gate, spec, or product file was modified by this review.


---

# Round 3 — final narrow re-review of N1–N6 (`2c1c049..83d8dda`)

Range is a **single CI commit** (`83d8dda`, parents = `2c1c049` = UI HEAD). Six files, none under `frontend/`
or `docs/desktop-workspaces*` — no UI change is interleaved. Scope: only the six open items from round 2 plus
any breakage this commit introduces. No new whole audit.

Executed (all pure text / stub; no Docker daemon, no Go, no Bun, no server, no `gh`, no subagents, no files
added to the repo):

- `bash -n` on all 7 shell files — 7/7 OK.
- `./e2e/test-run-cleanup.sh` under **three** TMP modes (unset, `/tmp/opencode`, `/tmp/opencode/opencode`) —
  **21 passed / 0 failed** in each.
- `./scripts/test-ci-contract.sh` — **14 passed / 0 failed**.
- `ci_contract_check` positive **75/0**, `ci_contract_selftest` **15/0**.
- Targeted probes: guard liveness (RED), symlink escape, root self-refusal, `TMPDIR=/` edge, N6 mutant diff,
  N4 regex matrix, shallow-clone fallback.

## Verdict

**APPROVED — all six items ADDRESSED, 0 open Critical or Important.** Publication remains gated on the actual
GitHub run; nothing in this round changes that.

| Item | Severity then | Status now |
| --- | --- | --- |
| N1 — guard suites never invoked | **Important** | **ADDRESSED** |
| N2 — cleanup fixture env-dependent (+ real temp-root bug) | Minor (real bug) | **ADDRESSED** |
| N3 — stale counts in the verification doc | Minor | **ADDRESSED** |
| N4 — `bash -c` claim stronger than the regex | Minor | **ADDRESSED** |
| N5 — failure labels not restored | Minor | **ADDRESSED** |
| N6 — `write-all` mutant not root-anchored | Minor | **ADDRESSED** |

## N1 — ADDRESSED

Two steps in the **existing** `integration` job, directly after `Packaging contract checks` and before
`Set up Docker Buildx` (`go.yml:346-350`); job count still 4, DAG unchanged (`integration` no `needs:`/no
`permissions:`, `publish` needs all three, global `contents: read`). Both scripts are `100755`.

The wiring is asserted twice over, and both assertions are live:

```bash
# scripts/lib/ci-contract.sh:152-153 (gate list) and :162-167 (name AND command)
'^        run: \./scripts/test-ci-contract\.sh$'   + 'Test CI contract regression guards'
'^        run: \./e2e/test-run-cleanup\.sh$'       + 'Test test-resource cleanup guards'
```

Removal mutants verified independently — each deletion trips exactly its own two labels, not "some error":

```
dropped scripts/test-ci-contract.sh -> failures=2
  labels: integration still runs scripts/test-ci-contract.sh | contract guard step is named and wired
dropped e2e/test-run-cleanup.sh     -> failures=2
  labels: integration still runs e2e/test-run-cleanup.sh | cleanup guard step is named and wired
```

`scripts/test-ci-contract.sh` also asserts the wiring textually (`:57-72`), so a workflow edit that keeps the
name but changes the command (or vice versa) is caught by both layers.

**New risk I checked because this is the first CI execution of that path:** `test-ci-contract.sh:65-79` prefers
`git show 0da549e:…` and falls back to an awk reconstruction "so the test still works in a shallow clone".
`actions/checkout@v6` is depth 1, so **CI will take the fallback**. Verified it still produces a rejected
workflow with the right label:

```
reconstructed integration head:  integration: / name: Integration / needs: [backend, frontend] / runs-on: ...
fallback failures=1   labels: integration does not wait on the quality jobs
```

So the step passes on a shallow checkout and does not turn the pipeline red for a harness reason. No action.

## N2 — ADDRESSED (this was a real runtime bug, not only a test bug)

`run_cleanup_temp_root` (`run-cleanup.sh:74-78`) now derives `<resolved TMPDIR>/opencode` deterministically —
no existence probe — and `run_cleanup_path_is_temp_owned` (`:84-96`) accepts exactly two roots:
`<TMPDIR>/opencode` and the fixed `/tmp/opencode`.

Consequences verified:

| Case | Result |
| --- | --- |
| Root itself (`/tmp/opencode`, `$(run_cleanup_temp_root)`) | **refused** (`case "$root"/*` requires a separator) |
| Child of an accepted root | accepted |
| `$HOME/...` (outside both roots) | refused |
| Symlink under `/tmp/opencode` → `$HOME` | **refused**, target untouched |
| Symlink → `$HOME/<dir>` with a file | **refused**, file intact |
| `TMPDIR=/` (degenerate) | root `//opencode`, `/opencode/x` **refused** — fails closed |
| `TMPDIR=/tmp/opencode` (repo convention) | root `/tmp/opencode/opencode`, and the real workdir `/tmp/opencode/<project>` is owned via the second root |

The last row is the genuine bug that round 2 surfaced: before this fix a `TMPDIR=/tmp/opencode` host would have
**refused to clean a real harness workdir**. Fixed, and the three-mode run proves it:

```
TMPDIR unset                    -> 21 passed, 0 failed
TMPDIR=/tmp/opencode            -> 21 passed, 0 failed
TMPDIR=/tmp/opencode/opencode   -> 21 passed, 0 failed
```

The negative fixture is now a dedicated empty throwaway under `$HOME` (fallback `/var/tmp`), created by the
test, asserted to be outside every root before use (`test-run-cleanup.sh:156-160`), and removed by exact path
in the `trap` (`:22, :174-175`). It never names the repository or a broad user directory, so the "must refuse
`rm -rf`" case cannot itself destroy project data. If `mktemp` fails the suite goes red rather than vacuously
green (an empty `OUTSIDE_ROOT` makes the refusal check fail).

RED proof reproduced — with the guard removed from the deletion path only, exactly the two labels the doc lists
fire:

```
CHECK 1 would FAIL: non-temp path was accepted for deletion
CHECK 2 would FAIL: non-temp path contents were deleted
```

(The harsher variant — stubbing the predicate to "always owned" — also breaks the fixture precondition, i.e.
three reds. The doc's figure of 2 is therefore accurate for the variant it names, not an overstatement.)

Unchanged and still correct: `run_cleanup_is_hosted_ephemeral` is still exactly
`GITHUB_ACTIONS == true && RUNNER_ENVIRONMENT == github-hosted` (`:21`); no new path authorises broad unowned
Docker deletion.

## N3 — ADDRESSED

Doc now states packaging **350** (335 with `ACTFB_SKIP_CI_CONTRACT_SELFTEST=1`), contract **75**, self-test
**15**, runner **14**, cleanup **21**, negative control **25**. Arithmetic is consistent and I reproduced the
underlying numbers: 337 − (66 + 11) + (75 + 15) = **350**, and 350 − 15 = **335**. The runner's own output
reports "25 failures, baseline 0da549e". No stale 337/342/18 figure remains as a current number.

## N4 — ADDRESSED

The comment (`ci-contract.sh:121-125`) now says what the pattern does: bare, `sudo`, leading `VAR=value`, and
explicitly "not a shell parser … `bash -c '…'` is out of scope". Verified against the matrix:

```
docker buildx build .              MATCH
sudo docker buildx build .         MATCH
GIT_SHA=x docker buildx build .    MATCH
bash -c 'docker buildx build .'    no-match   (now disclosed, not claimed)
```

No knowingly false data remains. The bake-shape checks are still the primary guard, and risk 7 in the doc's
table now records the text-matching limitation openly.

## N5 — ADDRESSED

`_ci_expect` saves and restores `CI_CONTRACT_FAILED_LABELS` alongside the counters
(`ci-contract.sh:265-273`), and `test-ci-contract.sh:47-56` asserts a failing invocation leaves labels, a
passing one leaves none. Runner output: `PASS failure labels reset between invocations (no stale match)`.
The fail-then-pass sequence does not leak into the final tally — self-test is 15/0 and the meta-check still
fires only in its subshell.

## N6 — ADDRESSED

`_ci_mutate_replace_line` (`:243-245`) uses whole-line equality. Verified on the generated mutant:

```
root block:     permissions: / write-all
publish block:        contents: read /       packages: write   (untouched)
```

Publish keeps both its read-only line and `packages: write`, so the fixture now isolates the workflow-level
case, and the expected label (`workflow never grants write-all`) is unchanged.

## No new breakage

- `go.yml` diff is exactly the two added steps; nothing else in the workflow moved. 4 jobs, DAG, triggers,
  permissions, bake, cache scopes and the 18-spec gate are all unchanged by this commit.
- Both runners still call `run_cleanup_remove_workdir … || true`, so a refusing helper cannot alter the exit
  status of a failing Playwright run — no false green, no masked failure.
- The guard suite runs with a stub `docker` on `PATH` and needs no daemon, so adding it to the job cannot
  interact with the job's service containers.
- `docs/ci-optimization-review.md` (this file) and `docs/desktop-workspaces-review.md` remain untracked
  artifacts; nothing here was staged, committed or pushed.

## Publication gate

Unchanged and still open by design: the first real GitHub run is the first execution of the 18 full-stack specs
and of these two guard steps on a hosted runner, and the 16 m 30 s baseline is still the only measured number.
A red result there is a real finding to report, not something to loosen. **This review raises no objection to
publishing** — no open Critical or Important item remains in CI scope.
