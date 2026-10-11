/**
 * 232-app-e2e-plaintext — an app's interface editing a file of an end-to-end
 * encrypted folder (filex 0.56, task #189; docs/E2E-ENCRYPTION.md → Editing
 * in an app). In every engine `E2E_BROWSERS` names:
 *
 *   1. the review lists `files:e2e-plaintext` - derived from the manifest's
 *      `encrypted_folders` - in the administrator's words;
 *   2. a file uploaded into an unlocked encrypted folder is ciphertext on the
 *      server's disk; opened, it opens in the granted app, whose interface is
 *      told `encrypted: "folder"`, `plaintext: true` and may save;
 *   3. the interface reads the plaintext, saves new plaintext, and what lands
 *      on the server's disk is ciphertext again (`filexe2e`) - no request the
 *      page sent carries the plaintext, and the read went through the app's
 *      own door with `e2e=1`;
 *   4. while it holds the file, a save-as answers `unavailable`;
 *   5. Lock closes the app (its frame and the plaintext in it go);
 *   6. the administrator's switch off (`apps.e2e_plaintext`): the same file
 *      opens in filex's own viewer, no app is offered.
 *
 * The app is built here, like 228's and 229's: an interface-only app whose
 * script speaks the bridge's protocol itself.
 *
 * ⚠ Language pinned to English on the ACCOUNT (lesson #616) and restored.
 */
import { test, expect, type APIRequestContext, type Frame, type Page, type Request } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';
import { installInterfaceApp, removeAppByName, type ReviewRow } from '../helpers/appInterface';
import { zip } from '../helpers/zip';

const STORE = `e2e-appplain-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;
const PW = 'correct horse battery staple';
const PREFS = '/api/me/prefs?surface=web';
const SETTING = '/api/admin/settings/apps.e2e_plaintext';
const BODY = 'içerik: çok gizli bütçe 2027';
const SAVED = 'yeni içerik: kayıt sonrası gizli';

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

/* ── the explorer ───────────────────────────────────────────────────────── */

async function openStorage(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  });
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORE)}`);
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
  await expect(dialog.getByTestId('e2e-level-content'), 'contents only: the level an app is offered in').toBeChecked();
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

/** Double-click the file and wait for the app's frame, connected and started. */
async function openInApp(page: Page, name: string): Promise<Frame> {
  await (await settled(row(page, name))).dblclick();
  const el = page.locator('iframe[data-testid="app-frame"]');
  await expect(el).toHaveAttribute('sandbox', 'allow-scripts', { timeout: 20_000 });
  await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible({ timeout: 20_000 });
  const frame = (await (await el.elementHandle())!.contentFrame())!;
  await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error);
  expect(await frame.evaluate(() => document.body.dataset.error ?? null), 'the interface started').toBeNull();
  return frame;
}

/** The bytes of a file as the SERVER stores them. */
function onDisk(rel: string): Buffer {
  return fs.readFileSync(path.join(storageRoot(MOUNT), rel));
}

/* ── the spec ───────────────────────────────────────────────────────────── */

