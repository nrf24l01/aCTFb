# Whole-slice independent review — desktop workspaces and direct authoring

Range reviewed: `0da549e..07b0be8` (`0ccd41e` docs, `8c4f22b`/`9c4466b` Task 1,
`006809d`/`21b14fe` Task 2, `c1be068`/`e3b613e`/`84302bc`/`c9e3d3f`/`07b0be8` Task 3).
Package: `.superpowers/sdd/2026-11-26-desktop-workspaces/review-0da549e..07b0be8.diff`.

Excluded from this verdict: `15bf7fa` and `6257347` (CI optimisation, separate review).
They touch `.github/workflows/go.yml`, `e2e/run-e2e.sh`, `e2e/run-full-e2e.sh`,
`scripts/*` and docs; no reviewed UI source, no `frontend/**`, no
`e2e/run-default-profile-e2e.sh`. The reviewed UI slice is intact at `07b0be8`.

Inputs: the approved spec, `progress.md`, the three task reviews and reports,
`docs/desktop-workspaces-verification.md`, `docs/frontend.md`,
`docs/implementation-plan.md`, the committed sources, the e2e spec and
`frontend/playwright.config.ts`.

Read-only review: no tests, typecheck, build, browser, Docker, server or subagent was
run; no commit, push or file change other than this report. Evidence was re-derived from
the diff, the committed sources and the recorded run log. I confirmed the screenshot
directories contain 20 files in `/tmp/opencode/desktop-workspaces/final/` and 39 in
`final-all/`, but I did not view the images (text-only model); main's visual acceptance
is the authority.

## Verdicts

- **SPEC: APPROVED.** The frozen design is implemented and measured on every surface it
  names. No new interaction, no new UX choice, no backend/API/schema/migration/deployment
  or dependency change, no new fetch or mutation, no removed product test.
- **QUALITY: APPROVED** for the product, source, tests and evidence. **No Critical and no
  product-affecting Important defect.** One **Important CI-reliability** item (F-1) and six
  **Minor documentation/evidence** items (F-2–F-7) must land in the publication wave. None
  changes a capability claim, a test assertion, a product behaviour or the approved layout.
- **Publication remains pending.** Nothing is pushed; the four-job CI has not run on this
  commit; main has accepted the 20 screenshots; GHCR `latest` remains the `0da549e`
  baseline. This review does not publish anything.

## Whole-slice checks

