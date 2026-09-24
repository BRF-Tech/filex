/**
 * 123 — the explorer's Trash says what a deleted item is; a phone's menu does
 * not list keyboard shortcuts.
 *
 * QA sweep, 2026-09-21:
 *   #32  in the Trash every row's date read "—", nothing said how long an item
 *        had left or where it had been, Owner read "System" on every row, and
 *        "+ New" stood above it all with every entry greyed;
 *   #40  on a phone the row sheet listed "Enter", "Ctrl+X", "F2"… beside every
 *        verb — keys nobody holding a phone can press.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';

const STORAGE = `e2e-trash-${Date.now()}`;
const GONE = `eski-${Date.now()}.txt`;
const KEPT = `kalan-${Date.now()}.txt`;

async function openExplorer(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
}

test.describe('Trash and touch', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    const mk = await request.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name: 'Raporlar' } });
    expect(mk.ok()).toBeTruthy();
    for (const name of [GONE, KEPT]) {
      const up = await request.post('/api/files/manager?action=upload', {
        multipart: { path: `${STORAGE}://Raporlar`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(name) } },
      });
      expect(up.ok(), `upload ${name}`).toBeTruthy();
    }
    const del = await request.post('/api/files/manager?action=delete', {
      data: { path: `${STORAGE}://Raporlar`, items: [{ path: `${STORAGE}://Raporlar/${GONE}` }] },
    });
    expect(del.ok(), `delete ${del.status()}`).toBeTruthy();
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('a trashed row says when, where from and how long it has left; "+ New" is gone', async ({ page }) => {
    await openExplorer(page);
    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
    await page.getByTestId('sidenav').getByText(/^(Trash|Çöp Kutusu)$/).click();
    const row = page.locator('[data-fe-path]').filter({ hasText: GONE }).first();
    await expect(row).toBeVisible({ timeout: 15_000 });

    const head = page.locator('.fe-list__head');
    await expect(head).toContainText(/Deleted from|Silindiği yer/);
    await expect(head).toContainText(/Time left|Kalan süre/);
    await expect(head).not.toContainText(/^Owner$|Sahibi/);

    await expect(row.locator('.fe-list__col--mod')).not.toHaveText('—');
    await expect(row.locator('.fe-list__col--location')).toHaveText(`${STORAGE}/Raporlar`);
    await expect(row.locator('.fe-list__col--remaining')).toHaveText(/^\d+ (days?|gün)$/);
    await expect(row.locator('.fe-list__col--owner')).toHaveCount(0);

    await expect(page.getByTestId('sidenav-new')).toHaveCount(0);
  });

  test.describe('on a phone', () => {
    test.use({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });

    test('the row sheet lists verbs, not keyboard shortcuts', async ({ page }) => {
      await openExplorer(page);
      await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
      await page.goto(`/drive/explore#${encodeURIComponent(STORAGE)}/Raporlar`);
      const row = page.locator('[data-fe-path]').filter({ hasText: KEPT }).first();
      await expect(row).toBeVisible({ timeout: 15_000 });
      await row.locator('.fe-list__menu').tap();
      const sheet = page.locator('.fe-sheet');
      await expect(sheet).toBeVisible();
      await expect(sheet.getByRole('menuitem').first()).toBeVisible();
      const shown = await sheet
        .locator('.fe-ctx__key')
        .evaluateAll((els) => els.filter((e) => getComputedStyle(e).display !== 'none').length);
      expect(shown).toBe(0);
    });
  });

  test('a mouse-and-keyboard menu keeps its shortcuts (the control for the case above)', async ({ page }) => {
    await openExplorer(page);
    await page.goto(`/drive/explore#${encodeURIComponent(STORAGE)}/Raporlar`);
    const row = page.locator('[data-fe-path]').filter({ hasText: KEPT }).first();
    await expect(row).toBeVisible({ timeout: 15_000 });
    await row.click({ button: 'right' });
    const menu = page.locator('.fe-ctx').last();
    await expect(menu).toBeVisible();
    const shown = await menu
      .locator('.fe-ctx__key')
      .evaluateAll((els) => els.filter((e) => getComputedStyle(e).display !== 'none').length);
    expect(shown).toBeGreaterThan(3);
  });
});
