# Student Task Progress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement all four approved student completion indicators and filters.
**Architecture:** Add optional authoritative progress projections to existing /api/v2
responses, batch-count distinct accepted tasks for the current subject, and filter
task lists server-side before pagination. Vue consumes those projections directly.
**Tech Stack:** Existing Go/Echo/pgx/PostgreSQL, Swagger generator, Vue/TypeScript/Bun,
shadcn-vue/Reka and Lucide; no new dependency.
**Spec:** `docs/superpowers/specs/2026-11-27-student-task-progress-design.md`

## Global Constraints

- Main owns UX and contracts; DeepSeek implements and collects evidence.
- Preserve existing API envelopes, routes, default behavior, permissions and scores.
- No client-only first-page counts, per-module task-list request fanout, emoji or new panels.
- Only current participant-readable, enabled, ACTIVE TASKS/EXTENSION modules and visible
  tasks contribute; staff management scope never expands own progress.
- ACCEPTED, including zero-point acceptance, is solved; pending review is not.
- No provider/configuration changes, new storage of answers or operator-resource cleanup.
- Serial low-RAM execution; Docker only; preserve the operator stack and port 8080.

## Review Focus

1. Beyond 100 tasks/modules: full aggregate counts and server-side filter totals.
2. Subject/privacy: team changes, revoked groups/audiences, joined staff and preview.
3. Empty/unavailable: no division by zero or fabricated 100% completion.
4. Async UI: rapid filter/route changes and load-more retries never mix lists.
5. Acceptance: back-navigation/reload refresh totals without duplicate score/solve counts.

### Task 1: Authoritative contract, aggregate and filtering

**Files:** modify `docs/swagger.yaml`, generated `internal/generated/openapi/models/**`
via `make swagger`, `internal/boards/{types,inputs,store,postgres_tasks,service_tasks,
service_boards}.go`, `internal/boards/http/{participant_handlers,mappers}.go`.
Create focused `internal/boards/{postgres_task_progress,service_task_progress}.go`
and focused tests alongside them; update the existing mock stores as required.
Update `docs/p1-core-contracts.md` with a current additive-contract section, not
by rewriting the historical first-slice record.

**Interfaces:**
- `TaskProgress { solvedTasks: integer >= 0, totalTasks: integer >= 0 }`.
- Optional `taskProgress` on `BoardMe`, `ModuleListItem`, `Module`.
- Participant listTasks query `completion=SOLVED|UNSOLVED`; absent means All.
  Invalid values return existing 422 VALIDATION_FAILED; existing paging remains.
- Go domain `TaskProgress { SolvedTasks int; TotalTasks int }`, optional pointer
  on BoardMe/Module; extend ListQuery/TaskFilter with Completion string.
- Store aggregate signature:
  `TaskProgressByModule(ctx context.Context, boardID uuid.UUID, moduleIDs []uuid.UUID, subject ScoreSubject) (map[uuid.UUID]TaskProgress, error)`.
  Return visible-task progress keyed by module UUID in one batched query. Service
  owns live participation/access/audience checks and qualifies module IDs using
  existing effective status functions; store does not fork those policies in SQL.
  No module IDs means no rows, never all modules. Missing eligible empty modules
  become explicit zero objects only after service has established eligibility.
- Keep aggregate results independent of completion filters and paging.

- [x] Write failing real-PG tests: individual/team, zero-point ACCEPTED, multiple
  attempts, pending/manual acceptance, 101+ tasks and 101+ modules, empty modules,
  audience/group revocation, hidden/disabled/upcoming modules/tasks, joined staff
  restricted audience, no-team/preview/unjoined, cross-board IDs, invalid filters.
- [x] Run focused tests and record RED; validate baseline migrations on owned PG.
- [x] Add canonical schemas/query, regenerate with `make swagger`, and validate
  Swagger with the repository tooling. No hand edits to generated types.
- [x] Implement batch aggregate, participant-only attachment, completion predicates
  using EXISTS/NOT EXISTS ACCEPTED and filtered total before LIMIT/OFFSET.
  No exception swallowing that turns dependency failures into zero progress.
- [x] Test HTTP real responses against the updated schema, including omission
  and zero-object cases; default unfiltered behavior stays compatible.
- [x] Run `go test ./...`, `go vet ./...` plus real-PG affected-package tests.
- [x] Commit contract/backend/tests as one logical group; no push yet.

### Task 2: Student progress surfaces, filters and pagination

