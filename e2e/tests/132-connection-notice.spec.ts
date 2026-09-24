/**
 * 132 — the server goes away, and the page says so ONCE.
 *
 * The owner, 2026-09-24: "Bir kurulum var açtık tarayıcıdan diyelim bağlantı
 * kopunca network error diye hata veriyor ki çok doğru ama bu hatadan çok
 * fazla atıyor… attığı tüm istekler için ayrı ayrı atıyor o yüzden çok fazla
 * popover çıkıyor."
 *
 * ⚠⚠ Why a browser and not a unit test. The unit tests measure the RULE
 * (web/tests/lib/connection.test.ts, web/tests/api/client.test.ts): a read is
 * folded, a write still speaks, and the notice clears on the next answer.
 * What they cannot measure is the SIZE OF THE STORM — how many calls a real
 * page fires while it is down. That number is the whole complaint, and it
 * comes from the product's own cadence (the listing, a thumbnail per row, the
 * bell's 15 s poll, every panel a click opens), not from anything a test could
 * declare. So the failed calls are COUNTED here and asserted against the
 * count of notices.
 *
 * ⚠ The server is taken away with `page.route(...).abort('failed')` rather
 * than by stopping a process or closing a port. From inside the page it is
 * the same event — a request that gets no answer — and it leaves the harness's
 * server (and every other run on this machine) alone.
 *
 * ⚠⚠ NOTHING IS RELOADED while the connection is down, and that is not
 * convenience. A cold load re-runs the router guard, whose `fetchMe` cannot
 * answer with no server, so the app treats the visit as signed-out and
 * redirects to /login — a different (and older) behaviour that has nothing to
 * do with what is measured here. The complaint is about a page that is ALREADY
 * OPEN when the connection drops, so every step below moves inside the app.
 *
 * ⚠ No new polling was added for the recovery: the notice clears on the next
 * call the page was going to make anyway, which is why this test waits for it
 * instead of pressing anything.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';

/**
 * ⚠ No service worker. The panel is a PWA, and a worker answering a cached
 * `/api/...` out of its own store would mean the page was never actually cut
 * off — the test would be measuring a cache, not an outage.
 */
test.use({ serviceWorkers: 'block' });

const NOTICE = '[data-testid="connection-notice"]';
const TOASTS = '[data-testid="toast-layer"] [role="alert"]';

/** Everything the page is saying about the connection, right now. */
async function said(page: Page) {
  return {
    notices: await page.locator(NOTICE).count(),
    toasts: await page.locator(TOASTS).count(),
  };
}

test.describe('A connection that drops is one notice, not one per request', () => {
  test('a storm of failed calls says it once, and it clears by itself', async ({ page }) => {
    // Two 15 s polls, the walk through the panel, and the wait for recovery —
    // well past the suite's 30 s default.
    test.setTimeout(180_000);

    await loginAs(page);
    /* ⚠ The dashboard, not `/admin/explore`: the drive is drawn full-bleed
       with the explorer's own slim header, and the panel's chrome — the side
       navigation this test walks and the account menu it opens — lives in
       AdminLayout. */
    await page.goto('/admin/dashboard');
    await expect(page.getByTestId('account-menu')).toBeVisible({ timeout: 20_000 });
    await expect(page.locator(NOTICE), 'a healthy page says nothing').toHaveCount(0);
    expect((await said(page)).toasts, 'and starts with a clean corner').toBe(0);

    // Count what actually fell over, so "one notice" is measured against a
    // real number rather than an assumed one.
    let failed = 0;
    page.on('requestfailed', (r) => {
      if (r.url().includes('/api/')) failed += 1;
    });

    // ── the server goes away ──────────────────────────────────────────
    await page.route('**/api/**', (route) => route.abort('failed'));

    // Move around the way somebody would while it is down — inside the app,
    // never a reload. Every screen fires its own reads and the bell keeps
    // polling underneath at the product's own 15 s cadence, unchanged.
    /* ⚠ Panel routes only. `nav-explore` leaves AdminLayout for the drive's
       full-bleed shell, and the walk would lose the navigation it is using. */
    for (const testid of ['nav-users', 'nav-shares', 'nav-storages', 'nav-settings', 'nav-dashboard']) {
      const link = page.getByTestId(testid);
      if (await link.count()) await link.first().click({ timeout: 5_000 }).catch(() => undefined);
      await page.waitForTimeout(1_500);
    }
    await page.waitForTimeout(22_000);

    const storm = await said(page);
    expect(failed, 'the page really did keep trying').toBeGreaterThan(5);
    expect(
      storm.notices,
      `${failed} failed calls must be ONE notice, not ${storm.notices}`,
    ).toBe(1);
    expect(
      storm.toasts,
      `${failed} failed calls raised ${storm.toasts} toasts — this is the pile the notice replaces`,
    ).toBe(0);
    await expect(page.locator(NOTICE)).toBeVisible();

    // ── something the PERSON did still speaks for itself ──────────────
    /* ⚠ The one thing that must NOT be folded. A write is something they are
       waiting on, and swallowing it into a generic "offline" would be worse
       than the storm. Choosing a language writes it to the account
       (PATCH /api/auth/profile) — one axios write, one message. */
    await page.getByTestId('account-menu').click();
    await page.getByTestId('account-user-settings').click();
    await page.getByTestId('user-settings-dialog').waitFor({ timeout: 15_000 });
    await page.getByTestId('user-settings-tab-preferences').click();
    await page.getByTestId('user-settings-locale-tr').click();
    await expect
      .poll(async () => (await said(page)).toasts, { timeout: 20_000 })
      .toBeGreaterThan(0);
    expect((await said(page)).notices, 'and still one notice over it').toBe(1);

    // ── the server comes back ─────────────────────────────────────────
    /* Nothing is pressed and nothing is reloaded: the notice has to come down
       on the next call the page makes on its own. */
    await page.unroute('**/api/**');
    await expect(page.locator(NOTICE), 'nobody should dismiss a stale notice').toHaveCount(0, {
      timeout: 45_000,
    });
  });
});
