# CI optimisation verification (2026-11-26)

Status: **published and verified from `main` at `5ac573f`.** Two full pipelines are green on that exact
source: run `45909528261` (push, cold) and run `45946460043` (manual, warm). All ten images are publicly
pullable with `latest` and `sha-5ac573f…` resolving to the same digest, `linux/amd64`, revision
`5ac573f…`.

## Measured result: before vs after

Baseline `0da549e` (run `45889089999`) was the old scope: four sequential jobs, no full-stack browser gate.

| Job | Baseline 990 s | Cold 45909528261 | Warm 45946460043 |
| --- | --- | --- | --- |
| Frontend quality | 36 s | 39 s | 51 s |
| Backend quality | 188 s | 233 s | 184 s |
| Integration | 738 s | **1417 s** | **1360 s** |
| Publish images | 54 s | 54 s | 46 s |
| **Wall clock** | **990 s (16 m 30 s)** | **1475 s (24 m 35 s)** | **1413 s (23 m 33 s)** |

All three quality jobs start at the same second in both new runs, so the dependency removal works.

### Where the time went — this is a coverage change, not a speed win

| Step | Baseline | Cold | Warm |
| --- | --- | --- | --- |
| full-stack browser gate | did not exist | 349 s | 350 s |
| default-profile browser | 173 s | 278 s | 279 s |
| real-browser | 90 s | 121 s | 136 s |
| build all production images | 72 s | 93 s | **8 s** |

- **H1 (parallel jobs)** removed ~188 s of serial wait, as predicted.
- **H2 (working gha cache)** is proven: the integration build went **93 s → 8 s** with **82 `CACHED`**
  lines on the warm run versus 54 cold. The credential theory was correct — the same ten scopes now read
  and write across runs.
- **H3 (full-stack gate)** adds ~350 s plus ~105 s of extra browser time from the new UI geometry, i.e.
  roughly +8 minutes of genuine coverage.

Net wall clock is therefore **~7 minutes slower**, and that is the honest number. The added time buys 18
previously-skipped specs that now execute on every run; it is not a regression in efficiency per unit of
coverage, and no speed improvement is claimed.

on this machine but has not been pushed, so `main` still carries the previous workflow and GHCR `latest`
still points at the pre-change build.

## What this change is

Measured baseline first, then four changes:

1. `integration` no longer waits for the two quality jobs (`needs:` removed). It consumes no artifact from
   them — it checks out, installs Go/Bun and builds the ten images itself — so the dependency only serialised
   it behind ~3 minutes of idle runner time.
2. The hand-written `docker buildx build` loop becomes `docker/bake-action@v7` with `push: false`,
   `load: true` and ten tag overrides keeping `actfb-<target>:ci`. The action receives the Actions cache
   credentials that a `run:` step does not.
3. The 18 browser specs that were being collected and skipped become a real gate through
   `./e2e/run-full-e2e.sh full-stage.spec.ts organizer-ux.spec.ts`.
4. `scripts/test-production-packaging.sh` stops grepping workflow prose and asserts the job graph, the build
   shape and the publish gate semantically, with a self-test that proves each assertion rejects an unsafe
   edit.

`publish` still needs all three prior jobs and is still main-only, so nothing is pushed unless every gate
passes.

## Baseline measurement (actual, not estimated)

| Field | Value |
| --- | --- |
| Run | https://github.com/nrf24l01/aCTFb/actions/runs/45889089999 |
| Commit | `0da549e35ba0fa4aab811abc96a0411a8482e966` |
| Event | `workflow_dispatch` on `main` |
| Conclusion | success, 4/4 jobs |
| Wall clock | **990 s = 16 m 30 s** |
| Queue latency | 3 s |

| Job | Start (UTC) | End (UTC) | Duration |
| --- | --- | --- | --- |
| Frontend quality | 20:10:38 | 20:11:14 | **36 s** |
| Backend quality | 20:10:39 | 20:13:47 | **188 s** |
| Integration | 20:13:50 | 20:26:08 | **738 s** |
| Publish images | 20:26:10 | 20:27:04 | **54 s** |

Critical path was `backend 188 s → integration 738 s → publish 54 s`. Frontend finished 152 s before backend,
so it never gated anything.

