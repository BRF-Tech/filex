/**
 * 229-app-print-encrypted — what an office editor needs from filex besides
 * its sandbox's doors (task #189, platform slice P1b). In every engine
 * `E2E_BROWSERS` names (`chromium,firefox,webkit`):
 *
 *   1. `ui.print` → `ui:print`: the review lists it, in the administrator's
 *      words, only for the app that asks;
 *   2. the print page (`/_print/`) keeps a policy of its own: it frames the
 *      `blob:` PDF it makes and nothing of filex or the network, and only
 *      filex, the desktop app and the operator's FILEX_FRAME_ANCESTORS may
 *      frame it (security review sec055 S9);
 *   3. an interface hands filex a PDF (`ui.print`): filex asks the person,
 *      every time, and nothing is printed until the person clicks Allow -
 *      which is the print page's own button, a frame of `/_print/` in the
 *      question row, so the click is the person's IN that page (S9). Then
 *      the page prints from a `blob:` frame of its own and the interface is
 *      told `{printed: true}`. A sandboxed frame could not have opened the
 *      dialog itself. "Don't allow" tells the interface `cancelled`;
 *   4. an app without the grant is refused (`not_granted`) and nothing is
 *      drawn;
 *   5. `encrypted`: the folder listing stamps a file in an end-to-end
 *      encrypted folder `encrypted: "folder"` and a plain one nothing, and an
 *      interface opened on a plain file is told no `encrypted`.
 *
 * The app is built here, like 228's: an interface-only app whose script
 * speaks the bridge's protocol itself.
 */
import { test, expect, type APIRequestContext, type Frame, type Page } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { installInterfaceApp, openAppInterface, removeAppByName, type ReviewRow } from '../helpers/appInterface';
import { zip } from '../helpers/zip';

/* ── the app ────────────────────────────────────────────────────────────── */

/**
 * The bridge's hello, then `session.get` (kept in `__session`) and two
 * probes the spec calls with `frame.evaluate`:
 *   __print()       ui.print with a one-page PDF, the bridge's raw answer
 *   __call(m, p)    any call, the bridge's raw answer
 */
const APP_JS = `
let port = null;
let seq = 0;
const pending = new Map();
const call = (method, params, transfer) => new Promise((ok) => {
  const id = ++seq;
  pending.set(id, ok);
  port.postMessage({ id, method, params }, transfer || []);
});
const PDF = [
  '%PDF-1.4',
  '1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj',
  '2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj',
  '3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj',
  'trailer<</Root 1 0 R>>',
  '%%EOF',
].join(String.fromCharCode(10));
window.__session = null;
window.__print = () => {
  const pdf = new TextEncoder().encode(PDF).buffer;
  return call('ui.print', { name: 'report.pdf', data: pdf, mime: 'application/pdf' }, [pdf]);
};
window.__call = (m, p) => call(m, p);
(async () => {
  port = await new Promise((resolve, reject) => {
    addEventListener('message', (ev) => {
      if (ev.source === parent && ev.data && ev.data.type === 'filex:port' && ev.ports[0]) resolve(ev.ports[0]);
    });
    parent.postMessage({ type: 'filex:hello', v: 1 }, '*');
    setTimeout(() => reject(new Error('filex did not answer')), 10000);
  });
  port.onmessage = (ev) => {
    const m = ev.data;
    if (m && typeof m.id === 'number' && pending.has(m.id)) {
      pending.get(m.id)(m);
      pending.delete(m.id);
    }
  };
  window.__session = (await call('session.get')).result;
  document.body.dataset.ready = '1';
})().catch((e) => { document.body.dataset.error = String((e && (e.message || e.code)) || e); });
`;

const UI = zip({
  'index.html': '<!doctype html><html><head><meta charset="utf-8"><script src="app.js" defer></script></head><body><h1>print</h1></body></html>',
  'app.js': APP_JS,
});

function manifest(name: string, ext: string, ui: Record<string, unknown>) {
  return {
    manifest_version: 1,
    name,
    version: '1.0.0',
    label: { en: name },
    permissions: ['files:read', 'files:write'],
    ui: { bundle: {}, ...ui },
    views: [{ id: 'view', placement: 'viewer', ui: 'index.html', label: { en: name }, applies: { ext: [ext] } }],
  };
}

