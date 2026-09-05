// The browser uploader, against a real implementation of the staged protocol.
//
// ⚠ The composable lives in @brftech/filex-core because every browser surface
// mounts the same component — the admin SPA, the desktop app's explorer and the
// work.example.com / fishapp embeds. The core package has no test runner of its own,
// so it is exercised here, in the app that ships it (same arrangement as
// tests/lib/shareCli.test.ts).
//
// What these assert is deliberately BYTES, not endpoints. "Did it resume?" is a
// question about how much crossed the wire; a test that only checked which URLs
// were called would pass while the product re-uploaded the whole file (see
// trap #7 in the handover: a share-limit test asserted the server *stored* 3
// while the link served 4).

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  useUploadChunked,
  isStagedUnsupported,
} from '@brftech/filex-core/src/composables/useUploadChunked';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';
import type { ExplorerConfig } from '@brftech/filex-core/src/types/ExplorerConfig';
import { FakeStagedServer, installXHR, memoryStorage } from './stagedServer';

const CHUNK = 4096;

function makeFile(bytes: Uint8Array, name = 'big.bin', lastModified = 1_700_000_000_000): File {
  return new File([bytes], name, { lastModified });
}

function pattern(n: number): Uint8Array {
  const b = new Uint8Array(n);
  for (let i = 0; i < n; i++) b[i] = (i * 31 + 7) & 0xff;
  return b;
}

function fakeApi(server: FakeStagedServer): FileApi {
  return {
    endpoints: {
      manager: 'https://filex.test/api/files/manager',
      uploadBegin: 'https://filex.test/api/files/upload/begin',
      opsShow: 'https://filex.test/api/files/ops/{id}',
    },
    jsonFetch: (url: string, init?: RequestInit) => server.json(url, init ?? {}),
    authHeadersSync: (extra: Record<string, string> = {}) => ({ Accept: 'application/json', ...extra }),
    credentialsMode: () => 'same-origin',
  } as unknown as FileApi;
}

const config: ExplorerConfig = { apiBase: 'https://filex.test', chunkSize: CHUNK };

