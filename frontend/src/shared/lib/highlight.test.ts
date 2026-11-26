import { describe, expect, it, vi } from 'vitest'
import {
  DEFAULT_CODE_LANGUAGE,
  HIGHLIGHT_LANGUAGES,
  highlightCode,
  isHighlightLanguage,
  lowlight,
  normalizeCodeLanguage,
  safeLowlight,
} from './highlight'

const parseHtml = (html: string): HTMLElement => {
  const host = document.createElement('div')
  host.innerHTML = html
  return host
}

describe('highlight registry', () => {
  it('offers plaintext plus the common CTF languages', () => {
    const ids = HIGHLIGHT_LANGUAGES.map((language) => language.id)
    expect(ids[0]).toBe('plaintext')
    expect(DEFAULT_CODE_LANGUAGE).toBe('plaintext')
    expect(ids).toEqual(
      expect.arrayContaining([
        'python',
        'javascript',
        'typescript',
        'go',
        'bash',
        'json',
        'yaml',
        'xml',
        'css',
        'sql',
        'c',
        'cpp',
        'java',
        'rust',
        'dockerfile',
        'http',
      ]),
    )
  })

  it('recognises registry ids, common aliases and plaintext', () => {
    expect(isHighlightLanguage('python')).toBe(true)
    expect(isHighlightLanguage('py')).toBe(true)
    expect(isHighlightLanguage('plaintext')).toBe(true)
    expect(isHighlightLanguage('brainfuck')).toBe(false)
    expect(isHighlightLanguage(null)).toBe(false)
    expect(normalizeCodeLanguage('py')).toBe('python')
    expect(normalizeCodeLanguage('js')).toBe('javascript')
    expect(normalizeCodeLanguage('sh')).toBe('bash')
    expect(normalizeCodeLanguage('html')).toBe('xml')
    expect(normalizeCodeLanguage('python title=x')).toBe('python')
    expect(normalizeCodeLanguage('python" onmouseover="alert(1)')).toBe('plaintext')
  })
})

describe('highlightCode', () => {
  it('returns escaped hljs spans for a registered language', () => {
    const html = highlightCode('def f():\n  return 1', 'python')
    expect(html).toContain('hljs-keyword')
    expect(html).toContain('def')
  })

  it('supports the natural fence aliases py, js, sh and html', () => {
    expect(highlightCode('def f():\n  return 1', 'py')).toContain('hljs-keyword')
    expect(highlightCode('const a = 1', 'js')).toContain('hljs-keyword')
    expect(highlightCode('echo hello', 'sh')).toContain('hljs-built_in')
    expect(highlightCode('<div class="x">y</div>', 'html')).toContain('hljs-tag')
  })

  it('returns null for plaintext, empty, and unknown languages', () => {
    expect(highlightCode('anything', 'plaintext')).toBeNull()
    expect(highlightCode('anything', 'brainfuck')).toBeNull()
    expect(highlightCode('anything', null)).toBeNull()
    expect(highlightCode('', 'python')).toBeNull()
  })

  it('escapes markup inside the code instead of emitting it', () => {
    const source = '<img src=x onerror="alert(1)">'
    const html = highlightCode(source, 'xml') ?? ''
    expect(html).not.toContain('<img')
    expect(html).toContain('&lt;')
    expect(html).toContain('&quot;alert(1)&quot;')
    expect(html).not.toMatch(/onerror="alert/)
    const host = parseHtml(html)
    expect(host.querySelector('img')).toBeNull()
    expect(host.textContent).toBe(source)
  })

  it('only emits span elements with hljs- classes', () => {
    const html = highlightCode('const a = 1', 'javascript') ?? ''
    const host = parseHtml(html)
    const elements = Array.from(host.querySelectorAll('*'))
    expect(elements.length).toBeGreaterThan(0)
    for (const element of elements) {
      expect(element.tagName).toBe('SPAN')
      expect(element.classList.length).toBeGreaterThan(0)
      for (const className of element.classList) {
        expect(className).toMatch(/^hljs-[a-z0-9_-]+$/)
      }
    }
    expect(host.textContent).toBe('const a = 1')
  })

  it('drops the secondary unprefixed hljs-title class but keeps hljs-title', () => {
    const html = highlightCode('def f():\n  return 1', 'python') ?? ''
    const host = parseHtml(html)
    const title = host.querySelector('.hljs-title')
    expect(title).not.toBeNull()
    expect(title?.className).toBe('hljs-title')
    expect(html).not.toContain('function_')
  })
})

describe('safeLowlight', () => {
  it('never auto-detects: unknown languages return a plain root', () => {
    const spy = vi.spyOn(lowlight, 'highlightAuto')
    expect(safeLowlight.registered('not-a-language')).toBe(true)
    const tree = safeLowlight.highlight('not-a-language', 'x = 1')
    expect(tree).toEqual({ type: 'root', children: [{ type: 'text', value: 'x = 1' }] })
    expect(JSON.stringify(tree)).not.toContain('hljs-')
    expect(spy).not.toHaveBeenCalled()
    expect(JSON.stringify(safeLowlight.highlightAuto('x = 1'))).not.toContain('hljs-')
    expect(spy).not.toHaveBeenCalled()
  })

  it('delegates known languages to lowlight without auto-detection', () => {
    const spy = vi.spyOn(lowlight, 'highlightAuto')
    const tree = safeLowlight.highlight('python', 'def f():\n  return 1')
    expect(JSON.stringify(tree)).toContain('hljs-keyword')
    expect(spy).not.toHaveBeenCalled()
    expect(safeLowlight.listLanguages()).toContain('python')
  })
})
