# Student task progress — whole-branch review

- Branch `feat/student-task-progress`, range `6aa65d5..2114af4` (8 commits), unpublished.
- Reviewed: full package `.superpowers/sdd/2026-11-27-student-task-progress/review-6aa65d5..2114af4.diff`,
  the current tree, the design spec and plan (Global Constraints + D1–D10), the Task 1/2 reviews, the
  Task 3 report, and the retained logs/measurements under `/tmp/opencode/student-task-progress/`.
- This file is the only artifact written by this review. No product code, Git, provider or operator change.

## Verdict

| Axis | Verdict |
| --- | --- |
| **Spec** | **APPROVED** |
| **Quality** | **APPROVED** |
| Critical findings | none |
| Important findings | none |

The branch delivers the approved additive surface end to end: `TaskProgress` on `BoardMe`/`ModuleListItem`/
`Module`, participant `completion=SOLVED|UNSOLVED`, server-filtered totals before paging, one batched
per-subject aggregate, and a board summary over every permitted active `TASKS`/`EXTENSION` module. No
route, envelope, permission, score, migration, dependency or operator-config change is present. Minor
items below are documentation/cleanup/test-hardening notes; none is load-bearing for function, privacy or
a green gate.

## Cross-layer checks (verified)

**Subject and privacy.** The subject is resolved server-side for every projection; no request accepts a
user/team UUID for progress. Progress is omitted (never null) for unjoined, staff-preview, revoked-group and
`TEAM`-without-team reads; an eligible subject with no readable active tasks gets explicit `0/0`
(`internal/boards/service_boards.go:191-238`, `service_task_progress.go:15-34`). A joined participant who is
also staff gets progress only for modules where the canonical participant gate passes; management-sibling
modules, hidden tasks and other audiences never contribute (`service_boards.go:290-326`). The aggregate
store enforces only board parent scope + `visible = TRUE` + the subject's `ACCEPTED` rows; participation,
access-group, audience and team policy remain in the existing service/access layer
(`internal/boards/postgres_task_progress.go:31-56`). Raw HTTP bodies for `BoardMe`, `ModuleListResponse` and
`Module` are schema-validated against the expanded Swagger (`internal/boards/http/task_progress_http_test.go`).

**Aggregate and filter.** One query counts visible tasks and `count(*) FILTER (WHERE EXISTS …ACCEPTED…)` per
module; `EXISTS` plus the partial unique accepted index makes the numerator distinct, and `solved <= total` is
structural. The board summary loads all board modules (no first-100 cap) and sums only service-qualified
module IDs; the retained real-PG test pins `1/203` on a 101-module board. The completion predicate lives in
the shared `WHERE`, so the count and page use the same filter; subject arguments are bound before the count
when filtering, and `TaskListResponse.total` is the filtered total before `LIMIT/OFFSET`
(`internal/boards/postgres_tasks.go:147-225`). The enum is validated at HTTP and service boundaries
(`http/participant_handlers.go:248-253`, `service_tasks.go:190-201`). Staff preview publishes no own solved
flags: `SOLVED` is an empty page, `UNSOLVED`/default keep the existing preview list with `solved=false`
(`service_tasks.go:139-160`). `EXTENSION` task lists use the same endpoint; no plugin permission or route
change.

**Contract and frontend.** `docs/swagger.yaml` adds a closed `TaskProgress` (`solvedTasks`/`totalTasks`
required, minimum 0) and the optional field on the three models; `make swagger` changed only the four
generated files. The Go mapper returns nil for absent progress, so the generated `omitempty` tag omits the
field instead of sending null (`internal/boards/http/mappers.go:14-23`). `AdminModule`/`BoardListItem` are
unchanged. The TS mirror uses the same optional keys and adds `completion?: 'SOLVED'|'UNSOLVED'`; the page
query helper omits `undefined` and forwards an `AbortSignal` (`frontend/src/features/boards/api/boards.ts:62-72,149-160`).

