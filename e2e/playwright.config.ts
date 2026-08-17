import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright config for the filex e2e suite.
 *
 * Don't run this by hand — use the harness, which starts a server on a free
 * port against a throwaway data dir and tears it down again:
 *
 *   node e2e/run.mjs local
 *   node e2e/run.mjs local --s3          # + MinIO and an s3 storage
 *
 * If you do drive Playwright directly, point it at a server you started
 * yourself and give it a deterministic admin:
 *
 *   FILEX_ADMIN_EMAIL=admin@local FILEX_ADMIN_PASSWORD=admin \
 *   FILEX_LISTEN=127.0.0.1:5212 FILEX_DATA_DIR=$(mktemp -d) filex serve
 *   E2E_BASE_URL=http://127.0.0.1:5212 pnpm test
 *
 * ⚠ 127.0.0.1, not "localhost": on Windows localhost resolves to ::1 first
 * and a server bound to 127.0.0.1 answers that with ECONNREFUSED, which reads
 * exactly like a server that failed to start.
 *
 * ⚠ There is no `FILEX_E2E_BOOTSTRAP` env var. This file and the README both
 * documented one for a long time; the binary has never read it (grep the Go
 * tree). Anyone following those instructions got a server with a random
 * first-run password and a login failure in every test.
 */
const BASE_URL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:5212';

export default defineConfig({
  testDir: './tests',
  timeout: 30_000,
  expect: { timeout: 5_000 },
  fullyParallel: false,         // serialize: shared admin user state
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,                   // single worker — backend isn't yet
                                // multi-tenant safe within a single DB

  reporter: process.env.CI
    ? [['html', { outputFolder: 'playwright-report' }], ['list']]
    : [['html', { open: 'never' }], ['list']],

  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    // Uncomment to test cross-browser:
    // { name: 'firefox', use: { ...devices['Desktop Firefox'] } },
    // { name: 'webkit',  use: { ...devices['Desktop Safari'] } },
  ],

  // Optional: spin up the docker image automatically. Disabled by default
  // because most local runs already have a server up. CI sets E2E_AUTOSTART=1.
  ...(process.env.E2E_AUTOSTART
    ? {
        webServer: {
          command:
            'docker run --rm --name filex-e2e -p 5212:5212 ' +
            '-e FILEX_ADMIN_EMAIL=admin@local -e FILEX_ADMIN_PASSWORD=admin ' +
            '-e FILEX_LISTEN=0.0.0.0:5212 ' +
            'filex:test serve',
          url: `${BASE_URL}/healthz`,
          reuseExistingServer: !process.env.CI,
          timeout: 60_000,
        },
      }
    : {}),
});
