/**
 * 163 — "Delete permanently" in the explorer's Trash deletes, for an operator.
 *
 * The Trash's menu offered "Delete permanently", the dialog it opened said the
 * items "will be moved to trash", and then nothing was deleted: the explorer
 * showed a retention notice instead. An operator's press now purges, through
 * the queue where the server runs purges as jobs (capabilities.queued).
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';

const STAMP = Date.now();
const STORAGE = `e2e-purge-${STAMP}`;
const PREFS = '/api/me/prefs?surface=web';

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
  // ⚠ Turkish on the ACCOUNT, and put back (lesson #616). This spec used to
  // set only localStorage's filex.locale; the page then wrote "tr" into the
  // shared admin's preferences and nothing took it back, so every later spec
  // that restored "the language it found" restored Turkish: in the 0.50
  // integration run 171 read "Güncelleme var" and 173's password change found
  // no "Change password…".
  let api: APIRequestContext;
  let prefsBefore: Record<string, unknown> = {};

  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    const doc = got.ok() ? (await got.json()).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'tr' } } })).ok()).toBe(true);
  });

  test.afterAll(async ({ request }) => {
    await api?.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await api?.dispose();
    await dropStorageByName(request, STORAGE);
  });

  test('an operator’s "Delete permanently" removes the item from the trash', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    /* In Turkish: the dialog's wording is what this checks, and the old
       assertion's `çöpe taşın` matched no Turkish sentence at all (the
       delete dialog says "çöpe atılacak"), so only English was ever tested. */
    await page.addInitScript(() => localStorage.setItem('filex.locale', 'tr'));
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
    await expect(dialog, 'the dialog is not in Turkish').toContainText('kalıcı olarak silinecek');
    await expect(dialog, 'the dialog still says "moved to trash"').not.toContainText(/moved to trash|çöpe atılacak/i);

    // One request for the selection (0.54, finding A15): POST /api/admin/trash/purge.
    const purged = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().includes('/api/admin/trash/purge'),
    );
    await dialog.getByRole('button', { name: /^(Delete permanently|Kalıcı olarak sil)$/ }).click();
    const res = await purged;
    expect(res.ok(), `purge ${res.status()}`).toBeTruthy();

    await expect(row, 'the item is still in the trash').toHaveCount(0, { timeout: 15_000 });
    const list = await page.request.get('/api/files/manager/trash?limit=500');
    const entries = ((await list.json()).entries ?? []) as Array<{ name: string }>;
    expect(entries.map((e) => e.name)).not.toContain(name);
  });

  // The test above was red in WebKit on GitHub's full matrix (runs
  // 37661356185, 37702037032; task #199): the dialog closed and no purge left.
  // The trash was listed a second time right at the right-click (a realtime
  // refresh), and every listing of the trash cleared the selection, so
  // "Delete permanently" found nothing to delete. Here the refresh is the
  // Refresh button's, at a moment the test chooses.
  test('a refresh of the trash keeps what was selected, and "Delete permanently" then deletes it', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await page.addInitScript(() => localStorage.setItem('filex.locale', 'tr'));
    await loginAs(page);
    await setAccountViewMode(page.request, 'list');
    const name = `yenilenen-${STAMP}.txt`;
    await trashed(page.request, name);

    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
    await page.getByTestId('sidenav').getByText(/^(Trash|Çöp kutusu)$/i).click();
    const row = page.locator('.fe-list__row').filter({ hasText: name }).first();
    await expect(row).toBeVisible({ timeout: 15_000 });
    await row.locator('.fe-list__check').first().click();
    await expect(row).toHaveAttribute('aria-selected', 'true');

    const relisted = page.waitForResponse(
      (r) => r.request().method() === 'GET' && new URL(r.url()).pathname === '/api/files/manager/trash',
    );
    await page.getByRole('button', { name: /^(Refresh|Yenile)$/ }).first().click();
    expect((await relisted).ok()).toBe(true);
    await expect(row, 'the refresh dropped the selection').toHaveAttribute('aria-selected', 'true');

    await row.click({ button: 'right' });
    await page.getByRole('menu').first().getByRole('menuitem', { name: /^(Delete permanently|Kalıcı olarak sil)$/ }).click();
    const dialog = page.locator('.fe-modal__card').last();
    await expect(dialog).toContainText('kalıcı olarak silinecek');
    const purged = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().includes('/api/admin/trash/purge'),
    );
    await dialog.getByRole('button', { name: /^(Delete permanently|Kalıcı olarak sil)$/ }).click();
    const res = await purged;
    expect(res.ok(), `purge ${res.status()}`).toBeTruthy();
    await expect(row, 'the item is still in the trash').toHaveCount(0, { timeout: 15_000 });
  });
});
