/**
 * 174 — encrypting a folder that already exists, in place, measured on the
 * server's own disk (docs/E2E-ENCRYPTION.md → "Encrypting a folder you
 * already have").
 *
 *   1. level 1: right-click → Encrypt with E2EE… → every file becomes
 *      ciphertext where it is, names untouched, the key file ends at v2, and
 *      the plaintext versions filex kept are gone;
 *   2. level 2, interrupted: the second write fails, the strip says the job
 *      did not finish, Continue takes it to the end — contents AND names.
 *
 * ⚠ Language pinned to English on the ACCOUNT (lesson #616) and restored.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';

const STORE = `e2e-conv-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;
const PW = 'correct horse battery staple';
const PREFS = '/api/me/prefs?surface=web';
const MARKER = '.filex-e2e.json';
const STORED = /^[A-Za-z0-9_-]{23,}(\.[A-Za-z0-9_-]{22})?$/;

let prefsBefore: Record<string, unknown> = {};

function disk(rel: string): string[] {
  return fs
    .readdirSync(path.join(storageRoot(MOUNT), rel))
    .filter((n) => n !== MARKER && n !== '.keepdir' && !n.startsWith('.filex-'))
    .sort();
}

/** Every file under `rel` (not the key file): relative path → first 8 bytes. */
function heads(rel: string): Record<string, string> {
  const out: Record<string, string> = {};
  const walk = (dir: string, prefix: string) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      if (e.name === MARKER) continue;
      const full = path.join(dir, e.name);
      if (e.isDirectory()) walk(full, `${prefix}${e.name}/`);
      else out[`${prefix}${e.name}`] = fs.readFileSync(full).subarray(0, 8).toString('latin1');
    }
  };
  walk(path.join(storageRoot(MOUNT), rel), '');
  return out;
}

function markerOnDisk(folder: string): Record<string, unknown> {
  return JSON.parse(fs.readFileSync(path.join(storageRoot(MOUNT), folder, MARKER), 'utf8'));
}

async function upload(api: APIRequestContext, dir: string, name: string, body: string) {
  const res = await api.post('/api/files/manager?q=upload&action=upload', {
    multipart: {
      path: `${STORE}://${dir}`,
      'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(body, 'utf8') },
    },
  });
  expect(res.ok(), `upload ${dir}/${name}: ${res.status()}`).toBe(true);
}

async function mkdir(api: APIRequestContext, dir: string, name: string) {
  const res = await api.post('/api/files/manager?q=newfolder&action=newfolder', {
    data: { path: `${STORE}://${dir}`, name },
  });
  expect(res.ok(), `mkdir ${dir}/${name}: ${res.status()}`).toBe(true);
}

async function versionsOf(api: APIRequestContext, dir: string, name: string): Promise<number> {
  const listed = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORE}://${dir}`)}`);
  const row = ((await listed.json()).files as Array<{ basename: string; id?: number }>).find((f) => f.basename === name);
  if (!row?.id) return -1;
  const res = await api.get(`/api/files/versions?node_id=${row.id}`);
  return ((await res.json()).versions ?? []).length;
}

