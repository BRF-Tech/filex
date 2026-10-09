/**
 * 228-app-frame-package — the two doors an editor that runs in the browser
 * needs in an app's sandbox (task #189, platform slice P1; the office editor
 * app opens ONLYOFFICE's editor page in a frame of its own and loads the
 * document from a blob: address): `ui.frame_package` → `ui:frame-package`,
 * `ui.connect_blob` → `ui:connect-blob`. In every engine `E2E_BROWSERS`
 * names (`chromium,firefox,webkit`):
 *
 *   1. the review lists both, in the administrator's words, only for the app
 *      that asks;
 *   2. with them the interface frames a page of its OWN package - and the
 *      page really loads: its script reports back, the interface is told of
 *      no frame refusal, and Chromium logs none (the page carries no
 *      frame-ancestors: its parent is an opaque origin, which `*` does not
 *      match) - and that page is a sandbox of its own: an opaque origin, no storage or cookie,
 *      no WebRTC (the bootstrap ran in it too), no filex API, no page of
 *      filex, no outside address, and filex's bridge does not answer it;
 *   3. it still frames nothing else: not another app's page, not a page of
 *      filex, not an outside address, not a data: or blob: document;
 *   4. it reads a blob: address it made, with fetch and with XHR (what the
 *      editor does) - in Chromium under the Connection-Allowlist filex sends;
 *   5. an app without them frames nothing and reads no blob:.
 *
 * The app is built here, like 175's: an interface-only app whose script
 * speaks the bridge's protocol itself. The "outside address" is a tiny HTTP
 * server on 127.0.0.1 that counts what reaches it.
 */
import { test, expect, type APIRequestContext, type Frame, type Response } from '@playwright/test';
import { createServer, type Server } from 'node:http';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { AddressInfo } from 'node:net';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { installInterfaceApp, openAppInterface, removeAppByName, type ReviewRow } from '../helpers/appInterface';
import { zip } from '../helpers/zip';

/* ── the app ────────────────────────────────────────────────────────────── */

/**
 * The outer page: the bridge's hello (so filex marks the frame connected),
 * then three probes the spec calls with `frame.evaluate`:
 *   __blob(text)        reads a blob: of its own with fetch and with XHR
 *   __get(url)          a plain fetch, to show nothing else opened
 *   __frame(src, wait)  frames src and waits for the framed page's report
 * and `__violations`, every CSP violation the page itself was told about.
 */
const APP_JS = `
window.__violations = [];
document.addEventListener('securitypolicyviolation', (e) => {
  window.__violations.push((e.effectiveDirective || e.violatedDirective) + ' ' + e.blockedURI);
});
window.__blob = async (text) => {
  const url = URL.createObjectURL(new Blob([text], { type: 'application/octet-stream' }));
  const viaFetch = await fetch(url).then((r) => r.text()).then((t) => 'read ' + t, (e) => 'blocked ' + e.name);
  const viaXHR = await new Promise((ok) => {
    const x = new XMLHttpRequest();
    const timer = setTimeout(() => ok('blocked timeout'), 5000);
    x.onload = () => { clearTimeout(timer); ok('read ' + new TextDecoder().decode(x.response)); };
    x.onerror = () => { clearTimeout(timer); ok('blocked error'); };
    try {
      x.open('GET', url);
      x.responseType = 'arraybuffer';
      x.send();
    } catch (e) {
      clearTimeout(timer);
      ok('blocked ' + e.name);
    }
  });
  return { fetch: viaFetch, xhr: viaXHR, url };
};
window.__get = (u) => fetch(u).then((x) => 'reached ' + x.status, (e) => 'blocked ' + e.name);
window.__frame = (src, wait) => new Promise((ok) => {
  const f = document.createElement('iframe');
  let timer = 0;
  const on = (ev) => {
    if (ev.source !== f.contentWindow || !ev.data || ev.data.type !== 'inner-report') return;
    clearTimeout(timer);
    removeEventListener('message', on);
    ok({ loaded: true, report: ev.data.report });
  };
  addEventListener('message', on);
  timer = setTimeout(() => { removeEventListener('message', on); ok({ loaded: false }); }, wait);
  f.src = src;
  document.body.append(f);
});
(async () => {
  await new Promise((resolve, reject) => {
    addEventListener('message', (ev) => {
      if (ev.source === parent && ev.data && ev.data.type === 'filex:port' && ev.ports[0]) resolve(ev.ports[0]);
    });
    parent.postMessage({ type: 'filex:hello', v: 1 }, '*');
    setTimeout(() => reject(new Error('filex did not answer')), 10000);
  });
  document.body.dataset.ready = '1';
})().catch((e) => { document.body.dataset.error = String((e && (e.message || e.code)) || e); });
`;

