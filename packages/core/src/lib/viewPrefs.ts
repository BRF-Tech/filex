/**
 * viewPrefs — HOW A FOLDER LOOKS, remembered per folder.
 *
 * ⚠⚠ THE RULE, in one sentence, because a rule that cannot be said in one
 * sentence cannot be explained to the person it surprises:
 *
 *     A change you make in a folder belongs to THAT folder; a folder you have
 *     never changed opens with your default view, or the instance's default
 *     when you have not set one, or filex's own.
 *
 * Resolution, field by field (view mode · sort · columns):
 *
 *     the folder's own memory  →  your default  →  the instance default
 *                                              →  filex's built-in default
 *
 * ⚠⚠ WHY IT CHANGED, 2026-09-21. The owner, verbatim: "Explore içindeki
 * değişikliklerimiz o klasör özelinde olmalı; tüm klasörlerde görünüm
 * değişikliği geçerli oluyor." Reproduced the same day against the v0.43.0
 * branch: switch folder A to grid, open folder B — B is grid too. Two things
 * made it so, and both were design rather than accident:
 *
 *   1. the per-folder memory shipped in v0.41.0 as an OPT-IN, off by default,
 *      so for nearly everyone nothing was ever remembered per folder at all;
 *   2. every change — with the memory on or off — ALSO wrote a "global
 *      default" (`g`), defined as "your last choice becomes the default for
 *      every folder you have never set up". The last click anywhere was
 *      therefore what every untouched folder showed, which is exactly the leak.
 *      Measured with the memory ON: grid in FolderB wrote `f["Repro/FolderB"]`
 *      AND `g.v = "grid"`, and FolderA — never touched — opened as grid.
 *   This release made it worse by moving `g` onto the account: a click in one
 *   folder in one browser re-arranged every untouched folder in every browser.
 *
 * So: the memory is always on, a change writes ONLY the folder it was made in,
 * and "what an untouched folder opens as" is a DEFAULT the person sets on
 * purpose (their settings) or the operator sets for the instance (admin
 * settings) — never a side effect of a click. Changing a default never touches
 * a folder that remembers its own view; it only changes what untouched folders
 * open as.
 *
 * ⚠ The columns are per folder too now, and that reverses a decision written
 * here before ("widths are a fact about your SCREEN, so they are global"). The
 * owner's sentence is about "görünüm değişikliği" — every change to how a
 * folder looks — and a column dragged wider in Downloads showing up wider in
 * Photos is the same leak with a different control. A person who does want
 * one arrangement everywhere has "Apply to all folders" in the column menu,
 * which makes this folder's setup their default and forgets the others.
 *
 * ⚠⚠ WHERE IT IS STORED: the DATABASE, against the user, as one JSON document
 * (migration 00039) — not `localStorage`. The owner's decision, 2026-09-13:
 * "tarayıcıya değil db'ye kaydedeceğiz, basit bir json olarak." Browser
 * storage is per BROWSER, so on a shared machine the second person would
 * silently inherit the first one's folder arrangements.
 *
 * ⚠ This file holds NO transport. The explorer (and the admin app) inject one
 * (`attachViewPrefsStore`, usually `lib/viewPrefsHttp`), so the module stays
 * testable without a server and an embed with no account degrades to
 * "remember nothing across sessions" rather than to an error.
 *
 * ⚠ Grouping is NOT a separate axis. `ListView` draws date headings exactly
 * when the sort key is `modified`, so "a time-grouped view in this folder" is
 * already what remembering the sort key delivers.
 */
import { ref } from 'vue';

import type { SortDir, SortKey } from './sortOrder';
import {
  columnDropBand as genericDropBand,
  createColumnStore,
  emptyCols,
  readCols,
  reorderColumns as genericReorder,
  type ColsState,
  type ColumnStore,
  type TableColumnSpec,
} from './tableColumns';

export type ViewMode = 'list' | 'grid' | 'gallery';

/**
 * The explorer's columns, in the order the list draws them.
 *
 * `name` is the table's LEAD: pinned first, never hidden, never moved, but
 * sized by the person like every other column (the owner: "istersem name'i de
 * kısabilir olmalıyım"). The star is a control, not a value: not hideable, not
 * resizable, pinned last beside the row menu.
 */
export type ColumnId = 'name' | 'type' | 'location' | 'owner' | 'modified' | 'remaining' | 'size' | 'star';

// ── per-folder memory ─────────────────────────────────────────────────

/** One folder's remembered setup. Short keys: the whole map is re-serialised
 *  on every write. */
