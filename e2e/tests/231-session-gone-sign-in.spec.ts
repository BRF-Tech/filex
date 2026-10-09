/**
 * 231-session-gone-sign-in - what the panel does with a 401 (web/src/lib/
 * sessionGone.ts, task #199): it asks the server whether the session is over
 * and only then goes to the sign-in page, naming the page the reader was on.
 *
 * e2e 202 ("the session gone, a second link in that tab goes into no sign-in
 * address") was red on GitHub's full matrix in Chromium (runs 37598598805,
 * 37661356185, 37702037032): the sign-in page opened with `?redirect=/home`,
 * and the store link was lost. The panel pushed to the sign-in page the
 * moment a 401 arrived, while it still believed in the session; the sign-in
 * page's guard sent that "signed-in" reader on to the start page (Home),
 * whose own 401 then named Home as the place to come back to - over the store
 * page's own sign-in, when that one had started in between.
 *
 * The two cases below take the race out of the timing:
 *
 *   1. a 401 that is not the session (the session is alive): the reader stays
 *      on the page, which says what was refused. Before the fix the panel left
 *      for Home every time;
 *   2. the session gone, with the server's answer to "who is signed in" held
 *      back until nothing else of the page is in flight: the sign-in page
 *      names the store page and no other page is on the way; after the
 *      sign-in the store page reads the link. Before the fix the panel was on
 *      Home before the answer came, every time.
 *
 * No store is needed: the session is refused before any store is asked, and
 * the first case answers the link's request itself.
 */
import { test, expect, type Page, type Request, type Route } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD, dismissInstallBanner, loginAs } from '../helpers/auth';

/** A store that is never reached (the discard port on this machine). */
const STORE = 'http://127.0.0.1:9';
const linkTo = (token: string) => `/admin/store-install#store=${encodeURIComponent(STORE)}&intent=${token}`;

/** The paths the main frame went to, from now on. */
function pathsOf(page: Page): string[] {
  const seen: string[] = [];
  page.on('framenavigated', (f) => {
    if (f === page.mainFrame()) seen.push(new URL(f.url()).pathname);
  });
  return seen;
}

/** The requests the page has in flight (sent, not yet finished or failed). */
function inFlight(page: Page): Set<Request> {
  const open = new Set<Request>();
  page.on('request', (r) => open.add(r));
  page.on('requestfinished', (r) => open.delete(r));
  page.on('requestfailed', (r) => open.delete(r));
  return open;
}

async function signInOnForm(page: Page) {
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(ADMIN_EMAIL);
  await page.getByLabel(/password|parola/i).fill(ADMIN_PASSWORD);
  await page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Oturum aç', exact: true }))
    .first()
    .click();
}

test.describe('a 401 and the sign-in page', () => {
  /* ⚠ No service worker: both cases answer or hold a request with
     page.route, and a request the panel's worker carries is not routed
     (Playwright's documented limit; 85, 97 and 132 block it for the same
     reason). The race is the panel's, not the worker's. */
  test.use({ serviceWorkers: 'block' });

  test('a 401 with the session alive leaves the reader on the page, which says what was refused', async ({ page }) => {
    await loginAs(page);
    let refused = 0;
    await page.route('**/api/admin/app-plugins/store-intent', async (route: Route) => {
      if (route.request().method() !== 'POST' || refused > 0) return route.continue();
      refused++;
      await route.fulfill({ status: 401, contentType: 'application/json', body: JSON.stringify({ error: 'unauthorized' }) });
    });
    const seen = pathsOf(page);
    await page.goto(linkTo('tok-alive-0231'));
    await expect(page.getByTestId('store-install-error')).toBeVisible({ timeout: 30_000 });
    expect(refused, 'the link was asked once, and refused').toBe(1);
    expect(new URL(page.url()).pathname).toBe('/admin/store-install');
    // ⚠ From the link's page on: Home, where loginAs left the reader, can
    // still finish a navigation of its own after loginAs returned (the
    // nightly of b5c58508 saw "/admin/home" here in all three browsers, with
    // the reader on the store page the whole time after it). What the 401
    // must not do is take the reader anywhere AFTER the link's page opened.
    const opened = seen.indexOf('/admin/store-install');
    expect(opened, 'the link opened the store page').toBeGreaterThanOrEqual(0);
    expect(
      seen.slice(opened).filter((p) => p !== '/admin/store-install'),
      'the panel went nowhere else',
    ).toEqual([]);
  });

  test('the session gone: the panel asks before it moves; the sign-in names the store page and nothing else is on the way', async ({ page }) => {
    await dismissInstallBanner(page);
    await loginAs(page);
    await page.goto('/admin/store-install');
    await expect(page.getByTestId('store-install-none')).toBeVisible();

    // The server's answer to "who is signed in", held back.
    const held: Route[] = [];
    let holding = true;
    await page.route('**/api/auth/me', (route: Route) => {
      if (holding) held.push(route);
      else void route.continue();
    });
    const open = inFlight(page);

    // The session ends behind the page's back: no cookie, no bearer.
    await page.context().clearCookies();
    await page.evaluate(() => sessionStorage.removeItem('filex.bearer'));
    const seen = pathsOf(page);
    const refused = page.waitForResponse((r) => r.url().includes('/store-intent') && r.status() === 401);
    await page.evaluate((u) => {
      window.location.href = u;
    }, linkTo('tok-gone-0231'));
    await refused;

    // Everything the page set off is over but the question held: anything the
    // panel would do before the answer, it has done.
    await expect
      .poll(() => held.length > 0 && [...open].every((r) => r.url().endsWith('/api/auth/me')), {
        message: 'only the question about the session is still in flight',
        timeout: 30_000,
      })
      .toBe(true);
    expect(
      seen.filter((p) => p !== '/admin/store-install'),
      'the panel moved before the server said the session was over',
    ).toEqual([]);

    holding = false;
    for (const r of held.splice(0)) await r.continue();

    await page.waitForURL(/\/admin\/login/, { timeout: 30_000 });
    expect(new URL(page.url()).searchParams.get('redirect'), 'the sign-in comes back to the store page').toBe('/store-install');
    expect(
      seen.filter((p) => p !== '/admin/store-install' && p !== '/admin/login'),
      'no other page on the way to the sign-in',
    ).toEqual([]);

    // After the sign-in, the store page reads the link that waited in the tab.
    const readAgain = page.waitForRequest(
      (r) => r.url().endsWith('/store-intent') && r.method() === 'POST' && (r.postData() ?? '').includes('tok-gone-0231'),
      { timeout: 30_000 },
    );
    await signInOnForm(page);
    await readAgain;
    await expect(page.getByTestId('store-install')).toBeVisible();
    expect(new URL(page.url()).pathname).toBe('/admin/store-install');
    await page.unroute('**/api/auth/me');
  });
});
