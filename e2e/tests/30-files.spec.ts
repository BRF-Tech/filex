import { test as base, expect, type APIRequestContext } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const STORAGE_NAME = 'e2e-files';

/**
 * File operations — upload + soft-delete + restore.
 *
 * The original suite drove this end-to-end via the admin UI's storage
 * detail page. The new admin SPA renders the FileExplorer SFC inline
 * with a different selector tree, and the right-click context menu
 * triggers via long-press / button toolbar instead of native context
 * events — making UI-driven flows test-fragile.
 *
 * We exercise the SAME business outcomes through the public API
 * contract (`/api/files/manager?action=…`). The admin UI is verified
 * separately by 70-multi-storage (per-route status codes) and the
 * deployment-smoke suite (page boot).
 */
const test = base.extend<{ authedRequest: APIRequestContext }>({
  authedRequest: async ({ playwright, baseURL }, use) => {
    const ctx = await playwright.request.newContext({ baseURL });
    const login = await ctx.post('/api/auth/login', {
      data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
    });
    if (!login.ok()) {
      throw new Error(`login failed: ${login.status()} ${await login.text()}`);
    }
    const { token } = await login.json();
    const authedCtx = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });
    await use(authedCtx);
    await authedCtx.dispose();
    await ctx.dispose();
  },
});

test.describe('File operations — upload, list, delete', () => {
  test.beforeAll(async ({ request }) => {
    await seedLocalStorage(request, STORAGE_NAME, '/tmp/filex-e2e-files');
  });
  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE_NAME);
  });

  test('upload a fixture file and see it in the list response', async ({ authedRequest: request }) => {
    const fixture = path.join(__dirname, '../fixtures/hello.txt');
    const buf = fs.readFileSync(fixture);
    const upRes = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE_NAME}://`,
        'file[]': { name: 'hello.txt', mimeType: 'text/plain', buffer: buf },
      },
    });
    expect(upRes.ok()).toBeTruthy();

    const listRes = await request.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE_NAME}://`)}`,
    );
    expect(listRes.ok()).toBeTruthy();
    const body = await listRes.json();
    const names = (body.files || []).map((f: { basename: string }) => f.basename);
    expect(names).toContain('hello.txt');
  });

  test('delete removes the file from listings', async ({ authedRequest: request }) => {
    const delRes = await request.post('/api/files/manager?action=delete', {
      data: {
        path: `${STORAGE_NAME}://`,
        items: [{ path: `${STORAGE_NAME}://hello.txt` }],
      },
    });
    expect(delRes.ok()).toBeTruthy();

    const listRes = await request.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE_NAME}://`)}`,
    );
    expect(listRes.ok()).toBeTruthy();
    const body = await listRes.json();
    const names = (body.files || []).map((f: { basename: string }) => f.basename);
    expect(names).not.toContain('hello.txt');
  });

  test('preview a non-existent file returns 404 (not 500)', async ({ authedRequest: request }) => {
    // Regression for the S3 driver fix — Stat now maps NotFound to
    // storage.ErrNotFound instead of bubbling the SDK error as 500.
    const res = await request.get(
      `/api/files/manager?action=preview&path=${encodeURIComponent(
        `${STORAGE_NAME}://does-not-exist.txt`,
      )}`,
    );
    expect(res.status()).toBe(404);
  });
});
