/**
 * 233-app-coedit — editing a file together through filex's relay (task #189,
 * filex 0.56, `files:co-edit`). In every engine `E2E_BROWSERS` names:
 *
 *   1. `co_edit: {open: true}` is granted as `files:co-edit`, which the
 *      review lists in the administrator's words;
 *   2. two browser contexts open the same file in an app's interface: the
 *      first starts the session and stores the base document, the second
 *      joins it (filex waits for the base by itself) and opens the base -
 *      never a key, never the account id;
 *   3. what one appends reaches the other, in one order, opened by filex
 *      (the app never holds a key); the changes lease goes to one writer at
 *      a time, and only to one that has seen every change; a cursor reaches
 *      the other and is not kept;
 *   4. a save with `through` is recorded in the log for everybody;
 *   5. a member whose frame goes is out of the session for the others.
 *
 * The app is built here, like 228's and 229's: an interface-only app whose
 * script speaks the bridge's protocol itself. All red before 0.56: filex
 * refused a manifest with a `co_edit` block, and coedit.* answered
 * `unavailable`.
 */
import { test, expect, type APIRequestContext, type Browser, type Frame, type Page } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { installInterfaceApp, openAppInterface, removeAppByName, type ReviewRow } from '../helpers/appInterface';
import { zip } from '../helpers/zip';

/* ── the app ────────────────────────────────────────────────────────────── */

/**
 * The bridge's hello, `session.get`, then `coedit.join`: the member that
 * started the session puts the bytes it opened as the base; the others read
 * the base. It subscribes from 0 and keeps every entry, cursor and drop in
 * window.__log; `__call(m, p)` is any call's raw answer.
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
window.__log = { entries: [], cursors: [], dropped: [] };
window.__hello = null;
window.__base = null;
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
      return;
    }
    if (m && m.event === 'coedit.entry') window.__log.entries.push(m.data);
    if (m && m.event === 'coedit.cursor') window.__log.cursors.push(m.data);
    if (m && m.event === 'coedit.dropped') window.__log.dropped.push(m.data);
  };
  await call('session.get');
  const joined = await call('coedit.join', { index: 0 });
  if (joined.error) throw new Error('join: ' + joined.error.code + ' ' + (joined.error.message || ''));
  window.__hello = joined.result;
  if (joined.result.created) {
    const read = await call('file.read', { index: 0, as: 'bytes' });
    const put = await call('coedit.blob.put', { name: 'base', data: read.result.bytes }, [read.result.bytes]);
    if (put.error) throw new Error('base: ' + put.error.code);
  }
  const base = await call('coedit.blob.get', { name: 'base' });
  window.__base = base.error ? 'error:' + base.error.code : new TextDecoder().decode(base.result.bytes);
  await call('coedit.subscribe', { from: 0 });
  document.body.dataset.ready = '1';
})().catch((e) => { document.body.dataset.error = String((e && (e.message || e.code)) || e); });
`;

const UI = zip({
  'index.html': '<!doctype html><html><head><meta charset="utf-8"><script src="app.js" defer></script></head><body><h1>together</h1></body></html>',
  'app.js': APP_JS,
});

function manifest(name: string, ext: string) {
  return {
    manifest_version: 1,
    name,
    version: '1.0.0',
    filex: '>=0.56.0',
    label: { en: name },
    permissions: ['files:read', 'files:write'],
    co_edit: { open: true },
    ui: { bundle: {} },
    views: [{ id: 'view', placement: 'viewer', ui: 'index.html', label: { en: name }, applies: { ext: [ext] } }],
  };
}

interface Answer {
  result?: Record<string, unknown> | null;
  error?: { code: string; message?: string };
}
interface Log {
  entries: Array<{ seq: number; kind: string; client: string; body?: unknown; member?: { client: string; user: string; indexUser: number }; through?: number }>;
  cursors: Array<{ client: string; cursor: unknown }>;
  dropped: Array<{ from: number; reason: string }>;
}
type Probe = Window & {
  __log: Log;
  __hello: { session: string; created: boolean; me: { client: string; user: string; indexUser: number; canEdit: boolean } } | null;
  __base: string | null;
  __call: (m: string, p?: unknown) => Promise<Answer>;
};

const log = (f: Frame) => f.evaluate(() => (window as unknown as Probe).__log);
const hello = (f: Frame) => f.evaluate(() => (window as unknown as Probe).__hello);
const ask = (f: Frame, m: string, p?: unknown) => f.evaluate(([m2, p2]) => (window as unknown as Probe).__call(m2 as string, p2), [m, p] as const);

async function second(browser: Browser): Promise<Page> {
  const ctx = await browser.newContext();
  return ctx.newPage();
}

/* ── the spec ───────────────────────────────────────────────────────────── */

