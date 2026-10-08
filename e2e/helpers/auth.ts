import type { Page, APIRequestContext, Request } from '@playwright/test';

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
 * Log in via the Vue admin form. Lands on the account's start page on success:
 * Home for everyone by default, the dashboard for an admin who picked it.
 *
 * The login page in OIDC-enabled builds shows TWO buttons: the local submit
 * and a `Sign in with SSO` redirect, so the submit is picked by
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
  // ⚠ What the browser could not fetch, kept until the form is there. A page
  // that never starts is otherwise a 10 s timeout on an empty screen (task
  // #81), and the reason is only in the trace (bootFailure below).
  const failed: string[] = [];
  const onFailed = (r: Request) => failed.push(`${r.method()} ${r.url()} -> ${r.failure()?.errorText ?? '(no reason)'}`);
  page.on('requestfailed', onFailed);
  try {
    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(email);
  } catch (err) {
    throw bootFailure(err, failed);
  } finally {
    page.off('requestfailed', onFailed);
  }
  await page.getByLabel(/password|parola/i).fill(password);
  // `exact` per name, so neither matches "Sign in with SSO".
  const submit = page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Oturum aç', exact: true }));
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
  //
  // ⚠ The start page, not the dashboard. Since 0.41.0 everybody — admins
  // included — lands on Home unless they chose the dashboard in user settings
  // (web/src/lib/startPage.ts). Waiting for /admin/dashboard timed out on a
  // login that had worked, and every spec behind this helper went red for it.
  await page.waitForURL(/\/admin\/(home|dashboard)([?#]|$)/);
}

/** Network errors that come from the machine's own network stack. */
const HOST_NETWORK_ERRORS = /ERR_NO_BUFFER_SPACE|ERR_ADDRESS_IN_USE|ERR_INSUFFICIENT_RESOURCES/;

/**
 * The error for a sign-in page that did not start, naming what the browser
 * could not fetch.
 *
 * ⚠⚠ Task #81, measured 2026-10-01: the "blank sign-in page, once in a few
 * hundred loads" was a boot file whose connection failed in the OPERATING
 * SYSTEM with `net::ERR_NO_BUFFER_SPACE` (WSAENOBUFS), in the same second as
 * Windows' Tcpip event 4231: every ephemeral TCP port on the machine was in
 * use. The request never left the browser, so the server log has no line for
 * it, and a module that fails to load leaves an empty page. That is the host,
 * not filex: say so instead of timing out on a blank screen. No retry here -
 * a retry would hide the next real boot failure too.
 */
function bootFailure(err: unknown, failed: string[]): unknown {
  if (!failed.length) return err;
  const host = failed.some((f) => HOST_NETWORK_ERRORS.test(f));
  const why = err instanceof Error ? err.message.split('\n')[0] : String(err);
  return new Error(
    [
      `the sign-in page did not start: the browser could not fetch ${failed.length} request(s):`,
      ...failed.map((f) => `  ${f}`),
      ...(host
        ? [
            'This error comes from the HOST, not from filex: the machine ran out of TCP ports for new',
            'connections (Windows: System log, Tcpip event 4231). See e2e/README.md, "A blank sign-in page".',
          ]
        : []),
      `(${why})`,
    ].join('\n'),
  );
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
 * Logs out via the user menu. Asserts redirection back to the sign-in page.
 *
 * ⚠ The sign-in page of the DOOR the page was on: a sign-out on /drive/ lands
 * on /drive/login?signed_out=1, one on /admin/ on /admin/login (web
 * lib/signOut.ts signInPage - the server accepts exactly these two as the
 * IdP's way back). Waiting for /admin/login alone failed 213, which signs out
 * of /drive/explore (0.54 full run 001b652e).
 *
 * The user menu trigger lives in TopNav and isn't tagged with a stable
 * data-testid (the project's test-id strategy is informal). Match by
 * visible avatar/email instead — fall back to clearing cookies + a
 * direct nav if the menu strategy times out (some builds use a slide-
 * out user panel without a click trigger).
 */
export async function logout(page: Page) {
  try {
    // Since 0.41.0 the landing page is the explorer, whose avatar menu is
    // `explore-account` with an `explore-signout` row; the admin pages keep
    // their own top bar menu.
    const trigger = page
      .getByTestId('explore-account')
      .or(page.getByTestId('user-menu-button'))
      .or(page.getByRole('button', { name: /admin@local|profile|user|hesap/i }));
    await trigger.first().click({ timeout: 3_000 });
    await page
      .getByTestId('explore-signout')
      .or(page.getByRole('menuitem', { name: /logout|çıkış|sign out/i }))
      .first()
      .click({ timeout: 2_000 });
  } catch {
    // Last resort. ⚠ The web app authenticates with a bearer kept in
    // sessionStorage as well as the cookie, so clearing cookies alone leaves
    // the session alive and a following "is the session gone?" assertion
    // fails for a reason that has nothing to do with sign-out.
    await page.context().clearCookies();
    await page.evaluate(() => sessionStorage.removeItem('filex.bearer')).catch(() => undefined);
    await page.goto('/admin/login');
  }
  await page.waitForURL(/\/(admin|drive)\/login/);
}
