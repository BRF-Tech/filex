<script setup lang="ts">
/**
 * ListView — the explorer's listing: files, in the product's ONE table.
 *
 * ⚠⚠ Since 2026-09-21 the table itself is `DataTable`, extracted from this
 * file so that every table in the product — admin pages, connection panels,
 * an app's `list` node — runs the same code this listing does. The owner:
 * "Artık explore tablomuz bizim her yerde kullanacağımız tablo yapısıdır."
 * This file is the layer ON TOP of it that knows about files: the columns
 * (Name · Type · Location · Owner · Modified · Size · ★), what each prints for
 * a file, the row's gestures (open, select, drag, drop, touch), the date
 * headings, and where the arrangement is remembered — per folder.
 *
 * Selection + context menu + double-click open are caller-owned — we just
 * emit the events and let FileExplorer.vue handle them uniformly across List
 * and Grid.
 */
import { computed, ref, watch } from 'vue';
import { hasInternalDrag } from '../lib/dragOut';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { inlineStartX } from '../lib/direction';
import { lockOf, lockWords } from '../lib/appLock';
import { linkWordsFor } from '../lib/symlink'; /* issue #34 — a link that will not open */
import { checkMod, clickMod, useRowTouch, type ClickMod } from '../composables/useRowTouch';
import {
  arrivedFromOutside,
  deletedByViewer,
  deleterIdOf,
  deleterNameOf,
  ownedByViewer,
  ownerIdOf,
  ownerNameOf,
} from '../lib/fileFilters';
import {
  encryptedFolderTile,
  fileIconTile,
  isEncryptedFolder,
  isStorageRow,
  typeLabelFor,
} from '../lib/fileIcons'; /* gorunum:v1-preview · ikon:emoji */
import {
  SORT_KEYS,
  setSortLocale,
  useSortStore,
  type ListingOrder,
  type SortKey,
} from '../lib/sortOrder'; /* surucu:d1-sort */
import { matchedInContent, snippetSegments } from '../lib/snippet'; /* bul:s3 */
import { drawsAsPage, previewKindFor } from '../lib/filePreview'; /* tablo:t1 */
import { applyDragGhost } from '../lib/dragGhost'; /* wiring:c4 */
import { parentDirOf } from '../lib/listing'; /* Location column — one split rule for every view */
import { trashTimeLeft } from '../lib/trashTimeLeft'; /* one "time left" sentence, shared with the admin Trash page */
import {
  groupByDate,
  groupingActive,
  type DateRun,
} from '../lib/dateGroups'; /* zaman:z1 · gruplama */
import {
  EXPLORER_METRICS,
  applyToAllFolders,
  folderColumnStore,
  folderIsRemembered,
  forgetFolder,
  rememberedCount,
  type ColumnId,
} from '../lib/viewPrefs'; /* tablo:t1 — per-folder columns */
import DataTable, { type DataColumn } from './DataTable.vue';
import StarButton from './StarButton.vue';
import ThumbTile from './ThumbTile.vue';

const props = defineProps<{
  files: FileNode[];
  selected: Set<string>;
  /** Paths that were cut → dim them so the user sees the pending move. */
  clipped?: Set<string>;
  /**
   * Show each row's parent directory under the basename. Caller flips
   * this on while a search filter is active so the user can tell
   * `invoice.pdf` (in 2024/) apart from `invoice.pdf` (in 2025/) when
   * both match the same query.
   */
  showParentPath?: boolean;
  locale: LocaleCode;
  loading?: boolean;
  /** Set of node IDs flagged starred by the user — render an inline
   *  filled star indicator and let the user toggle it from the row. */
  starredIds?: Set<number>;
  /**
   * Offer the star affordance at all. Follows the Starred view: when the panel
   * leaves that view out — a shared app token has no single person behind it,
   * so "your starred files" is one list shown to strangers — offering to star
   * something is offering to write into that same shared list. Owner's call,
   * 2026-09-05, verbatim (translated from Turkish): "if the starred place is
   * not visible, then star/unstar should not be visible either".
   */
  starEnabled?: boolean;
  /** Backend base URL + auth header builder forwarded to StarButton
   *  so it can POST /api/files/manager/star on click. Optional —
   *  embedders without auth wire-up pass nothing and the star column
   *  hides itself. */
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  authCredentials?: RequestCredentials;
  /** Desktop selective sync: availability badge per row (kept on this
   *  computer / syncing right now / online only). Absent on the web —
   *  no badge renders at all. */
  keepBadgeFor?: (n: FileNode) => 'kept' | 'syncing' | 'cloud' | 'partial' | null;
  /**
   * surucu:d1-sort — where these rows got their order (`lib/sortOrder`).
   *
   * `relevance` means the server ranked them: this view then draws them in
   * the order they arrived, groups nothing by date and closes its column
   * sorts. Omitted = an ordinary folder listing, sorted by the active key —
   * which is what every caller that does not search passes, including the
   * split view's secondary pane.
   */
  order?: ListingOrder;
  /**
   * tablo:t1 — the key the folder on screen is remembered under
   * (`lib/viewPrefs.folderKey`), or '' when this listing has no folder behind
   * it or the host turned the memory off. The column menu reads it for its
   * two per-folder rows and nothing else does; the memory itself is written by
   * the caller, which is the only thing that knows when a navigation ended.
   */
  folderKey?: string;
  /**
   * tablo:t1 — the resolved theme, forwarded to the column menu.
   *
   * ⚠ Needed because that menu is teleported to <body>, where the `.fe`
   * ancestor that scopes the design tokens is gone — the same reason
   * `ContextMenu` takes one, and the class it tags itself with is the same one
   * `variables.css` already re-applies dark values under.
   */
  theme?: ThemeMode;
  /**
   * tablo:t1 — the authenticated thumbnail resolver, the same one the grid and
   * the gallery are handed (`useThumbs.src`).
   *
   * ⚠ Optional, and the list draws its type tile when it is absent: an embed
   * with no auth wire-up cannot fetch a thumb at all, and a broken <img> in
   * every row would be worse than the glyph it replaced.
   */
  thumbSrc?: (n: FileNode) => string | null;
  /**
   * These rows are the TRASH.
   *
   * ⚠ A deleted item's facts are not a file's facts (QA, 2026-09-21): its
   * date column read "—" (a trashed row has no modification date to show),
   * it said nothing about how long it had left or where it had been, and its
   * Owner read "System" for every row because the trash listing carries no
   * owner. So in the Trash the date is WHEN it was deleted, Location is where
   * it was deleted FROM, a column says how long until it is gone for good, and
   * Owner and the star are not drawn — the same facts the admin's Trash page
   * shows, in the explorer's own table.
   */
  trash?: boolean;
}>();