export interface FolderPrefs {
  /** view mode */
  v?: ViewMode;
  /** sort key */
  k?: SortKey;
  /** sort direction */
  d?: SortDir;
  /** this folder's own columns — absent = the default's */
  c?: ColsState<ColumnId>;
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
 * ⚠⚠ Nothing is applied to a folder until it has: a folder whose document has
 * not arrived follows the first-paint cache and is corrected once, exactly
 * when the answer is known. A caller with NO user degrades cleanly: `load`
 * resolves null, `ready` still becomes true, and nothing is ever written.
 */
const ready = ref(false);
/** True when there is somebody to save for. False = session-only. */
const persistable = ref(false);

export function viewPrefsReady(): boolean {
  return ready.value;
}

/**
 * ⚠ DEPRECATED — always true. The per-folder memory used to be an opt-in,
 * off by default, and that default was half of the leak the owner reported
 * (see the header). Kept exported so an embedder that read it keeps
 * compiling; a host that genuinely wants no per-folder memory passes
 * `config.rememberFolderView: false` to the explorer, which is the host's
 * decision rather than a hidden switch in a person's settings.
 */
export function folderMemoryEnabled(): boolean {
  return true;
}

/** ⚠ DEPRECATED — a no-op. See `folderMemoryEnabled`. */
export function setFolderMemoryEnabled(_on: boolean): void {
  void _on;
}

type FolderMap = Record<string, FolderPrefs>;

function nowSec(): number {
  return Math.floor(Date.now() / 1000);
}

function isViewMode(v: unknown): v is ViewMode {
  return v === 'list' || v === 'grid' || v === 'gallery';
}

function isSortKey(v: unknown): v is SortKey {
  return v === 'name' || v === 'type' || v === 'modified' || v === 'size';
}

function isSortDir(v: unknown): v is SortDir {
  return v === 'asc' || v === 'desc';
}

/** A column state is worth storing only if it says something. */
function colsSaySomething(c: ColsState<ColumnId> | undefined): c is ColsState<ColumnId> {
  return !!c && (Object.keys(c.w).length > 0 || c.hidden.length > 0 || (c.o?.length ?? 0) > 0);
}

/** The map, in memory. A ref so the column menu's per-folder rows re-render
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
    if (isSortKey(e.k)) entry.k = e.k;
    if (isSortDir(e.d)) entry.d = e.d;
    if (e.c !== undefined) {
      const c = readCols(e.c, COLUMNS);
      if (colsSaySomething(c)) entry.c = c;
    }
    // An entry that remembers nothing is noise from a half-written document.
    if (entry.v || entry.k || entry.c) out[k] = entry;
  }
  return out;
}

/**
 * HOW MANY FOLDERS WE REMEMBER, and how the number was chosen.
 *
 * ⚠⚠ Without a cap this is one entry per folder ever changed, forever, in a
 * database row replicated into every backup; the endpoint refuses a document
 * over 128 KB, so left uncapped saving would eventually stop working,
 * silently, for the people who use the product most.
 *
 * 300 is derived: a view-only entry serialises to ~110 bytes and one that also
 * carries its own columns to ~260, so even 300 of the heavier kind is ~78 KB —
 * inside the endpoint's limit with room for longer paths. And the map only
 * gains an entry when somebody CHANGES a folder (walking through one writes
 * nothing), so in real use the cap is a ceiling on the pathological case.
 */
export const FOLDER_CAP = 300;

/** Drop the least-recently-USED entries until the map fits the cap. ⚠ Used,
 *  not written: `touchFolder` bumps `t` on open too, so a folder you keep
 *  visiting does not age out merely because you have not re-sorted it. */
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
 * ⚠⚠ Read filex issue #21 before changing this. A storage's NAME is editable,
 * so it is not a stable address; every protocol accepts a storage's immutable
 * `uid` as the first path segment for exactly that reason. This takes the
 * storage REF the host gave us — name or uid — and the moment
 * `config.storages[].uid` is populated, keys become rename-proof. Until then
 * renaming a storage loses its folders' memory (they fall back to the
 * default — a mild, self-healing loss).
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
  /* ⚠ No trailing slash on a storage ROOT: `thumbfix/` and `thumbfix` would
   * be two keys for one folder — measured, the seeded default for `.recent`
   * never fired because the explorer asked for `.recent/`. */
  return rel ? `${ref}/${rel}` : ref;
}

/**
 * The key for the listing of every storage at once (the multi-storage root).
 * It is a place like any other — a person who sorts their drives by name
 * expects them to stay sorted — so it gets a key of its own rather than
 * falling through to "no folder", which would write the person's DEFAULT.
 */
