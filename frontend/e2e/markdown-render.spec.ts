import { expect, test, type Page } from '@playwright/test'

/**
 * Real-Chromium verification for the shared Markdown renderer and the real
 * editor component. No backend and no API interception: the fixture mounts the
 * actual `MarkdownEditor` with the actual app stylesheet, so computed colours,
 * selection and paste behaviour are the product's, not a mock's.
 *
 * happy-dom does not exercise DOMPurify or ProseMirror selection, so these are
 * the only proofs for sanitisation, highlighting contrast and paste filtering.
 */

const FIXTURE = '/fixtures/markdown-editor/index.html'

const fixtureUrl = (content?: string): string =>
  content === undefined ? FIXTURE : `${FIXTURE}?content=${encodeURIComponent(content)}`

async function openFixture(page: Page, content?: string): Promise<void> {
  await page.goto(fixtureUrl(content))
  await expect(page.locator('.markdown-editor')).toBeVisible()
}

async function waitForVisualEditor(page: Page): Promise<void> {
  await expect(page.locator('.ProseMirror')).toBeVisible()
}

/** Focus the editor and replace its content with plain typing. */
async function replaceVisualText(page: Page, text: string): Promise<void> {
  await page.locator('.ProseMirror').click()
  await page.keyboard.press('Control+a')
  await page.keyboard.type(text)
}

/** Paste HTML into ProseMirror the way a real clipboard paste arrives. */
async function pasteHtml(page: Page, html: string, text = 'x'): Promise<void> {
  await page.locator('.ProseMirror').evaluate((element, payload) => {
    const data = new DataTransfer()
    data.setData('text/html', payload.html)
    data.setData('text/plain', payload.text)
    element.dispatchEvent(new ClipboardEvent('paste', {
      bubbles: true,
      cancelable: true,
      clipboardData: data,
    }))
  }, { html, text })
}

async function sourceValue(page: Page): Promise<string> {
  return page.locator('textarea').inputValue()
}

/** Seed the fixture's real reader (`.markdown-body`) and wait for it. */
async function openReader(page: Page, content: string): Promise<void> {
  await page.goto(fixtureUrl(content))
  await expect(page.locator('#fixture-reader')).toBeVisible()
  await expect(page.locator('#fixture-reader > *').first()).toBeVisible()
}

const READER_CONTENT = [
  '# Reader heading',
  '',
  'Body paragraph with a [link](https://example.com) and `inline code`.',
  '',
  '## Second heading',
  '',
  '- bullet one',
  '- bullet two',
  '  - nested bullet',
  '',
  '1. first',
  '2. second',
  '',
  '> quoted text',
  '',
  '| left | middle | right |',
  '| :-- | :-: | --: |',
  '| a | b | c |',
  '',
  '```python',
  'def f():',
  '  return 1',
  '```',
].join('\n')

function srgbToLinear(channel: number): number {
  const c = channel / 255
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}

function parseColor(value: string): { r: number; g: number; b: number } {
  const match = /rgba?\(([^)]+)\)/.exec(value)
  if (!match) throw new Error(`cannot parse colour: ${value}`)
  const parts = match[1].split(/[,\s/]+/).filter(Boolean).map(Number)
  return { r: parts[0], g: parts[1], b: parts[2] }
}

function luminance(color: { r: number; g: number; b: number }): number {
  return 0.2126 * srgbToLinear(color.r) + 0.7152 * srgbToLinear(color.g) + 0.0722 * srgbToLinear(color.b)
}

function contrast(foreground: string, background: string): number {
  const hi = Math.max(luminance(parseColor(foreground)), luminance(parseColor(background)))
  const lo = Math.min(luminance(parseColor(foreground)), luminance(parseColor(background)))
  return (hi + 0.05) / (lo + 0.05)
}

