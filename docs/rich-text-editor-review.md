# Rich-text Markdown editor — whole-slice independent review

Date: 2026-11-26
Scope: `81cdbaa..22f4fd4` (10 local commits, `main` untouched by this review)
Reviewers: independent whole-slice pass (spec + quality + security + UX), read-only on product code

## Verdict

**APPROVED.** No open Critical and no open Important finding.

C1/I1/I2/I3 (Task 3 round 1) and I4 (round 2) are closed in the shipped code, the four organizer
surfaces and every reader render the stored Markdown through the one sanitising renderer, the
visual editor never rewrites an untouched description, and the security boundary that the slice
depends on (one HTML producer, escaped raw HTML, token-level URL allowlist, strict DOMPurify
allowlist) is intact. The remaining items are minors, all triaged below with an explicit
disposition; none of them needs to change before publish.

This is not a rubber stamp: the findings below include the concrete failure modes I went looking
for and could **not** reproduce (entity-obfuscated `href` on the reader path, `type="submit"`
controls inside the site form, a paragraph that round-trips into a code block, `div`/`align`
allowlist widening), plus the one genuine user-danger vector I probed hardest — silent content loss
on a real edit — with the reasoning and the corpus that closes it.

## What was reviewed and how

Read in full: `review-81cdbaa..22f4fd4.diff` (8 571 lines), the approved spec
(`docs/superpowers/specs/2026-11-26-rich-text-editor-design.md`), the plan, `progress.md`, the
Task 1/2/3 briefs, reports and review reports (including all deferred items), and the current
working-tree source of every file the slice adds or changes.

Per the brief this was a read-only review: **no tests, typecheck, build, dev server or browser run
by me**, and no product edit. Gate results are therefore *accepted as reported*, not re-run:
391 unit tests / 42 files, clean typecheck, passing build, renderer spec 23/23 in `chromium` and
23/23 in `phone` (46 total), default profile 5 + frontend 48 on one native-PG/backend/Vite stack
(`/tmp/opencode/run-default-r2.log`), editor chunk 159.06 kB gzip lazy. What I did verify myself is
the *content* of those gates: I read the spec files, the unit suites and the CI wiring, and I
checked the built stylesheet where a claim depended on generated CSS.

`git status` is clean; no user work was touched, nothing was committed or pushed.

## Scope compliance

| Requirement | Status |
|---|---|
| Stored value stays Markdown; no new stored format | Met — `v-model` is the Markdown string; visual mode writes `getMarkdown()`, Source mode writes the exact textarea bytes, and `normalizeOutput` only trims trailing blank lines on the visual path |
| No `/api/v2`, migration, `deploy/`, `config.yaml` change | Met — `git diff --stat` is frontend + `e2e/run-default-profile-e2e.sh` + `docs/` only; `docs/swagger.yaml` untouched |
| Four authoring surfaces, layout and ids preserved | Met — `board-description`, `module-description`, `task-description`, `site-intro`; Write/Preview tabs, props, emits and payloads unchanged |
| Reader surfaces actually show formatting | Met — `BoardPage`/`ModulePage` through `renderMarkdown` in the existing `PageHeader` slot (`.markdown-body.markdown-body-inline`), `TaskPage`/`LandingPage`/site page and all three dialog Previews carry `.markdown-body` |
| Highlighting in editor and reader from one registry | Met — `highlight.ts` registry used by both `renderMarkdown` and `CodeBlockLowlight` |
| Exact pins, one cohort, one `marked` copy | Met — `@tiptap/*` 3.35.0, `@tiptap/extension-paragraph` 3.35.0 now a direct dependency, `lowlight` 3.3.0, `highlight.js` 11.11.2, `marked` 17.0.6 (single entry in `bun.lock`) |
| Lazy editor, public readers unaffected | Met — `MarkdownEditorLazy` (`defineAsyncComponent`) on all four surfaces; the authoring spec asserts no `MarkdownEditor.vue` request on the public landing page and one after opening a dialog |
| Browser gate executes in CI, never skips | Met — `e2e/run-default-profile-e2e.sh:302-311` sets `ACTFB_E2E=1`; `.github/workflows/go.yml:256` installs Chromium, `:271` runs the script; the `frontend` job runs typecheck + `vitest run` + build |

