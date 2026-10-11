/**
 * 234-app-e2e-level2 — an app's interface editing a file of an encrypted
 * folder whose NAMES are encrypted too (level 2; filex 0.56, task #189;
 * docs/E2E-ENCRYPTION.md → Editing in an app). 232 is level 1. In every
 * engine `E2E_BROWSERS` names:
 *
 *   1. a file uploaded into an unlocked level-2 folder is stored under an
 *      encrypted name, its content ciphertext;
 *   2. the browser picks the app by the DECRYPTED name (the server cannot
 *      read the kind): "Open with" offers the granted app beside filex's own
 *      viewer, and "Choose an app…" offers no "Always use this app for
 *      .<ext> files" - the kind would reach the account;
 *   3. the interface is told the decrypted name, reads the plaintext and
 *      saves new plaintext; what lands on the disk is ciphertext again, under
 *      the same encrypted name;
 *   4. what it keeps with `state.set` in this session goes to the account
 *      SEALED (`appStateE2e`), apart from its ordinary store, and reads back;
 *   5. no request the page sent - addresses or bodies - carries the file's
 *      name, its extension or its text.
 *
 * The app is built here, like 232's: an interface-only app whose script
 * speaks the bridge's protocol itself.
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
import { installInterfaceApp, removeAppByName } from '../helpers/appInterface';
import { zip } from '../helpers/zip';

const STORE = `e2e-applvl2-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;
const PW = 'correct horse battery staple';
const PREFS = '/api/me/prefs?surface=web';
const SETTING = '/api/admin/settings/apps.e2e_plaintext';
const STEM = 'maasbordrosu';
const BODY = 'içerik: çok gizli bordro 2027';
const SAVED = 'yeni içerik: kayıt sonrası gizli';
const KEPT = 'son arama: gizli bordro';

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
  result?: unknown;
  error?: { code: string; message?: string };
}
type Probe = Window & {
  __session: { files: Array<Record<string, unknown>>; grants: string[] } | null;
  __read: () => Promise<Answer & { result?: { text?: string } }>;
  __save: (text: string) => Promise<Answer & { result?: { saved?: boolean } }>;
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

/** A level-2 folder: contents AND names encrypted. */
async function createNamesFolder(page: Page, name: string) {
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
  await dialog.getByTestId('e2e-level-names').check();
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

/** Wait for the app's frame, connected and started. */
async function appFrame(page: Page): Promise<Frame> {
  const el = page.locator('iframe[data-testid="app-frame"]');
  await expect(el).toHaveAttribute('sandbox', 'allow-scripts', { timeout: 20_000 });
  await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible({ timeout: 20_000 });
  const frame = (await (await el.elementHandle())!.contentFrame())!;
  await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error);
  expect(await frame.evaluate(() => document.body.dataset.error ?? null), 'the interface started').toBeNull();
  return frame;
}

/** The files the SERVER stores in a folder (its key file and sidecars aside). */
function storedFiles(rel: string): string[] {
  return fs
    .readdirSync(path.join(storageRoot(MOUNT), rel), { withFileTypes: true })
    .filter((d) => d.isFile() && d.name !== '.filex-e2e.json' && !d.name.endsWith('.fxl.name'))
    .map((d) => d.name);
}

/* ── the spec ───────────────────────────────────────────────────────────── */

