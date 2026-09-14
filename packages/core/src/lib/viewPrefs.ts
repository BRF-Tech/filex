/**
 * viewPrefs — HOW A FOLDER LOOKS, remembered.
 *
 * Two pieces of state live here, and the split between them is the whole
 * design decision, so it is written down before any code:
 *
 *   1. PER-FOLDER, because it is a fact about the FOLDER. The view mode
 *      (list / grid / gallery) and the sort (key + direction). A photo album
 *      wants a gallery sorted by name; Downloads wants a list sorted by date.
 *      Those are properties of the contents, so they are remembered against
 *      the folder — Windows Explorer's behaviour, and what the owner asked
 *      for: "x folder'ında son görünüm nasıl kaldı ise öyle görünümde
 *      göstermemiz lazım."
 *
 *   2. GLOBAL, because it is a fact about YOUR SCREEN. Column widths and
 *      which columns you hide. The owner's own complaint is the proof: the
 *      Name column is crushed *at 960px*, which is the width of his review
 *      window — not a property of any folder he was standing in. Remembering
 *      a width per folder would mean dragging Name wider in Documents and
 *      finding it narrow again in Photos, forever. "I never look at Owner" is
 *      likewise a statement about the person, not about a folder.
 *
 * ⚠⚠ THE RULE, in one sentence, because a rule that cannot be said in one
 * sentence cannot be explained to the person it surprises:
 *
 *     Your last choice becomes the default for every folder you have never
 *     set up, and each folder you DO set up keeps its own — "Apply to all
 *     folders" throws the per-folder ones away.
 *
 * That is deliberately not "per-folder silently wins". Every change writes
 * BOTH the folder's memory and the global default (`filex.view-mode`,
 * `filex.list-sort`, which already existed and are not replaced here), so the
 * person who switches to List while standing in one folder gets List in every
 * folder they have not deliberately configured. The only surprise left is the
 * folder they configured themselves last week, and that surprise IS the
 * feature. The escape hatch exists for the day it stops being one.
 *
 * ⚠⚠ WHERE IT IS STORED: the DATABASE, against the user, as one JSON
 * document — not `localStorage`, where every other view preference in this
 * product lives. The owner's decision, 2026-09-13: "tarayıcıya değil db'ye
 * kaydedeceğiz, basit bir json olarak. Oradan çekersek ayarları, tarayıcıda 36
 * user ile girsin yine fark etmez."
 *
 * Browser storage is per BROWSER, not per person. On a shared machine — or any
 * browser two accounts sign into one after the other — the second person
 * silently inherits the first person's folder arrangements: no error, nothing
 * to notice, just somebody else's layout. Namespacing the key by account would
 * only narrow that; it could not close it, because the account is not known
 * until a round trip has already happened. On the user row it cannot happen at
 * all, and the arrangements follow the person to the desktop app and to their
 * other machines — which is what "her user kendi görünümünü görür" actually
 * asks for.
 *
 * ⚠ This file therefore holds NO transport. The explorer injects one
 * (`attachViewPrefsStore`) the same way `useThumbs` is handed an api, so the
 * module stays testable without a server and an embed with no account degrades
 * to "remember nothing" rather than to an error.
 *
 * ⚠ Grouping is NOT a third axis. `ListView` draws date headings exactly when
 * the sort key is `modified`, so "a time-grouped view in this folder" — the
 * owner's own example — is already what remembering the sort key delivers.
 * Adding a separate `grouping` field would create a state where the key says
 * one thing and the grouping another, and no view could honestly draw both.
 */
import { ref } from 'vue';

import type { SortDir, SortKey } from './sortOrder';

export type ViewMode = 'list' | 'grid' | 'gallery';

/**
 * The columns, in the order the list draws them.
 *
 * ⚠⚠ `name` IS one of them now. It used to be excluded here with the note "it
 * is the flexible column and is never hidden or sized by hand", and that
 * exclusion is precisely the behaviour the owner rejected: as the only
 * `1fr` track Name absorbed whatever every other column gave up, so narrowing
 * Size made Name grow — "küçültme yapınca name kısmı büyüyormuş gibi davranıyor
 * — bu davranış yanlış, istersem name'i de kısabilir olmalıyım." It has a real
 * width like every other column and a handle of its own.
 *
 * It is still not HIDEABLE and not MOVABLE: it carries the tick, the tile and
 * the row's click target, and every file manager pins it first.
 */
export type ColumnId = 'name' | 'type' | 'location' | 'owner' | 'modified' | 'size' | 'star';

// ── per-folder memory ─────────────────────────────────────────────────

/**
 * One folder's remembered setup. Short keys on purpose — this is a map with
 * one entry per folder the person has configured, and the whole map is
 * re-serialised on every write.
 */
export interface FolderPrefs {
  /** view mode */
  v?: ViewMode;
  /** sort key */
  k?: SortKey;
  /** sort direction */
  d?: SortDir;
  /** last used, epoch SECONDS — the LRU clock. */
  t: number;
}

/**
 * THE TRANSPORT — injected, so this module knows nothing about fetch, auth or
 * URLs and a test can drive it with two functions.
 *
 * `load` resolves the stored document, or null when there is nobody to store
 * one for (an embed on a shared app token, a public share link). `save` is
 * FIRE AND FORGET on purpose: a view preference is never worth blocking an
 * interface for, and a failed write must leave the change on screen — the next
 * write sends the whole document anyway, so a dropped one heals itself.
 */
