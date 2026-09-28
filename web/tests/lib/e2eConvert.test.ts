// Encrypting a folder that already exists, in place: the key file's `conv`
// feature (packages/core/src/lib/e2ecrypto.ts) and the job that converts the
// files (lib/e2econvert.ts), against an in-memory folder.

import { beforeAll, describe, expect, it } from 'vitest';

import {
  E2E_MARKER_NAME,
  conversionPending,
  createEncryptedFolder,
  decryptFile,
  encryptFile,
  finishConversion,
  hasMagic,
  parseMarker,
  parseMarkerDetailed,
  startConversion,
  enableNames,
  unlockWithPassword,
  type E2eMarker,
} from '../../../packages/core/src/lib/e2ecrypto';
import { expectOf, runConversion, type ConvertIo, type ConvertRow } from '../../../packages/core/src/lib/e2econvert';
import * as legacy047 from '../fixtures/e2ecrypto-legacy-v0.47.0';

const PW = 'correct horse battery';
const SLOW = 30_000;
const ROOT = 'local://Arşiv';
const enc = new TextEncoder();
const dec = new TextDecoder();

function wire(m: E2eMarker): E2eMarker {
  const parsed = parseMarker(JSON.stringify(m));
  if (!parsed) throw new Error('marker did not survive parseMarker');
  return parsed;
}

describe('the conv feature of the key file', () => {
  let base: E2eMarker;
  let fmk: CryptoKey;
  beforeAll(async () => {
    const made = await createEncryptedFolder(PW);
    base = wire(made.marker);
    fmk = made.fmk;
  }, SLOW);

  it('marks a conversion under way, as a required feature', () => {
    const m = wire(startConversion(base, '2026-09-27T10:00:00Z'));
    expect(m.v).toBe(3);
    expect(m.req).toEqual(['conv']);
    expect(m.conv).toEqual({ pending: true, started: '2026-09-27T10:00:00Z' });
    expect(conversionPending(m)).toBe(true);
    expect(conversionPending(base)).toBe(false);
  });

  it('is refused by filex 0.47 — it would treat plaintext files as encrypted ones', () => {
    expect(legacy047.parseMarker(JSON.stringify(startConversion(base)))).toBeNull();
  });

  it('keeps the password working while it runs', async () => {
    const m = wire(startConversion(base));
    expect(await unlockWithPassword(m, PW)).not.toBeNull();
  }, SLOW);

  it('goes back to v2 when it finishes at level 1, and keeps names at level 2', async () => {
    const done = wire(finishConversion(wire(startConversion(base))));
    expect(done.v).toBe(2);
    expect(done.req).toBeUndefined();
    expect(done.conv).toBeUndefined();

    const withNames = (await enableNames(base, fmk)).marker;
    const both = wire(startConversion(withNames));
    expect(both.req).toEqual(['names', 'conv']);
    const after = wire(finishConversion(both));
    expect(after.v).toBe(3);
    expect(after.req).toEqual(['names']);
  }, SLOW);

  it('rejects a conv slot that is not required, or required and missing', () => {
    const m = startConversion(base);
    expect(parseMarker(JSON.stringify({ ...m, req: [] }))).toBeNull();
    const { conv: _gone, ...noSlot } = m;
    expect(parseMarker(JSON.stringify(noSlot))).toBeNull();
    expect(parseMarker(JSON.stringify({ ...base, conv: { pending: true } }))).toBeNull();
    expect(parseMarkerDetailed(JSON.stringify(m))?.unsupported).toEqual([]);
  });
});

/** A folder by wire path: 'dir', or a file's bytes. */
class FakeFolder {
  readonly nodes = new Map<string, 'dir' | { bytes: Uint8Array; mtime: number }>();
  readonly writes: string[] = [];
  mtime = 1_000;
  constructor() {
    this.nodes.set(ROOT, 'dir');
  }
  mkdir(p: string) {
    this.nodes.set(p, 'dir');
  }
  put(p: string, bytes: Uint8Array) {
    this.nodes.set(p, { bytes, mtime: ++this.mtime });
  }
  text(p: string): Uint8Array {
    const n = this.nodes.get(p);
    if (!n || n === 'dir') throw new Error(`no file ${p}`);
    return n.bytes;
  }
  io(key: CryptoKey, stopAfter = Infinity, change?: (path: string) => void): ConvertIo {
    let written = 0;
    return {
      list: async (dir) => {
        const out: ConvertRow[] = [];
        for (const [p, v] of this.nodes) {
          if (!p.startsWith(dir + '/') || p.slice(dir.length + 1).includes('/')) continue;
          const basename = p.slice(dir.length + 1);
          out.push(
            v === 'dir'
              ? { path: p, basename, type: 'dir' }
              : { path: p, basename, type: 'file', size: v.bytes.byteLength, last_modified: v.mtime },
          );
        }
        return out;
      },
      head: async (p) => this.text(p).slice(0, 16),
      read: async (p) => {
        change?.(p);
        return this.text(p).slice().buffer as ArrayBuffer;
      },
      write: async (_dir, row, data, expect) => {
        const cur = this.nodes.get(row.path);
        if (!cur || cur === 'dir' || expect !== `${cur.bytes.byteLength}:${cur.mtime}`) {
          throw new Error('412');
        }
        this.put(row.path, new Uint8Array(await data.arrayBuffer()));
        this.writes.push(row.path);
        written++;
      },
      encrypt: (data) => encryptFile(key, data),
      stopped: () => written >= stopAfter,
    };
  }
}

