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

      await adminPage.goto('/admin/boards')
      await adminPage.getByRole('button', { name: 'Create board' }).first().click()
      const boardDialog = adminPage.getByRole('dialog')
      await boardDialog.locator('#board-slug').fill(slug)
      await boardDialog.locator('#board-name').fill(boardName)
      await boardDialog.locator('#board-description').fill('Private individual board created by real-browser E2E.')
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
      await taskDialog.getByRole('button', { name: 'Create task' }).click()
      await expect(taskDialog).toBeHidden()
      await expect(adminPage.getByText(taskTitle, { exact: true })).toBeVisible()

      await adminPage.getByRole('button', { name: 'Publish' }).click()
      await expect(adminPage.getByText('Active', { exact: true }).first()).toBeVisible()

      let solverPage: Page | undefined
      const studentPage = await studentContext.newPage()
      await signIn(studentPage, studentUser, studentPass)
      await studentPage.goto('/boards')
      await studentPage.locator('#join-board-uuid').fill(boardUuid)
      await studentPage.getByRole('button', { name: 'Join board' }).click()
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
      await expect(studentPage.getByText('100 points awarded.')).toBeVisible()
      await expect(studentPage.getByRole('table').getByText('Accepted').first()).toBeVisible()
      await expect(studentPage.getByRole('link', { name: 'Manage board' })).toHaveCount(0)
      await expect(studentPage.getByRole('link', { name: 'Staff & access' })).toHaveCount(0)

      solverPage = await scorerContext.newPage()
      await signIn(solverPage, scorerUser, scorerPass)
      await solverPage.goto('/boards')
      await solverPage.locator('#join-board-uuid').fill(boardUuid)
      await solverPage.getByRole('button', { name: 'Join board' }).click()
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
      await expect(outsiderPage.getByText('This board is private')).toBeVisible()
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