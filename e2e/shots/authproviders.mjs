// Identity providers — the operating-system providers (windows, pam) on the
// administrator's page: the test account beside the buttons, a test that
// signs nobody in without one, and a switch-on that a failing test refuses
// with no "switch on anyway" (0.50.0, #128).
//
//   node e2e/shots/authproviders.mjs       (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/authproviders/ (or SHOTS_OUT):
//
//   auth-providers-1440.png        Admin → Identity providers, whole page, after
//                                  the two actions below
//   auth-providers-958.png         the same at the narrowest desktop width
//   auth-provider-windows-1440.png the Windows card: its test run without an
//                                  account, and Save and apply refused on the
//                                  page because no account was given
//   auth-provider-pam-1440.png     the Linux (PAM) card: switched on with a test
//                                  account, refused by the server — the steps,
//                                  the failed one with its fix, the server's
//                                  sentence, and no "switch on anyway"
//
// ⚠ What the two cards say depends on the machine the instance runs on, and
// both are real answers: on Windows the Windows card reaches "give a test
// account" and the PAM card stops at "Linux only"; on Linux the Windows card
// says it cannot run here and the PAM card stops at the first setup step that
// is missing (pamtester, as a rule, with the command to install it as code).
// ⚠⚠ No real account is ever tried: the Windows test is sent WITHOUT an
// account, and the PAM save stops at a setup step before any sign-in.
//
// It also MEASURES, in a real browser and in both languages (jsdom has no
// layout): at 958 and 1440 px the page has no horizontal scroll, nothing of it
// sticks out of the window and no two controls overlap — with the test
// results and the refusal on screen. A failed measurement throws; the pictures
// are only written by a run that passed it.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { chromium } from '@playwright/test';
import { bootInstance, client, layoutProblems, log, mustSay, newContext, setLanguage, shot, signIn, sleep } from './scene.mjs';

const SET = 'authproviders';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
/** Not an account of any machine: the PAM save is refused before a sign-in is tried. */
const TEST_ACCOUNT = { username: 'alex', password: 'shots-not-a-real-password' };
const ON_WINDOWS = process.platform === 'win32';

async function openPage(page, url, width) {
  await page.setViewportSize({ width, height: 900 });
  await page.goto(`${url}/admin/auth-providers`);
  await page.getByTestId('auth-provider-pam').waitFor({ timeout: 20_000 });
  await page.getByTestId('auth-provider-windows').waitFor({ timeout: 20_000 });
}

/** The page as tall as it is, for a picture of all of it. */
async function wholePage(page, width) {
  const height = await page.evaluate(() => document.querySelector('main')?.scrollHeight ?? 900);
  await page.setViewportSize({ width, height: Math.min(6000, Math.max(900, height + 160)) });
  await page.mouse.move(4, 4);
  await sleep(500);
}

/**
 * The two actions every measurement and picture is taken after:
 *   · Windows: "Test now" with no account, then "Save and apply" switched on
 *     with no account — refused on the page, nothing sent;
 *   · Linux (PAM): switched on, a test account typed, "Save and apply" — the
 *     server runs the test, it fails at a setup step, nothing is saved.
 */
async function act(page) {
  const win = page.getByTestId('auth-provider-windows');
  await win.getByTestId('auth-provider-test-button-windows').click();
  await win.getByTestId('auth-provider-test-windows').waitFor({ timeout: 20_000 });
  await win.locator('button#auth-provider-enabled-windows').click();
  await win.getByTestId('auth-provider-save-windows').click();
  await win.getByTestId('auth-provider-refusal-windows').waitFor({ timeout: 10_000 });

  const pam = page.getByTestId('auth-provider-pam');
  await pam.locator('button#auth-provider-enabled-pam').click();
  await pam.locator('input[name="auth-test-account-pam-username"]').fill(TEST_ACCOUNT.username);
  await pam.locator('input[name="auth-test-account-pam-password"]').fill(TEST_ACCOUNT.password);
  await pam.getByTestId('auth-provider-save-pam').click();
  await pam.getByTestId('auth-provider-refusal-pam').waitFor({ timeout: 30_000 });
  await pam.getByTestId('auth-provider-test-pam').waitFor({ timeout: 10_000 });
}

