/**
 * useE2eVault — the explorer's vault mode (wiring:e2 vault;
 * docs/E2E-VAULT-FORMAT.md → "Clients", "The write lock", "The idle lock").
 *
 * A vault is one folder whose tree lives in an encrypted index: the server
 * stores only a key file, index files and packs of one size. Unlocked, the
 * explorer lists the vault from the index, read-only; the first action that
 * writes takes the server's write lock (one writer at a time), and the lock
 * ends by itself when its holder stays idle (the person's own setting, 1-10
 * minutes). Fifteen minutes after that - or after opening, for a vault that
 * never wrote - with nothing done in it at all, the vault locks itself: its
 * keys leave memory and the password is asked again.
 *
 * ONE composable for every surface that runs the explorer (the web app, the
 * desktop app, the embeds): nothing here is written for one of them. The
 * explorer hands in its HTTP client, its key ring's lock and its words.
 *
 * ⚠ This module is the light half, in the explorer's main chunk: the vault
 * folders this tab knows, the strips, and a loader. The work - the sessions,
 * the index, the write lock, every change - is the engine
 * (e2eVaultEngine.ts), loaded with `import()` on the first call that needs it
 * (opening or making a vault), and the rest of lib/e2evault comes with it.
 * Nothing is open before that, so until then the questions the explorer asks
 * on every listing (`rootOf`, `isOpen`, `rows`) are answered here. Only
 * `import type` from the engine and from lib/e2evault in this file
 * (web/tests/quality/vaultLazy.test.ts).
 *
 * Paths: a vault entry's wire path is the vault folder's wire path, `/`, and
 * its path in the tree (`docs://Kasa/Belgeler/not.txt`). Those paths exist
 * only in this tab; the server never sees them.
 */
import { reactive, ref, type Ref } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { VaultClientKind, VaultHttp } from '../lib/e2evault/api';
import type { Clock, LockEnd } from '../lib/e2evault/lock';
import type { VaultIndexState } from '../lib/e2evault/vindex';
import type { RandomSource } from '../lib/e2evault/writer';
import type { VaultEngine } from './e2eVaultEngine';

/** Why a vault is read-only for this session, beyond "nobody took the lock". */
export type VaultReadOnly = '' | 'damaged' | 'rollback' | 'newer' | 'unreadable';

/** What the vault strip draws (components/E2eVaultStrip.vue). Reactive. */
export interface VaultStripState {
  root: string;
  /** The vault folder's own name. */
  name: string;
  /** The generation on screen. */
  generation: number;
  /** 'write': this session holds the write lock. */
  mode: 'read' | 'write';
  /** A change is running. */
  busy: boolean;
  /** Plaintext bytes of the running change: done / total. */
  progress: { done: number; total: number } | null;
  /** Somebody else holds the write lock. */
  holder: { name: string; client: string; label?: string; since: string } | null;
  readOnly: VaultReadOnly;
  /** The commit time of the generation shown, when an older one is shown. */
  shownAt: number;
  /** The vault against its limits. */
  limit: 'ok' | 'warn' | 'full';
  entries: number;
  /** Why the write lock ended last, until the next change. */
  lost: LockEnd | '';
  /** The idle time that ended it, in minutes (for "you did nothing for N minutes"). */
  lostMinutes: number;
  /** Who took over writing, or broke the lock, when the server said (`taken`, `broken`). */
  lostTo: string;
  /** What was not saved when the lock was lost. */
  unsaved: string[];
  /** When the write lock ends for idleness (ms), or 0. */
  idleDeadline: number;
  /** The person's idle time, in minutes, while writing. */
  idleMinutes: number;
  /** When the vault locks itself (ms), or 0. */
  lockDeadline: number;
  /** The last thing that went wrong, said. */
  error: string;
}

