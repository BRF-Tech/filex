// A working copy of "Open with filex" to the server and back, in parts
// (src/transfer.ts): the staged upload every filex client sends a large file
// with (docs/UPLOADS.md), resumed from the SERVER's offset after a dropped
// part, and ranged reads on the way back, each part the one asked for.
// The ceiling is the server's own upload limit, not 256 MB.
//
// Run:  node --experimental-strip-types --test desktop/test/transfer.test.ts

import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import * as tr from '../src/transfer.ts';
import { uploadLimitOf } from '../src/openwith.ts';

const api = tr as unknown as Record<string, any>;
const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const noSleep = async () => undefined;

function bytesOf(n: number, seed = 7): Buffer {
  const b = Buffer.alloc(n);
  for (let i = 0; i < n; i++) b[i] = (i * 31 + seed) & 0xff;
  return b;
}

/**
 * The server's half of the staged protocol, in memory: parts on the grid it
 * hands out, the contiguous offset (docs/UPLOADS.md → The offset contract),
 * and the faults a link has - a part whose answer is lost after it landed,
 * one that never lands, a refusal.
 */
class FakeStaged {
  chunk: number;
  total = 0;
  parts = new Map<number, Buffer>();
  begun: Record<string, unknown> | null = null;
  puts: Array<{ start: number; end: number; total: number }> = [];
  aborted = 0;
  committed = 0;
  opStates: string[] = ['ok'];
  beginStatus = 200;
  /** Per part start: what goes wrong the first time it is sent. */
  faults = new Map<number, 'lost-answer' | 'not-landed' | number>();
  /** Every put answers this status (a refusal), when set. */
  putStatus: number | null = null;
  /** Every put throws (a link that is down). */
  down = false;

  constructor(chunk: number) {
    this.chunk = chunk;
  }

  offset(): number {
    let at = 0;
    while (this.parts.has(at)) at += this.parts.get(at)!.length;
    return at;
  }

  assembled(): Buffer {
    return Buffer.concat([...this.parts.entries()].sort((a, b) => a[0] - b[0]).map((e) => e[1]));
  }

  transport(): tr.StagedTransport {
    return {
      begin: async (body) => {
        this.begun = body;
        this.total = body.size as number;
        if (this.beginStatus !== 200) return { status: this.beginStatus, json: { error: 'quota exceeded' } };
        return { status: 200, json: { id: 'u1', chunk_size: this.chunk, offset: 0, total_size: this.total } };
      },
      put: async (_id, start, end, total, body) => {
        this.puts.push({ start, end, total });
        if (this.down) throw new Error('ECONNRESET');
        if (this.putStatus) return { status: this.putStatus, json: { error: 'no' } };
        assert.equal(start % this.chunk, 0, 'a part starts on the grid the server handed out');
        assert.equal(body.length, end - start);
        const fault = this.faults.get(start);
        this.faults.delete(start);
        if (fault === 'not-landed') throw new Error('socket hang up');
        if (typeof fault === 'number') return { status: fault, json: { error: 'SHORT_CHUNK' } };
        this.parts.set(start, Buffer.from(body));
        if (fault === 'lost-answer') throw new Error('the answer was lost');
        return { status: 200, json: { offset: this.offset(), total_size: total, state: 'staging' } };
      },
      status: async () => ({ status: 200, json: { offset: this.offset(), state: 'staging' } }),
      commit: async () => {
        this.committed++;
        return { status: 202, json: { op_id: 42, node_id: 7, transfer_state: 'staged' } };
      },
      abort: async () => {
        this.aborted++;
      },
      op: async () => ({ status: 200, json: { status: this.opStates.shift() ?? 'ok', error: 'the storage is full' } }),
    };
  }
}

test('a large working copy goes in parts, on the grid the server hands out, and is waited for until stored', async () => {
  const s = new FakeStaged(4096);
  const bytes = bytesOf(20_000);
  const sha = crypto.createHash('sha256').update(bytes).digest('hex');
  const seen: number[] = [];
  const out = await api.uploadStaged(s.transport(), {
    dir: 'docs://.filex-open', name: 'a1b2c3d4e5f6-Logo.psd', bytes, sha256: sha, sleep: noSleep, onProgress: (n: number) => seen.push(n),
  });
  assert.deepEqual(s.assembled(), bytes, 'what the server holds is the file');
  assert.deepEqual(s.puts.map((p) => p.start), [0, 4096, 8192, 12288, 16384]);
  assert.ok(s.puts.every((p) => p.total === 20_000));
  assert.equal(s.begun?.hash, 'sha256:' + sha, 'the server checks the whole file at the commit');
  assert.equal(s.begun?.size, 20_000);
  assert.equal(s.begun?.chunk_size, tr.PART_BYTES, 'asked for; the server\'s answer binds');
  assert.equal(s.committed, 1);
  assert.deepEqual({ opId: out.opId, nodeId: out.nodeId }, { opId: 42, nodeId: 7 });
  assert.equal(seen.at(-1), 20_000);
  assert.equal(s.aborted, 0);
});

