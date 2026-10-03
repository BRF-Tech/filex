// #86: encrypting a folder in place no longer leaves its files over 200 MB
// as plaintext. useE2eFiles.convertLarge reads such a file as a stream,
// encrypts it as a STREAM (header 0x02) file as it reads, and writes it over
// itself as a CONVERSION write: staged where the server has staged uploads
// (the commit carries `e2e_convert` and `expect`), a single POST gathered in
// memory where it has not - and nothing at all, resolving false, when that
// would be more than this browser may gather.

import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { createApp, defineComponent, h } from 'vue';

import { useE2eFiles } from '../../../packages/core/src/composables/useE2eFiles';
import type { UploadJob, UploadOptions } from '../../../packages/core/src/composables/useUploadChunked';
import { createEncryptedFolder, decryptFile } from '../../../packages/core/src/lib/e2ecrypto';
import { E2E_BLOB_SAVE_LIMIT } from '../../../packages/core/src/lib/e2esave';

const PW = 'correct horse battery';
const DIR = 's://Arşiv/Videolar';
const NAME = 'tatil.mp4';
const EXPECT = '3000:1700000000000';

function pattern(n: number): Uint8Array {
  const b = new Uint8Array(n);
  for (let i = 0; i < n; i++) b[i] = (i * 13 + 5) & 0xff;
  return b;
}

const unsupported = () => Object.assign(new Error('no staged uploads here'), { stagedUnsupported: true });

interface Harness {
  files: ReturnType<typeof useE2eFiles>;
  staged: UploadOptions[];
  stagedBytes: Uint8Array[];
  multipart: Array<{ dir: string; files: File[]; fields?: Record<string, string> }>;
  unmount(): void;
}

function mount(opts: { staged?: 'ok' | 'unsupported' | 'hang' } = {}): Harness {
  const staged: UploadOptions[] = [];
  const stagedBytes: Uint8Array[] = [];
  const multipart: Harness['multipart'] = [];
  const api = {
    authHeaders: async () => ({}),
    previewUrl: (p: string) => `http://filex.test/preview?path=${encodeURIComponent(p)}`,
    credentialsMode: () => 'same-origin',
    uploadMultipart: async (dir: string, list: File[], _p?: unknown, fields?: Record<string, string>) => {
      multipart.push({ dir, files: list, fields });
      return {};
    },
  };
  const chunked = {
    threshold: () => 1024,
    uploadFile: async (o: UploadOptions) => {
      staged.push(o);
      if (opts.staged === 'unsupported') throw unsupported();
      if (opts.staged === 'hang') {
        let cancelled = false;
        const job = { uploadedBytes: 0, cancel: () => (cancelled = true) } as unknown as UploadJob;
        o.onProgress?.(job);
        for (let i = 0; i < 200 && !cancelled; i++) await new Promise((r) => setTimeout(r, 5));
        throw new DOMException(cancelled ? 'Aborted by user' : 'never cancelled', 'AbortError');
      }
      const src = o.source!;
      const out = new Uint8Array(src.size);
      for (let at = 0; at < src.size; ) {
        const end = Math.min(at + 1024, src.size);
        const piece = new Uint8Array(await (await src.read(at, end)).arrayBuffer());
        out.set(piece, at);
        at = end;
      }
      stagedBytes.push(out);
      return { id: 'up-1' };
    },
  };
  let files!: ReturnType<typeof useE2eFiles>;
  const app = createApp(
    defineComponent({
      setup() {
        files = useE2eFiles({
          api: api as never,
          chunked: chunked as never,
          locale: () => 'en',
          t: (k: string) => k,
          toast: () => undefined,
          emitError: () => undefined,
          escrowPublicKey: () => null,
          registerOp: () => undefined,
          reload: async () => undefined,
          openPreview: () => undefined,
          showRecoveryKey: () => undefined,
        } as never);
        return () => h('div');
      },
    }),
  );
  app.mount(document.createElement('div'));
  return { files, staged, stagedBytes, multipart, unmount: () => app.unmount() };
}

describe('convertLarge', () => {
  let fmk: CryptoKey;
  const plain = pattern(3000);
  beforeAll(async () => {
    fmk = (await createEncryptedFolder(PW)).fmk;
  }, 30_000);
  afterEach(() => vi.unstubAllGlobals());

  function serve(body: Uint8Array) {
    const fetchMock = vi.fn(async () => new Response(body.slice()));
    vi.stubGlobal('fetch', fetchMock);
    return fetchMock;
  }

  const row = (size = plain.length) => ({ path: `${DIR}/${NAME}`, basename: NAME, size });

  it('staged: a 0x02 file over itself, the commit carrying e2e_convert and the precondition', async () => {
    const fetchMock = serve(plain);
    const h = mount();
    await expect(h.files.convertLarge(row(), fmk, EXPECT)).resolves.toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(h.staged).toHaveLength(1);
    expect(h.staged[0].path).toBe(DIR);
    expect(h.staged[0].file.name).toBe(NAME);
    expect(h.staged[0].commitQuery).toEqual({ e2e_convert: '1', expect: EXPECT });
    const stored = h.stagedBytes[0];
    expect(stored[8], 'STREAM, header version 0x02').toBe(2);
    expect(new Uint8Array(await decryptFile(fmk, stored.slice().buffer as ArrayBuffer))).toEqual(plain);
    expect(h.multipart).toHaveLength(0);
    h.unmount();
  }, 30_000);

  it('no staged path: one POST with the same fields, gathered in memory', async () => {
    serve(plain);
    const h = mount({ staged: 'unsupported' });
    await expect(h.files.convertLarge(row(), fmk, EXPECT)).resolves.toBe(true);
    expect(h.multipart).toHaveLength(1);
    expect(h.multipart[0].dir).toBe(DIR);
    expect(h.multipart[0].fields).toEqual({ e2e_convert: '1', expect: EXPECT });
    const sent = h.multipart[0].files[0];
    expect(sent.name).toBe(NAME);
    const bytes = new Uint8Array(await sent.arrayBuffer());
    expect(bytes[8]).toBe(2);
    expect(new Uint8Array(await decryptFile(fmk, bytes.slice().buffer as ArrayBuffer))).toEqual(plain);
    h.unmount();
  }, 30_000);

  it('no staged path and more than this browser may gather: false, and nothing written', async () => {
    serve(plain);
    const h = mount({ staged: 'unsupported' });
    // The size is what the listing said; nothing is read before the server
    // has answered, so the tiny body is never asked for.
    await expect(h.files.convertLarge(row(E2E_BLOB_SAVE_LIMIT + 1), fmk, EXPECT)).resolves.toBe(false);
    expect(h.multipart).toHaveLength(0);
    h.unmount();
  }, 30_000);

  it('Stop ends the upload in flight', async () => {
    serve(plain);
    const h = mount({ staged: 'hang' });
    const ctl = new AbortController();
    const run = h.files.convertLarge(row(), fmk, EXPECT, { signal: ctl.signal });
    await new Promise((r) => setTimeout(r, 20));
    ctl.abort();
    await expect(run).rejects.toThrow('Aborted by user');
    h.unmount();
  }, 30_000);

  it('a file whose size the listing did not give is not guessed at', async () => {
    serve(plain);
    const h = mount();
    await expect(
      h.files.convertLarge({ path: `${DIR}/${NAME}`, basename: NAME }, fmk, EXPECT),
    ).rejects.toThrow('unknown file size');
    expect(h.staged).toHaveLength(0);
    h.unmount();
  }, 30_000);
});
