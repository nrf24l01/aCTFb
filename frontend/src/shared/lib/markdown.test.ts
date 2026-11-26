import { describe, expect, it } from 'vitest'
import { renderMarkdown } from './markdown'

describe('renderMarkdown', () => {
  it('renders basic markdown', () => {
    const html = renderMarkdown('# Title\n\nSome **bold** text and a [link](https://example.test).')
    expect(html).toContain('Title')
    expect(html).toContain('<strong>bold</strong>')
    expect(html).toContain('href="https://example.test"')
  })

  // Block-level tags must survive sanitisation, because a flat text blob is the
  // regression this guards. happy-dom's serialiser drops the tag of the very
  // first top-level node (a DOM-fidelity quirk, not DOMPurify behaviour), so the
  // assertions target nodes after the first. The real-browser acceptance spec
  // asserts the leading heading as a real element.
  it('keeps block-level elements as real markup', () => {
    const html = renderMarkdown('Intro paragraph.\n\n## Section\n\n- one\n- two')
    expect(html).toContain('Intro paragraph.')
    expect(html).toContain('<h2>Section</h2>')
    expect(html).toContain('<li>one</li>')
    expect(html).toContain('<li>two</li>')
  })

  it('escapes raw HTML instead of executing it', () => {
    const html = renderMarkdown('<script>alert(1)</script>\n\n<img src=x onerror="alert(1)">')
    expect(html).not.toMatch(/<script/i)
    expect(html).not.toMatch(/<img[^>]*onerror/i)
    expect(html).toContain('&lt;script&gt;')
  })

  it('rejects javascript and data URL protocols', () => {
    const html = renderMarkdown('[click](javascript:alert(1)) [x](data:text/html;base64,PHN2Zz4=)')
    expect(html).not.toMatch(/href="javascript:/i)
    expect(html).not.toMatch(/href="data:/i)
  })

  it('strips svg and event attributes', () => {
    const html = renderMarkdown('<svg><a xlink:href="javascript:alert(1)">x</a></svg>')
    expect(html).not.toMatch(/<svg/i)
    expect(html).not.toMatch(/<a[^>]*xlink/i)
  })

  it('keeps relative links and mailto', () => {
    const html = renderMarkdown('[rel](/boards) [mail](mailto:a@b.test)')
    expect(html).toContain('href="/boards"')
    expect(html).toContain('href="mailto:a@b.test"')
  })
})

const parseRendered = (html: string): HTMLElement => {
  const host = document.createElement('div')
  host.innerHTML = html
  return host
}

describe('renderMarkdown syntax highlighting', () => {
  it('highlights a fenced block with a registered language', () => {
    const html = renderMarkdown('```python\ndef f():\n  return 1\n```')
    expect(html).toContain('class="language-python"')
    expect(html).toContain('hljs-keyword')
    const host = parseRendered(html)
    const code = host.querySelector('code.language-python')
    expect(code).not.toBeNull()
    expect(code?.querySelector('.hljs-keyword')).not.toBeNull()
    expect(code?.textContent).toBe('def f():\n  return 1')
  })

  it('renders an unknown or missing language as escaped plain text', () => {
    const unknown = renderMarkdown('```brainfuck\n+++[>+<-]\n```')
    const missing = renderMarkdown('```\n<a href="x">y</a>\n```')
    expect(unknown).not.toContain('hljs-')
    expect(missing).not.toContain('hljs-')
    expect(missing).toContain('&lt;a href=')
    const host = parseRendered(missing)
    expect(host.querySelector('a')).toBeNull()
    expect(host.textContent?.trimEnd()).toBe('<a href="x">y</a>')
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
    const host = parseRendered(html)
    expect(host.querySelector('[onmouseover]')).toBeNull()
    for (const element of host.querySelectorAll('*')) {
      for (const className of element.classList) {
        expect(className).toMatch(/^(?:language-[a-z0-9+#.-]+|hljs-[a-z0-9_-]+)$/)
      }
    }
  })

  it('still keeps tables, images, strikethrough and all heading levels', () => {
    // A leading paragraph keeps the table from being the first top-level node,
    // which happy-dom's serialiser drops (see the block-level test above).
    const html = renderMarkdown(
      'Intro\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\n![alt](/x.png "t")\n\n~~gone~~\n\n#### four',
    )
    expect(html).toContain('<table>')
    expect(html).toContain('src="/x.png"')
    expect(html).toContain('<del>gone</del>')
    expect(html).toContain('<h4>four</h4>')
  })

  it('escapes a raw span so no highlighter class reaches the DOM', () => {
    const html = renderMarkdown('<span class="hljs-evil">x</span>')
    const host = parseRendered(html)
    expect(host.querySelector('span')).toBeNull()
    expect(host.querySelector('.hljs-evil')).toBeNull()
    expect(host.textContent).toContain('<span class="hljs-evil">x</span>')
  })

  it('maps common fence aliases and ignores info-string metadata safely', () => {
    const aliased = renderMarkdown('```py\ndef f():\n  return 1\n```')
    expect(aliased).toContain('class="language-python"')
    expect(aliased).toContain('hljs-keyword')

    const metadata = renderMarkdown('```python title=x\ndef f():\n  return 1\n```')
    expect(metadata).toContain('class="language-python"')
    expect(metadata).toContain('hljs-keyword')
    expect(metadata).not.toContain('title=x')
  })

  it('keeps generated tags, attributes and classes inside the allowlist', () => {
    const html = renderMarkdown(
      'Intro\n\n```python\ndef f():\n  return "<img src=x onerror=alert(1)>"\n```\n\n| a | b |\n| - | - |\n| 1 | 2 |',
    )
    const host = parseRendered(html)
    const allowedTags = new Set([
      'P',
      'PRE',
      'CODE',
      'SPAN',
      'TABLE',
      'THEAD',
      'TBODY',
      'TR',
      'TH',
      'TD',
    ])
    const allowedAttributes = new Set(['class', 'href', 'title', 'alt', 'src', 'colspan', 'rowspan'])
    const elements = Array.from(host.querySelectorAll('*'))
    expect(elements.length).toBeGreaterThan(3)
    for (const element of elements) {
      expect(allowedTags.has(element.tagName)).toBe(true)
      for (const attribute of element.getAttributeNames()) {
        expect(allowedAttributes.has(attribute)).toBe(true)
      }
      for (const className of element.classList) {
        expect(className === 'language-python' || /^hljs-[a-z0-9_-]+$/.test(className)).toBe(true)
      }
    }
    expect(host.querySelector('img')).toBeNull()
    expect(host.textContent).toContain('<img src=x onerror=alert(1)>')
  })
})
