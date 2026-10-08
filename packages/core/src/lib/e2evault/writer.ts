/**
 * e2evault/writer — one generation's worth of changes, written the canonical
 * way (docs/E2E-VAULT-FORMAT.md → "Writing", "Canonical layout").
 *
 *   1. A generation starts with no open pack; a pack of an earlier generation
 *      is never added to.
 *   2. Operations are applied in order. New contents of n > 0 bytes: draw the
 *      content id, encrypt the body (STREAM, 2^20-byte chunks, nonce prefix
 *      7 zero bytes), append it to the open pack. No open pack: open one,
 *      drawing its id. A full pack is closed without padding and uploaded.
 *   3. After the last operation an open pack that is not full gets its
 *      padding drawn and is closed.
 *   4. The body: the new tree's pack table, the entries in canonical order,
 *      the graveyard (previous graveyard + packs of the previous table the
 *      tree no longer uses, `died` = this generation, minus the packs this
 *      writer deleted). Then the seal id; then the index is sealed.
 *
 * Every random byte comes from ONE source, drawn in exactly that order — so
 * the test vectors' DRBG replays a generation byte for byte. The product's
 * source is `crypto.getRandomValues` and nothing else.
 *
 * Packs go up before the index: the index is the only thing that makes
 * anything visible, and it never names a pack that is not stored.
 */
import {
  VAULT_CHUNK_LOG2,
  VAULT_ENTRIES_WARN,
  VAULT_ENTRIES_WRITE_MAX,
  VAULT_ENTRY_FILE,
  VAULT_ENTRY_FOLDER,
  VAULT_INDEX_HEADER_LEN,
  VAULT_INDEX_WARN,
  VAULT_INDEX_WRITE_MAX,
  VAULT_MAX_DEPTH,
  VAULT_TAG_LEN,
  VaultFormatError,
  bufferView,
  indexFileSize,
  packDataSize,
  toHex,
  utf8,
  vaultNameProblem,
} from './layout';
import { contentKey, indexKey } from './keys';
import { openPack, packBytes, type OpenPack } from './pack';
import { buildIndexHeader, vaultChunkNonce, type FetchRange } from './reader';
import {
  canonicalOrder,
  cloneTree,
  depthOf,
  encodeIndexBody,
  joinVaultPath,
  packTableOf,
  splitVaultPath,
  type VaultExtent,
  type VaultGrave,
  type VaultIndexState,
  type VaultNode,
  type VaultTree,
} from './vindex';

/** `n` random bytes. The product passes `cryptoRandom`; the vectors a DRBG. */
export type RandomSource = (n: number) => Uint8Array;

export const cryptoRandom: RandomSource = (n) => {
  const out = new Uint8Array(n);
  // getRandomValues fills at most 65 536 bytes per call.
  for (let o = 0; o < n; o += 65536) crypto.getRandomValues(out.subarray(o, Math.min(n, o + 65536)));
  return out;
};

/** What a write's new contents can be. `size` must be what the stream yields. */
export type VaultWriteContent =
  | Uint8Array
  | Blob
  | { size: number; stream: () => ReadableStream<Uint8Array> };

export type VaultOp =
  | { op: 'mkdir'; path: string; mtime: number }
  | { op: 'write'; path: string; mtime: number; content: VaultWriteContent }
  | { op: 'delete'; path: string }
  | { op: 'move'; from: string; to: string };

/** Where packs and the index go. Throwing aborts the change. */
export interface VaultSink {
  putPack(id: string, bytes: Uint8Array<ArrayBuffer>, signal?: AbortSignal): Promise<void>;
  putIndex(gen: number, bytes: Uint8Array<ArrayBuffer>, signal?: AbortSignal): Promise<void>;
}

/**
 * An upload whose outcome is unknown (a time-out, a dropped connection) or
 * whose id is taken: the pack goes up again under a new id.
 */
export class VaultPackRetry extends Error {
  constructor(message = 'vault: the pack upload did not finish') {
    super(message);
    this.name = 'VaultPackRetry';
  }
}

