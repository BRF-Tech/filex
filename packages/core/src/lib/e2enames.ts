/**
 * e2enames — encrypted file and folder names inside an E2E folder.
 *
 * Design and rationale: docs/E2E-ENCRYPTION.md → "Encrypted names".
 *
 * ── The scheme in one screen ──────────────────────────────────────────
 *
 *   name key   64 random bytes, minted when names are turned on, stored in
 *              the marker wrapped by the folder master key (FMK). Reaching
 *              the FMK — by password, recovery key or escrow — reaches it.
 *   folder id  16 bytes per folder. The encrypted root's is random and sits
 *              in the marker (`names.root_id`); every other folder's is fixed
 *              when it first gets an encrypted name and then travels IN ITS
 *              STORED NAME (`S.D`), so it never changes: a rename or a move
 *              re-encrypts the folder's own name and keeps `D`.
 *   cipher     AES-SIV (RFC 5297) with AES-256; associated data = the id of
 *              the folder the name is in. The same name in two folders gives
 *              two different ciphertexts.
 *   input      the name normalised to Unicode NFC, UTF-8, 1..255 bytes.
 *   encoding   base64url without padding — [A-Za-z0-9_-]. A file is `S`, a
 *              folder `S.D` (D = base64url of its 16-byte id, 22 chars).
 *   long names S longer than 220 characters (a folder counts its `.D`) is
 *              stored as `H.fxl` (file) or `H.fxl.D` (folder), H =
 *              base64url(SHA-256(S)); S goes in a sibling sidecar `H.fxl.name`.
 *
 * ── Why a path still decrypts one segment at a time ───────────────────
 *
 * Each segment's associated data is the id of the folder above it, and that
 * id is in the folder's own stored name — the segment just before it in the
 * same path (the root's comes from the marker). So a breadcrumb, a Recent or
 * Starred row, a search hit or a trash entry is named from its path alone,
 * with no server state and no walk of the tree. Cryptomator keeps the id in
 * a `dir.c9r` file and gocryptfs in `gocryptfs.diriv`; putting it in the name
 * means a move carries it atomically and nothing extra is stored or fetched.
 *
 * ── How a folder id is made ───────────────────────────────────────────
 *
 * A folder made in the browser gets 16 random bytes. A folder whose name was
 * never encrypted (made over WebDAV, or not yet reached by a level change)
 * has id = SIV-V(name, AD = [parent id, "filex-e2e-dir-id"]) — the 16-byte
 * synthetic IV, a keyed PRF of the parent id and the name — so it has an id
 * BEFORE it is renamed: its children are encrypted under it first, and an
 * interrupted level change computes the same id again. Either way, from then
 * on the id is whatever the stored name says; renaming or moving the folder
 * never recomputes it. (Random for new folders so that a folder renamed away
 * and a new one made under the old name do not share an id.)
 *
 * ── How a stored name is read ─────────────────────────────────────────
 *
 *   'file' / 'dir'   base64url (`S` / `S.D`) that passes the SIV check
 *   'long' / 'longdir' `H.fxl` / `H.fxl.D`, resolved through the sidecar
 *   'sidecar'        `H.fxl.name` — bookkeeping, hidden from every view
 *   'plain'          anything else: a name that was never encrypted.
 * SIV authenticates, so a plaintext name passes with probability 2^-128 —
 * the classification is exact, which is what makes a level change resumable.
 */

import { importSivKey, sivDecrypt, sivEncrypt, type SivKey } from './aessiv';

/** Algorithm id written into the marker's `names.alg`. */
export const E2E_NAMES_ALG = 'AES-SIV-512';
/** Encoding id written into the marker's `names.enc`. */
export const E2E_NAMES_ENC = 'b64url';
/** Encoded names longer than this are shortened (Cryptomator uses 220 too). */
export const E2E_NAMES_LONG_DEFAULT = 220;
/** Plaintext name ceiling in UTF-8 bytes — what a local file system allows. */
export const E2E_NAME_MAX_BYTES = 255;
/** Raw name-key length: AES-256-SIV takes two 256-bit keys. */
export const E2E_NAME_KEY_BYTES = 64;
/** A folder id: 16 bytes, 22 base64url characters. */
export const E2E_DIR_ID_BYTES = 16;
export const E2E_LONG_SUFFIX = '.fxl';
export const E2E_SIDECAR_SUFFIX = '.fxl.name';
/** Associated-data label that turns SIV into the folder-id PRF. */
const DIR_ID_LABEL = new TextEncoder().encode('filex-e2e-dir-id');

