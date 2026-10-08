// menuAnswers: the server's answers a row's right-click menu depends on, kept
// per person and per storage so the menu opens on them at once (#196).
//
// Some rows of the explorer's menu exist only where the server says so: which
// of the per-folder permissions are held at a path (`POST /api/files/manager
// ?action=allowed`, question `p`) and may this person start encrypting there
// (`POST /api/files/e2e/allowed`, questions `e:file`, `e:folder`,
// `e:new_folder`). The first round of #196 made the menu wait for them (up to
// 400 ms) so no row moved under the pointer. The maintainers' rule since
// (2026-10-08): a menu must not wait on the network. So the answers are asked
// BEFORE the menu is opened and remembered:
//
//   - When a folder is listed, every row's questions go out in one batched
//     request per question (FileExplorer prefetchMenuAnswers), together with
//     the rows remembered around it - the storage's root and its first level,
//     the parent's level and the level below (`around`). That is the HOT tier:
//     asked again whenever it is stale. Everything else is COLD: shown from
//     here as it was last answered, asked again when it becomes hot.
//   - "Stale" is an era, not only an age: the server sends `access.changed` on
//     the live socket when a grant, a role, a group, a rule, the encryption
//     policy or an approval changed (internal/realtime/access.go), and a
//     socket that was down may have missed one; either starts a new era
//     (`stale`) and the hot tier is asked again. Without a live socket every
//     read of a folder is a new era. An answer also ages out after FRESH_MS.
//   - Remembered in localStorage, one key per person (origin, user, tenant,
//     root) and storage, versioned, bounded (rows per storage, storages per
//     person, bytes per key, age) and dropped least-recently-used first. Where
//     localStorage is not there or refuses (a private window, blocked site
//     data, a full quota) the answers live in memory for the page. A sign-out
//     forgets every key (lib/prefs forgetPersonalPrefs → clearMenuAnswers).
//
// ⚠ An answer here only SHAPES a menu. Every action is decided by the server
// again, whatever the menu offered; a remembered answer that turned out wrong
// costs a refused click, never an action the person may not take.
// ⚠ Nothing below a vault's folder is ever asked or kept: the explorer asks
// about a vault's rows as the vault folder (vaultAskPath).
import { ref, type Ref } from 'vue';
import { registerPersonalForget } from './prefs';

/** `p`: the per-folder permissions held at a path; `e:<kind>`: may this
 *  person start encrypting there, for that kind. */
export type MenuQuestion = 'p' | 'e:file' | 'e:folder' | 'e:new_folder';
/** The two families: a family's answers are asked under one rule set (the
 *  account's per-folder permission list, the encryption policy) and dropped
 *  together when it changes (`sign`). */
export type MenuFamily = 'p' | 'e';

/** Every key this module writes starts with this. */
export const MENU_ANSWERS_PREFIX = 'filex.menuAnswers.v1|';
const VERSION = 1;
/** Remembered rows per storage; the least recently used go first. */
export const MENU_ANSWERS_MAX_ROWS = 1500;
/** Storages remembered per person; the least recently used go first. */
export const MENU_ANSWERS_MAX_STORAGES = 6;
/** Characters one storage's key may hold. */
export const MENU_ANSWERS_MAX_CHARS = 196_608;
/** A row older than this is not read back. */
export const MENU_ANSWERS_MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000;
/** An answer of this era older than this is asked again when it is hot. */
export const MENU_ANSWERS_FRESH_MS = 30_000;
const FLUSH_MS = 400;

const E2E_WORDS = ['allowed', 'request', 'denied'];
const QUESTIONS: readonly string[] = ['p', 'e:file', 'e:folder', 'e:new_folder'];

/** value, answered at (ms), era it was answered in ('' = an earlier page),
 *  persisted (1) or this page's only (0: a question that failed). */
type Row = [unknown, number, string, 0 | 1];

interface Bucket {
  sig: Partial<Record<MenuFamily, string>>;
  rows: Map<string, Row>;
  dirty: boolean;
}

