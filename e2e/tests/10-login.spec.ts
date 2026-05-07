import { test, expect } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD, loginAs, logout } from '../helpers/auth';

test.describe('Login flow', () => {
  test('rejects invalid credentials', async ({ page }) => {
    await page.goto('/admin/login');
    await page.getByLabel(/email/i).fill('admin@local');
    await page.getByLabel(/password|şifre/i).fill('definitely-wrong-password');
    // Exact name disambiguates the local form submit from the OIDC
    // 'Sign in with SSO (Keycloak)' button.
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(page).toHaveURL(/\/admin\/login/);
    await expect(page.getByText(/invalid|hata|incorrect|geçersiz|unauthorized/i)).toBeVisible({ timeout: 5_000 });
  });

  test('accepts admin credentials and lands on dashboard', async ({ page }) => {
    await loginAs(page, ADMIN_EMAIL, ADMIN_PASSWORD);
    await expect(page).toHaveURL(/\/admin\/dashboard/);
    // Dashboard always renders SOMETHING — either the empty-state CTA
    // (fresh install) or a stat card. Just sanity-check we left the
    // login page behind.
    await expect(page.getByRole('heading').first()).toBeVisible();
  });

  test('logout clears session', async ({ page }) => {
    await loginAs(page);
    await logout(page);
    // Trying to access dashboard should bounce to login.
    await page.goto('/admin/dashboard');
    await expect(page).toHaveURL(/\/admin\/login/);
  });
});
