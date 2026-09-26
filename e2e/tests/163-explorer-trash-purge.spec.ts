/**
 * 163 — "Delete permanently" in the explorer's Trash deletes, for an operator.
 *
 * The Trash's menu offered "Delete permanently", the dialog it opened said the
 * items "will be moved to trash", and then nothing was deleted: the explorer
 * showed a retention notice instead. An operator's press now purges, through
 * the queue where the server runs purges as jobs (capabilities.queued).
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';

const STAMP = Date.now();
const STORAGE = `e2e-purge-${STAMP}`;

async function trashed(request: Page['request'], name: string) {
  const up = await request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(name) } },
  });
  expect(up.ok(), `upload ${up.status()}`).toBeTruthy();
  const del = await request.post('/api/files/manager?action=delete', {
    data: { path: `${STORAGE}://`, items: [{ path: `${STORAGE}://${name}` }] },
  });
  expect(del.ok(), `delete ${del.status()}`).toBeTruthy();
}

test.describe('The explorer Trash deletes permanently', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('an operator’s "Delete permanently" removes the item from the trash', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await setAccountViewMode(page.request, 'list');
    const name = `kalici-${STAMP}.txt`;
    await trashed(page.request, name);

    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
    await page.getByTestId('sidenav').getByText(/^(Trash|Çöp kutusu)$/i).click();
    const row = page.locator('.fe-list__row').filter({ hasText: name }).first();
    await expect(row).toBeVisible({ timeout: 15_000 });

    await row.click({ button: 'right' });
    await page.getByRole('menu').first().getByRole('menuitem', { name: /^(Delete permanently|Kalıcı olarak sil)$/ }).click();
    const dialog = page.locator('.fe-modal__card').last();
    await expect(dialog, 'the dialog still says "moved to trash"').not.toContainText(/moved to trash|çöpe taşın/i);

    const purged = page.waitForResponse((r) => r.request().method() === 'DELETE' && r.url().includes('/api/admin/trash/'));
    await dialog.getByRole('button', { name: /^(Delete permanently|Kalıcı olarak sil)$/ }).click();
    const res = await purged;
    expect(res.ok(), `purge ${res.status()}`).toBeTruthy();

    await expect(row, 'the item is still in the trash').toHaveCount(0, { timeout: 15_000 });
    const list = await page.request.get('/api/files/manager/trash?limit=500');
    const entries = ((await list.json()).entries ?? []) as Array<{ name: string }>;
    expect(entries.map((e) => e.name)).not.toContain(name);
  });
});