function row(page: Page, name: string) {
  return page
    .locator('.fe-list__row[data-fe-path]')
    .filter({ has: page.locator('.fe-list__name', { hasText: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`) }) })
    .first();
}

/** With E2E_SHOTS=<dir>, a picture for a person to look at (nothing asserts on it). */
async function shot(page: Page, name: string) {
  const dir = process.env.E2E_SHOTS;
  if (!dir) return;
  fs.mkdirSync(dir, { recursive: true });
  await page.waitForTimeout(300);
  await page.screenshot({ path: path.join(dir, name) });
}

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  // A decrypted download goes through the File System Access save dialog
  // where the browser has one (Chromium; docs/E2E-ENCRYPTION.md → "Where a
  // decrypted download goes"), and a headless run cannot answer that dialog.
  // This spec is about names and contents, not the save sink — spec 173
  // measures both sinks — so every engine takes the in-memory path here,
  // which Playwright sees as a download.
  await page.addInitScript(() => {
    Object.defineProperty(window, 'showSaveFilePicker', { value: undefined, configurable: true, writable: true });
  });
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORE)}`);
  await expect(page.getByTestId('sidenav-new').first()).toBeVisible({ timeout: 30_000 });
}

async function encryptFolder(page: Page, name: string, level: 'content' | 'names') {
  const target = await settled(row(page, name));
  await target.click({ button: 'right' });
  await target.dispose();
  const menu = page.getByRole('menu').first();
  await menu.getByRole('menuitem', { name: /^Encrypt with E2EE…$/ }).click();
  const dialog = page.locator('.fe-modal__card').filter({ has: page.getByTestId('e2e-convert-past') });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByTestId('e2e-level-content'), 'level 1 is the default here too').toBeChecked();
  await expect(dialog.getByTestId('e2e-convert-versions')).toBeChecked();
  if (level === 'names') await dialog.getByTestId('e2e-level-names').check();
  const pws = dialog.locator('input[type="password"]');
  await pws.nth(0).fill(PW);
  await pws.nth(1).fill(PW);
  await dialog.getByTestId('e2e-create-ack').check();
  await shot(page, `convert-dialog-${level}.png`);
  await dialog.getByRole('button', { name: 'Encrypt folder', exact: true }).click();
  const keyEl = page.locator('.fe-e2e-rk__key');
  await expect(keyEl).toBeVisible({ timeout: 20_000 });
  await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(keyEl).toBeHidden();
}

test.describe.serial('E2E encrypt an existing folder in place', () => {
  let api: APIRequestContext;

  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORE);
    await seedLocalStorage(request, STORE, MOUNT);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    const doc = got.ok() ? (await got.json()).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } })).ok()).toBe(true);
  });

  test.afterAll(async ({ request }) => {
    await api?.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await api?.dispose();
    await dropStorageByName(request, STORE);
  });

  test('level 1: every file becomes ciphertext where it is, and its plaintext versions go', async ({ page }) => {
    test.setTimeout(180_000);
    await mkdir(api, '', 'Arşiv');
    await mkdir(api, 'Arşiv', 'Faturalar');
    await upload(api, 'Arşiv', 'not.txt', 'first draft\n');
    await upload(api, 'Arşiv', 'not.txt', 'second draft\n');
    await upload(api, 'Arşiv/Faturalar', '2024.txt', 'fatura 2024\n');
    expect(await versionsOf(api, 'Arşiv', 'not.txt'), 'the overwrite kept a plaintext version').toBe(1);

    await openStorage(page);
    await encryptFolder(page, 'Arşiv', 'content');

    // Taken into the folder; the job runs and finishes.
    await expect(page.getByTestId('e2e-names-status')).toHaveText('Contents only', { timeout: 20_000 });
    await expect
      .poll(() => (markerOnDisk('Arşiv').conv === undefined ? 'done' : 'running'), { timeout: 30_000 })
      .toBe('done');
    const m = markerOnDisk('Arşiv');
    expect(m.v, 'level 1 ends at v2').toBe(2);
    expect(m.req).toBeUndefined();
    for (const [p, head] of Object.entries(heads('Arşiv'))) expect(head, p).toBe('filexe2e');
    expect(disk('Arşiv')).toEqual(['Faturalar', 'not.txt']);
    await expect(row(page, 'not.txt')).toBeVisible();

    // No version of any file holds the plaintext any more.
    await expect.poll(() => versionsOf(api, 'Arşiv', 'not.txt'), { timeout: 15_000 }).toBe(0);
    expect(await versionsOf(api, 'Arşiv/Faturalar', '2024.txt')).toBe(0);

    // And the content still reads: download decrypts.
    const target = await settled(row(page, 'not.txt'));
    await target.click({ button: 'right' });
    await target.dispose();
    const [dl] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('menu').first().getByRole('menuitem', { name: /^Download$/ }).click(),
    ]);
    expect(fs.readFileSync((await dl.path())!, 'utf8')).toBe('second draft\n');
  });

  test('level 2, interrupted and continued: contents and names', async ({ page }) => {
    test.setTimeout(180_000);
    await mkdir(api, '', 'Eski');
    await mkdir(api, 'Eski', 'alt');
    await upload(api, 'Eski', 'bir.txt', 'bir\n');
    await upload(api, 'Eski', 'iki.txt', 'iki\n');
    await upload(api, 'Eski/alt', 'üç.txt', 'üç\n');

    await openStorage(page);
    // The second conversion write fails.
    let writes = 0;
    await page.route(
      (u) => u.pathname === '/api/files/manager' && u.searchParams.get('action') === 'upload',
      async (route) => {
        // The body is binary (ciphertext): read it as bytes, not as text.
        const body = route.request().postDataBuffer()?.toString('latin1') ?? '';
        if (body.includes('name="e2e_convert"')) {
          writes++;
          if (writes === 2) return route.abort('failed');
        }
        return route.continue();
      },
    );
    await encryptFolder(page, 'Eski', 'names');
    const strip = page.getByTestId('e2e-convert-strip');
    await expect(strip.locator('.fe-form__error')).toContainText('could not be encrypted', { timeout: 30_000 });
    await shot(page, 'convert-interrupted.png');
    await page.unrouteAll({ behavior: 'ignoreErrors' });
    const half = markerOnDisk('Eski');
    expect(half.req, 'still converting, names pending').toEqual(['names', 'conv']);
    expect(Object.values(heads('Eski')).filter((h) => h !== 'filexe2e').length, 'one file is still plaintext').toBe(1);

    await strip.getByTestId('e2e-convert-resume').click();
    await expect
      .poll(() => JSON.stringify(markerOnDisk('Eski').req ?? null), { timeout: 30_000 })
      .toBe(JSON.stringify(['names']));
    const done = markerOnDisk('Eski');
    expect((done.names as { pending?: boolean }).pending).toBeUndefined();
    for (const [p, head] of Object.entries(heads('Eski'))) expect(head, p).toBe('filexe2e');
    for (const n of disk('Eski')) expect(n, 'names are ciphertext').toMatch(STORED);
    await expect(page.getByTestId('e2e-names-status')).toHaveText('Contents and names');
    for (const n of ['bir.txt', 'iki.txt', 'alt']) await expect(row(page, n)).toBeVisible();
  });
});
