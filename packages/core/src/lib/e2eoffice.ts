/**
 * e2eoffice - the filex page's half of editing an encrypted office document
 * with ONLYOFFICE in the browser (task #189): the session key, the sealed
 * log entries, and the check that the log the server hands back is the one
 * the editors wrote. Design: docs/E2E-OFFICE.md.
 *
 * ⚠ Prototype: no surface uses this yet. The relay it talks to is
 * backend/internal/e2eoffice; the editor's side (the bridge that answers the
 * editor's Document Server protocol) is the filex-office-editor app, a
 * repository of its own (AGPL-3.0-or-later).
 *
 * Keys. A session has its own random 32-byte key (SK). The folder key (FMK)
 * seals it for the server to keep, exactly the way a file's DEK is sealed in
 * its header (e2ecrypto encryptFile): nothing is added to the folder's key
 * file, and when a session ends its key goes with it. The FMK cannot be a
 * source of derived keys anyway: it is imported for encrypt/decrypt only and
 * cannot be read back. Three keys come out of SK with HKDF, one per use, so a
 * sealed log entry can never be passed off as a cursor frame or a blob.
 *
 * Entries. Every entry an editor writes is AES-256-GCM under the log key, and
 * its authenticated data names the session, the place in the log (seq), the
 * writer, the writer's counter and the kind. The relay puts an entry at
 * exactly seq or refuses it, so the order a reader gets is the order the
 * writers sealed. Inside, each entry carries the digest of the sealed entry
 * before it: the server cannot leave one out, move one, or show one reader
 * something the others did not get without the next entry saying so.
 */
import { b64ToBytes, bytesToB64 } from './e2ecrypto';

/** Bytes in a session key. */
export const OFFICE_E2E_KEY_BYTES = 32;

/** What `prev` says in the first sealed entry of a log. */
export const OFFICE_CHAIN_START = '';

/** The kinds an editor writes (the relay writes join, leave and saved). */
export type OfficeEntryKind = 'changes' | 'lock' | 'release';

const OFFICE_ENTRY_KINDS: readonly string[] = ['changes', 'lock', 'release'];

const IV_LEN = 12;

/** Labels: each sealed thing names what it is, so one is never taken for another. */
const LABEL_KEY = 'filex-oo-sk-v1';
const LABEL_ENTRY = 'filex-oo-v1';
const LABEL_EPH = 'filex-oo-eph-v1';
const LABEL_BLOB = 'filex-oo-blob-v1';
const INFO_LOG = 'filex-oo-log';
const INFO_EPH = 'filex-oo-eph';
const INFO_BLOB = 'filex-oo-blob';

/** The three keys a session's key gives. None can be read back. */
export interface OfficeSessionKeys {
  /** Seals the log's entries. */
  log: CryptoKey;
  /** Seals cursor frames, which are not kept. */
  eph: CryptoKey;
  /** Seals the base document and its images. */
  blob: CryptoKey;
}

/** Where a session key belongs: its session and its file. */
export interface OfficeKeyScope {
  sid: string;
  file: string;
}

/** Copy into a fresh ArrayBuffer (WebCrypto wants a plain buffer, see e2ecrypto). */
function buf(b: Uint8Array): ArrayBuffer {
  return new Uint8Array(b).buffer as ArrayBuffer;
}

const utf8 = new TextEncoder();
const fromUtf8 = new TextDecoder('utf-8', { fatal: true });

/**
 * Authenticated data: the label and the fields, as a JSON array. JSON keeps
 * every field apart from its neighbours (no "ab"+"c" equal to "a"+"bc").
 */
function aad(label: string, ...fields: (string | number)[]): ArrayBuffer {
  return buf(utf8.encode(JSON.stringify([label, ...fields])));
}

/** IV || AES-GCM(key, plain, data). */
async function seal(key: CryptoKey, plain: Uint8Array, data: ArrayBuffer): Promise<Uint8Array> {
  const iv = crypto.getRandomValues(new Uint8Array(IV_LEN));
  const ct = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: buf(iv), additionalData: data }, key, buf(plain)),
  );
  const out = new Uint8Array(IV_LEN + ct.length);
  out.set(iv, 0);
  out.set(ct, IV_LEN);
  return out;
}

/** The inverse of seal; null for a wrong key, other data, or damage. */
async function unseal(key: CryptoKey, sealed: Uint8Array, data: ArrayBuffer): Promise<Uint8Array | null> {
  if (sealed.length <= IV_LEN) return null;
  try {
    const pt = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: buf(sealed.slice(0, IV_LEN)), additionalData: data },
      key,
      buf(sealed.slice(IV_LEN)),
    );
    return new Uint8Array(pt);
  } catch {
    return null;
  }
}

