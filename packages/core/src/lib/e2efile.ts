/**
 * e2efile — a single end-to-end encrypted file, `.fxe`
 * (docs/E2E-ENCRYPTION.md → "Single encrypted files").
 *
 * Any file can be encrypted on its own, without an encrypted folder around
 * it. The result is ONE self-contained file: the key slots a folder keeps in
 * its marker travel in the file's own header, so it can be moved, shared,
 * backed up or taken off the server and still opened with its password or
 * its recovery key — by the browser or by `filex decrypt`.
 *
 * ── Layout ────────────────────────────────────────────────────────────
 *
 *   [0..8)    magic "filexfxe"
 *   [8]       version 0x01
 *   [9..13)   header length H, uint32 big-endian (1 … 65 536)
 *   [13..13+H) header, UTF-8 JSON (below)
 *   [13+H..)  STREAM body (lib/e2estream.ts) under the file's DEK
 *
 * ── Header ────────────────────────────────────────────────────────────
 *
 * The SAME slot shapes as a folder marker (lib/e2ecrypto.ts), so the same
 * code unlocks, changes the password of and reads the recovery key of both:
 *
 *   salt, iter, verify        the password slot (PBKDF2-SHA256, 600 000)
 *   fmk: "wrapped", fmk_pw    a random 32-byte key, sealed under the password
 *   rk: {salt, blob}          the recovery key slot (HKDF), shown once
 *   esc?: {kid, alg, blob}    the escrow slot, when the installation has one
 *   dek                       the file's DEK, sealed under that key
 *   name                      the original file name (UTF-8, NFC), sealed
 *   chunk                     STREAM chunk size, log2 (writers: 20)
 *   nonce                     STREAM nonce prefix, 7 bytes, base64
 *   size                      plaintext bytes
 *   req?                      required features; an unknown one refuses
 *
 * A program that rewrites the header (a password change) keeps every field it
 * does not understand. Changing the password rewrites the header only: the
 * body — every byte after the header — is carried over unread.
 */

import {
  E2E_MAX_ITERATIONS,
  E2E_MIN_PASSWORD_LEN,
  E2eDecryptError,
  b64ToBytes,
  bytesToB64,
  changePassword,
  createEncryptedFolder,
  openBlob,
  sealBlob,
  unlockWithEscrowKey,
  unlockWithPasswordDetailed,
  unlockWithRecoveryKey,
  type E2eCredential,
  type E2eEscrowSlot,
  type E2eMarker,
  type E2eRecoverySlot,
} from './e2ecrypto';
import { namePlainProblem } from './e2enames';
import {
  ByteStreamReader,
  E2E_STREAM_CHUNK_LOG2,
  E2E_STREAM_PREFIX_LEN,
  createStreamDecryptor,
  createStreamEncryptor,
  importDek,
  prependBytes,
  randomDek,
  randomStreamPrefix,
  streamCiphertextSize,
  validChunkLog2,
} from './e2estream';

export const FXE_MAGIC = 'filexfxe';
export const FXE_VERSION = 1;
/** What every single encrypted file's stored name ends with. */
export const FXE_EXTENSION = '.fxe';
/** The largest header a reader accepts. */
export const FXE_MAX_HEADER_BYTES = 64 * 1024;
/** Required features this build understands (none yet). */
export const FXE_KNOWN_FEATURES: readonly string[] = [];

const MAGIC_BYTES = new TextEncoder().encode(FXE_MAGIC);
/** magic (8) + version (1) + header length (4). */
export const FXE_FIXED_LEN = 13;
const DEK_LEN = 32;

export interface FxeHeader {
  salt: string;
  iter: number;
  verify: string;
  fmk: 'wrapped';
  fmk_pw: string;
  rk?: E2eRecoverySlot;
  esc?: E2eEscrowSlot;
  dek: string;
  name: string;
  chunk: number;
  nonce: string;
  size: number;
  req?: string[];
  /** Anything a newer filex wrote; kept on rewrite. */
  [k: string]: unknown;
}

