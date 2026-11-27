# UI emoji audit and back-link icons (2026-11-27)

Base: `062dc25` (published source `bc266be`). Scope: app-owned UI only. No API, RBAC, migration,
sanitizer, dependency or configuration change.

## Finding: no app-owned emoji

A Unicode scan of `frontend/src`, `frontend/index.html`, `frontend/public`, `frontend/e2e`,
`backend`, `internal`, `cmd`, `migrations`, `email_templates`, `api`, `deploy`,
`events-dispatcher`, `examples`, `scripts` (Extended_Pictographic, VS16, ZWJ, keycaps, regional
indicators, plus ✓ ✔ ✗ ✖ ⚠ 🔒 🚩 ➕ ⏳ 📋 ‹ › ») found **zero emoji in rendered application UI**.
Full table: `/tmp/opencode/ui-icons/inventory.md`.

What the scan did find, and why it stays:

- 🎯 in `text.test.ts`, `MarkdownEditor.test.ts` and the `desktop-workspaces` / `markdown-render`
  e2e specs — deliberate Unicode fixtures and user-authored Markdown. Test data, not UI.
- `·`, `…`, `—`, `–`, `“ ”` — typography separators and prose.
- `→` in date ranges (`start → end`), diff versions (`v1 → v2`), repository values and the prose
  `Admin → Users` — rendered values/text, not icon affordances.
- `×` in `TaskEditorDialog` (`+30m × 3 extensions.`) — arithmetic in a sentence.
- `⌘` in the `Link (Ctrl/⌘ K)` tooltip — a keyboard-key legend, not a decorative icon.
- ✓/❯ in `aCTFb_CODEX_TASK_v4.md` and `.superpowers/` reports — specification/agent prose.

Backend default copy contains no emoji or escaped forms, so no default-copy removal was needed.
Existing Lucide icons (`StatusBadge`, selects, dialogs, editor toolbar, solved alert) were already
inline SVGs and were not touched.

## Change: 7 text chevrons became Lucide icons

The only app-owned decorative glyph affordance was the `‹` back-link/breadcrumb prefix. Each was
replaced with `<ChevronLeftIcon aria-hidden="true" class="size-3.5 shrink-0" />` inside an
`inline-flex items-center gap-1` link, keeping route, link text, colour classes and DOM order:

- `frontend/src/features/boards/pages/ModulePage.vue`
- `frontend/src/features/boards/pages/ScoreboardPage.vue`
- `frontend/src/features/boards/pages/BoardStaffPage.vue`
- `frontend/src/features/pentest/pages/PentestPage.vue`
- `frontend/src/features/attack-defense/pages/ADModulePage.vue`
- `frontend/src/features/admin/pages/AdminBoardPage.vue`
- `frontend/src/features/admin/pages/AdminTemplateEditorPage.vue`

The SVG uses Lucide defaults (`fill="none" stroke="currentColor"`), inherits
`text-muted-foreground`, is 14×14 px in a 20 px line box, adds no tab stop and no motion.
Accessible link text is the same human label as before (`Board`, `Admin boards`, `Templates`).
`@lucide/vue` was already a dependency; no icon library, font or CDN was added.

## Verification

- RED first: `ModulePage.test.ts` asserted an `svg` inside the back link before the change and
  failed (`expected false to be true`).
- `bunx vitest run --maxWorkers=1`: **421 passed / 45 files** (419 before + 2 new tests:
  `ModulePage.test.ts` staff-preview back link, `AdminBoardPage.test.ts` admin back link; both assert an
  SVG icon, unchanged link text and no literal `‹`).
- `bunx vue-tsc -b`: clean. `bunx vite build`: success (pre-existing >500 kB chunk warning only).
  Built chunks contain `chevron-left` and no `‹`.
- Browser sample of the real `ModulePage` (throwaway Vite fixture, deleted before commit):
  desktop 1440×900 and phone 390×844 both render `svg.lucide-chevron-left`, `aria-hidden="true"`,
  `fill="none"`, `stroke="currentColor"`, 14×14 px, link text `Board`, no `‹` on the page.
  Screenshots: `/tmp/opencode/ui-icons/back-link-{desktop,phone}.png`.

Not run locally: the 18-spec Docker gate and a second backend stack — the change is a pure inline SVG
swap, and the unit DOM plus browser geometry above cover it.

## Publication

**Published and verified from `main` at `3ac89c0`** (source `f10fbf5` + the review-artifact commit).
Workflow run https://github.com/nrf24l01/aCTFb/actions/runs/45982934703 — success 4/4
(Backend quality, Frontend quality, Integration, Publish images), 10:32:18Z → 10:57:49Z.

CI counts from the run logs, not from local runs:

- Frontend quality: `45 passed (45)` files, `421 passed (421)` tests, `bun run typecheck` clean,
  `bun run build` built in 1.07s.
- Backend quality: `go test ./... -count=1` — 50 packages `ok`, 13 `[no test files]`, 0 failures.
- Integration: `p1-core` 1 passed (36.3s); default-profile 5 passed (30.4s) and 69 passed (4.0m);
  full-stack 18 passed (5.1m); packaging contract 424 checks, CI-contract 14, cleanup-guard 21, all
  `0 failed`.
- All ten GHCR images resolved anonymously: `latest` digest equals `sha-3ac89c0…`, platform
  `linux/amd64`, `org.opencontainers.image.revision` = the full source SHA. Details and digests:
  `docs/ghcr-verification.md`.
- Published frontend bundle read from the published `sha-3ac89c0…` layers only (no application run):
  84 JS assets; nine chunks reference `chevron-left` (the seven changed pages plus the shared chunk and
  the entry bundle) and **no** asset contains a literal `‹`.

## Deliberately untouched

User-authored Markdown (rendered through the existing DOMPurify allowlist), rich-text content,
code blocks, security payload flags, template files, plugin iframe content, and persisted data.
No emoji filter, migration or sanitizer change was introduced.
