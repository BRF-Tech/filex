/**
 * e2evault/reader — opening index files, finding the generation to show, and
 * reading file contents by byte ranges (docs/E2E-VAULT-FORMAT.md → "Index
 * file", "Reading", "File contents").
 *
 * Nothing here talks HTTP: the caller hands in how to fetch an index file and
 * a byte range of a pack (lib/e2evault/api does it over the files API; a test
 * does it from the fixture on disk).
 */
import {
  VAULT_FORMAT,
  VAULT_INDEX_HEADER_LEN,
  VAULT_INDEX_READ_MAX,
  VAULT_KEEP_GENERATIONS,
  VAULT_KIND_INDEX,
  VAULT_MAGIC,
  VaultFormatError,
  bufferView,
  concatBytes,
  fromHex,
  indexFileSize,
  toHex,
  validIndexFileSize,
} from './layout';
import { contentKey, indexKey } from './keys';
import { chunkSpan, mapBodySpan } from './pack';
import { decodeIndexPlaintext, emptyIndexState, type VaultIndexState, type VaultNode } from './vindex';

/** What every read of one vault needs. */
export interface VaultKeys {
  /** The FMK as an HKDF key (lib/e2evault/keys importVaultFmk). */
  fmk: CryptoKey;
  /** The 16 raw bytes of `vault.id`. */
  vaultId: Uint8Array;
  /** `vault.pack`. */
  packLog2: number;
}

/** Read the generation an index file's header names (bytes 16-23, big-endian). */
export function indexHeaderGeneration(b: Uint8Array): number {
  const dv = new DataView(b.buffer, b.byteOffset, b.byteLength);
  const hi = dv.getUint32(16, false);
  const lo = dv.getUint32(20, false);
  return hi * 2 ** 32 + lo;
}

/** The 40-byte plaintext header of an index file of generation `gen`. */
export function buildIndexHeader(gen: number, sealId: Uint8Array): Uint8Array<ArrayBuffer> {
  if (!Number.isSafeInteger(gen) || gen < 0) throw new VaultFormatError('generation_range');
  if (sealId.length !== 16) throw new VaultFormatError('bad_id');
  const h = new Uint8Array(VAULT_INDEX_HEADER_LEN);
  h.set(VAULT_MAGIC, 0);
  h[8] = VAULT_FORMAT;
  h[9] = VAULT_KIND_INDEX;
  const dv = new DataView(h.buffer);
  dv.setUint32(16, Math.floor(gen / 2 ** 32), false);
  dv.setUint32(20, gen >>> 0, false);
  h.set(sealId, 24);
  return h;
}

/**
 * What the server can check of an index file without the key: magic,
 * version, kind, the six zeros, the generation, and a Padmé size.
 */
export function checkIndexHeader(b: Uint8Array, gen: number): void {
  if (b.length > VAULT_INDEX_READ_MAX) throw new VaultFormatError('index_too_large');
  if (!validIndexFileSize(b.length)) throw new VaultFormatError('index_size');
  for (let i = 0; i < 8; i++) if (b[i] !== VAULT_MAGIC[i]) throw new VaultFormatError('index_magic');
  if (b[8] !== VAULT_FORMAT) throw new VaultFormatError('index_version');
  if (b[9] !== VAULT_KIND_INDEX) throw new VaultFormatError('index_kind');
  for (let i = 10; i < 16; i++) if (b[i] !== 0) throw new VaultFormatError('index_zeros');
  if (indexHeaderGeneration(b) !== gen) throw new VaultFormatError('index_generation');
}

/**
 * Open the index file of generation `gen`: check the header, decrypt with
 * the index key of its seal id (associated data = bytes 0 to 39), decode the
 * body, and check the file is the padded size of the body it holds. Throws
 * VaultFormatError ("damaged") for anything wrong.
 */
export async function openIndexFile(keys: VaultKeys, gen: number, file: Uint8Array): Promise<VaultIndexState> {
  // Before anything is decrypted: a reader refuses an index over 64 MiB.
  if (file.length > VAULT_INDEX_READ_MAX) throw new VaultFormatError('index_too_large');
  checkIndexHeader(file, gen);
  const header = file.subarray(0, VAULT_INDEX_HEADER_LEN);
  const sealId = file.slice(24, 40);
  const key = await indexKey(keys.fmk, keys.vaultId, sealId);
  let plain: Uint8Array;
  try {
    plain = new Uint8Array(
      await crypto.subtle.decrypt(
        { name: 'AES-GCM', iv: new Uint8Array(12), additionalData: bufferView(header.slice()) },
        key,
        bufferView(file.subarray(VAULT_INDEX_HEADER_LEN)),
      ),
    );
  } catch {
    throw new VaultFormatError('index_tag', 'vault: the index file does not verify');
  }
  const state = decodeIndexPlaintext(plain, { generation: gen, packLog2: keys.packLog2 });
  if (file.length !== indexFileSize(state.bodyLen)) throw new VaultFormatError('index_size');
  return state;
}

