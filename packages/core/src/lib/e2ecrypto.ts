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
 * v3 is v2 plus REQUIRED FEATURES (`req`). A client must understand every
 * entry of `req` or refuse the folder — the ext4 "incompat flag" rule. The
 * only feature today is `names` (encrypted file and folder names, see
 * lib/e2enames.ts): the marker then carries a `names` slot, a random name
 * key wrapped by the FMK. A folder without encrypted names stays v2 on
 * purpose, so every filex since 0.31 keeps opening it. A folder WITH them
 * must not open in an older filex at all: that build would show ciphertext
 * as names and write plaintext names next to them. filex ≤ 0.47 rejects
 * `v: 3` outright, which is the refusal we want.
 *
 * File layout ('filexe2e' magic, fixed 97-byte header) — UNCHANGED in v2/v3:
 *   [0..8)   magic  "filexe2e"
 *   [8]      version 0x01
 *   [9..21)  wrapIV  (12B)  — GCM IV of the DEK wrap
 *   [21..69) wrappedDEK (48B = 32B DEK + 16B GCM tag), wrapped by the FMK
 *   [69..81) dataIV  (12B)  — GCM IV of the content
 *   [81..97) reserved (zeros; v2 chunking/metadata)
 *   [97..)   ciphertext (content + 16B GCM tag)
 */

import {
  E2E_NAMES_ALG,
  E2E_NAMES_ENC,
  E2E_NAMES_LONG_DEFAULT,
  b64urlDecode,
  b64urlEncode,
  E2E_DIR_ID_BYTES,
  generateNameKeyBytes,
  generateRootId,
  importNameKey,
  type E2eNameKey,
} from './e2enames';
/* wiring:e2 stream — a folder file over E2E_MAX_FILE_BYTES is a STREAM file,
 * header version 0x02 (lib/e2estream.ts). `decryptFile` and `rewrapFileKey`
 * below read both versions; the header up to offset 69 is the same. */
import { E2E_FILE_VERSION_STREAM, decryptStreamFolderFileBytes } from './e2estream';

export const E2E_MARKER_NAME = '.filex-e2e.json';
export const E2E_MAGIC = 'filexe2e';
/** File-header version byte. Unchanged by the recovery work. */
export const E2E_VERSION = 1;
/** Marker schema version written for a folder WITHOUT encrypted names. v1 still reads. */
export const E2E_MARKER_VERSION = 2;
/** Marker schema version of a folder that carries required features (`req`). */
export const E2E_MARKER_VERSION_FEATURES = 3;
/** Required features this build understands. Anything else in `req` → refuse. */
export const E2E_KNOWN_FEATURES: readonly string[] = ['names', 'rekey', 'conv'];
export const E2E_DEFAULT_ITERATIONS = 600_000;
export const E2E_MIN_ITERATIONS = 600_000;
/** The most a marker may ask for (a hostile `iter` would hang the tab). */
export const E2E_MAX_ITERATIONS = 100_000_000;
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

/**
 * Encrypted-names slot (marker v3, feature `names`). Nothing in it is secret:
 * the key is sealed under the FMK, and the rest is the recipe.
 */
export interface E2eNamesSlot {
  /** E2E_NAMES_ALG — AES-SIV (RFC 5297) with AES-256. */
  alg: string;
  /** E2E_NAMES_ENC — base64url, no padding. */
  enc: string;
  /** Encoded names longer than this are shortened to `<hash>.fxl` + sidecar. */
  long: number;
  /** base64: 12B IV || AES-GCM(FMK, 64-byte name key). */
  key: string;
  /**
   * base64url of the encrypted root's own 16-byte folder id: the associated
   * data of every name directly inside the root (lib/e2enames → "Folder
   * ids"). Not secret; random, minted with the name key.
   */
  root_id: string;
  /**
   * A switch from plaintext names to encrypted names started and has not
   * finished: some entries may still carry their plaintext name. The client
   * offers to resume; the rename pass is idempotent, so resuming is simply
   * running it again. Absent on a folder created with encrypted names.
   */
  pending?: boolean;
}

/**
 * A re-key in progress (marker v3, feature `rekey`): the folder has a NEW
 * folder master key, and some files still have their DEK wrapped under the
 * previous one. `from` is that previous key, sealed under the new one, so any
 * way into the folder also reaches the files not re-wrapped yet — and resuming
 * the re-wrap needs nothing but an unlock. Removed when the last file is done.
 */