test.describe.serial('An app edits a file of an encrypted folder, and the server sees only ciphertext', () => {
  let api: APIRequestContext;
  let app = '';
  let ext = '';
  let rows: ReviewRow[] = [];
  let prefsBefore: Record<string, unknown> = {};

  test.beforeAll(async ({ request, playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    app = `e2epl-${tag}`;
    ext = `epl${tag.slice(0, 3)}`;
    await dropStorageByName(request, STORE);
    await seedLocalStorage(request, STORE, MOUNT);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    const doc = got.ok() ? (await got.json()).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } })).ok()).toBe(true);
    expect((await api.put(SETTING, { data: { value: 'true' } })).ok()).toBe(true);
    await removeAppByName(api, app);
    rows = await installInterfaceApp(
      api,
      {
        manifest_version: 1,
        name: app,
        version: '1.0.0',
        filex: '>=0.56.0',
        label: { en: app },
        permissions: ['files:read', 'files:write'],
        ui: { bundle: {} },
        encrypted_folders: { open: true },
        views: [{ id: 'editor', placement: 'viewer', ui: 'index.html', label: { en: app }, applies: { ext: [ext] } }],
      },
      UI,
    );
  });

  test.afterAll(async () => {
    await api?.put(SETTING, { data: { value: 'true' } }).catch(() => undefined);
    await api?.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await removeAppByName(api, app).catch(() => undefined);
    await dropStorageByName(api, STORE).catch(() => undefined);
    await api?.dispose();
  });

  test('the review lists files:e2e-plaintext in the administrator’s words', async () => {
    const r = rows.find((x) => x.id === 'files:e2e-plaintext');
    expect(r, 'derived from encrypted_folders').toBeTruthy();
    expect(r!.label).toContain('in the clear');
    expect(r!.label).not.toBe('files:e2e-plaintext');
  });

  test('the app reads and saves the plaintext; the server holds only ciphertext', async ({ page }) => {
    await openStorage(page);
    await createEncryptedFolder(page, 'Kasa');
    await openFolder(page, 'Kasa');
    const name = `rapor.${ext}`;
    await uploadFile(page, name, BODY);
    const stored = onDisk(`Kasa/${name}`);
    expect(stored.subarray(0, 8).toString('latin1'), 'the upload is ciphertext on disk').toBe('filexe2e');
    expect(stored.toString('utf8')).not.toContain('gizli');

    const sent: Request[] = [];
    page.on('request', (r) => sent.push(r));
    const frame = await openInApp(page, name);
    const files = await frame.evaluate(() => (window as unknown as Probe).__session?.files ?? []);
    expect(files).toHaveLength(1);
    expect(files[0]).toMatchObject({ name, encrypted: 'folder', plaintext: true, readOnly: false });

    const read = await frame.evaluate(() => (window as unknown as Probe).__read());
    expect(read.error, JSON.stringify(read)).toBeUndefined();
    expect(read.result?.text).toBe(BODY);
    const readReq = sent.find((r) => r.url().includes('/plugins/ui/') && r.url().includes('/read?'));
    expect(readReq, 'the read went through the app door').toBeTruthy();
    expect(new URL(readReq!.url()).searchParams.get('e2e')).toBe('1');

    const saved = await frame.evaluate((t) => (window as unknown as Probe).__save(t), SAVED);
    expect(saved.error, JSON.stringify(saved)).toBeUndefined();
    expect(saved.result?.saved).toBe(true);
    const after = onDisk(`Kasa/${name}`);
    expect(after.subarray(0, 8).toString('latin1'), 'the save is ciphertext on disk').toBe('filexe2e');
    expect(after.toString('utf8')).not.toContain('kayıt');
    expect(after.equals(stored), 'a new version was written').toBe(false);

    // No request the page sent carried the plaintext, in its address or body.
    for (const r of sent) {
      const body = r.postDataBuffer()?.toString('utf8') ?? '';
      expect(`${r.url()} ${body}`, r.url()).not.toContain('gizli');
    }

    // Read again: what the server holds now opens as the new text.
    const again = await frame.evaluate(() => (window as unknown as Probe).__read());
    expect(again.result?.text).toBe(SAVED);

    // While it holds the file, nothing else leaves toward the server.
    const copy = await frame.evaluate(() => (window as unknown as Probe).__call('file.saveAs', { name: 'kopya.txt', data: 'x' }));
    expect(copy.error?.code).toBe('unavailable');

    // Lock while the app holds the file (the viewer covers the strip, so the
    // button is pressed as the page's own click): the app's frame, and the
    // plaintext in it, go.
    await page.locator('.fe-e2e-strip').getByRole('button', { name: 'Lock', exact: true }).dispatchEvent('click');
    await expect(page.locator('iframe[data-testid="app-frame"]')).toHaveCount(0);
    await expect(page.locator('.fe-e2e-lock')).toBeVisible();
  });

  test('the administrator’s switch off: filex’s own viewer, no app', async ({ page }) => {
    expect((await api.put(SETTING, { data: { value: 'false' } })).ok()).toBe(true);
    await openStorage(page);
    await openFolder(page, 'Kasa');
    await unlock(page);
    await (await settled(row(page, `rapor.${ext}`))).dblclick();
    await expect(page.locator('.fe-preview').first()).toBeVisible({ timeout: 20_000 });
    await expect(page.locator('iframe[data-testid="app-frame"]')).toHaveCount(0);
    // And the server's door says it too.
    const res = await api.get(
      `/api/files/plugins/ui/${app}/editor/read?path=${encodeURIComponent(`${STORE}://Kasa/rapor.${ext}`)}&e2e=1`,
    );
    expect(res.status()).toBe(403);
    expect(((await res.json()) as { error: string }).error).toBe('encrypted');
  });
});
