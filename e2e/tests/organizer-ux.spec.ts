import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'

const fullStack = process.env.E2E_FULL_STACK === '1'
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:5173'
const adminUser = process.env.E2E_ADMIN_USERNAME || ''
const adminPass = process.env.E2E_ADMIN_PASSWORD || ''
const studentUser = process.env.E2E_STUDENT_USERNAME || ''
const studentPass = process.env.E2E_STUDENT_PASSWORD || ''
const pgContainer = process.env.E2E_DB_CONTAINER || ''
const pgUser = process.env.E2E_PG_USER || ''
const pgDatabase = process.env.E2E_PG_DB || ''

/**
 * Organizer UX acceptance. Everything here drives the real Vue app against the
 * real backend; no route is intercepted and no response is stubbed.
 */
test.describe('organizer UX acceptance', () => {
  test.skip(!fullStack, 'requires E2E_FULL_STACK=1 from e2e/run-full-e2e.sh')

  test('anonymous landing renders organizer Markdown as real DOM after an admin edit', async ({ page, browser }) => {
    // 1. Anonymous first visit: the seeded intro must render as real block
    //    elements, not a flat text blob.
    await page.goto('/')
    // The landing document title is the single page-level heading.
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    const main = page.getByRole('main')
    await expect(main.getByRole('link', { name: 'Browse boards' })).toBeVisible()
    await expect(main.getByRole('link', { name: 'Sign in' })).toBeVisible()
    // The landing page is not an auth redirect.
    await expect(page).toHaveURL(/\/$/)

    // 2. Admin edits the document through the real editor with preview.
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)
    await page.goto('/admin/site')
    const title = `Organizer Landing ${Date.now().toString(36)}`
    const intro = [
      '## Rendered heading',
      '',
      'Organizer **bold** text with a [relative link](/boards).',
      '',
      '<script>window.__actfbXss = true</script>',
      '',
      '<img src=x onerror="window.__actfbXss = true">',
    ].join('\n')
    await page.locator('#site-title').fill(title)
    await page.locator('#site-intro').fill(intro)

    // Preview tab: real sanitised DOM elements inside the preview panel.
    await page.getByRole('tab', { name: 'Preview' }).click()
    const previewPanel = page.locator('[role="tabpanel"]').filter({ has: page.getByRole('heading', { name: 'Rendered heading' }) })
    await expect(previewPanel.getByRole('heading', { name: 'Rendered heading' })).toBeVisible()
    await expect(previewPanel.locator('strong')).toContainText('bold')
    await expect(previewPanel.locator('a[href="/boards"]')).toHaveCount(1)
    expect(await previewPanel.locator('script').count()).toBe(0)
    expect(await previewPanel.locator('[onerror]').count()).toBe(0)

    await page.getByRole('tab', { name: 'Write' }).click()
    await page.getByRole('button', { name: 'Save changes' }).click()
    await expect(page.getByText('Saved.')).toBeVisible({ timeout: 15_000 })

    // 3. A fresh anonymous browser context reloads and sees the sanitised DOM.
    const anonContext = await browser.newContext()
    try {
      const anonPage = await anonContext.newPage()
      await anonPage.goto('/')
      await expect(anonPage.getByRole('heading', { level: 1, name: title })).toBeVisible()
      await expect(anonPage.getByRole('heading', { name: 'Rendered heading' })).toBeVisible()
      await expect(anonPage.locator('article strong')).toContainText('bold')
      await expect(anonPage.locator('article a[href="/boards"]')).toHaveCount(1)
      // Sanitizer contract: no script element and no inline handler survived,
      // and the injected code never executed.
      expect(await anonPage.locator('article script').count()).toBe(0)
      expect(await anonPage.locator('article [onerror]').count()).toBe(0)
      expect(await anonPage.evaluate(() => (window as unknown as { __actfbXss?: boolean }).__actfbXss ?? false)).toBe(false)
      // Persisted across a hard reload, not just client state.
      await anonPage.reload()
      await expect(anonPage.getByRole('heading', { name: 'Rendered heading' })).toBeVisible()
    } finally {
      await anonContext.close()
    }

    // 4. A non-admin cannot update the site document.
    const studentToken = await studentTokenFor(page)
    const forbidden = await page.request.patch(`${base}/api/v2/admin/site`, {
      headers: { Authorization: `Bearer ${studentToken}`, 'Content-Type': 'application/json' },
      data: JSON.stringify({ title: 'hijacked' }),
    })
    expect(forbidden.status()).toBe(403)
    const stillThere = await page.request.get(`${base}/api/v2/site`)
    expect(((await stillThere.json()) as { title: string }).title).toBe(title)
    expect(token).toBeTruthy()
  })

  test('global group membership picker searches the server beyond the first page', async ({ page }) => {
    await signIn(page, adminUser, adminPass)

    // 105 extra users so a client-side filter over one page cannot pass.
    const suffix = Date.now().toString(36)
    const target = `zz-marker-${suffix}`
    psql(`
      INSERT INTO users (username, email, password)
      SELECT 'bulk-${suffix}-' || i, 'bulk-${suffix}-' || i || '@actfb.test', 'x'
      FROM generate_series(1, 105) AS i;
      INSERT INTO users (username, email, password)
      VALUES ('${target}', '${target}@actfb.test', 'x');
    `)

    await page.goto('/admin/access-groups')
    const groupName = `Picker Group ${suffix}`
    await page.getByRole('button', { name: 'Create group' }).click()
    const createDialog = page.getByRole('dialog')
    await createDialog.locator('#group-name').fill(groupName)
    await createDialog.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('row', { name: new RegExp(groupName) })).toBeVisible({ timeout: 15_000 })

    await page.getByRole('row', { name: new RegExp(groupName) }).getByRole('button', { name: 'Members' }).click()
    const membersDialog = page.getByRole('dialog')
    const search = membersDialog.getByRole('combobox')

    // Search by username beyond the first 100 users.
    await search.fill(target)
    await expect(membersDialog.getByRole('option', { name: new RegExp(target) })).toBeVisible({ timeout: 15_000 })
    await membersDialog.getByRole('option', { name: new RegExp(target) }).click()
    await expect(membersDialog.getByText(`${target}@actfb.test`)).toBeVisible({ timeout: 15_000 })

    // Search by exact email for a different user and add it.
    const byEmail = `bulk-${suffix}-7@actfb.test`
    await search.fill(byEmail)
    await expect(membersDialog.getByRole('option', { name: /bulk-/ })).toBeVisible({ timeout: 15_000 })
    await membersDialog.getByRole('option', { name: /bulk-/ }).first().click()
    await expect(membersDialog.locator('li').filter({ hasText: `bulk-${suffix}-7` })).toBeVisible({ timeout: 15_000 })

    // Remove one by identity, never by a typed UUID.
    await membersDialog.locator('li').filter({ hasText: target }).getByRole('button', { name: 'Remove' }).click()
    await expect(membersDialog.locator('li').filter({ hasText: target })).toHaveCount(0, { timeout: 15_000 })

    await membersDialog.getByRole('button', { name: 'Done' }).click()
  })

  test('board access is one control: public browse, audience and invite stay private', async ({ page, browser }) => {
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)
    const suffix = Date.now().toString(36)

    // The create modal exposes exactly one three-option access control.
    await page.goto('/admin/boards')
    await page.getByRole('button', { name: 'Create board' }).first().click()
    const dialog = page.getByRole('dialog')
    const radios = dialog.locator('input[name="board-access-mode"]')
    await expect(radios).toHaveCount(3)
    expect(await radios.evaluateAll((nodes) => nodes.map((node) => (node as HTMLInputElement).value))).toEqual([
      'PUBLIC',
      'AUDIENCE',
      'INVITE',
    ])
    await expect(dialog.getByText('Visibility')).toHaveCount(0)
    await expect(dialog.getByText('Registration')).toHaveCount(0)

    // Create a PUBLIC board through the UI and publish it.
    const publicName = `Public Board ${suffix}`
    await dialog.locator('#board-name').fill(publicName)
    await dialog.locator('#board-participation').selectOption('INDIVIDUAL')
    await dialog.getByRole('button', { name: 'Create board' }).click()
    await expect(page.getByText(publicName)).toBeVisible({ timeout: 15_000 })

    const boards = await api(page, token, 'GET', '/api/v2/admin/boards?limit=100')
    const publicBoard = (boards.items as Array<Record<string, unknown>>).find((item) => item.name === publicName)
    expect(publicBoard?.accessMode).toBe('PUBLIC')
    const publicUuid = publicBoard!.uuid as string
    // A board needs one enabled task module before PUBLISH.
    const module = await api(page, token, 'POST', `/api/v2/admin/boards/${publicUuid}/modules`, {
      name: 'Flag module', type: 'TASKS', allowedTaskTypes: ['FLAG'], enabled: true,
    })
    await api(page, token, 'POST', `/api/v2/admin/boards/${publicUuid}/modules/${module.uuid as string}/tasks`, {
      title: 'Public flag', type: 'FLAG', category: 'web', description: 'public',
      points: 10, visible: true,
      submission: { mode: 'TEXT', checker: 'AUTOMATIC' },
      acceptedAnswers: [`actfb{public_${suffix}}`],
      instance: { enabled: false },
    })
    await api(page, token, 'POST', `/api/v2/admin/boards/${publicUuid}/actions`, { action: 'PUBLISH' })

    // Private boards for the audience and invite modes.
    const group = await api(page, token, 'POST', '/api/v2/admin/access-groups', { name: `Audience Group ${suffix}` })
    const audienceBoard = await api(page, token, 'POST', '/api/v2/admin/boards', {
      slug: `audience-${suffix}`, name: `Audience Board ${suffix}`, participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN', visibility: 'PRIVATE', accessMode: 'AUDIENCE',
      accessGroupUuids: [group.uuid as string], rating: { visibility: 'PUBLIC' },
    })
    const inviteBoard = await api(page, token, 'POST', '/api/v2/admin/boards', {
      slug: `invite-${suffix}`, name: `Invite Board ${suffix}`, participationMode: 'INDIVIDUAL',
      registrationMode: 'INVITE_ONLY', visibility: 'PRIVATE', accessMode: 'INVITE',
      rating: { visibility: 'PUBLIC' },
    })
    expect(audienceBoard.accessMode).toBe('AUDIENCE')
    expect(inviteBoard.accessMode).toBe('INVITE')

    // Publish both private boards so a join attempt exercises the access gate
    // itself rather than the draft-state guard.
    for (const uuid of [audienceBoard.uuid as string, inviteBoard.uuid as string]) {
      await api(page, token, 'POST', `/api/v2/admin/boards/${uuid}/modules`, {
        name: 'Manual module', type: 'TASKS', allowedTaskTypes: ['MANUAL'], enabled: true,
      })
      await api(page, token, 'POST', `/api/v2/admin/boards/${uuid}/actions`, { action: 'PUBLISH' })
    }

    // Anonymous browsing: the public board is readable, private metadata 404s.
    const anonContext = await browser.newContext()
    try {
      const anon = await anonContext.newPage()
      await anon.goto('/boards')
      await expect(anon.getByText(publicName)).toBeVisible({ timeout: 15_000 })
      await expect(anon.getByText(`Audience Board ${suffix}`)).toHaveCount(0)
      await expect(anon.getByText(`Invite Board ${suffix}`)).toHaveCount(0)
      await anon.goto(`/boards/${publicUuid}`)
      await expect(anon.getByRole('heading', { level: 1, name: publicName })).toBeVisible()
      // Participation still needs a session: the anonymous participant panel
      // offers sign-in and the modules stay hidden.
      const participantPanel = anon.getByRole('complementary').filter({ hasText: 'Participate' })
      await expect(participantPanel.getByRole('link', { name: 'Sign in' })).toBeVisible()
      await expect(anon.getByText("Sign in to see this board's modules and submit answers.")).toBeVisible()

      const anonDetail = await anon.request.get(`${base}/api/v2/boards/${audienceBoard.uuid as string}`)
      expect(anonDetail.status()).toBe(404)
      const anonInvite = await anon.request.get(`${base}/api/v2/boards/${inviteBoard.uuid as string}`)
      expect(anonInvite.status()).toBe(404)
    } finally {
      await anonContext.close()
    }

    // A signed-in student cannot join the audience board without the group.
    const studentContext = await browser.newContext()
    try {
      const student = await studentContext.newPage()
      await signIn(student, studentUser, studentPass)
      const studentToken = await freshToken(student)
      // Not in the selected access group: the live group gate denies the join.
      const denied = await student.request.post(`${base}/api/v2/boards/${audienceBoard.uuid as string}/join`, {
        headers: { Authorization: `Bearer ${studentToken}`, 'Content-Type': 'application/json' },
        data: '{}',
      })
      expect([403, 404]).toContain(denied.status())
      // The invite board requires a valid code; a bare join is refused.
      const noCode = await student.request.post(`${base}/api/v2/boards/${inviteBoard.uuid as string}/join`, {
        headers: { Authorization: `Bearer ${studentToken}`, 'Content-Type': 'application/json' },
        data: '{}',
      })
      expect(noCode.status()).toBeGreaterThanOrEqual(400)
      const allowed = await student.request.post(`${base}/api/v2/boards/${publicUuid}/join`, {
        headers: { Authorization: `Bearer ${studentToken}`, 'Content-Type': 'application/json' },
        data: '{}',
      })
      expect(allowed.status()).toBe(200)
    } finally {
      await studentContext.close()
    }
  })

  test('module has one format and the task editor inherits it', async ({ page }) => {
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)
    const suffix = Date.now().toString(36)
    const board = await api(page, token, 'POST', '/api/v2/admin/boards', {
      slug: `format-${suffix}`, name: `Format Board ${suffix}`, participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN', visibility: 'PRIVATE', rating: { visibility: 'PUBLIC' },
    })
    const boardUuid = board.uuid as string

    await page.goto(`/admin/boards/${boardUuid}`)
    await page.getByRole('button', { name: 'Create module' }).first().click()
    const dialog = page.getByRole('dialog')
    const format = dialog.locator('#module-format')
    await expect(format).toBeVisible()
    expect(await format.locator('option').allTextContents()).toEqual([
      'Flag challenges',
      'Manual review',
      'Instance tasks',
      'Pentest',
      'Attack & Defense',
    ])
    // No second "allowed task types" checklist for new modules.
    await expect(dialog.getByText('Allowed task types')).toHaveCount(0)

    await dialog.locator('#module-name').fill('Flag module')
    await format.selectOption('FLAG')
    await dialog.getByRole('button', { name: 'Create module' }).click()
    await expect(page.getByText('Flag challenges').first()).toBeVisible({ timeout: 15_000 })

    // The new-task modal inherits the format: no task-type selector.
    await page.getByRole('button', { name: 'Create task' }).first().click()
    const taskDialog = page.getByRole('dialog')
    await expect(taskDialog.getByText('Flag', { exact: true }).first()).toBeVisible()
    await expect(taskDialog.locator('#task-type')).toHaveCount(0)
    await expect(taskDialog.locator('#task-choice')).toHaveCount(0)
    await expect(taskDialog.getByRole('button', { name: 'Save draft' })).toBeVisible()
    await taskDialog.getByRole('button', { name: 'Cancel' }).click()
  })

  test('draft FLAG task with Markdown publishes and accepts a real submission', async ({ page, browser }) => {
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)
    const suffix = Date.now().toString(36)
    const board = await api(page, token, 'POST', '/api/v2/admin/boards', {
      slug: `draft-${suffix}`, name: `Draft Board ${suffix}`, participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN', visibility: 'PUBLIC', rating: { visibility: 'PUBLIC' },
    })
    const boardUuid = board.uuid as string
    const module = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules`, {
      name: 'Flag module', type: 'TASKS', allowedTaskTypes: ['FLAG'], enabled: true,
    })
    const moduleUuid = module.uuid as string
    const flag = `actfb{draft_${suffix}}`

    await page.goto(`/admin/boards/${boardUuid}`)
    await page.getByRole('button', { name: 'Create task' }).first().click()
    const dialog = page.getByRole('dialog')
    await dialog.locator('#task-title').fill('Markdown flag')
    await dialog.locator('#task-category').fill('web')
    await dialog.locator('#task-points').fill('40')
    await dialog.locator('#task-description').fill('## Mission brief\n\nFind the **flag** in the lab.')
    await dialog.getByRole('tab', { name: 'Preview' }).click()
    await expect(dialog.getByRole('heading', { name: 'Mission brief' })).toBeVisible()
    await expect(dialog.locator('strong')).toContainText('flag')
    await dialog.getByRole('tab', { name: 'Write' }).click()
    await dialog.locator('#task-answers').fill(flag)
    await dialog.getByRole('button', { name: 'Save draft' }).click()
    await expect(dialog).toHaveCount(0, { timeout: 15_000 })

    // The draft is not visible to participants until it is published.
    const tasks = await api(page, token, 'GET', `/api/v2/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks?limit=50`)
    const created = (tasks.items as Array<Record<string, unknown>>)[0]
    expect(created.visible).toBe(false)
    const taskUuid = created.uuid as string
    await api(page, token, 'PATCH', `/api/v2/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`, {
      visible: true,
    })
    await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/actions`, { action: 'PUBLISH' })

    const studentContext = await browser.newContext({ viewport: { width: 390, height: 844 } })
    try {
      const student = await studentContext.newPage()
      await signIn(student, studentUser, studentPass)
      const studentToken = await freshToken(student)
      const joined = await student.request.post(`${base}/api/v2/boards/${boardUuid}/join`, {
        headers: { Authorization: `Bearer ${studentToken}`, 'Content-Type': 'application/json' },
        data: '{}',
      })
      expect(joined.status()).toBe(200)
      await student.goto(`/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
      // The description renders as real sanitised DOM, not raw Markdown text.
      await expect(student.getByRole('heading', { name: 'Mission brief' })).toBeVisible()
      await expect(student.locator('article strong')).toContainText('flag')
      await student.locator('#task-flag').fill(flag)
      await student.getByRole('button', { name: 'Submit flag' }).click()
      await expect(student.getByText('Accepted', { exact: true }).first()).toBeVisible({ timeout: 20_000 })
    } finally {
      await studentContext.close()
    }
  })

  test('every editor confirms before discarding unsaved work', async ({ page }) => {
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)
    const suffix = Date.now().toString(36)
    const board = await api(page, token, 'POST', '/api/v2/admin/boards', {
      slug: `dirty-${suffix}`, name: `Dirty Board ${suffix}`, participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN', visibility: 'PRIVATE', rating: { visibility: 'PUBLIC' },
    })
    const boardUuid = board.uuid as string
    await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules`, {
      name: 'Flag module', type: 'TASKS', allowedTaskTypes: ['FLAG'], enabled: true,
    })

    const discard = (label: string) =>
      page.getByRole('dialog').filter({ hasText: label })
    const keepEditing = (label: string) => discard(label).getByRole('button', { name: 'Cancel' })
    const confirmDiscard = (label: string) => discard(label).getByRole('button', { name: 'Discard changes' })

    // 1. Board editor: Escape on a dirty form asks first, keeps the value while
    //    editing continues, and closes only after an explicit discard.
    await page.goto(`/admin/boards/${boardUuid}`)
    await page.getByRole('button', { name: 'Edit board' }).first().click()
    await page.locator('#board-name').fill('Changed but unsaved')
    await page.keyboard.press('Escape')
    await expect(discard('Discard board changes?')).toBeVisible()
    await keepEditing('Discard board changes?').click()
    await expect(page.locator('#board-name')).toHaveValue('Changed but unsaved')
    await page.keyboard.press('Escape')
    await confirmDiscard('Discard board changes?').click()
    await expect(page.locator('#board-name')).toHaveCount(0)

    // 2. Module editor: clicking outside is guarded too.
    await page.getByRole('button', { name: 'Create module' }).first().click()
    await page.locator('#module-name').fill('Unsaved module')
    await page.mouse.click(5, 5)
    await expect(discard('Discard module changes?')).toBeVisible()
    await confirmDiscard('Discard module changes?').click()
    await expect(page.locator('#module-name')).toHaveCount(0)

    // 3. Task editor: a typed answer must survive "Keep editing".
    await page.getByRole('button', { name: 'Create task' }).first().click()
    await page.locator('#task-title').fill('Unsaved task')
    await page.locator('#task-answers').fill('flag{never_lose_me}')
    await page.keyboard.press('Escape')
    await expect(discard('Discard task changes?')).toBeVisible()
    await keepEditing('Discard task changes?').click()
    await expect(page.locator('#task-answers')).toHaveValue('flag{never_lose_me}')
    await page.keyboard.press('Escape')
    await confirmDiscard('Discard task changes?').click()
    await expect(page.locator('#task-answers')).toHaveCount(0)
  })

  test('instance task editor offers typed template parameters, not raw JSON', async ({ page }) => {
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)
    const suffix = Date.now().toString(36)
    const board = await api(page, token, 'POST', '/api/v2/admin/boards', {
      slug: `params-${suffix}`, name: `Params Board ${suffix}`, participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN', visibility: 'PRIVATE', rating: { visibility: 'PUBLIC' },
    })
    const boardUuid = board.uuid as string
    const module = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules`, {
      name: 'Instance module', type: 'TASKS', allowedTaskTypes: ['INSTANCE'], enabled: true,
    })
    const moduleUuid = module.uuid as string

    await page.goto(`/admin/boards/${boardUuid}`)
    await page.getByRole('button', { name: 'Create task' }).first().click()
    const dialog = page.getByRole('dialog')
    // No approved template yet: the empty state is actionable, never silent.
    await expect(dialog.getByText('No approved template version is available for this module.')).toBeVisible({ timeout: 15_000 })
    await expect(dialog.getByRole('link', { name: 'Open deployment templates' })).toBeVisible()
    // Raw JSON textareas and a UUID box are gone.
    await expect(dialog.locator('#task-instance-parameters')).toHaveCount(0)
    await expect(dialog.locator('#task-instance-template')).toHaveCount(0)
    expect(moduleUuid).toBeTruthy()
    await dialog.getByRole('button', { name: 'Cancel' }).click()
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

async function freshToken(page: Page): Promise<string> {
  const response = await page.request.post(`${base}/api/v2/auth/refresh`, {
    headers: { Origin: base },
  })
  expect(response.ok()).toBeTruthy()
  return ((await response.json()) as { accessToken: string }).accessToken
}

async function studentTokenFor(page: Page): Promise<string> {
  const response = await page.request.post(`${base}/api/v2/auth/login`, {
    headers: { Origin: base, 'Content-Type': 'application/json' },
    data: JSON.stringify({ username: studentUser, password: studentPass }),
  })
  expect(response.ok()).toBeTruthy()
  return ((await response.json()) as { accessToken: string }).accessToken
}

async function api(page: Page, token: string, method: string, path: string, data?: unknown): Promise<Record<string, unknown>> {
  const response = await page.request.fetch(`${base}${path}`, {
    method,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    data: data === undefined ? undefined : JSON.stringify(data),
  })
  if (!response.ok()) {
    throw new Error(`${method} ${path} -> ${response.status()} ${await response.text()}`)
  }
  if (response.status() === 204) return {}
  return (await response.json()) as Record<string, unknown>
}

function psql(sql: string): string {
  return execFileSync(
    'docker',
    ['exec', '-i', pgContainer, 'psql', '-U', pgUser, '-d', pgDatabase, '-v', 'ON_ERROR_STOP=1', '-tAc', sql],
    { encoding: 'utf8' },
  )
}
