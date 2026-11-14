import { defineConfig } from '@playwright/test'
import path from 'node:path'

const frontendDir = path.resolve(import.meta.dirname, '../frontend')

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  // The isolated suite signs in more than the production 10/minute auth limit
// allows; signIn() waits for the window instead of weakening the limiter.
timeout: 180_000,
  expect: { timeout: 10_000 },
  reporter: [['list'], ['html', { open: 'never', outputFolder: process.env.E2E_REPORT_DIR || 'playwright-report' }]],
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:5173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    launchOptions: process.env.E2E_CHROMIUM_PATH
      ? { executablePath: process.env.E2E_CHROMIUM_PATH }
      : undefined,
  },
  webServer: {
    command: `bun run dev -- --host 127.0.0.1 --port ${process.env.E2E_FRONTEND_PORT || '5173'}`,
    cwd: frontendDir,
    url: process.env.E2E_BASE_URL || 'http://127.0.0.1:5173',
    reuseExistingServer: true,
    timeout: 120_000,
    env: {
      ...process.env,
      VITE_DEV_PROXY_TARGET: process.env.E2E_BACKEND_URL || 'http://127.0.0.1:8080',
    },
  },
  outputDir: process.env.E2E_OUTPUT_DIR || 'test-results',
})