interface Answer {
  result?: { printed?: boolean; size?: number };
  error?: { code: string; message?: string };
}
type Probe = Window & {
  __session: { files: Array<Record<string, unknown>> } | null;
  __print: () => Promise<Answer>;
  __call: (m: string, p?: unknown) => Promise<Answer>;
};

/** The question filex asks before a print, once it shows. */
async function printQuestion(page: Page) {
  const bar = page.locator('[data-testid="appframe-print-ask"][data-shown="true"]');
  await expect(bar).toBeVisible();
  await expect(bar).toContainText('report.pdf');
  return bar;
}

/** Start a print in the interface; filex asks, the person clicks Allow - the
 *  print page's own button, in a frame of `/_print/` in the row. Nothing is
 *  printed before that click. */
async function printWithYes(page: Page, frame: Frame): Promise<Answer> {
  let settled = false;
  const answer = frame.evaluate(() => (window as unknown as Probe).__print()).finally(() => (settled = true));
  await printQuestion(page);
  await expect(page.locator('[data-testid="appframe-consent-allow"]'), 'filex draws no Allow of its own for a print').toHaveCount(0);
  // The row's Allow is a frame of filex's print page - filex's own page, not
  // the app's sandbox - and that page has framed the PDF it made itself.
  const printFrame = page.locator('[data-testid="appframe-print-ask"] iframe[data-testid="appframe-print"]');
  await expect(printFrame).toHaveCount(1);
  expect(new URL((await printFrame.getAttribute('src')) ?? '', page.url()).pathname).toMatch(/\/_print\/$/);
  expect(await printFrame.getAttribute('sandbox'), 'filex’s own page, not the app’s sandbox').toBeNull();
  // ⚠ The page's own frame of the PDF, by its address: a browser without a
  // PDF viewer (Playwright's chromium headless shell) turns that frame's
  // navigation into a download, so no frame ever commits to blob: and
  // page.frames() cannot see it - but the print page did frame its blob:.
  const page2 = page.frameLocator('[data-testid="appframe-print-ask"] iframe[data-testid="appframe-print"]');
  await expect(page2.locator('iframe.pdf')).toHaveAttribute('src', /^blob:/);
  const allow = page2.getByRole('button');
  await expect(allow).toBeEnabled();
  await page.waitForTimeout(1000);
  expect(settled, 'nothing is printed before the person clicks').toBe(false);
  await allow.click();
  return answer;
}

/* ── the spec ───────────────────────────────────────────────────────────── */

