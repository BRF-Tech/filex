/**
 * storageOrder — the order a person put their storages in (GitHub #57:
 * "Sidebar Storage sorting or manual reordering").
 *
 * The navigation panel used to list the storages in the order the HOST hands
 * them over (the server's creation order in our own app). That order is
 * nobody's choice: a person with a "Work" drive they open fifty times a day
 * and an "Archive" they open once a year had no way to put the first one
 * first, and neither had the administrator who set both up.
 *
 * What this module decides, once, for every surface the explorer runs on
 * (the web app, the desktop app, fm.example.com and every embed — it is the SAME
 * component everywhere, so there is no per-surface branch here and must not
 * be one). Three layers, the most personal first:
 *
 *   1. the PERSON's own order, when they have made one (dragged a row, used
 *      the row menu, "Sort by name");
 *   2. else the ADMINISTRATOR's order — `sortOrder` on each storage (the
 *      server's `sort_order`, set on the admin Storages page; 1 = first).
 *      Storages the administrator has not placed come after the placed ones;
 *   3. else the host's order, unchanged. Nobody who never reorders, under an
 *      administrator who never reorders, sees anything move.
 *
 *   · A person's order wins for the storages it names. A storage it does NOT
 *     name — added since, or never visible to them when they ordered — keeps
 *     the position layers 2-3 give it, and the storages it names fill the
 *     other positions in the person's order. So a drive the administrator
 *     adds at the top appears at the top for everybody, and one added at the
 *     end appears at the end, without undoing anybody's arrangement. A name
 *     it holds that no longer exists is dropped from the screen without a
 *     word.
 *   · "Sort by name" is not a mode: it WRITES a name-sorted order, which the
 *     person can then keep adjusting by hand. One mechanism, not two that can
 *     disagree about which one is in charge. "Use default order" is the empty
 *     order — back to layers 2-3.
 *
 * WHERE IT LIVES. It is a PERSON's preference, so it is kept where the palette
 * is kept — the account document (`lib/prefs`, key `storageOrder`), which
 * follows them to every browser that signs in as them, with this browser's
 * `filex.storageOrder` as the first-paint mirror. An embed that never wires
 * the account document keeps it in the mirror alone, per browser. The
 * administrator's order is a column on the storage (migration 00060), sent
 * with every storage list.
 *
 * A storage is identified by its `uid` when the host sends one (a name can be
 * edited; a uid cannot) and by its name otherwise, and a saved entry matches
 * either — so an order saved before a host started sending uids still applies.
 */
import { computed, getCurrentInstance, onBeforeUnmount, ref, type ComputedRef, type Ref } from 'vue';
import {
  PREF_LS_KEYS,
  currentPrefs,
  flushPrefs,
  hasSession,
  localPref,
  onPrefs,
  prefsConfigured,
  prefsHydrated,
  savePref,
  setLocalPref,
} from './prefs';
import { compareNames } from './sortOrder';

/** What a storage has to carry to be ordered. `ExplorerConfig.storages` rows fit. */
export interface OrderableStorage {
  name: string;
  uid?: string;
  label?: string;
  /** The administrator's position (1 = first); null/absent = not placed. */
  sortOrder?: number | null;
}

/** The key a saved order holds for this storage: its uid, else its name. */
export function storageOrderKey(s: OrderableStorage): string {
  return s.uid || s.name;
}

/** The keys of a list, in its order — the form every gesture below answers in. */
function keysOf(list: readonly OrderableStorage[]): string[] {
  return list.map(storageOrderKey);
}

/**
 * The stored form → a list of keys. Anything that is not a JSON list of
 * strings reads as "no order": a preference is never worth an exception, and
 * a damaged value must fall back to the host's order, not to a blank panel.
 */
export function parseStorageOrder(raw: unknown): string[] {
  if (typeof raw !== 'string' || !raw) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (!Array.isArray(parsed)) return [];
  const out: string[] = [];
  const seen = new Set<string>();
  for (const k of parsed) {
    if (typeof k !== 'string' || !k || seen.has(k)) continue;
    seen.add(k);
    out.push(k);
  }
  return out;
}

/** A list of keys → the stored form. Empty is `''`: "never reordered". */
export function serializeStorageOrder(keys: readonly string[]): string {
  return keys.length ? JSON.stringify(keys) : '';
}