| Check | Evidence |
|---|---|
| Page cap and shell | `PageContainer.vue:15-19` — 1600px outer, `bounded` keeps 1152 for the profile; `AppShell.vue:178` aligns the alert strip; measured 1552 / 1180 / 764 / 700 / 343 usable |
| Workspace rails | `style.css:671-698` — `workspace` container, 1100px threshold, 320px rail (360px task), 24px gap, `min-width: 0`; `TaskPage.vue:212-262`, `BoardPage.vue:218-239`, `BoardsPage.vue:72-95` |
| Card tracks | `style.css:702-756` — board 760px, module tasks/admin 720px/1200px, equal tracks, normal row order; applied at `BoardPage.vue:229-233`, `ModulePage.vue:150-160`, `AdminHubPage.vue:27-33`, `AdminTemplatesPage.vue:97-112` |
| Authoring geometry | `style.css:606-637` — board/module 1200px, task 1360px, 24px clearance, 1040px body container, one scroller; `style.css:797-809` — phone exactly `100dvw`/`100dvh`, 16px padding, 343px body |
| Direct authoring | Script blocks of the four forms differ from base only by removed `Tabs`/`renderMarkdown` imports and the `preview` computed; `MarkdownEditor.vue` untouched; one `MarkdownEditorLazy` per form; no `role="tab"`; Visual/Source, counter, one form-owned length error, `aria-invalid`/`aria-describedby` intact (`desktop-workspaces.spec.ts:1373-1455`) |
| Deployment fields | `TaskEditorDialog.vue:512-614` template/parameters in main, `:716-804` lifetime in rail; `instanceProblem`/`toStringMap`/payload unchanged; secrets blank, `password`, `new-password`; binding immutable on edit; `TaskEditorDialog.instanceLayout.test.ts` covers 24 parameters, unique IDs and labels |
| Preview scope | `TaskPage.vue:41-46` (preview flag, no role fallback), `:88-96` (no own-history request in preview), `:212-282` (no participant form/instance/history in preview); browser `desktop-workspaces.spec.ts:937-956` |
| No backend/dependency change | `git diff --stat 0da549e..07b0be8` is frontend, e2e and docs only; no Go, SQL, package or `deploy/` file; `run-default-profile-e2e.sh:311-314` adds the spec to the existing invocation; no workflow file changed |
| Phone/profile/zoom/prose | `ProfilePage.vue:93` bounded; `style.css:762-764` 80ch on text blocks only, `pre`/`table` keep panel width and local scroll; `style.css:770-776` legacy invite wraps; `style.css:783-789` 16rem editor; browser `:1218-1369` (CSS zoom and device metrics), `:747,1048,1516-1584` (375) |
| Container vs portalled modals | `PageContainer.vue:16` is the named inline-size container; `DialogContent.vue:24` and `SheetContent.vue` render through `DialogPortal`; no `fixed`/`sticky` descendant in `features/`; shell header/sidebar sit outside the container |
| Lifecycle | `AdminBoardPage.vue:259-313` — labelled, initially collapsed disclosure; `:272` binds `aria-controls` only while the `v-if` body exists; permission predicates and confirmations are identical to `0da549e`; `AdminBoardPage.test.ts:176-191`, browser `:960-1010` |
| ARIA and IDs | `AdminBoardPage.vue:272`; `TaskEditorDialog.vue:574` gives the boolean parameter its `param-<key>` id; server keys are validated (`internal/deployment/validator.go:16,101-107`); only the open dialog is in the DOM; fixture asserts unique IDs and a resolvable label for every parameter |
| Catalog and cards | DOM order preserved (no masonry); catalog fixtures use the real authoring API, this run's own 3 then 6 records, real links and the product Refresh (`desktop-workspaces.spec.ts:1052-1118`); no fake counters or invented metadata (`AdminTemplatesPage.vue:97-112`) |
| INSTANCE honesty | `desktop-workspaces.spec.ts:623-641` asserts the binding endpoint answers `404` and that the dialog is **not** the catalog-disabled state; the long-form layout comes from a labelled fixture whose header says it is not provider execution; no provider, credential, instance or container is touched |
| Evidence | Root suite `5 passed`; frontend combined `69 passed / 21 skipped`; 403 unit tests; typecheck/build accepted; 20/39 screenshots present |
| Post-`07b0be8` source | `git log 07b0be8..HEAD -- frontend/src frontend/e2e frontend/playwright.config.ts` is empty; only the excluded CI commits and docs follow |

The two Task 3 source fixes are verified and introduce no new bug:
`AdminBoardPage.vue:272` removes the dangling IDREF while collapsed and the test now
asserts the stronger absent/equal pair; `TaskEditorDialog.vue:574` repairs the orphan
boolean-parameter label, with validated unique keys, no duplicate IDs and unchanged
secret/payload behaviour.

## Findings

### F-1 (Important, CI reliability, latent) — the spec hook can exhaust its 90s budget

Files: `frontend/e2e/desktop-workspaces.spec.ts:77-92` (`signIn`), `:358` (`beforeAll`),
`frontend/playwright.config.ts:9` (`timeout: 90_000`).

After a failed sign-in the helper waits a blind `61_000` ms; `beforeAll` signs in two
roles and then creates the board, four modules, tasks, templates and the join code. The
theoretical two-role bound is 122s, but the realistic exposure is one 61s wait plus the
8s failed-login detection, leaving roughly 21s of the 90s hook for all fixture creation.
A slow cold CI can therefore time out at the hook before any assertion runs. This was not
observed in the recorded run and changes no product or test semantics.

