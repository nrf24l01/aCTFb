import { expect, test, type Page } from '@playwright/test'

const enabled = process.env.E2E_DEFAULT_PROFILE === '1'
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:5173'
const adminUser = process.env.E2E_ADMIN_USERNAME || ''
const adminPass = process.env.E2E_ADMIN_PASSWORD || ''
// Created once by the runner: registration is rate limited to 5/15min per IP, so
// tests never register their own users.
const participantUser = process.env.E2E_PARTICIPANT_USERNAME || ''
const participantPass = process.env.E2E_PARTICIPANT_PASSWORD || ''
const participantTwoUser = process.env.E2E_PARTICIPANT2_USERNAME || ''
const participantTwoPass = process.env.E2E_PARTICIPANT2_PASSWORD || ''

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
    await expect(page.getByText('Organizer preview — participant access is unavailable')).toBeVisible()
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
      const participantName = participantUser
      // The organizer grants the access group, which is the second, independent
      // gate. The code alone never bypasses it.
      const participantRecord = await api(page, token, 'GET',
        `/api/v2/admin/user-search?query=${encodeURIComponent(participantName)}&scope=DIRECTORY&limit=10`)
      const participantItems = (participantRecord.items ?? []) as Array<{ uuid: string; username: string }>
      const participantMatch = participantItems.find((item) => item.username === participantName)
      expect(participantMatch, `participant ${participantName} not found`).toBeTruthy()
      await api(page, token, 'PATCH', `/api/v2/admin/users/${participantMatch!.uuid}`, {
        accessGroupUuids: [group.uuid],
      })

      await signIn(participant, participantName, participantPass)
      await expect(participant.getByText(participantName)).toBeVisible({ timeout: 15_000 })

      // Browsing the board link alone must not create a membership. The page
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

      // A wrong code is a real, friendly rejection that reveals nothing about
      // the target board: no UUID, slug or name, and one generic error code.
      const wrongCode = await participant.request.post(`${base}/api/v2/boards/join`, {
        headers: { Authorization: `Bearer ${await accessToken(participant)}`, 'Content-Type': 'application/json' },
        data: JSON.stringify({ code: 'NOT-A-REAL-CODE' }),
      })
      expect(wrongCode.status()).toBe(403)
      const wrongBody = await wrongCode.text()
      expect(wrongBody).not.toContain(boardUuid)
      expect(wrongBody).not.toContain(String(board.slug))
      expect(wrongBody).not.toContain(String(board.name))
      const wrongParsed = JSON.parse(wrongBody) as { code: string }
      expect(wrongParsed.code).toBe('JOIN_CODE_INVALID')
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

const legacyBoard = process.env.E2E_LEGACY_BOARD_UUID || ''
const legacyCode = process.env.E2E_LEGACY_CODE || ''

