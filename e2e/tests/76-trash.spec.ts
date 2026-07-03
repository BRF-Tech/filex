/**
 * Trash flows — soft-delete → list → restore → purge.
 *
 * Guards the trash wiring landed in 87cf497 ("massive parity round 1").
 * The endpoints existed as orphaned handlers before that commit and
 * were never registered on the chi router, so every call was a 404.
 * The behavioural change at the same time: vfDelete now RENAMES the
 * file to `.filex-trash/<unix>-<rand>__<base>` instead of hard-
 * deleting via the driver, and stashes the original path in
 * `storage_key` so trash.Service.Restore can put it back.
 *
 * Cases covered (all API-driven, no UI):
 *   1.  Seed a fresh local storage and upload `disposable.txt`.
 *   2.  Soft-delete via POST /api/files/manager?action=delete.
 *   3.  GET /api/files/manager/trash → returns the row, `path` is the
 *       ORIGINAL location (storage_key, not the trash key).
 *   4.  POST /api/files/manager/restore with {node_id} → 200.
 *   5.  GET listing → file is back at its original path.
 *   6.  Soft-delete again, then admin DELETE /api/admin/trash/{id}.
 *   7.  Trash list is now empty.
 *   8.  Cleanup: drop the storage row.
 *
 * Endpoints exercised:
 *   - GET    /api/files/manager/trash
 *   - POST   /api/files/manager/restore
 *   - DELETE /api/admin/trash/{id}
 *   - POST   /api/admin/trash/empty           (sanity ping; full purge
 *                                               not exercised here to
 *                                               keep the suite scoped)
 */
import { test as base, expect, type APIRequestContext } from '@playwright/test';
import {
  dropStorageByName,
  seedLocalStorage,
  newAuthedRequest,
} from '../helpers/seed';