Fix (one small change): give the hook an explicit budget, e.g. `test.setTimeout(240_000)`
as the first statement of `beforeAll` (or an equivalent suite-level timeout), and/or wait
on the limiter response instead of a blind 61s. Keep `workers: 1`; do not weaken the
limiter. This is the only fix in the wave that touches a test file.

### F-2 (Minor, stale number) — verification doc keeps the corrected 776 expectation

`docs/desktop-workspaces-verification.md:62` — `| module tasks | 776 / 700 | 2 / 1 |`.
`776` is neither measured nor asserted anywhere; the report's own RED table
(`task-3-report.md:86`) records that the 776 expectation was the mistake and the real
effective width is 700. The row is behaviourally harmless (776 ≥ 720 would also be two
columns) but it sits under a "real DOM" heading. Fix it to `700 | 1`, or drop the row —
the `1199/1200` rows above already prove the 2/3-column switch.

### F-3 (Minor, new, evidence accuracy) — the catalog total is not 3 then 6 on a fresh CI database

Lines: `task-3-report.md:292-294` ("On a fresh CI database the total is exactly 3 then 6"),
`task-3-report.md:161-162` ("6 real records … 3×2", "3 real records … 3×1"),
`docs/desktop-workspaces-verification.md:34` ("3 then 6 on a fresh database") and
`:103` / `task-3-report.md:247` ("real six-template catalog").

The root default-profile suite runs first (`e2e/run-default-profile-e2e.sh:297`, before
the frontend invocation at `:314`) and creates one template
(`e2e/tests/default-profile.spec.ts:145-152` → `AdminTemplatesPage.vue:47-53` →
`templatesApi.create` → `internal/deployment/store.go:65` inserts into
`deployment_templates`; `listTemplates` at `:110-120` counts every row, with no status
filter). The desktop spec then owns 3 and then 6 records. On a fresh CI database the
**real total** is therefore 4 then 7, not 3 then 6, and the 1552px grid rows are 3×2 and
3×3. The test is correct — it derives rows from the real total
(`desktop-workspaces.spec.ts:1069-1118`) — and the report itself says the shared database
holds the root spec's template plus this run's (`task-3-report.md:90`), which contradicts
`:294`. Only the prose/table are wrong.

Fix: state "this run's own 3 then 6; the root suite's one template makes the real total
4 then 7 on a fresh CI database", and either drop the fixed 3×1/3×2 rows or label them
as the run's own records with rows derived from the real total. The screenshot name
`admin-templates-1920-6.png` refers to the run's own six; the rendered catalog is the
real total, so the caption should say so.

### F-4 (Minor, evidence) — test-count attribution is wrong

`task-3-report.md:14` — "11 new tests plus hardening of the existing 10". The base file
has 12 tests and the range adds 9 (12 → 21). Fix to "9 new tests plus hardening of the
existing 12". The 21 total is correct everywhere else.

### F-5 (Minor, evidence) — skip attribution is inverted

`task-3-report.md:130-132` — "16 chromium skips for the 5 phone-only tests, 5 phone skips
for the 16 chromium-only tests". It is the reverse: chromium contributes 5 skips (the 5
`phone`-only tests) and phone contributes 16 (the 16 `chromium`-only tests). The log and
the surrounding arithmetic are correct; only this clause is wrong.

### F-6 (Minor, docs) — the drawer breakpoint is 768px, not 760px

`docs/frontend.md:317` (new) and `:282` (pre-existing) say the sidebar becomes a Sheet
drawer "below 760px". The switch is Tailwind `md`, i.e. **768px** (`AppShell.vue:98`
`md:hidden`, `:151` `md:block`); 760px is the touch-target/typography query
(`style.css:570`). Fix the new line to 768px and optionally the old one; if precision
matters, split the sentence: the drawer switches at 768px, the 44px control rule applies
at ≤760px.

