# Status colours verification (2026-11-25)

Scope: accessible, semantic colours for frontend status badges only. No API, backend, dependency, layout,
or dark-theme changes. Local and unpushed pending review.

## What changed

- `frontend/src/shared/lib/status.ts` — single status → `{ label, tone }` mapping used by badges.
  Tones: `success`, `active`, `attention`, `error`, `restricted`, `neutral`, `muted`. Unknown or
  unlisted statuses fall back to `neutral` and keep a humanised raw label.
- `frontend/src/style.css` — `--status-<tone>-ink/surface/border` tokens added to `:root`; the existing
  Rosé Pine surfaces, typography and radii are untouched.
- `frontend/src/components/layout/StatusBadge.vue` — renders a tinted badge with a decorative
  `aria-hidden` icon and an always-visible label. No pulse or shape-only encoding. Legacy tone names
  (`ok`, `look`, `blocked`, `broken`, `unknown`, `off`, `running`, `gone`) still work.
- `frontend/src/components/layout/StatusPill.vue` — delegates to the shared mapping; its local table is gone.
- Call sites with inline `status === '…' ? 'ok' : …` ternaries (admin instances/plugins/integrations,
  AD, pentest, instance panel, file uploader) now pass `:status` and use the shared mapping. This is the
  concrete fix for `EXPIRED`/`CANCELED` previously rendering as running.
- Duplicate/incorrect non-badge markers found during review: `TaskCard.vue` dropped its second pine
  "Solved" (the green pill already says it) and `TeamPanel.vue`'s category badge moved from the health
  colour `ok` to `neutral`. `RoleBindingsPanel.vue`'s pine binding badges are a recorded follow-up.
- `frontend/fixtures/status-colors/` — test-only visual fixture (not a Vite build input, typechecked via
  `tsconfig.app.json`) used by the committed browser harness below.

## Mapping

| Tone | Colour | Statuses |
| --- | --- | --- |
| success | green `#9ccf8f` | ACCEPTED, SOLVED, PASSED, PATCHED, READY, ATTACHED, CONNECTED, ENABLED, HEALTHY, DELIVERED |
| active | cyan `#9ccfd8` | ACTIVE, RUNNING, CHECKING, VALIDATING, PROVISIONING, RESETTING, DESTROYING, AVAILABLE, OPEN |
| attention | amber `#f6c177` | PENDING, PENDING_REVIEW, SCHEDULED, UPCOMING, QUEUED, AWAITING_UPLOAD, FOUND |
| error | rose `#eb6f92` | REJECTED, ERROR, FAILED, UNHEALTHY, DEAD |
| restricted | lavender `#c4a7e7` | LOCKED, RESTRICTED, BLOCKED, INCOMPATIBLE |
| neutral | gray `#a6a1bd` | DRAFT, UNKNOWN, DISCOVERED, INSTALLED, NOT_CREATED, anything unlisted |
| muted | gray `#9a95b2` | FINISHED, ARCHIVED, EXPIRED, CANCELED/CANCELLED, CONSUMED, DISABLED, STOPPED, DESTROYED |

Labels are explicit (`PENDING_REVIEW` → "Needs review", `NOT_CREATED` → "Not started") and lowercase
API values are normalised (`enabled` → "Enabled"). Icons per tone: check, dot, clock, alert, lock,
circle, slash — all `aria-hidden`, always next to the text.

## Accessibility evidence

Contrast of each ink against its own tinted surface, computed from the committed token values
(`frontend/src/style.css`):

```
success     ink #9ccf8f surface #292d31 contrast 7.75:1 PASS
active      ink #9ccfd8 surface #292d3a contrast 8.06:1 PASS
attention   ink #f6c177 surface #342b2e contrast 8.37:1 PASS
error       ink #eb6f92 surface #322231 contrast 5.13:1 PASS
restricted  ink #c4a7e7 surface #2e283b contrast 6.76:1 PASS
neutral     ink #a6a1bd surface #2a2836 contrast 5.81:1 PASS
muted       ink #9a95b2 surface #282635 contrast 5.16:1 PASS
```

All seven exceed the 4.5:1 normal-text threshold; the lowest is error at 5.13:1. Colour is never the
only cue: every badge renders its label as text, the icon is decorative, and the former
`animate-pulse` running dot was removed (`grep animate-pulse frontend/src` only matches the unrelated
loading `Skeleton.vue`).

## Regression tests added first

- `frontend/src/shared/lib/status.test.ts` — normalisation, every tone group, unknown → neutral,
  humanised labels, and explicit assertions that `EXPIRED`/`CANCELED` are not `active`.
- `frontend/src/components/layout/StatusBadge.test.ts` — visible label, `aria-hidden` icon, all legacy
  tone aliases, neutral default, status-derived label/tone, tokenised ink/surface/border with no raw
  hex, no pulse/animation for running, and (after review M6) `tone`-over-`status` and
  `label`-over-`status` precedence plus a neutral fallback for an unexpected runtime tone.
- `frontend/src/components/layout/StatusPill.test.ts` — friendly labels, semantic tones, expired/
  canceled/finished muted, unknown status neutral and readable.

The three new files failed before `status.ts`/the new badge existed (10 failed, 1 passed) and pass after.

## Real-browser visual verification (Chromium)

