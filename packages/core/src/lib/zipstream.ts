/**
 * zipstream — a streaming ZIP writer: STORE only (no compression), sizes and
 * CRC-32 in a data descriptor after each entry, ZIP64 where a size or an
 * offset needs it, and UTF-8 names (general purpose bit 11).
 *
 * Built for the decrypted download of an encrypted folder
 * (docs/E2E-ENCRYPTION.md → "Downloading a decrypted copy"): the entries are
 * decrypted in this tab as the archive is written, so nothing is held but the
 * piece in flight, and the output goes straight to a save sink. No
 * dependency: the ZIP libraries that stream either compress (a waste on
 * photos and video, and CPU in the tab) or compute CRC-32 in WebAssembly,
 * which a strict Content-Security-Policy may refuse.
 *
 * Reference: PKWARE APPNOTE.TXT 6.3.10 — 4.3.7 (local header), 4.3.9 (data
 * descriptor), 4.3.12 (central directory), 4.3.14–16 (ZIP64 end records),
 * 4.5.3 (ZIP64 extra field).
 */

export interface ZipEntry {
  /** Path inside the archive, `/`-separated. A folder ends with `/`. */
  name: string;
  /** The entry's bytes; absent for a folder. Opened when its turn comes. */
  data?: ReadableStream<Uint8Array> | (() => Promise<ReadableStream<Uint8Array>>);
  /**
   * An UPPER BOUND on the entry's bytes, when one is known (the ciphertext
   * size bounds the plaintext). It decides up front whether the entry needs
   * ZIP64 records; unknown means "assume it might".
   */
  sizeHint?: number;
  mtime?: Date;
}

export interface ZipOptions {
  /** Use ZIP64 records for every entry and at the end (tests). */
  forceZip64?: boolean;
  /** Bytes written so far. */
  onProgress?: (bytes: number) => void;
}

const U32 = 0xffffffff;
const U16 = 0xffff;
const enc = new TextEncoder();

// ---------------------------------------------------------------------
// CRC-32 (IEEE 802.3, reflected, table-driven)
// ---------------------------------------------------------------------

let CRC_TABLE: Uint32Array | null = null;
function crcTable(): Uint32Array {
  if (CRC_TABLE) return CRC_TABLE;
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  CRC_TABLE = t;
  return t;
}

/** Continue a CRC-32 over `b`. Start with 0; the result is final as is. */
export function crc32(b: Uint8Array, crc = 0): number {
  const t = crcTable();
  let c = (crc ^ U32) >>> 0;
  for (let i = 0; i < b.length; i++) c = t[(c ^ b[i]) & 0xff] ^ (c >>> 8);
  return (c ^ U32) >>> 0;
}

// ---------------------------------------------------------------------
// Records
// ---------------------------------------------------------------------

/** A little-endian record builder. */
class Rec {
  private buf: Uint8Array;
  private dv: DataView;
  private o = 0;
  constructor(n: number) {
    this.buf = new Uint8Array(n);
    this.dv = new DataView(this.buf.buffer);
  }
  u16(v: number) {
    this.dv.setUint16(this.o, v, true);
    this.o += 2;
    return this;
  }
  u32(v: number) {
    this.dv.setUint32(this.o, v >>> 0, true);
    this.o += 4;
    return this;
  }
  u64(v: number) {
    this.dv.setUint32(this.o, v % 2 ** 32, true);
    this.dv.setUint32(this.o + 4, Math.floor(v / 2 ** 32), true);
    this.o += 8;
    return this;
  }
  bytes(b: Uint8Array) {
    this.buf.set(b, this.o);
    this.o += b.length;
    return this;
  }
  done(): Uint8Array {
    if (this.o !== this.buf.length) throw new Error(`zip: record size ${this.o} != ${this.buf.length}`);
    return this.buf;
  }
}

function dosTime(d: Date): { time: number; date: number } {
  const y = d.getFullYear();
  if (y < 1980) return { time: 0, date: (1 << 5) | 1 };
  return {
    time: (d.getHours() << 11) | (d.getMinutes() << 5) | Math.floor(d.getSeconds() / 2),
    date: ((Math.min(y, 2107) - 1980) << 9) | ((d.getMonth() + 1) << 5) | d.getDate(),
  };
}

interface CdEntry {
  name: Uint8Array;
  dir: boolean;
  zip64Local: boolean;
  crc: number;
  size: number;
  offset: number;
  time: number;
  date: number;
}

const FLAG_UTF8 = 0x0800;
const FLAG_DESCRIPTOR = 0x0008;
const MADE_BY = (3 << 8) | 45; // UNIX, spec 4.5
const MODE_FILE = 0o100644;
const MODE_DIR = 0o40755;

function localHeader(e: CdEntry): Uint8Array {
  const extra = e.zip64Local ? 20 : 0;
  const r = new Rec(30 + e.name.length + extra)
    .u32(0x04034b50)
    .u16(e.zip64Local ? 45 : 20)
    .u16(FLAG_UTF8 | (e.dir ? 0 : FLAG_DESCRIPTOR))
    .u16(0)
    .u16(e.time)
    .u16(e.date)
    .u32(0)
    .u32(e.zip64Local ? U32 : 0)
    .u32(e.zip64Local ? U32 : 0)
    .u16(e.name.length)
    .u16(extra)
    .bytes(e.name);
  if (e.zip64Local) r.u16(0x0001).u16(16).u64(0).u64(0);
  return r.done();
}

function dataDescriptor(e: CdEntry): Uint8Array {
  if (e.zip64Local) {
    return new Rec(24).u32(0x08074b50).u32(e.crc).u64(e.size).u64(e.size).done();
  }
  return new Rec(16).u32(0x08074b50).u32(e.crc).u32(e.size).u32(e.size).done();
}

