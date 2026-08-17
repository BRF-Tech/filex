import { test, expect } from '@playwright/test';
import { loginAs, ADMIN_EMAIL, ADMIN_PASSWORD } from '../helpers/auth';

test.describe('Profile — locale + password + TOTP enroll', () => {
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
        `\n⚠⚠ 60-profile could not sign the admin back in (${login.status()}). ` +
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

  test('locale switch persists across reload', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/profile');

    // ⚠ The field is labelled `profile.locale` = "Language" / "Dil". The old
    // matcher was /locale|dil/i, which never matched the English label, so
    // `test.skip('locale field not in profile UI')` fired on every run — a
    // shipped, working feature reported as "not built yet" for as long as
    // anyone cared to look. A skip that can never turn into a test is a
    // deleted test with extra steps.
    // ⚠ Locate the language field as "the account form's only <select>",
    // not by label. Playwright matches getByLabel against the label's
    // textContent, which for this control also swallows the option labels —
    // so an anchored /^Dil$/ finds nothing, while a loose /dil/i also matches
    // "Zaman dilimi" (timezone) and goes strict-mode ambiguous. The element
    // itself is unambiguous in every locale.
    const accountForm = page.locator('form').first();
    const localeSelect = accountForm.locator('select');
    await expect(localeSelect, 'the profile locale field must exist').toHaveCount(1);

    // ⚠ Display name is `required` and a freshly seeded admin has none, so the
    // browser refuses to submit the form and NO request is made — the click
    // looks like it worked and nothing happens. Fill it the way a user would.
    const displayName = accountForm.getByLabel(/display name|görünen isim/i);
    if (await displayName.count()) await displayName.fill('E2E Admin');
    await localeSelect.selectOption('tr');
    await accountForm.getByRole('button', { name: /save|kaydet/i }).click();
    // The save must actually reach the server.
    await expect(page.getByText(/profile saved|profil kaydedildi/i).first()).toBeVisible({
      timeout: 5_000,
    });

    await page.reload();
    // Verify a Turkish label is visible somewhere in the layout.
    await expect(page.getByText(/Çıkış|Ayarlar|Panel/i).first()).toBeVisible({ timeout: 5_000 });

    // Reset to en for following tests.
    await page.goto('/admin/profile');
    // Scope to the account form: once the UI is Turkish, /dil/i also matches
    // the top-bar language switcher, and an unscoped getByLabel goes
    // strict-mode ambiguous.
    const backForm = page.locator('form').first();
    const back = backForm.locator('select');
    const backName = backForm.getByLabel(/display name|görünen isim/i);
    if (await backName.count()) await backName.fill('E2E Admin');
    await back.selectOption('en');
    await backForm.getByRole('button', { name: /save|kaydet/i }).click();
    // ⚠⚠ WAIT for the save to land before reloading. The switch above does;
    // this one did not, so the reload raced the PATCH.
    //
    // What was measured (2026-08-17): about one run in four this assertion
    // failed with "element(s) not found" — after the reload there was no form
    // on the page AT ALL, i.e. the SPA had bounced to login. The two tests
    // below then timed out signing in, and ELEVEN later specs failed with
    // `apiLogin failed: 401 invalid credentials`.
    //
    // ⚠ The 401 is not explained by this await, and pretending otherwise
    // would be worse than admitting it: nothing found so far accounts for the
    // shared admin password ceasing to work, and both forms on this page are
    // separate (the password form is never submitted by the locale test).
    // What IS established is that adding this wait took the suite from roughly
    // one failure every two runs to 13 consecutive clean runs. If it comes
    // back, start from the server log in the run's data dir (`--keep`), not
    // from here.
    await expect(page.getByText(/profile saved|profil kaydedildi/i).first()).toBeVisible({
      timeout: 5_000,
    });
    await page.reload();
    await expect(page.locator('form').first().locator('select')).toHaveValue('en');
  });

  test('password change requires correct old password', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/profile');

    // ⚠ Same stale-matcher story as the locale field: the labels are
    // `common.currentPassword` ("Current password" / "Mevcut şifre") and
    // `common.newPassword`, and the submit button is a plain "Save" shared
    // with the account form above it. /old password/ matched nothing, so this
    // test skipped itself on every run while the endpoint went untested.
    const oldPw = page.getByLabel(/current password|mevcut şifre|old password|eski şifre/i);
    await expect(oldPw, 'the password-change form must exist').toHaveCount(1);
    const securityForm = page.locator('form').filter({ has: oldPw });

    await oldPw.fill('definitely-wrong');
    // "New password" also matches "New password (Confirm)" — fill both, since
    // the form refuses to submit unless they agree.
    const newPw = securityForm.getByLabel(/new password|yeni şifre/i);
    await newPw.nth(0).fill('something-else-1234');
    await newPw.nth(1).fill('something-else-1234');
    await securityForm.getByRole('button', { name: /save|kaydet|change password|şifre değiştir/i }).click();
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
    await page.goto('/admin/profile');

    // ⚠ `locator.count()` does not auto-wait, so `if (!count) test.skip()`
    // fires whenever the SPA has not finished painting — the test skips
    // itself at random and nobody notices, which is how the missing recovery
    // codes below survived. Wait for the control and require it.
    const enroll = page.getByRole('button', { name: /enroll|2fa|totp/i }).first();
    await expect(enroll, 'the profile page must offer 2FA enrollment').toBeVisible({
      timeout: 15_000,
    });
    await enroll.click();

    await expect(page.getByTestId('totp-qr')).toBeVisible({ timeout: 5_000 });
    await expect(page.getByTestId('totp-qr').locator('svg')).toBeVisible();

    // Recovery codes. /auth/totp/enroll generates ten, STORES them against
    // the pending secret and returns them exactly once — so if this panel is
    // missing, the user owns ten working codes they have never seen and a
    // lost authenticator means a lost account. That was the state of the
    // product until this assertion was made to run.
    const codes = page.getByTestId('totp-recovery-codes');
    await expect(codes).toBeVisible();
    const items = codes.locator('li');
    await expect(items).toHaveCount(10);
    for (const text of await items.allInnerTexts()) {
      // Format is fixed by generateRecoveryCodes (auth_self.go:289): two
      // groups of five from an unambiguous alphabet, hyphen-separated.
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
      await expect(page).toHaveURL(/\/admin\/dashboard/);
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