**UI behaviour.** Module progress stays the unfiltered server fraction while the task list uses the filtered
total; the board summary reads `me.taskProgress` and never sums visible cards or score. Solved cards use only
the existing success ink at 8% over black (12% hover), no new border/badge/motion; the existing `StatusPill`
remains the only Solved check. The bar is 4 px with `role=progressbar`, numeric values, dynamic
`aria-valuetext`, and `0/0` uses `max=1`/`now=0`, never Complete/100%. Filters are native buttons with
`aria-pressed`, 44 px targets and wrap without overflow. Preview/unjoined/unavailable/`PENTEST`/`ATTACK_DEFENSE`
surfaces show no own progress or filters. Async guards cover filter/route changes, abort/sequence ordering,
UUID dedupe and Load-more retry at the same offset/filter.

**Acceptance refresh is reload/navigation, not live push.** The browser evidence is explicit: wrong flag keeps
`2/6`; accepted `+80` moves the module to `3/6` and the board to `5/9` (one task, not points), score `360`;
manual `PENDING_REVIEW` stays `0/1`, then admin `ACCEPT` + page reload gives `1/1 Complete` and board `6/9`;
team `2/4 → 3/4` is visible to the second member after reload, a new team starts `0/4` and the old team keeps
`3/4`. There is no polling, websocket or local-storage progress cache; the UI re-reads on navigation/reload.

**Visual/measurement evidence.** 15 screenshots, 12 recorded browser results, 18 geometry samples, 0 recorded
failures. Measured from actual Chromium CSS: card title on tint 14.49:1, muted fraction 6.52:1, progress fill
`#9ccf8f` on track `#191724` 9.87:1, accepted alert 11.74:1, wrong-flag alert 7.22:1; filter targets
51/93/76 × 44 px; `scrollWidth === clientWidth` at 375/1024/1440/1920. No screen-reader study was performed;
contrast is computed from CSS, not pixel-sampled. Main's screenshot approval is recorded; this review makes
no visual redesign suggestion.

## Task 3 root fixes

**Editor toolbar focus (5d840ab).** Root cause: command controls did not cancel the browser's default
`mousedown`, so focus and the contenteditable selection left the editor between `mousedown` and `click`; the
command then ran against a lost selection (no-op or first typed character dropped). Fix:
`@mousedown.prevent` on every command button/Toggle (Bold/Italic/Inline code, lists, blockquote, Code block,
Undo/Redo, Link); the heading/language selects are untouched because they need focus. TDD: the new
`MarkdownEditor.test.ts` case failed on `defaultPrevented === false` and passes now; the browser spec asserts
the editor keeps focus at the code-block step; the diagnostic probe is reported `1/20` failing before and
`0/30` after (probe output was not retained; the captured failing trace contains the dropped `ef g():`
character). No timeout was raised, no retry/force-click added, no assertion weakened. Mouse command activation
no longer moves focus to the button; keyboard Tab/Space/Enter still activate the controls, and the inline link
row captures the selection and restores editor focus on close. Preventing `mousedown` default does not cancel
the subsequent click/tap.

**Full-stack leak assertion (9d47be4).** Root cause of the red run: the end-of-run snapshot caught a container
the daemon/worker was still deleting (the logged ID was gone minutes later; the next run reported zero). Fix:
a bounded (60 × 0.5 s) wait that only `docker container inspect`s the exact IDs that appeared during the run,
then re-snapshots; a real survivor still fails and prints id/name/image/state. Nothing is removed by the wait;
no prune, label-wide delete or hosted-marker change. The stub-docker guard suite is 30/30 (independently
re-run here), including mid-removal awaited, survivor still fails and is named, empty set no-op, absent ID one
inspection. The retained green logs (`diag/full18-green-1.log`, `diag/full18-green-2.log`) show 19 passed,
5 skipped and `owned Docker resources: 0 new containers, 0 new volumes` twice.

The identity of the first leaked container was not recovered (the workdir was deleted before instrumentation).
The `*-inject`/background-worker deletion explanation is a **hypothesis**, not a proven cause; the fix is a
defensive bound that preserves the exact-ID, no-deletion policy.

