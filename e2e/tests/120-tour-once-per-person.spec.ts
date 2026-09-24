/**
 * 120 — the first-use tour is offered to a PERSON once, never once per mount.
 *
 * ⚠⚠ What it did (v0.43.0 QA): the tour opened ~1 s after EVERY explorer mount
 * whose browser had no `filex.tourDone`, and that flag was written only when
 * the tour was CLOSED. A second tab opened while the first still showed it got
 * it again; so did another browser and another device. A browser-automation run
 * met its card on each fresh mount, and it swallowed a click one run in two.
 *
 * Measured here, as a fresh account (no spec in the run has touched it):
 *   1. the first mount offers it;
 *   2. a SECOND TAB in the same browser, with the first tab's tour still open,
 *      does not;
 *   3. a SECOND BROWSER (empty storage) signed in as the same person does not
 *      — the account says so;
 *   4. and the account document carries it next to the person's other
 *      preferences, without having wiped them.
 *
 * ⚠ No `filex.tourDone` init script in this file, on purpose: that flag is what
 * every other spec uses to keep the tour out of its way, and it is exactly what
 * is being measured here.
 */
import { test, expect, type Browser, type Page } from '@playwright/test';
import { apiLogin, dismissInstallBanner } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';

const EMAIL = `tour-${Date.now()}@example.com`;
const PASSWORD = 'tour-once-pw-2026';
// A person with no storage at all sees "nothing has been shared with you" and
// no explorer — so no tour. One drive they can open.
const STORAGE = `e2e-tour-${Date.now()}`;

async function signIn(page: Page) {
  await dismissInstallBanner(page);
  await page.goto('/admin/login');
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(EMAIL);
  await page.getByLabel(/password|şifre/i).fill(PASSWORD);
  await page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Giriş yap', exact: true }))
    .first()
    .click();
  await page.waitForURL(/\/drive\//, { timeout: 15_000 });
}

async function freshBrowser(browser: Browser, baseURL: string | undefined) {
  const ctx = await browser.newContext({ baseURL });
  const page = await ctx.newPage();
  return { ctx, page };
}

test.describe('the tour is offered once per person', () => {
  test.beforeAll(async ({ request }) => {
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    await apiLogin(request);
    const made = await request.post('/api/admin/users', {
      data: { email: EMAIL, password: PASSWORD, role: 'user', locale: 'en' },
    });
    expect(made.ok(), await made.text()).toBe(true);
    // The person's own preference, stored BEFORE the tour is recorded: the
    // record must not erase it (PUT replaces the whole document).
    const login = await request.post('/api/auth/login', { data: { email: EMAIL, password: PASSWORD } });
    const { token } = await login.json();
    const put = await request.put('/api/me/prefs?surface=web', {
      headers: { Authorization: `Bearer ${token}` },
      data: { prefs: { density: 'compact' } },
    });
    expect(put.ok(), await put.text()).toBe(true);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('first mount offers it; a second tab and a second browser do not', async ({ browser, baseURL }) => {
    const one = await freshBrowser(browser, baseURL);
    await signIn(one.page);
    // Sign-in lands on Home, which IS the explorer (a view of it): the first
    // mount is there, and that is where the one offer is made.
    await expect(one.page.locator('.fe-tour')).toBeVisible({ timeout: 10_000 });

    // Tab 2 — the tour in tab 1 is still open, never closed.
    const tab2 = await one.ctx.newPage();
    await tab2.goto('/drive/explore');
    await expect(tab2.getByTestId('sidenav-new')).toBeVisible();
    await tab2.waitForTimeout(6_500); // past the offer delay and the account wait
    await expect(tab2.locator('.fe-tour')).toHaveCount(0);

    // Another browser: nothing in its storage — only the account can say.
    const two = await freshBrowser(browser, baseURL);
    await signIn(two.page);
    await two.page.goto('/drive/explore');
    await expect(two.page.getByTestId('sidenav-new')).toBeVisible();
    await two.page.waitForTimeout(6_500);
    await expect(two.page.locator('.fe-tour')).toHaveCount(0);

    const prefs = await (await two.page.request.get('/api/me/prefs?surface=web')).json();
    expect(prefs.prefs).toMatchObject({ tour: 'done', density: 'compact' });

    await one.ctx.close();
    await two.ctx.close();
  });
});
