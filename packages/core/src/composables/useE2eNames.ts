/**
 * useE2eNames — the "name view" of encrypted folders: turns the ciphertext
 * names the server stores into the plaintext names people see, for EVERY
 * surface that draws a row or a path (folder listing, split pane, breadcrumb,
 * Recent, Starred, tags, Home, trash, search, the destination picker, the
 * recently-opened tray). One instance per explorer, provided to its children,
 * so the web app, the desktop app and every embed decrypt names the same way
 * (docs/E2E-ENCRYPTION.md → "Encrypted names").
 *
 * ── The contract a decorated row keeps ────────────────────────────────
 *
 *   path            UNCHANGED — the wire path the server knows. Every API
 *                   call keeps using it.
 *   basename        the PLAINTEXT name (or an honest placeholder when the
 *                   folder is locked). Everything that draws, sorts, filters
 *                   or picks an icon reads this.
 *   extension       recomputed from the plaintext name.
 *   e2e_stored      the name the server stores. Anything that SENDS a name
 *                   to the server (rename undo, presence, realtime) must use
 *                   this — `storedName(n)` — never `basename`.
 *   e2e_name_state  'enc' | 'plain' | 'unreadable' | 'locked'.
 *   e2e_root        the encrypted root the row sits in.
 *   e2e_display_dir the plaintext parent folder (adapter-stripped), for the
 *                   Location column and search hits.
 *
 * Decorating is idempotent (a decorated row is recognised by `e2e_stored`)
 * and a content-only folder (marker v1/v2) comes back untouched.
 *
 * ⚠ Nothing here sends a plaintext name anywhere. The only requests are
 * reads of the marker and of long-name sidecars, by their stored paths.
 */

import { ref, type InjectionKey, type Ref } from 'vue';

import type { FileNode } from '../types/FileNode';
import {
  E2E_MARKER_NAME,
  markerHasNames,
  parseMarkerDetailed,
  type E2eKeyRing,
  type E2eMarker,
} from '../lib/e2ecrypto';
import {
  classifyStoredName,
  decryptStoredName,
  effectiveDirId,
  encryptName,
  extensionOf,
  type DecodedName,
  type E2eNameKey,
  type EncryptedName,
} from '../lib/e2enames';

export type E2eNameState = 'enc' | 'plain' | 'unreadable' | 'locked';

export interface E2eNameViewApi {
  fetchBlob(path: string, opts?: { fresh?: boolean }): Promise<{ blob: Blob; url: string }>;
  fetchArrayBuffer(path: string): Promise<ArrayBuffer>;
}

export interface E2eNameViewOptions {
  api: E2eNameViewApi;
  ring: E2eKeyRing;
  /** Label for an item whose folder is locked ("🔒 Encrypted item"). */
  lockedLabel: () => string;
  /** Label for an item whose name could not be read. */
  unreadableLabel: () => string;
  /** wiring:e2 fxe — the original name of a single encrypted file opened in
   *  this tab (useE2eFiles), keyed by its path; undefined when not known. */
  fileName?: (path: string) => string | undefined;
}

/** Wire-path helpers — `<adapter>://<rel>`. */
export function wireJoinPath(dir: string, name: string): string {
  if (!dir) return name;
  return dir.endsWith('://') || dir.endsWith('/') ? dir + name : `${dir}/${name}`;
}

export function wireParentPath(p: string): string {
  const idx = p.indexOf('://');
  const prefix = idx === -1 ? '' : p.slice(0, idx + 3);
  const rel = (idx === -1 ? p : p.slice(idx + 3)).replace(/\/+$/, '');
  const slash = rel.lastIndexOf('/');
  return slash === -1 ? prefix : prefix + rel.slice(0, slash);
}

export function wireBaseName(p: string): string {
  const idx = p.indexOf('://');
  const rel = (idx === -1 ? p : p.slice(idx + 3)).replace(/\/+$/, '');
  const slash = rel.lastIndexOf('/');
  return slash === -1 ? rel : rel.slice(slash + 1);
}