/** A change the writer refuses before anything is written. */
export class VaultWriteError extends Error {
  constructor(
    public readonly code:
      | 'parent_missing'
      | 'parent_not_folder'
      | 'name_taken'
      | 'bad_name'
      | 'too_deep'
      | 'not_found'
      | 'is_folder'
      | 'into_itself'
      | 'too_many_entries'
      | 'index_too_large'
      | 'newer_format'
      | 'size_mismatch',
    public readonly path = '',
  ) {
    super(`vault: ${code}${path ? ` (${path})` : ''}`);
    this.name = 'VaultWriteError';
  }
}

export interface VaultWriterOptions {
  fmk: CryptoKey;
  vaultId: Uint8Array;
  packLog2: number;
  random: RandomSource;
  sink: VaultSink;
  signal?: AbortSignal;
  /** Packs this writer deleted (a collection): they leave the graveyard. */
  deleted?: ReadonlySet<string>;
  /** Plaintext bytes consumed so far by the current operation. */
  onProgress?: (bytes: number) => void;
  /** How often a pack upload of unknown outcome is sent again. Default 2. */
  packRetries?: number;
}

export interface VaultCommit {
  generation: number;
  index: Uint8Array<ArrayBuffer>;
  state: VaultIndexState;
  /** Every pack uploaded in this generation, used or not. */
  packsWritten: string[];
}

/** Where a vault stands against its limits (before a change). */
export function vaultLimitStatus(entries: number, indexBytes: number): 'ok' | 'warn' | 'full' {
  if (entries >= VAULT_ENTRIES_WRITE_MAX || indexBytes > VAULT_INDEX_WRITE_MAX) return 'full';
  if (entries >= VAULT_ENTRIES_WARN || indexBytes >= VAULT_INDEX_WARN) return 'warn';
  return 'ok';
}

/** The NFC path of a person's input; throws for a name the format refuses. */
export function normalizeVaultPath(path: string): string {
  const p = String(path ?? '')
    .normalize('NFC')
    .replace(/^\/+|\/+$/g, '');
  if (!p) throw new VaultWriteError('bad_name', path);
  for (const seg of p.split('/')) {
    if (vaultNameProblem(utf8(seg))) throw new VaultWriteError('bad_name', path);
  }
  return p;
}

function abortError(): Error {
  const e = new Error('vault: the change was stopped');
  e.name = 'AbortError';
  return e;
}

/**
 * One generation in the making. Apply operations, then `commit` once. A
 * writer is spent after its commit (or after any error): the next change
 * starts a new writer from the committed state.
 */
export class VaultWriter {
  readonly generation: number;
  private readonly tree: VaultTree;
  private readonly cap: number;
  private open: OpenPack | null = null;
  private readonly written: string[] = [];
  /** Extents made in this generation (an id change on a retry rewrites them). */
  private readonly fresh: VaultExtent[] = [];
  private done = false;

  constructor(
    private readonly base: VaultIndexState,
    private readonly opts: VaultWriterOptions,
  ) {
    if (base.hasExt) throw new VaultWriteError('newer_format');
    this.generation = base.generation + 1;
    this.tree = cloneTree(base.tree);
    this.cap = packDataSize(opts.packLog2);
  }

  /** The tree as it stands with the operations applied so far. */
  get current(): VaultTree {
    return this.tree;
  }

  /** How many entries the tree holds now. */
  get entryCount(): number {
    return this.tree.size;
  }

  private check(): void {
    if (this.done) throw new Error('vault: this writer has committed');
    if (this.opts.signal?.aborted) throw abortError();
  }

  private parentFolder(parent: string, path: string): void {
    if (!parent) return;
    const p = this.tree.get(parent);
    if (!p) throw new VaultWriteError('parent_missing', path);
    if (p.kind !== VAULT_ENTRY_FOLDER) throw new VaultWriteError('parent_not_folder', path);
  }

  private room(path: string): void {
    if (this.tree.size + 1 > VAULT_ENTRIES_WRITE_MAX) throw new VaultWriteError('too_many_entries', path);
    if (depthOf(path) > VAULT_MAX_DEPTH) throw new VaultWriteError('too_deep', path);
  }

