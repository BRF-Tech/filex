/**
 * e2ecrypto — client-side crypto for E2E-encrypted folders (wiring:e2).
 *
 * WebCrypto ONLY — zero dependencies. Design doc: docs/E2E-ENCRYPTION.md.
 *
 * Scheme (v1):
 *   folder password ─PBKDF2-SHA256(600k iter, per-folder 16B salt)─▶ KEK (AES-256-GCM)
 *   per-file random 32B DEK (AES-256-GCM) encrypts the content one-shot;
 *   the DEK is wrapped with the KEK and stored in the file's own header.
 *
 * File layout ('filexe2e' magic, fixed 97-byte header):
 *   [0..8)   magic  "filexe2e"
 *   [8]      version 0x01
 *   [9..21)  wrapIV  (12B)  — GCM IV of the DEK wrap
 *   [21..69) wrappedDEK (48B = 32B DEK + 16B GCM tag)
 *   [69..81) dataIV  (12B)  — GCM IV of the content
 *   [81..97) reserved (zeros; v2 chunking/metadata)
 *   [97..)   ciphertext (content + 16B GCM tag)
 *
 * Folder marker `.filex-e2e.json` at the encrypted-folder root:
 *   { v:1, salt:<b64 16B>, iter:600000, verify:<b64 12B IV || GCM('filex-e2e-verify-v1')> }
 *
 * The KEK NEVER leaves memory — no storage of any kind. Password loss is
 * data loss by design (no recovery path exists anywhere).
 */

export const E2E_MARKER_NAME = '.filex-e2e.json';
export const E2E_MAGIC = 'filexe2e';
export const E2E_VERSION = 1;
export const E2E_DEFAULT_ITERATIONS = 600_000;
export const E2E_MIN_ITERATIONS = 600_000;
/** MVP single-shot in-memory ceiling — larger uploads are refused with a warning. */
export const E2E_MAX_FILE_BYTES = 200 * 1024 * 1024;
export const E2E_MIN_PASSWORD_LEN = 8;

const VERIFY_PLAINTEXT = 'filex-e2e-verify-v1';
const MAGIC_BYTES = new TextEncoder().encode(E2E_MAGIC); // 8 bytes
const HEADER_LEN = 97;
const WRAP_IV_OFF = 9;
const WRAPPED_DEK_OFF = 21;
const WRAPPED_DEK_LEN = 48;
const DATA_IV_OFF = 69;
const IV_LEN = 12;

export interface E2eMarker {
  v: number;
  salt: string; // base64
  iter: number;
  verify: string; // base64: 12B IV || AES-GCM ciphertext of VERIFY_PLAINTEXT
}

/** Thrown on wrong password / corrupted ciphertext (GCM tag mismatch). */
export class E2eDecryptError extends Error {
  constructor(msg = 'e2e: decrypt failed') {
    super(msg);
    this.name = 'E2eDecryptError';
  }
}

// ---------------------------------------------------------------------
// base64 helpers (no deps)
// ---------------------------------------------------------------------

export function bytesToB64(b: Uint8Array): string {
  let s = '';
  for (let i = 0; i < b.length; i++) s += String.fromCharCode(b[i]);
  return btoa(s);
}

export function b64ToBytes(s: string): Uint8Array {
  const raw = atob(s);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

// ---------------------------------------------------------------------
// Key derivation
// ---------------------------------------------------------------------

/**
 * Derive the folder KEK from a password. Returns a NON-extractable
 * AES-256-GCM CryptoKey — it can encrypt/decrypt but never be exported,
 * so even a same-origin script can't read the raw key material back.
 */
export async function deriveKek(
  password: string,
  salt: Uint8Array,
  iterations: number,
): Promise<CryptoKey> {
  const material = await crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(password),
    'PBKDF2',
    false,
    ['deriveKey'],
  );
  return crypto.subtle.deriveKey(
    // Copy into a fresh ArrayBuffer-backed view — TS 5.9 BufferSource typing
    // rejects Uint8Array<ArrayBufferLike> that may wrap a SharedArrayBuffer.
    { name: 'PBKDF2', salt: new Uint8Array(salt).buffer as ArrayBuffer, iterations, hash: 'SHA-256' },
    material,
    { name: 'AES-GCM', length: 256 },
    false, // non-extractable
    ['encrypt', 'decrypt'],
  );
}

