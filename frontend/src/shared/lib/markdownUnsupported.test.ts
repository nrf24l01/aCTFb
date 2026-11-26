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

/**
 * GFM table cells. `marked` shapes a table as `header: Token[]` and
 * `rows: Token[][]`, and a *cell* is a token-shaped object with `tokens` but no
 * `type` field. A walk that requires `type` therefore skips every cell — the
 * whole table silently bypassed the gate and opened in the visual editor.
 */
describe('table cells are inspected', () => {
  it('flags raw HTML in a body cell', () => {
    expect(inspectMarkdownSupport('| a |\n| - |\n| <div>x</div> |').reasons).toEqual(['raw-html'])
  })

  it('flags raw HTML in a header cell', () => {
    expect(inspectMarkdownSupport('| <b>h</b> |\n| - |\n| ok |').reasons).toEqual(['raw-html'])
  })

  it('flags an unsafe link in a body cell', () => {
    expect(inspectMarkdownSupport('| a |\n| - |\n| [x](javascript:1) |').reasons).toEqual(['unsafe-url'])
  })

  it('flags a data: image in a body cell', () => {
    expect(inspectMarkdownSupport('| a |\n| - |\n| ![i](data:image/png;base64,AA) |').reasons)
      .toEqual(['unsafe-url'])
  })

  it('flags an entity-obfuscated unsafe link in a body cell', () => {
    expect(inspectMarkdownSupport('| a |\n| - |\n| [x](java&#9;script:1) |').reasons)
      .toEqual(['unsafe-url'])
  })

  it('finds a nested image and link inside a cell', () => {
    expect(inspectMarkdownSupport('| a |\n| - |\n| [![alt](/x.png)](/boards) |'))
      .toEqual({ supported: true, reasons: [] })
    expect(inspectMarkdownSupport('| a |\n| - |\n| [![a](data:image/png;base64,AA)](/boards) |').reasons)
      .toEqual(['unsafe-url'])
  })

  it('flags both problems when one cell has a link and an HTML tag', () => {
    expect(inspectMarkdownSupport('| a |\n| - |\n| [x](javascript:1) and <b>h</b> |').reasons)
      .toEqual(['raw-html', 'unsafe-url'])
  })

  // GFM lexes `- [ ]` inside a table cell as literal text, not a task-list item,
  // so there is nothing to lose and the cell is legitimately supported.
  it('keeps a task-list-looking cell as literal text', () => {
    expect(inspectMarkdownSupport('| a |\n| - |\n| - [ ] todo |'))
      .toEqual({ supported: true, reasons: [] })
  })

  it('still accepts a table whose cells hold only supported inline content', () => {
    const source = '| **a** | `b` |\n| :- | -: |\n| [link](/boards "t") | ![i](/x.png) |'
    expect(inspectMarkdownSupport(source)).toEqual({ supported: true, reasons: [] })
  })

  it('does not flag the same payload when it is inside a fenced block', () => {
    const fenced = [
      '```markdown',
      '| a |',
      '| - |',
      '| <div>x</div> |',
      '| [x](javascript:1) |',
      '| - [ ] todo |',
      '```',
    ].join('\n')
    expect(inspectMarkdownSupport(fenced)).toEqual({ supported: true, reasons: [] })
  })
})

/**
 * Entity decoding must never throw. `String.fromCodePoint` raises `RangeError`
 * above `0x10FFFF`, and `marked` passes such a destination straight through, so
 * `[x](&#x110000;)` used to crash `inspectMarkdownSupport` — which runs in
 * component setup and on every mode switch.
 */
describe('bounded entity decoding', () => {
  it('does not throw on an out-of-range hexadecimal entity', () => {
    expect(() => inspectMarkdownSupport('[x](&#x110000;)')).not.toThrow()
  })

  it('does not throw on an out-of-range decimal entity', () => {
    expect(() => inspectMarkdownSupport('[x](&#9999999999;)')).not.toThrow()
    expect(() => inspectMarkdownSupport('[x](&#1114112;)')).not.toThrow()
  })

  it('does not throw on a surrogate code point or a negative numeric reference', () => {
    expect(() => inspectMarkdownSupport('[x](&#xD800;)')).not.toThrow()
    expect(() => inspectMarkdownSupport('[x](&#-1;)')).not.toThrow()
    expect(() => inspectMarkdownSupport('[x](&#;)')).not.toThrow()
  })

  it('treats an undecodable destination as unsafe rather than as safe text', () => {
    expect(inspectMarkdownSupport('[x](&#x110000;)').reasons).toEqual(['unsafe-url'])
    expect(inspectMarkdownSupport('[x](&#xD800;)').reasons).toEqual(['unsafe-url'])
  })

  it('still decodes a valid numeric entity before judging the scheme', () => {
    expect(inspectMarkdownSupport('[x](javascript&#58;alert(1))').reasons).toEqual(['unsafe-url'])
    expect(inspectMarkdownSupport('[x](java&#115;cript:alert(1))').reasons).toEqual(['unsafe-url'])
  })

  it('leaves a normal relative path alone', () => {
    expect(inspectMarkdownSupport('[x](/boards?q=1&amp;r=2)').supported).toBe(true)
  })
})
