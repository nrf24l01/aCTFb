import { expect, test, type Page } from '@playwright/test'

const adminUser = process.env.E2E_ADMIN_USERNAME || ''
const adminPass = process.env.E2E_ADMIN_PASSWORD || ''
const studentUser = process.env.E2E_STUDENT_USERNAME || ''
const studentPass = process.env.E2E_STUDENT_PASSWORD || ''
const outsiderUser = process.env.E2E_OUTSIDER_USERNAME || ''
const outsiderPass = process.env.E2E_OUTSIDER_PASSWORD || ''
const scorerUser = process.env.E2E_SCORER_USERNAME || ''
const scorerPass = process.env.E2E_SCORER_PASSWORD || ''
const flag = process.env.E2E_FLAG || ''

test.describe('P1 board to flag to score', () => {
  test.skip(
    !adminUser || !adminPass || !studentUser || !studentPass || !outsiderUser || !outsiderPass || !scorerUser || !scorerPass || !flag,
    'E2E_* fixture credentials and E2E_FLAG must be provided by the isolated runner',
  )

  test('admin publishes a FLAG task and a separate student joins, submits, and sees history/score', async ({ browser }, testInfo) => {
    const slug = `e2e-${Date.now().toString(36)}`
    const boardName = `E2E Board ${slug}`
    const moduleName = `E2E Tasks ${slug}`
    const taskTitle = `E2E Flag ${slug}`

    const adminContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
    const studentContext = await browser.newContext({ viewport: { width: 390, height: 844 } })
    const outsiderContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
    const scorerContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })

    try {
      const adminPage = await adminContext.newPage()
      await signIn(adminPage, adminUser, adminPass)

      // A private board that an outsider cannot read. Under the canonical model
      // that is "Selected audiences" with a group the outsider is not in, so
      // create the group and add only the solver first.
      const adminToken = await freshToken(adminPage)
      const group = await apiCall(adminPage, adminToken, 'POST', '/api/v2/admin/access-groups', {
        name: `P1 Group ${slug}`,
      })
      const solverUser = await apiCall(
        adminPage,
        adminToken,
        'GET',
        `/api/v2/admin/user-search?query=${encodeURIComponent(studentUser)}&limit=5&offset=0`,
      )
      const solverEntry = (solverUser.items as Array<Record<string, unknown>>)[0]
      const scorerLookup = await apiCall(
        adminPage,
        adminToken,
        'GET',
        `/api/v2/admin/user-search?query=${encodeURIComponent(scorerUser)}&limit=5&offset=0`,
      )
      const scorerEntry = (scorerLookup.items as Array<Record<string, unknown>>)[0]
      for (const entry of [solverEntry, scorerEntry]) {
        await apiCall(adminPage, adminToken, 'POST', `/api/v2/admin/access-groups/${group.uuid as string}/members`, {
          userUuid: entry.uuid,
        })
      }

      await adminPage.goto('/admin/boards')
      await adminPage.getByRole('button', { name: 'Create board' }).first().click()
      const boardDialog = adminPage.getByRole('dialog')
      await boardDialog.locator('#board-name').fill(boardName)
      // The slug lives in the Advanced section and is auto-generated from the
      // name until the organizer edits it.
      await boardDialog.getByText('Advanced', { exact: true }).click()
      await boardDialog.locator('#board-slug').fill(slug)
      // The description is raw Markdown, so it is authored in the editor's
      // Source mode. The visual editor parses formatting and does not accept
      // Markdown syntax typed as plain text.
      await boardDialog.getByRole('button', { name: 'Source mode' }).click()
      await boardDialog.locator('#board-description').fill('Private individual board created by real-browser E2E.')
      // Exactly one access control: choose the audience mode and its group.
      await boardDialog.getByRole('radio', { name: /Selected audiences/ }).check()
      await boardDialog
        .locator('label')
        .filter({ hasText: `P1 Group ${slug}` })
        .getByRole('checkbox')
        .check()
      await boardDialog.getByRole('button', { name: 'Create board' }).click()
      await expect(boardDialog).toBeHidden()

      // The admin list must load the newly created board from the real API.
      await expect(adminPage.getByRole('link', { name: new RegExp(boardName) })).toBeVisible()
      await expect(adminPage.getByText('Could not load admin boards')).toHaveCount(0)
      await adminPage.getByRole('link', { name: new RegExp(boardName) }).click()
      await expect(adminPage).toHaveURL(/\/admin\/boards\/[0-9a-f-]{36}$/)
      const boardUuid = adminPage.url().split('/').pop() as string
      expect(boardUuid).toMatch(/^[0-9a-f-]{36}$/)

      await adminPage.getByRole('button', { name: 'Create module' }).first().click()
      const moduleDialog = adminPage.getByRole('dialog')
      await moduleDialog.locator('#module-name').fill(moduleName)
      await moduleDialog.getByRole('button', { name: 'Create module' }).click()
      await expect(moduleDialog).toBeHidden()
      await expect(adminPage.getByRole('heading', { name: moduleName })).toBeVisible()

      await adminPage.getByRole('button', { name: 'Create task' }).first().click()
      const taskDialog = adminPage.getByRole('dialog')
      await taskDialog.locator('#task-title').fill(taskTitle)
      await taskDialog.locator('#task-category').fill('web')
      await taskDialog.locator('#task-description').fill('Real-browser E2E flag task.')
      await taskDialog.locator('#task-answers').fill(flag)
      // A new task is saved as a hidden draft first.
      await taskDialog.getByRole('button', { name: 'Save draft' }).click()
      await expect(taskDialog).toBeHidden()
      await expect(adminPage.getByText(taskTitle, { exact: true })).toBeVisible()

      // Publishing the task is the separate, explicit step. `exact` matters: the
      // row swaps to "Unpublish" afterwards, and the default accessible-name
      // match is a case-insensitive substring, so a loose 'Publish' would match
      // both controls.
      const taskRow = adminPage.getByRole('row', { name: new RegExp(taskTitle) })
      await expect(taskRow).toContainText('No')
      await taskRow.getByRole('button', { name: 'Publish', exact: true }).click()
      await expect(taskRow).toContainText('Yes')

      // Board lifecycle actions sit behind a collapsed disclosure by design, and
      // must be scoped to it: the task row's "Unpublish" also contains the
      // substring "publish".
      await adminPage.getByRole('button', { name: 'Show lifecycle actions' }).click()
      const lifecycle = adminPage.getByRole('region', { name: 'Lifecycle' })
      await lifecycle.getByRole('button', { name: 'Publish', exact: true }).click()
      await expect(adminPage.getByText('Active', { exact: true }).first()).toBeVisible()

      // Enrollment is code-only: the organizer issues one code for the solvers.
      const joinCode = String(
        (await apiCall(adminPage, adminToken, 'POST', `/api/v2/admin/boards/${boardUuid}/join-code`, {})).code,
      )

      let solverPage: Page | undefined
      const studentPage = await studentContext.newPage()
      await signIn(studentPage, studentUser, studentPass)
      await studentPage.goto('/boards')
      await studentPage.getByRole('button', { name: 'Join a board' }).click()
      await studentPage.locator('#join-board-code').fill(joinCode)
      await studentPage.getByRole('button', { name: 'Join', exact: true }).click()
      await expect(studentPage).toHaveURL(new RegExp(`/boards/${boardUuid}$`))

      await studentPage.getByRole('link', { name: new RegExp(moduleName) }).click()
      await studentPage.getByRole('link', { name: new RegExp(taskTitle) }).click()

      // Phone viewport: the flag field and submit control must remain visible.
      await expect(studentPage.locator('#task-flag')).toBeVisible()
      await expect(studentPage.getByRole('button', { name: 'Submit flag' })).toBeVisible()
      const overflow = await studentPage.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
      expect(overflow).toBe(false)
      await studentPage.screenshot({ path: testInfo.outputPath('task-mobile.png'), fullPage: true })

      await studentPage.locator('#task-flag').fill(flag)
      await studentPage.getByRole('button', { name: 'Submit flag' }).click()
      await expect(studentPage.getByText('Accepted', { exact: true }).first()).toBeVisible()
      await expect(studentPage.getByText('Accepted · +100 points')).toBeVisible()
      await expect(studentPage.getByRole('table').getByText('Accepted').first()).toBeVisible()
      await expect(studentPage.getByRole('link', { name: 'Manage board' })).toHaveCount(0)
      await expect(studentPage.getByRole('link', { name: 'Staff & access' })).toHaveCount(0)

      solverPage = await scorerContext.newPage()
      await signIn(solverPage, scorerUser, scorerPass)
      await solverPage.goto('/boards')
      await solverPage.getByRole('button', { name: 'Join a board' }).click()
      await solverPage.locator('#join-board-code').fill(joinCode)
      await solverPage.getByRole('button', { name: 'Join', exact: true }).click()
      await expect(solverPage).toHaveURL(new RegExp(`/boards/${boardUuid}$`))
      await solverPage.getByRole('link', { name: new RegExp(moduleName) }).click()
      await solverPage.getByRole('link', { name: new RegExp(taskTitle) }).click()
      await solverPage.locator('#task-flag').fill(flag)
      await solverPage.getByRole('button', { name: 'Submit flag' }).click()
      await expect(solverPage.getByText('Accepted', { exact: true }).first()).toBeVisible()
      await solverPage.goto(`/boards/${boardUuid}/scoreboard`)
      await expect(solverPage.getByRole('row', { name: new RegExp(scorerUser) })).toContainText('100')
      await expect(solverPage.getByRole('row', { name: new RegExp(studentUser) })).toContainText('100')

      const outsiderPage = await outsiderContext.newPage()
      await signIn(outsiderPage, outsiderUser, outsiderPass)
      await outsiderPage.goto(`/boards/${boardUuid}`)
      await expect(outsiderPage.getByText('This board requires access')).toBeVisible()
      await expect(outsiderPage.getByText(taskTitle, { exact: true })).toHaveCount(0)
      await expect(outsiderPage.getByRole('link', { name: 'Manage board' })).toHaveCount(0)
    } finally {
      await adminContext.close()
      await studentContext.close()
      await outsiderContext.close()
      await scorerContext.close()
    }
  })
})

