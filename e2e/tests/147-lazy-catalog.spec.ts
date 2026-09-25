/**
 * 147-lazy-catalog — sync_mode `lazy` (issue #45, docs/LAZY-CATALOGUE.md), in
 * a real browser against a real binary.
 *
 * What is measured:
 *   · the storage form offers the mode for a local storage, with the
 *     behavior choice drawn from the descriptor;
 *   · behavior B ("only on open"): a folder is listed straight from disk
 *     before anything is cataloged, the explorer says plainly that search,
 *     folder sizes and usage cover opened folders only, a folder nobody opened
 *     has no size ("—", not "0 B"), and an administrator's "Catalog
 *     everything" runs the full sync, after which the notice is gone;
 *   · behavior A ("click first, fill in the background"): the background pass
 *     converges on its own and the storage page shows its catalog block.
 */
import fs from 'node:fs';
import path from 'node:path';

import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, storageRoot } from '../helpers/seed';
import { setAccountViewMode, VIEW_MODE_LS_KEY } from '../helpers/prefs';

const STAMP = Date.now();
const LAZY_B = `e2e-lazyb-${STAMP}`;
const LAZY_A = `e2e-lazya-${STAMP}`;

function tree(mount: string) {
  const root = storageRoot(mount);
  for (const dir of ['belgeler', 'arsiv/2019', 'arsiv/2020', 'fotolar']) {
    for (let i = 0; i < 3; i++) {
      const p = path.join(root, dir, `dosya-${i}.txt`);
      fs.mkdirSync(path.dirname(p), { recursive: true });
      fs.writeFileSync(p, `${dir} ${i}\n`);
    }
  }
  fs.writeFileSync(path.join(root, 'kok.txt'), 'kok\n');
  return root;
}

async function storageRow(request: APIRequestContext, id: number) {
  const res = await request.get(`/api/admin/storages/${id}`);
  expect(res.ok(), await res.text()).toBeTruthy();
  return (await res.json()) as {
    coverage?: { reason?: string } | null;
    catalogue?: { complete: boolean; filler?: string; catalogued_folders?: number } | null;
  };
}

async function openExplorer(page: Page, storage: string) {
  await page.addInitScript((key) => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem(key, 'list');
  }, VIEW_MODE_LS_KEY);
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/admin/explore?storage=${encodeURIComponent(storage)}`);
  await page.getByTestId(`sidenav-storage-${storage}`).click();
}

const row = (page: Page, storage: string, rel: string) => page.locator(`[data-fe-path="${storage}://${rel}"]`);

let idB = 0;
let idA = 0;

test.beforeAll(async ({ request }) => {
  await dropStorageByName(request, LAZY_B);
  await dropStorageByName(request, LAZY_A);
  const mountB = `/tmp/filex-${LAZY_B}`;
  tree(mountB);
  idB = (await seedLocalStorage(request, LAZY_B, mountB, { sync_mode: 'lazy', config: { path: storageRoot(mountB), lazy_fill: 'on_open' } })).id;
  const mountA = `/tmp/filex-${LAZY_A}`;
  tree(mountA);
  idA = (await seedLocalStorage(request, LAZY_A, mountA, { sync_mode: 'lazy', config: { path: storageRoot(mountA), lazy_fill: 'background' } })).id;
});

test.afterAll(async ({ request }) => {
  await dropStorageByName(request, LAZY_B);
  await dropStorageByName(request, LAZY_A);
});

test('the storage form offers the lazy catalog for a local storage', async ({ page }) => {
  await loginAs(page);
  await page.goto(`/admin/storages/${idB}`);
  // The mode's own select — the behaviour's is drawn inside the same block.
  const mode = page.getByTestId('storage-sync-mode');
  await expect(mode.locator('select[name="sync_mode"]')).toHaveValue('lazy');
  const fields = page.getByTestId('storage-lazy-fields');
  await expect(fields).toContainText('Catalog behavior');
  await expect(fields.locator('select')).toHaveValue('on_open');
  await expect(page.getByTestId('storage-catalog-status')).toBeVisible();
});

test('only on open: listed from disk at once, and it says what it covers', async ({ page, request }) => {
  await apiLogin(request);
  await openExplorer(page, LAZY_B);

  // Straight from disk, before anything is cataloged.
  await expect(row(page, LAZY_B, 'belgeler')).toBeVisible({ timeout: 15_000 });
  await expect(row(page, LAZY_B, 'kok.txt')).toBeVisible();

  const notice = page.getByTestId('catalog-coverage');
  await expect(notice).toContainText('Only the folders people open on this storage are cataloged');
  // A folder nobody has opened has no size the catalog knows — not "0 B".
  await expect(row(page, LAZY_B, 'fotolar').locator('.fe-list__col--size')).toHaveText('—');

  // An administrator can have the whole storage cataloged once.
  await page.getByTestId('catalog-coverage-all').click();
  await expect
    .poll(async () => (await storageRow(request, idB)).catalogue?.complete ?? false, { timeout: 30_000 })
    .toBe(true);
  await page.reload();
  await page.getByTestId(`sidenav-storage-${LAZY_B}`).click();
  await expect(row(page, LAZY_B, 'belgeler')).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId('catalog-coverage')).toHaveCount(0);
  await expect(row(page, LAZY_B, 'fotolar').locator('.fe-list__col--size')).not.toHaveText('—');
});

test('click first, fill in the background: the pass converges on its own', async ({ page, request }) => {
  await apiLogin(request);
  await expect
    .poll(async () => (await storageRow(request, idA)).catalogue?.complete ?? false, { timeout: 30_000 })
    .toBe(true);
  const status = await storageRow(request, idA);
  expect(status.coverage ?? null, 'nothing left out').toBeNull();
  expect(status.catalogue?.filler).toBe('converged');

  await loginAs(page);
  await page.goto(`/admin/storages/${idA}`);
  await expect(page.getByTestId('storage-catalog-summary')).toHaveText('Every folder of this storage is cataloged.');
});