function decodeB64(s: string): Uint8Array | null {
  try {
    return b64ToBytes(s);
  } catch {
    return null;
  }
}

function parseJson(bytes: Uint8Array): unknown {
  try {
    return JSON.parse(fromUtf8.decode(bytes));
  } catch {
    return undefined;
  }
}

// ---------------------------------------------------------------------------
// The session key
// ---------------------------------------------------------------------------

/** A new session key. The caller seals it, derives from it, then zeroes it. */
export function newSessionKey(): Uint8Array {
  return crypto.getRandomValues(new Uint8Array(OFFICE_E2E_KEY_BYTES));
}

/** Seal a session key under the folder key, for the server to keep. */
export async function wrapSessionKey(fmk: CryptoKey, raw: Uint8Array, scope: OfficeKeyScope): Promise<string> {
  if (raw.length !== OFFICE_E2E_KEY_BYTES) throw new Error('e2eoffice: a session key is 32 bytes');
  return bytesToB64(await seal(fmk, raw, aad(LABEL_KEY, scope.sid, scope.file)));
}

/**
 * Open a sealed session key and derive the session's keys from it; null when
 * the folder key is not the one it was sealed with, or it belongs to another
 * session or file.
 */
export async function unwrapSessionKey(
  fmk: CryptoKey,
  wrapped: string,
  scope: OfficeKeyScope,
): Promise<OfficeSessionKeys | null> {
  const sealed = decodeB64(wrapped);
  if (!sealed) return null;
  const raw = await unseal(fmk, sealed, aad(LABEL_KEY, scope.sid, scope.file));
  if (!raw || raw.length !== OFFICE_E2E_KEY_BYTES) return null;
  try {
    return await deriveSessionKeys(raw, scope.sid);
  } finally {
    raw.fill(0);
  }
}

/** The session's three keys from its raw key (HKDF-SHA-256, salt = the session id). */
export async function deriveSessionKeys(raw: Uint8Array, sid: string): Promise<OfficeSessionKeys> {
  const base = await crypto.subtle.importKey('raw', buf(raw), 'HKDF', false, ['deriveKey']);
  const one = (info: string) =>
    crypto.subtle.deriveKey(
      { name: 'HKDF', hash: 'SHA-256', salt: buf(utf8.encode(sid)), info: buf(utf8.encode(info)) },
      base,
      { name: 'AES-GCM', length: 256 },
      false,
      ['encrypt', 'decrypt'],
    );
  const [log, eph, blob] = await Promise.all([one(INFO_LOG), one(INFO_EPH), one(INFO_BLOB)]);
  return { log, eph, blob };
}

// ---------------------------------------------------------------------------
// Log entries
// ---------------------------------------------------------------------------

/** Where an entry is and who wrote it: all of it is authenticated. */
export interface OfficeEntryHeader {
  sid: string;
  seq: number;
  client: string;
  ctr: number;
  kind: OfficeEntryKind;
}

/** Seal an entry's body, chained to the sealed entry before it (`prev`). */
export async function sealEntry(
  key: CryptoKey,
  h: OfficeEntryHeader,
  prev: string,
  body: unknown,
): Promise<string> {
  const plain = utf8.encode(JSON.stringify({ p: prev, b: body }));
  return bytesToB64(await seal(key, plain, aad(LABEL_ENTRY, h.sid, h.seq, h.client, h.ctr, h.kind)));
}

/** Open a sealed entry; null when anything in it or around it is not what was sealed. */
export async function openEntry(
  key: CryptoKey,
  h: OfficeEntryHeader,
  ct: string,
): Promise<{ prev: string; body: unknown } | null> {
  const sealed = decodeB64(ct);
  if (!sealed) return null;
  const plain = await unseal(key, sealed, aad(LABEL_ENTRY, h.sid, h.seq, h.client, h.ctr, h.kind));
  if (!plain) return null;
  const v = parseJson(plain);
  if (!v || typeof v !== 'object' || Array.isArray(v)) return null;
  const o = v as { p?: unknown; b?: unknown };
  if (typeof o.p !== 'string') return null;
  return { prev: o.p, body: o.b };
}

/** The digest the next entry names: hex SHA-256 of the sealed bytes. */
export async function entryDigest(ct: string): Promise<string> {
  const sealed = decodeB64(ct) ?? new Uint8Array(0);
  const d = new Uint8Array(await crypto.subtle.digest('SHA-256', buf(sealed)));
  return Array.from(d, (x) => x.toString(16).padStart(2, '0')).join('');
}

/** One entry as the relay hands it out. */
export interface OfficeWireEntry {
  seq: number;
  kind: string;
  client: string;
  ctr?: number;
  ct?: string;
}