## Security review

The slice does not touch the backend, so this is the frontend trust boundary only.

**One HTML producer, one sanitiser.** `renderMarkdown` (`frontend/src/shared/lib/markdown.ts:154`)
is the only function whose output reaches `v-html`, and all eight `v-html` bindings in `src/` are
`renderMarkdown(...)` results (grep verified). The editor path never uses `v-html`; pasted HTML goes
through the ProseMirror schema.

**Layers, in order, and each holds:**

1. Raw HTML is escaped at token level (`walkTokens`, `:110-124`) before marked can emit it, so a
   `<script>`, `<img onerror>`, `<svg>` or `<a xlink:href>` payload is text, not markup.
2. URLs are filtered while tokenising with `isSafeUrl` (`:95-107`): explicit `http`/`https`/`mailto`
   only, plus same-origin relative forms. `javascript:`, `data:`, `vbscript:` and control-character
   obfuscation fail `new URL` parsing or the protocol set.
3. DOMPurify runs on the result with an explicit tag/attribute allowlist and `style` forbidden
   (`:24-92`). A private instance carries two hooks: `align` survives only on `th`/`td` with
   `left|center|right`; `class` survives only as `hljs-*` on `span`, `language-*` on `code`, and the
   one generated wrapper class on `div`.
4. Highlighting output is serialised by our own HAST walker (`highlight.ts:154-186`), which emits
   only `span` + `hljs-*` and escapes every text node; the sanitiser re-checks it anyway.
5. No auto-detection: `highlightAuto` is never called, and the editor adapter answers
   `registered()` truthy so the upstream plugin cannot fall back to it (`highlight.ts:192-214`).

**Specific attacks probed and closed:**

