# Rich-text Markdown editor + syntax highlighting — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give organizers visual Markdown authoring for board, module, task and site-intro text while
keeping the stored value a Markdown string, and highlight fenced code blocks in both the editor and
the existing shared read-only renderer.

**Architecture:** One shared highlighting registry feeds two paths — `marked`'s `code` renderer
(escaped lowlight HAST, then the existing DOMPurify pass) and Tiptap's `CodeBlockLowlight`. One
shared `MarkdownEditor.vue` replaces the `<Textarea>` inside the existing `Write` tabs; the stored
model stays `string`, so no API, schema or migration changes. A token-tree inspection decides
whether a stored value may open in visual mode; anything unsupported opens in Source with the exact
bytes.

**Tech Stack:** Vue 3.5 (`<script setup>`, `defineModel`), Tiptap 3.35.0 (`@tiptap/vue-3`,
`@tiptap/markdown`, `@tiptap/extension-code-block-lowlight`), lowlight 3.3.0, highlight.js 11.11.2,
marked 17.0.6, DOMPurify 3, reka-ui 2.11.0, Vitest 5 (happy-dom), Playwright 1.65.

**Spec:** `docs/superpowers/specs/2026-11-26-rich-text-editor-design.md`

**Base:** `81cdbaa` on `main`. `git status` must be clean before Task 1; the only new files at that
point are this plan and the spec.

## Global Constraints

- Persisted value stays the Markdown string. No DB migration, no `docs/swagger.yaml` change, no new
  route, no `deploy/` or `config.yaml` change.
- Exact pins, no carets: all `@tiptap/*` at `3.35.0`, `lowlight@3.3.0`, `highlight.js@11.11.2`,
  `marked@17.0.6`. Do not bump any other dependency.
- `renderMarkdown(source: string | null | undefined): string` keeps its name, parameters and return
  type; every existing consumer keeps working unchanged.
- No cloud editor service, no runtime network request, no API key, no telemetry, no `localStorage`
  draft, no auto-save.
- No global restyle, no gradients, no animation, no new font; reuse the Rosé Pine variables already
  in `frontend/src/style.css`.
- No `v-html` on anything that has not passed through `renderMarkdown`.
- Every interactive control has an accessible name; icon-only buttons use `Toggle`/`aria-pressed`
  plus a tooltip.
- `highlightAuto` is never called; unknown or plaintext languages render escaped plain text.
- Length limits come from the server: 50 000 for board/module/task description, 131 072 for the site
  introduction, counted in Unicode code points.
- Frontend gates for every task: `bun run typecheck`, `bun run test:unit`. Task 1 and Task 2 also
  `bun run build`.
- One commit per task, conventional message, no push.

## Review Focus

Failure modes this feature will actually meet, most likely first. Each line names the test that
pins it.

1. **Legacy description containing raw HTML or a `- [ ]` task list** is opened, "looks fine" in a
   rich editor, and is rewritten on save. Expected: opens in Source, bytes unchanged until an
   explicit edit, visual mode only behind a confirm. → `markdownUnsupported.test.ts` +
   `MarkdownEditor.test.ts` (Task 2).
2. **Fenced code with no language, or a language we do not ship**, is auto-detected and miscoloured
   (Python painted as SQL). Expected: plain escaped text, zero `hljs-` spans, no detection call.
   → `highlight.test.ts` (Task 1).
3. **`<script>` / `<img onerror>` inside a fenced block** becomes live DOM through the highlighter.
   Expected: escaped text only. → `markdown.test.ts` (Task 1).
4. **`[x](javascript:alert(1))` or `![a](data:…)` in an existing description** is silently dropped
   or silently kept when saved. Expected: detected as `unsafe-url`, opened in Source, never emitted
   as an attribute. → `markdownUnsupported.test.ts` (Task 2) and the existing
   `markdown.test.ts` URL assertions (Task 1).
5. **A 50 000-character description with emoji/CJK** disagrees with the server's rune count and the
   save fails with 422 after the editor accepted it. Expected: identical counting and a blocking
   field message. → `text.test.ts` (Task 2).

---

## File structure