export interface ViewPrefsTransport {
  load: () => Promise<unknown>;
  save: (doc: unknown) => void;
}

let transport: ViewPrefsTransport | null = null;

/**
 * Has the document resolved yet?
 *
 * ⚠⚠ Nothing is applied to a folder until it has. This is the whole answer to
 * "the remembered view arrives after the listing paints and the person sees
 * list view jump to grid": the fetch is started at mount, in parallel with the
 * first listing, and it is a single small row against a listing that has to
 * walk a storage — so in practice it lands first. Correctness does not depend
 * on that race, though, because a folder whose document has not arrived simply
 * follows the global default and is corrected once, exactly when the answer is
 * known.
 *
 * ⚠ It is also how a caller with NO user degrades cleanly: `load` resolves
 * null, `ready` still becomes true, and every read below answers "nothing
 * remembered" forever after. No error, no retry loop, no half state.
 */
const ready = ref(false);
/** True when there is somebody to save for. False = read-only, session-only. */
const persistable = ref(false);

export function viewPrefsReady(): boolean {
  return ready.value;
}

/**
 * IS THE MEMORY ON AT ALL — a preference the person turns on, default OFF.
 *
 * Owner's ruling: "User bunu ayarlardan açabilir olacak isterse." Off, every
 * folder follows the global view mode and sort exactly as it did before this
 * module existed, and nobody is surprised by a folder that disagrees with the
 * button they just pressed. On, a folder comes back the way they left it.
 *
 * ⚠ It lives in the same document as the folders themselves, so the switch
 * follows the person between devices like everything else here. The control
 * itself belongs to the host's user-settings modal, which is not this package
 * — the contract is `folderMemoryEnabled()` to read and
 * `setFolderMemoryEnabled()` to write.
 */
const memoryOn = ref(false);

export function folderMemoryEnabled(): boolean {
  return memoryOn.value;
}

export function setFolderMemoryEnabled(on: boolean): void {
  if (memoryOn.value === on) return;
  memoryOn.value = on;
  scheduleSave();
}

type FolderMap = Record<string, FolderPrefs>;

function nowSec(): number {
  return Math.floor(Date.now() / 1000);
}

function isViewMode(v: unknown): v is ViewMode {
  return v === 'list' || v === 'grid' || v === 'gallery';
}

/** The map, in memory. A ref so a surface that lists the remembered folders
 *  (the column menu's "Apply to all folders" / "Forget this folder") re-renders
 *  when one is written or dropped. */
const folderMap = ref<FolderMap>({});

function readFolderMap(raw: unknown): FolderMap {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {};
  const out: FolderMap = {};
  for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
    if (!v || typeof v !== 'object') continue;
    const e = v as Record<string, unknown>;
    const t = typeof e.t === 'number' && Number.isFinite(e.t) ? e.t : 0;
    const entry: FolderPrefs = { t };
    if (isViewMode(e.v)) entry.v = e.v;
    if (e.k === 'name' || e.k === 'type' || e.k === 'modified' || e.k === 'size') entry.k = e.k;
    if (e.d === 'asc' || e.d === 'desc') entry.d = e.d;
    // An entry that remembers nothing is noise from a half-written document.
    if (entry.v || entry.k) out[k] = entry;
  }
  return out;
}

/**
 * HOW MANY FOLDERS WE REMEMBER, and how the number was chosen.
 *
 * ⚠⚠ Without a cap this is one entry per folder ever visited, forever — and
 * now that the document lives in a database row rather than in a browser, it is
 * a row that grows without bound for the life of the account, replicated into
 * every backup. The endpoint refuses a document over 128 KB, so left uncapped
 * the failure would eventually be that saving stops working, silently, for the
 * people who use the product most.
 *
 * 300 is derived, not picked: an entry serialises to ~110 bytes (a qualified
 * folder path plus four short fields), so 300 of them is ~33 KB — comfortably
 * inside that limit with room for longer paths than the estimate assumed. And
 * it is far more folders than a person deliberately configures: the map only
 * gains an entry when someone CHANGES a view (see `touchFolder` — merely
 * walking through a folder writes nothing), so in real use the cap is never
 * reached and nothing is ever evicted. It is a ceiling on the pathological
 * case, not a working limit.
 */
export const FOLDER_CAP = 300;

/**
 * Drop the least-recently-USED entries until the map fits the cap.
 *
 * ⚠ Used, not written: `touchFolder` bumps `t` when a remembered folder is
 * OPENED as well as when it is changed, so a folder you keep visiting does not
 * age out merely because you have not re-sorted it lately. Evicting by write
 * time would throw away exactly the folders whose memory is working.
 */
function evict(): void {
  const entries = Object.entries(folderMap.value);
  if (entries.length <= FOLDER_CAP) return;
  entries.sort((a, b) => b[1].t - a[1].t); // newest first
  const kept: FolderMap = {};
  for (const [k, v] of entries.slice(0, FOLDER_CAP)) kept[k] = v;
  folderMap.value = kept;
}

