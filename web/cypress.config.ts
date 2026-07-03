import { defineConfig } from 'cypress';

/**
 * filex admin Cypress config.
 *
 * Default base URL is fm.example.com — the live production deployment.
 * Override with CYPRESS_BASE_URL or `--config baseUrl=<url>` for a
 * local dev server (Vite at 5173) or a staging instance.
 *
 * Credentials come from env vars (CYPRESS_ADMIN_EMAIL +
 * CYPRESS_ADMIN_PASSWORD) so the test suite stays free of secrets.
 * Defaults match the fm.example.com `admin@local` account whose password
 * the harness keeps in memory/filex_admin_creds.md.
 */
export default defineConfig({
  e2e: {
    baseUrl: process.env.CYPRESS_BASE_URL ?? 'https://fm.example.com',
    specPattern: 'cypress/e2e/**/*.cy.ts',
    supportFile: 'cypress/support/e2e.ts',
    fixturesFolder: 'cypress/fixtures',
    video: false,
    screenshotOnRunFailure: true,
    defaultCommandTimeout: 8000,
    requestTimeout: 15000,
    viewportWidth: 1440,
    viewportHeight: 900,
    env: {
      ADMIN_EMAIL: process.env.CYPRESS_ADMIN_EMAIL ?? 'admin@local',
      ADMIN_PASSWORD: process.env.CYPRESS_ADMIN_PASSWORD ?? '',
    },
  },
});
