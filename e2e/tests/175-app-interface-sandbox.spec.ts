/**
 * 175-app-interface-sandbox — an app's own interface, in a real browser: the
 * isolation the design promised and the security review checked, kept as a
 * spec (docs/APP-PLUGINS-API.md → An app's own interface). In every engine
 * `E2E_BROWSERS` names (`chromium,firefox,webkit`):
 *
 *   1. a `viewer` app opens its file; over the bridge it reads it and saves
 *      it — a new version on the storage;
 *   2. its origin is opaque (`null`): no filex storage, cookie or parent;
 *   3. it connects nowhere: not to filex's API, not to another address;
 *   4. with `ui:package-fetch` it reads THIS version's files — and not
 *      another version's, another app's, or a page of filex;
 *   5. its frame cannot navigate itself to a page of filex (`/admin/`):
 *      filex's pages frame themselves by path (`/_appui/`, `/z/`);
 *   6. a sibling frame's forged hello and save are ignored — the bridge
 *      answers only the frame filex drew;
 *   7. with a second file in the folder, the viewer's previous and next
 *      chevrons stand beside the frame, not on it, and after a save the
 *      viewer's header says the new size (task #110: in Firefox and WebKit
 *      the chevrons covered filextext's page list and scroll bar, and the
 *      header kept the size the file was opened with);
 *   8. save as (`file.saveAs`): filex asks for the folder in its own folder
 *      dialog, the new file lands in the folder the person chose, a name the
 *      view does not open is refused by the server (`not_applicable`), and
 *      closing the dialog saves nothing (task #149: until 0.51 no host drew
 *      the dialog and save-as answered `unavailable` everywhere).
 *
 * The app is built here: an interface-only app (manifest + a zip of three
 * files) whose script speaks the bridge's protocol itself — no SDK build is
 * needed, and the protocol is what is measured. The "outside address" is a
 * tiny HTTP server on 127.0.0.1 that counts what reaches it.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { createServer, type Server } from 'node:http';
import { deflateRawSync } from 'node:zlib';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { AddressInfo } from 'node:net';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';

/* ── a zip, by hand (deflate, no dependency) ───────────────────────────── */

const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

