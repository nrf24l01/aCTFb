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
    await expect(page.getByText('enabled', { exact: true }).first()).toBeVisible()

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
    await expect(page.getByText('disabled', { exact: true }).first()).toBeVisible()
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
    await expect(page.getByRole('row', { name: /Example Trusted/ })).toContainText('enabled')

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
      name: 'Manual Module', type: 'TASKS', allowedTaskTypes: ['FLAG', 'MANUAL'], enabled: true,
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

    const studentContext = await browser.newContext({ viewport: { width: 390, height: 844 } })
    try {
      const studentPage = await studentContext.newPage()
      await signIn(studentPage, studentUser, studentPass)
      // A visible but unjoined TEAM board must offer self-join on the board
      // page. Only after the real join may the team panel mount.
      await studentPage.goto(`/boards/${boardUuid}`)
      await expect(studentPage.getByRole('button', { name: 'Join this board' })).toBeVisible()
      await studentPage.getByRole('button', { name: 'Join this board' }).click()
      await expect(studentPage.getByText('Joined: yes')).toBeVisible({ timeout: 15_000 })
      await studentPage.locator('#team-create-name').fill('E2E Team')
      await studentPage.getByRole('button', { name: 'Create team' }).click()
      await expect(studentPage.getByText('E2E Team').first()).toBeVisible({ timeout: 15_000 })

      await studentPage.goto(`/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
      await studentPage.getByLabel('Choose submission files').setInputFiles({
        name: 'clean.txt', mimeType: 'text/plain', buffer: Buffer.from('clean manual text file\n'),
      })
      await expect(studentPage.getByText('READY', { exact: true }).first()).toBeVisible({ timeout: 30_000 })
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

    const studentContext = await browser.newContext({ viewport: { width: 1280, height: 900 } })
    let studentToken = ''
    let environmentStarted = false
    try {
      const studentPage = await studentContext.newPage()
      await signIn(studentPage, studentUser, studentPass)
      await studentPage.goto(`/boards/${boardUuid}`)
      await studentPage.getByRole('button', { name: 'Join this board' }).click()
      await expect(studentPage.getByText('Joined: yes')).toBeVisible({ timeout: 15_000 })
      await studentPage.getByRole('link', { name: /Pentest Lab Module/ }).click()
      await expect(studentPage.getByRole('link', { name: 'Open Pentest lab' })).toBeVisible()
      await studentPage.getByRole('link', { name: 'Open Pentest lab' }).click()
      await expect(studentPage).toHaveURL(new RegExp(`/boards/${boardUuid}/modules/${pentestModuleUuid}/pentest$`))
      await studentPage.getByRole('button', { name: 'Start environment' }).click()
      await expect(studentPage.getByText('RUNNING', { exact: true })).toBeVisible({ timeout: 90_000 })
      environmentStarted = true
      studentToken = await freshToken(studentPage)
      const environment = await api(studentPage, studentToken, 'GET',
        `/api/v2/boards/${boardUuid}/modules/${pentestModuleUuid}/pentest/environment`)
      expect(new Date(environment.expiresAt as string).getTime()).toBe(new Date(boardEndsAt).getTime())

      await studentPage.getByLabel('Choose submission files').setInputFiles({
        name: 'pentest-report.txt', mimeType: 'text/plain', buffer: Buffer.from('durable pentest report file\n'),
      })
      await expect(studentPage.getByText('READY', { exact: true }).first()).toBeVisible({ timeout: 30_000 })
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
      name: 'Instance Module', type: 'TASKS', allowedTaskTypes: ['FLAG', 'INSTANCE'], enabled: true,
    })
    const moduleUuid = module.uuid as string
    await api(page, token, 'POST', `/api/v2/admin/boards/${boardUuid}/modules/${moduleUuid}/tasks`, {
      title: 'Publishable flag', type: 'FLAG', category: 'web', description: 'keeps the board publishable',
      points: 10, visible: true, submission: { mode: 'TEXT', checker: 'AUTOMATIC' },
      acceptedAnswers: ['flag{publish}'], instance: { enabled: false },
    })

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
      await studentPage.goto('/boards')
      await studentPage.locator('#join-board-uuid').fill(boardUuid)
      await studentPage.getByRole('button', { name: 'Join board' }).click()
      await expect(studentPage).toHaveURL(new RegExp(`/boards/${boardUuid}$`))
      await studentPage.goto(`/boards/${boardUuid}/modules/${moduleUuid}/tasks/${taskUuid}`)
      await studentPage.getByRole('button', { name: 'Start instance' }).click()
      await expect(studentPage.getByText('RUNNING', { exact: true })).toBeVisible({ timeout: 90_000 })

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