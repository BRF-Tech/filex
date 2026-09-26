/**
 * 135 — which filex this is, where a person finds it.
 *
 * Burak, 2026-09-24: the version should be in the account dropdown or in user
 * settings — somewhere a PERSON can find it. It was on the sign-in page and on
 * the administrators' About page only, so somebody already signed in, and not
 * an administrator, had no way to answer "which version are you on?".
 *
 * Measured against the server's own answer (`/api/files/capabilities` →
 * `version`), in the three places the line is drawn: the admin panel's
 * account menu, the explorer's avatar menu, the user settings dialog. The
 * words are the product's name and that string — `filex 0.1.0-dev` on this
 * development build, which is the honest answer — and no catalogue key.
 */
import { test, expect } from '@playwright/test';
import { loginAs } from '../helpers/auth';

test.describe('The version is where a person can find it', () => {
  test.use({ serviceWorkers: 'block' });

  let expected = '';
  test.beforeAll(async ({ request }) => {
    const caps = await (await request.get('/api/files/capabilities')).json();
    expect(typeof caps.version === 'string' && caps.version !== '', 'the server reports its version').toBe(true);
    // The release only: the server adds the commit and build time in brackets.
    expected = `filex ${String(caps.version).split(' (')[0]}`;
  });

  test('at the foot of the admin panel’s account menu', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/dashboard');
    await page.getByTestId('account-menu').click();
    const line = page.getByTestId('product-version');
    await expect(line).toHaveText(expected);
    // …as the LAST thing in the menu: a line, not one of the rows.
    const last = await line.evaluate((el) => {
      const menu = el.closest('[role="menu"]');
      const all = menu ? [...menu.querySelectorAll('*')].filter((n) => n.children.length === 0 && (n.textContent ?? '').trim()) : [];
      return all.length ? all[all.length - 1].textContent?.trim() : '';
    });
    expect(last, 'the version closes the menu').toBe(expected);
  });

  test('at the foot of the explorer’s avatar menu', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/explore');
    await page.getByTestId('explore-account').first().click();
    const menu = page.getByTestId('explore-account-menu');
    await expect(menu).toBeVisible();
    await expect(menu.getByTestId('product-version')).toHaveText(expected);
  });

  test('in the head of the user settings dialog', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/dashboard?settings=1');
    const dialog = page.getByTestId('user-settings-dialog');
    await expect(dialog).toBeVisible({ timeout: 15_000 });
    await expect(dialog.locator('.fx-us__head').getByTestId('product-version')).toHaveText(expected);
  });

  /* ⚠ A release build's REAL string carries the full commit and the build
     time: `v0.46.0 (<40 hex digits>, <timestamp>)`. Printed whole it gave the
     explorer's avatar menu a sideways scroll bar and ran off the About card
     (Burak, 2026-09-26). This development build has no commit, so the answer
     is given that shape here. */
  const REAL = 'v0.46.0 (a2d7e34d1971707c638a5a44756685f1cd010bd6, 2026-09-26T03:41:30Z)';
  async function releaseShapedVersion(page: import('@playwright/test').Page) {
    // The admin app reads /api/capabilities, the explorer /api/files/capabilities.
    await page.route(/\/api\/(files\/)?capabilities(\?|$)/, async (route) => {
      const res = await route.fetch();
      await route.fulfill({ response: res, json: { ...(await res.json()), version: REAL } });
    });
  }
  const scrollsSideways = (el: Element) => el.scrollWidth > el.clientWidth + 1;

  test('a release build’s menus say the release only, and do not scroll sideways', async ({ page }) => {
    await releaseShapedVersion(page);
    await loginAs(page);
    await page.goto('/admin/explore');
    await page.getByTestId('explore-account').first().click();
    const menu = page.getByTestId('explore-account-menu');
    await expect(menu.getByTestId('product-version')).toHaveText('filex v0.46.0');
    expect(await menu.evaluate(scrollsSideways), 'the avatar menu scrolls sideways').toBe(false);

    await page.goto('/admin/dashboard');
    await page.getByTestId('account-menu').click();
    const line = page.getByTestId('product-version');
    await expect(line).toHaveText('filex v0.46.0');
    expect(await line.evaluate((el) => el.closest('[role="menu"]')!.scrollWidth <= el.closest('[role="menu"]')!.clientWidth + 1)).toBe(true);
  });

  test('the About page shows the release, and the commit short', async ({ page }) => {
    await releaseShapedVersion(page);
    await loginAs(page);
    await page.goto('/admin/about');
    await expect(page.getByTestId('about-version')).toHaveText('v0.46.0');
    await expect(page.getByTestId('about-build')).toHaveText('a2d7e34 · 2026-09-26');
    await expect(page.locator('main')).not.toContainText('a2d7e34d1971707c638a5a44756685f1cd010bd6');
  });
});
