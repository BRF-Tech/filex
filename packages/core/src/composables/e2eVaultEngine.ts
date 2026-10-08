/**
 * The vault's engine (wiring:e2 vault; docs/E2E-VAULT-FORMAT.md → "Clients",
 * "The write lock", "The idle lock"): the sessions of the vaults open in this
 * tab - reading the index, the write lock, changes, collection, the clocks.
 *
 * ⚠ Loaded only when a vault is opened or made: composables/useE2eVault.ts is
 * what the explorer holds, and it imports this module with `import()` on the
 * first call that needs it. Everything this file imports (the rest of
 * lib/e2evault) travels with it, out of the explorer's main chunk. Import it
 * statically from code the explorer always loads and the vault is back in the
 * main chunk (web/tests/quality/vaultLazy.test.ts says where).
 *
 * Paths: a vault entry's wire path is the vault folder's wire path, `/`, and
 * its path in the tree (`docs://Kasa/Belgeler/not.txt`). Those paths exist
 * only in this tab; the server never sees them.
 */
import type { FileNode } from '../types/FileNode';
import { markerIsVault, vaultIdOf, type E2eMarker } from '../lib/e2ecrypto';
import { e2eMimeForExt } from '../lib/e2emime';
import { numberedName } from '../lib/e2enamepass';
import { SaveCancelled, SaveNeedsGesture, SaveTooLarge, pickSaveTarget, type SaveTarget } from '../lib/e2esave';
import { createZipStream, type ZipEntry } from '../lib/zipstream';
import {
  VAULT_ENTRY_FOLDER,
  VAULT_ENTRIES_WRITE_MAX,
  indexFileSize,
  toHex,
  utf8,
  vaultNameProblem,
} from '../lib/e2evault/layout';
import {
  canonicalOrder,
  childrenOf,
  emptyIndexState,
  joinVaultPath,
  splitVaultPath,
  type VaultIndexState,
  type VaultNode,
} from '../lib/e2evault/vindex';
import {
  VaultContentError,
  VaultDamagedError,
  fileStream,
  loadLatestGeneration,
  readWholeFile,
  vaultRollbackGuard,
  type FetchRange,
  type VaultIndexSource,
  type VaultKeys,
} from '../lib/e2evault/reader';
import {
  VaultWriteError,
  VaultWriter,
  cryptoRandom,
  vaultLimitStatus,
  type VaultOp,
  type VaultSink,
} from '../lib/e2evault/writer';
import { planCollection, repackCandidates } from '../lib/e2evault/gc';
import {
  VaultApiError,
  createVaultApi,
  listedTime,
  vaultWire,
  type VaultApi,
  type VaultClientKind,
  type VaultLockGrant,
} from '../lib/e2evault/api';
import {
  VAULT_COMMIT_EVERY_MS,
  VAULT_STATE_POLL_MS,
  VaultIdleLock,
  VaultWriteLock,
  realClock,
  type LockEnd,
} from '../lib/e2evault/lock';
import { vaultRelOf, type VaultHost, type VaultOpenOptions, type VaultReadOnly, type VaultShared, type VaultStripState } from './useE2eVault';

interface Session {
  root: string;
  marker: E2eMarker;
  keys: VaultKeys;
  idHex: string;
  state: VaultIndexState;
  /** Children per folder of `state`, built on first use. */
  kids: Map<string, VaultNode[]> | null;
  /** The level the server gave this account at the vault folder. */
  perm: string | undefined;
  lock: VaultWriteLock | null;
  idle: VaultIdleLock;
  /** Packs uploaded since the lock was taken: never orphans. */
  uploaded: Set<string>;
  /** Graveyard packs this writer deleted: they leave the next commit's graveyard. */
  deleted: Set<string>;
  queue: Promise<unknown>;
  poll: unknown;
  abort: AbortController | null;
  lastState: number;
  closed: boolean;
  strip: VaultStripState;
}

/** A short label for the lock holder: the browser and the system. */
export function vaultLockLabel(kind: VaultClientKind, ua = typeof navigator === 'undefined' ? '' : navigator.userAgent): string {
  const os = /Windows/.test(ua) ? 'Windows' : /Mac OS X|Macintosh/.test(ua) ? 'macOS' : /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iOS' : /Linux/.test(ua) ? 'Linux' : '';
  if (kind === 'desktop') return os ? `filex, ${os}` : 'filex';
  const br = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : '';
  return [br, os].filter(Boolean).join(', ') || 'web';
}

/** A name search in a vault shows at most this many rows. */
const SEARCH_MAX = 500;

function stripWire(p: string): string {
  const i = p.indexOf('://');
  return i === -1 ? p : p.slice(i + 3);
}

function baseName(wire: string): string {
  const s = stripWire(wire).replace(/\/+$/, '');
  return s.slice(s.lastIndexOf('/') + 1);
}

function extOf(name: string): string {
  const i = name.lastIndexOf('.');
  return i > 0 ? name.slice(i + 1).toLowerCase() : '';
}

function abortError(): Error {
  const e = new Error('vault: the change was stopped');
  e.name = 'AbortError';
  return e;
}