export interface E2eRekeySlot {
  /** base64: 12B IV || AES-GCM(new FMK, previous FMK raw 32B). */
  from: string;
  pending: true;
}

/**
 * An existing folder being encrypted in place (marker v3, feature `conv`):
 * some of its files may still be plaintext. The folder is an encrypted folder
 * to the server from the moment this key file lands (the transfer guard, the
 * thumbnailer and the indexer treat it so), and while `pending` the server
 * lets a write replace a plaintext file with its ciphertext without keeping
 * the plaintext as a version. Removed when every file carries the magic.
 */
export interface E2eConvSlot {
  pending: true;
  /** ISO time the conversion started. */
  started?: string;
  /** What to remove when it finishes (the owner's choice in the dialog,
   *  kept here so a resumed run honours it): versions, trash entries. */
  cleanup?: { versions: boolean; trash: boolean };
}

export interface E2eMarker {
  v: number;
  /** v3 only: features a client must understand to open this folder. */
  req?: string[];
  /** v3 + req 'rekey': a re-key in progress. */
  rekey?: E2eRekeySlot;
  /** v3 + req 'conv': an in-place conversion in progress. */
  conv?: E2eConvSlot;
  /** v3 + req 'names': the encrypted-names slot. */
  names?: E2eNamesSlot;
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

/* wiring:e2 fxe — the sealed-blob shape, for a container that reuses the
 * marker's slots (lib/e2efile.ts seals a file's DEK and its name with these).
 * Same bytes as every blob in a marker: base64(12B IV ‖ ciphertext ‖ tag). */
/** Seal `plain` under `key` as a marker-style blob. */
export function sealBlob(key: CryptoKey, plain: Uint8Array): Promise<string> {
  return gcmSeal(key, plain);
}
/** Open a marker-style blob; null for a wrong key or a damaged blob. */
export function openBlob(key: CryptoKey, b64: string): Promise<Uint8Array | null> {
  return gcmOpen(key, b64);
}
/* /wiring:e2 fxe */

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
  /**
   * Encrypt file and folder names too (marker v3, feature `names`). The
   * default for a new folder in the UI; false keeps the v2 content-only
   * folder that every filex since 0.31 can open.
   */
  encryptNames?: boolean;
}

export interface CreatedFolder {
  marker: E2eMarker;
  /** The folder master key, ready for encryptFile/decryptFile. */
  fmk: CryptoKey;
  /** Show this ONCE. filex never stores it and can never show it again. */
  recoveryKey: string;
  /** The name key, when the folder was created with encrypted names. */
  names?: E2eNameKey;
}

/** Mint a name key and seal it under the FMK. The raw bytes are zeroed. */
async function sealNamesSlot(
  fmk: CryptoKey,
  pending: boolean,
): Promise<{ slot: E2eNamesSlot; key: E2eNameKey }> {
  const raw = generateNameKeyBytes();
  const rootId = generateRootId();
  const slot: E2eNamesSlot = {
    alg: E2E_NAMES_ALG,
    enc: E2E_NAMES_ENC,
    long: E2E_NAMES_LONG_DEFAULT,
    key: await gcmSeal(fmk, raw),
    root_id: b64urlEncode(rootId),
  };
  if (pending) slot.pending = true;
  const key = await importNameKey(raw, slot.long, rootId);
  raw.fill(0);
  return { slot, key };
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
  if (!opts.encryptNames) return { marker, fmk, recoveryKey };
  const sealed = await sealNamesSlot(fmk, false);
  marker.v = E2E_MARKER_VERSION_FEATURES;
  marker.req = ['names'];
  marker.names = sealed.slot;
  return { marker, fmk, recoveryKey, names: sealed.key };
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
  if (!hasSlots(marker)) throw new Error('e2e: not a v2 marker');
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
  if (!m || !hasSlots(m) || m.esc) return 'n/a';
  return m.esc_declined ? 'declined' : 'offer';
}

/** True for a marker that carries key slots: v2, and v3 (v2 + features). */
function hasSlots(m: E2eMarker): boolean {
  return m.v === 2 || m.v === E2E_MARKER_VERSION_FEATURES;
}

/** A parsed marker, plus the required features this build does not know. */
export interface ParsedMarker {
  marker: E2eMarker;
  /**
   * Entries of `req` this build cannot honour. Non-empty means: do NOT open
   * the folder — say which feature is missing and that a newer filex is
   * needed. Opening it anyway is how an old client writes plaintext into a
   * folder that promised otherwise.
   */
  unsupported: string[];
}

