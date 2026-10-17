/**
 * 272-app-save-beside-conflict - an app's save that does not go over the
 * file it opened (filex 0.57, task #268; docs/APP-PLUGINS-API.md → Saving
 * beside the file, conflict copies). No Document Server: the app is built
 * here, an interface-only one whose script speaks the bridge's protocol.
 *
 *   1. a .doc saved as DOCX (`beside: 'docx'`): rapor.doc stays as it was,
 *      the save is rapor.docx - filex chose the name and says it - and the
 *      next save of the same opening goes over rapor.docx, not beside again;
 *   2. a file somebody else changed after the app read it: with
 *      `conflict: 'copy'` the save is a conflict copy beside it, named in
 *      the answer, and the other change is kept; without it (an app built
 *      for 0.56) the save is what it was in 0.56: over the file.
 *
 * Opened through the file menu's "Open with" row of the app.
 */
import { test, expect, type APIRequestContext, type Frame, type Page } from '@playwright/test';
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
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
window.__read = () => call('file.read', { index: 0, as: 'bytes' });
window.__save = (text, opts) => {
  const data = new TextEncoder().encode(text).buffer;
  return call('file.save', Object.assign({ index: 0, data }, opts || {}), [data]);
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
  'index.html': '<!doctype html><html><head><meta charset="utf-8"><script src="app.js" defer></script></head><body><h1>office</h1></body></html>',
  'app.js': APP_JS,
});

interface Answer {
  result?: { saved?: boolean; size?: number; name?: string; beside?: boolean; conflict?: string };
  error?: { code: string; message?: string };
}
type Probe = Window & {
  __read: () => Promise<Answer>;
  __save: (text: string, opts?: Record<string, unknown>) => Promise<Answer>;
};

// What an office editor writes: a zip package (only its first bytes matter here).
const DOCX = (s: string) => 'PK\u0003\u0004' + s;

test.describe.serial('an app saves beside the file, or a conflict copy', () => {
  let api: APIRequestContext;
  let app = '';
  let store = '';
  let mount = '';

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    app = `beside-${tag}`;
    store = `e2e-beside-${tag}-${Date.now()}`;
    mount = `/tmp/filex-${store}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, 'rapor.doc'), 'a Word 97 file');
    writeFileSync(join(root, 'teklif.docx'), DOCX('v1'));
    writeFileSync(join(root, 'eski.docx'), DOCX('v1'));
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
        views: [{ id: 'editor', placement: 'viewer', ui: 'index.html', label: { en: app }, applies: { ext: ['doc', 'docx'] } }],
      },
      UI,
    );
  });

  test.afterAll(async () => {
    await removeAppByName(api, app).catch(() => undefined);
    await dropStorageByName(api, store).catch(() => undefined);
    await api?.dispose();
  });

  const onDisk = (name: string) => readFileSync(join(storageRoot(mount), name)).toString('latin1');

  /** Somebody else saves the file through filex meanwhile (its catalogue row
   *  moves on with it, as any save's does). */
  async function savedElsewhere(name: string, body: string): Promise<void> {
    const res = await api.put(`/api/files/plugins/ui/${app}/editor/save?path=${encodeURIComponent(`${store}://${name}`)}`, {
      data: Buffer.from(body, 'latin1'),
      headers: { 'content-type': 'application/octet-stream' },
    });
    expect(res.ok(), await res.text()).toBe(true);
  }

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

  test('a .doc saved as DOCX goes beside it, and the opening keeps saving there', async ({ page }) => {
    const frame = await openInApp(page, 'rapor.doc');
    const first = await frame.evaluate((t) => (window as unknown as Probe).__save(t, { beside: 'docx' }), DOCX('one'));
    expect(first.error, JSON.stringify(first)).toBeUndefined();
    expect(first.result).toMatchObject({ saved: true, name: 'rapor.docx', beside: true });
    expect(onDisk('rapor.doc'), 'the file itself is left as it was').toBe('a Word 97 file');
    expect(onDisk('rapor.docx')).toBe(DOCX('one'));

    const second = await frame.evaluate((t) => (window as unknown as Probe).__save(t, { beside: 'docx' }), DOCX('two'));
    expect(second.error, JSON.stringify(second)).toBeUndefined();
    expect(onDisk('rapor.docx')).toBe(DOCX('two'));
    expect(existsSync(join(storageRoot(mount), 'rapor (2).docx')), 'not a second file beside it').toBe(false);
  });

  test('a file changed after the app read it: a conflict copy when asked, a refusal when not', async ({ page }) => {
    const frame = await openInApp(page, 'teklif.docx');
    const read = await frame.evaluate(() => (window as unknown as Probe).__read());
    expect(read.error, JSON.stringify(read)).toBeUndefined();
    await savedElsewhere('teklif.docx', DOCX('v2, written by somebody else'));

    const saved = await frame.evaluate((t) => (window as unknown as Probe).__save(t, { conflict: 'copy' }), DOCX('mine'));
    expect(saved.error, JSON.stringify(saved)).toBeUndefined();
    expect(saved.result?.conflict ?? '').toMatch(/^teklif\.filex-conflict-\d{8}T\d{6}\.docx$/);
    expect(onDisk('teklif.docx'), 'the other change is kept').toBe(DOCX('v2, written by somebody else'));
    expect(onDisk(saved.result!.conflict!)).toBe(DOCX('mine'));
    expect(readdirSync(storageRoot(mount)).filter((n) => n.includes('filex-conflict'))).toHaveLength(1);
  });

  test('an app that asks for no conflict copy saves as in 0.56: over the file, no copy', async ({ page }) => {
    const frame = await openInApp(page, 'eski.docx');
    await frame.evaluate(() => (window as unknown as Probe).__read());
    await savedElsewhere('eski.docx', DOCX('v2, written by somebody else'));
    const saved = await frame.evaluate((t) => (window as unknown as Probe).__save(t), DOCX('mine'));
    expect(saved.error, JSON.stringify(saved)).toBeUndefined();
    expect(saved.result).toEqual({ saved: true, size: DOCX('mine').length });
    expect(onDisk('eski.docx')).toBe(DOCX('mine'));
    expect(readdirSync(storageRoot(mount)).filter((n) => n.startsWith('eski.filex-conflict'))).toHaveLength(0);
  });
});