### F-7 (Minor, status lines) — the review/screenshot status is now stale

`docs/desktop-workspaces-verification.md:3`, `docs/implementation-plan.md:1004-1006` and
`task-3-report.md:3` still say "pending independent review and main screenshot
inspection". This review and main's acceptance satisfy that gate. Update them in the
publication wave to "reviewed; publication pending CI" — do **not** claim published until
the four-job CI run is green.

## Non-blocking observations (intended, recorded)

- `serial` mode in the describe means one failure skips the remaining tests of the file.
  This is a deliberate fixture-sharing trade-off; the recorded run is fully green.
- The 300ms settle in the over-limit save test is a bounded negative check, not a
  substitute for a state assertion.
- The boolean parameter now has two labels (the `FormField` label and the wrapping
  `<label>`). This is valid HTML and the accessible name includes the field label; it is
  an improvement over the orphan label.
- The INSTANCE layout fixture is mocked and labelled; it is not provider execution. The
  browser test asserts the real `404` and the honest error state.
- On a reused local database the catalog test asserts this run's own records plus the real
  total; on a fresh CI database the real total is 4/7, not 3/6 (F-3).
- The phone project is Chromium at 375×812 with `isMobile`/`hasTouch`, not WebKit or a
  physical device.
- CSS `zoom` is a container-query surrogate, not OS zoom; the 960×540 device-metrics test
  is the closest local approximation. Neither claims operating-system zoom.
- The admin-hub card count is derived from the seeded admin's roles, so rows change with
  roles while columns are still asserted at 3/2/1.
- No `page.route`/`context.route`/`routeFromHAR`/`.only` appears in the e2e specs; no CRUD,
  catalog, editor, threshold or responsive assertion is skip-gated.
- I did not view the 20 screenshots; main accepted them. No new visual review is requested.

## Release gates (finite)

1. **One fix wave:** F-1 (hook timeout) and F-2–F-7 (docs/evidence). No product code, no
   UX, no test semantics; F-1 is the only test-file change.
2. **Push and run the existing four-job CI.** Do not add a job, matrix or secret in this
   wave. Confirm the default-profile step executes `desktop-workspaces.spec.ts` with the
   root `5 passed` and only the 21 project-specific skips, and that unit/typecheck/build
   stay green.
3. **Confirm no UI source changed** between `07b0be8` and the pushed commit except the fix
   wave (test/docs only). If the CI-optimisation commits land first, keep that review
   separate and do not re-open the GHA contract as a UI finding.
4. **Publish only after green.** GHCR `latest` stays the `0da549e` baseline until then.
5. **No new screenshot round:** main accepted the 20-file inspection set; the fix wave
   changes no visual surface.

## Handoff

Main: apply F-1–F-7 in one wave, push, let CI run, then publish. No product or UX change
is requested; the approved design and the measured composition remain as reviewed.

---

# Final UI one-fix-wave re-review (scoped)

Range: `78a95fd..2c1c049` — `61ddfdf` (hook budget), `2c1c049` (evidence and status).
`78a95fd` is the parent and is **excluded** (CI-reviewer scope: runner evidence retention
and container-cleanup ownership, a separate review). Package:
`.superpowers/sdd/2026-11-26-desktop-workspaces/review-78a95fd..2c1c049.diff`, which I
verified is byte-equivalent to `git diff 78a95fd..2c1c049` apart from its own header and
wider context.

Read-only: no tests, typecheck, build, browser, Docker, server or subagent was run; no
stage, commit or push. Only this file was appended. `docs/ci-optimization-review.md`
(another reviewer, untracked) was not touched.