function crc32(b: Buffer): number {
  let c = 0xffffffff;
  for (const x of b) c = CRC_TABLE[(c ^ x) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function zip(files: Record<string, string>): Buffer {
  const local: Buffer[] = [];
  const central: Buffer[] = [];
  let offset = 0;
  for (const [name, text] of Object.entries(files)) {
    const data = Buffer.from(text, 'utf8');
    const packed = deflateRawSync(data);
    const nameB = Buffer.from(name, 'utf8');
    const crc = crc32(data);
    const h = Buffer.alloc(30);
    h.writeUInt32LE(0x04034b50, 0);
    h.writeUInt16LE(20, 4);
    h.writeUInt16LE(0, 6);
    h.writeUInt16LE(8, 8);
    h.writeUInt32LE(0, 10);
    h.writeUInt32LE(crc, 14);
    h.writeUInt32LE(packed.length, 18);
    h.writeUInt32LE(data.length, 22);
    h.writeUInt16LE(nameB.length, 26);
    h.writeUInt16LE(0, 28);
    local.push(h, nameB, packed);
    const c = Buffer.alloc(46);
    c.writeUInt32LE(0x02014b50, 0);
    c.writeUInt16LE(20, 4);
    c.writeUInt16LE(20, 6);
    c.writeUInt16LE(0, 8);
    c.writeUInt16LE(8, 10);
    c.writeUInt32LE(0, 12);
    c.writeUInt32LE(crc, 16);
    c.writeUInt32LE(packed.length, 20);
    c.writeUInt32LE(data.length, 24);
    c.writeUInt16LE(nameB.length, 28);
    c.writeUInt32LE(0, 30);
    c.writeUInt32LE(0, 34);
    c.writeUInt32LE(0, 38);
    c.writeUInt32LE(offset, 42);
    central.push(c, nameB);
    offset += h.length + nameB.length + packed.length;
  }
  const cd = Buffer.concat(central);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(Object.keys(files).length, 8);
  end.writeUInt16LE(Object.keys(files).length, 10);
  end.writeUInt32LE(cd.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...local, cd, end]);
}

/* ── the app ────────────────────────────────────────────────────────────── */

/** The interface's script: the bridge's protocol, spoken by hand. */
const APP_JS = `
(async () => {
  const out = (window.__sbx = {});
  const port = await new Promise((resolve, reject) => {
    addEventListener('message', (ev) => {
      if (ev.source === parent && ev.data && ev.data.type === 'filex:port' && ev.ports[0]) resolve(ev.ports[0]);
    });
    parent.postMessage({ type: 'filex:hello', v: 1 }, '*');
    setTimeout(() => reject(new Error('filex did not answer')), 10000);
  });
  let seq = 0;
  const pending = new Map();
  port.onmessage = (ev) => {
    const m = ev.data;
    if (!m || !pending.has(m.id)) return;
    const p = pending.get(m.id);
    pending.delete(m.id);
    if (m.error) p.reject(m.error); else p.resolve(m.result);
  };
  window.__call = (method, params) => new Promise((resolve, reject) => {
    const id = ++seq;
    pending.set(id, { resolve, reject });
    port.postMessage({ id, method, params });
  });
  out.session = await window.__call('session.get');
  out.text = (await window.__call('file.read', { index: 0, as: 'text' })).text;
  document.body.dataset.ready = '1';
})().catch((e) => { document.body.dataset.error = String((e && (e.message || e.code)) || e); });
`;

const UI = zip({
  'index.html': '<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="style.css"><script src="app.js" defer></script></head><body><h1 id="title">sandbox</h1></body></html>',
  'app.js': APP_JS,
  'style.css': 'h1 { font: 16px sans-serif; }',
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

async function install(api: APIRequestContext, m: Record<string, unknown>) {
  const files = {
    manifest: { name: 'filex-app.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(m)) },
    ui: { name: 'ui.zip', mimeType: 'application/zip', buffer: UI },
  };
  const dry = await api.post('/api/admin/app-plugins?dry_run=1', { multipart: { ...files, grant: JSON.stringify({ permissions: [] }) } });
  expect(dry.ok(), `dry run: ${dry.status()} ${await dry.text()}`).toBe(true);
  const perms = ((await dry.json()) as { permissions: Array<{ id: string }> }).permissions.map((p) => p.id);
  const inst = await api.post('/api/admin/app-plugins', { multipart: { ...files, grant: JSON.stringify({ permissions: perms }) } });
  expect(inst.ok(), `install: ${inst.status()} ${await inst.text()}`).toBe(true);
  return perms;
}

async function removeApp(api: APIRequestContext, name: string) {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of ((await list.json()) as { plugins?: Array<{ id: number; name: string }> }).plugins ?? []) {
    if (p.name === name) await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

/* ── the spec ───────────────────────────────────────────────────────────── */

test.describe.serial('An app’s own interface — the sandbox, in every engine', () => {
  let api: APIRequestContext;
  let outside: Server;
  let outsideURL = '';
  const hits: string[] = [];
  // Per engine: the apps, the storage and the kind are global to the server,
  // and E2E_BROWSERS may run the engines side by side.
  let tag = '';
  let store = '';
  let root = '';
  let app = '';
  let other = '';
  let ext = '';

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-sbx-${tag}-${Date.now()}`;
    app = `sbx-${tag}`;
    other = `sbx-other-${tag}`;
    ext = `sbx${tag.slice(0, 3)}`;
    outside = createServer((req, res) => {
      hits.push(req.url ?? '');
      res.writeHead(200, { 'Access-Control-Allow-Origin': '*' }).end('outside');
    });
    await new Promise<void>((ok) => outside.listen(0, '127.0.0.1', ok));
    outsideURL = `http://127.0.0.1:${(outside.address() as AddressInfo).port}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await removeApp(api, app);
    await removeApp(api, other);
    const mount = `/tmp/filex-${store}`;
    root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, `note.${ext}`), 'hello from disk');
    // A second file of the kind: the viewer draws its previous/next chevrons.
    writeFileSync(join(root, `note2.${ext}`), 'the second file');
    await seedLocalStorage(api, store, mount);
    const perms = await install(api, manifest(app, ext, { package_fetch: true }));
    expect(perms, 'the review says it reads its own package').toContain('ui:package-fetch');
    await install(api, manifest(other, `${ext}o`, {}));
  });

  test.afterAll(async () => {
    await removeApp(api, app);
    await removeApp(api, other);
    await dropStorageByName(api, store);
    await api.dispose();
    await new Promise<void>((ok) => outside.close(() => ok()));
  });

  async function openInterface(page: Page) {
    await page.addInitScript(() => {
      localStorage.setItem('filex.tourDone', '1');
      localStorage.setItem('filex.installPrompt.dismissed', '1');
    });
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(store)}`);
    const row = page.locator(`[data-fe-path="${store}://note.${ext}"]`);
    await row.first().waitFor();
    await row.getByText(`note.${ext}`, { exact: true }).dblclick();
    const el = page.locator('iframe[data-testid="app-frame"]');
    await expect(el).toHaveAttribute('sandbox', 'allow-scripts');
    await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible({ timeout: 20_000 });
    const frame = (await (await el.elementHandle())!.contentFrame())!;
    await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error);
    expect(await frame.evaluate(() => document.body.dataset.error ?? null), 'the interface started').toBeNull();
    return frame;
  }

  test('a viewer opens its file, reads it and saves it over the bridge', async ({ page }) => {
    const frame = await openInterface(page);
    const got = await frame.evaluate(() => {
      const s = (window as unknown as { __sbx: { text: string; session: { files: Array<{ name: string }> } } }).__sbx;
      return { text: s.text, name: s.session.files[0].name };
    });
    expect(got).toEqual({ text: 'hello from disk', name: `note.${ext}` });
    const saved = await frame.evaluate(() =>
      (window as unknown as { __call: (m: string, p: unknown) => Promise<unknown> }).__call('file.save', { index: 0, data: 'saved by the sandbox app' }),
    );
    expect(saved).toMatchObject({ saved: true });
    await expect.poll(() => readFileSync(join(root, `note.${ext}`), 'utf8')).toBe('saved by the sandbox app');
  });

  test('its origin is opaque: no filex storage, cookie or parent', async ({ page }) => {
    const frame = await openInterface(page);
    const r = await frame.evaluate(() => {
      const tryDo = (fn: () => unknown) => {
        try {
          fn();
          return 'reachable';
        } catch (e) {
          return 'throws ' + (e as Error).name;
        }
      };
      return {
        origin: String(self.origin),
        localStorage: tryDo(() => localStorage.length),
        sessionStorage: tryDo(() => sessionStorage.getItem('filex.bearer')),
        cookie: tryDo(() => document.cookie),
        parent: tryDo(() => parent.document.title),
      };
    });
    expect(r.origin).toBe('null');
    expect(r.localStorage).toMatch(/^throws/);
    expect(r.sessionStorage).toMatch(/^throws/);
    expect(r.cookie).toMatch(/^throws/);
    expect(r.parent).toMatch(/^throws/);
  });

  test('it connects nowhere, and reads only its own version of its package', async ({ page }) => {
    const frame = await openInterface(page);
    const list = (await (await api.get('/api/files/plugins/actions')).json()) as { views?: Array<{ plugin: string; ui?: { url: string } }> };
    const otherURL = list.views?.find((v) => v.plugin === other)?.ui?.url ?? '';
    expect(otherURL, 'the other app has an interface address').toContain(`/_appui/${other}/`);
    const r = await frame.evaluate(
      async ({ outsideURL, otherURL }) => {
        const base = location.href.slice(0, location.href.indexOf('/_appui/'));
        const own = location.href.slice(0, location.href.lastIndexOf('/') + 1);
        const version = own.split('/').slice(-2, -1)[0];
        const get = (u: string) => fetch(u).then((x) => 'reached ' + x.status, (e) => 'blocked ' + (e as Error).name);
        return {
          own: await get('style.css'),
          otherVersion: await get(own.replace(`/${version}/`, '/0000000000000000/') + 'style.css'),
          otherApp: otherURL ? await get(base + otherURL.replace('index.html', 'style.css')) : 'no other app',
          api: await get(base + '/api/auth/me'),
          page: await get(base + '/admin/'),
          outside: await get(outsideURL + '/hit'),
        };
      },
      { outsideURL, otherURL },
    );
    expect(r.own).toBe('reached 200');
    expect(r.otherVersion).toMatch(/^blocked/);
    expect(r.otherApp).toMatch(/^blocked/);
    expect(r.api).toMatch(/^blocked/);
    expect(r.page).toMatch(/^blocked/);
    expect(r.outside).toMatch(/^blocked/);
    expect(hits, 'nothing reached the outside address').toEqual([]);
  });

  test('its frame cannot navigate itself to a page of filex', async ({ page }) => {
    const frame = await openInterface(page);
    const before = frame.url();
    expect(before).toContain('/_appui/');
    await frame
      .evaluate(() => {
        location.href = location.href.slice(0, location.href.indexOf('/_appui/')) + '/admin/';
      })
      .catch(() => undefined);
    await page.waitForTimeout(2500);
    const urls = page.frames().map((f) => f.url());
    expect(urls.filter((u) => u.includes('/admin/') && u !== page.url()), `frames: ${urls.join(' | ')}`).toEqual([]);
  });

  test('a sibling frame’s forged hello and save are ignored', async ({ page }) => {
    await openInterface(page);
    const before = readFileSync(join(root, `note.${ext}`), 'utf8');
    await page.evaluate(() => {
      const f = document.createElement('iframe');
      f.setAttribute('sandbox', 'allow-scripts');
      f.name = 'forger';
      f.srcdoc =
        '<script>window.got=[];addEventListener("message",function(e){window.got.push(e.data&&e.data.type||"?")});' +
        'parent.postMessage({type:"filex:hello",v:1},"*");' +
        'parent.postMessage({id:1,method:"file.save",params:{index:0,data:"forged"}},"*");<\/script>';
      document.body.append(f);
    });
    await page.waitForTimeout(1500);
    const forger = page.frames().find((f) => f.name() === 'forger');
    expect(forger, 'the sibling frame is there').toBeTruthy();
    expect(await forger!.evaluate(() => (window as unknown as { got: string[] }).got), 'no port, no answer').toEqual([]);
    expect(readFileSync(join(root, `note.${ext}`), 'utf8')).toBe(before);
  });

  test('the viewer: the chevrons stand beside the frame, and its header follows a save', async ({ page }) => {
    const frame = await openInterface(page);
    const box = await page.locator('iframe[data-testid="app-frame"]').boundingBox();
    expect(box, 'the frame is drawn').toBeTruthy();
    for (const sel of ['.fe-viewer__chev--prev', '.fe-viewer__chev--next']) {
      const chev = page.locator(sel);
      await expect(chev, `${sel} is drawn: two files in the folder`).toBeVisible();
      const c = (await chev.boundingBox())!;
      const beside = c.x + c.width <= box!.x || c.x >= box!.x + box!.width;
      expect(beside, `${sel} (${c.x}..${c.x + c.width}) beside the frame (${box!.x}..${box!.x + box!.width})`).toBe(true);
    }
    const meta = page.locator('.fe-viewer__meta');
    await expect(meta).not.toContainText('1.5 KB');
    const saved = await frame.evaluate(() =>
      (window as unknown as { __call: (m: string, p: unknown) => Promise<unknown> }).__call('file.save', { index: 0, data: 'x'.repeat(1500) }),
    );
    expect(saved).toMatchObject({ saved: true, size: 1500 });
    await expect(meta, 'the header says the size the save wrote').toContainText('1.5 KB');
  });

  test('save as: filex asks for the folder in its own dialog, and the new file lands there', async ({ page }) => {
    // Through filex, not the disk: the dialog lists what filex knows.
    const mk = await api.post('/api/files/manager?action=newfolder', { data: { path: `${store}://`, name: 'exports' } });
    expect(mk.ok(), `newfolder: ${mk.status()} ${await mk.text()}`).toBe(true);
    const frame = await openInterface(page);
    /** Save as from inside the interface; what it is answered, either way. */
    const saveAs = (name: string, data: string) =>
      frame.evaluate(
        ({ name, data }) =>
          (window as unknown as { __call: (m: string, p: unknown) => Promise<unknown> })
            .__call('file.saveAs', { name, data })
            .then((ok) => ({ ok }), (err) => ({ err })),
        { name, data },
      );
    const dialog = page.getByTestId('destpicker');
    // The dialog's own card: the viewer around it is a dialog card too.
    const card = dialog.locator('xpath=ancestor::div[contains(concat(" ", @class, " "), " fe-modal__card ")][1]');
    const name = `copy.${ext}`;

    // Filex's own folder dialog, naming the app and the file, in the file's
    // folder; the person walks into `exports` and saves there.
    let answer = saveAs(name, 'saved as');
    await expect(dialog, 'the folder dialog is on screen').toBeVisible();
    await expect(card).toContainText(`${app}: save “${name}” to`);
    await page.getByTestId('destpicker-row-exports').click();
    await expect(page.getByTestId('destpicker-target')).toContainText('exports');
    await page.getByTestId('destpicker-confirm').click();
    expect(await answer).toEqual({ ok: { saved: true, name, size: 8 } });
    await expect(dialog).toHaveCount(0);
    await expect.poll(() => readFileSync(join(root, 'exports', name), 'utf8')).toBe('saved as');

    // A kind of file the view does not open: the server refuses it after the
    // pick, and the interface is told the short code only.
    answer = saveAs('page.html', '<p>not a sketch</p>');
    await expect(dialog).toBeVisible();
    await page.getByTestId('destpicker-confirm').click();
    expect(await answer).toEqual({ err: { code: 'invalid', message: 'not_applicable' } });
    expect(existsSync(join(root, 'page.html')), 'nothing was written').toBe(false);

    // Closing the dialog saves nothing.
    answer = saveAs(`closed.${ext}`, 'never');
    await expect(dialog).toBeVisible();
    await card.getByRole('button', { name: 'Cancel', exact: true }).click();
    expect(await answer).toMatchObject({ err: { code: 'cancelled' } });
    await expect(dialog).toHaveCount(0);
    expect(existsSync(join(root, `closed.${ext}`))).toBe(false);
  });

  test('save as in the standalone editor: the focus ring of the first breadcrumb is drawn whole', async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.setItem('filex.tourDone', '1');
      localStorage.setItem('filex.installPrompt.dismissed', '1');
    });
    await loginAs(page);
    await page.goto(
      `/admin/files/edit?path=${encodeURIComponent(`${store}://note.${ext}`)}&type=${ext}&mode=edit&app=${encodeURIComponent(app)}`,
    );
    const el = page.locator('iframe[data-testid="app-frame"]');
    await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible({ timeout: 20_000 });
    const frame = (await (await el.elementHandle())!.contentFrame())!;
    await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error);
    void frame.evaluate(
      (name) =>
        (window as unknown as { __call: (m: string, p: unknown) => Promise<unknown> })
          .__call('file.saveAs', { name, data: 'x' })
          .catch(() => null),
      `ring.${ext}`,
    );
    const dialog = page.getByTestId('destpicker');
    await expect(dialog).toBeVisible();
    const crumb = dialog.locator('.fe-destpick__crumbs button').first();
    await expect(crumb).toBeVisible();
    // Keyboard modality first, so :focus-visible matches in every engine.
    await page.keyboard.press('Tab');
    await crumb.focus();
    await expect(crumb).toBeFocused();
    const probe = await crumb.evaluate((node) => {
      const cs = getComputedStyle(node);
      const ring = Math.max(parseFloat(cs.outlineWidth) || 0, 1) + (parseFloat(cs.outlineOffset) || 0);
      const r = node.getBoundingClientRect();
      const drawn = cs.outlineStyle !== 'none';
      // The ring's box: the border box grown by (width + offset), a negative offset pulling it inside.
      const box = { l: r.left - ring, t: r.top - ring, r: r.right + ring, b: r.bottom + ring };
      const clipped: string[] = [];
      for (let a = node.parentElement; a && a !== document.documentElement; a = a.parentElement) {
        const ac = getComputedStyle(a);
        if (ac.overflowX === 'visible' && ac.overflowY === 'visible') continue;
        const c = a.getBoundingClientRect();
        const bl = c.left + a.clientLeft;
        const bt = c.top + a.clientTop;
        const br = bl + a.clientWidth;
        const bb = bt + a.clientHeight;
        const sides: string[] = [];
        if (ac.overflowX !== 'visible' && box.l < bl - 0.5) sides.push('left');
        if (ac.overflowX !== 'visible' && box.r > br + 0.5) sides.push('right');
        if (ac.overflowY !== 'visible' && box.t < bt - 0.5) sides.push('top');
        if (ac.overflowY !== 'visible' && box.b > bb + 0.5) sides.push('bottom');
        if (sides.length) clipped.push(`${a.className || a.tagName} clips ${sides.join('+')}`);
      }
      return { drawn, style: cs.outlineStyle, width: cs.outlineWidth, offset: cs.outlineOffset, clipped };
    });
    await page.screenshot({ path: test.info().outputPath(`crumb-focus-${test.info().project.name}.png`) });
    expect(probe.drawn, `the focused crumb has a ring: ${JSON.stringify(probe)}`).toBe(true);
    expect(probe.clipped, `the ring is inside every scroller around it: ${JSON.stringify(probe)}`).toEqual([]);
    await dialog.locator('xpath=ancestor::div[contains(concat(" ", @class, " "), " fe-modal__card ")][1]').getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(dialog).toHaveCount(0);
  });
});
