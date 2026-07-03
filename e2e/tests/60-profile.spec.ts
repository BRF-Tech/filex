import { test, expect } from '@playwright/test';
import { loginAs, ADMIN_EMAIL, ADMIN_PASSWORD } from '../helpers/auth';

test.describe('Profile — locale + password + TOTP enroll', () => {
  test('locale switch persists across reload', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/profile');

    const localeSelect = page.getByLabel(/locale|dil/i);
    if (!(await localeSelect.count())) test.skip(true, 'locale field not in profile UI');
    await localeSelect.selectOption('tr');
    await page.getByRole('button', { name: /save|kaydet/i }).click();

    await page.reload();
    // Verify a Turkish label is visible somewhere in the layout.
    await expect(page.getByText(/Çıkış|Ayarlar|Panel/i)).toBeVisible({ timeout: 5_000 });

    // Reset to en for following tests.
    await page.goto('/admin/profile');
    await localeSelect.selectOption('en');
    await page.getByRole('button', { name: /save|kaydet/i }).click();
  });

  test('password change requires correct old password', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/profile');

    const oldPw = page.getByLabel(/old password|eski şifre/i);
    if (!(await oldPw.count())) test.skip(true, 'password change UI absent');
    await oldPw.fill('definitely-wrong');
    await page.getByLabel(/new password|yeni şifre/i).fill('something-else-1234');
    await page.getByRole('button', { name: /change password|şifre değiştir/i }).click();
    await expect(page.getByText(/incorrect|yanlış|invalid/i)).toBeVisible({ timeout: 5_000 });
  });

  test('TOTP enroll returns a QR + recovery codes', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/profile');

    const enroll = page.getByRole('button', { name: /enroll|2fa|totp/i });
    if (!(await enroll.count())) test.skip(true, 'TOTP UI not surfaced yet');
    await enroll.click();
    await expect(page.locator('img[alt*="qr"], svg[aria-label*="qr"], [data-testid="totp-qr"]'))
      .toBeVisible({ timeout: 5_000 });
    await expect(page.getByText(/recovery|kurtarma/i)).toBeVisible();
  });
});
