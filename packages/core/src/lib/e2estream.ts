/**
 * e2estream — chunked authenticated encryption for content that does not fit
 * in memory (docs/E2E-ENCRYPTION.md → "Streaming content (STREAM)").
 *
 * WebCrypto ONLY, no dependencies. Two users:
 *
 *   - a single encrypted file (`.fxe`, lib/e2efile.ts), whose body is always
 *     a STREAM;
 *   - a file inside an encrypted folder that is larger than the one-shot
 *     limit (E2E_MAX_FILE_BYTES): header version 0x02, below.
 *
 * ── The construction ──────────────────────────────────────────────────
 *
 * STREAM (Hoang, Reyhanitabar, Rogaway, Vizár — "Online Authenticated-
 * Encryption and its Nonce-Reuse Misuse-Resistance", CRYPTO 2015) over
 * AES-256-GCM with a random 32-byte key per file:
 *
 *   plaintext  = P0 ‖ P1 ‖ … ‖ Pn-1        every Pi is 2^log2 bytes except
 *                                          Pn-1, which is 1 … 2^log2 bytes
 *                                          (0 bytes only when the whole
 *                                          plaintext is empty, then n = 1)
 *   nonce(i)   = prefix(7) ‖ uint32 BE i ‖ last(1)     last = 1 only for i = n-1
 *   chunk(i)   = AES-GCM(key, nonce(i), Pi)            ciphertext ‖ 16-byte tag
 *   body       = chunk(0) ‖ chunk(1) ‖ … ‖ chunk(n-1)
 *
 * The counter binds each chunk to its position (a reordered chunk fails its
 * tag), and the last-flag binds the END: a body cut at a chunk boundary ends
 * on a chunk that was sealed as "not last" and fails, and a chunk appended
 * after the real last one makes that one "not last" and it fails. No
 * associated data, like every other AES-GCM use in filex.
 *
 * ⚠ A streaming decryptor hands out the plaintext of chunk i before it has
 * seen chunk i+1, so a consumer MUST NOT treat what it received as the file
 * until the stream finished without an error: an error is "truncated,
 * reordered, extended or damaged", and whatever was already written is to be
 * thrown away (the browser's File System Access writable discards on abort;
 * `filex decrypt` writes into a partial directory it renames at the end).
 *
 * ── Folder file, version 0x02 ─────────────────────────────────────────
 *
 * Same 97-byte header as 0x01 up to offset 69, so a re-key re-wraps the DEK
 * of both versions the same way (only [9..69) changes):
 *
 *   [0..8)    magic "filexe2e"
 *   [8]       version 0x02
 *   [9..21)   wrapIV (12B) — GCM IV of the DEK wrap
 *   [21..69)  wrappedDEK (48B) = AES-GCM(FMK, wrapIV, DEK) — exactly as 0x01
 *   [69..76)  STREAM nonce prefix (7B)
 *   [76]      chunk size, log2 (writers: 20 = 1 MiB)
 *   [77..97)  zeros (readers ignore them)
 *   [97..)    STREAM body under the DEK
 *
 * Files up to E2E_MAX_FILE_BYTES keep 0x01, so every older filex reads them;
 * filex ≤ 0.47 refuses a 0x02 file ("unsupported version 2").
 */

import { E2eDecryptError, decryptFile } from './e2ecrypto';

/** Chunk size writers use: 2^20 = 1 MiB of plaintext per chunk. */
export const E2E_STREAM_CHUNK_LOG2 = 20;
/** The smallest chunk size a reader accepts (1 KiB). Writers never go below 20. */
export const E2E_STREAM_MIN_CHUNK_LOG2 = 10;
/** The largest chunk size a reader accepts (16 MiB) — a hostile header must not
 *  make a reader allocate gigabytes for one chunk. */
export const E2E_STREAM_MAX_CHUNK_LOG2 = 24;
/** Random nonce prefix per file. */
export const E2E_STREAM_PREFIX_LEN = 7;
/** AES-GCM tag, appended to every chunk. */
export const E2E_STREAM_TAG_LEN = 16;
/** Folder file header version of a STREAM body. */
export const E2E_FILE_VERSION_STREAM = 2;