| Path | Responsibility |
|---|---|
| `frontend/src/shared/lib/highlight.ts` | Language registry, lowlight instance, safe adapter, HAST → escaped span HTML |
| `frontend/src/shared/lib/markdown.ts` *(modify)* | Renderer: `code` override + `span`/`class` sanitiser rule |
| `frontend/src/shared/lib/markdownUnsupported.ts` | Token-tree inspection → `raw-html` / `task-list` / `unsafe-url` |
| `frontend/src/shared/lib/markdownDocument.ts` | Single extension list; parse/serialize; semantic comparison |
| `frontend/src/shared/lib/text.ts` | Code-point counting and description length validation |
| `frontend/src/shared/components/MarkdownEditor.vue` | Visual/Source control, `v-model`, protected-content gate, link row |
| `frontend/src/shared/components/MarkdownEditorToolbar.vue` | Toolbar buttons, heading select, code-language select |
| `frontend/src/components/ui/toggle/*` | shadcn-vue-style `Toggle` wrapper over reka-ui |
| `frontend/src/components/ui/tooltip/*` | shadcn-vue-style `Tooltip` wrappers over reka-ui |
| `frontend/src/style.css` *(modify)* | `.hljs-*` token colours and `.markdown-editor` surface |
| `frontend/src/features/admin/components/{BoardSettingsDialog,ModuleEditorDialog,TaskEditorDialog}.vue` | Write tab uses `MarkdownEditor`, submit validates length |
| `frontend/src/features/site/pages/AdminSitePage.vue` | Same, with the 131 072 limit |
| `frontend/e2e/markdown-render.spec.ts` | Real Chromium renderer check (no backend) |
| `frontend/e2e/rich-text-editor.spec.ts` | Real Chromium authoring flow (`ACTFB_E2E=1`) |

---

### Task 1: Safe syntax highlighting in the shared renderer

**Files:**
- Modify: `frontend/package.json`, `frontend/bun.lock`
- Create: `frontend/src/shared/lib/highlight.ts`
- Create: `frontend/src/shared/lib/highlight.test.ts`
- Modify: `frontend/src/shared/lib/markdown.ts`
- Modify: `frontend/src/shared/lib/markdown.test.ts`
- Modify: `frontend/src/style.css`

**Interfaces:**
- Consumes: `isSafeUrl` and `escapeHtml` from `frontend/src/shared/lib/markdown.ts` (export
  `escapeHtml` in this task so the highlighter reuses one escaper).
- Produces:
  ```ts
  export interface HighlightLanguage { id: string; label: string }
  export const HIGHLIGHT_LANGUAGES: readonly HighlightLanguage[]
  export const DEFAULT_CODE_LANGUAGE = 'plaintext'
  export function isHighlightLanguage(id: string | null | undefined): boolean
  /** Escaped span HTML for the inner <code>, or null when plain text must be used. */
  export function highlightCode(code: string, language: string | null | undefined): string | null
  export const lowlight: Lowlight
  export interface SafeLowlight {
    highlight(language: string, value: string): Root
    highlightAuto(value: string): Root
    listLanguages(): string[]
    registered(aliasOrLanguage: string): boolean
  }
  export const safeLowlight: SafeLowlight
  ```

- [ ] **Step 1: Add the two runtime dependencies**

```bash
cd frontend && bun add --exact lowlight@3.3.0 highlight.js@11.11.2
```
Expected: `package.json` gains both exact versions; `bun.lock` changes; no other package moves.

- [ ] **Step 2: Write the failing highlighter test**

`frontend/src/shared/lib/highlight.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest'
import { HIGHLIGHT_LANGUAGES, highlightCode, lowlight, safeLowlight } from './highlight'

describe('highlight registry', () => {
  it('offers plaintext plus the common CTF languages', () => {
    const ids = HIGHLIGHT_LANGUAGES.map((l) => l.id)
    expect(ids[0]).toBe('plaintext')
    expect(ids).toEqual(expect.arrayContaining(['python', 'javascript', 'typescript', 'go', 'bash', 'json', 'yaml', 'xml', 'css', 'sql', 'c', 'cpp', 'java', 'rust', 'dockerfile', 'http']))
  })
})

describe('highlightCode', () => {
  it('returns escaped hljs spans for a registered language', () => {
    const html = highlightCode('def f():\n  return 1', 'python')
    expect(html).toContain('hljs-keyword')
    expect(html).toContain('def')
  })

  it('returns null for plaintext, empty, and unknown languages', () => {
    expect(highlightCode('anything', 'plaintext')).toBeNull()
    expect(highlightCode('anything', 'brainfuck')).toBeNull()
    expect(highlightCode('anything', null)).toBeNull()
    expect(highlightCode('', 'python')).toBeNull()
  })

  it('escapes markup inside the code instead of emitting it', () => {
    const html = highlightCode('<img src=x onerror="alert(1)">', 'xml') ?? ''
    expect(html).not.toContain('<img')
    expect(html).toContain('&lt;')
    expect(html).not.toMatch(/onerror="alert/)
  })

  it('only emits span elements with hljs- classes', () => {
    const html = highlightCode('const a = 1', 'javascript') ?? ''
    const tags = html.match(/<[a-z]+/g) ?? []
    expect(new Set(tags)).toEqual(new Set(['<span']))
    for (const cls of html.matchAll(/class="([^"]*)"/g)) {
      expect(cls[1]).toMatch(/^(?:hljs-[a-z0-9_-]+\s*)+$/)
    }
  })
})

describe('safeLowlight', () => {
  it('never auto-detects: unknown languages return a plain root', () => {
    const spy = vi.spyOn(lowlight, 'highlightAuto')
    expect(safeLowlight.registered('not-a-language')).toBe(true)
    const tree = safeLowlight.highlight('not-a-language', 'x = 1')
    expect(JSON.stringify(tree)).not.toContain('hljs-')
    expect(spy).not.toHaveBeenCalled()
    expect(JSON.stringify(safeLowlight.highlightAuto('x = 1'))).not.toContain('hljs-')
  })
})
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd frontend && bun run test:unit src/shared/lib/highlight.test.ts`
Expected: FAIL — `Cannot find module './highlight'`.

