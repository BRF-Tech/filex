/**
 * 201-table-a11y - the one table passes axe where it used to fail, and does not
 * raise a page error in WebKit (task #153).
 *
 * Measured on the Apps store (22 tables) with axe + Playwright:
 *
 *   1. the column resize handle is a focusable `role="separator"` and carried no
 *      `aria-valuenow` (axe "critical", aria-required-attr);
 *   2. an empty table's `rowgroup` held no `row` (aria-required-children);
 *   3. WebKit reported the table's ResizeObserver as a page error, "ResizeObserver
 *      loop completed with undelivered notifications".
 *
 * Every assertion runs on the REAL tables - the explorer's list and the admin
 * pages that draw DataTable - because the point of one table is that a fix
 * reaches all of them. Run it under WebKit as well:
 * `E2E_BROWSERS=chromium,webkit node e2e/run.mjs local --grep "table a11y"`.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { createRequire } from 'node:module';
import fs from 'node:fs';
import { apiLogin, loginAs } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { setAccountViewMode, VIEW_MODE_LS_KEY } from '../helpers/prefs';

const STORAGE = `e2e-tbla11y-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const AXE = fs.readFileSync(createRequire(import.meta.url).resolve('axe-core/axe.min.js'), 'utf8');
/* ⚠ Only the observer's own report is asserted. WebKit also lists aborted
 * fetches ("... due to access control checks") as page errors when a
 * navigation cuts a request short; those say nothing about the table. */
const RO_LOOP = /ResizeObserver loop/i;
const RULES = ['aria-required-attr', 'aria-required-children', 'aria-required-parent', 'aria-valid-attr-value', 'aria-allowed-attr'];

async function seedFiles(request: APIRequestContext) {
  await dropStorageByName(request, STORAGE);
  await seedLocalStorage(request, STORAGE, MOUNT);
  await apiLogin(request);
  for (const name of ['alpha.txt', 'beta.txt']) {
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(`${name}\n`) } },
    });
    if (!up.ok()) throw new Error(`upload failed: ${up.status()} ${await up.text()}`);
  }
}

/** axe's verdict on every table on the page, as "rule: where" lines. */
async function violations(page: Page): Promise<string[]> {
  await page.addScriptTag({ content: AXE });
  return page.evaluate(async (rules) => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const axe = (window as any).axe;
    const res = await axe.run(document.querySelector('.fe-table, .fe-list') ? '.fe-table, .fe-list' : 'body', {
      runOnly: { type: 'rule', values: rules },
    });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return res.violations.flatMap((v: any) => v.nodes.map((n: any) => `${v.id}: ${n.target.join(' ')}`));
  }, RULES);
}

async function boot(page: Page) {
  await page.addInitScript((key) => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem(key, 'list');
  }, VIEW_MODE_LS_KEY);
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
}

test.describe('table a11y', () => {
  test.beforeAll(async ({ request }) => seedFiles(request));
  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await dropStorageByName(request, STORAGE);
  });

  test('the explorer list: handles carry their value, axe is clean, no page error', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => {
      if (RO_LOOP.test(e.message)) errors.push(e.message);
    });
    await boot(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    await expect(page.locator('.fe-list__head [role="separator"]').first()).toBeVisible();
    const handles = page.locator('.fe-list__head [role="separator"]');
    for (let i = 0; i < (await handles.count()); i++) {
      const h = handles.nth(i);
      const [now, min, max] = await Promise.all(['aria-valuenow', 'aria-valuemin', 'aria-valuemax'].map((a) => h.getAttribute(a)));
      expect(now, `handle ${i} aria-valuenow`).not.toBeNull();
      expect(Number(min)).toBeLessThanOrEqual(Number(now));
      expect(Number(now)).toBeLessThanOrEqual(Number(max));
    }
    // a keyboard step moves the value
    const first = handles.first();
    const before = Number(await first.getAttribute('aria-valuenow'));
    await first.focus();
    await page.keyboard.press('ArrowRight');
    await expect.poll(async () => Number(await first.getAttribute('aria-valuenow'))).toBeGreaterThan(before);
    expect(await violations(page)).toEqual([]);
    await page.setViewportSize({ width: 700, height: 800 });
    await page.setViewportSize({ width: 1200, height: 800 });
    await page.waitForTimeout(500);
    expect(errors).toEqual([]);
  });

  test('admin tables, filled and empty: axe is clean, no page error', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => {
      if (RO_LOOP.test(e.message)) errors.push(e.message);
    });
    await boot(page);
    let tables = 0;
    let empty = 0;
    const found: string[] = [];
    for (const route of ['users', 'groups', 'trash', 'shares', 'audit', 'queue', 'webhooks', 'grants', 'tenants', 'roles']) {
      await page.goto(`/admin/${route}`);
      await page.waitForLoadState('networkidle');
      if (!(await page.locator('.fe-list').count())) continue;
      tables++;
      if (await page.locator('.fe-list__empty').count()) empty++;
      found.push(...(await violations(page)).map((v) => `${route} ${v}`));
    }
    expect(tables, 'admin pages that draw a table').toBeGreaterThanOrEqual(3);
    expect(empty, 'admin tables that were empty').toBeGreaterThanOrEqual(1);
    expect(found).toEqual([]);
    expect(errors).toEqual([]);
  });
});