/** True when a stored name is a single encrypted file's. */
export function isFxeName(name: string | null | undefined): boolean {
  return typeof name === 'string' && name.length > FXE_EXTENSION.length && name.toLowerCase().endsWith(FXE_EXTENSION);
}

/** `encrypted-<8 hex>.fxe` — a stored name that says nothing about the file. */
export function hiddenFxeName(): string {
  const b = crypto.getRandomValues(new Uint8Array(4));
  return `encrypted-${Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')}${FXE_EXTENSION}`;
}

/** True for a name `hiddenFxeName` makes. */
export function isHiddenFxeName(name: string): boolean {
  return /^encrypted-[0-9a-f]{8}\.fxe$/i.test(name);
}

/** The stored name for an encrypted copy of `original`. */
export function fxeStoredName(original: string, hide: boolean): string {
  return hide ? hiddenFxeName() : `${original}${FXE_EXTENSION}`;
}

/**
 * A marker-shaped view of a header's key slots, so the folder code
 * (`unlockWithPasswordDetailed`, `unlockWithRecoveryKey`,
 * `unlockWithEscrowKey`, `changePassword`) works on it unchanged.
 */
export function fxeSlots(h: FxeHeader): E2eMarker {
  const m: E2eMarker = { v: 2, salt: h.salt, iter: h.iter, verify: h.verify, fmk: 'wrapped', fmk_pw: h.fmk_pw };
  if (h.rk) m.rk = h.rk;
  if (h.esc) m.esc = h.esc;
  return m;
}

export function fxeHasRecovery(h: FxeHeader): boolean {
  return !!h.rk && typeof h.rk.salt === 'string' && typeof h.rk.blob === 'string';
}

// ---------------------------------------------------------------------
// Header encode / parse
// ---------------------------------------------------------------------

/** The bytes a `.fxe` starts with: magic, version, length, header. */
export function encodeFxePrefix(h: FxeHeader): Uint8Array<ArrayBuffer> {
  const json = new TextEncoder().encode(JSON.stringify(h));
  if (json.length === 0 || json.length > FXE_MAX_HEADER_BYTES) throw new Error('e2e: the .fxe header is too large');
  const out = new Uint8Array(FXE_FIXED_LEN + json.length);
  out.set(MAGIC_BYTES, 0);
  out[8] = FXE_VERSION;
  new DataView(out.buffer).setUint32(9, json.length, false);
  out.set(json, FXE_FIXED_LEN);
  return out;
}

/** True when `b` starts with the `.fxe` magic. */
export function hasFxeMagic(b: Uint8Array): boolean {
  if (b.length < MAGIC_BYTES.length) return false;
  for (let i = 0; i < MAGIC_BYTES.length; i++) if (b[i] !== MAGIC_BYTES[i]) return false;
  return true;
}

/** Why a `.fxe` could not be read, before any key is involved. */
export class FxeFormatError extends Error {
  constructor(msg: string) {
    super(msg);
    this.name = 'FxeFormatError';
  }
}

/** Header length from the first 13 bytes. Throws FxeFormatError. */
export function fxeHeaderLength(first: Uint8Array): number {
  if (first.length < FXE_FIXED_LEN || !hasFxeMagic(first)) throw new FxeFormatError('e2e: not a filex encrypted file (.fxe)');
  if (first[8] !== FXE_VERSION) throw new FxeFormatError(`e2e: unsupported .fxe version ${first[8]} — a newer filex wrote it`);
  const n = new DataView(first.buffer, first.byteOffset, first.byteLength).getUint32(9, false);
  if (n === 0 || n > FXE_MAX_HEADER_BYTES) throw new FxeFormatError('e2e: the .fxe header length is out of range');
  return n;
}

