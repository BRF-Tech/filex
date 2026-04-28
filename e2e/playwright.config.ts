import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright config for the filex e2e suite.
 *
 * Default base URL is http://localhost:5212 — the slim Docker image's
 * exposed port. Tests assume the server is already running. Spin it up
 * with:
 *
 *   docker run --rm -d --name filex-e2e -p 5212:5212 \
 *     -e FILEX_PUBLIC_URL=http://localhost:5212 \
 *     -e FILEX_E2E_BOOTSTRAP=1 \
 *     filex:test serve
 *
 * The `FILEX_E2E_BOOTSTRAP=1` env var tells the binary to seed a
 * deterministic admin user (admin@local / admin) so the tests don't
 * depend on the random first-run password.
 */
const BASE_URL = process.env.E2E_BASE_URL ?? 'http://localhost:5212';

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
            '-e FILEX_E2E_BOOTSTRAP=1 -e FILEX_LISTEN=0.0.0.0:5212 ' +
            'filex:test serve',
          url: `${BASE_URL}/healthz`,
          reuseExistingServer: !process.env.CI,
          timeout: 60_000,
        },
      }
    : {}),
});
