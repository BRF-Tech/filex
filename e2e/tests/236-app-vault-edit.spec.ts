/**
 * 236-app-vault-edit - an app's interface editing a file of a VAULT
 * (encryption level 3; filex 0.56, task #189; docs/E2E-ENCRYPTION.md →
 * Editing in an app → In a vault). In a real browser against a real binary
 * with the vault on:
 *
 *   1. a file uploaded into a vault opens in the app granted
 *      `files:e2e-plaintext` - the browser picks the app from the name it
 *      read in the vault's encrypted index; the interface is told
 *      `encrypted: "vault"`, `plaintext: true`, and may save;
 *   2. it reads the plaintext and saves new plaintext: the vault gets one more
 *      generation; no request the page sent carries the plaintext or the
 *      file's name, none goes to an app's server door (`…/plugins/ui/…/read`
 *      or `/save`), and the server's disk holds neither;
 *   3. read again: the new text;
 *   4. another tab takes over writing the vault: the app's save is refused
 *      (`failed` / `vault_locked`), the server's sentence is said, and nothing
 *      is committed;
 *   5. a minute before the vault locks itself (15 idle minutes, the page's
 *      clock moved on) while the app holds unsaved changes, the person is
 *      warned in the server's words and keeps it open with one click; with
 *      nothing more done it locks: the app's frame goes, the lock screen
 *      comes back, and the person is told what was lost.
 *
 * ⚠ Runs on a SECOND filex with the vault on (`FILEX_E2E_VAULT=1`, the
 * shared hermetic instance keeps it off), like 212: needs E2E_FILEX_BIN.
 * The app is built here, like 232's: an interface-only app whose script
 * speaks the bridge's protocol itself.
 */
import { test, expect, type APIRequestContext, type Browser, type Frame, type Page, type Request } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { loginAs } from '../helpers/auth';
import { newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';
import { installInterfaceApp } from '../helpers/appInterface';
import { bootVaultInstance } from '../helpers/vaultInstance';
import { zip } from '../helpers/zip';

const STORE = `e2e-vaultapp-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;
const PW = 'correct horse battery staple';
const PREFS = '/api/me/prefs?surface=web';
const VAULT = 'Kasa';
const APP = 'e2e-vaultapp';
const EXT = 'vtxt';
const FILE_NAME = `Bütçe planı.${EXT}`;
const BODY = 'içerik: yalnız kasada, 2027 bütçesi';
const SAVED = 'yeni içerik: kasaya geri yazıldı';

/* ── the app ────────────────────────────────────────────────────────────── */

const APP_JS = `
let port = null;
let seq = 0;
const pending = new Map();
const call = (method, params, transfer) => new Promise((ok) => {
  const id = ++seq;
  pending.set(id, ok);
  port.postMessage({ id, method, params }, transfer || []);
});
window.__session = null;
window.__read = () => call('file.read', { index: 0, as: 'text' });
window.__save = (text) => {
  const data = new TextEncoder().encode(text).buffer;
  return call('file.save', { index: 0, data }, [data]);
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
  'index.html': '<!doctype html><html><head><meta charset="utf-8"><script src="app.js" defer></script></head><body><h1>editor</h1></body></html>',
  'app.js': APP_JS,
});

interface Answer {
  result?: { text?: string; saved?: boolean; size?: number };
  error?: { code: string; message?: string };
}
type Probe = Window & {
  __session: { files: Array<Record<string, unknown>>; grants: string[] } | null;
  __read: () => Promise<Answer>;
  __save: (text: string) => Promise<Answer>;
  __call: (m: string, p?: unknown) => Promise<Answer>;
};

/* ── the server's disk ──────────────────────────────────────────────────── */

/** Every file under the vault folder on the server's disk: relative path → size. */
function onDisk(): Record<string, number> {
  const root = path.join(storageRoot(MOUNT), VAULT);
  const out: Record<string, number> = {};
  const walk = (dir: string, rel: string) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const r = rel ? `${rel}/${e.name}` : e.name;
      if (e.isDirectory()) walk(path.join(dir, e.name), r);
      else out[r] = fs.statSync(path.join(dir, e.name)).size;
    }
  };
  if (fs.existsSync(root)) walk(root, '');
  return out;
}

