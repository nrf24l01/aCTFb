# Quieter student UI — independent review

**Range:** `08a6df4..c14ff17` (5 commits, 22 files, +720/−160)
**Reviewer:** independent subagent; product code was read-only.
**Verdict:** **CHANGES REQUESTED — not approved for publication.**

No Critical issue was found: there is no API, authorization, persistence, TTL, configuration, dependency or
backend change, and the retry/idempotency path is intact. One **Important** product defect blocks approval: the
new success outcome text uses the brand `--pine` colour at **4.03:1** on black instead of the semantic success
ink. One **Important** documentation correction is also required: the committed verification record calls the
full-stack helper "unsafe" although its cleanup is now guarded against exactly that.

The review used the diff, current source, committed docs, the existing PNG screenshots (pixel measurement),
`measurements.json`, the run logs and the cleanup helper. I did not run a server, build, test suite, browser or
subagent, and I did not commit or push anything.

## Blocking findings

### I1 — Success outcome text is `--pine` (#31748f) at 4.03:1 on black

**New, user-visible accessibility and semantic regression.**

- `frontend/src/features/boards/components/FlagSubmitForm.vue:75` — `ACCEPTED` outcome returns `'text-pine'`.
- `frontend/src/features/boards/components/FlagSubmitForm.vue:160` — the already-solved `Solved` alert is
  `text-pine`.
- `frontend/src/features/boards/components/ManualSubmissionForm.vue:61` — `ACCEPTED` outcome returns
  `'text-pine'`.
- `frontend/src/style.css:99` defines `--pine: #31748f`; `frontend/src/style.css:112` defines the semantic
  success ink `--status-success-ink: #9ccf8f`.

**Evidence (actual rendered pixels, not class presence):**

| Screenshot | Pine pixels | Bounding box | Background | OCR |
| --- | --- | --- | --- | --- |
| `taskAccepted-desktop-1440.png` | 185 × `(49,116,143)` | x 1057–1238, y 253–266 | `(0,0,0)` | `Accepted · +100 points` |
| `taskAlreadySolved-desktop-1440.png` | 64 × `(49,116,143)` | x 1057–1130, y 252–265 | `(0,0,0)` | `Solved` |

Measured contrast:

| Pair | Ratio |
| --- | --- |
| `#31748f` on `#000000` | **4.03:1** |
| `#31748f` on `#191724` | 3.38:1 |
| old title colour `#e0def4` on `#000000` | 15.92:1 |
| semantic `--status-success-ink` `#9ccf8f` on `#000000` | 11.74:1 |
| `#9ccf8f` on its own `#292d31` surface | 7.75:1 |

The `Alert` root uses `text-sm` (`frontend/src/components/ui/alert/index.ts:12`), and `AlertTitle` only adds
`font-medium`; this is normal-sized text, so the main's **4.5:1** requirement applies. The rendered `text-pine`
utility wins over the Alert's `text-card-foreground`, as the pixel measurement shows. At base `08a6df4` the
success title inherited `#e0def4`; only the border was pine. This slice therefore moves the primary success
line from 15.92:1 to 4.03:1 and replaces the semantic success colour with a brand accent.

The rejected (`text-love`, 7.22:1) and manual-pending (`text-gold`, 12.81:1) tones are fine.

**Required action:** use the existing semantic success ink for the accepted and solved compact lines (and the
check icon) instead of `text-pine` — for example add `--color-status-success-ink: var(--status-success-ink)` to
the `@theme inline` block and use `text-status-success-ink`, or set `style="color: var(--status-success-ink)"`
on the Alert. Keep the text label and icon. Add a unit assertion that the accepted/solved line references the
success token (not `text-pine`) and re-measure the rendered colour at ≥4.5:1. This also resolves the visible
mismatch where the task-header `Solved` pill is green but the compact `Solved` line is cyan.

### I2 — Committed docs falsely call the full-stack helper unsafe

**Important documentation accuracy issue in the verification record.**

- `docs/quieter-student-ui-verification.md:115` — "its helper owns and force-removes containers and is
  unsafe beside the live operator stack."
- `docs/implementation-plan.md:1199` — "its helper is unsafe beside the live operator stack."
- `/tmp/opencode/quieter-student-ui/report.md:116-118` repeats the same wording.

The current helper contradicts this:

- `e2e/lib/run-cleanup.sh:12-19` — broad removal is allowed only when `GITHUB_ACTIONS=true` **and**
  `RUNNER_ENVIRONMENT=github-hosted`.
- `e2e/lib/run-cleanup.sh:51-57` — outside that case it refuses broad cleanup, prints the leftovers and
  returns 1.
