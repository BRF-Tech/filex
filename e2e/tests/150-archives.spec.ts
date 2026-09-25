/**
 * 150 — archives in the explorer (PR #48 by @ahjephson, integrated for 0.44.0).
 *
 *   1. The create dialog offers exactly what THIS server can make
 *      (`capabilities.archive`): its formats, and password fields only where
 *      it can encrypt. #48 as submitted offered every format everywhere, so a
 *      server without 7-Zip let the dialog be filled in and then failed. A ZIP
 *      made through the dialog lands beside the files.
 *   2. A .tar.gz — read by filex itself, no 7-Zip needed — opens like a folder,
 *      in the explorer's own table, and "Extract here" unpacks it.
 *   3. A .tar.gz carrying a symlink is refused, and "Extract here" SAYS so: the
 *      error used to be written into a dialog that "Extract here" never opens,
 *      so a refused archive looked like a click that did nothing.
 */
import { gzipSync } from 'node:zlib';
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';

const STAMP = Date.now();
const STORAGE = `e2e-arc-${STAMP}`;

/** One ustar member: a file ('0'), a symlink ('2') or a folder ('5'). */
function tarMember(name: string, type: '0' | '2' | '5', body = '', link = ''): Buffer {
  const data = Buffer.from(body, 'utf8');
  const h = Buffer.alloc(512);
  h.write(name, 0, 100, 'utf8');
  h.write(type === '5' ? '0000755\0' : '0000644\0', 100, 'ascii');
  h.write('0000000\0', 108, 'ascii');
  h.write('0000000\0', 116, 'ascii');
  h.write(data.length.toString(8).padStart(11, '0') + '\0', 124, 'ascii');
  h.write(Math.floor(Date.now() / 1000).toString(8).padStart(11, '0') + '\0', 136, 'ascii');
  h.write('        ', 148, 'ascii');
  h.write(type, 156, 'ascii');
  h.write(link, 157, 100, 'utf8');
  h.write('ustar\0', 257, 'ascii');
  h.write('00', 263, 'ascii');
  let sum = 0;
  for (const b of h) sum += b;
  h.write(sum.toString(8).padStart(6, '0') + '\0 ', 148, 'ascii');
  const pad = Buffer.alloc((512 - (data.length % 512)) % 512);
  return Buffer.concat([h, data, pad]);
}

function tarGz(...members: Buffer[]): Buffer {
  return gzipSync(Buffer.concat([...members, Buffer.alloc(1024)]));
}

async function upload(page: Page, name: string, buffer: Buffer, mimeType = 'application/octet-stream') {
  const res = await page.request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType, buffer } },
  });
  expect(res.ok(), `upload ${name}: ${res.status()}`).toBeTruthy();
}

function row(page: Page, name: string) {
  return page.locator(`[data-fe-path="${STORAGE}://${name}"]`).first();
}

/** Right-click a row and pick one verb from its menu. */
async function pick(page: Page, name: string, verb: RegExp) {
  const target = await settled(row(page, name));
  await target.click({ button: 'right' });
  await target.dispose();
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  await menu.getByRole('menuitem', { name: verb }).click();
}

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
}

test.describe('Archives', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('the create dialog offers what this server can make, and makes a ZIP', async ({ page }) => {
    await openStorage(page);
    const note = `notes-${STAMP}.txt`;
    await upload(page, note, Buffer.from('archived by the dialog'), 'text/plain');
    const caps = await (await page.request.get('/api/files/capabilities')).json();
    const archive = caps.archive as { allowed_formats: string[]; default_format: string; encryption?: boolean };
    expect(archive, 'the server says what it can make').toBeTruthy();
    expect(archive.allowed_formats, 'a plain ZIP needs nothing but filex').toContain('zip');
    if (!archive.encryption) {
      // No 7-Zip here: every other format and every password needs it.
      expect(archive.allowed_formats, 'a server without 7-Zip makes ZIP only').toEqual(['zip']);
    }

    await page.reload();
    await expect(row(page, note)).toBeVisible({ timeout: 15_000 });
    await pick(page, note, /^(Create archive…|Arşiv oluştur…)$/);
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    const format = dialog.locator('select').first();
    const offered = await format.locator('option').evaluateAll((els) => els.map((e) => (e as HTMLOptionElement).value));
    expect(offered, 'the dialog offers exactly the server\'s formats').toEqual(archive.allowed_formats);

    await format.selectOption('zip');
    await expect(dialog.locator('input[type="password"]')).toHaveCount(archive.encryption ? 2 : 0);

    await dialog.getByRole('button', { name: /^(Create|Oluştur)$/ }).click();
    await expect(dialog).toBeHidden();
    await expect(row(page, `notes-${STAMP}.zip`)).toBeVisible({ timeout: 20_000 });
  });

  test('a .tar.gz opens like a folder and "Extract here" unpacks it', async ({ page }) => {
    await openStorage(page);
    const name = `site-${STAMP}.tar.gz`;
    await upload(page, name, tarGz(
      tarMember('./', '5'),
      tarMember('docs/', '5'),
      tarMember('docs/readme.txt', '0', 'hello from a tarball'),
    ));
    await page.reload();
    await expect(row(page, name)).toBeVisible({ timeout: 15_000 });

    // A double-click opens in either open mode (a single click only selects
    // under the default 'double' trigger; with 'single' the second click of
    // the pair is swallowed by the open guard).
    const target = await settled(row(page, name));
    await target.dblclick();
    await target.dispose();
    const viewer = page.locator('.filex-viewer-archive');
    await expect(viewer).toBeVisible();
    // The explorer's own table (DataTable), not a private one.
    const folder = viewer.locator('.fe-list__row').filter({ hasText: 'docs' });
    await expect(folder).toHaveCount(1);
    await folder.getByRole('button').first().click();
    await expect(viewer.locator('.fe-list__row').filter({ hasText: 'readme.txt' })).toHaveCount(1);
    await page.keyboard.press('Escape');
    await expect(viewer).toBeHidden();

    await pick(page, name, /^(Extract here|Buraya çıkar)$/);
    await expect(row(page, 'docs')).toBeVisible({ timeout: 20_000 });
    const got = await page.request.get(`/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://docs/readme.txt`)}`);
    expect(got.ok(), `download ${got.status()}`).toBeTruthy();
    expect(await got.text()).toBe('hello from a tarball');
  });

  test('a .tar.gz with a symlink is refused, and "Extract here" says so', async ({ page }) => {
    await openStorage(page);
    const name = `evil-${STAMP}.tar.gz`;
    await upload(page, name, tarGz(
      tarMember(`kept-${STAMP}.txt`, '0', 'fine'),
      tarMember(`link-${STAMP}`, '2', '', '/etc/passwd'),
    ));
    await page.reload();
    await expect(row(page, name)).toBeVisible({ timeout: 15_000 });

    const answered = page.waitForResponse((r) => r.url().includes('/api/files/archive/extract'));
    await pick(page, name, /^(Extract here|Buraya çıkar)$/);
    const res = await answered;
    expect(res.status(), 'refused before anything is queued').toBe(400);
    expect((await res.json()).code).toBe('UNSUPPORTED_FORMAT');
    await expect(page.locator('.fe-toast__msg')).toHaveText(/cannot be extracted|çıkarılamıyor/);
    await expect(row(page, `kept-${STAMP}.txt`)).toHaveCount(0);
  });
});