/**
 * THE KEY a folder is remembered under.
 *
 * ⚠⚠ Read filex issue #21 before changing this. A storage's NAME is editable —
 * that is the point of a name — so it is not a stable address; every protocol
 * accepts a storage's immutable `uid` as the first path segment for exactly
 * that reason (`backend/internal/storageref`). This function therefore takes
 * the storage REF the host gave us and does not care which of the two it is:
 * the moment `config.storages[].uid` is populated, keys built here become
 * rename-proof with no change to this file.
 *
 * ⚠ Until then the key carries a storage NAME, and the consequences are worth
 * stating rather than hiding:
 *   · renaming a storage loses its folders' memory (they fall back to the
 *     global default — a mild, self-healing loss, and the LRU ages the orphans
 *     out);
 *   · deleting a storage and creating a new one with the SAME name hands the
 *     new one the old one's remembered views. Rare, bounded to view mode and
 *     sort, and the fix is the one line of plumbing named above.
 * What it does NOT do is collide across storages that exist at the same time,
 * because two live storages cannot share a name.
 *
 * The relative path is normalised so `a/b`, `/a/b/` and `a//b` are one folder.
 */
export function folderKey(storageRef: string, relPath: string): string {
  const ref = String(storageRef ?? '').trim();
  const rel = String(relPath ?? '')
    .replace(/^[a-z][a-z0-9+.-]*:\/\//i, '')
    .replace(/\/{2,}/g, '/')
    .replace(/^\/+|\/+$/g, '');
  if (!ref) return rel;
  /* ⚠ No trailing slash on a storage ROOT. `thumbfix/` and `thumbfix` would be
   * two keys for one folder, and the second is the one every caller builds —
   * measured: the seeded default for `.recent` never fired, because the
   * explorer asked for `.recent/` and the table is keyed `.recent`. */
  return rel ? `${ref}/${rel}` : ref;
}

/**
 * What a folder with NO memory of its own should open as, when the answer is
 * not simply the global default.
 *
 * Only Recent is in here, and it earns it: Recent's whole promise is "the
 * things you were just in", so opening it alphabetically buries the file the
 * person came back for. Sorting it by date is also what makes its date
 * headings honest — see the note on grouping in `FileExplorer`.
 *
 * ⚠ A seed, not an override: the moment someone sorts Recent by name, that
 * choice is stored against `.recent` like any other folder's and this table
 * stops being consulted for it.
 */
const SEEDED: Record<string, Omit<FolderPrefs, 't'>> = {
  '.recent': { k: 'modified', d: 'desc' },
};

/**
 * What this folder is remembered as, or the seed, or nothing.
 *
 * ⚠⚠ The SEED is answered even when the memory is switched off, and the
 * stored map only when it is on. They are two different things wearing one
 * shape: the seed is what Recent sorts by — a property of that view, which the
 * owner asked for directly and which must not hinge on an unrelated preference
 * — while the map is the per-folder memory the preference governs. Putting the
 * gate here rather than at the caller is what keeps every caller from having to
 * remember the distinction.
 */
export function folderPrefs(key: string): Omit<FolderPrefs, 't'> | null {
  if (!key) return null;
  if (memoryOn.value) {
    const hit = folderMap.value[key];
    if (hit) {
      const { t: _t, ...rest } = hit;
      void _t;
      return rest;
    }
  }
  return SEEDED[key] ?? null;
}

/** True when this folder has a memory the PERSON made (not a seed) — what the
 *  "Reset this folder" affordance is gated on. */
export function folderIsRemembered(key: string): boolean {
  return memoryOn.value && !!key && !!folderMap.value[key];
}

/** How many folders are remembered — the "Apply to all folders" copy says so,
 *  because an escape hatch that does not say what it throws away is a trap. */
export function rememberedCount(): number {
  return memoryOn.value ? Object.keys(folderMap.value).length : 0;
}

/**
 * Remember a change against this folder.
 *
 * ⚠ The ONLY thing that creates an entry. Navigation does not (see
 * `touchFolder`), so the map stays the set of folders somebody deliberately
 * set up rather than a log of everywhere they have ever been — which is both
 * why the cap is never reached in practice and why "Apply to all folders" has
 * a small, comprehensible number to report.
 */
export function rememberFolder(key: string, patch: Omit<FolderPrefs, 't'>): void {
  if (!key || !memoryOn.value) return;
  const prev = folderMap.value[key];
  folderMap.value = {
    ...folderMap.value,
    [key]: { ...(prev ?? {}), ...patch, t: nowSec() },
  };
  evict();
  scheduleSave();
}

/**
 * Mark a remembered folder as used, for the LRU clock — and ONLY if it is
 * already remembered. Called on navigation.
 *
 * ⚠ The guard is the point. Bumping an absent key would create an entry for
 * every folder anyone ever opens, which is the unbounded growth the cap exists
 * to survive; keeping the guard means the cap is a safety net rather than a
 * working limit.
 */
export function touchFolder(key: string): void {
  if (!key || !memoryOn.value) return;
  const prev = folderMap.value[key];
  if (!prev) return;
  const t = nowSec();
  if (prev.t === t) return; // same second — nothing to write
  folderMap.value = { ...folderMap.value, [key]: { ...prev, t } };
  scheduleSave();
}

/** Forget one folder: it goes back to following the global default. */
export function forgetFolder(key: string): void {
  if (!key || !folderMap.value[key]) return;
  const next = { ...folderMap.value };
  delete next[key];
  folderMap.value = next;
  scheduleSave();
}

/**
 * THE ESCAPE HATCH — "Apply to all folders".
 *
 * Forgets every per-folder memory. The caller has already written the current
 * folder's setup to the global default (every change does), so the visible
 * effect is that every folder now opens the way this one does, which is what
 * the words promise.
 */
export function forgetAllFolders(): void {
  folderMap.value = {};
  scheduleSave();
}

// ── columns: widths + visibility (global) ─────────────────────────────


export interface ColumnSpec {
  id: ColumnId;
  /** Default width in px. */
  width: number;
  min: number;
  max: number;
  /** Can the person hide it from the header menu? */
  hideable: boolean;
  /** Can the person drag its edge? */
  resizable: boolean;
}

/**
 * The row's tracks, in shipped order.
 *
 * ⚠ Defaults narrower than the ones they replace (Owner 120 → 104, Modified
 * 170 → 160): those numbers were set at 1440 where there is slack for them,
 * and 170px of Modified is 170px Name does not get at 960.
 *
 * ⚠⚠ There is no `priority` field any more, and its absence is the whole
 * change. It ordered a SHEDDING pass: when the tracks stopped fitting the
 * pane, the least informative column was dropped and the header menu marked it
 * "no room". The owner rejected that outright — "no room demesin, kenara devam
 * eden bir scroll getirsin… genişletildikçe no room olmasın" — so nothing is
 * dropped for want of room ever again. A table wider than its pane scrolls
 * sideways, which is what a table does.
 */
export const COLUMNS: readonly ColumnSpec[] = [
  /* Name is pinned first and cannot be hidden, but its width is the person's
     like any other (see the note on ColumnId). */
  { id: 'name', width: 260, min: 120, max: 900, hideable: false, resizable: true },
  { id: 'type', width: 88, min: 64, max: 220, hideable: true, resizable: true },
  { id: 'location', width: 160, min: 80, max: 420, hideable: true, resizable: true },
  { id: 'owner', width: 104, min: 72, max: 260, hideable: true, resizable: true },
  { id: 'modified', width: 160, min: 96, max: 300, hideable: true, resizable: true },
  { id: 'size', width: 88, min: 64, max: 200, hideable: true, resizable: true },
  /* The star is a control, not a value: there is nothing to size and hiding it
     is the Starred view's own call (`starEnabled`), not a column preference.
     It is in this list only so one pass owns every optional track and no
     stylesheet has to agree with it about which ones exist. */
  { id: 'star', width: 24, min: 24, max: 24, hideable: false, resizable: false },
] as const;

/** The two fixed tracks, in px, so the table arithmetic can be exact. */
export const FIXED_TRACKS = { check: 28, menu: 28 } as const;

/**
 * THE NAME COLUMN'S HARD FLOOR — how narrow a person may drag it.
 *
 * 120px, which at the list's 13px/500 is the 24px type tile, its 8px gap and
 * about eleven characters. Short, and deliberately so: the owner asked to be
 * able to shrink Name ("istersem name'i de kısabilir olmalıyım"), and a floor
 * that refuses at 220 would be the old behaviour wearing a smaller number.
 * Below 120 the cell is the tile and an ellipsis — a column that has stopped
 * being a name column at all — so that is where it stops.
 *
 * ⚠ Not to be confused with `NAME_AUTO`: this is what a DRAG may reach, that
 * is what an untouched table opens at.
 */
export const NAME_MIN = 120;

/**
 * What Name opens at when nobody has ever dragged anything.
 *
 * 220px holds roughly 26 characters at 13px/500 — enough for
 * `Quarterly Report Q2.pdf` to read whole, which is the length of a real
 * document name rather than a demo fixture's. The auto pass never goes below
 * it: on a phone it would rather let the table run off the side (it is going
 * to anyway, with six columns on a 390px screen) than open showing eleven
 * characters of every filename.
 */
export const NAME_AUTO = 220;

interface ColsState {
  w: Partial<Record<ColumnId, number>>;
  hidden: ColumnId[];
  /** The person's column order. Absent = the shipped order in `COLUMNS`. */
  o?: ColumnId[];
}

function specOf(id: ColumnId): ColumnSpec | undefined {
  return COLUMNS.find((c) => c.id === id);
}

function isColumnId(v: unknown): v is ColumnId {
  return typeof v === 'string' && COLUMNS.some((c) => c.id === v);
}

function readCols(raw: unknown): ColsState {
  const fallback: ColsState = { w: {}, hidden: [] };
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return fallback;
  {
    const p = raw as { w?: unknown; hidden?: unknown; o?: unknown };
    const w: Partial<Record<ColumnId, number>> = {};
    if (p.w && typeof p.w === 'object') {
      for (const [k, v] of Object.entries(p.w as Record<string, unknown>)) {
        if (!isColumnId(k) || typeof v !== 'number' || !Number.isFinite(v)) continue;
        const spec = specOf(k);
        if (!spec) continue;
        // ⚠ Clamped on the way IN as well as on the way out: a width stored by
        // an older build (or hand-edited) outside today's bounds would
        // otherwise draw a column nobody can reach the edge of, and nothing in
        // the UI would explain it.
        w[k] = Math.min(spec.max, Math.max(spec.min, Math.round(v)));
      }
    }
    const hidden = Array.isArray(p.hidden)
      ? (p.hidden.filter((x) => isColumnId(x) && specOf(x)?.hideable) as ColumnId[])
      : [];
    const o = Array.isArray(p.o)
      ? ([...new Set(p.o.filter(isColumnId))] as ColumnId[])
      : undefined;
    return { w, hidden: [...new Set(hidden)], o };
  }
}

const colsState = ref<ColsState>(readCols(null));

/**
 * THE COLUMN ORDER — the person's, reconciled with the columns that exist.
 *
 * ⚠⚠ Reconciled, not trusted. A stored order is a list written by an older
 * build, so it can be missing a column added since and can name one removed
 * since. Returning it as-is would make a new column INVISIBLE to everyone who
 * had ever touched the order — a feature that ships and then does not appear
 * for exactly the users engaged enough to have customised something. So:
 * stored entries that still exist keep their place, and anything the stored
 * list never heard of is inserted at its own shipped index.
 *
 * ⚠ ORDER IS GLOBAL, like width and visibility, and unlike the view mode. Two
 * reasons, and the second is the one that would have bitten:
 *   · it is a reading habit ("I want Size next to Name"), not a property of a
 *     folder's contents, which is the line this whole module is drawn on;
 *   · widths are global too, and a per-folder order with global widths would
 *     mean the same six columns at the same six sizes arriving in a different
 *     sequence in every folder — a layout that reads as random rather than as
 *     the person's.
 */
export function columnOrder(): ColumnId[] {
  const stored = colsState.value.o;
  const known = COLUMNS.map((c) => c.id);
  if (!stored || stored.length === 0) return known;
  /* ⚠ Name first, whatever the document says. It is pinned in the table, so an
     order that claimed otherwise would be a sequence nothing on screen obeys —
     and a stored order written before Name was a column at all has it appended
     at the END by the reconciliation below. */
  const out = stored.filter((id) => known.includes(id) && id !== 'name');
  out.unshift('name');
  /* ⚠ A column the stored list never heard of goes to the END, not to its own
   * shipped index. Measured: inserting at the shipped index walked newcomers
   * in FRONT of the columns the person had deliberately moved — somebody who
   * had put Size first would open a later release to find two new columns
   * ahead of it and their arrangement silently rewritten. Appending leaves the
   * stored sequence exactly as it was and puts the newcomer where it is
   * visible without being in the way. */
  for (const id of known) if (!out.includes(id)) out.push(id);
  return out;
}

/** Put `id` at `index` in the order. Everything else closes up behind it. */
export function moveColumn(id: ColumnId, index: number): void {
  const spec = specOf(id);
  /* The star is not the person's to move: it is a control pinned to the
     trailing edge beside the row menu, not a value with a place in a sequence. */
  if (!spec || !spec.hideable) return;
  const cur = columnOrder();
  const from = cur.indexOf(id);
  if (from === -1) return;
  const next = [...cur];
  next.splice(from, 1);
  next.splice(Math.max(0, Math.min(next.length, index)), 0, id);
  if (next.join() === cur.join()) return;
  colsState.value = { ...colsState.value, o: next };
  scheduleSave();
}

/** One step left or right — what the header menu offers, so the whole gesture
 *  is reachable without a pointer. */
export function moveColumnBy(id: ColumnId, delta: -1 | 1): void {
  const cur = columnOrder().filter((c) => specOf(c)?.hideable);
  const at = cur.indexOf(id);
  if (at === -1) return;
  const to = at + delta;
  if (to < 0 || to >= cur.length) return;
  /* Computed against the MOVABLE columns only, then translated back into the
     full order: stepping "right" past the pinned star would look like nothing
     happening, which reads as a broken button rather than as a boundary. */
  const neighbour = cur[to];
  const full = columnOrder();
  moveColumn(id, full.indexOf(neighbour));
}

/** Can this column still move that way? The menu greys the ends rather than
 *  offering a button that does nothing. */
export function canMoveColumn(id: ColumnId, delta: -1 | 1): boolean {
  const cur = columnOrder().filter((c) => specOf(c)?.hideable);
  const at = cur.indexOf(id);
  if (at === -1) return false;
  return at + delta >= 0 && at + delta < cur.length;
}

/** This column's width right now — the person's, or the default. */
export function columnWidth(id: ColumnId): number {
  const spec = specOf(id);
  if (!spec) return 0;
  return colsState.value.w[id] ?? spec.width;
}

/** Set a width, clamped to the column's own bounds. */
export function setColumnWidth(id: ColumnId, px: number): void {
  const spec = specOf(id);
  if (!spec || !spec.resizable) return;
  const next = Math.min(spec.max, Math.max(spec.min, Math.round(px)));
  if (colsState.value.w[id] === next) return;
  colsState.value = { ...colsState.value, w: { ...colsState.value.w, [id]: next } };
  scheduleSave();
}

/**
 * HAS ANYBODY EVER SIZED THIS TABLE — the one bit that separates "I have never
 * touched this" from "I deliberately made Name narrow".
 *
 * ⚠⚠ Without it the two are indistinguishable and the first resize is undone
 * by the next pane resize: an untouched table is sized to the pane it is in
 * (`tableLayout`), so a person who drags Name to 140 and then opens the
 * inspector gets Name recomputed back to whatever the narrower pane suggests.
 * The stored widths are that bit — the moment they exist, the pane stops
 * having an opinion.
 *
 * ⚠⚠ The bit is NAME's width, not "the map is empty", and that is a migration
 * decision rather than a stylistic one. Name was not a column until this
 * change, so every document written before it — including the owner's, which
 * carries `{size: 89, type: 125}` from the layout he rejected — has widths but
 * has never been asked about Name. Reading those as "configured" would open
 * his table at Name's shipped 260px, wider than his pane, scrolled sideways on
 * first sight, at a width nobody ever chose. Reading them as "Name is still
 * auto" keeps the two numbers he did choose and derives the one he did not.
 * The first drag writes every width at once (`freezeWidths`), so a table
 * somebody has actually sized can never fall back into this branch.
 */
export function widthsAreAuto(): boolean {
  return colsState.value.w.name === undefined;
}

/**
 * Freeze the widths currently ON SCREEN, for the columns that have none stored.
 *
 * ⚠⚠ Called by the view at the START of a resize gesture, and it has to write
 * EVERY drawn column, not the one being dragged. Writing only the dragged
 * column would leave Name auto — and auto Name takes the slack, so narrowing
 * Size would still widen Name, which is the exact behaviour the owner called
 * wrong. Freezing the whole row first turns the gesture into what it looks
 * like: one column changes, the others stay where they are.
 *
 * Existing entries are never overwritten: the widths handed in are what the
 * person is already looking at, and the stored ones are already that.
 */
export function freezeWidths(widths: Partial<Record<ColumnId, number>>): void {
  const w: Partial<Record<ColumnId, number>> = { ...colsState.value.w };
  let changed = false;
  for (const [k, v] of Object.entries(widths)) {
    if (!isColumnId(k) || typeof v !== 'number' || !Number.isFinite(v)) continue;
    if (w[k] !== undefined) continue;
    const spec = specOf(k);
    if (!spec || !spec.resizable) continue;
    w[k] = Math.min(spec.max, Math.max(spec.min, Math.round(v)));
    changed = true;
  }
  if (!changed) return;
  colsState.value = { ...colsState.value, w };
  scheduleSave();
}

/** True when the PERSON has hidden this column — the only reason a column is
 *  ever absent now that width sheds nothing (see `tableLayout`). */
export function columnHidden(id: ColumnId): boolean {
  return colsState.value.hidden.includes(id);
}

export function setColumnHidden(id: ColumnId, hidden: boolean): void {
  const spec = specOf(id);
  if (!spec || !spec.hideable) return;
  if (columnHidden(id) === hidden) return;
  const set = new Set(colsState.value.hidden);
  if (hidden) set.add(id);
  else set.delete(id);
  colsState.value = { ...colsState.value, hidden: [...set] };
  scheduleSave();
}

/** Back to the shipped widths, order and visibility — one button for all
 *  three, because "reset the columns" is one thought. */
export function resetColumns(): void {
  colsState.value = { w: {}, hidden: [] };
  scheduleSave();
}

/** True when anything about the columns has been changed by hand — what the
 *  header menu's "Reset columns" is gated on. */
export function columnsCustomised(): boolean {
  return (
    Object.keys(colsState.value.w).length > 0 ||
    colsState.value.hidden.length > 0 ||
    (colsState.value.o?.length ?? 0) > 0
  );
}

/* ── the document ─────────────────────────────────────────
 *
 * One JSON object, holding everything this module owns:
 *
 *   { "on": true,                                   the opt-in switch
 *     "f":  { "<storage>/<rel>": {v,k,d,t}, … },    per-folder, LRU-capped
 *     "c":  { "w": {…}, "hidden": […], "o": […] } } columns, global
 *
 * Short keys because the whole thing is re-serialised on every save and lives
 * in a database column: at the 300-folder cap that is roughly 33 KB, and the
 * endpoint refuses anything over 128 KB.
 */

interface ViewPrefsDoc {
  on?: boolean;
  f?: unknown;
  c?: unknown;
  [ns: string]: unknown;
}

/** The three top-level names this module owns. Everything else in the
 *  document belongs to somebody else and is none of our business. */
const OWNED_KEYS = ['on', 'f', 'c'] as const;

/**
 * ⚠⚠ EVERYTHING IN THE DOCUMENT THAT IS NOT OURS, kept verbatim.
 *
 * The endpoint stores an OPAQUE object — `handlers/viewprefs.go` validates it
 * as JSON, bounds its size and does not look inside, precisely so the shape
 * can change without the server changing with it. This module was the half
 * that broke that promise: `currentDoc()` rebuilt `{on, f, c}` from scratch on
 * every save, so a field written by any other feature was gone the next time
 * somebody dragged a column. Measured, and one resize was enough:
 *
 *     loaded: {"on":true,"f":{},"c":{},"install":{"dismissed":1}}
 *     saved:  {"on":true,"f":{},"c":{"w":{"size":130},"hidden":[]}}
 *
 * No error, nothing on screen, and the feature that lost its value could only
 * discover it by looking. The desktop-app reminder's "never show me again" was
 * left in `localStorage` because of this — per browser profile, so a second
 * machine, a private window or cleared site data asked again.
 *
 * ⚠ A `ref`, like everything else here, because a slot's value IS rendered:
 * the desktop-app reminder reads its own key inside a `computed` and has to
 * settle by itself when the document lands a moment after the page does.
 */
const foreign = ref<Record<string, unknown>>({});

/** Split a loaded document into "ours" and "everybody else's". */
function takeForeign(doc: Record<string, unknown>): Record<string, unknown> {
  const rest: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(doc)) {
    if ((OWNED_KEYS as readonly string[]).includes(k)) continue;
    rest[k] = v;
  }
  return rest;
}