**Files:** modify `frontend/src/shared/types/api.ts`,
`frontend/src/features/boards/api/boards.ts`, components `{TaskCard,ModuleCard}.vue`,
pages `{BoardPage,ModulePage}.vue`, their adjacent existing tests and browser specs.
Create feature component `frontend/src/features/boards/components/TaskProgress.vue`
and tests. Add only necessary shared primitive files under
`frontend/src/components/ui/progress/` using existing shadcn-vue/Reka conventions.
Use narrowly scoped styles, never change global card/border defaults.

**Interfaces:**
- TS `TaskProgress { solvedTasks: number; totalTasks: number }`;
  optional `taskProgress?: TaskProgress` on the three response interfaces.
- `boardsApi.listTasks` accepts PageQuery plus optional Completion.
- Feature TaskProgress consumes server `progress`, accessible `label`, optional
  compact presentation. It renders the visible fraction and accessible bar.
- ModulePage keeps unfiltered `module.taskProgress`, separate filtered list total,
  current completion selection, loaded items and offset.

- [x] Write failing tests for subtle solved-card tint/icon, correct fraction/bar
  semantics, optional suppression, empty/all-solved states and no duplicate badge.
- [x] Write failing ModulePage tests for all three filters, paging beyond 100,
  reset on filter/route change, out-of-order responses, and preserved error retry.
  Write BoardPage tests proving its summary is not summed from first-page cards.
- [x] Implement the exact spec: existing semantic green, borderless hierarchy,
  thin unanimated bar, concise copy, SVG icons, selected-state semantics.
- [x] Add participant module/task Load more using server total; keep preview
  management behavior intact. Do not expose own filters/progress in preview.
- [x] Run all frontend tests with one worker, typecheck and build. Pre-audit every
  affected browser selector without deleting behavior assertions.
- [x] Commit UI/tests as a logical group; no push yet.

### Task 3: Integration evidence, review and publication

**Files:** create `docs/student-task-progress-verification.md`;
update `docs/implementation-plan.md`; temporary sanitized browser artifacts under
`/tmp/opencode/student-task-progress/`. Review artifact owned by a fresh reviewer.

- [x] Use one owned stack to walk real individual and team students: wrong flag,
  accepted flag, back/reload totals, manual pending/review accepted, each filter,
  Load more, empty and all-solved modules, preview and unauthorized scope.
- [x] Capture board/module/card desktop and phone screenshots; main inspects them.
  Measure 375/1024/1440/1920 overflow, keyboard focus, accessible progress values,
  and actual computed text/graphic contrast. Preserve user-authored content.
- [x] Run existing default-profile/full-stack gates serially; record exact tests,
  skips and limitations, migrations, owned-resource cleanup and source commits.
- [x] Obtain independent contract/security/behavior review; fix important findings
  with failing tests first. Main reviews integration and visual evidence.
  (Whole-branch review `docs/student-task-progress-review.md`: Spec + Quality APPROVED, no
  Critical/Important findings; Main inspected five screenshots and approved. Minor factual
  follow-ups closed as docs/cleanup only.)
- [x] After approval, normal commit/push, one actual CI run, anonymous verification
  of all GHCR image tags/digests/revisions; only then mark published.
  (Run `46132838936` on `91d76b6`: 4/4 jobs success; 10/10 images verified anonymously with
  matching `latest`/`sha-*` digests and the full revision label.)

## Main technical rulings after builder recon

- D1: EXTENSION is task-bearing in ModulePage as well as aggregates; preserve plugin
  checker permissions and route behavior.
- D2: An eligible subject with no currently readable active tasks gets `{0,0}`.
  Ineligible/no-subject progress remains omitted.
- D3: Audience-read failure is an actual error, not empty audience data or zero
  progress. Propagate it through the existing error mapping.
- D4: Resolve own subject for participant-staff too. Preserve existing nonstaff
  TEAM-required errors. Staff with no team retain preview but no progress.
- D5: Revise the internal store signature as above: qualified IDs from service,
  one SQL aggregate; no duplicated availability/access policy.
- D6: Existing list-total quirks are not authorization to change unrelated scope rules.
- D7: Completion query is `SOLVED|UNSOLVED`, omission means All.
- D8: No progress on BoardListItem; the approved board summary is on BoardPage.
- D9: Board module Load more is included so every permitted module stays reachable.
- D10: Filter-empty labels are `No unsolved tasks` / `No solved tasks`; actual empty
  module remains `No tasks available`. No new explanatory paragraphs.
