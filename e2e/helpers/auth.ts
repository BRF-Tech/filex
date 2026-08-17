import type { Page, APIRequestContext } from '@playwright/test';

export const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL ?? 'admin@local';
export const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD ?? 'admin';

/**
 * Pre-dismiss the PWA install / desktop-download banner.
 *
 * `InstallPrompt.vue` renders `fixed inset-x-0 bottom-0 z-40` on every desktop
 * browser that has not installed the app — which is every Playwright run. It
 * covers the bottom strip of the page, so any control that lands there is
 * unclickable: Playwright retries for the full actionTimeout and reports
 * "<div data-testid=pwa-install-banner> intercepts pointer events", which
 * reads like the feature under test is broken. That is how the TOTP enroll
 * button in 60-profile "failed".
 *
 * A real user dismisses it once and it stays dismissed; `useInstallPrompt`
 * persists that in localStorage. Set the same flag before the first
 * navigation so the banner never mounts, rather than teaching every spec to
 * dodge it.
 */
export async function dismissInstallBanner(page: Page) {
  await page.addInitScript(() => {
    try {
      localStorage.setItem('filex.installPrompt.dismissed', '1');
    } catch {
      /* storage blocked — the banner will just be present */
    }
  });
}

/**
 * Log in via the Vue admin form. Lands on /admin/dashboard on success.
 *
 * The login page in OIDC-enabled builds shows TWO buttons: the local submit
 * and a `Sign in with SSO (Keycloak)` redirect, so the submit is picked by
 * exact name rather than a regex that would match both.
 *
 * ⚠ Matched in BOTH languages. `60-profile` deliberately flips the admin's UI
 * language, so a selector pinned to one of them is a spec-ordering hazard
 * waiting to happen — every later spec logs in through here.
 *
 * ⚠⚠ Honesty about what this did and did not fix. It was written while
 * chasing a cascade (one run in four, 2026-08-17: `60-profile` red, then
 * eleven later specs failing with `apiLogin failed: 401`), on the theory that
 * a Turkish page left the button unreachable. That theory is WRONG and the
 * regression test in 60-profile disproves it — the login page renders before
 * authentication, so it never reads the account's language. This stays as
 * hardening; it is not the fix. The fix was a missing `await` in that spec,
 * and the 401 itself is still unexplained — see the note there.
 */
export async function loginAs(page: Page, email = ADMIN_EMAIL, password = ADMIN_PASSWORD) {
  await dismissInstallBanner(page);
  await page.goto('/admin/login');
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(email);
  await page.getByLabel(/password|şifre/i).fill(password);
  // `exact` per name, so neither matches "Sign in with SSO (Keycloak)".
  const submit = page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Giriş yap', exact: true }));
  await submit.first().click();
  // ⚠ No hand-rolled budget here: this inherits the project's
  // `navigationTimeout` (15s), and the 10s it used to hardcode was TIGHTER
  // than the config every other navigation in the suite gets.
  //
  // ⚠⚠ Stated honestly: this is not a proven fix for anything. A full run
  // failed here once in five (2026-08-17) with a plain navigation timeout and
  // no server error, while twelve isolated runs of the same spec passed — so
  // it looks like the machine being busy (the suite spawns thumbnail
  // subprocesses), not a defect. What is certain is only that the number being
  // removed was arbitrary and smaller than the project's own.
  await page.waitForURL(/\/admin\/dashboard/);
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
