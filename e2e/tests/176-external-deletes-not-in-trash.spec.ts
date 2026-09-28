/**
 * 176 — issue #74: an item deleted outside filex is not in the Trash.
 *
 * A file or folder deleted on the disk (a shell, another program) was found
 * gone by the next scan and shown in the Trash with a Restore — but its bytes
 * were never in `.filex-trash/`, so there was nothing to bring back. The scan
 * now removes such an item from the catalogue; the Trash holds only what was
 * deleted in filex, and that still restores.
 *
 * One flow, in a real browser against the real binary and a local storage:
 *   · a file is deleted in the explorer → it is in the explorer's Trash;
 *   · a file and a folder are deleted on the disk → the admin's "Sync now"
 *     → neither is in the Trash (nor in the listing);
 *   · the item deleted in the explorer is restored from the Trash and its
 *     bytes are back on the disk.
 */
import fs from 'node:fs';
import path from 'node:path';

import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage, storageRoot } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';

const STAMP = Date.now();
const STORAGE = `e2e-dis-${STAMP}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const ON_DISK = `diskten-${STAMP}.txt`;
const DIR = `klasor-${STAMP}`;
const IN_DIR = `icerik-${STAMP}.txt`;
const IN_UI = `arayuzden-${STAMP}.txt`;
let storageId = 0;

async function names(request: APIRequestContext, rel = ''): Promise<string[]> {
  const res = await request.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://${rel}`)}`);
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = (await res.json()) as { files?: Array<{ basename: string }> };
  return (body.files ?? []).map((f) => f.basename).sort();
}

async function trashNames(request: APIRequestContext): Promise<string[]> {
  const res = await request.get(`/api/files/manager/trash?storage_id=${storageId}&limit=500`);
  expect(res.ok(), `trash listing ${res.status()}`).toBeTruthy();
  return (((await res.json()).entries ?? []) as Array<{ name: string }>).map((e) => e.name);
}

/** A full scan through the API, waited for until a run newer than the ones before it is over. */
async function scan(request: APIRequestContext) {
  const runs = async () => {
    const res = await request.get(`/api/admin/storages/${storageId}/sync-runs?limit=50`);
    expect(res.ok()).toBeTruthy();
    return ((await res.json()) as { entries?: Array<{ id: number; status: string }> }).entries ?? [];
  };
  const before = new Set((await runs()).map((r) => r.id));
  const started = await request.post(`/api/admin/storages/${storageId}/sync`);
  expect(started.status(), await started.text()).toBeLessThan(300);
  await expect
    .poll(async () => (await runs()).find((r) => !before.has(r.id) && r.status !== 'running')?.status ?? 'pending', {
      timeout: 30_000,
    })
    .toBe('ok');
}

function row(page: Page, rel: string) {
  return page.locator(`[data-fe-path="${STORAGE}://${rel}"]`).first();
}

function trashRow(page: Page, name: string) {
  return page.locator('.fe-list__row').filter({ hasText: name });
}

test.beforeAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);
  const root = storageRoot(MOUNT);
  // Twenty files that stay, so what the test deletes is well under the 30% a
  // scan's whole-listing guard allows.
  const files = [ON_DISK, `${DIR}/${IN_DIR}`, IN_UI, ...Array.from({ length: 20 }, (_, i) => `kalici/f${i}.txt`)];
  for (const rel of files) {
    fs.mkdirSync(path.join(root, path.dirname(rel)), { recursive: true });
    fs.writeFileSync(path.join(root, rel), rel);
  }
  // On demand: nothing scans it but the scans this spec asks for.
  const row = await seedLocalStorage(request, STORAGE, MOUNT, { sync_mode: 'ondemand' });
  storageId = row.id;
  await scan(request);
  expect(await names(request)).toEqual(expect.arrayContaining([ON_DISK, DIR, IN_UI]));
});

test.afterAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);
});

test('an item deleted on the disk is not in the Trash; one deleted in filex is, and restores', async ({ page }) => {
  const root = storageRoot(MOUNT);
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');

  await test.step('delete a file in the explorer', async () => {
    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
    const target = await settled(row(page, IN_UI));
    await target.click({ button: 'right' });
    await target.dispose();
    const menu = page.getByRole('menu').first();
    await expect(menu).toBeVisible();
    await menu.getByRole('menuitem', { name: /^(Delete|Sil)$/ }).click();
    const dialog = page.getByRole('dialog', { name: /^(Delete\?|Silinsin mi\?)$/ });
    await dialog.getByRole('button', { name: /^(Move to Trash|Çöpe at)$/ }).click();
    await expect(dialog).toBeHidden();
    await expect.poll(() => trashNames(page.request), { timeout: 15_000 }).toContain(IN_UI);
  });

  await test.step('delete a file and a folder on the disk, then "Sync now"', async () => {
    fs.rmSync(path.join(root, ON_DISK));
    fs.rmSync(path.join(root, DIR), { recursive: true, force: true });

    await page.goto('/admin/storages');
    await page.getByTestId(`storage-actions-${storageId}`).click();
    await page.getByRole('menuitem', { name: /^(Sync now|Şimdi senkronize et)$/ }).click();
    await expect(page.getByText(new RegExp(`^${STORAGE}: (the scan is done|tarama bitti)$`))).toBeVisible({
      timeout: 30_000,
    });

    const listed = await names(page.request);
    expect(listed, 'the scan left the deleted items in the listing').not.toContain(ON_DISK);
    expect(listed).not.toContain(DIR);
    const inTrash = await trashNames(page.request);
    expect(inTrash, 'an item deleted outside filex is in the Trash').not.toContain(ON_DISK);
    expect(inTrash).not.toContain(DIR);
    expect(inTrash).not.toContain(IN_DIR);
    expect(inTrash, 'the item deleted in filex left the Trash').toContain(IN_UI);
  });

  await test.step('the explorer’s Trash shows only what was deleted in filex', async () => {
    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
    await page.getByTestId('sidenav').getByText(/^(Trash|Çöp kutusu)$/i).click();
    await expect(trashRow(page, IN_UI)).toBeVisible({ timeout: 15_000 });
    await expect(trashRow(page, ON_DISK), 'a file deleted on the disk is offered for restore').toHaveCount(0);
    await expect(trashRow(page, DIR), 'a folder deleted on the disk is offered for restore').toHaveCount(0);
    await expect(trashRow(page, IN_DIR)).toHaveCount(0);
  });

  await test.step('restore what was deleted in filex', async () => {
    const target = await settled(trashRow(page, IN_UI).first());
    await target.click({ button: 'right' });
    await target.dispose();
    await page.getByRole('menu').first().getByRole('menuitem', { name: /^(Restore|Geri getir)$/ }).click();
    await expect(trashRow(page, IN_UI), 'the Trash still lists the restored item').toHaveCount(0, { timeout: 15_000 });
    await expect.poll(() => names(page.request), { timeout: 15_000 }).toContain(IN_UI);
    await expect.poll(() => fs.existsSync(path.join(root, IN_UI)), { timeout: 15_000 }).toBe(true);
    expect(fs.readFileSync(path.join(root, IN_UI), 'utf8')).toBe(IN_UI);
  });
});