/**
 * Parse marker JSON text, keeping what the caller needs to REFUSE a folder
 * honestly. Null when the text is not a marker at all (or a malformed one);
 * a well-formed v3 marker with a feature this build does not know comes back
 * with that feature in `unsupported`.
 */
export function parseMarkerDetailed(text: string): ParsedMarker | null {
  let m: E2eMarker;
  try {
    m = JSON.parse(text) as E2eMarker;
  } catch {
    return null;
  }
  if (!m || typeof m !== 'object') return null;
  if (m.v !== 1 && !hasSlots(m)) return null;
  if (typeof m.salt !== 'string' || typeof m.verify !== 'string') return null;
  // An integer, and bounded: the unlock derives with whatever the marker says,
  // and `iter: 1e12` in a hostile key file would hang the tab (filex writes
  // 600 000; `filex decrypt` accepts the same range).
  if (typeof m.iter !== 'number' || !Number.isInteger(m.iter) || m.iter < 1 || m.iter > E2E_MAX_ITERATIONS) {
    return null;
  }
  if (hasSlots(m)) {
    if (m.fmk !== 'kek' && m.fmk !== 'wrapped') return null;
    if (m.fmk === 'wrapped' && typeof m.fmk_pw !== 'string') return null;
  }
  const unsupported: string[] = [];
  if (m.v === E2E_MARKER_VERSION_FEATURES) {
    if (!Array.isArray(m.req) || m.req.some((f) => typeof f !== 'string')) return null;
    for (const f of m.req) if (!E2E_KNOWN_FEATURES.includes(f)) unsupported.push(f);
    if (m.req.includes('names') && !validNamesSlot(m.names)) return null;
    if (m.req.includes('rekey')) {
      if (m.fmk !== 'wrapped' || !m.rekey || typeof m.rekey.from !== 'string') return null;
    } else if (m.rekey !== undefined) {
      return null;
    }
    if (m.req.includes('conv')) {
      if (!m.conv || typeof m.conv !== 'object' || m.conv.pending !== true) return null;
    } else if (m.conv !== undefined) {
      return null;
    }
  } else if (m.req !== undefined || m.names !== undefined || m.rekey !== undefined || m.conv !== undefined) {
    // A v1/v2 marker carrying v3 fields is not something any filex wrote.
    return null;
  }
  return { marker: m, unsupported };
}

function validNamesSlot(n: E2eNamesSlot | undefined): boolean {
  return (
    !!n &&
    typeof n === 'object' &&
    n.alg === E2E_NAMES_ALG &&
    n.enc === E2E_NAMES_ENC &&
    typeof n.key === 'string' &&
    typeof n.long === 'number' &&
    Number.isInteger(n.long) &&
    n.long >= 64 &&
    n.long <= 255 &&
    typeof n.root_id === 'string' &&
    b64urlDecode(n.root_id)?.length === E2E_DIR_ID_BYTES
  );
}

/**
 * Parse marker JSON text; returns null when the shape is not a marker this
 * build can OPEN — including a v3 marker that requires a feature it does not
 * know. Callers that want to explain a refusal use `parseMarkerDetailed`.
 */
export function parseMarker(text: string): E2eMarker | null {
  const p = parseMarkerDetailed(text);
  if (!p || p.unsupported.length > 0) return null;
  return p.marker;
}

/** True when the folder's file and folder names are encrypted (feature `names`). */
export function markerHasNames(m: E2eMarker | null): boolean {
  return (
    !!m &&
    m.v === E2E_MARKER_VERSION_FEATURES &&
    Array.isArray(m.req) &&
    m.req.includes('names') &&
    !!m.names
  );
}

/**
 * Unwrap the folder's name key with the FMK an unlock returned. Null when the
 * folder has no encrypted names, or the slot does not open under this FMK
 * (a marker from another folder, or a damaged one).
 */
export async function unlockNameKey(marker: E2eMarker, fmk: CryptoKey): Promise<E2eNameKey | null> {
  if (!markerHasNames(marker)) return null;
  const raw = await gcmOpen(fmk, marker.names!.key);
  if (!raw || raw.length !== 64) return null;
  const key = await importNameKey(raw, marker.names!.long, b64urlDecode(marker.names!.root_id)!);
  raw.fill(0);
  return key;
}