export const ROOT_FOLDER_KEY = '.root';

/**
 * What a folder with NO memory of its own opens as, when the answer is not
 * simply the default. Only Recent is in here: its promise is "the things you
 * were just in", so opening it alphabetically buries the file the person came
 * back for. A SEED, not an override — the moment someone sorts Recent by name,
 * that is stored against `.recent` like any other folder's choice.
 */
const SEEDED: Record<string, Omit<FolderPrefs, 't'>> = {
  '.recent': { k: 'modified', d: 'desc' },
};

/** This folder's OWN memory (or its seed), or null — never a default. */
export function folderPrefs(key: string): Omit<FolderPrefs, 't'> | null {
  if (!key) return null;
  const hit = folderMap.value[key];
  if (hit) {
    const { t: _t, ...rest } = hit;
    void _t;
    const seed = SEEDED[key];
    /* A folder that remembers only its view mode keeps its seeded sort. */
    return seed && !rest.k ? { ...seed, ...rest } : rest;
  }
  return SEEDED[key] ? { ...SEEDED[key] } : null;
}

/** True when this folder has a memory the PERSON made (not a seed). */
export function folderIsRemembered(key: string): boolean {
  return !!key && !!folderMap.value[key];
}

/** How many folders are remembered — "Apply to all folders" says so, because
 *  an escape hatch that does not say what it throws away is a trap. */
export function rememberedCount(): number {
  return Object.keys(folderMap.value).length;
}

/**
 * Remember a change against this folder — ONLY the fields that changed.
 *
 * ⚠ The ONLY thing that creates an entry. Navigation does not (see
 * `touchFolder`), so the map is the set of folders somebody deliberately set
 * up rather than a log of everywhere they have been.
 *
 * ⚠ A patch, merged. Switching a folder to grid records `v` and nothing else,
 * so the folder's SORT still follows the default: a person who later changes
 * their default sort sees it in every folder whose sort they never touched,
 * including the ones whose view mode they did.
 */
export function rememberFolder(key: string, patch: Omit<FolderPrefs, 't'>): void {
  if (!key) return;
  const prev = folderMap.value[key];
  const next: FolderPrefs = { ...(prev ?? {}), ...patch, t: nowSec() };
  if (patch.c !== undefined && !colsSaySomething(patch.c)) delete next.c;
  if (!next.v && !next.k && !next.c) {
    // Nothing left to remember (a reset of the only thing this folder kept).
    if (prev) forgetFolder(key);
    return;
  }
  folderMap.value = { ...folderMap.value, [key]: next };
  evict();
  scheduleSave();
}

/**
 * Mark a remembered folder as used, for the LRU clock — and ONLY if it is
 * already remembered. ⚠ The guard is the point: bumping an absent key would
 * create an entry for every folder anyone opens.
 */
export function touchFolder(key: string): void {
  if (!key) return;
  const prev = folderMap.value[key];
  if (!prev) return;
  const t = nowSec();
  if (prev.t === t) return;
  folderMap.value = { ...folderMap.value, [key]: { ...prev, t } };
  scheduleSave();
}

/** Forget one folder: it goes back to following the default. */
export function forgetFolder(key: string): void {
  if (!key || !folderMap.value[key]) return;
  const next = { ...folderMap.value };
  delete next[key];
  folderMap.value = next;
  scheduleSave();
}

/** Forget every folder's own view. */
export function forgetAllFolders(): void {
  folderMap.value = {};
  scheduleSave();
}

// ── the defaults: the person's, then the instance's ───────────────────

/** A person's default folder view. */
export interface FolderViewDefault {
  v?: ViewMode;
  k?: SortKey;
  d?: SortDir;
  /** default columns (widths, hidden, order) */
  c?: ColsState<ColumnId>;
}

/**
 * The instance's default folder view — the OPERATOR's answer, set in the admin
 * settings (`ui.default_folder_view`) and handed to the client beside the
 * person's document. It carries no widths: a width is about a screen and the
 * operator does not know anybody's; which columns show is a fair instance-wide
 * call, so that part is here.
 */
export interface InstanceFolderDefault {
  v?: ViewMode;
  k?: SortKey;
  d?: SortDir;
  hidden?: ColumnId[];
}

const personDefault = ref<FolderViewDefault>({});

/**
 * ⚠⚠ NOT IN THE DOCUMENT, and that is the lesson the palette taught this same
 * release (web/src/lib/instanceThemes.ts `applyInstanceDefault`): the instance
 * default is the operator's answer, not the person's choice, so APPLYING it
 * must never RECORD it as the person's preference. Kept in its own ref, fed by
 * the server on every load, never saved. A person who has not chosen keeps
 * following the operator — including when the operator changes their mind.
 */