  async apply(op: VaultOp): Promise<void> {
    this.check();
    switch (op.op) {
      case 'mkdir': {
        const path = normalizeVaultPath(op.path);
        const { parent, name } = splitVaultPath(path);
        this.parentFolder(parent, path);
        if (this.tree.has(path)) throw new VaultWriteError('name_taken', path);
        this.room(path);
        this.tree.set(path, { path, parent, name, kind: VAULT_ENTRY_FOLDER, mtime: clampTime(op.mtime), size: 0, content: null });
        return;
      }
      case 'write': {
        const path = normalizeVaultPath(op.path);
        const { parent, name } = splitVaultPath(path);
        this.parentFolder(parent, path);
        const was = this.tree.get(path);
        if (was && was.kind === VAULT_ENTRY_FOLDER) throw new VaultWriteError('is_folder', path);
        if (!was) this.room(path);
        const size = contentSize(op.content);
        let content: VaultNode['content'] = null;
        if (size > 0) {
          const id = this.opts.random(16);
          const key = await contentKey(this.opts.fmk, this.opts.vaultId, id);
          const extents = await this.encryptInto(key, op.content, size);
          content = { id: toHex(id), log2: VAULT_CHUNK_LOG2, extents };
        }
        this.tree.set(path, { path, parent, name, kind: VAULT_ENTRY_FILE, mtime: clampTime(op.mtime), size, content });
        return;
      }
      case 'delete': {
        const path = normalizeVaultPath(op.path);
        const node = this.tree.get(path);
        if (!node) throw new VaultWriteError('not_found', path);
        this.tree.delete(path);
        if (node.kind === VAULT_ENTRY_FOLDER) {
          const prefix = path + '/';
          for (const k of [...this.tree.keys()]) if (k.startsWith(prefix)) this.tree.delete(k);
        }
        return;
      }
      case 'move': {
        const from = normalizeVaultPath(op.from);
        const to = normalizeVaultPath(op.to);
        const node = this.tree.get(from);
        if (!node) throw new VaultWriteError('not_found', from);
        if (from === to) return;
        if (to.startsWith(from + '/')) throw new VaultWriteError('into_itself', to);
        const { parent, name } = splitVaultPath(to);
        this.parentFolder(parent, to);
        if (this.tree.has(to)) throw new VaultWriteError('name_taken', to);
        const moved: VaultNode[] = [];
        this.tree.delete(from);
        moved.push({ ...node, path: to, parent, name });
        if (node.kind === VAULT_ENTRY_FOLDER) {
          const prefix = from + '/';
          for (const [k, e] of [...this.tree]) {
            if (!k.startsWith(prefix)) continue;
            this.tree.delete(k);
            const np = to + k.slice(from.length);
            moved.push({ ...e, path: np, parent: to + e.parent.slice(from.length) });
          }
        }
        for (const e of moved) {
          if (depthOf(e.path) > VAULT_MAX_DEPTH) throw new VaultWriteError('too_deep', e.path);
          this.tree.set(e.path, e);
        }
        return;
      }
    }
  }

  /**
   * Repacking (docs/E2E-VAULT-FORMAT.md → "Garbage collection"): copy the
   * live extents that lie in `packs` - entry by entry in index order, each
   * entry's extents in order, the bytes as they are, nothing re-encrypted -
   * into new packs by the canonical layout. Every piece stays an extent of
   * its own, even when two land side by side in one new pack (the format's
   * "Details the implementations settled": no merging), so two writers lay a
   * repack out alike.
   */
  async repack(packs: ReadonlySet<string>, fetchRange: FetchRange): Promise<void> {
    this.check();
    for (const node of canonicalOrder(this.tree)) {
      const c = node.content;
      if (!c || !c.extents.some((x) => packs.has(x.pack))) continue;
      const next: VaultExtent[] = [];
      for (const x of c.extents) {
        if (!packs.has(x.pack)) {
          next.push({ ...x });
          continue;
        }
        const bytes = await fetchRange(x.pack, x.offset, x.length, this.opts.signal);
        if (bytes.length !== x.length) throw new Error('vault: a pack returned fewer bytes than asked');
        next.push(...(await this.append(bytes)));
      }
      this.tree.set(node.path, { ...node, content: { ...c, extents: next } });
    }
  }