function generations(): number {
  return Object.keys(onDisk()).filter((r) => r.startsWith('v/idx/')).length;
}

/** Every byte the server holds for the vault, as one buffer (for a search). */
function allBytes(): Buffer {
  const root = path.join(storageRoot(MOUNT), VAULT);
  return Buffer.concat(Object.keys(onDisk()).map((r) => fs.readFileSync(path.join(root, r))));
}

/* ── the explorer ───────────────────────────────────────────────────────── */

function row(page: Page, name: string) {
  const re = new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`);
  return page.locator('.fe-list__row[data-fe-path]').filter({ has: page.locator('.fe-list__name', { hasText: re }) }).first();
}

async function newTab(browser: Browser, base: string, clock = false): Promise<Page> {
  const ctx = await browser.newContext({ baseURL: base, viewport: { width: 1280, height: 860 } });
  const page = await ctx.newPage();
  if (clock) await page.clock.install();
  await page.addInitScript(() => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  });
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORE)}`);
  await expect(page.getByTestId('sidenav-new').first()).toBeVisible({ timeout: 30_000 });
  return page;
}

/** Double-click a folder and wait for its listing (lesson #1431). */
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

async function unlock(page: Page) {
  const lock = page.locator('.fe-e2e-lock');
  await expect(lock).toBeVisible({ timeout: 20_000 });
  await lock.locator('input[type="password"]').fill(PW);
  await lock.getByRole('button', { name: 'Unlock', exact: true }).click();
  await expect(page.getByTestId('e2e-vault-strip')).toBeVisible({ timeout: 30_000 });
}

/** Double-click the file and wait for the app's frame, connected and started. */
async function openInApp(page: Page, name: string): Promise<Frame> {
  await (await settled(row(page, name))).dblclick();
  const el = page.locator('iframe[data-testid="app-frame"]');
  await expect(el).toHaveAttribute('sandbox', 'allow-scripts', { timeout: 30_000 });
  await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible({ timeout: 20_000 });
  const frame = (await (await el.elementHandle())!.contentFrame())!;
  await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error);
  expect(await frame.evaluate(() => document.body.dataset.error ?? null), 'the interface started').toBeNull();
  return frame;
}

/* ── the spec ───────────────────────────────────────────────────────────── */