const STORAGE = `e2e-trash-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const FILE_NAME = 'disposable.txt';
const FILE_BODY = 'goodbye-cruel-world';

// Per-test request fixture pre-injected with a Bearer token. Pattern
// mirrors 80-file-types.spec.ts — the cookies set by apiLogin in a
// beforeAll don't survive into the per-test `request` fixture.
const test = base.extend<{ authedRequest: APIRequestContext }>({
  authedRequest: async ({ playwright, baseURL }, use) => {
    const ctx = await newAuthedRequest(playwright, baseURL ?? '');
    await use(ctx);
    await ctx.dispose();
  },
});

test.describe('Trash — soft-delete + restore + admin purge', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('soft-delete moves the file into the trash listing', async ({ authedRequest: request }) => {
    // Upload first.
    const upRes = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': {
          name: FILE_NAME,
          mimeType: 'text/plain',
          buffer: Buffer.from(FILE_BODY),
        },
      },
    });
    expect(upRes.ok(), `upload status ${upRes.status()}`).toBeTruthy();

    // Soft-delete.
    const delRes = await request.post('/api/files/manager?action=delete', {
      data: {
        path: `${STORAGE}://`,
        items: [{ path: `${STORAGE}://${FILE_NAME}` }],
      },
    });
    expect(delRes.ok(), `delete status ${delRes.status()}`).toBeTruthy();

    // Listing the parent should NO LONGER show the file.
    const lsRes = await request.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`,
    );
    expect(lsRes.ok()).toBeTruthy();
    const ls = await lsRes.json();
    const liveBasenames = (ls.files ?? []).map((f: { basename: string }) => f.basename);
    expect(liveBasenames).not.toContain(FILE_NAME);

    // Trash listing should show it, with the ORIGINAL path (storage_key).
    const storageRow = await getStorageRowByName(request, STORAGE);
    expect(storageRow, `storage row for ${STORAGE}`).toBeTruthy();
    const trashRes = await request.get(
      `/api/files/manager/trash?storage_id=${storageRow!.id}`,
    );
    expect(trashRes.ok(), `trash status ${trashRes.status()}`).toBeTruthy();
    const trashBody: {
      entries: Array<{ id: number; name: string; path: string; storage_id: number }>;
    } = await trashRes.json();
    expect(Array.isArray(trashBody.entries)).toBeTruthy();

    const entry = (trashBody.entries ?? []).find((e) => e.name === FILE_NAME);
    expect(entry, `trash entry for ${FILE_NAME}`).toBeTruthy();
    // `path` here is the ORIGINAL pre-delete location. Server stores it
    // in `storage_key` and the projection prefers it over the trash
    // bucket key — that's the whole point of the contract.
    expect(entry!.path).toMatch(new RegExp(`/?${FILE_NAME}$`));
    expect(entry!.path).not.toContain('.filex-trash');
    expect(entry!.storage_id).toBe(storageRow!.id);
  });

  test('restore lifts deleted_at and the file shows up at its original path', async ({
    authedRequest: request,
  }) => {
    // Find the trashed node id from the previous test.
    const storageRow = await getStorageRowByName(request, STORAGE);
    const trashRes = await request.get(
      `/api/files/manager/trash?storage_id=${storageRow!.id}`,
    );
    expect(trashRes.ok()).toBeTruthy();
    const trashBody: { entries: Array<{ id: number; name: string }> } = await trashRes.json();
    const entry = (trashBody.entries ?? []).find((e) => e.name === FILE_NAME);
    expect(entry, 'trashed entry from prior step').toBeTruthy();

    const restoreRes = await request.post('/api/files/manager/restore', {
      data: { node_id: entry!.id },
    });
    expect(restoreRes.ok(), `restore status ${restoreRes.status()}`).toBeTruthy();
    const restored = await restoreRes.json();
    expect(restored.ok).toBe(true);

    // The file should be back at its original path.
    const lsRes = await request.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`,
    );
    expect(lsRes.ok()).toBeTruthy();
    const ls = await lsRes.json();
    const names = (ls.files ?? []).map((f: { basename: string }) => f.basename);
    expect(names, `listing after restore: ${JSON.stringify(names)}`).toContain(FILE_NAME);
  });

  test('admin DELETE /api/admin/trash/{id} hard-deletes a single trashed row', async ({
    authedRequest: request,
  }) => {
    // Re-soft-delete so we have something to purge.
    const delRes = await request.post('/api/files/manager?action=delete', {
      data: {
        path: `${STORAGE}://`,
        items: [{ path: `${STORAGE}://${FILE_NAME}` }],
      },
    });
    expect(delRes.ok()).toBeTruthy();

    const storageRow = await getStorageRowByName(request, STORAGE);
    const trashRes = await request.get(
      `/api/files/manager/trash?storage_id=${storageRow!.id}`,
    );
    expect(trashRes.ok()).toBeTruthy();
    const trashBody: { entries: Array<{ id: number; name: string }> } = await trashRes.json();
    const entry = (trashBody.entries ?? []).find((e) => e.name === FILE_NAME);
    expect(entry).toBeTruthy();

    const purgeRes = await request.delete(`/api/admin/trash/${entry!.id}`);
    expect(purgeRes.ok(), `purge status ${purgeRes.status()}`).toBeTruthy();
    const purgeBody = await purgeRes.json();
    expect(purgeBody.ok).toBe(true);

    // Trash should now be empty for this storage.
    const after = await request.get(
      `/api/files/manager/trash?storage_id=${storageRow!.id}`,
    );
    expect(after.ok()).toBeTruthy();
    const afterBody: { entries: Array<{ id: number; name: string }>; total?: number } =
      await after.json();
    const stillThere = (afterBody.entries ?? []).find((e) => e.name === FILE_NAME);
    expect(stillThere, `${FILE_NAME} should be hard-deleted`).toBeFalsy();
  });

  test('admin POST /api/admin/trash/empty responds 200 (sanity)', async ({
    authedRequest: request,
  }) => {
    // The admin endpoint accepts {storage_id, older_than_days}. We pass
    // a far-future cutoff so this run doesn't touch anyone else's data
    // — purpose is to confirm wiring, not exhaustive purge behaviour.
    const res = await request.post('/api/admin/trash/empty', {
      data: { older_than_days: 36500 }, // 100 years — effectively a no-op
    });
    expect(res.ok(), `empty status ${res.status()}`).toBeTruthy();
    const body: { ok: boolean; purged: number; failed: number; scanned: number } =
      await res.json();
    expect(body.ok).toBe(true);
    expect(typeof body.purged).toBe('number');
  });
});

// Helper: look up the storage row by name. We need its `id` to scope
// trash listings; the seed helper returns the row but we don't thread
// it through the test scope (each test's `request` fixture is fresh).
async function getStorageRowByName(
  request: APIRequestContext,
  name: string,
): Promise<{ id: number; name: string } | null> {
  const list = await request.get('/api/admin/storages');
  if (!list.ok()) return null;
  const items: Array<{ id: number; name: string }> = await list.json();
  return items.find((s) => s.name === name) ?? null;
}

