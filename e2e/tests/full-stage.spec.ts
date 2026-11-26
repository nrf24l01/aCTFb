import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { randomBytes, randomUUID } from 'node:crypto'

const fullStack = process.env.E2E_FULL_STACK === '1'
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:5173'
const adminUser = process.env.E2E_ADMIN_USERNAME || ''
const adminPass = process.env.E2E_ADMIN_PASSWORD || ''
const studentUser = process.env.E2E_STUDENT_USERNAME || ''
const studentPass = process.env.E2E_STUDENT_PASSWORD || ''
const reviewerUser = process.env.E2E_REVIEWER_USERNAME || ''
const reviewerPass = process.env.E2E_REVIEWER_PASSWORD || ''
const pluginPackage = process.env.E2E_PLUGIN_PACKAGE || 'hello'
const pluginUpgrade = process.env.E2E_PLUGIN_UPGRADE || 'hello-upgrade'
const pgContainer = process.env.E2E_DB_CONTAINER || ''
const pgUser = process.env.E2E_PG_USER || ''
const pgDatabase = process.env.E2E_PG_DB || ''

test.describe.configure({ mode: 'serial' })

test.describe('full-stage acceptance', () => {
  test.skip(!fullStack, 'requires E2E_FULL_STACK=1 from e2e/run-full-e2e.sh')

  test('plugin install / explicit grant / iframe bridge / API / disable / upgrade', async ({ page }, testInfo) => {
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)
    const me = await api(page, token, 'GET', '/api/v2/me')
    const board = await createBoard(page, token, {
      slug: `plugin-${Date.now().toString(36)}`,
      name: 'Plugin Board',
      participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN',
      visibility: 'PRIVATE',
    })
    const boardUuid = board.uuid as string

    await page.goto('/admin/plugins')
    await page.getByPlaceholder('hello').fill(pluginPackage)
    await page.getByRole('button', { name: 'Validate' }).click()
    await expect(page.getByText('Example Hello').first()).toBeVisible()
    await expect(page.getByText('1.0.0').first()).toBeVisible()
    await page.getByRole('button', { name: 'Install without enabling' }).click()
    await expect(page.getByRole('row', { name: /Example Hello/ })).toBeVisible()

    await page.getByRole('row', { name: /Example Hello/ }).getByRole('button', { name: 'Manage' }).click()
    await expect(page.getByRole('button', { name: 'Enable' })).toBeVisible()
    await grant(page, me.uuid as string, boardUuid, 'plugin.example.hello.view')
    await grant(page, me.uuid as string, boardUuid, 'plugin.example.hello.manage')
    await page.getByRole('button', { name: 'Enable' }).click()
    // The status column renders through the shared status badge, which maps the
    // API value `enabled` to the label `Enabled`.
    await expect(page.getByText('Enabled', { exact: true }).first()).toBeVisible()

    const visible = await api(page, token, 'GET', '/api/v2/plugins')
    const plugin = (visible.items as Array<Record<string, unknown>>).find((item) => item.id === 'example.hello') as
      | { pages: Array<{ path: string }> }
      | undefined
    expect(plugin?.pages?.length).toBeGreaterThan(0)
    const pluginPath = plugin!.pages[0].path
    await page.goto(`${pluginPath}?boardUuid=${boardUuid}`)
    const frame = page.frameLocator('iframe[title="Plugin page"]')
    await expect(frame.getByRole('heading', { name: 'Example Hello' })).toBeVisible()
    await expect(frame.locator('#contextLine')).toContainText(boardUuid)
    await frame.locator('#messageBody').fill('real bridge message')
    await frame.locator('#sendMessage').click()
    await expect(frame.locator('#messageStatus')).toHaveText('Saved.', { timeout: 15_000 })
    await expect(frame.getByText('real bridge message', { exact: false })).toBeVisible({ timeout: 15_000 })
    await page.screenshot({ path: testInfo.outputPath('plugin-bridge.png'), fullPage: true })

    await page.goto('/admin/plugins')
    await page.getByRole('row', { name: /Example Hello/ }).getByRole('button', { name: 'Manage' }).click()
    await page.getByRole('button', { name: 'Disable' }).click()
    await expect(page.getByText('Disabled', { exact: true }).first()).toBeVisible()
    await page.goto(`${pluginPath}?boardUuid=${boardUuid}`)
    await expect(page.getByText('Plugin not visible')).toBeVisible()

    await page.goto('/admin/plugins')
    await page.getByRole('row', { name: /Example Hello/ }).getByRole('button', { name: 'Manage' }).click()
    await page.getByRole('button', { name: 'Enable' }).click()
    await page.getByRole('button', { name: 'Upgrade' }).click()
    const upgradeDialog = page.getByRole('dialog')
    await upgradeDialog.locator('input').fill(pluginUpgrade)
    await upgradeDialog.getByRole('button', { name: 'Upgrade', exact: true }).click()
    await expect(page.getByText('1.1.0').first()).toBeVisible({ timeout: 20_000 })
    await page.goto(`${pluginPath}?boardUuid=${boardUuid}`)
    const upgradedFrame = page.frameLocator('iframe[title="Plugin page"]')
    await expect(upgradedFrame.getByRole('heading', { name: 'Example Hello' })).toBeVisible()
    await expect(upgradedFrame.getByText('real bridge message', { exact: false })).toBeVisible({ timeout: 15_000 })
  })

  test('trusted-js default-deny approval and verified full-trust ESM mount', async ({ page }) => {
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)
    const me = await api(page, token, 'GET', '/api/v2/me')
    const board = await createBoard(page, token, {
      slug: `trusted-${Date.now().toString(36)}`,
      name: 'Trusted Plugin Board',
      participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN',
      visibility: 'PRIVATE',
    })
    const boardUuid = board.uuid as string

    await page.goto('/admin/plugins')
    await page.getByPlaceholder('hello').fill('trusted')
    await page.getByRole('button', { name: 'Validate' }).click()
    await expect(page.getByText('Example Trusted').first()).toBeVisible()
    await expect(page.getByText('trusted-js', { exact: false }).first()).toBeVisible()
    // Per-install explicit approval is mandatory; the checkbox starts off.
    await page.getByRole('button', { name: 'Install without enabling' }).click()
    await expect(page.getByText(/Approve the trusted-js risk explicitly/)).toBeVisible()
    await page.locator('label').filter({ hasText: 'This package requests trusted-js' }).getByRole('checkbox').check()
    await page.getByRole('button', { name: 'Install without enabling' }).click()
    await expect(page.getByRole('row', { name: /Example Trusted/ })).toBeVisible()

    await page.getByRole('row', { name: /Example Trusted/ }).getByRole('button', { name: 'Manage' }).click()
    await grant(page, me.uuid as string, boardUuid, 'plugin.example.trusted.view')
    await grant(page, me.uuid as string, boardUuid, 'plugin.example.trusted.manage')
    const enableResponse = page.waitForResponse(
      (response) => response.url().includes('/admin/plugins/example.trusted/enable') && response.request().method() === 'POST',
    )
    await page.getByRole('button', { name: 'Enable' }).click()
    expect((await enableResponse).status()).toBe(200)
    await expect(page.getByRole('row', { name: /Example Trusted/ })).toContainText('Enabled')

    const adminPlugin = await api(page, token, 'GET', '/api/v2/admin/plugins/example.trusted')
    expect(adminPlugin.status).toBe('enabled')
    expect(adminPlugin.trustedJSApproved).toBe(true)
    const visible = await api(page, token, 'GET', '/api/v2/plugins')
    const plugin = (visible.items as Array<Record<string, unknown>>).find((item) => item.id === 'example.trusted') as
      | { pages: Array<{ path: string }>; trustedJS?: { url: string; integrity: string } }
      | undefined
    expect(plugin, JSON.stringify(visible)).toBeTruthy()
    expect(plugin?.trustedJS?.integrity).toMatch(/^sha256:[a-f0-9]{64}$/)
    expect(plugin?.pages?.[0]?.path).toBeTruthy()
    await page.goto(`${plugin!.pages[0].path}?boardUuid=${boardUuid}`)
    await expect(page.getByText('full platform trust')).toBeVisible()
    await expect(page.locator('#trustedPluginTitle')).toHaveText('Trusted Example')
    await expect(page.locator('#trustedPluginContext')).toHaveText(boardUuid)
  })

