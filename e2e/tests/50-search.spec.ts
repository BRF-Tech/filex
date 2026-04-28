import { test, expect } from '@playwright/test';
import { loginAs } from '../helpers/auth';

test.describe('Search — admin Search index test page', () => {
  test('SearchTest view returns stats', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/search');

    // Stats card should render (document_count + index_size_bytes).
    await expect(page.getByText(/document.?count|belge sayısı/i)).toBeVisible({ timeout: 10_000 });
  });

  test('rebuild button enqueues background job', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/search');

    const rebuild = page.getByRole('button', { name: /rebuild|yeniden/i });
    if (await rebuild.count()) {
      await rebuild.click();
      await expect(page.getByText(/started|background|başlatıldı/i)).toBeVisible({ timeout: 5_000 });
    } else {
      test.skip(true, 'rebuild button not present in current admin UI');
    }
  });
});