/** A number the list can be sorted on, or null. Anything else is "not placed". */
function placed(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

/**
 * Layers 2-3: the administrator's order over the host's. A STABLE sort, so
 * storages with no position, and storages that share one, keep the host's
 * relative order.
 */
export function defaultStorageOrder<T extends OrderableStorage>(storages: readonly T[]): T[] {
  return storages
    .map((s, i) => ({ s, i, p: placed(s.sortOrder) }))
    .sort((a, b) => {
      if (a.p === null || b.p === null) return a.p === b.p ? a.i - b.i : a.p === null ? 1 : -1;
      return a.p - b.p || a.i - b.i;
    })
    .map((x) => x.s);
}

/**
 * The storages in the order this person sees them: theirs over the
 * administrator's over the host's (the header has the rule).
 *
 * ⚠ The host's rows are returned as they came (same objects, every field
 * intact), only reordered. The person's order is applied by SLOT: the
 * positions the default order gives the storages the person named are
 * re-filled with those storages in the person's order; every other storage
 * stays exactly where the default order put it.
 */
export function orderStorages<T extends OrderableStorage>(storages: readonly T[], saved: readonly string[]): T[] {
  const base = defaultStorageOrder(storages);
  if (!saved.length || base.length < 2) return base;
  const rank = new Map<string, number>();
  saved.forEach((k, i) => {
    if (!rank.has(k)) rank.set(k, i);
  });
  const rankOf = (s: T): number | undefined => (s.uid ? rank.get(s.uid) : undefined) ?? rank.get(s.name);
  const named = base
    .filter((s) => rankOf(s) !== undefined)
    .sort((a, b) => (rankOf(a) ?? 0) - (rankOf(b) ?? 0));
  let next = 0;
  return base.map((s) => (rankOf(s) === undefined ? s : named[next++]));
}

/**
 * Move one storage `delta` rows (−1 = up, +1 = down). The whole new order, or
 * `null` when the move would go past either end — the menu greys the row out
 * for exactly that case, and a caller that asks anyway changes nothing.
 */
export function moveStorage(ordered: readonly OrderableStorage[], key: string, delta: number): string[] | null {
  const keys = keysOf(ordered);
  const from = keys.indexOf(key);
  const to = from + delta;
  if (from < 0 || delta === 0 || to < 0 || to >= keys.length) return null;
  keys.splice(from, 1);
  keys.splice(to, 0, key);
  return keys;
}

/**
 * A drag released over GAP `gap` — the gap BEFORE row `gap` among the rows on
 * screen, the dragged row still counted (`0` = above the first, `n` = below
 * the last). ⚠ Its own two gaps are not a drop: releasing there leaves the
 * order as it was, and `null` says so, so nothing is written for a drag that
 * went nowhere.
 */
export function dropStorageAt(ordered: readonly OrderableStorage[], key: string, gap: number): string[] | null {
  const keys = keysOf(ordered);
  const from = keys.indexOf(key);
  if (from < 0) return null;
  const g = Math.max(0, Math.min(keys.length, Math.trunc(gap)));
  if (g === from || g === from + 1) return null;
  keys.splice(from, 1);
  keys.splice(g > from ? g - 1 : g, 0, key);
  return keys;
}

/**
 * "Sort by name": the order the Name column would give the labels a person
 * READS (`label`, else the name) — `compareNames`, the listing's own rule, so
 * the panel and the drives listing sorted by name agree.
 */
export function sortStoragesByName(ordered: readonly OrderableStorage[]): string[] {
  return ordered
    .map((s, i) => ({ s, i }))
    .sort((a, b) => compareNames(a.s.label || a.s.name, b.s.label || b.s.name) || a.i - b.i)
    .map((x) => storageOrderKey(x.s));
}

/**
 * The order to STORE after the visible storages were reordered to `visibleKeys`.
 *
 * ⚠⚠ A saved entry this view cannot see is KEPT, right after the entry it
 * followed. What a view draws is not the whole list: a grant that comes and
 * goes, an administrator's explorer beside the RBAC-filtered one, a host that
 * hands an embed fewer drives. Storing only what is on screen would erase the
 * place of every drive that happened to be off it — and the person would find
 * their arrangement gone the next time it came back.
 */
export function mergeStorageOrder(
  visibleKeys: readonly string[],
  visible: readonly OrderableStorage[],
  saved: readonly string[],
): string[] {
  const result = [...visibleKeys];
  if (!saved.length) return result;
  /* Every identity a visible storage answers to (name AND uid), mapped to the
   * key it is stored under now. */
  const keyOf = new Map<string, string>();
  for (const s of visible) {
    keyOf.set(s.name, storageOrderKey(s));
    if (s.uid) keyOf.set(s.uid, storageOrderKey(s));
  }
  let anchor: string | null = null;
  for (const k of saved) {
    const visibleKey = keyOf.get(k);
    if (visibleKey !== undefined) {
      anchor = visibleKey;
      continue;
    }
    if (result.includes(k)) continue;
    const at = anchor === null ? 0 : result.indexOf(anchor) + 1;
    result.splice(at, 0, k);
    anchor = k;
  }
  return result;
}

/* ── the person's order, shared by every explorer on the page ────────────── */

const saved: Ref<string[]> = ref([]);
/** The account write queued until the account's document arrives. */
let queuedWrite: (() => void) | null = null;
let queuedRaw = '';

/**
 * This browser's copy. ⚠ Behind `hasSession()`, like the palette: with nobody
 * signed in, an order found here belongs to whoever used this browser last.
 */
function readMirror(): string[] {
  if (!hasSession()) return [];
  return parseStorageOrder(localPref('storageOrder'));
}

/**
 * Store a new order.
 *
 * `visibleKeys` is the whole list as the person now sees it; `[]` is "Reset
 * order". `visible` is the list the gesture was made on, so the entries it
 * does not draw keep their place (`mergeStorageOrder`).
 *
 * ⚠⚠ Never written to the account BEFORE the account's document has arrived
 * (the same rule `lib/tour` follows): `PUT /api/me/prefs` replaces the whole
 * document, and a write made from an empty copy would erase the person's
 * palette, density and language to record the order of their drives. Until it
 * arrives the order is on screen and in the mirror, and is sent the moment it
 * does.
 */
export function saveStorageOrder(visibleKeys: readonly string[], visible: readonly OrderableStorage[]): void {
  const next = visibleKeys.length ? mergeStorageOrder(visibleKeys, visible, saved.value) : [];
  saved.value = next;
  const raw = serializeStorageOrder(next);
  if (!prefsConfigured()) {
    // An embed with no account document: this browser is the only home.
    setLocalPref('storageOrder', raw);
    return;
  }
  if (prefsHydrated()) {
    sendNow(raw);
    return;
  }
  setLocalPref('storageOrder', raw);
  queuedRaw = raw;
  if (queuedWrite) return;
  queuedWrite = onPrefs(() => {
    queuedWrite?.();
    queuedWrite = null;
    sendNow(queuedRaw);
    saved.value = parseStorageOrder(queuedRaw);
  });
}

/**
 * To the account AT ONCE, not after the debounce — the language switcher's
 * rule (web/src/i18n), for the same reason: a reorder is one drop or one
 * click, not a slider, and the account's copy OUTRANKS this browser's on the
 * next load. Left to the 400 ms debounce, a reload or a closed tab right after
 * the drop read the old order back from the server (measured, e2e 158).
 */
function sendNow(raw: string): void {
  savePref('storageOrder', raw);
  void flushPrefs();
}

export interface StorageOrderHandle {
  /** The saved order, as keys (`[]` = the host's order). */
  saved: Readonly<Ref<string[]>>;
  /** The person has an order of their own — what "Use default order" lets go of. */
  custom: ComputedRef<boolean>;
}

/**
 * The explorer's handle on the order.
 *
 * Each call re-reads this browser's mirror — a mount after a sign-out, or
 * after another tab changed it, starts from the current answer rather than
 * the last one this module happened to hold — and follows the account's
 * document while the calling component lives.
 */
export function useStorageOrder(): StorageOrderHandle {
  saved.value = readMirror();
  /* ⚠ `currentPrefs()`, not the snapshot the listener is handed: a queued
   * write (above) may have run earlier in the same announcement, and the
   * snapshot predates it. */
  const off = onPrefs(() => {
    saved.value = parseStorageOrder(currentPrefs().storageOrder);
  });
  if (getCurrentInstance()) onBeforeUnmount(off);
  return { saved, custom: computed(() => saved.value.length > 0) };
}

/* Another tab changed it: follow, but only for a signed-in window (the same
 * gate, and the same reason, as the palette's cross-tab listener in
 * lib/themes). */
if (typeof window !== 'undefined') {
  try {
    window.addEventListener('storage', (e) => {
      if (e.key !== PREF_LS_KEYS.storageOrder || !hasSession()) return;
      saved.value = parseStorageOrder(e.newValue);
    });
  } catch {
    /* non-browser env */
  }
}

/** Testing seam: forget the module's state. */
export function resetStorageOrderState(): void {
  saved.value = [];
  queuedWrite?.();
  queuedWrite = null;
  queuedRaw = '';
}
