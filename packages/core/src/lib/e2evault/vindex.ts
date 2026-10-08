/**
 * e2evault/vindex — the index body: the vault's tree, its pack table and its
 * graveyard (docs/E2E-VAULT-FORMAT.md → "Index body", "Canonical order").
 *
 * One encoding per tree: two writers that agree on a tree, its pack table and
 * its graveyard write the same bytes. The decoder is the reader's half and
 * checks every rule of the format in one pass; a body that breaks one is
 * damaged (VaultFormatError with the rule's code).
 *
 * The tree is kept as a map from path (`Belgeler/Arşiv/büyük.bin`, NFC, no
 * leading slash) to its node. Pack and content ids travel as lower-case hex:
 * for ids of one length, string order is byte order.
 */
import {
  ByteReader,
  ByteWriter,
  VAULT_CONTENT_NONE,
  VAULT_CONTENT_STREAM,
  VAULT_ENTRIES_READ_MAX,
  VAULT_ENTRY_FILE,
  VAULT_ENTRY_FOLDER,
  VAULT_FORMAT,
  VAULT_MAX_DEPTH,
  VAULT_PACK_HEADER_LEN,
  VaultFormatError,
  compareBytes,
  fromHex,
  streamBodySize,
  toHex,
  utf8,
  utf8Decode,
  validChunkLog2,
  vaultNameProblem,
} from './layout';

export interface VaultExtent {
  /** The pack's id, 32 lower-case hex digits. */
  pack: string;
  offset: number;
  length: number;
}

export interface VaultContent {
  /** The content id (16 bytes, hex): what the content key is derived from. */
  id: string;
  /** STREAM chunk size, log2 (writers: 20). */
  log2: number;
  extents: VaultExtent[];
}

export interface VaultNode {
  /** `Belgeler/Arşiv/büyük.bin`: names joined by `/`, no leading slash. */
  path: string;
  /** The parent's path; '' = the vault root. */
  parent: string;
  name: string;
  kind: typeof VAULT_ENTRY_FOLDER | typeof VAULT_ENTRY_FILE;
  /** Milliseconds since 1970 (0 for anything earlier). */
  mtime: number;
  /** Plaintext bytes; 0 for a folder. */
  size: number;
  /** Null for a folder and for a file of 0 bytes. */
  content: VaultContent | null;
  /** Extension bytes a newer writer left on this entry (format 1 writes none). */
  ext?: Uint8Array;
}

export type VaultTree = Map<string, VaultNode>;

export interface VaultGrave {
  pack: string;
  died: number;
}

/** One generation, decoded. */
export interface VaultIndexState {
  generation: number;
  tree: VaultTree;
  /** The pack table: every pack an extent uses, ascending. */
  packs: string[];
  /** Packs the tree no longer uses that a kept older generation may. */
  grave: VaultGrave[];
  /** A newer filex wrote extension bytes somewhere: this build only reads. */
  hasExt: boolean;
  /** The body's length (the rest of the plaintext is zero padding). */
  bodyLen: number;
}

export function joinVaultPath(parent: string, name: string): string {
  return parent ? `${parent}/${name}` : name;
}

export function splitVaultPath(path: string): { parent: string; name: string } {
  const i = path.lastIndexOf('/');
  return i < 0 ? { parent: '', name: path } : { parent: path.slice(0, i), name: path.slice(i + 1) };
}

/** A tree's copy: the nodes are copied, their contents shared (never mutated). */
export function cloneTree(tree: VaultTree): VaultTree {
  const t: VaultTree = new Map();
  for (const [k, e] of tree) t.set(k, { ...e });
  return t;
}

/** The children of every folder, each list in byte order of the names. */
export function childrenOf(tree: VaultTree): Map<string, VaultNode[]> {
  const kids = new Map<string, VaultNode[]>();
  const bytes = new Map<VaultNode, Uint8Array>();
  for (const e of tree.values()) {
    let list = kids.get(e.parent);
    if (!list) kids.set(e.parent, (list = []));
    list.push(e);
    bytes.set(e, utf8(e.name));
  }
  for (const list of kids.values()) list.sort((a, b) => compareBytes(bytes.get(a)!, bytes.get(b)!));
  return kids;
}

/** Entries in canonical order: pre-order, siblings by the bytes of their name. */
export function canonicalOrder(tree: VaultTree): VaultNode[] {
  const kids = childrenOf(tree);
  const out: VaultNode[] = [];
  const walk = (p: string) => {
    for (const e of kids.get(p) ?? []) {
      out.push(e);
      if (e.kind === VAULT_ENTRY_FOLDER) walk(e.path);
    }
  };
  walk('');
  return out;
}

/** The pack table of a tree: every pack an extent uses, each once, ascending. */
export function packTableOf(tree: VaultTree): string[] {
  const ids = new Set<string>();
  for (const e of tree.values()) for (const x of e.content?.extents ?? []) ids.add(x.pack);
  return [...ids].sort();
}

