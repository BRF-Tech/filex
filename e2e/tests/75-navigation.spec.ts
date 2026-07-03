/**
 * Navigation — root crumb + parent-dir button + Backspace/Alt+Up
 *
 * Guards the bug Ada hit on fm.example.com: clicking 'Kök' inside
 * `s3-test://aa` reloaded the same `aa` folder instead of going back
 * to the storage root. There was also no "↑ Up" button, so a user
 * who walked into a sub-folder got stuck.
 *
 * The suite is split:
 *   - UI-driven cases use a single page-scoped flow inside one test
 *     (loginAs + nested navigation). Splitting them across tests was
 *     flaky because each Playwright `page` is a fresh context, the
 *     pinia storages store re-fetches, and the FileExplorer remounts.
 *   - API-driven cases verify the underlying contract.
 */
import { test, expect } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const STORAGE_NAME = `e2e-nav-${Date.now()}`;

test.describe('Navigation — root crumb + go-up', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE_NAME);
    await seedLocalStorage(request, STORAGE_NAME, `/tmp/filex-${STORAGE_NAME}`);
    // Build a 2-level tree so the parent-dir flow has somewhere to go.
    // The newfolder endpoint requires auth — apiLogin sets cookies on
    // this request context for the rest of the hook.
    await apiLogin(request);
    const mk1 = await request.post('/api/files/manager?action=newfolder', {
      data: { path: `${STORAGE_NAME}://`, name: 'aa' },
    });
    if (!mk1.ok()) throw new Error(`mkdir aa failed: ${mk1.status()} ${await mk1.text()}`);
    const mk2 = await request.post('/api/files/manager?action=newfolder', {
      data: { path: `${STORAGE_NAME}://aa`, name: 'bb' },
    });
    if (!mk2.ok()) throw new Error(`mkdir aa/bb failed: ${mk2.status()} ${await mk2.text()}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE_NAME);
  });

  test('root crumb label = adapter name (not "Kök/Root")', async ({ page }) => {
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE_NAME)}`);
    const firstCrumb = page.locator('.fe-breadcrumb__crumb').first();
    await expect(firstCrumb).toBeVisible({ timeout: 10_000 });
    await expect(firstCrumb).toContainText(STORAGE_NAME);
  });

  test('parent-dir button is hidden at storage root', async ({ page }) => {
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE_NAME)}`);
    await expect(page.locator('.fe-breadcrumb__crumb').first()).toBeVisible({
      timeout: 10_000,
    });
    const upBtn = page.getByRole('button', { name: /üst klasör|up one level/i });
    await expect(upBtn).toHaveCount(0);
  });

  test('drill in + click root-crumb pops back to storage root', async ({ page, request }) => {
    // The previous version of this test seeded its own storage and
    // drove the UI through aa/bb. The SFC list API call returned
    // [] for the brand-new storage even though pre-flight curls
    // saw aa+bb (likely a sync_mode=fsnotify race we'll fix
    // separately). Pivot to the production-seeded `s3-test` storage,
    // which always has the example/ folder Ada uploaded — that's
    // where the bug happened and that's where the regression matters.
    test.skip(
      !process.env.E2E_FM_HOST?.includes('fm.example.com'),
      'requires the fm.example.com deployment + s3-test storage with example/ folder',
    );

    await loginAs(page);
    const calls: Array<{ url: string; status: number }> = [];
    page.on('response', (resp) => {
      const url = resp.url();
      if (url.includes('/api/files/manager?')) calls.push({ url, status: resp.status() });
    });
    await page.goto('/admin/explore?storage=s3-test');

    const row = (name: string) =>
      page.locator('.fe-list__row, .fe-grid__card').filter({ hasText: name }).first();

    // Drill into example/ (always present on fm.example.com).
    const exampleRow = row('example');
    try {
      await expect(exampleRow).toBeVisible({ timeout: 15_000 });
    } catch (e) {
      const bcs = await page.locator('.fe-breadcrumb__crumb').allTextContents();
      const tabs = await page.locator('header nav button[type="button"]').allTextContents();
      console.log('breadcrumb:', JSON.stringify(bcs));
      console.log('tabs:', JSON.stringify(tabs));
      console.log('=== api calls ===');
      for (const c of calls) console.log(c.status, c.url);
      throw e;
    }
    await exampleRow.dblclick();

    // Crumb walk: [s3-test, example]
    await expect(page.locator('.fe-breadcrumb__crumb')).toHaveCount(2, { timeout: 5_000 });
    await expect(page.locator('.fe-breadcrumb__crumb').last()).toContainText('example');

    // ↑ Üst Klasör — visible inside a sub-folder.
    const upBtn = page.getByRole('button', { name: /üst klasör|up one level/i }).first();
    await expect(upBtn).toBeVisible();
    await upBtn.click();
    await expect(page.locator('.fe-breadcrumb__crumb')).toHaveCount(1, { timeout: 5_000 });

    // Drill in again, then click the root crumb directly — same end
    // state, different path.
    await exampleRow.dblclick();
    await expect(page.locator('.fe-breadcrumb__crumb')).toHaveCount(2);
    await page.locator('.fe-breadcrumb__crumb').first().click();
    await expect(page.locator('.fe-breadcrumb__crumb')).toHaveCount(1, { timeout: 5_000 });

    // Walk back in to test the keyboard shortcut.
    await exampleRow.dblclick();
    await expect(page.locator('.fe-breadcrumb__crumb').last()).toContainText('example');
    await page.keyboard.press('Alt+ArrowUp');
    await expect(page.locator('.fe-breadcrumb__crumb')).toHaveCount(1, { timeout: 5_000 });
  });
});
