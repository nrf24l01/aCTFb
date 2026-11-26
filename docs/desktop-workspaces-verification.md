# Desktop workspaces verification (2026-11-26)

Status: **implemented, pending independent review and main screenshot inspection**. Nothing is
published. This file records what was actually executed, the numbers the browser measured, and what
the evidence does *not* prove.

- Feature spec: `docs/superpowers/specs/2026-11-26-desktop-workspaces-design.md` (approved).
- Slice reports: `task-1-report.md`, `task-2-report.md`, `task-3-report.md` under
  `.superpowers/sdd/2026-11-26-desktop-workspaces/`.
- Source base for this task: `21b14fe` (Task 1 and Task 2 approved). Frontend, e2e and docs only; no
  backend, database, migration, API, dependency, deployment or provider change.

## What the browser acceptance covers

`frontend/e2e/desktop-workspaces.spec.ts` runs in the real Chromium `chromium` and 375px `phone`
projects against a real Go backend with the **default feature profile**
(`deployment.enabled=false`, integrations/plugins/files/A+D off, no credential key) and the real
Vite app. Fixtures are created through the documented HTTP API and the product UI; nothing is
intercepted and no SQL is used for fixtures.

Per project there are 21 tests: 16 run in `chromium`, 5 in `phone`. The remaining 5/16 are
**project-specific skips** (`test.skip(project !== ...)`), because desktop dialog geometry cannot be
measured at 375px and the phone drawer cannot be measured at 1440px. No CRUD, catalog, editor or
responsive assertion is skip-gated when `ACTFB_E2E=1`.

| Area | Assertion |
|---|---|
| Page cap | 1600px outer / 1552 usable at 1920, 1180 at 1440, 764 at 1024; profile form stays bounded at 1104 |
| Dialog caps | board/module 1200px, task 1360px, 24px viewport clearance, one scrolling body, header/footer outside it |
| Authoring columns | two columns at ≥1040px body width with a 320px rail and a 24px gap; stacked below |
| Workspace split | `minmax(0,1fr) 320px` (360px for the task reader) at ≥1100px usable; stacked below |
| Card tracks | board modules 2 columns at 760px; module tasks and admin hub/templates 2 at 720px and 3 at 1200px; 1 column on the phone |
| Threshold neighbours | 1099/1100 usable, 719/720, 759/760, 1199/1200 |
| Catalog | real templates created through the authoring API, 3 then 6 on a fresh database, measured in the browser |
| Zoom | 200% CSS-zoom reflow and the effective 960x540 CSS layout of a real 200% browser zoom |
| Authoring | no outer Write/Preview tabs on all four surfaces, Visual/Source functional, exact Unicode bytes, rune counting, one save, undo across resizes |
| Regressions | no duplicate editor, no extra request on resize, no orphan focus target, no own-history request in organizer preview |

## Measured geometry (real DOM, ±1px committed assertions)

### Widths

| Viewport | Usable page | Main | Rail | Result |
|---|---|---|---|---|
| 1920 | 1552 | 1208 (board/admin) | 320 | split |
| 1920 | 1552 | 1168 (task) | 360 | split |
| 1440 | 1180 | 836 / 796 | 320 / 360 | split |
| 1024 | 764 | 764 | stacked | full-width description |
| 1360 | 1100 | 716 (task) | 360 | split — first width that splits |
| 1359 | 1099 | 1099 | stacked | last width that stacks |
| 960 (200% of 1920) | 700 | 700 | stacked | reflow, not shrink |
| 375 | 343 | 343 | stacked | one column |

### Card tracks

| Collection | Own width | Columns |
|---|---|---|
| board modules (4 cards) | 760 | 2 (2 rows) |
| board modules | 759 | 1 (4 rows) |
| module tasks (6 cards) | 1200 | 3 (2 rows) |
| module tasks | 1199 | 2 (3 rows) |
| module tasks | 776 / 700 | 2 / 1 |
| admin hub / template catalog | 1200 | 3 |
| admin hub / template catalog | 1199 | 2 |
| admin hub / template catalog | 720 / 719 | 2 / 1 |
| any collection at 375 | 343 | 1 |

## Commands actually run

```bash
# unit, typecheck, build
cd frontend && bun run test:unit -- --maxWorkers=1     # 43 files / 403 tests passed
bun run typecheck                                       # clean
bun run build                                           # clean, chunk sizes below

# result of the owned-stack run below
#   5 passed (17.1s)        root default-profile suite, all 5 ran
#  21 skipped / 69 passed   markdown-render + rich-text-editor + desktop-workspaces
#  EXIT=0

# one owned cold stack, the real CI runner, everything in one invocation
E2E_PG_DSN=postgres://postgres@127.0.0.1:<owned-port>/postgres?sslmode=disable \
E2E_WORKDIR=/tmp/opencode/desktop-workspaces/final-run \
E2E_SCREENSHOT_DIR=/tmp/opencode/desktop-workspaces/final-all \
./e2e/run-default-profile-e2e.sh
```

The runner creates its own fixture database, builds the real backend, boots it with every optional
feature off, starts Vite, bootstraps one admin and two participants, checks the empty catalog answers
`200`, then runs `e2e/tests/default-profile.spec.ts` followed by `markdown-render.spec.ts`,
`rich-text-editor.spec.ts` and `desktop-workspaces.spec.ts` on that same stack. No workflow file was
changed: the existing four CI jobs already call this script.

## Bundle behaviour

`bun run build` keeps the previous chunk split: `MarkdownEditor-*.js` 503.77 kB / 159.06 kB gzip,
reader `markdown-*.js` 154.50 kB / 51.39 kB gzip. Public readers still do not load the editor chunk,
which `rich-text-editor.spec.ts` asserts by watching the network for `MarkdownEditor.vue`.

## Screenshots

`/tmp/opencode/desktop-workspaces/final/` holds 20 screenshots selected for the main assistant's
inspection (real app, never a fixture): admin board 1920/1440/375, the real six-template catalog at
1920, task participant 1920/1024/375/200%-zoom, organizer preview and board dialog at the effective
960px layout, module tasks 1920/1440/375, participant board 1920/375, site form 1920/375 and the task
editor at 1920/1440/375. The full run produced 39 captures in
`/tmp/opencode/desktop-workspaces/final-all/`. See `task-3-report.md` for the exact list and why each
one exists.

## Limitations, stated honestly

- The INSTANCE **runtime** binding cannot be exercised on the default profile: with
  `deployment.enabled=false` the approved-template endpoint is unregistered (`404`), which the spec
  asserts. The long INSTANCE form layout is therefore proved by a labelled component fixture
  (`TaskEditorDialog.instanceLayout.test.ts`) and is **not** provider execution. The template
  *catalog* is a separate switch and is always served; it is measured with real templates.
- CSS `zoom` is a layout surrogate for browser zoom, not OS zoom: it changes the effective CSS width
  for container queries but does not scale `vw` units. The `vw`-based dialog cap is covered by the
  separate Chromium device-metrics test (960x540 CSS px, devicePixelRatio 2), which reflows real
  media and container queries. Neither test claims to reproduce an operating-system zoom.
- The phone project is Chromium at 375x812 with `isMobile`/`hasTouch`, not WebKit or a real device;
  the CI image installs Chromium only.
- CI is the authority for the Go suites; this task ran only the frontend, browser and owned-stack
  parts locally, and did not push, trigger or cancel any workflow run.
- The admin hub card count depends on the seeded admin's roles, so the tests derive rows from the
  rendered card count instead of hard-coding a total.