function centralHeader(e: CdEntry, force64: boolean): Uint8Array {
  const bigSize = force64 || e.size >= U32;
  const bigOff = force64 || e.offset >= U32;
  const extraLen = bigSize || bigOff ? 4 + (bigSize ? 16 : 0) + (bigOff ? 8 : 0) : 0;
  const r = new Rec(46 + e.name.length + extraLen)
    .u32(0x02014b50)
    .u16(MADE_BY)
    .u16(bigSize || bigOff || e.zip64Local ? 45 : 20)
    .u16(FLAG_UTF8 | (e.dir ? 0 : FLAG_DESCRIPTOR))
    .u16(0)
    .u16(e.time)
    .u16(e.date)
    .u32(e.crc)
    .u32(bigSize ? U32 : e.size)
    .u32(bigSize ? U32 : e.size)
    .u16(e.name.length)
    .u16(extraLen)
    .u16(0)
    .u16(0)
    .u16(0)
    .u32(((e.dir ? MODE_DIR : MODE_FILE) * 65536 + (e.dir ? 0x10 : 0)) >>> 0)
    .u32(bigOff ? U32 : e.offset)
    .bytes(e.name);
  if (extraLen) {
    r.u16(0x0001).u16(extraLen - 4);
    if (bigSize) r.u64(e.size).u64(e.size);
    if (bigOff) r.u64(e.offset);
  }
  return r.done();
}

function endRecords(count: number, cdOffset: number, cdSize: number, force64: boolean): Uint8Array[] {
  const out: Uint8Array[] = [];
  const need64 = force64 || count >= U16 || cdOffset >= U32 || cdSize >= U32;
  if (need64) {
    const eocd64Offset = cdOffset + cdSize;
    out.push(
      new Rec(56)
        .u32(0x06064b50)
        .u64(44)
        .u16(MADE_BY)
        .u16(45)
        .u32(0)
        .u32(0)
        .u64(count)
        .u64(count)
        .u64(cdSize)
        .u64(cdOffset)
        .done(),
    );
    out.push(new Rec(20).u32(0x07064b50).u32(0).u64(eocd64Offset).u32(1).done());
  }
  out.push(
    new Rec(22)
      .u32(0x06054b50)
      .u16(0)
      .u16(0)
      .u16(need64 ? U16 : count)
      .u16(need64 ? U16 : count)
      .u32(need64 ? U32 : cdSize)
      .u32(need64 ? U32 : cdOffset)
      .u16(0)
      .done(),
  );
  return out;
}

/** Normalise an entry name: `/` separators, no leading `/`, no `.`/`..` parts. */
export function zipEntryName(name: string, dir: boolean): string {
  const parts = name
    .replace(/\\/g, '/')
    .split('/')
    .filter((p) => p !== '' && p !== '.');
  if (parts.length === 0 || parts.some((p) => p === '..')) throw new Error(`zip: unsafe entry name ${JSON.stringify(name)}`);
  return parts.join('/') + (dir ? '/' : '');
}

async function* zipChunks(
  entries: AsyncIterable<ZipEntry> | Iterable<ZipEntry>,
  opts: ZipOptions,
): AsyncGenerator<Uint8Array> {
  const force64 = !!opts.forceZip64;
  const cd: CdEntry[] = [];
  let offset = 0;
  const emit = (b: Uint8Array) => {
    offset += b.length;
    opts.onProgress?.(offset);
    return b;
  };
  for await (const entry of entries) {
    const dir = entry.name.endsWith('/') && !entry.data;
    const name = enc.encode(zipEntryName(entry.name, dir));
    if (name.length > U16) throw new Error('zip: entry name too long');
    const { time, date } = dosTime(entry.mtime ?? new Date());
    const e: CdEntry = {
      name,
      dir,
      zip64Local: !dir && (force64 || entry.sizeHint === undefined || entry.sizeHint >= U32),
      crc: 0,
      size: 0,
      offset,
      time,
      date,
    };
    yield emit(localHeader(e));
    if (!dir && entry.data) {
      const stream = typeof entry.data === 'function' ? await entry.data() : entry.data;
      const reader = stream.getReader();
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          if (!value || value.length === 0) continue;
          e.crc = crc32(value, e.crc);
          e.size += value.length;
          yield emit(value);
        }
      } catch (err) {
        await reader.cancel(err).catch(() => undefined);
        throw err;
      } finally {
        reader.releaseLock();
      }
      if (!e.zip64Local && e.size >= U32) throw new Error(`zip: ${entry.name} grew past its size hint and 4 GiB`);
      yield emit(dataDescriptor(e));
    }
    cd.push(e);
  }
  const cdOffset = offset;
  for (const e of cd) yield emit(centralHeader(e, force64));
  const cdSize = offset - cdOffset;
  for (const r of endRecords(cd.length, cdOffset, cdSize, force64)) yield emit(r);
}

/**
 * A ZIP of `entries`, as a stream. Entries are read one after another, each
 * only when the consumer pulls; an entry whose stream errors errors the
 * archive (a partial archive is never a finished one).
 */
export function createZipStream(
  entries: AsyncIterable<ZipEntry> | Iterable<ZipEntry>,
  opts: ZipOptions = {},
): ReadableStream<Uint8Array> {
  const gen = zipChunks(entries, opts);
  return new ReadableStream<Uint8Array>({
    async pull(ctl) {
      const { value, done } = await gen.next();
      if (done) ctl.close();
      else ctl.enqueue(value);
    },
    async cancel() {
      await gen.return(undefined);
    },
  });
}