export function createVaultEngine(host: VaultHost, shared: VaultShared) {
  const api: VaultApi = createVaultApi(host.http);
  const clock = host.clock ?? realClock;
  const random = host.random ?? cryptoRandom;
  const sessions = new Map<string, Session>();
  /** The shell's state (useE2eVault): the strips it hands the explorer, and
   *  the counter its listings watch. */
  const { strips, version } = shared;

  /** The tree on screen changed (a commit, a newer generation): draw it again.
   *  The strip is reactive by itself and needs no call. */
  function changed(s: Session): void {
    version.value++;
    host.onChanged(s.root);
  }

  // ------------------------------------------------------------------
  // Where a path is
  // ------------------------------------------------------------------

  function isOpen(root: string | null | undefined): boolean {
    return !!root && sessions.has(root) && !sessions.get(root)!.closed;
  }

  const relOf = vaultRelOf;

  function sessionOf(wire: string): Session | null {
    const r = shared.rootOf(wire);
    const s = r ? sessions.get(r) : null;
    return s && !s.closed ? s : null;
  }

  /** The node at `wire`, in the generation on screen. */
  function node(wire: string): VaultNode | null {
    const s = sessionOf(wire);
    if (!s) return null;
    return s.state.tree.get(relOf(s.root, wire)) ?? null;
  }

  function toRow(s: Session, n: VaultNode, count?: number): FileNode {
    const path = vaultWire(s.root, n.path);
    const dir = n.kind === VAULT_ENTRY_FOLDER;
    const ext = dir ? '' : extOf(n.name);
    return {
      path,
      basename: n.name,
      relativePath: stripWire(path),
      type: dir ? 'dir' : 'file',
      extension: ext,
      size: n.size,
      last_modified: n.mtime,
      mime_type: dir ? 'inode/directory' : e2eMimeForExt(ext),
      ...(dir && count !== undefined ? { count } : {}),
      e2e_root: s.root,
      vault_root: s.root,
      ...(s.perm ? { perm: s.perm as FileNode['perm'] } : {}),
    };
  }

  function kidsOf(s: Session): Map<string, VaultNode[]> {
    if (!s.kids) s.kids = childrenOf(s.state.tree);
    return s.kids;
  }

  /** The rows of folder `dirWire` (null: no such folder in the vault). */
  function rows(dirWire: string): FileNode[] | null {
    const s = sessionOf(dirWire);
    if (!s) return null;
    const rel = relOf(s.root, dirWire);
    if (rel) {
      const d = s.state.tree.get(rel);
      if (!d || d.kind !== VAULT_ENTRY_FOLDER) return null;
    }
    const kids = kidsOf(s);
    return (kids.get(rel) ?? []).map((n) => toRow(s, n, n.kind === VAULT_ENTRY_FOLDER ? (kids.get(n.path) ?? []).length : undefined));
  }

  /** Name search inside the vault's index, below `dirWire` (in this tab). */
  function search(dirWire: string, query: string): FileNode[] | null {
    const s = sessionOf(dirWire);
    if (!s) return null;
    const rel = relOf(s.root, dirWire);
    const q = query.trim().toLocaleLowerCase();
    if (!q) return rows(dirWire);
    const kids = kidsOf(s);
    const out: FileNode[] = [];
    for (const n of s.state.tree.values()) {
      if (rel && !n.path.startsWith(rel + '/')) continue;
      if (!n.name.toLocaleLowerCase().includes(q)) continue;
      out.push(toRow(s, n, n.kind === VAULT_ENTRY_FOLDER ? (kids.get(n.path) ?? []).length : undefined));
      if (out.length >= SEARCH_MAX) break;
    }
    return out;
  }

  // ------------------------------------------------------------------
  // Opening and closing
  // ------------------------------------------------------------------

  function indexSource(s: Session): VaultIndexSource {
    return {
      fetchIndex: (g) => api.fetchIndex(s.root, g),
      listGenerations: async () =>
        (await api.listAll(s.root, 'index')).items.map((i) => i.generation).filter((g): g is number => typeof g === 'number'),
      hasPacks: async () => (await api.list(s.root, 'pack', '', 1)).items.length > 0,
    };
  }

  const fetchRangeOf =
    (s: Session): FetchRange =>
    (pack, offset, length, signal) =>
      api.fetchRange(s.root, pack, offset, length, signal);

  function newStrip(root: string): VaultStripState {
    return {
      root,
      name: baseName(root),
      generation: 0,
      mode: 'read',
      busy: false,
      progress: null,
      holder: null,
      readOnly: '',
      shownAt: 0,
      limit: 'ok',
      entries: 0,
      lost: '',
      lostMinutes: 0,
      lostTo: '',
      unsaved: [],
      idleDeadline: 0,
      idleMinutes: 0,
      lockDeadline: 0,
      error: '',
    };
  }

  /** Put a generation on screen (and note it for the rollback guard). */
  function setState(s: Session, st: VaultIndexState): void {
    s.state = st;
    s.kids = null;
    s.strip.generation = st.generation;
    s.strip.entries = st.tree.size;
    s.strip.limit = vaultLimitStatus(st.tree.size, indexFileSize(st.bodyLen));
    if (st.hasExt && !s.strip.readOnly) s.strip.readOnly = 'newer';
    vaultRollbackGuard.note(s.idHex, st.generation);
  }

  function syncDeadlines(s: Session): void {
    s.strip.idleDeadline = s.lock?.held ? s.lock.idleDeadline : 0;
    s.strip.idleMinutes = s.lock?.held ? Math.round(s.lock.idleSeconds / 60) : 0;
    s.strip.lockDeadline = s.idle.deadline;
  }

  /**
   * Open an unlocked vault: ask `state`, load the latest generation that
   * verifies, start the clocks. A vault this tab has just made (`justMade`)
   * starts at generation 1, the empty tree, with nothing to fetch; `initial`
   * starts from a given state (tests).
   */
  async function open(root: string, marker: E2eMarker, fmk: CryptoKey, opts: VaultOpenOptions = {}): Promise<void> {
    if (!markerIsVault(marker)) throw new Error('vault: not a vault key file');
    const initial = opts.initial ?? (opts.justMade ? emptyIndexState(1) : undefined);
    const vaultId = vaultIdOf(marker)!;
    close(root, 'silent');
    const strip = newStrip(root);
    strips[root] = strip;
    const s: Session = {
      root,
      marker,
      keys: { fmk, vaultId, packLog2: marker.vault!.pack },
      idHex: toHex(vaultId),
      state: emptyIndexState(0),
      kids: null,
      perm: opts.perm,
      lock: null,
      idle: new VaultIdleLock(() => void lockIdle(root), clock),
      uploaded: new Set(),
      deleted: new Set(),
      queue: Promise.resolve(),
      poll: null,
      abort: null,
      lastState: 0,
      closed: false,
      strip: strips[root],
    };
    sessions.set(root, s);
    shared.remember(root);
    try {
      if (initial) {
        setState(s, initial);
      } else {
        const st = await api.state(root);
        s.lastState = clock.now();
        noteHolder(s, st.lock);
        if (st.generation < vaultRollbackGuard.highest(s.idHex)) s.strip.readOnly = 'rollback';
        const loaded = await loadLatestGeneration(s.keys, st.generation, indexSource(s));
        if (loaded.damagedLatest) {
          s.strip.readOnly = 'damaged';
          s.strip.shownAt = await commitTimeOf(s, loaded.state.generation);
        }
        setState(s, loaded.state);
      }
    } catch (err) {
      if (err instanceof VaultDamagedError) {
        s.strip.readOnly = 'unreadable';
        s.strip.error = host.t('e2e.vault.unreadable');
      } else {
        sessions.delete(root);
        delete strips[root];
        throw err;
      }
    }
    s.idle.start();
    s.poll = clock.setInterval(() => void poll(s), VAULT_STATE_POLL_MS);
    syncDeadlines(s);
    version.value++;
  }

  /** When generation `g` was committed: its index file's time in the listing. */
  async function commitTimeOf(s: Session, g: number): Promise<number> {
    try {
      const { items } = await api.listAll(s.root, 'index');
      const it = items.find((i) => i.generation === g);
      return it ? listedTime(it.mtime) : 0;
    } catch {
      return 0;
    }
  }

  /**
   * Close a vault in this tab. 'manual' (Lock, or leaving for good) releases
   * the write lock; 'silent' only stops the clocks. The keys are the
   * explorer's to drop (`host.dropKeys`), which `lock` does.
   */
  function close(root: string, how: 'manual' | 'silent' = 'manual'): void {
    const s = sessions.get(root);
    if (!s) return;
    s.closed = true;
    s.abort?.abort();
    if (s.poll !== null) clock.clearInterval(s.poll);
    s.poll = null;
    s.idle.stop();
    if (s.lock) {
      if (how === 'manual') void s.lock.release();
      else s.lock.dispose();
    }
    s.lock = null;
    sessions.delete(root);
    delete strips[root];
    version.value++;
  }

  /** "Lock": the keys leave memory, the lock screen comes back. */
  function lock(root: string): void {
    close(root, 'manual');
    host.dropKeys(root);
    host.onLocked(root, 'manual');
  }

  /** The vault lock: fifteen minutes with nothing done in an open vault. */
  async function lockIdle(root: string): Promise<void> {
    const s = sessions.get(root);
    if (!s || s.closed) return;
    // Never while this session writes: the clock is paused then, and a
    // change in flight is activity.
    if (s.lock?.held || s.strip.busy) {
      s.idle.touch();
      return;
    }
    close(root, 'manual');
    host.dropKeys(root);
    host.onLocked(root, 'idle');
  }

  /** Every vault (the explorer unmounts, or the page closes). */
  function closeAll(how: 'manual' | 'silent' = 'manual'): void {
    for (const r of [...sessions.keys()]) close(r, how);
  }

  /** The page is closing: release with keepalive, best effort. */
  function onPageHide(): void {
    for (const s of sessions.values()) s.lock?.releaseOnUnload();
  }

  /** The tab came back to the foreground: a vault lock that is due happens now. */
  function onVisible(): void {
    for (const s of [...sessions.values()]) s.idle.check();
  }

  /** A listing, an opening, a download or a write: the vault lock waits. */
  function touch(wire: string): void {
    const s = sessionOf(wire);
    if (!s) return;
    s.idle.touch();
    syncDeadlines(s);
  }

  // ------------------------------------------------------------------
  // Following other writers
  // ------------------------------------------------------------------

  function noteHolder(s: Session, l: { holder: { name: string; client: string; label?: string }; since: string; mine: boolean } | null): void {
    // While this session holds the lock, nobody else does.
    if (s.lock?.held) {
      s.strip.holder = null;
      return;
    }
    s.strip.holder = l && !l.mine ? { name: l.holder?.name ?? '', client: l.holder?.client ?? '', label: l.holder?.label, since: l.since } : null;
  }

  async function poll(s: Session): Promise<void> {
    if (s.closed) return;
    try {
      const st = await api.state(s.root, undefined, s.lock?.tokenValue);
      s.lastState = clock.now();
      if (s.closed) return;
      noteHolder(s, st.lock);
      if (st.generation < vaultRollbackGuard.highest(s.idHex)) {
        s.strip.readOnly = 'rollback';
      } else if (st.generation > s.state.generation && !s.strip.busy) {
        await refresh(s, st.generation);
      }
      syncDeadlines(s);
    } catch {
      /* the next poll asks again */
    }
  }

  /** Load generation `gen` (the newest the server names) and show it. */
  async function refresh(s: Session, gen: number): Promise<void> {
    const loaded = await loadLatestGeneration(s.keys, gen, indexSource(s));
    if (s.closed) return;
    if (loaded.damagedLatest) {
      s.strip.readOnly = 'damaged';
      s.strip.shownAt = await commitTimeOf(s, loaded.state.generation);
    } else if (s.strip.readOnly === 'damaged') {
      s.strip.readOnly = '';
      s.strip.shownAt = 0;
    }
    setState(s, loaded.state);
    changed(s);
  }

  /** A realtime event: `vault.generation` or `vault.lock`. */
  function onEvent(msg: { type?: string; path?: string; generation?: number; held?: boolean; holder?: unknown }): void {
    const root = typeof msg.path === 'string' ? msg.path.replace(/\/+$/, '') : '';
    const s = root ? sessions.get(root) : null;
    if (!s || s.closed) return;
    if (msg.type === 'vault.generation' && typeof msg.generation === 'number' && msg.generation > s.state.generation && !s.lock?.held) {
      s.queue = s.queue.then(() => refresh(s, msg.generation!)).catch(() => undefined);
    } else if (msg.type === 'vault.lock') {
      void poll(s);
    }
  }

  /** Ask `state` before opening a file (a newer generation may have moved it). */
  async function freshBeforeOpen(s: Session): Promise<void> {
    if (s.lock?.held || clock.now() - s.lastState < 5000) return;
    try {
      const st = await api.state(s.root);
      s.lastState = clock.now();
      noteHolder(s, st.lock);
      if (st.generation > s.state.generation) await refresh(s, st.generation);
    } catch {
      /* read what is on screen */
    }
  }

  // ------------------------------------------------------------------
  // Reading
  // ------------------------------------------------------------------

  /** The bytes of a file, decrypted. Tries once more after loading the latest
   *  generation when a pack is gone (a collection ran meanwhile). */
  async function readBytes(wire: string): Promise<Uint8Array> {
    const s = sessionOf(wire);
    if (!s) throw new Error('vault: not open');
    touch(wire);
    await freshBeforeOpen(s);
    const rel = relOf(s.root, wire);
    const n = s.state.tree.get(rel);
    if (!n || n.kind === VAULT_ENTRY_FOLDER) throw new VaultWriteError('not_found', rel);
    try {
      return await readWholeFile(s.keys, n, fetchRangeOf(s));
    } catch (err) {
      if (!(err instanceof VaultApiError && err.code === 'VAULT_PACK_GONE')) throw err;
      const st = await api.state(s.root);
      await refresh(s, st.generation);
      const again = s.state.tree.get(rel);
      if (!again || again.kind === VAULT_ENTRY_FOLDER) throw new VaultWriteError('not_found', rel);
      return readWholeFile(s.keys, again, fetchRangeOf(s));
    }
  }

  /** A blob URL of the decrypted file, for the viewers. The caller revokes it. */
  async function readUrl(wire: string, mime?: string): Promise<string> {
    const bytes = await readBytes(wire);
    const ext = extOf(baseName(wire));
    return URL.createObjectURL(new Blob([bytes as Uint8Array<ArrayBuffer>], { type: mime || e2eMimeForExt(ext) }));
  }

  /**
   * Download decrypted: one file as itself, anything else as a zip made in
   * this tab (lib/zipstream). The save target is picked FIRST, in the click.
   */
  async function download(targets: FileNode[]): Promise<void> {
    if (targets.length === 0) return;
    const s = sessionOf(targets[0].path);
    if (!s) return;
    const single = targets.length === 1 && targets[0].type === 'file';
    const zipName = targets.length === 1 ? `${targets[0].basename}.zip` : `${s.strip.name}.zip`;
    let target: SaveTarget;
    try {
      target = await pickSaveTarget(single ? targets[0].basename : zipName, single ? targets[0].size : undefined);
    } catch (err) {
      if (err instanceof SaveCancelled) return;
      if (err instanceof SaveNeedsGesture) host.toast(host.t('e2e.dl.click_again'), true);
      else if (err instanceof SaveTooLarge) host.toast(host.t('e2e.vault.dl_too_big'), true);
      else host.toast(host.t('e2e.download.failed'), true);
      return;
    }
    touch(targets[0].path);
    const fr = fetchRangeOf(s);
    try {
      if (single) {
        const n = node(targets[0].path);
        if (!n) throw new VaultWriteError('not_found', targets[0].path);
        await target.write(fileStream(s.keys, n, fr), { mime: e2eMimeForExt(extOf(n.name)) });
        return;
      }
      const kids = kidsOf(s);
      // A function declaration does not keep `s`'s narrowing (TS18047).
      const keys = s.keys;
      async function* walk(n: VaultNode, prefix: string): AsyncGenerator<ZipEntry> {
        if (n.kind === VAULT_ENTRY_FOLDER) {
          yield { name: `${prefix}/`, mtime: new Date(n.mtime) };
          for (const k of kids.get(n.path) ?? []) yield* walk(k, `${prefix}/${k.name}`);
        } else {
          yield { name: prefix, sizeHint: n.size, mtime: new Date(n.mtime), data: fileStream(keys, n, fr) };
        }
      }
      async function* all(): AsyncGenerator<ZipEntry> {
        for (const t of targets) {
          const n = node(t.path);
          if (n) yield* walk(n, n.name);
        }
      }
      host.toast(host.t('e2e.dl.zip_started', { name: zipName }));
      await target.write(createZipStream(all()), { mime: 'application/zip' });
      host.toast(host.t('e2e.dl.zip_done', { name: zipName }));
    } catch (err) {
      await target.discard();
      if (err instanceof SaveCancelled) return;
      host.emitError?.((err as Error)?.message ?? String(err), 'e2e-vault-download');
      host.toast(err instanceof VaultContentError ? host.t('e2e.vault.damaged_file') : host.t('e2e.download.failed'), true);
    }
  }

  // ------------------------------------------------------------------
  // Writing
  // ------------------------------------------------------------------

  function enqueue<T>(s: Session, task: () => Promise<T>): Promise<T> {
    const run = s.queue.then(task, task);
    s.queue = run.catch(() => undefined);
    return run;
  }

  function onLockLost(s: Session, reason: LockEnd, holder?: { name?: string; label?: string } | null): void {
    s.strip.lostMinutes = s.lock ? Math.max(1, Math.round(s.lock.idleSeconds / 60)) : Math.max(1, s.strip.idleMinutes);
    s.strip.lostTo = holder ? holder.name || holder.label || '' : '';
    s.lock = null;
    s.abort?.abort();
    s.strip.mode = 'read';
    s.strip.lost = reason;
    if (!s.closed) s.idle.resume();
    syncDeadlines(s);
    // Who took over, for the strip.
    if (reason === 'taken' || reason === 'broken') void poll(s);
  }

  /** Take the write lock (when it is not held), and load the latest generation. */
  async function ensureLock(s: Session): Promise<void> {
    if (s.lock?.held) return;
    s.lock?.dispose();
    const kind = host.clientKind();
    const lock = new VaultWriteLock({
      api,
      path: s.root,
      client: kind,
      label: vaultLockLabel(kind),
      clock,
      onLost: (r, h) => onLockLost(s, r, h),
      headersNow: () => host.headersNow(),
    });
    let grant: VaultLockGrant;
    try {
      grant = await lock.acquire();
    } catch (err) {
      if (err instanceof VaultApiError && err.code === 'VAULT_LOCKED') {
        const h = err.holder;
        s.strip.holder = { name: h?.name ?? '', client: String(h?.client ?? ''), label: h?.label, since: typeof err.fields.since === 'string' ? err.fields.since : '' };
      }
      throw err;
    }
    s.lock = lock;
    s.uploaded = new Set();
    s.deleted = new Set();
    s.strip.holder = null;
    s.strip.mode = 'write';
    s.idle.pause();
    // Latest first: right after taking the lock, the generation the server
    // names is the one to change.
    if (grant.generation !== s.state.generation) {
      const loaded = await loadLatestGeneration(s.keys, grant.generation, indexSource(s));
      if (loaded.damagedLatest) {
        s.strip.readOnly = 'damaged';
        s.strip.shownAt = await commitTimeOf(s, loaded.state.generation);
        setState(s, loaded.state);
        await releaseLock(s);
        throw new VaultReadOnlyError('damaged');
      }
      setState(s, loaded.state);
    }
    if (s.state.hasExt) {
      await releaseLock(s);
      throw new VaultReadOnlyError('newer');
    }
    syncDeadlines(s);
    // What earlier sessions left is collected by the pass that follows this
    // change's commit (`change`): the change in hand goes first.
  }

  async function releaseLock(s: Session): Promise<void> {
    const l = s.lock;
    s.lock = null;
    s.strip.mode = 'read';
    if (l) await l.release();
    if (!s.closed) s.idle.resume();
    syncDeadlines(s);
  }

  function sinkFor(s: Session, signal: AbortSignal): VaultSink {
    const tried = new Set<number>();
    const token = () => {
      const t = s.lock?.tokenValue;
      if (!t) throw abortError();
      return t;
    };
    return {
      async putPack(id, bytes) {
        try {
          await api.putPack(s.root, id, bytes, token(), signal);
        } catch (err) {
          if (s.lock?.lostBy(err)) throw abortError();
          throw err;
        }
        s.uploaded.add(id);
        s.lock?.wrote();
      },
      async putIndex(gen, bytes) {
        const again = tried.has(gen);
        tried.add(gen);
        try {
          await api.putIndex(s.root, gen, bytes, token(), signal);
        } catch (err) {
          if (s.lock?.lostBy(err)) throw abortError();
          // A retry whose first try landed: the server is at this generation.
          if (again && err instanceof VaultApiError && err.code === 'VAULT_GENERATION' && err.latest === gen) return;
          throw err;
        }
        s.lock?.wrote();
      },
    };
  }

  interface ChangeCtx {
    apply(op: VaultOp): Promise<void>;
    /** Copy the live extents in `packs` into new packs (garbage collection). */
    repack(packs: ReadonlySet<string>, fr: FetchRange): Promise<void>;
    /** The tree with the operations so far. */
    tree(): Map<string, VaultNode>;
    /** Commit now when 30 seconds of a long change stored packs. */
    checkpoint(): Promise<void>;
    progress(done: number, total: number): void;
    signal: AbortSignal;
  }

  /**
   * One change the person sees: take the lock, apply, commit. Only after the
   * commit succeeded is it reported as saved. False when nothing was saved
   * (the reason is already said).
   */
  async function change(s: Session, labels: string[], run: (ctx: ChangeCtx) => Promise<void>, opts: { quiet?: boolean } = {}): Promise<boolean> {
    return enqueue(s, async () => {
      if (s.closed) return false;
      if (s.strip.readOnly) {
        host.toast(readOnlyWords(s.strip.readOnly), true);
        return false;
      }
      s.strip.lost = '';
      s.strip.lostTo = '';
      s.strip.unsaved = [];
      s.strip.error = '';
      try {
        await ensureLock(s);
      } catch (err) {
        say(s, err);
        return false;
      }
      const lock = s.lock!;
      const done = lock.busy();
      const ctl = new AbortController();
      s.abort = ctl;
      s.strip.busy = true;
      const newWriter = () =>
        new VaultWriter(s.state, {
          fmk: s.keys.fmk,
          vaultId: s.keys.vaultId,
          packLog2: s.keys.packLog2,
          random,
          sink: sinkFor(s, ctl.signal),
          signal: ctl.signal,
          deleted: s.deleted,
        });
      let writer = newWriter();
      let packsAt = s.uploaded.size;
      let lastCommit = clock.now();
      const commit = async () => {
        const c = await writer.commit();
        s.deleted = new Set();
        setState(s, c.state);
        lastCommit = clock.now();
        packsAt = s.uploaded.size;
        changed(s);
      };
      const ctx: ChangeCtx = {
        apply: (op) => writer.apply(op),
        repack: (packs, fr) => writer.repack(packs, fr),
        tree: () => writer.current,
        async checkpoint() {
          if (clock.now() - lastCommit >= VAULT_COMMIT_EVERY_MS && s.uploaded.size > packsAt) {
            await commit();
            writer = newWriter();
          }
        },
        progress(d, t) {
          s.strip.progress = { done: d, total: t };
        },
        signal: ctl.signal,
      };
      try {
        await run(ctx);
        await commit();
        // After each commit: a collection pass (and a repack when it pays).
        void enqueue(s, () => collect(s));
        return true;
      } catch (err) {
        if (ctl.signal.aborted || (err as Error)?.name === 'AbortError') {
          s.strip.unsaved = labels;
        } else if (!opts.quiet) {
          say(s, err);
        }
        return false;
      } finally {
        done();
        if (s.abort === ctl) s.abort = null;
        s.strip.busy = false;
        s.strip.progress = null;
        syncDeadlines(s);
      }
    });
  }

  /**
   * A failure, said in the person's words.
   *
   * ⚠ A refusal of the vault API is said in the SERVER's words (its
   * `message`, server.e2e.vault.* in the reader's language - "Ayşe is writing
   * this vault…", "This folder is not a vault."): this used to keep its own
   * copies of those two (0.54 audit A2). What the browser alone knows - a
   * write the encrypted index refused, a vault that reads damaged - stays
   * said here: the server cannot see inside a vault.
   */
  function say(s: Session, err: unknown): void {
    let msg: string;
    const serverSaid = err instanceof VaultApiError && typeof err.fields.message === 'string' ? err.fields.message.trim() : '';
    if (err instanceof VaultReadOnlyError) msg = readOnlyWords(err.why);
    else if (err instanceof VaultWriteError) msg = host.t(`e2e.vault.err.${err.code}`, { name: err.path ? baseName(err.path) : '' });
    else if (serverSaid) {
      // Isolated for the reader's direction by the translator that knows it
      // (useLocale's `t.foreign`, lib/errorWords `T`).
      const foreign = (host.t as { foreign?: (text: string) => string }).foreign;
      msg = typeof foreign === 'function' ? foreign(serverSaid) : serverSaid;
    }
    else msg = host.t('e2e.vault.err.failed');
    s.strip.error = msg;
    host.emitError?.((err as Error)?.message ?? String(err), 'e2e-vault');
    host.toast(msg, true);
  }

  function readOnlyWords(why: VaultReadOnly): string {
    if (why === 'damaged') return host.t('e2e.vault.ro_damaged_short');
    if (why === 'rollback') return host.t('e2e.vault.ro_rollback');
    if (why === 'newer') return host.t('e2e.vault.ro_newer');
    if (why === 'unreadable') return host.t('e2e.vault.unreadable');
    return host.t('e2e.vault.err.failed');
  }

  /** The collection pass the lock holder runs after a commit. */
  async function collect(s: Session): Promise<void> {
    if (s.closed || !s.lock?.held || s.strip.readOnly || s.state.hasExt || s.state.generation < 1) return;
    const token = s.lock.tokenValue;
    if (!token) return;
    try {
      const [idx, packs] = await Promise.all([api.listAll(s.root, 'index'), api.listAll(s.root, 'pack')]);
      // Retention is measured on the SERVER's clock (the listing's `now`):
      // a client whose clock runs ahead would delete what a reader still
      // needs. Without it, nothing that depends on time is deleted.
      const serverNow = idx.now;
      const plan = planCollection({
        latest: s.state,
        indexes: idx.items.filter((i) => typeof i.generation === 'number').map((i) => ({ generation: i.generation!, mtime: listedTime(i.mtime) })),
        packs: packs.items.filter((p) => typeof p.id === 'string').map((p) => ({ id: p.id! })),
        uploaded: s.uploaded,
        now: serverNow,
      });
      if (plan.indexes.length + plan.packs.length > 0 && s.lock?.held) {
        await api.del(s.root, plan.packs, plan.indexes, token);
        s.lock?.wrote();
        const grave = new Set(s.state.grave.map((g) => g.pack));
        for (const p of plan.packs) if (grave.has(p)) s.deleted.add(p);
      }
    } catch (err) {
      if (s.lock?.lostBy(err)) return;
      /* the next pass deletes it */
    }
    const S = repackCandidates(s.state, s.keys.packLog2);
    if (S && s.lock?.held) {
      const fr = fetchRangeOf(s);
      void change(s, [], (ctx) => ctx.repack(S, fr), { quiet: true });
    }
  }

  // ------------------------------------------------------------------
  // The person's changes
  // ------------------------------------------------------------------

  function freeName(tree: Map<string, VaultNode>, dir: string, name: string): string {
    if (!tree.has(joinVaultPath(dir, name))) return name;
    for (let i = 2; i < 10_000; i++) {
      const n = numberedName(name, i);
      if (!tree.has(joinVaultPath(dir, n))) return n;
    }
    throw new VaultWriteError('name_taken', name);
  }

  function checkName(name: string): string {
    const n = String(name ?? '').normalize('NFC');
    if (vaultNameProblem(utf8(n))) throw new VaultWriteError('bad_name', n);
    return n;
  }

  /** Warn from 200 000 entries; refuse at the limit, before anything is written. */
  function roomFor(s: Session, more: number): boolean {
    const after = s.state.tree.size + more;
    if (after > VAULT_ENTRIES_WRITE_MAX) {
      host.toast(host.t('e2e.vault.err.too_many_entries'), true);
      return false;
    }
    const status = vaultLimitStatus(after, indexFileSize(s.state.bodyLen));
    if (status === 'full') {
      host.toast(host.t('e2e.vault.err.index_too_large'), true);
      return false;
    }
    if (status === 'warn') {
      const count = host.formatCount ?? ((n: number) => String(n));
      host.toast(host.t('e2e.vault.limit_warn', { held: count(after), max: count(VAULT_ENTRIES_WRITE_MAX) }));
    }
    return true;
  }

  /**
   * Upload files into the vault folder `dirWire`. A file from a folder upload
   * (`webkitRelativePath`) brings its folders. A name that is taken is asked
   * about (host.askName: "upload it as `x (2).txt`?") before anything is
   * written, as nothing in a vault is overwritten - it keeps no earlier
   * version; a file the person declines stays out. A name taken in the
   * meantime (another writer's generation) gets a number.
   */
  async function upload(dirWire: string, picked: File[]): Promise<boolean> {
    const s = sessionOf(dirWire);
    if (!s || picked.length === 0) return false;
    if (!roomFor(s, picked.length)) return false;
    touch(dirWire);
    const dir = relOf(s.root, dirWire);
    const asked = new Map<File, string>();
    const files: File[] = [];
    if (host.askName) {
      const planned = new Set<string>();
      for (const f of picked) {
        const rel = String((f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name);
        let segs: string[];
        try {
          segs = rel.split('/').filter(Boolean).map(checkName);
        } catch (err) {
          say(s, err);
          return false;
        }
        const at = segs.slice(0, -1).reduce((p, seg) => joinVaultPath(p, seg), dir);
        const name = segs[segs.length - 1] ?? checkName(f.name);
        const path = joinVaultPath(at, name);
        if (!s.state.tree.has(path) && !planned.has(path)) {
          planned.add(path);
          files.push(f);
          continue;
        }
        let suggested = name;
        for (let i = 2; i < 10_000; i++) {
          suggested = numberedName(name, i);
          const p = joinVaultPath(at, suggested);
          if (!s.state.tree.has(p) && !planned.has(p)) break;
        }
        const folder = [baseName(s.root), ...at.split('/').filter(Boolean)].join(' / ');
        if (!(await host.askName({ name, folder, suggested }))) continue;
        planned.add(joinVaultPath(at, suggested));
        asked.set(f, suggested);
        files.push(f);
      }
      if (files.length === 0) return false;
    } else {
      files.push(...picked);
    }
    const total = files.reduce((n, f) => n + f.size, 0);
    return change(s, files.map((f) => asked.get(f) ?? f.name), async (ctx) => {
      let done = 0;
      for (const f of files) {
        const rel = String((f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name);
        const segs = rel.split('/').filter(Boolean).map(checkName);
        let at = dir;
        for (const seg of segs.slice(0, -1)) {
          const p = joinVaultPath(at, seg);
          const there = ctx.tree().get(p);
          if (!there) await ctx.apply({ op: 'mkdir', path: p, mtime: clock.now() });
          else if (there.kind !== VAULT_ENTRY_FOLDER) throw new VaultWriteError('name_taken', p);
          at = p;
        }
        const name = freeName(ctx.tree(), at, asked.get(f) ?? segs[segs.length - 1] ?? checkName(f.name));
        await ctx.apply({ op: 'write', path: joinVaultPath(at, name), mtime: f.lastModified || clock.now(), content: f });
        done += f.size;
        ctx.progress(done, total);
        await ctx.checkpoint();
      }
    });
  }

  /** A new folder in `dirWire`. */
  async function mkdir(dirWire: string, name: string): Promise<boolean> {
    const s = sessionOf(dirWire);
    if (!s) return false;
    if (!roomFor(s, 1)) return false;
    touch(dirWire);
    let clean: string;
    try {
      clean = checkName(name);
    } catch (err) {
      say(s, err);
      return false;
    }
    const path = joinVaultPath(relOf(s.root, dirWire), clean);
    return change(s, [clean], (ctx) => ctx.apply({ op: 'mkdir', path, mtime: clock.now() }));
  }

  /** Rename an entry in place. */
  async function rename(wire: string, newName: string): Promise<boolean> {
    const s = sessionOf(wire);
    if (!s) return false;
    touch(wire);
    let clean: string;
    try {
      clean = checkName(newName);
    } catch (err) {
      say(s, err);
      return false;
    }
    const from = relOf(s.root, wire);
    const to = joinVaultPath(splitVaultPath(from).parent, clean);
    if (to === from) return true;
    return change(s, [baseName(wire)], (ctx) => ctx.apply({ op: 'move', from, to }));
  }

  /** Move entries to the folder `toDirWire` of the same vault. */
  async function move(wires: string[], toDirWire: string): Promise<boolean> {
    const s = sessionOf(toDirWire);
    if (!s || wires.length === 0) return false;
    if (wires.some((w) => shared.rootOf(w) !== s.root)) {
      host.toast(host.t('e2e.vault.cross'), true);
      return false;
    }
    touch(toDirWire);
    const toDir = relOf(s.root, toDirWire);
    return change(s, wires.map(baseName), async (ctx) => {
      for (const w of wires) {
        const from = relOf(s.root, w);
        const to = joinVaultPath(toDir, splitVaultPath(from).name);
        if (to !== from) await ctx.apply({ op: 'move', from, to });
      }
    });
  }

  /**
   * Copy entries to `toDirWire` of the same vault: read and written again,
   * every file under a new content id (the nonce rule). A taken name gets a
   * number.
   */
  async function copy(wires: string[], toDirWire: string): Promise<boolean> {
    const s = sessionOf(toDirWire);
    if (!s || wires.length === 0) return false;
    if (wires.some((w) => shared.rootOf(w) !== s.root)) {
      host.toast(host.t('e2e.vault.cross'), true);
      return false;
    }
    const sources = wires.map((w) => s.state.tree.get(relOf(s.root, w))).filter((n): n is VaultNode => !!n);
    const all = canonicalOrder(s.state.tree);
    let more = 0;
    for (const n of sources) more += 1 + (n.kind === VAULT_ENTRY_FOLDER ? all.filter((e) => e.path.startsWith(n.path + '/')).length : 0);
    if (!roomFor(s, more)) return false;
    touch(toDirWire);
    const toDir = relOf(s.root, toDirWire);
    const fr = fetchRangeOf(s);
    const keys = s.keys;
    return change(s, sources.map((n) => n.name), async (ctx) => {
      for (const src of sources) {
        const top = joinVaultPath(toDir, freeName(ctx.tree(), toDir, src.name));
        if (src.kind === VAULT_ENTRY_FOLDER && (top === src.path || top.startsWith(src.path + '/'))) throw new VaultWriteError('into_itself', top);
        const list = src.kind === VAULT_ENTRY_FOLDER ? [src, ...all.filter((e) => e.path.startsWith(src.path + '/'))] : [src];
        for (const e of list) {
          const path = top + e.path.slice(src.path.length);
          if (e.kind === VAULT_ENTRY_FOLDER) await ctx.apply({ op: 'mkdir', path, mtime: e.mtime });
          else await ctx.apply({ op: 'write', path, mtime: e.mtime, content: { size: e.size, stream: () => fileStream(keys, e, fr, ctx.signal) } });
          await ctx.checkpoint();
        }
      }
    });
  }

  /** Delete entries (a folder with everything under it). For good: a vault keeps no trash. */
  async function remove(wires: string[]): Promise<boolean> {
    if (wires.length === 0) return false;
    const s = sessionOf(wires[0]);
    if (!s) return false;
    touch(wires[0]);
    const paths = wires.map((w) => relOf(s.root, w)).filter(Boolean);
    // A child of a folder also being deleted goes with it.
    const top = paths.filter((p) => !paths.some((q) => q !== p && p.startsWith(q + '/')));
    return change(s, wires.map(baseName), async (ctx) => {
      for (const p of top) await ctx.apply({ op: 'delete', path: p });
    });
  }

  /** The strip's "Take over": end the other session's lock, then take it. */
  async function takeOver(root: string): Promise<boolean> {
    const s = sessions.get(root);
    if (!s || s.closed) return false;
    try {
      await api.breakLock(root);
    } catch (err) {
      say(s, err);
      return false;
    }
    s.strip.holder = null;
    try {
      await enqueue(s, () => ensureLock(s));
    } catch (err) {
      say(s, err);
      return false;
    }
    return true;
  }

  /** "Continue from this state": commit latest + 1 with the tree on screen. */
  async function continueFromShown(root: string): Promise<boolean> {
    const s = sessions.get(root);
    if (!s || s.closed || s.strip.readOnly !== 'damaged') return false;
    let latest = s.state.generation;
    try {
      latest = (await api.state(root)).generation;
    } catch {
      /* the server's answer to the commit decides */
    }
    s.strip.readOnly = '';
    s.state = { ...s.state, generation: latest };
    const ok = await change(s, [], async () => undefined);
    if (!ok) s.strip.readOnly = 'damaged';
    else s.strip.shownAt = 0;
    return ok;
  }

  /** Clear what the strip says about the last lost lock. */
  function dismiss(root: string): void {
    const s = sessions.get(root);
    if (!s) return;
    s.strip.lost = '';
    s.strip.lostTo = '';
    s.strip.unsaved = [];
    s.strip.error = '';
  }

  /** The person's idle time (minutes, 1-10), kept on the server. */
  const idlePrefs = {
    get: async (): Promise<number> => (await api.getPrefs()).idle_minutes,
    set: async (minutes: number): Promise<number> => (await api.putPrefs(minutes)).idle_minutes,
  };

  /** Make a vault on the server (`POST /create`); the caller made it in memory. */
  async function create(path: string, marker: E2eMarker, index: Uint8Array): Promise<void> {
    await api.create(path, marker, index);
  }

  /** The level the server gave this account at the vault folder. */
  function permOf(root: string): string | undefined {
    return sessions.get(root)?.perm;
  }

  /** The vault folders open in this tab (useE2eVault's rootOf reads them). */
  function openRoots(): string[] {
    return [...sessions.keys()];
  }

  return {
    openRoots,
    isOpen,
    permOf,
    node,
    rows,
    search,
    open,
    close,
    closeAll,
    lock,
    touch,
    onPageHide,
    onVisible,
    onEvent,
    readBytes,
    readUrl,
    download,
    upload,
    mkdir,
    rename,
    move,
    copy,
    remove,
    takeOver,
    continueFromShown,
    dismiss,
    create,
    idlePrefs,
  };
}

/** A vault this session may read and not write. */
export class VaultReadOnlyError extends Error {
  constructor(public readonly why: VaultReadOnly) {
    super(`vault: read-only (${why})`);
    this.name = 'VaultReadOnlyError';
  }
}

export type VaultEngine = ReturnType<typeof createVaultEngine>;