const SIV_TAG = 16;
const B64URL = /^[A-Za-z0-9_-]+$/;
const H = '[A-Za-z0-9_-]{43}';
const D = '[A-Za-z0-9_-]{22}';
const SIDECAR_RE = new RegExp(`^(${H})\\.fxl\\.name$`);
const LONG_RE = new RegExp(`^(${H})\\.fxl$`);
const LONGDIR_RE = new RegExp(`^(${H})\\.fxl\\.(${D})$`);
const DIR_RE = new RegExp(`^([A-Za-z0-9_-]{23,})\\.(${D})$`);
/** Shortest possible encoded name: tag + one byte = 17 bytes = 23 chars. */
const MIN_ENC_LEN = 23;

/** A folder's name key, ready to use. */
export interface E2eNameKey {
  siv: SivKey;
  /** Shortening threshold this folder was created with. */
  long: number;
  /** The encrypted root's own folder id (from the marker). */
  rootId: Uint8Array;
}

export type StoredNameKind = 'file' | 'dir' | 'long' | 'longdir' | 'sidecar' | 'plain';

export interface StoredName {
  kind: StoredNameKind;
  /** For 'long', 'longdir' and 'sidecar': the 43-char hash they share. */
  hash?: string;
  /** For 'file'/'dir': the encoded ciphertext. */
  encoded?: string;
  /** For 'dir'/'longdir': the folder's id. */
  dirId?: Uint8Array;
}

/** Why a plaintext name cannot be encrypted, or null when it can. */
export type NameProblem = 'empty' | 'dot' | 'slash' | 'control' | 'too_long';

/** Result of reading one stored name. */
export interface DecodedName {
  /** The plaintext, when there is one. */
  name: string | null;
  /**
   * 'enc'        decrypted.
   * 'plain'      never encrypted — `name` is the stored name itself.
   * 'unreadable' looked like ours and did not decrypt: a long name whose
   *              sidecar is missing or does not match, a name moved into this
   *              folder without being re-encrypted for it, or damage.
   * 'sidecar'    bookkeeping; hide it.
   */
  state: 'enc' | 'plain' | 'unreadable' | 'sidecar';
}

// ---------------------------------------------------------------------
// base64url (no padding)
// ---------------------------------------------------------------------

export function b64urlEncode(b: Uint8Array): string {
  let s = '';
  for (let i = 0; i < b.length; i++) s += String.fromCharCode(b[i]);
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** Strict decode: null for anything outside the alphabet or a bad length. */
export function b64urlDecode(s: string): Uint8Array | null {
  if (!s || !B64URL.test(s) || s.length % 4 === 1) return null;
  const std = s.replace(/-/g, '+').replace(/_/g, '/');
  const padded = std + '==='.slice((std.length + 3) % 4);
  let raw: string;
  try {
    raw = atob(padded);
  } catch {
    return null;
  }
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  // Reject a non-canonical tail (unused bits set): two spellings of the same
  // bytes would be two stored names for one plaintext.
  if (b64urlEncode(out) !== s) return null;
  return out;
}

// ---------------------------------------------------------------------
// Plaintext names
// ---------------------------------------------------------------------

/** NFC — so a name typed on macOS (NFD) and on Windows (NFC) is one name. */
export function normalizeName(name: string): string {
  return (name ?? '').normalize('NFC');
}

/**
 * The rules a plaintext name must meet before it is encrypted. They are the
 * rules a name must meet on a local disk, because `filex decrypt` writes
 * these names to one: nothing that is a path separator, a control
 * character, `.`/`..`, or longer than 255 UTF-8 bytes.
 */
export function namePlainProblem(name: string): NameProblem | null {
  const n = normalizeName(name);
  if (!n) return 'empty';
  if (n === '.' || n === '..') return 'dot';
  if (n.includes('/') || n.includes('\\')) return 'slash';
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(n)) return 'control';
  if (new TextEncoder().encode(n).length > E2E_NAME_MAX_BYTES) return 'too_long';
  return null;
}

// ---------------------------------------------------------------------
// Keys and folder ids
// ---------------------------------------------------------------------

/** A fresh raw name key. The caller wraps it and zeroes it. */
export function generateNameKeyBytes(): Uint8Array {
  return crypto.getRandomValues(new Uint8Array(E2E_NAME_KEY_BYTES));
}

