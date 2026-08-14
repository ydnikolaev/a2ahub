import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  // The quality matrix deliberately reuses one system Chrome process at a
  // time. Spawning a browser per route in parallel is both noisier than the
  // product workload and unreliable in sandboxed CI/desktop environments.
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? 'line' : 'list',
  use: {
    baseURL: 'http://127.0.0.1:4324',
    headless: true,
    viewport: { width: 1280, height: 900 },
    // CI installs Playwright's pinned Chromium. Developer machines use the
    // system Chrome promised above, so a package update does not require a
    // second 200+ MiB browser download before the local quality lane can run.
    channel: process.env.CI ? undefined : 'chrome',
  },
  webServer: {
    command: 'npm run preview -- --host 127.0.0.1 --port 4324',
    url: 'http://127.0.0.1:4324/',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