export interface VaultHost {
  http: VaultHttp;
  /** Headers for the keepalive release (synchronous; the page is closing). */
  headersNow(): Record<string, string>;
  t(key: string, vars?: Record<string, string | number>): string;
  toast(message: string, error?: boolean): void;
  emitError?(message: string, op: string): void;
  clientKind(): VaultClientKind;
  /** Drop the vault's keys from the explorer's key ring (and what hangs on them). */
  dropKeys(root: string): void;
  /** The vault locked itself (15 minutes idle) or was locked: show the lock screen. */
  onLocked(root: string, why: 'idle' | 'manual'): void;
  /** The tree, or the strip, changed: draw it again. */
  onChanged(root: string): void;
  clock?: Clock;
  random?: RandomSource;
  /** A count in the person's language ("212,400" / "212.400"). Default: digits. */
  formatCount?(n: number): string;
  /**
   * An upload's name is taken in the vault: may it go up as `suggested`
   * instead? The explorer asks with its "already there" dialog (the drafts'
   * one); false leaves that file out. Without it a taken name is numbered.
   * Nothing in a vault is overwritten: it keeps no earlier version.
   */
  askName?(q: { name: string; folder: string; suggested: string }): Promise<boolean>;
}

/** How `open` starts a session (the engine reads it). */
export interface VaultOpenOptions {
  /** The level the server gave this account at the vault folder. */
  perm?: string;
  /** The state to start from, nothing fetched (tests). */
  initial?: VaultIndexState;
  /** A vault this tab has just made: generation 1, the empty tree, nothing to fetch. */
  justMade?: boolean;
}

/**
 * What the shell and the engine (e2eVaultEngine.ts) share: the strips and the
 * counter the explorer watches, and the vault folders this tab knows. Made by
 * the shell, so the explorer can read them before the engine is loaded.
 */
export interface VaultShared {
  strips: Record<string, VaultStripState>;
  version: Ref<number>;
  /** The vault root `wire` is in (or is), among the open and the known ones. */
  rootOf(wire: string): string | null;
  /** A vault folder this browser has opened: remembered (see `known`). */
  remember(root: string): void;
}

/** The path inside the vault ('' = its root). */
export function vaultRelOf(root: string, wire: string): string {
  const w = String(wire ?? '').replace(/\/+$/, '');
  return w === root ? '' : w.slice(root.length + 1);
}