/** A fresh random folder id: the encrypted root's, and a new folder's. */
export function generateDirId(): Uint8Array {
  return crypto.getRandomValues(new Uint8Array(E2E_DIR_ID_BYTES));
}
/** The encrypted root's id (random, kept in the marker). */
export const generateRootId = generateDirId;

export async function importNameKey(
  raw: Uint8Array,
  long: number,
  rootId: Uint8Array,
): Promise<E2eNameKey> {
  if (raw.length !== E2E_NAME_KEY_BYTES) throw new Error('e2e: name key must be 64 bytes');
  if (rootId.length !== E2E_DIR_ID_BYTES) throw new Error('e2e: a folder id is 16 bytes');
  return { siv: await importSivKey(raw), long, rootId: rootId.slice() };
}

async function sha256b64url(s: string): Promise<string> {
  const d = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(s));
  return b64urlEncode(new Uint8Array(d));
}

/**
 * The id of a folder whose name was never encrypted: a keyed PRF of its
 * parent's id and its name (the SIV synthetic IV under a label). Only ever
 * computed for a folder whose stored name does not carry an id yet.
 */
export async function deriveDirId(key: E2eNameKey, parentId: Uint8Array, plain: string): Promise<Uint8Array> {
  const sealed = await sivEncrypt(key.siv, new TextEncoder().encode(normalizeName(plain)), [
    parentId,
    DIR_ID_LABEL,
  ]);
  return sealed.slice(0, E2E_DIR_ID_BYTES);
}

// ---------------------------------------------------------------------
// Classify / encrypt / decrypt
// ---------------------------------------------------------------------

/** What a stored name is, from its spelling alone (no key needed). */
export function classifyStoredName(stored: string): StoredName {
  let m = SIDECAR_RE.exec(stored);
  if (m) return { kind: 'sidecar', hash: m[1] };
  m = LONGDIR_RE.exec(stored);
  if (m) {
    const id = b64urlDecode(m[2]);
    if (id && id.length === E2E_DIR_ID_BYTES) return { kind: 'longdir', hash: m[1], dirId: id };
  }
  m = LONG_RE.exec(stored);
  if (m) return { kind: 'long', hash: m[1] };
  m = DIR_RE.exec(stored);
  if (m) {
    const id = b64urlDecode(m[2]);
    if (id && id.length === E2E_DIR_ID_BYTES) return { kind: 'dir', encoded: m[1], dirId: id };
  }
  if (stored.length >= MIN_ENC_LEN && B64URL.test(stored)) return { kind: 'file', encoded: stored };
  return { kind: 'plain' };
}

/** The folder id a folder's stored name carries, or null (a plaintext name). */
export function dirIdOf(stored: string): Uint8Array | null {
  return classifyStoredName(stored).dirId ?? null;
}

/**
 * The id the CHILDREN of a folder are encrypted under: the one its stored
 * name carries, or — for a folder whose name was never encrypted — the id it
 * WILL carry (deriveDirId over its plaintext name), so its contents can be
 * encrypted before it is renamed and read in the meantime.
 */
export async function effectiveDirId(
  key: E2eNameKey,
  parentId: Uint8Array,
  stored: string,
): Promise<Uint8Array> {
  return dirIdOf(stored) ?? deriveDirId(key, parentId, stored);
}

/** The sidecar that belongs to a long stored name (file or folder). */
export function sidecarNameFor(stored: string): string | null {
  const c = classifyStoredName(stored);
  return c.kind === 'long' || c.kind === 'longdir' ? `${c.hash}${E2E_SIDECAR_SUFFIX}` : null;
}

export interface EncryptedName {
  /** What the server stores as the entry's name. */
  stored: string;
  /** The encoded ciphertext S (without a folder's `.D`). */
  encoded: string;
  /** For a folder: its id (kept, or derived). */
  dirId?: Uint8Array;
  /** Present only for a long name: the sidecar to write next to it. */
  sidecar?: { name: string; content: string };
}

/**
 * Encrypt a plaintext name for the folder with id `parentId`. For a folder
 * pass `dirId` — its existing id when it is renamed or moved, or the derived
 * one when a plaintext-named folder gets its first encrypted name (never a
 * new one: its children are sealed under it) — or `isDir` alone for a NEW
 * folder, which gets a random id. Throws on a name `namePlainProblem` rejects.
 */
