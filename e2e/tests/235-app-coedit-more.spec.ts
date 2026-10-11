/**
 * 235-app-coedit-more — editing together, the four rulings of 2026-10-09
 * (task #189, filex 0.56; docs/APP-PLUGINS-API.md → Editing together). In
 * every engine `E2E_BROWSERS` names:
 *
 *   1. a person who may only READ the file (a `viewer` on an RBAC storage)
 *      joins the session as a watcher: `canEdit: false`, the base and every
 *      change reach them live, the relay refuses their entries and the lease
 *      (`read_only`), and the audit says they watched
 *      (`app_plugin.coedit_watch`);
 *   2. a change made while the connection is gone is kept in the browser
 *      (IndexedDB `filex-coedit`) and reaches the others once it is back;
 *   3. a change that never left before the page went is sent when the file
 *      is opened again - at once when nobody wrote since; after the person's
 *      yes, asked above the frame in the server's words, when somebody did;
 *   4. a file of an end-to-end encrypted folder is edited together: the
 *      member that starts the session makes its key in the page and gives
 *      the server only that key sealed with the folder key (…/key); the
 *      member that joins opens it with the folder key; no request either
 *      page sent carries the document's plaintext, and the join never
 *      answers a key.
 *
 * Cursors across processes (the fourth ruling) need a second filex process
 * and are the relay's Go test (coedit/relay_test.go
 * TestCursors_ReachTheMembersOfAnotherProcess).
 *
 * The app is built here, like 233's: an interface-only app whose script
 * speaks the bridge's protocol itself.
 *
 * ⚠ Language pinned to English on the ACCOUNT (lesson #616) and restored.
 */
import { test, expect, type APIRequestContext, type Browser, type Frame, type Page, type Request } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';
import { installInterfaceApp, openAppInterface, removeAppByName } from '../helpers/appInterface';
import { zip } from '../helpers/zip';

const PW = 'correct horse battery staple';
const PREFS = '/api/me/prefs?surface=web';
const SETTING = '/api/admin/settings/apps.e2e_plaintext';
const VIEWER = { email: 'coedit-watcher@example.test', password: 'coedit-watcher-pw-2026', name: 'Watcher' };
const SECRET = 'içerik: birlikte gizli plan 2027';

/* ── the app ────────────────────────────────────────────────────────────── */

/**
 * 233's app: `session.get`, `coedit.join`, the base (put by the member that
 * started the session, read by the others), `coedit.subscribe` from 0; every
 * entry, cursor and drop in window.__log. `__call(m, p)` is any call's
 * answer; `__fire(m, p)` sends one without waiting and keeps its answer in
 * `__fired[k]` once it comes (a change made while the connection is gone).
 */