- `e2e/run-full-e2e.sh:71-80` — removes only exact owned names and documents the function as "safe on any
  host, including a developer machine beside a live operator stack."

Not running the 18-spec Docker gate locally is a reasonable resource decision, but the reason is a second full
Docker stack and its CPU/RAM cost plus the remaining CI gate — not unsafe deletion. The wording should not tell
a future operator that a guarded helper is unsafe.

**Required action:** reword both committed docs (and the `/tmp` report when it is refreshed) to say that the
gate was not run locally to avoid a second full Docker stack and its resource cost; the cleanup guard would not
delete operator containers, and the 18-spec CI run remains the publication gate.

## Non-blocking findings

### M1 — The accepted screenshot caught a transient status-pill colour

The main's observation is real in the PNG but is a capture artifact, not a new colour-mapping bug.

- `taskAccepted-desktop-1440.png` header pill, x 1206–1259, y 136–143: active ink `(156,207,216)` on active
  surface `(41,45,58)`, while OCR reads **`Solved`**.
- `taskAlreadySolved-desktop-1440.png`, same box: success ink `(156,207,143)` on success surface
  `(41,45,49)`.

The label updates to `Solved` as soon as the parent refetches the task, but the shared `Badge` has a
pre-existing `transition-all` (`frontend/src/components/ui/badge/index.ts`), so the colour is still animating
from the previous `AVAILABLE` active tone. `verify.mjs:28-31` does not disable animations. The settled state is
green, and the header mapping is correct. To avoid misleading evidence, add `animations: 'disabled'` to the
screenshot call or wait for the pill to settle; no product code change is required.

### M2 — The wrong-flag history classification is no longer asserted in `board-flow`

`frontend/e2e/board-flow.spec.ts:172` replaced the old `Rejected` assertion with `Incorrect flag. Try again.`.
The source still keeps the real classification (`SubmissionHistory` uses `StatusPill` with the API status, and
`statusLabel('REJECTED')` is `Rejected`), and `verify.mjs:246` checks that a history row was added — but it does
not check that row's status. The user's requirement was "wrong flag ... same error classification"; this is now
a coverage gap rather than a code defect. Add
`await expect(page.getByRole('table').getByText('Rejected').first()).toBeVisible()` after the wrong attempt, or
assert the new row's status in `verify.mjs`, without weakening the compact-copy assertion.

### M3 — Task description lost its heading level

`frontend/src/features/boards/pages/TaskPage.vue:213` is `<section aria-label="Task description">` with no
heading. The region is still named and maps to a landmark, so this is not a region/name loss. Heading
navigation, however, no longer offers a "Task description" entry. If that matters, add
`<h2 class="sr-only">Task description</h2>` and keep the section labelled by it; this preserves the requested
"no visible Description header" design. Optional.

### M4 — Focus outline is faint (pre-existing)

`measurements.json` reports `outlineWidth: 1px`, `outlineOffset: 4px`, and
`outlineColor: oklab(0.653872 0.0161122 -0.0413805 / 0.5)` — the ring colour at 50%, about **2.28:1** on black,
not the full `--text` colour. This is pre-existing (`frontend/src/style.css:196,209-212`) and unchanged by the
diff; removing the card border makes the focus cue more important, though. If "focus clear" means the WCAG 2.2
3:1 focus-appearance bar, use `focus-visible:outline-ring` or `focus-visible:outline-foreground` and keep the
4 px offset. Not a new blocker.

### M5 — Card hover adds a new transition

`ModuleCard.vue:13` and `TaskCard.vue:13` add `transition-colors`. The plan's global constraints say "no ...
new animation"; this is a short colour transition used as hover feedback, so it is likely acceptable, but it is
a literal deviation. Either remove it or record it as intentional hover feedback.

### Observation — stale verdict and network error can be visible together

`taskNetworkError-SIMULATED-desktop-1440.png` shows the previous `Incorrect flag. Try again.` above the network
error; the manual-pending screenshot similarly shows `Awaiting review` plus a network error. This is
pre-existing: `submit()` does not clear `verdict`, and the base component behaved the same way. It is not
introduced by this slice and does not need a fix here; if the main wants a single current-state line, clearing
`verdict` at the start of a new attempt would be a separate behaviour change.

## Checklist verification

