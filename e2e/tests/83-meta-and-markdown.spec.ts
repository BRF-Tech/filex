/**
 * Per-user meta endpoints + markdown side-by-side editor.
 *
 * Covers two related contracts shipped together in commit 6d64554:
 *
 *   - /api/files/manager/{star,starred,tags,recent} — server-side state
 *     that the StarButton / TagPicker / RecentlyOpened components read
 *     and mutate. Without these tests the wire-up could regress silently
 *     (the inline star column would just stop lighting up).
 *
 *   - PreviewModal's markdown branch in edit mode: a side-by-side
 *     textarea + live preview. The earlier "kind dispatch routes md
 *     into the code editor" intermediate version killed the live
 *     preview, so we want a regression pin that the split layout
 *     re-renders the HTML pane as the textarea changes.
 *
 * Fixtures: demo.md from the standard suite tree, copied into a fresh
 * per-suite storage so the recent/star/tag state isn't polluted by
 * other tests.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD, loginAs } from '../helpers/auth';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const STORAGE_NAME = `meta-md-${Date.now()}`;
let storageId: number;
let demoNodeId: number;

async function adminBearer(request: APIRequestContext): Promise<string> {
  const login = await request.post('/api/auth/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
  });
  if (!login.ok()) {
    throw new Error(`bearer login failed: ${login.status()} ${await login.text()}`);
  }
  const { token } = await login.json();
  return token;
}

test.describe('Meta routes + markdown editor', () => {
  test.beforeAll(async ({ playwright, baseURL }) => {
    const ctx = await playwright.request.newContext({ baseURL });
    const token = await adminBearer(ctx);
    const authed = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });

    const tmpRoot = path.join(__dirname, '..', '.tmp', STORAGE_NAME);
    fs.mkdirSync(tmpRoot, { recursive: true });
    fs.copyFileSync(
      path.join(__dirname, '..', 'fixtures', 'file-types', 'demo.md'),
      path.join(tmpRoot, 'demo.md'),
    );

    const created = await (
      await authed.post('/api/admin/storages/', {
        data: { name: STORAGE_NAME, driver: 'local', config: { root: tmpRoot } },
      })
    ).json();
    storageId = created.id;
    await authed.patch(`/api/admin/storages/${storageId}`, { data: { enabled: true } });

    // Force one indexing pass so demo.md gets a DB node id.
    const idx = await (
      await authed.get(
        `/api/files/manager?action=index&path=${encodeURIComponent(STORAGE_NAME + '://')}`,
      )
    ).json();
    const demoRow = (idx.files as Array<{ id: number; basename: string }>).find(
      (r) => r.basename === 'demo.md',
    );
    if (!demoRow) throw new Error('demo.md not found after index');
    demoNodeId = demoRow.id;

    await ctx.dispose();
    await authed.dispose();
  });

  test.afterAll(async ({ playwright, baseURL }) => {
    if (!storageId) return;
    const ctx = await playwright.request.newContext({ baseURL });
    const token = await adminBearer(ctx);
    const authed = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });
    await authed.delete(`/api/admin/storages/${storageId}`);
    await ctx.dispose();
    await authed.dispose();
  });

  test('star toggle reflects in the starred list', async ({ playwright, baseURL }) => {
    const token = await adminBearer(await playwright.request.newContext({ baseURL }));
    const api = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });

    // Star demo.md
    const setRes = await api.post('/api/files/manager/star', {
      data: { node_id: demoNodeId, starred: true },
    });
    expect(setRes.ok()).toBeTruthy();

    // It should appear in /starred. The endpoint shape is intentionally
    // a bit loose — older builds returned a bare array, newer ones wrap
    // it in {entries:[…]} — accept both.
    const listRes = await api.get('/api/files/manager/starred?limit=50');
    expect(listRes.ok()).toBeTruthy();
    const listBody = await listRes.json();
    const rows: Array<{ id: number }> = Array.isArray(listBody)
      ? listBody
      : Array.isArray(listBody?.entries)
        ? listBody.entries
        : Array.isArray(listBody?.nodes)
          ? listBody.nodes
          : [];
    expect(rows.find((r) => r.id === demoNodeId)).toBeTruthy();

    // Unstar and confirm it disappears.
    await api.post('/api/files/manager/star', {
      data: { node_id: demoNodeId, starred: false },
    });
    const list2 = await (await api.get('/api/files/manager/starred?limit=50')).json();
    const rows2: Array<{ id: number }> = Array.isArray(list2)
      ? list2
      : Array.isArray(list2?.entries)
        ? list2.entries
        : Array.isArray(list2?.nodes)
          ? list2.nodes
          : [];
    expect(rows2.find((r) => r.id === demoNodeId)).toBeFalsy();

    await api.dispose();
  });

  test('tag set + get round-trip', async ({ playwright, baseURL }) => {
    const token = await adminBearer(await playwright.request.newContext({ baseURL }));
    const api = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });

    const writeRes = await api.post('/api/files/manager/tags', {
      data: { node_id: demoNodeId, tags: ['draft', 'review-needed'] },
    });
    expect(writeRes.ok()).toBeTruthy();

    const readRes = await api.get(`/api/files/manager/tags?node_id=${demoNodeId}`);
    expect(readRes.ok()).toBeTruthy();
    const body = await readRes.json();
    expect(body.tags).toEqual(expect.arrayContaining(['draft', 'review-needed']));

    // Clear so the next test sees a clean slate.
    await api.post('/api/files/manager/tags', {
      data: { node_id: demoNodeId, tags: [] },
    });
    await api.dispose();
  });

  test('recently-opened tracks the last POSTed node', async ({ playwright, baseURL }) => {
    const token = await adminBearer(await playwright.request.newContext({ baseURL }));
    const api = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });

    const markRes = await api.post('/api/files/manager/recent', {
      data: { node_id: demoNodeId },
    });
    expect(markRes.ok()).toBeTruthy();

    const listRes = await api.get('/api/files/manager/recent?limit=10');
    expect(listRes.ok()).toBeTruthy();
    const body = await listRes.json();
    const rows: Array<{ id: number }> = Array.isArray(body)
      ? body
      : Array.isArray(body?.entries)
        ? body.entries
        : [];
    expect(rows.length).toBeGreaterThan(0);
    expect(rows[0].id).toBe(demoNodeId);

    await api.dispose();
  });

  test('markdown editor renders split view in edit mode', async ({ page }) => {
    await loginAs(page);

    await page.goto(
      `/admin/files/edit?path=${encodeURIComponent(
        STORAGE_NAME + '://demo.md',
      )}&mode=edit&type=md`,
    );

    // Split layout root + bar + both panes must be visible.
    await expect(page.locator('.fe-preview__md-split')).toBeVisible({ timeout: 8_000 });
    await expect(page.locator('.fe-preview__md-split-bar')).toBeVisible();
    const input = page.locator('.fe-preview__md-split-input');
    const output = page.locator('.fe-preview__md-split-output');
    await expect(input).toBeVisible();
    await expect(output).toBeVisible();

    // Type new markdown — the right pane should re-render to include it
    // within the 250ms debounce window we configured.
    await input.click();
    await page.keyboard.press('ControlOrMeta+a');
    await page.keyboard.press('Backspace');
    await page.keyboard.type('# regression marker\n\n*one*\n', { delay: 5 });
    await expect(output.locator('h1', { hasText: 'regression marker' })).toBeVisible({
      timeout: 2_000,
    });
    await expect(output.locator('em', { hasText: 'one' })).toBeVisible();
  });

  test('chromeless route keeps no dialog chrome', async ({ page }) => {
    await loginAs(page);
    await page.goto(
      `/admin/files/edit?path=${encodeURIComponent(
        STORAGE_NAME + '://demo.md',
      )}&mode=edit&type=md`,
    );

    // The standalone editor route opts into the chromeless modal — no
    // header bar (× close button), no actions footer. Backdrop must
    // exist for ESC handling but use the chromeless variant class.
    await expect(page.locator('.fe-modal__card--chromeless')).toBeVisible({ timeout: 8_000 });
    await expect(page.locator('.fe-modal__head')).toHaveCount(0);
    await expect(page.locator('.fe-modal__actions')).toHaveCount(0);
    await expect(page.locator('.fe-modal__backdrop--chromeless')).toBeVisible();
  });
});