describe('staged upload (browser)', () => {
  let server: FakeStagedServer;
  let restoreXHR: () => void;

  beforeEach(() => {
    server = new FakeStagedServer();
    restoreXHR = installXHR(server);
  });
  afterEach(() => restoreXHR());

  it('chunks a large file onto the staged protocol and commits it', async () => {
    const data = pattern(CHUNK * 2 + 1000);
    const store = memoryStorage();
    const up = useUploadChunked(config, fakeApi(server), store);

    expect(up.shouldChunk({ size: data.length })).toBe(true);
    // The small-file fast path is untouched: below the chunk size nothing goes
    // near this uploader.
    expect(up.shouldChunk({ size: 10 })).toBe(false);

    const res = await up.uploadFile({ path: 'main://docs', file: makeFile(data) });

    expect(server.beginCalls).toBe(1);
    expect(server.commitCalls).toBe(1);
    expect(server.putBytes).toEqual([CHUNK, CHUNK, 1000]);
    expect(res.node_id).toBe(7);

    const id = [...server.sessions.keys()][0];
    expect(Array.from(server.assembled(id))).toEqual(Array.from(data));
  });

  it('reports progress as filex acknowledges bytes, not as the socket drains', async () => {
    const data = pattern(CHUNK * 3);
    const seen: Array<{ status: string; uploaded: number }> = [];
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());

    await up.uploadFile({
      path: 'main://docs',
      file: makeFile(data),
      onProgress: (job) => seen.push({ status: job.status, uploaded: job.uploadedBytes }),
    });

    // Monotonic, capped at the file size, and ending on the commit phases.
    let last = -1;
    for (const s of seen) {
      expect(s.uploaded).toBeGreaterThanOrEqual(last);
      expect(s.uploaded).toBeLessThanOrEqual(data.length);
      last = s.uploaded;
    }
    expect(seen.map((s) => s.status)).toContain('committing');
    expect(seen[seen.length - 1]).toEqual({ status: 'done', uploaded: data.length });
  });

  it('resumes from the server offset when a chunk is cut mid-flight', async () => {
    const data = pattern(CHUNK * 3);
    // Chunk 3 arrives half-eaten: the header promises 4096 bytes, 900 land.
    // The server refuses it and does NOT move its offset.
    server.failChunkAt(CHUNK * 2, { kind: 'short', deliver: 900 });

    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    await up.uploadFile({ path: 'main://docs', file: makeFile(data) });

    const id = [...server.sessions.keys()][0];
    expect(Array.from(server.assembled(id))).toEqual(Array.from(data));
    // One begin, and the third chunk sent twice — NOT the whole file again.
    expect(server.beginCalls).toBe(1);
    expect(server.wireBytes).toBe(data.length + CHUNK);
    expect(server.wireBytes).toBeLessThan(data.length * 2);
  });

  it('bookmarks the session BEFORE the first chunk, not after it lands', async () => {
    // The window this closes: `begin` has answered, the id exists on the
    // server, and the very first chunk dies on the way out. A bookmark written
    // only once a chunk is acknowledged would not exist yet — and the next
    // visit would have no way to name the session it already owns, so the file
    // would start again from zero and the staging would sit there until the
    // sweeper took it. That is the exact failure resume exists to prevent, and
    // it is likeliest on precisely the flaky links that need resume.
    const data = pattern(CHUNK * 3);
    const store = memoryStorage();
    for (let i = 0; i < 4; i++) server.failChunkAt(0, { kind: 'network' });

    const up = useUploadChunked(config, fakeApi(server), store);
    await expect(up.uploadFile({ path: 'main://docs', file: makeFile(data) })).rejects.toThrow();

    // Not one byte was accepted…
    expect(server.putBytes).toEqual([]);
    const id = [...server.sessions.keys()][0];
    expect(server.offset(server.sessions.get(id)!)).toBe(0);

    // …and the session is still recoverable by name, from offset zero.
    const bookmark = up.resumableFor('main://docs', makeFile(data));
    expect(bookmark?.uploadId).toBe(id);
    expect(bookmark?.offset).toBe(0);
  });

  it('continues the same session after a reload — no second begin, every byte once', async () => {
    const data = pattern(CHUNK * 4);
    const store = memoryStorage();
    const file = makeFile(data);

    // ── first visit: dies during chunk 3 ──
    server.failChunkAt(CHUNK * 2, { kind: 'status', code: 500 });
    server.failChunkAt(CHUNK * 2, { kind: 'status', code: 500 });
    server.failChunkAt(CHUNK * 2, { kind: 'status', code: 500 });
    server.failChunkAt(CHUNK * 2, { kind: 'status', code: 500 });
    const first = useUploadChunked(config, fakeApi(server), store);
    await expect(first.uploadFile({ path: 'main://docs', file })).rejects.toThrow();

    const id = [...server.sessions.keys()][0];
    expect(server.offset(server.sessions.get(id)!)).toBe(CHUNK * 2);
    // The bookmark is what survives the reload; the File itself cannot.
    expect(store._map.size).toBe(1);

    // ── second visit: a brand-new composable, same storage, same file ──
    const wireAfterFirst = server.wireBytes;
    const second = useUploadChunked(config, fakeApi(server), store);

    const pending = second.resumableFor('main://docs', file);
    expect(pending?.uploadId).toBe(id);
    expect(pending?.offset).toBe(CHUNK * 2);

    let resumedFrom = -1;
    await second.uploadFile({
      path: 'main://docs',
      file,
      onProgress: (job) => {
        if (job.resumedFrom != null) resumedFrom = job.resumedFrom;
      },
    });

    expect(resumedFrom).toBe(CHUNK * 2);
    expect(server.beginCalls).toBe(1); // ← the whole point: no new session
    expect(server.wireBytes - wireAfterFirst).toBe(CHUNK * 2); // only the tail
    expect(Array.from(server.assembled(id))).toEqual(Array.from(data));
    // Finished uploads leave no bookmark behind.
    expect(store._map.size === 0 || JSON.parse(store._map.get('filex:uploads:v1')!)).toEqual(
      store._map.size === 0 ? true : {},
    );
  });

  it('refuses to inherit a session when the file changed under it', async () => {
    const data = pattern(CHUNK * 3);
    const store = memoryStorage();
    server.failChunkAt(CHUNK * 2, { kind: 'status', code: 500 });
    server.failChunkAt(CHUNK * 2, { kind: 'status', code: 500 });
    server.failChunkAt(CHUNK * 2, { kind: 'status', code: 500 });
    server.failChunkAt(CHUNK * 2, { kind: 'status', code: 500 });
    const up = useUploadChunked(config, fakeApi(server), store);
    await expect(
      up.uploadFile({ path: 'main://docs', file: makeFile(data, 'x.bin', 1000) }),
    ).rejects.toThrow();

    // Same name and size, edited since. Splicing its tail onto the old head is
    // the one way this design can corrupt a file, so the session is not reused.
    const edited = makeFile(pattern(CHUNK * 3), 'x.bin', 2000);
    expect(up.resumableFor('main://docs', edited)).toBeNull();
    await up.uploadFile({ path: 'main://docs', file: edited });
    expect(server.beginCalls).toBe(2);
  });

  it('abort cancels in flight, deletes the staging and forgets the bookmark', async () => {
    const data = pattern(CHUNK * 6);
    const store = memoryStorage();
    const up = useUploadChunked(config, fakeApi(server), store);

    let job: { cancel(): void } | null = null;
    const p = up.uploadFile({
      path: 'main://docs',
      file: makeFile(data),
      onProgress: (j) => {
        job = j;
        if (j.uploadedBytes >= CHUNK) j.cancel();
      },
    });
    await expect(p).rejects.toThrow();
    expect(job).not.toBeNull();

    expect(server.abortCalls).toBe(1);
    expect(server.sessions.size).toBe(0);
    expect(store._map.get('filex:uploads:v1') ?? '{}').toBe('{}');
  });

  it('keeps the staging (and the bookmark) when an upload FAILS', async () => {
    // The difference from abort, and it matters: a failure is meant to be
    // resumable, so nothing is released.
    const data = pattern(CHUNK * 3);
    const store = memoryStorage();
    for (let i = 0; i < 4; i++) server.failChunkAt(CHUNK, { kind: 'status', code: 500 });
    const up = useUploadChunked(config, fakeApi(server), store);
    await expect(up.uploadFile({ path: 'main://docs', file: makeFile(data) })).rejects.toThrow();

    expect(server.abortCalls).toBe(0);
    expect(server.sessions.size).toBe(1);
    expect(up.resumableFor('main://docs', makeFile(data))).not.toBeNull();
  });

  it('does not retry a real refusal as a whole-file POST', async () => {
    // 413 QUOTA_EXCEEDED at begin is a refusal on the merits. Marking it
    // "unsupported" would send the user's 4 GB down the single-POST path to be
    // refused again, slowly.
    server.unsupported = 413;
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    const err = await up
      .uploadFile({ path: 'main://docs', file: makeFile(pattern(CHUNK * 2)) })
      .then(() => null)
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(Error);
    expect(isStagedUnsupported(err)).toBe(false);
  });

  it('flags a server with no staged path so the caller may fall back', async () => {
    server.unsupported = 404;
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    const err = await up
      .uploadFile({ path: 'main://docs', file: makeFile(pattern(CHUNK * 2)) })
      .then(() => null)
      .catch((e: unknown) => e);
    expect(isStagedUnsupported(err)).toBe(true);
  });

  it('waits for the backend transfer when asked to', async () => {
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    const states: string[] = [];
    await up.uploadFile({
      path: 'main://docs',
      file: makeFile(pattern(CHUNK * 2)),
      waitForTransfer: true,
      onProgress: (j) => states.push(j.status),
    });
    expect(states).toContain('transferring');
  });

  // Issue #16. The commit is answered 202 before the bytes are written to the
  // storage, so "done" on the 202 is a claim the server has not made yet. The
  // reporter's uploads all reported success and none of them existed.
  it('waits for the transfer by default, without being asked', async () => {
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    const states: string[] = [];
    await up.uploadFile({
      path: 'main://docs',
      file: makeFile(pattern(CHUNK * 2)),
      onProgress: (j) => states.push(j.status),
    });
    expect(states).toContain('transferring');
    expect(server.opCalls).toBeGreaterThan(0);
  });

  it('fails the upload when the backend transfer fails', async () => {
    server.opStatus = 'failed';
    server.opError = 'upload part 1: request stream is not seekable';
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    const states: string[] = [];
    const err = await up
      .uploadFile({
        path: 'main://docs',
        file: makeFile(pattern(CHUNK * 2)),
        onProgress: (j) => states.push(j.status),
      })
      .then(() => null)
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(Error);
    expect((err as Error).message).toContain('not seekable');
    expect(states).toContain('error');
    expect(states).not.toContain('done');
  });

  it('opts out of the wait when told to', async () => {
    server.opStatus = 'failed';
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    const states: string[] = [];
    await up.uploadFile({
      path: 'main://docs',
      file: makeFile(pattern(CHUNK * 2)),
      waitForTransfer: false,
      onProgress: (j) => states.push(j.status),
    });
    expect(states).not.toContain('transferring');
    expect(states).toContain('done');
  });

  // ⚠ An op row we cannot read at all is unknowable, not failed. Waiting on it
  // for ever would park the upload in `transferring` and be a worse bug than
  // the one the wait exists to fix.
  it('gives up waiting rather than hanging when the op row is unreadable', async () => {
    server.opUnreadable = true;
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    const states: string[] = [];
    await up.uploadFile({
      path: 'main://docs',
      file: makeFile(pattern(CHUNK * 2)),
      onProgress: (j) => states.push(j.status),
    });
    expect(states).toContain('done');
    expect(server.opCalls).toBeLessThanOrEqual(8);
  }, 20_000);
});