/** Depth of a path: the root's children are depth 1. */
export function depthOf(path: string): number {
  return path ? path.split('/').length : 0;
}

// ---------------------------------------------------------------------
// Encoding
// ---------------------------------------------------------------------

/**
 * The canonical body of a tree and a graveyard. A format-1 writer writes no
 * extension bytes; a tree that carries some is never written (the caller
 * refuses first — `VaultIndexState.hasExt`).
 */
export function encodeIndexBody(tree: VaultTree, grave: VaultGrave[]): { body: Uint8Array<ArrayBuffer>; packs: string[]; entries: VaultNode[] } {
  const entries = canonicalOrder(tree);
  const packs = packTableOf(tree);
  const packPos = new Map(packs.map((p, i) => [p, i]));
  const pos = new Map(entries.map((e, i) => [e.path, i + 1]));
  const w = new ByteWriter();
  w.byte(VAULT_FORMAT);
  w.uvarint(0); // flags
  w.uvarint(packs.length);
  for (const p of packs) w.bytes(hexId(p));
  w.uvarint(entries.length);
  for (const e of entries) {
    const name = utf8(e.name);
    w.byte(e.kind);
    w.uvarint(e.parent === '' ? 0 : pos.get(e.parent)!);
    w.uvarint(name.length);
    w.bytes(name);
    w.uvarint(Math.max(0, Math.floor(e.mtime)));
    if (e.kind === VAULT_ENTRY_FILE) {
      w.uvarint(e.size);
      if (e.content) {
        w.byte(VAULT_CONTENT_STREAM);
        w.bytes(hexId(e.content.id));
        w.byte(e.content.log2);
        w.uvarint(e.content.extents.length);
        for (const x of e.content.extents) {
          w.uvarint(packPos.get(x.pack)!);
          w.uvarint(x.offset);
          w.uvarint(x.length);
        }
      } else {
        w.byte(VAULT_CONTENT_NONE);
      }
    }
    w.uvarint(0); // entry ext
  }
  const g = [...grave].sort((a, b) => (a.pack < b.pack ? -1 : a.pack > b.pack ? 1 : 0));
  w.uvarint(g.length);
  for (const x of g) {
    w.bytes(hexId(x.pack));
    w.uvarint(x.died);
  }
  w.uvarint(0); // body ext
  return { body: w.done(), packs, entries };
}

function hexId(h: string): Uint8Array<ArrayBuffer> {
  const b = fromHex(h);
  if (!b || b.length !== 16) throw new VaultFormatError('bad_id', `vault: not a 16-byte id: ${h}`);
  return b;
}

// ---------------------------------------------------------------------
// Decoding
// ---------------------------------------------------------------------

export interface DecodeOptions {
  /** The generation this body claims to be (the graveyard's `died` ≤ it). */
  generation: number;
  /** The vault's pack size, log2 (extents stay inside one pack). */
  packLog2: number;
}

/**
 * Decode an index file's plaintext: the body, then zero bytes. Throws
 * VaultFormatError for anything the format does not allow.
 */