| Area | Result |
| --- | --- |
| Scope / no backend | **Pass.** `git diff --stat 08a6df4..c14ff17` contains only frontend source/tests, e2e tests and docs; no Go, migrations, `go.mod`/`go.sum`, config or dependency change. |
| Copy and states | **Pass** for solved-only, accepted points, wrong flag, manual pending, network copy and history heading. `FlagSubmitForm.vue:61-77`, `ManualSubmissionForm.vue:48-63`, `SubmissionHistory.vue`, `TaskPage.vue:268-273`. |
| Score source | **Pass.** `scoreAwarded` is used directly (`FlagSubmitForm.vue:64-69`, `ManualSubmissionForm.vue:51-56`); `accepted(40)` proves the task maximum is not substituted; reload shows `Solved` with no points. `history-check.json` records one `ACCEPTED` row with `scoreAwarded: 100`. |
| Duplicate outcomes | **Pass.** The `v-else-if` chain in `FlagSubmitForm.vue:153-163` renders either the current outcome or `Solved`, never both; the parent-reload test asserts one `Accepted`. |
| Retry / idempotency | **Pass by source and recorded evidence.** The in-memory `inFlight` key/text is kept on network failure and reused by `retry()` (`FlagSubmitForm.vue:88-150`); `measurements.json` records two posts with `sameKey: true` and `samePayload: true`; `history-check.json` records exactly one accepted flag row. Manual text and draft survive a network error and clear only on success (`ManualSubmissionForm.vue:106-126`). |
| Borders / geometry | **Pass.** Description and flag sections measure 0/0/0/0; flag input 1 px; history header 1 px and non-last body rows 1 px; cards 0 px with hover background; 5 semantic boxes remain; 12 geometry samples at 375/1024/1440/1920 show no document overflow. |
| Accessibility | **Mostly pass; I1 blocks.** Section labels/regions remain (`submit-heading`, `manual-submit-heading`, `history-heading`, `instance-heading`, `Task description`), the history table has an `sr-only` caption and semantic headers, and the status is never colour-only. The success text contrast and semantic tone fail I1; M3/M4 are optional follow-ups. |
| Primitive defaults | **Pass.** `alertVariants` and the shared Badge/Table defaults are unchanged; the new `border-0`/`bg-transparent` classes are local to student surfaces. |
| Cards / focus | **Pass with M4/M5 notes.** Cards remain semantic `RouterLink`s with visible labels, no hidden action fields, hover `#000000 → #191724`, and a 1 px / 4 px-offset focus outline. |
| Organizer surfaces | **Pass.** `BoardPage`/`ModulePage` changes are confined to the unauthenticated participate panel, the student standing panel and the pentest-lab section; organizer frames are untouched. |
| Runtime copy | **Pass.** `InstancePanel` removes only the developer paragraph, the fabricated-log placeholder and the provider/internal wording; status, `cannotStartReason`, TTL, extension credits, errors and the destroy confirmation remain. No action/function change. |
| Tests | **Reported, not rerun.** Logs match the report: 416 unit tests, typecheck/build pass; 12 browser checks + 12 geometry samples; `desktop-workspaces` 16 desktop + 5 phone; `rich-text-editor` 1. The full 18-spec Docker gate was not run; see I2 for the wording correction. |
| Cleanup | **Pass.** No owned `quieter-student-ui` / `actfb-full` containers or volumes, no listeners on 55433/18080/15173, no owned PID files or process groups; the operator 8080 listener is separate. |
| Secrets | **Pass.** Test strings are synthetic (`flag{...}`, `x`.repeat, `durable manual answer`); no real credentials or flags are committed. `/tmp` fixtures are outside the repo. |
| Visual composition | **Pass on the main's stated direction.** Fewer explanatory paragraphs and decorative frames; task/board/module/history/instance composition is materially quieter. Main owns the final visual judgement. |

## Required actions before approval

1. **I1:** replace `text-pine` with the semantic success ink for `Solved`/`Accepted` in
   `FlagSubmitForm.vue:75,160` and `ManualSubmissionForm.vue:61`; verify the rendered colour is ≥4.5:1 and add
   a regression assertion.
2. **I2:** correct the "unsafe" wording in `docs/quieter-student-ui-verification.md:115` and
   `docs/implementation-plan.md:1199` (and the `/tmp` report) to the accurate resource/CI reason.
3. **M2 (recommended):** restore the `Rejected` history assertion in `frontend/e2e/board-flow.spec.ts` or
   `verify.mjs`.
4. **M1 (evidence hygiene):** disable animations or wait for the status pill to settle before the accepted
   screenshot; no code change required.
5. Optional: M3 (sr-only description heading), M4 (stronger focus outline), M5 (remove/document the card
   transition).

No other new Critical or Important defect was found in this range.

---

# Re-review — fix wave `c14ff17..3e9cea0`

