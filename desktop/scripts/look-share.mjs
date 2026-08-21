// Photographs the LINK tab of the existing share dialog and reports whether a
// native-share control is there — and whether the API it needs even exists in
// this shell.
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

const SERVER = process.env.FILEX_SERVER;
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'ls-'));
const home = fs.mkdtempSync(path.join(os.tmpdir(), 'lshome-'));

const app = await _electron.launch({
  ...(process.env.FILEX_APP_BINARY
    ? { executablePath: process.env.FILEX_APP_BINARY, args: [`--user-data-dir=${profile}`] }
    : { args: [DESKTOP, `--user-data-dir=${profile}`], cwd: DESKTOP }),
  env: { ...process.env, FILEX_NO_BROWSER: '1', HOME: home, USERPROFILE: home },
});

const c = await app.firstWindow();
await c.waitForLoadState('domcontentloaded');
await c.locator('#server').fill(SERVER);
await c.locator('#go').click();
await c.locator('#authurl').waitFor({ timeout: 15000 });
const u = new URL(await c.locator('#authurl').inputValue());
const login = await fetch(`${SERVER}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ email: process.env.FILEX_EMAIL, password: process.env.FILEX_PASSWORD, remember: true }),
});
const cookie = (login.headers.getSetCookie?.() ?? []).map((x) => x.split(';')[0]).join('; ');
const done = await fetch(`${SERVER}/api/auth/desktop/complete`, {
  method: 'POST', headers: { 'Content-Type': 'application/json', Cookie: cookie },
  body: JSON.stringify({ state: u.searchParams.get('desktop_state'), challenge: u.searchParams.get('desktop_challenge'), label: 'ls' }),
});
await c.locator('#code').fill((await done.json()).code);
await c.locator('#usecode').click().catch(() => {});

const w = await app.waitForEvent('window', { timeout: 60000 });
await w.waitForURL(/^app:\/\/filex/).catch(() => {});
await w.waitForTimeout(6000);
await w.evaluate(() => {
  const s = [...document.querySelectorAll('button')].find((b) => /Turu atla|Skip/i.test(b.textContent ?? ''));
  s?.click();
});
await w.waitForTimeout(400);

// Does the shell even have the API the native-share button depends on?
const api = await w.evaluate(() => ({
  hasShare: typeof navigator.share === 'function',
  hasCanShare: typeof navigator.canShare === 'function',
  secure: window.isSecureContext,
  clipboard: typeof navigator.clipboard?.writeText === 'function',
}));
console.log('  web share API in this shell:', JSON.stringify(api));

await w.evaluate(([n, ts]) => (function robustRowEvent(n, types) {
  const byTitle = [...document.querySelectorAll('.fe-list__name, .fe-grid__label')]
    .find((e) => e.getAttribute('title') === n);
  // ⚠ Not textContent === name: the name span also holds the row's badges
  // (search "in content", and the desktop's availability glyph ✓ ◐ ⟳ ☁), so
  // an exact-text match finds nothing and looks like a lost row.
  const row = byTitle ?? [...document.querySelectorAll('*')].find(
    (e) => e.children.length === 0 && (e.textContent ?? '').trim() === n);
  for (const t of types) row?.dispatchEvent(new MouseEvent(t, { bubbles: true, clientX: 300, clientY: 250 }));
})(n, ts), ['sozlesme.pdf', ['click']]);
await w.waitForTimeout(500);
await w.evaluate(() => {
  const b = [...document.querySelectorAll('button')].find((x) => /Payla[sş] \/ [İI]zinler/i.test(x.textContent ?? ''));
  b?.click();
});
await w.waitForTimeout(1200);

// The LINK tab — this is where a shareable URL lives.
await w.evaluate(() => {
  const t = [...document.querySelectorAll('button, [role=tab]')].find((b) => /^Ba[gğ]lant[iı]$|^Link$/i.test(b.textContent?.trim() ?? ''));
  t?.click();
});
await w.waitForTimeout(1200);
await w.screenshot({ path: path.join(SHOTS, '20-share-link-tab.png') });
console.log('  shots/20-share-link-tab.png');

const buttons = await w.evaluate(() => {
  const m = document.querySelector('.fx-perm-modal');
  return m ? [...m.querySelectorAll('button')].map((b) => b.textContent.trim()).filter(Boolean) : [];
});
console.log('  buttons in the dialog:', JSON.stringify(buttons));

await app.close().catch(() => {});
