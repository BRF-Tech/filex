// A staged upload whose bytes are MADE as they are sent (wiring:e2 stream):
// a file encrypted in the browser — a `.fxe`, a STREAM file over 200 MB in an
// encrypted folder, a password change re-sending a body — goes up through
// `useUploadChunked` with a `source` instead of a File's bytes. Measured
// against the same fake staged server as stagedUpload.test.ts, in BYTES.

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { useUploadChunked, isStagedUnsupported } from '@brftech/filex-core/src/composables/useUploadChunked';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';
import type { ExplorerConfig } from '@brftech/filex-core/src/types/ExplorerConfig';
import { streamUploadSource, streamToFile } from '@brftech/filex-core/src/lib/uploadSource';
import { bytesStream } from '@brftech/filex-core/src/lib/e2estream';
import { FakeStagedServer, installXHR, memoryStorage } from './stagedServer';

const CHUNK = 4096;
const config: ExplorerConfig = { apiBase: 'https://filex.test', chunkSize: CHUNK };

function pattern(n: number): Uint8Array {
  const b = new Uint8Array(n);
  for (let i = 0; i < n; i++) b[i] = (i * 17 + 3) & 0xff;
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

describe('staged upload from a stream source', () => {
  let server: FakeStagedServer;
  let restoreXHR: () => void;

  beforeEach(() => {
    server = new FakeStagedServer();
    restoreXHR = installXHR(server);
  });
  afterEach(() => restoreXHR());

  it('sends exactly the bytes the stream makes, at the size the source announced', async () => {
    const data = pattern(CHUNK * 3 + 777);
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    const seen: number[] = [];
    await up.uploadFile({
      path: 'main://docs',
      file: new File([], 'x.fxe'),
      source: streamUploadSource(bytesStream(data, 1000), data.length),
      onProgress: (job) => seen.push(job.totalBytes),
    });
    const id = [...server.sessions.keys()][0];
    expect(Array.from(server.assembled(id))).toEqual(Array.from(data));
    expect(server.putBytes).toEqual([CHUNK, CHUNK, CHUNK, 777]);
    expect(new Set(seen)).toEqual(new Set([data.length]));
  });

  it('a chunk cut mid-flight is re-sent from what the source kept — not re-read from a stream that has moved on', async () => {
    const data = pattern(CHUNK * 3);
    server.failChunkAt(CHUNK, { kind: 'short', deliver: 900 });
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    await up.uploadFile({
      path: 'main://docs',
      file: new File([], 'x.fxe'),
      source: streamUploadSource(bytesStream(data, 333), data.length),
    });
    const id = [...server.sessions.keys()][0];
    expect(Array.from(server.assembled(id))).toEqual(Array.from(data));
    expect(server.wireBytes).toBe(data.length + CHUNK);
  });

  it('is never bookmarked: its bytes are gone with the tab, a resume could not rebuild them', async () => {
    const data = pattern(CHUNK * 2 + 5);
    const store = memoryStorage();
    const up = useUploadChunked(config, fakeApi(server), store);
    await up.uploadFile({
      path: 'main://docs',
      file: new File([], 'x.fxe'),
      source: streamUploadSource(bytesStream(data), data.length),
    });
    expect(store._map.size).toBe(0);
  });

  it('a failed sourced upload releases its staging and stops the source', async () => {
    const data = pattern(CHUNK * 3);
    for (let i = 0; i < 4; i++) server.failChunkAt(CHUNK, { kind: 'network' });
    const store = memoryStorage();
    const up = useUploadChunked(config, fakeApi(server), store);
    let cancelled = false;
    const src = streamUploadSource(bytesStream(data), data.length);
    const cancel = src.cancel!;
    src.cancel = () => {
      cancelled = true;
      cancel();
    };
    await expect(up.uploadFile({ path: 'main://docs', file: new File([], 'x.fxe'), source: src })).rejects.toThrow();
    expect(cancelled).toBe(true);
    expect(store._map.size).toBe(0);
  });

  it('with no staged path, the source is left untouched so the caller can send it the other way', async () => {
    server.unsupported = 501;
    const data = pattern(CHUNK * 2);
    const stream = bytesStream(data);
    const up = useUploadChunked(config, fakeApi(server), memoryStorage());
    const err = await up
      .uploadFile({ path: 'main://docs', file: new File([], 'x.fxe'), source: streamUploadSource(stream, data.length) })
      .then(() => null)
      .catch((e: unknown) => e);
    expect(isStagedUnsupported(err)).toBe(true);
    // Nothing was read and nothing cancelled: the whole stream is still there.
    const whole = await streamToFile(stream, 'x.fxe');
    expect(new Uint8Array(await whole.arrayBuffer())).toEqual(data);
  });
});