function currentDoc(): ViewPrefsDoc {
  /* ⚠ Spread FIRST, so the three keys this module owns always win: a foreign
   * key called `c` cannot overwrite the column state on its way back out. */
  return { ...foreign.value, on: memoryOn.value, f: folderMap.value, c: colsState.value };
}

/* ── namespaced slots: room in the document for somebody else ────────────
 *
 * A feature outside this module keeps a value in the SAME per-user document
 * without reaching into its shape — it gets one top-level key of its own and
 * cannot see or touch anybody else's. That is the whole API surface, on
 * purpose: a general "write anywhere in the document" helper is how the next
 * feature ends up owning `c`.
 *
 * ⚠ It is the same document, so it inherits its two properties exactly: it
 * saves only when there is somebody to save FOR (a share link and an app-token
 * embed have no account — `load` answers null and this writes nothing, for
 * ever), and the write is debounced and fire-and-forget. A caller that must
 * work with no account needs a fallback of its own; `web/src/composables/
 * useInstallPrompt.ts` is the worked example.
 */

/** One feature's corner of the document. */
export interface ViewPrefsSlot<T> {
  /** What is stored, or undefined — including before the document lands. */
  get(): T | undefined;
  /** Replace it. A no-op when there is nobody to save for. */
  set(value: T): void;
  /** Remove the key entirely. */
  clear(): void;
  /** Has the document been read yet? Reactive — read it in a `computed`. */
  ready(): boolean;
  /** Is there an account behind this session at all? Reactive. */
  persistable(): boolean;
}