describe('the conversion job', () => {
  let fmk: CryptoKey;
  beforeAll(async () => {
    fmk = (await createEncryptedFolder(PW)).fmk;
  }, SLOW);

  function folder(): FakeFolder {
    const f = new FakeFolder();
    f.put(`${ROOT}/${E2E_MARKER_NAME}`, enc.encode('{"v":3}'));
    f.put(`${ROOT}/bir.txt`, enc.encode('bir'));
    f.mkdir(`${ROOT}/Faturalar`);
    f.put(`${ROOT}/Faturalar/2024.pdf`, enc.encode('%PDF 2024'));
    f.put(`${ROOT}/Faturalar/boş.txt`, new Uint8Array(0));
    return f;
  }

  it('encrypts every file in place and leaves the key file alone', async () => {
    const f = folder();
    const prog = { total: 0, done: 0, skipped: 0, tooBig: 0, failed: 0 };
    await runConversion(ROOT, f.io(fmk), prog, { maxBytes: 1 << 20 });
    expect(prog).toEqual({ total: 3, done: 3, skipped: 0, tooBig: 0, failed: 0 });
    expect(dec.decode(f.text(`${ROOT}/${E2E_MARKER_NAME}`))).toBe('{"v":3}');
    for (const [p, plain] of [
      [`${ROOT}/bir.txt`, 'bir'],
      [`${ROOT}/Faturalar/2024.pdf`, '%PDF 2024'],
      [`${ROOT}/Faturalar/boş.txt`, ''],
    ] as const) {
      const bytes = f.text(p);
      expect(hasMagic(bytes), p).toBe(true);
      expect(dec.decode(await decryptFile(fmk, bytes.slice().buffer as ArrayBuffer))).toBe(plain);
    }
  }, SLOW);

  it('continues after a stop, touching nothing it already did', async () => {
    const f = folder();
    const first = { total: 0, done: 0, skipped: 0, tooBig: 0, failed: 0 };
    await runConversion(ROOT, f.io(fmk, 1), first, { maxBytes: 1 << 20 });
    expect(first.done).toBe(1);
    const second = { total: 0, done: 0, skipped: 0, tooBig: 0, failed: 0 };
    await runConversion(ROOT, f.io(fmk), second, { maxBytes: 1 << 20 });
    expect(second).toMatchObject({ total: 3, done: 2, skipped: 1, failed: 0 });
    expect(new Set(f.writes).size).toBe(3);
    const third = { total: 0, done: 0, skipped: 0, tooBig: 0, failed: 0 };
    await runConversion(ROOT, f.io(fmk), third, { maxBytes: 1 << 20 });
    expect(third).toMatchObject({ done: 0, skipped: 3 });
  }, SLOW);

  it('does not overwrite a file that changed while it was being encrypted', async () => {
    const f = folder();
    const prog = { total: 0, done: 0, skipped: 0, tooBig: 0, failed: 0 };
    const io = f.io(fmk, Infinity, (p) => {
      if (p.endsWith('bir.txt')) f.put(p, enc.encode('bir, edited meanwhile'));
    });
    await runConversion(ROOT, io, prog, { maxBytes: 1 << 20 });
    expect(prog).toMatchObject({ done: 2, failed: 1 });
    expect(dec.decode(f.text(`${ROOT}/bir.txt`))).toBe('bir, edited meanwhile');
    // The next run converts the edited file.
    const again = { total: 0, done: 0, skipped: 0, tooBig: 0, failed: 0 };
    await runConversion(ROOT, f.io(fmk), again, { maxBytes: 1 << 20 });
    expect(again).toMatchObject({ done: 1, skipped: 2, failed: 0 });
  }, SLOW);

  it('counts, and leaves, a file too big for one-shot encryption', async () => {
    const f = folder();
    f.put(`${ROOT}/büyük.bin`, new Uint8Array(64));
    const prog = { total: 0, done: 0, skipped: 0, tooBig: 0, failed: 0 };
    await runConversion(ROOT, f.io(fmk), prog, { maxBytes: 32 });
    expect(prog).toMatchObject({ total: 4, done: 3, tooBig: 1 });
    expect(hasMagic(f.text(`${ROOT}/büyük.bin`))).toBe(false);
  }, SLOW);

  it('writes on the condition the listing saw', () => {
    expect(expectOf({ path: 'x', basename: 'x', type: 'file', size: 12, last_modified: 1700 })).toBe('12:1700');
    expect(expectOf({ path: 'x', basename: 'x', type: 'file' })).toBeNull();
  });
});