Working tree at review time: `e2e/lib/run-cleanup.sh`, `e2e/test-run-cleanup.sh` and
`scripts/lib/ci-contract.sh` show as modified. They are the parallel CI reviewer's
uncommitted work on the runner cleanup and CI-contract helpers (the `78a95fd` follow-up),
not mine and not part of this slice; I did not modify or stage them, and the set may grow
while that review is still in progress. Nothing in the desktop workspace range depends on
them, and `git diff 78a95fd..2c1c049` remains the four files above.

## Scope of the wave

`git diff --stat 78a95fd..2c1c049` = 4 files, 93 insertions / 45 deletions:
`frontend/e2e/desktop-workspaces.spec.ts` (+7, the hook budget), and the three docs
(`docs/desktop-workspaces-verification.md`, `docs/frontend.md`,
`docs/implementation-plan.md`). No `frontend/src`, no Go, SQL, migration, dependency,
workflow or `deploy/` change.

`git log 07b0be8..HEAD -- frontend/src` is **empty**, so no UI source changed since the
reviewed slice; the approved composition, thresholds and editor behaviour are untouched.
Nothing in this wave adds scope or re-opens an approved decision.

## F-1..F-7 status

| ID | Status | Evidence |
|---|---|---|
| F-1 hook budget | **ADDRESSED** | `desktop-workspaces.spec.ts:358-365` — `test.setTimeout(240_000)` is the first statement of `beforeAll`, before the credentials guard. Per-test 90s in `playwright.config.ts:9` unchanged; `workers: 1` unchanged; the 61s limiter wait and its comment unchanged |
| F-2 `776` row | **ADDRESSED** | `docs/desktop-workspaces-verification.md:61-74` — the old `776 / 700` two-column row is gone; the replacement is a single `module tasks at 200% zoom` row with 700 width, 6 cards and 1 × 6. No `776` remains in that table (`grep 776` now matches only the honest RED table at `task-3-report.md:89`, which stays) |
| F-3 catalog totals | **ADDRESSED** | `verification:36,58-74,106-113`; `task-3-report.md:240-250,339-341`; `implementation-plan.md:1127-1130` — all now state the run's own 3 then 6 **plus** the root suite's 1, i.e. a real total of **4 then 7**, with rows derived from the API count |
| F-4 test counts | **ADDRESSED** | `task-3-report.md:15-17` — "9 new tests plus hardening of the existing 12 … for 21 tests in the file" |
| F-5 skip attribution | **ADDRESSED** | `task-3-report.md:135-141` — "the chromium project skips the 5 phone-only tests, and the phone project skips the 16 chromium-only tests" |
| F-6 drawer breakpoint | **ADDRESSED** | `docs/frontend.md:283-284` and `:319-321` — drawer under **768 px** (Tailwind `md`), 44 px controls at **760 px and below**; the two breakpoints are now stated as distinct |
| F-7 stale status | **ADDRESSED** | `verification:3-5`, `implementation-plan.md:1004-1008`, `task-3-report.md:3-5,344-347` — "reviewed and approved for the product; publication pending a green four-job CI run", GHCR `latest` still `0da549e` |

No F item is open. No new Critical or Important item was introduced by the wave.

### F-1 verified, not assumed

- The budget is hook-scoped, proven by a retained probe artifact:
  `/tmp/opencode/desktop-workspaces/hooktimeout/inherit.spec.ts` with a 2s project timeout
  and a 20s hook budget, whose body failed with `Test timeout of 2000ms exceeded`
  (`test-results/.../error-context.md`). So the 90s per-test default still applies to
  every individual test.
- The arithmetic in the code comment and the doc is right: 2 roles × (61s wait + 8s
  detection) = 138s ≈ 140s, and 240s leaves ~100s for the fixture seed. No test duration
  in the recorded run comes near 90s (slowest `desktop-workspaces` tests 2.1s / 3.1s).
- The limiter was not weakened, no wait was removed, and no retry or skip was added.