## Predictions vs what actually happened

The predictions were made before the first run and are kept here so the comparison is honest.

| Change | Predicted | Actual |
| --- | --- | --- |
| H1: integration independence | ≈ −193 s of serial wait | ~188 s removed; all three quality jobs now start at the same second |
| H2: working gha cache in the build | ≈ −40 to −50 s | **−85 s** (93 s → 8 s), 82 `CACHED` lines warm vs 54 cold |
| H3: full-stack gate | unknown, expected to add time | **+350 s**, plus ~105 s extra browser time from new UI geometry |
| Net wall clock | not claimed | **+~7 min** (990 s → 1413 s warm) |

H2 overshot the estimate because the whole ten-target export collapses to cache hits, not just the compile.
H3 dominates, exactly as flagged. The honest conclusion is unchanged: this is a coverage gain that costs
wall clock, and the two effects must not be netted against each other.

## Cache shape: cold versus warm

The bake-action change is what makes the ten `type=gha,mode=max` scopes in `docker-bake.hcl` reachable at all.
Evidence that they were previously unreachable:

- Baseline integration build: **0 `CACHED` of 27** steps for `backend`; `#20 … go build` took 37.6 s. The
  eight worker targets that followed showed `CACHED=8`, but that is local builder reuse of the shared `build`
  stage built seconds earlier in the same run, not cross-run cache.
- The integration step's environment contained only `ACTFB_*` and `GOTOOLCHAIN` — no `ACTIONS_RUNTIME_TOKEN`,
  no `ACTIONS_CACHE_URL`.
- The Actions cache API showed **zero** entries created or accessed during the integration build window
  (20:24:22–20:26:00). The 102 entries created in that hour are all stamped 20:26:27–20:26:38, i.e. the
  publish window.
- `publish`, which uses `docker/bake-action@v7` and therefore does receive the credentials, scored
  **29 `CACHED` of 86**.

So the credential hypothesis is **proven at the action level**: the same scopes and the same definition behave
completely differently depending on whether the action supplies runtime cache auth. What that does **not**
prove is that the first warm run will hit. It cannot — the cache is empty for this pipeline until a run
populates it. Hits are expected from the second run onward and are not claimed here.

Other cache facts, unchanged by this patch:

- `setup-go` key includes the runner image and Go version
  (`setup-go-Linux-x64-ubuntu26-go-1.27.1-<go.sum hash>`), 360,477,398 B, restored in ≈4 s (backend) and
  ≈6 s (integration).
- Bun key `bun-vyrKTwhw5PEh6pGRz1Zgyle11uQ=`, 50,544,731 B, restored in both jobs.
- Store usage 212 entries / 1,315,151,009 B (1.22 GiB) of 10 GiB — no eviction pressure.

Two jobs now restore the same `setup-go` key concurrently. That is safe: each job runs on its own runner, the
cache is read-only at restore time, and no lock exists to contend on.

## Full-stack gate: 18 new specs, now executing on every run

Baseline `Run real-browser acceptance` collected 24 specs and reported **1 passed, 23 skipped**. That 23
decomposes exactly:

| Spec file | Count | Baseline | After |
| --- | --- | --- | --- |
| `full-stage.spec.ts` | 7 | skipped (`E2E_FULL_STACK` unset) | **executed by the new gate** |
| `organizer-ux.spec.ts` | 11 | skipped (`E2E_FULL_STACK` unset) | **executed by the new gate** |
| `default-profile.spec.ts` | 5 | skipped here | already executed by its own step |
| `p1-core.spec.ts` | 1 | the one that passed | still executed by `run-e2e.sh` |

So the new gate covers **18** specs. The other 5 were never a coverage gap — they run in
`Run default-profile browser acceptance`. `p1-core.spec.ts` is not duplicated: `run-e2e.sh` is now scoped to
it explicitly and the full gate collects only the other two files.

Verified by collection, not by assumption:

```
$ bunx playwright test full-stage.spec.ts organizer-ux.spec.ts --list
Total: 18 tests in 2 files

$ bunx playwright test p1-core.spec.ts --list
Total: 1 test in 1 file

$ bunx playwright test --list          # operator default, unchanged
Total: 24 tests in 4 files

# overlap between the scoped 18 and default-profile/p1-core: 0
```

