/**
 * 158 — the explorer answers: a change the server refuses is said on screen,
 * and a batch upload lands where it was started.
 *
 * Audit of 2026-09-26 (a production install; "no positive or negative message,
 * nothing at all"):
 *
 *   1. A refused paste, drag-move, duplicate or copy went out as the explorer's
 *      `error` event only. Both first-party hosts write that event to the
 *      console (web Explore.vue, desktop app.html), so a refusal looked exactly
 *      like a paste that worked.
 *   2. A refused delete left its dialog open with nothing in it.
 *   3. A batch upload read the open folder again for every file: browsing
 *      while the first file was on its way sent the rest of the batch into
 *      whatever folder was open by then.
 *
 * The server's refusals are staged with `page.route` — the explorer's answer
 * to a refusal is what is under test, not the server's reasons for one.
 */
import { test, expect, type Page, type Route } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';

const STAMP = Date.now();
const STORAGE = `e2e-fb-${STAMP}`;
const REFUSED = /You are not allowed to do this|Bu işlem için yetkiniz yok/;

async function upload(page: Page, dir: string, name: string, body = 'feedback') {
  const res = await page.request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://${dir}`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(body) } },
  });
  expect(res.ok(), `upload ${name}: ${res.status()}`).toBeTruthy();
}

async function newFolder(page: Page, name: string) {
  const res = await page.request.post('/api/files/manager?action=newfolder', {
    data: { path: `${STORAGE}://`, name },
  });
  expect(res.ok(), `newfolder ${name}: ${res.status()}`).toBeTruthy();
}

async function namesIn(page: Page, dir: string): Promise<string[]> {
  const res = await page.request.get(
    `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://${dir}`)}`,
  );
  expect(res.ok(), `list ${dir}: ${res.status()}`).toBeTruthy();
  const body = (await res.json()) as { files?: Array<{ basename: string }> };
  return (body.files ?? []).map((f) => f.basename).sort();
}

function row(page: Page, rel: string) {
  return page.locator(`[data-fe-path="${STORAGE}://${rel}"]`).first();
}

async function pick(page: Page, rel: string, verb: RegExp) {
  const target = await settled(row(page, rel));
  await target.click({ button: 'right' });
  await target.dispose();
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  await menu.getByRole('menuitem', { name: verb }).click();
}

async function openFolder(page: Page, rel: string) {
  const target = await settled(row(page, rel));
  await target.dblclick();
  await target.dispose();
  await expect(page.locator('.fe-breadcrumb__crumb').last()).toContainText(rel);
}

const refuse = (route: Route) =>
  route.fulfill({ status: 403, contentType: 'application/json', body: JSON.stringify({ error: 'forbidden' }) });

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
}

test.describe('Explorer feedback', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('a paste the server refuses says why, on screen', async ({ page }) => {
    await openStorage(page);
    const file = `tasinacak-${STAMP}.txt`;
    const folder = `hedef-${STAMP}`;
    await upload(page, '', file);
    await newFolder(page, folder);
    await page.reload();

    await pick(page, file, /^(Cut|Kes)$/);
    await openFolder(page, folder);
    await page.route('**/api/files/move', refuse);

    await page.locator('.fe__body').click({ button: 'right', position: { x: 40, y: 200 } });
    const menu = page.getByRole('menu').first();
    await expect(menu).toBeVisible();
    await menu.getByRole('menuitem', { name: /^(Paste|Yapıştır)$/ }).click();

    await expect(page.locator('.fe-toast__msg')).toHaveText(REFUSED);
  });

  test('a delete the server refuses keeps its dialog, with the reason in it', async ({ page }) => {
    await openStorage(page);
    const file = `silinecek-${STAMP}.txt`;
    await upload(page, '', file);
    await page.reload();

    await pick(page, file, /^(Delete|Sil)$/);
    const dialog = page.getByRole('dialog', { name: /^(Delete\?|Silinsin mi\?)$/ });
    await expect(dialog).toBeVisible();
    await page.route('**/api/files/delete', refuse);
    const confirm = dialog.getByRole('button', { name: /^(Move to Trash|Çöpe at)$/ });
    await confirm.click();
    await expect(dialog.getByRole('alert')).toHaveText(REFUSED);
    await expect(dialog, 'the dialog stays: the file was not deleted').toBeVisible();

    await page.unroute('**/api/files/delete', refuse);
    await confirm.click();
    await expect(dialog).toBeHidden();
  });

  test('a batch upload lands in the folder it was started in, whatever is opened meanwhile', async ({ page }) => {
    await openStorage(page);
    const from = `buraya-${STAMP}`;
    const elsewhere = `baska-${STAMP}`;
    await newFolder(page, from);
    await newFolder(page, elsewhere);
    await page.reload();
    await openFolder(page, from);

    // The first file is held on its way, long enough to browse elsewhere.
    let held = false;
    await page.route(
      (url) => url.pathname.endsWith('/api/files/manager') && url.searchParams.get('action') === 'upload',
      async (route) => {
        if (!held) {
          held = true;
          await new Promise((r) => setTimeout(r, 2500));
        }
        await route.continue();
      },
    );
    const names = ['bir', 'iki', 'uc'].map((n) => `${n}-${STAMP}.txt`);
    await page.locator('input[type="file"]').first().setInputFiles(
      names.map((name) => ({ name, mimeType: 'text/plain', buffer: Buffer.from(name) })),
    );
    await expect.poll(() => held, { timeout: 5_000 }).toBe(true);

    await page.locator('.fe-breadcrumb__crumb', { hasText: STORAGE }).last().click();
    await openFolder(page, elsewhere);

    await expect
      .poll(() => namesIn(page, from), { timeout: 20_000, message: 'every file of the batch lands where it was started' })
      .toEqual([...names].sort());
    expect(await namesIn(page, elsewhere), 'nothing lands in the folder opened meanwhile').toEqual([]);
  });
});