const MAX_CHUNK_INDEX = 0xffffffff;
const IV_LEN = 12;
const HEADER_LEN = 97;
const WRAP_IV_OFF = 9;
const WRAPPED_DEK_OFF = 21;
const WRAPPED_DEK_LEN = 48;
const PREFIX_OFF = 69;
const LOG2_OFF = 76;
const DEK_LEN = 32;
const FOLDER_MAGIC = new TextEncoder().encode('filexe2e');

/** An integer chunk size a reader accepts. */
export function validChunkLog2(n: unknown): n is number {
  return (
    typeof n === 'number' &&
    Number.isInteger(n) &&
    n >= E2E_STREAM_MIN_CHUNK_LOG2 &&
    n <= E2E_STREAM_MAX_CHUNK_LOG2
  );
}

function chunkBytes(log2: number): number {
  if (!validChunkLog2(log2)) throw new Error(`e2e: unsupported chunk size 2^${log2}`);
  return 2 ** log2;
}

/** The 12-byte nonce of chunk `index`. */
export function streamNonce(prefix: Uint8Array, index: number, last: boolean): Uint8Array<ArrayBuffer> {
  if (prefix.length !== E2E_STREAM_PREFIX_LEN) throw new Error('e2e: the nonce prefix must be 7 bytes');
  if (!Number.isInteger(index) || index < 0 || index > MAX_CHUNK_INDEX) {
    throw new Error('e2e: too many chunks for one stream');
  }
  const n = new Uint8Array(IV_LEN);
  n.set(prefix, 0);
  new DataView(n.buffer).setUint32(E2E_STREAM_PREFIX_LEN, index, false);
  n[IV_LEN - 1] = last ? 1 : 0;
  return n;
}

/** How many chunks a plaintext of `plainSize` bytes is cut into. */
export function streamChunkCount(plainSize: number, log2: number = E2E_STREAM_CHUNK_LOG2): number {
  if (!Number.isSafeInteger(plainSize) || plainSize < 0) throw new Error('e2e: bad plaintext size');
  return plainSize === 0 ? 1 : Math.ceil(plainSize / chunkBytes(log2));
}

/** Bytes of STREAM body for a plaintext of `plainSize` bytes. */
export function streamCiphertextSize(plainSize: number, log2: number = E2E_STREAM_CHUNK_LOG2): number {
  return plainSize + E2E_STREAM_TAG_LEN * streamChunkCount(plainSize, log2);
}

/**
 * The plaintext size a STREAM body of `cipherSize` bytes carries, or null when
 * no writer could have produced that length (a partial tag, or an empty final
 * chunk after a full one).
 */
export function streamPlaintextSize(cipherSize: number, log2: number = E2E_STREAM_CHUNK_LOG2): number | null {
  if (!validChunkLog2(log2) || !Number.isSafeInteger(cipherSize) || cipherSize < E2E_STREAM_TAG_LEN) return null;
  const full = 2 ** log2 + E2E_STREAM_TAG_LEN;
  const n = Math.ceil(cipherSize / full);
  const last = cipherSize - (n - 1) * full;
  if (last < E2E_STREAM_TAG_LEN) return null;
  if (n > 1 && last === E2E_STREAM_TAG_LEN) return null;
  return cipherSize - E2E_STREAM_TAG_LEN * n;
}

/** A random 7-byte nonce prefix. */
export function randomStreamPrefix(): Uint8Array<ArrayBuffer> {
  return crypto.getRandomValues(new Uint8Array(E2E_STREAM_PREFIX_LEN));
}

// ---------------------------------------------------------------------
// Byte plumbing
// ---------------------------------------------------------------------

/** A FIFO of byte slices that hands out exactly-sized pieces. */
export class ByteQueue {
  private parts: Uint8Array[] = [];
  private head = 0;
  length = 0;

