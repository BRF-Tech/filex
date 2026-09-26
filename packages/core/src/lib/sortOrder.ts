/**
 * sortOrder — THE listing sort: one key, one direction, one comparator, for
 * every surface that draws a folder's contents.
 *
 * ⚠⚠ Why this file exists at all. Before it, the sort lived as two private
 * refs inside `ListView.vue` and the grid had none, so "sorted by size" was a
 * fact about one component rather than about the listing: switching grid ⇄
 * list re-ordered the rows under the user, and any second control that wanted
 * to sort would have had to keep its own copy of the answer. That is the exact
 * shape of filex lesson #67 — the grid and the list each holding a private
 * copy of "folders first" and drifting apart — one level up.
 *
 * ⚠⚠ …and why it is no longer a module-level SINGLETON. A singleton answers
 * "what is the sort" for the whole bundle, which is the right answer only
 * while the bundle draws one listing. The split view draws two, side by side,
 * and the owner's ruling (2026-09-13) is that the second pane is the first
 * pane: it gets the same crumbs, the same filter row, the same view switcher
 * and the same sort control. Two sort CONTROLS over one piece of state is a
 * control that moves the listing the person is not looking at — measured the
 * same day: with the singleton in place, choosing "Size ↓" in the right pane
 * re-ordered the left one too.
 *
 * So the state is a STORE (`createSortStore`) and there is one per pane, while
 * everything that is genuinely one answer stays module-level:
 *
 *   · the RULES (`byActiveKey`, `compareNodes`, folders-first, the two click
 *     vocabularies) — written once, closed over each store;
 *   · the LOCALE the `type` key sorts in — one alphabet for the whole bundle;
 *   · the DEFAULT a folder nobody configured opens with — the person's, else
 *     the instance's (`lib/viewPrefs`). ⚠ Since v0.43 it is NOT "the last sort
 *     a person chose": a choice belongs to the folder it was made in, and
 *     nothing in this file writes the default (see the note where `persist`
 *     used to be).
 *
 * ⚠ Panes find their store by INJECTION (`provideSortStore` / `useSortStore`),
 * not by a prop threaded through every view: `ListView` and `FilterBar` are
 * two levels down and an embedder may mount either on its own. With no
 * provider — a bare `<ListView>` in a test, a host that renders one listing —
 * `useSortStore()` hands back the default store, which IS the old singleton,
 * so nothing that worked before changes.
 *
 * ⚠ Two RULES this file owns, so no caller has to remember them:
 *
 *   1. Folders before files, in every key and BOTH directions. `compareNodes`
 *      applies `byFoldersFirst` as the primary comparator and never multiplies
 *      it by the direction — multiplying would make "Name ↓" mean "files
 *      first", i.e. the arrow would silently regroup the listing instead of
 *      reversing it. The key only decides the order WITHIN each group.
 *   2. One vocabulary. A column header cycles (`chooseSortKey`: a new key
 *      arrives in its default direction, the active one flips) and a menu item
 *      jumps (`setSort`: a named destination never reverses under you). Both
 *      live here, so the two surfaces reach for the same words instead of each
 *      re-deriving what a click means.
 *
 * ⚠ The persisted shape is the one `ListView` already wrote (`filex.list-sort`,
 * `{key, dir}`), so a user who had sorted a column keeps that sort. The old
 * `key: null` — "leave the backend's order alone" — is NOT a key here; see
 * `ListingOrder` below for where that state went and why it could not be one.
 */
import { inject, provide, ref, type InjectionKey, type Ref } from 'vue';

import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import { messages } from '../locales';
import { typeLabelKey } from './fileIcons';
import { byFoldersFirst } from './listing';
/* ⚠ The DEFAULT a folder nobody configured opens with (the person's, else the
 * instance's). `viewPrefs` imports only TYPES from this file, so there is no
 * runtime cycle between the two. */
import { defaultFolderView, onViewPrefsApplied, viewPrefsReady } from './viewPrefs';

export type SortKey = 'name' | 'type' | 'modified' | 'size';
export type SortDir = 'asc' | 'desc';

