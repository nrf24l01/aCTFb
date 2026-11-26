import type { AnyExtension, JSONContent } from '@tiptap/core'
import { Extension } from '@tiptap/core'
import CodeBlockLowlight from '@tiptap/extension-code-block-lowlight'
import Image from '@tiptap/extension-image'
import { Paragraph } from '@tiptap/extension-paragraph'
import { TableKit } from '@tiptap/extension-table'
import { Markdown, MarkdownManager } from '@tiptap/markdown'
import StarterKit from '@tiptap/starter-kit'
import { DEFAULT_CODE_LANGUAGE, safeLowlight } from './highlight'
import { isSafeUrl } from './markdown'
import { isUnsafeUrlCandidate } from './markdownUnsupported'

/**
 * The Markdown document model shared by the visual editor and the
 * parse/serialise helpers.
 *
 * Storage stays a Markdown string. This module is the single place that decides
 * which ProseMirror nodes and marks a stored description may become, so the
 * editor, the round-trip helpers and the paste filter can never drift apart.
 *
 * Schema safety: pasted or parsed HTML is mapped through these extensions, never
 * injected with `v-html`. Unknown elements, `style`, `class` and event-handler
 * attributes have no place in the schema and are therefore dropped, leaving the
 * supported marks. URLs are filtered at the extension level (`SafeLink`,
 * `SafeImage`) with the same `isSafeUrl` predicate the renderer uses.
 */

/** Image source that survives the schema. Empty means "no source", not unsafe. */
const safeImageSource = (value: unknown): string | null => {
  if (typeof value !== 'string' || value === '') return null
  return isUnsafeUrlCandidate(value) ? null : value
}

/**
 * `Image` with a source filtered through `isSafeUrl`.
 *
 * Two layers: `allowBase64: false` drops `data:` sources in the default parse
 * rule, and the explicit `parseHTML`/`renderHTML` guards reject `javascript:`,
 * entity-encoded and control-character-obfuscated sources that a selector alone
 * cannot see. Returning `false` from `getAttrs` cancels the match, so a pasted
 * `<img src="javascript:…">` produces no node at all.
 */
const createSafeImage = (): AnyExtension => Image.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      src: {
        default: null,
        parseHTML: (element: HTMLElement) => safeImageSource(element.getAttribute('src')),
        renderHTML: (attributes: Record<string, unknown>) => {
          const src = safeImageSource(attributes.src)
          return src === null ? {} : { src }
        },
      },
    }
  },
  parseHTML() {
    return [
      {
        tag: 'img[src]',
        getAttrs: (element) => {
          const src = safeImageSource((element as HTMLElement).getAttribute('src'))
          return src === null ? false : null
        },
      },
    ]
  },
}).configure({ allowBase64: false })

/**
 * Contextual escaping for paragraph text.
 *
 * The upstream serializer escapes inline delimiters (`` ` ``, `*`, `_`, `[`,
 * `]`, `~`, `\`) and HTML-encodes `<`, `>` and `&`, but it does not escape
 * *block* markers. A paragraph whose literal text starts with `#`, `>`, `-`,
 * `+`, `1.`, `2)`, `|` or a `---`/`===` run was therefore written back as
 * something a Markdown reader parses as a heading, quote, list, table or rule —
 * a silent transform of an already-supported document.
 *
 * Escaping is applied to each line start, because a hard break inside a
 * paragraph also starts a line. It is only applied where a block construct
 * could begin, so ordinary prose is untouched, and it never runs on code: the
 * paragraph renderer below is the only caller, and code blocks/code marks use
 * their own renderers.
 */
const escapeBlockStarts = (markdown: string): string =>
  markdown
    .split('\n')
    .map((line) => {
      const indent = /^\s{0,3}/.exec(line)?.[0] ?? ''
      const rest = line.slice(indent.length)
      const escapeFirst = (): string => `${indent}\\${rest}`

      // ATX heading: `#` .. `######` followed by a space or end of line.
      if (/^#{1,6}(?:\s|$)/.test(rest)) return escapeFirst()
      // Blockquote.
      if (rest.startsWith('>')) return escapeFirst()
      // Bullet list. `*` is already escaped upstream, so only `-` and `+` remain.
      if (/^[-+](?:\s|$)/.test(rest)) return escapeFirst()
      // Ordered list: `1.` / `1)` followed by a space or end of line.
      const ordered = /^(\d{1,9})([.)])(?:\s|$)/.exec(rest)
      if (ordered) {
        const marker = indent.length + ordered[1].length
        return `${line.slice(0, marker)}\\${line.slice(marker)}`
      }
      // Table row candidate.
      if (rest.startsWith('|')) return escapeFirst()
      // Thematic break / setext underline: a run of `-` or `=`.
      if (/^([-=])\1{2,}\s*$/.test(rest)) return escapeFirst()

      return line
    })
    .join('\n')

/** Mirrors `@tiptap/extension-paragraph`'s blank-line marker (`&nbsp;`). */
const EMPTY_PARAGRAPH_MARKDOWN = '&nbsp;'

/**
 * The blockquote shortcut the approved spec promises (`Mod-Shift-.`).
 *
 * `@tiptap/extension-blockquote` binds only `Mod-Shift-b`; this adds the
 * documented shortcut without touching or forking the upstream node, so both
 * keep working.
 */
