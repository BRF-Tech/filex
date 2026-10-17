// "Open with filex" moves a working copy without holding it whole
// (src/spool.ts, src/transfer.ts, openwith.ts DocumentReader): a save comes
// home into a spool on disk, hashed as it lands, and goes from there over the
// document (or beside it) through one fixed buffer; the document goes up read
// a part at a time and held to the version filex hashed - saved again while
// it goes up, nothing is committed. A file of 8 MiB or less goes in one
// request, its bytes checked against that version first.
//
// The memory measurements read `process.memoryUsage().arrayBuffers` (where
// Buffers live) after a garbage collection at every part: what is still held
// then is what the code holds. A control read of the whole file shows the
// instrument sees a whole-file read. The size is FILEX_STREAM_TEST_MB
// (default 96).
//
// Run:  node --experimental-strip-types --test desktop/test/openwith-stream.test.ts

import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import v8 from 'node:v8';
import vm from 'node:vm';

import * as ow from '../src/openwith.ts';
import * as sp from '../src/spool.ts';
import * as tr from '../src/transfer.ts';

const MiB = 1024 * 1024;
const BIG = Math.max(24, Number(process.env.FILEX_STREAM_TEST_MB ?? 96)) * MiB;
/** What streaming may hold at once: a few parts and buffers, nowhere near BIG. */
const HELD_CEILING = 40 * MiB;
const noSleep = async () => undefined;
const sha = (b: Uint8Array | string) => crypto.createHash('sha256').update(b).digest('hex');

v8.setFlagsFromString('--expose-gc');
const gc = vm.runInNewContext('gc') as () => void;

function tmp(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'filex-stream-'));
}

/**
 * Gives the file a modification time in whole seconds, and gives it back
 * after a write: a time every file system and every conversion keeps exactly
 * (a fractional one does not survive utimes), so a save can keep it as a
 * two-second file system would.
 */
function pinTime(p: string, t = Math.floor(Date.now() / 1000) - 100): number {
  fs.utimesSync(p, t, t);
  return t;
}

/** Byte `i` of a synthetic file: computed, so a server can serve any range of a large file without holding it. */
function byteAt(i: number, seed: number): number {
  return (Math.imul(i ^ (i >>> 13), 2654435761) + seed) >>> 24;
}

function fill(buf: Buffer, at: number, seed: number): Buffer {
  for (let k = 0; k < buf.length; k++) buf[k] = byteAt(at + k, seed);
  return buf;
}

/** A synthetic file of `size` bytes on disk, written a MiB at a time; its sha256. */
function writeSynthetic(p: string, size: number, seed = 1): string {
  const h = crypto.createHash('sha256');
  const fd = fs.openSync(p, 'w');
  try {
    const buf = Buffer.allocUnsafe(MiB);
    for (let at = 0; at < size; at += MiB) {
      const part = fill(buf.subarray(0, Math.min(MiB, size - at)), at, seed);
      h.update(part);
      fs.writeSync(fd, part, 0, part.length, at);
    }
  } finally {
    fs.closeSync(fd);
  }
  return h.digest('hex');
}

function syntheticSha(size: number, seed = 1): string {
  const h = crypto.createHash('sha256');
  const buf = Buffer.allocUnsafe(MiB);
  for (let at = 0; at < size; at += MiB) h.update(fill(buf.subarray(0, Math.min(MiB, size - at)), at, seed));
  return h.digest('hex');
}

/** Peak arrayBuffers above the baseline, sampled after a gc at each `sample()`. */
function meter() {
  gc();
  const base = process.memoryUsage().arrayBuffers;
  let peak = 0;
  return {
    sample() {
      gc();
      peak = Math.max(peak, process.memoryUsage().arrayBuffers - base);
    },
    get peak() {
      return peak;
    },
  };
}

/** What a whole-file read holds, measured as the code under test is (the control). */
async function heldByWholeRead(p: string): Promise<number> {
  const m = meter();
  const whole = await fs.promises.readFile(p);
  m.sample();
  assert.ok(whole.length > 0);
  return m.peak;
}

