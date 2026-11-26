import { createLowlight } from 'lowlight'
import type { LanguageFn } from 'highlight.js'
import type { Element, Root, RootContent } from 'hast'
import bash from 'highlight.js/lib/languages/bash'
import c from 'highlight.js/lib/languages/c'
import cpp from 'highlight.js/lib/languages/cpp'
import css from 'highlight.js/lib/languages/css'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import go from 'highlight.js/lib/languages/go'
import http from 'highlight.js/lib/languages/http'
import java from 'highlight.js/lib/languages/java'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import plaintextGrammar from 'highlight.js/lib/languages/plaintext'
import python from 'highlight.js/lib/languages/python'
import rust from 'highlight.js/lib/languages/rust'
import sql from 'highlight.js/lib/languages/sql'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'
import { escapeHtml } from './html'

export interface HighlightLanguage {
  id: string
  label: string
}

/**
 * The precompiled registry. Every grammar is a static import, so rendering a
 * fenced block never fetches code at runtime.
 */
const LANGUAGE_DEFINITIONS = [
  { id: 'plaintext', label: 'Plain text' },
  { id: 'python', label: 'Python' },
  { id: 'javascript', label: 'JavaScript' },
  { id: 'typescript', label: 'TypeScript' },
  { id: 'go', label: 'Go' },
  { id: 'bash', label: 'Bash' },
  { id: 'json', label: 'JSON' },
  { id: 'yaml', label: 'YAML' },
  { id: 'xml', label: 'HTML/XML' },
  { id: 'css', label: 'CSS' },
  { id: 'sql', label: 'SQL' },
  { id: 'c', label: 'C' },
  { id: 'cpp', label: 'C++' },
  { id: 'java', label: 'Java' },
  { id: 'rust', label: 'Rust' },
  { id: 'dockerfile', label: 'Dockerfile' },
  { id: 'http', label: 'HTTP' },
] as const satisfies readonly HighlightLanguage[]

export const HIGHLIGHT_LANGUAGES: readonly HighlightLanguage[] = LANGUAGE_DEFINITIONS

export const DEFAULT_CODE_LANGUAGE = 'plaintext'

/**
 * Common fence spellings people actually type. highlight.js also registers its
 * own grammar aliases; this map only pins the canonical ids we expose in the
 * picker and in the rendered `language-*` class.
 */
const LANGUAGE_ALIASES: Readonly<Record<string, string>> = {
  py: 'python',
  js: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  jsx: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  sh: 'bash',
  shell: 'bash',
  zsh: 'bash',
  yml: 'yaml',
  html: 'xml',
  htm: 'xml',
  golang: 'go',
  'c++': 'cpp',
  docker: 'dockerfile',
  text: 'plaintext',
  plain: 'plaintext',
  txt: 'plaintext',
}

const GRAMMARS: Readonly<Record<string, LanguageFn>> = {
  python,
  javascript,
  typescript,
  go,
  bash,
  json,
  yaml,
  xml,
  css,
  sql,
  c,
  cpp,
  java,
  rust,
  dockerfile,
  http,
}

export const lowlight = createLowlight({ plaintext: plaintextGrammar, ...GRAMMARS })

const SAFE_LANGUAGE_ID = /^[a-z0-9+#.-]+$/
const HLJS_CLASS_NAME = /^hljs-[a-z0-9_-]+$/

const firstWord = (value: string): string => value.trim().toLowerCase().split(/\s+/)[0] ?? ''

/**
 * Turn a Markdown fence info string into a safe registry id. Only the first
 * word is the language (standard Markdown); metadata such as `title=x` is
 * ignored, never injected into markup. Unknown but syntactically safe ids are
 * preserved so they can render as plain text with a stable `language-*` class.
 */
export function normalizeCodeLanguage(language: string | null | undefined): string {
  if (typeof language !== 'string') return DEFAULT_CODE_LANGUAGE
  const candidate = firstWord(language)
  if (candidate === '') return DEFAULT_CODE_LANGUAGE
  const canonical = LANGUAGE_ALIASES[candidate] ?? candidate
  if (canonical === DEFAULT_CODE_LANGUAGE) return DEFAULT_CODE_LANGUAGE
  return SAFE_LANGUAGE_ID.test(canonical) ? canonical : DEFAULT_CODE_LANGUAGE
}

export function isHighlightLanguage(id: string | null | undefined): boolean {
  if (typeof id !== 'string' || id.trim() === '') return false
  const normalized = normalizeCodeLanguage(id)
  return HIGHLIGHT_LANGUAGES.some((language) => language.id === normalized)
}

const classNames = (element: Element): string[] => {
  const raw = element.properties?.className
  const values = Array.isArray(raw) ? raw : []
  return values.filter(
    (value): value is string => typeof value === 'string' && HLJS_CLASS_NAME.test(value),
  )
}

const serializeChildren = (children: RootContent[]): string =>
  children.map((child) => serializeNode(child)).join('')

const serializeNode = (node: RootContent): string => {
  if (node.type === 'text') return escapeHtml(node.value)
  if (node.type !== 'element') return ''

  const inner = serializeChildren(node.children)
  // lowlight emits spans only; flatten anything else instead of inventing tags.
  if (node.tagName !== 'span') return inner

  const classes = classNames(node)
  if (classes.length === 0) return inner
  return `<span class="${classes.join(' ')}">${inner}</span>`
}

const serializeHast = (tree: Root): string => serializeChildren(tree.children)

/**
 * Escaped span HTML for the inner `<code>`, or null when the caller must render
 * escaped plain text. `plaintext`, empty input and unknown languages never call
 * highlight.js auto-detection.
 */
export function highlightCode(
  code: string,
  language: string | null | undefined,
): string | null {
  if (typeof code !== 'string' || code.length === 0) return null
  const id = normalizeCodeLanguage(language)
  if (id === DEFAULT_CODE_LANGUAGE || !lowlight.registered(id)) return null
  try {
    return serializeHast(lowlight.highlight(id, code))
  } catch {
    return null
  }
}

export interface SafeLowlight {
  highlight(language: string, value: string): Root
  highlightAuto(value: string): Root
  listLanguages(): string[]
  registered(aliasOrLanguage: string): boolean
}

const plainRoot = (value: string): Root => ({
  type: 'root',
  children: [{ type: 'text', value }],
})

/**
 * Adapter for the editor integration. It answers `registered()` truthy so the
 * upstream lowlight plugin never falls back to auto-detection, and returns a
 * plain root for unknown or plaintext languages.
 */
export const safeLowlight: SafeLowlight = {
  highlight(language: string, value: string): Root {
    const id = normalizeCodeLanguage(language)
    if (id === DEFAULT_CODE_LANGUAGE || !lowlight.registered(id)) return plainRoot(value)
    try {
      return lowlight.highlight(id, value)
    } catch {
      return plainRoot(value)
    }
  },
  highlightAuto(value: string): Root {
    return plainRoot(value)
  },
  listLanguages(): string[] {
    return lowlight.listLanguages()
  },
  registered(_aliasOrLanguage: string): boolean {
    return true
  },
}
