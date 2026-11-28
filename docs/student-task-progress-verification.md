# Student task progress — verification and publication

Scope: Task 3 evidence for the approved student task progress slice
(`docs/superpowers/specs/2026-11-27-student-task-progress-design.md`, plan
`docs/superpowers/plans/2026-11-27-student-task-progress.md`). Product source `8154f1b`
(backend/contract) + `12872e1` (UI/filters), plus one editor fix `5d840ab` for a proven authoring bug
found by the Task 3 gates. The whole branch `6aa65d5..2114af4` passed an independent final review —
Spec and Quality APPROVED, no Critical or Important findings (`docs/student-task-progress-review.md`).
Main inspected five screenshots and approved the composition with no UI redesign.

The slice is **published**: `main` at `91d76b6a1a8a2c8f292fe6941d9d8472827049ed` ran one push-triggered
CI workflow, all four jobs succeeded, and all ten GHCR images were verified anonymously. Sections below
keep the local gate record and add the actual CI and registry evidence.

## Publication (actual CI + GHCR, `91d76b6`)

| Field | Value |
| --- | --- |
| Workflow run (push) | https://github.com/nrf24l01/aCTFb/actions/runs/46132838936 |
| Commit (image source) | `91d76b6a1a8a2c8f292fe6941d9d8472827049ed` |
| Run window | 09:57:05Z → 10:20:45Z (~23.7 min) |
| Jobs | Backend quality success, Frontend quality success, Integration success, Publish images success (4/4) |
| Immutable tag | `sha-91d76b6a1a8a2c8f292fe6941d9d8472827049ed` |
| Platform | `linux/amd64` only (read from each image config) |
| Registry visibility | public; anonymous manifest + config-blob reads for all ten images |
| `latest` vs `sha-*` | same manifest digest on all ten |
| `org.opencontainers.image.revision` | full `91d76b6a1a8a2c8f292fe6941d9d8472827049ed` on all ten |
| Anonymous verification result | 10/10 verified, 0 failures, total compressed 155,188,577 B |

Counts derived from the actual CI job logs (not copied from the local runs):

| Job | Log evidence |
| --- | --- |
| Backend quality | `go test ./... -count=1`: 63 packages (50 with tests, 13 `[no test files]`), 0 `FAIL`; `go test -race ./... -count=1`: same 63/50/13, 0 `FAIL`; `go vet` and the command/package build steps passed |
| Frontend quality | `vue-tsc -b` passed; `vitest` **49 test files / 452 tests passed**; `vite build` passed (only the pre-existing large-editor-chunk warning) |
| Integration | migrations on a clean schema + backfill/down-up, real PostgreSQL auth/admin-CLI/retention, real Redis limiter, real-PG domain/HTTP/boot, P0 smoke, P1 HTTP, RustFS full-stack, p1-core browser **1 passed**, default-profile **5 passed** then **69 passed / 21 skipped**, full-stack **18 passed**, `0 new containers, 0 new volumes`; packaging contract **424 checks passed**; CI contract guards **14 passed**; cleanup guards **30 passed**; production image runtime smoke and production auth/proxy smoke all `PASS` |
| Publish images | all ten packages pushed with `latest` + `sha-91d76b6…` |

The Integration job's only non-`PASS` console line is the known pre-existing RustFS scratch cleanup
(`rm: cannot remove '/tmp/opencode/actfb-full-d622e591/rustfs-data/...': Permission denied`). It is the
same root-owned-UID 10001 artifact documented under Limitations, it lives only in the ephemeral runner
`/tmp`, and the step exited 0 — it is not a gate failure.

Published frontend artifact static check (anonymous blob read of `actfb-frontend:sha-91d76b6…`, 93
`js/css/html` entries scanned; this is a bundle-content check, not a full production-app run):
`Task progress` in `BoardPage`/`ModulePage`, `No solved tasks` and `No unsolved tasks` plus
`completion`/`SOLVED`/`UNSOLVED` in `ModulePage`, and the shipped rule
`.task-card--solved{background-color:color-mix(in srgb, var(--status-success-ink) 8%, black)}` with its
`12%` hover variant in the bundled CSS.