test.describe.serial('An app edits a file of a vault; the server sees packs and an index only', () => {
  let inst: { base: string; stop: () => Promise<void> } | null = null;
  let api: APIRequestContext;
  let writer: Page;
  let frame: Frame;

  test.beforeAll(async ({ playwright }) => {
    test.setTimeout(120_000);
    const bin = process.env.E2E_FILEX_BIN ?? '';
    test.skip(!bin || !fs.existsSync(bin), 'no binary to boot a vault installation with (E2E_FILEX_BIN)');
    inst = await bootVaultInstance(bin);
    api = await newAuthedRequest(playwright, inst.base);
    await seedLocalStorage(api, STORE, MOUNT);
    const caps = await (await api.get('/api/files/capabilities')).json();
    expect(caps?.e2e_vault, 'the instance has the vault on').toBe(true);
    expect(caps?.apps_e2e_plaintext, 'apps may be handed encrypted files (on by default)').toBe(true);
    expect((await api.put(PREFS, { data: { prefs: { locale: 'en' } } })).ok()).toBe(true);
    const rows = await installInterfaceApp(
      api,
      {
        manifest_version: 1,
        name: APP,
        version: '1.0.0',
        filex: '>=0.56.0',
        label: { en: APP },
        permissions: ['files:read', 'files:write'],
        ui: { bundle: {} },
        encrypted_folders: { open: true },
        views: [{ id: 'editor', placement: 'viewer', ui: 'index.html', label: { en: APP }, applies: { ext: [EXT] } }],
      },
      UI,
    );
    expect(rows.some((r) => r.id === 'files:e2e-plaintext'), 'the grant, derived from encrypted_folders').toBe(true);
  });

  test.afterAll(async () => {
    await writer?.context().close().catch(() => undefined);
    await api?.dispose();
    await inst?.stop();
  });

  test('a vault’s file opens in the granted app, which reads and saves it; the server holds neither text nor name', async ({ browser }) => {
    test.setTimeout(240_000);
    writer = await newTab(browser, inst!.base);
    // The vault, made from the New folder dialog (level 3).
    await writer.getByTestId('sidenav-new').first().click();
    await writer.locator('.fe-ctx__item', { has: writer.locator('.fe-ctx__label', { hasText: /^New folder$/ }) }).first().click();
    await writer.getByText('Create encrypted folder…', { exact: true }).click();
    const dialog = writer.locator('.fe-modal__card').filter({ has: writer.getByTestId('e2e-create-names') });
    await expect(dialog).toBeVisible();
    await dialog.locator('input[type="text"]').first().fill(VAULT);
    const pws = dialog.locator('input[type="password"]');
    await pws.nth(0).fill(PW);
    await pws.nth(1).fill(PW);
    await dialog.getByTestId('e2e-level-vault').check();
    await dialog.getByTestId('e2e-create-ack').check();
    await dialog.getByRole('button', { name: 'Create encrypted folder', exact: true }).click();
    await expect(writer.locator('.fe-e2e-rk__key')).toBeVisible({ timeout: 30_000 });
    await writer.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
    await writer.getByRole('button', { name: 'Done', exact: true }).click();

    await openFolder(writer, VAULT);
    await expect(writer.getByTestId('e2e-vault-strip')).toBeVisible({ timeout: 20_000 });
    await writer.locator('input[type="file"]').first().setInputFiles({ name: FILE_NAME, mimeType: 'text/plain', buffer: Buffer.from(BODY, 'utf8') });
    await expect(row(writer, FILE_NAME)).toBeVisible({ timeout: 60_000 });
    const before = generations();

    const sent: Request[] = [];
    writer.on('request', (r) => sent.push(r));
    frame = await openInApp(writer, FILE_NAME);
    const files = await frame.evaluate(() => (window as unknown as Probe).__session?.files ?? []);
    expect(files).toHaveLength(1);
    expect(files[0]).toMatchObject({ name: FILE_NAME, encrypted: 'vault', plaintext: true, readOnly: false });

    const read = await frame.evaluate(() => (window as unknown as Probe).__read());
    expect(read.error, JSON.stringify(read)).toBeUndefined();
    expect(read.result?.text).toBe(BODY);

    const saved = await frame.evaluate((t) => (window as unknown as Probe).__save(t), SAVED);
    expect(saved.error, JSON.stringify(saved)).toBeUndefined();
    expect(saved.result?.saved).toBe(true);
    await expect.poll(() => generations(), { message: 'the save is the vault’s next generation', timeout: 20_000 }).toBeGreaterThan(before);

    // The server's disk: packs and index files, no name, no byte of either text.
    const bytes = allBytes();
    for (const needle of [BODY, SAVED, 'Bütçe', 'kasaya geri']) {
      expect(bytes.includes(Buffer.from(needle, 'utf8')), needle).toBe(false);
    }
    expect(Object.keys(onDisk()).some((r) => r.includes('Bütçe') || r.includes(EXT))).toBe(false);

    // No request the page sent carried the text or the name, and none went
    // to an app's server door: the vault engine did both, in the page.
    for (const r of sent) {
      const body = r.postDataBuffer()?.toString('utf8') ?? '';
      const said = `${decodeURIComponent(r.url())} ${body}`;
      expect(said, r.url()).not.toContain('kasaya geri');
      expect(said, r.url()).not.toContain('2027 bütçesi');
      expect(said, r.url()).not.toContain('Bütçe planı');
      expect(/\/plugins\/ui\/[^?]*\/(read|save)\b/.test(r.url()), r.url()).toBe(false);
    }

    const again = await frame.evaluate(() => (window as unknown as Probe).__read());
    expect(again.result?.text).toBe(SAVED);

    // While it holds the file, nothing else leaves toward the server.
    const copy = await frame.evaluate(() => (window as unknown as Probe).__call('file.saveAs', { name: 'kopya.txt', data: 'x' }));
    expect(copy.error?.code).toBe('unavailable');
  });

  test('another tab takes over writing: the app’s save is refused, said in the server’s words, and nothing is committed', async ({ browser }) => {
    test.setTimeout(180_000);
    const other = await newTab(browser, inst!.base);
    await openFolder(other, VAULT);
    await unlock(other);
    // It says who writes (after its first look at the state, at most 30 s),
    // and the vault folder's owner may take over.
    await expect(other.getByTestId('e2e-vault-take-over')).toBeVisible({ timeout: 45_000 });
    await other.getByTestId('e2e-vault-take-over').click();
    await expect(other.getByTestId('e2e-vault-strip')).toHaveAttribute('data-mode', 'write', { timeout: 20_000 });
    // The writer's heartbeat hears it: back to read-only (the strip is under
    // the app's viewer, but on the page).
    await expect(writer.getByTestId('e2e-vault-strip')).toHaveAttribute('data-mode', 'read', { timeout: 45_000 });

    const before = generations();
    const refused = await frame.evaluate((t) => (window as unknown as Probe).__save(t), 'yazılmamalı');
    expect(refused.error, JSON.stringify(refused)).toEqual({ code: 'failed', message: 'vault_locked' });
    // The server's sentence (server.e2e.vault.locked), not a copy of it.
    await expect(writer.locator('.fe-toast', { hasText: /is writing (to )?this vault/ })).toBeVisible({ timeout: 20_000 });
    expect(generations()).toBe(before);
    await other.context().close();
  });

  test('a minute before the vault locks itself the app’s unsaved changes are warned about and it can be kept open; then it locks, the app goes, and the person is told what was lost', async ({ browser }) => {
    test.setTimeout(180_000);
    const page = await newTab(browser, inst!.base, true);
    await openFolder(page, VAULT);
    await unlock(page);
    const app = await openInApp(page, FILE_NAME);
    // ⚠ WebKit (Playwright 1.59.1): the interface's frame runs none of the
    // context's init scripts - not the clock, not the trace's snapshot
    // streamer, not the page's own (measured: no `__pwClock` there, while the
    // same scripts evaluated in it afterwards run) - and every `page.clock`
    // call runs in every frame, so it threw "undefined is not an object
    // (evaluating 'globalThis.__pwClock.controller')". The interface keeps no
    // timer this test waits on: where its frame has no clock it gets one that
    // does nothing; the explorer's page, which holds the vault's clocks, keeps
    // the real one.
    await app.evaluate(() => {
      const g = globalThis as unknown as { __pwClock?: unknown };
      g.__pwClock ??= { controller: new Proxy({}, { get: () => () => undefined }) };
    });
    expect((await app.evaluate(() => (window as unknown as Probe).__read())).result?.text).toBe(SAVED);
    expect((await app.evaluate(() => (window as unknown as Probe).__call('ui.dirty', { dirty: true }))).error).toBeUndefined();
    // A minute before: the server's words (state `words`), and its action.
    await page.clock.fastForward('14:10');
    const warning = page.locator('.fe-toast', { hasText: `An app has changes in “${VAULT}” that are not saved, and the vault locks itself in a minute.` });
    await expect(warning).toBeVisible({ timeout: 20_000 });
    await warning.getByRole('button', { name: 'Keep it open', exact: true }).dispatchEvent('click');
    await expect(page.locator('.fe-toast', { hasText: `“${VAULT}” stays open` })).toBeVisible({ timeout: 10_000 });
    // Kept: past the first deadline the app is still there.
    await page.clock.fastForward('02:00');
    await expect(page.locator('iframe[data-testid="app-frame"]')).toHaveCount(1);
    // Nothing more done in it: it locks.
    await page.clock.fastForward('14:00');
    await expect(page.locator('iframe[data-testid="app-frame"]')).toHaveCount(0, { timeout: 20_000 });
    await expect(page.locator('.fe-e2e-lock')).toBeVisible({ timeout: 20_000 });
    await expect(page.locator('.fe-toast', { hasText: `The changes to “${FILE_NAME}” that were not saved are gone` })).toBeVisible({ timeout: 20_000 });
    await page.context().close();
  });
});