test('P1R TEAM manual text reload, clean file scan, and reviewer accept', async ({ page, browser }) => {
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)
    const board = await createBoard(page, token, {
      slug: `p1r-${Date.now().toString(36)}`,
      name: 'P1R Team Board',
      participationMode: 'TEAM',
      registrationMode: 'OPEN',
      visibility: 'PUBLIC',
      teamSettings: { minMembers: 1, maxMembers: 4, allowSelfCreate: true, allowLeaveAfterStart: true },
    })
    const boardUuid = board.uuid as string
    const module = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules`, {
      name: 'Manual Module', type: 'TASKS', allowedTaskTypes: ['MANUAL'], enabled: true,
    })
    const moduleUuid = module.uuid as string
    const task = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks`, {
      title: 'Manual file task', type: 'MANUAL', category: 'web', description: 'manual',
      points: 10, visible: true,
      submission: {
        mode: 'TEXT_AND_FILES', checker: 'MANUAL',
        text: { required: true, minLength: 1, maxLength: 1000 },
        files: { required: true, minCount: 1, maxCount: 1, maxSizeBytes: 1048576, allowedContentTypes: ['text/plain'] },
      },
      instance: { enabled: false },
    })
    const taskUuid = task.uuid as string
    await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/actions`, { action: 'PUBLISH' })

    // Enrollment is code-only: even on a PUBLIC board, joining needs the code the
    // organizer issues. There is no codeless self-join affordance.
    const p1rJoinCode = String(
      (await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/join-code`, {})).code,
    )

    const studentContext = await browser.newContext({ viewport: { width: 390, height: 844 } })
    try {
      const studentPage = await studentContext.newPage()
      await signIn(studentPage, studentUser, studentPass)
      // A visible but unjoined TEAM board must offer the code prompt on the board
      // page. Only after the real join may the team panel mount.
      await studentPage.goto(`/boards/${boardUuid}`)
      await expect(studentPage.getByRole('button', { name: 'Enter board code' })).toBeVisible()
      await studentPage.getByRole('button', { name: 'Enter board code' }).click()
      await studentPage.locator('#join-board-code').fill(p1rJoinCode)
      await studentPage.getByRole('button', { name: 'Join', exact: true }).click()
      await expect(studentPage.getByText('Joined: yes')).toBeVisible({ timeout: 15_000 })
      await studentPage.locator('#team-create-name').fill('E2E Team')
      await studentPage.getByRole('button', { name: 'Create team' }).click()
      await expect(studentPage.getByText('E2E Team').first()).toBeVisible({ timeout: 15_000 })

      await studentPage.goto(`/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
      await studentPage.getByLabel('Choose submission files').setInputFiles({
        name: 'clean.txt', mimeType: 'text/plain', buffer: Buffer.from('clean manual text file\n'),
      })
      await expect(studentPage.getByText('Ready', { exact: true }).first()).toBeVisible({ timeout: 30_000 })
      await studentPage.locator('#manual-submission-text').fill('durable manual answer')
      await studentPage.getByRole('button', { name: 'Submit for review' }).click()
      await expect(studentPage.getByText(/PENDING_REVIEW|Needs review/).first()).toBeVisible({ timeout: 20_000 })

      await studentPage.reload()
      await expect(studentPage.getByText(/PENDING_REVIEW|Needs review/).first()).toBeVisible({ timeout: 20_000 })
      
      const reviewerContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
      try {
        const reviewerPage = await reviewerContext.newPage()
        await signIn(reviewerPage, reviewerUser, reviewerPass)
        await reviewerPage.goto(`/boards/${boardUuid}/staff`)
        await reviewerPage.getByRole('button', { name: 'Accept', exact: true }).first().click()
        const dialog = reviewerPage.getByRole('dialog')
        await dialog.getByRole('button', { name: 'Accept submission' }).click()
        await expect(reviewerPage.getByText(/ACCEPTED|Accepted/).first()).toBeVisible({ timeout: 20_000 })
      } finally {
        await reviewerContext.close()
      }

      await studentPage.reload()
      await expect(studentPage.getByText(/ACCEPTED|Accepted/).first()).toBeVisible({ timeout: 20_000 })
    } finally {
      await studentContext.close()
    }
  })

  test('A+D awards appear on the canonical board scoreboard', async ({ page }) => {
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)
    const board = await createBoard(page, token, {
      slug: `ad-${Date.now().toString(36)}`,
      name: 'AD Score Board',
      participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN',
      visibility: 'PRIVATE',
    })
    const boardUuid = board.uuid as string

    const student = psql(`SELECT id::text FROM users WHERE username='${studentUser.replace(/'/g, "''")}' LIMIT 1`)
      .trim()
    const admin = psql(`SELECT id::text FROM users WHERE username='${adminUser.replace(/'/g, "''")}' LIMIT 1`)
      .trim()
    const moduleUuid = randomUUID()
    const integrationUuid = randomUUID()
    const assignmentUuid = randomUUID()
    const vulnerabilityUuid = randomUUID()
    const runUuid = randomUUID()
    const awardUuid = randomUUID()
    const commit = randomBytes(20).toString('hex')
    psql(`
      INSERT INTO modules (id, board_id, name, type, allowed_task_types, enabled, created_by)
      VALUES ('${moduleUuid}','${boardUuid}','AD','ATTACK_DEFENSE',ARRAY['EXPLOIT','PATCH'],TRUE,'${admin}');
      INSERT INTO integrations (id, category, type, name, base_url, status, created_by)
      VALUES ('${integrationUuid}','GIT','GITEA','local-gitea','http://127.0.0.1:3001','CONNECTED','${admin}');
      INSERT INTO ad_vulnerabilities (id, module_id, key, title, description, exploit_points, patch_points, exploit_test_selector, patch_test_selector)
      VALUES ('${vulnerabilityUuid}','${moduleUuid}','sqli','SQL injection','',250,0,'TestSQLiExploit','TestSQLiPatch');
      INSERT INTO ad_repository_assignments
        (id, module_id, subject_type, subject_id, source_integration_id, source_repository, source_branch,
         exploit_integration_id, exploit_repository, exploit_branch, created_by)
      VALUES ('${assignmentUuid}','${moduleUuid}','USER','${student}','${integrationUuid}','team/source','main',
              '${integrationUuid}','team/exploit','exploit','${admin}');
      INSERT INTO ad_runs
        (id, module_id, repository_assignment_id, subject_type, subject_id, kind, commit_sha, status, requested_by)
      VALUES ('${runUuid}','${moduleUuid}','${assignmentUuid}','USER','${student}','EXPLOIT','${commit}','PASSED','${admin}');
      INSERT INTO ad_awards (id, module_id, subject_type, subject_id, vulnerability_id, kind, run_id, points)
      VALUES ('${awardUuid}','${moduleUuid}','USER','${student}','${vulnerabilityUuid}','EXPLOIT','${runUuid}',250);
      UPDATE boards SET status='ACTIVE' WHERE id='${boardUuid}';
    `)

    await page.goto(`/boards/${boardUuid}/scoreboard`)
    const row = page.getByRole('row', { name: new RegExp(studentUser) })
    await expect(row).toBeVisible()
    await expect(row).toContainText('250')
  })

  test('PENTEST approved binding, Docker environment, durable report and reviewer accept', async ({ page, browser }, testInfo) => {
    const dockerSocket = process.env.E2E_DOCKER_SOCKET || ''
    const imageRef = process.env.E2E_DOCKER_IMAGE_REF || ''
    const instancePort = process.env.E2E_INSTANCE_PORT || ''
    test.skip(!dockerSocket || !imageRef || !instancePort, 'pentest acceptance needs E2E_DOCKER_* and a free port')
    test.skip(dockerSocket !== '/var/run/docker.sock', 'pentest endpoint reachability requires real Docker Engine')

    const suffix = Date.now().toString(36)
    const integrationName = `E2E Pentest Docker ${suffix}`
    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)

    const integration = await api(page, token, 'POST', '/api/v2/admin/integrations', {
      type: 'DOCKER',
      kind: 'CONTAINER',
      driver: 'docker',
      name: integrationName,
      settings: {
        socket: dockerSocket,
        registryAllowlist: ['nginx'],
        dedicatedNode: `e2e-pentest-${suffix}`,
        dedicatedNodeVerified: true,
        pidsLimit: 128,
        network: { kind: 'network' },
      },
      instancePublicationPolicy: {
        publicTcpHost: '127.0.0.1',
        publicPortFrom: Number(instancePort),
        publicPortTo: Number(instancePort),
      },
    })
    const integrationUuid = integration.uuid as string

    const boardEndsAt = new Date(Date.now() + 3 * 60 * 60 * 1000).toISOString()
    const board = await createBoard(page, token, {
      slug: `pentest-${suffix}`,
      name: 'Pentest Board',
      participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN',
      visibility: 'PUBLIC',
      startsAt: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      endsAt: boardEndsAt,
    })
    const boardUuid = board.uuid as string

    const tasksModule = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules`, {
      name: 'Tasks', type: 'TASKS', allowedTaskTypes: ['FLAG'], enabled: true,
    })
    const tasksModuleUuid = tasksModule.uuid as string
    await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules/${tasksModuleUuid}/tasks`, {
      title: 'Publishable flag', type: 'FLAG', category: 'web', description: 'keeps the board publishable',
      points: 10, visible: true, submission: { mode: 'TEXT', checker: 'AUTOMATIC' },
      acceptedAnswers: ['flag{pentest-publish}'], instance: { enabled: false },
    })
    const pentestModule = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules`, {
      name: 'Pentest Lab Module', type: 'PENTEST', allowedTaskTypes: ['PENTEST_REPORT'], enabled: true,
    })
    const pentestModuleUuid = pentestModule.uuid as string

    const nginxConfig = 'server {\n  listen 8080;\n  root /usr/share/nginx/html;\n  location / { try_files $uri =404; }\n}\n'
    const manifest = `apiVersion: actfb.dev/v1alpha1
