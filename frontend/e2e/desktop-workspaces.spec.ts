import { expect, test, type APIRequestContext, type BrowserContext, type Locator, type Page } from '@playwright/test'

/**
 * Desktop authoring (Task 1) and workspace (Task 2) geometry, measured in a
 * real browser.
 *
 *   ACTFB_E2E=1 \
 *   E2E_BASE_URL=http://127.0.0.1:5173 \
 *   E2E_API_URL=http://127.0.0.1:8080/api/v2 \
 *   E2E_ADMIN_USERNAME=... E2E_ADMIN_PASSWORD=... \
 *   E2E_PARTICIPANT_USERNAME=... E2E_PARTICIPANT_PASSWORD=... \
 *   [E2E_SCREENSHOT_DIR=/tmp/opencode/desktop-workspaces/task2] \
 *   bun run test:e2e -- desktop-workspaces.spec.ts
 *
 * Task 1 covers the four authoring surfaces (three dialogs and the site
 * introduction). Task 2 covers the shared page container, the reader/organizer
 * boards, the module/task workspaces and the administration catalog. Fixtures
 * are created through the real HTTP API; no SQL and no token in browser
 * storage.
 */
const enabled = process.env.ACTFB_E2E === '1'
const apiUrl = (process.env.E2E_API_URL || 'http://127.0.0.1:8080/api/v2').replace(/\/+$/, '')
const baseUrl = process.env.E2E_BASE_URL || 'http://127.0.0.1:5173'
const adminUser = process.env.E2E_ADMIN_USERNAME ?? ''
const adminPass = process.env.E2E_ADMIN_PASSWORD ?? ''
const participantUser = process.env.E2E_PARTICIPANT_USERNAME ?? ''
const participantPass = process.env.E2E_PARTICIPANT_PASSWORD ?? ''
const screenshotDir = process.env.E2E_SCREENSHOT_DIR

interface Fixture {
  boardUuid: string
  boardName: string
  boardSlug: string
  flagModuleUuid: string
  manualModuleUuid: string
  instanceModuleUuid: string
  longModuleUuid: string
  firstTaskUuid: string
  joinCode: string
}