// ---------------------------------------------------------------------
// The generation to show
// ---------------------------------------------------------------------

/** Where index files come from. */
export interface VaultIndexSource {
  /** The bytes of `v/idx/G.fxi`, or null when there is no such file. */
  fetchIndex(gen: number): Promise<Uint8Array | null>;
  /** The generations present (from a listing), any order. Optional. */
  listGenerations?(): Promise<number[]>;
  /** Are there packs at all? Asked only when there is no index file. */
  hasPacks?(): Promise<boolean>;
  /** Waits before the second try of a damaged latest. Default: setTimeout. */
  sleep?(ms: number): Promise<void>;
}

/** The vault cannot be shown at all: no index verifies, or packs and no index. */
export class VaultDamagedError extends Error {
  constructor(public readonly code: 'no_index_but_packs' | 'nothing_verifies') {
    super(`vault: ${code}`);
    this.name = 'VaultDamagedError';
  }
}

export interface LoadedGeneration {
  /** The generation shown. Generation 0 is a vault whose creation stopped
   *  half way: no index file and no pack. */
  state: VaultIndexState;
  /** The newest generation the server names. */
  latest: number;
  /** The newest generation did not verify: an older one is shown, read-only. */
  damagedLatest: boolean;
}

/** How long to wait before fetching a damaged latest once more. */
export const DAMAGED_RETRY_MS = 2000;

/**
 * Load the generation to show: the latest; if it does not verify, the same
 * file once more after 2 seconds (a commit may be landing on a storage
 * without an atomic rename); then the newest older generation that verifies,
 * read-only.
 */
export async function loadLatestGeneration(keys: VaultKeys, latest: number, src: VaultIndexSource): Promise<LoadedGeneration> {
  if (latest <= 0) {
    if (src.hasPacks && (await src.hasPacks())) throw new VaultDamagedError('no_index_but_packs');
    return { state: emptyIndexState(0), latest: 0, damagedLatest: false };
  }
  const tryOpen = async (gen: number): Promise<VaultIndexState | null> => {
    const file = await src.fetchIndex(gen);
    if (!file) return null;
    try {
      return await openIndexFile(keys, gen, file);
    } catch (err) {
      if (err instanceof VaultFormatError) return null;
      throw err;
    }
  };
  let state = await tryOpen(latest);
  if (state) return { state, latest, damagedLatest: false };
  await (src.sleep ?? defaultSleep)(DAMAGED_RETRY_MS);
  state = await tryOpen(latest);
  if (state) return { state, latest, damagedLatest: false };

  let older: number[];
  if (src.listGenerations) {
    older = (await src.listGenerations()).filter((g) => g < latest).sort((a, b) => b - a);
  } else {
    older = [];
    for (let g = latest - 1; g >= Math.max(1, latest - VAULT_KEEP_GENERATIONS - 2); g--) older.push(g);
  }
  for (const g of older) {
    const s = await tryOpen(g);
    if (s) return { state: s, latest, damagedLatest: true };
  }
  throw new VaultDamagedError('nothing_verifies');
}

function defaultSleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/**
 * Rollback guard: within one session a client remembers the highest
 * generation it has seen of each vault; a lower one offered later is a
 * rollback, and the vault stays read-only.
 */
export function createRollbackGuard() {
  const seen = new Map<string, number>();
  return {
    /** Note `gen` of the vault `id` (hex): false when it is a rollback. */
    note(id: string, gen: number): boolean {
      const was = seen.get(id) ?? 0;
      if (gen < was) return false;
      seen.set(id, gen);
      return true;
    },
    highest(id: string): number {
      return seen.get(id) ?? 0;
    },
    forget(id: string): void {
      seen.delete(id);
    },
  };
}

/** The tab's guard: a reload is a new session. */
export const vaultRollbackGuard = createRollbackGuard();

// ---------------------------------------------------------------------
// Contents
// ---------------------------------------------------------------------

