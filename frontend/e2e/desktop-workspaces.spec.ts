import { expect, test, type APIRequestContext, type Locator, type Page } from '@playwright/test'

/**
 * Task 1 desktop-authoring geometry, measured in a real browser.
 *
 *   ACTFB_E2E=1 \
 *   E2E_BASE_URL=http://127.0.0.1:5173 \
 *   E2E_API_URL=http://127.0.0.1:8080/api/v2 \
 *   E2E_ADMIN_USERNAME=... E2E_ADMIN_PASSWORD=... \
 *   [E2E_SCREENSHOT_DIR=/tmp/opencode/desktop-workspaces/task1] \
 *   bun run test:e2e -- desktop-workspaces.spec.ts
 *
 * This task covers the four authoring surfaces only: the three dialogs and the
 * site-introduction form. Reader/organizer pages are Task 2 and are not
 * asserted here. Fixtures are created through the real HTTP API; no SQL and no
 * token in browser storage.
 */
const enabled = process.env.ACTFB_E2E === '1'
const apiUrl = (process.env.E2E_API_URL || 'http://127.0.0.1:8080/api/v2').replace(/\/+$/, '')
const baseUrl = process.env.E2E_BASE_URL || 'http://127.0.0.1:5173'
const adminUser = process.env.E2E_ADMIN_USERNAME ?? ''
const adminPass = process.env.E2E_ADMIN_PASSWORD ?? ''
const screenshotDir = process.env.E2E_SCREENSHOT_DIR