export function decodeIndexPlaintext(plain: Uint8Array, opts: DecodeOptions): VaultIndexState {
  const r = new ByteReader(plain);
  const packEnd = 2 ** opts.packLog2;

  const version = r.byte();
  if (version !== VAULT_FORMAT) throw new VaultFormatError('body_version', `vault: index body version ${version}`);
  const flags = r.uvarint();
  if (flags !== 0) throw new VaultFormatError('flags_unknown', `vault: unknown required feature flags ${flags.toString(2)}`);

  const packCount = r.uvarint();
  const packs: string[] = [];
  for (let i = 0; i < packCount; i++) {
    const id = toHex(r.bytes(16));
    if (i > 0 && !(packs[i - 1] < id)) throw new VaultFormatError('pack_table_order');
    packs.push(id);
  }
  const packUsed = new Uint8Array(packCount);

  const entryCount = r.uvarint();
  if (entryCount > VAULT_ENTRIES_READ_MAX) throw new VaultFormatError('too_many_entries');

  const tree: VaultTree = new Map();
  // Per entry (1-based): its path, whether it is a folder, its depth.
  const paths: string[] = [''];
  const isFolder: boolean[] = [true];
  const depth: number[] = [0];
  const lastName = new Map<number, Uint8Array>();
  const chain: number[] = [];
  let hasExt = false;

  for (let i = 1; i <= entryCount; i++) {
    const kind = r.byte();
    if (kind !== VAULT_ENTRY_FOLDER && kind !== VAULT_ENTRY_FILE) throw new VaultFormatError('entry_kind');
    const parent = r.uvarint();
    const nameLen = r.uvarint();
    const nameBytes = r.bytes(nameLen);
    const mtime = r.uvarint();

    // Where it hangs: an earlier folder on the chain we are inside.
    if (parent > 0) {
      if (parent >= i) throw new VaultFormatError('parent_forward');
      if (!isFolder[parent]) throw new VaultFormatError('parent_is_file');
      while (chain.length > 0 && chain[chain.length - 1] !== parent) chain.pop();
      if (chain.length === 0) throw new VaultFormatError('not_preorder');
    } else {
      chain.length = 0;
    }
    const problem = vaultNameProblem(nameBytes);
    if (problem) throw new VaultFormatError(`name_${problem}`);
    const prev = lastName.get(parent);
    if (prev && compareBytes(prev, nameBytes) >= 0) {
      throw new VaultFormatError(compareBytes(prev, nameBytes) === 0 ? 'duplicate_name' : 'sibling_order');
    }
    lastName.set(parent, nameBytes);
    const name = utf8Decode(nameBytes);
    if (name === null) throw new VaultFormatError('name_utf8');
    const d = depth[parent] + 1;
    if (d > VAULT_MAX_DEPTH) throw new VaultFormatError('too_deep');

    let size = 0;
    let content: VaultContent | null = null;
    if (kind === VAULT_ENTRY_FILE) {
      size = r.uvarint();
      const ck = r.byte();
      if (ck === VAULT_CONTENT_NONE) {
        if (size !== 0) throw new VaultFormatError('sized_without_content');
      } else if (ck === VAULT_CONTENT_STREAM) {
        if (size === 0) throw new VaultFormatError('empty_with_content');
        const id = toHex(r.bytes(16));
        const log2 = r.byte();
        if (!validChunkLog2(log2)) throw new VaultFormatError('chunk_log2');
        const n = r.uvarint();
        if (n < 1) throw new VaultFormatError('no_extents');
        const extents: VaultExtent[] = [];
        let total = 0;
        for (let k = 0; k < n; k++) {
          const p = r.uvarint();
          const offset = r.uvarint();
          const length = r.uvarint();
          if (p >= packCount) throw new VaultFormatError('extent_pack_index');
          if (length < 1) throw new VaultFormatError('extent_empty');
          if (offset < VAULT_PACK_HEADER_LEN) throw new VaultFormatError('extent_in_header');
          if (offset + length > packEnd) throw new VaultFormatError('extent_past_pack');
          packUsed[p] = 1;
          total += length;
          extents.push({ pack: packs[p], offset, length });
        }
        if (total !== streamBodySize(size, log2)) throw new VaultFormatError('extent_sum');
        content = { id, log2, extents };
      } else {
        throw new VaultFormatError('content_kind', `vault: content kind ${ck} is not known to this filex`);
      }
    }
    const extLen = r.uvarint();
    const ext = r.bytes(extLen);
    if (extLen > 0) hasExt = true;

    const parentPath = paths[parent];
    const path = parentPath ? `${parentPath}/${name}` : name;
    paths.push(path);
    isFolder.push(kind === VAULT_ENTRY_FOLDER);
    depth.push(d);
    if (kind === VAULT_ENTRY_FOLDER) chain.push(i);
    const node: VaultNode = { path, parent: parentPath, name, kind: kind as VaultNode['kind'], mtime, size, content };
    if (extLen > 0) node.ext = ext;
    tree.set(path, node);
  }

  for (let p = 0; p < packCount; p++) if (!packUsed[p]) throw new VaultFormatError('pack_unreferenced');

  const graveCount = r.uvarint();
  const grave: VaultGrave[] = [];
  const live = new Set(packs);
  for (let i = 0; i < graveCount; i++) {
    const id = toHex(r.bytes(16));
    const died = r.uvarint();
    if (i > 0 && !(grave[i - 1].pack < id)) throw new VaultFormatError('grave_order');
    if (live.has(id)) throw new VaultFormatError('grave_in_table');
    if (died < 2 || died > opts.generation) throw new VaultFormatError('grave_died');
    grave.push({ pack: id, died });
  }

  const extLen = r.uvarint();
  r.bytes(extLen);
  if (extLen > 0) hasExt = true;

  const bodyLen = r.pos;
  for (let i = bodyLen; i < plain.length; i++) {
    if (plain[i] !== 0) throw new VaultFormatError('nonzero_tail');
  }
  return { generation: opts.generation, tree, packs, grave, hasExt, bodyLen };
}

/** The empty tree of generation 1: `01 00 00 00 00 00`. */
export function emptyIndexState(generation = 0): VaultIndexState {
  return { generation, tree: new Map(), packs: [], grave: [], hasExt: false, bodyLen: 6 };
}

/** How many live bytes each pack of the table holds (extents of `tree`). */
export function liveBytesByPack(tree: VaultTree): Map<string, number> {
  const out = new Map<string, number>();
  for (const e of tree.values()) {
    for (const x of e.content?.extents ?? []) out.set(x.pack, (out.get(x.pack) ?? 0) + x.length);
  }
  return out;
}