- [ ] **Step 4: Implement `highlight.ts`**

Import `createLowlight` from `lowlight`, grammars from `highlight.js/lib/languages/<id>`, and reuse
`escapeHtml` from `./markdown`. Build `HIGHLIGHT_LANGUAGES` from a `const` array of `{ id, label }`
(the labels: `Plain text`, `Python`, `JavaScript`, `TypeScript`, `Go`, `Bash`, `JSON`, `YAML`,
`HTML/XML`, `CSS`, `SQL`, `C`, `C++`, `Java`, `Rust`, `Dockerfile`, `HTTP`). Register all ids except
`plaintext` plus the `plaintext` grammar into one `lowlight` instance. `highlightCode` returns
`null` when the language is not registered, is `plaintext`, or the code is empty; otherwise it walks
the returned HAST and serialises only `<span class="…">` with `escapeHtml` on text nodes, filtering
class names to `hljs-*`. `safeLowlight` wraps `lowlight`: `registered()` always `true`,
`listLanguages()` delegates, `highlight()` falls back to `{ type: 'root', children: [{ type: 'text',
value }] }` for unknown/plaintext, and `highlightAuto(value)` returns the same plain root.

- [ ] **Step 5: Run the highlighter test until green**

Run: `cd frontend && bun run test:unit src/shared/lib/highlight.test.ts`
Expected: PASS.

- [ ] **Step 6: Write the failing renderer tests**

Append to `frontend/src/shared/lib/markdown.test.ts`:

```ts
it('highlights a fenced block with a registered language', () => {
  const html = renderMarkdown('```python\ndef f():\n  return 1\n```')
  expect(html).toContain('class="language-python"')
  expect(html).toContain('hljs-keyword')
})

it('renders an unknown or missing language as escaped plain text', () => {
  const unknown = renderMarkdown('```brainfuck\n+++[>+<-]\n```')
  const missing = renderMarkdown('```\n<a href="x">y</a>\n```')
  expect(unknown).not.toContain('hljs-')
  expect(missing).not.toContain('hljs-')
  expect(missing).toContain('&lt;a href=')
})

it('keeps inline code plain and never colours it per word', () => {
  const html = renderMarkdown('use `SELECT 1` here')
  expect(html).toContain('<code>SELECT 1</code>')
  expect(html).not.toContain('hljs-')
})

it('does not let a crafted fence language inject attributes or classes', () => {
  const html = renderMarkdown('```python" onmouseover="alert(1)\nx\n```')
  expect(html).not.toMatch(/onmouseover/i)
  expect(html).not.toContain('hljs-')
})

it('still keeps tables, images, strikethrough and all heading levels', () => {
  const html = renderMarkdown('| a | b |\n| - | - |\n| 1 | 2 |\n\n![alt](/x.png "t")\n\n~~gone~~\n\n#### four')
  expect(html).toContain('<table>')
  expect(html).toContain('src="/x.png"')
  expect(html).toContain('<del>gone</del>')
  expect(html).toContain('<h4>four</h4>')
})

it('strips hljs classes that were not produced by the highlighter', () => {
  const html = renderMarkdown('<span class="hljs-evil">x</span>')
  expect(html).not.toContain('hljs-evil')
})
```

- [ ] **Step 7: Run them to verify they fail**

Run: `cd frontend && bun run test:unit src/shared/lib/markdown.test.ts`
Expected: FAIL on the new cases (no `hljs-keyword`, `span` stripped).

- [ ] **Step 8: Implement the renderer changes**