**Range:** `c14ff17..3e9cea0` (3 commits: `e7da990` fix, `37a6df4` tests, `3e9cea0` docs).
**Scope:** I1, I2, M1, M2, M5 only.
**Verdict: APPROVED — ready to push.** I1 and I2 are resolved, no Critical or Important issue remains, and no
new defect was introduced in the fix wave. One Minor comment-accuracy nit is listed below and does not block.

Diff shape (`git diff --stat c14ff17..3e9cea0`): 10 files, +141/−13 — `style.css` (+1), three component class
strings, two card class strings, three test files, two docs. No product logic line moved.

## I1 — resolved

**Source.** `frontend/src/style.css:72` adds `--color-status-success-ink: var(--status-success-ink)` inside the
existing `@theme inline` block; `--status-success-ink: #9ccf8f` at `frontend/src/style.css:112` is untouched, so
the token stays single-sourced and the utility resolves to `var(...)` rather than a copied hex.
`FlagSubmitForm.vue:75` (`ACCEPTED` tone), `FlagSubmitForm.vue:160` (`Solved` line) and
`ManualSubmissionForm.vue:61` (`ACCEPTED` tone) now use `text-status-success-ink`; `grep text-pine` in both forms
returns nothing.

**Emitted CSS, not just class presence.** The post-fix build emits
`.text-status-success-ink{color:var(--status-success-ink)}` in `frontend/dist/assets/index-xcievV26.css`. Byte
offset 31111 sits after `.text-card-foreground{` at 30315 and after the old `.text-pine{` at 30762, so the
success utility wins the equal-specificity conflict against the `Alert` variant's `text-card-foreground` for the
same reason `text-pine` used to — the mechanism is deterministic, not accidental.

**Measured colour, reproduced independently.** `contrast-red.log` shows the genuine RED: `flag accepted 1440:
colour must be the semantic success ink ... {"color":"rgb(49, 116, 143)","background":"rgb(0, 0, 0)"}` (4.03:1),
and the pre-mapping token probe resolving to body text `rgb(224, 222, 244)`. `contrast.json` after the fix:
11.74:1 for flag accepted/solved at 1440 and 375, 12.81:1 for manual pending (unchanged).

I re-counted the PNG pixels myself rather than trusting `pixel-proof.json`, and the numbers match it exactly:

| Capture | old pine `(49,116,143)` | success `(156,207,143)` |
| --- | --- | --- |
| `fix-taskAccepted-desktop-1440` | 0 | 244 |
| `fix-taskAlreadySolved-desktop-1440` | 0 | 123 |
| `fix-taskManualAccepted-desktop-1440` | 0 | 73 |
| `fix-taskManualPending-desktop-1440` | 0 | 0 (gold, correct) |
| `taskAccepted-phone-375` | 0 | 217 |
| `fix-taskAlreadySolved-phone-375` | 0 | 138 |

**Manual history vs manual form — labelled honestly, and the limitation is real.** The 7.75:1 figure is the
history `Accepted` badge on its own `rgb(41,45,49)` surface, and `contrast.mjs:290-296` targets exactly that
(`section[aria-labelledby="history-heading"] table tbody tr` → `[data-slot="badge"]`), not the form. The
`ManualSubmissionForm` `ACCEPTED` branch is **not** reachable through the normal flow — a manual submit answers
`PENDING_REVIEW` and the verdict only comes from a submit response (`ManualSubmissionForm.vue:48-63` with
`internal/boards/service_tasks.go:379-460`) — so it is covered only by the unit assertion
(`ManualSubmissionForm.test.ts:117-138`) plus the in-page token probe, and the docs say so. I am not claiming
that branch was browser-exercised; the screenshot `fix-taskManualAccepted-desktop-1440` shows the form with no
accepted line and the history row reading `Accepted`, which is the honest state.

## I2 — resolved

`docs/quieter-student-ui-verification.md:114-125` now states the gate passes locally and gives the correct
reason for the earlier deferral (a second full Docker stack costs CPU/RAM; CI is the publication gate), plus the
real guard: owned names removed on any host, broad removal only when `GITHUB_ACTIONS=true` **and**
`RUNNER_ENVIRONMENT=github-hosted`. That matches `e2e/lib/run-cleanup.sh:12-19,51-57` and
`e2e/run-full-e2e.sh:71-80`. `docs/implementation-plan.md:1199-1205` no longer repeats the claim, and the older
2026-11-26 entry at `docs/implementation-plan.md:1176` is a dated historical record that now carries an explicit
"Superseded 2026-11-27" note — an accurate way to correct a past statement. `grep -n unsafe` finds no remaining
current claim about the helper.