test.describe.serial('Two people edit one file together through filex’s relay', () => {
  let api: APIRequestContext;
  let tag = '';
  let store = '';
  let app = '';
  let ext = '';
  let rows: ReviewRow[] = [];

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-coed-${tag}-${Date.now()}`;
    app = `coed-${tag}`;
    ext = `coe${tag.slice(0, 3)}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await removeAppByName(api, app);
    const mount = `/tmp/filex-${store}`;
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, `doc.${ext}`), 'the document as it was opened');
    await seedLocalStorage(api, store, mount);
    rows = await installInterfaceApp(api, manifest(app, ext), UI);
  });

  test.afterAll(async () => {
    await removeAppByName(api, app);
    await dropStorageByName(api, store);
    await api.dispose();
  });

  test('the review lists files:co-edit in the administrator’s words', async () => {
    const row = rows.find((r) => r.id === 'files:co-edit');
    expect(row, 'listed').toBeTruthy();
    expect(row!.label).not.toBe('files:co-edit');
    expect(row!.label).toContain('together');
  });

  test('the second joins the first’s session, both read one log, the lease goes to one writer at a time', async ({ page, browser }) => {
    const a = await openAppInterface(page, store, `doc.${ext}`);
    const ha = await hello(a);
    expect(ha?.created).toBe(true);
    expect(ha?.me.indexUser).toBe(1);
    expect(ha?.me.canEdit).toBe(true);
    expect(JSON.stringify(ha), 'never a key, never an account id').not.toMatch(/"key"|"token"|"user_id"/);

    const pageB = await second(browser);
    try {
      const b = await openAppInterface(pageB, store, `doc.${ext}`);
      const hb = await hello(b);
      expect(hb?.created).toBe(false);
      expect(hb?.session).toBe(ha?.session);
      expect(hb?.me.indexUser).toBe(2);
      expect(await b.evaluate(() => (window as unknown as Probe).__base), 'the base the first opened').toBe('the document as it was opened');

      // What A appends reaches B, opened, from A.
      const lock = await ask(a, 'coedit.append', { kind: 'lock', body: { blocks: ['p1'] } });
      expect(lock.error).toBeUndefined();
      await expect.poll(async () => (await log(b)).entries.find((e) => e.kind === 'lock')?.body).toEqual({ blocks: ['p1'] });
      const fromA = (await log(b)).entries.find((e) => e.kind === 'lock')!;
      expect(fromA.client).toBe(ha!.me.client);
      // Both see the same order.
      await expect.poll(async () => (await log(a)).entries.map((e) => e.seq).join(',')).toBe((await log(b)).entries.map((e) => e.seq).join(','));

      // The lease: B, having seen every change, takes it; A cannot while B holds it.
      const lb = await ask(b, 'coedit.lease', { op: 'acquire', changesSeen: 0 });
      expect(lb.result).toEqual({ granted: true });
      const la = await ask(a, 'coedit.lease', { op: 'acquire', changesSeen: 0 });
      expect(la.result).toEqual({ granted: false });
      const noLease = await ask(a, 'coedit.append', { kind: 'changes', body: { changes: ['x'] } });
      expect(noLease.error).toMatchObject({ code: 'failed', message: 'no_lease' });
      const ch = await ask(b, 'coedit.append', { kind: 'changes', body: { changes: ['typed'] } });
      expect(ch.error).toBeUndefined();
      const changesSeq = (ch.result as { seq: number }).seq;
      await expect.poll(async () => (await log(a)).entries.find((e) => e.kind === 'changes')?.body).toEqual({ changes: ['typed'] });

      // A cursor reaches the other and is not kept.
      await ask(b, 'coedit.cursor', { cursor: { pos: 42 } });
      await expect.poll(async () => (await log(a)).cursors.at(-1)?.cursor).toEqual({ pos: 42 });

      // A save with `through` is recorded for everybody.
      const saved = await ask(b, 'file.save', { data: 'saved by B', through: changesSeq });
      expect(saved.error).toBeUndefined();
      await expect.poll(async () => (await log(a)).entries.find((e) => e.kind === 'saved')?.through).toBe(changesSeq);

      // B's page goes the way a person leaves it: navigated away, its
      // `pagehide` runs and the frame's leave goes out on the way.
      // ⚠ Not `context.close()`: Playwright closing a context under a page
      // is nothing a person does - Chromium and WebKit fire no `pagehide`
      // and Firefox sends nothing from it (measured on Linux, Playwright
      // 1.59) - and the member then stays until the relay times it out
      // (MemberTTL 45 s + a sweep), past this test's 30 s.
      await pageB.goto('about:blank');
    } finally {
      await pageB.context().close();
    }

    // B's frame went: the others see it leave.
    await expect.poll(async () => (await log(a)).entries.some((e) => e.kind === 'leave'), { timeout: 20_000 }).toBe(true);
  });
});