interface Fixture {
  boardUuid: string
  boardName: string
  flagModuleUuid: string
  manualModuleUuid: string
  instanceModuleUuid: string
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

async function signIn(page: Page): Promise<void> {
  await page.goto('/sign-in')
  await page.locator('#sign-in-username').fill(adminUser)
  await page.locator('#sign-in-password').fill(adminPass)
  await page.getByRole('button', { name: 'Sign in' }).click()
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

  test.beforeAll(async ({ request }) => {
    if (!adminUser || !adminPass) throw new Error('E2E_ADMIN_USERNAME and E2E_ADMIN_PASSWORD are required')
    const login = await request.post(`${apiUrl}/auth/login`, {
      data: { username: adminUser, password: adminPass },
    })
    if (!login.ok()) throw new Error(`admin login failed: ${login.status()} ${await login.text()}`)
    const token = ((await login.json()) as { accessToken: string }).accessToken

    const suffix = `${Date.now()}`
    const boardName = `Desktop workspace ${suffix}`
    const board = await json<{ uuid: string }>(request, 'POST', '/admin/boards', {
      slug: `desktop-workspace-${suffix}`,
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

    fixture = {
      boardUuid: board.uuid,
      boardName,
      flagModuleUuid: await createModule('Workspace FLAG', ['FLAG']),
      manualModuleUuid: await createModule('Workspace MANUAL', ['MANUAL']),
      instanceModuleUuid: await createModule('Workspace INSTANCE', ['INSTANCE']),
    }
  })

  test('board and module dialogs are 1200px with a 320px rail and one scroller at 1440', async ({ page }) => {
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1440, height: 900 })
    await signIn(page)
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

  test('task dialog is 1360px at 1920 and keeps one editor, selection and undo across width changes', async ({ page }) => {
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1920, height: 1080 })
    await signIn(page)
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    const taskDialog = await openTaskDialog(page, 'Workspace FLAG')
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

  test('long MANUAL and INSTANCE task forms keep their fields in the settings rail', async ({ page }) => {
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1920, height: 1080 })
    await signIn(page)
    await page.goto(`/admin/boards/${fixture.boardUuid}`)
    await expect(page.getByRole('heading', { name: fixture.boardName })).toBeVisible()

    const manualDialog = await openTaskDialog(page, 'Workspace MANUAL')
    await assertSingleScroller(manualDialog)
    await assertColumns(manualDialog, 320)
    await expect(manualDialog.locator('[data-slot="authoring-main"] #task-description')).toBeVisible()
    await expect(manualDialog.locator('[data-slot="authoring-settings"] #task-choice')).toBeVisible()
    await expect(manualDialog.locator('[data-slot="authoring-settings"] #task-text-min')).toBeVisible()
    await screenshot(page, 'task-dialog-manual-1920')
    await closeDialog(page, manualDialog)

    // The default profile disables deployment templates, so the approved
    // template list errors honestly. The lifetime controls still render; this
    // is a layout check, not provider execution.
    const instanceDialog = await openTaskDialog(page, 'Workspace INSTANCE')
    await assertSingleScroller(instanceDialog)
    await assertColumns(instanceDialog, 320)
    await expect(instanceDialog.locator('[data-slot="authoring-main"]')).toContainText(/template/i)
    await expect(instanceDialog.locator('[data-slot="authoring-settings"]').getByText('Lifetime', { exact: true })).toBeVisible()
    await instanceDialog.locator('[data-slot="authoring-settings"]').getByRole('button', { name: 'Custom' }).click()
    await expect(instanceDialog.locator('[data-slot="authoring-settings"] #task-lifetime-initial')).toBeVisible()
    await screenshot(page, 'task-dialog-instance-1920')
    await closeDialog(page, instanceDialog)
  })

  test('site introduction form uses the 1100px main/settings split and stacks at 1024', async ({ page }) => {
    test.skip(test.info().project.name !== 'chromium', 'Desktop geometry runs in the chromium project')
    await page.setViewportSize({ width: 1440, height: 900 })
    await signIn(page)
    await page.goto('/admin/site')
    await expect(page.locator('.ProseMirror')).toBeVisible()

    const form = page.locator('form.authoring-home')
    const layout = form.locator('[data-slot="authoring-layout"]')
    const main = form.locator('[data-slot="authoring-main"]')
    const settings = form.locator('[data-slot="authoring-settings"]')
    const layoutBox = await box(layout)
    const mainBox = await box(main)
    const settingsBox = await box(settings)
    expect(Math.abs(layoutBox.width - 1104)).toBeLessThanOrEqual(1)
    expect(Math.abs(mainBox.top - settingsBox.top)).toBeLessThanOrEqual(1)
    expect(Math.abs(settingsBox.width - 320)).toBeLessThanOrEqual(1)
    expect(Math.abs(settingsBox.left - mainBox.right - 24)).toBeLessThanOrEqual(1)
    await expect(form.locator('#site-intro')).toBeVisible()
    await expect(settings.getByRole('button', { name: 'Save changes' })).toBeVisible()
    await screenshot(page, 'site-introduction-1440')

    // 1920 still uses the current global 1152px page cap (Task 2 owns widening
    // it); the form remains 1104px and the split stays correct.
    await page.setViewportSize({ width: 1920, height: 1080 })
    await expect.poll(async () => Math.round((await box(form)).width)).toBe(1104)
    expect(Math.abs((await box(form)).width - 1104)).toBeLessThanOrEqual(1)
    await screenshot(page, 'site-introduction-1920')

    // Below the 1100px usable-width threshold the form stacks in one column.
    await page.setViewportSize({ width: 1024, height: 900 })
    await expect.poll(async () => Math.round((await box(form)).width)).toBeLessThan(1100)
    const stackedForm = await box(form)
    const stackedMain = await box(main)
    const stackedSettings = await box(settings)
    expect(stackedForm.width).toBeLessThan(1100)
    expect(Math.abs(stackedMain.width - stackedForm.width)).toBeLessThanOrEqual(1)
    expect(Math.abs(stackedSettings.width - stackedForm.width)).toBeLessThanOrEqual(1)
    expect(stackedSettings.top).toBeGreaterThanOrEqual(stackedMain.bottom - 1)
  })

  test('phone dialogs are exactly viewport wide, stack at 343px and keep 44px controls', async ({ page }) => {
    test.skip(test.info().project.name !== 'phone', 'Phone geometry runs in the 375px phone project')
    await signIn(page)
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
    await screenshot(page, 'task-dialog-375')
    await closeDialog(page, taskDialog)

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
})