const emit = defineEmits<{
  (e: 'click-row', node: FileNode, mod: ClickMod): void;
  (e: 'dbl-row', node: FileNode): void;
  (e: 'context-row', node: FileNode, ev: MouseEvent): void;
  (e: 'item-drag-start', node: FileNode, ev: DragEvent): void;
  (e: 'item-drop-into', target: FileNode, ev: DragEvent): void;
  (e: 'star-change', node: FileNode, value: boolean): void;
  /**
   * gorunum:v1 — the order this view actually renders.
   *
   * A shift-range is arithmetic over a list, and until now the parent did that
   * arithmetic over the BACKEND's order while the user was looking at a sorted
   * one. Sorting by size and shift-clicking two adjacent rows selected
   * whatever happened to lie between them in the server's answer. Hoisting
   * folders made it reachable without sorting anything, which is how it was
   * found. The view knows what it drew; it says so.
   */
  (e: 'display-order', nodes: FileNode[]): void;
}>();

const {
  t,
  formatNodeSize,
  nodeSizeHint,
  formatDate,
  formatDateFull,
  formatMonthYear,
  zonedYearMonth,
  toDate,
  nodeDisplayName,
  dir,
} = useLocale(() => props.locale);


/* App plugins — an app's hold on a row (docs/APP-PLUGINS-API.md → "File
   locks"). The listing already caps `perm` at viewer, so the write verbs are
   gone without this; the badge is what stops that reading as a bug. ⚠ One
   source of words for every view and the details panel (lib/appLock). */
function lockTitleOf(n: FileNode): string {
  return lockWords(lockOf(n), { t, formatDate, locale: props.locale });
}

/* issue #34 — a symlink the server will not follow. Same shape as the lock
   badge above and for the same reason: the words live in ONE module
   (lib/symlink), so the row, the details panel and the toast that explains a
   refused open cannot drift apart. `null` for every ordinary row — a link
   whose target is inside the root was already followed and arrives as that
   target, so it is not one of these. */
function linkOf(n: FileNode) {
  return linkWordsFor(n, { t });
}

function isSelected(n: FileNode): boolean {
  return props.selected.has(n.path);
}

function onRowClick(n: FileNode, ev: MouseEvent) {
  emit('click-row', n, clickMod(ev, touch.isTap(ev)));
}

function onRowDbl(n: FileNode) {
  emit('dbl-row', n);
}

function onRowCtx(n: FileNode, ev: MouseEvent) {
  // Stop bubbling so the root `@contextmenu` handler doesn't fire and
  // clear the selection we just set.
  ev.preventDefault();
  ev.stopPropagation();
  emit('context-row', n, ev);
}

/**
 * gorunum:v1 — the row's ⋮ button opens THE SAME menu the right-click opens.
 *
 * It emits `context-row` with the same node, i.e. it walks the caller's
 * existing context-menu path (`onContextTarget` → `selectionActionList`) with
 * a synthetic event carrying the button's own corner as the anchor. There is
 * deliberately no second action list here: a menu assembled beside the real
 * one drifts from it the first time an action is added, and the row's "⋮" and
 * its right-click would then offer different things on the same file
 * (filex lesson #67).
 */
