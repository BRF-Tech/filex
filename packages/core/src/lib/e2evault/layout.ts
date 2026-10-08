/**
 * e2evault/layout — the vault's constants, paths and byte primitives
 * (docs/E2E-VAULT-FORMAT.md → "Layout", "Packs", "The index").
 *
 * ⚠ With consts.ts, the one place the TypeScript side keeps these numbers. The Go side keeps
 * the same ones in backend/internal/e2e/vault.go; the test vectors
 * (backend/internal/e2edecrypt/testdata/vault-vectors.json) hold both to the
 * format, byte for byte.
 *
 * No WebCrypto here, no Vue: pure functions over bytes.
 *
 * The numbers code outside the vault needs while no vault is open (the key
 * file's rules, the pack sizes offered, the entry limit) live in consts.ts so
 * the main chunk carries only those; they are re-exported here, and every
 * import from this file keeps working.
 */
export {
  VAULT_DEFAULT_PACK_LOG2,
  VAULT_ENTRIES_WRITE_MAX,
  VAULT_FEATURE,
  VAULT_FORMAT,
  VAULT_ID_LEN,
  VAULT_LARGE_PACK_LOG2,
  VAULT_MAX_PACK_LOG2,
  VAULT_MIN_PACK_LOG2,
  VAULT_WRITER_PACK_LOG2,
  validReaderPackLog2,
} from './consts';

export const VAULT_PACK_HEADER_LEN = 32;
export const VAULT_INDEX_HEADER_LEN = 40;
export const VAULT_TAG_LEN = 16;
/** Every index file is at least this large (64 KiB). */
export const VAULT_INDEX_MIN_SIZE = 65536;
/** A reader refuses an index file larger than this, before decrypting it. */
export const VAULT_INDEX_READ_MAX = 64 * 1024 * 1024;
/** A writer refuses a change whose index file would be larger than this. */
export const VAULT_INDEX_WRITE_MAX = 32 * 1024 * 1024;
/** From this index file size every client warns before a change. */
export const VAULT_INDEX_WARN = 24 * 1024 * 1024;
export const VAULT_KIND_PACK = 0x50; // 'P'
export const VAULT_KIND_INDEX = 0x49; // 'I'
/** The newest generations that are always kept. */
export const VAULT_KEEP_GENERATIONS = 3;
/** A generation replaced less than this long ago is kept too. */
export const VAULT_RETENTION_MS = 15 * 60 * 1000;
/** A writer warns from this many entries (it refuses beyond
 *  VAULT_ENTRIES_WRITE_MAX, consts.ts). */
export const VAULT_ENTRIES_WARN = 200_000;
/** A reader refuses an index with more entries than this. */
export const VAULT_ENTRIES_READ_MAX = 1_000_000;
/** The root's children are depth 1. */
export const VAULT_MAX_DEPTH = 256;
/** A name, in UTF-8 bytes. */
export const VAULT_NAME_MAX = 255;
/** Chunk size writers use for file contents (STREAM): 2^20. */
export const VAULT_CHUNK_LOG2 = 20;
export const VAULT_MIN_CHUNK_LOG2 = 10;
export const VAULT_MAX_CHUNK_LOG2 = 24;
/** At most this many names in one `POST .../delete`, and per collection pass. */
export const VAULT_DELETE_MAX = 1000;
/** The largest integer a uvarint may carry (a JavaScript number holds it exactly). */
export const VAULT_UVARINT_MAX = Number.MAX_SAFE_INTEGER; // 2^53 - 1

/** Entry kinds. */
export const VAULT_ENTRY_FOLDER = 1;
export const VAULT_ENTRY_FILE = 2;
/** Content kinds. */
export const VAULT_CONTENT_NONE = 0;
export const VAULT_CONTENT_STREAM = 1;

/** The 8-byte magic of packs and index files. */
export const VAULT_MAGIC = new TextEncoder().encode('filexvlt');

/** HKDF labels (ASCII); the 16-byte id follows them raw. */
export const VAULT_INFO_INDEX = 'filex-vault-index-v1';
export const VAULT_INFO_CONTENT = 'filex-vault-content-v1';

/**
 * A malformed vault object: an index, a pack header, a key file. `code` names
 * the rule that was broken, for tests and for the words a person reads.
 */
export class VaultFormatError extends Error {
  constructor(
    public readonly code: string,
    message?: string,
  ) {
    super(message ?? `vault: ${code}`);
    this.name = 'VaultFormatError';
  }
}

// ---------------------------------------------------------------------
// Bytes
// ---------------------------------------------------------------------

/** A view WebCrypto accepts (TS 5.9 refuses views over a SharedArrayBuffer). */
export function bufferView(b: Uint8Array): Uint8Array<ArrayBuffer> {
  return (b.buffer instanceof ArrayBuffer ? b : new Uint8Array(b)) as Uint8Array<ArrayBuffer>;
}

export function toHex(b: Uint8Array): string {
  let s = '';
  for (let i = 0; i < b.length; i++) s += b[i].toString(16).padStart(2, '0');
  return s;
}

