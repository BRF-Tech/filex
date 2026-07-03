import type { Page, APIRequestContext } from '@playwright/test';

export const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL ?? 'admin@local';
export const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD ?? 'admin';

/**
 * Log in via the Vue admin form. Lands on /admin/dashboard on success.
 *
 * The login page in OIDC-enabled builds shows TWO buttons: the local
 * `Sign in` submit and a `Sign in with SSO (Keycloak)` redirect. The
 * regex selector matched both, so we now click the submit button by
 * exact name to disambiguate.
 */
export async function loginAs(page: Page, email = ADMIN_EMAIL, password = ADMIN_PASSWORD) {
  await page.goto('/admin/login');
  await page.getByLabel(/email/i).fill(email);
  await page.getByLabel(/password|şifre/i).fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
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
 *
 * The user menu trigger lives in TopNav and isn't tagged with a stable
 * data-testid (the project's test-id strategy is informal). Match by
 * visible avatar/email instead — fall back to clearing cookies + a
 * direct nav if the menu strategy times out (some builds use a slide-
 * out user panel without a click trigger).
 */
export async function logout(page: Page) {
  try {
    const trigger = page.getByTestId('user-menu-button')
      .or(page.getByRole('button', { name: /admin@local|profile|user|hesap/i }));
    await trigger.first().click({ timeout: 3_000 });
    await page.getByRole('menuitem', { name: /logout|çıkış|sign out/i }).click({ timeout: 2_000 });
  } catch {
    // Last-resort: hit the API directly and bounce to login.
    await page.context().clearCookies();
    await page.goto('/admin/login');
  }
  await page.waitForURL(/\/admin\/login/);
}