test('a part whose answer was lost is not sent again: the server\'s offset says where to go on', async () => {
  const s = new FakeStaged(4096);
  s.faults.set(4096, 'lost-answer');
  const bytes = bytesOf(16_384, 3);
  const out = await api.uploadStaged(s.transport(), { dir: 'docs://x', name: 'a.bin', bytes, sleep: noSleep });
  assert.deepEqual(s.assembled(), bytes);
  assert.equal(s.puts.filter((p) => p.start === 4096).length, 1, 'the part that landed is not sent twice');
  assert.ok(out.resumed >= 1, 'the server was asked');
});

test('a part that did not land is sent again, from where it starts - never the file from zero', async () => {
  const s = new FakeStaged(4096);
  s.faults.set(8192, 'not-landed');
  s.faults.set(12288, 400); // SHORT_CHUNK: the connection cut the body short
  const bytes = bytesOf(15_000, 9);
  await api.uploadStaged(s.transport(), { dir: 'docs://x', name: 'a.bin', bytes, sleep: noSleep });
  assert.deepEqual(s.assembled(), bytes);
  assert.deepEqual(s.puts.map((p) => p.start), [0, 4096, 8192, 8192, 12288, 12288]);
});

test('a refusal no retry changes ends the upload at once, and the server\'s staging is released', async () => {
  const s = new FakeStaged(4096);
  s.putStatus = 403;
  await assert.rejects(api.uploadStaged(s.transport(), { dir: 'docs://x', name: 'a.bin', bytes: bytesOf(10_000), sleep: noSleep }), (e: any) => e instanceof tr.TransferError && e.status === 403);
  assert.equal(s.puts.length, 1, 'not repeated');
  assert.equal(s.aborted, 1);

  const q = new FakeStaged(4096);
  q.beginStatus = 413;
  await assert.rejects(api.uploadStaged(q.transport(), { dir: 'docs://x', name: 'a.bin', bytes: bytesOf(10_000), sleep: noSleep }), /quota exceeded/);
  assert.equal(q.puts.length, 0);
});

test('a server without the staged path says so before a byte is sent', async () => {
  for (const status of [404, 501]) {
    const s = new FakeStaged(4096);
    s.beginStatus = status;
    await assert.rejects(api.uploadStaged(s.transport(), { dir: 'docs://x', name: 'a.bin', bytes: bytesOf(10_000), sleep: noSleep }), (e: any) => e.stagedUnsupported === true && e.status === status);
    assert.equal(s.puts.length, 0);
  }
});

test('a link that stays down: given up after failures in a row with nothing gained, and the staging released', async () => {
  const s = new FakeStaged(4096);
  s.down = true;
  await assert.rejects(api.uploadStaged(s.transport(), { dir: 'docs://x', name: 'a.bin', bytes: bytesOf(10_000), sleep: noSleep, maxFailures: 4 }), /stopped at 0 of 10000/);
  assert.equal(s.puts.length, 4);
  assert.equal(s.aborted, 1);
});

test('the transfer to the storage is waited for; one that failed is a failed upload, said as the server says it', async () => {
  const s = new FakeStaged(4096);
  s.opStates = ['running', 'running', 'ok'];
  await api.uploadStaged(s.transport(), { dir: 'docs://x', name: 'a.bin', bytes: bytesOf(9000), sleep: noSleep });
  assert.equal(s.opStates.length, 0, 'polled until ok');
  const f = new FakeStaged(4096);
  f.opStates = ['running', 'failed'];
  await assert.rejects(api.uploadStaged(f.transport(), { dir: 'docs://x', name: 'a.bin', bytes: bytesOf(9000), sleep: noSleep }), /could not store the upload: the storage is full/);
});

// ── the way back ─────────────────────────────────────────────────────

/** A file served with Range the way http.ServeContent serves it, with faults. */
function rangedServer(file: () => Buffer, o: { lastModified?: () => string; faults?: Array<'throw' | 'wrong' | 202 | 'ignore-range'> } = {}) {
  const asked: Array<{ range: string; ifRange?: string }> = [];
  const faults = [...(o.faults ?? [])];
  const get: tr.RangeTransport = async (range, ifRange) => {
    asked.push({ range, ifRange });
    const fault = faults.shift();
    const body = file();
    const lm = o.lastModified?.() ?? 'Fri, 16 Oct 2026 10:00:00 GMT';
    if (fault === 'throw') throw new Error('ECONNRESET');
    if (fault === 202) return { status: 202, body: Buffer.from('{"state":"preparing"}') };
    if (fault === 'ignore-range') return { status: 200, lastModified: lm, body };
    if (ifRange && ifRange !== lm) return { status: 200, lastModified: lm, body };
    if (body.length === 0) return { status: 416, contentRange: 'bytes */0', body: Buffer.alloc(0) };
    const m = /^bytes=(\d+)-(\d+)$/.exec(range)!;
    const start = Number(m[1]);
    const end = Math.min(Number(m[2]), body.length - 1);
    if (fault === 'wrong') return { status: 206, contentRange: `bytes ${start + 1}-${end}/${body.length}`, lastModified: lm, body: body.subarray(start + 1, end + 1) };
    return { status: 206, contentRange: `bytes ${start}-${end}/${body.length}`, lastModified: lm, body: body.subarray(start, end + 1) };
  };
  return { get, asked };
}