test.describe('default profile: legacy code and mixed role', () => {
  test.skip(!enabled, 'requires E2E_DEFAULT_PROFILE=1 from e2e/run-default-profile-e2e.sh')

  test('a pre-existing legacy INVITE_ONLY code still enrolls through the UI', async ({ page, browser }) => {
    test.skip(!legacyBoard || !legacyCode, 'requires the seeded legacy board from the runner')
    await signIn(page, adminUser, adminPass)
    const token = await accessToken(page)

    // The board has no canonical code, so the status read says so.
    const status = await api(page, token, 'GET', `/api/v2/admin/boards/${legacyBoard}/join-code`)
    expect(status.configured).toBe(false)
    expect(status.revision).toBe(0)

    const context = await browser.newContext()
    try {
      const participant = await context.newPage()
      await signIn(participant, participantTwoUser, participantTwoPass)
      await participant.goto(`/boards/${legacyBoard}`)
      await expect(participant.getByRole('button', { name: 'Enter board code' })).toBeVisible()
      await participant.getByRole('button', { name: 'Enter board code' }).click()
      await participant.locator('#join-board-code').fill(legacyCode)
      await participant.getByRole('button', { name: 'Join', exact: true }).click()
      await expect(participant).toHaveURL(new RegExp(`/boards/${legacyBoard}$`), { timeout: 15_000 })
      await expect(participant.getByText('Joined: yes')).toBeVisible()
    } finally {
      await context.close()
    }
  })

  test('a joined admin with a revoked group sees a safe preview, not a history error', async ({ page, browser }) => {
    await signIn(page, adminUser, adminPass)
    const token = await accessToken(page)
    const suffix = Date.now().toString(36)

    const group = await api(page, token, 'POST', '/api/v2/admin/access-groups', { name: `e2e-mixed-${suffix}` })
    const start = new Date(Date.now() - 60 * 60 * 1000).toISOString()
    const end = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString()
    const board = await api(page, token, 'POST', '/api/v2/admin/boards', {
      slug: `e2e-mixed-${suffix}`,
      name: `E2E mixed ${suffix}`,
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
      title: 'Mixed flag',
      type: 'FLAG',
      category: 'web',
      description: 'mixed role task',
      points: 100,
      visible: true,
      submission: { mode: 'TEXT', checker: 'AUTOMATIC' },
      acceptedAnswers: [`flag{e2e_mixed_${suffix}}`],
      instance: { enabled: false },
    })
    await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/actions`, { action: 'PUBLISH' })

    // A dedicated admin account, created with normalized role order so a later
    // access-group-only patch does not bump auth_version and kill its session.
    const mixedName = `e2e_mixed_admin_${suffix}`
    const mixedPassword = `E2e-mixed-${suffix}!`
    const mixedUser = await api(page, token, 'POST', '/api/v2/admin/users', {
      username: mixedName,
      email: `${mixedName}@actfb.test`,
      password: mixedPassword,
      roles: ['admin', 'user'],
      emailVerified: true,
      accessGroupUuids: [group.uuid],
    })
    const code = String((await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/join-code`, {})).code)

    const mixedContext = await browser.newContext()
    try {
      const mixed = await mixedContext.newPage()
      await signIn(mixed, mixedName, mixedPassword)
      // Join while the group still grants access.
      await api(mixed, await accessToken(mixed), 'POST', `/api/v2/boards/${boardUuid}/join`, { code })

      // Revoke the group only (roles untouched, so the session stays valid).
      await api(page, token, 'PATCH', `/api/v2/admin/users/${String(mixedUser.uuid)}`, { accessGroupUuids: [] })

      // Module and task pages stay readable as a preview with no own progress.
      await mixed.goto(`/boards/${boardUuid}/modules/${moduleUuid}`)
      await expect(mixed.getByText('Organizer preview', { exact: true })).toBeVisible({ timeout: 15_000 })
      await expect(mixed.getByText('Could not load module')).toHaveCount(0)
      await mixed.goto(`/boards/${boardUuid}/modules/${moduleUuid}/tasks/${task.uuid}`)
      await expect(mixed.getByText('Organizer preview — participant access is unavailable')).toBeVisible({ timeout: 15_000 })
      await expect(mixed.getByText('Could not load submission history')).toHaveCount(0)
      await expect(mixed.getByText('Your submissions')).toHaveCount(0)
      await expect(mixed.getByRole('button', { name: /Submit/i })).toHaveCount(0)
      const body = await mixed.locator('body').innerText()
      expect(body).not.toContain(`flag{e2e_mixed_${suffix}}`)
    } finally {
      await mixedContext.close()
    }
  })

  test('the join-code panel reveals once and never commits a stale custom value', async ({ page }) => {
    await signIn(page, adminUser, adminPass)
    const token = await accessToken(page)
    const suffix = Date.now().toString(36)
    const board = await api(page, token, 'POST', '/api/v2/admin/boards', {
      slug: `e2e-panel-${suffix}`,
      name: `E2E panel ${suffix}`,
      participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN',
      visibility: 'PUBLIC',
      rating: { visibility: 'PUBLIC' },
    })
    const boardUuid = String(board.uuid)
    await page.goto(`/admin/boards/${boardUuid}`)

    // Not configured yet.
    await expect(page.getByText('not configured')).toBeVisible({ timeout: 15_000 })

    // Custom first: the plaintext is revealed once.
    await page.locator('#join-code-custom').fill('my-custom-code-01')
    await page.getByRole('button', { name: 'Set custom code' }).click()
    await expect(page.getByText('Copy this code now')).toBeVisible({ timeout: 15_000 })
    const revealedCustom = await page.locator('input[aria-label="New board join code"]').inputValue()
    expect(revealedCustom).toBe('MY-CUSTOM-CODE-01')

    // Hide clears it; a reload reports configured without code material.
    await page.getByRole('button', { name: 'Hide' }).click()
    await expect(page.locator('input[aria-label="New board join code"]')).toHaveCount(0)
    await page.reload()
    await expect(page.getByText('configured', { exact: true })).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('input[aria-label="New board join code"]')).toHaveCount(0)

    // Typed custom, then "replace with random": the confirmation must commit a
    // random code, not the stale typed value.
    await page.locator('#join-code-custom').fill('stale-value-0001')
    await page.getByRole('button', { name: 'Replace with random code' }).click()
    await expect(page.getByText('Replace the current join code?')).toBeVisible()
    await page.getByRole('button', { name: 'Replace code' }).click()
    await expect(page.getByText('Copy this code now')).toBeVisible({ timeout: 15_000 })
    const revealedRandom = await page.locator('input[aria-label="New board join code"]').inputValue()
    expect(revealedRandom).not.toBe('STALE-VALUE-0001')
    expect(revealedRandom.length).toBe(26)
  })
})