/**
 * Start encrypting the names of an EXISTING content-only folder (v2 → v3).
 *
 * Only the marker changes here, and it changes FIRST: the returned marker
 * carries a fresh name key and `names.pending`, and must be written before a
 * single entry is renamed. That order is what makes the switch resumable —
 * an interrupted pass leaves a folder whose marker already says "names are
 * encrypted, some may not be yet", every renamed entry decrypts, and every
 * entry not yet renamed is recognisably plaintext. Running the pass again
 * finishes it.
 *
 * Needs the FMK, not the password: the folder has to be unlocked, which is
 * the only state in which the offer is shown, and the ring's FMK came from
 * unlocking this very marker.
 *
 * ⚠ After this the folder is v3, which filex ≤ 0.47 refuses to open. That
 * is deliberate (an old client would write plaintext names into it) and the
 * UI says so before asking.
 */
export async function enableNames(
  marker: E2eMarker,
  fmk: CryptoKey,
): Promise<{ marker: E2eMarker; names: E2eNameKey }> {
  if (marker.v !== 2) throw new Error('e2e: only a v2 folder can switch to encrypted names');
  const sealed = await sealNamesSlot(fmk, true);
  const next: E2eMarker = {
    ...marker,
    v: E2E_MARKER_VERSION_FEATURES,
    req: ['names'],
    names: sealed.slot,
  };
  return { marker: next, names: sealed.key };
}

/** A switch to encrypted names finished: drop `pending`. */
export function finishNames(marker: E2eMarker): E2eMarker {
  if (!markerHasNames(marker)) throw new Error('e2e: this folder has no encrypted names');
  const names = { ...marker.names! };
  delete names.pending;
  return { ...marker, names };
}

/**
 * A folder's encryption LEVEL — a property of the folder, chosen when it is
 * encrypted and changed only by a deliberate act in its settings, never by an
 * offer that pops up (docs/E2E-ENCRYPTION.md → "Encryption levels").
 *
 *   'content'   level 1: contents encrypted, names readable (marker v1/v2).
 *               Can move up to 'names' (v2; a v1 folder first gets its
 *               recovery upgrade, which makes it v2).
 *   'names'     level 2: contents and names encrypted (v3, req 'names').
 *   'pending'   a move from 1 to 2 started and did not finish.
 *
 * Level 3, the vault, is designed (docs/E2E-ROADMAP.md) and not built; no
 * marker carries it yet, and nothing offers it.
 */
export type EncryptionLevel = 'content' | 'names' | 'pending';

/** The levels a folder can be GIVEN today, in order (level 1 first — the
 *  default). The vault joins this list when it works, not before. */
export type ChoosableLevel = 'content' | 'names';
export const E2E_CHOOSABLE_LEVELS: readonly ChoosableLevel[] = ['content', 'names'];
export const E2E_DEFAULT_LEVEL: ChoosableLevel = 'content';

export function encryptionLevel(m: E2eMarker | null): EncryptionLevel {
  if (m && markerHasNames(m)) return m.names!.pending ? 'pending' : 'names';
  return 'content';
}

/** May this folder move from level 1 to level 2 now? (v2 only.) */
export function canRaiseToNames(m: E2eMarker | null): boolean {
  return !!m && m.v === 2 && !markerHasNames(m);
}

/** True when the folder has a user recovery key slot. */
export function markerHasRecovery(m: E2eMarker | null): boolean {
  return !!m && hasSlots(m) && !!m.rk;
}