/** Why a log was not taken. */
export type OfficeLogProblem =
  /** an entry is missing or repeated (its seq is not the next one) */
  | 'gap'
  /** a sealed entry does not open: another key, another place, or altered */
  | 'forged'
  /** an entry does not name the sealed entry before it */
  | 'chain'
  /** a writer's counter went back */
  | 'replay'
  /** a kind nobody writes */
  | 'kind';

export type OfficeReadResult =
  | { ok: true; seq: number; kind: string; client: string; body?: unknown }
  | { ok: false; seq: number; problem: OfficeLogProblem };

const RELAY_KINDS: readonly string[] = ['join', 'leave', 'saved'];

/**
 * Reads a session's log in order and refuses the first entry that is not
 * what the writers sealed. After a refusal it refuses everything: the
 * document the editors build from the log would no longer be the same for
 * everyone, so the editor has to stop rather than go on.
 *
 * Feed it one entry at a time and wait for each answer: the chain is a
 * sequence.
 */
export class OfficeLogReader {
  private seq: number;
  private last: string;
  private readonly ctrs = new Map<string, number>();
  private failed: OfficeLogProblem | null = null;

  constructor(
    private readonly key: CryptoKey,
    private readonly sid: string,
    /** Start after this entry (0 = the start of the log). */
    after = 0,
    /** The digest of the last sealed entry up to `after`. */
    digest: string = OFFICE_CHAIN_START,
  ) {
    this.seq = after;
    this.last = digest;
  }

  /** The last entry taken. */
  get head(): number {
    return this.seq;
  }

  /** What the next sealed entry has to name as `prev`. */
  get digest(): string {
    return this.last;
  }

  /** The counter a writer used last (0 = none yet). */
  counter(client: string): number {
    return this.ctrs.get(client) ?? 0;
  }

  async read(e: OfficeWireEntry): Promise<OfficeReadResult> {
    const refuse = (problem: OfficeLogProblem): OfficeReadResult => {
      this.failed = problem;
      return { ok: false, seq: e.seq, problem };
    };
    if (this.failed) return { ok: false, seq: e.seq, problem: this.failed };
    if (e.seq !== this.seq + 1) return refuse('gap');
    if (RELAY_KINDS.includes(e.kind)) {
      this.seq = e.seq;
      return { ok: true, seq: e.seq, kind: e.kind, client: e.client };
    }
    if (!OFFICE_ENTRY_KINDS.includes(e.kind)) return refuse('kind');
    const ctr = e.ctr ?? 0;
    if (typeof e.ct !== 'string' || ctr < 1) return refuse('forged');
    const opened = await openEntry(
      this.key,
      { sid: this.sid, seq: e.seq, client: e.client, ctr, kind: e.kind as OfficeEntryKind },
      e.ct,
    );
    if (!opened) return refuse('forged');
    if (opened.prev !== this.last) return refuse('chain');
    if (ctr <= this.counter(e.client)) return refuse('replay');
    this.ctrs.set(e.client, ctr);
    this.last = await entryDigest(e.ct);
    this.seq = e.seq;
    return { ok: true, seq: e.seq, kind: e.kind, client: e.client, body: opened.body };
  }
}

// ---------------------------------------------------------------------------
// Cursor frames and blobs
// ---------------------------------------------------------------------------

/** Who sent a cursor frame, and its counter (the relay does not keep them). */
export interface OfficeEphScope {
  sid: string;
  client: string;
  ctr: number;
}

export async function sealEph(key: CryptoKey, scope: OfficeEphScope, body: unknown): Promise<string> {
  const plain = utf8.encode(JSON.stringify(body));
  return bytesToB64(await seal(key, plain, aad(LABEL_EPH, scope.sid, scope.client, scope.ctr)));
}

/** undefined when the frame does not open. */
export async function openEph(key: CryptoKey, scope: OfficeEphScope, ct: string): Promise<unknown> {
  const sealed = decodeB64(ct);
  if (!sealed) return undefined;
  const plain = await unseal(key, sealed, aad(LABEL_EPH, scope.sid, scope.client, scope.ctr));
  return plain ? parseJson(plain) : undefined;
}

/** A blob's name inside its session ('base', or an image's name). */
export interface OfficeBlobScope {
  sid: string;
  name: string;
}

/** Seal a blob (raw bytes out: the base document can be large). */
export function sealSessionBlob(key: CryptoKey, scope: OfficeBlobScope, bytes: Uint8Array): Promise<Uint8Array> {
  return seal(key, bytes, aad(LABEL_BLOB, scope.sid, scope.name));
}

/** null when the blob does not open under this name in this session. */
export function openSessionBlob(key: CryptoKey, scope: OfficeBlobScope, sealed: Uint8Array): Promise<Uint8Array | null> {
  return unseal(key, sealed, aad(LABEL_BLOB, scope.sid, scope.name));
}