Raw evidence: `/tmp/opencode/student-task-progress/publication/` (`publication.json`, `ghcr-verify.json`,
`ci-run-46132838936.json`, `artifact-check.log`, job logs). No credential, cookie or token value is
recorded.

Operator action: this range adds no migration (schema stays at v20), no dependency, no `config.yaml` or
`.env` change and no required secret; `/api/v2` changes are additive fields plus an optional filter.
Pull and restart in `deploy/` with `docker compose pull && docker compose up -d`. The docs commit that
records this publication touches only `docs/`, which the workflow path filter excludes, so it starts no
new run and rebuilds no image.

## Gates actually run

Serial, low-RAM, one owned stack at a time. The operator stack on 8080 was never touched.

| Gate | Command | Result |
| --- | --- | --- |
| Focused real-PG | `ACTFB_TEST_POSTGRES_DSN=… go test -count=1 -v -run 'TestPostgresTaskProgressByModule\|TestStudentTaskProgress\|TestTaskProgressContract' ./internal/boards ./internal/boards/http ./internal/contracttests` | 8/8 passed, 0 skips; fresh migrations to version 20 on every isolated schema |
| Full Go | `go build ./...`, `go vet ./...`, `ACTFB_TEST_POSTGRES_DSN=… ACTFB_TEST_DATABASE_URL=… go test -count=1 ./...` | build/vet pass; 1069 test events passed, 24 skipped, 63 packages, 0 failures |
| Frontend unit | `bunx vitest run --maxWorkers=1` | 49 files / 452 tests passed (448 Task 2 baseline + 3 carried-over tests + 1 editor focus test) |
| Typecheck | `bunx vue-tsc -b` | pass |
| Build | `bun run build` | pass; only the existing >500 kB editor chunk warning |
| Default-profile browser | `e2e/run-default-profile-e2e.sh` (owned native PG 18, backend, Vite, Chromium) | exit 0: 5/5 default-profile tests, then 69 passed / 21 project-specific skips (16 phone + 5 chromium; 90 collected) |
| Full-stack browser (Docker) | `e2e/run-full-e2e.sh` (ephemeral PG/NATS/RustFS/ClamAV, host backend/workers/Vite, Chromium) | exit 0 twice: 19 passed (18 full-stack = 7 `full-stage` + 11 `organizer-ux`, plus 1 `p1-core`), 5 default-profile skips, 0 new containers/volumes |

The 24 Go skips are the historical dependency groups: real Redis, NATS, RustFS/S3, ClamAV,
Docker/Gitea, email delivery and full P0/P1 stack integration. No skipped test covers this
slice; the 8 focused progress tests are the real-PG proof and ran without skips.

## Resolved red gates

Both red gates from the first verification pass were investigated with the systematic-debugging
process and fixed; the original failure records are kept above and below rather than deleted.

**1. `rich-text-editor.spec.ts` Chromium code-block toggle (was 68 passed / 1 failed).**

- Reproduced evidence: the first two full default-profile runs failed the same assertion
  (`Code block` `aria-pressed` stayed `false` for 10 s); three runs of the same spec subset passed,
  so the failure was intermittent, not order-deterministic.
- Trace analysis (`test.trace`, `error-context.md`): the click itself completed in 61 ms with no
  Playwright retry, so the toolbar command ran but produced no code block.
- A deterministic probe repeated the exact step 20 times against the real app: iteration 15 left
  `document.activeElement` on the toolbar **button**, the DOM selection outside the editor, and the
  first typed character was dropped (`ef g():` instead of `def g():`). Root cause: the toolbar
  buttons did not cancel the browser's default `mousedown`, so the browser moved focus (and the
  selection) out of the contenteditable between `mousedown` and `click`; the command then ran
  against a lost selection — a no-op or a misplaced edit. This is a real authoring bug, not a test
  artifact.
- Fix (`frontend/src/shared/components/MarkdownEditorToolbar.vue`): every command control
  (Bold/Italic/Inline code, lists, blockquote, Code block, Undo/Redo, Link) now cancels the default
  `mousedown`, keeping the editable focused and the caret intact. The heading/language selects are
  unchanged because they need focus to open.
