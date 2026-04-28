import type { Page, APIRequestContext } from '@playwright/test';

export const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL ?? 'admin@local';
export const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD ?? 'admin';

/**
 * Log in via the Vue admin form. Lands on /admin/dashboard on success.
 */
export async function loginAs(page: Page, email = ADMIN_EMAIL, password = ADMIN_PASSWORD) {
  await page.goto('/admin/login');
  await page.getByLabel(/email/i).fill(email);
  await page.getByLabel(/password|şifre/i).fill(password);
  await page.getByRole('button', { name: /sign in|giriş/i }).click();
  await page.waitForURL(/\/admin\/dashboard/, { timeout: 10_000 });
}

/**
 * Backend API login, returns the session cookie string. Useful when an
 * individual test needs an authenticated APIRequestContext but doesn't
 * want to drive the UI form.
 */
export async function apiLogin(
  request: APIRequestContext,
  email = ADMIN_EMAIL,
  password = ADMIN_PASSWORD,
): Promise<string> {
  const res = await request.post('/api/auth/login', {
    data: { email, password },
  });
  if (!res.ok()) throw new Error(`apiLogin failed: ${res.status()} ${await res.text()}`);
  const cookies = res.headers()['set-cookie'];
  return cookies ?? '';
}

/**
 * Logs out via the user menu. Asserts redirection back to /admin/login.
 */
export async function logout(page: Page) {
  await page.getByTestId('user-menu-button').click();
  await page.getByRole('menuitem', { name: /logout|çıkış/i }).click();
  await page.waitForURL(/\/admin\/login/);
}