/**
 * WHERE A LISTING'S ORDER CAME FROM — the third state the old `key: null`
 * used to express, back as a mode rather than as a fourth key.
 *
 *   `sort`      — a folder's contents. The key and the direction below decide
 *                 the order, which is what every control in the UI drives.
 *   `relevance` — a RANKED answer. The server decided the order and it is the
 *                 whole value of the reply; re-applying a key here buries the
 *                 row the person came for.
 *
 * ⚠⚠ Why a mode and not a key. A key is a preference: it is persisted, it is
 * named by a button, and the user picks it. "Relevance" is none of those — it
 * is a FACT about the rows in hand, it must not outlive them, and there is no
 * meaningful "relevance descending". Making it a fifth member of `SortKey`
 * would have persisted it into the next folder (where nothing is ranked) and
 * put a word in the Sort-by menu that does nothing in nine listings out of
 * ten.
 *
 * ⚠⚠ …and why it is an ARGUMENT and not a field on the store. The store is a
 * pane's preference; this is a property of the ROWS a pane happens to be
 * holding, and one pane can be showing a search while the other shows a
 * folder. Same pane, two navigations, two answers — the caller holding the
 * rows is the only one that knows which it has.
 *
 * Measured, 2026-09-13 (qldemo, query `s`, Name ↑): the server ranked
 * `Documents/server.ts` first and the list drew it fifteenth of seventeen,
 * behind every alphabetically earlier row. The owner's ruling that day:
 * ordinary search and ⌘K stay in relevance order.
 */
export type ListingOrder = 'sort' | 'relevance';

/** The four, in the order the menu draws them. One array, so the menu and
 *  `isSortKey` cannot disagree about how many there are. */
export const SORT_KEYS: readonly SortKey[] = ['name', 'type', 'modified', 'size'] as const;

const SORT_LS_KEY = 'filex.list-sort';

/**
 * The order the listing arrives in when nobody has chosen one.
 *
 * ⚠ `name` / ascending, and NOT the reference build's `modified` / descending,
 * deliberately: the backend answers a listing name-ascending already, so this
 * default moves nothing on screen for an existing user — and our list view
 * draws date group headings whenever the key is `modified`, which the
 * reference has no equivalent of. Making `modified` the default would put a
 * heading on the first paint of every folder that nobody asked for. Changing
 * it is this one constant; the consequence is that heading.
 */
const DEFAULT_KEY: SortKey = 'name';

/** Dates read newest-first; everything else reads A→Z / smallest-first. */
export function defaultSortDir(key: SortKey): SortDir {
  return key === 'modified' ? 'desc' : 'asc';
}

function isSortKey(v: unknown): v is SortKey {
  return typeof v === 'string' && (SORT_KEYS as readonly string[]).includes(v);
}

function readStored(): { key: SortKey; dir: SortDir } {
  const fallback = { key: DEFAULT_KEY, dir: defaultSortDir(DEFAULT_KEY) };
  if (typeof localStorage === 'undefined') return fallback;
  try {
    const raw = localStorage.getItem(SORT_LS_KEY);
    if (!raw) return fallback;
    const p = JSON.parse(raw) as { key?: unknown; dir?: unknown };
    const key = isSortKey(p.key) ? p.key : DEFAULT_KEY;
    const dir = p.dir === 'asc' || p.dir === 'desc' ? p.dir : defaultSortDir(key);
    return { key, dir };
  } catch {
    return fallback; // private mode / bad JSON
  }
}

/**
 * The active locale, for the one comparison that needs words: `type` sorts by
 * the label the Type column PRINTS, so "Image" and "Görsel" have to fall in
 * their own alphabet's order.
 *
 * ⚠ Module-level and NOT per store, and that is deliberate even now that the
 * key and the direction are per pane: two panes of one window are read by one
 * person in one language. If each store carried its own resolver, the pane
 * whose host forgot to set it would sort by a different alphabet than the pane
 * beside it — the drift this file exists to prevent, in the one field where it
 * would be hardest to see.
 */
const localeRef = ref<LocaleCode>('en');

export function setSortLocale(code: LocaleCode): void {
  localeRef.value = code;
}

// ── the comparator ────────────────────────────────────────────────────

function modifiedMs(n: FileNode): number | null {
  const v = n.last_modified;
  if (!v) return null;
  return v * (v < 1e12 ? 1000 : 1);
}

/**
 * Two names, in the order the Name column puts them: numbers as numbers
 * ("Disk 2" before "Disk 10"), case and accents ignored.
 *
 * ⚠ Exported because it is not only the listing's rule: the navigation panel's
 * "Sort by name" (lib/storageOrder) orders the storages by it too, so the
 * panel and the drives listing sorted by name agree on which drive comes first.
 */
export function compareNames(a: string, b: string): number {
  return (a || '').localeCompare(b || '', undefined, {
    numeric: true,
    sensitivity: 'base',
  });
}

function nameCompare(a: FileNode, b: FileNode): number {
  return compareNames(a.basename, b.basename);
}

