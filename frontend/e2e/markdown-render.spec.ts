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

    expect(await page.locator('.markdown-body table, .markdown-body [align], .markdown-body [style]').count()).toBe(0)
    await expect(page.locator('.markdown-body')).toContainText('<table>')
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
