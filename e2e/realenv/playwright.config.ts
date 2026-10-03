import { defineConfig, devices } from '@playwright/test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

/**
 * The realenv suite (e2e/realenv/README.md): filex against real third-party
 * servers that e2e/realenv/run.sh starts in Docker. Run through that script;
 * run any other way, every spec skips and says what it needs.
 *
 * One worker, in file order: the specs share the servers of their stage and
 * change them (DNS records, a tenant's providers).
 */
const here = path.dirname(fileURLToPath(import.meta.url));
const results = process.env.REALENV_RESULTS ?? path.join(here, '.work', 'results', 'local');

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 300_000,
  expect: { timeout: 15_000 },
  outputDir: path.join(results, 'artifacts'),
  reporter: [['list'], ['json', { outputFile: path.join(results, 'report.json') }]],
  use: {
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    actionTimeout: 30_000,
    navigationTimeout: 60_000,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