export interface MenuAnswerStore {
  /** Bumped when remembered answers appear or go (attach, sign, stale,
   *  forget): a computed that reads `peek` re-runs. */
  readonly rev: Ref<number>;
  /** Whose answers these are: `scope` names the person (origin, user,
   *  tenant, root) or is null (kept in memory only). `sig` is the rule set
   *  each family is asked under. Answers asked before are kept when the
   *  person is the same one this store was used for. */
  attach(scope: string | null, sig: Record<MenuFamily, string>): void;
  /** A family's rule set changed: its answers are dropped. */
  sign(family: MenuFamily, sig: string): void;
  /** The remembered answer, fresh or not; undefined when never answered. */
  peek(q: MenuQuestion, path: string): unknown;
  /** Answered in this era and recently: asking again would be waste. */
  fresh(q: MenuQuestion, path: string): boolean;
  /** The server answered. `keep` false: shown for this page, not remembered
   *  (a question that failed and was answered by the fallback). */
  put(q: MenuQuestion, path: string, value: unknown, keep?: boolean): void;
  /** A new era: every answer is still shown, and asked again when hot. */
  stale(): void;
  /** The remembered questions around the folder `dir` (a wire path): the
   *  storage's root and its first level, `dir`'s parent's level, `dir`'s own
   *  level and the level below it, and those folders themselves. */
  around(dir: string): Array<{ q: MenuQuestion; path: string }>;
  /** Write what is waiting now (an unmount). */
  flush(): void;
  /** Drop everything this store holds, and stop writing (a sign-out). */
  forget(): void;
  /** The explorer is gone. */
  dispose(): void;
}

const live = new Set<MenuAnswerStore>();

/** The storage part of a wire path (`main://a/b` → `main`), or ''. */
function storageOf(path: string): string {
  const i = path.indexOf('://');
  return i > 0 ? path.slice(0, i) : '';
}

/** `main://a/b/` → `main://a/b`; `main:///` → `main://`. */
function normal(path: string): string {
  const i = path.indexOf('://');
  if (i < 0) return path;
  return path.slice(0, i + 3) + path.slice(i + 3).replace(/^\/+|\/+$/g, '');
}

/** The folder a normal wire path is in; null for a storage's root. */
function parentOf(path: string): string | null {
  const i = path.indexOf('://');
  if (i < 0) return null;
  const rest = path.slice(i + 3);
  if (!rest) return null;
  const j = rest.lastIndexOf('/');
  return path.slice(0, i + 3) + (j < 0 ? '' : rest.slice(0, j));
}

function familyOf(q: string): MenuFamily {
  return q === 'p' ? 'p' : 'e';
}

/** A remembered value is read back only in the shape its question answers. */
function validAnswer(q: string, v: unknown): boolean {
  if (q === 'p') return Array.isArray(v) && v.every((x) => typeof x === 'string');
  return typeof v === 'string' && E2E_WORDS.includes(v);
}

/** Two FNV-1a passes: the person's key without their address and id in the
 *  clear, short enough for a storage key. */
export function menuScopeId(scope: string): string {
  let a = 0x811c9dc5;
  let b = 0x01000193 ^ 0x5bd1e995;
  for (let i = 0; i < scope.length; i++) {
    const c = scope.charCodeAt(i);
    a = Math.imul(a ^ c, 0x01000193);
    b = Math.imul(b ^ c, 0x5bd1e995);
  }
  return (a >>> 0).toString(16).padStart(8, '0') + (b >>> 0).toString(16).padStart(8, '0');
}

/** localStorage when this page may use it, else null (memory only). */
function probeStorage(): Storage | null {
  try {
    const ls = globalThis.localStorage;
    if (!ls) return null;
    const k = `${MENU_ANSWERS_PREFIX}probe`;
    ls.setItem(k, '1');
    ls.removeItem(k);
    return ls;
  } catch {
    return null;
  }
}

export interface MenuAnswerStoreOptions {
  now?: () => number;
  /** Where to keep the answers; the page's localStorage when absent. Null
   *  keeps them in memory. A function so a test can hand one that throws. */
  storage?: () => Storage | null;
}