In `frontend/src/shared/lib/markdown.ts`: export `escapeHtml`; add `'span'` to `ALLOWED_TAGS` and
`'class'` to `ALLOWED_ATTR`; register one `DOMPurify.addHook('uponSanitizeAttribute', …)` that keeps
`class` only for `span` values matching `^(hljs-[a-z0-9_-]+\s*)+$` or `code` values matching
`^language-[a-z0-9+#.-]+$`, and forces `data.keepAttr = false` otherwise; add
`marked.use({ renderer: { code(token) { … } } })` that builds
`<pre><code class="language-<id>">…</code></pre>` using `highlightCode(token.text, token.lang)` and
falls back to `escapeHtml(token.text)`.

- [ ] **Step 9: Run the whole unit suite and typecheck**

Run: `cd frontend && bun run test:unit && bun run typecheck`
Expected: all suites pass, including the six pre-existing `markdown.test.ts` cases.

- [ ] **Step 10: Add the token styles**

In `frontend/src/style.css` add a flat block mapping `.hljs-keyword`, `.hljs-string`, `.hljs-number`,
`.hljs-comment`, `.hljs-title`, `.hljs-attr`, `.hljs-built_in`, `.hljs-literal`, `.hljs-meta`,
`.hljs-type`, `.hljs-symbol` to the existing `--iris/--foam/--gold/--rose/--love/--subtle`
variables, plus `.markdown-body pre` padding/overflow. No gradients, no transitions, no new font.

- [ ] **Step 11: Build and commit**

```bash
cd frontend && bun run build
cd .. && git add frontend/package.json frontend/bun.lock frontend/src/shared/lib/highlight.ts \
  frontend/src/shared/lib/highlight.test.ts frontend/src/shared/lib/markdown.ts \
  frontend/src/shared/lib/markdown.test.ts frontend/src/style.css
git commit -m "feat(frontend): highlight fenced code in the shared Markdown renderer"
```

---

### Task 2: Tiptap editor, Markdown round-trip lifecycle, protected content

**Files:**
- Modify: `frontend/package.json`, `frontend/bun.lock`
- Create: `frontend/src/shared/lib/text.ts`, `frontend/src/shared/lib/text.test.ts`
- Create: `frontend/src/shared/lib/markdownUnsupported.ts`, `frontend/src/shared/lib/markdownUnsupported.test.ts`
- Create: `frontend/src/shared/lib/markdownDocument.ts`, `frontend/src/shared/lib/markdownDocument.test.ts`
- Create: `frontend/src/components/ui/toggle/Toggle.vue`, `frontend/src/components/ui/toggle/index.ts`
- Create: `frontend/src/components/ui/tooltip/{TooltipProvider,Tooltip,TooltipTrigger,TooltipContent}.vue`, `frontend/src/components/ui/tooltip/index.ts`
- Create: `frontend/src/shared/components/MarkdownEditor.vue`
- Create: `frontend/src/shared/components/MarkdownEditorToolbar.vue`
- Create: `frontend/src/shared/components/MarkdownEditor.test.ts`
- Modify: `frontend/src/style.css`

**Interfaces:**
- Consumes: `HIGHLIGHT_LANGUAGES`, `DEFAULT_CODE_LANGUAGE`, `highlightCode`, `safeLowlight` (Task 1);
  `isSafeUrl` (existing); `ConfirmDialog` (existing).
- Produces:
  ```ts
  // text.ts
  export const DESCRIPTION_MAX_CHARACTERS = 50_000
  export const SITE_INTRO_MAX_CHARACTERS = 131_072
  export function countCharacters(value: string): number
  export function validateDescriptionLength(value: string, max: number): string | undefined

  // markdownUnsupported.ts
  export type UnsupportedReason = 'raw-html' | 'task-list' | 'unsafe-url'
  export interface MarkdownSupportReport { supported: boolean; reasons: UnsupportedReason[] }
  export function inspectMarkdownSupport(source: string): MarkdownSupportReport

  // markdownDocument.ts
  export const markdownExtensions: AnyExtension[]
  export function parseMarkdown(source: string): JSONContent
  export function serializeMarkdown(document: JSONContent): string
  export function markdownSemanticsEqual(left: string, right: string): boolean
  ```
  `MarkdownEditor.vue`: props `{ modelValue: string; label: string; id?: string; maxLength?: number;
  placeholder?: string }`, emits `update:modelValue`, exposes `focus(): void`.
  `MarkdownEditorToolbar.vue`: props `{ editor: Editor; disabled?: boolean }`, emits
  `{ requestLink: [] }`.

- [ ] **Step 1: Add the Tiptap cohort and bump marked**