  push(b: Uint8Array): void {
    if (b.length === 0) return;
    this.parts.push(b);
    this.length += b.length;
  }

  /** The next `n` bytes (n ≤ length), as a fresh buffer. */
  take(n: number): Uint8Array<ArrayBuffer> {
    if (n > this.length) throw new Error('ByteQueue: not enough bytes');
    const out = new Uint8Array(n);
    let o = 0;
    while (o < n) {
      const p = this.parts[0];
      const k = Math.min(p.length - this.head, n - o);
      out.set(p.subarray(this.head, this.head + k), o);
      o += k;
      this.head += k;
      if (this.head === p.length) {
        this.parts.shift();
        this.head = 0;
      }
    }
    this.length -= n;
    return out;
  }
}

function asBytes(chunk: unknown): Uint8Array {
  if (chunk instanceof Uint8Array) return chunk;
  if (chunk instanceof ArrayBuffer) return new Uint8Array(chunk);
  if (ArrayBuffer.isView(chunk)) return new Uint8Array(chunk.buffer, chunk.byteOffset, chunk.byteLength);
  throw new TypeError('e2e: a stream carried something that is not bytes');
}

/**
 * Reads a byte stream in exact pieces, and hands back what is left of it as
 * a stream of its own. Used to take a header off the front of a download.
 */
export class ByteStreamReader {
  private reader: ReadableStreamDefaultReader<Uint8Array>;
  private q = new ByteQueue();
  private done = false;

  constructor(stream: ReadableStream<Uint8Array>) {
    this.reader = stream.getReader();
  }

  /** Up to `n` bytes; fewer only when the stream ended. */
  async read(n: number): Promise<Uint8Array<ArrayBuffer>> {
    while (this.q.length < n && !this.done) {
      const { done, value } = await this.reader.read();
      if (done) this.done = true;
      else this.q.push(asBytes(value));
    }
    return this.q.take(Math.min(n, this.q.length));
  }

  /** Everything not read yet, as a stream. The reader is spent afterwards. */
  rest(): ReadableStream<Uint8Array> {
    const left = this.q.take(this.q.length);
    const reader = this.reader;
    let first = left.length > 0 ? left : null;
    const ended = this.done;
    return new ReadableStream<Uint8Array>({
      async pull(ctl) {
        if (first) {
          ctl.enqueue(first);
          first = null;
          return;
        }
        if (ended) {
          ctl.close();
          return;
        }
        const { done, value } = await reader.read();
        if (done) ctl.close();
        else ctl.enqueue(asBytes(value));
      },
      cancel(reason) {
        return reader.cancel(reason);
      },
    });
  }

  cancel(reason?: unknown): Promise<void> {
    return this.reader.cancel(reason);
  }
}

/** A stream that yields `head` first, then everything `rest` yields. */
export function prependBytes(head: Uint8Array, rest: ReadableStream<Uint8Array>): ReadableStream<Uint8Array> {
  const reader = rest.getReader();
  let sent = false;
  return new ReadableStream<Uint8Array>({
    async pull(ctl) {
      if (!sent) {
        sent = true;
        if (head.length > 0) {
          ctl.enqueue(head);
          return;
        }
      }
      const { done, value } = await reader.read();
      if (done) ctl.close();
      else ctl.enqueue(asBytes(value));
    },
    cancel(reason) {
      return reader.cancel(reason);
    },
  });
}

/** A stream of the given bytes, in pieces of at most `piece` bytes. */
export function bytesStream(b: Uint8Array, piece = 64 * 1024): ReadableStream<Uint8Array> {
  let off = 0;
  return new ReadableStream<Uint8Array>({
    pull(ctl) {
      if (off >= b.length) {
        ctl.close();
        return;
      }
      const end = Math.min(off + piece, b.length);
      ctl.enqueue(b.slice(off, end));
      off = end;
    },
  });
}

