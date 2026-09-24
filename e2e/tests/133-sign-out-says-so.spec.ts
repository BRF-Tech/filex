/**
 * 133-sign-out-says-so — "Sign out" lands on the sign-in page of the front
 * door the person was using, says they are signed out, and the session is
 * really gone (PR #40, Berk Başarır).
 *
 * Why in a browser: `lib/signOut` routes by the router's history base (`/admin`
 * or `/drive`) and adds `?signed_out=1`, and the sign-in page reads it to hold
 * back SSO-first mode's automatic redirect — the one thing that made "Sign
 * out" sign the same account straight back in. Unit tests mount Login.vue with
 * a mocked route; only a real navigation proves the two halves meet. A
 * password session has no identity-provider half to end, so this is the local
 * path of the same code; the IdP half is measured over HTTP in
 * handlers/auth_logout_live_test.go.
 */
import { test, expect, type Page } from '@playwright/test';
import { apiLogin, dismissInstallBanner, loginAs } from '../helpers/auth';

const USER_EMAIL = 'signout-reader@example.com';
const USER_PASSWORD = 'signout-reader-pw-2026';

async function signOutFromTheExplorer(page: Page) {
  await page.getByTestId('explore-account').click();
  await page.getByTestId('explore-signout').click();
}

async function expectSignedOutOn(page: Page, door: 'admin' | 'drive') {
  await page.waitForURL(new RegExp(`/${door}/login\\?signed_out=1`));
  const note = page.getByTestId('login-signed-out');
  await expect(note).toBeVisible();
  await expect(note).toHaveText(/You are signed out\.|Oturumunuz kapatıldı\./);
  await expect(note).toHaveAttribute('role', 'status');
  // …and it is not a picture of a sign-out: the session is gone.
  const me = await page.request.get('/api/auth/me');
  expect(me.status(), 'the session outlived "Sign out"').toBe(401);
}

test.describe('signing out says so, on the front door the person used', () => {
  test.beforeAll(async ({ request }) => {
    await apiLogin(request);
    await request.post('/api/admin/users', {
      data: { email: USER_EMAIL, password: USER_PASSWORD, role: 'user' },
    });
  });

  test('an administrator, from the explorer: /admin/login?signed_out=1', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/explore');
    await signOutFromTheExplorer(page);
    await expectSignedOutOn(page, 'admin');
  });

  test('a member, from their own front door: /drive/login?signed_out=1', async ({ page }) => {
    await dismissInstallBanner(page);
    await page.addInitScript(() => {
      try {
        localStorage.setItem('filex.tourDone', '1');
      } catch {
        /* storage blocked — the tour will just be present */
      }
    });
    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(USER_EMAIL);
    await page.getByLabel(/password|parola/i).fill(USER_PASSWORD);
    await page
      .getByRole('button', { name: 'Sign in', exact: true })
      .or(page.getByRole('button', { name: 'Oturum aç', exact: true }))
      .first()
      .click();
    await page.waitForURL(/\/drive\//, { timeout: 15_000 });
    await page.goto('/drive/explore');
    await signOutFromTheExplorer(page);
    // ⚠ Not /admin/login: a /drive user brought back through /admin is the
    // wrong front door (issue #14).
    await expectSignedOutOn(page, 'drive');
  });
});
