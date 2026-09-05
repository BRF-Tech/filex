/**
 * e2ecrypto — client-side crypto for E2E-encrypted folders (wiring:e2).
 *
 * WebCrypto ONLY — zero dependencies. Design doc: docs/E2E-ENCRYPTION.md.
 *
 * ── Scheme ────────────────────────────────────────────────────────────
 *
 * Every encrypted file wraps its own random DEK under ONE key, the folder
 * master key (FMK), and stores the wrapped copy in its own 97-byte header.
 * The FMK is what a "key slot" in the folder marker hands back:
 *
 *   password ─PBKDF2-SHA256(600k, 16B salt)─▶ KEK ─┐
 *   recovery key ─HKDF-SHA256(16B salt)─▶ RKEK ────┼─▶ unwraps the FMK
 *   escrow private key ─RSA-OAEP-256───────────────┘
 *                                                   │
 *   per-file random 32B DEK ◀── AES-GCM-wrapped by the FMK, in the header
 *
 * Adding a recovery path therefore costs one more wrapped copy of a single
 * 32-byte key in the marker — not a re-encrypt of anything. The file format
 * below is UNCHANGED from v1 and stays that way; only the marker grew.
 *
 * ── Marker versions ───────────────────────────────────────────────────
 *
 * v1 (shipped up to 0.30.1) has no slots: the DEK is wrapped directly by
 * the password KEK. Read that as "the FMK *is* the KEK". Such folders keep
 * opening with nothing but their password, forever — the v1 read path is a
 * first-class path here, not a migration shim.
 *
 * v2 adds the slots. It comes in two flavours, told apart by `fmk`:
 *   - `fmk: 'wrapped'` — a fresh random FMK, held in `fmk_pw` wrapped under
 *     the password KEK. Every folder created from 0.31 on.
 *   - `fmk: 'kek'`     — a v1 folder that was given recovery keys in place.
 *     Its files were already wrapped under the KEK and are not rewritten, so
 *     the FMK stays defined as "the password-derived KEK" and the recovery
 *     slots wrap those raw 32 bytes. The password path is byte-identical to
 *     v1; only the extra slots are new.
 *
 * ── Invariants ────────────────────────────────────────────────────────
 *
 *   - No key, password or recovery key is ever stored, logged or sent to a
 *     server. The FMK lives in an in-memory key ring and dies with the tab.
 *   - `deriveKek` imports non-extractable. Raw KEK bytes are produced ONLY
 *     by `deriveKekBits`, only for a marker whose FMK *is* the KEK
 *     (`upgradeMarkerV1`, and `addEscrowSlot` on a folder it produced), and
 *     only long enough to wrap them into a slot.
 *   - A folder created while escrow was off carries no escrow slot, so the
 *     escrow key cannot open it, and nothing the OPERATOR does changes
 *     that — not enabling escrow, not adopting it, not any admin action or
 *     future version. That is arithmetic, not policy: adding a slot needs
 *     the folder master key, and the server has never held a credential
 *     that produces one.
 *   - The folder's OWNER can, from inside, with the password:
 *     `addEscrowSlot`. That is the only door, it opens from one side only,
 *     and it is the reason `escrowAvailability` says "not as things stand"
 *     rather than "never".
 *
 * File layout ('filexe2e' magic, fixed 97-byte header) — UNCHANGED in v2:
 *   [0..8)   magic  "filexe2e"
 *   [8]      version 0x01
 *   [9..21)  wrapIV  (12B)  — GCM IV of the DEK wrap
 *   [21..69) wrappedDEK (48B = 32B DEK + 16B GCM tag), wrapped by the FMK
 *   [69..81) dataIV  (12B)  — GCM IV of the content
 *   [81..97) reserved (zeros; v2 chunking/metadata)
 *   [97..)   ciphertext (content + 16B GCM tag)
 */

export const E2E_MARKER_NAME = '.filex-e2e.json';
export const E2E_MAGIC = 'filexe2e';
/** File-header version byte. Unchanged by the recovery work. */
export const E2E_VERSION = 1;
/** Marker schema version written by this build. v1 markers still read. */
export const E2E_MARKER_VERSION = 2;
export const E2E_DEFAULT_ITERATIONS = 600_000;
export const E2E_MIN_ITERATIONS = 600_000;
/** MVP single-shot in-memory ceiling — larger uploads are refused with a warning. */
export const E2E_MAX_FILE_BYTES = 200 * 1024 * 1024;
export const E2E_MIN_PASSWORD_LEN = 8;
/** Entropy of a user recovery key: 20 bytes = 160 bits = exactly 32 base32 chars. */
export const E2E_RECOVERY_KEY_BYTES = 20;
/** The only escrow algorithm this version understands. */
export const E2E_ESCROW_ALG = 'RSA-OAEP-256';