- TDD: `MarkdownEditor.test.ts` gained a failing test first ("keeps the editable selection when a
  formatting control is pressed"); it failed with `defaultPrevented === false` and passes after the
  fix. The probe that caught the bug (30/30 clean, focus retained, no dropped character) is the
  before/after evidence.
- The browser spec also now asserts the editor keeps focus at the code-block step, so a regression
  fails at the cause instead of as an opaque 10 s attribute timeout. No assertion was weakened,
  no timeout raised, no retry added.
- GREEN: the same full default-profile gate now exits 0 with 69 passed and 21 project-specific skips
  (16 phone + 5 chromium; 90 collected) — the skips are the mutually exclusive project selections, not
  disabled gates.

**2. Full-stack leak assertion (was exit 1 with one container).**

- The failing run reported one container ID that no longer existed a couple of minutes later
  (`docker inspect` → no such object) and the next run reported zero; it was a container created
  during the run that was gone by the time the run finished, not a persistent leak. `docker ps -aq`
  on this Docker version returns 12-character IDs, which is why the logged ID looked short.
- Leading **hypothesis** for the late snapshot: the runtime injects files through short-lived
  `*-inject` helper containers and the harness starts background workers, so a single `docker ps -aq`
  snapshot at the end could catch a container the daemon or a worker was still deleting. This is
  explicitly a hypothesis, not a proven cause: the first container's identity was not recovered (its
  workdir was deleted before instrumentation), and the next two runs reported zero without it
  recurring. The shipped mitigation does not depend on the hypothesis being true — it waits for
  whatever exact IDs the run actually produced.
- Fix (`e2e/lib/run-cleanup.sh`, `e2e/run-full-e2e.sh`): the leak assertion now waits for exactly
  the container IDs that appeared during the run to reach "no such container" (bounded,
  condition-based, 60 × 0.5 s), then re-snapshots. Nothing is removed by the wait, no other
  container is inspected, and a container that genuinely survives still fails the gate — now with
  its id, name, image and state printed so the next occurrence is self-diagnosing.
- TDD: `e2e/test-run-cleanup.sh` gained a stub `docker container inspect` plus four regression
  checks (mid-removal is awaited, a real survivor still fails and is named, empty ID set is a
  no-op, an already-gone ID costs one inspection). All 30 checks pass.
- GREEN: `e2e/run-full-e2e.sh` exited 0 twice with `owned Docker resources: 0 new containers,
  0 new volumes`.

## Carried-over review findings (tests added, no product change)

The Task 1/2 reviews requested small coverage pins. All product guards already existed, so the new
tests are green characterisation, not fabricated RED. Each was mutation-checked: removing the guard
fails the new test.

- `ModuleCard.test.ts`: `Complete` is asserted as exact text with its own SVG, so the status icon
  alone cannot satisfy it; partial and `0/0` modules assert no `Complete`.
- `ModulePage.test.ts`: a filtered second page (offset 100 + `completion=SOLVED`) resets to All at
  offset 0 and the new total when the route changes; a failed Load more retries at the same offset
  and filter without dropping the 100 loaded rows. Mutation checks: removing the route filter reset
  fails the first; always resetting in `retryTasks` fails the second.
- `task_progress_http_test.go`: raw HTTP bodies for `BoardMe`, `ModuleListResponse` and `Module`
  are validated against the expanded Swagger definitions, closing the generated-model
  unknown-field gap for `additionalProperties: false`.
- `service_task_progress_pg_test.go`: preview + revoked participant-staff with own accepted rows
  returns an empty `SOLVED` page while `UNSOLVED`/default stay identical with the same total
  (mutation: removing the guard leaks the two accepted rows); the >100-task module is queried with
  `completion=SOLVED|UNSOLVED` and filtered totals precede LIMIT/OFFSET; the TEAM-without-team
  `GetModule` keeps its existing `ErrModuleNotAvailable` (the defensive metadata branch is
  unreachable under the live access policy, recorded honestly).