/** Fetch `length` bytes at `offset` of pack `pack` (hex). */
export type FetchRange = (pack: string, offset: number, length: number, signal?: AbortSignal) => Promise<Uint8Array>;

/** A pack an extent needs is gone (deleted by a collection meanwhile). */
export class VaultPackMissingError extends Error {
  constructor(public readonly pack: string) {
    super(`vault: pack ${pack} is gone`);
    this.name = 'VaultPackMissingError';
  }
}

/** The 12-byte nonce of chunk `i` of a vault file: 7 zero bytes ‖ i ‖ last. */
export function vaultChunkNonce(i: number, last: boolean): Uint8Array<ArrayBuffer> {
  const n = new Uint8Array(12);
  new DataView(n.buffer).setUint32(7, i, false);
  n[11] = last ? 1 : 0;
  return n;
}

/** Decryption failed: the file is damaged. */
export class VaultContentError extends Error {
  constructor(message = 'vault: the file is damaged') {
    super(message);
    this.name = 'VaultContentError';
  }
}

/**
 * Plaintext bytes `[a, b)` of a file (b clamped to its size). Fetches only
 * the chunks the span needs, one range request per run inside one pack.
 */
export async function readFileRange(
  keys: VaultKeys,
  node: VaultNode,
  a: number,
  b: number,
  fetchRange: FetchRange,
  opts: { key?: CryptoKey; signal?: AbortSignal } = {},
): Promise<Uint8Array<ArrayBuffer>> {
  const end = Math.min(b, node.size);
  if (!node.content || node.size === 0 || a >= end) return new Uint8Array(0);
  const c = node.content;
  const span = chunkSpan(node.size, c.log2, a, end);
  const runs = mapBodySpan(c.extents, span.start, span.end);
  const parts: Uint8Array[] = [];
  for (const run of runs) parts.push(await fetchRange(run.pack, run.offset, run.length, opts.signal));
  const body = concatBytes(parts);
  if (body.length !== span.end - span.start) throw new VaultContentError('vault: a pack returned fewer bytes than asked');
  const key = opts.key ?? (await contentKey(keys.fmk, keys.vaultId, fromHex(c.id)!));
  const chunk = 2 ** c.log2;
  const full = chunk + 16;
  const out = new Uint8Array((span.last - span.first) * chunk + Math.min(chunk, node.size - span.last * chunk));
  let o = 0;
  for (let i = span.first; i <= span.last; i++) {
    const off = (i - span.first) * full;
    const piece = body.subarray(off, Math.min(off + full, body.length));
    let plain: Uint8Array;
    try {
      plain = new Uint8Array(
        await crypto.subtle.decrypt({ name: 'AES-GCM', iv: vaultChunkNonce(i, i === span.count - 1) }, key, bufferView(piece.slice())),
      );
    } catch {
      throw new VaultContentError();
    }
    out.set(plain, o);
    o += plain.length;
  }
  const from = a - span.first * chunk;
  return out.slice(from, from + (end - a));
}

/** The whole file, in memory. */
export function readWholeFile(keys: VaultKeys, node: VaultNode, fetchRange: FetchRange, signal?: AbortSignal): Promise<Uint8Array<ArrayBuffer>> {
  return readFileRange(keys, node, 0, node.size, fetchRange, { signal });
}

/**
 * The file as a stream of plaintext, fetched a few chunks at a time (about
 * 4 MiB of body per step). An error mid-way errors the stream: what was
 * already read is not the file (lib/e2esave discards it).
 */
export function fileStream(keys: VaultKeys, node: VaultNode, fetchRange: FetchRange, signal?: AbortSignal): ReadableStream<Uint8Array> {
  let pos = 0;
  let key: CryptoKey | null = null;
  const step = node.content ? Math.max(1, Math.floor((4 * 1024 * 1024) / 2 ** node.content.log2)) * 2 ** node.content.log2 : 1;
  return new ReadableStream<Uint8Array>({
    async pull(ctl) {
      if (pos >= node.size) {
        ctl.close();
        return;
      }
      try {
        if (!key && node.content) key = await contentKey(keys.fmk, keys.vaultId, fromHex(node.content.id)!);
        const end = Math.min(node.size, pos + step);
        const piece = await readFileRange(keys, node, pos, end, fetchRange, { key: key ?? undefined, signal });
        pos = end;
        ctl.enqueue(piece);
      } catch (err) {
        ctl.error(err);
      }
    },
  });
}

/** Hex of the vault id from the key file's base64url `vault.id`. */
export function vaultIdHex(id: Uint8Array): string {
  return toHex(id);
}