const APP_JS = `
let port = null;
let seq = 0;
let fseq = 0;
const pending = new Map();
const call = (method, params, transfer) => new Promise((ok) => {
  const id = ++seq;
  pending.set(id, ok);
  port.postMessage({ id, method, params }, transfer || []);
});
window.__log = { entries: [], cursors: [], dropped: [] };
window.__hello = null;
window.__base = null;
window.__fired = {};
window.__call = (m, p) => call(m, p);
window.__fire = (m, p) => {
  const k = ++fseq;
  window.__fired[k] = null;
  call(m, p).then((a) => { window.__fired[k] = a; });
  return k;
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

interface Answer {
  result?: Record<string, unknown> | null;
  error?: { code: string; message?: string };
}
interface Log {
  entries: Array<{ seq: number; kind: string; client: string; body?: unknown }>;
  cursors: Array<{ client: string; cursor: unknown }>;
  dropped: Array<{ from: number; reason: string }>;
}
type Probe = Window & {
  __log: Log;
  __hello: { session: string; created: boolean; me: { client: string; canEdit: boolean } } | null;
  __base: string | null;
  __fired: Record<number, Answer | null>;
  __call: (m: string, p?: unknown) => Promise<Answer>;
  __fire: (m: string, p?: unknown) => number;
};

const log = (f: Frame) => f.evaluate(() => (window as unknown as Probe).__log);
const hello = (f: Frame) => f.evaluate(() => (window as unknown as Probe).__hello);
const base = (f: Frame) => f.evaluate(() => (window as unknown as Probe).__base);
const ask = (f: Frame, m: string, p?: unknown) => f.evaluate(([m2, p2]) => (window as unknown as Probe).__call(m2 as string, p2), [m, p] as const);
const fire = (f: Frame, m: string, p?: unknown) => f.evaluate(([m2, p2]) => (window as unknown as Probe).__fire(m2 as string, p2), [m, p] as const);
const fired = (f: Frame, k: number) => f.evaluate((k2) => (window as unknown as Probe).__fired[k2], k);
/** The bodies of the `changes` entries a member read, in the log's order. */
const changes = async (f: Frame) => (await log(f)).entries.filter((e) => e.kind === 'changes').map((e) => e.body);

/** How many changes this browser keeps for the relay (lib/coeditOutbox). */
function keptCount(page: Page): Promise<number> {
  return page.evaluate(
    () =>
      new Promise<number>((resolve) => {
        const req = indexedDB.open('filex-coedit');
        // Never create it here: filex's own store makes it, with its schema.
        req.onupgradeneeded = () => req.transaction?.abort();
        req.onerror = () => resolve(-1);
        req.onsuccess = () => {
          const db = req.result;
          if (!db.objectStoreNames.contains('outbox')) {
            db.close();
            resolve(0);
            return;
          }
          const q = db.transaction('outbox', 'readonly').objectStore('outbox').count();
          q.onsuccess = () => {
            db.close();
            resolve(q.result);
          };
          q.onerror = () => {
            db.close();
            resolve(-1);
          };
        };
      }),
  );
}

/** Open `fileName` in the explorer at `where` and wait for the app's frame, connected (not yet started). */
async function frameOf(page: Page, where: string, store: string, fileName: string): Promise<Frame> {
  await page.goto(`${where}?storage=${encodeURIComponent(store)}`);
  const row = page.locator(`[data-fe-path="${store}://${fileName}"]`);
  await row.first().waitFor();
  await row.getByText(fileName, { exact: true }).dblclick();
  const el = page.locator('iframe[data-testid="app-frame"]');
  await expect(el).toHaveAttribute('sandbox', 'allow-scripts');
  await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible({ timeout: 20_000 });
  return (await (await el.elementHandle())!.contentFrame())!;
}

/** The app's script got through its join (or said why not). */
async function started(frame: Frame) {
  await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error, null, { timeout: 45_000 });
  expect(await frame.evaluate(() => document.body.dataset.error ?? null), 'the interface started').toBeNull();
}

async function signInAs(page: Page, who: { email: string; password: string }) {
  await page.addInitScript(() => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  });
  await page.goto('/admin/login');
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(who.email);
  await page.getByLabel(/password|parola|şifre/i).fill(who.password);
  await page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Oturum aç', exact: true }))
    .first()
    .click();
  // ⚠ The start page, never the sign-in page itself: `/admin/login` is under
  // /admin/ too, so a bare /(drive|admin)/ matched before the sign-in was
  // answered, and the next goto cancelled it (the explorer then opened on the
  // sign-in form, nightly 2026-10-09, all three engines).
  await page.waitForURL((u) => /^\/(drive|admin)\//.test(u.pathname) && !u.pathname.startsWith('/admin/login'), { timeout: 15_000 });
}

async function second(browser: Browser): Promise<Page> {
  const ctx = await browser.newContext();
  return ctx.newPage();
}

async function userId(api: APIRequestContext, email: string): Promise<number> {
  const raw = (await (await api.get('/api/admin/users')).json()) as
    | { id: number; email: string }[]
    | { users?: { id: number; email: string }[]; items?: { id: number; email: string }[] };
  const list = Array.isArray(raw) ? raw : (raw.users ?? raw.items ?? []);
  const found = list.find((u) => u.email === email);
  expect(found, `${email} exists`).toBeTruthy();
  return found!.id;
}

/* ── the encrypted folder (232's steps) ─────────────────────────────────── */

async function openStorage(page: Page, store: string) {
  await page.addInitScript(() => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  });
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(store)}`);
  await expect(page.getByTestId('sidenav-new').first()).toBeVisible({ timeout: 30_000 });
}

