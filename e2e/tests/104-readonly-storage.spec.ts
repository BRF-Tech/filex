/**
 * A read-only storage looks read-only to the people using it.
 *
 * Issue #30: the admin list showed an "RO" badge, the users saw nothing — the
 * sidebar still offered "+ New" (Upload, New folder), the toolbar offered New
 * folder, and every one of those ended in "storage is read-only" from the
 * server. Now the sidebar row carries a "Read-only" tag, the "+ New" menu is
 * gone on such a storage, and the background context menu offers no write.
 *
 * Measured against a writable storage in the same run, so the selectors are
 * proven to find what they look for: the writable one MUST show "+ New" and
 * MUST NOT show the tag.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const RO = `e2e-ro-${Date.now()}`;
const RW = `e2e-rw-${Date.now()}`;

async function openStorage(page: Page, name: string) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(name)}`);
  await expect(page.getByTestId(`sidenav-storage-${name}`)).toBeVisible();
  await page.getByTestId(`sidenav-storage-${name}`).click();
}

test.describe('Read-only storage — the user is told, not refused', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, RO);
    await dropStorageByName(request, RW);
    await seedLocalStorage(request, RO, `/tmp/filex-${RO}`, { read_only: true });
    await seedLocalStorage(request, RW, `/tmp/filex-${RW}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, RO);
    await dropStorageByName(request, RW);
  });

  test('sidebar row carries the tag and the "+ New" menu is gone', async ({ page }) => {
    await openStorage(page, RO);

    const row = page.getByTestId(`sidenav-storage-${RO}`);
    await expect(row.getByTestId('sidenav-storage-readonly')).toBeVisible();
    await expect(row.getByTestId('sidenav-storage-readonly')).toHaveText(/read-only/i);
    await expect(page.getByTestId('sidenav-new')).toHaveCount(0);
  });

  test('the background context menu offers no New folder / Upload', async ({ page }) => {
    await openStorage(page, RO);
    // `.fe__body` is the pane's listing area whatever it holds (rows, a grid,
    // the empty state) — its `@contextmenu` is the background menu's owner.
    const pane = page.locator('.fe__body').first();
    await expect(pane).toBeVisible();
    const box = await pane.boundingBox();
    if (!box) throw new Error('pane has no box');
    await pane.click({ button: 'right', position: { x: box.width - 12, y: box.height - 12 } });
    // Either no menu at all, or a menu without a write in it.
    await page.waitForTimeout(300);
    await expect(page.getByRole('menuitem', { name: /^new folder$/i })).toHaveCount(0);
    await expect(page.getByRole('menuitem', { name: /^upload$/i })).toHaveCount(0);
  });

  test('a writable storage in the same install still offers everything', async ({ page }) => {
    await openStorage(page, RW);
    const row = page.getByTestId(`sidenav-storage-${RW}`);
    await expect(row.getByTestId('sidenav-storage-readonly')).toHaveCount(0);
    await expect(page.getByTestId('sidenav-new')).toBeVisible();
  });
});
