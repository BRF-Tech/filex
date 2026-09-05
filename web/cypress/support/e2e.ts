// e2e support file — loaded before every spec. Pulls in custom
// commands and any global setup (uncaught-exception muting, etc.).
import './commands';

// Cypress treats an uncaught error in the application as a test failure. Two
// messages have to be let through, and BOTH are narrow on purpose: a blanket
// `return false` here would swallow every real exception the app throws and
// leave a suite that cannot go red.
//
//  1. vue-router's "redirected from / to /dashboard" on cold load. A navigation
//     the router itself asked for, reported as a rejected promise.
//
//  2. "ResizeObserver loop completed with undelivered notifications." — a
//     BROWSER notice, not an application error: the spec is explicit that an
//     observer which cannot finish its work in one frame defers the rest to
//     the next one, and the notice is how it says so. Nothing in the page is
//     broken and there is no `error` object behind it.
//
//     ⚠ This is why narrow/drawer coverage did not exist. The explorer watches
//     its own container with a ResizeObserver to pick narrow mode (< 560px), so
//     `cy.viewport(390, 844)` triggers exactly this notice — and Cypress failed
//     the test inside its `before each` hook, before one assertion had run.
//     Measured 2026-09-05: 4 of 12 cases in 14-explorer-sidenav skipped for
//     this and nothing else.
Cypress.on('uncaught:exception', (err) => {
  if (/redirected from|Avoided redundant navigation/i.test(err.message)) {
    return false;
  }
  if (/ResizeObserver loop/i.test(err.message)) {
    return false;
  }
  return undefined;
});

/**
 * The desktop-app / PWA install banner is dismissed before every page load.
 *
 * ⚠ Not cosmetic. The banner's wrapper is `fixed inset-x-0 bottom-0 z-40` with
 * NO `pointer-events-none` (its neighbour PendingOpsTray has one), so the full
 * width of the bottom strip is a hit target — including the 256px the admin
 * sidebar occupies. Measured 2026-09-05 at the configured 1440x900 viewport:
 * `document.elementFromPoint()` over each sidebar link returns the banner's
 * wrapper for every destination below "API / MCP", i.e. Settings, Branding,
 * Protection, External, Replication, Queue, Notifications, Webhooks, Plugins,
 * Audit, Updates and About — eleven links Cypress correctly refuses to click,
 * and that a person at that window size cannot click either without dismissing
 * the banner first.
 *
 * It never showed up before because the suite ran against fm.example.com in a
 * browser where the banner had long been dismissed, and because nothing ran it
 * automatically. It cost 13-navigation-ui seven skipped cases and 20-dashboard
 * its whole file.
 *
 * This is the product's own dismissal key (`useInstallPrompt.ts`), not a test
 * hack: the suite is saying "this visitor already said no thanks". The banner
 * itself is measured by 90-pwa-install, which opts out with
 * `Cypress.env('KEEP_INSTALL_BANNER', true)`.
 *
 * ⚠ The overlap is a real UI bug and it is NOT fixed here — see the report /
 * `web/cypress/README.md`. Suppressing it in the suite keeps every other spec
 * measuring its own subject instead of measuring this one.
 */
const INSTALL_DISMISS_KEY = 'filex.installPrompt.dismissed';

/**
 * The explorer's onboarding tour is marked "already seen" before every page
 * load, for the same reason and with the same product key (`FileExplorer.vue`,
 * wiring:c4).
 *
 * ⚠ It is worse than the banner, because it is a RACE. The tour auto-starts on
 * a 900ms timer after mount whenever `filex.tourDone` is unset, and Cypress
 * clears localStorage between tests — so every explorer case is a coin flip on
 * whether it finishes its work before a modal `role="dialog"` covers the panel.
 * Measured 2026-09-05: the same 16-explorer-connections case passed in one full
 * run and failed in the next with `<div class="fe-tour" …> is covering this
 * element`, with no change in between. A suite that answers differently twice
 * for the same commit cannot be a gate.
 *
 * The tour itself belongs in a spec of its own (it has none today — listed as
 * uncovered), and that spec would clear this key the way 90-pwa-install clears
 * the banner's.
 */
const TOUR_DONE_KEY = 'filex.tourDone';

Cypress.on('window:before:load', (win) => {
  try {
    if (!Cypress.env('KEEP_INSTALL_BANNER')) {
      win.localStorage.setItem(INSTALL_DISMISS_KEY, '1');
    }
    if (!Cypress.env('KEEP_ONBOARDING_TOUR')) {
      win.localStorage.setItem(TOUR_DONE_KEY, '1');
    }
  } catch {
    /* private mode / storage blocked — the overlays just stay up */
  }
});

/**
 * The admin SPA's service worker is served empty, so it never precaches.
 *
 * ⚠ This is the single biggest source of noise the suite had, and it is
 * measured, not guessed. `useInstallPrompt` calls `useRegisterSW` on EVERY
 * admin page, so every page load installs the Workbox worker — and Cypress
 * clears storage between tests, so it reinstalls and re-downloads the whole
 * precache manifest on nearly every test. In one full run (2026-09-05, kept
 * with `--keep`): 3920 requests reached the server and **2568 of them were
 * `/admin/assets/*`** — two thirds of the traffic was the same bundle being
 * precached over and over, including every mermaid diagram chunk and every
 * syntax-highlighting language.
 *
 * While that is in flight the document's `load` event does not fire, and
 * `cy.visit` waits for `load`. The proof is in the same log: `/admin/sw.js` was
 * fetched at 05:48:18 and next at 05:49:19 — a 61-second gap, which is exactly
 * the `cy.visit` that failed with "Timed out after waiting 60000ms for your
 * remote page to load". Seven such 60-second stalls in the pre-change baseline
 * run, one in three runs after the real assertion failures were fixed: a flake
 * that costs a minute each time and blames the wrong thing.
 *
 * An empty worker still REGISTERS (so 90-pwa-install's scope assertion is
 * unaffected) but has no fetch handler and no precache, so it never intercepts
 * a navigation. A spec that wants the real one sets
 * `Cypress.env('KEEP_SERVICE_WORKER', true)`.
 */
beforeEach(() => {
  if (Cypress.env('KEEP_SERVICE_WORKER')) return;
  cy.intercept(
    { method: 'GET', url: '**/sw.js' },
    {
      statusCode: 200,
      headers: { 'content-type': 'application/javascript; charset=utf-8' },
      body: '/* service worker neutralised for tests — cypress/support/e2e.ts */',
    },
  );
});
