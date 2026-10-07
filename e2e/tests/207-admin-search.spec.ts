/**
 * 207 - the admin panel's search (task #168, docs/ADMIN-PANEL.md → Search).
 *
 * The owner's request (2026-10-06): the top bar's search was a button that
 * opened the file search page, and a phone had none at all. Now one search
 * finds a page, a setting, a person, a group, an API key, an app, a storage,
 * a share and - on demand - a file; a box on a wide screen, a button and a
 * layer over the window on a phone; Ctrl+K opens it; the recent searches are
 * kept on the server, so they follow the person to another browser.
 *
 * Measured in a real browser because happy-dom lays nothing out: that the
 * phone's layer really covers the window and nothing scrolls sideways, that
 * the keyboard really moves and opens, and that a chosen row lands on its
 * page. Rows are asked for by their `data-id`, not their words, so the spec
 * holds in whichever language an earlier spec left the account.
 */
import { test, expect, type Page } from '@playwright/test';
import { ADMIN_EMAIL, loginAs } from '../helpers/auth';

test.describe.configure({ mode: 'serial' });

/** One admin layout on the page (App.vue cross-fades two for 120 ms, lesson #994). */
async function settled(page: Page) {
  await expect(page.getByTestId('mega-menu')).toHaveCount(1);
}

async function openPanel(page: Page) {
  await loginAs(page);
  await page.goto('/admin/dashboard');
  await expect(page.getByTestId('account-menu')).toBeVisible({ timeout: 20_000 });
  await settled(page);
}

/** The row under the arrow keys. */
const active = (page: Page) => page.locator('[data-testid="panel-search"] [role="option"][aria-selected="true"]');

test.describe('Admin search - a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test('the search button opens a layer over the window; a page is found and opened', async ({ page }) => {
    await openPanel(page);
    const open = page.getByTestId('panel-search-open');
    await expect(open).toBeVisible();
    await expect(open).toHaveAttribute('aria-expanded', 'false');

    await open.click();
    const layer = page.getByTestId('panel-search');
    await expect(layer).toBeVisible();
    const box = await layer.boundingBox();
    // "The whole window" is the window less a scrollbar the engine draws for
    // the page behind: WebKit on Linux keeps a classic 10px one (198's note),
    // and a fixed layer's box ends where it begins - 380 of 390 there, 390 in
    // Chromium and Firefox (0.53 round). A phone's scrollbars take no room.
    const room = await page.evaluate(() => document.documentElement.clientWidth);
    expect(box!.x).toBeLessThanOrEqual(0.5);
    expect(box!.width).toBeGreaterThanOrEqual(room - 1);
    const input = page.getByTestId('panel-search-input');
    await expect(input).toBeFocused();
    await expect(input).toHaveAttribute('role', 'combobox');

    await input.fill('users');
    await expect(active(page)).toHaveAttribute('data-id', 'page:users');
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/admin\/users$/);
    await settled(page);
    await expect(page.getByTestId('panel-search')).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'nothing scrolls sideways').toBe(true);
  });

  test('a prefix is a tap away; the back button closes the layer and gives focus back', async ({ page }) => {
    await openPanel(page);
    await page.getByTestId('panel-search-open').click();
    await page.getByTestId('panel-search-prefix-setting').click();
    const input = page.getByTestId('panel-search-input');
    await expect(input).toHaveValue('setting:');
    await input.pressSequentially('2fa');
    await expect(active(page)).toHaveAttribute('data-id', 'setting:require-2fa');
    await expect(page.locator('[data-testid="panel-search"] [role="group"]:not([data-testid="panel-search-group-setting"])')).toHaveCount(0);

    await page.getByTestId('panel-search-close').click();
    await expect(page.getByTestId('panel-search')).toHaveCount(0);
    await expect(page.getByTestId('panel-search-open')).toBeFocused();
  });
});

test.describe('Admin search - a wide screen', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('Ctrl+K opens it; "apps" finds the Apps page first and Enter opens it', async ({ page }) => {
    await openPanel(page);
    await page.keyboard.press('Control+k');
    const input = page.getByTestId('panel-search-input');
    await expect(input).toBeFocused();
    // The box's panel hangs under the BOX, flush with its end edge, and stays
    // inside the window - core lib/anchoredPanel's one rule for the top bar's
    // panels (6px under the button), the bell's and the account menu's too.
    // ⚠ Not under the BAR: the box sits inside the 56px bar, so its panel
    // starts above the bar's bottom edge (51 against 56, measured in the 0.53
    // round), as the bell's does.
    const panel = await page.getByTestId('panel-search').boundingBox();
    const box = await page.getByTestId('panel-search-open').boundingBox();
    expect(panel!.y).toBeGreaterThanOrEqual(box!.y + box!.height);
    expect(Math.abs(panel!.x + panel!.width - (box!.x + box!.width))).toBeLessThanOrEqual(1.5);
    expect(panel!.x).toBeGreaterThanOrEqual(0);
    expect(panel!.x + panel!.width).toBeLessThanOrEqual(1280);

    await input.fill('apps');
    await expect(active(page)).toHaveAttribute('data-id', 'tab:plugins:apps');
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/admin\/plugins\?tab=apps$/);
  });

  test('"ldap" finds Identity providers; a person is found by the server; Esc closes', async ({ page }) => {
    await openPanel(page);
    await page.getByTestId('panel-search-open').click();
    const input = page.getByTestId('panel-search-input');
    await input.fill('ldap');
    await expect(active(page)).toHaveAttribute('data-id', 'page:auth-providers');

    await input.fill(ADMIN_EMAIL);
    await expect(page.getByTestId('panel-search-row-user').first()).toBeVisible({ timeout: 10_000 });

    await page.keyboard.press('Escape');
    await expect(page.getByTestId('panel-search')).toHaveCount(0);
    await expect(page.getByTestId('panel-search-open')).toBeFocused();
  });

  test('a query offers "search files"; `file:` keeps files alone', async ({ page }) => {
    await openPanel(page);
    await page.getByTestId('panel-search-open').click();
    const input = page.getByTestId('panel-search-input');
    await input.fill('convert');
    const all = page.getByTestId('panel-search-files-all');
    await expect(all).toBeVisible({ timeout: 10_000 });
    await expect(all).toContainText('convert');
    await all.click();
    await expect(input).toHaveValue('file:convert');
    await expect(page.getByTestId('panel-search-prefix-file')).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('[data-testid="panel-search"] [role="group"]:not([data-testid="panel-search-group-file"])')).toHaveCount(0);
  });

  test('the recent searches follow the person to another browser, and can be removed there', async ({ page, browser }) => {
    await openPanel(page);
    // A known start: this person's list emptied.
    expect((await page.request.delete('/api/admin/panel-search/recent')).ok()).toBe(true);

    await page.getByTestId('panel-search-open').click();
    const input = page.getByTestId('panel-search-input');
    await input.fill('grou');
    await expect(active(page)).toHaveAttribute('data-id', 'page:groups');
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/admin\/groups$/);

    const other = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    try {
      const there = await other.newPage();
      await openPanel(there);
      await there.getByTestId('panel-search-open').click();
      const rows = there.getByTestId('panel-search-recent');
      await expect(rows).toHaveCount(1);
      await expect(rows.first()).toHaveText('grou');
      await there.getByTestId('panel-search-recent-remove').first().click();
      await expect(rows).toHaveCount(0);
    } finally {
      await other.close();
    }
  });
});