const VERIFY_PLAINTEXT = 'filex-e2e-verify-v1';
const MAGIC_BYTES = new TextEncoder().encode(E2E_MAGIC); // 8 bytes
const HEADER_LEN = 97;
const WRAP_IV_OFF = 9;
const WRAPPED_DEK_OFF = 21;
const WRAPPED_DEK_LEN = 48;
const DATA_IV_OFF = 69;
const IV_LEN = 12;
const FMK_LEN = 32;
/** HKDF domain separation for the user recovery key. */
const RK_INFO = 'filex-e2e-recovery-v1';
const RK_SALT_LEN = 16;

/** How the folder master key is obtained from the password slot. */
export type E2eFmkMode = 'kek' | 'wrapped';

/** User-recovery-key slot: HKDF salt + the FMK wrapped under the derived key. */
export interface E2eRecoverySlot {
  salt: string; // base64, 16B HKDF salt
  blob: string; // base64: 12B IV || AES-GCM(RKEK, FMK)
}

/** Escrow slot: the FMK encrypted to the installation's escrow public key. */
export interface E2eEscrowSlot {
  /** First 8 bytes of SHA-256(SPKI), hex — names WHICH escrow key this is. */
  kid: string;
  alg: string; // E2E_ESCROW_ALG
  blob: string; // base64: RSA-OAEP-256(escrow public key, FMK)
}

export interface E2eMarker {
  v: number;
  salt: string; // base64, PBKDF2 salt for the password slot
  iter: number;
  verify: string; // base64: 12B IV || AES-GCM ciphertext of VERIFY_PLAINTEXT
  /** v2 only. Absent on a v1 marker, where the FMK is implicitly the KEK. */
  fmk?: E2eFmkMode;
  /** v2 + fmk==='wrapped' only: base64 12B IV || AES-GCM(KEK, FMK). */
  fmk_pw?: string;
  /** v2 only, optional: the user recovery key slot. */
  rk?: E2eRecoverySlot;
  /** v2 only, optional: the operator escrow slot. */
  esc?: E2eEscrowSlot;
  /**
   * v2 only, optional: an ISO timestamp recording that this folder's owner
   * was OFFERED an escrow slot and said no.
   *
   * It lives in the marker rather than in browser storage because the unit
   * of the decision is the FOLDER, not the device: the same person opening
   * the folder from their phone must not be asked again, and a decision
   * that vanished when someone cleared their site data would be no decision
   * at all. It travels with the folder through a move, a backup and a
   * restore, for the same reason the key slots do.
   *
   * It holds no key material and hides nothing from the operator — it is a
   * record of an answer, and its only effect is that filex stops asking.
   * `addEscrowSlot` clears it, so a decline is reversible by the one person
   * who can reverse it.
   */
  esc_declined?: string;
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

/**
 * Copy into a fresh ArrayBuffer — TS 5.9 BufferSource typing rejects views
 * that may wrap a SharedArrayBuffer, and WebCrypto wants a plain buffer.
 */
function buf(b: Uint8Array): ArrayBuffer {
  return new Uint8Array(b).buffer as ArrayBuffer;
}

/** IV || ciphertext, the shape every AES-GCM blob in the marker uses. */
function joinIvCt(iv: Uint8Array, ct: Uint8Array): string {
  const out = new Uint8Array(iv.length + ct.length);
  out.set(iv, 0);
  out.set(ct, iv.length);
  return bytesToB64(out);
}

async function gcmSeal(key: CryptoKey, plain: Uint8Array): Promise<string> {
  const iv = crypto.getRandomValues(new Uint8Array(IV_LEN));
  const ct = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: buf(iv) }, key, buf(plain)),
  );
  return joinIvCt(iv, ct);
}

/** Returns null (never throws) on a tag mismatch — i.e. "wrong key". */
async function gcmOpen(key: CryptoKey, b64: string): Promise<Uint8Array | null> {
  let raw: Uint8Array;
  try {
    raw = b64ToBytes(b64);
  } catch {
    return null;
  }
  if (raw.length <= IV_LEN) return null;
  try {
    const pt = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: buf(raw.slice(0, IV_LEN)) },
      key,
      buf(raw.slice(IV_LEN)),
    );
    return new Uint8Array(pt);
  } catch {
    return null;
  }
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
    { name: 'PBKDF2', salt: buf(salt), iterations, hash: 'SHA-256' },
    material,
    { name: 'AES-GCM', length: 256 },
    false, // non-extractable
    ['encrypt', 'decrypt'],
  );
}

