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
  hex, and no pulse/animation for running.
- `frontend/src/components/layout/StatusPill.test.ts` — friendly labels, semantic tones, expired/
  canceled/finished muted, unknown status neutral and readable.

The three new files failed before `status.ts`/the new badge existed (10 failed, 1 passed) and pass after.

## Commands actually run (serial, low-memory)

```
cd frontend
NODE_OPTIONS=--max-old-space-size=2048 bun run typecheck   # exit 0, no diagnostics
NODE_OPTIONS=--max-old-space-size=2048 bun run test -- --maxWorkers=1
  # Test Files  33 passed (33)
  # Tests       171 passed (171)
NODE_OPTIONS=--max-old-space-size=2048 bun run build       # ✓ built in 919ms
```

The full unit run prints two `ECONNREFUSED 127.0.0.1:3000` lines on stderr; no test failed, and they are
not emitted by the new status tests.

## Limits

- Verification is unit/typecheck/build level; no browser screenshot pass was run for this slice.
- The tokens are fixed dark-theme values; there is no light theme to re-measure.
- No commit was pushed. Backend and Docker were not touched or run.