Harness (committed, test-only): `e2e/run-status-colors-visual.sh` starts exactly one Vite dev server
through `e2e/lib/service-lifecycle.sh` (process-group leader + recorded `/proc` start time) and runs
`e2e/status-colors-visual.mjs` with Playwright's bundled Chromium driver against the system browser
(`/usr/bin/chromium`). The page is `frontend/fixtures/status-colors/index.html`, which mounts the real
`StatusBadge`/`StatusPill` with the real `src/style.css` — no router, no API, no backend, no database,
no Docker. It is component visual verification, not API acceptance. `fixtures/**/*.ts` is added to
`tsconfig.app.json` so the fixture is typechecked and cannot silently rot; it is not a Vite build input
and `dist/` contains no fixture reference.

```
OUT=/tmp/opencode/status-colors ./e2e/run-status-colors-visual.sh
  # vite owned pid: 619684 starttime: 7194835
  # success n=10 min=7.75:1   active n=10 min=8.06:1   attention n=10 min=8.37:1
  # error   n=10 min=5.13:1   restricted n=7 min=6.76:1  neutral n=8 min=5.81:1
  # muted   n=10 min=5.16:1
  # badges measured: 65 (desktop + 375px)
  # PASS: all computed-style and contrast assertions held
  # owned vite group 619684 stopped (no survivors)
```

Measured from **computed styles in the browser**, not from the CSS source: 65 badge instances across
three surfaces (black page, `bg-secondary` card, table row), covering all seven tones, 19 API statuses,
and all eight legacy `tone` props. Every instance asserted: `color`/`backgroundColor`/`borderColor`
equal their `--status-*` token values, border is solid and ≥1 px (so the shadcn CVA
`border-transparent`/`text-foreground` utilities do not win), text contrast ≥4.5:1, a non-empty visible
label, exactly one icon with `aria-hidden="true"` and ≥8 px width, `animation-name: none` with zero
animated descendants, and a badge height ≥18 px. Unknown `PROVIDER_TIMEOUT` rendered "Provider timeout"
in the neutral palette; `EXPIRED`, `CANCELED` and `FINISHED` rendered muted, never active.

Screenshots (`/tmp/opencode/status-colors/`): `desktop-1440.png` (1440×900, DPR 1),
`desktop-1440-zoom200.png` (1440×1563 at 200 % CSS zoom), `mobile-375.png` (375×812 CSS, DPR 2 →
750×2128), plus `measurements.json` with all 65 computed records.

Independent pixel check of those PNGs (Pillow, counting exact token colours) confirms the tints are
actually rasterised rather than merely computed: every tone surface has 9 073–57 601 matching pixels and
every ink 251–4 564 across the three images.

Responsive/zoom assertions: no horizontal overflow at 1440 px, at 375 px, or at 200 % zoom; zero clipped
labels (`scrollWidth > clientWidth`) and zero off-screen badges at 375 px; label width > 0 at 200 % zoom.

Process cleanup evidence: the owned group's PGID equals the recorded PID, `services_stop_all` TERM/KILLs
only that group, and the script asserts the group is gone before exit. After the run
`ps -eo pid,pgid,cmd | grep -E "vite|bun run dev|chromium"` returns nothing and the run's port is closed.
One earlier harness version wrapped `service_start` in `$(...)`, which made the command-substitution
subshell the group leader and left three dev servers behind; those exact groups (616633, 618351, 618945)
were terminated after inspecting their members, the bug is fixed in the committed script, and the final
run left no survivors.

## Review follow-ups applied (M3, M6, M8)

- **M3** — the `style.css` token comment now says the ratios are measured against each tone's own opaque
  surface (Chromium-measured), instead of the inaccurate "#191724 tints" wording.
- **M6** — precedence tests added (see above), and an unexpected runtime `tone` now falls back to
  `neutral` instead of producing an empty icon and `var(--status-undefined-*)`.
- **M8** — `TaskCard.vue` no longer prints a second pine "Solved" next to the green "Solved" pill
  (`status === 'SOLVED'` iff `solved`, `internal/boards/service_tasks.go:179`), so the card states it
  once. `TeamPanel.vue`'s category badge changed from `tone="ok"` (a health colour) to `tone="neutral"`.
  `RoleBindingsPanel.vue`'s pine active/expired binding badges are left as recorded follow-up, out of
  scope for this slice.

## Commands actually run (serial, low-memory)

```
cd frontend
NODE_OPTIONS=--max-old-space-size=2048 bun run typecheck   # exit 0, no diagnostics
NODE_OPTIONS=--max-old-space-size=2048 bun run test -- --maxWorkers=1
  # Test Files  33 passed (33)
  # Tests       174 passed (174)
NODE_OPTIONS=--max-old-space-size=2048 bun run build       # ✓ built in 820ms, no fixture in dist
```

The first slice ran 171 tests; the three added precedence/fallback tests make 174. The full unit run
prints `ECONNREFUSED 127.0.0.1:3000` lines on stderr; no test failed, and they are not emitted by the
status tests.

## Limits

- The browser pass is a component visual fixture, not an end-to-end app or API acceptance run: no
  router, auth, or backend data is exercised, and no full-page app screenshot was taken.
- Screenshots were captured and pixel-checked programmatically; this session cannot render images for
  human inspection, so the visual judgement rests on computed styles, measured contrast, layout
  assertions and the pixel counts above.
- The tokens are fixed dark-theme values; there is no light theme to re-measure.
- Contrast is measured against the badge's own opaque surface. Against the page/card surfaces the same
  inks measure higher, so no surface makes a badge fail.
- No commit was pushed. Backend, database and Docker were not touched or run.
