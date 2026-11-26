import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  // One worker: the browser gates share one backend/Vite stack and the
  // authoring spec creates fixtures in the same database.
  workers: 1,
  timeout: 90_000,
  expect: { timeout: 10_000 },
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:5173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    launchOptions: process.env.E2E_CHROMIUM_PATH
      ? { executablePath: process.env.E2E_CHROMIUM_PATH }
      : undefined,
  },
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : {
        command: 'bun run dev --host 127.0.0.1',
        url: 'http://127.0.0.1:5173',
        reuseExistingServer: true,
        timeout: 120_000,
      },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    {
      name: 'phone',
      use: {
        // Chromium at the 375 px layout the phone toolbar contract targets.
        // `devices['iPhone 13']` would switch the project to WebKit, which the
        // CI image does not install; the layout contract is engine-independent.
        ...devices['Desktop Chrome'],
        viewport: { width: 375, height: 812 },
        deviceScaleFactor: 2,
        isMobile: true,
        hasTouch: true,
      },
    },
  ],
})