function onRowMenu(n: FileNode, ev: MouseEvent) {
  const el = ev.currentTarget as HTMLElement | null;
  const box = el?.getBoundingClientRect();
  emit('context-row', n, {
    // ⚠ RTL: the button's START corner — its right one in RTL — so the menu
    // hangs from the control and opens the way the line reads.
    clientX: box ? inlineStartX(box, dir.value) : ev.clientX,
    clientY: box ? box.bottom : ev.clientY,
    preventDefault: () => {},
    stopPropagation: () => {},
  } as unknown as MouseEvent);
}

/**
 * issue #26 — the checkbox is the one click that selects (a click anywhere
 * else on the row opens it). It goes out as the same `click-row` emit every
 * other gesture uses, marked `check`, so FilePane routes it to the one
 * `useSelection.click` — one anchor, one set, the one the context menu acts
 * on. See composables/useRowTouch `checkMod`.
 */
function onCheckClick(n: FileNode, ev: MouseEvent) {
  emit('click-row', n, checkMod(ev));
}

function onItemDragStart(n: FileNode, ev: DragEvent) {
  /* wiring:c4 — custom ghost (name + multi-select count badge). Visual
   * only; the payload/selection logic stays in FileExplorer. */
  applyDragGhost(
    ev,
    nodeDisplayName(n),
    props.selected.has(n.path) ? props.selected.size : 1,
  );
  emit('item-drag-start', n, ev);
}

/* wiring:c4 — droptarget highlight: the folder row currently hovered by an
 * internal drag gets `.is-droptarget`. Purely visual — the accept/deny
 * decision below is untouched (files never preventDefault, so the browser
 * shows the native no-drop cursor on invalid targets). */
const dropTargetPath = ref<string | null>(null);

function onItemDragOver(n: FileNode, ev: DragEvent) {
  if (n.type !== 'dir') return;
  if (!hasInternalDrag(ev)) return;
  ev.preventDefault();
  ev.stopPropagation();
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
  dropTargetPath.value = n.path; /* wiring:c4 */
}

/* wiring:c4 */
function onItemDragLeave(n: FileNode) {
  if (dropTargetPath.value === n.path) dropTargetPath.value = null;
}

function onItemDrop(n: FileNode, ev: DragEvent) {
  dropTargetPath.value = null; /* wiring:c4 */
  if (n.type !== 'dir') return;
  if (!hasInternalDrag(ev)) return;
  ev.preventDefault();
  ev.stopPropagation();
  emit('item-drop-into', n, ev);
}

/* Long press → the row's menu; a tap is reported as a tap (issue #26). */
const touch = useRowTouch<FileNode>(
  (n, at) =>
    emit('context-row', n, { ...at, preventDefault: () => {}, stopPropagation: () => {} } as unknown as MouseEvent),
  {
    // A finger lifted on the item, off its controls, opens at touchend — see useRowTouch.
    onTap: (n) => emit('click-row', n, { ctrl: false, shift: false, touch: true }),
  },
);

function keepGlyph(b: 'kept' | 'syncing' | 'cloud' | 'partial'): string {
  if (b === 'kept') return '\u2713';
  if (b === 'syncing') return '\u27f3';
  if (b === 'partial') return '\u25d0';
  return '\u2601';
}

function isPinnedSpecial(n: FileNode): boolean {
  return n.basename === '.trash' || isStorageRow(n);
}

function displayDate(ms: number | undefined): string {
  return formatDate(ms, { time: true }) || '—';
}

/**
 * zaman:z1 — the hover text behind the date cell: the whole instant, named
 * with the zone it is being read in. The cell itself has room for
 * "Sep 12, 2026, 3:14 AM" and no room to say whose 3:14 that is.
 */
function fullDate(ms: number | undefined): string {
  return formatDateFull(ms);
}

/**
 * gorunum:v1-preview — the Type column names the KIND: `TypeScript`,
 * `Spreadsheet`, `PDF document`, `Folder`.
 *
 * ⚠ This reverses the note that stood here, which argued the raw extension in
 * caps was "the only kind fact a listing actually carries" and that a name
 * table would only repeat what the coloured tile says. Both halves were wrong.
 * `TS` is not a fact about the kind, it is the suffix someone typed; and the
 * tile says *family* (one glyph covers .ts, .go, .css and .sql), which is
 * exactly the distinction a column headed "Type" is there to make. The owner
 * asked for the words, and the words are strictly more informative.
 *
 * The table itself is in `lib/fileIcons` beside the extension → family map it
 * is built on, so this column, the info panel and anything else that names a
 * kind read one answer (filex lesson #67). Everything unmapped still falls
 * back to the uppercased extension — no empty cells.
 *
 * The pinned virtual rows (Trash, a storage drive) keep their em dash: Trash
 * is a `type: 'dir'` sentinel, and calling it a folder in this column is the
 * sort of small lie that sends someone looking for it on disk.
 */
function typeLabel(n: FileNode): string {
  if (isPinnedSpecial(n)) return '—';
  return typeLabelFor(n, t);
}