export interface ParsedFxe {
  header: FxeHeader;
  /** Required features this build does not know. Non-empty: do not open it. */
  unsupported: string[];
  /** Bytes before the body (13 + header length). */
  prefixLength: number;
}

function isB64Of(s: unknown, len?: number): boolean {
  if (typeof s !== 'string' || s.length === 0) return false;
  try {
    const b = b64ToBytes(s);
    return len === undefined || b.length === len;
  } catch {
    return false;
  }
}

/** Parse the JSON header. Throws FxeFormatError for anything malformed. */
export function parseFxeHeader(json: Uint8Array, prefixLength: number): ParsedFxe {
  let h: FxeHeader;
  try {
    h = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(json)) as FxeHeader;
  } catch {
    throw new FxeFormatError('e2e: the .fxe header is not readable');
  }
  const bad = (what: string) => new FxeFormatError(`e2e: the .fxe header is damaged (${what})`);
  if (!h || typeof h !== 'object' || Array.isArray(h)) throw bad('not an object');
  if (!isB64Of(h.salt)) throw bad('salt');
  if (typeof h.iter !== 'number' || !Number.isInteger(h.iter) || h.iter < 1 || h.iter > E2E_MAX_ITERATIONS) throw bad('iter');
  if (typeof h.verify !== 'string') throw bad('verify');
  if (h.fmk !== 'wrapped' || typeof h.fmk_pw !== 'string') throw bad('fmk');
  if (h.rk !== undefined && (typeof h.rk !== 'object' || typeof h.rk?.salt !== 'string' || typeof h.rk?.blob !== 'string')) {
    throw bad('rk');
  }
  if (
    h.esc !== undefined &&
    (typeof h.esc !== 'object' || typeof h.esc?.kid !== 'string' || typeof h.esc?.alg !== 'string' || typeof h.esc?.blob !== 'string')
  ) {
    throw bad('esc');
  }
  if (typeof h.dek !== 'string' || typeof h.name !== 'string') throw bad('dek/name');
  if (!validChunkLog2(h.chunk)) throw bad('chunk');
  if (!isB64Of(h.nonce, E2E_STREAM_PREFIX_LEN)) throw bad('nonce');
  if (typeof h.size !== 'number' || !Number.isSafeInteger(h.size) || h.size < 0) throw bad('size');
  const unsupported: string[] = [];
  if (h.req !== undefined) {
    if (!Array.isArray(h.req) || h.req.some((f) => typeof f !== 'string')) throw bad('req');
    for (const f of h.req) if (!FXE_KNOWN_FEATURES.includes(f)) unsupported.push(f);
  }
  return { header: h, unsupported, prefixLength };
}

/** Parse a whole prefix held in memory (13 + H bytes, or more). */
export function parseFxePrefix(b: Uint8Array): ParsedFxe {
  const n = fxeHeaderLength(b);
  if (b.length < FXE_FIXED_LEN + n) throw new FxeFormatError('e2e: the .fxe file is truncated');
  return parseFxeHeader(b.subarray(FXE_FIXED_LEN, FXE_FIXED_LEN + n), FXE_FIXED_LEN + n);
}

/** Total bytes of a `.fxe` with this header. */
export function fxeFileSize(h: FxeHeader, prefixLength: number): number {
  return prefixLength + streamCiphertextSize(h.size, h.chunk);
}

/**
 * Read the prefix off the front of a `.fxe` stream. The rest of the stream —
 * the body — comes back unread.
 */
export async function readFxe(stream: ReadableStream<Uint8Array>): Promise<{ parsed: ParsedFxe; body: ReadableStream<Uint8Array> }> {
  const r = new ByteStreamReader(stream);
  try {
    const first = await r.read(FXE_FIXED_LEN);
    const n = fxeHeaderLength(first);
    const json = await r.read(n);
    if (json.length < n) throw new FxeFormatError('e2e: the .fxe file is truncated');
    return { parsed: parseFxeHeader(json, FXE_FIXED_LEN + n), body: r.rest() };
  } catch (err) {
    await r.cancel().catch(() => undefined);
    throw err;
  }
}