const instanceDefault = ref<InstanceFolderDefault>({});

function normalisePersonDefault(raw: unknown): FolderViewDefault {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return {};
  const p = raw as Record<string, unknown>;
  const out: FolderViewDefault = {};
  if (isViewMode(p.v)) out.v = p.v;
  if (isSortKey(p.k)) {
    out.k = p.k;
    if (isSortDir(p.d)) out.d = p.d;
  }
  if (p.c !== undefined) {
    const c = readCols(p.c, COLUMNS);
    if (colsSaySomething(c)) out.c = c;
  }
  return out;
}

/** Parse the operator's default — an object, or the JSON string the settings
 *  table stores. Anything unreadable is "no instance default". */
export function readInstanceFolderDefault(raw: unknown): InstanceFolderDefault {
  let obj = raw;
  if (typeof raw === 'string') {
    try {
      obj = JSON.parse(raw);
    } catch {
      return {};
    }
  }
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return {};
  const p = obj as Record<string, unknown>;
  const out: InstanceFolderDefault = {};
  if (isViewMode(p.v)) out.v = p.v;
  if (isSortKey(p.k)) {
    out.k = p.k;
    if (isSortDir(p.d)) out.d = p.d;
  }
  if (Array.isArray(p.hidden)) {
    const hid = p.hidden.filter(
      (x) => typeof x === 'string' && COLUMNS.some((c) => c.id === x && c.hideable),
    ) as ColumnId[];
    if (hid.length) out.hidden = [...new Set(hid)];
  }
  return out;
}

/** The person's own default, as stored. Reactive. */
export function personFolderDefault(): FolderViewDefault {
  return personDefault.value;
}

/**
 * Set parts of the person's default; an `undefined` or `null` field CLEARS
 * that part, so it follows the instance default again.
 *
 * ⚠ Never called by a click in a folder — only by the settings control and by
 * "Apply to all folders". That is the whole fix.
 */
export function setPersonFolderDefault(
  patch: Partial<Record<keyof FolderViewDefault, unknown>>,
): void {
  const next: Record<string, unknown> = { ...personDefault.value };
  for (const [k, v] of Object.entries(patch)) {
    if (v === undefined || v === null) delete next[k];
    else next[k] = v;
  }
  personDefault.value = normalisePersonDefault(next);
  mirrorDefault();
  scheduleSave();
}

/**
 * THE ESCAPE HATCH — "Apply to all folders".
 *
 * Makes the setup on screen the person's DEFAULT and forgets every folder's
 * own memory, so every folder now opens the way this one does — which is what
 * the words promise. ⚠ It has to WRITE the default now: the old model got this
 * for free because every click already wrote a global default, and that free
 * write is the leak this file exists to close.
 */
export function applyToAllFolders(view: FolderViewDefault): void {
  personDefault.value = normalisePersonDefault({ ...personDefault.value, ...view });
  folderMap.value = {};
  mirrorDefault();
  scheduleSave();
}

/** The operator's default. Reactive. */
export function instanceFolderDefault(): InstanceFolderDefault {
  return instanceDefault.value;
}

/** Hand the module the operator's default (the transport does, on load). */
export function setInstanceFolderDefault(raw: unknown): void {
  instanceDefault.value = readInstanceFolderDefault(raw);
  mirrorDefault();
}

/**
 * What an UNTOUCHED folder opens as — the person's default, else the
 * instance's, field by field. `undefined` fields mean "filex's built-in
 * default", which the caller owns (the explorer's host may pass its own
 * `config.viewMode`).
 *
 * ⚠ Sort is resolved as a PAIR: a person who chose "Size" and an operator
 * who chose "Modified ↓" must not produce "Size ↓".
 */
export function defaultFolderView(): { v?: ViewMode; k?: SortKey; d?: SortDir } {
  const p = personDefault.value;
  const i = instanceDefault.value;
  const out: { v?: ViewMode; k?: SortKey; d?: SortDir } = {};
  const v = p.v ?? i.v;
  if (v) out.v = v;
  if (p.k) {
    out.k = p.k;
    if (p.d) out.d = p.d;
  } else if (i.k) {
    out.k = i.k;
    if (i.d) out.d = i.d;
  }
  return out;
}

/**
 * The folder's view, resolved: its own memory (or seed), then the defaults.
 * View mode and sort resolve independently — a folder that only remembers
 * "grid" still follows the default sort.
 */
