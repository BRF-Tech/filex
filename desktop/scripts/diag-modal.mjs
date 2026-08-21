// Measures WHY the share dialog renders unstyled inside the desktop shell.
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

const SERVER = process.env.FILEX_SERVER;
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'diag-'));
const home = fs.mkdtempSync(path.join(os.tmpdir(), 'diaghome-'));

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
  body: JSON.stringify({ state: u.searchParams.get('desktop_state'), challenge: u.searchParams.get('desktop_challenge'), label: 'diag' }),
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
await w.waitForTimeout(500);

// Open the share dialog through the toolbar (select + click the button).
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
await w.waitForTimeout(1500);

const report = await w.evaluate(() => {
  const styles = [...document.querySelectorAll('style')];
  const sheets = [...document.styleSheets];

  // Find the dialog root by its heading.
  const heading = [...document.querySelectorAll('*')].find(
    (e) => e.children.length === 0 && /^Payla[sş] \/ [İI]zinler$/.test(e.textContent?.trim() ?? ''));
  let root = heading;
  for (let i = 0; i < 6 && root?.parentElement; i++) root = root.parentElement;

  const classesOf = (el) => (el?.className?.toString?.() ?? '');
  const cs = heading ? getComputedStyle(heading) : null;

  // Does ANY loaded stylesheet define one of the dialog's classes?
  const wanted = classesOf(root).split(/\s+/).filter(Boolean).slice(0, 6);
  const found = {};
  for (const cls of wanted) {
    found[cls] = false;
    for (const sh of sheets) {
      let rules = [];
      try { rules = [...sh.cssRules]; } catch { continue; }
      if (rules.some((r) => r.selectorText?.includes('.' + cls))) { found[cls] = true; break; }
    }
  }

  return {
    styleTags: styles.length,
    styleTagSizes: styles.map((s) => s.textContent.length),
    sheetCount: sheets.length,
    sheetHrefs: sheets.map((s) => s.href).filter(Boolean),
    dialogParentTag: root?.parentElement?.tagName,
    dialogParentIsBody: root?.parentElement === document.body,
    dialogClasses: classesOf(root).slice(0, 200),
    headingFontSize: cs?.fontSize,
    headingFontWeight: cs?.fontWeight,
    classesDefinedInCss: found,
    // Is the dialog inside <filex-explorer> or outside it?
    insideExplorer: !!root?.closest?.('filex-explorer'),
  };
});

console.log(JSON.stringify(report, null, 1));
await app.close().catch(() => {});