/** Everything a stream yields, joined. Only for data known to be small. */
export async function collectBytes(stream: ReadableStream<Uint8Array>, limit = Number.MAX_SAFE_INTEGER): Promise<Uint8Array<ArrayBuffer>> {
  const q = new ByteQueue();
  const reader = stream.getReader();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    q.push(asBytes(value));
    if (q.length > limit) {
      await reader.cancel();
      throw new Error('e2e: more data than expected');
    }
  }
  return q.take(q.length);
}

// ---------------------------------------------------------------------
// Chunks
// ---------------------------------------------------------------------

async function sealChunk(
  key: CryptoKey,
  prefix: Uint8Array,
  index: number,
  last: boolean,
  plain: Uint8Array<ArrayBuffer>,
): Promise<Uint8Array<ArrayBuffer>> {
  return new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: streamNonce(prefix, index, last) }, key, plain),
  );
}

async function openChunk(
  key: CryptoKey,
  prefix: Uint8Array,
  index: number,
  last: boolean,
  ct: Uint8Array<ArrayBuffer>,
): Promise<Uint8Array<ArrayBuffer>> {
  try {
    return new Uint8Array(
      await crypto.subtle.decrypt({ name: 'AES-GCM', iv: streamNonce(prefix, index, last) }, key, ct),
    );
  } catch {
    throw new E2eDecryptError(
      last
        ? 'e2e: the last chunk failed authentication (the file is truncated, extended or damaged)'
        : `e2e: chunk ${index} failed authentication (the file is damaged, reordered or truncated)`,
    );
  }
}

export interface StreamEncryptOptions {
  /** Refuse a plaintext that is not exactly this long (the header promised it). */
  expectSize?: number;
  /** Called with the plaintext bytes consumed so far. */
  onProgress?: (bytes: number) => void;
}

/** Plaintext in, STREAM body out. */
export function createStreamEncryptor(
  key: CryptoKey,
  prefix: Uint8Array,
  log2: number = E2E_STREAM_CHUNK_LOG2,
  opts: StreamEncryptOptions = {},
): TransformStream<Uint8Array, Uint8Array> {
  const size = chunkBytes(log2);
  if (prefix.length !== E2E_STREAM_PREFIX_LEN) throw new Error('e2e: the nonce prefix must be 7 bytes');
  const q = new ByteQueue();
  let index = 0;
  let seen = 0;
  return new TransformStream<Uint8Array, Uint8Array>({
    async transform(chunk, ctl) {
      const b = asBytes(chunk);
      seen += b.length;
      if (opts.expectSize !== undefined && seen > opts.expectSize) {
        throw new Error('e2e: the file is longer than it said it was');
      }
      q.push(b);
      // Strictly more than a chunk: the last chunk has to be known to be the
      // last one, so a full chunk is held back until more data (or the end)
      // arrives.
      while (q.length > size) {
        ctl.enqueue(await sealChunk(key, prefix, index++, false, q.take(size)));
        opts.onProgress?.(index * size);
      }
    },
    async flush(ctl) {
      if (opts.expectSize !== undefined && seen !== opts.expectSize) {
        throw new Error('e2e: the file is shorter than it said it was');
      }
      ctl.enqueue(await sealChunk(key, prefix, index++, true, q.take(q.length)));
      opts.onProgress?.(seen);
    },
  });
}

export interface StreamDecryptOptions {
  /** Fail when the plaintext is not exactly this long (the header said so). */
  expectSize?: number;
}

