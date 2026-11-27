# Quieter student UI verification

Status: **published and verified.** Source `bc266beafdb78aeaea22c2d16de3b0037c3b4615`, CI run
https://github.com/nrf24l01/aCTFb/actions/runs/45974501044 — success 4/4 (Backend quality, Frontend quality,
Integration, Publish images). Base of the slice: `08a6df4`.
Spec: `docs/superpowers/specs/2026-11-27-quieter-student-ui-design.md`.
Plan: `docs/superpowers/plans/2026-11-27-quieter-student-ui.md`.
Registry evidence: `docs/ghcr-verification.md`.

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
# 45 files, 419 tests passed

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

A direct student API check after the retry found exactly one `ACCEPTED` flag submission with
`scoreAwarded = 100`; the repeated wrong attempts and the aborted/retried POST added no second accepted row.
The manual task kept its `PENDING_REVIEW` rows with the submitted text stored, while the visible textarea had
cleared after the successful retry.

Existing browser specs against the same stack:

```bash
cd frontend && bunx playwright test e2e/desktop-workspaces.spec.ts --project=chromium
# 16 passed, 5 phone-only skipped

cd frontend && bunx playwright test e2e/desktop-workspaces.spec.ts --project=phone
# 5 passed, 16 desktop-only skipped

cd frontend && bunx playwright test e2e/rich-text-editor.spec.ts --project=chromium
# 1 passed
```

The full-stack 18-spec Docker gate (`run-full-e2e.sh full-stage.spec.ts organizer-ux.spec.ts`) now **passes
locally**: `18 passed (4.2m)`, `0 new containers, 0 new volumes`, core and Vue artifact hashes unchanged. It
was deferred in the first wave only because a second full Docker stack (PostgreSQL, NATS, RustFS, ClamAV) costs
CPU/RAM and the final CI run is the publication gate — not because of cleanup danger. `e2e/lib/run-cleanup.sh`
removes only exact owned names on any host and permits broad removal solely on a hosted ephemeral GitHub
runner (`GITHUB_ACTIONS=true` **and** `RUNNER_ENVIRONMENT=github-hosted`); beside a live operator stack it
refuses and reports instead of deleting. Observed locally: RustFS writes its temp data as uid 10001, so the
harness workdir `rm -rf` cannot unlink those files as the invoking user; the leftover directory was removed
with a throwaway container and no container or volume was left behind.
`frontend/e2e/board-flow.spec.ts` was updated but not executed because it needs `E2E_PROVISIONER_URL` to join
its private fixture; the equivalent wrong→accepted→history path is covered by the real-browser run above, and
the spec now asserts the history row's real `Rejected` status.

## Screenshots

After (this slice, `/tmp/opencode/quieter-student-ui/`):

`taskUnsolved`, `taskWrong`, `taskAccepted`, `taskAlreadySolved`, `taskManualPending`,
`boardmodulecards`, `module-tasks`, `taskNetworkError-SIMULATED`, `taskPreview` at desktop 1440;
`taskUnsolved`, `taskWrong`, `taskAccepted`, `taskAlreadySolved`, `taskManualPending`,
`boardmodulecards`, `module-tasks` at phone 375.

Fix-wave captures with animations disabled (contrast evidence): `fix-taskAccepted-desktop-1440`,
`fix-taskAlreadySolved-desktop-1440`, `fix-taskManualPending-desktop-1440`,
`fix-taskManualAccepted-desktop-1440`, `fix-taskAlreadySolved-phone-375`; the accepted phone state is
`taskAccepted-phone-375`.

Before (base `08a6df4`, `/tmp/opencode/student-ux-audit/shots/`):
`10-task-read-1440`, `11-wrong-flag-1440`, `12-accepted-flag-1440`, `33-task-solved-1920`,
`44-phone-task-read-375`, `45-phone-wrong-flag-375`, `46-phone-accepted-375`,
`49-phone-manual-pending-375`, `08-joined-board-1440`, `09-module-http-1440`.

Main owns the visual judgement of these images.

## Review fix wave (contrast + evidence truth)

The independent review requested two changes; both are implemented and verified.

**Success ink.** The compact `Accepted` and `Solved` lines used the brand accent `--pine` (`#31748f`), which
measures **4.03:1** on black — below the 4.5:1 bar for normal text — and mismatched the green `Solved` pill.
They now use the existing semantic success ink. `frontend/src/style.css` maps
`--color-status-success-ink: var(--status-success-ink)` in the `@theme inline` block (the same narrow pattern
already used for the attention/error tones), and `FlagSubmitForm.vue` (`ACCEPTED` tone and the `Solved` line)
plus `ManualSubmissionForm.vue` (`ACCEPTED` tone) use `text-status-success-ink`. No global palette, primitive
or organizer change; the check icon inherits `currentColor`.

| Location | Rendered colour | Background | Contrast |
| --- | --- | --- | --- |
| Flag accepted, 1440 | `#9ccf8f` | `#000000` | **11.74:1** |
| Flag solved, 1440 | `#9ccf8f` | `#000000` | **11.74:1** |
| Flag accepted, 375 | `#9ccf8f` | `#000000` | **11.74:1** |
| Flag solved, 375 | `#9ccf8f` | `#000000` | **11.74:1** |
| Manual accepted (history pill), 1440 | `#9ccf8f` | `#292d31` | **7.75:1** |
| Manual pending, 1440 (unchanged) | `#f6c177` | `#000000` | 12.81:1 |