| Vector | Result |
|---|---|
| Entity-obfuscated scheme, e.g. `[x](&#106;avascript:alert(1))` | Two independent guards: `isSafeUrl` sees the decoded form on marked's token path (or the entity survives as a literal `&amp;#106;…` relative URL, which is not a scheme), and DOMPurify decodes attribute values before matching its URI allowlist. The compatibility gate decodes the same spellings (`markdownUnsupported.ts:117-133`) so such a link also routes to Source instead of being converted. |
| Control-character obfuscation (`java<TAB>script:`) | `new URL` strips C0/C1, so the protocol test rejects it; the prose scan (`:136-155`) catches the spelling marked refuses to tokenise as a link. |
| Crafted fence language (` ```python" onmouseover= `) | Not a registered id, not `SAFE_LANGUAGE_ID`, falls back to `plaintext`; renders as escaped text with no attributes. |
| Pasted styled HTML (`<b style onclick>`) | Schema-filtered to `strong`; `style`/`class`/handlers have no place in the schema. Proven by a real `ClipboardEvent` in Chromium and by `expect(stored).not.toContain('style=')`. |
| `data:`/`javascript:` image source | `allowBase64: false` plus `parseHTML`/`renderHTML` guards using the shared predicate (`markdownDocument.ts:43-76`); `getAttrs` returns `false`, so no node is created. |
| `align="evil"`, non-cell `align`, arbitrary `class`, `style` | Removed by the hook; proven by a direct `sanitizeMarkdownHtml` test in Chromium (happy-dom cannot exercise DOMPurify, which the docs state). |
| `div` added to the allowlist | Only our own table wrapper can produce a `div` (author HTML is escaped earlier), and the hook pins its class exactly. No new author-controlled attribute. |
| Code execution | No `eval`, `new Function`, `innerHTML`, `localStorage`, `sessionStorage`, `fetch` or `XMLHttpRequest` anywhere in the new `src/` code (grep verified). |
| Accidental form submission | `Toggle` and `SelectTrigger` from `reka-ui` 2.11.0 render `type="button"` when `as` is its default `button` (verified in `node_modules`), which matters because `AdminSitePage` puts the editor inside `<form @submit.prevent="save">`. `Enter` inside the contenteditable and in the link row are covered by real browser assertions (`getSubmits() === 0`). |

**Supply chain.** Every new dependency is an exact pin; the `@tiptap/*` cohort is one maintained
release (3.35.0), which is required because the peers pin exactly. `marked` resolves to a single
17.0.6 copy, which is the reason for the bump. No cloud editor, no runtime fetch, no telemetry.

## Data-safety review (the main risk of this slice)

**The gate.** `inspectMarkdownSupport` (`markdownUnsupported.ts:176-223`) lexes with the same GFM
configuration the renderer uses and walks the token tree by shape, so table cells (which have
`tokens` but no `type`) and nested arrays are inspected, while `code`/`codespan` payloads are never
mistaken for prose. It reports exactly the three constructs that the schema would silently drop:
`raw-html`, `task-list`, `unsafe-url`. A malformed numeric entity is treated as unsafe rather than
"no destination", and `decodeCodePoint` is bounded, so no `RangeError` on mount.

**The supported subset round-trips.** The corpus (`markdownDocument.test.ts:15-45`) covers every
construct in the approved table and asserts three independent things per item: document equality,
`renderMarkdown` equality before/after, and hand-written expected nodes (hard break, link title,
per-column table `align`, ordered `start`, unknown fence language with extra info). Escaped block
markers are preserved through a paragraph-renderer shim (`markdownDocument.ts:86-167`) that
reproduces the upstream renderer exactly and then escapes line-leading `#`/`>`/`-`/`+`/`n.`/`|`/
`---`; fence and inline-code bodies are asserted byte-identical, so code is never touched.

**Silent loss — what I looked for and why it is closed.** The dangerous direction is content that is
*visible in the reader* but *dropped by the schema* without the gate noticing. I worked through the
candidates: raw HTML at block, inline, heading, list and cell level (flagged); HTML inside a fence
(correctly not flagged); GFM task lists at any depth and inside a blockquote (flagged); task-list
text inside a table cell (literal, preserved); reference-style links and their definitions (become
inline links, same rendering); autolinks (become links); setext headings (become ATX); indented code
blocks (become fences, and marked already treats four leading spaces as code on both sides, so the
editor and reader agree); entity spellings (decoded to characters); `&nbsp;` blank paragraphs
(preserved via the upstream marker). I found no construct that is visible in the reader, dropped by
the editor, and not reported by the gate.

**Exact bytes until a real edit.** The component's contract (`MarkdownEditor.vue:23-38`) is enforced
in code, not by convention: `onUpdate` is suppressed during initial `setContent`
(`:125-154`), `toSource()` restores `lastEmitted` rather than the prop (`:156-168`), an external
`model` change is ignored when it equals our own emission (`:319-345`), and `onBeforeUnmount`
destroys the editor. Opening, focusing, selecting, switching modes and cancelling emit nothing —
asserted in happy-dom and again in Chromium ("reopen unchanged: Source shows the exact stored
bytes", then Cancel).

**Length limits.** The server limits are 50 000 / 50 000 / 50 000 / 131 072 runes; the client counts
code points with `Array.from` (`text.ts`), which matches Go `utf8.RuneCountInString` for valid UTF-8.
The guard runs on the **emitted Markdown string** (`form.description` / `form.introMarkdown`) before
the API call in all four submit paths, so normalisation that expands the stored form on a real edit
(`*` → `-`, `__bold__` → `**bold**`, table padding, the `&nbsp;` empty-paragraph marker) is counted,
not the pre-normalisation text. The counter, `aria-invalid` and the form-owned alert all read that
same value, and the alert lives outside the tabs, so an over-limit save from Preview is neither
silent nor duplicated. This directly answers the open question from the hand-off: the expansion case
is guarded.

One residual edge, not reachable in the product surfaces: if a parent validated and *withheld* the
echo, the visual-mode counter would read the parent's lagging value. The four product forms echo
immediately, and the withheld-echo path is only used to prove `toSource()` does not roll back the
author's work.

## Accessibility and UX

- Toolbar is `role="toolbar"` with a name; every control is a real `button` with an `aria-label`
  and a tooltip; pressed state is `aria-pressed` driven by `editor.isActive(...)` re-read on each
  transaction (`MarkdownEditorToolbar.vue:54-121`), so it cannot go stale; icon-only controls never
  rely on colour, and the pressed style has a non-colour indicator.
- The editable surface is `role="textbox"`, `aria-multiline`, labelled, with `aria-invalid` and a
  conditional `aria-describedby` that is removed when the field becomes valid (no dangling IDREF).
- `role="alert"` for the limit and link errors, `role="status"` for the unsupported-content notice
  and the loading state; the chunk-failure state is a non-editable notice with a reload action, so a
  half-loaded editor cannot accept input.
- Keyboard: `Mod-b`/`Mod-i`/`Mod-e`/`Mod-Alt-1..6`/`Mod-Shift-7|8`/`Mod-Alt-c`/`Mod-z`/
  `Mod-Shift-z` from the schema, `Mod-Shift-.` added by a small extension, `Mod-k` intercepted on the
  wrapper so the browser's address bar never opens, `Escape` closes the link row. `Enter` stays a
  newline and never submits.
- Mobile: toolbar wraps at 375 px without document overflow, and code scrolls inside its own box —
  measured in the `phone` project, not inferred.
- The destructive "Edit visually anyway" confirm offers Cancel first and Escape cancels; verified in
  real Chromium, which is the only place reka's focus trap can be proven.

## Verification and CI

The gates are real gates, not presence checks: the renderer spec asserts computed `fontSize`,
`fontWeight`, `lineHeight`, list `listStyleType`, cell `borderTopWidth`, link
`textDecorationLine`, `pre` background/`overflowX`, `scrollWidth <= clientWidth` for prose tables,
equal `thead`/`tbody` column widths, and a 16-column table that scrolls locally without widening the
page. The sanitiser spec removes forbidden attributes/classes in Chromium and calls the production
options directly. The authoring spec creates fixtures through the real HTTP API and the real UI,
asserts exact stored Markdown on reopen, the single over-limit alert with no PATCH, and reads the
result as a participant. `e2e/run-default-profile-e2e.sh` runs both files with `ACTFB_E2E=1`
against the same backend/Vite stack, `workers: 1`, and the CI integration job installs Chromium and
executes that script unfiltered. The `phone` project was changed to a 375 px Chromium viewport so the
CI image needs only the browser it already installs.

## Findings

### Critical

None.

### Important

None.

### Minor (triage — no blocker; disposition stated)

| # | Item | Where | Disposition |
|---|---|---|---|
| m1 | `docs/frontend.md:191-192` still says keyboard users "get the browser's native scrollable-region focus" without the Chromium-132+ qualification that the verification doc already carries | `docs/frontend.md` | **Fix in a docs-only follow-up** (one clause). It was requested in the round-2 review and the two documents still disagree; no code change. |
| m2 | The `th:not([align])` rule is never exercised: every table in the renderer spec uses explicit `:--`/`:-:`/`--:`, so the assertion labelled "unaligned cells stay left" measures cells that actually carry `align="left"` | `frontend/e2e/markdown-render.spec.ts:398-417` | **Accept as carried** (recorded in round 2). One bare `\| --- \|` table would close it; the riskier direction — a rule that clobbers explicit alignment — is covered. |
| m3 | Roving tabindex and tooltips on the two `Select` controls are deferred, but the deferral lives only in `.superpowers/.../task-2-review.md`, not in the product docs' deferred list | `docs/frontend.md`, `docs/rich-text-editor-verification.md` | **Record it in the deferred list** when the docs are next touched. Every control is already Tab-reachable and named, so this is polish, not a defect. |
| m4 | Spec §7 still instructs "if the editor chunk exceeds ~150 KB gzip, drop to 8 languages", while the shipped chunk is 159.06 kB with all 17 ids kept under main's lazy-loading ruling | `docs/superpowers/specs/2026-11-26-rich-text-editor-design.md:100-104` | **Optional:** add one line to the spec recording the superseding ruling so the approved document matches the shipped decision. The implementation docs and the ledger already state it. |
| m5 | `BoardPage.vue:183` and `ModulePage.vue:95` call `renderMarkdown` in the template instead of a `computed` (as `TaskPage` and `LandingPage` do), so any re-render re-parses the description | those two files | **Accept.** No timer drives those pages and the strings are bounded at 50 000 runes; a `computed` would be a consistency nicety, not a fix. |
| m6 | A bare long token in a cell (`flag{…}`, base64) keeps `overflow-wrap: normal`, so it widens its column and the table scrolls instead of breaking the token — but `docs/frontend.md:179-184` says "a long URL or token breaks inside its cell", which is only true for links and inline `code` | `frontend/src/style.css:431-476`, `docs/frontend.md` | **Accept the CSS** (widening then a local scroll is the better CTF behaviour); **narrow the doc sentence** to links/inline code, or record the bare-token scroll choice in one line. Docs-only. |
| m7 | All reported unit gates used `--maxWorkers=1`, while CI runs plain `vitest run` (default forks pool) | `frontend/package.json`, workflow `frontend` job | **No action expected:** Vitest isolates each test file, and no module-level state is shared across files. Mentioned only so the two commands are not conflated; a plain `bun run test` before publish would make the evidence exact. |
| m8 | `.markdown-body pre { margin: 0 }` outranks the shared `> * + *` rhythm, so a fenced block is flush with the preceding block in containers without a `space-y-*` utility (the header subtitle and the test fixture) | `frontend/src/style.css:378-390` | **Cosmetic, no action.** Every product reader article carries `space-y-*`, whose `margin-block-end` supplies the gap. |

Items already deferred by the task reviews — Write→Preview→Write destroying Tiptap undo history and
the caret, a `pre` inside the board/module header subtitle, table keyboard scrolling needing
`tabindex`, and DOMPurify only being exercised in a real browser — are **correctly recorded** in
`docs/rich-text-editor-verification.md:148-163` and are accepted as deferred. I re-read each one and
agree with the triage; none of them is a hidden Critical/Important.

## Positive observations

- The security work is layered rather than relying on one control: escaped raw HTML *and* a token
  URL allowlist *and* a strict DOMPurify allowlist with attribute-level hooks, with the hook scoped
  to a private instance instead of mutating the global singleton.
- The compatibility gate is token-based, not regex-based, and it is the reason the feature is safe to
  enable on legacy data at all; the table-cell traversal fix (walk by shape, classify by `type`) is
  the right generalisation rather than a patch.
- The round-trip corpus asserts three independent properties per item, which is exactly what a
  same-parser comparison cannot do on its own — the reviewers were right to demand it, and the
  implementer built it properly.
- The browser gates measure layout and computed style, so C1/I4-style "looks flat" defects cannot
  return silently. The reader table fix (`max-content` + `max-width: 100%` + `8em` floor +
  `normal` wrapping) was chosen from measurements and the rejected alternative is documented with
  its numbers.
- Error ownership is clean: one live message per form, outside the tabs, with `aria-invalid` and a
  conditional `aria-describedby`, and no duplicated alert from the editor.

## Final

The slice is coherent, scoped, and safe to publish. `docs/implementation-plan.md` marking the
feature **implemented** is now accurate. Publish may proceed after the main's own merge/publish
step; nothing in this review requires a code change first.