export function useE2eVault(host: VaultHost) {
  /**
   * Vault folders this browser has opened. A path BELOW one exists only in a
   * tab that holds the vault's keys, so a restored tab, a reload or a link
   * that names such a path is sent to the vault folder itself - and the
   * server never hears of the path below it. The folders' own paths are no
   * secret (the server stores them), which is why they may be remembered.
   */
  const known = new Set<string>(readKnown());
  /** Strips by root: what the vault strip draws. */
  const strips = reactive<Record<string, VaultStripState>>({});
  /** Bumped on every change a listing shows. */
  const version = ref(0);

  /** The engine, once loaded (the first vault opened or made in this tab). */
  let engine: VaultEngine | null = null;
  let loading: Promise<VaultEngine> | null = null;

  /** A vault folder this browser has opened, remembered (see `known`). */
  function remember(root: string): void {
    if (known.has(root)) return;
    known.add(root);
    writeKnown([...known]);
  }

  /** The folder at `root` is not a vault (any more): forget it. */
  function forget(root: string): void {
    if (!known.delete(root)) return;
    writeKnown([...known]);
  }

  /** The vault root `wire` is in (or is), among the open and the known ones. */
  function rootOf(wire: string): string | null {
    const w = String(wire ?? '').replace(/\/+$/, '');
    let best: string | null = null;
    for (const r of [...(engine?.openRoots() ?? []), ...known]) {
      if ((w === r || w.startsWith(r + '/')) && (!best || r.length > best.length)) best = r;
    }
    return best;
  }

  /** Every vault folder this tab knows: open here, or remembered. */
  function roots(): string[] {
    return [...new Set([...(engine?.openRoots() ?? []), ...known])];
  }

  const shared: VaultShared = { strips, version, rootOf, remember };

  /** The engine: loaded once, on the first call that needs it. */
  function load(): Promise<VaultEngine> {
    if (engine) return Promise.resolve(engine);
    if (!loading) {
      loading = import('./e2eVaultEngine').then(
        (m) => (engine = m.createVaultEngine(host, shared)),
        (err) => {
          // A failed download (offline, a deploy in between) is tried again
          // by the next call, not remembered.
          loading = null;
          throw err;
        },
      );
    }
    return loading;
  }

  /** Is this row inside a vault this tab knows? */
  function isVaultRow(n: FileNode | null | undefined): boolean {
    return !!n && typeof n.vault_root === 'string' && !!n.vault_root;
  }

  /** "Lock": the keys leave memory, the lock screen comes back. */
  function lock(root: string): void {
    if (engine) {
      engine.lock(root);
      return;
    }
    host.dropKeys(root);
    host.onLocked(root, 'manual');
  }

  /** The page is closing: release with keepalive, best effort. */
  function onPageHide(): void {
    engine?.onPageHide();
  }

  // What needs no engine answers from the state above; nothing is open
  // before the engine is loaded, so the rest answers "nothing" until then.
  // Every call that changes or reads a vault loads it.
  return {
    version,
    strips,
    rootOf,
    roots,
    isVaultRow,
    forget,
    relOf: vaultRelOf,
    isOpen: (root: string | null | undefined): boolean => !!engine?.isOpen(root),
    permOf: (root: string): string | undefined => engine?.permOf(root),
    node: (...a: Parameters<VaultEngine['node']>) => engine?.node(...a) ?? null,
    rows: (...a: Parameters<VaultEngine['rows']>) => engine?.rows(...a) ?? null,
    search: (...a: Parameters<VaultEngine['search']>) => engine?.search(...a) ?? null,
    close: (...a: Parameters<VaultEngine['close']>): void => engine?.close(...a),
    closeAll: (...a: Parameters<VaultEngine['closeAll']>): void => engine?.closeAll(...a),
    touch: (...a: Parameters<VaultEngine['touch']>): void => engine?.touch(...a),
    onVisible: (): void => engine?.onVisible(),
    onEvent: (...a: Parameters<VaultEngine['onEvent']>): void => engine?.onEvent(...a),
    dismiss: (...a: Parameters<VaultEngine['dismiss']>): void => engine?.dismiss(...a),
    lock,
    onPageHide,
    open: async (...a: Parameters<VaultEngine['open']>) => (await load()).open(...a),
    create: async (...a: Parameters<VaultEngine['create']>) => (await load()).create(...a),
    readBytes: async (...a: Parameters<VaultEngine['readBytes']>) => (await load()).readBytes(...a),
    readUrl: async (...a: Parameters<VaultEngine['readUrl']>) => (await load()).readUrl(...a),
    download: async (...a: Parameters<VaultEngine['download']>) => (await load()).download(...a),
    upload: async (...a: Parameters<VaultEngine['upload']>) => (await load()).upload(...a),
    mkdir: async (...a: Parameters<VaultEngine['mkdir']>) => (await load()).mkdir(...a),
    rename: async (...a: Parameters<VaultEngine['rename']>) => (await load()).rename(...a),
    move: async (...a: Parameters<VaultEngine['move']>) => (await load()).move(...a),
    copy: async (...a: Parameters<VaultEngine['copy']>) => (await load()).copy(...a),
    remove: async (...a: Parameters<VaultEngine['remove']>) => (await load()).remove(...a),
    takeOver: async (...a: Parameters<VaultEngine['takeOver']>) => (await load()).takeOver(...a),
    continueFromShown: async (...a: Parameters<VaultEngine['continueFromShown']>) => (await load()).continueFromShown(...a),
    /** The person's idle time (minutes, 1-10), kept on the server. */
    idlePrefs: {
      get: async (): Promise<number> => (await load()).idlePrefs.get(),
      set: async (minutes: number): Promise<number> => (await load()).idlePrefs.set(minutes),
    },
  };
}

const KNOWN_KEY = 'filex.e2e.vaults';
const KNOWN_MAX = 200;

function readKnown(): string[] {
  try {
    const raw = typeof localStorage === 'undefined' ? null : localStorage.getItem(KNOWN_KEY);
    const list = raw ? (JSON.parse(raw) as unknown) : [];
    return Array.isArray(list) ? list.filter((x): x is string => typeof x === 'string' && x.includes('://')).slice(-KNOWN_MAX) : [];
  } catch {
    return [];
  }
}

function writeKnown(list: string[]): void {
  try {
    if (typeof localStorage !== 'undefined') localStorage.setItem(KNOWN_KEY, JSON.stringify(list.slice(-KNOWN_MAX)));
  } catch {
    /* private mode, quota: this tab still knows them */
  }
}

export type E2eVault = ReturnType<typeof useE2eVault>;
