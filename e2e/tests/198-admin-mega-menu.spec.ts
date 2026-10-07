/**
 * 198 - the admin panel's mega menu (GitHub #82, 0.51.0).
 *
 * The owner's shape (2026-10-03): the dashboard as a plain link, then three
 * top entries - Files & storage, People & security, System - each opening a
 * panel whose sections are columns, every page with a short line under it.
 * On a phone, a drawer with the same pages as a headed list, all open.
 * Addresses unchanged; opens on a click, never on hover.
 *
 * Measured in a real browser because the happy-dom tests lay nothing out:
 * that the panel is actually on screen under the bar and its pages can be
 * clicked, that focus really moves, and that the drawer opens over a phone
 * page and closes when a page is chosen.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';

/** Every page an administrator of a single-tenant install is offered, by address. */
const EVERY_PAGE = [
  '/admin/explore', '/admin/files', '/admin/shares', '/admin/trash', '/admin/tagged', '/admin/duplicates', '/admin/search',
  '/admin/storages', '/admin/connections', '/admin/sync', '/admin/replica', '/admin/usage',
  '/admin/users', '/admin/groups', '/admin/roles', '/admin/grants',
  '/admin/auth-providers', '/admin/login-security', '/admin/encryption', '/admin/protection', '/admin/api-mcp',
  '/admin/plugins', '/admin/external', '/admin/webhooks', '/admin/notifications',
  '/admin/settings', '/admin/tenancy', '/admin/branding', '/admin/appearance', '/admin/archives',
  '/admin/queue', '/admin/tools', '/admin/audit', '/admin/updates', '/admin/about',
];

/**
 * ⚠ The app root keys its view by the path (web/src/App.vue), so a navigation
 * mounts a fresh admin layout and cross-fades the two for 120ms: for that
 * moment every test id of the bar is in the document twice and a strict
 * locator throws. Wait for the old one to leave before asking anything.
 */
async function settled(page: Page) {
  await expect(page.getByTestId('mega-menu')).toHaveCount(1);
}

async function openPanel(page: Page) {
  await loginAs(page);
  await page.goto('/admin/dashboard');
  await expect(page.getByTestId('account-menu')).toBeVisible({ timeout: 20_000 });
  await settled(page);
}

test.describe('Admin mega menu - a wide screen', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('the dashboard is a link and every other entry a button; a page is two clicks away', async ({ page }) => {
    await openPanel(page);
    const nav = page.getByTestId('mega-menu');
    await expect(nav).toHaveAttribute('aria-label', /admin menu|yönetim menüsü/i);
    await expect(page.getByTestId('nav-dashboard')).toHaveAttribute('aria-current', 'page');

    const people = page.getByTestId('nav-top-people');
    await expect(people).toHaveAttribute('aria-expanded', 'false');
    await expect(page.getByTestId('nav-users')).toBeHidden();

    await people.click();
    await expect(people).toHaveAttribute('aria-expanded', 'true');
    const panel = page.getByTestId('nav-panel-people');
    await expect(panel).toBeVisible();
    // The short line under the page, and the panel really under the bar.
    await expect(page.getByTestId('nav-users').locator('.fx-mega__hint')).toBeVisible();
    const bar = await nav.locator('xpath=ancestor::header[1]').boundingBox();
    const box = await panel.boundingBox();
    expect(box!.y).toBeGreaterThanOrEqual(bar!.y + bar!.height - 1);

    await page.getByTestId('nav-users').click();
    await expect(page).toHaveURL(/\/admin\/users$/);
    await settled(page);
    await expect(panel).toBeHidden();
    await expect(people).toHaveClass(/is-current/);
    // The trail names the section, as words.
    await expect(page.getByTestId('crumb-section')).toHaveText(/people & access|kişiler ve erişim/i);
  });

  test('every page of the panel is in one of the three panels, at its old address', async ({ page }) => {
    await openPanel(page);
    const hrefs: string[] = [];
    for (const entry of ['files', 'people', 'system']) {
      await page.getByTestId(`nav-top-${entry}`).click();
      const panel = page.getByTestId(`nav-panel-${entry}`);
      await expect(panel).toBeVisible();
      hrefs.push(...(await panel.locator('a.fx-mega__item').evaluateAll((as) => as.map((a) => new URL((a as HTMLAnchorElement).href).pathname))));
    }
    // An installed app's screen is a row too (the Apps section) - whether
    // one is installed depends on what ran before; the fixed pages do not.
    expect(hrefs.filter((h) => !h.startsWith('/admin/apps/')).sort()).toEqual([...EVERY_PAGE].sort());
  });

  test('a page opened by its address is marked in the menu, its entry too', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/login-security');
    await expect(page.getByTestId('account-menu')).toBeVisible({ timeout: 20_000 });
    await settled(page);
    await expect(page.getByTestId('nav-top-people')).toHaveClass(/is-current/);
    await page.getByTestId('nav-top-people').click();
    await expect(page.getByTestId('nav-login-security')).toHaveAttribute('aria-current', 'page');
  });

  test('the keyboard: ↓ enters, ↓ walks, Esc returns to the button, ← / → along the bar', async ({ page }) => {
    await openPanel(page);
    const system = page.getByTestId('nav-top-system');
    await system.focus();
    await page.keyboard.press('ArrowDown');
    await expect(page.getByTestId('nav-panel-system')).toBeVisible();
    await expect(page.getByTestId('nav-plugins')).toBeFocused();
    await page.keyboard.press('ArrowDown');
    await expect(page.getByTestId('nav-external')).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('nav-panel-system')).toBeHidden();
    await expect(system).toBeFocused();
    await page.keyboard.press('ArrowLeft');
    await expect(page.getByTestId('nav-top-people')).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('nav-panel-people')).toBeVisible();
  });

  test('hovering does not open a panel; a click outside closes one', async ({ page }) => {
    await openPanel(page);
    await page.getByTestId('nav-top-files').hover();
    await page.waitForTimeout(400);
    await expect(page.getByTestId('nav-panel-files')).toBeHidden();
    await page.getByTestId('nav-top-files').click();
    await expect(page.getByTestId('nav-panel-files')).toBeVisible();
    // The footer's padding: nothing to press there but the page itself.
    await page.locator('footer').last().click({ position: { x: 2, y: 2 } });
    await expect(page.getByTestId('nav-panel-files')).toBeHidden();
  });
});