/** Strict lower-case hex; null for anything else. */
export function fromHex(s: string): Uint8Array<ArrayBuffer> | null {
  if (typeof s !== 'string' || s.length % 2 !== 0 || !/^[0-9a-f]*$/.test(s)) return null;
  const out = new Uint8Array(s.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(s.slice(i * 2, i * 2 + 2), 16);
  return out;
}

/** Byte order: byte by byte, a shorter one first when it is a prefix. */
export function compareBytes(a: Uint8Array, b: Uint8Array): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

export function equalBytes(a: Uint8Array, b: Uint8Array): boolean {
  return a.length === b.length && compareBytes(a, b) === 0;
}

export function concatBytes(parts: Uint8Array[]): Uint8Array<ArrayBuffer> {
  let n = 0;
  for (const p of parts) n += p.length;
  const out = new Uint8Array(n);
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
}

const UTF8 = new TextEncoder();
const UTF8_STRICT = new TextDecoder('utf-8', { fatal: true });

export function utf8(s: string): Uint8Array<ArrayBuffer> {
  return UTF8.encode(s) as Uint8Array<ArrayBuffer>;
}

/** Decode UTF-8; null for bytes that are not UTF-8. */
export function utf8Decode(b: Uint8Array): string | null {
  try {
    return UTF8_STRICT.decode(b);
  } catch {
    return null;
  }
}

/** Compare two names the way the format orders siblings: as UTF-8 bytes. */
export function compareNames(a: string, b: string): number {
  return compareBytes(utf8(a), utf8(b));
}

// ---------------------------------------------------------------------
// uvarint: unsigned LEB128, minimal, at most 2^53 - 1
// ---------------------------------------------------------------------

/** Encode `n` as a uvarint. Throws for a value the format cannot carry. */
export function uvarint(n: number): Uint8Array<ArrayBuffer> {
  if (!Number.isSafeInteger(n) || n < 0) throw new VaultFormatError('uvarint_range', `vault: uvarint out of range: ${n}`);
  const out: number[] = [];
  let v = n;
  for (;;) {
    const low = v % 128;
    v = Math.floor(v / 128);
    if (v === 0) {
      out.push(low);
      break;
    }
    out.push(low | 0x80);
  }
  return new Uint8Array(out);
}

/** A growable byte buffer for building a body. */
export class ByteWriter {
  private buf = new Uint8Array(1024);
  length = 0;

  private room(n: number): void {
    if (this.length + n <= this.buf.length) return;
    let cap = this.buf.length * 2;
    while (cap < this.length + n) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this.buf.subarray(0, this.length));
    this.buf = next;
  }

  byte(b: number): void {
    this.room(1);
    this.buf[this.length++] = b & 0xff;
  }

  bytes(b: Uint8Array): void {
    this.room(b.length);
    this.buf.set(b, this.length);
    this.length += b.length;
  }

  uvarint(n: number): void {
    this.bytes(uvarint(n));
  }

  /** The bytes written, as a fresh buffer. */
  done(): Uint8Array<ArrayBuffer> {
    return this.buf.slice(0, this.length);
  }
}

/** Reads a body front to back; every read past the end is `truncated`. */
export class ByteReader {
  pos = 0;

  constructor(
    private readonly b: Uint8Array,
    private readonly end: number = b.length,
  ) {}

  get left(): number {
    return this.end - this.pos;
  }

  byte(): number {
    if (this.pos >= this.end) throw new VaultFormatError('truncated');
    return this.b[this.pos++];
  }

  bytes(n: number): Uint8Array<ArrayBuffer> {
    if (!Number.isSafeInteger(n) || n < 0 || this.pos + n > this.end) throw new VaultFormatError('truncated');
    const out = this.b.slice(this.pos, this.pos + n) as Uint8Array<ArrayBuffer>;
    this.pos += n;
    return out;
  }

  /** A minimal uvarint of at most 2^53 - 1; anything else is refused. */
  uvarint(): number {
    let value = 0;
    let scale = 1;
    for (let i = 0; ; i++) {
      // 2^53 - 1 needs 8 bytes; a 9th is always too large.
      if (i >= 8) throw new VaultFormatError('uvarint_range');
      const b = this.byte();
      value += (b & 0x7f) * scale;
      if ((b & 0x80) === 0) {
        if (i > 0 && b === 0) throw new VaultFormatError('uvarint_nonminimal');
        break;
      }
      scale *= 128;
    }
    if (value > VAULT_UVARINT_MAX) throw new VaultFormatError('uvarint_range');
    return value;
  }
}

/** Decode one uvarint from the start of `b`: its value and its length. */
export function readUvarint(b: Uint8Array): { value: number; length: number } {
  const r = new ByteReader(b);
  const value = r.uvarint();
  return { value, length: r.pos };
}

// ---------------------------------------------------------------------
// Padmé and the index file's size
// ---------------------------------------------------------------------

function bitLength(n: number): number {
  let b = 0;
  let v = n;
  while (v >= 1) {
    b++;
    v = Math.floor(v / 2);
  }
  return b;
}