/**
 * Claim `namespace` as a top-level key in the per-user view-prefs document.
 *
 * ⚠ The three names this module owns are refused outright rather than
 * quietly renamed: a slot called `c` that silently became `c2` would be a
 * value the caller could never read back.
 */
export function viewPrefsSlot<T>(namespace: string): ViewPrefsSlot<T> {
  if (!namespace || (OWNED_KEYS as readonly string[]).includes(namespace)) {
    throw new Error(`viewPrefsSlot: "${namespace}" is reserved by lib/viewPrefs`);
  }
  return {
    get: () => foreign.value[namespace] as T | undefined,
    set(value: T) {
      foreign.value = { ...foreign.value, [namespace]: value };
      scheduleSave();
    },
    clear() {
      if (!(namespace in foreign.value)) return;
      const next = { ...foreign.value };
      delete next[namespace];
      foreign.value = next;
      scheduleSave();
    },
    ready: () => ready.value,
    persistable: () => persistable.value,
  };
}

/**
 * SAVING — debounced, coalesced, fire and forget.
 *
 * ⚠⚠ A view change is a keystroke-rate event once a column can be dragged: a
 * single resize is dozens of `setColumnWidth` calls, one per pointermove. A PUT
 * per change would hammer the server and, worse, race itself — two writes in
 * flight of a document that is replaced wholesale, arriving in whatever order
 * the network chose. Waiting for the gesture to settle and then sending the
 * WHOLE current document makes the last write the true one by construction,
 * and makes a dropped write self-healing: the next save carries everything the
 * lost one did.
 */
