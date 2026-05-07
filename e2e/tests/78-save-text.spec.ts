/**
 * Save-text endpoint — Monaco editor write-back.
 *
 * Guards `/api/files/save-text` (introduced in 87cf497). The SFC's code
 * editor + markdown viewer post the in-memory buffer back here on save;
 * the server resolves the storage from the adapter prefix, writes
 * through the Driver, and refreshes the cache row's metadata so the
 * next listing carries the new size.
 *
 * The whitelist (handler isTextSafePath) blocks binary extensions —
 * tested with `.bin` returning 415 Unsupported Media Type.
 *
 * Cases covered:
 *   1.  Seed a local storage and upload `note.md` with body "v1".
 *   2.  POST /api/files/save-text with new content "v2" → 200 +
 *       `{ok: true, size: 2}`.
 *   3.  GET preview → body is now "v2".
 *   4.  Negative: POST save-text on a `.bin` file → 415.
 *   5.  Cleanup: drop the storage.
 */
import { test as base, expect, type APIRequestContext } from '@playwright/test';
import { dropStorageByName, seedLocalStorage, newAuthedRequest } from '../helpers/seed';

const STORAGE = `e2e-savetext-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const NOTE_NAME = 'note.md';
const NOTE_V1 = 'v1';
const NOTE_V2 = 'v2';
const BIN_NAME = 'thing.bin';

const test = base.extend<{ authedRequest: APIRequestContext }>({
  authedRequest: async ({ playwright, baseURL }, use) => {
    const ctx = await newAuthedRequest(playwright, baseURL ?? '');
    await use(ctx);
    await ctx.dispose();
  },
});

test.describe('Save-text — Monaco editor write-back', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('uploads note.md "v1" then save-text rewrites it to "v2"', async ({
    authedRequest: request,
  }) => {
    // Seed initial body.
    const upRes = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': {
          name: NOTE_NAME,
          mimeType: 'text/markdown',
          buffer: Buffer.from(NOTE_V1),
        },
      },
    });
    expect(upRes.ok(), `upload status ${upRes.status()}`).toBeTruthy();

    // Save the new content via the editor endpoint.
    const saveRes = await request.post('/api/files/save-text', {
      data: {
        path: `${STORAGE}://${NOTE_NAME}`,
        content: NOTE_V2,
      },
    });
    expect(saveRes.ok(), `save-text status ${saveRes.status()}`).toBeTruthy();
    const saveBody: { ok: boolean; size: number } = await saveRes.json();
    expect(saveBody.ok).toBe(true);
    expect(saveBody.size).toBe(NOTE_V2.length);

    // Read it back via preview to confirm bytes hit storage.
    const prevRes = await request.get(
      `/api/files/manager?action=preview&path=${encodeURIComponent(
        `${STORAGE}://${NOTE_NAME}`,
      )}`,
    );
    expect(prevRes.status()).toBe(200);
    const text = await prevRes.text();
    expect(text).toBe(NOTE_V2);
  });

  test('save-text on a binary extension is rejected with 415', async ({
    authedRequest: request,
  }) => {
    // Upload an arbitrary `.bin` so the path resolves; the whitelist
    // check happens before storage I/O so the file body is irrelevant.
    const upRes = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': {
          name: BIN_NAME,
          mimeType: 'application/octet-stream',
          buffer: Buffer.from([0x00, 0x01, 0x02, 0x03]),
        },
      },
    });
    expect(upRes.ok()).toBeTruthy();

    const saveRes = await request.post('/api/files/save-text', {
      data: {
        path: `${STORAGE}://${BIN_NAME}`,
        content: 'cannot-write-this-as-text',
      },
    });
    expect(
      saveRes.status(),
      `expected 415 for .bin save-text; got ${saveRes.status()}: ${await saveRes.text()}`,
    ).toBe(415);
  });

  test('save-text without path returns 400', async ({ authedRequest: request }) => {
    const res = await request.post('/api/files/save-text', {
      data: { content: 'no path' },
    });
    expect(res.status(), `bad-path status ${res.status()}`).toBe(400);
  });
});
