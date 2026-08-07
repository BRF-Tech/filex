// Photographs the rail with MORE THAN ONE account — the case the rail exists
// for. One account makes any rail look like a stray square in a margin.
//
// Run: node scripts/look-multi.mjs
// Env: FILEX_SERVER_A/EMAIL_A/PASSWORD_A, FILEX_SERVER_B/EMAIL_B/PASSWORD_B

import fs, { readdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const DESKTOP = path.resolve(__dirname, '..');
const REPO = path.resolve(DESKTOP, '..');
const SHOTS = path.join(REPO, 'shots');
fs.mkdirSync(SHOTS, { recursive: true });

const PNPM = path.join(REPO, 'node_modules/.pnpm');
const pwDir = readdirSync(PNPM).find((d) => d.startsWith('playwright-core@'));
const { _electron } = await import(
  pathToFileURL(path.join(PNPM, pwDir, 'node_modules/playwright-core/index.mjs')).href
);

const A = { url: process.env.FILEX_SERVER_A, email: process.env.FILEX_EMAIL_A, pw: process.env.FILEX_PASSWORD_A };
const B = { url: process.env.FILEX_SERVER_B, email: process.env.FILEX_EMAIL_B, pw: process.env.FILEX_PASSWORD_B };

async function codeFor(server, email, pw, authUrl) {
  const u = new URL(authUrl);
  const login = await fetch(`${server}/api/auth/login`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password: pw, remember: true }),
  });
  const cookie = (login.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ');
  const done = await fetch(`${server}/api/auth/desktop/complete`, {
    method: 'POST', headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify({
      state: u.searchParams.get('desktop_state'),
      challenge: u.searchParams.get('desktop_challenge'),
      label: 'filex desktop — look-multi',
    }),
  });
  return (await done.json()).code;
}

/** Runs one sign-in on whatever connect window is currently open. */
async function signIn(page, acct) {
  await page.waitForLoadState('domcontentloaded');
  await page.locator('#server').fill(acct.url);
  await page.locator('#go').click();
  await page.locator('#authurl').waitFor({ timeout: 15_000 });
  const code = await codeFor(acct.url, acct.email, acct.pw, await page.locator('#authurl').inputValue());
  await page.locator('#code').fill(code);
  await page.locator('#usecode').click().catch(() => {});
}

const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-multi-'));
const home = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-home-'));
const app = await _electron.launch({
  ...(process.env.FILEX_APP_BINARY
    ? { executablePath: process.env.FILEX_APP_BINARY, args: [`--user-data-dir=${profile}`] }
    : { args: [DESKTOP, `--user-data-dir=${profile}`], cwd: DESKTOP }),
  env: { ...process.env, FILEX_NO_BROWSER: '1', HOME: home, USERPROFILE: home },
});

try {
  await signIn(await app.firstWindow(), A);

  const win = await app.waitForEvent('window', { timeout: 60_000 });
  await win.waitForURL(/^app:\/\/filex/, { timeout: 30_000 }).catch(() => {});
  await win.waitForTimeout(5000);
  await win.evaluate(() => {
    const s = [...document.querySelectorAll('button')].find((b) => /Turu atla|Skip/i.test(b.textContent ?? ''));
    s?.click();
  });

  // Second account, through the rail's own "+" — the real path.
  await win.evaluate(() => document.querySelector('#rail .rail-btn').click());
  const connect2 = await app.waitForEvent('window', { timeout: 30_000 });
  await signIn(connect2, B);
  await win.waitForTimeout(4000);

  const accounts = await win.evaluate(() => document.querySelectorAll('#rail .avatar').length);
  console.log(`  rail shows ${accounts} account(s)`);

  await win.screenshot({ path: path.join(SHOTS, '10-multi-files.png') });
  console.log('  shots/10-multi-files.png');

  const rail = await win.locator('#rail').boundingBox();
  if (rail) {
    await win.screenshot({
      path: path.join(SHOTS, '11-multi-rail.png'),
      clip: { x: 0, y: 0, width: Math.ceil(rail.width) + 10, height: 300 },
    });
    console.log('  shots/11-multi-rail.png');
  }

  // Switch to the other account and prove the explorer follows.
  await win.evaluate(() => document.querySelectorAll('#rail .avatar')[0].click());
  await win.waitForTimeout(4000);
  await win.screenshot({ path: path.join(SHOTS, '12-switched.png') });
  console.log('  shots/12-switched.png');
} catch (e) {
  console.log('FAILED:', String(e && e.message).split('\n')[0]);
} finally {
  await app.close().catch(() => {});
}