const SAVE_DEBOUNCE_MS = 800;
let saveTimer: ReturnType<typeof setTimeout> | undefined;
let saveDirty = false;

function scheduleSave(): void {
  /* ⚠ Not before the document has loaded. A save fired while the fetch is
   * still in flight would write this session's empty defaults over everything
   * the person had arranged — the classic "my settings reset themselves".
   * Nothing mutates the state before `ready` except the load itself. */
  if (!ready.value || !persistable.value || !transport) return;
  saveDirty = true;
  if (saveTimer) clearTimeout(saveTimer);
  saveTimer = setTimeout(flushSave, SAVE_DEBOUNCE_MS);
}

function flushSave(): void {
  if (saveTimer) clearTimeout(saveTimer);
  saveTimer = undefined;
  if (!saveDirty || !transport || !persistable.value) return;
  saveDirty = false;
  transport.save(currentDoc());
}

/**
 * ⚠ A change made in the last moment before a tab closes is inside the
 * debounce window and would simply be lost. `pagehide` is the last event a
 * browser reliably delivers (`beforeunload` never fires on mobile, and
 * `unload` is skipped when a page enters the back/forward cache), so the
 * pending document goes out there — the transport sends it with `keepalive`,
 * which is what lets a request outlive the document that started it.
 */
