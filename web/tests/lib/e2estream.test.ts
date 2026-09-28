// STREAM content and the 0x02 folder file (docs/E2E-ENCRYPTION.md →
// "Streaming content (STREAM)").
//
// The vectors come from Python `cryptography` (backend/internal/e2edecrypt/
// testdata/gen_stream_vectors.py), not from this code: the claim is that the
// browser writes and reads exactly what an independent implementation of the
// normative format does. The Go decryptor is held to the same file.
//
// Most tests run at a small chunk size (2^10) so a handful of bytes spans
// many chunks — the logic is the same at 2^20, which writers always use and
// which the vectors and the >200 MB pass also cover.

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

import { describe, expect, it } from 'vitest';

import {
  E2E_MAX_FILE_BYTES,
  E2eDecryptError,
  createEncryptedFolder,
  decryptFile,
  decryptFileAny,
  encryptFile,
  rewrapFileKey,
  startRekey,
  unlockWithPassword,
  type E2eMarker,
} from '../../../packages/core/src/lib/e2ecrypto';
import {
  E2E_FILE_VERSION_STREAM,
  E2E_STREAM_CHUNK_LOG2,
  bytesStream,
  collectBytes,
  createStreamDecryptor,
  createStreamEncryptor,
  decryptFolderFileStream,
  decryptStreamBytes,
  encryptFolderFileStream,
  encryptStreamBytes,
  parseStreamFolderHeader,
  streamCiphertextSize,
  streamFolderFileSize,
  streamNonce,
  streamPlaintextSize,
  validChunkLog2,
} from '../../../packages/core/src/lib/e2estream';

import * as legacy047 from '../fixtures/e2ecrypto-legacy-v0.47.0';

const VECTORS = JSON.parse(
  fs.readFileSync(path.resolve(__dirname, '../../../backend/internal/e2edecrypt/testdata/stream-vectors.json'), 'utf8'),
) as {
  key_hex: string;
  prefix_hex: string;
  stream: Array<{ log2: number; len: number; seed: number; ct_len: number; ct_sha256: string; ct_hex?: string }>;
  folder_v2: { fmk_hex: string; len: number; seed: number; file_hex: string };
};

const hex = (s: string) => new Uint8Array(Buffer.from(s, 'hex'));
const sha = (b: Uint8Array) => createHash('sha256').update(b).digest('hex');

function pattern(n: number, seed: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) out[i] = (i * 31 + seed) & 0xff;
  return out;
}

async function aesKey(raw: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', new Uint8Array(raw), { name: 'AES-GCM' }, false, ['encrypt', 'decrypt']);
}

/** A stream of `b`, cut into pieces of the given sizes (then the rest). */
function piecewise(b: Uint8Array, sizes: number[]): ReadableStream<Uint8Array> {
  const pieces: Uint8Array[] = [];
  let off = 0;
  let i = 0;
  while (off < b.length) {
    const n = sizes[i++ % sizes.length];
    pieces.push(b.slice(off, off + n));
    off += n;
  }
  return new ReadableStream({
    pull(ctl) {
      const p = pieces.shift();
      if (p) ctl.enqueue(p);
      else ctl.close();
    },
  });
}

