import { defineConfig } from 'cypress';

/**
 * filex admin Cypress config.
 *
 * ⚠ The default target is a LOCAL instance, never a live host.
 *
 * It used to default to `https://fm.example.com` — the production deployment. That
 * one line is why this suite could not be automated: a run needed a secret
 * nobody could commit, it could not be pointed at the build under review, and
 * every red result had two possible causes. A suite aimed at production does
 * not answer "is this build good?", it answers "is production up?".
 *
 * So the hermetic run is the default and the live run is the opt-in:
 *
 *   node e2e/run.mjs cypress          boots a throwaway instance, seeds an
 *                                     admin and a storage, runs every spec
 *   pnpm --filter @brftech/filex-admin cy:run
 *                                     against an instance you already have
 *                                     on 127.0.0.1:5212
 *   CYPRESS_BASE_URL=https://…        explicit, with its own credentials,
 *   CYPRESS_ADMIN_PASSWORD=… cy:run   and never in CI
 *
 * ⚠ `127.0.0.1`, not `localhost`: on Windows `localhost` resolves to `::1`
 * first and a server bound to 127.0.0.1 answers that with ECONNREFUSED, which
 * is indistinguishable from a server that never started (e2e/README.md).
 *
 * Credentials come from env vars so no secret lives in the repo. The defaults
 * are the harness's deterministic throwaway admin — `admin@local` / `admin` —
 * an account that only exists on an instance this repo started for a test run.
 */
export default defineConfig({
  e2e: {
    baseUrl: process.env.CYPRESS_BASE_URL ?? 'http://127.0.0.1:5212',
    specPattern: 'cypress/e2e/**/*.cy.ts',
    supportFile: 'cypress/support/e2e.ts',
    fixturesFolder: 'cypress/fixtures',
    // On in CI: a failed assertion in a browser is far cheaper to read as
    // twenty seconds of video than as a stack trace. The workflow uploads
    // both this and the screenshots as artifacts.
    video: process.env.CI === 'true',
    videosFolder: 'cypress/videos',
    screenshotOnRunFailure: true,
    screenshotsFolder: 'cypress/screenshots',
    defaultCommandTimeout: 8000,
    requestTimeout: 15000,
    viewportWidth: 1440,
    viewportHeight: 900,
    // ⚠ Off, deliberately. Cypress retries a failed test twice in `cypress
    // run` by default, which turns a flaky test green and hides exactly the
    // regressions this suite is being automated to catch. A test that only
    // passes on the third attempt is a bug report, not a pass.
    retries: { runMode: 0, openMode: 0 },
    env: {
      ADMIN_EMAIL: process.env.CYPRESS_ADMIN_EMAIL ?? 'admin@local',
      ADMIN_PASSWORD: process.env.CYPRESS_ADMIN_PASSWORD ?? 'admin',
      // Set by `e2e/run.mjs cypress`. Specs that need a storage prefer this
      // one over "whatever the instance happens to list first".
      SEEDED_STORAGE: process.env.CYPRESS_SEEDED_STORAGE ?? '',
    },
  },
});