interface Box {
  x: number
  y: number
  width: number
  height: number
  top: number
  left: number
  right: number
  bottom: number
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
 * The production login limiter is 10/minute per IP, and the root default-profile
 * suite that shares this owned backend has just signed several sessions in. Wait
 * out one window instead of weakening the limiter, then sign in again.
 */
async function signIn(page: Page, username: string, password: string): Promise<void> {
  for (let attempt = 0; attempt < 2; attempt += 1) {
    await page.goto('/sign-in')
    await page.locator('#sign-in-username').fill(username)
    await page.locator('#sign-in-password').fill(password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    const signedIn = await expect(page).toHaveURL(/\/boards/, { timeout: 8_000 })
      .then(() => true)
      .catch(() => false)
    if (signedIn) return
    if (attempt === 0) await page.waitForTimeout(61_000)
  }
  await expect(page).toHaveURL(/\/boards/)
}

async function box(locator: Locator): Promise<Box> {
  return locator.evaluate((element) => {
    const rect = element.getBoundingClientRect()
    return {
      x: rect.x,
      y: rect.y,
      width: rect.width,
      height: rect.height,
      top: rect.top,
      left: rect.left,
      right: rect.right,
      bottom: rect.bottom,
    }
  })
}

async function screenshot(page: Page, name: string): Promise<void> {
  if (!screenshotDir) return
  await page.screenshot({ path: `${screenshotDir}/${name}.png` })
}

/** Screenshots start at the top with local fonts settled, so the page title is visible. */
async function capture(page: Page, name: string): Promise<void> {
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.evaluate(() => document.fonts.ready)
  await screenshot(page, name)
}

/**
 * Usable page width: the shared PageContainer is the last direct div in
 * `main`; its content box excludes the 24px desktop side padding. This is the
 * width the CSS container queries see, not `window.innerWidth`.
 */
async function pageContentWidth(page: Page): Promise<number> {
  return page.evaluate(() => {
    const main = document.querySelector('main')
    if (!main) throw new Error('main element missing')
    const containers = Array.from(main.children).filter(
      (child): child is HTMLElement => child instanceof HTMLElement && child.tagName === 'DIV',
    )
    const container = containers[containers.length - 1]
    const style = getComputedStyle(container)
    return container.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)
  })
}

/** Count distinct card columns/rows from actual viewport coordinates. */
async function cardTracks(locator: Locator): Promise<{ columns: number; rows: number }> {
  const positions = await locator.evaluateAll((elements) =>
    elements.map((element) => {
      const rect = element.getBoundingClientRect()
      return { left: Math.round(rect.left), top: Math.round(rect.top) }
    }),
  )
  return {
    columns: new Set(positions.map((position) => position.left)).size,
    rows: new Set(positions.map((position) => position.top)).size,
  }
}

/**
 * Real catalog fixtures through the documented authoring API. The catalog is
 * always available on the default profile (runtime instances are the separate
 * switch), so this needs no provider and no runtime credential.
 *
 * `ownTarget` is how many templates *this run* owns: the first call guarantees
 * three, the second six. The catalog itself is shared with the root
 * default-profile suite (which creates exactly one template on a fresh CI
 * database), so the grid is always asserted against the real total and against
 * the run's own record count, never against a hard-coded catalog size.
 */
async function createCatalogTemplates(
  request: APIRequestContext,
  token: string,
  prefix: string,
  ownTarget: number,
): Promise<{ items: Array<{ uuid: string; name: string }>; created: number }> {
  const list = async () => {
    const page = await json<{ items: Array<{ uuid: string; name: string }> }>(
      request,
      'GET',
      '/admin/deployment-templates?limit=100&offset=0',
      undefined,
      token,
    )
    return page.items
  }

  const ownCount = (catalog: Array<{ name: string }>) =>
    catalog.filter((item) => item.name.startsWith(`${prefix} `)).length
  let items = await list()
  let created = 0
  while (ownCount(items) < ownTarget) {
    const index = ownCount(items) + 1
    const slug = `${prefix.toLowerCase().replace(/[^a-z0-9]+/g, '-')}-${index}`
    const manifestYaml = [
      'apiVersion: actfb.dev/v1alpha1',
      'kind: DeploymentTemplate',
      'metadata:',
      `  name: ${prefix} ${index}`,
      `  slug: ${slug}`,
      "  description: 'Browser-measured catalog fixture'",
      'nodes: []',
      '',
    ].join('\n')
    await json(request, 'POST', '/admin/deployment-templates', { manifestYaml, files: [] }, token)
    created += 1
    items = await list()
  }
  return { items, created }
}

/** Rows a three-column grid needs for `count` cards. */
function gridRows(count: number, columns: number): number {
  return Math.ceil(count / columns)
}

/** `getBoundingClientRect` is visually scaled under CSS `zoom`; undo that for layout geometry. */
async function zoomedBox(locator: Locator, zoom: number): Promise<Box> {
  const rect = await box(locator)
  return {
    x: rect.x / zoom,
    y: rect.y / zoom,
    width: rect.width / zoom,
    height: rect.height / zoom,
    top: rect.top / zoom,
    left: rect.left / zoom,
    right: rect.right / zoom,
    bottom: rect.bottom / zoom,
  }
}

/** CSS zoom is a layout surrogate for browser zoom; `vw` units are not adjusted by it. */
async function setCssZoom(page: Page, zoom: number): Promise<void> {
  await page.evaluate((value) => {
    document.documentElement.style.zoom = value === 1 ? '' : String(value)
  }, zoom)
}

/**
 * The effective CSS viewport of a 200% browser zoom at 1920x1080: 960x540 CSS
 * pixels at devicePixelRatio 2. This uses Chromium's real device-metrics
 * emulation, not `setPageScaleFactor` (which only scales the visual viewport).
 */
async function emulateBrowserZoomLayout(page: Page): Promise<() => Promise<void>> {
  const session = await page.context().newCDPSession(page)
  await session.send('Emulation.setDeviceMetricsOverride', {
    width: 960,
    height: 540,
    deviceScaleFactor: 2,
    mobile: false,
  })
  return async () => {
    await session.send('Emulation.clearDeviceMetricsOverride')
    await session.detach()
  }
}

async function openBoardDialog(page: Page): Promise<Locator> {
  await page.getByRole('button', { name: 'Edit board' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.locator('#board-description')).toBeVisible()
  await expect(dialog.locator('.ProseMirror')).toBeVisible()
  return dialog
}

async function openModuleDialog(page: Page): Promise<Locator> {
  await page.getByRole('button', { name: 'Create module' }).first().click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.locator('#module-name')).toBeVisible()
  await expect(dialog.locator('.ProseMirror')).toBeVisible()
  return dialog
}

async function moduleSection(page: Page, name: string): Promise<Locator> {
  return page.getByRole('heading', { name, exact: true }).locator('xpath=ancestor::section[1]')
}

async function openTaskDialog(page: Page, moduleName: string): Promise<Locator> {
  const section = await moduleSection(page, moduleName)
  await section.getByRole('button', { name: 'Create task' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.locator('#task-title')).toBeVisible()
  await expect(dialog.locator('.ProseMirror')).toBeVisible()
  return dialog
}

async function closeDialog(page: Page, dialog: Locator): Promise<void> {
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  const discard = page.getByRole('button', { name: 'Discard changes' })
  if (await discard.isVisible().catch(() => false)) await discard.click()
  await expect(dialog).toBeHidden()
}

/** Canonical code enrollment through the product UI, never a request mock. */
async function joinBoard(page: Page, joinCode: string): Promise<void> {
  const join = page.getByRole('button', { name: 'Enter board code' })
  if (!(await join.isVisible().catch(() => false))) return
  await join.click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Board code').fill(joinCode)
  await dialog.getByRole('button', { name: 'Join', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Leave board' })).toBeVisible()
}

async function assertSingleScroller(dialog: Locator): Promise<Locator> {
  const scrollers = dialog.locator('[data-slot="authoring-scroll"]')
  await expect(scrollers).toHaveCount(1)
  const scroller = scrollers.first()
  expect(await scroller.evaluate((element) => getComputedStyle(element).overflowY)).toBe('auto')
  // Header and footer stay outside the one scrolling body.
  expect(await dialog.locator('[data-slot="dialog-header"]').evaluate((element) =>
    element.closest('[data-slot="authoring-scroll"]') === null)).toBe(true)
  expect(await dialog.locator('[data-slot="dialog-footer"]').evaluate((element) =>
    element.closest('[data-slot="authoring-scroll"]') === null)).toBe(true)
  return scroller
}

async function assertColumns(dialog: Locator, expectedRail: number): Promise<void> {
  const layout = dialog.locator('[data-slot="authoring-layout"]')
  const main = dialog.locator('[data-slot="authoring-main"]')
  const settings = dialog.locator('[data-slot="authoring-settings"]')
  const layoutBox = await box(layout)
  const mainBox = await box(main)
  const settingsBox = await box(settings)

  // Real column geometry, not class names: equal top edge, 320px rail and a
  // 24px gap that together fill the container exactly.
  expect(Math.abs(mainBox.top - settingsBox.top)).toBeLessThanOrEqual(1)
  expect(Math.abs(settingsBox.width - expectedRail)).toBeLessThanOrEqual(1)
  expect(Math.abs(settingsBox.left - mainBox.right - 24)).toBeLessThanOrEqual(1)
  expect(Math.abs(mainBox.width + 24 + settingsBox.width - layoutBox.width)).toBeLessThanOrEqual(1)
  expect(layoutBox.width).toBeGreaterThanOrEqual(1040)
}

async function assertStacked(dialog: Locator, expectedWidth?: number): Promise<void> {
  const layout = dialog.locator('[data-slot="authoring-layout"]')
  const main = dialog.locator('[data-slot="authoring-main"]')
  const settings = dialog.locator('[data-slot="authoring-settings"]')
  const layoutBox = await box(layout)
  const mainBox = await box(main)
  const settingsBox = await box(settings)

  if (expectedWidth !== undefined) {
    expect(Math.abs(layoutBox.width - expectedWidth)).toBeLessThanOrEqual(1)
  }
  expect(Math.abs(mainBox.width - layoutBox.width)).toBeLessThanOrEqual(1)
  expect(Math.abs(settingsBox.width - layoutBox.width)).toBeLessThanOrEqual(1)
  expect(Math.abs(mainBox.left - settingsBox.left)).toBeLessThanOrEqual(1)
  expect(settingsBox.top).toBeGreaterThanOrEqual(mainBox.bottom - 1)
}

test.describe('desktop authoring workspaces', () => {
  test.skip(!enabled, 'Set ACTFB_E2E=1 with a real backend to run browser geometry')
  test.describe.configure({ mode: 'serial' })

  let fixture: Fixture
  let adminContext: BrowserContext
  let adminPage: Page
  let participantContext: BrowserContext | undefined
  let participantPage: Page | undefined
  /** API token for fixture setup and byte-exact payload reads. */
  let adminToken: string
  /** Unique catalog prefix, so the measured catalog is this run's own data. */
  let catalogPrefix: string

  test.beforeAll(async ({ browser, request }) => {
    // This hook signs in two roles and then seeds the whole board. The login
    // limiter can cost one 61s wait plus an 8s failed-login detection per role,
    // so the worst case is ~140s of waiting before any fixture work. Playwright
    // applies `test.setTimeout` to the running hook only (verified: a 5s hook
    // passes under a 3s project timeout, and a 1s hook budget aborts it), so the
    // 90s project default stays in force for every individual test.
    test.setTimeout(240_000)
    if (!adminUser || !adminPass) throw new Error('E2E_ADMIN_USERNAME and E2E_ADMIN_PASSWORD are required')

    // One owned browser session per role for the whole spec: the backend
    // limits logins per IP and the refresh token rotates on every use, so each
    // test must not sign in again.
    const contextOptions = {
      baseURL: baseUrl,
      viewport: test.info().project.use.viewport ?? undefined,
      deviceScaleFactor: test.info().project.use.deviceScaleFactor,
      isMobile: test.info().project.use.isMobile,
      hasTouch: test.info().project.use.hasTouch,
    }
    adminContext = await browser.newContext(contextOptions)
    adminPage = await adminContext.newPage()
    await signIn(adminPage, adminUser, adminPass)
    if (participantUser && participantPass) {
      participantContext = await browser.newContext(contextOptions)
      participantPage = await participantContext.newPage()
      await signIn(participantPage, participantUser, participantPass)
    }

    // The UI already signed in, so the fixture token comes from the existing
    // browser session through the app origin. That avoids a second password
    // login against the production limiter.
    const token = await adminPage.evaluate(async () => {
      const response = await fetch('/api/v2/auth/refresh', {
        method: 'POST',
        headers: { Origin: window.location.origin },
      })
      if (!response.ok) throw new Error(`token refresh failed: ${response.status}`)
      return ((await response.json()) as { accessToken: string }).accessToken
    })

    const suffix = `${Date.now()}${Math.floor(Math.random() * 1000)}`
    const boardName = `Desktop workspace ${suffix}`
    const boardSlug = `desktop-workspace-${suffix}`
    const board = await json<{ uuid: string }>(request, 'POST', '/admin/boards', {
      slug: boardSlug,
      name: boardName,
      description: 'Workspace fixture',
      participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN',
      visibility: 'PUBLIC',
      rating: { visibility: 'PUBLIC' },
      startsAt: new Date(Date.now() - 60_000).toISOString(),
      endsAt: new Date(Date.now() + 3_600_000).toISOString(),
    }, token)

    const createModule = async (name: string, allowedTaskTypes: string[]) => {
      const module = await json<{ uuid: string }>(
        request,
        'POST',
        `/admin/boards/${board.uuid}/modules`,
        {
          name,
          description: '',
          type: 'TASKS',
          allowedTaskTypes,
          audienceUuids: [],
          enabled: true,
          order: 0,
          startsAt: null,
          endsAt: null,
          taskInstanceSettings: null,
        },
        token,
      )
      return module.uuid
    }

    const createFlagTask = async (moduleUuid: string, title: string, description: string) =>
      json<{ uuid: string }>(
        request,
        'POST',
        `/admin/boards/${board.uuid}/modules/${moduleUuid}/tasks`,
        {
          title,
          type: 'FLAG',
          category: 'misc',
          description,
          points: 50,
          visible: true,
          submission: {
            mode: 'TEXT',
            checker: 'AUTOMATIC',
            text: { required: true, minLength: 1, maxLength: 1024 },
            files: null,
          },
          acceptedAnswers: [`flag{workspace_${suffix}_${title.replace(/\W+/g, '_')}}`],
          instance: { enabled: false },
        },
        token,
      )

    const flagModuleUuid = await createModule('Workspace FLAG', ['FLAG'])
    const manualModuleUuid = await createModule('Workspace MANUAL', ['MANUAL'])
    const instanceModuleUuid = await createModule('Workspace INSTANCE', ['INSTANCE'])
    const longModuleUuid = await createModule('Workspace LONG', ['FLAG'])

    const firstTask = await createFlagTask(
      flagModuleUuid,
      'Workspace task 1',
      [
        'A readable paragraph for the desktop task reader.',
        '',
        '```',
        'x'.repeat(200),
        '```',
        '',
        '| field | description |',
        '|---|---|',
        '| one | two |',
      ].join('\n'),
    )
    for (const index of [2, 3, 4, 5, 6]) {
      await createFlagTask(flagModuleUuid, `Workspace task ${index}`, `Task ${index} description.`)
    }
    // A single unbroken token: the administrative table may only scroll inside
    // its own panel, never widen the document.
    await createFlagTask(longModuleUuid, `Long${'L'.repeat(120)}`, 'Long token fixture.')

    await json(request, 'POST', `/admin/boards/${board.uuid}/actions`, { action: 'PUBLISH' }, token)

    const joinCode = `WORKSPACE-${suffix}`
    await json(
      request,
      'POST',
      `/admin/boards/${board.uuid}/join-code`,
      { code: joinCode },
      token,
    )

    fixture = {
      boardUuid: board.uuid,
      boardName,
      boardSlug,
      flagModuleUuid,
      manualModuleUuid,
      instanceModuleUuid,
      longModuleUuid,
      firstTaskUuid: firstTask.uuid,
      joinCode,
    }
    adminToken = token
    catalogPrefix = `Workspace catalog ${suffix}`
  })

  test.afterAll(async () => {
    await adminContext?.close()
    await participantContext?.close()
  })

  test('board and module dialogs are 1200px with a 320px rail and one scroller at 1440', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    const boardDialog = await openBoardDialog(page)
    const dialogBox = await box(boardDialog)
    expect(Math.abs(dialogBox.width - 1200)).toBeLessThanOrEqual(1)
    expect(dialogBox.left).toBeGreaterThanOrEqual(24)
    expect(1440 - dialogBox.right).toBeGreaterThanOrEqual(24)
    const boardScroller = await assertSingleScroller(boardDialog)
    await assertColumns(boardDialog, 320)

    // The footer is outside the scroller and stays reachable; scrolling the
    // body reveals the last rail field without a second scroll region.
    const footer = boardDialog.locator('[data-slot="dialog-footer"]')
    const footerBox = await box(footer)
    expect(footerBox.bottom).toBeLessThanOrEqual(900)
    await boardScroller.evaluate((element) => {
      element.scrollTop = element.scrollHeight
    })
    const settings = boardDialog.locator('[data-slot="authoring-settings"]')
    const scrollerBox = await box(boardScroller)
    const settingsBox = await box(settings)
    expect(settingsBox.bottom).toBeLessThanOrEqual(scrollerBox.bottom + 1)
    expect((await box(footer)).bottom).toBeLessThanOrEqual(900)
    await screenshot(page, 'board-dialog-1440')
    await closeDialog(page, boardDialog)

    const moduleDialog = await openModuleDialog(page)
    expect(Math.abs((await box(moduleDialog)).width - 1200)).toBeLessThanOrEqual(1)
    await assertSingleScroller(moduleDialog)
    await assertColumns(moduleDialog, 320)
    await screenshot(page, 'module-dialog-1440')
    await closeDialog(page, moduleDialog)
  })

  test('task dialog is 1360px at 1920 and keeps one editor, selection and undo across width changes', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    const taskDialog = await openTaskDialog(page, 'Workspace FLAG')
    await expect(taskDialog.getByRole('tab')).toHaveCount(0)
    const dialogBox = await box(taskDialog)
    expect(Math.abs(dialogBox.width - 1360)).toBeLessThanOrEqual(1)
    expect(dialogBox.left).toBeGreaterThanOrEqual(24)
    expect(1920 - dialogBox.right).toBeGreaterThanOrEqual(24)
    await assertSingleScroller(taskDialog)
    await assertColumns(taskDialog, 320)
    await screenshot(page, 'task-dialog-1920')

    const editor = taskDialog.locator('.ProseMirror')
    await expect(editor).toHaveCount(1)
    await editor.evaluate((element) => {
      element.dataset.editorInstance = 'kept'
    })
    await editor.click()
    await page.keyboard.type('Undoable')
    await expect(editor).toContainText('Undoable')
    await page.keyboard.press('Control+z')
    await expect(editor).not.toContainText('Undoable')
    await page.keyboard.press('Control+Shift+z')
    await expect(editor).toContainText('Undoable')

    // Shrink below the 1040px body threshold: the same DOM editor must survive
    // the stack, with its undo history and selection still working.
    await page.setViewportSize({ width: 1100, height: 900 })
    await expect.poll(async () => Math.round((await box(taskDialog)).width)).toBeLessThanOrEqual(1053)
    await expect(taskDialog.locator('.ProseMirror')).toHaveCount(1)
    expect(await taskDialog.locator('.ProseMirror').evaluate((element) =>
      (element as HTMLElement).dataset.editorInstance)).toBe('kept')
    await assertStacked(taskDialog)
    await taskDialog.locator('.ProseMirror').click()
    await page.keyboard.type(' again')
    await expect(taskDialog.locator('.ProseMirror')).toContainText('again')
    await page.keyboard.press('Control+z')
    await expect(taskDialog.locator('.ProseMirror')).not.toContainText('again')

    // Grow back: still one editor and the same two-column geometry.
    await page.setViewportSize({ width: 1440, height: 900 })
    await expect.poll(async () => Math.round((await box(taskDialog)).width)).toBeGreaterThanOrEqual(1359)
    await expect(taskDialog.locator('.ProseMirror')).toHaveCount(1)
    expect(await taskDialog.locator('.ProseMirror').evaluate((element) =>
      (element as HTMLElement).dataset.editorInstance)).toBe('kept')
    await assertColumns(taskDialog, 320)
    await screenshot(page, 'task-dialog-1440')
    await closeDialog(page, taskDialog)
  })

  test('long MANUAL and INSTANCE task forms keep their fields in the settings rail', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    const manualDialog = await openTaskDialog(page, 'Workspace MANUAL')
    await assertSingleScroller(manualDialog)
    await assertColumns(manualDialog, 320)
    await expect(manualDialog.getByRole('tab')).toHaveCount(0)
    await expect(manualDialog.locator('[data-slot="authoring-main"] #task-description')).toBeVisible()
    await expect(manualDialog.locator('[data-slot="authoring-settings"] #task-choice')).toBeVisible()
    await expect(manualDialog.locator('[data-slot="authoring-settings"] #task-text-min')).toBeVisible()
    await screenshot(page, 'task-dialog-manual-1920')
    await closeDialog(page, manualDialog)

    // Runtime instances are the separate switch: the default profile disables
    // `deployment.enabled`, so the approved-template binding endpoint is not
    // registered and the dialog says so honestly. The authoring *catalog* stays
    // available and is measured with real templates in the catalog test.
    const instanceDialog = await openTaskDialog(page, 'Workspace INSTANCE')
    await assertSingleScroller(instanceDialog)
    await assertColumns(instanceDialog, 320)
    const instanceMain = instanceDialog.locator('[data-slot="authoring-main"]')
    await expect(instanceMain).toContainText('Could not load approved templates')
    await expect(instanceMain).not.toContainText('Template management is disabled')
    const binding = await page.request.get(
      `/api/v2/boards/${fixture.boardUuid}/modules/${fixture.instanceModuleUuid}/instance-templates`,
    )
    expect(binding.status()).toBe(404)
    await expect(instanceDialog.locator('[data-slot="authoring-settings"]').getByText('Lifetime', { exact: true })).toBeVisible()
    await instanceDialog.locator('[data-slot="authoring-settings"]').getByRole('button', { name: 'Custom' }).click()
    await expect(instanceDialog.locator('[data-slot="authoring-settings"] #task-lifetime-initial')).toBeVisible()
    await screenshot(page, 'task-dialog-instance-1920')
    await closeDialog(page, instanceDialog)
  })

  test('site introduction form uses the 1100px main/settings split and stacks at 1024', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto('/admin/site')
    await expect(page.locator('.ProseMirror')).toBeVisible()

    const form = page.locator('form.authoring-home')
    const layout = form.locator('[data-slot="authoring-layout"]')
    const main = form.locator('[data-slot="authoring-main"]')
    const settings = form.locator('[data-slot="authoring-settings"]')
    const layoutBox = await box(layout)
    const mainBox = await box(main)
    const settingsBox = await box(settings)
    // Task 2 raises the shared page cap to 1600px outer / 1552px usable at
    // 1920, so 1440 now has 1180px of usable page width.
    expect(Math.abs(layoutBox.width - 1180)).toBeLessThanOrEqual(1)
    expect(Math.abs(mainBox.width - 836)).toBeLessThanOrEqual(1)
    expect(Math.abs(mainBox.top - settingsBox.top)).toBeLessThanOrEqual(1)
    expect(Math.abs(settingsBox.width - 320)).toBeLessThanOrEqual(1)
    expect(Math.abs(settingsBox.left - mainBox.right - 24)).toBeLessThanOrEqual(1)
    await expect(form.locator('#site-intro')).toBeVisible()
    await expect(settings.getByRole('button', { name: 'Save changes' })).toBeVisible()
    await capture(page, 'site-introduction-1440')

    // The widened page container gives the site form the full 1552px usable
    // width at 1920; the 1100px container query still splits it.
    await page.setViewportSize({ width: 1920, height: 1080 })
    await expect.poll(async () => Math.round((await box(form)).width)).toBe(1552)
    expect(Math.abs((await box(form)).width - 1552)).toBeLessThanOrEqual(1)
    const wideMain = await box(main)
    const wideSettings = await box(settings)
    expect(Math.abs(wideMain.width - 1208)).toBeLessThanOrEqual(1)
    expect(Math.abs(wideSettings.width - 320)).toBeLessThanOrEqual(1)
    await capture(page, 'site-introduction-1920')

    // Below the 1100px usable-width threshold the form stacks in one column.
    await page.setViewportSize({ width: 1024, height: 900 })
    await expect.poll(async () => Math.round((await box(form)).width)).toBe(764)
    const stackedForm = await box(form)
    const stackedMain = await box(main)
    const stackedSettings = await box(settings)
    expect(stackedForm.width).toBeLessThan(1100)
    expect(Math.abs(stackedMain.width - stackedForm.width)).toBeLessThanOrEqual(1)
    expect(Math.abs(stackedSettings.width - stackedForm.width)).toBeLessThanOrEqual(1)
    expect(stackedSettings.top).toBeGreaterThanOrEqual(stackedMain.bottom - 1)
  })

  test('phone dialogs are exactly viewport wide, stack at 343px and keep 44px controls', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'phone', 'Phone geometry runs in the 375px phone project')
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    const boardDialog = await openBoardDialog(page)
    const boardBox = await box(boardDialog)
    expect(Math.abs(boardBox.width - 375)).toBeLessThanOrEqual(1)
    expect(Math.abs(boardBox.left)).toBeLessThanOrEqual(1)
    expect(Math.abs(boardBox.top)).toBeLessThanOrEqual(1)
    const boardScroller = await assertSingleScroller(boardDialog)
    await assertStacked(boardDialog, 343)
    expect((await box(boardDialog.locator('#board-name'))).height).toBeGreaterThanOrEqual(44)
    const footer = boardDialog.locator('[data-slot="dialog-footer"]')
    expect((await box(footer.getByRole('button', { name: 'Cancel' }))).height).toBeGreaterThanOrEqual(44)
    await boardScroller.evaluate((element) => {
      element.scrollTop = element.scrollHeight
    })
    const boardScrollerBox = await box(boardScroller)
    expect((await box(boardDialog.locator('[data-slot="authoring-settings"]'))).bottom)
      .toBeLessThanOrEqual(boardScrollerBox.bottom + 1)
    expect((await box(footer)).bottom).toBeLessThanOrEqual(812)
    await screenshot(page, 'board-dialog-375')
    await closeDialog(page, boardDialog)

    const moduleDialog = await openModuleDialog(page)
    expect(Math.abs((await box(moduleDialog)).width - 375)).toBeLessThanOrEqual(1)
    await assertSingleScroller(moduleDialog)
    await assertStacked(moduleDialog, 343)
    await screenshot(page, 'module-dialog-375')
    await closeDialog(page, moduleDialog)

    const taskDialog = await openTaskDialog(page, 'Workspace FLAG')
    expect(Math.abs((await box(taskDialog)).width - 375)).toBeLessThanOrEqual(1)
    await assertSingleScroller(taskDialog)
    await assertStacked(taskDialog, 343)
    await expect(taskDialog.getByRole('tab')).toHaveCount(0)
    // Visual/Source stay functional on the phone, in one editor surface.
    await taskDialog.getByRole('button', { name: 'Source mode' }).click()
    const phoneSource = taskDialog.locator('#task-description')
    await expect(phoneSource).toBeVisible()
    expect(await phoneSource.evaluate((element) => getComputedStyle(element).overflowY)).toBe('auto')
    await taskDialog.getByRole('button', { name: 'Visual mode' }).click()
    await expect(taskDialog.locator('.ProseMirror')).toHaveCount(1)
    await expect(taskDialog.locator('[data-slot="dialog-footer"]').getByRole('button', { name: 'Cancel' }))
      .toBeVisible()
    expect((await box(taskDialog.locator('[data-slot="dialog-footer"]').getByRole('button', { name: 'Cancel' }))).height)
      .toBeGreaterThanOrEqual(44)
    await screenshot(page, 'task-dialog-375')
    await closeDialog(page, taskDialog)

    // The desktop sidebar becomes a Sheet drawer; its entries stay 44px tall
    // and the page never widens past the phone viewport.
    await page.goto('/boards')
    await expect(page.getByRole('heading', { name: 'Boards', exact: true })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375)
    await page.getByRole('button', { name: 'Open navigation' }).click()
    const drawer = page.getByRole('dialog')
    const boardsLink = drawer.getByRole('link', { name: 'Boards', exact: true })
    await expect(boardsLink).toBeVisible()
    expect((await box(boardsLink)).height).toBeGreaterThanOrEqual(44)
    await page.keyboard.press('Escape')
    await expect(drawer).toBeHidden()

    await page.goto('/admin/site')
    await expect(page.locator('.ProseMirror')).toBeVisible()
    const form = page.locator('form.authoring-home')
    expect(Math.abs((await box(form)).width - 343)).toBeLessThanOrEqual(1)
    const main = await box(form.locator('[data-slot="authoring-main"]'))
    const settings = await box(form.locator('[data-slot="authoring-settings"]'))
    expect(settings.top).toBeGreaterThanOrEqual(main.bottom - 1)
    expect((await box(form.getByRole('button', { name: 'Save changes' }))).height).toBeGreaterThanOrEqual(44)
    await screenshot(page, 'site-introduction-375')
  })

  test('page container uses the 1600px cap and keeps simple forms bounded', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.goto('/boards')
    await expect(page.getByRole('heading', { name: 'Boards', exact: true })).toBeVisible()
    await expect.poll(() => pageContentWidth(page)).toBe(1552)

    await page.setViewportSize({ width: 1440, height: 900 })
    await expect.poll(() => pageContentWidth(page)).toBe(1180)

    await page.setViewportSize({ width: 1024, height: 900 })
    await expect.poll(() => pageContentWidth(page)).toBe(764)

    // A simple account form stays on its previous bounded width; the wider
    // workspace never stretches every input to the page.
    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.goto('/profile')
    await expect(page.getByRole('heading', { name: 'Profile', exact: true })).toBeVisible()
    await expect.poll(() => pageContentWidth(page)).toBe(1104)
    expect((await box(page.locator('#profile-username'))).width).toBeLessThanOrEqual(1104)

    // Administrative tables use the wide page; their search control does not.
    await page.goto('/admin/users')
    await expect(page.getByRole('heading', { name: 'Users', exact: true })).toBeVisible()
    expect((await box(page.getByPlaceholder('Search users'))).width).toBeLessThanOrEqual(384)
    const table = page.locator('[data-slot="table-container"]').first()
    await expect(table).toBeVisible()
    expect((await box(table)).width).toBeGreaterThan(1200)
    await capture(page, 'admin-users-1920')

    // Administration navigation cards use the same content-width tracks. The
    // card count follows the seeded admin's roles, so rows are derived from the
    // real number of rendered cards instead of a hard-coded total.
    await page.goto('/admin')
    await expect(page.getByRole('heading', { name: 'Administration', exact: true })).toBeVisible()
    const adminCards = page.locator('[data-slot="admin-cards"] .workspace-card-grid > *')
    const adminCardCount = await adminCards.count()
    expect(adminCardCount).toBeGreaterThanOrEqual(6)
    expect(await cardTracks(adminCards)).toEqual({
      columns: 3,
      rows: Math.ceil(adminCardCount / 3),
    })
    await page.setViewportSize({ width: 1440, height: 900 })
    await expect.poll(() => pageContentWidth(page)).toBe(1180)
    expect(await cardTracks(adminCards)).toEqual({
      columns: 2,
      rows: Math.ceil(adminCardCount / 2),
    })
  })

  test('participant board uses a 320px rail and two module card columns at 1920', async () => {
    const page = participantPage!
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    test.skip(!participantUser || !participantPass, 'E2E_PARTICIPANT_USERNAME/PASSWORD are required')
    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.goto(`/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    // Real canonical code enrollment through the UI, no request interception.
    await joinBoard(page, fixture.joinCode)
    await expect(page.getByRole('heading', { name: 'Your standing' })).toBeVisible()

    const workspace = page.locator('[data-slot="workspace"]')
    const main = workspace.locator('[data-slot="workspace-main"]')
    const aside = workspace.locator('[data-slot="workspace-aside"]')
    const mainBox = await box(main)
    const asideBox = await box(aside)
    expect(Math.abs(asideBox.width - 320)).toBeLessThanOrEqual(1)
    expect(Math.abs(asideBox.left - mainBox.right - 24)).toBeLessThanOrEqual(1)
    expect(Math.abs(mainBox.top - asideBox.top)).toBeLessThanOrEqual(1)

    const cards = main.locator('[data-slot="board-modules"] .workspace-card-grid > *')
    await expect(cards).toHaveCount(4)
    expect(await cardTracks(cards)).toEqual({ columns: 2, rows: 2 })
    await capture(page, 'board-participant-1920')

    // 1440: 1180 usable -> 836 main + 320 rail + 24 gap.
    await page.setViewportSize({ width: 1440, height: 900 })
    await expect.poll(async () => Math.round((await box(main)).width)).toBe(836)
    expect(Math.abs((await box(aside)).width - 320)).toBeLessThanOrEqual(1)
    await capture(page, 'board-participant-1440')

    // Module tasks count their own width: three columns at 1920, two at 1440.
    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.goto(`/boards/${fixture.boardUuid}/modules/${fixture.flagModuleUuid}`)
    await expect(page.getByRole('heading', { name: 'Workspace FLAG' })).toBeVisible()
    const taskCards = page.locator('[data-slot="module-tasks"] .workspace-card-grid > *')
    await expect(taskCards).toHaveCount(6)
    expect(await cardTracks(taskCards)).toEqual({ columns: 3, rows: 2 })
    await capture(page, 'module-participant-1920')

    await page.setViewportSize({ width: 1440, height: 900 })
    await expect.poll(async () => Math.round((await box(page.locator('[data-slot="module-tasks"]'))).width)).toBe(1180)
    expect(await cardTracks(taskCards)).toEqual({ columns: 2, rows: 3 })
    await capture(page, 'module-participant-1440')
  })

  test('participant task stacks at 1024 and splits at 1920/1440 with an 80ch prose measure', async () => {
    const page = participantPage!
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    test.skip(!participantUser || !participantPass, 'E2E_PARTICIPANT_USERNAME/PASSWORD are required')
    await page.setViewportSize({ width: 1920, height: 1080 })
    // Join through the real canonical code flow if the previous participant
    // test has not already enrolled this fixture account.
    await page.goto(`/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()
    await joinBoard(page, fixture.joinCode)
    let historyRequests = 0
    page.on('request', (request) => {
      if (request.url().includes('/submissions/me')) historyRequests += 1
    })

    await page.goto(
      `/boards/${fixture.boardUuid}/modules/${fixture.flagModuleUuid}/tasks/${fixture.firstTaskUuid}`,
    )
    await expect(page.getByRole('heading', { name: 'Workspace task 1' })).toBeVisible()

    const workspace = page.locator('[data-slot="workspace"]')
    const main = workspace.locator('[data-slot="workspace-main"]')
    const aside = workspace.locator('[data-slot="workspace-aside"]')
    const mainBox = await box(main)
    const asideBox = await box(aside)
    expect(Math.abs(mainBox.width - 1168)).toBeLessThanOrEqual(1)
    expect(Math.abs(asideBox.width - 360)).toBeLessThanOrEqual(1)
    expect(Math.abs(asideBox.left - mainBox.right - 24)).toBeLessThanOrEqual(1)
    expect(Math.abs(mainBox.top - asideBox.top)).toBeLessThanOrEqual(1)
    await expect(aside.getByRole('textbox', { name: 'Flag', exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Your submissions' })).toBeVisible()
    expect(historyRequests).toBe(1)

    // Prose text blocks keep the 80ch measure; code uses the panel and scrolls
    // inside itself instead of widening the document.
    const description = page.locator('section[aria-labelledby="description-heading"]')
    const paragraphBox = await box(description.locator('.markdown-body p').first())
    // 80ch at the 14px reader size is ~672px, far below the 1136px panel.
    expect(paragraphBox.width).toBeGreaterThan(600)
    expect(paragraphBox.width).toBeLessThanOrEqual(680)
    const pre = description.locator('.markdown-body pre').first()
    const preBox = await box(pre)
    expect(preBox.width).toBeGreaterThan(paragraphBox.width)
    const preOverflow = await pre.evaluate((element) => ({
      scrollWidth: element.scrollWidth,
      clientWidth: element.clientWidth,
    }))
    expect(preOverflow.scrollWidth).toBeGreaterThan(preOverflow.clientWidth)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1920)
    await capture(page, 'task-participant-1920')

    // 1440: 1180 usable -> 796 main + 360 rail + 24 gap.
    await page.setViewportSize({ width: 1440, height: 900 })
    await expect.poll(async () => Math.round((await box(main)).width)).toBe(796)
    expect(Math.abs((await box(aside)).width - 360)).toBeLessThanOrEqual(1)
    await capture(page, 'task-participant-1440')

    // 1024 with the persistent 212px sidebar: 764 usable -> full-width stack.
    await page.setViewportSize({ width: 1024, height: 900 })
    await expect.poll(() => pageContentWidth(page)).toBe(764)
    const stackedMain = await box(main)
    const stackedAside = await box(aside)
    expect(Math.abs(stackedMain.width - 764)).toBeLessThanOrEqual(1)
    expect(Math.abs(stackedAside.width - 764)).toBeLessThanOrEqual(1)
    expect(stackedAside.top).toBeGreaterThanOrEqual(stackedMain.bottom - 1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1024)
    // Resizing neither refetches own history nor mounts a second form.
    expect(historyRequests).toBe(1)
    await expect(page.getByRole('textbox', { name: 'Flag', exact: true })).toHaveCount(1)
    await capture(page, 'task-participant-1024')
  })

  test('organizer preview keeps the explanation without participant controls or own-history requests', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1920, height: 1080 })
    const ownHistory: string[] = []
    page.on('request', (request) => {
      if (request.url().includes('/submissions/me')) ownHistory.push(request.url())
    })

    await page.goto(
      `/boards/${fixture.boardUuid}/modules/${fixture.flagModuleUuid}/tasks/${fixture.firstTaskUuid}`,
    )
    await expect(page.getByText('Organizer preview')).toBeVisible()
    await expect(page.getByRole('textbox', { name: 'Flag', exact: true })).toHaveCount(0)
    await expect(page.getByRole('heading', { name: 'Your submissions' })).toHaveCount(0)
    const aside = page.locator('[data-slot="workspace-aside"]')
    await expect(aside).toContainText('Submission and instance actions are available after joining the board.')
    expect(ownHistory).toHaveLength(0)
    await capture(page, 'task-preview-1920')
  })

  test('admin board puts module work beside the Join code and lifecycle rail at 1920 and 1440', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    const workspace = page.locator('[data-slot="workspace"]')
    const main = workspace.locator('[data-slot="workspace-main"]')
    const aside = workspace.locator('[data-slot="workspace-aside"]')
    const mainBox = await box(main)
    const asideBox = await box(aside)
    expect(Math.abs(asideBox.width - 320)).toBeLessThanOrEqual(1)
    expect(Math.abs(asideBox.left - mainBox.right - 24)).toBeLessThanOrEqual(1)
    expect(Math.abs(mainBox.top - asideBox.top)).toBeLessThanOrEqual(1)
    expect(mainBox.width).toBeGreaterThanOrEqual(1207)

    // Module work now starts at or above the Lifecycle section instead of below
    // it; Join code and Lifecycle live in the rail.
    const modulesTop = (await box(page.locator('#admin-modules-heading'))).top
    const lifecycleTop = (await box(page.locator('#lifecycle-heading'))).top
    expect(modulesTop).toBeLessThanOrEqual(lifecycleTop + 1)
    await expect(main.locator('#admin-modules-heading')).toBeVisible()
    await expect(aside.locator('#join-code-heading')).toBeVisible()
    await expect(aside.locator('#lifecycle-heading')).toBeVisible()

    const toggle = aside.getByRole('button', { name: 'Show lifecycle actions' })
    expect(await toggle.getAttribute('aria-expanded')).toBe('false')
    await expect(page.getByRole('button', { name: 'Delete board' })).toHaveCount(0)
    await capture(page, 'admin-board-1920')

    await toggle.click()
    await expect(page.getByRole('button', { name: 'Delete board' })).toBeVisible()
    await capture(page, 'admin-board-1920-lifecycle-open')
    await page.getByRole('button', { name: 'Delete board' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: 'Delete board' })).toBeVisible()
    await dialog.getByRole('button', { name: 'Cancel' }).click()

    // 1440: 1180 usable -> 836 main + 320 rail + 24 gap; the ordinary module
    // table fits without local scrolling.
    await page.setViewportSize({ width: 1440, height: 900 })
    await expect.poll(async () => Math.round((await box(main)).width)).toBe(836)
    expect(Math.abs((await box(aside)).width - 320)).toBeLessThanOrEqual(1)
    const flagScroller = (await moduleSection(page, 'Workspace FLAG')).locator('[data-slot="table-container"]').first()
    const flagMetrics = await flagScroller.evaluate((element) => ({
      scrollWidth: element.scrollWidth,
      clientWidth: element.clientWidth,
    }))
    expect(flagMetrics.scrollWidth).toBeLessThanOrEqual(flagMetrics.clientWidth + 1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1440)
    await capture(page, 'admin-board-1440')
  })

  test('admin board keeps a long table token inside its own scroller at 1024', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1024, height: 900 })
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    const workspace = page.locator('[data-slot="workspace"]')
    const mainBox = await box(workspace.locator('[data-slot="workspace-main"]'))
    const asideBox = await box(workspace.locator('[data-slot="workspace-aside"]'))
    expect(Math.abs(mainBox.width - 764)).toBeLessThanOrEqual(1)
    expect(Math.abs(asideBox.width - 764)).toBeLessThanOrEqual(1)
    expect(asideBox.top).toBeGreaterThanOrEqual(mainBox.bottom - 1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1024)

    const scroller = (await moduleSection(page, 'Workspace LONG')).locator('[data-slot="table-container"]').first()
    const metrics = await scroller.evaluate((element) => ({
      scrollWidth: element.scrollWidth,
      clientWidth: element.clientWidth,
    }))
    expect(metrics.scrollWidth).toBeGreaterThan(metrics.clientWidth)
    await capture(page, 'admin-board-1024')
  })

  test('legacy invite action wraps inside 375px without widening the document', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'phone', 'Phone geometry runs in the 375px phone project')
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    const legacy = page.getByRole('button', { name: /Legacy invite code/ })
    await expect(legacy).toBeVisible()
    const legacyBox = await box(legacy)
    expect(legacyBox.width).toBeLessThanOrEqual(343)
    expect(legacyBox.height).toBeGreaterThanOrEqual(44)
    await expect(legacy).toHaveCSS('white-space', 'normal')
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375)
    await capture(page, 'admin-board-375')
  })

  test('deployment template catalog measures real three- and six-template grids', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1920, height: 1080 })

    // The authoring catalog is a separate switch from runtime instances and is
    // always served, so real templates are created through the documented API
    // and measured here. No provider, no runtime credential, no interception.
    const ownCount = (items: Array<{ name: string }>) =>
      items.filter((item) => item.name.startsWith(`${catalogPrefix} `)).length
    const three = await createCatalogTemplates(page.request, adminToken, catalogPrefix, 3)
    // Three real templates of this run's own, plus whatever the shared
    // default-profile suite already created, are in the catalog.
    expect(ownCount(three.items)).toBe(3)
    expect(three.items.length).toBeGreaterThanOrEqual(3)
    await page.goto('/admin/templates')
    await expect(page.getByRole('heading', { name: 'Deployment templates' })).toBeVisible()
    await expect(page.getByText('Template management is disabled')).toHaveCount(0)
    await expect(page.getByText('Could not load deployment templates')).toHaveCount(0)

    const cards = page.locator('[data-slot="admin-cards"] .workspace-card-grid > *')
    await expect(cards).toHaveCount(three.items.length)
    expect(await cardTracks(cards)).toEqual({ columns: 3, rows: gridRows(three.items.length, 3) })

    // Every card is a real navigable link to its own template route, and the
    // run's own three templates are real rendered records.
    const links = page.locator('[data-slot="admin-cards"] a[href^="/admin/templates/"]')
    await expect(links).toHaveCount(three.items.length)
    expect(await links.first().getAttribute('href')).toMatch(/^\/admin\/templates\/[0-9a-f-]{36}$/)
    const ownLinks = page.getByRole('link', { name: new RegExp(catalogPrefix) })
    await expect(ownLinks).toHaveCount(3)
    await capture(page, 'admin-templates-1920-3')

    // 1440 -> 1180 usable, below the 1200px three-column threshold.
    await page.setViewportSize({ width: 1440, height: 900 })
    await expect.poll(() => pageContentWidth(page)).toBe(1180)
    expect(await cardTracks(cards)).toEqual({ columns: 2, rows: gridRows(three.items.length, 2) })
    await capture(page, 'admin-templates-1440-3')

    // 1024 -> 764 usable: two columns and no page overflow.
    await page.setViewportSize({ width: 1024, height: 768 })
    await expect.poll(() => pageContentWidth(page)).toBe(764)
    expect(await cardTracks(cards)).toEqual({ columns: 2, rows: gridRows(three.items.length, 2) })
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(1024)

    // Three more templates through the same API, then the product's own Refresh
    // control: six of this run's records in a three-column grid at 1920.
    const six = await createCatalogTemplates(page.request, adminToken, catalogPrefix, 6)
    expect(ownCount(six.items)).toBe(6)
    expect(six.items.length).toBeGreaterThanOrEqual(6)
    expect(six.created).toBe(3)
    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.getByRole('button', { name: 'Refresh', exact: true }).click()
    await expect(cards).toHaveCount(six.items.length)
    expect(await cardTracks(cards)).toEqual({ columns: 3, rows: gridRows(six.items.length, 3) })
    await expect(ownLinks).toHaveCount(6)
    const listed = await json<{ items: Array<{ name: string }> }>(
      page.request,
      'GET',
      '/admin/deployment-templates?limit=100&offset=0',
      undefined,
      adminToken,
    )
    expect(listed.items).toHaveLength(six.items.length)
    expect(await cards.count()).toBe(listed.items.length)
    await capture(page, 'admin-templates-1920-6')
  })

  test('workspace and card thresholds switch on the measured container width', async () => {
    const admin = adminPage
    const page = participantPage!
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    test.skip(!participantUser || !participantPass, 'E2E_PARTICIPANT_USERNAME/PASSWORD are required')

    // The 1100px usable-width threshold has 1px neighbours: 1360px viewport
    // gives 1100px usable (split, 716px task main); 1359 gives 1099 (stacked).
    await page.setViewportSize({ width: 1360, height: 900 })
    await page.goto(`/boards/${fixture.boardUuid}/modules/${fixture.flagModuleUuid}/tasks/${fixture.firstTaskUuid}`)
    await expect(page.getByRole('heading', { name: 'Workspace task 1' })).toBeVisible()
    await expect.poll(() => pageContentWidth(page)).toBe(1100)
    const taskMain = page.locator('[data-slot="workspace-main"]')
    const taskAside = page.locator('[data-slot="workspace-aside"]')
    expect(Math.abs((await box(taskMain)).width - 716)).toBeLessThanOrEqual(1)
    expect(Math.abs((await box(taskAside)).width - 360)).toBeLessThanOrEqual(1)
    expect(Math.abs((await box(taskAside)).left - (await box(taskMain)).right - 24)).toBeLessThanOrEqual(1)
    await capture(page, 'task-participant-1360-threshold')

    await page.setViewportSize({ width: 1359, height: 900 })
    await expect.poll(() => pageContentWidth(page)).toBe(1099)
    expect(Math.abs((await box(taskMain)).width - 1099)).toBeLessThanOrEqual(1)
    expect(Math.abs((await box(taskAside)).width - 1099)).toBeLessThanOrEqual(1)
    expect((await box(taskAside)).top).toBeGreaterThanOrEqual((await box(taskMain)).bottom - 1)

    // Board modules switch at their own 760px main-column width: 1364px
    // viewport gives 1104 usable -> 760 main (two columns); 1363 -> 759 (one).
    await page.setViewportSize({ width: 1364, height: 900 })
    await page.goto(`/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()
    await joinBoard(page, fixture.joinCode)
    await expect.poll(() => pageContentWidth(page)).toBe(1104)
    const boardMain = page.locator('[data-slot="workspace-main"]')
    expect(Math.abs((await box(boardMain)).width - 760)).toBeLessThanOrEqual(1)
    const boardCards = page.locator('[data-slot="board-modules"] .workspace-card-grid > *')
    await expect(boardCards).toHaveCount(4)
    expect(await cardTracks(boardCards)).toEqual({ columns: 2, rows: 2 })

    await page.setViewportSize({ width: 1363, height: 900 })
    await expect.poll(() => pageContentWidth(page)).toBe(1103)
    expect(Math.abs((await box(boardMain)).width - 759)).toBeLessThanOrEqual(1)
    expect(await cardTracks(boardCards)).toEqual({ columns: 1, rows: 4 })

    // Module tasks switch at 1200px of their own width: 1460px viewport gives
    // 1200 usable (three columns); 1459 -> 1199 (two).
    await page.setViewportSize({ width: 1460, height: 900 })
    await page.goto(`/boards/${fixture.boardUuid}/modules/${fixture.flagModuleUuid}`)
    await expect(page.getByRole('heading', { name: 'Workspace FLAG' })).toBeVisible()
    await expect.poll(() => pageContentWidth(page)).toBe(1200)
    const taskCards = page.locator('[data-slot="module-tasks"] .workspace-card-grid > *')
    await expect(taskCards).toHaveCount(6)
    expect(await cardTracks(taskCards)).toEqual({ columns: 3, rows: 2 })

    await page.setViewportSize({ width: 1459, height: 900 })
    await expect.poll(() => pageContentWidth(page)).toBe(1199)
    expect(await cardTracks(taskCards)).toEqual({ columns: 2, rows: 3 })

    // Administration tracks: 720px for two columns, 1200px for three.
    await admin.setViewportSize({ width: 980, height: 900 })
    await admin.goto('/admin')
    await expect(admin.getByRole('heading', { name: 'Administration', exact: true })).toBeVisible()
    await expect.poll(() => pageContentWidth(admin)).toBe(720)
    const hubCards = admin.locator('[data-slot="admin-cards"] .workspace-card-grid > *')
    const hubCount = await hubCards.count()
    expect(await cardTracks(hubCards)).toEqual({ columns: 2, rows: Math.ceil(hubCount / 2) })

    await admin.setViewportSize({ width: 979, height: 900 })
    await expect.poll(() => pageContentWidth(admin)).toBe(719)
    expect(await cardTracks(hubCards)).toEqual({ columns: 1, rows: hubCount })

    // The same 1200px boundary on the real template catalog.
    const catalog = await json<{ items: Array<{ uuid: string }> }>(
      admin.request,
      'GET',
      '/admin/deployment-templates?limit=100&offset=0',
      undefined,
      adminToken,
    )
    expect(catalog.items.length).toBeGreaterThanOrEqual(6)
    await admin.setViewportSize({ width: 1460, height: 900 })
    await admin.goto('/admin/templates')
    await expect(admin.getByRole('heading', { name: 'Deployment templates' })).toBeVisible()
    await expect.poll(() => pageContentWidth(admin)).toBe(1200)
    const templateCards = admin.locator('[data-slot="admin-cards"] .workspace-card-grid > *')
    await expect(templateCards).toHaveCount(catalog.items.length)
    expect(await cardTracks(templateCards)).toEqual({
      columns: 3,
      rows: gridRows(catalog.items.length, 3),
    })

    await admin.setViewportSize({ width: 1459, height: 900 })
    await expect.poll(() => pageContentWidth(admin)).toBe(1199)
    expect(await cardTracks(templateCards)).toEqual({
      columns: 2,
      rows: gridRows(catalog.items.length, 2),
    })
  })

  test('200% CSS zoom reflows columns instead of shrinking controls', async () => {
    const page = participantPage!
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    test.skip(!participantUser || !participantPass, 'E2E_PARTICIPANT_USERNAME/PASSWORD are required')

    // CSS `zoom` is an honest layout surrogate: the layout viewport becomes
    // 1920 / 2 = 960 CSS px, so the persistent sidebar and the page padding
    // leave the same 700px usable width a real 200% browser zoom produces. It is
    // not OS zoom, and `vw` units are deliberately not adjusted by it; the
    // dialog/`vw` behaviour of a real 200% zoom is covered by the device-metrics
    // test below.
    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.goto(`/boards/${fixture.boardUuid}/modules/${fixture.flagModuleUuid}/tasks/${fixture.firstTaskUuid}`)
    await expect(page.getByRole('heading', { name: 'Workspace task 1' })).toBeVisible()
    await joinBoard(page, fixture.joinCode)
    await setCssZoom(page, 2)
    await expect.poll(() => pageContentWidth(page)).toBe(700)

    const taskMain = page.locator('[data-slot="workspace-main"]')
    const taskAside = page.locator('[data-slot="workspace-aside"]')
    const zoomMain = await zoomedBox(taskMain, 2)
    const zoomAside = await zoomedBox(taskAside, 2)
    expect(Math.abs(zoomMain.width - 700)).toBeLessThanOrEqual(1)
    expect(Math.abs(zoomAside.width - 700)).toBeLessThanOrEqual(1)
    expect(zoomAside.top).toBeGreaterThanOrEqual(zoomMain.bottom - 1)

    // The submission control keeps its size and stays focusable; nothing is
    // squeezed to fit a column that no longer exists.
    const flag = page.getByRole('textbox', { name: 'Flag', exact: true })
    await expect(flag).toHaveCount(1)
    expect((await zoomedBox(flag, 2)).width).toBeLessThanOrEqual(700)
    expect((await zoomedBox(flag, 2)).height).toBeGreaterThanOrEqual(32)
    await flag.focus()
    expect(await flag.evaluate((element) => document.activeElement === element)).toBe(true)

    // No page-level horizontal scrolling: document scroll width is scaled by
    // the same zoom factor.
    const overflow = await page.evaluate(() => ({
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
    }))
    expect(overflow.scrollWidth).toBeLessThanOrEqual(overflow.innerWidth * 2 + 1)
    await capture(page, 'task-participant-200zoom')

    // Module cards at an effective 700px: one column, never three squeezed
    // columns.
    await setCssZoom(page, 1)
    await page.goto(`/boards/${fixture.boardUuid}/modules/${fixture.flagModuleUuid}`)
    await expect(page.getByRole('heading', { name: 'Workspace FLAG' })).toBeVisible()
    await setCssZoom(page, 2)
    await expect.poll(() => pageContentWidth(page)).toBe(700)
    const taskCards = page.locator('[data-slot="module-tasks"] .workspace-card-grid > *')
    await expect(taskCards).toHaveCount(6)
    expect(await cardTracks(taskCards)).toEqual({ columns: 1, rows: 6 })
    expect((await zoomedBox(taskCards.first(), 2)).width).toBeGreaterThan(600)
    await capture(page, 'module-participant-200zoom')

    // The site form stacks at the same effective width and its save action
    // stays reachable, not clipped. This is the organizer surface, so it uses
    // the admin session rather than the participant one.
    await setCssZoom(page, 1)
    const admin = adminPage
    await admin.setViewportSize({ width: 1920, height: 1080 })
    await admin.goto('/admin/site')
    await expect(admin.locator('.ProseMirror')).toBeVisible()
    await setCssZoom(admin, 2)
    await expect.poll(() => pageContentWidth(admin)).toBe(700)
    const siteForm = admin.locator('form.authoring-home')
    expect((await zoomedBox(siteForm, 2)).width).toBeLessThanOrEqual(700)
    const save = siteForm.getByRole('button', { name: 'Save changes' })
    await save.scrollIntoViewIfNeeded()
    await expect(save).toBeInViewport()
    await setCssZoom(admin, 1)
  })

  test('the effective 960px layout of a 200% browser zoom keeps dialogs and focus usable', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')

    // Real Chromium device metrics: a 1920x1080 screen at 200% zoom has a
    // 960x540 CSS layout viewport at devicePixelRatio 2. Unlike
    // `Emulation.setPageScaleFactor` (visual viewport only) and unlike CSS
    // `zoom`, media/container queries and `vw` units really reflow here.
    await page.setViewportSize({ width: 1920, height: 1080 })
    const restore = await emulateBrowserZoomLayout(page)
    try {
      expect(await page.evaluate(() => window.innerWidth)).toBe(960)
      expect(await page.evaluate(() => window.devicePixelRatio)).toBe(2)

      await page.goto(`/admin/boards/${fixture.boardUuid}`)
      await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()
      await expect.poll(() => pageContentWidth(page)).toBe(700)
      const main = page.locator('[data-slot="workspace-main"]')
      const aside = page.locator('[data-slot="workspace-aside"]')
      expect(Math.abs((await box(main)).width - 700)).toBeLessThanOrEqual(1)
      expect(Math.abs((await box(aside)).width - 700)).toBeLessThanOrEqual(1)
      expect((await box(aside)).top).toBeGreaterThanOrEqual((await box(main)).bottom - 1)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(960)

      // The 1200px dialog is capped by the real viewport: 912px with 24px
      // clearance on both sides, one scroller, stacked body.
      const dialog = await openBoardDialog(page)
      const dialogBox = await box(dialog)
      expect(Math.abs(dialogBox.width - 912)).toBeLessThanOrEqual(1)
      expect(dialogBox.left).toBeGreaterThanOrEqual(24)
      expect(960 - dialogBox.right).toBeGreaterThanOrEqual(24)
      await assertSingleScroller(dialog)
      await assertStacked(dialog)

      // The footer stays outside the scroller and inside the 540px viewport.
      const footer = dialog.locator('[data-slot="dialog-footer"]')
      expect((await box(footer)).bottom).toBeLessThanOrEqual(540)
      const save = dialog.getByRole('button', { name: 'Save changes' })
      await expect(save).toBeVisible()

      // Source mode is the exact-bytes guard: it scrolls internally and the
      // save action is still reachable by keyboard, never clipped.
      await dialog.getByRole('button', { name: 'Source mode' }).click()
      const source = dialog.locator('#board-description')
      await source.fill(['Long source at 200% zoom', '', '```', 'x'.repeat(4000), '```'].join('\n'))
      expect(await source.evaluate((element) => getComputedStyle(element).overflowY)).toBe('auto')
      expect(await source.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true)
      let reachedSave = false
      for (let step = 0; step < 120; step += 1) {
        await page.keyboard.press('Tab')
        if (await save.evaluate((element) => document.activeElement === element)) {
          reachedSave = true
          break
        }
      }
      expect(reachedSave).toBe(true)
      await expect(save).toBeInViewport()
      await screenshot(page, 'board-dialog-960-effective-200zoom')
      await closeDialog(page, dialog)

      // The organizer preview stacks at 700px as well; its rail keeps the
      // explanation and still offers no participant form or own history.
      await page.goto(`/boards/${fixture.boardUuid}/modules/${fixture.flagModuleUuid}/tasks/${fixture.firstTaskUuid}`)
      await expect(page.getByRole('heading', { name: 'Workspace task 1' })).toBeVisible()
      const taskMain = page.locator('[data-slot="workspace-main"]')
      const taskAside = page.locator('[data-slot="workspace-aside"]')
      expect(Math.abs((await box(taskMain)).width - 700)).toBeLessThanOrEqual(1)
      expect(Math.abs((await box(taskAside)).width - 700)).toBeLessThanOrEqual(1)
      expect((await box(taskAside)).top).toBeGreaterThanOrEqual((await box(taskMain)).bottom - 1)
      await expect(page.getByText('Organizer preview')).toBeVisible()
      await expect(page.getByRole('textbox', { name: 'Flag', exact: true })).toHaveCount(0)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(960)
      await capture(page, 'task-preview-960-effective-200zoom')
    } finally {
      await restore()
    }
  })

  test('board editor authors exact Unicode bytes, counts runes and saves once', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    const dialog = await openBoardDialog(page)
    // Direct authoring: no outer Write/Preview tabs anywhere on this surface.
    await expect(dialog.getByRole('tab')).toHaveCount(0)
    const editor = dialog.locator('.ProseMirror')
    await expect(editor).toHaveCount(1)
    await editor.click()
    await page.keyboard.press('Control+a')
    await page.keyboard.type('Direct authoring')
    await expect(editor).toContainText('Direct authoring')

    // Source mode holds the exact bytes, including multi-byte characters.
    const exact = [
      '# Ünïcode 🎯',
      '',
      'Emoji: 🎯🎯 and combining é',
      '',
      '```python',
      'print("héllo")',
      '```',
    ].join('\n')
    await dialog.getByRole('button', { name: 'Source mode' }).click()
    const source = dialog.locator('#board-description')
    await source.fill(exact)
    const runes = Array.from(exact).length
    const utf16 = exact.length
    expect(utf16).toBeGreaterThan(runes)
    await expect(dialog.locator('.markdown-editor-shell')).toContainText(`${runes} / 50000`)

    // 25,001 emoji are 50,002 UTF-16 units but only 25,001 Go runes, so the
    // form is still valid: the counter must count code points.
    await source.fill('🎯'.repeat(25_001))
    await expect(dialog.locator('.markdown-editor-shell')).toContainText('25001 / 50000')
    await expect(dialog.getByRole('alert')).toHaveCount(0)

    // One more rune crosses the 50,000 limit; the form owns one message.
    await source.fill('🎯'.repeat(50_001))
    const alert = dialog.getByRole('alert').filter({ hasText: 'at most 50,000 characters' })
    await expect(alert).toHaveCount(1)
    await expect(dialog.locator('.markdown-editor-shell')).toContainText('50001 / 50000')
    const describedBy = await source.getAttribute('aria-describedby')
    expect(describedBy).toBeTruthy()
    await expect(dialog.locator(`#${describedBy}`)).toContainText('at most 50,000 characters')
    expect(await source.getAttribute('aria-invalid')).toBe('true')

    let patches = 0
    page.on('request', (request) => {
      if (request.method() === 'PATCH' && request.url().includes(`/admin/boards/${fixture.boardUuid}`)) {
        patches += 1
      }
    })
    await dialog.getByRole('button', { name: 'Save changes' }).click()
    await expect(alert).toHaveCount(1)
    await page.waitForTimeout(300)
    expect(patches).toBe(0)

    // A valid payload saves exactly once and stores the exact bytes.
    await source.fill(exact)
    await expect(alert).toHaveCount(0)
    await dialog.getByRole('button', { name: 'Save changes' }).click()
    await expect(dialog).toBeHidden()
    expect(patches).toBe(1)
    const stored = await json<{ description: string }>(
      page.request,
      'GET',
      `/admin/boards/${fixture.boardUuid}`,
      undefined,
      adminToken,
    )
    expect(stored.description).toBe(exact)

    // Reopening shows the same bytes; cancelling never rewrites them.
    const reopened = await openBoardDialog(page)
    await reopened.getByRole('button', { name: 'Source mode' }).click()
    expect(await reopened.locator('#board-description').inputValue()).toBe(exact)
    await reopened.getByRole('button', { name: 'Cancel' }).click()
    await expect(reopened).toBeHidden()
    await expect(page.getByRole('button', { name: 'Discard changes' })).toHaveCount(0)
  })

  test('navigation and resizes add no duplicate editor, request or hidden form target', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    let boardGets = 0
    page.on('request', (request) => {
      if (
        request.method() === 'GET'
        && new RegExp(`/api/v2/admin/boards/${fixture.boardUuid}$`).test(request.url())
      ) {
        boardGets += 1
      }
    })

    await page.setViewportSize({ width: 1920, height: 1080 })
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()
    // No editor or form field is mounted outside a dialog.
    await expect(page.locator('.ProseMirror')).toHaveCount(0)
    await expect(page.locator('#board-name, #module-name, #task-title')).toHaveCount(0)
    const afterLoad = boardGets

    // Responsive resizes reflow only: no refetch, no second mount.
    for (const [width, usable] of [[1440, 1180], [1024, 764], [1920, 1552]] as const) {
      await page.setViewportSize({ width, height: 900 })
      await expect.poll(() => pageContentWidth(page)).toBe(usable)
    }
    expect(boardGets).toBe(afterLoad)
    await expect(page.locator('.ProseMirror')).toHaveCount(0)

    // Opening and closing a dialog leaves exactly one editor and no orphan
    // focus target behind.
    const moduleDialog = await openModuleDialog(page)
    await expect(moduleDialog.locator('.ProseMirror')).toHaveCount(1)
    await closeDialog(page, moduleDialog)
    await expect(page.locator('.ProseMirror')).toHaveCount(0)
    await expect(page.locator('[role="dialog"]')).toHaveCount(0)
    await expect(page.locator('#module-name')).toHaveCount(0)

    // SPA navigation away and back: one page form each time, no stale dialog.
    await page.getByRole('link', { name: 'Boards', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Boards', exact: true })).toBeVisible()
    await expect(page.locator('.ProseMirror')).toHaveCount(0)
    await page.getByRole('link', { name: 'Admin', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Administration', exact: true })).toBeVisible()
    await expect(page.locator('.ProseMirror')).toHaveCount(0)
    await expect(page.locator('[role="dialog"]')).toHaveCount(0)
  })

  test('phone catalog and admin hub use one card column at 375', async () => {
    const page = adminPage
    test.skip(test.info().project.name !== 'phone', 'Phone geometry runs in the 375px phone project')

    await page.goto('/admin/templates')
    await expect(page.getByRole('heading', { name: 'Deployment templates' })).toBeVisible()
    const cards = page.locator('[data-slot="admin-cards"] .workspace-card-grid > *')
    const count = await cards.count()
    expect(count).toBeGreaterThanOrEqual(1)
    expect(await cardTracks(cards)).toEqual({ columns: 1, rows: count })
    expect((await box(cards.first())).width).toBeLessThanOrEqual(343)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375)
    await capture(page, 'admin-templates-375')

    await page.goto('/admin')
    await expect(page.getByRole('heading', { name: 'Administration', exact: true })).toBeVisible()
    const hubCards = page.locator('[data-slot="admin-cards"] .workspace-card-grid > *')
    const hubCount = await hubCards.count()
    expect(hubCount).toBeGreaterThanOrEqual(3)
    expect(await cardTracks(hubCards)).toEqual({ columns: 1, rows: hubCount })
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375)
    await capture(page, 'admin-hub-375')
  })

  test('participant board and module cards fall back to one column at 375', async () => {
    const page = participantPage!
    test.skip(test.info().project.name !== 'phone', 'Phone geometry runs in the 375px phone project')
    test.skip(!participantUser || !participantPass, 'E2E_PARTICIPANT_USERNAME/PASSWORD are required')

    await page.goto(`/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()
    await joinBoard(page, fixture.joinCode)

    const main = page.locator('[data-slot="workspace-main"]')
    const aside = page.locator('[data-slot="workspace-aside"]')
    expect(Math.abs((await box(main)).width - 343)).toBeLessThanOrEqual(1)
    expect(Math.abs((await box(aside)).width - 343)).toBeLessThanOrEqual(1)
    expect((await box(aside)).top).toBeGreaterThanOrEqual((await box(main)).bottom - 1)
    const boardCards = main.locator('[data-slot="board-modules"] .workspace-card-grid > *')
    await expect(boardCards).toHaveCount(4)
    expect(await cardTracks(boardCards)).toEqual({ columns: 1, rows: 4 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375)
    await capture(page, 'board-participant-375')

    await page.goto(`/boards/${fixture.boardUuid}/modules/${fixture.flagModuleUuid}`)
    await expect(page.getByRole('heading', { name: 'Workspace FLAG' })).toBeVisible()
    const taskCards = page.locator('[data-slot="module-tasks"] .workspace-card-grid > *')
    await expect(taskCards).toHaveCount(6)
    expect(await cardTracks(taskCards)).toEqual({ columns: 1, rows: 6 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375)
    await capture(page, 'module-participant-375')
  })

  test('participant task stacks at 375 with one 44px form and a reachable footer', async () => {
    const page = participantPage!
    test.skip(test.info().project.name !== 'phone', 'Phone geometry runs in the 375px phone project')
    test.skip(!participantUser || !participantPass, 'E2E_PARTICIPANT_USERNAME/PASSWORD are required')

    await page.goto(`/boards/${fixture.boardUuid}/modules/${fixture.flagModuleUuid}/tasks/${fixture.firstTaskUuid}`)
    await expect(page.getByRole('heading', { name: 'Workspace task 1' })).toBeVisible()
    const main = page.locator('[data-slot="workspace-main"]')
    const aside = page.locator('[data-slot="workspace-aside"]')
    expect(Math.abs((await box(main)).width - 343)).toBeLessThanOrEqual(1)
    expect(Math.abs((await box(aside)).width - 343)).toBeLessThanOrEqual(1)
    expect((await box(aside)).top).toBeGreaterThanOrEqual((await box(main)).bottom - 1)

    // Exactly one submission form, with phone-sized controls.
    const flag = page.getByRole('textbox', { name: 'Flag', exact: true })
    await expect(flag).toHaveCount(1)
    expect((await box(flag)).height).toBeGreaterThanOrEqual(44)
    const submit = page.getByRole('button', { name: 'Submit flag' })
    await expect(submit).toHaveCount(1)
    expect((await box(submit)).height).toBeGreaterThanOrEqual(44)

    // The full-width history below the workspace stays reachable and the page
    // never scrolls horizontally.
    const history = page.getByRole('heading', { name: 'Your submissions' })
    await history.scrollIntoViewIfNeeded()
    await expect(history).toBeInViewport()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(375)
    await capture(page, 'task-participant-375')
  })
})
