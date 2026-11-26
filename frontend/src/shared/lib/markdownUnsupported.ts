import { Marked } from 'marked'
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

/** Highest valid Unicode code point (and the end of the basic plane). */
const MAX_CODE_POINT = 0x10_FFFF
const HIGH_SURROGATE_START = 0xD800
const LOW_SURROGATE_END = 0xDFFF

/**
 * A numeric reference that is not a scalar value cannot be decoded, and it is
 * not something a browser would show either: it is a malformed destination.
 * Returning it unchanged would let `[x](&#x110000;)` look like a harmless
 * relative path, so the caller treats it as unsafe instead.
 */
const UNDECODABLE = Symbol('undecodable')

const decodeCodePoint = (raw: string, radix: number): string | typeof UNDECODABLE => {
  const codePoint = Number.parseInt(raw, radix)
  if (!Number.isFinite(codePoint) || !Number.isInteger(codePoint)) return UNDECODABLE
  if (codePoint < 0 || codePoint > MAX_CODE_POINT) return UNDECODABLE
  // A lone surrogate is not a valid character; refuse instead of emitting it.
  if (codePoint >= HIGH_SURROGATE_START && codePoint <= LOW_SURROGATE_END) return UNDECODABLE
  return String.fromCodePoint(codePoint)
}

interface DecodedDestination {
  value: string
  /** True when a numeric reference could not be decoded into a scalar value. */
  invalid: boolean
}

/**
 * Decode the entity spellings a browser would honour, without ever throwing.
 * `String.fromCodePoint` raises `RangeError` above `0x10FFFF`; a stored
 * description containing such a reference used to crash the component on mount.
 */
const decodeEntities = (value: string): DecodedDestination => {
  let invalid = false
  const replaceNumeric = (raw: string, radix: number): string => {
    const decoded = decodeCodePoint(raw, radix)
    if (decoded === UNDECODABLE) {
      invalid = true
      return raw
    }
    return decoded
  }
  const decoded = value
    .replace(/&#x([0-9a-f]+);/gi, (_match, hex: string) => replaceNumeric(hex, 16))
    .replace(/&#(\d+);/g, (_match, decimal: string) => replaceNumeric(decimal, 10))
    .replace(/&[a-z]+;/gi, (entity) => ENTITIES[entity.toLowerCase()] ?? entity)
  return { value: decoded, invalid }
}

/**
 * `isSafeUrl` already parses with `new URL`, which rejects `javascript:`,
 * `data:` and friends. Decoding entities and dropping control characters first
 * catches the obfuscated spellings that a browser would resolve to the same
 * unsafe scheme.
 *
 * Shared with the editor extensions (`SafeLink`, `SafeImage`), so the paste
 * filter and the compatibility gate agree on what "unsafe" means. An empty
 * value is not unsafe: it simply has no destination. A destination that cannot
 * be decoded is treated as unsafe.
 */
export function isUnsafeUrlCandidate(value: unknown): boolean {
  if (typeof value !== 'string') return false
  const decoded = decodeEntities(value)
  if (decoded.invalid) return true
  const canonical = decoded.value.replace(CONTROL_CHARACTERS, '').trim()
  if (canonical === '') return false
  return !isSafeUrl(canonical)
}

const isUnsafeDestination = isUnsafeUrlCandidate

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
    const decoded = decodeEntities(destination)
    if (decoded.invalid) {
      reasons.add('unsafe-url')
    } else if (HAS_CONTROL_CHARACTER.test(decoded.value) && isUnsafeDestination(destination)) {
      reasons.add('unsafe-url')
    }
    index = text.indexOf('](', end)
  }
}

/**
 * Child arrays that can hold further tokens. `marked` nests them: list items
 * live in `items`, table cells in `header` and in `rows`, and `rows` is an
 * array of arrays.
 */
const CHILD_KEYS = ['tokens', 'items', 'header', 'rows'] as const

/**
 * Walk the token tree by shape rather than by `type`.
 *
 * A table *cell* is a token-shaped object with `tokens` but no `type` field, so
 * requiring `type` skipped every cell: a table row could carry raw HTML, an
 * unsafe URL or a `data:` image straight into the visual editor with no
 * warning. Arrays are flattened at any depth for the same reason.
 *
 * Classification stays type-based, so `code` and `codespan` payloads (which have
 * no child arrays) are never inspected as prose — HTML inside a fence remains
 * supported.
 */
const inspectNode = (node: unknown, reasons: Set<UnsupportedReason>): void => {
  if (Array.isArray(node)) {
    for (const child of node) inspectNode(child, reasons)
    return
  }
  if (!node || typeof node !== 'object') return

  const record = node as Record<string, unknown>
  switch (typeof record.type === 'string' ? record.type : '') {
    case 'html':
    case 'tag':
      reasons.add('raw-html')
      break
    case 'list_item':
      if (record.task === true) reasons.add('task-list')
      break
    case 'link':
    case 'image':
      if (isUnsafeDestination(record.href)) reasons.add('unsafe-url')
      break
    case 'text':
      if (typeof record.text === 'string') inspectProseText(record.text, reasons)
      break
    default:
      break
  }

  for (const key of CHILD_KEYS) {
    const child = record[key]
    if (Array.isArray(child)) inspectNode(child, reasons)
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

  inspectNode(lexer.lexer(source), reasons)

  const ordered = REASON_ORDER.filter((reason) => reasons.has(reason))
  return { supported: ordered.length === 0, reasons: ordered }
}
