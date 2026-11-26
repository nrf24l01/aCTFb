# CI optimisation verification (2026-11-26)

Status: **implemented locally, pending CI, not yet published.** The five code files are applied and validated
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

## Theoretical savings — not yet measured

| Change | Basis | Theory |
| --- | --- | --- |
| H1: integration independence | 188 s of dead wait removed from the serial path | **≈ −193 s (−19.5 %)** |
| H2: working gha cache in the integration build | 37.6 s cold `go build` + 11.4 s `bun run build` | **≈ −40 to −50 s** |

These are arithmetic on measured durations, **not** measurements. No second run has been performed.

**Run 1 is expected to be slower, not merely "cold", for two concrete reasons.** Neither is a regression:

1. The integration build now performs a real `mode=max` cache **export** that it previously skipped entirely —
   without credentials the gha exporter silently no-oped, so that cost simply did not exist. The
   `Build all production images` step may therefore exceed the 72 s baseline on run 1.
2. `scripts/lib/ci-contract.sh` is a new file under `scripts/`, and `.dockerignore` does **not** exclude
   `scripts/`, so `COPY . .` changes and the 37.6 s compile is cold for that reason as well. `.github/`,
   `e2e/` and `docs/` *are* excluded, so future CI-only edits will not bust the image cache.

The second pipeline is the first real signal for H2, and the two `Build all production images` durations
should be compared before quoting any saving. No net improvement is claimed anywhere in this document: the
full-stack gate adds wall clock and its cost is unknown.

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

## Full-stack gate: 18 new specs, runtime unknown

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

**Runtime of this gate is unknown.** It has never executed in CI, on any runner. The dominant cost is the
ClamAV signature download plus its health wait (fresh volume every run), which is network-bound. It is
expected to **add** wall clock, and must not be netted against the H1/H2 savings. The step is bounded at
45 minutes inside a 120-minute job.

### Why it was not executed on this machine

The runner's cleanup force-removes any container created during its window. That is correct on an ephemeral
CI VM and unsafe beside a live operator stack, so the removal is now gated (see "Cleanup ownership" below)
and the run still belongs on the CI host rather than this machine, which has `actfb-prod-*` on 8080.

The specs were not weakened to make them runnable in a smaller profile, and this document does not claim the
gate passed anywhere. It has never executed, on any host.

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

Verified with `./e2e/test-run-cleanup.sh` — 18 checks against a stub `docker` on `PATH`, no daemon:
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

## Local validations performed

All commands below were run on this machine at the change, after applying it. Exit codes are the real ones.
No Docker build, no Go or frontend test run, no server: the cleanup tests use a stub `docker` on `PATH`.

| Check | Command | Result |
| --- | --- | --- |
| Patch applied exactly | `diff <(grep -v '^index ' prepared.patch) <(grep -v '^index ' <(git diff))` | byte-identical |
| Shell syntax | `bash -n` on all changed scripts | OK |
| Contract, positive | `ci_contract_check .github/workflows/go.yml docker-bake.hcl` | **69 passed, 0 failed** (deterministic over 3 runs) |
| Contract self-test | `ci_contract_selftest …` | **13 passed, 0 failed** — 11 mutants + 1 meta-check, each pinned to its own expected label |
| Contract, negative control | `ci_contract_check` against baseline `0da549e` | **19 failed** — the gate is live, not decorative |
| Contract test runner | `./scripts/test-ci-contract.sh` | **9 passed, 0 failed** |
| Cleanup policy tests | `./e2e/test-run-cleanup.sh` (mock `docker`, no daemon) | **18 passed, 0 failed** |
| Packaging script | `./scripts/test-production-packaging.sh` | **337 passed, 0 failed**, 1 SKIP |
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

Only the baseline is published. Run `45889089999` pushed ten images at
`sha-0da549e35ba0fa4aab811abc96a0411a8482e966`, and all ten resolve anonymously (HTTP 200):

| Image | Digest |
| --- | --- |
| `actfb-backend` | `sha256:8e1669023b8c87cdbc7c47225da7dd427c33e102b512ec9e2ba8ebee068ef01f` |
| `actfb-frontend` | `sha256:ab741340c28820179ebd666e04dd01b47ef32e9b3c7be5ccd7445cb132c684f9` |
| `actfb-notification` | `sha256:85f7d486bcbebe10249fc380559bf61ad7797f43605fd84eca376bbbfaaf1d62` |
| `actfb-retention` | `sha256:7230ac1c725159cc07d776a1ef1533085bc76c5d5be0bfe3b9aae20015d1cc5e` |
| `actfb-events-dispatcher` | `sha256:ee6f71bf1b9b7f8cb74dcba3c374df7a22bc164a65604273c9368ee9732277da` |
| `actfb-file-scanner` | `sha256:474ac2618c3d39e96bf89bad56b7264e26c77a2963b3a039fd101761e097a8b3` |
| `actfb-storage-init` | `sha256:dc9efbd4ad038ea4044a5cfb9b88de7fc79a72eec765fb3fd25a118c2721da7d` |
| `actfb-plugin-runner` | `sha256:fc2c330a1b1b648b46ef2aed49377d5154283b16cdb25a67bdcc3c4a6241f5ca` |
| `actfb-ad-worker` | `sha256:ca39e387beeba8e4e5abe690b18639ed7efa72c4ac18358c8e279f4392739801` |
| `actfb-instance-worker` | `sha256:2c98e57c2bf13247c0573c58b8c0df56d806f966c82f4f821b3d9a9c093c681f` |

Total compressed 155,182,305 B. The new UI work is **not** published: `latest` will only move once this
change reaches `main` and a new run passes.

## Open risks

| # | Risk | Handling |
| --- | --- | --- |
| 1 | The full-stack gate has never run in CI; duration unknown, ClamAV download is a flake source | 45-minute step bound inside a 120-minute job. A real failure is a product finding, not something to loosen |
| 2 | If the gate plus existing steps exceed 120 minutes, the job times out | Watch the first run; raising `timeout-minutes` is a one-line follow-up |
| 3 | First pipeline after landing is cold and may be slower (first real cache export + new `scripts/` file busts the compile) | Expected, see above. Second pipeline is the first measurement of H2 |
| 4 | H1 makes a red commit more expensive | Accepted and documented in the workflow comment. Green commits are strictly faster; `publish` still blocks on all three |
| 5 | The integration independence assertion is a known-snapshot check | If a genuine artifact dependency is ever wanted, that assertion must change deliberately |
| 6 | `bake-action` tag overrides verified from the action schema and `bake --print`, not yet observed on a real runner | If a tag were wrong, the packaging/runtime smokes fail loudly in the same job |

## Not verified here

- `actionlint` (not installed).
- The full-stack gate end to end (see above).
- Anything about the desktop UI work committed by another agent — not in scope, not claimed.
- Actual post-change run times. Nothing in this document beyond the baseline table is measured.
