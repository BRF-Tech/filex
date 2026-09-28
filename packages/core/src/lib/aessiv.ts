/**
 * aessiv — AES-SIV (RFC 5297) on top of WebCrypto.
 *
 * WebCrypto has no SIV mode, so it is assembled here from the two primitives
 * it does have, exactly as the RFC defines it:
 *
 *   - AES-CMAC (RFC 4493) — computed with AES-CBC under a zero IV. CBC-MAC of
 *     the prepared message IS the CMAC; WebCrypto's CBC always appends a
 *     PKCS#7 padding block, which we simply ignore.
 *   - S2V (RFC 5297 §2.4) — CMAC over the associated-data strings and the
 *     plaintext, chained with doubling in GF(2^128).
 *   - CTR (RFC 5297 §2.5) — AES-CTR with the synthetic IV as the counter,
 *     bits 31 and 63 cleared. With those bits cleared a 32-bit counter can
 *     never carry for any message shorter than 2^31 blocks, so WebCrypto's
 *     `length: 32` counter is bit-for-bit the RFC's 128-bit addition.
 *
 * Output layout: V (16 bytes, the synthetic IV / tag) || C (same length as P).
 *
 * Key: 32 bytes = AES-128-SIV, 64 bytes = AES-256-SIV. The left half keys
 * S2V (CMAC), the right half keys CTR (RFC 5297 §2.6).
 *
 * Why SIV at all: it is DETERMINISTIC authenticated encryption. The same key
 * and the same plaintext always give the same ciphertext, and any change to
 * the ciphertext fails the tag. For file names that is the property we want:
 * the server can keep enforcing "one name per folder" on the ciphertext, and
 * a name that does not decrypt is recognisably not one of ours. See
 * docs/E2E-ENCRYPTION.md → "Encrypted names".
 *
 * Measured against RFC 5297 appendix A and against an independent
 * implementation (Python `cryptography`'s AESSIV) in
 * web/tests/lib/e2enames.test.ts; the Go twin is backend/internal/e2edecrypt.
 */

const BLOCK = 16;
const ZERO_BLOCK = new Uint8Array(BLOCK);

/** A prepared AES-SIV key: the two halves imported for their one job each. */
export interface SivKey {
  /** S2V half, as AES-CBC (the CMAC engine). */
  mac: CryptoKey;
  /** CTR half. */
  ctr: CryptoKey;
  /** CMAC subkeys K1/K2 of the S2V half (RFC 4493 §2.3). */
  k1: Uint8Array;
  k2: Uint8Array;
}

/** Thrown when a ciphertext fails the SIV check — tampered, or another key. */
export class SivAuthError extends Error {
  constructor(msg = 'aes-siv: authentication failed') {
    super(msg);
    this.name = 'SivAuthError';
  }
}

function ab(b: Uint8Array): ArrayBuffer {
  return new Uint8Array(b).buffer as ArrayBuffer;
}

/** Doubling in GF(2^128) with the CMAC polynomial x^128 + x^7 + x^2 + x + 1. */
export function dbl(b: Uint8Array): Uint8Array {
  const out = new Uint8Array(BLOCK);
  let carry = 0;
  for (let i = BLOCK - 1; i >= 0; i--) {
    const v = b[i];
    out[i] = ((v << 1) | carry) & 0xff;
    carry = v >>> 7;
  }
  if (b[0] & 0x80) out[BLOCK - 1] ^= 0x87;
  return out;
}

function xorInto(dst: Uint8Array, off: number, src: Uint8Array): void {
  for (let i = 0; i < src.length; i++) dst[off + i] ^= src[i];
}

function xor16(a: Uint8Array, b: Uint8Array): Uint8Array {
  const out = new Uint8Array(BLOCK);
  for (let i = 0; i < BLOCK; i++) out[i] = a[i] ^ b[i];
  return out;
}

/** One raw AES block encryption, via CBC with a zero IV. */
async function aesBlock(k: CryptoKey, block: Uint8Array): Promise<Uint8Array> {
  const out = await crypto.subtle.encrypt({ name: 'AES-CBC', iv: ab(ZERO_BLOCK) }, k, ab(block));
  return new Uint8Array(out).slice(0, BLOCK);
}