describe('STREAM — pinned to the independent implementation', () => {
  it('encrypts every vector byte for byte, and decrypts it back', async () => {
    const key = await aesKey(hex(VECTORS.key_hex));
    const prefix = hex(VECTORS.prefix_hex);
    for (const v of VECTORS.stream) {
      const plain = pattern(v.len, v.seed);
      const ct = await encryptStreamBytes(key, prefix, plain, v.log2);
      expect(ct.length, `len ${v.len} @2^${v.log2}`).toBe(v.ct_len);
      expect(sha(ct), `len ${v.len} @2^${v.log2}`).toBe(v.ct_sha256);
      if (v.ct_hex) expect(Buffer.from(ct).toString('hex')).toBe(v.ct_hex);
      expect(streamCiphertextSize(v.len, v.log2)).toBe(v.ct_len);
      expect(streamPlaintextSize(v.ct_len, v.log2)).toBe(v.len);
      expect(sha(await decryptStreamBytes(key, prefix, ct, v.log2))).toBe(sha(plain));
    }
  });

  it('the vectors cover empty, one byte, exact multiples and more than one chunk at 2^20', () => {
    const at20 = VECTORS.stream.filter((v) => v.log2 === 20).map((v) => v.len);
    expect(at20).toEqual([0, 5, 2 ** 20 + 5, 2 * 2 ** 20]);
    expect(E2E_STREAM_CHUNK_LOG2).toBe(20);
  });

  it('the nonce is prefix ‖ uint32 BE counter ‖ last flag', () => {
    const p = hex('a0a1a2a3a4a5a6');
    expect(Buffer.from(streamNonce(p, 0, false)).toString('hex')).toBe('a0a1a2a3a4a5a6' + '00000000' + '00');
    expect(Buffer.from(streamNonce(p, 0x01020304, true)).toString('hex')).toBe('a0a1a2a3a4a5a6' + '01020304' + '01');
    expect(() => streamNonce(p, 2 ** 32, false)).toThrow(/too many chunks/);
    expect(() => streamNonce(p.slice(1), 0, false)).toThrow(/7 bytes/);
  });

  it('sizes: every plaintext length maps to one ciphertext length and back; impossible lengths are refused', () => {
    for (const log2 of [10, 20]) {
      const c = 2 ** log2;
      for (const n of [0, 1, c - 1, c, c + 1, 2 * c, 2 * c + 7, 5 * c]) {
        expect(streamPlaintextSize(streamCiphertextSize(n, log2), log2)).toBe(n);
      }
      expect(streamPlaintextSize(15, log2)).toBeNull();
      // a full chunk, then a bare tag: an empty last chunk no writer makes
      expect(streamPlaintextSize(c + 16 + 16, log2)).toBeNull();
      // a full chunk, then a partial tag
      expect(streamPlaintextSize(c + 16 + 9, log2)).toBeNull();
    }
    expect(validChunkLog2(9)).toBe(false);
    expect(validChunkLog2(25)).toBe(false);
    expect(validChunkLog2(20)).toBe(true);
  });
});

describe('STREAM — the stream transforms', () => {
  it('any way the input is cut, the output is the same', async () => {
    const key = await aesKey(pattern(32, 1));
    const prefix = pattern(7, 2);
    const plain = pattern(5000, 3);
    const want = await encryptStreamBytes(key, prefix, plain, 10);
    for (const sizes of [[1], [7], [1023, 1, 1025], [4999], [5000]]) {
      const ct = await collectBytes(piecewise(plain, sizes).pipeThrough(createStreamEncryptor(key, prefix, 10)));
      expect(sha(ct), JSON.stringify(sizes)).toBe(sha(want));
      const back = await collectBytes(piecewise(ct, sizes).pipeThrough(createStreamDecryptor(key, prefix, 10)));
      expect(sha(back), JSON.stringify(sizes)).toBe(sha(plain));
    }
  });

  it('an empty plaintext is one empty last chunk, and exact multiples add no empty chunk', async () => {
    const key = await aesKey(pattern(32, 4));
    const prefix = pattern(7, 5);
    expect((await encryptStreamBytes(key, prefix, new Uint8Array(0), 10)).length).toBe(16);
    expect((await encryptStreamBytes(key, prefix, pattern(2048, 1), 10)).length).toBe(2048 + 32);
    expect(await decryptStreamBytes(key, prefix, await encryptStreamBytes(key, prefix, new Uint8Array(0), 10), 10)).toEqual(
      new Uint8Array(0),
    );
  });

  it('refuses a source that is shorter or longer than it promised', async () => {
    const key = await aesKey(pattern(32, 6));
    const prefix = pattern(7, 7);
    await expect(
      collectBytes(bytesStream(pattern(100, 1)).pipeThrough(createStreamEncryptor(key, prefix, 10, { expectSize: 101 }))),
    ).rejects.toThrow(/shorter/);
    await expect(
      collectBytes(bytesStream(pattern(100, 1)).pipeThrough(createStreamEncryptor(key, prefix, 10, { expectSize: 99 }))),
    ).rejects.toThrow(/longer/);
  });
});

