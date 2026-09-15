import { test, expect } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD, loginAs, logout } from '../helpers/auth';

test.describe('Login flow', () => {
  test('rejects invalid credentials', async ({ page }) => {
    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill('admin@local');
    await page.getByLabel(/password|şifre/i).fill('definitely-wrong-password');
    // Exact name disambiguates the local form submit from the OIDC
    // 'Sign in with SSO' button.
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(page).toHaveURL(/\/admin\/login/);
    await expect(page.getByText(/invalid|hata|incorrect|geçersiz|unauthorized/i)).toBeVisible({ timeout: 5_000 });
  });

  // ⚠ Home, not the dashboard: since 0.41.0 every account, administrators
  // included, starts on Home unless it picked the dashboard in user settings
  // (web/src/lib/startPage.ts).
  test('accepts admin credentials and lands on Home', async ({ page }) => {
    await loginAs(page, ADMIN_EMAIL, ADMIN_PASSWORD);
    await expect(page).toHaveURL(/\/admin\/home/);
    // With a storage, Home is the explorer's Home view. On a fresh install —
    // which is what this spec usually meets, being among the first to run —
    // it is the "no storages yet" state, and that state has a layout of its
    // own to hold.
    const home = page.getByTestId('home-view');
    const emptyActions = page.getByTestId('explore-empty-actions');
    await expect(home.or(emptyActions)).toBeVisible({ timeout: 15_000 });
    if (await emptyActions.isVisible()) {
      // ⚠⚠ Measured 2026-09-14 on the first screen after a fresh install: the
      // bell and the avatar sat in a 420px-tall framed column down the middle
      // of the page, because their wrapper carries the explorer root's `.fe`
      // class and inherited its box. A row of two controls is a few dozen
      // pixels tall.
      const box = await emptyActions.boundingBox();
      expect(box, 'the empty state draws its bell and avatar').not.toBeNull();
      expect(box!.height, 'bell + avatar are a row, not an explorer-sized column').toBeLessThan(80);
      await expect(page.getByTestId('explore-account')).toBeVisible();
    }
  });

  test('logout clears session', async ({ page }) => {
    await loginAs(page);
    await logout(page);
    // Trying to access dashboard should bounce to login.
    await page.goto('/admin/dashboard');
    await expect(page).toHaveURL(/\/admin\/login/);
  });
});
