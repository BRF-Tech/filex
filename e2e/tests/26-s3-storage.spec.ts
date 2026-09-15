/**
 * An S3 backend, end to end — against a real S3 server, not a fake.
 *
 * `node e2e/run.mjs local --s3` starts MinIO in Docker, registers an `s3`
 * storage through the admin API and sets `E2E_S3_STORAGE`. Without that the
 * whole file skips, loudly enough to see in the report: a green run that
 * silently omitted S3 is worse than a red one.
 *
 * What is actually worth proving here — the things a local-disk run cannot:
 *
 *   1. A file written through filex lands in the bucket and comes back byte
 *      for byte. Every other assertion is meaningless if this is not true.
 *   2. Ranged reads go out as an S3 `Range:` request rather than a full
 *      download that filex then trims. The assertion is the byte count and
 *      the content, because both look identical from the status code.
 *   3. **Re-chunking.** A staged upload's part boundaries are the client's;
 *      S3 requires every non-final multipart part to be at least 5 MiB. A
 *      client sending small chunks must therefore not be able to produce an
 *      upload the backend rejects — this is the one path where the staging
 *      layer and the driver have to disagree about part size and still work.
 *   4. Deleting goes to trash on S3 too, where "rename" is a copy and a
 *      delete rather than an atomic operation.
 *   5. **A move between two S3 storages of a file past 8 MiB** (issue #27). The harness's
 *      MinIO is plaintext http://, which is what makes this worth a real
 *      server: over http the SDK signs the payload hash and must rewind the
 *      body, and a cross-storage move hands it the source's stream, which
 *      cannot rewind (a local source is a file, which can — measured: the
 *      same move from local disk passed on the unfixed build). It failed with "failed to compute payload hash … request
 *      stream is not seekable" before a byte left the process.
 */
import { test as base, expect, type APIRequestContext } from '@playwright/test';
import { createHash } from 'node:crypto';
import { newAuthedRequest, waitForOp } from '../helpers/seed';

const STORAGE = process.env.E2E_S3_STORAGE ?? '';
/** A second s3 storage on the same MinIO, for moves between object stores. */
const STORAGE_B = process.env.E2E_S3_STORAGE_B ?? '';
/** 12 MiB: more than two 5 MiB backend parts, so re-chunking is exercised. */
const BIG = 12 * 1024 * 1024;
/** Deliberately far below S3's 5 MiB floor. */
const CLIENT_CHUNK = 512 * 1024;

const test = base.extend<{ authedRequest: APIRequestContext }>({
  authedRequest: async ({ playwright, baseURL }, use) => {
    const ctx = await newAuthedRequest(playwright, baseURL ?? '');
    await use(ctx);
    await ctx.dispose();
  },
});

function payload(n: number): Buffer {
  const b = Buffer.alloc(n);
  for (let i = 0; i < n; i++) b[i] = (i * 131 + 17) & 0xff;
  return b;
}

const sha256 = (b: Buffer | Uint8Array) => createHash('sha256').update(b).digest('hex');

/**
 * Push `data` into `dir` through the staged upload (begin → chunks → commit)
 * and wait for the commit's op. Returns the chunk size the server granted.
 */
async function stagedUpload(
  request: APIRequestContext,
  dir: string,
  name: string,
  data: Buffer,
  chunkSize: number,
): Promise<number> {
  const begin = await request.post('/api/files/upload/begin', {
    data: {
      path: dir,
      name,
      size: data.length,
      mime: 'application/octet-stream',
      hash: `sha256:${sha256(data)}`,
      chunk_size: chunkSize,
    },
  });
  expect(begin.ok(), `begin: ${begin.status()} ${await begin.text()}`).toBeTruthy();
  const { id, chunk_size: chunk } = await begin.json();

  for (let start = 0; start < data.length; start += chunk) {
    const end = Math.min(start + chunk, data.length) - 1;
    const put = await request.put(`/api/files/upload/${id}`, {
      headers: {
        'content-range': `bytes ${start}-${end}/${data.length}`,
        'content-type': 'application/octet-stream',
      },
      data: data.subarray(start, end + 1),
    });
    expect(put.ok(), `chunk at ${start}: ${put.status()} ${await put.text()}`).toBeTruthy();
  }

  const commit = await request.post(`/api/files/upload/${id}/commit`);
  expect(commit.status(), 'commit acknowledges before the bytes have moved').toBe(202);
  const { op_id: opId } = await commit.json();
  await waitForOp(request, opId, 120_000);
  return chunk;
}

