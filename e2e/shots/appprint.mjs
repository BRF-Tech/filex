// An app prints a PDF through filex (`ui.print`, 0.55, task #189) - the row
// filex shows above an app's interface each time an app asks to print. Its
// Allow is the print page's own button (a frame of `/_print/` in the row:
// only a click in that page opens the print dialog, security review sec055
// S9):
//
//   node e2e/shots/appprint.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/appprint/ (or SHOTS_OUT):
//
//   print-consent-1280.png          a report open in an app's own interface,
//                                   and above it filex's question, in filex's
//                                   words: "Reports wants to print “Q3
//                                   2026.pdf”." - Allow (armed) and Don't allow
//   print-consent-dark-tr-1280.png  the same in Turkish, in the dark theme
//   print-consent-390.png           the same on a 390-px phone
//   print-consent-dark-tr-390.png   the phone, Turkish, dark
//
// It also MEASURES the row in a real browser at 1280 and 390 px, light and
// dark, in English and Turkish: the row and its two buttons inside the
// window, nothing on top of anything. A failed measurement throws. The
// pictures are shown in docs/APP-PLUGINS-API.md → Printing a PDF (the
// `<!-- shot: … -->` lines there become the pictures once they are
// published: scripts/lib/shots-site.mjs → relinkText).
//
// The app is built here: "Reports", an interface-only app (no module) that
// draws a `.report` file and hands filex a PDF with the SDK this tree just
// built (packages/app-ui/dist/filex-app-ui.iife.js → `fx.print`), granted
// `ui:print` by its manifest's `"ui": {"print": true}`. It is never
// published. The scene starts the print from the frame (Playwright, no click
// in it); filex asks; the scene answers "Don't allow" after the picture, so
// nothing is printed and the app is told `cancelled`.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';
import { zipStored } from './fixtures.mjs';
import {
  addLocalStorage,
  bootInstance,
  client,
  dismissToasts,
  documentPDF,
  log,
  mustSay,
  newContext,
  setLanguage,
  shot,
  signIn,
  sleep,
  uploadTree,
} from './scene.mjs';

const SET = 'appprint';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const HERE = dirname(fileURLToPath(import.meta.url));
const SDK_IIFE = join(HERE, '..', '..', 'packages', 'app-ui', 'dist', 'filex-app-ui.iife.js');

const APP = 'reports';
const FOLDER = 'Reports';
const FILE = 'Q3 2026.report';
const PDF_NAME = 'Q3 2026.pdf';
const REPORT = {
  title: 'Sales, third quarter 2026',
  rows: [
    ['Orders', '1,284'],
    ['Revenue', 'EUR 412,600'],
    ['Average order', 'EUR 321'],
    ['Returns', '2.1%'],
    ['New customers', '318'],
  ],
};

/** The PDF the app prints: the same report, one A4 page. */
const PDF = documentPDF([
  [20, REPORT.title],
  [10, 'Northwind Studio Ltd. · Prepared 1 October 2026'],
  [0],
  ...REPORT.rows.map(([k, v]) => [12, `${k}: ${v}`]),
]);

/* ── the app ────────────────────────────────────────────────────────────── */

const INDEX_HTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Reports</title>
    <link rel="stylesheet" href="style.css" />
  </head>
  <body>
    <main id="report" aria-live="polite"><p class="status">Opening…</p></main>
    <script src="filex-app-ui.iife.js"></script>
    <script src="pdf.js"></script>
    <script src="app.js"></script>
  </body>
