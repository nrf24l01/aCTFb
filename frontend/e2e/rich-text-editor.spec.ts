import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

/**
 * Real authoring acceptance against the real backend and the real Vue app.
 *
 *   ACTFB_E2E=1 \
 *   E2E_BASE_URL=http://127.0.0.1:5173 \
 *   E2E_API_URL=http://127.0.0.1:8080/api/v2 \
 *   E2E_ADMIN_USERNAME=... E2E_ADMIN_PASSWORD=... \
 *   [E2E_PARTICIPANT_USERNAME=... E2E_PARTICIPANT_PASSWORD=...] \
 *   bun run test:e2e -- rich-text-editor.spec.ts
 *
 * The CI default-profile runner supplies all of these, so the spec executes
 * there rather than skipping. Board/module/task fixtures are created through
 * the real HTTP API and the UI; no SQL and no token in browser storage.
 */
const enabled = process.env.ACTFB_E2E === '1'
const apiUrl = (process.env.E2E_API_URL || 'http://127.0.0.1:8080/api/v2').replace(/\/+$/, '')
const baseUrl = process.env.E2E_BASE_URL || 'http://127.0.0.1:5173'
const adminUser = process.env.E2E_ADMIN_USERNAME ?? ''
const adminPass = process.env.E2E_ADMIN_PASSWORD ?? ''

interface Fixture {
  boardUuid: string
  boardName: string
  joinCode: string
  moduleUuid: string
  taskUuid: string
  participant: { username: string; password: string }
}

