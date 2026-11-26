import type { AnyExtension, JSONContent } from '@tiptap/core'
import CodeBlockLowlight from '@tiptap/extension-code-block-lowlight'
import Image from '@tiptap/extension-image'
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
export const SafeImage = Image.extend({
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
 * The document schema. `StarterKit` supplies the approved subset (paragraph,
 * headings 1–6, bold/italic/strike, inline code, lists with `start`, blockquote,
 * hard break, horizontal rule, undo/redo); the code block comes from
 * `CodeBlockLowlight` so the editor and the reader share one registry, and
 * `SafeImage` replaces the stock image node.
 *
 * `underline` is disabled because it is not part of the supported Markdown
 * subset and has no Markdown spelling.
 */
export const markdownExtensions: AnyExtension[] = [
  StarterKit.configure({
    codeBlock: false,
    underline: false,
    heading: { levels: [1, 2, 3, 4, 5, 6] },
    link: {
      openOnClick: false,
      autolink: true,
      // Same predicate as `renderMarkdown`: an unsafe `href` never becomes a
      // link mark on paste, and is not rendered as an `href` if one exists.
      isAllowedUri: (url: string) => isSafeUrl(url),
    },
  }),
  CodeBlockLowlight.configure({
    lowlight: safeLowlight,
    defaultLanguage: DEFAULT_CODE_LANGUAGE,
    enableTabIndentation: true,
    tabSize: 2,
  }),
  TableKit,
  SafeImage,
  Markdown.configure({
    markedOptions: { gfm: true },
    indentation: { style: 'space', size: 2 },
  }),
]

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