/**
 * tablo:t1 — THE ROW'S PICTURE, where there is a real one to show.
 *
 * The grid and the gallery have drawn true thumbnails for a while; the list
 * drew a type glyph for every row, including a 38 KB photograph. The data was
 * always there and the endpoint always worked — the list simply never asked.
 *
 * ⚠⚠ It asks under exactly the grid's condition, not a second one of its own.
 * `previewKindFor` is non-null for anything we can read as text, and for those
 * the backend's "thumbnail" is a generated rectangle with the extension
 * stencilled on it — the same three letters the Type column prints in words
 * two inches to the right. Skipping the call for them is what keeps this
 * costing zero extra requests rather than one per row; copying the rule rather
 * than restating it is what stops the list and the grid disagreeing about
 * which files have pictures (filex lesson #67).
 */
function thumbOf(n: FileNode): string | null {
  if (n.type === 'dir') return null;
  if (previewKindFor(n)) return null;
  return props.thumbSrc ? props.thumbSrc(n) : (n.thumb_url ?? null);
}

/** Whether the Type column is on screen. While it is, it already says "Folder"
 *  in its own cell, and the word beside the name printed it twice per row. */
const typeColumnShown = computed(() => visibleCols.value.includes('type'));

/** The muted "Folder" said beside a directory's name — not on the sentinels,
 *  and only while no Type column says it (see typeColumnShown). */
function isPlainDir(n: FileNode): boolean {
  return n.type === 'dir' && !isPinnedSpecial(n);
}

/**
 * tablo:t1 — WHERE THE ROW LIVES, for the Location column.
 *
 * ⚠⚠ This used to be a second line printed UNDER the file's name, and that is
 * the defect the owner reported, verbatim: "name yazılarının altında uzun tire
 * görüyorum aynı modified'da olduğu gibi… her değer kendi sütununda gözükmesi
 * gerekmekte" — a long dash under the name text, and every value belongs in
 * its own column. Measured on Recent, where every row sits at its storage's
 * root: the parent folder was '' for all of them, the template printed the
 * `|| '—'` fallback, and eleven em dashes hung under eleven filenames. A value
 * with no column of its own has nowhere to be absent from, so it had to invent
 * a placeholder; give it a column and "no sub-folder" is simply an empty cell.
 *
 * The storage leads, so this reads the way the reference build's own Location
 * column does (`demo`, `demo/Design`) and a row at the root of a drive still
 * says which drive.
 *
 * ⚠ The folder comes from `parentDirOf` (lib/listing), which splits on the
 * first `://`. This file had its own copy that stripped a URL-scheme pattern
 * instead, and a storage NAME is not a scheme: for `My files` nothing was
 * stripped and this printed `My files/My files://Photos` (2026-09-14).
 */
function locationLabel(n: FileNode): string {
  const dir = parentDirOf(n.path);
  const store = String((n as Record<string, unknown>).storage ?? '').trim();
  return [store, dir].filter(Boolean).join('/');
}

/**
 * The Trash's "time left": the server's own count of whole days before the
 * item is purged (`ttl_days`, trash/service.go — retention minus the days
 * since deletion, never below zero). No count, no claim.
 */
function remainingLabel(n: FileNode): string {
  const meta = (n as Record<string, unknown>).extra_metadata as { ttl_days?: number | null } | undefined;
  const ttl = meta?.ttl_days;
  return trashTimeLeft(ttl, t);
}

/* bul:s3 — search-result enrichment. The v0.2 backend inlines `snippet`
 * (plain text, «» highlights) + `matched` on search hits; regular listings
 * never carry them, so presence-gating keeps normal rows untouched and an
 * older backend simply renders nothing extra. */
function rowSnippet(n: FileNode): string {
  const s = (n as Record<string, unknown>).snippet;
  return typeof s === 'string' ? s : '';
}

function rowInContent(n: FileNode): boolean {
  return matchedInContent((n as Record<string, unknown>).matched);
}
// ------------------------------------------------------------------
// Sorting.
//
// ⚠⚠ NOT local to the list view. The key, the direction, the click cycle and
// the comparator live in `lib/sortOrder`, the store of the PANE this list is
// inside, which the filter row's sort control also drives — so these headers
// and that control are two handles on ONE piece of state (filex lesson #67).
// The table draws the headers and reports a click; the store decides.
//
// ⚠⚠ `order === 'relevance'` says the server ranked this listing: no key is
// applied, no column claims to be sorting, and the headers CLOSE and say why.
// A header click over a ranked listing would set the pane's key, move nothing
// at all, and then re-order the FOLDER the person returns to — a control
// whose only effect is somewhere the user is not looking.
// ------------------------------------------------------------------

/* ⚠⚠ The store of the PANE this list is inside, not a module-level singleton:
 * two panes of a split each carry a sort control, and one shared answer means
 * the control in the right pane re-orders the left one. */
const sort = useSortStore();
const sortKey = computed<SortKey>(() => sort.key.value);
/** These rows are the server's ranked answer — see the `order` prop. */
const ranked = computed(() => props.order === 'relevance');

/* `type` sorts by the word this very column prints, so the comparator needs
 * the catalogue that word comes from. */
watch(() => props.locale, (l) => setSortLocale(l), { immediate: true });

