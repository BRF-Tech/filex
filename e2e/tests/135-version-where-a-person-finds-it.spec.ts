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
    expected = `filex ${caps.version}`;
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
});
