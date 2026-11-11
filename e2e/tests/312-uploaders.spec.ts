import { test, expect } from '@playwright/test';
import { readdirSync, existsSync } from 'node:fs';
import path from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';

/**
 * Uploaders (0.60, docs/UPLOADERS.md) - in a real browser, against the
 * binary:
 *
 *  1. made in the Uploaders tab, the key shown once and ON SCREEN (Create is
 *     at the foot of the form, the key at the head of the pane);
 *  2. the request ShareX sends - multipart `file`, the key as a bearer -
 *     stores the file and answers a link that opens inline;
 *  3. the deletion link: opening it (as a link preview would) deletes
 *     nothing; its button moves the file to the trash and ends the link;
 *  4. the key opens no other door.
 *
 * The storage is this spec's own, dropped at the end.
 */
const STORAGE = 'e2e-uploaders';
const MOUNT = '/tmp/filex-e2e-uploaders';

test.describe('Uploaders', () => {
  test.beforeAll(async ({ request }) => {
    await seedLocalStorage(request, STORAGE, MOUNT);
  });

  test.afterAll(async ({ request, playwright, baseURL }) => {
    const admin = await newAuthedRequest(playwright, baseURL ?? '');
    const list = await admin.get('/api/auth/uploaders');
    if (list.ok()) {
      const { uploaders } = (await list.json()) as { uploaders: Array<{ id: number; storage: string }> };
      for (const u of uploaders.filter((x) => x.storage === STORAGE)) await admin.delete(`/api/auth/uploaders/${u.id}`);
    }
    await admin.dispose();
    await dropStorageByName(request, STORAGE);
  });

  test('made in the tab, used as ShareX uses it, deleted with its link', async ({ page, request, baseURL }) => {
    await loginAs(page);
    await page.goto('/admin/dashboard?settings=1');
    await expect(page.getByTestId('user-settings-dialog')).toBeVisible({ timeout: 15_000 });
    await page.getByTestId('user-settings-tab-uploaders').click();
    await expect(page.getByTestId('uploaders-panel')).toBeVisible();

    // No native select anywhere: the storage is the shared list.
    await expect(page.getByTestId('uploaders-panel').locator('select')).toHaveCount(0);
    await page.getByTestId('uploaders-storage').click();
    await page.getByTestId(`uploaders-storage-option-${STORAGE}`).click();
    await page.getByTestId('uploaders-name').fill('E2E capture');
    await page.getByTestId('uploaders-folder').fill('captures');

    // The answer's key is kept for the request below; the page shows it once.
    const created = page.waitForResponse((r) => r.url().endsWith('/api/auth/uploaders') && r.request().method() === 'POST');
    await page.getByTestId('uploaders-submit').click();
    const body = (await (await created).json()) as { key: string; setup: { sharex: Record<string, unknown> } };
    expect(body.key).toMatch(/^fxu_/);
    expect(body.setup.sharex.DestinationType).toBe('ImageUploader, TextUploader, FileUploader');

    const key = page.getByTestId('uploaders-key');
    await expect(key).toBeVisible();
    await expect(page.getByTestId('uploaders-key-value')).toHaveText(body.key);
    await expect(key).toBeInViewport();
    await expect(page.getByTestId('uploaders-download-sharex')).toBeVisible();

    // ── the request ShareX sends ──────────────────────────────────────────
    const up = await request.post('/api/uploads', {
      headers: { Authorization: `Bearer ${body.key}` },
      multipart: { file: { name: 'shot.png', mimeType: 'image/png', buffer: Buffer.from('\x89PNG\r\n\x1a\n-e2e', 'binary') } },
    });
    expect(up.status(), await up.text()).toBe(200);
    const ans = (await up.json()) as { url: string; deletion_url: string; thumbnail_url?: string };
    expect(ans.url).toMatch(/\/s\/[^/?]+\?inline=1$/);
    expect(ans.deletion_url).toMatch(/\/x\/[A-Za-z0-9_-]+$/);
    const local = (u: string) => new URL(u).pathname + new URL(u).search;

    const folder = path.join(storageRoot(MOUNT), 'captures');
    const stored = readdirSync(folder).filter((n) => n.endsWith('-shot.png'));
    expect(stored).toHaveLength(1);
    const opened = await request.get(local(ans.url));
    expect(opened.status()).toBe(200);
    expect(opened.headers()['content-disposition'] ?? '').toContain('inline');

    // ── the key opens nothing else ────────────────────────────────────────
    for (const p of ['/api/ai/files?path=' + STORAGE + '://', '/api/auth/me', '/api/files/manager?action=index&path=' + STORAGE + '://']) {
      const r = await request.get(p, { headers: { Authorization: `Bearer ${body.key}` } });
      expect(r.status(), p).toBe(401);
    }

    // ── the deletion link: opening it deletes nothing ─────────────────────
    const anon = await page.context().browser()!.newContext({ baseURL });
    const visitor = await anon.newPage();
    await visitor.goto(local(ans.deletion_url));
    await expect(visitor.getByTestId('uploaddel-name')).toHaveText(stored[0]);
    await visitor.reload();
    expect(existsSync(path.join(folder, stored[0]))).toBe(true);
    expect((await request.get(local(ans.url))).status()).toBe(200);

    await visitor.getByTestId('uploaddel-confirm').click();
    await expect(visitor.getByText('File deleted')).toBeVisible();
    expect(existsSync(path.join(folder, stored[0]))).toBe(false);
    expect((await request.get(local(ans.url))).status()).not.toBe(200);

    // Spent.
    await visitor.goto(local(ans.deletion_url));
    await expect(visitor.getByText('Link not valid')).toBeVisible();
    await anon.close();
  });
});