const tableSort = computed(() =>
  ranked.value ? null : { key: sortKey.value as string, dir: sort.dir.value },
);

function onSort(p: { key: string; dir: 'asc' | 'desc' }) {
  /* The table computed a direction too, but the STORE owns the click
     vocabulary (`chooseSortKey`: a new key arrives in its own default
     direction, the active one flips) — handing it the key keeps one answer. */
  if (ranked.value) return;
  if ((SORT_KEYS as readonly string[]).includes(p.key)) sort.chooseSortKey(p.key as SortKey);
}

// ------------------------------------------------------------------
// The columns.
//
// ⚠⚠ THE TABLE ITSELF IS `DataTable` — the product's one table, which this
// view used to BE. Everything that is not about files moved there on
// 2026-09-21 (resizing, reordering, the column menu, the frozen edges, the
// sideways scroll) so that every other table in the product runs the same
// code rather than an imitation of it. What stays here is what only a file
// listing knows: which columns exist, what a cell prints for a file, the row's
// gestures, and where the arrangement is remembered — per FOLDER.
// ------------------------------------------------------------------

/** Which optional columns this listing is willing to draw at all, before any
 *  question of the person's own choices. */
const candidateCols = computed<ColumnId[]>(() => {
  /* The Trash has no owner to show (its listing carries none), and it draws
     WHO DELETED IT on that track instead — the way the date track says when
     it was deleted and the location track where from. */
  if (props.trash) return ['type', 'location', 'owner', 'modified', 'remaining', 'size'];
  const out: ColumnId[] = ['type'];
  /* Location only where the rows come from more than one folder. In an
     ordinary folder every row shares one location and the column would be a
     wall of the same string. */
  if (props.showParentPath) out.push('location');
  out.push('owner', 'modified', 'size');
  if (props.starEnabled !== false && (!!props.apiBase || props.apiBase === '')) out.push('star');
  return out;
});

/**
 * THIS FOLDER's columns (`lib/viewPrefs.folderColumnStore`): its own, else the
 * person's default, else the instance's, else the shipped layout. A column
 * dragged wider here stays here — the owner, 2026-09-21: "Explore içindeki
 * değişikliklerimiz o klasör özelinde olmalı."
 *
 * ⚠ Built once, reading `folderKey` through a getter, so a navigation swaps
 * the arrangement on screen without rebuilding the store.
 */
const cols = folderColumnStore(() => props.folderKey ?? '');

/** The optional columns on screen, in the person's order — the same list the
 *  table draws (width never sheds a column). */
const visibleCols = computed<ColumnId[]>(() =>
  cols
    .order()
    .filter((id) => id !== 'name' && candidateCols.value.includes(id) && !cols.hidden(id)),
);

/** The column definitions the table draws, in the explorer's own classes so
 *  every rule written against `fe-list__col--*` keeps applying. */
const columns = computed<DataColumn<FileNode>[]>(() => [
  { id: 'name', label: t('col.name'), lead: true, sortable: true, class: 'fe-list__col--name' },
  { id: 'type', label: t('col.type'), sortable: true, class: 'fe-list__col--type', format: typeLabel },
  {
    id: 'location',
    label: props.trash ? t('col.deleted_from') : t('col.location'),
    class: 'fe-list__col--location',
    format: locationLabel,
    title: (n) => locationLabel(n) || undefined,
  },
  {
    id: 'owner',
    label: props.trash ? t('col.deleted_by') : t('col.owner'),
    class: 'fe-list__col--owner',
    format: props.trash ? deleterLabel : ownerLabel,
    title: props.trash ? deleterTitle : ownerTitle,
  },
  {
    id: 'modified',
    label: props.trash ? t('col.deleted') : t('col.modified'),
    sortable: true,
    sortDir: 'desc',
    class: 'fe-list__col--mod',
    format: (n) => displayDate(n.last_modified),
    title: (n) => fullDate(n.last_modified),
  },
  {
    id: 'remaining',
    label: t('col.remaining'),
    class: 'fe-list__col--remaining',
    format: remainingLabel,
  },
  {
    id: 'size',
    label: t('col.size'),
    sortable: true,
    class: 'fe-list__col--size',
    // A folder the catalog does not cover in full says so: "≥ 1.2 GB", or
    // "—" when nothing below it is known (useLocale formatNodeSize).
    format: (n) => formatNodeSize(n),
    title: (n) => nodeSizeHint(n),
  },
  { id: 'star', label: t('col.star'), class: 'fe-list__col--star', headerLabel: false },
]);

/**
 * The name cell's hover text.
 *
 * ⚠ Carries the location too when the Location column is not drawn (only when
 * the person has turned it off): a column somebody hid must not silently
 * destroy the only copy of a fact, or `report.pdf` in two folders is two
 * identical rows.
 */
function nameTitle(n: FileNode): string {
  if (!props.showParentPath || visibleCols.value.includes('location')) return n.basename;
  const loc = locationLabel(n);
  return loc ? `${n.basename} · ${loc}` : n.basename;
}