export async function encryptName(
  key: E2eNameKey,
  plain: string,
  parentId: Uint8Array,
  opts: { isDir?: boolean; dirId?: Uint8Array } = {},
): Promise<EncryptedName> {
  const problem = namePlainProblem(plain);
  if (problem) throw new Error(`e2e: invalid name (${problem})`);
  const bytes = new TextEncoder().encode(normalizeName(plain));
  const encoded = b64urlEncode(await sivEncrypt(key.siv, bytes, [parentId]));
  const isDir = opts.isDir || !!opts.dirId;
  const dirId = isDir ? (opts.dirId ?? generateDirId()) : undefined;
  const suffix = dirId ? `.${b64urlEncode(dirId)}` : '';
  if (encoded.length + suffix.length <= key.long) {
    return { stored: encoded + suffix, encoded, ...(dirId ? { dirId } : {}) };
  }
  const hash = await sha256b64url(encoded);
  return {
    stored: `${hash}${E2E_LONG_SUFFIX}${suffix}`,
    encoded,
    ...(dirId ? { dirId } : {}),
    sidecar: { name: `${hash}${E2E_SIDECAR_SUFFIX}`, content: encoded },
  };
}

/** Decrypt an encoded ciphertext name sealed for `parentId`. Null if not ours. */
export async function decryptEncoded(
  key: E2eNameKey,
  encoded: string,
  parentId: Uint8Array,
): Promise<string | null> {
  const raw = b64urlDecode(encoded);
  if (!raw || raw.length <= SIV_TAG) return null;
  try {
    const p = await sivDecrypt(key.siv, raw, [parentId]);
    return new TextDecoder('utf-8', { fatal: true }).decode(p);
  } catch {
    return null;
  }
}

/**
 * Decrypt, and hold the plaintext to the same rules a name had to meet to be
 * encrypted. The browser never seals `../x` or `a/b`, but whoever holds the
 * folder key can: such a "name" would become a download filename or a path
 * segment, so it is reported as unreadable instead (`filex decrypt` refuses it
 * the same way).
 */
async function decryptName(
  key: E2eNameKey,
  encoded: string,
  parentId: Uint8Array,
): Promise<'unsafe' | string | null> {
  const plain = await decryptEncoded(key, encoded, parentId);
  if (plain === null) return null;
  return namePlainProblem(plain) ? 'unsafe' : plain;
}

/**
 * Read one stored name found in the folder with id `parentId`.
 *
 * `readSidecar` fetches a sibling sidecar's content by its stored name; it is
 * only called for a long name. Returning null means "no such file".
 */
export async function decryptStoredName(
  key: E2eNameKey,
  stored: string,
  parentId: Uint8Array,
  readSidecar?: (sidecarName: string) => Promise<string | null>,
): Promise<DecodedName> {
  const c = classifyStoredName(stored);
  if (c.kind === 'sidecar') return { name: null, state: 'sidecar' };
  if (c.kind === 'plain') return { name: stored, state: 'plain' };
  if (c.kind === 'long' || c.kind === 'longdir') {
    const content = readSidecar ? await readSidecar(`${c.hash}${E2E_SIDECAR_SUFFIX}`) : null;
    const encoded = (content ?? '').trim();
    // The sidecar has to be the one this name was made from. A sidecar that
    // was swapped, truncated or belongs to another item fails here rather
    // than naming the file after something else.
    if (!encoded || (await sha256b64url(encoded)) !== c.hash) {
      return { name: null, state: 'unreadable' };
    }
    const plain = await decryptName(key, encoded, parentId);
    return plain === null || plain === 'unsafe'
      ? { name: null, state: 'unreadable' }
      : { name: plain, state: 'enc' };
  }
  const plain = await decryptName(key, c.encoded!, parentId);
  if (plain === 'unsafe') return { name: null, state: 'unreadable' };
  if (plain !== null) return { name: plain, state: 'enc' };
  // A folder-shaped name (`S.D`) is ours by construction; not opening under
  // this folder's id means it was moved here without being re-encrypted (or
  // is damaged). A file-shaped base64url name that does not open is simply a
  // plaintext name that happens to look like one (`ReadMe_2024-final`).
  return c.kind === 'dir' ? { name: null, state: 'unreadable' } : { name: stored, state: 'plain' };
}

/** Lower-cased extension of a plaintext name, without the dot ('' if none). */
export function extensionOf(name: string): string {
  const i = name.lastIndexOf('.');
  if (i <= 0 || i === name.length - 1) return '';
  return name.slice(i + 1).toLowerCase();
}