test.describe('Admin mega menu - the narrowest wide screen', () => {
  test.use({ viewport: { width: 1024, height: 768 } });

  test('at 1024px the bar fits beside the search, the quota, the bell and the account, and every panel stays on screen', async ({ page }) => {
    await openPanel(page);
    // ⚠ "1024px" is the media query's width (AdminLayout's `(min-width:
    // 1024px)`), not the window's. WebKit on Linux draws a classic scrollbar
    // (10px in the e2e image) and leaves it out of the width the query sees:
    // a 1024px window there is 1014px to the query and gets the phone's
    // drawer, measured 3/3 (Chromium and Firefox draw none and see 1024).
    // So the window is widened by the scrollbar the engine draws: the test
    // stays at the narrowest width that gets the bar, on every engine.
    const scrollbar = await page.evaluate(() => window.innerWidth - document.documentElement.clientWidth);
    if (scrollbar > 0 && !(await page.evaluate(() => window.matchMedia('(min-width: 1024px)').matches))) {
      await page.setViewportSize({ width: 1024 + scrollbar, height: 768 });
      await expect(page.getByTestId('nav-drawer-toggle')).toHaveCount(0);
      await settled(page);
    }
    const width = page.viewportSize()!.width;
    const header = page.getByTestId('mega-menu').locator('xpath=ancestor::header[1]');
    const fits = await header.evaluate((h) => h.scrollWidth <= h.clientWidth);
    expect(fits, 'the top bar overflows').toBe(true);
    for (const entry of ['files', 'people', 'system']) {
      await page.getByTestId(`nav-top-${entry}`).click();
      const box = await page.getByTestId(`nav-panel-${entry}`).boundingBox();
      expect(box!.x, entry).toBeGreaterThanOrEqual(0);
      expect(box!.x + box!.width, entry).toBeLessThanOrEqual(width);
    }
  });
});

test.describe('Admin mega menu - a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test('the menu button opens a drawer of headed lists; a page is the second tap', async ({ page }) => {
    await openPanel(page);
    // No bar on a phone: the drawer holds the menu.
    await expect(page.getByTestId('mega-menu')).toHaveCount(1);
    const toggle = page.getByTestId('nav-drawer-toggle');
    await expect(toggle).toHaveAttribute('aria-label', /^(menu|menü)$/i);
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await toggle.click();
    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    const drawer = page.getByTestId('nav-drawer');
    await expect(drawer).toBeVisible();
    // Every group open: no button between the reader and a page.
    await expect(drawer.locator('[aria-expanded]')).toHaveCount(0);
    await expect(drawer.getByTestId('nav-group-people')).toBeVisible();
    await expect(drawer.getByTestId('nav-audit')).toBeAttached();

    await drawer.getByTestId('nav-users').click();
    await expect(page).toHaveURL(/\/admin\/users$/);
    await settled(page);
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    // Closed is out of the way: off screen, and out of the tab order.
    await expect(drawer).toHaveAttribute('inert', /.*/);
  });
});