export function resolveFolderView(key: string): { v?: ViewMode; k?: SortKey; d?: SortDir } {
  const own = folderPrefs(key);
  const def = defaultFolderView();
  const out: { v?: ViewMode; k?: SortKey; d?: SortDir } = {};
  const v = own?.v ?? def.v;
  if (v) out.v = v;
  if (own?.k) {
    out.k = own.k;
    if (own.d) out.d = own.d;
  } else if (def.k) {
    out.k = def.k;
    if (def.d) out.d = def.d;
  }
  return out;
}

// ── the columns ───────────────────────────────────────────────────────

export type ColumnSpec = TableColumnSpec<ColumnId>;

/**
 * The row's tracks, in shipped order.
 *
 * ⚠⚠ There is no `priority` field and nothing is shed for width: the owner
 * rejected a table that dropped columns and said "no room" — "kenara devam
 * eden bir scroll getirsin". A table wider than its pane scrolls sideways.
 */
export const COLUMNS: readonly ColumnSpec[] = [
  { id: 'name', width: 260, min: 120, max: 900, hideable: false, resizable: true },
  { id: 'type', width: 88, min: 64, max: 220, hideable: true, resizable: true },
  { id: 'location', width: 160, min: 80, max: 420, hideable: true, resizable: true },
  { id: 'owner', width: 104, min: 72, max: 260, hideable: true, resizable: true },
  { id: 'modified', width: 160, min: 96, max: 300, hideable: true, resizable: true },
  /* The Trash only: how long an item has left before it is purged. */
  { id: 'remaining', width: 112, min: 80, max: 220, hideable: true, resizable: true },
  { id: 'size', width: 88, min: 64, max: 200, hideable: true, resizable: true },
  { id: 'star', width: 24, min: 24, max: 24, hideable: false, resizable: false },
] as const;

/** The two fixed tracks, in px, so the table arithmetic can be exact. */
export const FIXED_TRACKS = { check: 28, menu: 28 } as const;

/** How narrow a person may drag Name: the tile, its gap and ~11 characters. */
export const NAME_MIN = 120;

/** What Name opens at when nobody has sized anything — ~26 characters at
 *  13px/500, enough for `Quarterly Report Q2.pdf` to read whole. */
export const NAME_AUTO = 220;

/** The explorer's table metrics — a tick column, the 28px ⋮, Name opening at
 *  `NAME_AUTO` — handed to `ColumnStore.layout` by the list view. */
export const EXPLORER_METRICS = {
  gap: 8,
  padding: 24,
  check: FIXED_TRACKS.check,
  menu: FIXED_TRACKS.menu,
  leadAuto: NAME_AUTO,
} as const;

/**
 * The column state a folder with no columns of its own shows: the person's
 * default columns, else the instance's hidden set, else the shipped layout.
 */
function defaultCols(): ColsState<ColumnId> {
  if (personDefault.value.c) return personDefault.value.c;
  const hid = instanceDefault.value.hidden;
  return hid && hid.length ? { w: {}, hidden: [...hid] } : emptyCols<ColumnId>();
}

/**
 * THE EXPLORER'S COLUMN STORE for one folder — the generic store
 * (`lib/tableColumns`) over the explorer's columns, backed by this folder's
 * memory with the default beneath it.
 *
 * ⚠ `''` is not a folder: it is the person's DEFAULT layout. A listing reaches
 * it only when its host switched the per-folder memory off
 * (`config.rememberFolderView: false`), and for such a host "one arrangement
 * everywhere" is exactly what it asked for.
 *
 * ⚠ "Reset columns" in a folder means "follow my default here again": the
 * store writes an empty state and `rememberFolder` turns that into dropping
 * the folder's `c`, not into pinning the SHIPPED layout on this folder.
 */
export function folderColumnStore(keyOf: () => string): ColumnStore<ColumnId> {
  return createColumnStore<ColumnId>(
    () => COLUMNS,
    {
      get: () => {
        const key = keyOf();
        if (!key) return defaultCols();
        return folderMap.value[key]?.c ?? defaultCols();
      },
      set: (next) => {
        const key = keyOf();
        if (!key) {
          setPersonFolderDefault({ c: colsSaySomething(next) ? next : undefined });
          return;
        }
        rememberFolder(key, { c: next });
      },
      customised: () => {
        const key = keyOf();
        if (!key) return colsSaySomething(personDefault.value.c);
        return !!folderMap.value[key]?.c;
      },
    },
    { lead: 'name' },
  );
}

