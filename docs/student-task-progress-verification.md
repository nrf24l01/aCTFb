# Student task progress — local verification

Scope: Task 3 local integration evidence for the approved student task progress slice
(`docs/superpowers/specs/2026-11-27-student-task-progress-design.md`, plan
`docs/superpowers/plans/2026-11-27-student-task-progress.md`). Branch
`feat/student-task-progress`; product source `8154f1b` (backend/contract) + `12872e1`
(UI/filters), both independently reviewed with spec APPROVED and quality APPROVED, no
Critical/Important findings. This record covers **local** verification only. Push, CI and
GHCR publication remain pending; no pipeline was run.

## Gates actually run

Serial, low-RAM, one owned stack at a time. The operator stack on 8080 was never touched.

| Gate | Command | Result |
| --- | --- | --- |
| Focused real-PG | `ACTFB_TEST_POSTGRES_DSN=… go test -count=1 -v -run 'TestPostgresTaskProgressByModule\|TestStudentTaskProgress\|TestTaskProgressContract' ./internal/boards ./internal/boards/http ./internal/contracttests` | 8/8 passed, 0 skips; fresh migrations to version 20 on every isolated schema |
| Full Go | `go build ./...`, `go vet ./...`, `ACTFB_TEST_POSTGRES_DSN=… ACTFB_TEST_DATABASE_URL=… go test -count=1 ./...` | build/vet pass; 1069 test events passed, 24 skipped, 63 packages, 0 failures |
| Frontend unit | `bunx vitest run --maxWorkers=1` | 49 files / 451 tests passed (448 Task 2 baseline + 3 carried-over tests) |
| Typecheck | `bunx vue-tsc -b` | pass |
| Build | `bun run build` | pass; only the existing >500 kB editor chunk warning |
| Default-profile browser | `e2e/run-default-profile-e2e.sh` (owned native PG 18, backend, Vite, Chromium) | 5/5 default-profile tests passed; frontend specs 68 passed, 1 failed, 21 skipped (phone-only selection) |
| Full-stack browser (Docker) | `e2e/run-full-e2e.sh` (ephemeral PG/NATS/RustFS/ClamAV, host backend/workers/Vite, Chromium) | 19 passed, 5 skipped, 0 test failures; see cleanup note |

The 24 Go skips are the historical dependency groups: real Redis, NATS, RustFS/S3, ClamAV,
Docker/Gitea, email delivery and full P0/P1 stack integration. No skipped test covers this
slice; the 8 focused progress tests are the real-PG proof and ran without skips.

The one default-profile failure is the pre-existing `rich-text-editor.spec.ts` Chromium code-block
toggle (`aria-pressed` stays `false` in the full-suite order). The same test passes when run alone
(both Chromium and phone projects, 2/2) and the phone project passed in the full run. The spec and
editor were not changed by this slice; it is recorded as an unrelated flaky order-dependent test,
not a regression. The full-stack run exited non-zero only because its leak assertion observed one
transient container mid-removal; re-checking immediately afterwards showed no run containers or
volumes, and the operator stack was unchanged.

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
- Paging board: 101 modules, 150 tasks, 120 solved. Module Load more requests `offset=100` and reaches
  101; task Load more requests `offset=100`; failed Load more + Retry reuses `offset=100` and keeps
  the filter/pages; Solved filter requests `offset=0&completion=SOLVED`, Load more requests
  `offset=100&completion=SOLVED`, and all 120 solved rows are reachable. Board summary stays `120/250`.
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

Screenshots (main inspects these; `/tmp/opencode/student-task-progress/shots/`, 15 files):
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
- The >100 dataset is SQL-seeded valid published rows (documented fixture), not 150 HTTP-created
  tasks; the API still serves and counts them.
- PENTEST, ATTACK_DEFENSE and EXTENSION runtime browser journeys were not exercised. Their absence of
  task percentages is covered by backend/unit tests, and the full-stack plugin acceptance covers the
  EXTENSION install/checker path.
- The upcoming module was verified from its board card (no progress, no count); opening it as a
  participant is correctly refused by the availability gate.
- Team-without-team `GetModule` keeps the existing denial under the live access policy; the service's
  defensive success branch is not reachable in the shipped configuration.
- Publication gates (push, CI, anonymous GHCR verification) were intentionally not run.

## Owned-resource cleanup

Owned browser stack: PostgreSQL cluster, backend, Vite and Chromium process groups stopped; private
fixtures, config, `.env`, generated passwords/JWT/credential keys and the backend binary removed.
Owned native PG clusters for the Go and default-profile gates were stopped and their data removed;
the default-profile script dropped its own database. The full-stack run removed its named containers
and volumes; a root-owned RustFS scratch directory under `/tmp/opencode/actfb-full-a45641b7` could not
be deleted by the unprivileged user and remains as inert scratch data. No operator container, volume,
image or port 8080 resource was touched, and no broad `pkill`/prune/`compose down -v` was used.