async function json<T>(
  request: APIRequestContext,
  method: 'GET' | 'POST' | 'PATCH' | 'DELETE',
  path: string,
  data?: unknown,
  token?: string,
): Promise<T> {
  const response = await request.fetch(`${apiUrl}${path}`, {
    method,
    data,
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  if (!response.ok()) {
    throw new Error(`${method} ${path} failed: ${response.status()} ${await response.text()}`)
  }
  if (response.status() === 204) return undefined as T
  return (await response.json()) as T
}

/**
 * The access token from the browser session. The UI already signed in, so this
 * uses the refresh cookie through the app origin instead of a second password
 * login (the production login limiter is 10/minute per IP).
 */
async function accessToken(page: Page): Promise<string> {
  const response = await page.request.post('/api/v2/auth/refresh', { headers: { Origin: baseUrl } })
  if (!response.ok()) throw new Error(`token refresh failed: ${response.status()} ${await response.text()}`)
  return ((await response.json()) as { accessToken: string }).accessToken
}
async function ensureParticipant(request: APIRequestContext): Promise<{ username: string; password: string }> {
  const username = process.env.E2E_PARTICIPANT_USERNAME
  const password = process.env.E2E_PARTICIPANT_PASSWORD
  if (username && password) return { username, password }

  const suffix = `${Date.now()}${Math.floor(Math.random() * 1000)}`
  const created = {
    username: `e2e_md_${suffix}`.slice(0, 32),
    password: `E2e-markdown-${suffix}`,
  }
  const response = await request.post(`${apiUrl}/auth/register`, {
    data: {
      email: `${created.username}@example.test`,
      username: created.username,
      password: created.password,
    },
  })
  if (!response.ok()) {
    throw new Error(`participant registration failed: ${response.status()} ${await response.text()}`)
  }
  return created
}

async function signIn(page: Page, username: string, password: string): Promise<void> {
  for (let attempt = 0; attempt < 2; attempt += 1) {
    await page.goto('/sign-in')
    await page.locator('#sign-in-username').fill(username)
    await page.locator('#sign-in-password').fill(password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    const signedIn = await expect(page).toHaveURL(/\/boards/, { timeout: 8_000 }).then(() => true).catch(() => false)
    if (signedIn) return
    // The login IP limiter is 10/minute. Wait for the window instead of
    // weakening the production limiter.
    if (attempt === 0) await page.waitForTimeout(61_000)
  }
  await expect(page).toHaveURL(/\/boards/)
}

async function pasteHtml(page: Page, selector: string, html: string): Promise<void> {
  await page.locator(selector).evaluate((element, payload) => {
    const data = new DataTransfer()
    data.setData('text/html', payload)
    data.setData('text/plain', 'x')
    element.dispatchEvent(new ClipboardEvent('paste', {
      bubbles: true,
      cancelable: true,
      clipboardData: data,
    }))
  }, html)
}

async function openModuleEditor(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Edit module' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.locator('.ProseMirror')).toBeVisible()
}

test.describe('rich-text authoring acceptance', () => {
  test.skip(!enabled, 'Set ACTFB_E2E=1 with a real backend to run browser acceptance')
  test.describe.configure({ mode: 'serial' })

  let fixture: Fixture
  let token: string

  test.beforeAll(async ({ request }) => {
    if (!adminUser || !adminPass) throw new Error('E2E_ADMIN_USERNAME and E2E_ADMIN_PASSWORD are required')
    const participant = await ensureParticipant(request)
    fixture = {
      boardUuid: '',
      boardName: '',
      joinCode: '',
      moduleUuid: '',
      taskUuid: '',
      participant,
    }
  })

  test('authors all four surfaces, persists exact Markdown and renders it read-only', async ({ page }) => {
    // ---- Board description through the real dialog -------------------------
    await signIn(page, adminUser, adminPass)
    token = await accessToken(page)

    // Public pages must not download the editor. The dev server serves the
    // component module directly; the built chunk is measured in the report.
    const requested: string[] = []
    page.on('request', (request) => requested.push(request.url()))
    await page.goto('/')
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    await page.waitForLoadState('networkidle')
    expect(requested.some((url) => url.includes('MarkdownEditor.vue'))).toBe(false)

    const suffix = `${Date.now()}`
    const board = await json<{ uuid: string }>(page.request, 'POST', '/admin/boards', {
      slug: `e2e-md-${suffix}`,
      name: `E2E Markdown ${suffix}`,
      description: 'API board description',
      participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN',
      visibility: 'PUBLIC',
      rating: { visibility: 'PUBLIC' },
      startsAt: new Date(Date.now() - 60_000).toISOString(),
      endsAt: new Date(Date.now() + 3_600_000).toISOString(),
    }, token)
    const join = await json<{ code: string }>(
      page.request,
      'POST',
      `/admin/boards/${board.uuid}/join-code`,
      {},
      token,
    )
    fixture.boardUuid = board.uuid
    fixture.boardName = `E2E Markdown ${suffix}`
    fixture.joinCode = join.code

    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    await page.getByRole('button', { name: 'Edit board' }).click()
    const boardDialog = page.getByRole('dialog')
    await expect(boardDialog.locator('.ProseMirror')).toBeVisible()
    expect(requested.some((url) => url.includes('MarkdownEditor.vue'))).toBe(true)

    if ((page.viewportSize()?.width ?? 1280) <= 400) {
      const toolbar = boardDialog.getByRole('toolbar', { name: 'Formatting' })
      expect(await toolbar.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
      const scroller = boardDialog.locator('.overflow-y-auto').first()
      expect(await scroller.evaluate((element) => getComputedStyle(element).overflowY)).toBe('auto')
    }

    await boardDialog.locator('.ProseMirror').click()
    await page.keyboard.press('Control+a')
    await page.keyboard.type('Board ')
    await page.keyboard.press('Control+b')
    await page.keyboard.type('bold')
    await page.keyboard.press('Control+b')
    await boardDialog.getByRole('button', { name: 'Save changes' }).click()
    await expect(boardDialog).toBeHidden()

    const storedBoard = await json<{ description: string }>(
      page.request, 'GET', `/admin/boards/${fixture.boardUuid}`, undefined, token,
    )
    expect(storedBoard.description).toContain('**bold**')

    // ---- Module description, including a Python code block -----------------
    await page.getByRole('button', { name: 'Create module' }).first().click()
    const moduleDialog = page.getByRole('dialog')
    await expect(moduleDialog.locator('#module-name')).toBeVisible()
    await moduleDialog.locator('#module-name').fill('Rich module')
    await moduleDialog.locator('.ProseMirror').click()
    await page.keyboard.type('Module ')
    await page.keyboard.press('Control+b')
    await page.keyboard.type('bold')
    await page.keyboard.press('Control+b')
    await page.keyboard.press('Enter')
    await expect(moduleDialog.locator('.ProseMirror p')).toHaveCount(2)
    await moduleDialog.getByRole('button', { name: 'Code block' }).click()
    await page.keyboard.type('def f():')
    await page.keyboard.press('Enter')
    await page.keyboard.type('  return 1')
    await moduleDialog.getByRole('combobox', { name: 'Code language' }).click()
    await page.getByRole('option', { name: 'Python', exact: true }).click()

    // Add a heading and a GFM table through Source mode; both are supported
    // Markdown, so returning to Visual keeps them.
    await moduleDialog.getByRole('button', { name: 'Source mode' }).click()
    const moduleSourceBox = moduleDialog.locator('#module-description')
    const moduleSourceText = await moduleSourceBox.inputValue()
    expect(moduleSourceText).toContain('```python')
    await moduleSourceBox.fill(
      `${moduleSourceText}\n\n## Module heading\n\n| name | value |\n| :-- | --: |\n| a | b |`,
    )
    await moduleDialog.getByRole('button', { name: 'Visual mode' }).click()
    await expect(moduleDialog.locator('.ProseMirror h2')).toBeVisible()

    // Direct authoring: the Visual editor itself must show the published
    // formatting (the shared `.markdown-editor .ProseMirror` typography),
    // including a styled code block, a real heading and a bordered table. There
    // is no outer Preview tab any more.
    await expect(moduleDialog.getByRole('tab')).toHaveCount(0)
    const moduleEditor = moduleDialog.locator('.ProseMirror')
    await expect(moduleEditor.locator('pre').first()).toBeVisible()
    expect(await moduleEditor.locator('pre').first().evaluate((element) =>
      getComputedStyle(element).backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
    expect(await moduleEditor.locator('pre').first().evaluate((element) =>
      Number.parseFloat(getComputedStyle(element).paddingLeft))).toBeGreaterThan(0)
    expect(await moduleEditor.locator('strong').first().evaluate((element) =>
      Number.parseInt(getComputedStyle(element).fontWeight, 10))).toBeGreaterThanOrEqual(600)
    const editorHeadingSize = await moduleEditor.locator('h2').first().evaluate((element) =>
      Number.parseFloat(getComputedStyle(element).fontSize))
    const editorBodySize = await moduleEditor.locator('p').first().evaluate((element) =>
      Number.parseFloat(getComputedStyle(element).fontSize))
    expect(editorHeadingSize).toBeGreaterThan(editorBodySize)
    expect(await moduleEditor.locator('td').first().evaluate((element) =>
      Number.parseFloat(getComputedStyle(element).borderTopWidth))).toBeGreaterThanOrEqual(1)

    await moduleDialog.getByRole('button', { name: 'Create module' }).click()
    await expect(moduleDialog).toBeHidden()
    await expect(page.getByRole('heading', { name: 'Rich module' })).toBeVisible()

    const modules = await json<{ items: Array<{ uuid: string; name: string }> }>(
      page.request, 'GET', `/admin/boards/${fixture.boardUuid}/modules?limit=100`, undefined, token,
    )
    fixture.moduleUuid = modules.items.find((module) => module.name === 'Rich module')!.uuid

    const storedModule = await json<{ description: string }>(
      page.request,
      'GET',
      `/admin/boards/${fixture.boardUuid}/modules/${fixture.moduleUuid}`,
      undefined,
      token,
    )
    expect(storedModule.description).toContain('**bold**')
    expect(storedModule.description).toContain('```python')

    // Reopen unchanged: Source must show the exact stored bytes.
    await openModuleEditor(page)
    const reopenedModule = page.getByRole('dialog')
    await reopenedModule.getByRole('button', { name: 'Source mode' }).click()
    const moduleSource = await reopenedModule.locator('#module-description').inputValue()
    expect(moduleSource).toBe(storedModule.description)
    await reopenedModule.getByRole('button', { name: 'Cancel' }).click()
    await expect(reopenedModule).toBeHidden()

    // ---- Task description with a styled paste and a code block -------------
    await page.getByRole('button', { name: 'Create task' }).click()
    const taskDialog = page.getByRole('dialog')
    await expect(taskDialog.locator('#task-title')).toBeVisible()
    await taskDialog.locator('#task-title').fill('Rich flag')
    await taskDialog.locator('#task-category').fill('misc')
    await taskDialog.locator('#task-answers').fill('flag{rich_text}')
    await taskDialog.locator('.ProseMirror').click()
    await page.keyboard.type('Paste ')
    await pasteHtml(page, '[role="dialog"] .ProseMirror', '<b style="color:red" onclick="window.__xss=1">x</b>')
    await expect(taskDialog.locator('.ProseMirror strong')).toHaveText('x')
    await page.keyboard.press('End')
    await page.keyboard.press('Enter')
    await expect(taskDialog.locator('.ProseMirror p')).toHaveCount(2)
    await taskDialog.getByRole('button', { name: 'Code block' }).click()
    await page.keyboard.type('def g():')
    await page.keyboard.press('Enter')
    await page.keyboard.type('  return 2')
    await taskDialog.getByRole('combobox', { name: 'Code language' }).click()
    await page.getByRole('option', { name: 'Python', exact: true }).click()
    await taskDialog.getByRole('checkbox', { name: 'Visible to participants' }).click()
    await taskDialog.getByRole('button', { name: 'Save draft' }).click()
    await expect(taskDialog).toBeHidden()

    const tasks = await json<{ items: Array<{ uuid: string; title: string }> }>(
      page.request,
      'GET',
      `/admin/boards/${fixture.boardUuid}/modules/${fixture.moduleUuid}/tasks?limit=100`,
      undefined,
      token,
    )
    fixture.taskUuid = tasks.items.find((task) => task.title === 'Rich flag')!.uuid

    const storedTask = await json<{ description: string }>(
      page.request,
      'GET',
      `/admin/boards/${fixture.boardUuid}/modules/${fixture.moduleUuid}/tasks/${fixture.taskUuid}`,
      undefined,
      token,
    )
    expect(storedTask.description).toContain('**x**')
    expect(storedTask.description).not.toContain('style=')
    expect(storedTask.description).not.toContain('javascript:')
    expect(storedTask.description).toContain('```python')

    // Reopen unchanged: exact stored bytes, then cancel without a rewrite.
    await page.getByRole('button', { name: 'Edit', exact: true }).click()
    const taskEdit = page.getByRole('dialog')
    await expect(taskEdit.locator('.ProseMirror')).toBeVisible()
    await taskEdit.getByRole('button', { name: 'Source mode' }).click()
    expect(await taskEdit.locator('#task-description').inputValue()).toBe(storedTask.description)
    await taskEdit.getByRole('button', { name: 'Cancel' }).click()
    await expect(taskEdit).toBeHidden()

    // ---- Over-limit description cannot be saved, and the error is visible ---
    await openModuleEditor(page)
    const limitDialog = page.getByRole('dialog')
    await limitDialog.getByRole('button', { name: 'Source mode' }).click()
    await limitDialog.locator('#module-description').fill('x'.repeat(50_001))

    // One owner for the message: the editor does not duplicate the parent alert.
    const limitAlert = limitDialog.getByRole('alert').filter({ hasText: 'at most 50,000 characters' })
    await expect(limitAlert).toHaveCount(1)
    const describedBy = await limitDialog.locator('#module-description').getAttribute('aria-describedby')
    expect(describedBy).toBeTruthy()
    await expect(limitDialog.locator(`#${describedBy}`)).toContainText('at most 50,000 characters')
    expect(await limitDialog.locator('#module-description').getAttribute('aria-invalid')).toBe('true')

    // The form-owned message stays visible while the author is in Source mode,
    // and the editor does not duplicate it.
    await expect(limitDialog.getByRole('tab')).toHaveCount(0)
    let modulePatches = 0
    page.on('request', (request) => {
      if (request.method() === 'PATCH' && request.url().includes(`/modules/${fixture.moduleUuid}`)) {
        modulePatches += 1
      }
    })
    await limitDialog.getByRole('button', { name: 'Save changes' }).click()
    await expect(limitAlert).toHaveCount(1)
    await page.waitForTimeout(300)
    expect(modulePatches).toBe(0)
    await limitDialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByText('Discard module changes?')).toBeVisible()
    await page.getByRole('button', { name: 'Discard changes' }).click()
    await expect(page.getByText('Discard module changes?')).toBeHidden()

    // ---- Site introduction -------------------------------------------------
    await page.goto('/admin/site')
    await expect(page.locator('.ProseMirror')).toBeVisible()
    await page.locator('.ProseMirror').click()
    await page.keyboard.press('Control+a')
    await page.keyboard.type('Site ')
    await page.keyboard.press('Control+b')
    await page.keyboard.type('bold')
    await page.keyboard.press('Control+b')
    // The direct Visual editor shows the formatted result; no Preview tab.
    await expect(page.getByRole('tab')).toHaveCount(0)
    await expect(page.locator('.ProseMirror strong').first()).toHaveText('bold')
    expect(await page.locator('.ProseMirror strong').first().evaluate((element) =>
      Number.parseInt(getComputedStyle(element).fontWeight, 10))).toBeGreaterThanOrEqual(600)
    await page.getByRole('button', { name: 'Save changes' }).click()
    await expect(page.getByText('Saved.')).toBeVisible()
    await page.goto('/')
    await expect(page.locator('.markdown-body')).toContainText('Site bold')
    await expect(page.locator('.markdown-body strong').first()).toHaveText('bold')

    // ---- Publish, then a real participant reads all three pages ------------
    await json(page.request, 'POST', `/admin/boards/${fixture.boardUuid}/actions`, { action: 'PUBLISH' }, token)

    const participantContext = await page.context().browser()!.newContext()
    try {
      const participant = await participantContext.newPage()
      await signIn(participant, fixture.participant.username, fixture.participant.password)
      await participant.goto(`/boards/${fixture.boardUuid}`)
      await participant.getByRole('button', { name: 'Enter board code' }).click()
      await participant.getByLabel('Board code').fill(fixture.joinCode)
      await participant.getByRole('button', { name: 'Join', exact: true }).click()

      await expect(participant.locator('.markdown-body')).toContainText('Board bold')
      await expect(participant.locator('.markdown-body strong').first()).toHaveText('bold')
      expect(await participant.locator('body').innerText()).not.toContain('**bold**')

      await participant.goto(`/boards/${fixture.boardUuid}/modules/${fixture.moduleUuid}`)
      await expect(participant.locator('.markdown-body')).toContainText('Module bold')
      await expect(participant.locator('.markdown-body strong').first()).toHaveText('bold')
      await expect(participant.locator('.markdown-body .hljs-keyword').first()).toBeVisible()
      expect(await participant.locator('.markdown-body pre').first().evaluate((element) =>
        getComputedStyle(element).backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
      const readerHeadingSize = await participant.locator('.markdown-body h2').first().evaluate((element) =>
        Number.parseFloat(getComputedStyle(element).fontSize))
      const readerBodySize = await participant.locator('.markdown-body p').first().evaluate((element) =>
        Number.parseFloat(getComputedStyle(element).fontSize))
      expect(readerHeadingSize).toBeGreaterThan(readerBodySize)
      expect(await participant.locator('.markdown-body td').first().evaluate((element) =>
        Number.parseFloat(getComputedStyle(element).borderTopWidth))).toBeGreaterThanOrEqual(1)
      expect(await participant.locator('body').innerText()).not.toContain('```')

      await participant.goto(
        `/boards/${fixture.boardUuid}/modules/${fixture.moduleUuid}/tasks/${fixture.taskUuid}`,
      )
      const description = participant.locator('section[aria-labelledby="description-heading"] article')
      await expect(description).toHaveClass(/markdown-body/)
      await expect(description.locator('.hljs-keyword').first()).toBeVisible()
      await expect(description).toContainText('Paste x')
      expect(await description.locator('pre').first().evaluate((element) =>
        getComputedStyle(element).backgroundColor)).not.toBe('rgba(0, 0, 0, 0)')
      expect(await description.locator('strong').first().evaluate((element) =>
        Number.parseInt(getComputedStyle(element).fontWeight, 10))).toBeGreaterThanOrEqual(600)
      expect(await description.innerText()).not.toContain('```')
      expect(await description.innerText()).not.toContain('**x**')
      expect(await description.locator('[style], [onclick], script').count()).toBe(0)
      expect(await participant.locator('.ProseMirror').count()).toBe(0)
    } finally {
      await participantContext.close()
    }
  })
})