/** The word the Type column prints for this row — the thing a user sorting by
 *  "Type" is looking at. Unmapped kinds fall back to the uppercased extension,
 *  exactly as `typeLabelFor` does, so no row sorts as an empty string. */
function typeLabel(n: FileNode): string {
  const key = typeLabelKey(n);
  if (key) {
    const cat = messages[localeRef.value] ?? messages.en;
    return cat[key] ?? key;
  }
  const ext = (n.extension || '').trim();
  return ext ? ext.toUpperCase() : '';
}

/** The active key's own order, direction applied. Folders-first is NOT here —
 *  it is the primary comparator in `compareNodes` and must not be reversed.
 *
 *  ⚠ Written ONCE and closed over each store's refs, rather than copied into
 *  a store factory: the rule is the same in every pane and a per-pane copy of
 *  it is how the grid and the list drifted in the first place. */
function byActiveKey(key: SortKey, dirWord: SortDir, a: FileNode, b: FileNode): number {
  const dir = dirWord === 'asc' ? 1 : -1;
  switch (key) {
    case 'name':
      return dir * nameCompare(a, b);
    case 'size':
      return dir * ((a.size ?? -1) - (b.size ?? -1));
    case 'type': {
      const c = typeLabel(a).localeCompare(typeLabel(b), undefined, { sensitivity: 'base' });
      // ⚠ The tiebreak is name-ASCENDING in both directions, on purpose: every
      // image in a folder shares one label, so without it a Type sort leaves
      // whole runs in whatever order the backend happened to answer, and
      // "Type ↓" would scramble them again rather than reverse the groups.
      return c !== 0 ? dir * c : nameCompare(a, b);
    }
    default: {
      const am = modifiedMs(a);
      const bm = modifiedMs(b);
      // Undated rows go last in BOTH directions, so date groups stay clean.
      if (am == null && bm == null) return 0;
      if (am == null) return 1;
      if (bm == null) return -1;
      return dir * (am - bm);
    }
  }
}

// ── the store ─────────────────────────────────────────────────────────

/**
 * One pane's sort. Everything a surface needs to READ the order, to CHANGE it
 * from either of the two control vocabularies, and to sort rows by it.
 *
 * ⚠ `key` and `dir` are exposed as refs so a `computed` that reads them
 * subscribes to the next change — the property the module-level singleton had
 * and the reason every listing re-sorted itself for free.
 */
export interface SortStore {
  readonly key: Ref<SortKey>;
  readonly dir: Ref<SortDir>;
  /** Set both halves outright. `dir` omitted → the key's own default. */
  setSort(key: SortKey, dir?: SortDir): void;
  /** Restore a remembered sort WITHOUT recording it as a fresh choice. */
  applySort(key: SortKey, dir: SortDir): void;
  /** Same order, other way round. The direction button's whole job. */
  toggleSortDir(): void;
  /** A column header click — see `chooseSortKey` below. */
  chooseSortKey(key: SortKey): void;
  /** Folders first, then the active key. Subscribes the reader. */
  compareNodes(a: FileNode, b: FileNode): number;
  /** THE comparator for a listing, given where its order came from. */
  compareInOrder(order: ListingOrder): (a: FileNode, b: FileNode) => number;
  /** The listing, in its order. A COPY. */
  sortListing(files: FileNode[], order?: ListingOrder): FileNode[];
}

/*
 * A person CHOSE a sort — and that is ALL it is. There is no `persist` here
 * any more.
 *
 * ⚠⚠ This used to write the choice as the GLOBAL DEFAULT (on the account and
 * into `filex.list-sort`), under the rule "your last choice becomes the
 * default for every folder you have never set up". That rule was the leak the
 * owner reported on 2026-09-21 — "Explore içindeki değişikliklerimiz o klasör
 * özelinde olmalı; tüm klasörlerde görünüm değişikliği geçerli oluyor": sort
 * folder A by size and every untouched folder came up sorted by size. A
 * choice now belongs to the folder it was made in, and the explorer records
 * it there (FileExplorer → `rememberFolder`); the default is set on purpose,
 * in the person's settings, and nothing in this file writes it.
 *
 * ⚠ `filex.list-sort` is still READ (`readStored`) — for the first paint only,
 * as the cache of the resolved default that `lib/viewPrefs` keeps. Writing a
 * click into it would paint the next page load in the last folder's sort
 * before the document corrected it: the same leak, for half a second.
 */

/**
 * Every store this module has handed out.
 *
 * ⚠ Needed so a document arriving from the SERVER can reach the panes that
 * are already on screen — the main one, the split one, an embed's. Without
 * it the account's answer would only apply to panes created after it landed,
 * which is "it works if you navigate first".
 */
