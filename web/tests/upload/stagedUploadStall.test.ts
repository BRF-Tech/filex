import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useUploadChunked, type UploadJob } from '@brftech/filex-core/src/composables/useUploadChunked';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { FakeStagedServer, memoryStorage } from './stagedServer';

const CHUNK = 4096;
const IDLE = 120_000;

describe('staged upload compatibility and inactivity recovery', () => {
  let server: FakeStagedServer;
  let requests: ProbeXHR[];
  let behavior: (xhr: ProbeXHR, body: Blob, index: number) => Promise<void>;
  const bytes = Uint8Array.from({ length: CHUNK * 2 + 31 }, (_, i) => i % 251);
  const file = () => new File([bytes], 'test.bin', { lastModified: 123 });
  const store = () => memoryStorage();
  const api = () => ({
    endpoints: { manager: '/api/files/manager', uploadBegin: '/api/files/upload/begin' },
    jsonFetch: (url: string, init?: RequestInit) => server.json(url, init),
    authHeadersSync: (headers: Record<string, string>) => headers,
    credentialsMode: () => 'same-origin',
  }) as unknown as FileApi;
  const uploader = (storage = store()) => useUploadChunked({ chunkSize: CHUNK }, api(), storage);

  class ProbeXHR {
    upload = { onprogress: null as ((e: { loaded: number; lengthComputable: boolean }) => void) | null };
    onprogress: ((e: { loaded: number }) => void) | null = null;
    onload: (() => void) | null = null;
    onerror: (() => void) | null = null;
    onabort: (() => void) | null = null;
    status = 0;
    responseText = '';
    aborted = false;
    url = '';
    headers: Record<string, string> = {};
    open(_method: string, url: string) { this.url = url; }
    setRequestHeader(k: string, v: string) { this.headers[k] = v; }
    abort() { this.aborted = true; this.onabort?.(); }
    send(body: Blob) {
      requests.push(this);
      void behavior(this, body, requests.length - 1);
    }
    async accept(body: Blob, finish = true) {
      const data = new Uint8Array(await body.arrayBuffer());
      this.upload.onprogress?.({ loaded: data.length, lengthComputable: true });
      if (this.aborted) return;
      const out = server.putChunk(this.url.split('/').pop()!, this.headers['Content-Range'], data);
      this.status = 200;
      this.responseText = JSON.stringify(out);
      this.onprogress?.({ loaded: this.responseText.length });
      if (finish) this.onload?.();
    }
  }

  beforeEach(() => {
    vi.useFakeTimers();
    server = new FakeStagedServer();
    requests = [];
    behavior = (xhr, body) => xhr.accept(body);
    vi.stubGlobal('XMLHttpRequest', ProbeXHR);
  });
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

  it('materializes one slice at a time and preserves every byte', async () => {
    const f = file();
    const slices: Blob[] = [];
    const slice = f.slice.bind(f);
    vi.spyOn(f, 'slice').mockImplementation((start, end) => {
      // The prior chunk must be acknowledged before the next disk read.
      expect(server.putBytes.length).toBe(slices.length);
      const part = slice(start, end);
      expect(part.size).toBeLessThanOrEqual(CHUNK);
      slices.push(part);
      return part;
    });
    behavior = async (xhr, body, index) => {
      expect(body).toBeInstanceOf(Blob);
      expect(body).not.toBe(slices[index]);
      await xhr.accept(body);
    };
    await uploader().uploadFile({ path: 'main://docs', file: f });
    expect(server.assembled('up-1')).toEqual(bytes);
    expect(server.putBytes).toEqual([CHUNK, CHUNK, 31]);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('reconciles a complete 200 response without load, without resending accepted bytes', async () => {
    behavior = (xhr, body, i) => xhr.accept(body, i !== 0);
    const result = uploader().uploadFile({ path: 'main://docs', file: file() });
    await vi.advanceTimersByTimeAsync(IDLE + 1000);
    await result;
    expect(requests[0].aborted).toBe(true);
    expect(server.statusCalls).toBe(1);
    expect(server.wireBytes).toBe(bytes.length);
    expect(server.assembled('up-1')).toEqual(bytes);
    expect(server.commitCalls).toBe(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('retries an unreceived chunk rather than trusting upload progress', async () => {
    behavior = async (xhr, body, i) => {
      if (i === 0) xhr.upload.onprogress?.({ loaded: body.size, lengthComputable: true });
      else await xhr.accept(body);
    };
    const result = uploader().uploadFile({ path: 'main://docs', file: file() });
    await vi.advanceTimersByTimeAsync(IDLE + 1000);
    await result;
    expect(requests[1].headers['Content-Range']).toBe(requests[0].headers['Content-Range']);
    expect(server.assembled('up-1')).toEqual(bytes);
  });

  it('allows a slow upload with continuing byte progress beyond the idle interval', async () => {
    let finish!: () => Promise<void>;
    behavior = async (xhr, body, i) => {
      if (i === 0) finish = () => xhr.accept(body);
      else await xhr.accept(body);
    };
    const result = uploader().uploadFile({ path: 'main://docs', file: file() });
    await vi.advanceTimersByTimeAsync(0);
    for (let n = 1; n <= 4; n++) {
      await vi.advanceTimersByTimeAsync(60_000);
      requests[0].upload.onprogress?.({ loaded: n * 500, lengthComputable: true });
    }
    expect(requests[0].aborted).toBe(false);
    await finish();
    await result;
    expect(server.statusCalls).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('does not let duplicate progress events keep a stalled request alive', async () => {
    behavior = async () => {};
    const result = uploader().uploadFile({ path: 'main://docs', file: file() }).catch(e => e);
    await vi.advanceTimersByTimeAsync(0);
    requests[0].upload.onprogress?.({ loaded: 10, lengthComputable: true });
    await vi.advanceTimersByTimeAsync(IDLE - 1);
    requests[0].upload.onprogress?.({ loaded: 10, lengthComputable: true });
    await vi.advanceTimersByTimeAsync(1);
    expect(requests[0].aborted).toBe(true);
    await vi.runAllTimersAsync();
    expect(await result).toBeInstanceOf(Error);
    expect(requests).toHaveLength(4);
    expect(server.commitCalls).toBe(0);
    expect(server.abortCalls).toBe(0); // failure remains resumable
    expect(vi.getTimerCount()).toBe(0);
  });

  it('cancels a pending disk read without sending or committing it', async () => {
    const f = file();
    const part = f.slice(0, CHUNK);
    let completeRead!: (buffer: ArrayBuffer) => void;
    vi.spyOn(f, 'slice').mockReturnValue(part);
    vi.spyOn(part, 'arrayBuffer').mockReturnValue(new Promise(resolve => { completeRead = resolve; }));
    let job!: UploadJob;
    const result = uploader().uploadFile({ path: 'main://docs', file: f, onProgress: j => { job = j; } })
      .catch(e => e);
    await vi.advanceTimersByTimeAsync(0);
    job.cancel();
    completeRead(new ArrayBuffer(CHUNK));
    expect((await result).name).toBe('AbortError');
    expect(requests).toHaveLength(0);
    expect(server.abortCalls).toBe(1);
    expect(server.commitCalls).toBe(0);
  });

  it('user cancellation clears the watchdog and never enters retry', async () => {
    behavior = async () => {};
    let job!: UploadJob;
    const result = uploader().uploadFile({ path: 'main://docs', file: file(), onProgress: j => { job = j; } })
      .catch(e => e);
    await vi.advanceTimersByTimeAsync(0);
    job.cancel();
    expect((await result).name).toBe('AbortError');
    await vi.runAllTimersAsync();
    expect(requests).toHaveLength(1);
    expect(server.abortCalls).toBe(1);
    expect(server.statusCalls).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('does not send a retry when cancelled during the offset lookup', async () => {
    behavior = async () => {};
    const client = api();
    const json = client.jsonFetch;
    let finishLookup!: () => void;
    client.jsonFetch = ((url: string, init?: RequestInit) => {
      if (!init?.method && url.endsWith('/up-1')) {
        return new Promise(resolve => { finishLookup = () => resolve({ offset: 0 }); });
      }
      return json(url, init);
    }) as typeof client.jsonFetch;
    let job!: UploadJob;
    const result = useUploadChunked({ chunkSize: CHUNK }, client, store()).uploadFile({
      path: 'main://docs', file: file(), onProgress: j => { job = j; },
    }).catch(e => e);
    await vi.advanceTimersByTimeAsync(IDLE + 1000);
    job.cancel();
    finishLookup();
    expect((await result).name).toBe('AbortError');
    expect(requests).toHaveLength(1);
    expect(server.abortCalls).toBe(1);
    expect(server.commitCalls).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });
});