The 18 tests:

| # | Spec | Test |
| --- | --- | --- |
| 1 | full-stage | plugin install / explicit grant / iframe bridge / API / disable / upgrade |
| 2 | full-stage | trusted-js default-deny approval and verified full-trust ESM mount |
| 3 | full-stage | P1R TEAM manual text reload, clean file scan, and reviewer accept |
| 4 | full-stage | A+D awards appear on the canonical board scoreboard |
| 5 | full-stage | PENTEST approved binding, Docker environment, durable report and reviewer accept |
| 6 | full-stage | deployment template publish, scoped allowlist, and instance start/reset/destroy |
| 7 | full-stage | instance lifetime: module settings, task overrides, real extend cap and reset |
| 8 | organizer-ux | anonymous landing renders organizer Markdown as real DOM after an admin edit |
| 9 | organizer-ux | global group membership picker searches the server beyond the first page |
| 10 | organizer-ux | board access is one control: public browse, audience and invite stay private |
| 11 | organizer-ux | module has one format and the task editor inherits it |
| 12 | organizer-ux | draft FLAG task with Markdown publishes and accepts a real submission |
| 13 | organizer-ux | every editor confirms before discarding unsaved work |
| 14 | organizer-ux | scoped pickers only offer eligible board subjects |
| 15 | organizer-ux | legacy invite-only boards keep their invite affordances |
| 16 | organizer-ux | legacy board saves metadata without touching its access gate |
| 17 | organizer-ux | typed instance parameters bind a real task from the published manifest |
| 18 | organizer-ux | instance task editor offers typed template parameters, not raw JSON |

**Runtime is now measured: 349 s cold, 350 s warm** — stable across both runs, so the ClamAV signature
download plus health wait dominates and does not vary much between a cold and warm build cache. Both runs
report `18 passed`, so no spec is silently skipped by a feature guard. The step stays bounded at 45 minutes
inside a 120-minute job.

### Why it was not executed on this machine

The runner's cleanup force-removes any container created during its window. That is correct on an ephemeral
CI VM and unsafe beside a live operator stack, so the removal is now gated (see "Cleanup ownership" below)
and the run still belongs on the CI host rather than this machine, which has `actfb-prod-*` on 8080.

The specs were not weakened to make them runnable in a smaller profile. The gate now has three independent
green executions: a local rehearsal (`18 passed`, 4.9 min) and both CI runs (`18 passed`, 5.1 min each).

## Cleanup ownership

Root cause: `cleanup_new_docker_resources` diffed container IDs and force-removed **everything** new. Volumes
were namespace-filtered; containers were not. On a shared host that destroys a concurrent `docker compose up`,
a recreated buildx builder, or an operator container.

The policy now lives in `e2e/lib/run-cleanup.sh` and is applied by both runners:

| Resource | Rule |
| --- | --- |
| Containers/volumes the harness created by exact name (`$CLAMAV_NAME`, `$PG_NAME`, …) | always removed — safe on any host |
| Anything else that appeared during the run | removed **only** when `GITHUB_ACTIONS == true` **and** `RUNNER_ENVIRONMENT == github-hosted`; otherwise reported and left in place |
| Volumes in the difference set | stay filtered to `^actfb` **even on a hosted runner**, so a concurrently created operator volume is never deleted |
| The scratch `$WORKDIR` | removed only after resolving it under the shared temp root, so a bad `E2E_WORKDIR` cannot delete a checkout |

`${CI:-}` alone is deliberately **not** accepted: a shared CI box or a developer machine can set `CI=true`
without being disposable. A self-hosted runner (`RUNNER_ENVIRONMENT=self-hosted`) is denied.

When removal is refused, the existing leak assertion still runs and fails the script, so a leftover cannot be
mistaken for success. The cost of the conservative choice is documented: on a manual host a failed full-stack
run may need explicit cleanup of its own containers.