test.describe.serial('An app edits a file of a folder whose names are encrypted; the server sees neither its name nor its kind', () => {
  let api: APIRequestContext;
  let app = '';
  let ext = '';
  let prefsBefore: Record<string, unknown> = {};

  test.beforeAll(async ({ request, playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    app = `e2el2-${tag}`;
    ext = `lvtwo${tag.slice(0, 2)}`;
    await dropStorageByName(request, STORE);
    await seedLocalStorage(request, STORE, MOUNT);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    const doc = got.ok() ? (await got.json()).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } })).ok()).toBe(true);
    expect((await api.put(SETTING, { data: { value: 'true' } })).ok()).toBe(true);
    await removeAppByName(api, app);
    await installInterfaceApp(
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
    await api?.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await removeAppByName(api, app).catch(() => undefined);
    await dropStorageByName(api, STORE).catch(() => undefined);
    await api?.dispose();
  });

  test('the browser picks the app by the decrypted name; nothing names the file to the server', async ({ page }) => {
    await openStorage(page);
    await createNamesFolder(page, 'Kasa2');
    await openFolder(page, 'Kasa2');
    const name = `${STEM}.${ext}`;
    await uploadFile(page, name, BODY);

    // 1. On the disk: an encrypted name, ciphertext.
    const [stored, ...rest] = storedFiles('Kasa2');
    expect(rest, 'one file').toEqual([]);
    expect(stored).not.toContain(STEM);
    expect(stored).not.toContain(ext);
    const before = fs.readFileSync(path.join(storageRoot(MOUNT), 'Kasa2', stored));
    expect(before.subarray(0, 8).toString('latin1')).toBe('filexe2e');

    const sent: Request[] = [];
    page.on('request', (r) => sent.push(r));

    // 2. "Open with": the app beside filex's own viewer; "Choose an app…"
    // offers no "Always use" for a kind the server must not learn.
    await settled(row(page, name)).then((h) => h.dispose());
    await row(page, name).locator('.fe-list__name').click({ button: 'right' });
    await expect(page.getByTestId(`ctx-open-with-app:${app}/editor`)).toBeVisible();
    await expect(page.getByTestId('ctx-open-with-builtin')).toBeVisible();
    await page.getByTestId('ctx-open-with-choose').click();
    const dialog = page.getByTestId('open-with-dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(name);
    await expect(page.getByTestId('open-with-always')).toHaveCount(0);
    await page.getByTestId(`open-with-choice-app:${app}/editor`).click();
    await page.getByTestId('open-with-open').click();

    // 3. The interface: the decrypted name, the plaintext, a save.
    const frame = await appFrame(page);
    const files = await frame.evaluate(() => (window as unknown as Probe).__session?.files ?? []);
    expect(files[0]).toMatchObject({ name, ext, encrypted: 'folder', plaintext: true, readOnly: false });
    const read = await frame.evaluate(() => (window as unknown as Probe).__read());
    expect(read.error, JSON.stringify(read)).toBeUndefined();
    expect(read.result?.text).toBe(BODY);
    const saved = await frame.evaluate((t) => (window as unknown as Probe).__save(t), SAVED);
    expect(saved.error, JSON.stringify(saved)).toBeUndefined();
    expect(storedFiles('Kasa2'), 'saved under the same encrypted name').toEqual([stored]);
    const after = fs.readFileSync(path.join(storageRoot(MOUNT), 'Kasa2', stored));
    expect(after.subarray(0, 8).toString('latin1')).toBe('filexe2e');
    expect(after.equals(before), 'a new version').toBe(false);
    expect(after.toString('utf8')).not.toContain('kayıt');

    // 4. What the app keeps in this session: sealed, apart, and it reads back.
    // ⚠ The PUT's ANSWER, not the request: the account is read back over the
    // API right after, and a GET sent while the PUT is still being written
    // reads the document before it (the 0.56 nightly, Firefox: the GET went
    // out 11 ms into the PUT's 84 ms write and had no appStateE2e).
    const putPrefs = page.waitForResponse(
      (r) => r.request().method() === 'PUT' && r.url().includes('/api/me/prefs') && (r.request().postData() ?? '').includes('appStateE2e'),
      { timeout: 10_000 },
    );
    const kept = await frame.evaluate((v) => (window as unknown as Probe).__call('state.set', { key: 'recent', value: v }), KEPT);
    expect(kept.error, JSON.stringify(kept)).toBeUndefined();
    expect((await putPrefs).ok(), 'the account took the sealed store').toBe(true);
    const back = await frame.evaluate(() => (window as unknown as Probe).__call('state.get', { key: 'recent' }));
    expect(back.result).toBe(KEPT);
    const account = (await (await api.get(PREFS)).json()).prefs as Record<string, string>;
    expect(account.appStateE2e, 'the sealed store').toBeTruthy();
    expect(account.appStateE2e).not.toContain('gizli');
    expect(account.appStateE2e).not.toContain('recent');
    expect(account.appState ?? '', 'not the ordinary store').not.toContain('gizli');

    // 5. Nothing the page sent named the file or carried its text.
    expect(sent.length).toBeGreaterThan(2);
    for (const r of sent) {
      const body = r.postDataBuffer()?.toString('utf8') ?? '';
      const all = `${decodeURIComponent(r.url())} ${body}`;
      expect(all, r.url()).not.toContain(STEM);
      expect(all, r.url()).not.toContain(ext);
      expect(all, r.url()).not.toContain('gizli');
    }

    await page.locator('.fe-e2e-strip').getByRole('button', { name: 'Lock', exact: true }).dispatchEvent('click');
    await expect(page.locator('iframe[data-testid="app-frame"]')).toHaveCount(0);
  });
});