function rowAttrs(n: FileNode): Record<string, unknown> {
  return {
    tabindex: 0,
    'aria-label': nodeDisplayName(n) /* wiring:c4 */,
    'data-fe-path': n.path /* wiring:d1 — middle-click open-in-new-tab delegation */,
    draggable: 'true',
  };
}

function rowClass(n: FileNode) {
  return {
    'is-dir': n.type === 'dir',
    'is-trash': !!n.trashed,
    'is-clipped': !!props.clipped?.has(n.path),
    'is-droptarget': dropTargetPath.value === n.path /* wiring:c4 */,
  };
}

// ── the column menu's folder rows ─────────────────────────────────────

const rememberedFolders = computed(() => rememberedCount());
const thisFolderRemembered = computed(() => !!props.folderKey && folderIsRemembered(props.folderKey));

/**
 * "Apply to all folders" — makes THIS folder's setup the person's default and
 * forgets every folder's own, which is what the words promise.
 *
 * ⚠ It has to WRITE the default. It used to be a pure forget, because every
 * click already wrote a global default — and that implicit write was the leak
 * the owner reported ("tüm klasörlerde görünüm değişikliği geçerli oluyor").
 * The view mode is `list` because this menu only exists in the list.
 */
function onApplyToAll(close: () => void) {
  applyToAllFolders({
    v: 'list',
    ...(ranked.value ? {} : { k: sort.key.value, d: sort.dir.value }),
    c: cols.state(),
  });
  close();
}

function onForgetFolder(close: () => void) {
  if (props.folderKey) forgetFolder(props.folderKey);
  close();
}

const rows = computed(() => {
  // Virtual rows (.trash / storage drives) stay pinned on top in their
  // original order; only real entries take part in the sort.
  const pinned: FileNode[] = [];
  const rest: FileNode[] = [];
  for (const n of props.files) (isPinnedSpecial(n) ? pinned : rest).push(n);

  /* The listing's order, from the one place that owns the rule
   * (`lib/sortOrder.compareInOrder`). An ordinary folder: folders first, then
   * the active key. A ranked answer: folders first and NOTHING else, which is
   * byte-for-byte the pass GridView already makes — so the two views cannot
   * disagree in either mode. Array#sort is stable, so ties keep the order the
   * backend answered in, and in `relevance` every pair of same-kind rows is a
   * tie: that stability IS how the server's ranking survives this line. */
  return [...pinned, ...rest.sort(sort.compareInOrder(props.order ?? 'sort'))];
});

// ------------------------------------------------------------------
// Date grouping — only while sorted by modified date. Rows are split
// into contiguous segments so the row markup below stays untouched.
//
// ⚠⚠ The LADDER and the "is a heading honest here" rule both moved to
// `lib/dateGroups`, because the grid and the gallery draw the same headings
// now and a second copy of either would drift the first time a rung changed.
// This file keeps only what is its own: which rows are aside from the dates.
// ------------------------------------------------------------------

/* zaman:z1 — the headings answer "what day is this row from", so they read the
 * same clock the cell beside them prints (composables/useLocale → lib/timezone
 * → lib/dateGroups). They used to ask the DEVICE, so for a viewer set to UTC a
 * file touched at 01:00 Istanbul sat under "Today" in the group header and
 * printed yesterday's date in its own cell, one line apart. */
const groupLabels = computed(() => ({ t, formatMonthYear, zonedYearMonth }));

watch(
  rows,
  (list) => emit('display-order', list),
  { immediate: true },
);

/* ── Owner ────────────────────────────────────────────────────────────────
 *
 * Three answers, and only three: your own name is "You", a row nobody put here
 * through filex is "System", and anything else is the account's display name.
 *
 * ⚠ "System" is not a placeholder for missing data — it is the true answer for
 * a file the scanner found in the bucket, and most rows on a mounted storage
 * are exactly that. Leaving the cell blank there would read as "we failed to
 * look it up", which is a different and false statement. */
function ownerLabel(n: FileNode): string {
  if (ownedByViewer(n)) return t('owner.you');
  if (ownerIdOf(n) === null) return t('owner.system');
  return ownerNameOf(n) || t('owner.unknown');
}

/** The hover text. It carries the two facts the column has no room for: that
 *  the bytes were handed in by an anonymous visitor through a drop link, and
 *  who touched the thing last when that is not the owner. */
function ownerTitle(n: FileNode): string {
  const parts = [ownerLabel(n)];
  if (arrivedFromOutside(n)) parts.push(t('owner.external'));
  const actor = (n as Record<string, unknown>).last_actor_name;
  const actorSelf = (n as Record<string, unknown>).last_actor_self === true;
  const actorId = (n as Record<string, unknown>).last_actor_id;
  if (actorSelf || (typeof actor === 'string' && actor)) {
    const who = actorSelf ? t('owner.you') : String(actor);
    if (who !== parts[0]) parts.push(t('owner.last_actor', { who }));
  } else if (actorId === undefined && ownerIdOf(n) !== null) {
    // An owned row whose last change came from outside filex.
    parts.push(t('owner.last_actor', { who: t('owner.system') }));
  }
  return parts.join(' · ');
}