/**
 * The person's DEFAULT layout as a store — what the module-level column
 * functions below operate on (a host with the per-folder memory off, and the
 * tests that drive the arithmetic). Created once, for the life of the module.
 */
const defaultColumns = folderColumnStore(() => '');

export function columnOrder(): ColumnId[] {
  return defaultColumns.order();
}

/** THE REORDER ARITHMETIC — `lib/tableColumns.reorderColumns`, re-exported. */
export const reorderColumns = genericReorder;

/** The stretch of an order a movable explorer column may occupy. */
export function columnDropBand(order: readonly ColumnId[]): { lo: number; hi: number } {
  return genericDropBand(order, COLUMNS);
}

export function moveColumn(id: ColumnId, index: number): void {
  defaultColumns.move(id, index);
}

/** One step left or right among the movable columns of the default layout. */
export function moveColumnBy(id: ColumnId, delta: -1 | 1): void {
  const cur = columnOrder().filter((c) => COLUMNS.find((s) => s.id === c)?.hideable);
  const at = cur.indexOf(id);
  if (at === -1) return;
  const to = at + delta;
  if (to < 0 || to >= cur.length) return;
  const neighbour = cur[to];
  defaultColumns.move(id, columnOrder().indexOf(neighbour));
}

export function canMoveColumn(id: ColumnId, delta: -1 | 1): boolean {
  const cur = columnOrder().filter((c) => COLUMNS.find((s) => s.id === c)?.hideable);
  const at = cur.indexOf(id);
  if (at === -1) return false;
  return at + delta >= 0 && at + delta < cur.length;
}

export function columnWidth(id: ColumnId): number {
  return defaultColumns.width(id);
}

export function setColumnWidth(id: ColumnId, px: number): void {
  defaultColumns.setWidth(id, px);
}

export function widthsAreAuto(): boolean {
  return defaultColumns.widthsAreAuto();
}

export function freezeWidths(widths: Partial<Record<ColumnId, number>>): void {
  defaultColumns.freeze(widths);
}

export function columnHidden(id: ColumnId): boolean {
  return defaultColumns.hidden(id);
}

export function setColumnHidden(id: ColumnId, hidden: boolean): void {
  defaultColumns.setHidden(id, hidden);
}

export function resetColumns(): void {
  defaultColumns.reset();
}

export function columnsCustomised(): boolean {
  return defaultColumns.customised();
}

/** What one explorer table looks like right now. */
export interface TableLayout {
  visible: ColumnId[];
  widths: Record<ColumnId, number>;
  total: number;
  auto: boolean;
}

/** The explorer's table arithmetic over the DEFAULT layout. A folder's own
 *  layout is `folderColumnStore(key).layout(…, EXPLORER_METRICS)`. */
export function tableLayout(
  available: number,
  candidates: readonly ColumnId[],
  opts: { gap?: number; padding?: number } = {},
): TableLayout {
  return defaultColumns.layout(available, candidates, { ...EXPLORER_METRICS, ...opts });
}

// ── the first-paint cache ─────────────────────────────────────────────

/**
 * The localStorage names the FIRST PAINT reads (FileExplorer's view mode,
 * lib/sortOrder's sort). ⚠ Caches of the resolved DEFAULT, never the answer:
 * the document cannot arrive before the first frame, and a listing that paints
 * name-ascending and then jumps would flash. ⚠⚠ Written from the DEFAULT only,
 * never from a click — a click in folder A written here would paint folder B
 * in A's view on the next load before the document corrected it, which is the
 * leak again for half a second.
 */
const MIRRORS = {
  sort: 'filex.list-sort',
  view: 'brf-file-explorer:view-mode',
} as const;

function mirrorDefault(): void {
  const d = defaultFolderView();
  try {
    if (d.k) {
      const dir = d.d ?? (d.k === 'modified' ? 'desc' : 'asc');
      localStorage.setItem(MIRRORS.sort, JSON.stringify({ key: d.k, dir }));
    } else localStorage.removeItem(MIRRORS.sort);
    if (d.v) localStorage.setItem(MIRRORS.view, d.v);
    else localStorage.removeItem(MIRRORS.view);
  } catch {
    /* private mode / quota — the first paint falls back to the product's
       default until the document lands */
  }
}