/** STREAM body in, plaintext out. Errors with E2eDecryptError on any tampering. */
export function createStreamDecryptor(
  key: CryptoKey,
  prefix: Uint8Array,
  log2: number = E2E_STREAM_CHUNK_LOG2,
  opts: StreamDecryptOptions = {},
): TransformStream<Uint8Array, Uint8Array> {
  const full = chunkBytes(log2) + E2E_STREAM_TAG_LEN;
  if (prefix.length !== E2E_STREAM_PREFIX_LEN) throw new Error('e2e: the nonce prefix must be 7 bytes');
  const q = new ByteQueue();
  let index = 0;
  let out = 0;
  const count = (b: Uint8Array) => {
    out += b.length;
    if (opts.expectSize !== undefined && out > opts.expectSize) {
      throw new E2eDecryptError('e2e: the file holds more than its header says');
    }
    return b;
  };
  return new TransformStream<Uint8Array, Uint8Array>({
    async transform(chunk, ctl) {
      q.push(asBytes(chunk));
      while (q.length > full) ctl.enqueue(count(await openChunk(key, prefix, index++, false, q.take(full))));
    },
    async flush(ctl) {
      if (q.length < E2E_STREAM_TAG_LEN) throw new E2eDecryptError('e2e: the encrypted content is truncated');
      if (index > 0 && q.length === E2E_STREAM_TAG_LEN) {
        throw new E2eDecryptError('e2e: the encrypted content ends in an empty chunk no writer produces');
      }
      ctl.enqueue(count(await openChunk(key, prefix, index++, true, q.take(q.length))));
      if (opts.expectSize !== undefined && out !== opts.expectSize) {
        throw new E2eDecryptError('e2e: the file holds less than its header says');
      }
    },
  });
}

/** One-shot STREAM encryption of bytes in memory (small content and tests). */
export async function encryptStreamBytes(
  key: CryptoKey,
  prefix: Uint8Array,
  plain: Uint8Array,
  log2: number = E2E_STREAM_CHUNK_LOG2,
): Promise<Uint8Array<ArrayBuffer>> {
  return collectBytes(bytesStream(plain).pipeThrough(createStreamEncryptor(key, prefix, log2)));
}

/** One-shot STREAM decryption of bytes in memory. */
export async function decryptStreamBytes(
  key: CryptoKey,
  prefix: Uint8Array,
  ct: Uint8Array,
  log2: number = E2E_STREAM_CHUNK_LOG2,
): Promise<Uint8Array<ArrayBuffer>> {
  const size = chunkBytes(log2);
  const full = size + E2E_STREAM_TAG_LEN;
  const plain = streamPlaintextSize(ct.length, log2);
  if (plain === null) throw new E2eDecryptError('e2e: the encrypted content has an impossible length');
  const out = new Uint8Array(plain);
  const n = streamChunkCount(plain, log2);
  for (let i = 0; i < n; i++) {
    const last = i === n - 1;
    const piece = ct.slice(i * full, last ? ct.length : (i + 1) * full);
    out.set(await openChunk(key, prefix, i, last, piece), i * size);
  }
  return out;
}

// ---------------------------------------------------------------------
// The DEK
// ---------------------------------------------------------------------

/** A fresh random 32-byte data key, raw. The caller zeroes it. */
export function randomDek(): Uint8Array<ArrayBuffer> {
  return crypto.getRandomValues(new Uint8Array(DEK_LEN));
}

/** Import raw DEK bytes as the AES-256-GCM key STREAM runs under. */
export async function importDek(raw: Uint8Array<ArrayBuffer>): Promise<CryptoKey> {
  if (raw.length !== DEK_LEN) throw new E2eDecryptError('e2e: a file key must be 32 bytes');
  return crypto.subtle.importKey('raw', raw, { name: 'AES-GCM' }, false, ['encrypt', 'decrypt']);
}

// ---------------------------------------------------------------------
// Folder files, header version 0x02
// ---------------------------------------------------------------------

export interface StreamFolderHeader {
  wrapIV: Uint8Array<ArrayBuffer>;
  wrappedDek: Uint8Array<ArrayBuffer>;
  prefix: Uint8Array<ArrayBuffer>;
  log2: number;
}