function trimWire(p: string): string {
  const idx = p.indexOf('://');
  if (idx === -1) return p.replace(/^\/+|\/+$/g, '');
  return p.slice(0, idx + 3) + p.slice(idx + 3).replace(/^\/+|\/+$/g, '');
}

function stripAdapter(p: string): string {
  const idx = p.indexOf('://');
  return idx === -1 ? p : p.slice(idx + 3);
}

/** Is `path` strictly below `root` (both wire form)? */
export function isBelowRoot(path: string, root: string): boolean {
  const p = trimWire(path);
  const r = trimWire(root);
  if (!r) return false;
  const base = r.endsWith('://') ? r : `${r}/`;
  return p.length > base.length && p.startsWith(base);
}

/** The stored segments of `path` below `root`. */
function segmentsBelow(path: string, root: string): string[] {
  const p = trimWire(path);
  const r = trimWire(root);
  const base = r.endsWith('://') ? r : `${r}/`;
  return p.slice(base.length).split('/').filter(Boolean);
}

/** The name the server stores for a row — use this for anything SENT to the server. */
export function storedName(n: { basename: string; e2e_stored?: unknown }): string {
  return typeof n.e2e_stored === 'string' ? n.e2e_stored : n.basename;
}

/** Display form of a row's parent folder (plaintext below an encrypted root). */
export function displayParentDir(n: FileNode, fallback: (path: string) => string): string {
  return typeof n.e2e_display_dir === 'string' ? n.e2e_display_dir : fallback(n.path);
}

