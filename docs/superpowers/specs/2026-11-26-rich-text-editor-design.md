# Rich-text Markdown editor + syntax highlighting (approved spec)

Date: 2026-11-26
Base commit: `81cdbaa` (`main`, working tree clean)
Status: approved by the user; main owns the UX/contract decisions. Task 1 (safe shared syntax
highlighting) and Task 2 (Tiptap editor, Markdown round-trip, protected content) are implemented;
Task 3 (surface integration, browser gates, docs) remains missing.
No database migration, `/api/v2`, `deploy/` or `config.yaml` change has been made.

## 1. Context

Organizer-authored prose is Markdown today: board, module and task descriptions, plus the site
introduction. Every surface is a plain `Textarea`; every reader renders through one function,
`frontend/src/shared/lib/markdown.ts#renderMarkdown(source: string | null | undefined): string`
(marked → URL validation → DOMPurify). Consumers: `TaskPage.vue`, `LandingPage.vue`, and the
preview tabs of `BoardSettingsDialog.vue`, `ModuleEditorDialog.vue`, `TaskEditorDialog.vue`,
`AdminSitePage.vue`.

The approved scope is a visual (WYSIWYG) editor for those four authoring surfaces plus syntax
highlighting for fenced code blocks in **both** the editor and the shared read-only renderer.
Storage stays Markdown. No database migration, no `/api/v2` change, no new stored format.

## 2. Scope

In scope:

1. Visual editing of bold, italic, headings, bullet/ordered lists, links, blockquotes, fenced code
   blocks, undo/redo, with keyboard shortcuts.
2. Shared `MarkdownEditor.vue` used by board, module, task and site-intro authoring.
3. Optional Source mode (raw Markdown textarea) inside the same control.
4. Syntax highlighting of fenced code blocks in the editor and in `renderMarkdown`.
5. A language picker: `Plain text` default plus the common CTF languages, in the editor toolbar
   for the active code block.

Out of scope (explicit):

- No rich HTML is stored; the persisted value remains the Markdown string.
- No images/files upload, no drag-drop upload, no attachments changes.
- No cloud editor service, no runtime network request, no API keys, no telemetry/analytics.
- No auto-save, no `localStorage` draft persistence.
- No global restyle, no gradients, no animation, no new font.
- No page redesign. Board and module descriptions are rendered as safe Markdown inside their
  existing subtitle slot on `BoardPage`/`ModulePage` (description-only change, Task 3), and
  `TaskPage`'s rendered description gets the `.markdown-body` class, so saved formatting and
  highlighted code are actually visible. Task description, landing page and all previews already
  call `renderMarkdown` and gain highlighting without further changes.

## 3. Data contract (unchanged)

| Field | Where | Max (server, runes) |
|---|---|---|
| `Board.description` | `AdminBoard*Request` | 50 000 |
| `Module.description` | `AdminModule*Request` | 50 000 |
| `Task.description` | `AdminTask*Request` | 50 000 |
| `Site.introMarkdown` | `AdminSitePatchRequest` | 131 072 |

Verified in `internal/generated/openapi/models/*.go` (`validate.MaxLength`). The editor therefore
takes a `maxLength` prop, counts Unicode code points (`Array.from(value).length`, equivalent to Go
`utf8.RuneCountInString` for valid UTF-8), shows `count / max`, and blocks submit with a field
message instead of truncating. No server or schema change.

## 4. Supported Markdown subset

Already rendered by `renderMarkdown` and required to survive an open → edit → save cycle:

