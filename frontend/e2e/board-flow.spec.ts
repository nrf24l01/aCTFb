import { expect, test, type APIRequestContext } from '@playwright/test'

/**
 * Opt-in real-browser verification against a real backend.
 *
 *   ACTFB_E2E=1 \
 *   E2E_BASE_URL=http://127.0.0.1:5173 \
 *   E2E_API_URL=http://127.0.0.1:8080/api/v2 \
 *   E2E_ADMIN_USERNAME=... E2E_ADMIN_PASSWORD=... \
 *   bun run test:e2e
 *
 * Board setup never happens in SQL and never puts a token in browser storage:
 * the fixture uses an HTTP API/provisioner from the test process only. If
 * E2E_PROVISIONER_URL is set, it returns
 * {boardUuid, moduleUuid, taskUuid, flag} for a private individual board.
 */
const enabled = process.env.ACTFB_E2E === '1'
const apiUrl = (process.env.E2E_API_URL || 'http://127.0.0.1:8080/api/v2').replace(/\/+$/, '')

interface Fixture {
  boardUuid: string
  moduleUuid: string
  taskUuid: string
  flag: string
  username: string
  password: string
}

async function json<T>(request: APIRequestContext, method: 'GET' | 'POST' | 'PATCH' | 'DELETE', path: string, data?: unknown, token?: string) {
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

async function registerOrLogin(request: APIRequestContext): Promise<{ username: string; password: string; token: string }> {
  const suffix = `${Date.now()}${Math.floor(Math.random() * 1000)}`
  const username = (process.env.E2E_USERNAME || `e2e_${suffix}`).slice(0, 32)
  const password = process.env.E2E_PASSWORD || `E2e-password-${suffix}`
  const email = process.env.E2E_EMAIL || `${username}@example.test`

  const registerResponse = await request.post(`${apiUrl}/auth/register`, {
    data: { email, username, password },
  })
  if (registerResponse.ok()) {
    const body = await registerResponse.json()
    return { username, password, token: body.accessToken }
  }
  const login = await json<{ accessToken: string }>(request, 'POST', '/auth/login', { username, password })
  return { username, password, token: login.accessToken }
}

async function provisionViaAdmin(request: APIRequestContext, participantUsername: string): Promise<Fixture> {
  const adminUsername = process.env.E2E_ADMIN_USERNAME
  const adminPassword = process.env.E2E_ADMIN_PASSWORD
  if (!adminUsername || !adminPassword) {
    throw new Error('E2E_ADMIN_USERNAME and E2E_ADMIN_PASSWORD are required when E2E_PROVISIONER_URL is unset')
  }
  const admin = await json<{ accessToken: string }>(request, 'POST', '/auth/login', {
    username: adminUsername,
    password: adminPassword,
  })
  const token = admin.accessToken
  const suffix = `${Date.now()}`

  const board = await json<{ uuid: string }>(request, 'POST', '/admin/boards', {
    slug: `e2e-${suffix}`,
    name: `E2E board ${suffix}`,
    description: 'Automated private board',
    participationMode: 'INDIVIDUAL',
    registrationMode: 'OPEN',
    visibility: 'PRIVATE',
    rating: { visibility: 'PUBLIC' },
    startsAt: new Date(Date.now() - 60_000).toISOString(),
    endsAt: new Date(Date.now() + 3_600_000).toISOString(),
  }, token)

  const module = await json<{ uuid: string }>(request, 'POST', `/admin/boards/${board.uuid}/modules`, {
    name: `E2E module ${suffix}`,
    description: 'Automated TASKS module',
    type: 'TASKS',
    allowedTaskTypes: ['FLAG'],
    enabled: true,
    order: 0,
  }, token)

  const flag = `flag{e2e_${suffix}}`
  const task = await json<{ uuid: string }>(
    request,
    'POST',
    `/admin/boards/${board.uuid}/modules/${module.uuid}/tasks`,
    {
      title: `E2E flag ${suffix}`,
      type: 'FLAG',
      category: 'misc',
      description: 'Submit the generated flag.',
      points: 50,
      visible: true,
      submission: {
        mode: 'TEXT',
        checker: 'AUTOMATIC',
        text: { required: true, minLength: 1, maxLength: 1024 },
        files: null,
      },
      acceptedAnswers: [flag],
      instance: { enabled: false },
    },
    token,
  )

  await json(request, 'POST', `/admin/boards/${board.uuid}/actions`, { action: 'PUBLISH' }, token)

  return {
    boardUuid: board.uuid,
    moduleUuid: module.uuid,
    taskUuid: task.uuid,
    flag,
    username: participantUsername,
    password: '',
  }
}

test.describe('real private board flow', () => {
  test.skip(!enabled, 'Set ACTFB_E2E=1 with a real backend to run browser verification')
  test.describe.configure({ mode: 'serial' })

  let fixture: Fixture

  test.beforeAll(async ({ request }) => {
    const participant = await registerOrLogin(request)
    if (process.env.E2E_PROVISIONER_URL) {
      const response = await request.post(process.env.E2E_PROVISIONER_URL, {
        data: { username: participant.username },
      })
      if (!response.ok()) throw new Error(`Provisioner failed: ${response.status()} ${await response.text()}`)
      const provisioned = await response.json()
      fixture = {
        ...provisioned,
        username: participant.username,
        password: participant.password,
      }
    } else {
      fixture = {
        ...(await provisionViaAdmin(request, participant.username)),
        password: participant.password,
      }
    }
  })

  test('participant joins, submits wrong then right flag, and sees history', async ({ page }) => {
    await page.goto('/sign-in')
    await page.getByLabel('Username').fill(fixture.username)
    await page.getByLabel('Password').fill(fixture.password)
    await page.getByRole('button', { name: 'Sign in' }).click()

    await page.goto(`/boards/${fixture.boardUuid}`)
    const join = page.getByRole('button', { name: 'Join this board' })
    if (await join.isVisible().catch(() => false)) {
      await join.click()
    }
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()

    await page.goto(`/boards/${fixture.boardUuid}/modules/${fixture.moduleUuid}/tasks/${fixture.taskUuid}`)
    await page.getByLabel('Flag').fill('flag{definitely-wrong}')
    await page.getByRole('button', { name: 'Submit flag' }).click()
    await expect(page.getByText('Rejected', { exact: true }).first()).toBeVisible()
    await expect(page.getByLabel('Flag')).toHaveValue('')

    await page.getByLabel('Flag').fill(fixture.flag)
    await page.getByRole('button', { name: 'Submit flag' }).click()
    await expect(page.getByText('Accepted', { exact: true }).first()).toBeVisible()
    await expect(page.getByLabel('Flag')).toHaveValue('')
    await expect(page.getByText('Your submission history')).toBeVisible()

    await page.goto(`/boards/${fixture.boardUuid}/scoreboard`)
    await expect(page.getByRole('heading', { name: 'Scoreboard' })).toBeVisible()
  })
})