Evidence: `contrast.json` (computed styles in Chromium) and `pixel-proof.json` (exact pixels in the committed
PNG captures, 0 old-pine pixels in every success image). The `ManualSubmissionForm` `ACCEPTED` branch is not
reachable through the normal review flow (a manual submit answers `PENDING_REVIEW`), so its colour is covered
by a unit assertion on the semantic token plus the in-browser `text-status-success-ink` probe that resolves to
`rgb(156, 207, 143)`.

**Evidence hygiene.** The accepted screenshot had caught the shared `Badge` mid colour transition (cyan while
the label already read `Solved`). Captures now pass `animations: 'disabled'` and the run waits for the header
pill's settled success ink before asserting and shooting; the mapping was never wrong.

**History classification.** `frontend/e2e/board-flow.spec.ts` and the real-browser script again assert the
history row's real `Rejected` status after a wrong flag, scoped to the history table, so the concise form copy
does not hide the server classification.

**Animation rule.** `transition-colors` was removed from `ModuleCard`/`TaskCard`; hover feedback is immediate,
so the slice adds no new transition.

New gates after this wave: 419 unit tests (45 files; 3 new contrast/semantic assertions), typecheck, build,
`desktop-workspaces` 16 desktop + 5 phone, `rich-text-editor` 1, 12 real-browser checks + 12 geometry samples,
4 success lines + pixel proof, and the local full-stack 18-spec run above.

## Publication and CI result

| Field | Value |
| --- | --- |
| Source commit (image source) | `bc266beafdb78aeaea22c2d16de3b0037c3b4615` |
| `main` HEAD at publication | `bc266be` (same commit; this docs update touches only `docs/`, which the workflow path filter excludes, so no further run or rebuild is expected) |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45974501044 |
| Conclusion | **success 4/4** — Backend quality, Frontend quality, Integration, Publish images |
| Run start / finish | 09:32:04Z → 09:50:05Z (~18 min) |
| Immutable tag | `sha-bc266beafdb78aeaea22c2d16de3b0037c3b4615` on all ten images |
| Registry | public; all ten verified by anonymous manifest + config-blob reads (`latest` digest equals the `sha-*` digest, `linux/amd64`, `org.opencontainers.image.revision` = source commit) |
| Published frontend assets | contain `Incorrect flag. Try again.`, `text-status-success-ink` and `Awaiting review`; no `Already solved` and no `points awarded` string |

Counts read from the job logs of that run (not inferred):

| Job | Step | Result |
| --- | --- | --- |
| Backend quality | Test all packages / Race detector | **50 packages `ok`, 13 without test files, 0 FAIL** (both runs) |
| Frontend quality | Unit tests | **45 files, 419 tests passed** |
| Frontend quality | Typecheck / build | pass / `built in 1.25s` |
| Integration | Real-browser acceptance (`p1-core`) | **1 passed** |
| Integration | Default-profile acceptance | **5 passed**, then **69 passed / 21 project-exclusive skipped** |
| Integration | Full-stack acceptance (`full-stage` + `organizer-ux`) | **18 passed** |
| Integration | Isolated P0 / P1 HTTP / RustFS integration | `ok` (42.2 s / 75.5 s / 67.9 s) |
| Integration | Real PostgreSQL auth / admin CLI / domain+boot / retention / Redis | `ok` (9.8 s / 1.1 s / 1.3 s / 2.3 s / 0.004 s) |
| Integration | Migration backfill and down/up | `ok` (2.3 s) |
| Integration | Packaging contract / CI-contract / cleanup guards | **424 / 14 / 21 checks passed, 0 failed** |
| Integration | Production image runtime + auth/proxy smokes | `PASS` |
| Publish images | Publish containers + summary | success; ten digests recorded in `docs/ghcr-verification.md` |

The `ManualSubmissionForm` `ACCEPTED` branch is unreachable through the browser (a manual submission answers
`PENDING_REVIEW`), so it is covered by a unit assertion and the browser token probe; no synthetic product
workflow was invented to exercise it. The manual *history* pill contrast (7.75:1 on its tinted surface) is
lower than the 11.74:1 black-background figure and is reported as measured.

## Limits

- Network failure is a clearly labelled simulated abort of one POST, not a real outage.
- Runtime instances are disabled in the default profile; `InstancePanel` copy was checked by source and unit
  tests, not by starting a real instance.
- One scripted student account; this is not a usability study.
- The `ManualSubmissionForm` accepted branch is unreachable in the real flow; see above.

## Cleanup

The owned PostgreSQL cluster, backend and Vite process groups were stopped with
`/tmp/opencode/quieter-student-ui/stack.sh stop`; no operator resource was started, stopped or pruned. The
operator stack (`actfb-prod`, 7 containers, port 8080) was untouched throughout, and its container/volume
counts were unchanged before and after the local runs.