// ---------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------

/** What an unlock hands back: everything reading and naming the file needs. */
export interface FxeKey {
  header: FxeHeader;
  /** The file's own master key (what the slots wrap). */
  fmk: CryptoKey;
  /** The STREAM key. */
  dek: CryptoKey;
  /** The original name, NFC. */
  name: string;
}

/** How the person opens a `.fxe`. */
export type FxeCredential = { password: string } | { recoveryKey: string } | { escrowKey: CryptoKey };

export type FxeUnlock = { key: FxeKey } | { error: 'wrong' | 'damaged' };

/** The DEK and the name, from the file's master key. Null when damaged. */
async function keysFromFmk(h: FxeHeader, fmk: CryptoKey): Promise<FxeKey | null> {
  const raw = await openBlob(fmk, h.dek);
  if (!raw || raw.length !== DEK_LEN) return null;
  let dek: CryptoKey;
  try {
    dek = await importDek(new Uint8Array(raw));
  } finally {
    raw.fill(0);
  }
  const nameBytes = await openBlob(fmk, h.name);
  if (!nameBytes) return null;
  let name: string;
  try {
    name = new TextDecoder('utf-8', { fatal: true }).decode(nameBytes);
  } catch {
    return null;
  }
  if (namePlainProblem(name)) return null;
  return { header: h, fmk, dek, name: name.normalize('NFC') };
}

/**
 * Open a `.fxe` header with a password, its recovery key or the escrow key.
 * 'wrong': the credential does not open it. 'damaged': it did (or the header
 * gives no way to tell), and the file key or the name is unreadable.
 */
export async function unlockFxe(h: FxeHeader, cred: FxeCredential): Promise<FxeUnlock> {
  const slots = fxeSlots(h);
  let fmk: CryptoKey | null;
  if ('password' in cred) {
    const opened = await unlockWithPasswordDetailed(slots, cred.password);
    if ('error' in opened) return { error: opened.error };
    fmk = opened.fmk;
  } else if ('recoveryKey' in cred) {
    fmk = await unlockWithRecoveryKey(slots, cred.recoveryKey);
    if (!fmk) return { error: 'wrong' };
  } else {
    fmk = await unlockWithEscrowKey(slots, cred.escrowKey);
    if (!fmk) return { error: 'wrong' };
  }
  const key = await keysFromFmk(h, fmk);
  return key ? { key } : { error: 'damaged' };
}

/** The same file with an updated header (a password change) keeps its keys. */
export function rekeyedFxeKey(key: FxeKey, header: FxeHeader): FxeKey {
  return { ...key, header };
}

// ---------------------------------------------------------------------
// Encrypt / decrypt
// ---------------------------------------------------------------------

export interface CreateFxeOptions {
  /** Base64 SPKI of the installation's escrow key, when it has one. */
  escrowPublicKey?: string | null;
  iterations?: number;
  /** Chunk size, log2. Tests only: writers use E2E_STREAM_CHUNK_LOG2. */
  chunkLog2?: number;
  onProgress?: (plainBytes: number) => void;
}

export interface CreatedFxe {
  header: FxeHeader;
  /** The whole `.fxe`: prefix, then the body as the source is read. */
  stream: ReadableStream<Uint8Array>;
  /** Bytes the stream will carry. */
  size: number;
  /** Show this ONCE. filex never stores it. */
  recoveryKey: string;
  /** Keys for this tab's ring, so the new file opens without a prompt. */
  key: FxeKey;
}

/**
 * Encrypt `src` (exactly `plainSize` bytes, named `name`) into a `.fxe`.
 * The password slot, recovery key and escrow slot are made exactly as for a
 * new encrypted folder (`createEncryptedFolder`); the stream errors if the
 * source is not `plainSize` bytes long.
 */
