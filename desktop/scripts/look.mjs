// Opens the app and PHOTOGRAPHS it — the rail, the share dialog, settings.
//
// This exists because the functional suites all passed while the thing looked
// bad. "18/18" answers "does it work", never "is this presentable". Screenshots
// are the only honest answer to the second question, and they have to be looked
// at by a person.
//
// Run: node scripts/look.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD, FILEX_APP_BINARY (optional)

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

const SERVER = process.env.FILEX_SERVER ?? 'https://fm.example.com';
const EMAIL = process.env.FILEX_EMAIL ?? '';
const PASSWORD = process.env.FILEX_PASSWORD ?? '';

async function browserHalf(authUrl) {
  const u = new URL(authUrl);
  const login = await fetch(`${SERVER}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD, remember: true }),
  });
  const cookie = (login.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ');
  const done = await fetch(`${SERVER}/api/auth/desktop/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify({
      state: u.searchParams.get('desktop_state'),
      challenge: u.searchParams.get('desktop_challenge'),
      label: 'filex desktop — look',
    }),
  });
  return (await done.json()).code;
}

const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-look-'));
const fakeHome = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-home-'));
const packaged = process.env.FILEX_APP_BINARY;

const app = await _electron.launch({
  ...(packaged
    ? { executablePath: packaged, args: [`--user-data-dir=${profile}`] }
    : { args: [DESKTOP, `--user-data-dir=${profile}`], cwd: DESKTOP }),
  env: { ...process.env, FILEX_NO_BROWSER: '1', HOME: fakeHome, USERPROFILE: fakeHome },
});

const shot = async (page, name) => {
  await page.screenshot({ path: path.join(SHOTS, `${name}.png`) });
  console.log(`  shots/${name}.png`);
};

try {
  const connect = await app.firstWindow();
  await connect.waitForLoadState('domcontentloaded');
  await shot(connect, '01-connect');

  await connect.locator('#server').fill(SERVER);
  await connect.locator('#go').click();
  await connect.locator('#authurl').waitFor({ timeout: 15_000 });
  await shot(connect, '02-waiting');

  const code = await browserHalf(await connect.locator('#authurl').inputValue());
  await connect.locator('#code').fill(code);
  await connect.locator('#usecode').click().catch(() => {});

  const win = await app.waitForEvent('window', { timeout: 60_000 });
  await win.waitForURL(/^app:\/\/filex/, { timeout: 30_000 }).catch(() => {});
  await win.waitForLoadState('domcontentloaded');
  await win.waitForTimeout(6000);

  // Dismiss the explorer's onboarding tour — it is not what we are looking at.
  await win.evaluate(() => {
    const skip = [...document.querySelectorAll('button')].find((b) =>
      /Turu atla|Skip/i.test(b.textContent ?? ''));
    skip?.click();
  });
  await win.waitForTimeout(800);
  await shot(win, '03-files');

  // The rail on its own, magnified by cropping.
  const rail = await win.locator('#rail').boundingBox();
  if (rail) {
    await win.screenshot({
      path: path.join(SHOTS, '04-rail.png'),
      clip: { x: 0, y: 0, width: Math.ceil(rail.width) + 8, height: Math.min(400, Math.ceil(rail.height)) },
    });
    console.log('  shots/04-rail.png');
  }

  // The share dialog, opened the way a user opens it: select a file, then the
  // toolbar/context action.
  await win.evaluate(() => {
    const row = [...document.querySelectorAll('*')].find((e) =>
      e.textContent?.trim() === 'sozlesme.pdf');
    row?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
  await win.waitForTimeout(600);
  await win.evaluate(() => {
    const row = [...document.querySelectorAll('*')].find((e) =>
      e.textContent?.trim() === 'sozlesme.pdf');
    row?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: 300, clientY: 250 }));
  });
  await win.waitForTimeout(900);
  await shot(win, '05-context-menu');

  const shareItem = await win.evaluate(() => {
    const it = [...document.querySelectorAll('*')].filter((e) =>
      e.children.length === 0 && /payla|share|eriş|access/i.test(e.textContent ?? ''));
    return it.map((e) => e.textContent.trim()).slice(0, 12);
  });
  console.log('  context menu share-ish entries:', JSON.stringify(shareItem));

  await win.evaluate(() => {
    const it = [...document.querySelectorAll('*')].find((e) =>
      e.children.length === 0 && /^(Payla[sş]|Share|Eri[sş]im|Access)/i.test(e.textContent?.trim() ?? ''));
    it?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
  await win.waitForTimeout(1500);
  await shot(win, '06-share');

  await win.evaluate(() => document.querySelectorAll('#rail .rail-btn')[1].click());
  await win.waitForTimeout(700);
  await shot(win, '07-settings');
} catch (e) {
  console.log('FAILED:', String(e && e.message).split('\n')[0]);
} finally {
  await app.close().catch(() => {});
}
console.log('\nlook at the files in shots/ — that is the point of this script.');