## Real-browser evidence

One owned stack: fresh native PostgreSQL 18 (migrations v20), current-source backend, Vite and
Chromium. All product states were produced through real HTTP/UI: organizer creates boards, students
register/login/join, wrong flag, accepted flag, pending manual review accepted by the admin, team
invite/join/leave/create. Only the >100 quantity dataset was seeded with valid published task/module
rows so the run stayed fast; no API response was mocked or fulfilled with a fake success. One
Load-more request was deliberately aborted once to exercise the existing Retry path.

Journeys (all passed):

- Individual board: initial `4/9`; Web Mixed `2/6` (one zero-point accepted task counts); wrong flag
  keeps `2/6`; accepted `+80` moves the module to `3/6` and the board to `5/9` (one task, not points),
  score `360`; reload/back re-reads the server.
- Manual: pending review stays `0/1`; admin `ACCEPT` then reload gives `1/1` + `Complete` and board
  `6/9`; Unsolved shows `No unsolved tasks`.
- All-solved `2/2` + one check-circle + `Complete` (no `100%`); Unsolved filter empty with controls
  still visible. Empty module `0/0`, `aria-valuenow=0`, `aria-valuemax=1`, `aria-valuetext="0 of 0
  tasks solved"`, never Complete. Upcoming module card has no progress and no hidden task count.
- Filters: `completion=UNSOLVED|SOLVED` is visible in the decoded request URL; rows contain only the
  matching state; module progress stays unfiltered.
- Paging board: 101 modules, 150 tasks in the paging module and 250 tasks board-wide (100 extra
  one-task modules), 120 solved; the board summary is `120/250`. Module Load more requests
  `offset=100` and reaches 101; task Load more requests `offset=100`; failed Load more + Retry reuses
  `offset=100` and keeps the filter/pages; Solved filter requests `offset=0&completion=SOLVED`, Load
  more requests `offset=100&completion=SOLVED`, and all 120 solved rows are reachable. The board
  summary stays `120/250` while paging and while a filter is applied.
- Team: member B sees member A's two accepted tasks (`2/4`), solves a third (`3/4`) and reloads; A
  sees `3/4`. B leaves and creates a new team: `0/4`; the old team keeps `3/4`.
- Staff preview and unjoined reader: no own progress, no participant filters.
- Accessibility: each progress surface exposes exactly one `aria-valuenow`; the visible fraction is
  `aria-hidden`; filter group is a native button group with `aria-pressed`; every filter target is
  at least 44×44 px (measured 51/93/76 × 44).

Computed measurements (Chromium, actual CSS):

| Measure | Result |
| --- | --- |
| Solved card background | `color(srgb 0.0489 0.0649 0.0449)` ≈ 8% success ink over black |
| Solved card hover | `color(srgb 0.0734 0.0974 0.0673)` ≈ 12% |
| Card title on tint | 14.49:1 |
| Muted fraction on black | 6.52:1 |
| Progress fill `#9ccf8f` on track `#191724` | 9.87:1 |
| Accepted task alert | 11.74:1 |
| Wrong-flag alert | 7.22:1 |
| Card focus ring | 1 px solid, offset 4 px, `:focus-visible` |
| Filter focus ring | 2 px solid, offset 2 px |

Responsive: 18 samples at 375/1024/1440/1920 across board, module, solved task and paging board;
`scrollWidth === clientWidth` in every sample (no document overflow).

Screenshots (`/tmp/opencode/student-task-progress/shots/`, 15 files; Main personally inspected five of
them — `board-1440`, `module-mixed-1440`, `module-mixed-375`, `module-allsolved-1440`,
`module-empty-1440` — and approved the composition, with no UI redesign requested):
`board-1440`, `module-mixed-1440`, `module-mixed-375`, `module-filter-unsolved-1440`,
`module-filter-solved-1440`, `module-allsolved-1440`, `module-empty-1440`, `manual-awaiting-1440`,
`module-manual-accepted-1440`, `team-shared-1440`, `board-paging-1440`,
`module-paging-solved-1440`, `task-accepted-1440`, `preview-1440`, `board-unjoined-1440`.
Measurements: `/tmp/opencode/student-task-progress/measurements.json`.