/** True when the folder has an operator escrow slot. */
export function markerHasEscrow(m: E2eMarker | null): boolean {
  return !!m && hasSlots(m) && !!m.esc;
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
 * `unlockWithPassword`, telling a wrong password from a damaged key file: the
 * password proved right (the verify blob opened) and the folder key still did
 * not unwrap. "Wrong password" for that case sends a person hunting for a
 * password they already have.
 */
export async function unlockWithPasswordDetailed(
  marker: E2eMarker,
  password: string,
): Promise<{ fmk: CryptoKey } | { error: 'wrong' | 'damaged' }> {
  const kek = await verifyPassword(marker, password);
  if (!kek) return { error: 'wrong' };
  const fmk = await fmkFromKek(marker, kek);
  return fmk ? { fmk } : { error: 'damaged' };
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
  if (!hasSlots(marker) || !marker.rk) return null;
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
  if (!hasSlots(marker) || !marker.esc || marker.esc.alg !== E2E_ESCROW_ALG) return null;
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
  /* wiring:e2 stream — a 0x02 file, whole in memory. */
  if (b[8] === E2E_FILE_VERSION_STREAM) return decryptStreamFolderFileBytes(fmk, b);
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
// Changing the password, and re-keying
// ---------------------------------------------------------------------
//
// A folder whose FMK is random (`fmk: 'wrapped'`, every folder since 0.31)
// changes its password by re-wrapping that one key: a new salt, a new verify
// blob and a new `fmk_pw`, and nothing else — no file, and no other slot,
// because the recovery key, the escrow key and the name key all reach the
// same FMK.
//
// A folder whose FMK IS the password-derived key (v1, and v2 `fmk: 'kek'`)
// cannot: a new password is a new FMK, and every file's DEK is wrapped under
// the old one. That is a RE-KEY: a fresh random FMK, every DEK re-wrapped
// under it (the 48 bytes at [21..69) of each header, plus its IV — the
// content ciphertext is never touched), and the marker moved to
// `fmk: 'wrapped'`. It is resumable: the marker is written FIRST, with the
// previous FMK sealed under the new one (`rekey.from`, feature `rekey`), so a
// later session reaches every file whichever key its DEK is under, and
// running the re-wrap again finishes it. The same machinery re-keys a wrapped
// folder on purpose, when the old password may be known to someone.

/** How the person proves they may change the password. */
export type E2eCredential = { password: string } | { recoveryKey: string };

/**
 * The raw FMK, from the password or the recovery key. Throws E2eDecryptError
 * for a wrong one. The caller zeroes the result.
 *
 * ⚠ The second place raw FMK bytes exist (after unlockWithRecoveryKey's
 * import): re-wrapping a key needs its bytes, and a non-extractable CryptoKey
 * has none to give.
 */
async function fmkRawFrom(marker: E2eMarker, cred: E2eCredential): Promise<Uint8Array> {
  if ('recoveryKey' in cred) {
    if (!hasSlots(marker) || !marker.rk) throw new E2eDecryptError('e2e: this folder has no recovery key');
    const raw = parseRecoveryKey(cred.recoveryKey);
    if (!raw) throw new E2eDecryptError('e2e: wrong recovery key');
    let salt: Uint8Array;
    try {
      salt = b64ToBytes(marker.rk.salt);
    } catch {
      throw new E2eDecryptError('e2e: damaged recovery slot');
    }
    const rkek = await deriveRecoveryKek(raw, salt);
    raw.fill(0);
    const fmk = await gcmOpen(rkek, marker.rk.blob);
    if (!fmk || fmk.length !== FMK_LEN) throw new E2eDecryptError('e2e: wrong recovery key');
    return fmk;
  }
  const salt = b64ToBytes(marker.salt);
  const kek = await deriveKek(cred.password, salt, marker.iter);
  const proof = await gcmOpen(kek, marker.verify);
  if (!proof || new TextDecoder().decode(proof) !== VERIFY_PLAINTEXT) {
    throw new E2eDecryptError('e2e: wrong password');
  }
  if (marker.v === 1 || marker.fmk === 'kek') return deriveKekBits(cred.password, salt, marker.iter);
  const fmk = marker.fmk_pw ? await gcmOpen(kek, marker.fmk_pw) : null;
  if (!fmk || fmk.length !== FMK_LEN) throw new E2eDecryptError('e2e: could not unwrap the folder master key');
  return fmk;
}

/** A fresh password slot (salt, iterations, verify blob, `fmk_pw`) for `rawFmk`. */
async function passwordSlot(
  newPassword: string,
  rawFmk: Uint8Array,
  iterations?: number,
): Promise<Pick<E2eMarker, 'salt' | 'iter' | 'verify' | 'fmk' | 'fmk_pw'>> {
  if ((newPassword ?? '').length < E2E_MIN_PASSWORD_LEN) {
    throw new Error(`e2e: the new password must be at least ${E2E_MIN_PASSWORD_LEN} characters`);
  }
  const iter = Math.max(E2E_MIN_ITERATIONS, iterations ?? E2E_DEFAULT_ITERATIONS);
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const kek = await deriveKek(newPassword, salt, iter);
  return {
    salt: bytesToB64(salt),
    iter,
    verify: await gcmSeal(kek, new TextEncoder().encode(VERIFY_PLAINTEXT)),
    fmk: 'wrapped',
    fmk_pw: await gcmSeal(kek, rawFmk),
  };
}

/**
 * True when a new password needs a re-key: the folder key IS the old
 * password's key (v1, or v2 `fmk: 'kek'`).
 */
export function passwordChangeNeedsRekey(m: E2eMarker): boolean {
  return m.v === 1 || m.fmk === 'kek';
}

/** A re-key has started and not finished. */
export function rekeyPending(m: E2eMarker | null): boolean {
  return !!m && Array.isArray(m.req) && m.req.includes('rekey') && !!m.rekey;
}

/**
 * Change the password of a folder whose FMK is wrapped. Only the password slot
 * changes: every file, the recovery key, the escrow slot and the name key are
 * untouched and keep working. Proof is the current password OR the recovery
 * key (a reset).
 *
 * ⚠ The previous password stops opening THIS marker. It still opens any copy
 * of the old marker — a version of `.filex-e2e.json`, a backup — and through
 * it the same FMK. When that matters, re-key instead (`startRekey`).
 */
export async function changePassword(
  marker: E2eMarker,
  cred: E2eCredential,
  newPassword: string,
  opts: { iterations?: number } = {},
): Promise<E2eMarker> {
  if (passwordChangeNeedsRekey(marker)) {
    throw new Error('e2e: this folder needs its file keys re-wrapped to change its password (startRekey)');
  }
  if (rekeyPending(marker)) throw new Error('e2e: finish the re-key first');
  const raw = await fmkRawFrom(marker, cred);
  try {
    return { ...marker, ...(await passwordSlot(newPassword, raw, opts.iterations)) };
  } finally {
    raw.fill(0);
  }
}

export interface RekeyStart {
  /** Write this FIRST, before any file is re-wrapped. */
  marker: E2eMarker;
  /** The new folder master key. */
  fmk: CryptoKey;
  /** The previous one — what the files not re-wrapped yet are under. */
  previous: CryptoKey;
  /** The recovery key that opens the new FMK. Show it when `recoveryKeyIsNew`. */
  recoveryKey: string;
  recoveryKeyIsNew: boolean;
  /** The folder's name key, re-sealed under the new FMK, when it has one. */
  names?: E2eNameKey;
}

/** Thrown when a re-key would lose the folder's escrow slot. */
export class E2eRekeyEscrowError extends Error {
  constructor(msg = 'e2e: the escrow slot cannot be carried over to the new folder key') {
    super(msg);
    this.name = 'E2eRekeyEscrowError';
  }
}

/**
 * Start a re-key: a fresh random FMK, the new password, and every other slot
 * carried over to it.
 *
 *   - recovery: a reset made WITH the recovery key keeps that key (it is in
 *     hand, so it is re-sealed to the new FMK). Otherwise a new recovery key is
 *     minted — the old slot wraps the old FMK and nothing can re-seal it
 *     without the key itself — and the caller shows it once.
 *   - escrow: re-sealed to the new FMK when the installation's key is the one
 *     the slot names. Otherwise REFUSED (E2eRekeyEscrowError): a re-key never
 *     quietly drops an escrow slot. It never adds one either.
 *   - names: the same name key (no entry is renamed), re-sealed under the new
 *     FMK.
 *   - `rekey.from`: the previous FMK sealed under the new one, until
 *     `finishRekey`.
 */
export async function startRekey(
  marker: E2eMarker,
  cred: E2eCredential,
  newPassword: string,
  opts: { escrowPublicKey?: string | null; iterations?: number } = {},
): Promise<RekeyStart> {
  if (rekeyPending(marker)) throw new Error('e2e: a re-key is already in progress; resume it');
  const oldRaw = await fmkRawFrom(marker, cred);
  const newRaw = crypto.getRandomValues(new Uint8Array(FMK_LEN));
  try {
    const oldFmk = await importFmk(oldRaw);
    const newFmk = await importFmk(newRaw);

    let esc: E2eEscrowSlot | undefined;
    if (hasSlots(marker) && marker.esc) {
      const pub = opts.escrowPublicKey || null;
      if (!pub || (await escrowKeyId(pub)) !== marker.esc.kid) throw new E2eRekeyEscrowError();
      esc = await sealEscrowSlot(newRaw, pub);
    }

    const reuse = 'recoveryKey' in cred;
    const recoveryKey = reuse ? cred.recoveryKey : generateRecoveryKey();

    let names: E2eNameKey | undefined;
    let namesSlot: E2eNamesSlot | undefined;
    if (markerHasNames(marker)) {
      const nkRaw = await gcmOpen(oldFmk, marker.names!.key);
      if (!nkRaw || nkRaw.length !== 64) throw new E2eDecryptError('e2e: could not open the name key');
      namesSlot = { ...marker.names!, key: await gcmSeal(newFmk, nkRaw) };
      names = await importNameKey(nkRaw, marker.names!.long, b64urlDecode(marker.names!.root_id)!);
      nkRaw.fill(0);
    }

    const req = Array.from(new Set([...(marker.req ?? []), 'rekey']));
    const next: E2eMarker = {
      ...marker,
      v: E2E_MARKER_VERSION_FEATURES,
      req,
      ...(await passwordSlot(newPassword, newRaw, opts.iterations)),
      rk: await sealRecoverySlot(newRaw, recoveryKey),
      rekey: { from: await gcmSeal(newFmk, oldRaw), pending: true },
    };
    if (esc) next.esc = esc;
    if (namesSlot) next.names = namesSlot;
    return {
      marker: next,
      fmk: newFmk,
      previous: oldFmk,
      recoveryKey,
      recoveryKeyIsNew: !reuse,
      ...(names ? { names } : {}),
    };
  } finally {
    oldRaw.fill(0);
    newRaw.fill(0);
  }
}

/**
 * The previous FMK of a folder mid re-key, reached through the new one.
 * Null when no re-key is in progress (or the slot does not open under `fmk`).
 */
export async function unlockPrevious(marker: E2eMarker, fmk: CryptoKey): Promise<CryptoKey | null> {
  if (!rekeyPending(marker)) return null;
  const raw = await gcmOpen(fmk, marker.rekey!.from);
  if (!raw || raw.length !== FMK_LEN) return null;
  const key = await importFmk(raw);
  raw.fill(0);
  return key;
}

/**
 * Re-wrap one encrypted file's DEK from `previous` to `fmk`. Returns the new
 * bytes — the same length, the same content ciphertext, a new wrap IV and
 * wrapped DEK — or null when the file is already under `fmk` (so resuming is
 * running this again). Throws E2eDecryptError when neither key opens it, and
 * a plain Error when it is not an encrypted file.
 */
export async function rewrapFileKey(
  data: ArrayBuffer,
  previous: CryptoKey,
  fmk: CryptoKey,
): Promise<ArrayBuffer | null> {
  const b = new Uint8Array(data);
  if (!hasMagic(b) || b.length < HEADER_LEN) throw new Error('e2e: not an encrypted file');
  /* wiring:e2 stream — 0x02 wraps its DEK at the same offsets; `data` may be
   * just the 97-byte header of a large file (the body is re-sent unread). */
  if (b[8] !== E2E_VERSION && b[8] !== E2E_FILE_VERSION_STREAM) throw new Error(`e2e: unsupported version ${b[8]}`);
  const wrapIV = b.slice(WRAP_IV_OFF, WRAP_IV_OFF + IV_LEN);
  const wrapped = b.slice(WRAPPED_DEK_OFF, WRAPPED_DEK_OFF + WRAPPED_DEK_LEN);
  try {
    await crypto.subtle.decrypt({ name: 'AES-GCM', iv: buf(wrapIV) }, fmk, buf(wrapped));
    return null; // already re-wrapped
  } catch {
    /* not under the new key: try the previous one */
  }
  let rawDek: Uint8Array;
  try {
    rawDek = new Uint8Array(
      await crypto.subtle.decrypt({ name: 'AES-GCM', iv: buf(wrapIV) }, previous, buf(wrapped)),
    );
  } catch {
    throw new E2eDecryptError('e2e: this file opens under neither the new nor the previous folder key');
  }
  const iv = crypto.getRandomValues(new Uint8Array(IV_LEN));
  const rewrapped = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: buf(iv) }, fmk, buf(rawDek)),
  );
  rawDek.fill(0);
  const out = b.slice();
  out.set(iv, WRAP_IV_OFF);
  out.set(rewrapped, WRAPPED_DEK_OFF);
  return out.buffer;
}

