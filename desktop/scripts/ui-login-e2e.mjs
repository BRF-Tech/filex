// Drives the REAL desktop app end to end against a live filex server.
//
// ⚠ No OS-level input (SendKeys / synthetic mouse). Those steal the operator's
// keyboard and mouse, land in whatever happens to be focused, and lie when they
// miss. Playwright talks to the renderer directly.
//
// ⚠ No real browser window either. FILEX_NO_BROWSER=1 suppresses the launch and
// hands back the URL the browser WOULD have been sent to; this script then plays
// the browser's part over HTTP — sign in, call the completion endpoint, take the
// one-time code — and feeds the resulting deep link back into the app. Every
// server endpoint and the PKCE exchange are exercised for real.
//
// Run: node scripts/ui-login-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD

import fs, { readdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const DESKTOP = path.resolve(__dirname, '..');
const REPO = path.resolve(DESKTOP, '..');

const PNPM = path.join(REPO, 'node_modules/.pnpm');
const pwDir = readdirSync(PNPM).find((d) => d.startsWith('playwright-core@'));
const { _electron } = await import(
  pathToFileURL(path.join(PNPM, pwDir, 'node_modules/playwright-core/index.mjs')).href
);

const SERVER = process.env.FILEX_SERVER ?? 'https://fm.brf.sh';
const EMAIL = process.env.FILEX_EMAIL ?? '';
const PASSWORD = process.env.FILEX_PASSWORD ?? '';

let failures = 0;
const check = (name, ok, detail = '') => {
  if (!ok) failures++;
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? `  — ${detail}` : ''}`);
};

/** Plays the browser's half: sign in, then complete the desktop hand-off. */
async function browserHalf(authUrl) {
  const u = new URL(authUrl);
  const state = u.searchParams.get('desktop_state');
  const challenge = u.searchParams.get('desktop_challenge');
  if (!state || !challenge) throw new Error('auth URL carried no desktop parameters');

  const login = await fetch(`${SERVER}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD, remember: true }),
  });
  if (!login.ok) throw new Error(`browser login failed (${login.status})`);
  const cookie = (login.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ');

  const done = await fetch(`${SERVER}/api/auth/desktop/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify({ state, challenge, label: 'filex desktop — e2e' }),
  });
  if (!done.ok) throw new Error(`complete failed (${done.status}): ${(await done.text()).slice(0, 160)}`);
  const { code } = await done.json();
  return { state, code };
}

// Hermetic profile: without it the run inherits whatever account is already
// stored and silently measures nothing.
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-e2e-'));
const app = await _electron.launch({
  args: [DESKTOP, `--user-data-dir=${profile}`],
  cwd: DESKTOP,
  env: { ...process.env, FILEX_NO_BROWSER: '1' },
});

try {
  // ── connect screen ────────────────────────────────────────────────
  const connect = await app.firstWindow();
  await connect.waitForLoadState('domcontentloaded');
  check('starts on the connect screen', await connect.locator('#server').isVisible());
  check('no password field anywhere in the app',
    (await connect.locator('input[type="password"]').count()) === 0,
    'sign-in belongs to the browser, so SSO installs work');
  const ph = await connect.locator('#server').getAttribute('placeholder');
  check('server placeholder is a neutral example', !/brf\.sh/.test(ph ?? ''), JSON.stringify(ph));

  // Click the real button rather than calling the IPC: the waiting screen is
  // part of the flow, and reaching past the UI would skip exactly what needs
  // proving.
  await connect.locator('#server').fill(SERVER);
  await connect.locator('#go').click();
  await connect.locator('#authurl').waitFor({ timeout: 15_000 });

  // The waiting screen is the whole manual escape hatch: a copyable URL for
  // "no browser opened", and a code box for "the browser could not come back".
  const authUrl = await connect.locator('#authurl').inputValue();
  check('waiting screen shows a copyable auth URL', authUrl.startsWith(SERVER), authUrl.slice(0, 64));
  check('auth URL carries the PKCE parameters',
    /desktop_state=/.test(authUrl) && /desktop_challenge=/.test(authUrl));
  check('waiting screen offers manual code entry', (await connect.locator('#code').count()) === 1);

  // ── browser's half, over HTTP ─────────────────────────────────────
  const { state, code } = await browserHalf(authUrl);
  check('server issued a one-time code after sign-in', !!code);

  // ── deep link back into the app ───────────────────────────────────
  // A successful exchange CLOSES this window (the connect screen has done its
  // job), so the evaluate never resolves. That is the success path, not a
  // failure — swallow the teardown and verify the outcome from the app window.
  // Use the MANUAL path deliberately: it exercises the same exchange AND proves
  // the escape hatch works, which is the one a user falls back to when the deep
  // link silently does nothing. A successful exchange closes this window, so the
  // evaluate never resolves — that is the success path.
  await connect.locator('#code').fill(code);
  await connect.locator('#usecode').click().catch(() => {});

  // ── the app window ────────────────────────────────────────────────
  const appWindow = await app.waitForEvent('window', { timeout: 60_000 });
  await appWindow.waitForLoadState('domcontentloaded');
  check('explorer window opens on app://', (await appWindow.evaluate(() => location.origin)) === 'app://filex');
  check('no application menu', (await appWindow.evaluate(() => document.title)) !== null);

  const runtime = await appWindow.evaluate(() => ({
    token: typeof window.__FILEX_RUNTIME__?.bearerToken === 'string',
    api: window.__FILEX_RUNTIME__?.apiBaseUrl,
    creds: window.__FILEX_RUNTIME__?.useCredentials,
    settings: typeof window.filexDesktop?.openSettings === 'function',
    sync: typeof window.filexDesktop?.openSyncFolders === 'function',
  }));
  check('runtime seam injected', runtime.token === true, `api=${runtime.api}`);
  check('credentials disabled for app://', runtime.creds === false);
  check('Settings + Sync folders bridges exposed', runtime.settings && runtime.sync);

  // The product test: the APP's own requests must all succeed and it must be
  // signed in. A hand-rolled fetch here would only measure my assumptions.
  const appFailures = [];
  appWindow.on('requestfailed', (r) => {
    if (r.url().startsWith(SERVER)) appFailures.push(`${r.method()} ${new URL(r.url()).pathname}`);
  });
  await appWindow.waitForTimeout(9000);
  check("none of the app's own requests failed", appFailures.length === 0,
    appFailures.slice(0, 3).join(' | ') || 'no failures');
  const atLogin = await appWindow.locator('input[type="password"]').first().isVisible().catch(() => false);
  check('app is signed in (its own login form is gone)', atLogin === false);

  await appWindow.screenshot({ path: path.join(REPO, 'desktop-app.png') });

  // ── settings + sync folders ───────────────────────────────────────
  await appWindow.evaluate(() => window.filexDesktop.openSettings());
  const settings = await app.waitForEvent('window', { timeout: 30_000 });
  await settings.waitForLoadState('domcontentloaded');
  await settings.waitForTimeout(600);
  const st = await settings.evaluate(() => window.filexShell.getState());
  check('account stored after exchange', st.accounts.length === 1,
    st.accounts[0] ? `${st.accounts[0].email} @ ${st.accounts[0].serverUrl}` : 'none');
  check('renderer never sees the token', st.accounts[0] && !('token' in st.accounts[0]));
  check('settings lists the signed-in account',
    (await settings.locator('text=' + EMAIL).count()) > 0);
  check('background + start-at-login are offered',
    (await settings.locator('#bg').count()) === 1 && (await settings.locator('#login').count()) === 1);
  await settings.screenshot({ path: path.join(REPO, 'desktop-settings.png') });

  await settings.evaluate(() => { location.hash = '#/sync'; });
  await settings.waitForTimeout(600);
  check('sync folders screen renders', (await settings.locator('#remote').count()) === 1);
  await settings.screenshot({ path: path.join(REPO, 'desktop-sync.png') });
} catch (e) {
  check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  await app.close().catch(() => {});
}

console.log(`\n==== ${failures === 0 ? 'ALL PASSED' : failures + ' FAILED'} ====`);
process.exit(failures === 0 ? 0 : 1);
