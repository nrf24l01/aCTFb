import { expect, test, type Page } from '@playwright/test'

const enabled = process.env.E2E_DEFAULT_PROFILE === '1'
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:5173'
const adminUser = process.env.E2E_ADMIN_USERNAME || ''
const adminPass = process.env.E2E_ADMIN_PASSWORD || ''

/**
 * Default-profile acceptance: deployment.enabled=false, integrations disabled,
 * SMTP off and no credential key — the exact operator profile from the bug
 * reports. Everything here drives the real Vue app against the real backend; no
 * response is intercepted and no empty state is faked.
 */
test.describe('default profile acceptance', () => {
  test.skip(!enabled, 'requires E2E_DEFAULT_PROFILE=1 from e2e/run-default-profile-e2e.sh')

  test('an admin previews an unjoined module/task and authors a template with runtime disabled', async ({ page }) => {
    await signIn(page, adminUser, adminPass)
    const token = await accessToken(page)

    // The admin creates the board but never joins it. The board is PRIVATE and
    // gated by an access group the admin is not a member of.
    const suffix = Date.now().toString(36)
    const group = await api(page, token, 'POST', '/api/v2/admin/access-groups', { name: `e2e-group-${suffix}` })
    const start = new Date(Date.now() - 60 * 60 * 1000).toISOString()
    const end = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString()
    const board = await api(page, token, 'POST', '/api/v2/admin/boards', {
      slug: `e2e-preview-${suffix}`,
      name: `E2E preview ${suffix}`,
      participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN',
      visibility: 'PRIVATE',
      accessMode: 'AUDIENCE',
      accessGroupUuids: [group.uuid],
      startsAt: start,
      endsAt: end,
      rating: { visibility: 'PUBLIC' },
    })
    const boardUuid = String(board.uuid)
    const module = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules`, {
      name: 'Flag module',
      type: 'TASKS',
      allowedTaskTypes: ['FLAG'],
      enabled: true,
    })
    const moduleUuid = String(module.uuid)
    const task = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks`, {
      title: 'Preview flag',
      type: 'FLAG',
      category: 'web',
      description: 'Organizer preview task',
      points: 100,
      visible: true,
      submission: { mode: 'TEXT', checker: 'AUTOMATIC' },
      acceptedAnswers: [`flag{e2e_preview_${suffix}}`],
      instance: { enabled: false },
    })
    await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/actions`, { action: 'PUBLISH' })

    // The module page renders the real staff task list, not an error.
    await page.goto(`/boards/${boardUuid}/modules/${moduleUuid}`)
    await expect(page.getByRole('heading', { name: 'Flag module' })).toBeVisible()
    await expect(page.getByText('Organizer preview', { exact: true })).toBeVisible()
    await expect(page.getByText('Preview flag')).toBeVisible()
    await expect(page.getByText('Could not load module')).toHaveCount(0)

    // The task page renders the preview without calling own history or showing
    // a submission form, and never leaks the accepted answer.
    await page.goto(`/boards/${boardUuid}/modules/${moduleUuid}/tasks/${task.uuid}`)
    await expect(page.getByRole('heading', { name: 'Preview flag' })).toBeVisible()
    await expect(page.getByText('Organizer preview', { exact: true })).toBeVisible()
    await expect(page.getByText('Could not load submission history')).toHaveCount(0)
    await expect(page.getByText('Your submissions')).toHaveCount(0)
    await expect(page.getByRole('button', { name: /Submit/i })).toHaveCount(0)
    const body = await page.locator('body').innerText()
    expect(body).not.toContain(`flag{e2e_preview_${suffix}}`)

    // Code-only enrollment: the organizer issues one code and a participant
    // joins with the code alone. Opening the board URL never enrolls.
    const joinCode = String(
      (await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/join-code`, {})).code,
    )
    expect(joinCode.length).toBe(26)

    // A second browser context is a real participant, not the admin.
    const participantContext = await page.context().browser()!.newContext()
    try {
      const participant = await participantContext.newPage()
      const participantName = `e2e_join_${suffix}`
      const participantPassword = `E2e-join-${suffix}`
      const registered = await participant.request.post(`${base}/api/v2/auth/register`, {
        headers: { Origin: base, 'Content-Type': 'application/json' },
        data: JSON.stringify({
          username: participantName,
          email: `${participantName}@actfb.test`,
          password: participantPassword,
        }),
      })
      expect(registered.ok(), `register ${registered.status()}: ${await registered.text()}`).toBeTruthy()
      const registeredBody = (await registered.json()) as { user: { uuid: string } }
      // The organizer grants the access group, which is the second, independent
      // gate. The code alone never bypasses it.
      await api(page, token, 'PATCH', `/api/v2/admin/users/${registeredBody.user.uuid}`, {
        accessGroupUuids: [group.uuid],
      })

      // The register response already set the refresh cookie for this browser
      // context, so the app bootstraps the session from it. This avoids a second
      // sign-in round against the auth rate limiter.
      await participant.goto('/boards')
      await expect(participant.getByRole('link', { name: 'Boards' })).toBeVisible()
      await expect(participant.getByText(participantName)).toBeVisible({ timeout: 15_000 })

      // Browsing the board link alone must not create a membership. The board is
      // PRIVATE and gated by a group this participant is not in, so the page
      // offers the code prompt and the organizer still sees no participant row.
      await participant.goto(`/boards/${boardUuid}`)
      await expect(participant.getByRole('button', { name: 'Enter board code' })).toBeVisible()
      const beforeJoin = await api(page, token, 'GET', `/api/v2/admin/boards/${boardUuid}/participants?limit=100&offset=0`)
      expect(JSON.stringify(beforeJoin.items)).not.toContain(participantName)

      // The code alone enrolls: no UUID or link is typed.
      await participant.goto('/boards')
      await participant.getByRole('button', { name: 'Join a board' }).click()
      await expect(participant.locator('#join-board-code')).toBeVisible()
      await participant.locator('#join-board-code').fill(joinCode)
      await participant.getByRole('button', { name: 'Join', exact: true }).click()
      await expect(participant).toHaveURL(new RegExp(`/boards/${boardUuid}$`), { timeout: 15_000 })
      await expect(participant.getByText('Joined: yes')).toBeVisible()

      // A wrong code is a real, friendly rejection.
      const otherBoard = await api(page, token, 'POST', '/api/v2/admin/boards', {
        slug: `e2e-wrong-${suffix}`,
        name: `E2E wrong ${suffix}`,
        participationMode: 'INDIVIDUAL',
        registrationMode: 'OPEN',
        visibility: 'PUBLIC',
        rating: { visibility: 'PUBLIC' },
      })
      const wrongCode = await participant.request.post(`${base}/api/v2/boards/join`, {
        headers: { Authorization: `Bearer ${await accessToken(participant)}`, 'Content-Type': 'application/json' },
        data: JSON.stringify({ code: 'NOT-A-REAL-CODE' }),
      })
      expect(wrongCode.status()).toBe(403)
      const wrongBody = await wrongCode.text()
      expect(wrongBody).not.toContain(String(otherBoard.uuid))
    } finally {
      await participantContext.close()
    }

    // The catalog is empty but valid: no error, a create action, and a working
    // editor even though runtime instances are disabled.
    await page.goto('/admin/templates')
    await expect(page.getByText('No deployment templates yet')).toBeVisible()
    await expect(page.getByText('Could not load deployment templates')).toHaveCount(0)
    await page.getByRole('button', { name: 'Create template' }).click()
    await expect(page).toHaveURL(/\/admin\/templates\/[0-9a-f-]{36}/)
    await expect(page.getByLabel('Template manifest YAML')).toBeVisible()
    await expect(page.getByText('Allowlists require runtime instances and integrations.')).toBeVisible()
    await expect(page.getByText('Could not load template')).toHaveCount(0)
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

async function accessToken(page: Page): Promise<string> {
  const response = await page.request.post(`${base}/api/v2/auth/refresh`, { headers: { Origin: base } })
  expect(response.ok()).toBeTruthy()
  return ((await response.json()) as { accessToken: string }).accessToken
}

async function api(
  page: Page,
  token: string,
  method: string,
  path: string,
  data?: unknown,
): Promise<Record<string, unknown>> {
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
