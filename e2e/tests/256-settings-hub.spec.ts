/**
 * 256 - the settings in their one place each (task #258, the owner's grouping
 * of 2026-10-15), measured in a real browser.
 *
 *   - Admin → Settings → General holds the server's defaults (what lived in
 *     the environment alone until 0.57), the thumbnail settings and the
 *     search settings; a change to a default read at start says it waits for
 *     the next start;
 *   - Tools → Thumbnail repair and the Search index page LINK to their card
 *     on General, and the link lands on that card;
 *   - the default folder view is on Appearance, not on General;
 *   - User settings says where each preference is saved (Account / This
 *     device), and its keyboard shortcuts' editor opens over the dialog:
 *     Escape closes the editor and leaves the dialog open.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { newAuthedRequest } from '../helpers/seed';

let api: APIRequestContext;

test.beforeAll(async ({ playwright, baseURL }) => {
  api = await newAuthedRequest(playwright, baseURL ?? '');
});

test.afterAll(async () => {
  // Back to what the suite's server started with: nothing waits for a restart.
  await api.patch('/api/admin/instance-settings', { data: { 'sync.default_interval_minutes': 15 } });
  await api.dispose();
});

test.describe('Admin → Settings: one home per setting', () => {
  test('General holds the server defaults, the thumbnail and the search settings', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/settings');
    const defaults = page.getByTestId('instance-settings-general');
    await expect(defaults).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('thumb-limits')).toBeVisible();
    await expect(page.getByTestId('search-settings')).toBeVisible();
    await expect(page.getByTestId('settings-folder-view')).toHaveCount(0);

    // A default read at start: saved, and said to wait for the next start.
    const row = page.getByTestId('instance-setting-sync.default_interval_minutes');
    await row.locator('input').fill('20');
    await page.getByTestId('instance-settings-general-save').click();
    await expect(page.getByTestId('instance-setting-pending-sync.default_interval_minutes')).toBeVisible();
    const items = (await (await api.get('/api/admin/instance-settings?group=general')).json()).items as Array<{ key: string; value: unknown }>;
    expect(items.find((i) => i.key === 'sync.default_interval_minutes')?.value).toBe(20);
  });

  test('a number out of its bounds is refused under its field, and nothing is saved', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/settings');
    const row = page.getByTestId('instance-setting-cache.max_gb');
    await expect(row).toBeVisible({ timeout: 20_000 });
    await row.locator('input').fill('0');
    await page.getByTestId('instance-settings-general-save').click();
    await expect(row).toContainText(/1.*10240/);
    const items = (await (await api.get('/api/admin/instance-settings?group=general')).json()).items as Array<{ key: string; value: unknown }>;
    expect(items.find((i) => i.key === 'cache.max_gb')?.value).toBe(20);
  });

  test('Tools → Thumbnail repair and the Search index link to their card on General', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/tools?tab=thumbnails');
    await expect(page.getByTestId('thumb-limits')).toHaveCount(0);
    await page.getByTestId('thumb-limits-link').getByRole('link').click();
    await expect(page).toHaveURL(/\/admin\/settings#thumb-limits$/);
    await expect(page.getByTestId('thumb-limits')).toBeInViewport({ timeout: 10_000 });

    await page.goto('/admin/search');
    await page.getByTestId('search-settings-link').getByRole('link').click();
    await expect(page).toHaveURL(/\/admin\/settings#search-settings$/);
    await expect(page.getByTestId('search-settings')).toBeInViewport({ timeout: 10_000 });
  });

  test('the default folder view is on Appearance', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/appearance');
    await expect(page.getByTestId('settings-folder-view')).toBeVisible({ timeout: 20_000 });
  });
});

test.describe('User settings: where each preference is saved', () => {
  test('Account and This device; the shortcuts editor opens over the dialog and Escape closes it alone', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/dashboard?settings=1');
    const dialog = page.getByTestId('user-settings-dialog');
    await expect(dialog).toBeVisible({ timeout: 20_000 });
    await page.getByTestId('user-settings-tab-preferences').click();
    await expect(dialog.getByTestId('user-settings-prefs-account').getByTestId('user-settings-timezone')).toBeVisible();
    await expect(dialog.getByTestId('user-settings-prefs-device').getByTestId('user-settings-shortcuts')).toBeAttached();

    await page.getByTestId('user-settings-shortcuts-edit').click();
    const editor = page.locator('.fe-shortset');
    await expect(editor).toBeVisible();
    // In front: the topmost element at the editor's middle is the editor's.
    const box = (await editor.boundingBox())!;
    const onTop = await page.evaluate(
      ([x, y]) => !!document.elementFromPoint(x, y)?.closest('.fe-shortset'),
      [box.x + box.width / 2, box.y + Math.min(60, box.height / 2)],
    );
    expect(onTop).toBe(true);

    await page.keyboard.press('Escape');
    await expect(editor).toHaveCount(0);
    await expect(dialog).toBeVisible();

    await page.getByTestId('user-settings-tab-notifications').click();
    await expect(dialog.getByTestId('user-settings-notify-account').getByTestId('user-settings-inapp')).toBeVisible();
  });
});