## Evidence ledger

| Gate | Result | Source |
| --- | --- | --- |
| Focused real-PG/HTTP/contract | 8/8 pass, 0 skip; migrations to v20 | `pg-tests.log` |
| Full Go | 1069 pass, 24 dependency skips, 0 fail, 63 packages; build/vet pass | `go-test-full.json` + report |
| Frontend unit | 452 tests / 49 files (448 baseline + 3 carried-over + 1 editor focus) | Task 3 report; not re-run in full |
| Typecheck/build | pass | Task 3 report |
| Default-profile browser | exit 0: 5/5 default-profile + 69 passed / 21 skipped (16 phone + 5 chromium) | `default-profile-e2e.log` |
| Full-stack Docker | exit 0 twice: 19 passed / 5 skipped, 0 new containers/volumes | `diag/full18-green-1.log`, `diag/full18-green-2.log` |
| Cleanup guard suite | 30/30 | re-run here |
| CI contract guards | 14/14 | re-run here |
| Targeted board unit files | 41/41 (6 files) | re-run here |
| Targeted editor unit file | 32/32 | re-run here |

The 452-test full frontend suite and the 1069-test Go suite were not re-run end to end for this review; their
counts are from the retained logs/reports. The green full-stack logs are retained in `diag/` (not only in the
deleted workdirs). The operator stack was not touched: `actfb-prod-*` containers are up 37 hours and no
`actfb`-namespaced volume was created after 2026-11-25; the run-scoped before/after snapshot files were
removed with the workdirs, so the per-run proof is the script's own assertion line in the green logs plus this
current-state corroboration.

## Findings (all Minor)

1. **Harness false-green on a Docker error (pre-existing).** `e2e/lib/run-cleanup.sh:95` treats any
   `docker container inspect` failure as "container gone", and `e2e/run-full-e2e.sh:340,349` re-snapshot with
   `2>/dev/null || true`. A daemon error at the assertion can therefore print a clean result. This is not
   introduced by `9d47be4`; the retained green runs had a healthy daemon and current Docker state corroborates
   zero new owned resources. Hardening (optional, small): verify `docker version`/`docker ps` exit status
   before declaring clean, or capture the final snapshot without swallowing errors. Related pre-existing
   ordering: the assertion at `e2e/run-full-e2e.sh:339-357` runs before `cleanup()` stops services
   (`:79-94`), so a container created after the snapshot is not caught; a post-stop re-assert would close it.
   Neither path was observed to leak.

2. **RustFS scratch leftovers (documentation/cleanup).** `docs/student-task-progress-verification.md:193-199`
   records only the first leftover. The two green runs each left a 76 KB RustFS directory owned by the
   container user (UID 10001), which the host user cannot remove: `/tmp/opencode/actfb-full-12541a58` and
   `/tmp/opencode/actfb-full-966aeb92` (the green logs show `Permission denied` on removal). They are outside
   the repo and contain no operator data; clear them with the same exact-path throwaway container already used,
   or record them. No operator container/volume/port 8080 resource is involved.

3. **`69/69` wording.** `docs/student-task-progress-verification.md:22,59-60` says "69/69 frontend tests" while
   21 were skipped (16 phone + 5 chromium, 90 collected). Write "69 passed, 21 project-specific skips" to avoid
   implying the skipped tests ran.

4. **Board B task-count wording.** `docs/student-task-progress-verification.md:125-128` says "101 modules,
   150 tasks, 120 solved" but the board summary is `120/250`; the fixture seeds 150 tasks in the paging module
   plus 100 one-task modules (`fixture-summary.json` `boardB.expected.board`). Say "150 tasks in the paging
   module, 250 board-wide" so the two numbers are not read as contradictory.