```bash
cd frontend && bun add --exact @tiptap/core@3.35.0 @tiptap/pm@3.35.0 @tiptap/vue-3@3.35.0 \
  @tiptap/starter-kit@3.35.0 @tiptap/markdown@3.35.0 @tiptap/extension-code-block-lowlight@3.35.0 \
  @tiptap/extension-table@3.35.0 @tiptap/extension-image@3.35.0 @tiptap/extensions@3.35.0 \
  marked@17.0.6
```
Expected: `bun.lock` has exactly one `marked@17.0.6`; `bun pm ls | grep -c tiptap` shows the cohort
at one version.

- [ ] **Step 2: Write the failing `text.ts` test**

`frontend/src/shared/lib/text.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { DESCRIPTION_MAX_CHARACTERS, SITE_INTRO_MAX_CHARACTERS, countCharacters, validateDescriptionLength } from './text'

it('counts code points like Go runes', () => {
  expect(countCharacters('plain')).toBe(5)
  expect(countCharacters('🎯')).toBe(1)
  expect(countCharacters('日本語')).toBe(3)
  expect(countCharacters('a🎯日')).toBe(3)
})

it('accepts the limit and rejects one character over', () => {
  expect(validateDescriptionLength('x'.repeat(50_000), DESCRIPTION_MAX_CHARACTERS)).toBeUndefined()
  expect(validateDescriptionLength('x'.repeat(50_001), DESCRIPTION_MAX_CHARACTERS))
    .toBe('Description must be at most 50,000 characters.')
  expect(validateDescriptionLength('🎯'.repeat(131_072), SITE_INTRO_MAX_CHARACTERS)).toBeUndefined()
})
```

- [ ] **Step 3: Run it to verify it fails, then implement `text.ts`**

Run: `cd frontend && bun run test:unit src/shared/lib/text.test.ts` → FAIL (module missing).
Implement with `Array.from(value).length` and a `toLocaleString('en-US')`-formatted message, then
re-run → PASS.

- [ ] **Step 4: Write the failing `markdownUnsupported.ts` test**

`frontend/src/shared/lib/markdownUnsupported.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { inspectMarkdownSupport } from './markdownUnsupported'

it('accepts the supported subset', () => {
  const source = '# H\n\n**b** *i* ~~s~~ `c`\n\n- a\n  - b\n\n1. x\n\n> q\n\n```python\nprint(1)\n```\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\n![alt](/x.png)\n\n[rel](/boards)'
  expect(inspectMarkdownSupport(source)).toEqual({ supported: true, reasons: [] })
})

it('flags raw HTML blocks and inline tags', () => {
  expect(inspectMarkdownSupport('<div>x</div>').reasons).toEqual(['raw-html'])
  expect(inspectMarkdownSupport('text <b>bold</b> text').reasons).toEqual(['raw-html'])
})

it('does not flag HTML inside a fenced code block', () => {
  expect(inspectMarkdownSupport('```html\n<div onmouseover="x">y</div>\n```').supported).toBe(true)
  expect(inspectMarkdownSupport('```\n<script>alert(1)</script>\n```').supported).toBe(true)
})

it('flags GFM task list items', () => {
  expect(inspectMarkdownSupport('- [ ] todo\n- [x] done').reasons).toEqual(['task-list'])
})

it('flags unsafe link and image URLs, including obfuscated ones', () => {
  expect(inspectMarkdownSupport('[x](javascript:alert(1))').reasons).toEqual(['unsafe-url'])
  expect(inspectMarkdownSupport('![a](data:image/svg+xml;base64,PHN2Zz4=)').reasons).toEqual(['unsafe-url'])
  expect(inspectMarkdownSupport('[x](java\tscript:alert(1))').reasons).toEqual(['unsafe-url'])
})
```

- [ ] **Step 5: Run it to verify it fails, then implement `markdownUnsupported.ts`**

Run: `cd frontend && bun run test:unit src/shared/lib/markdownUnsupported.test.ts` → FAIL.
Implement with a dedicated `new Marked({ gfm: true, breaks: false })` and `.lexer(source)` (not the
shared renderer instance, whose `walkTokens` escapes HTML), then a recursive walk over
`tokens`/`items`/`header`/`rows` collecting the three reasons; `supported` is `reasons.length === 0`.
Re-run → PASS.

- [ ] **Step 6: Write the failing round-trip test**