// ---------------------------------------------------------------------
// Marker create / verify
// ---------------------------------------------------------------------

/** Create a fresh folder marker for `password` (also returns the derived KEK). */
export async function createMarker(
  password: string,
  iterations: number = E2E_DEFAULT_ITERATIONS,
): Promise<{ marker: E2eMarker; kek: CryptoKey }> {
  const iter = Math.max(E2E_MIN_ITERATIONS, iterations);
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const kek = await deriveKek(password, salt, iter);
  const iv = crypto.getRandomValues(new Uint8Array(IV_LEN));
  const ct = new Uint8Array(
    await crypto.subtle.encrypt(
      { name: 'AES-GCM', iv: iv.buffer as ArrayBuffer },
      kek,
      new TextEncoder().encode(VERIFY_PLAINTEXT),
    ),
  );
  const verify = new Uint8Array(IV_LEN + ct.length);
  verify.set(iv, 0);
  verify.set(ct, IV_LEN);
  return {
    marker: { v: E2E_VERSION, salt: bytesToB64(salt), iter, verify: bytesToB64(verify) },
    kek,
  };
}

/** Parse marker JSON text; returns null when the shape is not a v1 marker. */
export function parseMarker(text: string): E2eMarker | null {
  try {
    const m = JSON.parse(text) as E2eMarker;
    if (!m || m.v !== E2E_VERSION) return null;
    if (typeof m.salt !== 'string' || typeof m.verify !== 'string') return null;
    if (typeof m.iter !== 'number' || m.iter < 1) return null;
    return m;
  } catch {
    return null;
  }
}

/**
 * Check `password` against a folder marker. Resolves to the derived KEK on
 * success, or `null` on a wrong password (GCM tag mismatch on the verify
 * blob). Never talks to any server.
 */
export async function verifyPassword(
  marker: E2eMarker,
  password: string,
): Promise<CryptoKey | null> {
  const salt = b64ToBytes(marker.salt);
  const kek = await deriveKek(password, salt, marker.iter);
  const verify = b64ToBytes(marker.verify);
  if (verify.length <= IV_LEN) return null;
  try {
    const pt = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: verify.slice(0, IV_LEN).buffer as ArrayBuffer },
      kek,
      verify.slice(IV_LEN).buffer as ArrayBuffer,
    );
    if (new TextDecoder().decode(pt) !== VERIFY_PLAINTEXT) return null;
    return kek;
  } catch {
    return null; // wrong password
  }
}

// ---------------------------------------------------------------------
// Magic sniff
// ---------------------------------------------------------------------

/** True when the buffer starts with the 'filexe2e' magic. */
export function hasMagic(buf: ArrayBuffer | Uint8Array): boolean {
  const b = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
  if (b.length < MAGIC_BYTES.length) return false;
  for (let i = 0; i < MAGIC_BYTES.length; i++) {
    if (b[i] !== MAGIC_BYTES[i]) return false;
  }
  return true;
}

// ---------------------------------------------------------------------
// File encrypt / decrypt (one-shot MVP)
// ---------------------------------------------------------------------

/**
 * Encrypt `content` under the folder KEK: mints a fresh DEK, encrypts the
 * content one-shot, wraps the DEK with the KEK and prepends the fixed
 * 'filexe2e' header. Throws when content exceeds E2E_MAX_FILE_BYTES.
 */
