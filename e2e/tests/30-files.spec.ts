import { test, expect } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const STORAGE_NAME = 'e2e-files';

test.describe('File operations — upload, download, delete, restore', () => {
  test.beforeAll(async ({ request }) => {
    await seedLocalStorage(request, STORAGE_NAME, '/tmp/filex-e2e-files');
  });
  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE_NAME);
  });

  test('upload a fixture file and see it listed', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/storages');
    await page.getByText(STORAGE_NAME).click();

    // The admin UI navigates into the storage. The actual file explorer
    // is rendered by the @brftech/filex-core component (or a thin admin
    // wrapper page). Either way an upload button should be present.
    const uploadBtn = page.getByRole('button', { name: /upload|yükle/i }).first();
    await expect(uploadBtn).toBeVisible();

    // Trigger a hidden <input type=file> via setInputFiles.
    const fileInput = page.locator('input[type="file"]').first();
    await fileInput.setInputFiles(path.join(__dirname, '../fixtures/hello.txt'));

    // Expect the file in the list.
    await expect(page.getByText('hello.txt')).toBeVisible({ timeout: 15_000 });
  });

  test('soft-delete moves file to trash, restore brings it back', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/storages');
    await page.getByText(STORAGE_NAME).click();

    // Right-click to open context menu, click delete.
    await page.getByText('hello.txt').click({ button: 'right' });
    await page.getByRole('menuitem', { name: /delete|sil/i }).click();
    await page.getByRole('button', { name: /confirm|onayla|delete|sil/i }).click();
    await expect(page.getByText('hello.txt')).not.toBeVisible({ timeout: 5_000 });

    // Open trash, restore.
    await page.getByRole('link', { name: /trash|çöp/i }).first().click();
    await expect(page.getByText('hello.txt')).toBeVisible();
    await page.getByText('hello.txt').click({ button: 'right' });
    await page.getByRole('menuitem', { name: /restore|geri yükle/i }).click();
    await expect(page.getByText('hello.txt')).not.toBeVisible({ timeout: 5_000 });
  });
});