test.describe.serial('An app’s interface prints a PDF through filex, and is told how a file is encrypted', () => {
  let api: APIRequestContext;
  let tag = '';
  let store = '';
  let app = '';
  let plain = '';
  let ext = '';
  let rows: ReviewRow[] = [];
  let plainRows: ReviewRow[] = [];

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-prn-${tag}-${Date.now()}`;
    app = `prn-${tag}`;
    plain = `prn-plain-${tag}`;
    ext = `prn${tag.slice(0, 3)}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await removeAppByName(api, app);
    await removeAppByName(api, plain);
    const mount = `/tmp/filex-${store}`;
    const root = storageRoot(mount);
    mkdirSync(join(root, 'Sifreli'), { recursive: true });
    writeFileSync(join(root, `doc.${ext}`), 'a document');
    writeFileSync(join(root, `doc.${ext}p`), 'a document for the app without the grant');
    // An end-to-end encrypted folder as a browser leaves it: the marker, and
    // a file the server cannot read (the listing's cold-cache path).
    writeFileSync(join(root, 'Sifreli', '.filex-e2e.json'), '{}');
    writeFileSync(join(root, 'Sifreli', 'belge.fxe'), 'sealed');
    await seedLocalStorage(api, store, mount);
    rows = await installInterfaceApp(api, manifest(app, ext, { print: true }), UI);
    plainRows = await installInterfaceApp(api, manifest(plain, `${ext}p`, {}), UI);
  });

  test.afterAll(async () => {
    await removeAppByName(api, app);
    await removeAppByName(api, plain);
    await dropStorageByName(api, store);
    await api.dispose();
  });

  test('the review lists ui:print, only for the app that asks', async () => {
    const row = rows.find((r) => r.id === 'ui:print');
    expect(row, 'listed').toBeTruthy();
    expect(row!.label).not.toBe('');
    expect(row!.label).not.toBe('ui:print');
    expect(plainRows.map((r) => r.id), 'off unless the manifest asks').not.toContain('ui:print');
  });

  test('the print page frames only its own blob: and nothing of filex', async () => {
    const res = await api.get('/_print/');
    expect(res.status()).toBe(200);
    expect(res.headers()['content-type']).toContain('text/html');
    const csp = res.headers()['content-security-policy'] ?? '';
    expect(csp).toContain('frame-src blob:');
    expect(csp).toContain("connect-src 'none'");
    expect(csp, 'filex, the desktop app and the listed pages only - never any site').toContain("frame-ancestors 'self'");
    expect(csp).toContain('app://filex');
    expect(csp).not.toContain('frame-ancestors *');
    expect(csp.split('frame-ancestors').length - 1, 'once').toBe(1);
    expect(csp).not.toContain('/_appui/');
    expect(csp).not.toContain('data:');
  });

  test('the interface hands over a PDF and filex prints it from its own page', async ({ page }) => {
    const frame = await openAppInterface(page, store, `doc.${ext}`);
    const r = await printWithYes(page, frame);
    // A browser with a PDF viewer prints (`{printed: true}`). A headless
    // build without one (Playwright's chromium headless shell) turns the
    // blob: PDF into a download: the print page then answers `failed` after
    // its 15 s guard, never a hang. Recorded, so the run says which it was.
    test.info().annotations.push({ type: 'print', description: JSON.stringify(r) });
    const printFrame = page.locator('iframe[data-testid="appframe-print"]');
    if (r.error) {
      expect(r.error.code, 'a refusal of filex would be not_granted / invalid / unavailable').toBe('failed');
      // Nothing is left behind.
      await expect(printFrame).toHaveCount(0);
      await expect(page.locator('[data-testid="appframe-print-ask"]')).toHaveCount(0);
    } else {
      expect(r.result?.printed).toBe(true);
      expect(r.result?.size).toBeGreaterThan(0);
      // The question is out of sight again, its frame kept for the dialog.
      await expect(page.locator('[data-testid="appframe-print-ask"]')).toHaveAttribute('data-shown', 'false');
      await expect(printFrame).toHaveCount(1);
    }
  });

  test('"Don’t allow" prints nothing and tells the interface cancelled', async ({ page }) => {
    const frame = await openAppInterface(page, store, `doc.${ext}`);
    const answer = frame.evaluate(() => (window as unknown as Probe).__print());
    await printQuestion(page);
    await page.locator('[data-testid="appframe-print-deny"]').click();
    expect((await answer).error?.code).toBe('cancelled');
    await expect(page.locator('[data-testid="appframe-print-ask"]')).toHaveCount(0);
    await expect(page.locator('iframe[data-testid="appframe-print"]')).toHaveCount(0);
  });

  test('without the grant: not_granted, and nothing is drawn', async ({ page }) => {
    const frame = await openAppInterface(page, store, `doc.${ext}p`);
    const r = await frame.evaluate(() => (window as unknown as Probe).__print());
    expect(r.error?.code).toBe('not_granted');
    await expect(page.locator('[data-testid="appframe-print-ask"]')).toHaveCount(0);
    await expect(page.locator('iframe[data-testid="appframe-print"]')).toHaveCount(0);
  });

  test('encrypted: the listing stamps a file in an encrypted folder, never a plain one', async ({ page }) => {
    const rowsOf = async (p: string) => {
      const res = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(p)}`);
      expect(res.ok(), `${p}: ${res.status()}`).toBe(true);
      const body = (await res.json()) as { files: Array<Record<string, unknown>> };
      return new Map(body.files.map((f) => [String(f.basename), f]));
    };
    const enc = await rowsOf(`${store}://Sifreli`);
    expect(enc.get('belge.fxe')?.encrypted).toBe('folder');
    const top = await rowsOf(`${store}://`);
    expect(top.get(`doc.${ext}`)?.encrypted).toBeUndefined();
    expect(top.get('Sifreli')?.encrypted, 'a folder is told by e2e, not by this field').toBeUndefined();

    // The interface on a plain file is told nothing about encryption.
    const frame = await openAppInterface(page, store, `doc.${ext}`);
    const files = await frame.evaluate(() => (window as unknown as Probe).__session?.files ?? []);
    expect(files).toHaveLength(1);
    expect('encrypted' in files[0]).toBe(false);
  });
});