/**
 * The same 32 bytes as `deriveKek`, but as raw material.
 *
 * ⚠ Used in exactly one place: upgrading a v1 marker, where the files are
 * already wrapped under the KEK and the recovery slots must therefore hold
 * those very bytes. Nothing else may call this — the steady-state password
 * path uses `deriveKek`, whose key cannot be exported.
 */
async function deriveKekBits(
  password: string,
  salt: Uint8Array,
  iterations: number,
): Promise<Uint8Array> {
  const material = await crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(password),
    'PBKDF2',
    false,
    ['deriveBits'],
  );
  const bits = await crypto.subtle.deriveBits(
    { name: 'PBKDF2', salt: buf(salt), iterations, hash: 'SHA-256' },
    material,
    FMK_LEN * 8,
  );
  return new Uint8Array(bits);
}

/** Import raw 32 bytes as the AES-256-GCM folder master key. */
async function importFmk(raw: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', buf(raw), { name: 'AES-GCM' }, false, [
    'encrypt',
    'decrypt',
  ]);
}

// ---------------------------------------------------------------------
// User recovery key — 160 bits, Crockford base32, 8 groups of 4
// ---------------------------------------------------------------------

/** Crockford base32: no I, L, O or U, so it survives being read aloud. */
const B32_ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';

/**
 * Format 20 raw bytes as the string the user writes down:
 * `XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX` (160 bits, no padding waste).
 */