The 18-spec gate was actually executed this time: `full18.log` ends with `18 passed (4.2m)`,
`owned Docker resources: 0 new containers, 0 new volumes`, `core and Vue artifact hashes unchanged`, and the log
openly records the RustFS uid-10001 `rm` failures instead of hiding them. That leftover directory is gone now
(`/tmp/opencode/actfb-full-*` absent), and no owned container, volume, listener (55433/18080/15173), process or
PID file remains.

## M1 — resolved in evidence, correctly not treated as a product bug

`contrast.mjs:92-100` passes `animations: 'disabled'` to every screenshot and polls the header pill until it
settles; `contrast.json` records `header pill settled {"ink":"rgb(156, 207, 143)","surface":"rgb(41, 45, 49)"}`.
The accepted capture now shows the settled success pill, so the cyan-in-transition artifact is gone without a
product change. The mapping was never wrong, as established in the first pass.

## M2 — resolved, with the honest caveat

`frontend/e2e/board-flow.spec.ts:173-177` reinstates the classification assertion, scoped to
`section[aria-labelledby="history-heading"] table` and filtered on `tbody tr`, so it asserts the history status
row rather than a generic page match; the compact-copy assertion is kept. The same check now exists in the
real-browser script (`verify.mjs:254-256`, `history must keep the real Rejected classification`) and
`contrast.json` records the row text `27 Nov 2026, 09:18 UTC Rejected — Incorrect flag —`. `board-flow.spec.ts`
itself still needs `E2E_PROVISIONER_URL` and was not executed locally; the docs say so, so the coverage is
claimed where it was actually exercised.

## M5 — resolved

`ModuleCard.vue:13` and `TaskCard.vue:13` drop `transition-colors`; hover `bg-muted` and
`focus-visible:outline-offset-4` remain, so the slice adds no transition and keeps both affordances.

## Scope and regression check

- No `send`/`retry`/`submit`/`inFlight`/key line appears in the fix diff; retry still reuses the same
  in-memory key and payload, and no request payload, verdict, score field, idempotency or storage behaviour
  changed.
- No API, RBAC, TTL, permissions, migration, config or dependency change; the diff is class strings, one theme
  mapping, tests and docs.
- No global palette or primitive default changed: the new mapping adds one utility and leaves `Alert`,
  `Badge`, `Table` and the `--status-*` token values alone. Organizer surfaces are untouched.
- TDD is real: `fix-red-unit.log` shows `3 failed | 17 passed (20)` before the fix (the three new semantic-ink
  assertions), `unit-fix.log` shows `45 files / 419 tests passed`, and `typecheck-fix.log` / `build-fix.log`
  pass. 416 + 3 new = 419, consistent with the docs.

## New Minor finding (non-blocking)

**`frontend/src/style.css:61-67` — the `@theme inline` comment is now inaccurate.** It still ends with "Only
the tones the editor uses are exposed, so nothing else changes", but `--color-status-success-ink` is now exposed
for the student success lines, not the editor. Suggested: "The tones used by the editor and the student
outcome lines are exposed; nothing else changes." Comment-only, no behaviour.

Also a precision note on the evidence, not a defect: `pixel-proof.json`'s `contrastOnBlack` is a constant
target-vs-black computation for every capture, so the `manual accepted 1440` entry reads 11.74 while the real
local ratio is 7.75 on the badge surface — `contrast.json` carries the correct background-aware number and both
are labelled, so nothing is overstated.

## Residual items from the first pass

M3 (no heading for the description region) and M4 (focus ring at `ring/50`, ~2.28:1) were accepted by the main
as pre-existing or outside the "less text, fewer borders" request; I did not treat them as blockers then or now.
No other audit findings were pulled into this wave.

## What this re-review did and did not do

I read the diff, the appended report, the committed docs, the logs/JSON evidence and the PNG pixels, and
independently recomputed the pixel counts and contrast ratios; I also confirmed the emitted CSS and its ordering
in the existing build output. I did not run a server, build, test suite, browser or Docker stack, and I used no
subagents. Verification here is therefore artifact-level and consistent with the implementer's reported gates,
not an independent re-execution. The only file I wrote is this review document (appended, untracked); I made no
commit, push or other working-tree change.

**Final verdict: APPROVED.** I1 and I2 are closed, no Critical or Important defect remains in
`c14ff17..3e9cea0`, and the slice stays within the small presentation/copy/frames scope. The single Minor
comment fix can ride along or be skipped without affecting approval.