test.describe('s3 storage', () => {
  test.skip(!STORAGE, 'no S3 backend — run with `node e2e/run.mjs local --s3`');

  test('a small file round-trips through the bucket unchanged', async ({
    authedRequest: request,
  }) => {
    const data = payload(64 * 1024);
    const name = `s3-roundtrip-${Date.now()}.bin`;

    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        file: { name, mimeType: 'application/octet-stream', buffer: data },
      },
    });
    expect(up.ok(), `upload: ${up.status()} ${await up.text()}`).toBeTruthy();

    const list = await request.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`,
    );
    expect(list.ok()).toBeTruthy();
    const body = await list.json();
    // The listing projects `basename` (see projectFileNodes); `name` is not a
    // field it emits, so the old map produced [undefined] and the assertion
    // could only ever fail — it was reporting a broken S3 round-trip on a
    // bucket that had the object.
    const names = (body.files ?? body.data?.files ?? []).map(
      (f: { basename?: string; name?: string }) => f.basename ?? f.name,
    );
    expect(names, 'the uploaded object must appear in the bucket listing').toContain(name);

    const dl = await request.get(
      `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://${name}`)}`,
    );
    expect(dl.ok()).toBeTruthy();
    expect(sha256(await dl.body())).toBe(sha256(data));
  });

  test('a ranged read fetches only the range', async ({ authedRequest: request }) => {
    const data = payload(256 * 1024);
    const name = `s3-range-${Date.now()}.bin`;
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        file: { name, mimeType: 'application/octet-stream', buffer: data },
      },
    });
    expect(up.ok()).toBeTruthy();

    const url = `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://${name}`)}`;
    const res = await request.get(url, { headers: { Range: 'bytes=1000-1099' } });

    expect(res.status(), 'a ranged request must be answered 206, not 200').toBe(206);
    const got = await res.body();
    expect(got.length, 'exactly the requested window').toBe(100);
    expect(Buffer.from(got).equals(data.subarray(1000, 1100))).toBeTruthy();
    expect(res.headers()['content-range']).toBe(`bytes 1000-1099/${data.length}`);
  });

  test('small client chunks are re-chunked to sizes S3 accepts', async ({
    authedRequest: request,
  }) => {
    const data = payload(BIG);
    const name = `s3-staged-${Date.now()}.bin`;

    const chunk = await stagedUpload(request, `${STORAGE}://`, name, data, CLIENT_CHUNK);
    expect(chunk, 'the server decides the chunk size and must honour ours here').toBe(CLIENT_CHUNK);

    // The proof is the object in the bucket, not the op's status: an upload
    // rejected by S3 for a too-small part fails here and nowhere else.
    const dl = await request.get(
      `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://${name}`)}`,
    );
    expect(dl.ok(), `download after commit: ${dl.status()}`).toBeTruthy();
    const back = await dl.body();
    expect(back.length).toBe(BIG);
    expect(sha256(back), 'what S3 holds must be what we sent').toBe(sha256(data));
  });

  test('deleting an object puts it in the trash', async ({ authedRequest: request }) => {
    const data = payload(4096);
    const name = `s3-trash-${Date.now()}.bin`;
    await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        file: { name, mimeType: 'application/octet-stream', buffer: data },
      },
    });

    const del = await request.post('/api/files/manager?action=delete', {
      data: { items: [{ path: `${STORAGE}://${name}` }] },
    });
    expect(del.ok(), `delete: ${del.status()} ${await del.text()}`).toBeTruthy();

    // ⚠ The trash listing lives at /api/files/manager/trash and is scoped by
    // storage (76-trash uses the same route). `/api/files/trash` has never
    // existed — the request 404'd and the assertion below never got to run.
    const storages = await request.get('/api/admin/storages');
    expect(storages.ok()).toBeTruthy();
    const row = ((await storages.json()) as Array<{ id: number; name: string }>).find(
      (st) => st.name === STORAGE,
    );
    expect(row, `storage row for ${STORAGE}`).toBeTruthy();

    const trash = await request.get(`/api/files/manager/trash?storage_id=${row!.id}`);
    expect(trash.ok(), `trash: ${trash.status()} ${await trash.text()}`).toBeTruthy();
    const items = await trash.json();
    const found = (items.entries ?? items.items ?? items.data ?? items).some?.(
      (t: { name?: string; path?: string }) => t.name === name || t.path?.endsWith(name),
    );
    expect(found, 'an S3 delete must be recoverable, not permanent').toBeTruthy();
  });

  test('after a move both folders show their new size without waiting for a sync (issue #27)', async ({
    authedRequest: request,
  }) => {
    test.skip(!STORAGE_B, 'the harness registered only one s3 storage');
    const stamp = Date.now();
    const srcDir = `sizes-src-${stamp}`;
    const dstDir = `sizes-dst-${stamp}`;
    const name = `sized-${stamp}.bin`;
    const data = payload(300 * 1024);

    for (const [storage, dir] of [[STORAGE_B, srcDir], [STORAGE, dstDir]] as const) {
      const mk = await request.post('/api/files/manager?action=newfolder', {
        data: { path: `${storage}://`, name: dir },
      });
      expect(mk.ok(), `mkdir ${dir}: ${mk.status()} ${await mk.text()}`).toBeTruthy();
    }
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE_B}://${srcDir}`,
        file: { name, mimeType: 'application/octet-stream', buffer: data },
      },
    });
    expect(up.ok(), `upload: ${up.status()} ${await up.text()}`).toBeTruthy();

    const sizeOf = async (storage: string, dir: string): Promise<number | undefined> => {
      const list = await request.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${storage}://`)}`);
      expect(list.ok()).toBeTruthy();
      const body = await list.json();
      const row = (body.files ?? body.data?.files ?? []).find(
        (f: { basename?: string; name?: string }) => (f.basename ?? f.name) === dir,
      );
      return row ? Number(row.size ?? row.file_size ?? 0) : undefined;
    };
    // The refresh is debounced (2 s quiet, 15 s ceiling); a sync pass is far
    // further away, so 12 s tells the two apart.
    const expectSize = async (storage: string, dir: string, want: number, why: string) => {
      await expect.poll(() => sizeOf(storage, dir), { timeout: 12_000, intervals: [500], message: why }).toBe(want);
    };

    await expectSize(STORAGE_B, srcDir, data.length, 'the upload counts toward its folder');

    const mv = await request.post('/api/files/move', {
      data: { source: [`${STORAGE_B}://${srcDir}/${name}`], target: `${STORAGE}://${dstDir}` },
    });
    expect(mv.status(), `move: ${mv.status()} ${await mv.text()}`).toBe(202);
    const { op } = (await mv.json()) as { op: { id: number } };
    const final = await waitForOp(request, op.id, 60_000);
    expect(final.status, `the move ended ${final.status}: ${final.error ?? ''}`).toBe('ok');

    await expectSize(STORAGE_B, srcDir, 0, 'the source folder lost the file');
    await expectSize(STORAGE, dstDir, data.length, 'the destination folder gained it');
  });

  test('a file past 8 MiB moves from one S3 storage onto another (issue #27)', async ({
    authedRequest: request,
  }) => {
    test.skip(!STORAGE_B, 'the harness registered only one s3 storage');
    const data = payload(20 * 1024 * 1024 + 12345);
    const name = `s3-move-${Date.now()}.mp4`;

    await stagedUpload(request, `${STORAGE_B}://`, name, data, 8 * 1024 * 1024);

    const mv = await request.post('/api/files/move', {
      data: { source: [`${STORAGE_B}://${name}`], target: `${STORAGE}://` },
    });
    expect(mv.status(), `move: ${mv.status()} ${await mv.text()}`).toBe(202);
    const { op } = (await mv.json()) as { op: { id: number } };
    const final = await waitForOp(request, op.id, 120_000);
    expect(final.status, `the move ended ${final.status}: ${final.error ?? ''}`).toBe('ok');

    const dl = await request.get(
      `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://${name}`)}`,
    );
    expect(dl.ok(), `download from S3: ${dl.status()}`).toBeTruthy();
    const back = await dl.body();
    expect(back.length).toBe(data.length);
    expect(sha256(back), 'what S3 holds must be what was moved').toBe(sha256(data));
  });
});