Verified with `./e2e/test-run-cleanup.sh` — 21 checks against a stub `docker` on `PATH`, no daemon, and
passing under both the default `TMPDIR` and `TMPDIR=/tmp/opencode`:
a bystander container survives a manual host, `CI=true` alone does not authorise deletion, a self-hosted
runner is denied, a hosted runner removes its own and the bystander but never a pre-existing container,
non-`actfb` volumes are untouched, and a non-temp workdir is refused. Removing the guard from the helper
turns 5 of those checks red, so they are live rather than decorative.

## Failure evidence capture

Root cause: both runners assigned `E2E_OUTPUT_DIR`/`E2E_REPORT_DIR` unconditionally inside `$WORKDIR`, which
`cleanup()` deletes on every exit including failure. `playwright.config.ts` produces trace/screenshot/video
`on-failure` — so a failing gate deleted its own evidence, and the workflow's `if: failure()` upload collects
only workspace paths.

Both runners now honour a caller-supplied directory (`${E2E_OUTPUT_DIR:-…}`), and the workflow sets
`${{ github.workspace }}/e2e/test-results/<gate>` and `…/playwright-report/<gate>` for both browser steps. The
scratch workdir — which holds the generated PostgreSQL credentials — is still deleted, and the run log now
prints the two artifact directories (paths only, never file contents).

What the upload can therefore contain: Playwright traces, screenshots and video, plus the HTML report. Traces
record browser network activity, which includes the synthetic CI bearer tokens issued by the harness's
ephemeral accounts. Those accounts and their credentials are generated per run, never persisted, and the
generated PostgreSQL password lives only in the deleted workdir. No operator data, no `config.yaml`, no
`.env` and no repository secret is written to these paths.

## Contract self-test exactness

Root cause: `_ci_expect` treated *any* failure as a rejection, so a fixture whose mutation accidentally broke
an unrelated check would still pass. It also injected `packages: write` at an indentation that is not valid
YAML, so the fixture did not prove the check fires on a real job-level block.

Now each fixture names the failure label it must produce, matched as a fixed string against the recorded
labels, and failure labels are stable identities with detail carried separately (so an expected label never
embeds an interpolated value). The permissions fixture is a correctly indented job-level block. Two fixtures
were added: workflow-level `write-all` (M1) and a meta-check proving a wrong expected label fails. Verified
RED: pointing the push fixture at the wrong label makes the self-test fail with
`expected failure label: … / actual failure labels: …`.

## Round-2 review fixes (N1–N6)

Second review found one Important item: the two guard suites existed but **nothing in CI ran them**. The rest
were correctness and honesty repairs to the guards themselves.

### N1 — the guards are now real CI steps

Two steps in the existing `integration` job, immediately after `Packaging contract checks` (pure text, no
service needed, and before `Set up Docker Buildx` so a guard failure fails fast):

```yaml
      - name: Test CI contract regression guards
        run: ./scripts/test-ci-contract.sh

      - name: Test test-resource cleanup guards
        run: ./e2e/test-run-cleanup.sh
```

The job count stays at four. The contract now asserts each step by **name and command**, and two new mutants
prove the wiring is load-bearing: deleting either step is rejected with its own label
(`integration still runs scripts/test-ci-contract.sh`, `integration still runs e2e/test-run-cleanup.sh`).
Verified RED: removing the contract-guard line trips exactly those two checks.

Both scripts are committed `100755`, so the `run:` lines are executable.

### N2 — the cleanup fixture was environment-dependent (true RED)

`./e2e/test-run-cleanup.sh` under `TMPDIR=/tmp/opencode` failed 2 of 18. Two causes, both real:

1. **The fixture was only accidentally outside the temp root.** It used `$WORK/fake-repo`, where `$WORK`
   itself is created under `${TMPDIR:-/tmp}`. Whether that counted as "outside" depended on the helper
   resolving `${TMPDIR}/opencode` — and on this host `/tmp/opencode/opencode` happens to exist, so the
   fixture sat outside it. On a host without that directory the fallback `/tmp/opencode` swallowed the
   fixture and the negative case broke. Existence-dependent behaviour is the defect.
2. **The same instability was a runtime bug, not just a test bug.** With `TMPDIR=/tmp/opencode` — this
   repository's own convention — the helper's temp root became `/tmp/opencode/opencode`, so the real workdir
   `/tmp/opencode/<project>` was **not** under it and `run_cleanup_remove_workdir` would have refused to
   clean up a genuine harness workdir.