/**
 * Padmé (Nikitin et al., PETS 2019): `E = ⌊log2 L⌋`, `S = ⌊log2 E⌋ + 1`,
 * `step = 2^(E - S)`, the length rounded up to a multiple of `step`.
 */
export function padme(L: number): number {
  if (!Number.isSafeInteger(L) || L < 1) throw new VaultFormatError('padme_range');
  const E = bitLength(L) - 1;
  const S = bitLength(E);
  const step = 2 ** Math.max(0, E - S);
  return Math.ceil(L / step) * step;
}

/** The size of the index file that holds a body of `bodyLen` bytes. */
export function indexFileSize(bodyLen: number): number {
  return Math.max(VAULT_INDEX_MIN_SIZE, padme(VAULT_INDEX_HEADER_LEN + bodyLen + VAULT_TAG_LEN));
}

/** 65 536 ≤ n ≤ 64 MiB and Padmé(n) = n: the sizes an index file can have. */
export function validIndexFileSize(n: number): boolean {
  return Number.isSafeInteger(n) && n >= VAULT_INDEX_MIN_SIZE && n <= VAULT_INDEX_READ_MAX && padme(n) === n;
}

// ---------------------------------------------------------------------
// Paths (relative to the vault folder)
// ---------------------------------------------------------------------

/** `v/p/b0/b067….fxp`: a pack, by its 16-byte id. */
export function packPath(id: Uint8Array | string): string {
  const h = typeof id === 'string' ? id : toHex(id);
  return `v/p/${h.slice(0, 2)}/${h}.fxp`;
}

/** `v/idx/0000000000000003.fxi`: the index of one generation. */
export function indexPath(gen: number): string {
  return `v/idx/${generationHex(gen)}.fxi`;
}

/** A generation as 16 lower-case hex digits. */
export function generationHex(gen: number): string {
  if (!Number.isSafeInteger(gen) || gen < 0) throw new VaultFormatError('generation_range');
  return gen.toString(16).padStart(16, '0');
}

export type VaultPathKind = 'other' | 'keyfile' | 'pack' | 'index' | 'temp';

/** What a path relative to the vault root is (the Go `ParseVaultPath`). */
export function parseVaultPath(rel: string): { kind: VaultPathKind; id?: string; gen?: number } {
  const p = String(rel ?? '').replace(/^\/+/, '');
  if (p === '.filex-e2e.json') return { kind: 'keyfile' };
  let m = /^v\/p\/([0-9a-f]{2})\/([0-9a-f]{32})\.fxp$/.exec(p);
  if (m && m[2].startsWith(m[1])) return { kind: 'pack', id: m[2] };
  m = /^v\/idx\/([0-9a-f]{16})\.fxi$/.exec(p);
  if (m) {
    const gen = parseInt(m[1], 16);
    if (Number.isSafeInteger(gen)) return { kind: 'index', gen };
    return { kind: 'other' };
  }
  if (/^v\/idx\/\.tmp-[^/]+$/.test(p)) return { kind: 'temp' };
  return { kind: 'other' };
}

/** A valid pack id in its wire spelling: 32 lower-case hex digits. */
export function isPackIdHex(s: unknown): s is string {
  return typeof s === 'string' && /^[0-9a-f]{32}$/.test(s);
}

/** The number of bytes of a pack's data area. */
export function packDataSize(packLog2: number): number {
  return 2 ** packLog2 - VAULT_PACK_HEADER_LEN;
}

/** A chunk size a reader accepts. */
export function validChunkLog2(n: unknown): n is number {
  return typeof n === 'number' && Number.isInteger(n) && n >= VAULT_MIN_CHUNK_LOG2 && n <= VAULT_MAX_CHUNK_LOG2;
}

/** STREAM body length of `size` plaintext bytes in chunks of 2^log2. */
export function streamBodySize(size: number, log2: number): number {
  return size + VAULT_TAG_LEN * Math.max(1, Math.ceil(size / 2 ** log2));
}

/**
 * The rules a name must meet (docs/E2E-VAULT-FORMAT.md → "Index body",
 * `name`): 1 to 255 UTF-8 bytes; not `.` or `..`; no `/`, `\`, 0x00-0x1F or
 * 0x7F. Null when it meets them, else what is wrong.
 */
export function vaultNameProblem(bytes: Uint8Array): 'empty' | 'too_long' | 'dot' | 'slash' | 'control' | null {
  if (bytes.length === 0) return 'empty';
  if (bytes.length > VAULT_NAME_MAX) return 'too_long';
  if (bytes.length === 1 && bytes[0] === 0x2e) return 'dot';
  if (bytes.length === 2 && bytes[0] === 0x2e && bytes[1] === 0x2e) return 'dot';
  for (let i = 0; i < bytes.length; i++) {
    const c = bytes[i];
    if (c === 0x2f || c === 0x5c) return 'slash';
    if (c <= 0x1f || c === 0x7f) return 'control';
  }
  return null;
}
