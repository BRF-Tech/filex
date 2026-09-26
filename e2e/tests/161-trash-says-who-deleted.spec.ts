/**
 * 161 — the Trash says who put each item there.
 *
 * The trash listed what was deleted, where from and when, never who; in a
 * shared storage "who deleted this?" is the first question asked of a missing
 * file. A delete now names the person on the row (migration 00061), whether it
 * ran inside the request or later through the ops queue, and both Trash views
 * show it: "You" in the explorer for the asker's own, the name on the admin's
 * page.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';

const STAMP = Date.now();
const STORAGE = `e2e-trashby-${STAMP}`;

type Entry = {
  id: number;
  name: string;
  deleted_by_id?: number;
  deleted_by_name?: string;
  deleted_by_self?: boolean;
};

async function upload(request: Page['request'], name: string) {
  const up = await request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(name) } },
  });
  expect(up.ok(), `upload ${name}: ${up.status()}`).toBeTruthy();
}

async function deleteNow(request: Page['request'], name: string) {
  const del = await request.post('/api/files/manager?action=delete', {
    data: { path: `${STORAGE}://`, items: [{ path: `${STORAGE}://${name}` }] },
  });
  expect(del.ok(), `delete ${name}: ${del.status()}`).toBeTruthy();
}

async function trashEntry(request: Page['request'], name: string): Promise<Entry | undefined> {
  const res = await request.get('/api/files/manager/trash?limit=500');
  expect(res.ok(), `trash listing ${res.status()}`).toBeTruthy();
  return ((await res.json()).entries as Entry[]).find((e) => e.name === name);
}

test.describe('The Trash says who deleted each item', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('a delete in the request and one through the ops queue both name the person', async ({ page }) => {
    await loginAs(page);
    const direct = `direct-${STAMP}.txt`;
    const queued = `queued-${STAMP}.txt`;
    await upload(page.request, direct);
    await upload(page.request, queued);

    await deleteNow(page.request, direct);
    const q = await page.request.post('/api/files/delete', { data: { source: [`${STORAGE}://${queued}`] } });
    expect(q.status(), 'the queue took the delete').toBe(202);
    const { op } = (await q.json()) as { op: { id: number } };
    await expect
      .poll(async () => ((await (await page.request.get(`/api/files/ops/${op.id}`)).json()) as { status: string }).status, {
        timeout: 15_000,
      })
      .toBe('ok');

    for (const name of [direct, queued]) {
      const e = await trashEntry(page.request, name);
      expect(e, `${name} is in the trash`).toBeTruthy();
      expect(e!.deleted_by_self, `${name}: the asker's own delete`).toBe(true);
      expect(e!.deleted_by_id, `${name}: an account is named`).toBeGreaterThan(0);
      expect(e!.deleted_by_name, `${name}: by name`).toBeTruthy();
    }
  });

  test('the explorer’s Trash says "You", the admin Trash page says the name', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await setAccountViewMode(page.request, 'list');
    const name = `ui-${STAMP}.txt`;
    await upload(page.request, name);
    await deleteNow(page.request, name);
    const e = await trashEntry(page.request, name);
    expect(e?.deleted_by_name).toBeTruthy();

    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
    await page.getByTestId('sidenav').getByText(/^(Trash|Çöp kutusu)$/i).click();
    const row = page.locator('.fe-list__row').filter({ hasText: name }).first();
    await expect(row).toBeVisible({ timeout: 15_000 });
    await expect(row.locator('.fe-list__col--owner')).toHaveText(/^(You|Siz)$/);

    await page.goto('/admin/trash');
    await expect(page.getByTestId(`trash-deleted-by-${e!.id}`)).toHaveText(e!.deleted_by_name!, { timeout: 15_000 });
  });
});
