import { describe, expect, it } from 'vitest'
import { inspectMarkdownSupport } from './markdownUnsupported'

describe('inspectMarkdownSupport', () => {
  it('accepts the supported subset', () => {
    const source = '# H\n\n**b** *i* ~~s~~ `c`\n\n- a\n  - b\n\n1. x\n\n> q\n\n```python\nprint(1)\n```\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\n![alt](/x.png)\n\n[rel](/boards)'
    expect(inspectMarkdownSupport(source)).toEqual({ supported: true, reasons: [] })
  })

  it('accepts GFM tables with alignment, link titles, images and hard breaks', () => {
    const source = '| a | b |\n| :- | -: |\n| 1 | 2 |\n\n[link](/boards "Board list") and ![img](/x.png "Logo")\n\nline one  \nline two'
    expect(inspectMarkdownSupport(source)).toEqual({ supported: true, reasons: [] })
  })

  it('flags raw HTML blocks and inline tags', () => {
    expect(inspectMarkdownSupport('<div>x</div>').reasons).toEqual(['raw-html'])
    expect(inspectMarkdownSupport('text <b>bold</b> text').reasons).toEqual(['raw-html'])
    expect(inspectMarkdownSupport('<!-- comment -->').reasons).toEqual(['raw-html'])
  })

  it('does not flag literal angle brackets in prose', () => {
    expect(inspectMarkdownSupport('1 < 2 and 3 > 2').supported).toBe(true)
    expect(inspectMarkdownSupport('use < and > literally').supported).toBe(true)
  })

  it('does not flag HTML inside a fenced code block', () => {
    expect(inspectMarkdownSupport('```html\n<div onmouseover="x">y</div>\n```').supported).toBe(true)
    expect(inspectMarkdownSupport('```\n<script>alert(1)</script>\n```').supported).toBe(true)
  })

  it('does not flag HTML or URLs inside inline code', () => {
    expect(inspectMarkdownSupport('`<script>alert(1)</script>` and `[x](javascript:1)`').supported).toBe(true)
  })

  it('does not flag unknown fence languages', () => {
    expect(inspectMarkdownSupport('```brainfuck\n+++[>+<-]\n```').supported).toBe(true)
  })

  it('flags GFM task list items', () => {
    expect(inspectMarkdownSupport('- [ ] todo\n- [x] done').reasons).toEqual(['task-list'])
  })

  it('flags unsafe link and image URLs, including obfuscated ones', () => {
    expect(inspectMarkdownSupport('[x](javascript:alert(1))').reasons).toEqual(['unsafe-url'])
    expect(inspectMarkdownSupport('![a](data:image/svg+xml;base64,PHN2Zz4=)').reasons).toEqual(['unsafe-url'])
    expect(inspectMarkdownSupport('[x](java\tscript:alert(1))').reasons).toEqual(['unsafe-url'])
    expect(inspectMarkdownSupport('[x](vbscript:msgbox(1))').reasons).toEqual(['unsafe-url'])
  })

  it('accepts safe protocols and same-origin paths', () => {
    expect(inspectMarkdownSupport('[a](https://example.com) [b](mailto:a@example.com) [c](/boards)').supported).toBe(true)
    expect(inspectMarkdownSupport('<https://example.com>').supported).toBe(true)
  })

  it('reports every distinct reason once', () => {
    const source = '<div>x</div>\n\n- [ ] todo\n\n[x](javascript:alert(1))'
    expect(inspectMarkdownSupport(source)).toEqual({
      supported: false,
      reasons: ['raw-html', 'task-list', 'unsafe-url'],
    })
  })
})