const liveStores = new Set<SortStore>();

/**
 * The default each store was last put on — its creation seed, then whatever
 * the account's answer moved it to. "Still following the default" means the
 * store holds exactly this.
 *
 * ⚠ Per store, and NOT a fresh read of the first-paint cache: by the time the
 * document is announced, `lib/viewPrefs` has already rewritten that cache with
 * the NEW default, so a comparison against it would see every store as
 * "somebody chose something" and move none of them.
 */
const followed = new WeakMap<SortStore, { key: SortKey; dir: SortDir }>();

/**
 * A fresh sort store, seeded from the DEFAULT (the first-paint cache of it
 * until the account's answer lands).
 *
 * ⚠ A choice made in one store moves only that store's refs: the split pane
 * sorting by Size leaves the main pane on Name ↑ (measured 2026-09-13), and
 * since v0.43 it no longer changes what any other folder opens with either.
 */
export function createSortStore(): SortStore {
  const stored = defaultSort();
  const key = ref<SortKey>(stored.key);
  const dir = ref<SortDir>(stored.dir);

  function setSort(k: SortKey, d?: SortDir): void {
    key.value = k;
    dir.value = d ?? defaultSortDir(k);
  }

  /**
   * tablo:t1 — RESTORE a sort (a folder's memory, or the default) without
   * recording it as a fresh choice. The explorer's recorder tells the two
   * apart by comparing against what it last applied, so a restore can never
   * write itself into the folder it was restored into.
   */
  function applySort(k: SortKey, d: SortDir): void {
    key.value = k;
    dir.value = d;
  }

  function toggleSortDir(): void {
    dir.value = dir.value === 'asc' ? 'desc' : 'asc';
  }

  /**
   * A COLUMN HEADER click: a new key arrives in its own default direction, the
   * key already active flips.
   *
   * ⚠ Not what the Sort-by menu calls — see `setSort`. A header is a toggle
   * (click Name twice and you expect Z→A); a menu item is a destination (pick
   * Name twice and you expect Name, still). Both meanings are written here so
   * neither surface has to invent one.
   */
  function chooseSortKey(k: SortKey): void {
    if (key.value === k) toggleSortDir();
    else setSort(k);
  }

  function compareNodesFn(a: FileNode, b: FileNode): number {
    return byFoldersFirst(a, b) || byActiveKey(key.value, dir.value, a, b);
  }

  function compareInOrderFn(order: ListingOrder): (a: FileNode, b: FileNode) => number {
    return order === 'relevance' ? byFoldersFirst : compareNodesFn;
  }

  const store: SortStore = {
    key,
    dir,
    setSort,
    applySort,
    toggleSortDir,
    chooseSortKey,
    compareNodes: compareNodesFn,
    compareInOrder: compareInOrderFn,
    sortListing: (files: FileNode[], order: ListingOrder = 'sort') =>
      [...(files || [])].sort(compareInOrderFn(order)),
  };
  liveStores.add(store);
  followed.set(store, { ...stored });
  return store;
}

/**
 * The store a surface gets when nobody provided one: a bare `<ListView>` in a
 * test, an embedder mounting one listing, and the explorer's own MAIN pane —
 * which keeps using it so that everything already reading the module-level
 * functions below (per-folder view memory, `displayFiles`) is talking about
 * the same state the main pane's controls drive.
 */
const defaultStore = createSortStore();

export const SORT_STORE_KEY: InjectionKey<SortStore> = Symbol('fe-sort-store');

/** Hand this subtree its own sort. Called by the pane component, once. */
export function provideSortStore(store: SortStore): void {
  provide(SORT_STORE_KEY, store);
}

/**
 * THE store for the surface calling it.
 *
 * ⚠ Must be called during `setup()` (it injects). Surfaces hold the result and
 * read `.key` / `.dir` inside their computeds, which is what subscribes them.
 */
export function useSortStore(): SortStore {
  return inject(SORT_STORE_KEY, defaultStore);
}

// ── the default store's API, as free functions ────────────────────────
//
// Everything below is the module-level API this file shipped with, now a thin
// pass-through to the default store. Kept — not deprecated — because the sort
// is genuinely one answer for a host that draws ONE listing, which is every
// embed except the split view, and because the explorer's per-folder view
// memory reads and writes exactly this state for its main pane.

export function activeSortKey(): SortKey {
  return defaultStore.key.value;
}

export function activeSortDir(): SortDir {
  return defaultStore.dir.value;
}