`frontend/src/shared/lib/markdownDocument.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { markdownSemanticsEqual, parseMarkdown, serializeMarkdown } from './markdownDocument'

const CORPUS = [
  '# H1\n\n###### H6',
  'Some **bold**, *italic*, ~~strike~~ and `inline code`.',
  '- one\n- two\n  - nested\n\n1. first\n2. second',
  '> quoted\n>\n> > nested quote',
  '```python\ndef f():\n  return 1\n```',
  '```brainfuck\n+++[>+<-]\n```',
  '[link](/boards "title") and ![img](/x.png "t")',
  '| a | b |\n| :- | -: |\n| 1 | 2 |',
  '3. starts at three\n4. then four',
  '---',
]

describe('markdown round-trip', () => {
  it.each(CORPUS)('preserves semantics for %s', (source) => {
    expect(markdownSemanticsEqual(source, serializeMarkdown(parseMarkdown(source)))).toBe(true)
  })

  it('is idempotent after one serialisation', () => {
    const once = serializeMarkdown(parseMarkdown(CORPUS.join('\n\n')))
    expect(serializeMarkdown(parseMarkdown(once))).toBe(once)
  })

  it('keeps an unknown fence language instead of dropping it', () => {
    expect(serializeMarkdown(parseMarkdown('```brainfuck\n+++\n```'))).toContain('```brainfuck')
  })
})
```

- [ ] **Step 7: Run it to verify it fails, then implement `markdownDocument.ts`**

Run: `cd frontend && bun run test:unit src/shared/lib/markdownDocument.test.ts` → FAIL.
Implement `markdownExtensions` (single source of truth) as: `StarterKit.configure({ codeBlock: false,
underline: false, heading: { levels: [1,2,3,4,5,6] }, link: { openOnClick: false, autolink: true,
isAllowedUri: (url) => isSafeUrl(url) } })`, `CodeBlockLowlight.configure({ lowlight: safeLowlight,
defaultLanguage: DEFAULT_CODE_LANGUAGE, enableTabIndentation: true, tabSize: 2 })`, `TableKit`,
`SafeImage` (an `Image.extend` that filters `src` through `isSafeUrl`, `allowBase64: false`),
`Markdown.configure({ markedOptions: { gfm: true }, indentation: { style: 'space', size: 2 } })`.
`parseMarkdown`/`serializeMarkdown` delegate to one cached `new MarkdownManager({ extensions:
markdownExtensions, markedOptions: { gfm: true } })`; `markdownSemanticsEqual` compares
`JSON.stringify` of both parses. Re-run → PASS.

- [ ] **Step 8: Add the `Toggle` and `Tooltip` primitives**

Create `src/components/ui/toggle/Toggle.vue` forwarding reka-ui `Toggle` (`modelValue` +
`update:modelValue`, `data-slot="toggle"`, shadcn sizing classes, `variant`/`size` via `cva`
matching `Button`'s neutral palette) and the four tooltip wrappers over `TooltipProvider`,
`TooltipRoot`, `TooltipTrigger`, `TooltipContent` with `data-slot` attributes and
`bg-popover text-popover-foreground border border-border px-2 py-1 text-xs` styling. No animation
class beyond the existing `tw-animate-css` defaults already used by dialogs.

- [ ] **Step 9: Write the failing component test**

`frontend/src/shared/components/MarkdownEditor.test.ts`:

```ts
import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import MarkdownEditor from './MarkdownEditor.vue'

const mountEditor = (modelValue: string, maxLength?: number) =>
  mount(MarkdownEditor, { props: { modelValue, label: 'Task content', maxLength }, attachTo: document.body })

it('exposes a named toolbar and a source toggle', async () => {
  const wrapper = mountEditor('**bold**')
  await flushPromises()
  const labels = Array.from(document.querySelectorAll('[aria-label]')).map((el) => el.getAttribute('aria-label'))
  expect(labels).toEqual(expect.arrayContaining(['Bold', 'Italic', 'Bullet list', 'Code block', 'Source mode']))
  wrapper.unmount()
})

it('keeps exact bytes when switching to source and back without editing', async () => {
  const wrapper = mountEditor('__bold__\n\n* item')
  await flushPromises()
  await wrapper.get('[aria-label="Source mode"]').trigger('click')
  const textarea = wrapper.get('textarea')
  expect((textarea.element as HTMLTextAreaElement).value).toBe('__bold__\n\n* item')
  expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  wrapper.unmount()
})

it('opens unsupported content in source with a warning and no visual editor', async () => {
  const wrapper = mountEditor('<div>legacy</div>')
  await flushPromises()
  expect(document.body.textContent).toContain('Source')
  expect(document.body.textContent).toMatch(/raw HTML/i)
  expect(wrapper.find('.ProseMirror').exists()).toBe(false)
  expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  wrapper.unmount()
})

