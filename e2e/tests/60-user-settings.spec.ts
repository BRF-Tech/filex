import { test, expect } from '@playwright/test';
import { loginAs, ADMIN_EMAIL, ADMIN_PASSWORD } from '../helpers/auth';

/**
 * The user-settings dialog — language, password, two-factor.
 *
 * ⚠ This file was `60-profile.spec.ts` and drove `/admin/profile`, a page that
 * carried the same fields and has been retired: it lived inside the admin-only
 * layout, so half the people who own an account could never reach it, while
 * the dialog is the door a non-admin comes through too. The flows below are
 * the ones that page was the only cover for, moved onto the surface that
 * replaced it — not deleted with it.
 *
 * ⚠ The dialog is reached by URL (`?settings=1`) rather than by clicking the
 * avatar menu, for the same reason the parameter exists at all: the server
 * prints that address into its startup banner and into `.first-run.txt`, so
 * it is a real entry point and is worth exercising on every run.
 */
test.describe('User settings — language + password + TOTP enroll', () => {
  // ⚠ This file deliberately flips the admin's UI language mid-test. If it
  // fails between the switch and the reset, EVERY later spec that matches an
  // English label inherits a Turkish admin panel and fails for a reason that
  // has nothing to do with it. Put the account back by API, unconditionally,
  // so one red here cannot become ten reds elsewhere.
  test.afterAll(async ({ request }) => {
    const login = await request.post('/api/auth/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    // ⚠ A failure HERE is the loudest signal in the suite and it used to be
    // swallowed by a bare `return`: it means this file left the shared admin
    // account unusable, and every spec after it is about to fail for a reason
    // none of them can explain. Say so, on the console, where the person
    // reading eleven red tests will see it.
    if (!login.ok()) {
      console.error(
        `\n⚠⚠ 60-user-settings could not sign the admin back in (${login.status()}). ` +
          `Every later spec will fail on login, and NOT because of its own code.\n`,
      );
      return;
    }
    const { token } = await login.json();
    await request
      .patch('/api/auth/profile', {
        headers: { Authorization: `Bearer ${token}` },
        data: { locale: 'en' },
      })
      .catch(() => undefined);
  });

  /** Opens the dialog through the deep link and waits for it to paint. */
  async function openSettings(page: import('@playwright/test').Page, section = 'profile') {
    await page.goto('/admin/dashboard?settings=1');
    await expect(page.getByTestId('user-settings-dialog')).toBeVisible({ timeout: 15_000 });
    if (section !== 'profile') await page.getByTestId(`user-settings-tab-${section}`).click();
    await expect(page.getByTestId(`user-settings-${section}`)).toBeVisible({ timeout: 5_000 });
  }

  // The deep link is load-bearing: it is what `.first-run.txt` and the startup
  // banner tell a brand-new operator, now that /admin/profile is gone. A dialog
  // that only opens from an avatar menu cannot be named in a printed line.
  test('?settings=1 opens the dialog and then leaves the URL alone', async ({ page }) => {
    await loginAs(page);
    await openSettings(page);
    // The parameter is consumed, so a reload or a shared URL does not reopen
    // the dialog behind the person's back.
    await expect(page).toHaveURL(/\/admin\/dashboard(?!.*settings=)/);
  });

  test('language switch persists across reload', async ({ page }) => {
    await loginAs(page);
    await openSettings(page, 'preferences');

    // ⚠ Locate the language field by its testid, not by label. Playwright
    // matches getByLabel against the label's textContent, and a loose /dil/i
    // also matches "Zaman dilimi" (timezone) and goes strict-mode ambiguous.
    // The testid is unambiguous in every locale.
    //
    // ⚠ It is a SEGMENTED STRIP, not a <select> (owner's call, 2026-09-13:
    // "dil seçimi dropdown yerine direk buton yapabiliriz, startpage'deki
    // gibi"). So the state is `aria-pressed` on a button, not the group's
    // value — `selectOption`/`toHaveValue` would throw on a <div>.
    const localeGroup = page.getByTestId('user-settings-locale');
    await expect(localeGroup, 'the language field must exist').toHaveCount(1);

    // ⚠ No Save here, on purpose: the preferences pane applies on the click
    // that made the change (see the footer note in UserSettingsModal.vue), so
    // waiting for a "saved" toast would wait for something that never comes.
    // What has to be awaited instead is the PATCH that writes it to the
    // account — a reload that races it reads back the old language.
    const wrote = page.waitForResponse(
      (r) => r.url().includes('/api/auth/profile') && r.request().method() === 'PATCH' && r.ok(),
      { timeout: 10_000 },
    );
    await page.getByTestId('user-settings-locale-tr').click();
    await wrote;

    await page.reload();
    // A Turkish label visible somewhere in the layout.
    await expect(page.getByText(/Çıkış|Ayarlar|Panel/i).first()).toBeVisible({ timeout: 5_000 });

    // Reset to en for the specs that follow.
    await openSettings(page, 'preferences');
    const back = page.getByTestId('user-settings-locale-en');
    // ⚠⚠ WAIT for the write to land before reloading. The switch above does;
    // the reset did not, so the reload raced the PATCH.
    //
    // What was measured (2026-08-17): about one run in four the assertion
    // after the reload failed with "element(s) not found" — the SPA had
    // bounced to login, the two tests below then timed out signing in, and
    // ELEVEN later specs failed with `apiLogin failed: 401 invalid
    // credentials`.
    //
    // ⚠ The 401 is not explained by this await, and pretending otherwise
    // would be worse than admitting it: nothing found so far accounts for the
    // shared admin password ceasing to work. What IS established is that
    // adding this wait took the suite from roughly one failure every two runs
    // to 13 consecutive clean runs. If it comes back, start from the server
    // log in the run's data dir (`--keep`), not from here.
    const wroteBack = page.waitForResponse(
      (r) => r.url().includes('/api/auth/profile') && r.request().method() === 'PATCH' && r.ok(),
      { timeout: 10_000 },
    );
    await back.click();
    await wroteBack;
    await page.reload();
    await openSettings(page, 'preferences');
    await expect(page.getByTestId('user-settings-locale-en')).toHaveAttribute(
      'aria-pressed',
      'true',
    );
  });

  test('password change requires correct old password', async ({ page }) => {
    await loginAs(page);
    await openSettings(page, 'security');

    // The labels are `common.currentPassword` ("Current password" / "Mevcut
    // şifre") and `common.newPassword`; the submit is a plain "Save".
    const pane = page.getByTestId('user-settings-security');
    const oldPw = pane.getByLabel(/current password|mevcut şifre|old password|eski şifre/i);
    await expect(oldPw, 'the password-change form must exist').toHaveCount(1);
    // ⚠ `has` takes a locator RELATIVE to the form. `oldPw` starts from the
    // pane's testid, which is not inside the form, so filtering on it matched
    // no form at all.
    const securityForm = pane
      .locator('form')
      .filter({ has: page.getByLabel(/current password|mevcut şifre|old password|eski şifre/i) });

    await oldPw.fill('definitely-wrong');
    // "New password" also matches the confirm box — fill both, since the form
    // refuses to submit unless they agree.
    const newPw = securityForm.getByLabel(/new password|yeni şifre|confirm|onayla/i);
    await newPw.nth(0).fill('something-else-1234');
    await newPw.nth(1).fill('something-else-1234');
    await securityForm.getByRole('button', { name: /save|kaydet/i }).click();
    await expect(page.getByText(/incorrect|yanlış|invalid|hatalı/i).first()).toBeVisible({
      timeout: 5_000,
    });

    // And the account still opens with the ORIGINAL password — a rejected
    // change that silently succeeded would be the real disaster here.
    const check = await page.request.post('/api/auth/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    expect(check.ok(), 'the password must not have changed').toBeTruthy();
  });

  test('TOTP enroll returns a QR + recovery codes', async ({ page }) => {
    await loginAs(page);
    await openSettings(page, 'security');

    // ⚠ `locator.count()` does not auto-wait, so `if (!count) test.skip()`
    // fires whenever the SPA has not finished painting — the test skips itself
    // at random and nobody notices, which is how the missing recovery codes
    // below survived. Wait for the control and require it.
    const enroll = page.getByTestId('user-settings-totp-enable');
    await expect(enroll, 'the security pane must offer 2FA enrollment').toBeVisible({
      timeout: 15_000,
    });
    await enroll.click();

    await expect(page.getByTestId('user-settings-totp-qr')).toBeVisible({ timeout: 5_000 });
    await expect(page.getByTestId('user-settings-totp-qr').locator('svg')).toBeVisible();

    // Recovery codes. /auth/totp/enroll generates ten, STORES them against the
    // pending secret and returns them exactly once — so if this panel is
    // missing, the user owns ten working codes they have never seen and a lost
    // authenticator means a lost account. That was the state of the product
    // until this assertion was made to run.
    const items = page.locator('.fx-us__recovery-list li');
    await expect(items).toHaveCount(10);
    for (const text of await items.allInnerTexts()) {
      // Format is fixed by generateRecoveryCodes (auth_self.go): two groups of
      // five from an unambiguous alphabet, hyphen-separated.
      expect(text.trim(), 'each recovery code must be a real code').toMatch(
        /^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{5}-[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{5}$/,
      );
    }
  });

  /**
   * A spec-ordering guard: this file flips the admin's UI language, so put the
   * account in Turkish ON PURPOSE — the way a botched reset would leave it —
   * and require that signing in still works.
   *
   * ⚠ It is also the test that DISPROVED the first theory about the cascade
   * above. The theory was that a Turkish account made the login button read
   * "Giriş yap" and an English-only selector timed out; this passes with that
   * selector restored, because the login page renders before authentication
   * and never reads the account's language. Kept because the property is worth
   * holding — not because it is the fix.
   */
  test('logging in still works when the account UI is Turkish', async ({ page, request }) => {
    const login = await request.post('/api/auth/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    expect(login.ok(), 'the admin account must be usable before this test').toBeTruthy();
    const { token } = await login.json();

    const toTr = await request.patch('/api/auth/profile', {
      headers: { Authorization: `Bearer ${token}` },
      data: { locale: 'tr' },
    });
    expect(toTr.ok()).toBeTruthy();

    try {
      // The whole assertion: this must not time out.
      await loginAs(page);
      await expect(page).toHaveURL(/\/admin\/home/);
    } finally {
      // Unconditional — this test must not become the thing it guards against.
      await request
        .patch('/api/auth/profile', {
          headers: { Authorization: `Bearer ${token}` },
          data: { locale: 'en' },
        })
        .catch(() => undefined);
    }
  });
});