export async function encryptFile(kek: CryptoKey, content: ArrayBuffer): Promise<ArrayBuffer> {
  if (content.byteLength > E2E_MAX_FILE_BYTES) {
    throw new Error('e2e: file exceeds the 200MB single-shot limit');
  }
  const rawDek = crypto.getRandomValues(new Uint8Array(32));
  const dek = await crypto.subtle.importKey('raw', rawDek.buffer as ArrayBuffer, { name: 'AES-GCM' }, false, [
    'encrypt',
  ]);
  const wrapIV = crypto.getRandomValues(new Uint8Array(IV_LEN));
  const dataIV = crypto.getRandomValues(new Uint8Array(IV_LEN));
  const wrappedDek = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: wrapIV.buffer as ArrayBuffer }, kek, rawDek.buffer as ArrayBuffer),
  );
  const ct = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: dataIV.buffer as ArrayBuffer }, dek, content),
  );
  // Zero the raw DEK copy as a hygiene measure (best-effort — GC may have
  // other copies, but don't leave the obvious one around).
  rawDek.fill(0);

  const out = new Uint8Array(HEADER_LEN + ct.length);
  out.set(MAGIC_BYTES, 0);
  out[8] = E2E_VERSION;
  out.set(wrapIV, WRAP_IV_OFF);
  out.set(wrappedDek, WRAPPED_DEK_OFF); // 48 bytes
  out.set(dataIV, DATA_IV_OFF);
  // [81..97) reserved zeros
  out.set(ct, HEADER_LEN);
  return out.buffer;
}

/**
 * Decrypt a 'filexe2e' blob with the folder KEK. Throws E2eDecryptError on
 * a wrong key / tampered data, and a plain Error when the header is not an
 * e2e file at all.
 */
export async function decryptFile(kek: CryptoKey, data: ArrayBuffer): Promise<ArrayBuffer> {
  const b = new Uint8Array(data);
  if (!hasMagic(b) || b.length < HEADER_LEN) {
    throw new Error('e2e: not an encrypted file');
  }
  if (b[8] !== E2E_VERSION) {
    throw new Error(`e2e: unsupported version ${b[8]}`);
  }
  const wrapIV = b.slice(WRAP_IV_OFF, WRAP_IV_OFF + IV_LEN);
  const wrappedDek = b.slice(WRAPPED_DEK_OFF, WRAPPED_DEK_OFF + WRAPPED_DEK_LEN);
  const dataIV = b.slice(DATA_IV_OFF, DATA_IV_OFF + IV_LEN);
  let rawDek: ArrayBuffer;
  try {
    rawDek = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: wrapIV.buffer as ArrayBuffer },
      kek,
      wrappedDek.buffer as ArrayBuffer,
    );
  } catch {
    throw new E2eDecryptError('e2e: DEK unwrap failed (wrong password?)');
  }
  const dek = await crypto.subtle.importKey('raw', rawDek, { name: 'AES-GCM' }, false, ['decrypt']);
  try {
    return await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: dataIV.buffer as ArrayBuffer },
      dek,
      b.slice(HEADER_LEN).buffer as ArrayBuffer,
    );
  } catch {
    throw new E2eDecryptError('e2e: content decrypt failed');
  }
}

// ---------------------------------------------------------------------
// In-memory session key ring
// ---------------------------------------------------------------------

/**
 * Tiny per-explorer key ring: encrypted-folder root (wire path) → KEK.
 * Lives ONLY in memory — "Kilitle" drops the entry, a reload drops all.
 */
export function createKeyRing() {
  const keys = new Map<string, CryptoKey>();
  return {
    get(root: string): CryptoKey | undefined {
      return keys.get(root);
    },
    set(root: string, kek: CryptoKey): void {
      keys.set(root, kek);
    },
    /** Drop one folder's key ("Kilitle"). */
    lock(root: string): void {
      keys.delete(root);
    },
    has(root: string): boolean {
      return keys.has(root);
    },
    clear(): void {
      keys.clear();
    },
  };
}

export type E2eKeyRing = ReturnType<typeof createKeyRing>;
