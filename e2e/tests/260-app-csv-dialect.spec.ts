/**
 * 260-app-csv-dialect - a .csv edited in an app's interface is written the
 * file's way (filex 0.57, tasks #255 and #268; docs/APP-PLUGINS-API.md → CSV
 * files: how they are written). No Document Server: the app is built here,
 * an interface-only one whose script speaks the bridge's protocol itself.
 *
 *   1. a Windows-1254 file: the read says so (`text_dialect`), a read as text
 *      is the file's letters, and a UTF-8 save is written back in Windows-1254
 *      with its own records byte for byte (`007`, `şeker`);
 *   2. a semicolon file saved with semicolons (as Docs 10 and an app may)
 *      keeps its columns and its cells: 0.56 wrote every row as one field;
 *   3. a save whose declaration its bytes contradict is refused (`invalid`,
 *      `csv_dialect`) and the file does not change.
 *
 * Opened through the file menu's "Open with" row of the app: the account's
 * own choice for .csv (199's) is not touched.
 */
import { test, expect, type APIRequestContext, type Frame, type Page } from '@playwright/test';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { installInterfaceApp, removeAppByName } from '../helpers/appInterface';
import { zip } from '../helpers/zip';

const APP_JS = `
let port = null;
let seq = 0;
const pending = new Map();
const call = (method, params, transfer) => new Promise((ok) => {
  const id = ++seq;
  pending.set(id, ok);
  port.postMessage({ id, method, params }, transfer || []);
});
window.__read = (as) => call('file.read', { index: 0, as });
window.__save = (text, csv) => {
  const data = new TextEncoder().encode(text).buffer;
  return call('file.save', csv ? { index: 0, data, csv } : { index: 0, data }, [data]);
};
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
  await call('session.get');
  document.body.dataset.ready = '1';
})().catch((e) => { document.body.dataset.error = String((e && (e.message || e.code)) || e); });
`;

const UI = zip({
  'index.html': '<!doctype html><html><head><meta charset="utf-8"><script src="app.js" defer></script></head><body><h1>sheet</h1></body></html>',
  'app.js': APP_JS,
});

interface Answer {
  result?: { text?: string; saved?: boolean; text_dialect?: Record<string, unknown> };
  error?: { code: string; message?: string };
}
type Probe = Window & {
  __read: (as: string) => Promise<Answer>;
  __save: (text: string, csv?: Record<string, unknown>) => Promise<Answer>;
};

// Windows-1254: 0xFE is ş, 0xFD ı, 0xF0 ğ.
const CP1254 = Buffer.from('kod;ad;not\r\n007;\xFEeker;a\r\n', 'latin1');
const CP1254_SAVED = Buffer.from('kod;ad;not\r\n007;\xFEeker;a\r\n9;\xFD\xF0d\xFDr;b\r\n', 'latin1');
// The e2e 199 fixture (task #255).
const ORIGINAL =
  'ad;adet;not;kod;tel;tarih\r\n' +
  'elma;3;"a; b";007;05320000001;01.02.2026\r\n' +
  'armut;5;şeker;042;05330000002;15.03.2026\r\n';
const EDITED = ORIGINAL.replace('elma;3;', 'elma;42;');
// What an editor writes in the file's own delimiter (Docs 10 does; 0.56 read
// it as commas).
const SEMICOLONS =
  'ad;adet;not;kod;tel;tarih\n' +
  'elma;42;"a; b";7;5320000001;1/2/2026\n' +
  'armut;5;şeker;42;5330000002;15.03.2026\n';

test.describe.serial('a .csv edited in an app is written the file’s way', () => {
  let api: APIRequestContext;
  let app = '';
  let store = '';
  let mount = '';

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    app = `csvdl-${tag}`;
    store = `e2e-csvdl-${tag}-${Date.now()}`;
    mount = `/tmp/filex-${store}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, 'eski.csv'), CP1254);
    writeFileSync(join(root, 'liste.csv'), ORIGINAL);
    await seedLocalStorage(api, store, mount);
    await removeAppByName(api, app);
    await installInterfaceApp(
      api,
      {
        manifest_version: 1,
        name: app,
        version: '1.0.0',
        filex: '>=0.57.0',
        label: { en: app },
        permissions: ['files:read', 'files:write'],
        ui: { bundle: {} },
        views: [{ id: 'editor', placement: 'viewer', ui: 'index.html', label: { en: app }, applies: { ext: ['csv'] } }],
      },
      UI,
    );
  });

  test.afterAll(async () => {
    await removeAppByName(api, app).catch(() => undefined);
    await dropStorageByName(api, store).catch(() => undefined);
    await api?.dispose();
  });

  const onDisk = (name: string) => readFileSync(join(storageRoot(mount), name));

  /** Open the file in the app through its "Open with" row; the interface's frame. */
  async function openInApp(page: Page, name: string): Promise<Frame> {
    await page.addInitScript(() => {
      localStorage.setItem('filex.tourDone', '1');
      localStorage.setItem('filex.installPrompt.dismissed', '1');
    });
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(store)}`);
    const row = page.locator(`[data-fe-path="${store}://${name}"]`).first();
    await row.waitFor();
    await row.getByText(name, { exact: true }).click({ button: 'right' });
    await page.getByTestId(`ctx-open-with-app:${app}/editor`).click();
    const el = page.locator('iframe[data-testid="app-frame"]');
    await expect(el).toHaveAttribute('sandbox', 'allow-scripts', { timeout: 20_000 });
    await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible({ timeout: 20_000 });
    const frame = (await (await el.elementHandle())!.contentFrame())!;
    await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error);
    expect(await frame.evaluate(() => document.body.dataset.error ?? null), 'the interface started').toBeNull();
    return frame;
  }

  test('a Windows-1254 file: the read says so, and a UTF-8 save is written back in it', async ({ page }) => {
    const frame = await openInApp(page, 'eski.csv');
    const read = await frame.evaluate(() => (window as unknown as Probe).__read('text'));
    expect(read.error, JSON.stringify(read)).toBeUndefined();
    expect(read.result?.text).toBe('kod;ad;not\r\n007;şeker;a\r\n');
    expect(read.result?.text_dialect).toEqual({ delimiter: ';', bom: false, crlf: true, encoding: 'windows-1254' });

    const saved = await frame.evaluate(
      (t) => (window as unknown as Probe).__save(t, { delimiter: ',', encoding: 'utf-8', bom: false, eol: 'lf' }),
      'kod,ad,not\n7,şeker,a\n9,ığdır,b\n',
    );
    expect(saved.error, JSON.stringify(saved)).toBeUndefined();
    expect(onDisk('eski.csv').equals(CP1254_SAVED), onDisk('eski.csv').toString('latin1')).toBe(true);
  });

  test('a declaration the bytes contradict is refused; the file’s own delimiter keeps its columns', async ({ page }) => {
    const frame = await openInApp(page, 'liste.csv');
    const refused = await frame.evaluate((t) => (window as unknown as Probe).__save(t, { delimiter: ',' }), SEMICOLONS);
    expect(refused.error?.code, JSON.stringify(refused)).toBe('invalid');
    expect(refused.error?.message).toBe('csv_dialect');
    expect(onDisk('liste.csv').toString('utf8'), 'nothing was written').toBe(ORIGINAL);

    const saved = await frame.evaluate((t) => (window as unknown as Probe).__save(t), SEMICOLONS);
    expect(saved.error, JSON.stringify(saved)).toBeUndefined();
    expect(onDisk('liste.csv').toString('utf8')).toBe(EDITED);
  });
});