### F-3 verified against the run, not against prose

`/tmp/opencode/desktop-workspaces/catalog-counts.txt` is the sampler's own output:
`0` at 0s, `1` at 9s, `1` through 103s, `7` at 105s and every sample after (trailing `?`
entries are the backend being torn down, which the sampler handles by design). So the doc's
"1 at 9s, 7 at 105s" is literal, stage 1's total of 4 is stated as arithmetic on measured
inputs, and the report says plainly that the sampler missed that window. `sample-catalog.sh`
issues `GET` only. Nothing fabricates a stage-1 observation.

Cross-check of the new card table against the committed assertions: stage 1, 4 records →
3 columns / `ceil(4/3)=2` rows at 1552, 2 / 2 at 1180 and 764; stage 2, 7 records → 3 / 3
at 1552 and 1200, 2 / `ceil(7/2)=4` at 1199. `desktop-workspaces.spec.ts` derives exactly
those rows from the real total, so the table and the test agree.

### Run evidence re-read (not re-executed)

`/tmp/opencode/desktop-workspaces/final-fix-run.log` — root suite `5 passed (17.9s)`, then
`69 passed / 21 skipped (2.9m)`, `EXIT=0`; the catalog test passes in chromium and is
skip-gated only in phone; 39 captures exist in `final-fix-shots/`, 20 in `final/`. This
matches `verification:83-90` and the report's `:120-134` line for line. The 403 unit tests,
typecheck and build in the doc are the accepted figures and were not rerun here.

## Observations (no action required)

- The F-1 code comment and the doc quote a 3s project timeout for two probes, while the
  retained `hooktimeout/playwright.config.ts` reads `timeout: 2_000` (the value the
  surviving `inherit` probe used). Only that artifact survives; the `extend`/`shrink`
  results were overwritten by later probe runs. The conclusion is unaffected — the
  surviving artifact alone proves the hook budget does not leak into tests, and the
  `beforeAll` semantics are documented Playwright behaviour.
- The spec's test title still reads "three- and six-template grids". That is accurate for
  the run's **own** records and the assertion compares against the real total, so it is
  not a misclaim; renaming it would be cosmetic.
- The accepted `admin-templates-1920-6.png` caption changed from "six-template catalog" to
  "7 rendered cards". That is arithmetic (root's 1 + the run's 6) corroborated by the
  sampler's `7` for the stage-2 window; the image set itself was not re-inspected, and no
  new visual review is needed because no UI source changed.
- No doc now claims the pipeline is already green or that anything is published. The only
  forward-looking statements are "pending a green four-job CI run" and the unchanged
  `0da549e` GHCR baseline.

## Release gate (finite, unchanged)

1. **Commit the review file with the wave.** `docs/desktop-workspaces-verification.md:4`,
   `docs/implementation-plan.md:1006` and `task-3-report.md:4` now reference
   `docs/desktop-workspaces-review.md`, which is currently untracked. If it is not added
   in the same commit, those references dangle.
2. **Push and run the existing four-job CI.** No new job, matrix or secret. Confirm the
   default-profile step runs `desktop-workspaces.spec.ts` (root `5 passed`, frontend
   `69 passed / 21 project skips`) and that unit/typecheck/build stay green.
3. **Publish only after green**, then flip the status lines from "publication pending CI"
   to the published state with the run URL and image digests. Until then GHCR `latest`
   remains the `0da549e` baseline.
4. **No new screenshot round** and no further UI change: the wave touched one test hook and
   three docs.

## Final verdict

**F-1 through F-7: ADDRESSED. No open Critical or Important finding.**

**APPROVED — final for this gate.** The desktop workspaces and direct authoring slice,
including the one-fix wave at `2c1c049`, is approved for publication once the four-job CI
run is green. The product verdict from the whole-slice review stands unchanged: no new UX,
no backend/API/schema/deployment/dependency change, and the approved composition is
untouched.