</html>
`;

const STYLE_CSS = `
:root { color-scheme: light dark; font-family: system-ui, sans-serif; }
body { margin: 0; padding: 24px; background: Canvas; color: CanvasText; }
.head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: 12px; }
h1 { margin: 0; font-size: 20px; }
.print { font: inherit; padding: 6px 14px; border-radius: 6px; border: 1px solid #6366f1; background: #6366f1; color: #fff; }
.rows { margin: 20px 0 0; padding: 0; list-style: none; max-width: 520px; }
.rows li { display: flex; justify-content: space-between; gap: 16px; padding: 10px 0; border-bottom: 1px solid color-mix(in srgb, CanvasText 15%, transparent); }
.rows .k { opacity: .7; }
.rows .v { font-variant-numeric: tabular-nums; font-weight: 600; }
.status { opacity: .7; }
`;

// ⚠ No innerHTML: every word of the report comes from the file, set as text.
const APP_JS = `
(async () => {
  const main = document.getElementById('report');
  const el = (tag, cls, text) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  };
  let fx;
  try {
    fx = await window.FilexAppUI.connect();
  } catch (e) {
    document.body.dataset.error = 'not inside filex';
    return;
  }
  const file = await fx.open();
  const report = JSON.parse(await file.text());
  const pdfName = file.name.replace(/\\.report$/, '') + '.pdf';
  // A fresh buffer every time: the bridge transfers it.
  const pdf = () => Uint8Array.from(atob(window.REPORT_PDF), (c) => c.charCodeAt(0)).buffer;
  window.__print = () => fx.print(pdfName, pdf()).then(
    (result) => ({ result }),
    (e) => ({ error: { code: (e && e.code) || 'failed', message: String((e && e.message) || e) } }),
  );
  const head = el('header', 'head');
  const print = el('button', 'print', 'Print');
  print.type = 'button';
  print.addEventListener('click', () => void window.__print());
  head.append(el('h1', '', report.title), print);
  const list = el('ul', 'rows');
  for (const [k, v] of report.rows) {
    const li = el('li');
    li.append(el('span', 'k', k), el('span', 'v', v));
    list.append(li);
  }
  main.replaceChildren(head, list);
  document.body.dataset.ready = '1';
})().catch((e) => { document.body.dataset.error = String((e && (e.message || e.code)) || e); });
`;

/** The app's ui.zip and its manifest, the zip pinned by SHA-256. */
function packReportsApp() {
  if (!existsSync(SDK_IIFE)) {
    throw new Error(`${SDK_IIFE} is missing - build the packages first (pnpm run build:packages; pnpm shots does)`);
  }
  const zip = zipStored([
    { name: 'app.js', data: APP_JS },
    { name: 'filex-app-ui.iife.js', data: readFileSync(SDK_IIFE) },
    { name: 'index.html', data: INDEX_HTML },
    { name: 'pdf.js', data: `window.REPORT_PDF = ${JSON.stringify(PDF.toString('base64'))};\n` },
    { name: 'style.css', data: STYLE_CSS },
  ]);
  const manifest = {
    manifest_version: 1,
    name: APP,
    version: '1.0.0',
    label: { en: 'Reports', tr: 'Raporlar' },
    description: { en: "Opens a .report file as a one-page summary and prints it. An example app for filex's screenshots; it is not published." },
    permissions: ['files:read'],
    ui: { bundle: { url: 'ui.zip', sha256: createHash('sha256').update(zip).digest('hex') }, print: true },
    views: [{ id: 'report', placement: 'viewer', ui: 'index.html', applies: { ext: ['report'] }, label: { en: 'Reports', tr: 'Raporlar' } }],
  };
  return { zip, manifest };
}

/**
 * Install an interface-only app the way the wizard does: a dry run for the
 * review, then the install granting exactly what the review listed (the
 * manifest's permissions and the ones filex derives from its `ui` block).
 */
async function installInterfaceApp(admin, { zip, manifest }) {
  const form = (grant) => {
    const fd = new FormData();
    fd.append('grant', JSON.stringify({ permissions: grant }));
    fd.append('manifest', new Blob([Buffer.from(JSON.stringify(manifest))]), 'filex-app.json');
    fd.append('ui', new Blob([zip]), 'ui.zip');
    return fd;
  };
  const dry = await admin.call('/api/admin/app-plugins?dry_run=1', { method: 'POST', body: form([]) });
  if (!dry.ok) throw new Error(`the review of ${manifest.name}: ${dry.status} ${(await dry.text()).slice(0, 400)}`);
  const rows = ((await dry.json()).permissions ?? []).map((r) => r.id);
  if (!rows.includes('ui:print')) throw new Error(`the review of ${manifest.name} does not list ui:print: ${rows.join(', ')}`);
  const res = await admin.call('/api/admin/app-plugins', { method: 'POST', body: form(rows) });
  if (!res.ok) throw new Error(`install ${manifest.name}: ${res.status} ${(await res.text()).slice(0, 400)}`);
}

/* ── measuring the row ──────────────────────────────────────────────────── */

/** The four looks a picture is taken in, of the eight the scene measures. */
const LOOKS = [];
for (const scheme of ['light', 'dark']) {
  for (const locale of ['en', 'tr']) {
    for (const width of [1280, 390]) {
      const shoots = (scheme === 'light' && locale === 'en') || (scheme === 'dark' && locale === 'tr');
      LOOKS.push({ scheme, locale, width, height: width === 390 ? 844 : 900, suffix: shoots ? `${scheme === 'dark' ? 'dark-tr-' : ''}${width}` : '' });
    }
  }
}

/** What of the print question does not fit: the row past the window, a part past the row, two parts on each other. */
async function rowProblems(row) {
  return row.evaluate((el) => {
    const out = [];
    const view = window.innerWidth;
    const box = el.getBoundingClientRect();
    if (box.left < -0.5 || box.right > view + 0.5) out.push(`the row sticks out: ${Math.round(box.left)}..${Math.round(box.right)} of ${view}`);
    const parts = [...el.children].map((c) => ({ c, r: c.getBoundingClientRect() })).filter(({ r }) => r.width > 0 && r.height > 0);
    for (const { c, r } of parts) {
      if (r.left < box.left - 0.5 || r.right > box.right + 0.5) {
        out.push(`"${(c.textContent || '').trim().slice(0, 40)}" sticks out of the row: ${Math.round(r.left)}..${Math.round(r.right)}`);
      }
    }
    for (let i = 0; i < parts.length; i++) {
      for (let j = i + 1; j < parts.length; j++) {
        const a = parts[i].r;
        const b = parts[j].r;
        const w = Math.min(a.right, b.right) - Math.max(a.left, b.left);
        const h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
        if (w > 2 && h > 2) out.push(`"${(parts[i].c.textContent || '').trim().slice(0, 24)}" and "${(parts[j].c.textContent || '').trim().slice(0, 24)}" overlap`);
      }
    }
    return out;
  });
}

async function main() {
  const reports = packReportsApp();
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  const seed = mkdtempSync(join(tmpdir(), 'filex-shots-appprint-'));
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    mkdirSync(join(seed, FOLDER), { recursive: true });
    writeFileSync(join(seed, FOLDER, FILE), `${JSON.stringify(REPORT, null, 2)}\n`);
    await addLocalStorage(admin, 'demo', inst.storageRoot('demo'), { onHost: !inst.container });
    await uploadTree(admin, 'demo://', seed);
    await installInterfaceApp(admin, reports);

    const problems = [];
    for (const look of LOOKS) {
      const ctx = await newContext(browser, { scheme: look.scheme, width: look.width, height: look.height });
      const page = await ctx.newPage();
      await signIn(page, inst.url, ADMIN);
      await setLanguage(admin, page, look.locale);
      await page.goto(`${inst.url}/admin/explore?storage=demo`);
      await page.waitForFunction((l) => document.documentElement.lang === l, look.locale, { timeout: 20_000 });
      const folder = page.locator(`[data-fe-path="demo://${FOLDER}"]`).first();
      await folder.waitFor({ timeout: 25_000 });
      await folder.dblclick();
      const row = page.locator(`[data-fe-path="demo://${FOLDER}/${FILE}"]`).first();
      await row.waitFor({ timeout: 15_000 });
      // ⚠ The pane ignores a second open within 500 ms of the last one
      // (FilePane OPEN_GUARD_MS): the folder above opened just now.
      await sleep(700);
      await row.dblclick();
      const appframe = page.locator(`.fe-appframe[data-app="${APP}"]`);
      await page.locator(`.fe-appframe[data-app="${APP}"][data-connected="true"]`).waitFor({ timeout: 20_000 });
      const frame = await (await appframe.locator('iframe').first().elementHandle()).contentFrame();
      if (!frame) throw new Error('the app frame has no document');
      await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error, null, { timeout: 20_000 });
      const failed = await frame.evaluate(() => document.body.dataset.error ?? null);
      if (failed) throw new Error(`the Reports interface did not start: ${failed}`);

      // The app asks to print: filex asks, every time.
      const asked = frame.evaluate(() => window.__print());
      const consent = page.locator('[data-testid="appframe-print-ask"][data-shown="true"]');
      await consent.waitFor({ state: 'visible', timeout: 15_000 });
      await mustSay(consent, 'the print question', [PDF_NAME]);
      // "Allow" is the print page's own button, in a frame in the row; it is
      // armed a moment after the question shows (AppFrame CONSENT_ARM_MS):
      // the picture is of the question a person can answer.
      const allow = page.frameLocator('iframe[data-testid="appframe-print"]').getByRole('button');
      await allow.waitFor({ state: 'visible', timeout: 15_000 });
      for (let i = 0; i < 40 && (await allow.isDisabled()); i++) await sleep(100);
      if (await allow.isDisabled()) throw new Error('"Allow" stays disabled under the print question');
      for (const x of await rowProblems(consent)) problems.push(`${look.scheme} ${look.locale} ${look.width}px: ${x}`);
      if (look.suffix) {
        await dismissToasts(page);
        await page.mouse.move(2, 2);
        await sleep(500);
        await shot(page, SET, `print-consent-${look.suffix}.png`);
      }
      await page.getByTestId('appframe-print-deny').click();
      const answer = await asked;
      if (answer?.error?.code !== 'cancelled') throw new Error(`"Don't allow" should tell the app cancelled, it was told ${JSON.stringify(answer)}`);
      await setLanguage(admin, page, 'en');
      await ctx.close();
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('the print question fits at 1280 and 390 px, light and dark, in English and Turkish; "Don\'t allow" tells the app cancelled');
  } finally {
    await browser.close();
    await inst.stop();
    rmSync(seed, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
