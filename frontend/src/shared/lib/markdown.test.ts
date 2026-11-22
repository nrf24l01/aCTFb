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