/** AES-CMAC (RFC 4493). */
async function cmac(key: SivKey, msg: Uint8Array): Promise<Uint8Array> {
  const n = Math.max(1, Math.ceil(msg.length / BLOCK));
  const complete = msg.length > 0 && msg.length % BLOCK === 0;
  const m = new Uint8Array(n * BLOCK);
  m.set(msg);
  const last = (n - 1) * BLOCK;
  if (complete) {
    xorInto(m, last, key.k1);
  } else {
    m[msg.length] = 0x80;
    xorInto(m, last, key.k2);
  }
  const ct = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-CBC', iv: ab(ZERO_BLOCK) }, key.mac, ab(m)),
  );
  return ct.slice(last, last + BLOCK);
}

/** S2V (RFC 5297 §2.4) over the associated data strings and the plaintext. */
async function s2v(key: SivKey, ad: Uint8Array[], p: Uint8Array): Promise<Uint8Array> {
  let d = await cmac(key, ZERO_BLOCK);
  for (const a of ad) d = xor16(dbl(d), await cmac(key, a));
  let t: Uint8Array;
  if (p.length >= BLOCK) {
    // xorend: XOR D into the last 16 bytes of P.
    t = p.slice();
    xorInto(t, p.length - BLOCK, d);
  } else {
    const padded = new Uint8Array(BLOCK);
    padded.set(p);
    padded[p.length] = 0x80;
    t = xor16(dbl(d), padded);
  }
  return cmac(key, t);
}

function counterFrom(v: Uint8Array): Uint8Array {
  const q = v.slice();
  q[8] &= 0x7f;
  q[12] &= 0x7f;
  return q;
}

async function ctr(key: SivKey, v: Uint8Array, data: Uint8Array): Promise<Uint8Array> {
  if (data.length === 0) return new Uint8Array(0);
  const out = await crypto.subtle.encrypt(
    { name: 'AES-CTR', counter: ab(counterFrom(v)), length: 32 },
    key.ctr,
    ab(data),
  );
  return new Uint8Array(out);
}

/**
 * Import a raw AES-SIV key (32 or 64 bytes). The raw bytes are not kept:
 * both halves are imported non-extractable.
 */
export async function importSivKey(raw: Uint8Array): Promise<SivKey> {
  if (raw.length !== 32 && raw.length !== 64) {
    throw new Error('aes-siv: key must be 32 or 64 bytes');
  }
  const half = raw.length / 2;
  const mac = await crypto.subtle.importKey('raw', ab(raw.slice(0, half)), { name: 'AES-CBC' }, false, [
    'encrypt',
  ]);
  const ctrKey = await crypto.subtle.importKey('raw', ab(raw.slice(half)), { name: 'AES-CTR' }, false, [
    'encrypt',
    'decrypt',
  ]);
  const l = await aesBlock(mac, ZERO_BLOCK);
  const k1 = dbl(l);
  const k2 = dbl(k1);
  return { mac, ctr: ctrKey, k1, k2 };
}

/** SIV-ENCRYPT: returns V || C. Deterministic by design. */
export async function sivEncrypt(
  key: SivKey,
  plaintext: Uint8Array,
  ad: Uint8Array[] = [],
): Promise<Uint8Array> {
  const v = await s2v(key, ad, plaintext);
  const c = await ctr(key, v, plaintext);
  const out = new Uint8Array(BLOCK + c.length);
  out.set(v, 0);
  out.set(c, BLOCK);
  return out;
}

/** SIV-DECRYPT: throws SivAuthError when the tag does not match. */
export async function sivDecrypt(
  key: SivKey,
  sealed: Uint8Array,
  ad: Uint8Array[] = [],
): Promise<Uint8Array> {
  if (sealed.length < BLOCK) throw new SivAuthError('aes-siv: ciphertext too short');
  const v = sealed.slice(0, BLOCK);
  const p = await ctr(key, v, sealed.slice(BLOCK));
  const t = await s2v(key, ad, p);
  let diff = 0;
  for (let i = 0; i < BLOCK; i++) diff |= t[i] ^ v[i];
  if (diff !== 0) {
    p.fill(0);
    throw new SivAuthError();
  }
  return p;
}
