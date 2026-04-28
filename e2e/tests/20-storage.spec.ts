import { test, expect } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName } from '../helpers/seed';

const STORAGE_NAME = 'e2e-local-ui';

test.describe('Storage management — UI flow', () => {
  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE_NAME);
  });

  test('add a local storage via UI and see it in the list', async ({ page }) => {
    await loginAs(page);

    await page.goto('/admin/storages');
    await page.getByRole('link', { name: /add|new|yeni/i }).first().click();
    await page.waitForURL(/\/admin\/storages\/new/);

    await page.getByLabel(/name|isim/i).fill(STORAGE_NAME);
    await page.getByLabel(/driver|sürücü/i).selectOption('local');
    await page.getByLabel(/mount|kök/i).fill('/tmp/filex-e2e-ui');

    // Optional "test connection" button — exists for s3/sftp/webdav too.
    const testBtn = page.getByRole('button', { name: /test/i });
    if (await testBtn.count()) {
      await testBtn.first().click();
      await expect(page.getByText(/ok|connected|başarılı/i)).toBeVisible({ timeout: 10_000 });
    }

    await page.getByRole('button', { name: /save|kaydet/i }).click();
    await page.waitForURL(/\/admin\/storages/);
    await expect(page.getByText(STORAGE_NAME)).toBeVisible();
  });

  test('storage shows up in dashboard widget', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/dashboard');
    // The storage widget card should now exist.
    await expect(page.getByText(STORAGE_NAME)).toBeVisible({ timeout: 10_000 });
  });
});
