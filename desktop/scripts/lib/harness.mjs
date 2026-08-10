// Shared rig for the desktop end-to-end scripts.
//
// Every one of these scripts has to do the same four things before it can
// measure anything: find Playwright inside the workspace, launch the app
// against a throwaway profile, play the browser's half of the sign-in, and
// report pass/fail. That preamble was copied into each script, so a fix to it
// (the locale pin below is one) landed in whichever copy happened to be open.
//
// ⚠ No OS-level input anywhere. Playwright talks to the renderer; the operator
// keeps their mouse and keyboard.

import fs, { readdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
export const DESKTOP = path.resolve(__dirname, '..', '..');
export const REPO = path.resolve(DESKTOP, '..');
export const SHOTS = path.join(REPO, 'shots');

export const SERVER = process.env.FILEX_SERVER ?? 'https://fm.brf.sh';
export const EMAIL = process.env.FILEX_EMAIL ?? '';
export const PASSWORD = process.env.FILEX_PASSWORD ?? '';
export const STORAGE = process.env.FILEX_STORAGE ?? 'docs';

const PNPM = path.join(REPO, 'node_modules/.pnpm');
const pwDir = readdirSync(PNPM).find((d) => d.startsWith('playwright-core@'));
if (!pwDir) throw new Error('playwright-core is not installed — run pnpm install at the repo root');
export const { _electron } = await import(
  pathToFileURL(path.join(PNPM, pwDir, 'node_modules/playwright-core/index.mjs')).href
);

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let failed = 0;
export function check(name, ok, detail = '') {
  if (!ok) failed++;
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? `  — ${detail}` : ''}`);
  return ok;
}
export function failures() {
  return failed;
}
/** Prints the tally and exits with a shell-meaningful status. */
export function finish() {
  console.log(`\n==== ${failed === 0 ? 'ALL PASSED' : `${failed} FAILED`} ====`);
  process.exit(failed === 0 ? 0 : 1);
}

export async function api(pathname, init = {}, token) {
  return fetch(`${SERVER}${pathname}`, {
    ...init,
    headers: { ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(init.headers ?? {}) },
  });
}

/**
 * Launches the app against a throwaway Electron profile and a throwaway HOME.
 *
 * ⚠ Both are mandatory, for two different reasons. The profile keeps the
 * single-instance lock separate, so a run never hijacks or kills the app the
 * operator has open; the HOME keeps the sync engine's `~/.filex/sync` away from
 * their real pairings — the engine deletes files.
 *
 * ⚠ FILEX_CLI is deliberately NOT set: handing the app the path to its own
 * engine is how the suite stayed green while the app said "the sync engine is
 * missing" when launched normally.
 *
 * ⚠ The locale is PINNED. The app follows the OS language now, so on a Turkish
 * machine every text assertion in these scripts would compare English strings
 * against a Turkish UI and fail for the wrong reason. Pass `lang` to test the
 * other side of that on purpose.
 */
export async function launchApp({ env = {}, lang = 'en-US', args = [] } = {}) {
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-e2e-'));
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-home-'));
  const packaged = process.env.FILEX_APP_BINARY;
  const common = [`--user-data-dir=${profile}`, `--lang=${lang}`, ...args];
  const app = await _electron.launch({
    ...(packaged
      ? { executablePath: packaged, args: common }
      : { args: [DESKTOP, ...common], cwd: DESKTOP }),
    env: { ...process.env, FILEX_NO_BROWSER: '1', HOME: home, USERPROFILE: home, ...env },
  });
  if (packaged) console.log(`(driving the PACKAGED app: ${packaged})`);
  return { app, profile, home };
}

/**
 * Plays the browser's half of the sign-in over HTTP and feeds the code back
 * through the app's own manual-entry box.
 *
 * The manual path is used deliberately: it exercises the same PKCE exchange as
 * the deep link AND proves the escape hatch a user falls back to when the deep
 * link silently does nothing.
 *
 * Returns the app window plus an admin bearer for seeding fixtures.
 */
export async function signIn(app, { label = 'filex desktop — e2e' } = {}) {
  if (!EMAIL || !PASSWORD) throw new Error('set FILEX_EMAIL and FILEX_PASSWORD');
  const connect = await app.firstWindow();
  await connect.waitForLoadState('domcontentloaded');
  await connect.locator('#server').fill(SERVER);
  await connect.locator('#go').click();
  await connect.locator('#authurl').waitFor({ timeout: 15_000 });
  const authUrl = await connect.locator('#authurl').inputValue();

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
  const adminToken = (await login.clone().json().catch(() => ({}))).token ?? null;

  const done = await fetch(`${SERVER}/api/auth/desktop/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify({ state, challenge, label }),
  });
  if (!done.ok) throw new Error(`complete failed (${done.status}): ${(await done.text()).slice(0, 160)}`);
  const { code } = await done.json();

  // A successful exchange CLOSES the connect window, so this click never
  // resolves. That is the success path, not a failure.
  await connect.locator('#code').fill(code);
  await connect.locator('#usecode').click().catch(() => {});

  const win = await app.waitForEvent('window', { timeout: 60_000 });
  // The window event fires at CREATION, while the document is still Electron's
  // blank one (origin "null"). Wait for the real navigation before sampling.
  await win.waitForURL(/^app:\/\/filex/, { timeout: 30_000 }).catch(() => {});
  await win.waitForLoadState('domcontentloaded');

  // ⚠⚠ Stop here if the window did not load. The preload runs on Chromium's
  // error page too, so `window.filexApp` exists there and no password field is
  // visible — every check after this would report PASS against a window showing
  // nothing at all. Measured: a missing web bundle produced 6 green checks on
  // chrome-error://chromewebdata/.
  const origin = await win.evaluate(() => location.origin);
  if (origin !== 'app://filex') {
    throw new Error(`the explorer never loaded (${win.url()}) — run \`pnpm run build\` first`);
  }
  return { win, adminToken, authUrl };
}

/** Dismisses the explorer's first-run tour, which otherwise swallows clicks. */
export async function skipTour(win) {
  await win.evaluate(() => {
    const b = [...document.querySelectorAll('button')].find((x) =>
      /Turu atla|Skip the tour|Skip/i.test(x.textContent ?? ''));
    b?.click();
  });
  await win.waitForTimeout(300);
}