  /** Encrypt `content` (`size` bytes) as a STREAM under `key`, into packs. */
  private async encryptInto(key: CryptoKey, content: VaultWriteContent, size: number): Promise<VaultExtent[]> {
    const chunk = 2 ** VAULT_CHUNK_LOG2;
    const count = Math.ceil(size / chunk);
    const extents: VaultExtent[] = [];
    const next = chunkReader(content);
    let seen = 0;
    for (let i = 0; i < count; i++) {
      this.check();
      const want = Math.min(chunk, size - i * chunk);
      const plain = await next(want);
      if (plain.length !== want) throw new VaultWriteError('size_mismatch');
      seen += plain.length;
      const ct = new Uint8Array(
        await crypto.subtle.encrypt({ name: 'AES-GCM', iv: vaultChunkNonce(i, i === count - 1) }, key, bufferView(plain)),
      );
      extents.push(...(await this.append(ct)));
      this.opts.onProgress?.(seen);
    }
    if ((await next(1)).length !== 0) throw new VaultWriteError('size_mismatch');
    return mergeFresh(extents);
  }

  /** Append bytes to the open pack (opening and closing packs as needed). */
  private async append(bytes: Uint8Array): Promise<VaultExtent[]> {
    const out: VaultExtent[] = [];
    let pos = 0;
    while (pos < bytes.length) {
      if (!this.open) this.open = openPack(toHex(this.opts.random(16)), this.opts.packLog2);
      const p = this.open;
      const n = Math.min(bytes.length - pos, this.cap - p.used);
      p.data.set(bytes.subarray(pos, pos + n), p.used);
      const x: VaultExtent = { pack: p.id, offset: 32 + p.used, length: n };
      this.fresh.push(x);
      out.push(x);
      p.used += n;
      pos += n;
      if (p.used === this.cap) {
        this.open = null;
        await this.upload(p);
      }
    }
    return out;
  }

  private async upload(p: OpenPack): Promise<void> {
    const retries = this.opts.packRetries ?? 2;
    for (let attempt = 0; ; attempt++) {
      this.check();
      try {
        await this.opts.sink.putPack(p.id, packBytes(p, this.opts.packLog2), this.opts.signal);
        this.written.push(p.id);
        return;
      } catch (err) {
        if (!(err instanceof VaultPackRetry) || attempt >= retries) throw err;
        // The first upload may have landed: it becomes an orphan. Only the
        // header changes; the data area is sent again as it is.
        const old = p.id;
        p.id = toHex(cryptoRandom(16));
        for (const x of this.fresh) if (x.pack === old) x.pack = p.id;
      }
    }
  }

  /** Close the generation: pad and upload the open pack, seal and send the index. */
  async commit(): Promise<VaultCommit> {
    this.check();
    if (this.open) {
      const p = this.open;
      this.open = null;
      if (p.used < this.cap) p.data.set(this.opts.random(this.cap - p.used), p.used);
      await this.upload(p);
    }
    if (this.tree.size > VAULT_ENTRIES_WRITE_MAX) throw new VaultWriteError('too_many_entries');

    const table = new Set(packTableOf(this.tree));
    const deleted = this.opts.deleted ?? new Set<string>();
    const grave: VaultGrave[] = this.base.grave.filter((g) => !deleted.has(g.pack) && !table.has(g.pack));
    for (const id of this.base.packs) {
      if (!table.has(id) && !deleted.has(id)) grave.push({ pack: id, died: this.generation });
    }
    const { body, packs } = encodeIndexBody(this.tree, grave);
    const size = indexFileSize(body.length);
    if (size > VAULT_INDEX_WRITE_MAX) throw new VaultWriteError('index_too_large');

    const index = await sealIndexFile(this.opts.fmk, this.opts.vaultId, this.generation, body, this.opts.random);
    // A commit of unknown outcome is sent again with the SAME bytes: nothing
    // new is sealed (the nonce rule), and the server's generation check tells
    // a landed first try from a lost one (the sink's business).
    const retries = this.opts.packRetries ?? 2;
    for (let attempt = 0; ; attempt++) {
      this.check();
      try {
        await this.opts.sink.putIndex(this.generation, index, this.opts.signal);
        break;
      } catch (err) {
        if (!(err instanceof VaultPackRetry) || attempt >= retries) throw err;
      }
    }
    this.done = true;
    const sortedGrave = [...grave].sort((a, b) => (a.pack < b.pack ? -1 : a.pack > b.pack ? 1 : 0));
    return {
      generation: this.generation,
      index,
      state: { generation: this.generation, tree: this.tree, packs, grave: sortedGrave, hasExt: false, bodyLen: body.length },
      packsWritten: [...this.written],
    };
  }
}