describe('staged upload — resume bookmarks', () => {
  beforeEach(() => vi.useRealTimers());

  it('discards a bookmark older than the server keeps its staging', async () => {
    const { saveResume, loadResume, RESUME_TTL_MS } = await import(
      '@brftech/filex-core/src/lib/uploadResume'
    );
    const store = memoryStorage();
    const now = 1_000_000_000;
    saveResume(
      store,
      'k',
      { uploadId: 'up-1', path: 'main://', name: 'a.bin', size: 10, lastModified: 1, chunkSize: 4, offset: 4 },
      now,
    );
    expect(loadResume(store, 'k', now + 60_000)?.uploadId).toBe('up-1');
    // Past the TTL the staging is gone server-side; a "resuming…" that
    // immediately restarts is worse than an honest fresh upload.
    expect(loadResume(store, 'k', now + RESUME_TTL_MS + 1)).toBeNull();
  });

  it('fingerprints on name, size and mtime together', async () => {
    const { uploadFingerprint } = await import('@brftech/filex-core/src/lib/uploadResume');
    const base = { name: 'a.bin', size: 10, lastModified: 1 };
    expect(uploadFingerprint('main://', base)).toBe(uploadFingerprint('main://', { ...base }));
    expect(uploadFingerprint('main://', base)).not.toBe(
      uploadFingerprint('main://', { ...base, lastModified: 2 }),
    );
    expect(uploadFingerprint('main://', base)).not.toBe(
      uploadFingerprint('other://', base),
    );
  });
});