export function setSort(key: SortKey, dir?: SortDir): void {
  defaultStore.setSort(key, dir);
}

export function applySort(key: SortKey, dir: SortDir): void {
  defaultStore.applySort(key, dir);
}

/**
 * THE DEFAULT SORT — what a folder nobody has configured opens in: the
 * person's default, else the instance's, else this browser's cached copy of
 * it (until the document lands), else filex's own (Name ↑).
 *
 * ⚠ Once the document has landed the cache is NOT consulted: an account with
 * no default of its own must get filex's default, not whatever the cache held
 * from the last person or the last release.
 */
export function defaultSort(): { key: SortKey; dir: SortDir } {
  const d = defaultFolderView();
  if (d.k) return { key: d.k, dir: d.d ?? defaultSortDir(d.k) };
  if (viewPrefsReady()) return { key: DEFAULT_KEY, dir: defaultSortDir(DEFAULT_KEY) };
  return readStored();
}

/** ⚠ DEPRECATED name for `defaultSort` — kept so an embedder importing it
 *  keeps compiling. It is no longer "the last sort a person chose". */
export const globalSort = defaultSort;

/**
 * The account's document landed (or another browser's newer one did): move
 * every store that is still following the DEFAULT onto the resolved one.
 *
 * ⚠⚠ "Still following" is the whole subtlety. A store whose sort was restored
 * from a folder's own memory, or chosen in this tab a second ago, must not be
 * yanked — so the move happens only where the store still holds what this
 * browser's cache said, i.e. where nobody has chosen anything since the page
 * opened. (The explorer's main pane is re-applied by the explorer itself,
 * folder memory included; this reaches the other stores.)
 */
onViewPrefsApplied(() => {
  const want = defaultSort();
  for (const store of liveStores) {
    const was = followed.get(store);
    if (!was || store.key.value !== was.key || store.dir.value !== was.dir) continue;
    store.applySort(want.key, want.dir);
    followed.set(store, { ...want });
  }
});

export function toggleSortDir(): void {
  defaultStore.toggleSortDir();
}

export function chooseSortKey(key: SortKey): void {
  defaultStore.chooseSortKey(key);
}

/**
 * The whole rule, in order: folders before files, then the active key.
 *
 * Reading this subscribes the caller to the sort state, so a `computed` built
 * on it re-runs when the key or the direction changes.
 */
export function compareNodes(a: FileNode, b: FileNode): number {
  return defaultStore.compareNodes(a, b);
}

/**
 * THE comparator for a listing, given where its order came from. Every
 * surface that draws rows asks this one question; none of them re-derives the
 * answer, which is the whole reason this module exists.
 *
 * ⚠⚠ `relevance` is `byFoldersFirst` and NOTHING ELSE — deliberately the
 * exact function `GridView` already sorts by, so the grid needs no knowledge
 * of relevance at all and cannot drift from the list. `web/tests/lib/
 * searchRelevanceOrder.test.ts` asserts that identity; if this ever returns
 * something else, that test goes red and names GridView as the file that has
 * to change in the same commit. Teaching one view about relevance and leaving
 * the other sorting is filex lesson #67 one level up.
 *
 * ⚠ Folders still come FIRST in a ranked set, and that is a decision, not an
 * oversight. Folders-first is not a sort here, it is a GROUPING: the grid
 * draws it as two labelled sections ("Folders" / "Files"), so a result set
 * that interleaved them would make the grid's own headings false. The ranking
 * is kept in full INSIDE each group — `Array#sort` is stable and this
 * comparator returns 0 for any two rows of the same kind — which is as much
 * relevance as a surface that groups folders can honestly offer. The cost is
 * visible and bounded: a folder that ranked seventh is drawn above the file
 * that ranked first. The alternative costs the grid its sections.
 */
export function compareInOrder(order: ListingOrder): (a: FileNode, b: FileNode) => number {
  return defaultStore.compareInOrder(order);
}

/**
 * The listing, in its order. A COPY — the caller's array is usually a prop or
 * a computed's source and sorting it in place mutates somebody else's state.
 *
 * `Array#sort` is stable, so rows the comparator calls equal keep the order
 * they arrived in — which is precisely what carries the server's ranking
 * through `relevance` mode.
 *
 * ⚠ `order` defaults to `'sort'`: an omitted argument is how every caller
 * holding an ordinary folder listing asks for the active key, and that is the
 * overwhelming majority of them.
 */
export function sortListing(files: FileNode[], order: ListingOrder = 'sort'): FileNode[] {
  return defaultStore.sortListing(files, order);
}