Fix: `run_cleanup_temp_root` now resolves the base directory and appends `opencode` deterministically, without
probing for an existing nested directory, and `run_cleanup_path_is_temp_owned` accepts both
`<TMPDIR>/opencode` and the fixed `/tmp/opencode` default (the runners default their workdir to
`/tmp/opencode/<project>` either way). The production guard is unchanged in strength: it still refuses
anything outside those roots.

The negative fixture now uses a dedicated empty throwaway directory under `$HOME` (falling back to `/var/tmp`),
created by the test and removed exactly on `trap`. It never points at the repository or a broad user
directory — pointing the "must refuse `rm -rf`" test at real data would be the very hazard under test. The test
also asserts the fixture base really is outside every temp root, and fails loudly if it is not, so the case can
never silently pass for the wrong reason. Added: absent-path no-op, and `/tmp/opencode` default accepted.

| Run | Result |
| --- | --- |
| default `TMPDIR` | **21 passed, 0 failed** |
| `TMPDIR=/tmp/opencode` | **21 passed, 0 failed** |
| guard disabled (RED) | **2 failed** — `workdir: non-temp path was accepted for deletion`, `… contents were deleted` |

### N3 — counts in this document are the current ones

`350` packaging checks (not the earlier 342, and not the historical 337), contract `75`, self-test `15`,
contract runner `14`, cleanup `21`, negative control `25`. The 337 figure belongs to an earlier phase and is
not used as a current number anywhere.

### N4 — no false claim about `bash -c`

The buildx guard's comment claimed it covered `bash -c` prefixes. It does not: the pattern matches bare,
`sudo`, and leading `VAR=value` invocations. The comment now says so and states plainly that this is a
pattern guard for known forms, not a shell parser, and that an obfuscated call is out of scope. The bake-shape
checks remain the primary guard. No shell parser was added — repository trust plus source review is the
control there.

### N5 — failure labels reset with the counters

`_ci_expect` restored `CI_CONTRACT_PASSES`/`CI_CONTRACT_FAILURES` but not `CI_CONTRACT_FAILED_LABELS`, so a
label left by an earlier fixture could satisfy a later expectation that never fired. The label array is now
saved and restored alongside the counters, and `scripts/test-ci-contract.sh` asserts the snapshot does not
leak (fail, then pass, then expect zero labels).

### N6 — the `write-all` mutant is root-anchored

The mutant used a substring replace on `  contents: read`, which also matches publish's more deeply indented
copy. Added `_ci_mutate_replace_line` (whole-line equality) so only the root `permissions:` child is
rewritten. Publish's read-only line and its `packages: write` are untouched, and the expected label is
unchanged.

## Round-2 validation (actual)

| Check | Result |
| --- | --- |
| `bash -n` on all changed scripts | OK |
| Contract positive | **75 passed, 0 failed** |
| Contract self-test | **15 passed, 0 failed** |
| Contract runner (`scripts/test-ci-contract.sh`) | **14 passed, 0 failed** |
| Cleanup guards, both TMPDIR modes | **21 passed, 0 failed** each |
| Packaging script | **350 passed, 0 failed**, 1 SKIP |
| Workflow YAML | parses; 4 jobs; integration no `needs`/`permissions` |
| Step modes | `100755` for both new guard scripts |

No Docker build, no Go or frontend run, no server, no `gh run`. The full-stack gate is still **unexecuted** —
it will run for the first time on the CI host after approval.

## Local validations performed

All commands below were run on this machine at the change, after applying it. Exit codes are the real ones.
No Docker build, no Go or frontend test run, no server: the cleanup tests use a stub `docker` on `PATH`.

