/**
 * 159 — a folder rename and a restore from the trash are jobs of the queue.
 *
 * Both ran inside the request. On an object store a folder is one request per
 * object, so the dialog waited with nothing on screen until the proxy gave up
 * (nginx after 60 s, Cloudflare after 100 s), then said the change had failed
 * while the server carried on.
 *
 * The server now says it runs them on its queue (`capabilities.queued`), and
 * the explorer asks for a job (`queued=1`): the answer is immediate, the
 * operations centre follows the job, and the listing follows when it ends. A
 * file is still renamed inside the request — it is one object on every driver.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';

const STAMP = Date.now();
const STORAGE = `e2e-queued-${STAMP}`;
const RENAME = /^(Rename|Yeniden adlandır)$/;

async function upload(page: Page, dir: string, name: string) {
  const res = await page.request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://${dir}`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(name) } },
  });
  expect(res.ok(), `upload ${name}: ${res.status()}`).toBeTruthy();
}

async function newFolder(page: Page, name: string) {
  const res = await page.request.post('/api/files/manager?action=newfolder', {
    data: { path: `${STORAGE}://`, name },
  });
  expect(res.ok(), `newfolder ${name}: ${res.status()}`).toBeTruthy();
}

async function namesIn(page: Page, dir: string): Promise<string[]> {
  const res = await page.request.get(
    `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://${dir}`)}`,
  );
  if (!res.ok()) return [];
  const body = (await res.json()) as { files?: Array<{ basename: string }> };
  return (body.files ?? []).map((f) => f.basename).sort();
}

async function pick(page: Page, rel: string, verb: RegExp) {
  const target = await settled(page.locator(`[data-fe-path="${STORAGE}://${rel}"]`).first());
  await target.click({ button: 'right' });
  await target.dispose();
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  await menu.getByRole('menuitem', { name: verb }).click();
}

/** Renames rel to name through the dialog and returns the server's answer. */
async function renameThroughTheDialog(page: Page, rel: string, name: string) {
  await pick(page, rel, RENAME);
  const dialog = page.getByRole('dialog', { name: RENAME });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('textbox').fill(name);
  const answered = page.waitForResponse(
    (r) => r.request().method() === 'POST' && r.url().includes('action=rename'),
  );
  await dialog.getByRole('button', { name: /^(Save|Kaydet)$/ }).click();
  return { dialog, res: await answered };
}

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
}

test.describe('Queued rename and restore', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('a folder is renamed as a job, and the dialog does not wait for it', async ({ page }) => {
    await openStorage(page);
    const from = `klasor-${STAMP}`;
    const to = `yeni-${STAMP}`;
    await newFolder(page, from);
    await upload(page, from, 'icinde.txt');
    await page.reload();

    const { dialog, res } = await renameThroughTheDialog(page, from, to);
    expect(res.url(), 'the explorer asked for a job').toContain('queued=1');
    expect(res.status(), 'the server queued it').toBe(202);
    expect(((await res.json()) as { op?: { kind?: string } }).op?.kind).toBe('rename');
    await expect(dialog).toBeHidden();

    await expect.poll(() => namesIn(page, ''), { timeout: 15_000 }).toContain(to);
    expect(await namesIn(page, '')).not.toContain(from);
    expect(await namesIn(page, to)).toEqual(['icinde.txt']);
    // When the job ends the explorer says so, and offers the undo it would
    // have offered for a rename inside the request.
    await expect(page.locator('.fe-toast__msg')).toHaveText(/^(Renamed|Yeniden adlandırıldı)$/);
    await expect(page.locator('.fe-toast__action')).toHaveText(/^(Undo|Geri al)$/);
  });

  test('a file is still renamed inside the request', async ({ page }) => {
    await openStorage(page);
    const file = `dosya-${STAMP}.txt`;
    await upload(page, '', file);
    await page.reload();

    const { dialog, res } = await renameThroughTheDialog(page, file, `ad-${file}`);
    expect(res.url()).not.toContain('queued=1');
    expect(res.status()).toBe(200);
    await expect(dialog).toBeHidden();
    expect(await namesIn(page, '')).toContain(`ad-${file}`);
  });

  test('a restore from the trash is one request, answered with its job', async ({ page }) => {
    await openStorage(page);
    const folder = `geri-${STAMP}`;
    await newFolder(page, folder);
    await upload(page, folder, 'b.txt');
    const del = await page.request.post('/api/files/manager?action=delete', {
      data: { path: `${STORAGE}://`, items: [{ path: `${STORAGE}://${folder}` }] },
    });
    expect(del.ok(), `delete ${del.status()}`).toBeTruthy();
    await page.reload();

    await page.getByTestId('sidenav').getByText(/^(Trash|Çöp Kutusu)$/).click();
    // The trash listing is flat: the file inside is a row of its own, and its
    // "Deleted from" names the folder too.
    const trashed = page.locator('[data-fe-path]').filter({ hasText: folder }).filter({ hasNotText: 'b.txt' }).first();
    await expect(trashed).toBeVisible({ timeout: 15_000 });

    let restoreRequests = 0;
    page.on('request', (r) => {
      if (r.method() === 'POST' && r.url().includes('/api/files/manager/restore')) restoreRequests++;
    });
    const answered = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().includes('/api/files/manager/restore'),
    );
    await trashed.click({ button: 'right' });
    await page.getByRole('menu').first().getByRole('menuitem', { name: /^(Restore|Geri getir)$/ }).click();
    const res = await answered;
    expect(res.url(), 'the explorer asked for a job').toContain('queued=1');
    expect(res.status(), 'the server queued it').toBe(202);
    expect(((await res.json()) as { ops?: Array<{ kind?: string }> }).ops?.map((o) => o.kind)).toEqual(['restore']);

    // The folder's row comes back a moment before its children do, so both
    // are waited for, as the explorer waits for the job to end.
    await expect.poll(() => namesIn(page, ''), { timeout: 15_000 }).toContain(folder);
    await expect.poll(() => namesIn(page, folder), { timeout: 15_000 }).toEqual(['b.txt']);
    await expect(trashed, 'the trash listing follows the job').toBeHidden({ timeout: 15_000 });
    expect(restoreRequests, 'one request for the selection').toBe(1);
  });
});
