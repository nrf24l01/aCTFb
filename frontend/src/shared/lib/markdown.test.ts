import { describe, expect, it } from 'vitest'
import { renderMarkdown } from './markdown'

describe('renderMarkdown', () => {
  it('renders basic markdown', () => {
    const html = renderMarkdown('# Title\n\nSome **bold** text and a [link](https://example.test).')
    expect(html).toContain('Title')
    expect(html).toContain('<strong>bold</strong>')
    expect(html).toContain('href="https://example.test"')
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