/**
 * The framed page: it measures its own sandbox and reports to the page that
 * framed it. `?out=` is the outside address, `?peer=` a blob: address the
 * outer page made (read across frames: measured and recorded, not asserted).
 */
const INNER_JS = `
(async () => {
  const q = new URLSearchParams(location.search);
  const out = q.get('out') || '';
  const peer = q.get('peer') || '';
  const base = location.href.slice(0, location.href.indexOf('/_appui/'));
  const tryDo = (fn) => { try { fn(); return 'reachable'; } catch (e) { return 'throws ' + e.name; } };
  const get = (u) => fetch(u).then((x) => 'reached ' + x.status, (e) => 'blocked ' + e.name);
  const read = (u) => fetch(u).then((r) => r.text()).then((t) => 'read ' + t, (e) => 'blocked ' + e.name);
  const report = {
    origin: String(self.origin),
    rtc: typeof window.RTCPeerConnection,
    localStorage: tryDo(() => localStorage.length),
    cookie: tryDo(() => document.cookie),
    parentDocument: tryDo(() => parent.document.title),
    topDocument: tryDo(() => top.document.title),
    blob: await read(URL.createObjectURL(new Blob(['inner blob']))),
    peerBlob: peer ? await read(peer) : 'none',
    api: await get(base + '/api/auth/me'),
    page: await get(base + '/admin/'),
    packageFile: await get('inner.js'),
    outside: out ? await get(out + '/from-inner') : 'none',
  };
  report.port = await new Promise((ok) => {
    addEventListener('message', (ev) => { if (ev.data && ev.data.type === 'filex:port') ok('a port'); });
    top.postMessage({ type: 'filex:hello', v: 1 }, '*');
    setTimeout(() => ok('no port'), 1500);
  });
  parent.postMessage({ type: 'inner-report', report }, '*');
})();
`;