if (typeof window !== 'undefined') {
  window.addEventListener('pagehide', flushSave);
  window.addEventListener('visibilitychange', () => {
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') flushSave();
  });
}

/**
 * Hand this module its transport and read the document.
 *
 * Called once per explorer mount. Calling it again is a no-op on purpose: two
 * explorers on one page share this module's state, and the second must not
 * re-fetch over the first.
 */
export function attachViewPrefsStore(t: ViewPrefsTransport): void {
  if (transport) return;
  transport = t;
  void (async () => {
    let doc: unknown = null;
    try {
      doc = await t.load();
    } catch {
      /* No account, offline, a 500 — all one answer: remember nothing this
         session and never write, so a transient failure cannot erase what is
         already stored. */
      doc = null;
    }
    if (doc && typeof doc === 'object' && !Array.isArray(doc)) {
      const d = doc as ViewPrefsDoc;
      memoryOn.value = d.on === true;
      folderMap.value = readFolderMap(d.f);
      colsState.value = readCols(d.c);
      /* ⚠ Whatever else was in there travels on untouched. This is the half
       * that makes the endpoint's "opaque object" promise true on the client
       * as well — see `foreign` above for what it cost while it was false. */
      foreign.value = takeForeign(d as Record<string, unknown>);
      persistable.value = true;
    }
    ready.value = true;
  })();
}

/** Test seam: drop the transport and the state, so one suite cannot leak a
 *  document into the next. Not used by the app. */