| Check | Command | Result |
| --- | --- | --- |
| Patch applied exactly | `diff <(grep -v '^index ' prepared.patch) <(grep -v '^index ' <(git diff))` | byte-identical |
| Shell syntax | `bash -n` on all changed scripts | OK |
| Contract, positive | `ci_contract_check .github/workflows/go.yml docker-bake.hcl` | **75 passed, 0 failed** (deterministic over 3 runs) |
| Contract self-test | `ci_contract_selftest …` | **15 passed, 0 failed** — 13 mutants + 1 meta-check + 1 positive control, each mutant pinned to its own expected label |
| Contract, negative control | `ci_contract_check` against baseline `0da549e` | **25 failed** — the gate is live, not decorative |
| Contract test runner | `./scripts/test-ci-contract.sh` | **14 passed, 0 failed** |
| Cleanup policy tests | `./e2e/test-run-cleanup.sh` (mock `docker`, no daemon) | **21 passed, 0 failed**, identical under `TMPDIR` unset and `TMPDIR=/tmp/opencode` |
| Packaging script | `./scripts/test-production-packaging.sh` | **350 passed, 0 failed**, 1 SKIP (335 with `ACTFB_SKIP_CI_CONTRACT_SELFTEST=1`) |
| Workflow YAML | PyYAML 6.0.4 (preinstalled) | parses; 4 jobs; `integration` has no `needs`, no `permissions`; `publish` keeps `needs: [backend, frontend, integration]` |
| Bake definition | `docker buildx bake -f docker-bake.hcl --print` with the ten `set` overrides | 10 targets, exact `actfb-<target>:ci` tags, `context: .` (frontend `frontend`), `linux/amd64`, per-target gha scopes with `mode=max` |
| Test collection | `bunx playwright test … --list` | 18 / 1 / 24 as tabulated above |
| `actionlint` | `actionlint .github/workflows/go.yml` | **not run — not installed on this host.** Not claimed as passing |

The single SKIP is `SKIP actual image checks (set ACTFB_TEST_IMAGES=1 after building local images)`. That is
correct here: the local `actfb-*:ci` tags, if any, predate this change, and asserting against stale images
would be a false claim. Those checks run in CI after the build step loads the real images.

Bake `--print` also confirms the build arguments survived the switch:
`GIT_SHA`/`IMAGE_TAG` for every target and `VITE_API_BASE_URL=/api` for the frontend, so the SPA keeps
proxying `/api/v2` rather than a generic base path.

## Contract hardening: what the old checks could not see

The previous checks were file-wide greps, including

```bash
require_pattern .github/workflows/go.yml 'actfb-\$\{target\}:ci'
grep -A4 '^  integration:' … | grep -Eq 'needs:.*\[.*backend.*frontend'
```

Two weaknesses: a file-wide pattern is satisfied by a comment rather than by behaviour, and a fixed
`-A4`/`-A6` window breaks whenever a comment line is added. The replacement parses job blocks and asserts per
block, and it re-runs itself against mutated copies.

The self-test caught a real instance of the first weakness during development: an early draft put the words
`push: false` and `load: true` in a comment, and the substring check passed a workflow whose actual step had
been mutated to push. The checks are now line-anchored and the prose was reworded, so a comment can no longer
satisfy a gate.

`forbid_pattern 'docker push'` is retained, and `push: true` is forbidden only **inside the integration
block**, so publish's legitimate push still passes.

## The `GOARCH` non-issue

An earlier report in this session rendered the Dockerfile build line as a garbled token. **That was a
display artifact, not a defect in the file.** Byte-exact check of the committed blob:

```
$ git show HEAD:Dockerfile | sed -n '31p' | od -c
… C G O _ E N A B L E D = 0   G O O S = l i n u x   G O A R C H = a m d 6 4   \
```

Target architecture is configured correctly; no Dockerfile change was needed, and none is part of this
work. `docs/instance-lifetime-verification.md` already records the same finding from an earlier session.

Related: `GOFLAGS` / `-p=1` never existed in this Dockerfile (`git log -S GOFLAGS -- Dockerfile` is empty),
so it was not added. Build memory is bounded by `max-parallelism = 2` plus the already-sequential
`go build` loop.

## Publication state

Ten images are published at `sha-5ac573fa864d456101242c866db18480243ca237` from run `45909528261`, and the
warm run `45946460043` pushed the same source again. For every image `latest` and the immutable SHA tag
resolve to the **same digest**, confirmed by anonymous pull (no stored credentials):