export function createE2eNameView(opts: E2eNameViewOptions) {
  const { api, ring } = opts;
  /** Bumped whenever something a label depends on changes. */
  const version: Ref<number> = ref(0);
  const markers = new Map<string, Promise<{ marker: E2eMarker | null; unsupported: string[] }>>();
  const markerValues = new Map<string, E2eMarker | null>();
  /** root → stored-name (with parent context for long names) → decoded. */
  const plain = new Map<string, Map<string, DecodedName>>();
  const pendingSegments = new Set<string>();

  function bump() {
    version.value++;
  }

  // ── markers ──────────────────────────────────────────────────────────

  async function loadMarker(root: string) {
    try {
      const { blob, url } = await api.fetchBlob(wireJoinPath(root, E2E_MARKER_NAME), { fresh: true });
      URL.revokeObjectURL(url);
      const parsed = parseMarkerDetailed(await blob.text());
      const value = parsed ? { marker: parsed.marker, unsupported: parsed.unsupported } : { marker: null, unsupported: [] };
      markerValues.set(root, value.marker);
      return value;
    } catch {
      markerValues.set(root, null);
      return { marker: null, unsupported: [] as string[] };
    }
  }

  /** The folder's marker, fetched once per root (see `setMarker`/`forget`). */
  async function markerFor(root: string): Promise<E2eMarker | null> {
    const key = trimWire(root);
    let p = markers.get(key);
    if (!p) {
      p = loadMarker(key);
      markers.set(key, p);
    }
    return (await p).marker;
  }

  /** Known synchronously? `undefined` = not fetched yet. */
  function markerCached(root: string): E2eMarker | null | undefined {
    const key = trimWire(root);
    return markerValues.has(key) ? markerValues.get(key)! : undefined;
  }

  /** Adopt a marker the client just wrote (or just read to unlock). */
  function setMarker(root: string, marker: E2eMarker | null) {
    const key = trimWire(root);
    markerValues.set(key, marker);
    markers.set(key, Promise.resolve({ marker, unsupported: [] }));
    bump();
  }

  /** Forget everything about a root: its marker and every plaintext name. */
  function forget(root: string) {
    const key = trimWire(root);
    markers.delete(key);
    markerValues.delete(key);
    plain.delete(key);
    for (const k of [...dirIds.keys()]) if (k.startsWith(`${key}|`)) dirIds.delete(k);
    bump();
  }

  function namesOn(root: string): boolean {
    return markerHasNames(markerCached(root) ?? null);
  }

  // ── roots ────────────────────────────────────────────────────────────

  /** The unlocked (or otherwise known) encrypted root a wire path sits under. */
  function rootOf(path: string, hint?: string | null): string | null {
    if (hint && isBelowRoot(path, hint)) return trimWire(hint);
    let best: string | null = null;
    const candidates = new Set<string>([...ring.roots(), ...markerValues.keys()]);
    for (const r of candidates) {
      if (isBelowRoot(path, r) && (!best || r.length > best.length)) best = trimWire(r);
    }
    return best;
  }

  // ── names ────────────────────────────────────────────────────────────

  function cacheFor(root: string): Map<string, DecodedName> {
    const key = trimWire(root);
    let m = plain.get(key);
    if (!m) {
      m = new Map();
      plain.set(key, m);
    }
    return m;
  }

  /** Folder ids by wire path (the root's comes from the key). */
  const dirIds = new Map<string, Promise<Uint8Array>>();

  /**
   * The id names directly inside `dirWire` are sealed under: the root's own
   * id, the id a folder's stored name carries, or — for a folder whose name
   * was never encrypted — the one it will carry (lib/e2enames
   * `effectiveDirId`). Walks up the path, once per folder.
   */
  function parentIdOf(nk: E2eNameKey, root: string, dirWire: string): Promise<Uint8Array> {
    const d = trimWire(dirWire);
    const r = trimWire(root);
    if (d === r || !isBelowRoot(d, r)) return Promise.resolve(nk.rootId);
    const key = `${r}|${d}`;
    let p = dirIds.get(key);
    if (!p) {
      p = parentIdOf(nk, root, wireParentPath(d)).then((up) => effectiveDirId(nk, up, wireBaseName(d)));
      dirIds.set(key, p);
    }
    return p;
  }

  /**
   * Decode one stored name. Cached by its full path: the same stored name
   * means different things in different folders.
   */
  const inflight = new Map<string, Promise<DecodedName>>();

  function decode(nk: E2eNameKey, root: string, parentWire: string, stored: string): Promise<DecodedName> {
    const cache = cacheFor(root);
    const key = `${trimWire(parentWire)}/${stored}`;
    const hit = cache.get(key);
    if (hit) return Promise.resolve(hit);
    // One decryption per name even when a whole listing asks at once.
    const fk = `${trimWire(root)}|${key}`;
    const running = inflight.get(fk);
    if (running) return running;
    const p = parentIdOf(nk, root, parentWire).then((parentId) => decryptStoredName(nk, stored, parentId, async (sidecar) => {
      try {
        const buf = await api.fetchArrayBuffer(wireJoinPath(parentWire, sidecar));
        return new TextDecoder().decode(buf);
      } catch {
        return null;
      }
    }))
      .then((out) => {
        // A long name whose sidecar is not there YET (an upload in flight)
        // must not be remembered as unreadable forever.
        if (out.state !== 'unreadable') cache.set(key, out);
        return out;
      })
      .finally(() => inflight.delete(fk));
    inflight.set(fk, p);
    return p;
  }

  /** Remember a name we just produced, so it shows without a round trip. */
  function remember(root: string, parentWire: string, enc: EncryptedName, name: string) {
    cacheFor(root).set(`${trimWire(parentWire)}/${enc.stored}`, { name, state: 'enc' });
    bump();
  }

  /**
   * The plaintext of every segment of `path` below `root`, or null for one
   * that is locked / unreadable. Fetches long-name sidecars as needed.
   */
  async function decodePath(root: string, path: string): Promise<(string | null)[]> {
    const nk = ring.names(root);
    const segs = segmentsBelow(path, root);
    if (!nk) return segs.map(() => null);
    const out: (string | null)[] = [];
    let parent = trimWire(root);
    for (const s of segs) {
      const d = await decode(nk, root, parent, s);
      out.push(d.name);
      parent = wireJoinPath(parent, s);
    }
    return out;
  }

  /** The decoded last segment of `wire` (null: not inside a names root, or locked). */
  async function plainNameOf(wire: string): Promise<DecodedName | null> {
    const root = rootOf(wire);
    if (!root) return null;
    const nk = ring.names(root);
    if (!nk) return null;
    return decode(nk, root, wireParentPath(wire), wireBaseName(wire));
  }

  /** Plaintext relative path of `path`'s parent folder (adapter stripped). */
  async function displayDirOf(root: string, path: string): Promise<string> {
    const rootRel = stripAdapter(trimWire(root));
    const segs = await decodePath(root, wireParentPath(path));
    const below = segs.map((s) => (s === null ? opts.lockedLabel() : s));
    return [rootRel, ...below].filter(Boolean).join('/');
  }

  /**
   * Decorate rows for display. `root` is the listing-level encrypted root
   * (a folder listing's `e2e_root`); rows from other views carry their own
   * `e2e_root`, or are matched against the roots this tab has unlocked.
   */
  async function decorate(rows: FileNode[], ctx: { root?: string | null } = {}): Promise<FileNode[]> {
    // ⚠ The listing's own root is learned even when the listing is EMPTY:
    // `writesEncrypted` answers from what is known, and a new folder or an
    // upload into an empty encrypted-names folder must be encrypted too.
    if (ctx.root) await markerFor(ctx.root);
    const done = await Promise.all(rows.map((r) => decorateOne(r, ctx.root ?? null)));
    return done.filter((r): r is FileNode => r !== null);
  }

  async function decorateOne(r: FileNode, listingRoot: string | null): Promise<FileNode | null> {
    /* wiring:e2 fxe — a `.fxe` opened in this tab shows its real name. */
    const fxe = r.type === 'file' ? opts.fileName?.(r.path) : undefined;
    if (fxe) r = { ...r, fxe_name: fxe };
    const hint = (typeof r.e2e_root === 'string' && r.e2e_root) || listingRoot;
    const root = rootOf(r.path, hint);
    if (!root) return r;
    const marker = await markerFor(root);
    if (!markerHasNames(marker)) return r;
    const stored = storedName(r);
    if (stored === E2E_MARKER_NAME) return null;
    const cls = classifyStoredName(stored);
    if (cls.kind === 'sidecar') return null;
    const nk = ring.names(root);
    const displayDir = await displayDirOf(root, r.path);
    const base = { ...r, e2e_stored: stored, e2e_root: root, e2e_display_dir: displayDir };
    if (!nk) {
      return { ...base, basename: opts.lockedLabel(), extension: '', e2e_name_state: 'locked' as E2eNameState };
    }
    const d = await decode(nk, root, wireParentPath(r.path), stored);
    if (d.state === 'enc' && d.name !== null) {
      return {
        ...base,
        basename: d.name,
        extension: r.type === 'dir' ? '' : extensionOf(d.name),
        e2e_name_state: 'enc' as E2eNameState,
      };
    }
    if (d.state === 'plain') {
      return { ...base, basename: stored, e2e_name_state: 'plain' as E2eNameState };
    }
    return { ...base, basename: opts.unreadableLabel(), extension: '', e2e_name_state: 'unreadable' as E2eNameState };
  }

  /**
   * Synchronous label for one breadcrumb / tab segment: the plaintext when
   * it is known, a locked placeholder when the folder is locked, and null
   * when `wire` is not below an encrypted-names root (use the raw segment).
   * An unknown segment is resolved in the background and `version` bumps.
   */
  function segmentLabel(wire: string): string | null {
    void version.value;
    const root = rootOf(wire);
    if (!root) return null;
    const m = markerCached(root);
    if (m === undefined) {
      void markerFor(root).then(bump);
      return null;
    }
    if (!markerHasNames(m)) return null;
    const nk = ring.names(root);
    if (!nk) return opts.lockedLabel();
    const stored = wireBaseName(wire);
    const parent = wireParentPath(wire);
    const key = `${trimWire(parent)}/${stored}`;
    const hit = cacheFor(root).get(key);
    if (hit) return hit.name ?? opts.unreadableLabel();
    const pk = `${root}|${key}`;
    if (!pendingSegments.has(pk)) {
      pendingSegments.add(pk);
      void decode(nk, root, parent, stored).finally(() => {
        pendingSegments.delete(pk);
        bump();
      });
    }
    return '…';
  }

  /** Plaintext form of a whole wire path (adapter stripped) — best effort, sync. */
  function displayPath(wire: string): string {
    void version.value;
    const root = rootOf(wire);
    if (!root || !namesOn(root)) return stripAdapter(trimWire(wire));
    const rootRel = stripAdapter(trimWire(root));
    const segs = segmentsBelow(wire, root);
    const out: string[] = [];
    let acc = trimWire(root);
    for (const s of segs) {
      acc = wireJoinPath(acc, s);
      out.push(segmentLabel(acc) ?? s);
    }
    return [rootRel, ...out].filter(Boolean).join('/');
  }

  // ── writing names ────────────────────────────────────────────────────

  /**
   * Is a NEW name written into `dirWire` to be encrypted? True when the
   * folder is (inside) an encrypted root whose names are encrypted. The
   * encrypted root's own folder is `dirWire === root`, which counts.
   */
  function writesEncrypted(dirWire: string): string | null {
    const d = trimWire(dirWire);
    for (const r of new Set<string>([...ring.roots(), ...markerValues.keys()])) {
      const rr = trimWire(r);
      if ((d === rr || isBelowRoot(d, rr)) && namesOn(rr)) return rr;
    }
    return null;
  }

  /**
   * The stored name for a plaintext name written into `dirWire`. Throws when
   * the folder's names are encrypted but it is locked — a caller must never
   * fall back to sending the plaintext.
   *
   * A folder needs its id: a NEW folder (`isDir`) gets the derived one; a
   * folder being renamed or moved passes `keepIdOf` — its current wire path —
   * and keeps the id its contents are sealed under (`dirIdFor`).
   */
  async function nameForWrite(
    dirWire: string,
    plainName: string,
    opts: { isDir?: boolean; keepIdOf?: string } = {},
  ): Promise<EncryptedName | null> {
    const root = writesEncrypted(dirWire);
    if (!root) return null;
    const nk = ring.names(root);
    if (!nk) throw new Error('e2e: the folder is locked');
    const parentId = await parentIdOf(nk, root, dirWire);
    const dirId = opts.keepIdOf ? await parentIdOf(nk, root, opts.keepIdOf) : undefined;
    const enc = await encryptName(nk, plainName, parentId, { isDir: opts.isDir, dirId });
    remember(root, dirWire, enc, plainName.normalize('NFC'));
    return enc;
  }

  /** The id a folder's contents are sealed under (see parentIdOf). */
  async function dirIdFor(dirWire: string): Promise<Uint8Array | null> {
    const root = rootOf(dirWire) ?? writesEncrypted(dirWire);
    if (!root) return null;
    const nk = ring.names(root);
    if (!nk) return null;
    return parentIdOf(nk, root, dirWire);
  }

  return {
    version,
    markerFor,
    markerCached,
    setMarker,
    forget,
    namesOn,
    rootOf,
    decorate,
    decodePath,
    plainNameOf,
    segmentLabel,
    displayPath,
    writesEncrypted,
    nameForWrite,
    dirIdFor,
    remember,
    bump,
  };
}

export type E2eNameView = ReturnType<typeof createE2eNameView>;

/** Provided by FileExplorer; injected by the panes, pickers and trays it hosts. */
export const E2E_NAME_VIEW: InjectionKey<E2eNameView> = Symbol('filex.e2eNameView');