export function __resetViewPrefs(): void {
  transport = null;
  ready.value = false;
  persistable.value = false;
  memoryOn.value = false;
  folderMap.value = {};
  colsState.value = readCols(null);
  foreign.value = {};
  saveDirty = false;
  if (saveTimer) clearTimeout(saveTimer);
  saveTimer = undefined;
}

/** Test seam: send whatever is pending now, without waiting out the debounce. */
export function __flushViewPrefs(): void {
  flushSave();
}

/** What one table looks like right now: which optional columns are drawn, how
 *  wide every track is, and how wide the table therefore is. */
export interface TableLayout {
  /** The optional columns, in the person's order. Never shortened for width. */
  visible: ColumnId[];
  /** Every drawn track's width in px, Name included. */
  widths: Record<ColumnId, number>;
  /** The table's own width: every track, every gap, both paddings. */
  total: number;
  /** True while these widths are derived from the pane rather than stored. */
  auto: boolean;
}

/**
 * THE TABLE'S WIDTHS — one answer, used by the header and by every row.
 *
 * ⚠⚠ This replaces `fitColumns`, and the difference is not a tuning. That pass
 * fitted the columns TO the container and dropped one whenever they stopped
 * fitting; the header menu then told the person there was "no room". The owner
 * rejected the whole model, verbatim: "no room demesin, kenara devam eden bir
 * scroll getirsin… tablo vertical ve horizontal scrollable olarak açılacak,
 * genişletildikçe no room olmasın." So:
 *
 *   · nothing is ever dropped for want of room — `visible` is the person's
 *     choice and the caller's candidates, and width has no vote;
 *   · the table's width is the SUM of its columns, so when that exceeds the
 *     pane the table scrolls sideways (the view's job) instead of shrinking;
 *   · Name is an ordinary column with a width of its own, so narrowing Size no
 *     longer widens Name.
 *
 * ⚠ `candidates` is what the CALLER is willing to draw at all: Location is
 * only meaningful where rows come from more than one folder, and the star only
 * where starring is offered. Passing them is how those two facts stay with the
 * component that knows them instead of being re-derived here.
 *
 * ⚠⚠ AUTO vs STORED, which is the only subtle thing left. With nothing stored
 * (`widthsAreAuto`), the widths are derived from the pane so a table nobody has
 * configured opens sensibly at 1440 and at 390 alike: Name asks for `NAME_AUTO`,
 * the other columns give up room toward their own minimums to pay for it, and
 * Name takes any slack that is left. The instant one width is stored — and the
 * view stores ALL of them on the first drag, see `freezeWidths` — the pane
 * stops having an opinion and these are the person's numbers, at every window
 * size, forever. Without that line a resize would be silently undone by the
 * next auto-fit, which is the trap this pair exists to avoid.
 */
export function tableLayout(
  available: number,
  candidates: readonly ColumnId[],
  opts: { gap?: number; padding?: number } = {},
): TableLayout {
  const gap = opts.gap ?? 8; // --fe-gap-sm
  const padding = opts.padding ?? 24; // --fe-gap on both sides

  const visible = columnOrder().filter(
    (id) => id !== 'name' && candidates.includes(id) && !columnHidden(id),
  );

  const widths = {} as Record<ColumnId, number>;
  for (const id of visible) widths[id] = columnWidth(id);
  widths.name = columnWidth('name');

  /* tick + Name + the optional ones + ⋮, so one gap fewer than that. */
  const chrome =
    FIXED_TRACKS.check + FIXED_TRACKS.menu + padding + gap * (visible.length + 2);
  const auto = widthsAreAuto();
  const nameSpec = specOf('name')!;

  /* A container we cannot measure yet (0 on the first tick, or a test with no
     layout) keeps the shipped widths — the observer corrects it one frame
     later, and a table that guessed would flash. */
  if (auto && available > 0) {
    const room = available - chrome;
    let rest = visible.reduce((s, id) => s + widths[id], 0);
    /* What the other columns may occupy if Name is to open at its comfortable
       width. Over that, they give up room in proportion to how much each HAS
       to give, so a 300px Modified yields more than a 24px star (which yields
       nothing — its min is its max). */
    const target = room - NAME_AUTO;
    if (rest > target) {
      const give = visible.reduce((s, id) => s + (widths[id] - specOf(id)!.min), 0);
      if (give > 0) {
        const f = Math.min(1, (rest - target) / give);
        for (const id of visible) {
          const spec = specOf(id)!;
          /* ⚠ `ceil` on the reduction, not `floor`. Rounding the other way
             leaves the row a pixel or two over the pane and hands an otherwise
             perfectly fitting table a horizontal scrollbar on first sight —
             measured at 960, where the arithmetic lands on 712 in a 711px
             pane. Over-shrinking by a pixel is invisible; Name absorbs it. */
          widths[id] = Math.max(spec.min, widths[id] - Math.ceil((widths[id] - spec.min) * f));
        }
        rest = visible.reduce((s, id) => s + widths[id], 0);
      }
    }
    /* Slack goes to Name, and only to Name. Below `NAME_AUTO` it stops: at
       that point the table is wider than the pane and scrolls, which beats
       opening with eleven characters of every filename. */
    widths.name = Math.round(
      Math.max(NAME_AUTO, Math.min(nameSpec.max, room - rest)),
    );
  }

  const total = chrome + widths.name + visible.reduce((s, id) => s + widths[id], 0);
  return { visible, widths, total, auto };
}