export function menuAnswerStore(opts: MenuAnswerStoreOptions = {}): MenuAnswerStore {
  const now = opts.now ?? (() => Date.now());
  const rev = ref(0);
  const buckets = new Map<string, Bucket>();
  const page = Math.random().toString(36).slice(2, 8);
  let era = 0;
  let epoch = `${page}.0`;
  let scope: string | null = null;
  let ls: Storage | null = null;
  let sig: Partial<Record<MenuFamily, string>> = {};
  let timer: ReturnType<typeof setTimeout> | null = null;
  let disposed = false;

  const indexKey = () => `${MENU_ANSWERS_PREFIX}${scope}`;
  const bucketKey = (storage: string) => `${MENU_ANSWERS_PREFIX}${scope}|${encodeURIComponent(storage)}`;

  /** Read one storage's remembered rows into b, under the rows b already has
   *  (those are this page's, and newer). */
  function load(storage: string, b: Bucket): void {
    if (!ls || !scope || !storage) return;
    let doc: { v?: unknown; sig?: Record<string, unknown>; r?: unknown } | null = null;
    try {
      doc = JSON.parse(ls.getItem(bucketKey(storage)) ?? 'null');
    } catch {
      doc = null;
    }
    if (!doc || doc.v !== VERSION || !Array.isArray(doc.r)) return;
    const t = now();
    const kept = new Map<string, Row>();
    for (const row of doc.r as unknown[]) {
      if (!Array.isArray(row)) continue;
      const [k, v, at] = row as [unknown, unknown, unknown];
      if (typeof k !== 'string' || typeof at !== 'number' || t - at > MENU_ANSWERS_MAX_AGE_MS) continue;
      const q = k.slice(0, k.indexOf('|'));
      if (!QUESTIONS.includes(q) || doc.sig?.[familyOf(q)] !== sig[familyOf(q)] || !validAnswer(q, v)) continue;
      if (!b.rows.has(k)) kept.set(k, [v, at, '', 1]);
    }
    // The remembered rows are older than this page's: they go first in the
    // least-recently-used order.
    for (const [k, r] of b.rows) kept.set(k, r);
    b.rows = kept;
  }

  function bucket(storage: string): Bucket {
    let b = buckets.get(storage);
    if (!b) {
      b = { sig: { ...sig }, rows: new Map(), dirty: false };
      buckets.set(storage, b);
      load(storage, b);
    }
    return b;
  }

  function schedule(): void {
    if (!ls || !scope || timer || disposed) return;
    timer = setTimeout(flushNow, FLUSH_MS);
  }

  /** One storage's rows, as small as they must be to fit. The older half goes
   *  until they do; a key that holds nothing is removed. */
  function write(storage: string, b: Bucket, t: number): void {
    if (!ls) return;
    let rows = [...b.rows].filter(([, r]) => r[3] === 1).map(([k, r]) => [k, r[0], r[1]]);
    while (rows.length > 0) {
      const text = JSON.stringify({ v: VERSION, at: t, sig: b.sig, r: rows });
      if (text.length <= MENU_ANSWERS_MAX_CHARS) {
        try {
          ls.setItem(bucketKey(storage), text);
          return;
        } catch {
          /* the quota: fewer rows */
        }
      }
      rows = rows.slice(Math.ceil(rows.length / 2));
    }
    try {
      ls.removeItem(bucketKey(storage));
    } catch {
      /* nothing to do */
    }
  }

  function readIndex(): Record<string, number> {
    try {
      const doc = JSON.parse(ls?.getItem(indexKey()) ?? 'null') as { v?: unknown; s?: unknown } | null;
      if (doc && doc.v === VERSION && doc.s && typeof doc.s === 'object') {
        const out: Record<string, number> = {};
        for (const [k, v] of Object.entries(doc.s as Record<string, unknown>)) if (typeof v === 'number') out[k] = v;
        return out;
      }
    } catch {
      /* unreadable: start again */
    }
    return {};
  }

  function flushNow(): void {
    if (timer) clearTimeout(timer);
    timer = null;
    if (!ls || !scope) return;
    const t = now();
    const index = readIndex();
    for (const [storage, b] of buckets) {
      if (!b.dirty) continue;
      b.dirty = false;
      if (!storage) continue;
      index[storage] = t;
      write(storage, b, t);
    }
    const order = Object.entries(index).sort((x, y) => y[1] - x[1]);
    for (const [storage] of order.slice(MENU_ANSWERS_MAX_STORAGES)) {
      delete index[storage];
      try {
        ls.removeItem(bucketKey(storage));
      } catch {
        /* nothing to do */
      }
    }
    try {
      ls.setItem(indexKey(), JSON.stringify({ v: VERSION, at: t, s: index }));
    } catch {
      /* full: the rows are written, the order is not */
    }
  }

  /** Remove every key of this module older than the age limit, whoever it
   *  belongs to (a person who never signed out on this browser). */
  function sweep(): void {
    if (!ls) return;
    const t = now();
    const old: string[] = [];
    try {
      for (let i = 0; i < ls.length; i++) {
        const k = ls.key(i);
        if (!k || !k.startsWith(MENU_ANSWERS_PREFIX)) continue;
        let at = 0;
        try {
          at = Number((JSON.parse(ls.getItem(k) ?? 'null') as { at?: unknown } | null)?.at) || 0;
        } catch {
          at = 0;
        }
        if (t - at > MENU_ANSWERS_MAX_AGE_MS) old.push(k);
      }
      for (const k of old) ls.removeItem(k);
    } catch {
      /* nothing to do */
    }
  }

  const store: MenuAnswerStore = {
    rev,
    attach(next, nextSig) {
      if (disposed) return;
      flushNow();
      const id = next ? menuScopeId(next) : null;
      // Somebody else's answers are never kept: a different person starts
      // from nothing. The same person (or nobody known yet) keeps what this
      // page already asked.
      if (scope !== null && id !== scope) buckets.clear();
      scope = id;
      ls = id ? (opts.storage ? opts.storage() : probeStorage()) : null;
      for (const f of ['p', 'e'] as const) if (sig[f] !== undefined && sig[f] !== nextSig[f]) store.sign(f, nextSig[f]);
      sig = { ...nextSig };
      if (ls) {
        sweep();
        for (const [storage, b] of buckets) {
          b.sig = { ...sig };
          load(storage, b);
          b.dirty = true;
        }
        schedule();
      }
      rev.value++;
    },
    sign(family, value) {
      if (sig[family] === value) return;
      sig = { ...sig, [family]: value };
      for (const b of buckets.values()) {
        for (const k of [...b.rows.keys()]) if (familyOf(k.slice(0, k.indexOf('|'))) === family) b.rows.delete(k);
        b.sig = { ...sig };
        b.dirty = true;
      }
      schedule();
      rev.value++;
    },
    peek(q, path) {
      void rev.value;
      return bucket(storageOf(path)).rows.get(`${q}|${path}`)?.[0];
    },
    fresh(q, path) {
      const r = bucket(storageOf(path)).rows.get(`${q}|${path}`);
      return !!r && r[2] === epoch && now() - r[1] < MENU_ANSWERS_FRESH_MS;
    },
    put(q, path, value, keep = true) {
      if (disposed) return;
      // ⚠ A path with no storage in it is answered too, in memory only (the
      // '' bucket is never written): an answer that could not be put would
      // never be fresh, and the explorer would ask it again on every render.
      const b = bucket(storageOf(path));
      const k = `${q}|${path}`;
      b.rows.delete(k);
      b.rows.set(k, [value, now(), epoch, keep ? 1 : 0]);
      while (b.rows.size > MENU_ANSWERS_MAX_ROWS) {
        const oldest = b.rows.keys().next().value;
        if (oldest === undefined) break;
        b.rows.delete(oldest);
      }
      if (keep) {
        b.dirty = true;
        schedule();
      }
    },
    stale() {
      era++;
      epoch = `${page}.${era}`;
      rev.value++;
    },
    around(dir) {
      const st = storageOf(dir);
      if (!st) return [];
      const here = normal(dir);
      const root = `${st}://`;
      const up = parentOf(here);
      const levels = new Set([root, here]);
      if (up !== null) levels.add(up);
      const out: Array<{ q: MenuQuestion; path: string }> = [];
      for (const k of bucket(st).rows.keys()) {
        const i = k.indexOf('|');
        const path = k.slice(i + 1);
        const at = normal(path);
        const p = parentOf(at);
        if (levels.has(at) || (p !== null && (levels.has(p) || parentOf(p) === here))) {
          out.push({ q: k.slice(0, i) as MenuQuestion, path });
        }
      }
      return out;
    },
    flush() {
      flushNow();
    },
    forget() {
      if (timer) clearTimeout(timer);
      timer = null;
      buckets.clear();
      scope = null;
      ls = null;
      rev.value++;
    },
    dispose() {
      flushNow();
      disposed = true;
      live.delete(store);
    },
  };
  live.add(store);
  return store;
}

/**
 * Forget every remembered answer on this browser: each open explorer's, and
 * every key this module wrote, whoever it belonged to. A sign-out runs it
 * (lib/prefs forgetPersonalPrefs); a host with a sign-out of its own that does
 * not go through there may call it.
 */
export function clearMenuAnswers(): void {
  for (const s of live) s.forget();
  try {
    const ls = globalThis.localStorage;
    if (!ls) return;
    const keys: string[] = [];
    for (let i = 0; i < ls.length; i++) {
      const k = ls.key(i);
      if (k && k.startsWith(MENU_ANSWERS_PREFIX)) keys.push(k);
    }
    for (const k of keys) ls.removeItem(k);
  } catch {
    /* blocked site data: nothing was written there either */
  }
}

registerPersonalForget(clearMenuAnswers);

// A page that is closed within a moment of an answer still remembers it.
if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
  window.addEventListener('pagehide', () => {
    for (const s of live) s.flush();
  });
}
