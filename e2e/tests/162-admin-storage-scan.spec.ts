/**
 * 162 — a storage made from the admin panel is on, and "Sync now" says what
 * happened.
 *
 * The panel's "New storage" form sends no `enabled`, and the server saved such
 * a body switched off: no scan, and "Sync now" answering 404 "storage not
 * found". "Sync now" itself said "Sync started" whatever the server answered,
 * and nothing after.
 */
import { test, expect } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';

const STAMP = Date.now();
const PANEL = `e2e-panel-${STAMP}`;
const SCANNED = `e2e-scan-${STAMP}`;

test.describe('A storage and its scan, from the admin panel', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, PANEL);
    await dropStorageByName(request, SCANNED);
    // On demand: nothing scans it until somebody asks.
    await seedLocalStorage(request, SCANNED, `/tmp/filex-${SCANNED}`, { sync_mode: 'ondemand' });
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, PANEL);
    await dropStorageByName(request, SCANNED);
  });

  test('a storage created the way the panel form creates it is switched on', async ({ page }) => {
    await loginAs(page);
    // Exactly the body web/src/views/StorageNew.vue sends: no `enabled`.
    const res = await page.request.post('/api/admin/storages', {
      data: {
        name: PANEL,
        driver: 'local',
        config: { path: `/tmp/filex-${PANEL}` },
        read_only: false,
        sync_interval_s: 900,
        sync_mode: 'ondemand',
      },
    });
    expect(res.ok(), `create ${res.status()}`).toBeTruthy();
    const created = (await res.json()) as { id: number; enabled: boolean };
    expect(created.enabled, 'saved switched off').toBe(true);

    const sync = await page.request.post(`/api/admin/storages/${created.id}/sync`);
    expect(sync.status(), 'its scan could not be started').toBe(202);
  });

  test('"Sync now" says it started, and says when the scan is done', async ({ page }) => {
    await loginAs(page);
    const list = (await (await page.request.get('/api/admin/storages')).json()) as Array<{ id: number; name: string }>;
    const id = list.find((s) => s.name === SCANNED)?.id;
    expect(id, 'the seeded storage is not listed').toBeTruthy();
    await page.goto('/admin/storages');
    // "Sync now" is in the row's Actions menu (the page is a table since #57).
    await page.getByTestId(`storage-actions-${id}`).click();
    await page.getByRole('menuitem', { name: /^(Sync now|Şimdi senkronize et)$/ }).click();

    await expect(page.getByText(/^(Sync started|Senkron başladı)$/).first()).toBeVisible();
    await expect(page.getByText(new RegExp(`^${SCANNED}: (the scan is done|tarama bitti)$`))).toBeVisible({
      timeout: 20_000,
    });
  });
});