/** The 97-byte header of a 0x02 folder file. */
export function buildStreamFolderHeader(h: StreamFolderHeader): Uint8Array<ArrayBuffer> {
  if (h.wrapIV.length !== IV_LEN || h.wrappedDek.length !== WRAPPED_DEK_LEN) {
    throw new Error('e2e: bad wrapped key');
  }
  chunkBytes(h.log2);
  const out = new Uint8Array(HEADER_LEN);
  out.set(FOLDER_MAGIC, 0);
  out[8] = E2E_FILE_VERSION_STREAM;
  out.set(h.wrapIV, WRAP_IV_OFF);
  out.set(h.wrappedDek, WRAPPED_DEK_OFF);
  out.set(h.prefix, PREFIX_OFF);
  out[LOG2_OFF] = h.log2;
  return out;
}

/** Parse a 0x02 header. Throws a plain Error for anything that is not one. */
export function parseStreamFolderHeader(b: Uint8Array): StreamFolderHeader {
  if (b.length < HEADER_LEN) throw new Error('e2e: not an encrypted file');
  for (let i = 0; i < FOLDER_MAGIC.length; i++) {
    if (b[i] !== FOLDER_MAGIC[i]) throw new Error('e2e: not an encrypted file');
  }
  if (b[8] !== E2E_FILE_VERSION_STREAM) throw new Error(`e2e: unsupported version ${b[8]}`);
  const log2 = b[LOG2_OFF];
  if (!validChunkLog2(log2)) throw new Error(`e2e: unsupported chunk size 2^${log2}`);
  return {
    wrapIV: b.slice(WRAP_IV_OFF, WRAP_IV_OFF + IV_LEN),
    wrappedDek: b.slice(WRAPPED_DEK_OFF, WRAPPED_DEK_OFF + WRAPPED_DEK_LEN),
    prefix: b.slice(PREFIX_OFF, PREFIX_OFF + E2E_STREAM_PREFIX_LEN),
    log2,
  };
}

/** Bytes of a 0x02 folder file carrying `plainSize` bytes. */
export function streamFolderFileSize(plainSize: number, log2: number = E2E_STREAM_CHUNK_LOG2): number {
  return HEADER_LEN + streamCiphertextSize(plainSize, log2);
}

/**
 * Unwrap a file's DEK with the folder key, or — for a folder mid re-key —
 * the previous folder key. Throws E2eDecryptError when neither opens it.
 */
export async function unwrapFolderDek(
  fmk: CryptoKey,
  previous: CryptoKey | null | undefined,
  wrapIV: Uint8Array<ArrayBuffer>,
  wrapped: Uint8Array<ArrayBuffer>,
): Promise<CryptoKey> {
  for (const k of previous ? [fmk, previous] : [fmk]) {
    let raw: Uint8Array<ArrayBuffer>;
    try {
      raw = new Uint8Array(await crypto.subtle.decrypt({ name: 'AES-GCM', iv: wrapIV }, k, wrapped));
    } catch {
      continue;
    }
    try {
      return await importDek(raw);
    } finally {
      raw.fill(0);
    }
  }
  throw new E2eDecryptError('e2e: DEK unwrap failed (wrong key?)');
}

export interface FolderFileEncryptOptions {
  /** Chunk size, log2. Tests only — writers use E2E_STREAM_CHUNK_LOG2. */
  chunkLog2?: number;
  onProgress?: (bytes: number) => void;
}

/**
 * Encrypt a file for an encrypted folder as a 0x02 STREAM file: a fresh DEK
 * wrapped under the folder key exactly as 0x01 wraps it, then the body.
 * `plainSize` must be exact — the stream errors when the source disagrees.
 */
export async function encryptFolderFileStream(
  fmk: CryptoKey,
  plainSize: number,
  src: ReadableStream<Uint8Array>,
  opts: FolderFileEncryptOptions = {},
): Promise<{ stream: ReadableStream<Uint8Array>; size: number }> {
  const log2 = opts.chunkLog2 ?? E2E_STREAM_CHUNK_LOG2;
  const rawDek = randomDek();
  const wrapIV = crypto.getRandomValues(new Uint8Array(IV_LEN));
  let dek: CryptoKey;
  let wrappedDek: Uint8Array<ArrayBuffer>;
  try {
    wrappedDek = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: wrapIV }, fmk, rawDek));
    dek = await importDek(rawDek);
  } finally {
    rawDek.fill(0);
  }
  const prefix = randomStreamPrefix();
  const header = buildStreamFolderHeader({ wrapIV, wrappedDek, prefix, log2 });
  const body = src.pipeThrough(
    createStreamEncryptor(dek, prefix, log2, { expectSize: plainSize, onProgress: opts.onProgress }),
  );
  return { stream: prependBytes(header, body), size: streamFolderFileSize(plainSize, log2) };
}