function row(page: Page, name: string) {
  return page
    .locator('.fe-list__row[data-fe-path]')
    .filter({ has: page.locator('.fe-list__name', { hasText: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`) }) })
    .first();
}

async function openFolder(page: Page, name: string) {
  const target = await settled(row(page, name));
  const wire = ((await target.getAttribute('data-fe-path')) ?? '').replace(/\/+$/, '');
  const listed = page
    .waitForResponse(
      (r) => {
        const u = new URL(r.url());
        return u.searchParams.get('action') === 'index' && (u.searchParams.get('path') ?? '').replace(/\/+$/, '') === wire;
      },
      { timeout: 20_000 },
    )
    .catch(() => undefined);
  await target.dblclick();
  await target.dispose();
  if (wire) await listed;
  await page.waitForTimeout(700);
}

async function createEncryptedFolder(page: Page, name: string) {
  await page.getByTestId('sidenav-new').first().click();
  await page
    .locator('.fe-ctx__item', { has: page.locator('.fe-ctx__label', { hasText: /^New folder$/ }) })
    .first()
    .click();
  await page.getByText('Create encrypted folder…', { exact: true }).click();
  const dialog = page.locator('.fe-modal__card').filter({ has: page.getByTestId('e2e-create-names') });
  await expect(dialog).toBeVisible();
  await dialog.locator('input[type="text"]').first().fill(name);
  const pws = dialog.locator('input[type="password"]');
  await pws.nth(0).fill(PW);
  await pws.nth(1).fill(PW);
  await expect(dialog.getByTestId('e2e-level-content')).toBeChecked();
  await dialog.getByTestId('e2e-create-ack').check();
  await dialog.getByRole('button', { name: 'Create encrypted folder', exact: true }).click();
  const keyEl = page.locator('.fe-e2e-rk__key');
  await expect(keyEl).toBeVisible({ timeout: 20_000 });
  await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(keyEl).toBeHidden();
}

async function uploadFile(page: Page, name: string, body: string) {
  const answered = page.waitForResponse(
    (r) => r.request().method() === 'POST' && new URL(r.url()).searchParams.get('action') === 'upload',
  );
  await page.locator('input[type="file"]').first().setInputFiles({ name, mimeType: 'application/octet-stream', buffer: Buffer.from(body, 'utf8') });
  await answered;
  await expect(row(page, name)).toBeVisible({ timeout: 15_000 });
}

async function unlock(page: Page) {
  const lock = page.locator('.fe-e2e-lock');
  await expect(lock).toBeVisible({ timeout: 20_000 });
  await lock.locator('input[type="password"]').fill(PW);
  await lock.getByRole('button', { name: 'Unlock', exact: true }).click();
  await expect(page.locator('.fe-e2e-strip')).toBeVisible({ timeout: 20_000 });
}

async function openInApp(page: Page, name: string): Promise<Frame> {
  await (await settled(row(page, name))).dblclick();
  const el = page.locator('iframe[data-testid="app-frame"]');
  await expect(el).toHaveAttribute('sandbox', 'allow-scripts', { timeout: 20_000 });
  await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible({ timeout: 20_000 });
  const frame = (await (await el.elementHandle())!.contentFrame())!;
  await started(frame);
  return frame;
}

/* ── the spec ───────────────────────────────────────────────────────────── */

test.describe.serial('Editing together: watchers, changes kept through a lost connection, encrypted folders', () => {
  let api: APIRequestContext;
  let store = '';
  let e2eStore = '';
  let app = '';
  let ext = '';
  let prefsBefore: Record<string, unknown> = {};

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-coed2-${tag}-${Date.now()}`;
    e2eStore = `e2e-coed2e-${tag}-${Date.now()}`;
    app = `coed2-${tag}`;
    ext = `ce${tag.slice(0, 3)}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    const doc = got.ok() ? (await got.json()).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } })).ok()).toBe(true);
    expect((await api.put(SETTING, { data: { value: 'true' } })).ok()).toBe(true);
    await removeAppByName(api, app);

    // An RBAC storage: the watcher is a viewer on it.
    const mount = `/tmp/filex-${store}`;
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    for (const f of ['watch', 'live', 'again', 'asked']) writeFileSync(join(root, `${f}.${ext}`), `the ${f} document as it was opened`);
    await seedLocalStorage(api, store, mount, { rbac_enabled: true });
    await dropStorageByName(api, e2eStore);
    await seedLocalStorage(api, e2eStore, `/tmp/filex-${e2eStore}`);

    await api.post('/api/admin/users', {
      data: { email: VIEWER.email, password: VIEWER.password, display_name: VIEWER.name, role: 'user' },
    });
    const g = await api.post('/api/files/permissions', {
      data: { path: `${store}://`, user_id: await userId(api, VIEWER.email), level: 'viewer', is_dir: true },
    });
    expect(g.ok(), `grant viewer: ${g.status()} ${await g.text()}`).toBe(true);

    await installInterfaceApp(
      api,
      {
        manifest_version: 1,
        name: app,
        version: '1.0.0',
        filex: '>=0.56.0',
        label: { en: app },
        permissions: ['files:read', 'files:write'],
        co_edit: { open: true },
        encrypted_folders: { open: true },
        ui: { bundle: {} },
        views: [{ id: 'view', placement: 'viewer', ui: 'index.html', label: { en: app }, applies: { ext: [ext] } }],
      },
      UI,
    );
  });

  test.afterAll(async () => {
    await api?.put(SETTING, { data: { value: 'true' } }).catch(() => undefined);
    await api?.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await removeAppByName(api, app).catch(() => undefined);
    await dropStorageByName(api, store).catch(() => undefined);
    await dropStorageByName(api, e2eStore).catch(() => undefined);
    await api?.dispose();
  });

  test('a person who may only read the file watches: every change live, no write, audited as watching', async ({ page, browser }) => {
    const name = `watch.${ext}`;
    const a = await openAppInterface(page, store, name);
    const ha = await hello(a);
    expect(ha?.created).toBe(true);
    expect(ha?.me.canEdit).toBe(true);

    const pageV = await second(browser);
    try {
      await signInAs(pageV, VIEWER);
      const v = await frameOf(pageV, '/drive/explore', store, name);
      await started(v);
      const hv = await hello(v);
      expect(hv?.session, 'the same session').toBe(ha?.session);
      expect(hv?.created).toBe(false);
      expect(hv?.me.canEdit, 'a watcher').toBe(false);
      expect(await base(v), 'the base the first opened').toBe('the watch document as it was opened');

      // The relay refuses whatever a watcher would write.
      expect((await ask(v, 'coedit.append', { kind: 'lock', body: { blocks: ['p1'] } })).error?.code).toBe('read_only');
      expect((await ask(v, 'coedit.lease', { op: 'acquire', changesSeen: 0 })).error?.code).toBe('read_only');
      expect(await keptCount(pageV), 'a watcher keeps nothing').toBeLessThanOrEqual(0);

      // What the editor writes reaches the watcher, live.
      expect((await ask(a, 'coedit.lease', { op: 'acquire', changesSeen: 0 })).result).toEqual({ granted: true });
      expect((await ask(a, 'coedit.append', { kind: 'changes', body: { changes: ['typed by the editor'] } })).error).toBeUndefined();
      await expect.poll(() => changes(v)).toEqual([{ changes: ['typed by the editor'] }]);
      expect((await log(v)).entries.some((e) => e.client === hv!.me.client && e.kind !== 'join'), 'nothing of the watcher’s in the log').toBe(false);

      // The audit says they watched.
      const res = await api.get(`/api/admin/audit?action=${encodeURIComponent('app_plugin.coedit_watch')}&limit=50`);
      expect(res.ok(), `audit: ${res.status()}`).toBe(true);
      const body = (await res.json()) as unknown[] | { items?: unknown[]; entries?: unknown[] };
      const rows = Array.isArray(body) ? body : (body.items ?? body.entries ?? []);
      expect(rows.some((r) => JSON.stringify(r).includes(hv!.session)), 'a coedit_watch row for this session').toBe(true);
    } finally {
      await pageV.context().close();
    }
  });

  test('a change made while the connection is gone is kept in the browser and reaches the others once it is back', async ({ page, browser }) => {
    const name = `live.${ext}`;
    const a = await openAppInterface(page, store, name);
    const pageB = await second(browser);
    try {
      const b = await openAppInterface(pageB, store, name);
      expect((await hello(b))?.session).toBe((await hello(a))?.session);
      expect((await ask(a, 'coedit.lease', { op: 'acquire', changesSeen: 0 })).result).toEqual({ granted: true });

      await page.context().setOffline(true);
      const k = await fire(a, 'coedit.append', { kind: 'changes', body: { changes: ['typed offline'] } });
      await expect.poll(() => keptCount(page), { timeout: 15_000 }).toBe(1);
      await page.waitForTimeout(1500);
      expect(await changes(b), 'nothing reached the others yet').toEqual([]);
      expect(await fired(a, k), 'the interface waits').toBeNull();

      await page.context().setOffline(false);
      await expect.poll(() => fired(a, k), { timeout: 45_000 }).not.toBeNull();
      expect((await fired(a, k))?.error, JSON.stringify(await fired(a, k))).toBeUndefined();
      await expect.poll(() => changes(b), { timeout: 30_000 }).toEqual([{ changes: ['typed offline'] }]);
      await expect.poll(() => keptCount(page)).toBe(0);
    } finally {
      await page.context().setOffline(false);
      await pageB.context().close();
    }
  });

  test('a change that never left before the page went is sent when the file is opened again, nobody having written since', async ({ page, browser }) => {
    const name = `again.${ext}`;
    const a = await openAppInterface(page, store, name);
    const pageB = await second(browser);
    try {
      const b = await openAppInterface(pageB, store, name);
      expect((await ask(a, 'coedit.lease', { op: 'acquire', changesSeen: 0 })).result).toEqual({ granted: true });

      // The changes never leave this page (the connection to the relay's
      // append is gone); the rest of the page still reaches the server.
      await page.route('**/coedit/*/append', (r) => r.abort('internetdisconnected'));
      await fire(a, 'coedit.append', { kind: 'changes', body: { changes: ['kept while away'] } });
      await expect.poll(() => keptCount(page), { timeout: 15_000 }).toBe(1);
      // The lease given back now: the page's leave on the way out (a
      // keepalive request) is not something every engine sends in time.
      expect((await ask(a, 'coedit.lease', { op: 'release', changesSeen: 0 })).error).toBeUndefined();

      // The page goes (its member leaves), comes back.
      await page.goto('about:blank');
      await page.unroute('**/coedit/*/append');
      const again = await frameOf(page, '/admin/explore', store, name);
      await started(again);
      expect(await page.getByTestId('appframe-unsent').count(), 'nothing to ask').toBe(0);
      await expect.poll(() => changes(b), { timeout: 30_000 }).toEqual([{ changes: ['kept while away'] }]);
      expect(await changes(again), 'in the log the reopened interface reads').toEqual([{ changes: ['kept while away'] }]);
      expect(await keptCount(page)).toBe(0);
    } finally {
      await page.unroute('**/coedit/*/append').catch(() => undefined);
      await pageB.context().close();
    }
  });

  test('when somebody wrote since, the person is asked in the server’s words, and their yes sends it on top', async ({ page, browser }) => {
    const name = `asked.${ext}`;
    const a = await openAppInterface(page, store, name);
    const pageB = await second(browser);
    try {
      const b = await openAppInterface(pageB, store, name);
      expect((await ask(a, 'coedit.lease', { op: 'acquire', changesSeen: 0 })).result).toEqual({ granted: true });
      await page.route('**/coedit/*/append', (r) => r.abort('internetdisconnected'));
      await fire(a, 'coedit.append', { kind: 'changes', body: { changes: ['mine, from before'] } });
      await expect.poll(() => keptCount(page), { timeout: 15_000 }).toBe(1);
      expect((await ask(a, 'coedit.lease', { op: 'release', changesSeen: 0 })).error).toBeUndefined();
      await page.goto('about:blank');
      await page.unroute('**/coedit/*/append');

      // Meanwhile the other writes.
      await expect.poll(async () => (await ask(b, 'coedit.lease', { op: 'acquire', changesSeen: 0 })).result, { timeout: 20_000 }).toEqual({ granted: true });
      const theirs = await ask(b, 'coedit.append', { kind: 'changes', body: { changes: ['theirs, meanwhile'] } });
      expect(theirs.error).toBeUndefined();
      const theirSeq = (theirs.result as { seq: number }).seq;
      expect((await ask(b, 'coedit.lease', { op: 'release', changesSeen: theirSeq })).error).toBeUndefined();

      // Opened again: asked above the frame, in the server's words.
      const again = await frameOf(page, '/admin/explore', store, name);
      const q = page.getByTestId('appframe-unsent');
      await expect(q).toBeVisible({ timeout: 30_000 });
      await expect(q).toContainText(name);
      await expect(q).toContainText('Others have edited the document since');
      await expect(page.getByTestId('appframe-unsent-discard')).toHaveText('Discard them');
      await page.getByTestId('appframe-unsent-apply').click();
      await expect(q).toBeHidden();
      await started(again);
      await expect.poll(() => changes(b), { timeout: 30_000 }).toEqual([{ changes: ['theirs, meanwhile'] }, { changes: ['mine, from before'] }]);
      expect(await keptCount(page)).toBe(0);
    } finally {
      await page.unroute('**/coedit/*/append').catch(() => undefined);
      await pageB.context().close();
    }
  });

  test('a file of an encrypted folder is edited together; the server holds only the key sealed with the folder key', async ({ page, browser }) => {
    const name = `ortak.${ext}`;
    await openStorage(page, e2eStore);
    await createEncryptedFolder(page, 'Kasa');
    await openFolder(page, 'Kasa');
    await uploadFile(page, name, SECRET);
    const sentA: Request[] = [];
    page.on('request', (r) => sentA.push(r));

    const joined = (p: Page) =>
      p.waitForResponse((r) => r.request().method() === 'POST' && /\/coedit\/join$/.test(new URL(r.url()).pathname));
    const joinA = joined(page);
    const keyA = page.waitForRequest((r) => r.method() === 'POST' && /\/coedit\/[0-9a-f]{32}\/key$/.test(new URL(r.url()).pathname));
    const a = await openInApp(page, name);
    const ansA = (await (await joinA).json()) as Record<string, unknown>;
    expect(ansA.key_mode).toBe('folder');
    expect(ansA.created).toBe(true);
    expect(ansA, 'the server hands no key').not.toHaveProperty('key');
    expect((await joinA).request().postDataJSON()).toMatchObject({ e2e: true });
    const stored = (await keyA).postDataJSON() as { sealed?: string };
    expect(typeof stored.sealed).toBe('string');
    expect(await base(a)).toBe(SECRET);

    const pageB = await second(browser);
    const sentB: Request[] = [];
    pageB.on('request', (r) => sentB.push(r));
    try {
      await openStorage(pageB, e2eStore);
      await openFolder(pageB, 'Kasa');
      await unlock(pageB);
      const joinB = joined(pageB);
      const b = await openInApp(pageB, name);
      const ansB = (await (await joinB).json()) as Record<string, unknown>;
      expect(ansB.session).toBe(ansA.session);
      expect(ansB.created).toBe(false);
      expect(ansB, 'the server hands no key').not.toHaveProperty('key');
      expect(ansB.sealed_key, 'the key the first sealed, as it is').toBe(stored.sealed);
      expect(await base(b), 'opened with the folder key').toBe(SECRET);

      expect((await ask(a, 'coedit.lease', { op: 'acquire', changesSeen: 0 })).result).toEqual({ granted: true });
      expect((await ask(a, 'coedit.append', { kind: 'changes', body: { changes: ['gizli ek satır'] } })).error).toBeUndefined();
      await expect.poll(() => changes(b)).toEqual([{ changes: ['gizli ek satır'] }]);

      // No request either page sent carried the plaintext.
      for (const r of [...sentA, ...sentB]) {
        const body = r.postDataBuffer()?.toString('utf8') ?? '';
        expect(`${r.url()} ${body}`, r.url()).not.toContain('gizli');
      }
    } finally {
      await pageB.context().close();
    }
  });
});
