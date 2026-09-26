/**
 * 160 — the admin's Trash page restores and purges as jobs of the queue.
 *
 * Both ran inside the request, and a folder is moved back, or purged, one
 * object at a time: the page's HTTP client gave up after 30 s and read "the
 * server could not be reached" beside a raw "AxiosError: timeout of 30000ms
 * exceeded" while the server carried on. A server that runs them as jobs says
 * so (`capabilities.queued`); the page asks for one and says how it ended.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';

const STAMP = Date.now();
const STORAGE = `e2e-atrash-${STAMP}`;
const INSIDE = 'inside.txt';

/** A folder with one file in it, in the trash. */
async function trashedFolder(request: Page['request'], name: string) {
  const mk = await request.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name } });
  expect(mk.ok(), `newfolder ${mk.status()}`).toBeTruthy();
  const up = await request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://${name}`, 'file[]': { name: INSIDE, mimeType: 'text/plain', buffer: Buffer.from(name) } },
  });
  expect(up.ok(), `upload ${up.status()}`).toBeTruthy();
  const del = await request.post('/api/files/manager?action=delete', {
    data: { path: `${STORAGE}://`, items: [{ path: `${STORAGE}://${name}` }] },
  });
  expect(del.ok(), `delete ${del.status()}`).toBeTruthy();
}

async function namesIn(page: Page, dir: string): Promise<string[]> {
  const res = await page.request.get(
    `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://${dir}`)}`,
  );
  if (!res.ok()) return [];
  const body = (await res.json()) as { files?: Array<{ basename: string }> };
  return (body.files ?? []).map((f) => f.basename).sort();
}

/** The folder's own row: the trash listing is flat, and the file inside it is a
 *  row of its own whose path names the folder too. */
function folderRow(page: Page, name: string) {
  return page.locator('.fe-list__body [role="row"]').filter({ hasText: name }).filter({ hasNotText: INSIDE }).first();
}

async function pick(page: Page, name: string, verb: RegExp) {
  const row = folderRow(page, name);
  await expect(row).toBeVisible({ timeout: 15_000 });
  await row.locator('[data-testid^="trash-actions-"]').click();
  await page.locator('.fe-ctx').last().getByRole('menuitem', { name: verb }).click();
}

test.describe('The admin Trash page works through the queue', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('restores a folder as a job, and says when it is back', async ({ page }) => {
    await loginAs(page);
    const name = `geri-${STAMP}`;
    await trashedFolder(page.request, name);
    await page.goto('/admin/trash');

    const answered = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().includes('/api/files/manager/restore'),
    );
    await pick(page, name, /^(Restore|Geri yükle)$/);
    const res = await answered;
    expect(res.url(), 'the page asked for a job').toContain('queued=1');
    expect(res.status(), 'the server queued it').toBe(202);

    await expect(page.getByText(new RegExp(`^${name} (restored|geri yüklendi)$`))).toBeVisible({ timeout: 15_000 });
    await expect.poll(() => namesIn(page, name), { timeout: 15_000 }).toEqual([INSIDE]);
    await expect(folderRow(page, name), 'the list follows the job').toHaveCount(0);
  });

  test('purges a folder as a job, and says when it is gone', async ({ page }) => {
    await loginAs(page);
    const name = `sil-${STAMP}`;
    await trashedFolder(page.request, name);
    await page.goto('/admin/trash');

    page.once('dialog', (d) => void d.accept());
    const answered = page.waitForResponse(
      (r) => r.request().method() === 'DELETE' && r.url().includes('/api/admin/trash/'),
    );
    await pick(page, name, /^(Purge|Kalıcı sil)$/);
    const res = await answered;
    expect(res.url(), 'the page asked for a job').toContain('queued=1');
    expect(res.status(), 'the server queued it').toBe(202);

    await expect(page.getByText(new RegExp(`^${name} (permanently deleted|kalıcı olarak silindi)$`))).toBeVisible({
      timeout: 15_000,
    });
    await expect(folderRow(page, name), 'the list follows the job').toHaveCount(0);
    expect(await namesIn(page, name), 'nothing came back').toEqual([]);
  });
});