5. **Hypothesis labelling.** `docs/student-task-progress-verification.md:68-70` and
   `.superpowers/sdd/2026-11-27-student-task-progress/task-3-report.md:120-128` present the `*-inject`/worker
   deletion as the cause. The first container's identity was not recovered; label it a leading hypothesis, as
   the ledger already does, and keep the exact-ID bound as the shipped mitigation.

6. **Known coverage notes (accepted Minor).** `ModulePage.vue:41-43,89-138` and `BoardPage.vue:53,96-180` do not
   abort outstanding requests on unmount; late responses write refs but cannot affect the DOM, so the cost is a
   wasted request. The phone project sets `hasTouch: true` (`frontend/playwright.config.ts:31-40`) but the
   rich-text spec uses `click()` (`frontend/e2e/rich-text-editor.spec.ts:250,331`), not `tap()`; the mousedown
   fix does not cancel click/tap, and the phone project passed, but a true `tap()` assertion would be stronger.
   Neither is a regression from this branch.

## Go / no-go

**Go for Main's final integration review.** There is no Critical or Important code, contract, privacy or
test-trust defect in the reviewed range. The two red gates are root-caused and fixed with targeted tests, and
the retained green logs cover the default-profile and full-stack gates.

Publication is **not** approved by this review: push, CI and anonymous GHCR tag/digest/revision verification
were deliberately not run and remain Main's gate. Before publication, close or explicitly accept findings 1–5
(documentation/cleanup only); finding 6 is a declared coverage note. Declared limitations remain: no
screen-reader study, contrast computed from CSS, PENTEST/ATTACK_DEFENSE/EXTENSION runtime browser journeys not
exercised (backend/unit and the full-stack plugin acceptance cover their exclusion/install paths), and the
`>100` dataset is SQL-seeded valid published rows.

## Task artifacts

- Task 1: `.superpowers/sdd/2026-11-27-student-task-progress/task-1-report.md`, `task-1-review.md`.
- Task 2: `.superpowers/sdd/2026-11-27-student-task-progress/task-2-report.md`, `task-2-review.md`.
- Task 3: `.superpowers/sdd/2026-11-27-student-task-progress/task-3-report.md`; no Task 3 review file exists yet.
- Verification record: `docs/student-task-progress-verification.md`.
- Spec/plan: `docs/superpowers/specs/2026-11-27-student-task-progress-design.md`,
  `docs/superpowers/plans/2026-11-27-student-task-progress.md`.

## Minor factual follow-ups resolved (2026-11-28, appended by Main's direction)

The review body above is unchanged and its Spec/Quality APPROVED verdict stands. Main personally
inspected five screenshots (`board-1440`, `module-mixed-1440`, `module-mixed-375`, `module-allsolved-1440`,
`module-empty-1440`) and approved the composition; no UI redesign was requested. The six Minor findings
were then closed as documentation/cleanup only — **no product code changed** after the reviewed
`2114af4`:

- Finding 1 (harness false-green on a Docker error): deliberately **not** changed; pre-existing and
  deferred, recorded here as a known risk.
- Finding 2: the two named scratch directories `/tmp/opencode/actfb-full-12541a58` and
  `/tmp/opencode/actfb-full-966aeb92` were verified to exist, cleared with a throwaway container
  mounting only each exact path, and removed from the host; the earlier
  `/tmp/opencode/actfb-full-a45641b7` was already gone. Zero remain.
- Finding 3: "69/69" corrected to "69 passed, 21 project-specific skips (16 phone + 5 chromium; 90
  collected)" in `docs/student-task-progress-verification.md` and `docs/implementation-plan.md`.
- Finding 4: board B corrected to "150 tasks in the paging module, 250 board-wide", summary `120/250`.
- Finding 5: the `*-inject`/worker deletion explanation is labelled a hypothesis in the verification
  record and the Task 3 report.
- Finding 6: accepted as declared Minor coverage notes; unchanged.

Docker state was snapshotted immediately before and after the cleanup and is identical (9 running /
35 stored containers, 1801 volumes, same names). Publication (push, CI, anonymous GHCR verification)
remains Main's gate and is not claimed here.