## Privacy and contract behaviour observed

Subject is resolved server-side only; no request carries a user/team UUID for progress. Progress is
omitted for preview/unjoined/revoked/team-less reads and explicit `0/0` only for an eligible empty
board. Audience/group revocation and team changes take effect on the next request. PENDING_REVIEW,
REJECTED and hidden/upcoming/disabled content never count; zero-point ACCEPTED does. Staff management
scope never expands own progress. No migration, dependency, RBAC, score, provider or operator
configuration change is present.

## Limitations

- No screen-reader study; accessibility was checked through the DOM/ARIA contract and keyboard focus.
- Contrast was computed from actual Chromium CSS values, not sampled from rendered pixels.
- The published-artifact check above is a static scan of the bundled `js/css` entries; it proves the
  progress UI and the success tint shipped in the image, not that a deployed production stack renders
  them.
- The CI Integration job logs a root-owned RustFS scratch `rm: Permission denied` under
  `/tmp/opencode/actfb-full-d622e591/`; the step still exited 0 and the directory was on the ephemeral
  runner only. The equivalent local leftovers were cleaned as described below.
- The >100 dataset is SQL-seeded valid published rows (documented fixture), not 150 HTTP-created
  tasks; the API still serves and counts them.
- PENTEST, ATTACK_DEFENSE and EXTENSION runtime browser journeys were not exercised. Their absence of
  task percentages is covered by backend/unit tests, and the full-stack plugin acceptance covers the
  EXTENSION install/checker path.
- The upcoming module was verified from its board card (no progress, no count); opening it as a
  participant is correctly refused by the availability gate.
- Team-without-team `GetModule` keeps the existing denial under the live access policy; the service's
  defensive success branch is not reachable in the shipped configuration.
- Publication gates were run afterwards and are recorded at the top of this document: one push-triggered
  CI run (`46132838936`) with 4/4 successful jobs and all ten images verified anonymously. That record is
  separate from the local gates above, which were not re-run.

## Owned-resource cleanup

Owned browser stack: PostgreSQL cluster, backend, Vite and Chromium process groups stopped; private
fixtures, config, `.env`, generated passwords/JWT/credential keys and the backend binary removed.
Owned native PG clusters for the Go and default-profile gates were stopped and their data removed;
the default-profile script dropped its own database. The full-stack runs removed their named
containers and volumes and reported `0 new containers, 0 new volumes`; the leak assertion now waits
(bounded) for containers that appeared during the run before declaring a leak.

Three full-stack runs each left a root-owned RustFS scratch directory (`rustfs-data`/`rustfs-logs`
written by UID 10001, ~76 KB each) that the host user could not delete: the first run's
`/tmp/opencode/actfb-full-a45641b7`, and the two green runs' `/tmp/opencode/actfb-full-12541a58` and
`/tmp/opencode/actfb-full-966aeb92` (both named in `diag/full18-green-1.log` and
`diag/full18-green-2.log`). Each was verified to exist before removal, then cleared with a throwaway
container (`alpine:3.22`, already present locally) that mounted **only that exact directory**
read-write (`find /cleanup -mindepth 1 -exec rm -rf {} +`); the empty directory was then removed from
the host. No other path, the Docker socket, the repo or operator data was mounted, and no ownership
was changed. All three paths are now absent (`0` remaining), and the container was ephemeral
(`--rm`).

The historical record stands: the first path was initially blocked and only cleaned afterwards, so
the earlier "remains as inert scratch data" note was true when written, not a claim of zero leftovers
before the paths were checked.

Docker state was snapshotted immediately before and after this cleanup and is byte-identical:
9 running / 35 stored containers and 1801 volumes, with the same names (the operator `actfb-prod-*`
stack plus one unrelated `canonizator-dev-*` container started by another job). Only the two
run-scoped directories changed; no container, volume, image or port 8080 resource was touched, and no
broad `pkill`/prune/`compose down -v` was used.
