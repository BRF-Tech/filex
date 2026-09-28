// The streaming ZIP writer (packages/core/src/lib/zipstream.ts) behind the
// decrypted download of an encrypted folder: STORE, data descriptors, UTF-8
// names, ZIP64 when needed. Read back by a reader written here from the
// APPNOTE, AND by Python's zipfile (an independent implementation) when a
// Python is on the machine.

import { spawnSync } from 'node:child_process';

import { describe, expect, it } from 'vitest';

import { createZipStream, crc32, zipEntryName, type ZipEntry } from '../../../packages/core/src/lib/zipstream';
import { bytesStream, collectBytes } from '../../../packages/core/src/lib/e2estream';

const enc = new TextEncoder();

function pattern(n: number, seed: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = (i * 31 + seed) & 0xff;
  return out;
}

interface Read {
  name: string;
  data: Uint8Array;
  crc: number;
  flags: number;
}

/** A central-directory reader, from APPNOTE 4.3.12–4.3.16 and 4.5.3. */
function readZip(z: Uint8Array): { entries: Read[]; zip64: boolean } {
  const dv = new DataView(z.buffer, z.byteOffset, z.byteLength);
  const u64 = (o: number) => dv.getUint32(o, true) + dv.getUint32(o + 4, true) * 2 ** 32;
  const eocd = z.length - 22;
  expect(dv.getUint32(eocd, true)).toBe(0x06054b50);
  let count = dv.getUint16(eocd + 10, true);
  let cdSize = dv.getUint32(eocd + 12, true);
  let cdOff = dv.getUint32(eocd + 16, true);
  let zip64 = false;
  if (count === 0xffff || cdOff === 0xffffffff) {
    zip64 = true;
    const loc = eocd - 20;
    expect(dv.getUint32(loc, true)).toBe(0x07064b50);
    const e64 = u64(loc + 8);
    expect(dv.getUint32(e64, true)).toBe(0x06064b50);
    count = u64(e64 + 32);
    cdSize = u64(e64 + 40);
    cdOff = u64(e64 + 48);
  }
  expect(cdOff + cdSize).toBeLessThanOrEqual(z.length);
  const entries: Read[] = [];
  let o = cdOff;
  for (let i = 0; i < count; i++) {
    expect(dv.getUint32(o, true)).toBe(0x02014b50);
    const flags = dv.getUint16(o + 8, true);
    const crc = dv.getUint32(o + 16, true);
    let size = dv.getUint32(o + 24, true);
    const nameLen = dv.getUint16(o + 28, true);
    const extraLen = dv.getUint16(o + 30, true);
    let local = dv.getUint32(o + 42, true);
    const name = new TextDecoder().decode(z.subarray(o + 46, o + 46 + nameLen));
    let e = o + 46 + nameLen;
    const end = e + extraLen;
    while (e < end) {
      const id = dv.getUint16(e, true);
      const len = dv.getUint16(e + 2, true);
      if (id === 0x0001) {
        let f = e + 4;
        if (size === 0xffffffff) {
          size = u64(f);
          f += 16;
        }
        if (local === 0xffffffff) local = u64(f);
      }
      e += 4 + len;
    }
    expect(dv.getUint32(local, true)).toBe(0x04034b50);
    const lName = dv.getUint16(local + 26, true);
    const lExtra = dv.getUint16(local + 28, true);
    const start = local + 30 + lName + lExtra;
    entries.push({ name, data: z.slice(start, start + size), crc, flags });
    o = end;
  }
  return { entries, zip64 };
}

async function zipOf(entries: ZipEntry[], forceZip64 = false): Promise<Uint8Array> {
  return collectBytes(createZipStream(entries, { forceZip64 }));
}

const python = (() => {
  for (const cmd of ['python', 'python3']) {
    const r = spawnSync(cmd, ['-c', 'import zipfile'], { encoding: 'utf8' });
    if (r.status === 0) return cmd;
  }
  return null;
})();