describe('STREAM — tampering is detected', () => {
  const log2 = 10;
  const full = 1024 + 16;

  async function setup() {
    const key = await aesKey(pattern(32, 8));
    const prefix = pattern(7, 9);
    const plain = pattern(3 * 1024 + 100, 10); // 4 chunks
    const ct = await encryptStreamBytes(key, prefix, plain, log2);
    const open = (b: Uint8Array) => collectBytes(bytesStream(b, 333).pipeThrough(createStreamDecryptor(key, prefix, log2)));
    return { key, prefix, plain, ct, open };
  }

  it('the untouched file opens', async () => {
    const { ct, plain, open } = await setup();
    expect(sha(await open(ct))).toBe(sha(plain));
  });

  it('truncation at a chunk boundary, and in the middle of one', async () => {
    const { ct, open } = await setup();
    for (const cut of [full, 2 * full, 3 * full, 3 * full + 50, ct.length - 1, 10]) {
      await expect(open(ct.slice(0, cut)), `cut at ${cut}`).rejects.toBeInstanceOf(E2eDecryptError);
    }
  });

  it('reordering two chunks', async () => {
    const { ct, open } = await setup();
    const swapped = new Uint8Array(ct);
    swapped.set(ct.slice(full, 2 * full), 0);
    swapped.set(ct.slice(0, full), full);
    await expect(open(swapped)).rejects.toBeInstanceOf(E2eDecryptError);
  });

  it('appending a chunk — a copy of the last, or of any other', async () => {
    const { ct, open } = await setup();
    const lastChunk = ct.slice(3 * full);
    for (const extra of [lastChunk, ct.slice(0, full), new Uint8Array(16)]) {
      const longer = new Uint8Array(ct.length + extra.length);
      longer.set(ct, 0);
      longer.set(extra, ct.length);
      await expect(open(longer)).rejects.toBeInstanceOf(E2eDecryptError);
    }
  });

  it('a flipped bit anywhere', async () => {
    const { ct, open } = await setup();
    for (const at of [0, 100, full - 1, full, 2 * full + 17, ct.length - 1]) {
      const bad = new Uint8Array(ct);
      bad[at] ^= 0x01;
      await expect(open(bad), `bit at ${at}`).rejects.toBeInstanceOf(E2eDecryptError);
    }
  });

  it('the wrong nonce prefix or the wrong key', async () => {
    const { ct, key } = await setup();
    await expect(
      collectBytes(bytesStream(ct).pipeThrough(createStreamDecryptor(key, pattern(7, 99), log2))),
    ).rejects.toBeInstanceOf(E2eDecryptError);
    const other = await aesKey(pattern(32, 77));
    await expect(
      collectBytes(bytesStream(ct).pipeThrough(createStreamDecryptor(other, pattern(7, 9), log2))),
    ).rejects.toBeInstanceOf(E2eDecryptError);
  });

  it('a header that lies about the size', async () => {
    const { ct, key, prefix, plain } = await setup();
    for (const expectSize of [plain.length - 1, plain.length + 1]) {
      await expect(
        collectBytes(bytesStream(ct).pipeThrough(createStreamDecryptor(key, prefix, log2, { expectSize }))),
      ).rejects.toBeInstanceOf(E2eDecryptError);
    }
  });
});