it('shows the code-point counter and blocks over-limit content', async () => {
  const wrapper = mountEditor('🎯'.repeat(3), 2)
  await flushPromises()
  expect(document.body.textContent).toContain('3 / 2')
  expect(document.body.textContent).toContain('at most 2 characters')
  wrapper.unmount()
})
```

- [ ] **Step 10: Run it to verify it fails, then implement the editor**

Run: `cd frontend && bun run test:unit src/shared/components/MarkdownEditor.test.ts` → FAIL.
Implement `MarkdownEditor.vue` with `defineModel<string>()`, `mode: 'visual' | 'source'`,
`report = inspectMarkdownSupport(modelValue)` computed on mount and on entering visual mode,
`sourceDraft` bound to the textarea, and the editor created only when `mode === 'visual'` and
`report.supported`. Emit only from the editor's `onUpdate` with an `initialising` guard around the
initial `setContent(value, { contentType: 'markdown' })`. `MarkdownEditorToolbar.vue` renders the
controls from the spec, with `Toggle` for bold/italic/inline code and the active marks bound to
`editor.isActive(...)`; the code-language `Select` is rendered only when
`editor.isActive('codeBlock')` and writes with
`editor.chain().focus().updateAttributes('codeBlock', { language }).run()`.
Re-run → PASS, then `bun run typecheck`.

- [ ] **Step 11: Add editor surface styles, build, commit**

Add `.markdown-editor` (border, min-height, padding, `:focus-within` ring) and
`.markdown-editor .ProseMirror` (headings, lists, blockquote, `pre`, table borders, `code`) to
`frontend/src/style.css` using existing variables only.

```bash
cd frontend && bun run test:unit && bun run typecheck && bun run build
cd .. && git add frontend/package.json frontend/bun.lock frontend/src/shared/lib/text.ts \
  frontend/src/shared/lib/text.test.ts frontend/src/shared/lib/markdownUnsupported.ts \
  frontend/src/shared/lib/markdownUnsupported.test.ts frontend/src/shared/lib/markdownDocument.ts \
  frontend/src/shared/lib/markdownDocument.test.ts frontend/src/components/ui/toggle \
  frontend/src/components/ui/tooltip frontend/src/shared/components/MarkdownEditor.vue \
  frontend/src/shared/components/MarkdownEditorToolbar.vue \
  frontend/src/shared/components/MarkdownEditor.test.ts frontend/src/style.css
git commit -m "feat(frontend): add the Markdown rich-text editor with source fallback"
```

---

### Task 3: Integrate the four authoring surfaces, browser verification, docs

**Files:**
- Modify: `frontend/src/features/admin/components/BoardSettingsDialog.vue`
- Modify: `frontend/src/features/admin/components/ModuleEditorDialog.vue`
- Modify: `frontend/src/features/admin/components/TaskEditorDialog.vue`
- Modify: `frontend/src/features/site/pages/AdminSitePage.vue`
- Modify: `frontend/src/features/admin/components/BoardSettingsDialog.test.ts`
- Modify: `frontend/src/features/admin/components/ModuleEditorDialog.test.ts`
- Modify: `frontend/src/features/admin/components/TaskEditorDialog.test.ts`
- Create: `frontend/e2e/markdown-render.spec.ts`
- Create: `frontend/e2e/rich-text-editor.spec.ts`
- Modify: `docs/frontend.md`, `docs/implementation-plan.md`

**Interfaces:**
- Consumes: `MarkdownEditor` (`v-model` Markdown string, `label`, `id`, `maxLength`),
  `DESCRIPTION_MAX_CHARACTERS`, `SITE_INTRO_MAX_CHARACTERS`, `validateDescriptionLength`.
- Produces: no new exported API; the dialogs keep their existing props, emits and payload shapes.

- [ ] **Step 1: Write the failing dialog tests**

Add to `ModuleEditorDialog.test.ts` (same pattern for `BoardSettingsDialog.test.ts` and
`TaskEditorDialog.test.ts`):

```ts
it('edits the description with the rich editor and keeps Markdown on save', async () => {
  const wrapper = mount(ModuleEditorDialog, { props: { open: true, boardUuid: '…', audiences: [], module: { …, description: '**bold**' } }, attachTo: document.body })
  await flushPromises()
  expect(document.querySelector('[aria-label="Bold"]')).not.toBeNull()
  expect(document.querySelector('#module-description')).not.toBeNull()
  await document.querySelector<HTMLButtonElement>('[aria-label="Source mode"]')!.click()
  await flushPromises()
  expect((document.querySelector('textarea') as HTMLTextAreaElement).value).toBe('**bold**')
  wrapper.unmount()
})