/** Python's zipfile reads it: names, sizes, and every CRC checked. */
function pythonReads(z: Uint8Array): Record<string, number> {
  const script = [
    'import io, json, sys, zipfile',
    'zf = zipfile.ZipFile(io.BytesIO(sys.stdin.buffer.read()))',
    'bad = zf.testzip()',
    'assert bad is None, bad',
    'print(json.dumps({i.filename: i.file_size for i in zf.infolist()}, ensure_ascii=False))',
  ].join('\n');
  const r = spawnSync(python!, ['-c', script], { input: Buffer.from(z), encoding: 'utf8', env: { ...process.env, PYTHONIOENCODING: 'utf-8' } });
  if (r.status !== 0) throw new Error(r.stderr);
  return JSON.parse(r.stdout);
}

describe('zipstream', () => {
  it('CRC-32 is the IEEE one, and continues across pieces', () => {
    expect(crc32(enc.encode('123456789'))).toBe(0xcbf43926);
    const all = pattern(10_000, 1);
    expect(crc32(all.subarray(5000), crc32(all.subarray(0, 5000)))).toBe(crc32(all));
    expect(crc32(new Uint8Array(0))).toBe(0);
  });

  const entries = (): ZipEntry[] => [
    { name: 'Kasa/' },
    { name: 'Kasa/İzmir notları.txt', data: bytesStream(enc.encode('merhaba dünya\n')), sizeHint: 100 },
    { name: 'Kasa/alt/', mtime: new Date(2026, 8, 27, 10, 20, 30) },
    { name: 'Kasa/alt/boş.bin', data: bytesStream(new Uint8Array(0)), sizeHint: 0 },
    { name: 'Kasa/büyük.bin', data: async () => bytesStream(pattern(200_000, 2), 7777) },
  ];

  it('writes STORE entries with UTF-8 names and data descriptors a reader can open', async () => {
    const z = await zipOf(entries());
    const { entries: got, zip64 } = readZip(z);
    expect(zip64).toBe(false);
    expect(got.map((e) => e.name)).toEqual([
      'Kasa/',
      'Kasa/İzmir notları.txt',
      'Kasa/alt/',
      'Kasa/alt/boş.bin',
      'Kasa/büyük.bin',
    ]);
    for (const e of got) {
      expect(e.flags & 0x0800, e.name).toBe(0x0800);
      expect(crc32(e.data), e.name).toBe(e.crc);
    }
    expect(new TextDecoder().decode(got[1].data)).toBe('merhaba dünya\n');
    expect(got[4].data).toEqual(pattern(200_000, 2));
  });

  it('ZIP64 records when asked (or when a size is unknown), still readable', async () => {
    const z = await zipOf(entries(), true);
    const { entries: got, zip64 } = readZip(z);
    expect(zip64).toBe(true);
    expect(got[4].data).toEqual(pattern(200_000, 2));
  });

  it.skipIf(!python)('Python zipfile opens both, every CRC right', async () => {
    const want = {
      'Kasa/': 0,
      'Kasa/İzmir notları.txt': 15,
      'Kasa/alt/': 0,
      'Kasa/alt/boş.bin': 0,
      'Kasa/büyük.bin': 200_000,
    };
    expect(pythonReads(await zipOf(entries()))).toEqual(want);
    expect(pythonReads(await zipOf(entries(), true))).toEqual(want);
  });

  it('an entry that fails fails the archive — a partial archive is never a finished one', async () => {
    const broken = new ReadableStream<Uint8Array>({
      pull(ctl) {
        ctl.error(new Error('decrypt failed'));
      },
    });
    await expect(zipOf([{ name: 'a.txt', data: bytesStream(enc.encode('ok')) }, { name: 'b.txt', data: broken }])).rejects.toThrow(
      /decrypt failed/,
    );
  });

  it('refuses a name that would climb out of the archive', () => {
    expect(() => zipEntryName('../etc/passwd', false)).toThrow(/unsafe/);
    expect(() => zipEntryName('a/../../b', false)).toThrow(/unsafe/);
    expect(zipEntryName('/a//b/./c', false)).toBe('a/b/c');
    expect(zipEntryName('a\\b', true)).toBe('a/b/');
  });
});
