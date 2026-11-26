import { Marked } from 'marked'
import type { Token, Tokens } from 'marked'
import { isSafeUrl } from './markdown'

/**
 * Compatibility gate for stored Markdown.
 *
 * The visual editor can only represent the subset described in the approved
 * spec. Anything else must open in Source mode with the exact bytes, because a
 * silent conversion would destroy organizer content (a `- [ ]` task list would
 * come back as a plain bullet, raw HTML would be dropped).
 *
 * Detection is token-based on purpose. A regex over the source would misread
 * HTML that lives inside a fenced code block (```` ```html ```` examples are
 * common in CTF descriptions) and would flag literal angle brackets in prose.
 * We therefore lex with the same GFM configuration the renderer uses and walk
 * the token tree.
 */

export type UnsupportedReason = 'raw-html' | 'task-list' | 'unsafe-url'

export interface MarkdownSupportReport {
  supported: boolean
  reasons: UnsupportedReason[]
}

/** Canonical reporting order, so the notice never reshuffles between renders. */
const REASON_ORDER: readonly UnsupportedReason[] = ['raw-html', 'task-list', 'unsafe-url']

/**
 * A private instance. It must not be the renderer's instance: that one installs
 * a `walkTokens` hook which escapes HTML, and we need the untouched tokens.
 */
const lexer = new Marked({ gfm: true, breaks: false })

/**
 * C0/C1 control characters. `new URL` silently strips them, which is how
 * `java<TAB>script:` style obfuscation slips past a naive protocol check, so
 * they are removed before the protocol test.
 */
const CONTROL_CHARACTERS = /[\u0000-\u001F\u007F]/g
const HAS_CONTROL_CHARACTER = /[\u0000-\u001F\u007F]/

/** Entities that can hide a scheme separator or a control character in an href. */
const ENTITIES: Readonly<Record<string, string>> = {
  '&amp;': '&',
  '&colon;': ':',
  '&tab;': '\t',
  '&newline;': '\n',
  '&lt;': '<',
  '&gt;': '>',
  '&quot;': '"',
  '&#39;': "'",
  '&apos;': "'",
}

const decodeEntities = (value: string): string =>
  value
    .replace(/&#x([0-9a-f]+);/gi, (_match, hex: string) =>
      String.fromCodePoint(Number.parseInt(hex, 16)),
    )
    .replace(/&#(\d+);/g, (_match, decimal: string) =>
      String.fromCodePoint(Number.parseInt(decimal, 10)),
    )
    .replace(/&[a-z]+;|&#39;/gi, (entity) => ENTITIES[entity.toLowerCase()] ?? entity)

/**
 * `isSafeUrl` already parses with `new URL`, which rejects `javascript:`,
 * `data:` and friends. Decoding entities and dropping control characters first
 * catches the obfuscated spellings that a browser would resolve to the same
 * unsafe scheme.
 *
 * Shared with the editor extensions (`SafeLink`, `SafeImage`), so the paste
 * filter and the compatibility gate agree on what "unsafe" means. An empty
 * value is not unsafe: it simply has no destination.
 */
export function isUnsafeUrlCandidate(value: unknown): boolean {
  if (typeof value !== 'string') return false
  const decoded = decodeEntities(value).replace(CONTROL_CHARACTERS, '').trim()
  if (decoded === '') return false
  return !isSafeUrl(decoded)
}

const isUnsafeDestination = isUnsafeUrlCandidate

type AnyToken = Token | Tokens.Generic

/**
 * marked refuses to parse a destination that contains raw whitespace, so
 * `[x](java<TAB>script:alert(1))` arrives as a prose `text` token instead of a
 * `link` token. Such a construct is never a legitimate link, and the browser
 * would strip the control character and resolve it to `javascript:` — so it is
 * still reported. The scan is limited to prose tokens: fenced code (`code`) and
 * inline code (`codespan`) are separate token types and are never inspected.
 */
const inspectProseText = (text: string, reasons: Set<UnsupportedReason>): void => {
  let index = text.indexOf('](')
  while (index !== -1) {
    const start = index + 2
    const end = text.indexOf(')', start)
    if (end === -1) return
    let destination = text.slice(start, end).trim()
    if (destination.startsWith('<')) destination = destination.slice(1)
    if (destination.endsWith('>')) destination = destination.slice(0, -1)
    const titleSeparator = /^(\S+)\s+["'][\s\S]*$/.exec(destination)
    if (titleSeparator) destination = titleSeparator[1]
    if (HAS_CONTROL_CHARACTER.test(decodeEntities(destination)) && isUnsafeDestination(destination)) {
      reasons.add('unsafe-url')
    }
    index = text.indexOf('](', end)
  }
}

const collectFromChildren = (token: AnyToken, visit: (child: AnyToken) => void): void => {
  const record = token as unknown as Record<string, unknown>
  for (const key of ['tokens', 'items', 'header', 'rows']) {
    const value = record[key]
    if (!Array.isArray(value)) continue
    for (const child of value) {
      if (child && typeof child === 'object' && typeof (child as AnyToken).type === 'string') {
        visit(child as AnyToken)
      }
    }
  }
}

const inspectTokens = (tokens: AnyToken[], reasons: Set<UnsupportedReason>): void => {
  for (const token of tokens) {
    switch (token.type) {
      case 'html':
      case 'tag':
        reasons.add('raw-html')
        break
      case 'list_item':
        if ((token as Tokens.ListItem).task === true) reasons.add('task-list')
        break
      case 'link':
      case 'image':
        if (isUnsafeDestination((token as Tokens.Link).href)) reasons.add('unsafe-url')
        break
      case 'text':
        inspectProseText((token as Tokens.Text).text, reasons)
        break
      default:
        break
    }
    collectFromChildren(token, (child) => inspectTokens([child], reasons))
  }
}

/**
 * Lex the source and report which unsupported constructs it contains.
 * Everything is classified from the token tree: HTML inside a fenced block is a
 * `code` token, so it is never mistaken for raw HTML.
 */
export function inspectMarkdownSupport(source: string): MarkdownSupportReport {
  const reasons = new Set<UnsupportedReason>()
  if (typeof source !== 'string' || source.trim() === '') {
    return { supported: true, reasons: [] }
  }

  inspectTokens(lexer.lexer(source) as AnyToken[], reasons)

  const ordered = REASON_ORDER.filter((reason) => reasons.has(reason))
  return { supported: ordered.length === 0, reasons: ordered }
}
