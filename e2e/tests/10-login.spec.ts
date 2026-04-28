import { test, expect } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD, loginAs, logout } from '../helpers/auth';

test.describe('Login flow', () => {
  test('rejects invalid credentials', async ({ page }) => {
    await page.goto('/admin/login');
    await page.getByLabel(/email/i).fill('admin@local');
    await page.getByLabel(/password|şifre/i).fill('definitely-wrong-password');
    await page.getByRole('button', { name: /sign in|giriş/i }).click();
    // Stay on login + error toast/message visible.
    await expect(page).toHaveURL(/\/admin\/login/);
    await expect(page.getByText(/invalid|hata|incorrect|geçersiz/i)).toBeVisible({ timeout: 5_000 });
  });

  test('accepts admin credentials and lands on dashboard', async ({ page }) => {
    await loginAs(page, ADMIN_EMAIL, ADMIN_PASSWORD);
    await expect(page).toHaveURL(/\/admin\/dashboard/);
    // Empty-state CTA: no storage configured yet.
    await expect(page.getByText(/no storage|depolama yok/i)).toBeVisible();
  });

  test('logout clears session', async ({ page }) => {
    await loginAs(page);
    await logout(page);
    // Trying to access dashboard should bounce to login.
    await page.goto('/admin/dashboard');
    await expect(page).toHaveURL(/\/admin\/login/);
  });
});