const UI = zip({
  'index.html': '<!doctype html><html><head><meta charset="utf-8"><script src="app.js" defer></script></head><body><h1>frame</h1></body></html>',
  'app.js': APP_JS,
  'inner.html': '<!doctype html><html><head><meta charset="utf-8"><script src="inner.js" defer></script></head><body><p>inner</p></body></html>',
  'inner.js': INNER_JS,
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

interface InnerReport {
  origin: string;
  rtc: string;
  localStorage: string;
  cookie: string;
  parentDocument: string;
  topDocument: string;
  blob: string;
  peerBlob: string;
  api: string;
  page: string;
  packageFile: string;
  outside: string;
  port: string;
}
type Framed = { loaded: false } | { loaded: true; report: InnerReport };
type Probe = Window & {
  __violations: string[];
  __blob: (text: string) => Promise<{ fetch: string; xhr: string; url: string }>;
  __get: (u: string) => Promise<string>;
  __frame: (src: string, wait: number) => Promise<Framed>;
};

/** The interface's own address: everything up to its version's path. */
const ownPath = (frame: Frame) => frame.evaluate(() => location.href.slice(0, location.href.lastIndexOf('/') + 1));

/* ── the spec ───────────────────────────────────────────────────────────── */

test.describe.serial('An app’s interface frames its own package and reads its own blob: - only when granted', () => {
  let api: APIRequestContext;
  let outside: Server;
  let outsideURL = '';
  const hits: string[] = [];
  let tag = '';
  let store = '';
  let app = '';
  let plain = '';
  let ext = '';
  let rows: ReviewRow[] = [];
  let plainRows: ReviewRow[] = [];

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-frm-${tag}-${Date.now()}`;
    app = `frm-${tag}`;
    plain = `frm-plain-${tag}`;
    ext = `frm${tag.slice(0, 3)}`;
    outside = createServer((req, res) => {
      hits.push(req.url ?? '');
      res.writeHead(200, { 'Access-Control-Allow-Origin': '*', 'Content-Type': 'text/html' }).end('<p>outside</p>');
    });
    await new Promise<void>((ok) => outside.listen(0, '127.0.0.1', ok));
    outsideURL = `http://127.0.0.1:${(outside.address() as AddressInfo).port}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await removeAppByName(api, app);
    await removeAppByName(api, plain);
    const mount = `/tmp/filex-${store}`;
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, `doc.${ext}`), 'a document');
    writeFileSync(join(root, `doc.${ext}p`), 'a document for the app without the grant');
    await seedLocalStorage(api, store, mount);
    rows = await installInterfaceApp(api, manifest(app, ext, { frame_package: true, connect_blob: true }), UI);
    plainRows = await installInterfaceApp(api, manifest(plain, `${ext}p`, {}), UI);
  });

  test.afterAll(async () => {
    await removeAppByName(api, app);
    await removeAppByName(api, plain);
    await dropStorageByName(api, store);
    await api.dispose();
    await new Promise<void>((ok) => outside.close(() => ok()));
  });

  test('the review lists both, only for the app that asks', async () => {
    const ids = rows.map((r) => r.id);
    expect(ids).toContain('ui:frame-package');
    expect(ids).toContain('ui:connect-blob');
    // In the administrator's language (English or Turkish here): a sentence,
    // never the permission's id.
    const label = (id: string) => rows.find((r) => r.id === id)?.label ?? '';
    expect(label('ui:frame-package')).not.toBe('');
    expect(label('ui:frame-package')).not.toBe('ui:frame-package');
    expect(label('ui:connect-blob')).toContain('blob:');
    expect(label('ui:connect-blob')).not.toBe('ui:connect-blob');
    const plainIds = plainRows.map((r) => r.id);
    expect(plainIds, 'off unless the manifest asks').not.toContain('ui:frame-package');
    expect(plainIds, 'off unless the manifest asks').not.toContain('ui:connect-blob');
  });

  test('a page of its own package opens in a frame, and is a sandbox of its own', async ({ page, browserName }) => {
    // What the browser says when it refuses to draw a frame: Chromium logs
    // "Refused to frame ..." naming frame-src or frame-ancestors. The framed
    // page's parent is the interface, an opaque origin, which no
    // frame-ancestors source matches (not even *): the page must carry none.
    const frameRefusals: string[] = [];
    page.on('console', (msg) => {
      const text = msg.text();
      if (/Refused to frame|frame-ancestors/i.test(text)) frameRefusals.push(text);
    });
    const frame = await openAppInterface(page, store, `doc.${ext}`);
    const own = await ownPath(frame);
    const peer = (await frame.evaluate((t) => (window as unknown as Probe).__blob(t), 'outer blob')).url;
    const src = `${own}inner.html?out=${encodeURIComponent(outsideURL)}&peer=${encodeURIComponent(peer)}`;
    await frame.evaluate(() => {
      (window as unknown as Probe).__violations.length = 0;
    });
    const got = await frame.evaluate(({ src }) => (window as unknown as Probe).__frame(src, 15000), { src });
    expect(got.loaded, 'the framed page of its own package REALLY loaded: its script ran and reported').toBe(true);
    const violations = await frame.evaluate(() => (window as unknown as Probe).__violations);
    expect(violations.filter((v) => /^(frame-src|child-src)\b/.test(v)), 'the interface was told of no frame refusal').toEqual([]);
    if (browserName === 'chromium') {
      expect(frameRefusals, 'Chromium refused no frame (frame-src or frame-ancestors)').toEqual([]);
    } else if (frameRefusals.length) {
      test.info().annotations.push({ type: 'measured', description: `${browserName} logged: ${frameRefusals.join(' | ')}` });
    }
    const r = (got as { loaded: true; report: InnerReport }).report;
    expect(r.origin, 'an opaque origin').toBe('null');
    expect(r.rtc, 'the bootstrap ran in the framed page too: no WebRTC').toBe('undefined');
    expect(r.localStorage).toMatch(/^throws/);
    expect(r.cookie).toMatch(/^throws/);
    expect(r.parentDocument, 'another origin than the frame that opened it').toMatch(/^throws/);
    expect(r.topDocument, 'not filex’s page either').toMatch(/^throws/);
    expect(r.blob, 'its own blob: - the same grant as the page that framed it').toBe('read inner blob');
    expect(r.api).toMatch(/^blocked/);
    expect(r.page).toMatch(/^blocked/);
    expect(r.packageFile, 'reading its package with fetch is ui:package-fetch, not granted here').toMatch(/^blocked/);
    expect(r.outside).toMatch(/^blocked/);
    expect(r.port, 'filex’s bridge answers only the frame filex drew').toBe('no port');
    expect(hits, 'nothing reached the outside address').toEqual([]);
    test.info().annotations.push({
      type: 'measured',
      description: `${browserName}: a framed page reading the outer page's blob: address → ${r.peerBlob}`,
    });
  });

  test('it frames nothing else: another app, filex, the outside, data: or blob:', async ({ page }) => {
    // A page of filex framed from inside would load the SPA, which reports
    // nothing: what shows it is a response for it in a frame of the interface.
    const filexPages: string[] = [];
    page.on('response', (res: Response) => {
      if (!res.url().includes('/admin/')) return;
      try {
        if (res.frame() !== page.mainFrame()) filexPages.push(res.url());
      } catch {
        /* a service worker's own request has no frame */
      }
    });
    const frame = await openAppInterface(page, store, `doc.${ext}`);
    const own = await ownPath(frame);
    const list = (await (await api.get('/api/files/plugins/actions')).json()) as { views?: Array<{ plugin: string; ui?: { url: string } }> };
    const plainURL = list.views?.find((v) => v.plugin === plain)?.ui?.url ?? '';
    expect(plainURL, 'the other app has an interface address').toContain(`/_appui/${plain}/`);
    const base = own.slice(0, own.indexOf('/_appui/'));
    const report = '<script>parent.postMessage({type:"inner-report",report:"escaped"},"*")</script>';
    const r = await frame.evaluate(
      async ({ others, report }) => {
        const p = window as unknown as Probe;
        const blobPage = URL.createObjectURL(new Blob([report], { type: 'text/html' }));
        const [otherApp, filexPage, outsidePage, dataPage, blobDoc] = await Promise.all([
          p.__frame(others.otherApp, 5000),
          p.__frame(others.filexPage, 5000),
          p.__frame(others.outsidePage, 5000),
          p.__frame('data:text/html,' + encodeURIComponent(report), 5000),
          p.__frame(blobPage, 5000),
        ]);
        return {
          otherApp: otherApp.loaded,
          filexPage: filexPage.loaded,
          outsidePage: outsidePage.loaded,
          dataPage: dataPage.loaded,
          blobDoc: blobDoc.loaded,
        };
      },
      {
        others: {
          otherApp: base + plainURL.replace('index.html', 'inner.html'),
          filexPage: base + '/admin/',
          outsidePage: outsideURL + '/framed',
        },
        report,
      },
    );
    expect(r).toEqual({ otherApp: false, filexPage: false, outsidePage: false, dataPage: false, blobDoc: false });
    expect(filexPages, 'no page of filex was served into a frame of the interface').toEqual([]);
    expect(hits, 'nothing reached the outside address').toEqual([]);
  });

  test('it reads a blob: address it made, with fetch and XHR - and nothing else opened', async ({ page }) => {
    const frame = await openAppInterface(page, store, `doc.${ext}`);
    const own = await ownPath(frame);
    const base = own.slice(0, own.indexOf('/_appui/'));
    const got = await frame.evaluate((t) => (window as unknown as Probe).__blob(t), 'blob says hi');
    expect(got.fetch).toBe('read blob says hi');
    expect(got.xhr, 'XMLHttpRequest, as the editor loads its document').toBe('read blob says hi');
    const others = await frame.evaluate(
      async ({ api, pkg, out }) => {
        const p = window as unknown as Probe;
        return { api: await p.__get(api), pkg: await p.__get(pkg), outside: await p.__get(out) };
      },
      { api: base + '/api/auth/me', pkg: own + 'inner.js', out: outsideURL + '/fetch' },
    );
    expect(others.api).toMatch(/^blocked/);
    expect(others.pkg, 'its package is ui:package-fetch').toMatch(/^blocked/);
    expect(others.outside).toMatch(/^blocked/);
    expect(hits, 'nothing reached the outside address').toEqual([]);
  });

  test('without the grant: no frame of its own package, no blob: read', async ({ page }) => {
    const frame = await openAppInterface(page, store, `doc.${ext}p`);
    const own = await ownPath(frame);
    expect(own).toContain(`/_appui/${plain}/`);
    const framed = await frame.evaluate((src) => (window as unknown as Probe).__frame(src, 6000), `${own}inner.html`);
    expect(framed.loaded, 'frame-src is none').toBe(false);
    const got = await frame.evaluate((t) => (window as unknown as Probe).__blob(t), 'not for you');
    expect(got.fetch).toMatch(/^blocked/);
    expect(got.xhr).toMatch(/^blocked/);
  });
});

