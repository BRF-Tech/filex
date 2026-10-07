/**
 * 204 - multi-tenant mode off: nothing about tenants or realms is shown
 * (task #167, docs/MULTI-TENANCY.md "Mode gating").
 *
 * The suite's server runs single-tenant: no FILEX_MULTI_TENANT, and the
 * switch on Admin → Multi-tenant mode never saved. The owner's rule for that
 * install: not a word of "tenant", "realm" or "kiracı" on the sign-in page or
 * on any page of the panel, no Tenants or My tenant in the menu, and an
 * address of a tenant page leads to the dashboard. The one exception is the
 * switch itself - the page where the mode is turned on.
 *
 * The happy-dom tests pin each screen against a capabilities answer; this
 * measures the screens a real server draws, in a real browser, after
 * whatever the earlier specs left behind.
 */
import { test, expect, type Page } from '@playwright/test';
import { dismissInstallBanner, loginAs } from '../helpers/auth';

/** The words. ⚠ No `\b`: it is an ASCII boundary, and "kiracı" ends in a letter it does not know. */
const TENANT_WORDS = /tenant|realm|kiracı/i;

/** Pages an administrator of a single-tenant install opens, by address. */
const PAGES = [
  '/admin/dashboard',
  '/admin/users',
  '/admin/groups',
  '/admin/roles',
  '/admin/grants',
  '/admin/connections',
  '/admin/auth-providers',
  '/admin/login-security',
  '/admin/encryption',
  '/admin/protection',
  '/admin/settings',
  '/admin/branding',
  '/admin/appearance',
];

/** One mega menu on the page (lesson #994: the cross-fade draws two for 120 ms). */
async function settled(page: Page) {
  await expect(page.getByTestId('mega-menu')).toHaveCount(1);
}

test.describe('Multi-tenant mode off', () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test('the server says so, and names no realm', async ({ page }) => {
    await loginAs(page);
    const caps = await (await page.request.get('/api/capabilities')).json();
    expect(caps.multi_tenant).toBe(false);
    expect(caps.realm).toBeUndefined();
    expect(caps.tenant).toBeUndefined();
  });

  // Each test has a context of its own: this one is signed out.
  test('the sign-in page has no Realm field and no word of tenants', async ({ page }) => {
    await dismissInstallBanner(page);
    await page.goto('/admin/login');
    await expect(page.getByLabel(/e-?mail|kullanıcı adı/i)).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('login-realm')).toHaveCount(0);
    expect(await page.locator('body').innerText()).not.toMatch(TENANT_WORDS);
  });

  test('the menu offers neither Tenants nor My tenant, and their addresses lead to the dashboard', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/dashboard');
    await expect(page.getByTestId('account-menu')).toBeVisible({ timeout: 20_000 });
    await settled(page);
    await page.getByTestId('nav-top-people').click();
    await expect(page.getByTestId('nav-panel-people')).toBeVisible();
    await expect(page.getByTestId('nav-users')).toBeVisible();
    await expect(page.getByTestId('nav-tenants')).toHaveCount(0);
    await expect(page.getByTestId('nav-tenant-self')).toHaveCount(0);
    expect(await page.getByTestId('nav-panel-people').innerText()).not.toMatch(TENANT_WORDS);

    for (const address of ['/admin/tenants', '/admin/my-tenant', '/admin/tenants/1']) {
      await page.goto(address);
      await expect(page).toHaveURL(/\/admin\/dashboard$/);
    }
  });

  test('no page of the panel says tenant or realm', async ({ page }) => {
    await loginAs(page);
    const said: string[] = [];
    for (const address of PAGES) {
      await page.goto(address);
      await expect(page.getByTestId('account-menu')).toBeVisible({ timeout: 20_000 });
      await settled(page);
      await page.waitForLoadState('networkidle');
      // One main region on every page. Corporate identity's preview drew the
      // public link page's card as a second <main> inside the panel's (0.53
      // round): not valid HTML, and a second landmark for a screen reader. The
      // preview mounts the shell `embedded` now (core PublicShell).
      await expect(page.locator('main'), `${address}: one main region`).toHaveCount(1);
      const text = await page.locator('main').innerText();
      const hit = text.match(TENANT_WORDS);
      if (hit) said.push(`${address}: "${text.slice(Math.max(0, (hit.index ?? 0) - 60), (hit.index ?? 0) + 60).replace(/\s+/g, ' ')}"`);
    }
    expect(said).toEqual([]);
  });

  test('the switch is there for the administrator: off, nothing pending, and a change waits for a restart', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/tenancy');
    await expect(page.getByTestId('tenancy-page')).toBeVisible({ timeout: 20_000 });
    const sw = page.getByTestId('tenancy-switch').getByRole('switch');
    await expect(sw).toHaveAttribute('aria-checked', 'false');
    await expect(page.getByTestId('tenancy-restart')).toHaveCount(0);
    await expect(page.getByTestId('tenancy-locked')).toHaveCount(0);

    // On: saved for the next start, said so; the running server stays off.
    await sw.click();
    await expect(page.getByTestId('tenancy-restart')).toBeVisible();
    await expect(sw).toHaveAttribute('aria-checked', 'true');
    const caps = await (await page.request.get('/api/capabilities')).json();
    expect(caps.multi_tenant, 'nothing changes before the restart').toBe(false);

    // Back off (no tenants: no confirmation): nothing left pending for the specs after this one.
    await sw.click();
    await expect(page.getByTestId('tenancy-restart')).toHaveCount(0);
    await expect(sw).toHaveAttribute('aria-checked', 'false');
    const state = await (await page.request.get('/api/admin/tenancy')).json();
    expect(state).toMatchObject({ in_force: false, next_start: false, restart_required: false, tenants: 0 });
  });
});