describe('folder files, header version 0x02', () => {
  it('opens the independent vector (layout, wrap and body)', async () => {
    const v = VECTORS.folder_v2;
    const fmk = await aesKey(hex(v.fmk_hex));
    const file = hex(v.file_hex);
    const h = parseStreamFolderHeader(file);
    expect(file[8]).toBe(E2E_FILE_VERSION_STREAM);
    expect(h.log2).toBe(10);
    expect(Buffer.from(h.prefix).toString('hex')).toBe('b0b1b2b3b4b5b6');
    expect(Array.from(file.slice(77, 97))).toEqual(new Array(20).fill(0));
    // whole, in memory — what the preview path does
    const whole = new Uint8Array(await decryptFile(fmk, file.buffer.slice(0) as ArrayBuffer));
    expect(sha(whole)).toBe(sha(pattern(v.len, v.seed)));
    // streamed — what a download does
    const streamed = await collectBytes(decryptFolderFileStream(fmk, null, bytesStream(file, 700)));
    expect(sha(streamed)).toBe(sha(pattern(v.len, v.seed)));
  });

  it('round-trips under a folder key, and the body is a STREAM at the size it promised', async () => {
    const made = await createEncryptedFolder('a folder password');
    const plain = pattern(10_000, 11);
    const enc = await encryptFolderFileStream(made.fmk, plain.length, bytesStream(plain), { chunkLog2: 10 });
    const file = await collectBytes(enc.stream);
    expect(file.length).toBe(enc.size);
    expect(enc.size).toBe(streamFolderFileSize(plain.length, 10));
    expect(Buffer.from(file.slice(0, 8)).toString()).toBe('filexe2e');
    expect(file[8]).toBe(2);
    expect(sha(new Uint8Array(await decryptFile(made.fmk, file.buffer)))).toBe(sha(plain));
    expect(sha(await collectBytes(decryptFolderFileStream(made.fmk, null, bytesStream(file, 999))))).toBe(sha(plain));
    // another folder's key opens nothing
    const other = await createEncryptedFolder('another password!');
    await expect(decryptFile(other.fmk, file.buffer)).rejects.toBeInstanceOf(E2eDecryptError);
  });

  it('decryptFolderFileStream reads 0x01 too, and passes a never-encrypted file through only when asked', async () => {
    const made = await createEncryptedFolder('a folder password');
    const plain = pattern(3000, 12);
    const v1 = new Uint8Array(await encryptFile(made.fmk, plain.buffer.slice(0) as ArrayBuffer));
    expect(sha(await collectBytes(decryptFolderFileStream(made.fmk, null, bytesStream(v1, 100))))).toBe(sha(plain));
    await expect(collectBytes(decryptFolderFileStream(made.fmk, null, bytesStream(plain)))).rejects.toThrow(/not an encrypted file/);
    let flagged = false;
    const passed = await collectBytes(
      decryptFolderFileStream(made.fmk, null, bytesStream(plain), { passPlain: true, onPlain: () => (flagged = true) }),
    );
    expect(sha(passed)).toBe(sha(plain));
    expect(flagged).toBe(true);
  });

  it('filex 0.47 refuses a 0x02 file by name — "unsupported version 2" — and still reads 0x01', async () => {
    const made = await legacy047.createEncryptedFolder('a folder password');
    const plain = pattern(4000, 13);
    const enc = await encryptFolderFileStream(made.fmk, plain.length, bytesStream(plain), { chunkLog2: 10 });
    const file = await collectBytes(enc.stream);
    await expect(legacy047.decryptFile(made.fmk, file.buffer)).rejects.toThrow(/unsupported version 2/);
    const v1 = await encryptFile(made.fmk, plain.buffer.slice(0) as ArrayBuffer);
    expect(sha(new Uint8Array(await legacy047.decryptFile(made.fmk, v1)))).toBe(sha(plain));
  });

  it('a re-key re-wraps a 0x02 file from its header alone; the body does not change', async () => {
    const legacyMade = await (await import('../fixtures/e2ecrypto-legacy-v0.30.1')).createMarker('before-rekey');
    const plain = pattern(6000, 14);
    const enc = await encryptFolderFileStream(legacyMade.kek, plain.length, bytesStream(plain), { chunkLog2: 10 });
    const file = await collectBytes(enc.stream);
    const start = await startRekey(legacyMade.marker as E2eMarker, { password: 'before-rekey' }, 'after-the-rekey');
    const newHead = await rewrapFileKey(file.slice(0, 97).buffer, start.previous, start.fmk);
    expect(newHead).not.toBeNull();
    const rewrapped = new Uint8Array(file.length);
    rewrapped.set(new Uint8Array(newHead!), 0);
    rewrapped.set(file.slice(97), 97);
    // only the wrap IV and the wrapped DEK moved
    expect(Buffer.compare(Buffer.from(rewrapped.slice(0, 9)), Buffer.from(file.slice(0, 9)))).toBe(0);
    expect(Buffer.compare(Buffer.from(rewrapped.slice(69)), Buffer.from(file.slice(69)))).toBe(0);
    expect(Buffer.compare(Buffer.from(rewrapped.slice(9, 69)), Buffer.from(file.slice(9, 69)))).not.toBe(0);
    expect(sha(new Uint8Array(await decryptFile(start.fmk, rewrapped.buffer)))).toBe(sha(plain));
    await expect(decryptFile(start.previous, rewrapped.buffer)).rejects.toBeInstanceOf(E2eDecryptError);
    // already under the new key: nothing to do (so resuming is running it again)
    expect(await rewrapFileKey(rewrapped.slice(0, 97).buffer, start.previous, start.fmk)).toBeNull();
    // mid re-key, a file not re-wrapped yet still opens through `previous`
    expect(sha(new Uint8Array(await decryptFileAny(start.fmk, start.previous, file.buffer)))).toBe(sha(plain));
    expect(
      sha(await collectBytes(decryptFolderFileStream(start.fmk, start.previous, bytesStream(file, 512)))),
    ).toBe(sha(plain));
    const pwKey = await unlockWithPassword(start.marker, 'after-the-rekey');
    expect(pwKey).not.toBeNull();
  });

  it(
    'more than 200 MB, at the production chunk size, without holding the file: encrypt → decrypt as one pipe',
    async () => {
      // A source that makes its bytes as it is read, and a sink that only
      // hashes: nothing here allocates more than a few chunks.
      const total = E2E_MAX_FILE_BYTES + 3 * 2 ** 20 + 12_345;
      const block = pattern(2 ** 20, 15);
      let made = 0;
      const inHash = createHash('sha256');
      const src = new ReadableStream<Uint8Array>({
        pull(ctl) {
          if (made >= total) {
            ctl.close();
            return;
          }
          const n = Math.min(block.length, total - made);
          const piece = block.subarray(0, n);
          inHash.update(piece);
          ctl.enqueue(piece.slice());
          made += n;
        },
      });
      const folder = await createEncryptedFolder('a folder password');
      const enc = await encryptFolderFileStream(folder.fmk, total, src);
      expect(enc.size).toBe(97 + total + 16 * Math.ceil(total / 2 ** 20));
      let cipherBytes = 0;
      const counted = enc.stream.pipeThrough(
        new TransformStream<Uint8Array, Uint8Array>({
          transform(c, ctl) {
            cipherBytes += c.length;
            ctl.enqueue(c);
          },
        }),
      );
      const outHash = createHash('sha256');
      let plainBytes = 0;
      await decryptFolderFileStream(folder.fmk, null, counted).pipeTo(
        new WritableStream({
          write(c) {
            plainBytes += c.length;
            outHash.update(c);
          },
        }),
      );
      expect(cipherBytes).toBe(enc.size);
      expect(plainBytes).toBe(total);
      expect(outHash.digest('hex')).toBe(inHash.digest('hex'));
    },
    180_000,
  );
});
