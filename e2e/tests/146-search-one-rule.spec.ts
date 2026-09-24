/**
 * One rule for when a typed word is in a file's name — measured in the
 * browser, on both boxes that answer it.
 *
 * GitHub PR #46: a name a Mac uploaded is stored decomposed (`u` + U+0308 for
 * `ü`) and the search box sends the composed letter, so typing the name as it
 * is shown found nothing. v0.43.0 also folds the four Latin i's into one
 * letter, as tags do: `kış` did not find `KIŞ LİSTESİ.xlsx` on any path,
 * because the default lower case of `I` is `i`, not `ı`.
 *
 * The toolbar search asks the server; the "Filter in this folder…" box
 * filters the listing in the browser (lib/fileFilters). Both must give the
 * same answer.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const STORAGE = `e2e-onerule-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
// What a Mac uploads: decomposed.
const GUREL = 'Ayşe Gürel - Günlük Plan.pdf'.normalize('NFD');
const FILES = [GUREL, 'KIŞ LİSTESİ.xlsx', 'ışık notları.txt', 'IŞIK.pdf', 'rapor.txt'];

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
  await expect(page.getByText('rapor.txt', { exact: true }).first()).toBeVisible();
}

test.describe('Search — one rule for a name, on both boxes', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    for (const name of FILES) {
      const up = await request.post('/api/files/manager?action=upload', {
        multipart: {
          path: `${STORAGE}://`,
          'file[]': { name, mimeType: 'application/octet-stream', buffer: Buffer.from(`${name}\n`) },
        },
      });
      if (!up.ok()) throw new Error(`upload ${name} failed: ${up.status()} ${await up.text()}`);
    }
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('the toolbar search finds a decomposed name and a Turkish name in capitals', async ({ page }) => {
    await openStorage(page);
    const field = page.locator('[data-testid="drive-search"] input').first();
    const cases: Array<[string, string[]]> = [
      ['gürel', [GUREL]],
      ['AYŞE GÜREL', [GUREL]],
      ['kış', ['KIŞ LİSTESİ.xlsx']],
      ['ışık', ['IŞIK.pdf', 'ışık notları.txt']],
      ['IŞIK', ['IŞIK.pdf', 'ışık notları.txt']],
    ];
    for (const [q, want] of cases) {
      await field.fill(q);
      await field.press('Enter');
      // The listing has been replaced by the answer (rapor.txt answers none
      // of these) before the answer is read — the folder's own listing holds
      // every file and would pass for any query.
      await expect(page.getByText('rapor.txt', { exact: true }), `${q} does not answer rapor.txt`).toHaveCount(0);
      for (const name of want) {
        await expect(page.getByText(name, { exact: true }).first(), `${q} → ${name}`).toBeVisible();
      }
    }
  });

  test('"Filter in this folder" folds the same way', async ({ page }) => {
    await openStorage(page);
    const find = page.getByTestId('filter-find');
    await expect(find).toBeVisible();
    for (const [q, want] of [
      ['ışık', ['IŞIK.pdf', 'ışık notları.txt']],
      ['IŞIK', ['IŞIK.pdf', 'ışık notları.txt']],
      ['kış', ['KIŞ LİSTESİ.xlsx']],
      ['gürel', [GUREL]],
    ] as Array<[string, string[]]>) {
      await find.fill(q);
      await expect(page.getByText('rapor.txt', { exact: true }), `${q} hides rapor.txt`).toHaveCount(0);
      for (const name of want) {
        await expect(page.getByText(name, { exact: true }).first(), `${q} → ${name}`).toBeVisible();
      }
    }
  });
});