/** Decrypt a whole 0x02 file held in memory (what `decryptFile` delegates to). */
export async function decryptStreamFolderFileBytes(
  fmk: CryptoKey,
  data: Uint8Array,
  previous?: CryptoKey | null,
): Promise<ArrayBuffer> {
  const h = parseStreamFolderHeader(data);
  const dek = await unwrapFolderDek(fmk, previous, h.wrapIV, h.wrappedDek);
  const plain = await decryptStreamBytes(dek, h.prefix, data.subarray(HEADER_LEN), h.log2);
  return plain.buffer;
}

/** The most a 0x01 file can be (one-shot content up to the limit + header + tag). */
const V1_MAX_TOTAL = 200 * 1024 * 1024 + HEADER_LEN + 16;

/**
 * Decrypt a folder file as a stream, whichever version it is. A 0x01 file is
 * one AES-GCM message, so it is gathered (it is at most 200 MB) and opened in
 * one piece; a 0x02 file streams. A stream without the magic is passed
 * through as it is when `passPlain` (a file written in the clear over DAV),
 * and refused otherwise.
 */
export function decryptFolderFileStream(
  fmk: CryptoKey,
  previous: CryptoKey | null | undefined,
  ct: ReadableStream<Uint8Array>,
  opts: { passPlain?: boolean; onPlain?: () => void } = {},
): ReadableStream<Uint8Array> {
  const r = new ByteStreamReader(ct);
  let inner: ReadableStreamDefaultReader<Uint8Array> | null = null;
  return new ReadableStream<Uint8Array>({
    async start(ctl) {
      const head = await r.read(HEADER_LEN);
      const magic = head.length >= 8 && FOLDER_MAGIC.every((c, i) => head[i] === c);
      if (!magic) {
        if (!opts.passPlain) throw new Error('e2e: not an encrypted file');
        opts.onPlain?.();
        inner = prependBytes(head, r.rest()).getReader();
        return;
      }
      if (head.length < HEADER_LEN) throw new E2eDecryptError('e2e: the encrypted file is truncated');
      if (head[8] === E2E_FILE_VERSION_STREAM) {
        const h = parseStreamFolderHeader(head);
        const dek = await unwrapFolderDek(fmk, previous, h.wrapIV, h.wrappedDek);
        inner = r.rest().pipeThrough(createStreamDecryptor(dek, h.prefix, h.log2)).getReader();
        return;
      }
      if (head[8] !== 1) throw new Error(`e2e: unsupported version ${head[8]}`);
      const body = await collectBytes(r.rest(), V1_MAX_TOTAL - HEADER_LEN);
      const whole = new Uint8Array(HEADER_LEN + body.length);
      whole.set(head, 0);
      whole.set(body, HEADER_LEN);
      let plain: ArrayBuffer;
      try {
        plain = await decryptFile(fmk, whole.buffer);
      } catch (err) {
        if (!previous || !(err instanceof E2eDecryptError)) throw err;
        plain = await decryptFile(previous, whole.buffer);
      }
      ctl.enqueue(new Uint8Array(plain));
      ctl.close();
    },
    async pull(ctl) {
      if (!inner) return;
      const { done, value } = await inner.read();
      if (done) ctl.close();
      else ctl.enqueue(value);
    },
    cancel(reason) {
      return inner ? inner.cancel(reason) : r.cancel(reason);
    },
  });
}

/** The size of the header a folder file starts with, for either version. */
export const E2E_FOLDER_HEADER_LEN = HEADER_LEN;