// ── the document ─────────────────────────────────────────────────────
//
//   { "f": { "<storage>/<rel>": {v,k,d,c,t}, … },   per folder, LRU-capped
//     "p": { v, k, d, c },                          the person's default
//     "u": <epoch ms>,                              last write
//     "on": true }                                  for older clients (below)
//
// ⚠ RETIRED KEYS, read once and never written again:
//   · `g` — the old "global default", written by every click. It is NOT
//     carried into `p`: it was never a choice anybody made on purpose, it was
//     their last click anywhere, and promoting it to "your default" would ship
//     the leak frozen into the settings screen. Untouched folders open with
//     the instance's or filex's default until the person sets their own.
//   · `c` — the old global columns. These ARE carried into `p.c` once: a
//     column width was only ever changed by a deliberate drag, and dropping
//     them would throw away work the person did on purpose.
//   · `on` — the opt-in switch. Always written `true` so an older client (a
//     desktop app not yet updated) still honours the per-folder memory it
//     finds in the same document.

interface ViewPrefsDoc {
  on?: boolean;
  f?: unknown;
  p?: unknown;
  c?: unknown;
  g?: unknown;
  u?: unknown;
  [ns: string]: unknown;
}

/** The top-level names this module owns — retired ones included, so a slot
 *  can never claim them and they are not carried through as somebody else's. */
const OWNED_KEYS = ['on', 'f', 'p', 'c', 'g', 'u'] as const;

/**
 * When this session last WROTE the document, epoch milliseconds — what "the
 * last thing I did wins" across browsers is made of. A refresh that finds a
 * NEWER stamp applies the server's document; an older or equal one keeps what
 * is on screen.
 */
const stamp = ref(0);

/**
 * ⚠⚠ EVERYTHING IN THE DOCUMENT THAT IS NOT OURS, kept verbatim. The endpoint
 * stores an opaque object; a save that rebuilt only our keys once erased the
 * desktop-app reminder's "never show me again" on the next column drag.
 */
const foreign = ref<Record<string, unknown>>({});

function takeForeign(doc: Record<string, unknown>): Record<string, unknown> {
  const rest: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(doc)) {
    if ((OWNED_KEYS as readonly string[]).includes(k)) continue;
    rest[k] = v;
  }
  return rest;
}

function currentDoc(): ViewPrefsDoc {
  /* ⚠ Spread FIRST, so the keys this module owns always win. */
  const doc: ViewPrefsDoc = {
    ...foreign.value,
    on: true,
    f: folderMap.value,
    u: stamp.value,
  };
  if (Object.keys(personDefault.value).length) doc.p = personDefault.value;
  return doc;
}

/* ── namespaced slots: room in the document for somebody else ──────────── */

/** One feature's corner of the document. */
export interface ViewPrefsSlot<T> {
  /** What is stored, or undefined — including before the document lands. */
  get(): T | undefined;
  /** Replace it. Session-only when there is nobody to save for. */
  set(value: T): void;
  /** Remove the key entirely. */
  clear(): void;
  /** Has the document been read yet? Reactive. */
  ready(): boolean;
  /** Is there an account behind this session at all? Reactive. */
  persistable(): boolean;
}

/**
 * Claim `namespace` as a top-level key in the per-user document. ⚠ The names
 * this module owns are refused outright rather than quietly renamed: a slot
 * called `c` that silently became `c2` would be a value the caller could never
 * read back.
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
 * SAVING — debounced, coalesced, fire and forget. A resize is dozens of writes
 * per second; waiting for the gesture to settle and then sending the WHOLE
 * document makes the last write the true one by construction, and a dropped
 * write self-healing.
 */
const SAVE_DEBOUNCE_MS = 800;
let saveTimer: ReturnType<typeof setTimeout> | undefined;
let saveDirty = false;

function scheduleSave(): void {
  /* ⚠ Not before the document has loaded: a save fired while the fetch is in
   * flight would write this session's empty defaults over everything the
   * person had arranged — the classic "my settings reset themselves". */
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
  // ⚠ Stamped at SEND time: the stamp answers "whose copy is newer".
  stamp.value = Date.now();
  transport.save(currentDoc());
}

/* ⚠ A change in the last moment before a tab closes is inside the debounce
 * window; `pagehide` is the last event a browser reliably delivers, and the
 * transport sends it with `keepalive`. */
if (typeof window !== 'undefined') {
  window.addEventListener('pagehide', flushSave);
  window.addEventListener('visibilitychange', () => {
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') flushSave();
  });
}

/**
 * Hand this module its transport and read the document. Calling it again is a
 * no-op: two explorers on one page share this module's state, and the admin
 * app attaches before the explorer it hosts does.
 */
export function attachViewPrefsStore(t: ViewPrefsTransport): void {
  if (transport) return;
  transport = t;
  void (async () => {
    await readDoc(true);
    ready.value = true;
    watchForFreshDocuments();
  })();
}

