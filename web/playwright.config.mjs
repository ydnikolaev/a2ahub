import { defineConfig } from '@playwright/test';

// a2ahub's registered port block is 5500-5599 (~/Developer/_ops/registry/ports.yaml).
// Inside the host queue the port derives from the run's slot, so two worktrees
// running this suite at once do not meet on one preview server; and a server
// already on the port is never adopted there, because it may be another run's.
const slot = Number(process.env.HEAVY_RUN_SLOT);
const port = Number.isInteger(slot) && slot > 0 ? 5520 + (slot % 60) : 5524;
const origin = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: './tests',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  // The quality matrix deliberately reuses one browser process at a
  // time. Spawning a browser per route in parallel is both noisier than the
  // product workload and unreliable in sandboxed CI/desktop environments.
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? 'line' : 'list',
  use: {
    baseURL: origin,
    headless: true,
    viewport: { width: 1280, height: 900 },
    // Playwright's pinned Chromium everywhere (`npx playwright install
    // chromium` once per machine). The system Chrome left an APFS code-sign
    // clone in $TMPDIR for every launch, and a killed run never removed it.
  },
  webServer: {
    // --ignore-lock keeps the server in the foreground, owned by this run.
    // Astro 7.4 backgrounds `astro preview` when it detects a coding agent and
    // refuses a second instance while that background one holds the lock.
    command: `npm run preview -- --host 127.0.0.1 --port ${port} --ignore-lock`,
    url: `${origin}/`,
    reuseExistingServer: !process.env.CI && !process.env.HEAVY_RUN_SLOT,
    timeout: 120_000,
  },
});
