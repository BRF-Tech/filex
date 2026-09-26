// Following a storage from the outside — a scan to its end, a deletion to the
// storage leaving the list — on the worker's own signal, with ONE poll of the
// storage list however many storages are followed (lib/storageWatch).
//
// ⚠ #66 (the admin's "Sync now", composables/useSyncNow) and #69 (the
// explorer's "Catalog everything", lib/catalogRun) followed the same scan two
// ways: #66 on the list's `running` flag and `last_sync_state`; #69 on the
// runs table, with heuristics to tell the new run from the old ones and a
// give-up after 60 s. And every storage #66 followed read the whole list
// again every 3 s on its own. #66's "still deleting" toast promised the
// storage would leave the list when done, and nothing read the list again.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { createStorageWatch, type WatchedStorageRow } from '@brftech/filex-core/src/lib/storageWatch';

/** Successive answers of GET /admin/storages; the last one repeats. `null`:
 *  the read failed. */
function server(answers: Array<WatchedStorageRow[] | null>) {
  const reads = { n: 0 };
  const list = vi.fn(async () => {
    reads.n++;
    const a = answers.length > 1 ? answers.shift()! : answers[0];
    if (!a) throw new Error('503');
    return a;
  });
  return { list, reads };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('a scan', () => {
  it('is followed until `running` drops, and says how it ended', async () => {
    const s = server([
      [{ id: 7, running: true }],
      [{ id: 7, running: true }],
      [{ id: 7, running: false, last_sync_state: 'failed', last_sync_error: 'bucket gone' }],
    ]);
    const watch = createStorageWatch({ list: s.list, pollMs: 3000 });
    const end = watch.scan(7);
    expect(watch.watching(7)).toBe('scan');
    await vi.advanceTimersByTimeAsync(9000);
    await expect(end).resolves.toEqual({ status: 'failed', error: 'bucket gone' });
    expect(watch.watching(7)).toBeNull();
  });

  it('reads `ok`, `aborted`, and the list’s spelling `error` as failed', async () => {
    for (const [state, status] of [['ok', 'ok'], ['aborted', 'aborted'], ['error', 'failed']] as const) {
      const s = server([[{ id: 1, running: false, last_sync_state: state }]]);
      const end = createStorageWatch({ list: s.list }).scan(1);
      await vi.advanceTimersByTimeAsync(3000);
      await expect(end).resolves.toMatchObject({ status });
    }
  });

  it('a storage that left the list mid-scan is `gone`', async () => {
    const s = server([[]]);
    const end = createStorageWatch({ list: s.list }).scan(3);
    await vi.advanceTimersByTimeAsync(3000);
    await expect(end).resolves.toEqual({ status: 'gone' });
  });

  it('keeps following through a failed read, and gives up only after many', async () => {
    const s = server([null, null, [{ id: 7, running: false, last_sync_state: 'ok' }]]);
    const end = createStorageWatch({ list: s.list, giveUpAfter: 3 }).scan(7);
    await vi.advanceTimersByTimeAsync(9000);
    await expect(end).resolves.toEqual({ status: 'ok' });

    const dead = server([null]);
    const lost = createStorageWatch({ list: dead.list, giveUpAfter: 3 }).scan(7);
    await vi.advanceTimersByTimeAsync(9000);
    await expect(lost).resolves.toEqual({ status: 'unknown' });
  });

  it('ends with null, silently, once the watch is stopped', async () => {
    const s = server([[{ id: 7, running: true }]]);
    const watch = createStorageWatch({ list: s.list });
    const end = watch.scan(7);
    watch.stop();
    await vi.advanceTimersByTimeAsync(30_000);
    await expect(end).resolves.toBeNull();
    expect(s.reads.n, 'a stopped watch read the list').toBe(0);
  });
});

describe('one poll for everything followed', () => {
  it('reads the list once per tick however many storages it follows', async () => {
    const s = server([[{ id: 1, running: true }, { id: 2, running: true }, { id: 3, running: true }]]);
    const watch = createStorageWatch({ list: s.list, pollMs: 3000 });
    void watch.scan(1);
    void watch.scan(2);
    void watch.deletion(3);
    await vi.advanceTimersByTimeAsync(3000);
    expect(s.reads.n).toBe(1);
    await vi.advanceTimersByTimeAsync(3000);
    expect(s.reads.n).toBe(2);
    watch.stop();
  });

  it('joins a storage already followed rather than following it twice', async () => {
    const s = server([[{ id: 1, running: true }], [{ id: 1, running: false, last_sync_state: 'ok' }]]);
    const watch = createStorageWatch({ list: s.list });
    const a = watch.scan(1);
    const b = watch.scan(1);
    await vi.advanceTimersByTimeAsync(6000);
    await expect(a).resolves.toEqual({ status: 'ok' });
    await expect(b).resolves.toEqual({ status: 'ok' });
    expect(s.reads.n).toBe(2);
  });

  it('stops reading once nothing is followed', async () => {
    const s = server([[{ id: 1, running: false, last_sync_state: 'ok' }]]);
    const watch = createStorageWatch({ list: s.list });
    await Promise.all([watch.scan(1), vi.advanceTimersByTimeAsync(3000)]);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(s.reads.n).toBe(1);
  });
});

describe('a deletion the server is still doing', () => {
  it('is followed until the storage leaves the list', async () => {
    const s = server([[{ id: 9 }], [{ id: 9 }], []]);
    const watch = createStorageWatch({ list: s.list });
    const done = watch.deletion(9);
    expect(watch.watching(9)).toBe('deletion');
    await vi.advanceTimersByTimeAsync(9000);
    await expect(done).resolves.toBe(true);
    expect(watch.watching(9)).toBeNull();
  });
});