kind: DeploymentTemplate
metadata:
  name: E2E Pentest
  slug: pentest-${suffix}
  description: Browser acceptance pentest lab
nodes:
  - key: web
    name: Web
    kind: CONTAINER
    source:
      type: OCI_IMAGE
      ref: ${imageRef}
    resources:
      cpuMillicores: 200
      memoryMiB: 64
      diskMiB: 64
    files:
      - source: nginx.conf
        target: /etc/nginx/conf.d/default.conf
      - source: marker
        target: /usr/share/nginx/html/marker
    endpoints:
      - key: web
        protocol: http
        targetPort: 8080
        exposure: PUBLIC
    driverOptions:
      docker:
        readOnlyRootFilesystem: true
        user: "101:101"
        userNumeric: true
        entrypoint: ["nginx"]
        cmd: ["-g", "daemon off;"]
        tmpfs:
          /tmp: 8
          /var/cache/nginx: 16
          /var/run: 8
lifecycle:
  reset: RECREATE
`
    const template = await api(page, token, 'POST', '/api/v2/admin/deployment-templates', {
      manifestYaml: manifest,
      files: [
        { path: 'nginx.conf', contentBase64: Buffer.from(nginxConfig).toString('base64') },
        { path: 'marker', contentBase64: Buffer.from('pentest-marker').toString('base64') },
      ],
    })
    const templateUuid = template.uuid as string
    const published = await api(page, token, 'POST', `/api/v2/admin/deployment-templates/${templateUuid}/publish`, {
      versionNote: 'Pentest browser acceptance',
    })
    const versionUuid = published.uuid as string
    await api(page, token, 'POST', `/api/v2/admin/deployment-templates/${templateUuid}/allowlist`, {
      templateVersionUuid: versionUuid,
      boardUuid,
      moduleUuid: pentestModuleUuid,
      targets: [{ integrationUuid, kind: 'CONTAINER' }],
    })

    // Real admin UI: bind the exact approved version. The browser never sends
    // a provider integration, image, target or secret.
    await page.goto(`/admin/boards/${boardUuid}`)
    const pentestPanel = page.locator('section').filter({ hasText: 'Pentest Lab Module' }).first()
    await expect(pentestPanel.getByText('Pentest config')).toBeVisible({ timeout: 20_000 })
    await pentestPanel.locator('#pentest-template').click()
    await page.getByRole('option', { name: /E2E Pentest/ }).click()
    await pentestPanel.locator('#pentest-max-resets').fill('2')
    await pentestPanel.locator('#pentest-mode').click()
    await page.getByRole('option', { name: 'TEXT_AND_FILES', exact: true }).click()
    await pentestPanel.getByRole('button', { name: 'Save config' }).click()
    await expect(page.getByText('Pentest admin action failed')).toHaveCount(0)
    await expect.poll(async () => {
      const config = await api(page, token, 'GET', `/api/v2/admin/boards/${boardUuid}/modules/${pentestModuleUuid}/pentest/config`)
      return config.deploymentTemplateUuid
    }, { timeout: 20_000 }).toBe(templateUuid)
    await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/actions`, { action: 'PUBLISH' })

    // Enrollment is code-only, so the organizer issues the code the participant
    // uses. There is no codeless self-join affordance.
    const pentestJoinCode = String(
      (await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/join-code`, {})).code,
    )

    const studentContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
    let studentToken = ''
    let environmentStarted = false
    try {
      const studentPage = await studentContext.newPage()
      await signIn(studentPage, studentUser, studentPass)
      await studentPage.goto(`/boards/${boardUuid}`)
      await studentPage.getByRole('button', { name: 'Enter board code' }).click()
      await studentPage.locator('#join-board-code').fill(pentestJoinCode)
      await studentPage.getByRole('button', { name: 'Join', exact: true }).click()
      await expect(studentPage.getByText('Joined: yes')).toBeVisible({ timeout: 15_000 })
      await studentPage.getByRole('link', { name: /Pentest Lab Module/ }).click()
      await expect(studentPage.getByRole('link', { name: 'Open Pentest lab' })).toBeVisible()
      await studentPage.getByRole('link', { name: 'Open Pentest lab' }).click()
      await expect(studentPage).toHaveURL(new RegExp(`/boards/${boardUuid}/modules/${pentestModuleUuid}/pentest$`))
      await studentPage.getByRole('button', { name: 'Start environment' }).click()
      await expect(studentPage.getByText('Running', { exact: true })).toBeVisible({ timeout: 90_000 })
      environmentStarted = true
      studentToken = await freshToken(studentPage)
      const environment = await api(studentPage, studentToken, 'GET',
        `/api/v2/boards/${boardUuid}/modules/${pentestModuleUuid}/pentest/environment`)
      expect(new Date(environment.expiresAt as string).getTime()).toBe(new Date(boardEndsAt).getTime())

      await studentPage.getByLabel('Choose submission files').setInputFiles({
        name: 'pentest-report.txt', mimeType: 'text/plain', buffer: Buffer.from('durable pentest report file\n'),
      })
      await expect(studentPage.getByText('Ready', { exact: true }).first()).toBeVisible({ timeout: 30_000 })
      const reportText = `durable pentest report ${suffix}`
      await studentPage.locator('#pentest-report-text').fill(reportText)
      await studentPage.getByRole('button', { name: 'Submit report' }).click()
      await expect(studentPage.getByText(/PENDING_REVIEW|Needs review/).first()).toBeVisible({ timeout: 20_000 })
      await studentPage.reload()
      await expect(studentPage.getByText(/PENDING_REVIEW|Needs review/).first()).toBeVisible({ timeout: 20_000 })
      const reports = await api(studentPage, studentToken, 'GET',
        `/api/v2/boards/${boardUuid}/modules/${pentestModuleUuid}/pentest/submissions?limit=100&offset=0`)
      const report = (reports.items as Array<Record<string, unknown>>)[0]
      expect(report.text).toBe(reportText)
      expect((report.files as Array<unknown>).length).toBeGreaterThan(0)
      await studentPage.screenshot({ path: testInfo.outputPath('pentest-report.png'), fullPage: true })

      const reviewerContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
      try {
        const reviewerPage = await reviewerContext.newPage()
        await signIn(reviewerPage, reviewerUser, reviewerPass)
        await reviewerPage.goto(`/boards/${boardUuid}/staff`)
        await reviewerPage.getByRole('button', { name: 'Accept', exact: true }).first().click()
        const dialog = reviewerPage.getByRole('dialog')
        await dialog.getByRole('button', { name: 'Accept submission' }).click()
        await expect(reviewerPage.getByText(/ACCEPTED|Accepted/).first()).toBeVisible({ timeout: 20_000 })
      } finally {
        await reviewerContext.close()
      }

      await studentPage.reload()
      await expect(studentPage.getByText(/ACCEPTED|Accepted/).first()).toBeVisible({ timeout: 20_000 })

      // Pentest has no independent TTL; release the Docker binding/port before
      // the next test registers another integration on the shared port.
      await destroyPentestEnvironment(studentPage, studentToken, boardUuid, pentestModuleUuid)
      environmentStarted = false
    } finally {
      if (environmentStarted && studentToken) {
        await destroyPentestEnvironment(page, studentToken, boardUuid, pentestModuleUuid).catch(() => undefined)
      }
      await studentContext.close()
    }
  })

  test('deployment template publish, scoped allowlist, and instance start/reset/destroy', async ({ page, browser }, testInfo) => {
    const dockerSocket = process.env.E2E_DOCKER_SOCKET || ''
    const imageRef = process.env.E2E_DOCKER_IMAGE_REF || ''
    const instancePort = process.env.E2E_INSTANCE_PORT || ''
    test.skip(!dockerSocket || !imageRef || !instancePort, 'instance acceptance needs E2E_DOCKER_* and a free port')
    // The Docker driver publishes through a worker-side forwarder to the
    // container IP. Rootless Podman container IPs are not host-routable, so the
    // full start/reset/destroy + HTTP check requires a real Docker Engine.
    test.skip(dockerSocket !== '/var/run/docker.sock', 'instance endpoint reachability requires real Docker Engine')
    const suffix = Date.now().toString(36)
    const integrationName = `E2E Docker ${suffix}`

    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)

    // Real admin UI: register the trusted Docker-compatible compute target.
    await page.goto('/admin/integrations')
    await page.getByRole('button', { name: 'Create integration' }).click()
    const integrationDialog = page.getByRole('dialog')
    await integrationDialog.locator('#integration-type').click()
    await page.getByRole('option', { name: 'DOCKER', exact: true }).click()
    await integrationDialog.locator('#integration-name').fill(integrationName)
    await integrationDialog.locator('#integration-settings').fill(
      JSON.stringify({
        socket: dockerSocket,
        registryAllowlist: ['nginx'],
        dedicatedNode: `e2e-${suffix}`,
        dedicatedNodeVerified: true,
        pidsLimit: 128,
        network: { kind: 'network' },
      }),
    )
    await integrationDialog.locator('#integration-public-host').fill('127.0.0.1')
    await integrationDialog.locator('#integration-port-from').fill(instancePort)
    await integrationDialog.locator('#integration-port-to').fill(instancePort)
    await integrationDialog.getByRole('button', { name: 'Save integration' }).click()
    await expect(integrationDialog).toBeHidden()
    const integrationList = await api(page, token, 'GET', '/api/v2/admin/integrations?limit=100&offset=0')
    const integration = (integrationList.items as Array<Record<string, unknown>>).find((item) => item.name === integrationName)
    expect(integration).toBeTruthy()
    const integrationUuid = integration!.uuid as string

    // A real board/module are required before a template version can be allowlisted.
    const board = await createBoard(page, token, {
      slug: `inst-${suffix}`,
      name: 'Instance Board',
      participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN',
      visibility: 'PRIVATE',
    })
    const boardUuid = board.uuid as string
    const module = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules`, {
      name: 'Instance Module', type: 'TASKS', allowedTaskTypes: ['INSTANCE'], enabled: true,
    })
    const moduleUuid = module.uuid as string

    // Real admin UI: create draft, validate, preview and publish the immutable version.
    await page.goto('/admin/templates')
    await page.getByRole('button', { name: 'Create template' }).click()
    await expect(page).toHaveURL(/\/admin\/templates\/[0-9a-f-]{36}$/)
    const templateUuid = page.url().split('/').pop() as string
    const nginxConfig = 'server {\n  listen 8080;\n  root /usr/share/nginx/html;\n  location / { try_files $uri =404; }\n}\n'
    const manifest = `apiVersion: actfb.dev/v1alpha1
kind: DeploymentTemplate
metadata:
  name: E2E Instance
  slug: inst-${suffix}
  description: Browser acceptance instance
nodes:
  - key: web
    name: Web
    kind: CONTAINER
    source:
      type: OCI_IMAGE
      ref: ${imageRef}
    resources:
      cpuMillicores: 200
      memoryMiB: 64
      diskMiB: 64
    files:
      - source: nginx.conf
        target: /etc/nginx/conf.d/default.conf
      - source: marker
        target: /usr/share/nginx/html/marker
    endpoints:
      - key: web
        protocol: http
        targetPort: 8080
        exposure: PUBLIC
    driverOptions:
      docker:
        readOnlyRootFilesystem: true
        user: "101:101"
        userNumeric: true
        entrypoint: ["nginx"]
        cmd: ["-g", "daemon off;"]
        tmpfs:
          /tmp: 8
          /var/cache/nginx: 16
          /var/run: 8
lifecycle:
  reset: RECREATE
`
    await page.getByLabel('Template manifest YAML').fill(manifest)
    const filesSection = page.locator('section').filter({ hasText: 'Package files' })
    await filesSection.locator('input[type=file]').setInputFiles([
      { name: 'nginx.conf', mimeType: 'text/plain', buffer: Buffer.from(nginxConfig) },
      { name: 'marker', mimeType: 'text/plain', buffer: Buffer.from('gen-1') },
    ])
    await expect(filesSection.getByRole('textbox', { name: 'Package file path' })).toHaveCount(2)
    await expect(filesSection.getByRole('textbox', { name: 'Package file path' }).nth(0)).toHaveValue('nginx.conf')
    await expect(filesSection.getByRole('textbox', { name: 'Package file path' }).nth(1)).toHaveValue('marker')
    await page.getByRole('button', { name: 'Save draft' }).click()
    await page.getByRole('button', { name: 'Validate', exact: true }).click()
    await expect(page.getByText('Validation: valid')).toBeVisible({ timeout: 20_000 })
    await page.getByRole('button', { name: 'Preview', exact: true }).click()
    await expect(page.getByText('Preview (redacted)')).toBeVisible({ timeout: 20_000 })
    await page.getByRole('button', { name: 'Publish', exact: true }).click()
    const publishDialog = page.getByRole('dialog')
    await publishDialog.locator('#version-note').fill('Browser acceptance version')
    await publishDialog.getByRole('button', { name: 'Publish', exact: true }).click()
    await expect(page.getByText(/^v1 ·/)).toBeVisible({ timeout: 20_000 })

    // The allowlist must be board/module scoped; a global row can never bind.
    await page.locator('#allowlist-version').click()
    await page.getByRole('option', { name: /^v1 / }).click()
    await page.locator('#allowlist-board').fill(boardUuid)
    await page.locator('#allowlist-module').fill(moduleUuid)
    await page.locator('#allowlist-integration').click()
    await page.getByRole('option', { name: integrationName }).click()
    await page.getByRole('button', { name: 'Create allowlist' }).click()
    await expect(page.getByText('Allowlist action failed')).toHaveCount(0)
    const allowlists = await api(page, token, 'GET', `/api/v2/admin/deployment-templates/${templateUuid}/allowlist`)
    expect((allowlists.items as Array<Record<string, unknown>>).some((item) => item.boardUuid === boardUuid)).toBeTruthy()

    const instanceTask = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks`, {
      title: 'Start the lab', type: 'INSTANCE', category: 'web', description: 'real instance acceptance',
      points: 10, visible: true,
      submission: { mode: 'TEXT', checker: 'MANUAL', text: { required: true, minLength: 1, maxLength: 1000 } },
      instance: { enabled: true, deploymentTemplateUuid: templateUuid, templateVersion: 1, parameters: {} },
    })
    const taskUuid = instanceTask.uuid as string
    await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/actions`, { action: 'PUBLISH' })

    const instancePath = `/api/v2/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/instance`
    let instanceToken = token
    const getInstance = async (): Promise<Record<string, unknown> | null> => {
      const response = await page.request.get(`${base}${instancePath}`, {
        headers: { Authorization: `Bearer ${instanceToken}` },
      })
      if (!response.ok()) return null
      return (await response.json()) as Record<string, unknown>
    }

    const studentContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
    try {
      const studentPage = await studentContext.newPage()
      await signIn(studentPage, studentUser, studentPass)
      instanceToken = await freshToken(studentPage)
      const joinCode = String((await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/join-code`, {})).code)
      await studentPage.goto('/boards')
      await studentPage.getByRole('button', { name: 'Join a board' }).click()
      await studentPage.locator('#join-board-code').fill(joinCode)
      await studentPage.getByRole('button', { name: 'Join', exact: true }).click()
      await expect(studentPage).toHaveURL(new RegExp(`/boards/${boardUuid}$`))
      await studentPage.goto(`/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
      await studentPage.getByRole('button', { name: 'Start instance' }).click()
      await expect(studentPage.getByText('Running', { exact: true })).toBeVisible({ timeout: 90_000 })

      const instanceSection = studentPage.locator('section[aria-labelledby="instance-heading"]')
      const endpointText = await instanceSection.innerText()
      const endpoint = endpointText.match(/127\.0\.0\.1:(\d+)/)
      expect(endpoint).toBeTruthy()
      const markerUrl = `http://127.0.0.1:${endpoint![1]}/marker`
      await expect.poll(async () => {
        try {
          const response = await studentPage.request.get(markerUrl)
          return response.ok() ? (await response.text()).trim() : 'not-ready'
        } catch {
          return 'not-ready'
        }
      }, { timeout: 30_000 }).toBe('gen-1')
      await studentPage.screenshot({ path: testInfo.outputPath('instance-running.png'), fullPage: true })

      const beforeReset = await getInstance()
      expect(beforeReset?.status).toBe('RUNNING')
      const resetResponsePromise = studentPage.waitForResponse(
        (response) =>
          response.url().includes(`/tasks/${taskUuid}/instance/actions`) &&
          response.request().method() === 'POST',
      )
      await studentPage.getByRole('button', { name: 'Reset', exact: true }).click()
      const resetResponse = await resetResponsePromise
      expect(resetResponse.status()).toBe(202)
      const resetBody = (await resetResponse.json()) as Record<string, unknown>
      expect(['RESETTING', 'PROVISIONING']).toContain(resetBody.status)
      await expect.poll(async () => (await getInstance())?.status ?? 'missing', { timeout: 60_000 }).toBe('RUNNING')
      const afterReset = await getInstance()
      const nodes = (afterReset?.nodes ?? []) as Array<{ endpoints?: Array<{ host?: string; port?: number }> }>
      const resetEndpoint = nodes.flatMap((node) => node.endpoints ?? []).find((item) => item.host && item.port)
      expect(resetEndpoint).toBeTruthy()
      const resetMarker = `http://${resetEndpoint!.host}:${resetEndpoint!.port}/marker`
      await expect.poll(async () => {
        try {
          const response = await studentPage.request.get(resetMarker)
          return response.ok() ? (await response.text()).trim() : 'not-ready'
        } catch {
          return 'not-ready'
        }
      }, { timeout: 30_000 }).toBe('gen-1')

      await studentPage.getByRole('button', { name: 'Destroy', exact: true }).click()
      const destroyDialog = studentPage.getByRole('dialog')
      await destroyDialog.getByRole('button', { name: 'Destroy instance' }).click()
      await expect.poll(async () => (await getInstance())?.status ?? 'gone', { timeout: 60_000 }).toMatch(/EXPIRED|gone/)
    } finally {
      const leftover = await getInstance()
      if (leftover && leftover.status !== 'EXPIRED') {
        await page.request.post(`${base}${instancePath}/actions`, {
          headers: {
            Authorization: `Bearer ${instanceToken}`,
            'Content-Type': 'application/json',
            'Idempotency-Key': randomUUID(),
          },
          data: JSON.stringify({ action: 'DESTROY' }),
        }).catch(() => undefined)
        await expect.poll(async () => (await getInstance())?.status ?? 'gone', { timeout: 30_000 }).toMatch(/EXPIRED|gone/).catch(() => undefined)
      }
      await studentContext.close()
    }
  })

  test('instance lifetime: module settings, task overrides, real extend cap and reset', async ({ page, browser }, testInfo) => {
    const dockerSocket = process.env.E2E_DOCKER_SOCKET || ''
    const imageRef = process.env.E2E_DOCKER_IMAGE_REF || ''
    const instancePort = process.env.E2E_INSTANCE_PORT || ''
    test.skip(!dockerSocket || !imageRef || !instancePort, 'lifetime acceptance needs E2E_DOCKER_* and a free port')
    test.skip(dockerSocket !== '/var/run/docker.sock', 'instance endpoint reachability requires real Docker Engine')
    const suffix = Date.now().toString(36)
    const integrationName = `E2E Lifetime ${suffix}`

    await signIn(page, adminUser, adminPass)
    const token = await freshToken(page)

    // --- real admin UI: register the Docker target -------------------------
    await page.goto('/admin/integrations')
    await page.getByRole('button', { name: 'Create integration' }).click()
    const integrationDialog = page.getByRole('dialog')
    await integrationDialog.locator('#integration-type').click()
    await page.getByRole('option', { name: 'DOCKER', exact: true }).click()
    await integrationDialog.locator('#integration-name').fill(integrationName)
    await integrationDialog.locator('#integration-settings').fill(
      JSON.stringify({
        socket: dockerSocket,
        registryAllowlist: ['nginx'],
        dedicatedNode: `e2e-life-${suffix}`,
        dedicatedNodeVerified: true,
        pidsLimit: 128,
        network: { kind: 'network' },
      }),
    )
    await integrationDialog.locator('#integration-public-host').fill('127.0.0.1')
    await integrationDialog.locator('#integration-port-from').fill(instancePort)
    await integrationDialog.locator('#integration-port-to').fill(instancePort)
    await integrationDialog.getByRole('button', { name: 'Save integration' }).click()
    await expect(integrationDialog).toBeHidden()
    const integrationList = await api(page, token, 'GET', '/api/v2/admin/integrations?limit=100&offset=0')
    const integration = (integrationList.items as Array<Record<string, unknown>>).find((item) => item.name === integrationName)
    expect(integration).toBeTruthy()

    const board = await createBoard(page, token, {
      slug: `life-${suffix}`,
      name: 'Lifetime Board',
      participationMode: 'INDIVIDUAL',
      registrationMode: 'OPEN',
      visibility: 'PRIVATE',
    })
    const boardUuid = board.uuid as string

    // --- real admin UI: module instance settings persist --------------------
    await page.goto(`/admin/boards/${boardUuid}`)
    await page.getByRole('button', { name: 'Create module' }).first().click()
    const moduleDialog = page.getByRole('dialog')
    await moduleDialog.locator('#module-name').fill('Lifetime Module')
    // #module-format is a native select, so select by value.
    await moduleDialog.locator('#module-format').selectOption('INSTANCE')
    // Instance limits live inside the collapsed Advanced section.
    await moduleDialog.locator('summary').filter({ hasText: 'Advanced' }).click()
    await expect(moduleDialog.locator('#module-instance-concurrent')).toBeVisible()
    await moduleDialog.locator('#module-instance-concurrent').fill('1')
    await moduleDialog.locator('#module-instance-default-ttl').fill('0.5')
    await moduleDialog.locator('#module-instance-max-ttl').fill('2')
    await moduleDialog.locator('#module-instance-extension-minutes').fill('15')
    await moduleDialog.locator('#module-instance-extension-count').fill('6')
    await moduleDialog.getByRole('button', { name: 'Create module' }).click()
    await expect(moduleDialog).toBeHidden({ timeout: 20_000 })
    // The backend used to answer UNSUPPORTED here; assert the real values came back.
    await expect(page.getByText('Could not save module')).toHaveCount(0)
    const modules = await api(page, token, 'GET', `/api/v2/admin/boards/${boardUuid}/modules?limit=100&offset=0`)
    const module = (modules.items as Array<Record<string, unknown>>).find((item) => item.name === 'Lifetime Module')
    expect(module).toBeTruthy()
    const moduleUuid = module!.uuid as string
    const moduleSettings = module!.taskInstanceSettings as Record<string, unknown>
    expect(moduleSettings).toBeTruthy()
    expect(moduleSettings.defaultTtlSeconds).toBe(1800)
    expect(moduleSettings.maxTtlSeconds).toBe(7200)
    expect((moduleSettings.extension as Record<string, unknown>).seconds).toBe(900)
    expect((moduleSettings.extension as Record<string, unknown>).maxCount).toBe(6)

    // Reload the editor: the persisted values must be shown, not defaults.
    await page.goto(`/admin/boards/${boardUuid}`)
    const moduleSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Lifetime Module' }) })
    await moduleSection.getByRole('button', { name: 'Edit module' }).click()
    const reopened = page.getByRole('dialog')
    await reopened.locator('summary').filter({ hasText: 'Advanced' }).click()
    await expect(reopened.locator('#module-instance-default-ttl')).toHaveValue('0.5')
    await expect(reopened.locator('#module-instance-max-ttl')).toHaveValue('2')
    await expect(reopened.locator('#module-instance-extension-minutes')).toHaveValue('15')
    await expect(reopened.locator('#module-instance-extension-count')).toHaveValue('6')
    await reopened.getByRole('button', { name: 'Cancel' }).click()

    // --- template + allowlist (API; the UI flow is covered by the test above)
    await page.goto('/admin/templates')
    await page.getByRole('button', { name: 'Create template' }).click()
    await expect(page).toHaveURL(/\/admin\/templates\/[0-9a-f-]{36}$/)
    const templateUuid = page.url().split('/').pop() as string
    const nginxConfig = 'server {\n  listen 8080;\n  root /usr/share/nginx/html;\n  location / { try_files $uri =404; }\n}\n'
    const manifest = `apiVersion: actfb.dev/v1alpha1
kind: DeploymentTemplate
metadata:
  name: E2E Lifetime
  slug: life-${suffix}
  description: Browser acceptance lifetime
nodes:
  - key: web
    name: Web
    kind: CONTAINER
    source:
      type: OCI_IMAGE
      ref: ${imageRef}
    resources:
      cpuMillicores: 200
      memoryMiB: 64
      diskMiB: 64
    files:
      - source: nginx.conf
        target: /etc/nginx/conf.d/default.conf
      - source: marker
        target: /usr/share/nginx/html/marker
    endpoints:
      - key: web
        protocol: http
        targetPort: 8080
        exposure: PUBLIC
    driverOptions:
      docker:
        readOnlyRootFilesystem: true
        user: "101:101"
        userNumeric: true
        entrypoint: ["nginx"]
        cmd: ["-g", "daemon off;"]
        tmpfs:
          /tmp: 8
          /var/cache/nginx: 16
          /var/run: 8
lifecycle:
  reset: RECREATE
`
    await page.getByLabel('Template manifest YAML').fill(manifest)
    const filesSection = page.locator('section').filter({ hasText: 'Package files' })
    await filesSection.locator('input[type=file]').setInputFiles([
      { name: 'nginx.conf', mimeType: 'text/plain', buffer: Buffer.from(nginxConfig) },
      { name: 'marker', mimeType: 'text/plain', buffer: Buffer.from('lifetime-1') },
    ])
    await expect(filesSection.getByRole('textbox', { name: 'Package file path' })).toHaveCount(2)
    await page.getByRole('button', { name: 'Save draft' }).click()
    await page.getByRole('button', { name: 'Validate', exact: true }).click()
    await expect(page.getByText('Validation: valid')).toBeVisible({ timeout: 20_000 })
    await page.getByRole('button', { name: 'Publish', exact: true }).click()
    const publishDialog = page.getByRole('dialog')
    await publishDialog.locator('#version-note').fill('Lifetime acceptance')
    await publishDialog.getByRole('button', { name: 'Publish', exact: true }).click()
    await expect(page.getByText(/^v1 ·/)).toBeVisible({ timeout: 20_000 })
    await page.locator('#allowlist-version').click()
    await page.getByRole('option', { name: /^v1 / }).click()
    await page.locator('#allowlist-board').fill(boardUuid)
    await page.locator('#allowlist-module').fill(moduleUuid)
    await page.locator('#allowlist-integration').click()
    await page.getByRole('option', { name: integrationName }).click()
    await page.getByRole('button', { name: 'Create allowlist' }).click()
    await expect(page.getByText('Allowlist action failed')).toHaveCount(0)

    // --- task with a narrowed lifetime override ----------------------------
    const instanceTask = await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks`, {
      title: 'Lifetime lab', type: 'INSTANCE', category: 'web', description: 'lifetime acceptance',
      points: 10, visible: true,
      submission: { mode: 'TEXT', checker: 'MANUAL', text: { required: true, minLength: 1, maxLength: 1000 } },
      instance: {
        enabled: true, deploymentTemplateUuid: templateUuid, templateVersion: 1, parameters: {},
        ttlSeconds: 1800, maxTtlSeconds: 7200,
        extension: { enabled: true, seconds: 900, maxCount: 2 },
      },
    })
    const taskUuid = instanceTask.uuid as string
    expect((instanceTask.instanceConfig as Record<string, unknown>).ttlSeconds).toBe(1800)
    expect((instanceTask.instanceConfig as Record<string, unknown>).maxTtlSeconds).toBe(7200)
    expect(((instanceTask.instanceConfig as Record<string, unknown>).extension as Record<string, unknown>).maxCount).toBe(2)

    // --- real admin UI: the task editor round-trips the policy -------------
    const openTaskEditor = async () => {
      await page.goto(`/admin/boards/${boardUuid}`)
      const row = page.getByRole('row').filter({ hasText: 'Lifetime lab' })
      await row.getByRole('button', { name: 'Edit', exact: true }).click()
      return page.getByRole('dialog')
    }
    // The advanced lifetime fields only exist in Custom mode, inside a collapsed
    // <details>. Select Custom first, then expand.
    const openCustomLifetime = async () => {
      const dialog = await openTaskEditor()
      await dialog.getByRole('button', { name: 'Custom' }).click()
      await dialog.locator('summary').filter({ hasText: 'Advanced' }).click()
      return dialog
    }
    const taskDialog = await openCustomLifetime()
    await expect(taskDialog.getByText('Inherit module defaults')).toBeVisible()
    // The stored override is Custom, so the custom inputs are visible.
    await expect(taskDialog.locator('#task-lifetime-initial')).toHaveValue('30')
    await expect(taskDialog.locator('#task-lifetime-max')).toHaveValue('120')
    await expect(taskDialog.locator('#task-extension-step')).toHaveValue('15')
    await expect(taskDialog.locator('#task-extension-count')).toHaveValue('2')
    await taskDialog.getByRole('button', { name: 'Save changes' }).click()
    await expect(taskDialog).toBeHidden({ timeout: 20_000 })
    // Saving metadata must not revert to the hardcoded 1h or clear the binding.
    const afterSave = await api(page, token, 'GET', `/api/v2/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
    const savedConfig = afterSave.instanceConfig as Record<string, unknown>
    expect(savedConfig.ttlSeconds).toBe(1800)
    expect(savedConfig.maxTtlSeconds).toBe(7200)
    expect((savedConfig.extension as Record<string, unknown>).maxCount).toBe(2)
    // The immutable binding must survive a metadata/lifetime save. The admin
    // read model does not expose the template id, so check the real row.
    const bindingAfterSave = psql(`SELECT deployment_binding_id IS NOT NULL FROM tasks WHERE id='${taskUuid}'`).trim()
    expect(bindingAfterSave).toBe('t')

    // Inherit clears every override explicitly (null, never zero).
    const inheritDialog = await openTaskEditor()
    await inheritDialog.getByRole('button', { name: 'Inherit module defaults' }).click()
    await inheritDialog.getByRole('button', { name: 'Save changes' }).click()
    await expect(inheritDialog).toBeHidden({ timeout: 20_000 })
    const inherited = await api(page, token, 'GET', `/api/v2/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
    const inheritedConfig = inherited.instanceConfig as Record<string, unknown>
    expect(inheritedConfig.ttlSeconds ?? null).toBeNull()
    expect(inheritedConfig.maxTtlSeconds ?? null).toBeNull()
    expect(inheritedConfig.extension ?? null).toBeNull()
    expect(psql(`SELECT deployment_binding_id IS NOT NULL FROM tasks WHERE id='${taskUuid}'`).trim()).toBe('t')

    // Restore the narrowed policy through the real UI (Custom + values).
    const customDialog = await openCustomLifetime()
    await customDialog.locator('#task-lifetime-initial').fill('30')
    await customDialog.locator('#task-lifetime-max').fill('120')
    await customDialog.locator('#task-extension-step').fill('15')
    await customDialog.locator('#task-extension-count').fill('2')
    await customDialog.getByRole('button', { name: 'Save changes' }).click()
    await expect(customDialog).toBeHidden({ timeout: 20_000 })
    const restored = await api(page, token, 'GET', `/api/v2/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
    expect((restored.instanceConfig as Record<string, unknown>).ttlSeconds).toBe(1800)

    await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/actions`, { action: 'PUBLISH' })

    // --- student: real Docker start, real extend, real cap, real reset -----
    const instancePath = `/api/v2/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}/instance`
    let instanceToken = token
    const getInstance = async (): Promise<Record<string, unknown> | null> => {
      const response = await page.request.get(`${base}${instancePath}`, {
        headers: { Authorization: `Bearer ${instanceToken}` },
      })
      if (!response.ok()) return null
      return (await response.json()) as Record<string, unknown>
    }

    const studentContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
    try {
      const studentPage = await studentContext.newPage()
      await signIn(studentPage, studentUser, studentPass)
      instanceToken = await freshToken(studentPage)
      const joinCode = String((await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/join-code`, {})).code)
      await studentPage.goto('/boards')
      await studentPage.getByRole('button', { name: 'Join a board' }).click()
      await studentPage.locator('#join-board-code').fill(joinCode)
      await studentPage.getByRole('button', { name: 'Join', exact: true }).click()
      await expect(studentPage).toHaveURL(new RegExp(`/boards/${boardUuid}$`))
      await studentPage.goto(`/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
      await studentPage.getByRole('button', { name: 'Start instance' }).click()
      await expect(studentPage.getByText('Running', { exact: true })).toBeVisible({ timeout: 90_000 })

      const started = await getInstance()
      expect(started?.status).toBe('RUNNING')
      expect(started?.canExtend).toBe(true)
      expect(started?.extensionSeconds).toBe(900)
      expect(started?.extensionsRemaining).toBe(2)
      const startedRemaining = Number(started?.remainingSeconds ?? 0)
      expect(startedRemaining).toBeGreaterThan(29 * 60)
      expect(startedRemaining).toBeLessThanOrEqual(30 * 60)

      const section = studentPage.locator('section[aria-labelledby="instance-heading"]')
      await expect(section.getByText(/Extend \+15 min/)).toBeVisible()
      await expect(section.getByText(/2 left/)).toBeVisible()

      // First real extension over HTTP.
      const firstExpiry = new Date(String(started?.expiresAt)).getTime()
      const extendResponse = studentPage.waitForResponse(
        (response) =>
          response.url().includes(`/tasks/${taskUuid}/instance/actions`) &&
          response.request().method() === 'POST',
      )
      await section.getByRole('button', { name: /Extend \+15 min/ }).click()
      expect((await extendResponse).status()).toBe(202)
      await expect.poll(async () => Number((await getInstance())?.extensionCount ?? 0), { timeout: 30_000 }).toBe(1)
      const afterFirst = await getInstance()
      expect(afterFirst?.extensionsRemaining).toBe(1)
      const secondExpiry = new Date(String(afterFirst?.expiresAt)).getTime()
      expect(secondExpiry - firstExpiry).toBe(900_000)

      // Second extension consumes the last credit, then the button is disabled
      // with the server reason (a real cap, not an intercepted response).
      await section.getByRole('button', { name: /Extend \+15 min/ }).click()
      await expect.poll(async () => Number((await getInstance())?.extensionCount ?? 0), { timeout: 30_000 }).toBe(2)
      const capped = await getInstance()
      expect(capped?.extensionsRemaining).toBe(0)
      expect(capped?.canExtend).toBe(false)
      const extendButton = section.getByRole('button', { name: /Extend \+15 min/ })
      await expect(extendButton).toBeDisabled()
      await expect(section.getByText('No extensions left')).toBeVisible()

      // Reset keeps the expiry and the consumed credits.
      const beforeResetExpiry = new Date(String((await getInstance())?.expiresAt)).getTime()
      const resetResponse = studentPage.waitForResponse(
        (response) =>
          response.url().includes(`/tasks/${taskUuid}/instance/actions`) &&
          response.request().method() === 'POST',
      )
      await section.getByRole('button', { name: 'Reset', exact: true }).click()
      expect((await resetResponse).status()).toBe(202)
      await expect.poll(async () => (await getInstance())?.status ?? 'missing', { timeout: 60_000 }).toBe('RUNNING')
      const afterReset = await getInstance()
      expect(new Date(String(afterReset?.expiresAt)).getTime()).toBe(beforeResetExpiry)
      expect(afterReset?.extensionCount).toBe(2)

      await studentPage.screenshot({ path: testInfo.outputPath('instance-lifetime.png'), fullPage: true })

      await section.getByRole('button', { name: 'Destroy', exact: true }).click()
      const destroyDialog = studentPage.getByRole('dialog')
      await destroyDialog.getByRole('button', { name: 'Destroy instance' }).click()
      await expect.poll(async () => (await getInstance())?.status ?? 'gone', { timeout: 60_000 }).toMatch(/EXPIRED|gone/)
    } finally {
      const leftover = await getInstance()
      if (leftover && leftover.status !== 'EXPIRED') {
        await page.request.post(`${base}${instancePath}/actions`, {
          headers: {
            Authorization: `Bearer ${instanceToken}`,
            'Content-Type': 'application/json',
            'Idempotency-Key': randomUUID(),
          },
          data: JSON.stringify({ action: 'DESTROY' }),
        }).catch(() => undefined)
        await expect.poll(async () => (await getInstance())?.status ?? 'gone', { timeout: 30_000 }).toMatch(/EXPIRED|gone/).catch(() => undefined)
      }
      await studentContext.close()
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
    // The login IP limiter is 10/minute. The full acceptance suite signs in
    // more distinct users than that; wait for the window instead of weakening
    // the production limiter.
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

async function createBoard(
  page: Page,
  token: string,
  input: {
    slug: string
    name: string
    participationMode: string
    registrationMode: string
    visibility: string
    teamSettings?: unknown
    startsAt?: string
    endsAt?: string
  },
): Promise<Record<string, unknown>> {
  return api(page, token, 'POST', '/api/v2/admin/boards', {
    ...input,
    description: 'Full-stage browser acceptance board',
    rating: { visibility: 'PUBLIC' },
  })
}

async function grant(page: Page, userUuid: string, boardUuid: string, permission: string) {
  await page.locator('#grant-user').fill(userUuid)
  await page.locator('#grant-permission').click()
  await page.getByRole('option', { name: permission }).click()
  await page.locator('#grant-board').fill(boardUuid)
  await page.getByRole('button', { name: 'Grant', exact: true }).click()
  await expect(page.getByText(permission).first()).toBeVisible({ timeout: 15_000 })
}

async function destroyPentestEnvironment(page: Page, token: string, boardUuid: string, moduleUuid: string) {
  const path = `/api/v2/boards/${boardUuid}/modules/${moduleUuid}/pentest/environment`
  const response = await page.request.post(`${base}${path}/actions`, {
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
      'Idempotency-Key': randomUUID(),
    },
    data: JSON.stringify({ action: 'DESTROY' }),
  })
  if (!response.ok() && response.status() !== 404 && response.status() !== 409) {
    throw new Error(`pentest destroy -> ${response.status()} ${await response.text()}`)
  }
  await expect.poll(async () => {
    const current = await page.request.get(`${base}${path}`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    if (!current.ok()) return 'gone'
    return ((await current.json()) as { status?: string }).status ?? 'unknown'
  }, { timeout: 60_000 }).toMatch(/DESTROYING|EXPIRED|gone/)
}

function psql(sql: string): string {
  return execFileSync(
    'docker',
    ['exec', '-i', pgContainer, 'psql', '-U', pgUser, '-d', pgDatabase, '-v', 'ON_ERROR_STOP=1', '-tAc', sql],
    { encoding: 'utf8' },
  )
}