const createBlockquoteShortcut = (): AnyExtension => Extension.create({
  name: 'blockquoteShortcut',
  addKeyboardShortcuts() {
    return {
      'Mod-Shift-.': () => this.editor.commands.toggleBlockquote(),
    }
  },
})

/**
 * `Paragraph` with contextual block-marker escaping on serialisation.
 *
 * `@tiptap/markdown` has no text-node serializer hook, so the smallest safe
 * seam is the paragraph renderer: it produces the paragraph's inline Markdown
 * and is the only place a block marker can become line-leading text. This is a
 * shim over the stock node, not a fork: the schema (name, `parseHTML`,
 * `addCommands`) is inherited unchanged, and the empty-paragraph marker keeps
 * Tiptap's blank-line behaviour.
 */
const createSafeParagraph = (): AnyExtension => Paragraph.extend({
  renderMarkdown(node, helpers, context) {
    const content = Array.isArray(node?.content) ? node.content : []
    if (content.length === 0) {
      const previous = (context as { previousNode?: { type?: string, content?: unknown[] } } | undefined)
        ?.previousNode
      const previousIsEmptyParagraph =
        previous?.type === 'paragraph'
        && Array.isArray(previous.content)
        && previous.content.length === 0
      return previousIsEmptyParagraph ? EMPTY_PARAGRAPH_MARKDOWN : ''
    }
    return escapeBlockStarts(helpers.renderChildren(content))
  },
})

/**
 * The document schema. `StarterKit` supplies the approved subset (paragraph,
 * headings 1–6, bold/italic/strike, inline code, lists with `start`, blockquote,
 * hard break, horizontal rule, undo/redo); the code block comes from
 * `CodeBlockLowlight` so the editor and the reader share one registry, and
 * a source-filtered image node replaces the stock one. A paragraph shim keeps
 * literal block markers literal on serialisation.
 *
 * `underline` is disabled because it is not part of the supported Markdown
 * subset and has no Markdown spelling.
 *
 * Call `createMarkdownExtensions()` per editor: Tiptap keeps each extension's
 * `storage` on the instance, so sharing instances between two mounted editors
 * would share `@tiptap/markdown`'s manager as well.
 */
export function createMarkdownExtensions(): AnyExtension[] {
  return [
    StarterKit.configure({
      codeBlock: false,
      underline: false,
      paragraph: false,
      heading: { levels: [1, 2, 3, 4, 5, 6] },
      link: {
        openOnClick: false,
        autolink: true,
        // Same predicate as `renderMarkdown`: an unsafe `href` never becomes a
        // link mark on paste, and is not rendered as an `href` if one exists.
        isAllowedUri: (url: string) => isSafeUrl(url),
      },
    }),
    createSafeParagraph(),
    CodeBlockLowlight.configure({
      lowlight: safeLowlight,
      defaultLanguage: DEFAULT_CODE_LANGUAGE,
      enableTabIndentation: true,
      tabSize: 2,
    }),
    TableKit.configure({}),
    createSafeImage(),
    createBlockquoteShortcut(),
    Markdown.configure({
      markedOptions: { gfm: true },
      indentation: { style: 'space', size: 2 },
    }),
  ]
}

/**
 * The shared instance list, kept for the parse/serialise helpers and for tests.
 * Read-only in practice: editors must call `createMarkdownExtensions()`.
 */
export const markdownExtensions: AnyExtension[] = createMarkdownExtensions()

/**
 * One cached manager for both directions. Constructing a `MarkdownManager` is
 * the expensive part (it builds the tokenizer registry), and a single instance
 * guarantees that parse and serialise use identical options.
 */
const markdownManager = new MarkdownManager({
  extensions: markdownExtensions,
  markedOptions: { gfm: true },
  indentation: { style: 'space', size: 2 },
})

export function parseMarkdown(source: string): JSONContent {
  const markdown = typeof source === 'string' ? source : ''
  if (markdown.trim() === '') return { type: 'doc', content: [{ type: 'paragraph' }] }
  return markdownManager.parse(markdown)
}

export function serializeMarkdown(document: JSONContent): string {
  if (!document || typeof document !== 'object') return ''
  return markdownManager.serialize(document)
}

/**
 * Deterministic structural form: attributes that are null/undefined are dropped
 * and object keys are sorted, so two documents that mean the same thing compare
 * equal even if one carries an explicit `language: null`.
 */
const canonicalize = (value: unknown): unknown => {
  if (Array.isArray(value)) return value.map(canonicalize)
  if (value === null || typeof value !== 'object') return value

  const source = value as Record<string, unknown>
  const result: Record<string, unknown> = {}
  for (const key of Object.keys(source).sort()) {
    const entry = source[key]
    if (entry === null || entry === undefined) continue
    result[key] = canonicalize(entry)
  }
  return result
}

/**
 * True when both Markdown strings produce the same editor document.
 *
 * This answers "would editing one give me the other?", which is what the
 * editor needs. It deliberately does **not** prove that the reader sees the
 * same output: both sides go through the same parser, so a construct that the
 * parser cannot represent compares equal to itself while still being lost. That
 * is why unsupported constructs are routed to Source mode by
 * `inspectMarkdownSupport`, and why the round-trip test additionally asserts
 * explicit expected nodes and the rendered HTML.
 */
export function markdownSemanticsEqual(left: string, right: string): boolean {
  return (
    JSON.stringify(canonicalize(parseMarkdown(left))) ===
    JSON.stringify(canonicalize(parseMarkdown(right)))
  )
}
