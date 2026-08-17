/**
 * Share — admin list surface + public viewer, end to end.
 *
 * 77-share pins the share API. This spec's distinct job is the ADMIN LIST
 * (Shares.vue) and the public entry point a recipient actually hits.
 *
 * ⚠ What was here before could not fail. It navigated to /admin/shares,
 * counted `tbody tr` immediately after `goto` and skipped when the count was
 * 0 — i.e. it skipped whenever the table had not finished loading, and
 * otherwise depended on whatever shares an earlier spec happened to leave
 * behind. When rows did exist it read `getByTestId('share-token')`, which
 * existed nowhere in web/src, so the only two outcomes were "skip" and
 * "time out". Its final assertion accepted `[200, 401]`, which no broken
 * build could violate.
 *
 * It now seeds its own file, mints its own PIN-protected share, and asserts
 * the whole path: the row shows up in the admin table with the right token
 * and a PIN badge, the metadata endpoint declares the PIN, and the public
 * URL answers with the unlock form instead of leaking the bytes.
 */
import { test, expect } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STORAGE = `e2e-share40-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const FILE_NAME = 'pinned.txt';
const FILE_BODY = 'forty-share-bytes';

test.describe('Share — create, public access with PIN', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('admin creates a share with PIN, public viewer accepts it', async ({
    page,
    playwright,
    baseURL,
    browser,
  }) => {
    const api = await newAuthedRequest(playwright, baseURL ?? '');

    const up = await api.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name: FILE_NAME, mimeType: 'text/plain', buffer: Buffer.from(FILE_BODY) },
      },
    });
    expect(up.ok(), `upload status ${up.status()}`).toBeTruthy();

    const made = await api.post('/api/files/share', {
      data: { path: `${STORAGE}://${FILE_NAME}`, password: true },
    });
    expect(made.ok(), `share status ${made.status()}`).toBeTruthy();
    const { share } = await made.json();
    const token: string = share.token;
    expect(token).toBeTruthy();

    // ── the admin list renders the share ──────────────────────────────────
    await loginAs(page);
    await page.goto('/admin/shares');
    const row = page.locator('tbody tr', { has: page.locator(`[data-token="${token}"]`) });
    await expect(row).toHaveCount(1, { timeout: 10_000 });
    // The cell shows a truncated token — enough to recognise the share,
    // never the whole secret.
    await expect(row.getByTestId('share-token')).toHaveText(new RegExp(`^${token.slice(0, 10)}`));
    await expect(row.getByText('PIN', { exact: true })).toBeVisible();

    // ── the public entry point is PIN-gated ───────────────────────────────
    const ctx = await browser.newContext();
    const meta = await ctx.request.get(`/api/files/share/${token}`);
    expect(meta.status()).toBe(200);
    expect((await meta.json()).requires_pin, 'metadata must declare the PIN').toBe(true);

    const pub = await ctx.request.get(`/s/${token}`);
    expect(pub.status()).toBe(200);
    const html = await pub.text();
    expect(html.toLowerCase(), 'an un-PINned visitor gets the form').toContain('<form');
    expect(html, 'and must NOT get the file').not.toContain(FILE_BODY);
    await ctx.close();

    await api.delete(`/api/files/share/${share.id}`).catch(() => undefined);
    await api.dispose();
  });
});
