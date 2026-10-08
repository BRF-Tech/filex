import { defineConfig, devices, type Project } from '@playwright/test';

/**
 * Playwright config for the filex e2e suite.
 *
 * Don't run this by hand — use the harness, which starts a server on a free
 * port against a throwaway data dir and tears it down again:
 *
 *   node e2e/run.mjs local
 *   node e2e/run.mjs local --s3          # + an S3 server and an s3 storage
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

/** Playwright device per engine this suite knows. */
const BROWSER_DEVICES: Record<string, string> = {
  chromium: 'Desktop Chrome',
  firefox: 'Desktop Firefox',
  webkit: 'Desktop Safari',
};

/**
 * What an engine needs on top of its device to BE that device.
 *
 * ⚠⚠ Headless Firefox on Linux has no pointing device, so it answers
 * `(hover: none)` and `(pointer: none)` — a desktop with no mouse. Every hover
 * reveal the product keeps behind `@media (hover: hover)` (the card checkbox,
 * the star, issue #26) is then simply not there, and a desktop spec fails on
 * a screen no desktop Firefox user has (measured 2026-10-01: Chromium and
 * WebKit headless both answer hover + fine; Firefox does once these two
 * capability prefs say "fine pointer that hovers", the value a desktop with a
 * mouse reports). The phone describes that need `hover: none` skip Firefox,
 * which cannot emulate a phone at all.
 *
 * ⚠ The HTTP cache in memory and no history database, as Chromium's
 * Playwright contexts have them. Firefox keeps one profile on disk for the
 * whole run (/tmp/playwright_firefoxdev_profile-*): six minutes into the
 * build host's night of 2026-10-07 its cache2 held 815 MB and Firefox had
 * written 2.2 GB; the Firefox line wrote 33-34 GB to the disk in a 0.53 full
 * run, half of what the whole run wrote, on a disk that stalls under it
 * (task #194). No spec reads either: a page's cache and its back/forward
 * history live in memory either way.
 */
const ENGINE_USE: Record<string, Project['use']> = {
  firefox: {
    launchOptions: {
      firefoxUserPrefs: {
        'ui.primaryPointerCapabilities': 6,
        'ui.allPointerCapabilities': 6,
        'browser.cache.disk.enable': false,
        'places.history.enabled': false,
      },
    },
  },
};

/**
 * The engines E2E_BROWSERS names (comma-separated), or Chromium alone.
 * ⚠ An engine name this suite does not know stops the run: a typo that
 * silently fell back to Chromium would report a cross-browser pass that
 * never happened (the rule e2e/lib/args.mjs keeps for run.mjs options).
 */
function selectedBrowsers(): string[] {
  const raw = (process.env.E2E_BROWSERS ?? '').trim();
  if (!raw) return ['chromium'];
  const names = raw.split(',').map((s) => s.trim()).filter(Boolean);
  const unknown = names.filter((n) => !BROWSER_DEVICES[n]);
  if (unknown.length) {
    throw new Error(`E2E_BROWSERS: unknown engine(s) ${unknown.join(', ')} — known: ${Object.keys(BROWSER_DEVICES).join(', ')}`);
  }
  return names;
}

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
    // Pin the mouse open gesture to 'single' for every context.
    //
    // Since v0.42.0 the default (web/src/lib/explorerConfig.ts →
    // openTriggerPref) is 'double': a single click SELECTS and a double click
    // OPENS. This suite opens files and folders with a single `.click()`
    // throughout, so under the new default those "open" steps would only select
    // the row and the following assertion would time out. Seeding the product's
    // own preference key via storageState restores one-click open for the whole
    // run — the one shared seam that reaches every spec's built-in `page`
    // fixture, including the ones that don't go through helpers/auth. Touch
    // always taps-to-open and ignores this key. The origin is BASE_URL (the
    // hermetic server's dynamic address, set by e2e/run.mjs).
    storageState: {
      cookies: [],
      origins: [
        { origin: BASE_URL, localStorage: [{ name: 'filex.openTrigger', value: 'single' }] },
      ],
    },
    // ⚠ retain-on-failure, not on-first-retry. A local run has no retries, so
    // 'on-first-retry' recorded nothing there, and on CI it records the RETRY:
    // the first attempt (the one that failed, often for a reason that does not
    // come back) left no trace at all. A blank sign-in page seen once in a
    // full run (task #81) could not be read browser-side for exactly this
    // reason: was the missing chunk requested, pending or refused?
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
  },

  // Chromium by default. E2E_BROWSERS opts other engines in for a run —
  // `E2E_BROWSERS=chromium,firefox,webkit node e2e/run.mjs local --grep "…"`
  // runs every selected spec once per engine (a project each). Opt-in, never
  // the default: the suite is calibrated on Chromium, and a spec that measures
  // something engine-specific (the single encrypted file's save path, 173)
  // says so itself. The engines must match this Playwright's revisions:
  // `cd e2e && ./node_modules/.bin/playwright install firefox webkit`.
  projects: selectedBrowsers().map((name) => ({
    name,
    use: { ...devices[BROWSER_DEVICES[name]], ...(ENGINE_USE[name] ?? {}) },
  })),

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