/** Records the largest read and write any FileHandle makes, and refuses a whole-file read. */
function watchIo() {
  const seen = { maxRead: 0, maxWrite: 0, wholeReads: 0 };
  let handleProto: any = null;
  const saved: Record<string, any> = {};
  return {
    seen,
    async start() {
      const fh = await fs.promises.open(process.execPath, 'r');
      handleProto = Object.getPrototypeOf(fh);
      await fh.close();
      saved.read = handleProto.read;
      saved.write = handleProto.write;
      saved.readFile = handleProto.readFile;
      saved.fsReadFile = fs.promises.readFile;
      handleProto.read = function (this: unknown, buf: Buffer, off?: number, len?: number, ...rest: unknown[]) {
        const n = typeof len === 'number' ? len : buf.length;
        seen.maxRead = Math.max(seen.maxRead, n);
        return saved.read.call(this, buf, off, len, ...rest);
      };
      handleProto.write = function (this: unknown, data: Uint8Array, off?: number, len?: number, ...rest: unknown[]) {
        const n = typeof len === 'number' ? len : data.byteLength;
        seen.maxWrite = Math.max(seen.maxWrite, n);
        return saved.write.call(this, data, off, len, ...rest);
      };
      handleProto.readFile = function (this: unknown, ...args: unknown[]) {
        seen.wholeReads++;
        return saved.readFile.apply(this, args);
      };
      (fs.promises as any).readFile = (...args: unknown[]) => {
        seen.wholeReads++;
        return saved.fsReadFile.apply(fs.promises, args);
      };
    },
    stop() {
      if (!handleProto) return;
      handleProto.read = saved.read;
      handleProto.write = saved.write;
      handleProto.readFile = saved.readFile;
      (fs.promises as any).readFile = saved.fsReadFile;
    },
  };
}

/**
 * The server's half of the staged upload that keeps NOTHING but a running
 * hash of what landed in order - so the measurement is of the client.
 */
class HashingStaged {
  chunk: number;
  begun: Record<string, unknown> | null = null;
  received = 0;
  hash = crypto.createHash('sha256');
  puts: Array<{ start: number; end: number }> = [];
  maxBody = 0;
  committed = 0;
  aborted = 0;
  onPut: ((start: number) => void) | null = null;
  constructor(chunk: number) {
    this.chunk = chunk;
  }
  transport(): tr.StagedTransport {
    return {
      begin: async (body) => {
        this.begun = body;
        return { status: 200, json: { id: 'u1', chunk_size: this.chunk, offset: 0 } };
      },
      put: async (_id, start, end, _total, body) => {
        this.puts.push({ start, end });
        this.maxBody = Math.max(this.maxBody, body.byteLength);
        assert.equal(start, this.received, 'parts in order');
        this.hash.update(body);
        this.received = end;
        this.onPut?.(start);
        return { status: 200, json: { offset: end } };
      },
      status: async () => ({ status: 200, json: { offset: this.received } }),
      commit: async () => {
        this.committed++;
        const declared = String(this.begun?.hash ?? '');
        const got = 'sha256:' + this.hash.copy().digest('hex');
        if (declared && declared !== got) return { status: 422, json: { error: 'hash mismatch' } };
        return { status: 202, json: { op_id: 1, node_id: 2 } };
      },
      abort: async () => {
        this.aborted++;
      },
      op: async () => ({ status: 200, json: { status: 'ok' } }),
    };
  }
}

/** A ranged server for a synthetic file it never holds whole, with faults. */
function syntheticServer(size: number, o: { seed?: () => number; lastModified?: () => string; faults?: Array<string | undefined> } = {}) {
  const asked: Array<{ range: string; ifRange?: string }> = [];
  const faults = [...(o.faults ?? [])];
  async function* stream(from: number, to: number, seed: number, breakAt?: number): AsyncGenerator<Uint8Array> {
    for (let at = from; at < to; at += 256 * 1024) {
      if (breakAt !== undefined && at - from >= breakAt) throw new Error('ECONNRESET');
      yield fill(Buffer.allocUnsafe(Math.min(256 * 1024, to - at)), at, seed);
    }
  }
  const get: tr.RangeTransport = async (range, ifRange) => {
    asked.push({ range, ifRange });
    const fault = faults.shift();
    const seed = o.seed?.() ?? 1;
    const lm = o.lastModified?.() ?? 'Fri, 16 Oct 2026 10:00:00 GMT';
    if (fault === 'throw') throw new Error('ECONNRESET');
    if ((ifRange && ifRange !== lm) || fault === 'whole' || fault === 'whole-broken' || fault === 'whole-short') {
      const breakAt = fault === 'whole-broken' ? 3 * 256 * 1024 : undefined;
      return {
        status: 200,
        lastModified: lm,
        contentLength: fault === 'whole-short' ? size + 10 : size,
        body: stream(0, size, seed, breakAt),
      };
    }
    const m = /^bytes=(\d+)-(\d+)$/.exec(range)!;
    const start = Number(m[1]);
    const end = Math.min(Number(m[2]), size - 1);
    if (fault === 'part-broken') {
      return { status: 206, contentRange: `bytes ${start}-${end}/${size}`, lastModified: lm, body: stream(start, end + 1, seed, 256 * 1024) };
    }
    return { status: 206, contentRange: `bytes ${start}-${end}/${size}`, lastModified: lm, body: stream(start, end + 1, seed) };
  };
  return { get, asked };
}