/**
 * The re-key is done: every file is under the new FMK. Drop `rekey` (and the
 * previous FMK with it); a marker left with no required feature goes back to
 * v2, which every filex since 0.31 opens.
 */
/** Is an in-place conversion of this folder under way? */
export function conversionPending(m: E2eMarker | null): boolean {
  return !!m && Array.isArray(m.req) && m.req.includes('conv') && m.conv?.pending === true;
}

/**
 * The key file of a folder about to be encrypted in place: v3, `conv`
 * required (an older filex refuses a folder whose files are half plaintext),
 * the conversion marked pending.
 */
export function startConversion(
  marker: E2eMarker,
  when: string = new Date().toISOString(),
  cleanup?: { versions: boolean; trash: boolean },
): E2eMarker {
  const req = Array.from(new Set([...(marker.req ?? []), 'conv']));
  const conv: E2eConvSlot = { pending: true, started: when, ...(cleanup ? { cleanup } : {}) };
  return { ...marker, v: E2E_MARKER_VERSION_FEATURES, req, conv };
}

/** Every file carries the magic: the conversion is over. Back to v2 when
 *  nothing else is required. */
export function finishConversion(marker: E2eMarker): E2eMarker {
  const next: E2eMarker = { ...marker };
  delete next.conv;
  const req = (marker.req ?? []).filter((f) => f !== 'conv');
  if (req.length > 0) {
    next.req = req;
  } else {
    delete next.req;
    next.v = E2E_MARKER_VERSION;
  }
  return next;
}