| Image | Digest (`latest` == `sha-5ac573f…`) | Compressed |
| --- | --- | --- |
| `actfb-backend` | `sha256:cab864f0a7a1d80de97c6a4b8ad69f5c61b93c0abe5320f1dca1ecc23dc82b86` | 20,454,128 B |
| `actfb-frontend` | `sha256:5b9d29288920ab302d2a08b7a95a40ded7bb7134b7ea38f106a9c11745b34a10` | 23,877,622 B |
| `actfb-notification` | `sha256:a47145c9314dfdd60340115d324fc018182af0ef5e6e969964d90f69503ca763` | 12,342,732 B |
| `actfb-retention` | `sha256:67d04b483ca0cc97c3a537a9ad111bbf90c173ee3464a0966bcb7e85d334cbba` | 10,940,563 B |
| `actfb-events-dispatcher` | `sha256:da25c84752899ca7905ad3a00e0e1f789a206154a9136e56137b726623a4b0aa` | 12,001,243 B |
| `actfb-file-scanner` | `sha256:9ddf45af3b8354b2f979a014696689cf3f68b892db6b9e52e129e466d09032be` | 13,359,289 B |
| `actfb-storage-init` | `sha256:34c2b33ff5990b01ed73c8d20ed517c64fe713cf1c8292c41a3649f0495ba888` | 10,956,257 B |
| `actfb-plugin-runner` | `sha256:20e20b82a4fc83f521e591f13519a9e6778e0b7639da8ca51bbdd188636c0e46` | 10,292,879 B |
| `actfb-ad-worker` | `sha256:f6176cdc25d0b5e3a173cb5f52859e1551bf968d4942b63d32ce219873e5395d` | 27,023,886 B |
| `actfb-instance-worker` | `sha256:a2454abf0c8b88d5dcf0df17992183a2c42b46887c8900bc9d993fd2020bdebc` | 13,932,167 B |

Total compressed 155,180,766 B. The backend config blob reports `os/arch = linux/amd64`,
`org.opencontainers.image.revision = 5ac573fa864d456101242c866db18480243ca237` and
`org.opencontainers.image.version = sha-5ac573f…`, so the tags and the embedded metadata agree.

Operators need no migration, no `config.yaml` and no `.env` change for this range: the schema is unchanged
and the workflow change is CI-only.

## Resolved risks (now measured)

| # | Was | Outcome |
| --- | --- | --- |
| 1 | Full-stack gate never run; duration unknown | **349 s cold / 350 s warm**, `18 passed` both times |
| 2 | Gate might push the job past 120 min | Integration peaked at 1417 s (23 m 37 s), well inside the budget |
| 3 | First pipeline cold and slower | Confirmed: build 93 s cold vs 8 s warm; the export cost is real but one-time |
| 4 | H1 makes a red commit more expensive | Accepted; `publish` still blocks on all three jobs |
| 5 | Independence is a snapshot assertion | Unchanged by design |
| 6 | `bake-action` tag overrides unproven on a runner | Proven: the runtime/packaging smokes passed and the ten tags resolved |
| 7 | Contract is text matching, not a parser | Unchanged by design |

## Remaining limitations

- **Net wall clock is ~7 minutes slower.** This is a coverage decision, not a speed claim. If the added
  browser time is not wanted, the gate is one step to remove — but it would restore 18 silently skipped
  specs.
- The contract is pattern matching, not a YAML or shell parser. Obfuscated build calls are out of scope;
  repository trust plus source review is the control there.
- `actionlint` was never run (not installed on the authoring host). Not claimed as passing.
- The RustFS workdir at the end of the gate emits `rm: Permission denied` for files owned by uid 10001. It is
  `|| true`-guarded and cannot fail the job; it only leaves a directory on an ephemeral runner. Not fixed
  here because the alternative is a broad `chmod`, which is worse.
- `frontend/e2e/board-flow.spec.ts` still clicks the removed `Join this board` control. That spec is not
  referenced by the workflow or any e2e script, so it is out of scope and untouched.

## Not verified here

- `actionlint` (not installed).
- Anything about the desktop UI work beyond the fact that its browser specs pass in CI.
- Operator production containers were never touched: no pull, no restart, no deploy. `latest` moved on
  GHCR, but the running stack keeps whatever it started with until an operator runs `compose pull`.
