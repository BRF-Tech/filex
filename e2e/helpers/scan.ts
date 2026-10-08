/**
 * The storage scan, from a spec: start one, and hold the catalogue to what it
 * must keep while scans run.
 *
 * ⚠⚠ Issue #192. A queued folder rename running beside a scan of the same
 * storage dropped the folder's rows: the scan listed the folder's parent
 * before the rename moved the bytes and the folder itself after, read "not
 * found" as "empty", and judged everything inside gone. The renamed folder
 * opened empty, and its files came back on a later scan as new rows - other
 * ids, no shares, versions or comments. It was caught only under load (e2e
 * 159 and 172, WebKit, the 0.53 run), when the fsnotify scan two seconds
 * after a write happened to meet the rename.
 *
 * So a spec that renames or moves a folder does not wait for luck: it starts
 * a scan beside the change (`scanNow`) and then reads the folder, by row id,
 * for as long as the scans that follow run (`holdsThroughScans`).
 */
import { expect, type APIRequestContext } from '@playwright/test';

/** One catalogue entry as the server lists it: its row id and its name. */
export type Entry = [id: number, name: string];

/** What the catalogue lists in `storage://rel`, sorted by name. */
export async function cataloguedIn(request: APIRequestContext, storage: string, rel: string): Promise<Entry[]> {
  const res = await request.get(
    `/api/files/manager?action=index&path=${encodeURIComponent(`${storage}://${rel}`)}`,
  );
  if (!res.ok()) return [];
  const body = (await res.json()) as { files?: Array<{ id: number; basename: string }> };
  return (body.files ?? [])
    .map((f): Entry => [f.id, f.basename])
    .sort((a, b) => (a[1] < b[1] ? -1 : a[1] > b[1] ? 1 : 0));
}

/** The admin's "Scan now" for one storage: a full scan starts in the
 *  background (202 whether it started or one was already walking). */
export async function scanNow(request: APIRequestContext, storageId: number) {
  const res = await request.post(`/api/admin/storages/${storageId}/sync`);
  expect(res.status(), `scan storage ${storageId} now`).toBe(202);
}

/**
 * `storage://rel` lists exactly `want` - the same rows, by id - once the
 * change in flight has landed, and then on every read for `ms`, through a
 * scan started here and the one a local storage's own events start two
 * seconds after a change (fsnotify).
 */
export async function holdsThroughScans(
  request: APIRequestContext,
  storage: string,
  storageId: number,
  rel: string,
  want: Entry[],
  ms = 3_000,
) {
  await expect
    .poll(() => cataloguedIn(request, storage, rel), {
      message: `${rel} lists the rows it held before the change`,
      timeout: 15_000,
    })
    .toEqual(want);
  await scanNow(request, storageId);
  const until = Date.now() + ms;
  for (let read = 1; Date.now() < until; read++) {
    expect(
      await cataloguedIn(request, storage, rel),
      `read ${read} of ${rel} while the storage is scanned: a row was dropped or came back as another`,
    ).toEqual(want);
    await new Promise((r) => setTimeout(r, 150));
  }
}