async function freshToken(page: Page): Promise<string> {
  const response = await page.request.post(`${baseUrl()}/api/v2/auth/refresh`, {
    headers: { Origin: baseUrl() },
  })
  expect(response.ok()).toBeTruthy()
  return ((await response.json()) as { accessToken: string }).accessToken
}

async function apiCall(
  page: Page,
  token: string,
  method: string,
  path: string,
  data?: unknown,
): Promise<Record<string, unknown>> {
  const response = await page.request.fetch(`${baseUrl()}${path}`, {
    method,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    data: data === undefined ? undefined : JSON.stringify(data),
  })
  if (!response.ok()) {
    throw new Error(`${method} ${path} -> ${response.status()} ${await response.text()}`)
  }
  if (response.status() === 204) return {}
  const raw = await response.text()
  if (raw.trim() === '') return {}
  return JSON.parse(raw) as Record<string, unknown>
}

function baseUrl(): string {
  return process.env.E2E_BASE_URL || 'http://127.0.0.1:5173'
}

async function signIn(page: Page, username: string, password: string) {
  for (let attempt = 0; attempt < 2; attempt += 1) {
    await page.goto('/sign-in')
    await page.locator('#sign-in-username').fill(username)
    await page.locator('#sign-in-password').fill(password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    const signedIn = await expect(page).toHaveURL(/\/boards/, { timeout: 8_000 }).then(() => true).catch(() => false)
    if (signedIn) return
    if (attempt === 0) await page.waitForTimeout(61_000)
  }
  await expect(page).toHaveURL(/\/boards/)
}