/**
 * Forget the account this module was reading for — sign-out. The next
 * `attachViewPrefsStore` reads the next person's document from scratch, and
 * nothing of the previous person's arrangement is left on screen for them.
 */
export function detachViewPrefsStore(): void {
  flushSave();
  resetState();
}

/** Is a transport attached? */
export function viewPrefsAttached(): boolean {
  return transport !== null;
}

const applied = new Set<() => void>();

/** Be told when the stored preferences have been applied (the first load and
 *  every refresh that wins). */
export function onViewPrefsApplied(fn: () => void): () => void {
  applied.add(fn);
  return () => applied.delete(fn);
}

function announceApplied(): void {
  for (const fn of applied) {
    try {
      fn();
    } catch {
      /* one surface's mistake is not another's */
    }
  }
}

/**
 * Read the document and apply it — unless what is on screen is newer.
 *
 * ⚠⚠ `first` is the mount. Afterwards this is the FRESHNESS half: a tab that
 * read the document once at boot never sees what the other browser did, so
 * the refresh runs when the tab comes back to the front.
 */
async function readDoc(first: boolean): Promise<void> {
  if (!transport) return;
  let doc: unknown = null;
  try {
    doc = await transport.load();
  } catch {
    /* No account, offline, a 500 — all one answer: remember nothing this
       session and never write, so a transient failure cannot erase what is
       already stored. */
    doc = null;
  }
  if (!doc || typeof doc !== 'object' || Array.isArray(doc)) {
    // Still tell the surfaces: the instance default may have arrived with it.
    mirrorDefault();
    announceApplied();
    return;
  }
  const d = doc as ViewPrefsDoc;
  const theirs = typeof d.u === 'number' && Number.isFinite(d.u) ? d.u : 0;

  if (!first) {
    // ⚠ A change this session has not sent yet OUTRANKS the server's copy:
    // the person is mid-gesture and their own tab must not be overwritten by
    // what they did five minutes ago somewhere else.
    if (saveDirty) {
      flushSave();
      return;
    }
    // ⚠ Strictly newer. Equal stamps mean "our own document coming back".
    if (theirs <= stamp.value) return;
  }

  folderMap.value = readFolderMap(d.f);
  let migrated = false;
  if (d.p !== undefined) {
    personDefault.value = normalisePersonDefault(d.p);
  } else {
    /* ONE-TIME MIGRATION of the retired global columns (see the note above
       the document shape): a deliberate drag is kept as the person's default
       columns. The retired `g` is deliberately NOT migrated. */
    const c = readCols(d.c, COLUMNS);
    personDefault.value = colsSaySomething(c) ? { c } : {};
    migrated = colsSaySomething(c) || d.g !== undefined || d.c !== undefined;
  }
  stamp.value = theirs;
  /* ⚠ Whatever else was in there travels on untouched. */
  foreign.value = takeForeign(d as Record<string, unknown>);
  persistable.value = true;
  mirrorDefault();
  if (migrated) {
    /* Rewrite the document in the new shape once, so the retired `g` stops
       travelling. ⚠ `ready` before `scheduleSave`, whose guard exists to stop
       a write landing while the fetch is out — and the fetch just finished. */
    ready.value = true;
    scheduleSave();
  }
  announceApplied();
}

const REFRESH_THROTTLE_MS = 3_000;
let lastRefresh = 0;
let watching = false;

/** Re-read the document (throttled) — when a tab comes back to the front. */
export function refreshViewPrefs(force = false): void {
  if (!transport || !ready.value) return;
  const now = Date.now();
  if (!force && now - lastRefresh < REFRESH_THROTTLE_MS) return;
  lastRefresh = now;
  void readDoc(false);
}

function watchForFreshDocuments(): void {
  if (watching || typeof window === 'undefined') return;
  watching = true;
  window.addEventListener('focus', () => refreshViewPrefs());
  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') refreshViewPrefs();
    });
  }
}

function resetState(): void {
  transport = null;
  ready.value = false;
  persistable.value = false;
  folderMap.value = {};
  personDefault.value = {};
  instanceDefault.value = {};
  stamp.value = 0;
  lastRefresh = 0;
  foreign.value = {};
  saveDirty = false;
  if (saveTimer) clearTimeout(saveTimer);
  saveTimer = undefined;
}

/** Test seam: drop the transport and the state, so one suite cannot leak a
 *  document into the next. Not used by the app. */
export function __resetViewPrefs(): void {
  resetState();
  applied.clear();
}

/** Test seam: send whatever is pending now, without waiting out the debounce. */
export function __flushViewPrefs(): void {
  flushSave();
}