| Construct | Markdown | Editor node/mark |
|---|---|---|
| Paragraph, hard break | text, two spaces + newline | `paragraph`, `hardBreak` |
| Bold / italic / strikethrough | `**x**`, `*x*`, `~~x~~` | `bold`, `italic`, `strike` |
| Headings 1–6 | `#`…`######` | `heading` levels 1–6 |
| Bullet / ordered list (nested) | `-`, `1.` | `bulletList`, `orderedList` (+`start`) |
| Blockquote (nested) | `>` | `blockquote` |
| Inline code | `` `x` `` | `code` mark (plain, no highlighting) |
| Fenced code block + language | ```` ```python ```` | `codeBlock` + `language` |
| Link (+title) | `[t](url "title")` | `link` |
| Image (+title) | `![alt](src "title")` | `image` |
| Table (GFM, alignment) | pipe table | `table`/`tableRow`/`tableCell`/`tableHeader` |
| Horizontal rule | `---` | `horizontalRule` |

Not representable → protected (section 5): raw HTML, GFM task-list items (`- [ ]`), and any
link/image URL that `isSafeUrl` rejects.

## 5. Protected legacy content (never silently converted)

`inspectMarkdownSupport(source)` lexes the source with the same `marked` GFM configuration used for
rendering and walks the **token tree** — never a regex, so HTML inside a fenced code block is not
mistaken for raw HTML. It reports:

- `raw-html` — an `html`/`tag` token at block or inline level.
- `task-list` — a `list_item` token with `task: true`.
- `unsafe-url` — a `link`/`image` token whose `href` fails `isSafeUrl` (covers `javascript:`,
  `data:`, control-character obfuscation, since `isSafeUrl` parses with `new URL`).

Behaviour when a stored value is unsupported:

1. The control opens in **Source** mode; the textarea holds the exact bytes.
2. A non-blocking notice lists the reasons in plain language.
3. Visual mode stays available behind an explicit **Edit visually anyway** action guarded by the
   existing `ConfirmDialog`, which states that unsupported markup will be dropped on save.
4. Nothing is written back to `v-model` until the user actually edits. In visual mode the component
   only emits on a ProseMirror document change (initial `setContent` is suppressed), so opening a
   description and cancelling a dialog leaves the stored string byte-identical.

Markdown normalisation that the visual editor performs on a *real* edit (`*` → `-` bullets,
`__bold__` → `**bold**`, table column padding) is accepted. Semantic loss is not.

## 6. UX contract

- Layout: unchanged dialog layout. The existing `Write | Preview` tabs stay; only the **Write** tab
  body changes from `<Textarea>` to `<MarkdownEditor>`. Preview stays `renderMarkdown`.
- Toolbar (single row, wraps at phone width): Undo, Redo · Paragraph/H1/H2/H3 select · Bold,
  Italic, Inline code · Bullet list, Ordered list, Blockquote · Link, Code block · language select
  (only while the caret is inside a code block) · `Source`/`Visual` toggle at the end.
- Primitives: existing `Button`, `Select`, `Input`; plus thin shadcn-vue wrappers added for
  `Toggle` and `Tooltip` (reka-ui `Toggle`/`TooltipRoot` are already available through the
  installed `reka-ui` 2.11.0 — no new dependency). `Toggle` supplies `aria-pressed`.
- Every control has an accessible name (`aria-label`) and a tooltip; icon-only buttons never rely
  on colour. Focus ring, borders, 44 px phone touch targets reuse existing global rules.
- Keyboard: `Mod-b`, `Mod-i`, `Mod-e` (inline code), `Mod-Alt-1..6` (headings), `Mod-Shift-8` /
  `Mod-Shift-7` (lists), `Mod-Shift-.` (blockquote), `Mod-Alt-c` (code block), `Mod-z` /
  `Mod-Shift-z` (undo/redo), `Mod-k` (link bar). `Enter` exits an empty code block on the third
  press (`exitOnTripleEnter`).
- Link editing uses an inline URL row inside the editor (Input + Apply/Remove), not a nested dialog
  and not `window.prompt`. Invalid URLs are rejected with an inline message; `isSafeUrl` is the
  single validator.
- The editor surface is `contenteditable` with `role="textbox"`, `aria-multiline="true"` and the
  caller's label; it is styled with existing Rosé Pine tokens (`.markdown-editor` block in
  `src/style.css`, flat, no gradients, no animation).

## 7. Syntax highlighting contract

Shared module `frontend/src/shared/lib/highlight.ts`, used by both paths:

- Precompiled registry (static imports, no dynamic fetch, no network): `plaintext` (default),
  `python`, `javascript`, `typescript`, `go`, `bash`, `json`, `yaml`, `xml` (html/xml),
  `css`, `sql`, `c`, `cpp`, `java`, `rust`, `dockerfile`, `http`.
- Read-only path: `marked`'s `code` renderer asks `highlightCode(text, lang)`. Registered language →
  lowlight HAST → serialised by our own escaper that emits **only** `<span class="hljs-…">` and
  escapes every text node. `plaintext`, empty or unknown language → `<pre><code>` with escaped
  text and no spans. **No auto-detection**: `highlightAuto` is never called (misclassification and
  cost), and the editor adapter answers `registered()` truthy and returns a plain root for unknown
  languages so the upstream lowlight plugin cannot fall back to auto-detection.
- Editor path: `CodeBlockLowlight` with the same registry, `defaultLanguage: 'plaintext'`,
  `enableTabIndentation: true`, `tabSize: 2`. Inline `code` marks stay plain (no per-word colour).
- Sanitiser: `span` joins the DOMPurify allowlist and `class` is admitted only when the node is a
  `span` whose value matches `^(hljs-[a-z0-9_-]+\s*)+$` or a `code` whose value matches
  `^language-[a-z0-9+#.-]+$`. `style`, SVG, MathML, event handlers and all other attributes stay
  forbidden exactly as today.
- Styling: a small `.hljs-*` token map (keyword, string, number, comment, title/function, attr,
  built_in, literal, meta, type, symbol) mapped to the existing `--iris/--foam/--gold/--rose/--love`
  variables, plus `.markdown-body pre` spacing. Unknown `hljs-*` classes inherit the text colour.
- Bundle: 16 grammars are accepted if the built chunk stays reasonable; measured at execution with
  `bun run build`. If the editor chunk exceeds ~150 KB gzip, drop to `python, javascript,
  typescript, go, bash, json, sql, html` and record the measurement.

## 8. Security model

- The stored value is Markdown; the only HTML ever produced is from `renderMarkdown`.
- Raw HTML in Markdown stays escaped (unchanged behaviour).
- `javascript:`, `data:`, `vbscript:`, control-character obfuscation are rejected at token level by
  the existing `isSafeUrl`; the editor uses the same function as `Link.isAllowedUri`, and images are
  wrapped in a `SafeImage` extension whose `src` attribute is filtered through `isSafeUrl`
  (`allowBase64: false`).
- Pasted styled HTML is parsed by ProseMirror's schema, not by `v-html`: unknown nodes/marks and
  every `style`/`class`/event attribute are dropped, leaving text plus the supported marks.
- Code content is never executed or evaluated; no `eval`, no `new Function`, no `v-html` on code.
- Highlighting output is generated from an AST we serialise ourselves and then still passes
  DOMPurify; a crafted ```` ```python" onmouseover= ```` language is not a registered id and
  therefore renders as escaped plain text.
- No content is logged, persisted to `localStorage`, or sent anywhere beyond the existing save call.

## 9. Dependencies (exact pins, verified 2026-11-26 against the npm registry and official docs)

| Package | Version | License | Why |
|---|---|---|---|
| `@tiptap/core` | 3.35.0 | MIT | editor core |
| `@tiptap/pm` | 3.35.0 | MIT | ProseMirror peer |
| `@tiptap/vue-3` | 3.35.0 | MIT | `useEditor` |
| `@tiptap/starter-kit` | 3.35.0 | MIT | bold/italic/strike/headings/lists/quote/code/undo |
| `@tiptap/markdown` | 3.35.0 | MIT | `Markdown`, `MarkdownManager` parse/serialize |
| `@tiptap/extension-code-block-lowlight` | 3.35.0 | MIT | highlighted code blocks in the editor |
| `@tiptap/extension-table` | 3.35.0 | MIT | `TableKit` (GFM tables) |
| `@tiptap/extension-image` | 3.35.0 | MIT | images survive editing |
| `@tiptap/extensions` | 3.35.0 | MIT | `Placeholder` (UI only) |
| `lowlight` | 3.3.0 | MIT | HAST highlighting |
| `highlight.js` | 11.11.2 | BSD-3-Clause | grammars; satisfies lowlight `~11.11.0` and the extension peer `^11` |
| `marked` (bump) | 17.0.6 | MIT | one copy for `@tiptap/markdown` (`^17.0.1`) and `renderMarkdown` |

All `@tiptap/*` packages are one cohort at the single maintained release 3.35.0 (registry
`dist-tags.latest`, published 2026-11-20); mixing versions is unsupported because the peers pin
exactly. Transitives: `linkifyjs` (via `extension-link`), `prosemirror-*` (via `@tiptap/pm`),
`devlop`/`@types/hast` (types). `@floating-ui/dom` (peer of `@tiptap/vue-3`) is already installed
through `reka-ui` 2.11.0. OSV queries for `highlight.js@11.11.2`, `lowlight@3.3.0` and
`@tiptap/markdown@3.35.0` returned no advisories at planning time.

## 10. Compatibility risks and mitigations

| Risk | Mitigation |
|---|---|
| `@tiptap/markdown` is documented as an early release | Semantic round-trip corpus test, Source fallback, no auto-save; a failed round-trip is a RED test, not a silent rewrite |
| `marked` 15 → 17 (ESM-only, Node 20+, list tokenizer/renderer changes) | Existing `markdown.test.ts` is the regression gate; single copy satisfies both consumers |
| lowlight plugin falls back to `highlightAuto` | `safeLowlight` adapter (`registered()` truthy, `highlight()` plain for unknown) + `defaultLanguage: 'plaintext'`; test spies that auto-detect is never called |
| Table cells hold one child node in Markdown | Source Markdown cannot express multi-block cells, so no loss on this path; documented |
| Task lists are not in the approved node set | Detected as `task-list` and routed to Source |
| Tiptap parses embedded HTML through the schema | Unsupported content is detected first and opened in Source; visual mode is an explicit opt-in |
| happy-dom cannot exercise ProseMirror selection reliably | Unit tests cover the shell, lifecycle and pure modules; editing is verified in Chromium |
| Bundle growth from 16 grammars + editor | Measure at build; documented fallback list |

## 11. Acceptance gates

- `cd frontend && bun run test:unit` (existing 184 tests at base `81cdbaa` plus the new suites, all
  green).
- `bun run typecheck` and `bun run build`.
- `bun run test:e2e` renderer spec (real Chromium, no backend) green. happy-dom does not exercise
  DOMPurify, so this spec is the only proof of sanitisation: it must assert that forbidden classes
  and attributes are actually **removed** from the rendered DOM (not merely that a wrapping element
  still exists).
- `ACTFB_E2E=1 … bun run test:e2e` authoring spec green when a local backend is available. The
  existing CI browser step must execute it; a spec that silently skips without `ACTFB_E2E` is not
  acceptance. If the stack is unavailable, record "not executed" — never "passing".
- No `docs/swagger.yaml`, migration, `deploy/` or `config.yaml` change in the whole slice.

## 12. Repository findings that shaped this spec

1. `src/components/ui/toggle` and `src/components/ui/tooltip` do not exist yet, although
   `reka-ui` 2.11.0 ships `Toggle` (with `aria-pressed`) and `Tooltip*`. The plan adds thin
   shadcn-vue-style wrappers; no new dependency.
2. `renderMarkdown` is the only HTML producer and the only DOMPurify consumer, so the sanitiser
   hook is local to one module.
3. `BoardPage`/`ModulePage` print descriptions as plain subtitles. Ruling (preflight, Task 2/3
   boundary): they must render through `renderMarkdown` in that same slot — otherwise formatting and
   highlighted code that the new editor produces are invisible after saving. Description-only, no
   page redesign.
4. `frontend/package.json` currently pins `marked: ^15`, which conflicts with
   `@tiptap/markdown`'s `^17.0.1`; the bump to 17.0.6 is required for a single copy.
5. Description length is validated server-side only; the frontend has no length helper today.