/** What the page must hold after act(), whatever the language. */
async function checkState(page) {
  // The password is gone the moment its request was answered.
  const left = await page.locator('input[name="auth-test-account-pam-password"]').inputValue();
  if (left !== '') throw new Error('the PAM test account password box still holds a value after its request was answered');
  // No "switch on anyway" for an operating-system provider.
  const asked = await page.locator('dialog[open]').count();
  if (asked !== 0) throw new Error('a confirmation dialog opened for an operating-system provider');
  // The failed step of the PAM test, and on a Linux host its fix as code.
  const failed = page.getByTestId('auth-provider-test-pam').locator('li[data-status="fail"]');
  if ((await failed.count()) !== 1) throw new Error(`the PAM test shows ${await failed.count()} failed steps, expected the one it stopped at`);
  const step = await failed.getAttribute('data-testid');
  if (ON_WINDOWS && step !== 'auth-provider-check-platform') throw new Error(`on Windows the PAM test should stop at "platform", it stopped at ${step}`);
  if (!ON_WINDOWS && step === 'auth-provider-check-pamtester' && (await failed.locator('code').count()) === 0) {
    throw new Error('the pamtester step does not show its install command as code');
  }
  const winFailed = await page.getByTestId('auth-provider-test-windows').locator('li[data-status="fail"]').getAttribute('data-testid');
  const wantWin = ON_WINDOWS ? 'auth-provider-check-test_account' : 'auth-provider-check-unsupported_os';
  if (winFailed !== wantWin) throw new Error(`the Windows test stopped at ${winFailed}, expected ${wantWin}`);
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    const { providers } = await admin.json('/api/admin/auth-providers');
    for (const name of ['windows', 'pam']) {
      const p = providers.find((x) => x.name === name);
      if (!p || !p.managed || !p.test_account_required) throw new Error(`${name} is not a page provider that asks for a test account: ${JSON.stringify(p)}`);
    }

    const ctx = await newContext(browser, { height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);

    // ── measure: both widths, both languages ────────────────────────────────
    const problems = [];
    for (const locale of ['en', 'tr']) {
      await setLanguage(admin, page, locale);
      for (const width of [958, 1440]) {
        await openPage(page, inst.url, width);
        await act(page);
        await checkState(page);
        for (const p of await layoutProblems(page)) problems.push(`${locale} ${width}px: ${p}`);
        if (locale === 'tr') {
          await mustSay(page.locator('main'), 'the Turkish page', [
            'Windows hesabı', 'Linux hesabı (PAM)', 'Deneme hesabı', 'Kullanıcı adı', 'Parola', 'süper yönetici',
            'pamtester yolu', 'Aynı anda oturum açma sayısı', 'Kaydet ve uygula',
          ]);
        }
      }
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('no overflow or overlap at 958 and 1440 px, in English and Turkish');

    // ── pictures, English ───────────────────────────────────────────────────
    await setLanguage(admin, page, 'en');
    for (const width of [1440, 958]) {
      await openPage(page, inst.url, width);
      await act(page);
      await checkState(page);
      await mustSay(page.locator('main'), 'the Identity providers page', [
        'Windows account', 'Linux account (PAM)', 'Test account', 'Username', 'Password', 'super administrator',
        'pamtester path', 'Sign-ins at the same time', 'Save and apply',
      ]);
      await mustSay(page.getByTestId('auth-provider-refusal-windows'), 'the Windows card', ['Give the test account']);
      await mustSay(page.getByTestId('auth-provider-refusal-pam'), 'the PAM card', ['was not switched on']);
      await wholePage(page, width);
      await shot(page, SET, `auth-providers-${width}.png`);
      if (width === 1440) {
        await shot(page.getByTestId('auth-provider-windows'), SET, 'auth-provider-windows-1440.png');
        await shot(page.getByTestId('auth-provider-pam'), SET, 'auth-provider-pam-1440.png');
      }
    }
    log(`identity providers page (1440 and 958), the Windows and Linux (PAM) cards — ${ON_WINDOWS ? 'on Windows' : 'on Linux'}`);
    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