export function formatRecoveryKey(raw: Uint8Array): string {
  let bits = 0;
  let acc = 0;
  let out = '';
  for (let i = 0; i < raw.length; i++) {
    acc = (acc << 8) | raw[i];
    bits += 8;
    while (bits >= 5) {
      out += B32_ALPHABET[(acc >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) out += B32_ALPHABET[(acc << (5 - bits)) & 31];
  return (out.match(/.{1,4}/g) || []).join('-');
}

/** Mint a fresh user recovery key. Shown once, never stored by filex. */
export function generateRecoveryKey(): string {
  return formatRecoveryKey(crypto.getRandomValues(new Uint8Array(E2E_RECOVERY_KEY_BYTES)));
}

/**
 * Parse a typed-in recovery key back to its 20 bytes, or null when it is not
 * one. Forgiving about how a human retypes it: case, dashes, spaces and the
 * Crockford look-alikes (O to 0, I/L to 1) are all normalised away.
 */
export function parseRecoveryKey(s: string): Uint8Array | null {
  const clean = (s || '')
    .toUpperCase()
    .replace(/[\s-]/g, '')
    .replace(/O/g, '0')
    .replace(/[IL]/g, '1');
  const need = Math.ceil((E2E_RECOVERY_KEY_BYTES * 8) / 5); // 32 chars
  if (clean.length !== need) return null;
  const out = new Uint8Array(E2E_RECOVERY_KEY_BYTES);
  let acc = 0;
  let bits = 0;
  let n = 0;
  for (const ch of clean) {
    const v = B32_ALPHABET.indexOf(ch);
    if (v < 0) return null;
    acc = (acc << 5) | v;
    bits += 5;
    if (bits >= 8) {
      out[n++] = (acc >>> (bits - 8)) & 0xff;
      bits -= 8;
    }
  }
  return n === E2E_RECOVERY_KEY_BYTES ? out : null;
}

/** HKDF-SHA256 the recovery key into the AES key that wraps the FMK. */
async function deriveRecoveryKek(raw: Uint8Array, salt: Uint8Array): Promise<CryptoKey> {
  const base = await crypto.subtle.importKey('raw', buf(raw), 'HKDF', false, ['deriveKey']);
  return crypto.subtle.deriveKey(
    {
      name: 'HKDF',
      hash: 'SHA-256',
      salt: buf(salt),
      info: buf(new TextEncoder().encode(RK_INFO)),
    },
    base,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt', 'decrypt'],
  );
}

async function sealRecoverySlot(fmk: Uint8Array, recoveryKey: string): Promise<E2eRecoverySlot> {
  const raw = parseRecoveryKey(recoveryKey);
  if (!raw) throw new Error('e2e: malformed recovery key');
  const salt = crypto.getRandomValues(new Uint8Array(RK_SALT_LEN));
  const rkek = await deriveRecoveryKek(raw, salt);
  const slot = { salt: bytesToB64(salt), blob: await gcmSeal(rkek, fmk) };
  raw.fill(0);
  return slot;
}

// ---------------------------------------------------------------------
// Escrow (operator recovery) — RSA-OAEP-256
// ---------------------------------------------------------------------
//
// The server holds the PUBLIC half only, so it can wrap new folders' FMKs to
// the escrow identity. The private half was handed to the admin at install
// and is supplied back by hand when it is used. A stolen filex database
// therefore decrypts nothing.

/** Import the installation escrow public key (base64 SPKI, as the server serves it). */
export async function importEscrowPublicKey(spkiB64: string): Promise<CryptoKey> {
  return crypto.subtle.importKey(
    'spki',
    buf(b64ToBytes(spkiB64)),
    { name: 'RSA-OAEP', hash: 'SHA-256' },
    true,
    ['encrypt'],
  );
}

/** Import the escrow private key the admin pastes in (base64 PKCS#8, PEM tolerated). */
export async function importEscrowPrivateKey(pkcs8B64: string): Promise<CryptoKey> {
  const clean = (pkcs8B64 || '').replace(/-----[A-Z ]+-----/g, '').replace(/\s+/g, '');
  return crypto.subtle.importKey(
    'pkcs8',
    buf(b64ToBytes(clean)),
    { name: 'RSA-OAEP', hash: 'SHA-256' },
    false,
    ['decrypt'],
  );
}

/**
 * Stable short name for an escrow key: first 8 bytes of SHA-256(SPKI), hex.
 * Written into every escrow slot so a marker says WHICH key opens it, and so
 * the UI can tell "this server's escrow key" from "some other one".
 */
export async function escrowKeyId(spkiB64: string): Promise<string> {
  const d = new Uint8Array(await crypto.subtle.digest('SHA-256', buf(b64ToBytes(spkiB64))));
  return Array.from(d.slice(0, 8))
    .map((x) => x.toString(16).padStart(2, '0'))
    .join('');
}

async function sealEscrowSlot(fmk: Uint8Array, escrowSpkiB64: string): Promise<E2eEscrowSlot> {
  const pub = await importEscrowPublicKey(escrowSpkiB64);
  const ct = new Uint8Array(await crypto.subtle.encrypt({ name: 'RSA-OAEP' }, pub, buf(fmk)));
  return { kid: await escrowKeyId(escrowSpkiB64), alg: E2E_ESCROW_ALG, blob: bytesToB64(ct) };
}

// ---------------------------------------------------------------------
// Marker create / parse / verify
// ---------------------------------------------------------------------

/**
 * Create a v1 folder marker — the pre-0.31 format, with NO recovery of any
 * kind.
 *
 * @deprecated Use `createEncryptedFolder`. Kept exported, and kept producing
 * a genuine v1 marker, so an embedder pinned to the old API keeps creating
 * folders this build can still open rather than half-formed v2 ones.
 */
export async function createMarker(
  password: string,
  iterations: number = E2E_DEFAULT_ITERATIONS,
): Promise<{ marker: E2eMarker; kek: CryptoKey }> {
  const iter = Math.max(E2E_MIN_ITERATIONS, iterations);
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const kek = await deriveKek(password, salt, iter);
  const verify = await gcmSeal(kek, new TextEncoder().encode(VERIFY_PLAINTEXT));
  return { marker: { v: 1, salt: bytesToB64(salt), iter, verify }, kek };
}

export interface CreateFolderOptions {
  iterations?: number;
  /** Base64 SPKI of the installation escrow key, when escrow is enabled. */
  escrowPublicKey?: string | null;
}

export interface CreatedFolder {
  marker: E2eMarker;
  /** The folder master key, ready for encryptFile/decryptFile. */
  fmk: CryptoKey;
  /** Show this ONCE. filex never stores it and can never show it again. */
  recoveryKey: string;
}

/**
 * Create a v2 encrypted folder: random FMK, wrapped under the password KEK,
 * under a freshly minted user recovery key, and — when the installation has
 * escrow enabled — to the escrow public key.
 */
export async function createEncryptedFolder(
  password: string,
  opts: CreateFolderOptions = {},
): Promise<CreatedFolder> {
  const iter = Math.max(E2E_MIN_ITERATIONS, opts.iterations ?? E2E_DEFAULT_ITERATIONS);
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const kek = await deriveKek(password, salt, iter);
  const rawFmk = crypto.getRandomValues(new Uint8Array(FMK_LEN));
  const recoveryKey = generateRecoveryKey();

  const marker: E2eMarker = {
    v: E2E_MARKER_VERSION,
    salt: bytesToB64(salt),
    iter,
    verify: await gcmSeal(kek, new TextEncoder().encode(VERIFY_PLAINTEXT)),
    fmk: 'wrapped',
    fmk_pw: await gcmSeal(kek, rawFmk),
    rk: await sealRecoverySlot(rawFmk, recoveryKey),
  };
  if (opts.escrowPublicKey) marker.esc = await sealEscrowSlot(rawFmk, opts.escrowPublicKey);

  const fmk = await importFmk(rawFmk);
  rawFmk.fill(0);
  return { marker, fmk, recoveryKey };
}

/**
 * Give an existing v1 folder recovery keys, in place and without rewriting a
 * single file.
 *
 * The v1 files are wrapped under the password KEK, so the FMK stays defined
 * as "the KEK" (`fmk: 'kek'`) and the new slots wrap those raw bytes. The
 * password path afterwards is byte-identical to what it was.
 *
 * ⚠ Requires the password — this is only callable at the one moment filex
 * ever has it. There is no way to give a v1 folder recovery without it.
 * ⚠ When the installation has escrow on, this ALSO hands the operator a key
 * to a folder that did not have one. The caller must say so before asking.
 */
export async function upgradeMarkerV1(
  marker: E2eMarker,
  password: string,
  opts: CreateFolderOptions = {},
): Promise<CreatedFolder> {
  if (marker.v !== 1) throw new Error('e2e: not a v1 marker');
  const salt = b64ToBytes(marker.salt);
  const kek = await deriveKek(password, salt, marker.iter);
  // Prove the password before touching anything.
  const ok = await gcmOpen(kek, marker.verify);
  if (!ok || new TextDecoder().decode(ok) !== VERIFY_PLAINTEXT) {
    throw new E2eDecryptError('e2e: wrong password');
  }
  const rawKek = await deriveKekBits(password, salt, marker.iter);
  const recoveryKey = generateRecoveryKey();
  const next: E2eMarker = {
    v: E2E_MARKER_VERSION,
    salt: marker.salt,
    iter: marker.iter,
    verify: marker.verify,
    fmk: 'kek',
    rk: await sealRecoverySlot(rawKek, recoveryKey),
  };
  if (opts.escrowPublicKey) next.esc = await sealEscrowSlot(rawKek, opts.escrowPublicKey);
  rawKek.fill(0);
  return { marker: next, fmk: kek, recoveryKey };
}

/**
 * Give an EXISTING v2 folder an escrow slot, in place, using the folder
 * password its owner has just typed.
 *
 * ── Why this exists ─────────────────────────────────────────────────
 *
 * Escrow used to be all-or-nothing at install time, and then adoptable but
 * never retroactive: on any installation that had been running for a while,
 * escrow covered only folders nobody had created yet. On a real deployment
 * the folders that matter already exist, so "new folders only" means escrow
 * covers nothing anyone cares about.
 *
 * The server still cannot do this, and that has not changed: adding a slot
 * needs the folder master key, which needs a credential the server has never
 * held. What CAN do it is the browser, at the one moment the password is in
 * memory — exactly where `upgradeMarkerV1` already lives. Same moment, same
 * shape, different slot.
 *
 * ⚠⚠ Accepting hands the operator of this installation a second, permanent
 * way into this folder. It is the folder's owner who decides, from inside,
 * with the password; no configuration change and no admin action can do it
 * for them. The caller MUST say that in those words before calling this —
 * see `e2e.escrowoffer.*` in the locales.
 *
 * ⚠ v2 only. A v1 marker has no slots at all; the path for those is
 * `upgradeMarkerV1`, which already seals an escrow slot when the
 * installation has a key and already discloses it. Two doors into the same
 * room would be two chances to get the disclosure wrong.
 *
 * ⚠ No file is re-encrypted, moved or rewritten. Only `.filex-e2e.json`
 * changes, and only by gaining `esc` (and losing `esc_declined`).
 */
export async function addEscrowSlot(
  marker: E2eMarker,
  password: string,
  escrowPublicKey: string,
): Promise<E2eMarker> {
  if (marker.v !== 2) throw new Error('e2e: not a v2 marker');
  if (marker.esc) throw new Error('e2e: this folder already has an escrow slot');
  if (!escrowPublicKey) throw new Error('e2e: no escrow public key');

  const salt = b64ToBytes(marker.salt);
  const kek = await deriveKek(password, salt, marker.iter);
  // Prove the password before touching anything, exactly as the v1 upgrade
  // does. A wrong password here must not produce a marker at all — half a
  // marker is a folder nobody can open.
  const proof = await gcmOpen(kek, marker.verify);
  if (!proof || new TextDecoder().decode(proof) !== VERIFY_PLAINTEXT) {
    throw new E2eDecryptError('e2e: wrong password');
  }

  // The raw FMK, by mode. `wrapped` keeps a random FMK in `fmk_pw`, so the
  // bytes come back from a GCM open and no extractable KEK is ever derived.
  // `kek` (a v1 folder upgraded in place) defines the FMK AS the password
  // key, so those very bytes are what the slot has to wrap — the one case
  // that needs `deriveKekBits`, for the same reason `upgradeMarkerV1` does.
  let rawFmk: Uint8Array;
  if (marker.fmk === 'wrapped') {
    const opened = marker.fmk_pw ? await gcmOpen(kek, marker.fmk_pw) : null;
    if (!opened || opened.length !== FMK_LEN) {
      throw new E2eDecryptError('e2e: could not unwrap the folder master key');
    }
    rawFmk = opened;
  } else {
    rawFmk = await deriveKekBits(password, salt, marker.iter);
  }

  const esc = await sealEscrowSlot(rawFmk, escrowPublicKey);
  rawFmk.fill(0);

  const next: E2eMarker = { ...marker, esc };
  // A decline that is now moot. Leaving it would make the record say two
  // contradictory things about the same folder.
  delete next.esc_declined;
  return next;
}

/**
 * Record that this folder's owner was offered an escrow slot and declined.
 *
 * A refusal is a decision, not a delay: without this the offer would come
 * back on every single unlock, which is how people learn to click past
 * security dialogs without reading them. Nothing about the folder's keys
 * changes — the only effect is that filex stops asking.
 *
 * Reversible by `addEscrowSlot`, which is the way back for somebody who
 * says no today and changes their mind next month.
 */
export function declineEscrowSlot(marker: E2eMarker, when: string): E2eMarker {
  return { ...marker, esc_declined: when };
}

/**
 * Whether this folder's owner should be offered an escrow slot, and whether
 * they have already answered.
 *
 *   'n/a'       nothing to offer: the installation has no escrow key, the
 *               folder already has a slot, or the marker is v1 (whose path
 *               is `upgradeMarkerV1`).
 *   'offer'     the offer applies and no answer has been recorded.
 *   'declined'  the offer applies and the owner said no. Do not ask again;
 *               leave a way back.
 *
 * ⚠ This deliberately does NOT look at whether the folder is unlocked. That
 * is the caller's business, and it matters: the offer may only be shown
 * after an unlock actually succeeded, because accepting needs the password
 * and because asking someone who cannot open the folder to give away a key
 * to it is asking the wrong person.
 */
export type EscrowOfferState = 'n/a' | 'offer' | 'declined';

export function escrowOfferState(
  m: E2eMarker | null,
  installationKid: string | null | undefined,
): EscrowOfferState {
  if (!installationKid) return 'n/a';
  if (!m || m.v !== 2 || m.esc) return 'n/a';
  return m.esc_declined ? 'declined' : 'offer';
}

/** Parse marker JSON text; returns null when the shape is not a marker we read. */
export function parseMarker(text: string): E2eMarker | null {
  try {
    const m = JSON.parse(text) as E2eMarker;
    if (!m || (m.v !== 1 && m.v !== 2)) return null;
    if (typeof m.salt !== 'string' || typeof m.verify !== 'string') return null;
    if (typeof m.iter !== 'number' || m.iter < 1) return null;
    if (m.v === 2) {
      if (m.fmk !== 'kek' && m.fmk !== 'wrapped') return null;
      if (m.fmk === 'wrapped' && typeof m.fmk_pw !== 'string') return null;
    }
    return m;
  } catch {
    return null;
  }
}

/** True when the folder has a user recovery key slot. */
export function markerHasRecovery(m: E2eMarker | null): boolean {
  return !!m && m.v === 2 && !!m.rk;
}

/** True when the folder has an operator escrow slot. */
export function markerHasEscrow(m: E2eMarker | null): boolean {
  return !!m && m.v === 2 && !!m.esc;
}

/**
 * Why the escrow door is, or is not, on offer for this folder.
 *
 *   'off'        this installation has no escrow key at all.
 *   'available'  the folder is sealed to THIS installation's escrow key.
 *   'predates'   the installation has an escrow key, and this folder has no
 *                escrow slot: it was created before escrow existed here.
 *   'other-key'  the folder carries an escrow slot sealed to a DIFFERENT
 *                key id — it came from another installation, via a restore
 *                or a copied data directory.
 *
 * ⚠ 'predates' exists because escrow can be ADOPTED by an installation
 * that already has folders (FILEX_INSTALLATION_E2E_ESCROW_ADOPT), and
 * adoption is not retroactive: the folder's master key was wrapped to its
 * recovery paths when the folder was created. Before this distinction
 * existed the dialog simply showed no Escrow tab, which is true but says
 * nothing — an admin who knows escrow is on reads a missing tab as a bug,
 * tries the key anyway, and learns the real answer from a failure. The UI
 * has to say it instead.
 *
 * ⚠⚠ 'predates' means "not as things stand", NOT "never". The folder's
 * owner can add a slot from inside with the password (`addEscrowSlot`,
 * offered at unlock). Any wording built on this state has to leave that
 * door visible, or it tells an operator their escrow key can never reach a
 * folder whose owner could hand it over this afternoon.
 *
 * ⚠ 'other-key' was a quieter lie: the dialog labelled the escrow field
 * with the INSTALLATION's key id whatever the folder's slot said, so a
 * folder restored from another install looked openable by the key the
 * operator has, and was not.
 */
export type EscrowAvailability = 'off' | 'available' | 'predates' | 'other-key';

export function escrowAvailability(
  m: E2eMarker | null,
  installationKid: string | null | undefined,
): EscrowAvailability {
  if (!installationKid) return 'off';
  if (!markerHasEscrow(m)) return 'predates';
  return m!.esc!.kid === installationKid ? 'available' : 'other-key';
}

/**
 * Check `password` against a folder marker. Resolves to the derived KEK on
 * success, or `null` on a wrong password (GCM tag mismatch on the verify
 * blob). Never talks to any server.
 *
 * ⚠ This returns the KEK, not the FMK. On a v1 folder they are the same key;
 * on a v2 `fmk: 'wrapped'` folder they are not. Use `unlockWithPassword` to
 * get the key that actually decrypts files.
 */
export async function verifyPassword(
  marker: E2eMarker,
  password: string,
): Promise<CryptoKey | null> {
  const kek = await deriveKek(password, b64ToBytes(marker.salt), marker.iter);
  const pt = await gcmOpen(kek, marker.verify);
  if (!pt || new TextDecoder().decode(pt) !== VERIFY_PLAINTEXT) return null;
  return kek;
}

// ---------------------------------------------------------------------
// Unlock — the three ways to reach the FMK
// ---------------------------------------------------------------------

/** Turn a password KEK into the FMK for this marker. */
async function fmkFromKek(marker: E2eMarker, kek: CryptoKey): Promise<CryptoKey | null> {
  // v1, and v2 folders upgraded from v1: the files are wrapped by the KEK.
  if (marker.v === 1 || marker.fmk === 'kek') return kek;
  if (!marker.fmk_pw) return null;
  const raw = await gcmOpen(kek, marker.fmk_pw);
  if (!raw || raw.length !== FMK_LEN) return null;
  const fmk = await importFmk(raw);
  raw.fill(0);
  return fmk;
}

/**
 * Unlock with the folder password. Returns the FMK (the key `decryptFile`
 * wants) or null when the password is wrong.
 */
export async function unlockWithPassword(
  marker: E2eMarker,
  password: string,
): Promise<CryptoKey | null> {
  const kek = await verifyPassword(marker, password);
  if (!kek) return null;
  return fmkFromKek(marker, kek);
}

/**
 * Unlock with the user recovery key shown when the folder was created.
 * Returns null for a malformed key, a wrong key, or a folder that has no
 * recovery slot at all — the caller cannot tell those apart, and neither can
 * an attacker.
 */
export async function unlockWithRecoveryKey(
  marker: E2eMarker,
  recoveryKey: string,
): Promise<CryptoKey | null> {
  if (marker.v !== 2 || !marker.rk) return null;
  const raw = parseRecoveryKey(recoveryKey);
  if (!raw) return null;
  let salt: Uint8Array;
  try {
    salt = b64ToBytes(marker.rk.salt);
  } catch {
    return null;
  }
  const rkek = await deriveRecoveryKek(raw, salt);
  raw.fill(0);
  const fmkRaw = await gcmOpen(rkek, marker.rk.blob);
  if (!fmkRaw || fmkRaw.length !== FMK_LEN) return null;
  const fmk = await importFmk(fmkRaw);
  fmkRaw.fill(0);
  return fmk;
}

/**
 * Unlock with the installation escrow private key.
 *
 * Returns null when the folder has no escrow slot — which is the case for
 * every folder created while escrow was off, and is why escrow cannot be
 * turned on retroactively.
 */
export async function unlockWithEscrowKey(
  marker: E2eMarker,
  privateKey: CryptoKey,
): Promise<CryptoKey | null> {
  if (marker.v !== 2 || !marker.esc || marker.esc.alg !== E2E_ESCROW_ALG) return null;
  let raw: Uint8Array;
  try {
    raw = new Uint8Array(
      await crypto.subtle.decrypt(
        { name: 'RSA-OAEP' },
        privateKey,
        buf(b64ToBytes(marker.esc.blob)),
      ),
    );
  } catch {
    return null; // wrong escrow key, or a slot sealed to another installation
  }
  if (raw.length !== FMK_LEN) return null;
  const fmk = await importFmk(raw);
  raw.fill(0);
  return fmk;
}

// ---------------------------------------------------------------------
// Magic sniff
// ---------------------------------------------------------------------

/** True when the buffer starts with the 'filexe2e' magic. */
export function hasMagic(data: ArrayBuffer | Uint8Array): boolean {
  const b = data instanceof Uint8Array ? data : new Uint8Array(data);
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
 * Encrypt `content` under the folder master key: mints a fresh DEK, encrypts
 * the content one-shot, wraps the DEK with the FMK and prepends the fixed
 * 'filexe2e' header. Throws when content exceeds E2E_MAX_FILE_BYTES.
 *
 * `fmk` is the key an unlock returned. On a v1 folder that is the password
 * KEK, which is why v1 files keep working untouched.
 */
export async function encryptFile(fmk: CryptoKey, content: ArrayBuffer): Promise<ArrayBuffer> {
  if (content.byteLength > E2E_MAX_FILE_BYTES) {
    throw new Error('e2e: file exceeds the 200MB single-shot limit');
  }
  const rawDek = crypto.getRandomValues(new Uint8Array(32));
  const dek = await crypto.subtle.importKey('raw', buf(rawDek), { name: 'AES-GCM' }, false, [
    'encrypt',
  ]);
  const wrapIV = crypto.getRandomValues(new Uint8Array(IV_LEN));
  const dataIV = crypto.getRandomValues(new Uint8Array(IV_LEN));
  const wrappedDek = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: buf(wrapIV) }, fmk, buf(rawDek)),
  );
  const ct = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: buf(dataIV) }, dek, content),
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
 * Decrypt a 'filexe2e' blob with the folder master key. Throws
 * E2eDecryptError on a wrong key / tampered data, and a plain Error when the
 * header is not an e2e file at all.
 */
export async function decryptFile(fmk: CryptoKey, data: ArrayBuffer): Promise<ArrayBuffer> {
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
    rawDek = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: buf(wrapIV) }, fmk, buf(wrappedDek));
  } catch {
    throw new E2eDecryptError('e2e: DEK unwrap failed (wrong key?)');
  }
  const dek = await crypto.subtle.importKey('raw', rawDek, { name: 'AES-GCM' }, false, ['decrypt']);
  try {
    return await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: buf(dataIV) },
      dek,
      buf(b.slice(HEADER_LEN)),
    );
  } catch {
    throw new E2eDecryptError('e2e: content decrypt failed');
  }
}

// ---------------------------------------------------------------------
// In-memory session key ring
// ---------------------------------------------------------------------

/**
 * Tiny per-explorer key ring: encrypted-folder root (wire path) → FMK.
 * Lives ONLY in memory — "Lock" drops the entry, a reload drops all.
 */
export function createKeyRing() {
  const keys = new Map<string, CryptoKey>();
  return {
    get(root: string): CryptoKey | undefined {
      return keys.get(root);
    },
    set(root: string, fmk: CryptoKey): void {
      keys.set(root, fmk);
    },
    /** Drop one folder's key ("Lock"). */
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