export function finishRekey(marker: E2eMarker): E2eMarker {
  const next: E2eMarker = { ...marker };
  delete next.rekey;
  const req = (marker.req ?? []).filter((f) => f !== 'rekey');
  if (req.length > 0) {
    next.req = req;
  } else {
    delete next.req;
    next.v = E2E_MARKER_VERSION;
  }
  return next;
}

/**
 * Decrypt with the FMK, falling back to the previous FMK of a folder mid
 * re-key. Throws exactly what decryptFile throws when neither opens it.
 */
export async function decryptFileAny(
  fmk: CryptoKey,
  previous: CryptoKey | null | undefined,
  data: ArrayBuffer,
): Promise<ArrayBuffer> {
  try {
    return await decryptFile(fmk, data);
  } catch (err) {
    if (!previous || !(err instanceof E2eDecryptError)) throw err;
    return decryptFile(previous, data);
  }
}

// ---------------------------------------------------------------------
// In-memory session key ring
// ---------------------------------------------------------------------

/**
 * Tiny per-explorer key ring: encrypted-folder root (wire path) → FMK, and
 * the folder's name key when its names are encrypted.
 * Lives ONLY in memory — "Lock" drops the entry, a reload drops all.
 */
export function createKeyRing() {
  const keys = new Map<string, CryptoKey>();
  const nameKeys = new Map<string, E2eNameKey>();
  const previousKeys = new Map<string, CryptoKey>();
  return {
    get(root: string): CryptoKey | undefined {
      return keys.get(root);
    },
    set(root: string, fmk: CryptoKey, names?: E2eNameKey | null, previous?: CryptoKey | null): void {
      keys.set(root, fmk);
      if (names) nameKeys.set(root, names);
      else nameKeys.delete(root);
      if (previous) previousKeys.set(root, previous);
      else previousKeys.delete(root);
    },
    /** The previous FMK of a folder mid re-key (files not re-wrapped yet). */
    previous(root: string): CryptoKey | undefined {
      return previousKeys.get(root);
    },
    /** A re-key finished: the previous FMK is no longer needed. */
    dropPrevious(root: string): void {
      previousKeys.delete(root);
    },
    /** The folder's name key; undefined for a content-only folder or a locked one. */
    names(root: string): E2eNameKey | undefined {
      return nameKeys.get(root);
    },
    setNames(root: string, names: E2eNameKey): void {
      if (keys.has(root)) nameKeys.set(root, names);
    },
    /** Every unlocked root, for resolving a path from any view. */
    roots(): string[] {
      return [...keys.keys()];
    },
    /** Drop one folder's keys ("Lock"). */
    lock(root: string): void {
      keys.delete(root);
      nameKeys.delete(root);
      previousKeys.delete(root);
    },
    has(root: string): boolean {
      return keys.has(root);
    },
    clear(): void {
      keys.clear();
      nameKeys.clear();
      previousKeys.clear();
    },
  };
}

export type E2eKeyRing = ReturnType<typeof createKeyRing>;
