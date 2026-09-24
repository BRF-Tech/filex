/**
 * 141 — "Empty trash" is an operation of the queue (PR #47 + its v0.43.0
 * integration, backend ops/trash_empty.go).
 *
 * A trash of 61,844 files could not be emptied: the purge ran inside the
 * request and nginx cut it at sixty seconds. The purge is an ops job now: the
 * endpoint answers within two seconds, the run is a row of the operations
 * list (and the explorer's operations centre), an administrator can stop it,
 * and the admin Trash page follows it. These walk the real bundle:
 *
 *   1. the explorer's trash banner empties a trash, says so, and the run is a
 *      `trash-empty` row of the operations list and of the operations centre;
 *   2. the admin Trash page empties a storage's trash and says how many;
 *   3. a run that is still going: the admin page draws its strip, stops it
 *      with the queue's cancel, and says it was stopped — the server side of
 *      a long run is faked here (a real one needs tens of thousands of rows).
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';

const STAMP = Date.now();
const STORAGE = `e2e-empty-${STAMP}`;

async function trashSome(request: Page['request'], names: string[]) {
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

test.describe('Empty trash is an operation', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('the explorer empties the trash, says so, and the run is an ops row', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await setAccountViewMode(page.request, 'list');
    const names = [`a-${STAMP}.txt`, `b-${STAMP}.txt`, `c-${STAMP}.txt`];
    await trashSome(page.request, names);

    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
    await page.getByTestId('sidenav').getByText(/^(Trash|Çöp Kutusu)$/).click();
    await expect(page.locator('[data-fe-path]').filter({ hasText: names[0] }).first()).toBeVisible({ timeout: 15_000 });

    const posted = page.waitForResponse((r) => r.url().includes('/api/admin/trash/empty') && r.request().method() === 'POST');
    await page.getByTestId('trash-empty').click();
    await page.getByTestId('trash-empty-confirm').click();
    const res = await posted;
    expect(res.status(), 'an ordinary trash is done within the wait').toBe(200);
    const body = await res.json();
    expect(body.op_id, 'the answer names its ops row').toBeGreaterThan(0);
    expect(body.running).toBe(false);

    await expect(page.getByText(/^(Trash emptied|Çöp kutusu boşaltıldı)$/)).toBeVisible();
    for (const n of names) {
      await expect(page.locator('[data-fe-path]').filter({ hasText: n })).toHaveCount(0);
    }

    const list = await page.request.get('/api/files/ops');
    expect(list.ok()).toBeTruthy();
    const row = ((await list.json()).ops as Array<Record<string, unknown>>).find((o) => o.id === body.op_id);
    expect(row, 'the run is a row of the operations list').toBeTruthy();
    expect(row!.kind).toBe('trash-empty');
    expect(row!.status).toBe('ok');
    expect(row!.sources, 'it names no file').toEqual([]);

    // In the operations centre, under its own name.
    await page.locator('.fe-opc__badge').click();
    await expect(page.locator('.fe-opc__panel')).toContainText(/Empty trash|Çöp kutusunu boşaltma/);
  });

  test('the admin Trash page empties one storage and says how many', async ({ page }) => {
    await loginAs(page);
    const names = [`d-${STAMP}.txt`, `e-${STAMP}.txt`];
    await trashSome(page.request, names);
    await page.goto('/admin/trash');
    await expect(page.getByText(names[0]).first()).toBeVisible({ timeout: 15_000 });

    await page.getByTestId('trash-empty-open').click();
    await page.getByTestId('trash-empty-confirm').click();
    await expect(page.getByText(/items? purged|öğe silindi/).first()).toBeVisible();
    await expect(page.getByText(names[0])).toHaveCount(0);
    await expect(page.getByTestId('trash-emptying')).toHaveCount(0);
  });

  test('a run still going is drawn, stopped with the queue\'s cancel, and said as stopped', async ({ page }) => {
    await loginAs(page);
    await trashSome(page.request, [`f-${STAMP}.txt`]);
    let cancelled = false;
    let posted = false;
    let looks = 0;
    await page.route('**/api/admin/trash/empty', async (route) => {
      if (route.request().method() === 'GET' && !posted) {
        // The page asks on arrival whether a run is already going: none yet.
        await route.fulfill({ status: 200, json: { running: false } });
        return;
      }
      if (route.request().method() === 'POST') {
        posted = true;
        await route.fulfill({
          status: 202,
          json: { ok: true, op_id: 987654, running: true, total: 61844, scanned: 120, purged: 120, failed: 0, bytes: 0, started_at: new Date().toISOString() },
        });
        return;
      }
      looks++;
      await route.fulfill({
        status: 200,
        json: cancelled
          ? { ok: true, op_id: 987654, running: false, cancelled: true, total: 61844, scanned: 4000, purged: 4000, failed: 0, bytes: 0, started_at: new Date().toISOString() }
          : { ok: true, op_id: 987654, running: true, total: 61844, scanned: 3000, purged: 3000, failed: 0, bytes: 0, started_at: new Date().toISOString() },
      });
    });
    await page.route('**/api/files/ops/987654/cancel', async (route) => {
      cancelled = true;
      await route.fulfill({ status: 200, json: { op: { id: 987654, kind: 'trash-empty', status: 'running' } } });
    });

    await page.goto('/admin/trash');
    await page.getByTestId('trash-empty-open').click();
    await page.getByTestId('trash-empty-confirm').click();
    const strip = page.getByTestId('trash-emptying');
    await expect(strip).toBeVisible();
    await expect(strip).toContainText(/(120|3[,.]000) (of|\/) 61[,.]844/);
    await expect(page.getByTestId('trash-empty-open'), 'no second press while it runs').toBeDisabled();
    await expect(strip.getByRole('progressbar')).toBeVisible();

    await page.getByTestId('trash-empty-stop').click();
    await expect(strip).toHaveCount(0, { timeout: 10_000 });
    await expect(page.getByText(/Stopped after 4[,.]000 items|4[,.]000 öğeden sonra durduruldu/)).toBeVisible();
    expect(cancelled, 'the stop is the queue\'s cancel').toBe(true);
    expect(looks).toBeGreaterThan(0);
  });
});