/* ── Deleted by (the Trash) ───────────────────────────────────────────────
 *
 * "You", the account's name, or a dash. ⚠ Not "System": a row nobody is named
 * on may have been removed outside filex (the scanner found it gone) OR
 * trashed before filex kept this, and "System" would be a false answer for
 * the second. The dash says "not recorded", and its hover text says why. */
function deleterLabel(n: FileNode): string {
  if (deletedByViewer(n)) return t('owner.you');
  if (deleterIdOf(n) === null) return '—';
  return deleterNameOf(n) || t('owner.unknown');
}

function deleterTitle(n: FileNode): string | undefined {
  if (deleterIdOf(n) === null && !deletedByViewer(n)) return t('trash.deleted_by_nobody');
  return deleterLabel(n);
}

const segments = computed<DateRun<FileNode>[]>(() =>
  groupByDate(rows.value, {
    /* ⚠ `ranked` is half of this and not a detail: over a search's ranked
     * answer the key is still `modified` (nothing cleared it), the listing
     * just is not obeying it — see `lib/dateGroups.groupingActive`. */
    active: groupingActive(sortKey.value, props.order),
    dateOf: (n) => toDate(n.last_modified),
    /* ⚠ Folders are their OWN run, above the first date heading, and that is
     * what lets "folders before files" hold while sorted by date. The rule
     * used to be softened instead — rows sorted by date, folders floating to
     * the top of each bucket — because hoisting them all would have split a
     * bucket in two and drawn "Today" twice, once over the folders and once
     * over the files. A run of their own removes that reason, and a folder has
     * no meaningful modification date to file under anyway. */
    aside: (n) =>
      isPinnedSpecial(n) ? { id: 'pinned' } : n.type === 'dir' ? { id: 'dirs' } : null,
    labels: groupLabels.value,
  }).runs,
);
/** The date runs, in the shape the table draws groups in. */
const tableGroups = computed(() =>
  segments.value.map((s) => ({ id: s.id, label: s.label ?? undefined, items: s.items })),
);

</script>