// ── the spool ────────────────────────────────────────────────────────

test('spool: written at positions, started over, on the disk under its name only when finished', async () => {
  const dir = tmp();
  try {
    const w = await sp.openSpool(dir, 'a1b2c3d4e5f6');
    assert.match(path.basename(w.file), /^a1b2c3d4e5f6-[0-9a-f]{16}\.part$/);
    await w.write(0, Buffer.from('OLD-VERSION-LONGER'));
    await w.restart();
    await w.write(4, Buffer.from('EFGH'));
    await w.write(0, Buffer.from('ABCD'));
    const s = await w.finish(8, sha('ABCDEFGH'));
    assert.equal(fs.readFileSync(s.file, 'utf8'), 'ABCDEFGH', 'nothing of the attempt before the restart');
    assert.equal(s.size, 8);
    assert.ok(!fs.existsSync(w.file), 'the .part name is gone');
    assert.equal(path.dirname(s.file), dir);

    const d = await sp.openSpool(dir, 'a1b2c3d4e5f6');
    await d.write(0, Buffer.from('X'));
    await d.discard();
    assert.ok(!fs.existsSync(d.file));
    await assert.rejects(sp.openSpool(dir, '../evil'), /lower-case letters and digits/);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('spool: clearing takes only the names it makes - by tag, keeping what is kept, sparing a spool still being written', async () => {
  const dir = tmp();
  try {
    const keep = ['notes.txt', 'a1-zz', 'A1-0123456789abcdef', 'a1-0123456789abcde'];
    for (const n of keep) fs.writeFileSync(path.join(dir, n), 'x');
    const mk = (n: string) => fs.writeFileSync(path.join(dir, n), 'x');
    mk('aaa-0123456789abcdef');
    mk('aaa-1123456789abcdef.part');
    mk('bbb-2123456789abcdef');
    mk('ccc-3123456789abcdef');
    assert.equal(await sp.clearSpools(dir, { tag: 'aaa', parts: false }), 1);
    assert.ok(fs.existsSync(path.join(dir, 'aaa-1123456789abcdef.part')), 'one still being written is spared');
    assert.equal(await sp.clearSpools(dir, { keep: (t) => t === 'ccc' }), 2);
    assert.deepEqual(fs.readdirSync(dir).sort(), [...keep, 'ccc-3123456789abcdef'].sort());
    assert.equal(await sp.clearSpools(path.join(dir, 'missing')), 0);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('spool: written from, it is checked on the way - a spool that is not what was received writes nothing', async () => {
  const dir = tmp();
  try {
    const src = path.join(dir, 'aa-0123456789abcdef');
    fs.writeFileSync(src, 'RECEIVED');
    const good: sp.Spooled = { file: src, size: 8, sha256: sha('RECEIVED') };
    const out = path.join(dir, 'copy.docx');
    await sp.writeContent(out, good, { exclusive: true });
    assert.equal(fs.readFileSync(out, 'utf8'), 'RECEIVED');
    await assert.rejects(sp.writeContent(out, good, { exclusive: true }), (e: any) => e.code === 'EEXIST');
    assert.equal(fs.readFileSync(out, 'utf8'), 'RECEIVED', 'exclusive never replaces');

    fs.writeFileSync(src, 'TAMPERED');
    const bad = path.join(dir, 'bad.docx');
    await assert.rejects(sp.writeContent(bad, good, { exclusive: true }), /not what was received/);
    assert.ok(!fs.existsSync(bad), 'a half-checked copy is not left under a name the person reads as theirs');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('spool: a document written from a spool carries the time it was written, not the spool\'s', async () => {
  const dir = tmp();
  try {
    const src = path.join(dir, 'aa-0123456789abcdef');
    fs.writeFileSync(src, 'EDIT');
    const old = new Date('2020-01-01T00:00:00Z');
    fs.utimesSync(src, old, old);
    const doc = path.join(dir, 'doc.docx');
    fs.writeFileSync(doc, 'BEFORE');
    const before = Date.now();
    await ow.writeBackAtomic(doc, { file: src, size: 4, sha256: sha('EDIT') });
    assert.equal(fs.readFileSync(doc, 'utf8'), 'EDIT');
    assert.ok(fs.statSync(doc).mtimeMs >= before - 2000, 'a backup tool comparing times would pass the edit over');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

// ── a spooled save, everywhere a save goes ───────────────────────────

test('write-back from a spool: over the document only while it is the version filex knows; the spool stays usable when it is not', async () => {
  const dir = tmp();
  try {
    const doc = path.join(dir, 'Bütçe.xlsx');
    fs.writeFileSync(doc, 'V0');
    const seen = await ow.hashLocalVersion(doc);
    assert.ok(seen);
    const spool = path.join(dir, 'aa-0123456789abcdef');
    fs.writeFileSync(spool, 'FILEX-EDIT');
    const save: sp.Spooled = { file: spool, size: 10, sha256: sha('FILEX-EDIT') };

    // Changed outside filex: not written, the spool still there to keep beside.
    fs.writeFileSync(doc, 'AGENT');
    await assert.rejects(ow.writeBackAtomic(doc, save, { expect: seen }), (e: any) => e instanceof ow.LocalChangedError);
    assert.equal(fs.readFileSync(doc, 'utf8'), 'AGENT');
    const kept = await ow.writeConflictCopy(doc, save, { now: new Date('2026-10-16T10:00:00Z') });
    assert.equal(fs.readFileSync(kept, 'utf8'), 'FILEX-EDIT');

    // The version filex knows: written, and the version it records is the spool's.
    const now = await ow.hashLocalVersion(doc);
    const v = await ow.writeBackAtomic(doc, save, { expect: now });
    assert.equal(fs.readFileSync(doc, 'utf8'), 'FILEX-EDIT');
    assert.equal(v.sha256, save.sha256);
    assert.equal(v.size, 10);
    assert.deepEqual(
      fs.readdirSync(dir).filter((n) => n.includes('filex-openwith')),
      [],
      'no temp file left beside the document',
    );

    // Gone: the edit is kept, from the spool, where the error says.
    fs.rmSync(doc);
    await assert.rejects(ow.writeBackAtomic(doc, save, { expect: v }), (e: any) => {
      assert.ok(e instanceof ow.WriteBackError);
      assert.equal(fs.readFileSync(e.keptAt, 'utf8'), 'FILEX-EDIT');
      return true;
    });
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('a save in another format, told from a spool as from bytes in hand - a zip whose part list is far in, or across a read', async () => {
  const dir = tmp();
  try {
    const zipHead = Buffer.from([0x50, 0x4b, 0x03, 0x04, ...new Array(26).fill(0)]);
    zipHead[26] = 4; // a first entry named `word`, not `mimetype`
    const make = (name: string, body: Buffer): sp.Spooled => {
      const p = path.join(dir, 'aa-' + crypto.randomBytes(8).toString('hex'));
      fs.writeFileSync(p, body);
      return { file: p, size: body.length, sha256: sha(body) };
    };
    const needle = Buffer.from('[Content_Types].xml', 'latin1');
    const at = (pos: number) => {
      const b = Buffer.alloc(pos + needle.length + 100, 0x41);
      zipHead.copy(b, 0);
      needle.copy(b, pos);
      return b;
    };
    const vectors: Array<[string, Buffer]> = [
      ['far in', at(3 * MiB + 17)],
      ['across a read', at(sp.COPY_BUFFER_BYTES - 7)],
      ['at the very start of the second read', at(sp.COPY_BUFFER_BYTES)],
      ['a zip with no part list', Buffer.concat([zipHead, Buffer.alloc(2 * MiB, 0x42)])],
      ['an OLE file', Buffer.from([0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1, 0, 0])],
      ['text', Buffer.from('ad;adet\r\nelma;3\r\n')],
      ['empty', Buffer.alloc(0)],
    ];
    for (const doc of ['Rapor.doc', 'Rapor.docx', 'Tablo.csv', 'logo.psd']) {
      for (const [what, body] of vectors) {
        assert.equal(
          await ow.savedFormatOfContent(doc, make(what, body)),
          ow.savedFormatOf(doc, body),
          `${doc}, ${what}: the spool and the bytes in hand disagree`,
        );
      }
    }
    assert.equal(await ow.savedFormatOfContent('Rapor.doc', make('far', at(3 * MiB + 17))), 'docx');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

// ── the document, read a part at a time and held to one version ─────

test('the document goes up as the version filex hashed: a part asked for again is the same bytes, the whole hashes to it', async () => {
  const dir = tmp();
  try {
    const doc = path.join(dir, 'a.psd');
    writeSynthetic(doc, 50_000, 3);
    const v = await ow.hashLocalVersion(doc);
    assert.ok(v);
    const r = await ow.openDocument(doc, v);
    try {
      const a = await r.read(0, 16_384);
      const b = await r.read(16_384, 32_768);
      assert.deepEqual(await r.read(16_384, 32_768), b, 'asked again: the same part');
      await r.read(32_768, 50_000);
      await r.verify();
      assert.equal(a.byteLength, 16_384);
    } finally {
      await r.close();
    }
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('saved again while it goes up - another size, another time, or the same size and time with other bytes - nothing is taken as the version', async () => {
  const dir = tmp();
  try {
    const doc = path.join(dir, 'a.psd');
    writeSynthetic(doc, 40_000, 5);
    const v = (await ow.hashLocalVersion(doc))!;

    // Another size.
    let r = await ow.openDocument(doc, v);
    await r.read(0, 10_000);
    fs.appendFileSync(doc, 'MORE');
    await assert.rejects(r.read(10_000, 20_000), (e: any) => e instanceof ow.SourceChangedError);
    await r.close();
    fs.truncateSync(doc, 40_000);

    // Another time, the same bytes (the person saved what they had).
    const v2 = (await ow.hashLocalVersion(doc))!;
    r = await ow.openDocument(doc, v2);
    await r.read(0, 10_000);
    const later = new Date(Date.now() + 60_000);
    fs.utimesSync(doc, later, later);
    await assert.rejects(r.read(10_000, 20_000), /saved again/);
    await r.close();

    // The same size and the same time, other bytes (a two-second file
    // system): the part read before is not what is there now, and the
    // whole does not hash to the version.
    const t3 = pinTime(doc);
    const v3 = (await ow.hashLocalVersion(doc))!;
    r = await ow.openDocument(doc, v3);
    await r.read(0, 10_000);
    const fd = fs.openSync(doc, 'r+');
    fs.writeSync(fd, Buffer.from('XXXX'), 0, 4, 100);
    fs.writeSync(fd, Buffer.from('YYYY'), 0, 4, 30_000);
    fs.closeSync(fd);
    pinTime(doc, t3);
    assert.equal(fs.statSync(doc).mtimeMs, v3.mtimeMs, 'the time was not kept - the case below measures nothing');
    await assert.rejects(r.read(0, 10_000), /not what was sent/);
    await r.read(10_000, 20_000);
    await r.read(20_000, 40_000);
    await assert.rejects(r.verify(), /not the version filex read/);
    await r.close();
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

// ── which way it goes up ─────────────────────────────────────────────

function documentTransport(staged: tr.StagedTransport) {
  const calls = { whole: [] as Buffer[], streamed: 0 };
  const t: tr.DocumentTransport = {
    staged,
    whole: async (b) => {
      calls.whole.push(Buffer.from(b));
    },
    streamed: async (src) => {
      calls.streamed++;
      const h = crypto.createHash('sha256');
      for (let at = 0; at < src.size; at += MiB) h.update(await src.read(at, Math.min(at + MiB, src.size)));
      await src.verify?.();
      (calls as any).streamedSha = h.digest('hex');
    },
  };
  return { t, calls };
}

test('a small document (8 MiB or less) goes in one request, its bytes checked against the version first; the staged path is never begun', async () => {
  const dir = tmp();
  try {
    for (const size of [0, 3, tr.PART_BYTES]) {
      const doc = path.join(dir, `small-${size}.docx`);
      const want = writeSynthetic(doc, size, 7);
      const v = (await ow.hashLocalVersion(doc))!;
      const staged = new HashingStaged(4096);
      const { t, calls } = documentTransport(staged.transport());
      const r = await ow.openDocument(doc, v);
      try {
        assert.equal(await tr.sendDocument(t, r, { dir: 'docs://.filex-open', name: 'x.docx', sha256: v.sha256 }), 'whole');
      } finally {
        await r.close();
      }
      assert.equal(calls.whole.length, 1);
      assert.equal(sha(calls.whole[0]!), want, 'the request carries the document');
      assert.equal(staged.begun, null, 'no staged upload for a small file');
    }

    // Changed after it was hashed: not sent at all.
    const doc = path.join(dir, 'changed.docx');
    fs.writeFileSync(doc, 'FIRST');
    const t0 = pinTime(doc);
    const v = (await ow.hashLocalVersion(doc))!;
    fs.writeFileSync(doc, 'OTHER');
    pinTime(doc, t0);
    const { t, calls } = documentTransport(new HashingStaged(4096).transport());
    const r = await ow.openDocument(doc, v);
    await assert.rejects(tr.sendDocument(t, r, { dir: 'd://', name: 'x', sha256: v.sha256 }), (e: any) => e instanceof ow.SourceChangedError);
    await r.close();
    assert.equal(calls.whole.length, 0, 'a version filex did not hash was sent');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('a large document goes staged, read a part at a time; over the server\'s limit it is refused before a byte is read; with no staged path it is streamed', async () => {
  const dir = tmp();
  try {
    const doc = path.join(dir, 'large.psd');
    const size = tr.PART_BYTES + 12_345;
    const want = writeSynthetic(doc, size, 9);
    const v = (await ow.hashLocalVersion(doc))!;

    const staged = new HashingStaged(1 * MiB);
    const { t } = documentTransport(staged.transport());
    let r = await ow.openDocument(doc, v);
    assert.equal(await tr.sendDocument(t, r, { dir: 'docs://.filex-open', name: 'a.psd', sha256: v.sha256, sleep: noSleep }), 'staged');
    await r.close();
    assert.equal(staged.begun?.hash, 'sha256:' + want);
    assert.equal(staged.committed, 1);
    assert.equal(staged.received, size);
    assert.ok(staged.maxBody <= MiB, 'one part at a time');

    let reads = 0;
    const counting: tr.UploadSource = { size, read: async () => (reads++, new Uint8Array(0)) };
    await assert.rejects(
      tr.sendDocument(t, counting, { dir: 'd://', name: 'a.psd', limitBytes: tr.PART_BYTES }),
      /larger than this server takes \(8 MB\)/,
    );
    assert.equal(reads, 0);

    const none = new HashingStaged(MiB);
    const noStaged = none.transport();
    noStaged.begin = async () => ({ status: 501, json: { error: 'staged uploads are not configured' } });
    const { t: t2, calls } = documentTransport(noStaged);
    r = await ow.openDocument(doc, v);
    assert.equal(await tr.sendDocument(t2, r, { dir: 'd://', name: 'a.psd', sha256: v.sha256 }), 'streamed');
    await r.close();
    assert.equal(calls.streamed, 1);
    assert.equal((calls as any).streamedSha, want);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('saved again in the middle of a staged upload: given up before the commit, the staging released - a mixed file is never committed', async () => {
  const dir = tmp();
  try {
    const doc = path.join(dir, 'large.psd');
    const size = 6 * 256 * 1024;
    writeSynthetic(doc, size, 11);
    const t0 = pinTime(doc);
    const v = (await ow.hashLocalVersion(doc))!;

    // The same size and time, other bytes AHEAD of what was sent (the
    // first parts are the old version, the last ones the new): no look at
    // the file can tell, the whole's hash does - before the commit.
    const staged = new HashingStaged(256 * 1024);
    staged.onPut = (start) => {
      if (start !== 256 * 1024) return;
      const fd = fs.openSync(doc, 'r+');
      fs.writeSync(fd, Buffer.from('SAVED-AGAIN'), 0, 11, 4 * 256 * 1024 + 10);
      fs.closeSync(fd);
      pinTime(doc, t0);
    };
    let r = await ow.openDocument(doc, v);
    try {
      await assert.rejects(
        tr.uploadStaged(staged.transport(), { dir: 'd://', name: 'a.psd', source: r, sha256: v.sha256, sleep: noSleep }),
        /not the version filex read/,
      );
    } finally {
      await r.close();
    }
    assert.equal(fs.statSync(doc).mtimeMs, v.mtimeMs, 'the time was not kept - the case measures nothing');
    assert.equal(staged.received, size, 'every part went up - the check that stopped it is the one before the commit');
    assert.equal(staged.committed, 0, 'committed anyway');
    assert.equal(staged.aborted, 1, 'the staging was not released');

    // Behind what was sent, the same way: what went up is the version
    // filex hashed, whole - the change is the next outside change.
    writeSynthetic(doc, size, 11);
    const tb = pinTime(doc);
    const vb = (await ow.hashLocalVersion(doc))!;
    const behind = new HashingStaged(256 * 1024);
    behind.onPut = (start) => {
      if (start !== 2 * 256 * 1024) return;
      const fd = fs.openSync(doc, 'r+');
      fs.writeSync(fd, Buffer.from('SAVED-AGAIN'), 0, 11, 10);
      fs.closeSync(fd);
      pinTime(doc, tb);
    };
    r = await ow.openDocument(doc, vb);
    try {
      await tr.uploadStaged(behind.transport(), { dir: 'd://', name: 'a.psd', source: r, sha256: vb.sha256, sleep: noSleep });
    } finally {
      await r.close();
    }
    assert.equal(behind.committed, 1);
    assert.equal(behind.hash.digest('hex'), vb.sha256, 'what was committed is one version of the document');

    // Another size: given up at the next part.
    writeSynthetic(doc, size, 11);
    const v2 = (await ow.hashLocalVersion(doc))!;
    const grow = new HashingStaged(256 * 1024);
    grow.onPut = (start) => {
      if (start === 0) fs.appendFileSync(doc, 'MORE');
    };
    r = await ow.openDocument(doc, v2);
    try {
      await assert.rejects(
        tr.uploadStaged(grow.transport(), { dir: 'd://', name: 'a.psd', source: r, sha256: v2.sha256, sleep: noSleep }),
        /its size is/,
      );
    } finally {
      await r.close();
    }
    assert.equal(grow.puts.length, 1, 'not one more part of a file that is no longer the version');
    assert.equal(grow.committed, 0);
    assert.equal(grow.aborted, 1);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

// ── the way back, into a spool ───────────────────────────────────────

test('a save comes home into a spool in ranged parts, hashed as they land; a part that breaks off is asked for again and not hashed twice', async () => {
  const dir = tmp();
  try {
    const size = 5 * 256 * 1024 + 99;
    const s = syntheticServer(size, { faults: [undefined, 'part-broken', 'throw'] });
    const w = await sp.openSpool(dir, 'sess1');
    const got = await tr.downloadRangedTo(s.get, w, { chunkSize: 512 * 1024, sleep: noSleep });
    const spool = await w.finish(got.size, got.sha256);
    assert.equal(got.size, size);
    assert.equal(got.sha256, syntheticSha(size), 'the hash taken on the way is the file\'s');
    assert.equal(sha(fs.readFileSync(spool.file)), got.sha256, 'what is on the disk is what was hashed');
    assert.deepEqual(s.asked.slice(0, 4).map((a) => a.range), ['bytes=0-524287', 'bytes=524288-1048575', 'bytes=524288-1048575', 'bytes=524288-1048575']);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('saved again while it comes home: the whole new version, streamed into the spool from the start - one that breaks off, or is short of its length, is asked for again', async () => {
  const dir = tmp();
  try {
    const size = 4 * 256 * 1024;
    let seed = 1;
    const s = syntheticServer(size, {
      seed: () => seed,
      lastModified: () => (seed === 1 ? 'Fri, 16 Oct 2026 10:00:00 GMT' : 'Fri, 16 Oct 2026 10:00:05 GMT'),
      faults: [undefined, 'whole-broken', 'whole-short'],
    });
    const get: tr.RangeTransport = async (range, ifRange) => {
      const a = await s.get(range, ifRange);
      seed = 2; // saved again after the first part
      return a;
    };
    const w = await sp.openSpool(dir, 'sess1');
    const got = await tr.downloadRangedTo(get, w, { chunkSize: 256 * 1024, sleep: noSleep });
    const spool = await w.finish(got.size, got.sha256);
    assert.equal(got.sha256, syntheticSha(size, 2), 'the new version, whole - never spliced');
    assert.equal(sha(fs.readFileSync(spool.file)), syntheticSha(size, 2));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('a disk that takes no more is not a dropped connection: the download ends at once', async () => {
  const s = syntheticServer(3 * 256 * 1024);
  let writes = 0;
  const full: tr.RangeSink = {
    restart: async () => undefined,
    write: async () => {
      if (++writes > 2) throw Object.assign(new Error('no space left on device'), { code: 'ENOSPC' });
    },
  };
  await assert.rejects(tr.downloadRangedTo(s.get, full, { chunkSize: 256 * 1024, sleep: noSleep }), /no space left/);
  assert.equal(s.asked.length, 3, 'the part that could not be written is not asked for again');
});

// ── memory: flat, whatever the size ──────────────────────────────────

test(`memory: a ${BIG / MiB} MiB working copy goes up, comes home and is written back holding a few parts at most`, { timeout: 300_000 }, async (t) => {
  const dir = tmp();
  const io = watchIo();
  try {
    const doc = path.join(dir, 'Logo.psd');
    const want = writeSynthetic(doc, BIG, 21);

    // The instrument sees a whole-file read (the control) - well above the
    // ceiling below, or the ceiling would measure nothing. (Not to the byte:
    // garbage of the tests before may go between two collections.)
    const controlHeld = await heldByWholeRead(doc);
    assert.ok(controlHeld >= BIG / 2 && controlHeld > HELD_CEILING, `the instrument cannot see a whole-file read (${controlHeld})`);

    await io.start();

    // Up: hashed, then read a part at a time.
    const up = meter();
    const v = (await ow.hashLocalVersion(doc))!;
    up.sample();
    assert.equal(v.sha256, want);
    const staged = new HashingStaged(tr.PART_BYTES);
    staged.onPut = () => up.sample();
    const r = await ow.openDocument(doc, v);
    const { t: dt } = documentTransport(staged.transport());
    assert.equal(await tr.sendDocument(dt, r, { dir: 'docs://.filex-open', name: 'Logo.psd', sha256: v.sha256, sleep: noSleep }), 'staged');
    await r.close();
    assert.equal(staged.committed, 1);

    // Home: into a spool, a part at a time.
    const down = meter();
    const s = syntheticServer(BIG, { seed: () => 21 });
    const sampled: tr.RangeTransport = async (range, ifRange) => {
      down.sample();
      return s.get(range, ifRange);
    };
    const w = await sp.openSpool(path.join(dir, 'spool'), 'sess1');
    const got = await tr.downloadRangedTo(sampled, w, { sleep: noSleep });
    const save = await w.finish(got.size, got.sha256);
    down.sample();
    assert.equal(save.sha256, want);

    // Over the document: copied through one buffer, checked, renamed.
    const back = meter();
    const timer = setInterval(() => back.sample(), 25);
    try {
      const mine = await ow.writeBackAtomic(doc, save, { expect: v });
      assert.equal(mine.sha256, want);
    } finally {
      clearInterval(timer);
    }
    back.sample();
    io.stop();

    t.diagnostic(
      `held at most (MiB): control ${(controlHeld / MiB).toFixed(1)}, up ${(up.peak / MiB).toFixed(1)}, home ${(down.peak / MiB).toFixed(1)}, written back ${(back.peak / MiB).toFixed(1)}; largest read ${(io.seen.maxRead / MiB).toFixed(2)}, largest write ${(io.seen.maxWrite / MiB).toFixed(2)}`,
    );
    assert.equal(io.seen.wholeReads, 0, 'a file was read whole');
    assert.ok(io.seen.maxRead <= tr.PART_BYTES, 'a read larger than a part');
    assert.ok(io.seen.maxWrite <= tr.PART_BYTES, 'a write larger than a part');
    for (const [what, peak] of [['up', up.peak], ['home', down.peak], ['written back', back.peak]] as const) {
      assert.ok(peak < HELD_CEILING, `${what}: ${(peak / MiB).toFixed(1)} MiB held for a ${BIG / MiB} MiB file`);
    }
    assert.equal(sha(fs.readFileSync(doc)), want, 'the document is the save');
  } finally {
    io.stop();
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