test('a saved version comes back in ranged parts, each the part asked for', async () => {
  const file = bytesOf(2500, 5);
  const s = rangedServer(() => file);
  const got = await api.downloadRanged(s.get, { chunkSize: 1000, sleep: noSleep });
  assert.deepEqual(got, file);
  assert.deepEqual(s.asked.map((a) => a.range), ['bytes=0-999', 'bytes=1000-1999', 'bytes=2000-2499']);
  assert.equal(s.asked[0]!.ifRange, undefined);
  assert.ok(s.asked.slice(1).every((a) => a.ifRange === 'Fri, 16 Oct 2026 10:00:00 GMT'), 'later parts hold the version the first said');
});

test('a part that fails, or is not the one asked for, is asked for again from where it starts', async () => {
  const file = bytesOf(3000, 1);
  const s = rangedServer(() => file, { faults: [undefined as never, 'throw', 'wrong', 202] });
  const got = await api.downloadRanged(s.get, { chunkSize: 1000, sleep: noSleep });
  assert.deepEqual(got, file);
  assert.deepEqual(s.asked.map((a) => a.range), ['bytes=0-999', 'bytes=1000-1999', 'bytes=1000-1999', 'bytes=1000-1999', 'bytes=1000-1999', 'bytes=2000-2999']);
});

test('a file saved again in the middle comes back whole - the new version, never spliced onto the old', async () => {
  let version = 1;
  const v1 = bytesOf(3000, 1);
  const v2 = bytesOf(3000, 2);
  const s = rangedServer(() => (version === 1 ? v1 : v2), { lastModified: () => (version === 1 ? 'Fri, 16 Oct 2026 10:00:00 GMT' : 'Fri, 16 Oct 2026 10:00:05 GMT') });
  const get: tr.RangeTransport = async (range, ifRange) => {
    const a = await s.get(range, ifRange);
    version = 2; // saved again after the first part
    return a;
  };
  const got = await api.downloadRanged(get, { chunkSize: 1000, sleep: noSleep });
  assert.deepEqual(got, v2);
});

test('an empty file, a server that ignores Range, and one more than this computer can hold', async () => {
  assert.equal((await api.downloadRanged(rangedServer(() => Buffer.alloc(0)).get, { sleep: noSleep })).length, 0);
  const file = bytesOf(5000);
  assert.deepEqual(await api.downloadRanged(rangedServer(() => file, { faults: ['ignore-range'] }).get, { chunkSize: 1000, sleep: noSleep }), file);
  await assert.rejects(api.downloadRanged(rangedServer(() => file).get, { chunkSize: 1000, maxBytes: 4000, sleep: noSleep }), /more than this computer can hold/);
  await assert.rejects(
    api.downloadRanged(async () => ({ status: 403, body: Buffer.from('{}') }), { sleep: noSleep }),
    (e: any) => e instanceof tr.TransferError && e.status === 403,
  );
  assert.deepEqual(api.parseContentRange('bytes 0-9/10'), { start: 0, end: 9, total: 10 });
  assert.equal(api.parseContentRange('bytes */0'), null);
});

// ── the ceiling ──────────────────────────────────────────────────────

test('the ceiling is the server\'s own upload limit, not 256 MB', () => {
  assert.equal(uploadLimitOf({ upload_limit_mb: 4096 }), 4096 * 1024 * 1024);
  assert.equal(uploadLimitOf({ upload_limit_mb: 0 }), 0, 'none');
  assert.equal(uploadLimitOf({}), 0);
  assert.equal(uploadLimitOf(null), 0);
  const io = fs.readFileSync(path.join(ROOT, 'src', 'openwith-io.ts'), 'utf8');
  assert.doesNotMatch(io, /256 \* 1024 \* 1024|MAX_DOCUMENT_BYTES/, 'no ceiling of the app\'s own');
  assert.match(io, /export async function sendWorkingCopy\(/);
  // The way up is sendDocument's (measured in openwith-stream.test.ts), read
  // from the file a part at a time; the way back pours into a spool.
  assert.match(io, /await sendDocument\(/);
  assert.match(io, /const src = await openDocument\(doc\.path, doc\.version\)/);
  assert.match(io, /await downloadRangedTo\(get, w\)/);
  assert.doesNotMatch(io, /downloadRanged\(get,|MAX_LENGTH/, 'no download into memory');
  // Every working copy goes up the one way.
  const main = fs.readFileSync(path.join(ROOT, 'src', 'main.ts'), 'utf8');
  assert.doesNotMatch(main, /\buploadFile\(/);
  assert.equal(
    main.match(/sendWorkingCopy\(ctx, dir, basename, \{ path: (?:localPath|live\.record\.localPath), version \}, \{ limitBytes: serverUploadLimit\(acc\.id\) \}\)/g)?.length,
    2,
    'the opening and an outside change\'s copy',
  );
});
