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
- `bunx vitest run --maxWorkers=1`: **421 passed / 45 files** (419 before + 2 new assertions:
  `ModulePage.test.ts` student back link, `AdminBoardPage.test.ts` admin back link; both assert an
  SVG icon, unchanged link text and no literal `‹`).
- `bunx vue-tsc -b`: clean. `bunx vite build`: success (pre-existing >500 kB chunk warning only).
  Built chunks contain `chevron-left` and no `‹`.
- Browser sample of the real `ModulePage` (throwaway Vite fixture, deleted before commit):
  desktop 1440×900 and phone 390×844 both render `svg.lucide-chevron-left`, `aria-hidden="true"`,
  `fill="none"`, `stroke="currentColor"`, 14×14 px, link text `Board`, no `‹` on the page.
  Screenshots: `/tmp/opencode/ui-icons/back-link-{desktop,phone}.png`.

Not run: the 18-spec Docker gate, e2e suites and any second backend stack — the change is a pure
inline SVG swap, and the unit DOM plus browser geometry above cover it. Nothing published.

## Deliberately untouched

User-authored Markdown (rendered through the existing DOMPurify allowlist), rich-text content,
code blocks, security payload flags, template files, plugin iframe content, and persisted data.
No emoji filter, migration or sanitizer change was introduced.