/**
 * Seal an index file: the body padded with zeros to its Padmé size, under the
 * index key of a seal id drawn from `random`, the 40-byte header as
 * associated data (docs/E2E-VAULT-FORMAT.md → "Index file").
 */
export async function sealIndexFile(
  fmk: CryptoKey,
  vaultId: Uint8Array,
  generation: number,
  body: Uint8Array,
  random: RandomSource,
): Promise<Uint8Array<ArrayBuffer>> {
  const size = indexFileSize(body.length);
  const sealId = random(16);
  const header = buildIndexHeader(generation, sealId);
  const plain = new Uint8Array(size - VAULT_INDEX_HEADER_LEN - VAULT_TAG_LEN);
  plain.set(body, 0);
  const key = await indexKey(fmk, vaultId, sealId);
  const ct = new Uint8Array(
    await crypto.subtle.encrypt({ name: 'AES-GCM', iv: new Uint8Array(12), additionalData: header }, key, plain),
  );
  const index = new Uint8Array(size);
  index.set(header, 0);
  index.set(ct, VAULT_INDEX_HEADER_LEN);
  return index;
}

function clampTime(ms: number): number {
  return Number.isFinite(ms) && ms > 0 ? Math.min(Math.floor(ms), Number.MAX_SAFE_INTEGER) : 0;
}

function contentSize(c: VaultWriteContent): number {
  if (c instanceof Uint8Array) return c.length;
  if (typeof Blob !== 'undefined' && c instanceof Blob) return c.size;
  return (c as { size: number }).size;
}

/** Hands out exactly-sized pieces of the content, in order. */
function chunkReader(c: VaultWriteContent): (n: number) => Promise<Uint8Array> {
  if (c instanceof Uint8Array) {
    let off = 0;
    return async (n) => {
      const out = c.subarray(off, Math.min(c.length, off + n));
      off += out.length;
      return out;
    };
  }
  if (typeof Blob !== 'undefined' && c instanceof Blob) {
    let off = 0;
    return async (n) => {
      const end = Math.min(c.size, off + n);
      const out = new Uint8Array(await c.slice(off, end).arrayBuffer());
      off = end;
      return out;
    };
  }
  const reader = (c as { stream: () => ReadableStream<Uint8Array> }).stream().getReader();
  const pending: Uint8Array[] = [];
  let have = 0;
  let ended = false;
  return async (n) => {
    while (have < n && !ended) {
      const { done, value } = await reader.read();
      if (done) ended = true;
      else if (value && value.length) {
        pending.push(value);
        have += value.length;
      }
    }
    const take = Math.min(n, have);
    const out = new Uint8Array(take);
    let o = 0;
    while (o < take) {
      const p = pending[0];
      const k = Math.min(p.length, take - o);
      out.set(p.subarray(0, k), o);
      o += k;
      if (k === p.length) pending.shift();
      else pending[0] = p.subarray(k);
    }
    have -= take;
    return out;
  };
}

/**
 * Merge extents that follow each other in one pack, IN PLACE: the objects
 * kept are the ones `append` made, so a pack sent again under a new id
 * (`upload`) still finds and renames every extent that points into it.
 */
function mergeFresh(extents: VaultExtent[]): VaultExtent[] {
  const out: VaultExtent[] = [];
  for (const x of extents) {
    const last = out[out.length - 1];
    if (last && last.pack === x.pack && last.offset + last.length === x.offset) last.length += x.length;
    else out.push(x);
  }
  return out;
}