it('blocks submit when the description exceeds the server limit', async () => {
  // description = 'x'.repeat(DESCRIPTION_MAX_CHARACTERS + 1); submit; expect updateModule not called
  // and a visible 'at most 50,000 characters' message
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd frontend && bun run test:unit src/features/admin/components`
Expected: FAIL — no `Bold` control, no `#module-description` on a rich editor.

- [ ] **Step 3: Replace the Write tab bodies**

In each of the three dialogs and `AdminSitePage.vue`, replace the `<Textarea …/>` inside the `Write`
`TabsContent` with:

```vue
<MarkdownEditor
  id="module-description"
  v-model="form.description"
  label="Module description"
  :max-length="DESCRIPTION_MAX_CHARACTERS"
/>
```

(keep the existing ids `board-description`, `task-description`, `site-intro`, the existing
`Write | Preview` tabs, the existing preview `<article v-html="preview" />` and the existing
"Nothing to preview yet" empty state). In each dialog's submit guard, add
`problems.description = validateDescriptionLength(form.description, DESCRIPTION_MAX_CHARACTERS)`
(`SITE_INTRO_MAX_CHARACTERS` for the site page) and return before the API call when set.

- [ ] **Step 4: Run the unit suite and typecheck**

Run: `cd frontend && bun run test:unit && bun run typecheck`
Expected: all green, including the existing dialog tests (they read `document.body.textContent`, so
the visible labels must not change).

- [ ] **Step 5: Add the real-browser renderer spec**

`frontend/e2e/markdown-render.spec.ts` (no backend; relies on the Playwright `webServer` Vite
instance):

```ts
test('renders highlighted, escaped code in a real browser', async ({ page }) => {
  await page.goto('/')
  const html = await page.evaluate(async () => {
    const mod = await import('/src/shared/lib/markdown.ts')
    return mod.renderMarkdown('```python\ndef f():\n  return 1\n```\n\n```\n<script>alert(1)</script>\n```\n\n```brainfuck\n+++\n```')
  })
  expect(html).toContain('class="language-python"')
  expect(html).toContain('hljs-keyword')
  expect(html).not.toContain('<script')
  expect(html).not.toContain('hljs-</code>')
  await page.evaluate((markup) => { document.body.innerHTML = `<div class="markdown-body">${markup}</div>` }, html)
  const color = await page.locator('.hljs-keyword').first().evaluate((el) => getComputedStyle(el).color)
  expect(color).not.toBe('rgb(224, 222, 244)')
})
```

- [ ] **Step 6: Add the gated authoring spec**

`frontend/e2e/rich-text-editor.spec.ts`, following the existing `board-flow.spec.ts` conventions
(`const enabled = process.env.ACTFB_E2E === '1'`, `test.skip(!enabled, …)`, admin credentials from
env, fixture created through the HTTP API, no SQL): log in, create a board, open the module editor,
type into the rich editor, press `Mod-b` and assert a `<strong>` exists, insert a code block, choose
`Python` in the language picker, save, reopen the editor and assert the Markdown round-tripped
(```` ```python ```` present in Source), then open the task page as a participant and assert
`.hljs-keyword` is rendered read-only. Also paste `<b style="color:red">x</b>` into the editor and
assert the stored Markdown contains `**x**` and no `style=`.

- [ ] **Step 7: Run the browser gates**

```bash
cd frontend && bun run test:e2e -- markdown-render.spec.ts
```
Expected: PASS in both configured projects (chromium, phone).

```bash
ACTFB_E2E=1 E2E_API_URL=http://127.0.0.1:8080/api/v2 E2E_ADMIN_USERNAME=… E2E_ADMIN_PASSWORD=… \
  bun run test:e2e -- rich-text-editor.spec.ts
```
Expected: PASS when the local stack is available. If it is not, record "not executed" — never
report it as passing.

- [ ] **Step 8: Update the docs and commit**

`docs/frontend.md`: add a short "Markdown authoring" section (editor capabilities, Source mode and
why it exists, supported subset, highlighting registry, how to add a language).
`docs/implementation-plan.md`: replace the trailing "The Markdown editor remains a separate decision
and is not implemented." with the implemented status, the exact gates that ran and their results,
and the spec/plan paths.

```bash
cd frontend && bun run test:unit && bun run typecheck && bun run build
cd .. && git add frontend/src/features frontend/e2e docs/frontend.md docs/implementation-plan.md
git commit -m "feat(frontend): use the rich-text editor for board, module, task and site text"
```

---

## Final verification

```bash
cd frontend && bun run test:unit && bun run typecheck && bun run build
cd .. && git status --porcelain && git log --oneline -4
```
Report: changed files per task, the exact commands run with their output, bundle size of the editor
chunk (and whether the 15-language registry was kept), which browser specs actually executed, and
any remaining blocker. Do not push.