<template>
  <!-- tablo:t3 — the explorer's listing is DataTable, the product's one table,
       with the file-specific parts handed in: the columns, what a cell prints
       for a file, the row's gestures and a per-FOLDER column store. Every
       class the explorer's stylesheet and the e2e suites address
       (`fe-list__row`, `fe-list__col--name`, `[data-fe-path]`, the ⋮) is
       still drawn, by DataTable. -->
  <DataTable
    :rows="rows"
    :groups="tableGroups"
    :columns="columns"
    :column-store="cols"
    :candidates="candidateCols"
    :metrics="EXPLORER_METRICS"
    :row-key="(n: FileNode) => n.path"
    :locale="locale"
    :theme="theme"
    :loading="loading"
    :empty="t('empty.folder')"
    :aria-label="t('list.aria')"
    :framed="false"
    selectable
    :is-selected="isSelected"
    :sort="tableSort"
    :sort-closed="ranked ? t('sort.relevance_why') : null"
    :row-class="rowClass"
    :row-attrs="rowAttrs"
    @sort="onSort"
    @row-click="onRowClick"
    @row-dblclick="onRowDbl"
    @row-contextmenu="onRowCtx"
    @check-click="onCheckClick"
    @row-dragstart="onItemDragStart"
    @row-dragover="onItemDragOver"
    @row-dragleave="onItemDragLeave"
    @row-drop="onItemDrop"
    @row-touchstart="(n: FileNode, ev: TouchEvent) => touch.onTouchStart(n, ev)"
    @row-touchend="(ev: TouchEvent) => touch.onTouchEnd(ev)"
    @row-touchmove="(ev: TouchEvent) => touch.onTouchMove(ev)"
  >
    <template #cell-name="{ row }">
      <!-- ikon:emoji — an encrypted folder is still a FOLDER: it keeps the
           folder's shape and colour with the padlock cut out of it. One
           definition for all three views, in lib/fileIcons. -->
      <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
      <span
        v-if="isEncryptedFolder(row)"
        class="fe-list__icon fe-list__icon--svg"
        role="img"
        :aria-label="t('e2e.badge')"
        v-html="encryptedFolderTile()"
      ></span>
      <!-- tablo:t1 — the real thumbnail, at the tile's own size so the row
           height does not move. ⚠⚠ Through ThumbTile, never read in this
           template: here, each thumbnail that arrived re-rendered every row of
           the table. The tile draws the <img> (`draggable="false"`: dragging
           an <img> puts a 'Files' MIME on the dataTransfer and the parent's
           upload handler re-uploads the thing you were only moving) and the
           type tile until then. -->
      <ThumbTile
        v-else
        :node="row"
        :src-of="thumbOf"
        class="fe-list__icon fe-list__icon--img"
        :class="{ 'fe-thumb--page': drawsAsPage(row) }"
        alt=""
        aria-hidden="true"
      >
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
        <span class="fe-list__icon fe-list__icon--svg" aria-hidden="true" v-html="fileIconTile(row)"></span>
      </ThumbTile>
      <div class="fe-list__name-wrap">
        <span class="fe-list__name" :title="nameTitle(row)">
          <!-- ⚠ RTL: `<bdi>` — the name is the person's text, not the
               interface's: its own letters decide its direction, so `2026
               report.pdf` keeps its number in front in an Arabic list and an
               Arabic name keeps its order in an English one. -->
          <bdi>{{ nodeDisplayName(row) }}</bdi>
          <!-- bul:s3 — content-match badge -->
          <span v-if="rowInContent(row)" class="fe-list__badge">{{ t('search.in_content') }}</span>
          <span
            v-if="keepBadgeFor && keepBadgeFor(row)"
            :class="['fe-keepbadge', 'fe-keepbadge--' + keepBadgeFor(row)]"
            :title="t('keep.badge_' + keepBadgeFor(row))"
            role="img"
            :aria-label="t('keep.badge_' + keepBadgeFor(row))"
          >{{ keepGlyph(keepBadgeFor(row)!) }}</span>
        </span>
        <!-- bul:s3 — content snippet («» → <mark> via TEXT segments, no innerHTML) -->
        <span v-if="rowSnippet(row)" class="fe-list__snippet">
          <template v-for="(seg, si) in snippetSegments(rowSnippet(row))" :key="si">
            <mark v-if="seg.match" class="fe-list__mark">{{ seg.text }}</mark>
            <template v-else>{{ seg.text }}</template>
          </template>
        </span>
      </div>
      <!-- ⚠ Badges OUTSIDE `.fe-list__name`: the shot scripts read that
           element's textContent as the filename, and a row whose name reads
           "nda.pdf Locked" breaks them silently (lesson #29). -->
      <span
        v-if="lockTitleOf(row)"
        class="fe-applock"
        role="img"
        :title="lockTitleOf(row)"
        :aria-label="lockTitleOf(row)"
        data-testid="lock-badge"
      ><span class="fe-applock__glyph" aria-hidden="true">&#128274;</span>{{ t('applock.badge') }}</span>
      <!-- issue #34 — a symlink the server will not follow; `aria-label`
           carries the SENTENCE, because "Outside storage" read aloud is a
           second riddle. -->
      <span
        v-if="linkOf(row)"
        class="fe-symlink"
        :class="'fe-symlink--' + linkOf(row)!.state"
        role="img"
        :title="linkOf(row)!.why"
        :aria-label="linkOf(row)!.why"
        data-testid="symlink-badge"
        :data-link-state="linkOf(row)!.state"
      ><span class="fe-symlink__glyph" aria-hidden="true">&#128279;</span>{{ linkOf(row)!.badge }}</span>
      <span v-if="isPlainDir(row) && !typeColumnShown" class="fe-list__kind">{{ t('node.folder') }}</span>
    </template>

    <template #cell-star="{ row }">
      <!-- issue #26 — only the star itself is a control; the rest of the cell
           opens the row like any other empty space on it. -->
      <span
        v-if="starEnabled !== false && typeof row.id === 'number' && row.type === 'file'"
        class="fe-list__star-hit"
        data-fe-control
        @click.stop
        @dblclick.stop
      >
        <StarButton
          :starred="!!starredIds?.has(row.id)"
          :node-id="row.id"
          :api-base="apiBase"
          :auth-headers="authHeaders"
          :auth-credentials="authCredentials"
          :locale="locale"
          compact
          @change="(val: boolean) => emit('star-change', row, val)"
        />
      </span>
    </template>

    <template #actions="{ row }">
      <!-- gorunum:v1 — the ⋮ opens THE SAME menu the right-click opens. -->
      <button
        type="button"
        class="fe-list__menu"
        :title="t('toolbar.more')"
        :aria-label="t('toolbar.more')"
        @click="onRowMenu(row, $event)"
      ><span aria-hidden="true">&#8942;</span></button>
    </template>

    <template #colmenu-extra="{ close }">
      <div v-if="thisFolderRemembered || rememberedFolders > 0" class="fe-colmenu__sep" role="separator"></div>
      <p v-if="thisFolderRemembered || rememberedFolders > 0" class="fe-colmenu__title">{{ t('cols.folder_title') }}</p>
      <!-- The rule, in one sentence, next to the two buttons that act on it.
           A per-folder memory nobody can explain is the difference between a
           feature and a haunting. -->
      <p v-if="thisFolderRemembered || rememberedFolders > 0" class="fe-colmenu__hint">{{ t('cols.folder_hint') }}</p>
      <button v-if="thisFolderRemembered" type="button" class="fe-colmenu__row" @click="onForgetFolder(close)">
        <span class="fe-colmenu__label">{{ t('cols.forget_folder') }}</span>
      </button>
      <button v-if="rememberedFolders > 0 || thisFolderRemembered" type="button" class="fe-colmenu__row" @click="onApplyToAll(close)">
        <span class="fe-colmenu__label">{{ t('cols.apply_all', { count: rememberedFolders }) }}</span>
      </button>
    </template>
  </DataTable>
</template>