export async function createFxe(
  name: string,
  plainSize: number,
  src: ReadableStream<Uint8Array>,
  password: string,
  opts: CreateFxeOptions = {},
): Promise<CreatedFxe> {
  const nfc = (name ?? '').normalize('NFC');
  const problem = namePlainProblem(nfc);
  if (problem) throw new Error(`e2e: invalid name (${problem})`);
  if ((password ?? '').length < E2E_MIN_PASSWORD_LEN) {
    throw new Error(`e2e: the password must be at least ${E2E_MIN_PASSWORD_LEN} characters`);
  }
  if (!Number.isSafeInteger(plainSize) || plainSize < 0) throw new Error('e2e: bad file size');
  const log2 = opts.chunkLog2 ?? E2E_STREAM_CHUNK_LOG2;
  if (!validChunkLog2(log2)) throw new Error('e2e: bad chunk size');

  const made = await createEncryptedFolder(password, {
    escrowPublicKey: opts.escrowPublicKey ?? null,
    iterations: opts.iterations,
  });
  const rawDek = randomDek();
  let dekSealed: string;
  let dek: CryptoKey;
  try {
    dekSealed = await sealBlob(made.fmk, rawDek);
    dek = await importDek(rawDek);
  } finally {
    rawDek.fill(0);
  }
  const prefix = randomStreamPrefix();
  const m = made.marker;
  const header: FxeHeader = {
    salt: m.salt,
    iter: m.iter,
    verify: m.verify,
    fmk: 'wrapped',
    fmk_pw: m.fmk_pw!,
    ...(m.rk ? { rk: m.rk } : {}),
    ...(m.esc ? { esc: m.esc } : {}),
    dek: dekSealed,
    name: await sealBlob(made.fmk, new TextEncoder().encode(nfc)),
    chunk: log2,
    nonce: bytesToB64(prefix),
    size: plainSize,
  };
  const head = encodeFxePrefix(header);
  const body = src.pipeThrough(
    createStreamEncryptor(dek, prefix, log2, { expectSize: plainSize, onProgress: opts.onProgress }),
  );
  return {
    header,
    stream: prependBytes(head, body),
    size: head.length + streamCiphertextSize(plainSize, log2),
    recoveryKey: made.recoveryKey,
    key: { header, fmk: made.fmk, dek, name: nfc },
  };
}

/** The body (everything after the prefix) → the plaintext, checked end to end. */
export function decryptFxeBody(key: FxeKey, body: ReadableStream<Uint8Array>): ReadableStream<Uint8Array> {
  const h = key.header;
  return body.pipeThrough(createStreamDecryptor(key.dek, b64ToBytes(h.nonce), h.chunk, { expectSize: h.size }));
}

/**
 * A new password for a `.fxe`. Proof is the current password or the recovery
 * key. Only the password slot changes; the recovery key, the escrow slot, the
 * DEK — and so every byte of the body — stay as they are. Throws
 * E2eDecryptError for a wrong credential.
 */
export async function changeFxePassword(h: FxeHeader, cred: E2eCredential, newPassword: string): Promise<FxeHeader> {
  const next = await changePassword(fxeSlots(h), cred, newPassword);
  if (next.fmk !== 'wrapped' || !next.fmk_pw) throw new E2eDecryptError('e2e: the password slot did not re-seal');
  return { ...h, salt: next.salt, iter: next.iter, verify: next.verify, fmk: 'wrapped', fmk_pw: next.fmk_pw };
}

/**
 * The same `.fxe` under a new header: new prefix, then the old body carried
 * over byte for byte. `oldBody` is the stream after the old prefix.
 */
export function replaceFxeHeader(
  h: FxeHeader,
  oldBody: ReadableStream<Uint8Array>,
): { stream: ReadableStream<Uint8Array>; size: number } {
  const head = encodeFxePrefix(h);
  return { stream: prependBytes(head, oldBody), size: head.length + streamCiphertextSize(h.size, h.chunk) };
}
