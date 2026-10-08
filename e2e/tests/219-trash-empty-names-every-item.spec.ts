/**
 * 219 — "Empty the trash?" names what the purge deletes: the server's count,
 * not the rows on screen (0.54, finding D1, task #206).
 *
 * The explorer read the trash once, with no limit - the server's default page
 * of 50 - and the confirmation said "This permanently deletes 50 items
 * (X MB)" while the purge took the whole trash: every entry of every storage
 * the caller reaches. With 65 entries of our own in the trash (and whatever
 * other specs left there) the old dialog said 50.
 *
 * Now the dialog asks the purge's own dry run (GET
 * /api/admin/trash/empty/preview) and shows its sentence, the button waits for
 * it, and the banner above the list says the server's count and size of the
 * whole trash. Nothing is emptied here: the dialog is the subject, and an
 * empty would take other specs' trash with it.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';

const STAMP = Date.now();
const STORAGE = `e2e-trash219-${STAMP}`;
const COUNT = 65;

async function trashMany(request: Page['request'], names: string[]) {
  for (const name of names) {
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(name) } },
    });
    expect(up.ok(), `upload ${name}`).toBeTruthy();
  }
  const del = await request.post('/api/files/manager?action=delete', {
    data: { path: `${STORAGE}://`, items: names.map((n) => ({ path: `${STORAGE}://${n}` })) },
  });
  expect(del.ok(), `delete ${del.status()}`).toBeTruthy();
}

test.describe('Empty the trash names every item', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test("the confirmation says the server's count of a trash larger than one page", async ({ page }) => {
    test.setTimeout(120_000);
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await setAccountViewMode(page.request, 'list');
    const names = Array.from({ length: COUNT }, (_, i) => `t219-${STAMP}-${String(i).padStart(2, '0')}.txt`);
    await trashMany(page.request, names);

    // The listing the view reads: one page, with the server's totals beside it.
    const listed = page.waitForResponse(
      (r) => r.url().includes('/api/files/manager/trash') && r.url().includes('limit=200') && r.request().method() === 'GET',
    );
    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
    await page.getByTestId('sidenav').getByText(/^(Trash|Çöp kutusu)$/i).click();
    const listing = await (await listed).json();
    expect(listing.total, 'the server counts every entry, not the page').toBeGreaterThanOrEqual(COUNT);
    expect(listing.total_bytes).toBeGreaterThan(0);
    await expect(page.getByTestId('trash-summary')).toContainText(String(listing.summary));

    // The dialog: the purge's dry run, said by the server.
    const previewed = page.waitForResponse((r) => r.url().includes('/api/admin/trash/empty/preview'));
    await page.getByTestId('trash-empty').click();
    const preview = await (await previewed).json();
    expect(preview.dry_run).toBe(true);
    expect(preview.count, 'every entry the purge takes - more than the 50 the old dialog counted').toBeGreaterThanOrEqual(COUNT);
    expect(preview.count).toBeGreaterThan(50);

    const said = page.getByTestId('trash-empty-count');
    await expect(said).toHaveText(preview.summary);
    // The number in the sentence is the server's count, in the screen's digit
    // grouping ("65", "1,204", "1.204") - never the 50 rows of a page.
    const digits = ((await said.textContent()) ?? '').match(/\d[\d.,\s]*/)?.[0] ?? '';
    expect(Number(digits.replace(/[.,\s]/g, ''))).toBe(preview.count);
    await expect(page.getByTestId('trash-empty-confirm')).toBeEnabled();

    // Nothing is emptied here: an empty would take other specs' trash with it.
    await page.locator('.fe-modal__card').last().getByRole('button', { name: /^(Cancel|Vazgeç)$/ }).click();
    await expect(said).toHaveCount(0);
  });
});
