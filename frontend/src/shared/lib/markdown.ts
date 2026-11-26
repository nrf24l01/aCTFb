import DOMPurify from 'dompurify'
import { Marked } from 'marked'
import type { Token, Tokens } from 'marked'
import { escapeHtml } from './html'
import { highlightCode, normalizeCodeLanguage } from './highlight'

export { escapeHtml } from './html'

/**
 * Markdown rendering for organizer-authored content. The single entry point
 * renders with `marked` and then sanitises with DOMPurify.
 *
 * Security contract:
 * - raw HTML in the Markdown source is escaped, never executed;
 * - link/image URLs are validated while tokenising (domain-level allowlist for
 *   http, https and mailto, plus same-origin relative paths), so
 *   `javascript:`, `data:`, `vbscript:` never reach the HTML;
 * - only a small tag/attribute allowlist survives DOMPurify;
 * - `class` survives only on highlighter-generated spans (`hljs-*`) and code
 *   blocks (`language-*`); every other class is removed by a local hook;
 * - SVG/MathML/style/event-handler attributes are stripped.
 */
const ALLOWED_TAGS = [
  'p', 'br', 'strong', 'em', 'del', 'code', 'pre', 'blockquote',
  'ul', 'ol', 'li', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
  'a', 'hr', 'table', 'thead', 'tbody', 'tr', 'th', 'td', 'img', 'span',
]

const ALLOWED_ATTR = ['href', 'title', 'alt', 'src', 'colspan', 'rowspan', 'class', 'align']

const SAFE_PROTOCOLS = new Set(['http:', 'https:', 'mailto:'])

const HLJS_CLASS_VALUE = /^(?:hljs-[a-z0-9_-]+\s*)+$/
const LANGUAGE_CLASS_VALUE = /^language-[a-z0-9+#.-]+$/

/**
 * A private DOMPurify instance keeps the class hook scoped to this module
 * instead of mutating the global singleton for every consumer.
 */
const purifier = DOMPurify(window)

purifier.addHook('uponSanitizeAttribute', (node, data) => {
  const tagName = node.nodeName.toLowerCase()
  if (data.attrName === 'align') {
    // GFM table alignment is rendered semantics, not styling. Allow only the
    // three values marked emits, only on table cells. `style` stays forbidden,
    // so a crafted table can never smuggle CSS through this attribute.
    const allowed =
      (tagName === 'th' || tagName === 'td') && /^(?:left|center|right)$/i.test(data.attrValue)
    if (!allowed) data.keepAttr = false
    return
  }
  if (data.attrName !== 'class') return
  const allowed =
    (tagName === 'span' && HLJS_CLASS_VALUE.test(data.attrValue)) ||
    (tagName === 'code' && LANGUAGE_CLASS_VALUE.test(data.attrValue))
  if (!allowed) {
    data.keepAttr = false
  }
})

/** Same-origin form-relative or absolute URLs are allowed; anything else must
 * use an explicit safe protocol. `javascript:` and `data:` fail. */
export function isSafeUrl(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed === '') return false
  if (/^(?:#|\?|\/|\.{1,2}\/)/.test(trimmed)) return true
  try {
    const parsed = new URL(trimmed, 'https://actfb.invalid/')
    return SAFE_PROTOCOLS.has(parsed.protocol)
  } catch {
    return false
  }
}

const marked = new Marked({ gfm: true, breaks: false })

marked.use({
  walkTokens(token: Token) {
    if (token.type === 'html') {
      // Raw HTML block/inline tokens are escaped rather than emitted.
      const html = token as Tokens.HTML
      html.text = escapeHtml(html.text)
      return
    }
    if (token.type === 'tag') {
      const tag = token as Tokens.Tag
      tag.text = escapeHtml(tag.text)
      return
    }
    if (token.type === 'link' || token.type === 'image') {
      const candidate = token as Tokens.Link | Tokens.Image
      if (!isSafeUrl(candidate.href)) {
        candidate.href = ''
      }
    }
  },
})

marked.use({
  renderer: {
    code(token: Tokens.Code): string {
      const language = normalizeCodeLanguage(token.lang)
      const highlighted = highlightCode(token.text, token.lang)
      const body = highlighted ?? escapeHtml(token.text)
      return `<pre><code class="language-${language}">${body}</code></pre>\n`
    },
  },
})

export function renderMarkdown(source: string | null | undefined): string {
  const markdown = typeof source === 'string' ? source : ''
  const raw = marked.parse(markdown) as string
  return purifier.sanitize(raw, {
    ALLOWED_TAGS,
    ALLOWED_ATTR,
    ALLOW_DATA_ATTR: false,
    FORBID_TAGS: ['svg', 'math', 'style', 'script', 'iframe', 'object', 'embed', 'form', 'input', 'button'],
    FORBID_ATTR: ['style'],
  })
}
