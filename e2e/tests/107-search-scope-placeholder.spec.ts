/**
 * The top search field searches the WHOLE storage, so its placeholder must
 * name the storage — never the open folder.
 *
 * 2026-09-19: inside a folder called "Photos" the field read "Search in
 * Photos" while the results came from the entire storage (`?action=search`
 * filters by storage id only). The folder-scoped box is the filter bar's
 * "Filter in this folder…"; the search field now says "Search in <storage>"
 * and, where no single storage is in view (Home, the virtual root, Recent…),
 * "Search all storages".
 */
import { test, expect } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const STORAGE = `e2e-scope-${Date.now()}`;
const FOLDER = 'Photos';

test.describe('Search placeholder names the storage, not the folder', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    await apiLogin(request);
    const mk = await request.post('/api/files/manager?action=newfolder', {
      data: { path: `${STORAGE}://`, name: FOLDER },
    });
    if (!mk.ok()) throw new Error(`newfolder failed: ${mk.status()} ${await mk.text()}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('inside a folder the field still says the storage; on Home it says all storages', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    await page.getByTestId(`sidenav-storage-${STORAGE}`).click();

    const field = page.locator('[data-testid="drive-search"] input').first();
    await expect(field).toBeVisible();
    await expect(field).toHaveAttribute('placeholder', `Search in ${STORAGE}`);

    // Enter the folder: the scope must NOT become the folder name.
    await page.getByText(FOLDER, { exact: true }).first().dblclick();
    await expect(page).toHaveURL(new RegExp(encodeURIComponent(FOLDER)));
    await expect(field).toHaveAttribute('placeholder', `Search in ${STORAGE}`);
    await expect(field).not.toHaveAttribute('placeholder', `Search in ${FOLDER}`);

    // Home: no single storage in view.
    await page.getByTestId('sidenav-view-home').click();
    await expect(field).toHaveAttribute('placeholder', 'Search all storages');
  });
});