test.describe('shared Markdown renderer', () => {
  test('renders highlighted, escaped code in a real browser', async ({ page }) => {
    await page.goto(FIXTURE)
    const html = await page.evaluate(async () => {
      const mod = await import('/src/shared/lib/markdown.ts')
      return mod.renderMarkdown(
        '```python\ndef f():\n  return 1\n```\n\n```\n<script>alert(1)</script>\n```\n\n```brainfuck\n+++\n```',
      )
    })

    expect(html).toContain('class="language-python"')
    expect(html).toContain('hljs-keyword')
    expect(html).not.toContain('<script')

    await page.evaluate((markup) => {
      document.body.innerHTML = `<div class="markdown-body">${markup}</div>`
    }, html)

    const color = await page.locator('.hljs-keyword').first().evaluate((element) => getComputedStyle(element).color)
    expect(color).not.toBe('rgb(224, 222, 244)')
  })

  test('sanitiser removes forbidden attributes and classes in Chromium', async ({ page }) => {
    await page.goto(FIXTURE)
    const html = await page.evaluate(async () => {
      const mod = await import('/src/shared/lib/markdown.ts')
      return mod.renderMarkdown([
        '<img src=x onerror="alert(1)">',
        '<span class="hljs-evil" style="color:red" onclick="alert(1)">x</span>',
        '```python" class="hljs-evil\ndef f():\n  return 1\n```',
        '```python\ndef f():\n  return 1\n```',
      ].join('\n\n'))
    })
    await page.evaluate((markup) => {
      document.body.innerHTML = `<div class="markdown-body">${markup}</div>`
    }, html)

    expect(await page.locator('.markdown-body script, .markdown-body [onerror], .markdown-body [onclick], .markdown-body [style]').count()).toBe(0)
    expect(await page.locator('.markdown-body .hljs-evil').count()).toBe(0)
    expect(await page.locator('.markdown-body span[class]:not([class^="hljs-"])').count()).toBe(0)
    // The legitimate highlighter class must survive the same pass.
    expect(await page.locator('.markdown-body .hljs-keyword').count()).toBeGreaterThan(0)
  })

  test('keeps generated highlighter contrast at or above 4.5:1', async ({ page }) => {
    await page.goto(FIXTURE)
    const html = await page.evaluate(async () => {
      const mod = await import('/src/shared/lib/markdown.ts')
      return mod.renderMarkdown('```python\ndef f():\n  return "value"\n```')
    })
    await page.evaluate((markup) => {
      document.body.innerHTML = `<div class="markdown-body">${markup}</div>`
    }, html)

    const colors = await page.locator('.hljs-keyword').first().evaluate((element) => ({
      foreground: getComputedStyle(element).color,
      background: getComputedStyle(element.closest('pre')!).backgroundColor,
    }))
    expect(colors.foreground).not.toBe('rgb(224, 222, 244)')
    expect(contrast(colors.foreground, colors.background)).toBeGreaterThanOrEqual(4.5)
  })

  test('renders a code payload literally with only generated classes', async ({ page }) => {
    await page.goto(FIXTURE)
    const html = await page.evaluate(async () => {
      const mod = await import('/src/shared/lib/markdown.ts')
      return mod.renderMarkdown(
        '```python\ndef f():\n  return "<script>alert(1)</script>"\n```\n\n```\n<img src=x onerror=alert(1)>\n```',
      )
    })
    await page.evaluate((markup) => {
      document.body.innerHTML = `<div class="markdown-body">${markup}</div>`
    }, html)

    expect(await page.locator('.markdown-body script, .markdown-body img, .markdown-body [onerror], .markdown-body [style]').count()).toBe(0)
    const text = await page.locator('code.language-python').innerText()
    expect(text).toContain('<script>alert(1)</script>')
    const classes = await page.locator('.markdown-body [class]').evaluateAll((elements) =>
      elements.flatMap((element) => Array.from(element.classList)),
    )
    for (const className of classes) {
      expect(className).toMatch(/^(?:language-[a-z0-9+#.-]+|hljs-[a-z0-9_-]+)$/)
    }
  })

  test('renders GFM table alignment as real cell semantics', async ({ page }) => {
    await page.goto(FIXTURE)
    const html = await page.evaluate(async () => {
      const mod = await import('/src/shared/lib/markdown.ts')
      return mod.renderMarkdown('Intro\n\n| l | c | r |\n| :-- | :-: | --: |\n| 1 | 2 | 3 |')
    })
    await page.evaluate((markup) => {
      document.body.innerHTML = `<div class="markdown-body">${markup}</div>`
    }, html)

    const alignments = await page.locator('td').evaluateAll((cells) =>
      cells.map((cell) => getComputedStyle(cell).textAlign.replace(/^-webkit-/, '')),
    )
    expect(alignments).toEqual(['left', 'center', 'right'])
    expect(await page.locator('th[align]').count()).toBe(3)
    expect(await page.locator('.markdown-body [style]').count()).toBe(0)
  })

  test('never turns a crafted align in raw HTML into a cell attribute', async ({ page }) => {
    await page.goto(FIXTURE)
    const html = await page.evaluate(async () => {
      const mod = await import('/src/shared/lib/markdown.ts')
      return mod.renderMarkdown('<table><tr><td align="evil" style="color:red">x</td></tr></table>')
    })
    await page.evaluate((markup) => {
      document.body.innerHTML = `<div class="markdown-body">${markup}</div>`
    }, html)

    // Raw HTML is escaped by `marked` before the sanitiser runs, so this is a
    // raw-HTML-escaping proof, not an alignment-hook proof. The hook itself is
    // covered by the direct sanitiser test below.
    expect(await page.locator('.markdown-body table, .markdown-body [align], .markdown-body [style]').count()).toBe(0)
    await expect(page.locator('.markdown-body')).toContainText('<table>')
  })

  test('sanitiser hook keeps only validated table alignment on real cells', async ({ page }) => {
    await page.goto(FIXTURE)
    const sanitized = await page.evaluate(async () => {
      const mod = await import('/src/shared/lib/markdown.ts')
      // Sanitising markup directly (not through `marked`) is the only way the
      // hook sees a crafted `align`: the renderer never emits an invalid one.
      return mod.sanitizeMarkdownHtml([
        '<table><tbody><tr>',
        '<td align="evil" style="color:red">x</td>',
        '<td align="center">y</td>',
        '<td align="right">z</td>',
        '<th align="left" onclick="alert(1)">h</th>',
        '</tr></tbody></table>',
        '<p align="center">not a cell</p>',
        '<span align="left" class="evil">span</span>',
        '<div class="markdown-table-scroll" style="color:red">generated wrapper</div>',
        '<div class="evil">crafted div</div>',
      ].join(''))
    })

    await page.evaluate((markup) => {
      document.body.innerHTML = `<div class="markdown-body">${markup}</div>`
    }, sanitized)

    // Invalid value, non-cell tag, style and event handler are all removed.
    expect(await page.locator('.markdown-body [align="evil"]').count()).toBe(0)
    expect(await page.locator('.markdown-body [style], .markdown-body [onclick]').count()).toBe(0)
    expect(await page.locator('.markdown-body p[align]').count()).toBe(0)
    expect(await page.locator('.markdown-body span[align]').count()).toBe(0)
    expect(await page.locator('.markdown-body .evil').count()).toBe(0)
    // The only class the sanitiser keeps on a div is our own generated wrapper,
    // and even that div cannot carry a style attribute.
    expect(await page.locator('.markdown-body div.markdown-table-scroll').count()).toBe(1)
    expect(await page.locator('.markdown-body div[style]').count()).toBe(0)
    // Valid cell alignment survives the same pass.
    expect(await page.locator('.markdown-body td[align="center"]').count()).toBe(1)
    expect(await page.locator('.markdown-body td[align="right"]').count()).toBe(1)
    expect(await page.locator('.markdown-body th[align="left"]').count()).toBe(1)
  })

  test('gives the reader real block typography, not flat text', async ({ page }) => {
    await openReader(page, READER_CONTENT)

    const size = async (selector: string): Promise<number> =>
      page.locator(`#fixture-reader ${selector}`).first().evaluate((element) =>
        Number.parseFloat(getComputedStyle(element).fontSize),
      )
    const body = await size('p')
    const h1 = await size('h1')
    const h2 = await size('h2')

    // Headings are distinguishable from body text, scaled h1 > h2 > body.
    expect(h1).toBeGreaterThan(body)
    expect(h2).toBeGreaterThan(body)
    expect(h1).toBeGreaterThan(h2)
    expect(await page.locator('#fixture-reader h1').first().evaluate((element) =>
      Number.parseInt(getComputedStyle(element).fontWeight, 10))).toBeGreaterThanOrEqual(600)
    expect(await page.locator('#fixture-reader h2').first().evaluate((element) =>
      Number.parseInt(getComputedStyle(element).fontWeight, 10))).toBeGreaterThanOrEqual(600)

    // Body rhythm: line height above 1.4 and block spacing between paragraphs.
    const lineHeightRatio = await page.locator('#fixture-reader p').first().evaluate((element) =>
      Number.parseFloat(getComputedStyle(element).lineHeight) / Number.parseFloat(getComputedStyle(element).fontSize))
    expect(lineHeightRatio).toBeGreaterThanOrEqual(1.4)
    expect(await page.locator('#fixture-reader h2').first().evaluate((element) =>
      Number.parseFloat(getComputedStyle(element).marginTop))).toBeGreaterThan(0)

    // Lists keep visible markers and an indent; nested lists are indented too.
    const ul = page.locator('#fixture-reader ul').first()
    expect(await ul.evaluate((element) => getComputedStyle(element).listStyleType)).toBe('disc')
    expect(await ul.evaluate((element) => Number.parseFloat(getComputedStyle(element).paddingLeft))).toBeGreaterThan(0)
    expect(await page.locator('#fixture-reader ol').first().evaluate((element) =>
      getComputedStyle(element).listStyleType)).toBe('decimal')
    expect(await page.locator('#fixture-reader li ul').first().evaluate((element) =>
      Number.parseFloat(getComputedStyle(element).paddingLeft))).toBeGreaterThan(0)

    // Blockquote and links are visually distinct without relying on colour alone.
    expect(await page.locator('#fixture-reader blockquote').first().evaluate((element) =>
      Number.parseFloat(getComputedStyle(element).borderLeftWidth))).toBeGreaterThanOrEqual(2)
    const link = page.locator('#fixture-reader a').first()
    expect(await link.evaluate((element) => getComputedStyle(element).textDecorationLine)).toContain('underline')
    expect(await link.evaluate((element) => getComputedStyle(element).color)).toBe('rgb(156, 207, 216)')

    // Inline code and fenced blocks keep their own surface, and code scrolls
    // locally instead of widening the document.
    const inlineCode = page.locator('#fixture-reader p code').first()
    expect(await inlineCode.evaluate((element) => getComputedStyle(element).backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
    expect(await inlineCode.evaluate((element) => Number.parseFloat(getComputedStyle(element).paddingLeft))).toBeGreaterThan(0)
    const pre = page.locator('#fixture-reader pre').first()
    expect(await pre.evaluate((element) => getComputedStyle(element).backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
    expect(await pre.evaluate((element) => getComputedStyle(element).overflowX)).toBe('auto')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
  })

  test('renders GFM table cells with borders, padding and aligned columns', async ({ page }) => {
    await openReader(page, READER_CONTENT)

    const cell = page.locator('#fixture-reader td').first()
    expect(await cell.evaluate((element) => Number.parseFloat(getComputedStyle(element).borderTopWidth))).toBeGreaterThanOrEqual(1)
    expect(await cell.evaluate((element) => Number.parseFloat(getComputedStyle(element).paddingLeft))).toBeGreaterThanOrEqual(6)
    const header = page.locator('#fixture-reader th').first()
    expect(await header.evaluate((element) => getComputedStyle(element).backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
    expect(await header.evaluate((element) => Number.parseInt(getComputedStyle(element).fontWeight, 10))).toBeGreaterThanOrEqual(600)

    // Header and body columns line up: the same column has the same width in
    // the header row and the body row.
    const widths = await page.locator('#fixture-reader table').first().evaluate((table) => {
      const headerCells = Array.from(table.querySelectorAll('thead th')).map((element) => element.getBoundingClientRect().width)
      const bodyCells = Array.from(table.querySelectorAll('tbody td')).map((element) => element.getBoundingClientRect().width)
      return { headerCells, bodyCells }
    })
    expect(widths.headerCells.length).toBeGreaterThan(0)
    expect(widths.bodyCells.length).toBe(widths.headerCells.length)
    widths.headerCells.forEach((width, index) => {
      expect(Math.abs(width - widths.bodyCells[index])).toBeLessThanOrEqual(1)
    })
  })

  test('wraps an ordinary prose cell instead of scrolling the table', async ({ page }) => {
    const sentence = `${'word '.repeat(60)}end`
    const twoColumn = [
      'Intro',
      '',
      '| field | description |',
      '| :-- | :-- |',
      `| name | ${sentence} |`,
      '| result | ok |',
    ].join('\n')
    await openReader(page, twoColumn)

    const wrapper = page.locator('#fixture-reader .markdown-table-scroll').first()
    const metrics = await wrapper.evaluate((element) => {
      const table = element.querySelector('table')!
      const cells = Array.from(table.querySelectorAll('tbody td'))
      return {
        scrollWidth: element.scrollWidth,
        clientWidth: element.clientWidth,
        tableWidth: table.getBoundingClientRect().width,
        containerWidth: element.getBoundingClientRect().width,
        cellHeights: cells.map((cell) => cell.getBoundingClientRect().height),
        cellFontSize: Number.parseFloat(getComputedStyle(cells[1]).fontSize),
        thAlign: getComputedStyle(table.querySelector('thead th')!).textAlign.replace(/^-webkit-/, ''),
        tdAlign: getComputedStyle(cells[0]).textAlign.replace(/^-webkit-/, ''),
      }
    })

    // Evidence for the ruling: the prose cell wraps onto several lines and the
    // table fits its container without a horizontal scroll.
    expect(metrics.cellHeights[1]).toBeGreaterThan(metrics.cellFontSize * 2)
    expect(metrics.tableWidth).toBeLessThanOrEqual(metrics.containerWidth + 1)
    expect(metrics.scrollWidth).toBeLessThanOrEqual(metrics.clientWidth + 1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
    // Unaligned cells are left on both header and body, matching the editor.
    expect(metrics.thAlign).toBe('left')
    expect(metrics.tdAlign).toBe('left')

    // The same holds for an ordinary three-column table.
    await openReader(page, [
      'Intro',
      '',
      '| name | description | result |',
      '| :-- | :-- | :-- |',
      `| task | ${sentence} | ok |`,
    ].join('\n'))
    const three = await wrapper.evaluate((element) => ({
      scrollWidth: element.scrollWidth,
      clientWidth: element.clientWidth,
    }))
    expect(three.scrollWidth).toBeLessThanOrEqual(three.clientWidth + 1)
  })

  test('keeps explicit GFM alignment while unaligned cells stay left', async ({ page }) => {
    await openReader(page, [
      'Intro',
      '',
      '| plain | centered | right |',
      '| :-- | :-: | --: |',
      '| a | b | c |',
    ].join('\n'))

    const alignment = await page.locator('#fixture-reader table').first().evaluate((table) => {
      const header = Array.from(table.querySelectorAll('thead th')).map((cell) =>
        getComputedStyle(cell).textAlign.replace(/^-webkit-/, ''))
      const body = Array.from(table.querySelectorAll('tbody td')).map((cell) =>
        getComputedStyle(cell).textAlign.replace(/^-webkit-/, ''))
      return { header, body }
    })
    expect(alignment.header).toEqual(['left', 'center', 'right'])
    expect(alignment.body).toEqual(['left', 'center', 'right'])
  })

  test('keeps unbroken code and URLs from widening an ordinary table', async ({ page }) => {
    const longCode = `https://example.test/${'a'.repeat(120)}`
    await openReader(page, [
      'Intro',
      '',
      '| field | description |',
      '| :-- | :-- |',
      `| url | ${longCode} |`,
      '| code | `' + 'x'.repeat(120) + '` |',
    ].join('\n'))

    const wrapper = page.locator('#fixture-reader .markdown-table-scroll').first()
    const metrics = await wrapper.evaluate((element) => ({
      scrollWidth: element.scrollWidth,
      clientWidth: element.clientWidth,
      tableWidth: element.querySelector('table')!.getBoundingClientRect().width,
      containerWidth: element.getBoundingClientRect().width,
    }))
    expect(metrics.tableWidth).toBeLessThanOrEqual(metrics.containerWidth + 1)
    expect(metrics.scrollWidth).toBeLessThanOrEqual(metrics.clientWidth + 1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
  })

  test('scrolls a wide table locally instead of widening the page', async ({ page }) => {
    // Many short columns: the `8em` cell floor keeps each column readable, so
    // the table exceeds the container and must scroll inside its wrapper.
    const columns = Array.from({ length: 16 }, (_, index) => `column-${index}`)
    const wide = [
      'Intro',
      '',
      `| ${columns.join(' | ')} |`,
      `| ${columns.map(() => ':--').join(' | ')} |`,
      `| ${columns.map((column) => `${column}-value`).join(' | ')} |`,
    ].join('\n')
    await openReader(page, wide)

    const wrapper = page.locator('#fixture-reader .markdown-table-scroll').first()
    expect(await wrapper.evaluate((element) => getComputedStyle(element).overflowX)).toBe('auto')
    expect(await wrapper.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
  })
})

test.describe('real Markdown editor (fixture, no backend)', () => {
  test('supports keyboard bold, toolbar lists, blockquote, inline code and undo/redo', async ({ page }) => {
    await openFixture(page)
    await waitForVisualEditor(page)

    await replaceVisualText(page, 'bold text')
    await page.keyboard.press('Control+a')
    await page.keyboard.press('Control+b')
    await expect(page.locator('.ProseMirror strong')).toHaveText('bold text')

    await page.getByRole('button', { name: 'Bullet list' }).click()
    await expect(page.locator('.ProseMirror ul li')).toHaveCount(1)

    await page.getByRole('button', { name: 'Blockquote' }).click()
    await expect(page.locator('.ProseMirror blockquote')).toHaveCount(1)

    await page.getByRole('button', { name: 'Inline code' }).click()
    await expect(page.locator('.ProseMirror code').first()).toBeVisible()

    await page.getByRole('button', { name: 'Undo' }).click()
    await page.getByRole('button', { name: 'Redo' }).click()

    const submits = await page.evaluate(() => (window as any).__markdownFixture.getSubmits())
    expect(submits).toBe(0)
  })

  test('keeps a multi-line Enter inside the editor and never submits the form', async ({ page }) => {
    await openFixture(page)
    await waitForVisualEditor(page)

    await replaceVisualText(page, 'first')
    await page.keyboard.press('Enter')
    await page.keyboard.type('second')

    await expect(page.locator('.ProseMirror p')).toHaveCount(2)
    expect(await page.evaluate(() => (window as any).__markdownFixture.getSubmits())).toBe(0)
    expect(await page.evaluate(() => (window as any).__markdownFixture.getValue())).toContain('first\n\nsecond')
  })

  test('selects Python and renders highlighted keywords with real contrast', async ({ page }) => {
    await openFixture(page)
    await waitForVisualEditor(page)

    await page.locator('.ProseMirror').click()
    await page.getByRole('button', { name: 'Code block' }).click()
    await page.keyboard.type('def f():')
    await page.keyboard.press('Enter')
    await page.keyboard.type('  return 1')

    await page.getByRole('combobox', { name: 'Code language' }).click()
    await page.getByRole('option', { name: 'Python', exact: true }).click()

    const keyword = page.locator('.ProseMirror .hljs-keyword').first()
    await expect(keyword).toBeVisible()
    const colors = await keyword.evaluate((element) => ({
      foreground: getComputedStyle(element).color,
      background: getComputedStyle(element.closest('pre')!).backgroundColor,
    }))
    expect(colors.foreground).not.toBe('rgb(224, 222, 244)')
    expect(contrast(colors.foreground, colors.background)).toBeGreaterThanOrEqual(4.5)

    const value = await page.evaluate(() => (window as any).__markdownFixture.getValue())
    expect(value).toContain('```python')
    expect(value).toContain('def f():')
  })

  test('keeps an unknown fence language plain and never auto-detects', async ({ page }) => {
    const source = '```brainfuck\n+++[>+<-]\n```'
    await openFixture(page, source)
    await waitForVisualEditor(page)

    await expect(page.locator('.ProseMirror .hljs-keyword')).toHaveCount(0)
    await expect(page.locator('.ProseMirror pre code')).toContainText('+++[>+<-]')
    expect(await page.evaluate(() => (window as any).__markdownFixture.getValue())).toBe(source)
  })

  test('filters a styled HTML paste to plain bold and never executes scripts', async ({ page }) => {
    await openFixture(page)
    await waitForVisualEditor(page)

    await page.locator('.ProseMirror').click()
    await pasteHtml(
      page,
      '<b style="color:red" onclick="window.__xss=1">x</b>'
        + '<a href="javascript:window.__xss=2">link</a>'
        + '<script>window.__xss=3</script>',
    )

    await expect(page.locator('.ProseMirror strong')).toHaveText('x')
    expect(await page.locator('.ProseMirror [style], .ProseMirror [onclick], .ProseMirror script').count()).toBe(0)
    expect(await page.locator('.ProseMirror a[href^="javascript"]').count()).toBe(0)
    expect(await page.evaluate(() => (window as any).__xss)).toBeUndefined()

    const value = await page.evaluate(() => (window as any).__markdownFixture.getValue())
    expect(value).toContain('**x**')
    expect(value).not.toContain('style=')
    expect(value).not.toContain('javascript:')
  })

  test('opens Source for legacy raw HTML without emitting or converting on Escape', async ({ page }) => {
    await openFixture(page, '<div>legacy</div>\n\n- [ ] todo')
    await expect(page.locator('.ProseMirror')).toHaveCount(0)
    await expect(page.locator('textarea')).toBeVisible()
    expect(await sourceValue(page)).toBe('<div>legacy</div>\n\n- [ ] todo')
    expect(await page.evaluate(() => (window as any).__markdownFixture.getValue())).toBe('<div>legacy</div>\n\n- [ ] todo')
    expect(await page.locator('.markdown-editor .ProseMirror').count()).toBe(0)
    await expect(page.locator('.markdown-editor-shell')).toContainText('raw HTML')

    await page.getByRole('button', { name: 'Visual mode' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()
    await expect(page.locator('.ProseMirror')).toHaveCount(0)
    expect(await sourceValue(page)).toBe('<div>legacy</div>\n\n- [ ] todo')
  })

  test('opens the visual override with Cancel focused, and Escape cancels it', async ({ page }) => {
    await openFixture(page, '<div>legacy</div>')
    await page.getByRole('button', { name: 'Edit visually anyway' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()

    const cancel = dialog.getByRole('button', { name: 'Cancel', exact: true })
    await expect(cancel).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()
    await expect(page.locator('.ProseMirror')).toHaveCount(0)

    await page.getByRole('button', { name: 'Edit visually anyway' }).click()
    await page.getByRole('button', { name: 'Edit visually', exact: true }).click()
    await expect(page.locator('.ProseMirror')).toBeVisible()
  })

  test('Mod-K opens the inline link row and Enter applies the link without submitting', async ({ page }) => {
    await openFixture(page, 'link me')
    await waitForVisualEditor(page)

    await page.locator('.ProseMirror').click()
    await page.keyboard.press('Control+a')
    await page.keyboard.press('Control+k')
    const url = page.getByLabel('Link URL')
    await expect(url).toBeVisible()
    await url.fill('https://example.com')
    await url.press('Enter')

    await expect(page.locator('.ProseMirror a[href="https://example.com"]')).toHaveText('link me')
    expect(await page.evaluate(() => (window as any).__markdownFixture.getSubmits())).toBe(0)
  })

  test('counts code points and shows the over-limit message', async ({ page }) => {
    await openFixture(page, '🎯🎯')
    await waitForVisualEditor(page)
    await expect(page.locator('.markdown-editor-shell')).toContainText('2 / 50000')

    await page.getByRole('button', { name: 'Source mode' }).click()
    await page.locator('textarea').fill('x'.repeat(50_001))
    await expect(page.getByRole('alert').filter({ hasText: 'at most 50,000 characters' }).first()).toBeVisible()
    await expect(page.locator('.markdown-editor-shell')).toContainText('50001 / 50000')
  })

  test('wraps the toolbar at 375px without document overflow and scrolls code locally', async ({ page }) => {
    const width = page.viewportSize()?.width ?? 1280
    await openFixture(page, '```python\nx = "' + 'a'.repeat(160) + '"\n```')
    await waitForVisualEditor(page)

    if (width > 400) {
      // Desktop is allowed to show the single-row toolbar; the phone project
      // below exercises the wrapping contract.
      await expect(page.getByRole('toolbar', { name: 'Formatting' })).toBeVisible()
      return
    }

    await expect(page.getByRole('button', { name: 'Bold' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Code block' })).toBeVisible()
    const toolbarFits = await page.getByRole('toolbar', { name: 'Formatting' }).evaluate((element) =>
      element.scrollWidth <= element.clientWidth + 1,
    )
    expect(toolbarFits).toBe(true)

    const noDocumentOverflow = await page.evaluate(() =>
      document.documentElement.scrollWidth <= window.innerWidth + 1,
    )
    expect(noDocumentOverflow).toBe(true)

    const pre = page.locator('.ProseMirror pre').first()
    // Long code may scroll inside its own block; it must never widen the page.
    const preOverflow = await pre.evaluate((element) => getComputedStyle(element).overflowX)
    expect(preOverflow).toBe('auto')
    expect(noDocumentOverflow).toBe(true)
  })
})
