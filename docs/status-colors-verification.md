# Status colours verification (2026-11-25)

Scope: accessible, semantic colours for frontend status badges only. No API, backend, dependency, layout,
or dark-theme changes.

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

### Final-review nits (tone lookup, harness coverage, hygiene)

- **Tone lookup uses `Object.hasOwn`.** A regression test for `toString`/`constructor`/`hasOwnProperty`
  found a real hole: the plain-object legacy alias table resolved inherited keys, so `tone="toString"`
  produced a function-shaped tone and an empty `style` attribute. Both lookups (legacy aliases and tone
  icons) now use `Object.hasOwn`, and the test asserts those inherited names render the neutral palette.
- **Harness asserts every expected key is measured.** Besides the `>= 60` floor, the browser run now
  requires each of the 27 `EXPECTED_TONE` keys (19 API statuses + 8 legacy aliases) and every
  `EXPECTED_LABEL` case to appear in the measured set, so a dropped or renamed fixture badge fails
  instead of quietly shrinking the sample.
- **Hygiene** — `frontend/tsconfig.app.json` ends with a newline again, and the legacy tone comment now
  states explicitly that the old names are retained only for compatibility with existing call sites.
- Reviewer artifact `docs/status-colors-review.md` is preserved verbatim and committed unchanged.

## Commands actually run (serial, low-memory)

```
cd frontend
NODE_OPTIONS=--max-old-space-size=2048 bun run typecheck   # exit 0, no diagnostics
NODE_OPTIONS=--max-old-space-size=2048 bun run test -- --maxWorkers=1
  # Test Files  33 passed (33)
  # Tests       175 passed (175)
NODE_OPTIONS=--max-old-space-size=2048 bun run build       # ✓ built in 986ms, no fixture in dist
```

The first slice ran 171 tests; the precedence/fallback tests and the inherited-key regression make 175.
The full unit run prints `ECONNREFUSED 127.0.0.1:3000` lines on stderr; no test failed, and they are not
emitted by the status tests.

## Limits

- The browser pass is a component visual fixture, not an end-to-end app or API acceptance run: no
  router, auth, or backend data is exercised, and no full-page app screenshot was taken.
- Screenshots were captured and pixel-checked programmatically; this session cannot render images for
  human inspection, so the visual judgement rests on computed styles, measured contrast, layout
  assertions and the pixel counts above.
- The tokens are fixed dark-theme values; there is no light theme to re-measure.
- Contrast is measured against the badge's own opaque surface. Against the page/card surfaces the same
  inks measure higher, so no surface makes a badge fail.
- This slice is frontend-only; no local backend test suite was run. The existing CI jobs remain the
  authoritative gate for the whole repository.
- Nothing was pulled or run locally as a container: this environment has no Docker daemon, so image
  behaviour rests on the CI publish job and the anonymous registry verification below.

## Publication (DONE)

Source commit `332fdcb422e9eb35d155c07dd28aee270445f00f` on `main`, pushed as a normal fast-forward
(`bc13047..332fdcb`; `origin/main` was an ancestor, 0 behind / 6 ahead, no force/reset/rebase).

| Field | Value |
| --- | --- |
| Workflow run | https://github.com/nrf24l01/aCTFb/actions/runs/45644672697 |
| Commit | `332fdcb422e9eb35d155c07dd28aee270445f00f` |
| Conclusion | success — Backend quality, Frontend quality, Integration, Publish images |
| Immutable tag | `sha-332fdcb422e9eb35d155c07dd28aee270445f00f` |
| Platform | `linux/amd64` only (`os`/`architecture` in each image config) |

Job timings from the run: Backend quality 15:23:47→15:27:45Z, Frontend quality 15:23:47→15:24:18Z,
Integration 15:27:47→15:40:12Z, Publish images 15:40:14→15:41:19Z (all `success`; `gh run watch
--exit-status` exited 0).

Anonymous registry verification (no credentials, no Docker daemon; token + manifest + config-blob reads):
all ten images are publicly readable, `latest` and `sha-332fdcb…` resolve to the same manifest digest,
each config reports `linux/amd64`, and every `org.opencontainers.image.revision` label equals the source
commit above.

| Image | Digest | Compressed |
| --- | --- | --- |
| `ghcr.io/nrf24l01/actfb-backend` | `sha256:1fd48237595afee34c9abca42ad8b4651f4e57b5071d225ad61883c10e79cb9b` | 20,448,093 B |
| `ghcr.io/nrf24l01/actfb-notification` | `sha256:30c1699e84439d8f4d03d269e33f5ba72ad14675d04496d989f3c1b904875a72` | 12,342,732 B |
| `ghcr.io/nrf24l01/actfb-retention` | `sha256:9db9952957b86ac8bc708006f37726b2022ef0196ec1344e367a7ced6687c5de` | 10,940,563 B |
| `ghcr.io/nrf24l01/actfb-events-dispatcher` | `sha256:a2b5a887d8b8c9fd21fd94958a4edfdbf2b3aa877fa856b35238e3b9898a303e` | 12,001,243 B |
| `ghcr.io/nrf24l01/actfb-file-scanner` | `sha256:ff3857b1ec0896c50c33e94805eaa30e51c7eedeab01c94812bf9d0a896f8976` | 13,359,289 B |
| `ghcr.io/nrf24l01/actfb-storage-init` | `sha256:67e92043f6572ad122be24c6cfb3525ba2a0b3cb5b93829b5743d317a732044e` | 10,956,257 B |
| `ghcr.io/nrf24l01/actfb-plugin-runner` | `sha256:d9c3b732fc765d1f104f694e087b893d4310d695df39987219332aa9a22f88fd` | 10,292,879 B |
| `ghcr.io/nrf24l01/actfb-ad-worker` | `sha256:ecfb21e8c01ad5349a29a8452ec1cfe92512891b1c130b51536a587d8f0ade81` | 27,023,831 B |
| `ghcr.io/nrf24l01/actfb-instance-worker` | `sha256:bf6ead91a190e8ace25b65dcecdbd7a93a4302d06ac01402466705f465dafd2a` | 13,932,157 B |
| `ghcr.io/nrf24l01/actfb-frontend` | `sha256:7f60a206a6adfed105294e69edf43a99f90a6d1dfae47e432deecceaa5a913f1` | 23,690,231 B |

The published frontend image really carries this change: its 613,298 B SPA-asset layer contains
`--status-success-ink` and three `PENDING_REVIEW` occurrences, i.e. the built bundle ships the new
tokens and the shared mapping, not just a matching revision label.

Deployment for this UI-only change is the standard `docker compose pull && docker compose up -d`
in `deploy/`; no migration, config or `.env` edit is required (the frontend is a static bundle and no
`/api/v2` contract changed).
