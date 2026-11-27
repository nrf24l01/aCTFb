# Quieter student UI verification

Status: **implemented and locally verified; independent review and publication pending.** Base `08a6df4`.
Spec: `docs/superpowers/specs/2026-11-27-quieter-student-ui-design.md`.
Plan: `docs/superpowers/plans/2026-11-27-quieter-student-ui.md`.

This is a presentation-only slice. No backend, API, authorization, persistence, configuration, dependency or
TTL change is included. All request payloads, idempotency keys, verdicts, score values, history rows and
destructive confirmations keep their existing behaviour.

## Changed areas

| File | Change |
| --- | --- |
| `frontend/src/features/boards/components/FlagSubmitForm.vue` | Removed the outer panel/header strip and the duplicate status pill; one compact outcome line; real `scoreAwarded` only; concise solved/wrong/network copy; retained retry of the in-memory answer and key. |
| `frontend/src/features/boards/components/ManualSubmissionForm.vue` | Same unframed treatment; `Awaiting review` instead of the raw enum/points paragraph; text still clears only after success and survives a network failure. |
| `frontend/src/features/boards/components/SubmissionHistory.vue` | Removed the outer frame and repeated visible heading; one `sr-only` table caption; kept header separators and useful row separators. |
| `frontend/src/features/boards/pages/TaskPage.vue` | Description is an unframed `section[aria-label="Task description"]`; removed the grey duplicate `Solved` badge; unframed files/rail fallbacks; one `Your submissions` heading. |
| `frontend/src/features/boards/components/{ModuleCard,TaskCard}.vue` | Four-sided outlines replaced with spacing plus the existing `hover:bg-muted` background; the global focus outline remains. |
| `frontend/src/features/boards/pages/{BoardPage,ModulePage}.vue` | Removed decorative outer/header frames from the student standing, participate and pentest-lab sections. Organizer/staff frames are unchanged. |
| `frontend/src/features/instances/components/InstancePanel.vue` | Removed the backend-authorized/idempotent paragraph and the fabricated-log placeholder; kept real status, `cannotStartReason`, TTL, extension credits, errors and the destroy confirmation. |
| `e2e/tests/{p1-core,full-stage}.spec.ts`, `frontend/e2e/{board-flow,desktop-workspaces,rich-text-editor}.spec.ts` | Updated only selectors whose user-visible copy changed; behavioural assertions were strengthened, not weakened. |

## Copy before → after

| State | Before | After |
| --- | --- | --- |
| Already solved | `Already solved` + `Accepted answers are never returned by the server. Further submissions cannot add score.` | `Solved` only |
| Just accepted | `Accepted` + `N points awarded.` + `Submitted answers are never shown again.` | `Accepted · +N points` only when the response carries a numeric `scoreAwarded`; otherwise `Accepted` |
| Wrong flag | `Rejected` + `That flag is not correct.` + answer-replay note | `Incorrect flag. Try again.` (the history status pill still shows the real `Rejected`) |
| Manual pending | `Submitted` + `Status: PENDING_REVIEW. Points are awarded only when a reviewer accepts the submission.` | `Awaiting review` |
| Network error | `Retry uses the same idempotency key and the same answer.` | `The request may not have reached the server. Your answer is kept for a retry.` (button unchanged) |
| Flag hint | `1–200 characters. The input clears immediately after submit.` | `1–200 characters.` (the real bounds stay) |
| Task description | framed panel with a visible `Description` header | unframed `section[aria-label="Task description"]` |
| History | outer frame + inner `Your submission history` heading | one `Your submissions` heading in `TaskPage`; `sr-only` table caption |
| Instance logs | `No log endpoint exists in the current Swagger…` placeholder | removed; no log API exists |
| Destroy dialog | `The provider must confirm destroy before ports/leases are released. This cannot be undone.` | `This cannot be undone.` |

The `Accepted · +N points` number comes from the response's `scoreAwarded` (`formatPoints`), never from
`Task.points`. A reload of an already-solved task has no result row in the form and therefore shows `Solved`
without a points suffix.

## Border / hierarchy measurements

Measured in Chromium against the owned stack at 1440×900 unless noted:

| Element | Measured |
| --- | --- |
| Task description section | border top/right/bottom/left = 0 / 0 / 0 / 0; no visible `Description` heading |
| Flag submission section | border top/right/bottom/left = 0 / 0 / 0 / 0; no header strip; no duplicate status pill |
| Flag input | 1 px border on all four sides retained |
| Submission history header row | 1 px bottom separator retained |
| Submission history body rows | 1 px separators on non-last rows (the shared `TableBody` still removes the last row's border) |
| Module card link | 0 px border on all four sides; background changes `#000000 → #191724` on hover |
| Task card link | 0 px border on all four sides; background changes `#000000 → #191724` on hover |
| Card keyboard focus | `:focus-visible` true; 1 px solid outline; 4 px offset |
| Full four-sided boxes left on the solved task page | 5, all semantic: the status/type/history badges and the `Refresh` button. No decorative frames remain. |

No global `border: none`, badge, table, card or alert default was changed. Organizer screens keep their
existing frames.

## Verification actually run

All commands were run serially on one owned stack (native PostgreSQL 18.7, Go backend with every optional
feature off, Vite, Chromium). The operator stack, port 8080, its database and its volumes were not touched.

```bash
# RED first: new unit assertions
cd frontend && bun run test -- \
  src/features/boards/components/FlagSubmitForm.test.ts \
  src/features/boards/components/ManualSubmissionForm.test.ts \
  src/features/boards/components/SubmissionHistory.test.ts \
  src/features/boards/pages/TaskPage.test.ts
# 14 failed | 14 passed (28) — recorded before implementation

# Green
cd frontend && bun run test -- --maxWorkers=1
# 45 files, 416 tests passed

cd frontend && bun run typecheck
cd frontend && bun run build
```

Real-browser checks (`/tmp/opencode/quieter-student-ui/verify.mjs`) passed 12 checks and 12 geometry samples
(board, module and task at 375/1024/1440/1920, all `scrollWidth == viewport width`). They cover:

- wrong flag → `Incorrect flag. Try again.`, input empty, history row added;
- one aborted POST → `Network error`; retry sends exactly one additional POST with the same
  `Idempotency-Key` and the same payload hash, then shows `Accepted · +100 points`;
- reload → `Solved` only, no `Already solved`, no invented points, no flag form;
- manual pending → network failure preserves the text and the payload, retry succeeds, text clears only on
  success, `Awaiting review` replaces `PENDING_REVIEW`, `Needs review` remains in history;
- staff preview → no flag/manual fields, no own history, zero `/submissions/me` requests;
- module/task card hover background and keyboard `:focus-visible` + Enter navigation.

Existing browser specs against the same stack:

```bash
cd frontend && bunx playwright test e2e/desktop-workspaces.spec.ts --project=chromium
# 16 passed, 5 phone-only skipped

cd frontend && bunx playwright test e2e/desktop-workspaces.spec.ts --project=phone
# 5 passed, 16 desktop-only skipped

cd frontend && bunx playwright test e2e/rich-text-editor.spec.ts --project=chromium
# 1 passed
```

The full-stack 18-spec Docker gate (`run-full-e2e.sh full-stage.spec.ts organizer-ux.spec.ts`) was **not**
run locally: its helper owns and force-removes containers and is unsafe beside the live operator stack. The
affected full-stack selectors were pre-audited and updated; the actual CI run remains the publication gate.
`frontend/e2e/board-flow.spec.ts` was updated but not executed because it needs `E2E_PROVISIONER_URL` to join
its private fixture; the equivalent wrong→accepted→history path is covered by the real-browser run above.

## Screenshots

After (this slice, `/tmp/opencode/quieter-student-ui/`):

`taskUnsolved`, `taskWrong`, `taskAccepted`, `taskAlreadySolved`, `taskManualPending`,
`boardmodulecards`, `module-tasks`, `taskNetworkError-SIMULATED`, `taskPreview` at desktop 1440;
`taskUnsolved`, `taskWrong`, `taskAccepted`, `taskAlreadySolved`, `taskManualPending`,
`boardmodulecards`, `module-tasks` at phone 375.

Before (base `08a6df4`, `/tmp/opencode/student-ux-audit/shots/`):
`10-task-read-1440`, `11-wrong-flag-1440`, `12-accepted-flag-1440`, `33-task-solved-1920`,
`44-phone-task-read-375`, `45-phone-wrong-flag-375`, `46-phone-accepted-375`,
`49-phone-manual-pending-375`, `08-joined-board-1440`, `09-module-http-1440`.

Main owns the visual judgement of these images.

## Limits

- Network failure is a clearly labelled simulated abort of one POST, not a real outage.
- Runtime instances are disabled in the default profile; `InstancePanel` copy was checked by source and unit
  tests, not by starting a real instance.
- One scripted student account; this is not a usability study.
- The CI full-stack suite is the remaining execution gate. Nothing is pushed or published before review.

## Cleanup

The owned PostgreSQL cluster, backend and Vite process groups were stopped with
`/tmp/opencode/quieter-student-ui/stack.sh stop`; no operator resource was started, stopped or pruned.
