<script setup lang="ts">
/**
 * ListView — tabular layout (name / size / modified).
 *
 * Selection + context menu + double-click open are caller-owned — we
 * just emit the events and let FileExplorer.vue handle them uniformly
 * across List and Grid.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { hasInternalDrag } from '../lib/dragOut';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import {
  arrivedFromOutside,
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
  setSortLocale,
  useSortStore,
  type ListingOrder,
  type SortKey,
} from '../lib/sortOrder'; /* surucu:d1-sort */
import { matchedInContent, snippetSegments } from '../lib/snippet'; /* bul:s3 */
import { drawsAsPage, previewKindFor } from '../lib/filePreview'; /* tablo:t1 */
import { applyDragGhost } from '../lib/dragGhost'; /* wiring:c4 */
import { parentDirOf } from '../lib/listing'; /* Location column — one split rule for every view */
import {
  groupByDate,
  groupingActive,
  type DateRun,
} from '../lib/dateGroups'; /* zaman:z1 · gruplama */
import {
  COLUMNS,
  columnHidden,
  columnWidth,
  columnsCustomised,
  canMoveColumn,
  columnOrder,
  folderIsRemembered,
  freezeWidths,
  moveColumn,
  moveColumnBy,
  forgetAllFolders,
  forgetFolder,
  rememberedCount,
  resetColumns,
  setColumnHidden,
  setColumnWidth,
  tableLayout,
  widthsAreAuto,
  type ColumnId,
} from '../lib/viewPrefs'; /* tablo:t1 */
import StarButton from './StarButton.vue';

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
}>();

const emit = defineEmits<{
  (e: 'click-row', node: FileNode, mod: { ctrl: boolean; shift: boolean }): void;
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
  formatSize,
  formatDate,
  formatDateFull,
  formatMonthYear,
  zonedYearMonth,
  toDate,
  nodeDisplayName,
} = useLocale(() => props.locale);

function isSelected(n: FileNode): boolean {
  return props.selected.has(n.path);
}

function onRowClick(n: FileNode, ev: MouseEvent) {
  emit('click-row', n, { ctrl: ev.ctrlKey || ev.metaKey, shift: ev.shiftKey });
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
    clientX: box ? box.left : ev.clientX,
    clientY: box ? box.bottom : ev.clientY,
    preventDefault: () => {},
    stopPropagation: () => {},
  } as unknown as MouseEvent);
}

/**
 * gorunum:v1 — the row checkbox drives THE SAME selection the row click
 * drives: one `click-row` emit, one `useSelection.click`, one anchor. A
 * checkbox that kept its own Set would disagree with the row the first time
 * someone mixed the two, and the context menu acts on the row's set.
 *
 * `ctrl: true` because ticking a box adds to a selection rather than replacing
 * it; `shift` is passed through so shift-clicking a box still extends the
 * range from the anchor (the composable answers shift before ctrl).
 */
function onCheckClick(n: FileNode, ev: MouseEvent) {
  emit('click-row', n, { ctrl: true, shift: ev.shiftKey });
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

let pressTimer: ReturnType<typeof setTimeout> | undefined;
let pressTarget: FileNode | null = null;

function onTouchStart(n: FileNode, ev: TouchEvent) {
  pressTarget = n;
  if (pressTimer) clearTimeout(pressTimer);
  pressTimer = setTimeout(() => {
    if (pressTarget) {
      const t0 = ev.touches[0];
      emit('context-row', pressTarget, {
        clientX: t0.clientX,
        clientY: t0.clientY,
        preventDefault: () => {},
      } as unknown as MouseEvent);
    }
  }, 500);
}

function cancelPress() {
  if (pressTimer) clearTimeout(pressTimer);
  pressTarget = null;
}

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

/** The muted "Folder" said beside a directory's name — not on the sentinels. */
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
// Column sorting.
//
// ⚠⚠ NOT local to the list view any more. The key, the direction, the click
// cycle and the comparator all live in `lib/sortOrder`, the module-level
// singleton the filter row's sort control also drives — so these headers and
// that control are two handles on ONE piece of state rather than two sorts
// racing to re-order the same rows (filex lesson #67, one level up from the
// "folders first" copy it is about). The headers kept their behaviour: click
// a column to sort by it, click it again to reverse.
//
// ⚠⚠ The old third click — "back to the backend's order" — is back, but as a
// property of the ROWS rather than of the click: `order === 'relevance'` says
// the server ranked this listing, and then no key is applied, no column
// claims to be sorting, and these headers close. The filter row's button says
// "Relevance" and why, so the state the list is in is one the control can
// describe — which is the thing that made `key: null` unworkable as a key.
//
// ⚠ Closing the headers is not politeness. A header click while a ranked
// listing is on screen would set the shared key, move nothing at all, and
// then re-order the FOLDER the person returns to — a control whose only
// effect is somewhere the user is not looking.
// ------------------------------------------------------------------

/** The Type column sorts too now, so every key the filter row's Sort-by menu
 *  offers is reachable from a header as well. */
/* ⚠⚠ The store of the PANE this list is inside, not a module-level singleton.
 * Two panes of a split each draw a listing and each carry a sort control; one
 * shared answer means the control in the right pane re-orders the left one.
 * With no provider (a bare <ListView>, a host with one listing) this is the
 * default store, i.e. exactly the old behaviour. */
const sort = useSortStore();
const sortKey = computed<SortKey>(() => sort.key.value);
const sortDir = computed(() => sort.dir.value);
/** These rows are the server's ranked answer — see the `order` prop. */
const ranked = computed(() => props.order === 'relevance');

/* `type` sorts by the word this very column prints, so the comparator needs
 * the catalogue that word comes from. */
watch(() => props.locale, (l) => setSortLocale(l), { immediate: true });

function toggleSort(key: SortKey) {
  /* ⚠⚠ The `disabled` attribute on the header is the AFFORDANCE, not the
   * rule. A disabled button suppresses the browser's own activation, but any
   * click dispatched in script still runs this listener — and an embedder is
   * free to restyle or re-render these headers. The consequence of letting one
   * through is invisible and delayed: nothing moves on screen (the listing is
   * ranked), the shared key is written to `filex.list-sort`, and the FOLDER
   * the person returns to comes back in an order they never asked for.
   * Measured 2026-09-13: a scripted click on the closed Size header left the
   * rows untouched and still turned the stored sort from Name ↑ into Size ↑. */
  if (ranked.value) return;
  sort.chooseSortKey(key);
}

/** What a column header promises. Over a ranked listing the headers are
 *  closed, so they say WHY rather than going quietly grey — the same sentence
 *  the filter row's sort button carries, because it is the same fact. */
const sortTitle = computed(() => t(ranked.value ? 'sort.relevance_why' : 'col.sort'));

function ariaSort(key: SortKey): 'ascending' | 'descending' | 'none' {
  // ⚠ 'none' for every column over a ranked listing: `aria-sort` is a claim
  // about the rows below, and the rows below are in the server's order.
  if (ranked.value || sortKey.value !== key) return 'none';
  return sortDir.value === 'asc' ? 'ascending' : 'descending';
}

// ------------------------------------------------------------------
// tablo:t2 — the columns: how wide, and who decides.
//
// ⚠⚠ THE LAYOUT IS COMPUTED HERE, IN JAVASCRIPT, and applied as inline widths
// — it is not a stylesheet rule. It used to be three places at once (a base
// template in `base.css`, a `.fe--narrow` override restating it and a
// `@media (max-width: 640px)` rule restating it again, each with its own list
// of cells to `display: none`), and they disagreed: the media query answers
// the VIEWPORT while `.fe--narrow` answers the CONTAINER, so an embed 500px
// wide on a 1400px screen got one rule's hiding and the other rule's tracks.
//
// ⚠⚠ IT IS A REAL TABLE NOW, not a fit-to-container layout. The previous
// arrangement sized the columns to the pane and, when they stopped fitting,
// SHED the least informative one and told the person "no room" in the header
// menu. The owner rejected the model, verbatim: "büyütme küçültme düzgün
// çalışmıyor, tablo gibi durmuyor orası. Çok büyütünce yok oluyor, 'no room'
// diyor — no room demesin, kenara devam eden bir scroll getirsin." So the
// table's width is the sum of its columns, the pane scrolls sideways when that
// is more than it has, the ⋮ stays pinned to the right edge, and a table
// narrower than its pane sits at the LEFT with the slack after it.
// ------------------------------------------------------------------

/** Which optional columns this listing is willing to draw at all, before any
 *  question of width or of the person's own choices. */
const candidateCols = computed<ColumnId[]>(() => {
  const out: ColumnId[] = ['type'];
  /* Location only where the rows come from more than one folder — the same
     condition that used to draw the parent path under the name. In an ordinary
     folder every row shares one location and the column would be a wall of the
     same string. */
  if (props.showParentPath) out.push('location');
  out.push('owner', 'modified', 'size');
  if (props.starEnabled !== false && (!!props.apiBase || props.apiBase === '')) out.push('star');
  return out;
});

/**
 * The width the list actually has.
 *
 * ⚠ Its OWN width, watched with a ResizeObserver, not the window's. An
 * explorer embedded in a side panel is narrow on a wide screen, and that is
 * the case the old media query could not see. It is also the SCROLLPORT's
 * width — `.fe-list` is the scrolling box now, so `contentRect` already has
 * the vertical scrollbar taken out of it and the auto widths are computed
 * against the room a row really has. 0 until the first observation, which
 * `tableLayout` reads as "keep the shipped widths" so the first paint is not a
 * guess that then jumps.
 */
const listEl = ref<HTMLElement | null>(null);
const listWidth = ref(0);
let ro: ResizeObserver | null = null;

/**
 * Is the table scrolled off its left edge?
 *
 * Only used to draw the pinned ⋮ column's edge. A permanent divider there
 * would claim the column is floating when it is simply the last cell of a
 * table that fits.
 */
const scrolledX = ref(false);

function onListScroll(ev: Event) {
  const el = ev.currentTarget as HTMLElement | null;
  const on = (el?.scrollLeft ?? 0) > 0;
  if (on !== scrolledX.value) scrolledX.value = on;
}

onMounted(() => {
  if (!listEl.value || typeof ResizeObserver === 'undefined') return;
  ro = new ResizeObserver((entries) => {
    const w = entries[0]?.contentRect?.width ?? 0;
    if (Math.abs(w - listWidth.value) >= 1) listWidth.value = w;
  });
  ro.observe(listEl.value);
});
onBeforeUnmount(() => {
  ro?.disconnect();
  ro = null;
  window.removeEventListener('pointermove', onResizeMove);
  window.removeEventListener('pointerup', onResizeEnd);
});

const layout = computed(() => tableLayout(listWidth.value, candidateCols.value));
const visibleCols = computed<ColumnId[]>(() => layout.value.visible);

/**
 * ONE object, bound to the header and to every row.
 *
 * ⚠⚠ The header has to be exactly as wide as the rows or it drifts out of line
 * with its own columns the moment the table is scrolled sideways — which is
 * worse than having no header at all, because it labels the wrong values.
 * `min-width: 100%` in the stylesheet is what keeps a NARROW table's rows
 * spanning the pane (so hover and selection do not stop halfway across) while
 * the cells inside them still pack left.
 */
const tableStyle = computed(() => ({ width: `${layout.value.total}px` }));

/** A drawn track's width, as the inline style the cell carries. `flex-basis`
 *  rather than a grid track, because the pinned ⋮ has to be able to move
 *  within the row — a sticky GRID item is confined to its own grid area and
 *  therefore cannot move at all. */
function colStyle(id: ColumnId) {
  return { flex: `0 0 ${layout.value.widths[id] ?? columnWidth(id)}px` };
}

/** A column's cell class — the existing `fe-list__col--*` names, so every
 *  style already written against them keeps applying. */
function colClass(id: ColumnId): string {
  return `fe-list__col fe-list__col--${id === 'modified' ? 'mod' : id}`;
}

/** The four keys a header can sort by, keyed by column. Location and the star
 *  are not sorts: there is no `SortKey` for either, and inventing one would put
 *  a word in the Sort-by menu that means nothing in nine listings out of ten. */
const SORT_OF: Partial<Record<ColumnId, SortKey>> = {
  type: 'type',
  modified: 'modified',
  size: 'size',
};

const COL_LABEL: Record<ColumnId, string> = {
  name: 'col.name',
  type: 'col.type',
  location: 'col.location',
  owner: 'col.owner',
  modified: 'col.modified',
  size: 'col.size',
  star: 'col.star',
};

function colLabel(id: ColumnId): string {
  return t(COL_LABEL[id]);
}

/** What the cell prints for this row. One switch, so the head's order and the
 *  body's values are driven by the same list. */
function cellText(id: ColumnId, n: FileNode): string {
  switch (id) {
    case 'type':
      return typeLabel(n);
    case 'location':
      return locationLabel(n);
    case 'owner':
      return ownerLabel(n);
    case 'modified':
      return displayDate(n.last_modified);
    case 'size':
      return formatSize(n.size);
    default:
      return '';
  }
}

function cellTitle(id: ColumnId, n: FileNode): string | undefined {
  if (id === 'owner') return ownerTitle(n);
  if (id === 'modified') return fullDate(n.last_modified);
  if (id === 'location') return locationLabel(n) || undefined;
  return undefined;
}

/**
 * The name cell's hover text.
 *
 * ⚠ Carries the location too when the Location column is not drawn — which
 * now only happens when the person has turned it off, since width sheds
 * nothing. The point of moving the parent path into a column was that every
 * value gets a column; the point of this line is that a column somebody hid
 * must not silently destroy the only copy of a fact, or `report.pdf` in two
 * folders is two identical rows.
 */
function nameTitle(n: FileNode): string {
  if (!props.showParentPath || visibleCols.value.includes('location')) return n.basename;
  const loc = locationLabel(n);
  return loc ? `${n.basename} · ${loc}` : n.basename;
}

// ── resizing ──────────────────────────────────────────────────────────

function isResizable(id: ColumnId): boolean {
  return COLUMNS.find((c) => c.id === id)?.resizable === true;
}

/**
 * ⚠⚠ NAME HAS A HANDLE NOW, and the note that stood here said the opposite:
 * "there is deliberately NO handle on Name… it is whatever the fixed columns
 * leave, so dragging Name can only mean dragging everything else." That was
 * true of the flexible `minmax(220px, 1fr)` track, and the track is what the
 * owner rejected: "küçültme yapınca name kısmı büyüyormuş gibi davranıyor — bu
 * davranış yanlış, istersem name'i de kısabilir olmalıyım." Name is an
 * ordinary column with a width of its own, so its handle resizes the column it
 * sits on, like every other.
 */
let resizing: { id: ColumnId; startX: number; startW: number } | null = null;

/**
 * THE FIRST DRAG FREEZES THE WHOLE ROW.
 *
 * ⚠⚠ Not just the column under the pointer. Until somebody sizes a column the
 * widths are derived from the pane (`tableLayout`, auto), and auto Name takes
 * whatever slack is going — so narrowing Size while Name is still auto would
 * widen Name, which is precisely the behaviour being fixed. Committing every
 * drawn width the instant a gesture starts turns the drag into what it looks
 * like, and is also what makes "I have never touched this" distinguishable
 * from "I deliberately made Name narrow" forever after (`widthsAreAuto`).
 */
function freezeBeforeEdit() {
  if (!widthsAreAuto()) return;
  freezeWidths(layout.value.widths);
}

function onResizeStart(id: ColumnId, ev: PointerEvent) {
  if (!isResizable(id)) return;
  ev.preventDefault();
  ev.stopPropagation();
  freezeBeforeEdit();
  resizing = { id, startX: ev.clientX, startW: columnWidth(id) };
  /* ⚠ On the WINDOW, not on the handle with setPointerCapture: the pointer
   * leaves the 9px handle within the first few pixels of any real drag, and a
   * capture on an element Vue may re-render mid-drag (the template width is
   * reactive and changes on every move) is released the moment that element is
   * patched — the column would stop following the cursor halfway. */
  window.addEventListener('pointermove', onResizeMove);
  window.addEventListener('pointerup', onResizeEnd);
}

function onResizeMove(ev: PointerEvent) {
  if (!resizing) return;
  setColumnWidth(resizing.id, resizing.startW + (ev.clientX - resizing.startX));
}

function onResizeEnd() {
  resizing = null;
  window.removeEventListener('pointermove', onResizeMove);
  window.removeEventListener('pointerup', onResizeEnd);
}

/** Keyboard resizing — the handle is focusable, so it has to do something.
 *  16px a step, which is about one character of the 13px body text. */
function onResizeKey(id: ColumnId, ev: KeyboardEvent) {
  const step = ev.key === 'ArrowLeft' ? -16 : ev.key === 'ArrowRight' ? 16 : 0;
  if (!step) return;
  ev.preventDefault();
  freezeBeforeEdit();
  setColumnWidth(id, columnWidth(id) + step);
}

/** Double-click a handle: back to this column's shipped width. "Size to fit"
 *  would mean measuring every cell in a listing that may be thousands of rows
 *  long, and the honest cheap answer is the default. */
function onResizeReset(id: ColumnId) {
  const spec = COLUMNS.find((c) => c.id === id);
  if (!spec) return;
  freezeBeforeEdit();
  setColumnWidth(id, spec.width);
}

// ── reordering ────────────────────────────────────────────

/**
 * DRAG A HEADER TO MOVE ITS COLUMN — the owner's third ask for this table:
 * "ayrıca yerlerini falan değiştirebilir olayım ya o sütunların."
 *
 * ⚠⚠ The threshold is the whole difficulty. Clicking a header sorts and
 * dragging it moves, and both start with the same pointerdown — so a reorder
 * that begins on the first pixel of movement turns every sort click into an
 * accidental two-pixel drag. Nothing happens here until the pointer has
 * travelled `DRAG_SLOP`; below that the gesture stays a click and the sort
 * button gets it, untouched. Above it, the click the browser fires on
 * pointerup is swallowed on its way past (`onHeadClickCapture`), or every
 * completed drag would also re-sort the column it had just moved.
 *
 * ⚠ Name is not in this: it is pinned first and nothing may be dropped before
 * it. It carries the tick, the icon and the row's click target, which is why
 * every file manager pins it — and `columnOrder` enforces it, so a document
 * that claims otherwise cannot put a column ahead of the one the row is read
 * by. Its WIDTH is the person's, though; only its place is fixed.
 */
const DRAG_SLOP = 5;
let dragState: { id: ColumnId; startX: number; armed: boolean } | null = null;
/** The column being dragged, once the slop is cleared. */
const dragCol = ref<ColumnId | null>(null);
/** Where it would land: an index into the drawn columns. */
const dropAt = ref<number | null>(null);
/**
 * When a real drag last ended — the one thing standing between "I moved a
 * column" and "I moved a column and also re-sorted it".
 *
 * ⚠⚠ A TIMESTAMP and not a boolean. A flag is armed on pointerup and
 * disarmed by the click that follows — except when no click follows, which
 * happens whenever the pointer went down on one header and up on another and
 * the browser found no common ancestor to fire on. The flag then sits armed
 * and eats the NEXT header click the person makes, which is a sort that
 * silently does nothing minutes later. A window bounds the damage to the
 * gesture it belongs to. Measured 2026-09-13: a sort click after a reorder
 * drag did nothing at all.
 */
let dragEndedAt = 0;
const CLICK_SWALLOW_MS = 300;
/** Set by Escape, cleared by the pointerup it pre-empts. */
let cancelled = false;

function isMovable(id: ColumnId): boolean {
  return COLUMNS.find((c) => c.id === id)?.hideable === true;
}

function onHeadPointerDown(id: ColumnId, ev: PointerEvent) {
  if (ev.button !== 0 || !isMovable(id)) return;
  dragState = { id, startX: ev.clientX, armed: false };
  window.addEventListener('pointermove', onHeadPointerMove);
  window.addEventListener('pointerup', onHeadPointerUp);
  window.addEventListener('keydown', onDragKey, true);
}

function onHeadPointerMove(ev: PointerEvent) {
  if (!dragState) return;
  if (!dragState.armed) {
    if (Math.abs(ev.clientX - dragState.startX) < DRAG_SLOP) return;
    dragState.armed = true;
    dragCol.value = dragState.id;
  }
  /* Where the pointer is, in terms of the drawn header cells. The insertion
   * point is the gap nearest the cursor, and it is ONE number — the marker
   * draws it and the drop applies it, so the picture cannot promise a landing
   * the drop does not honour. */
  const head = listEl.value?.querySelector('.fe-list__head');
  if (!head) return;
  const cells = [...head.querySelectorAll('.fe-list__col')] as HTMLElement[];
  /* The first two cells are the tick and Name, the last is the row menu; only
     the ones between are positions a column can take. */
  const movable = cells.slice(2, 2 + visibleCols.value.length);
  let idx = movable.length;
  for (let i = 0; i < movable.length; i++) {
    const box = movable[i].getBoundingClientRect();
    if (ev.clientX < box.left + box.width / 2) {
      idx = i;
      break;
    }
  }
  dropAt.value = idx;
}

function onHeadPointerUp() {
  const st = dragState;
  const target = dropAt.value;
  endDrag();
  /* ⚠ `cancelled` is checked as well as `armed`: Escape clears `dragState`,
   * but a pointerup already queued behind it must not be able to complete the
   * drop the person just abandoned. */
  if (!st?.armed || cancelled) {
    cancelled = false;
    return;
  }
  dragEndedAt = Date.now();
  if (target == null) return;
  /* Translate a position among the DRAWN columns into a position in the full
   * stored order: a column shed for want of room still has a place in the
   * sequence, and dropping between two visible columns must not silently
   * reshuffle the one hiding between them. */
  const drawn = visibleCols.value.filter((id) => id !== st.id);
  const before = drawn[Math.min(target, drawn.length)];
  const full = columnOrder();
  moveColumn(st.id, before ? full.indexOf(before) : full.length);
}

function onDragKey(ev: KeyboardEvent) {
  if (ev.key !== 'Escape') return;
  /* Cancel: the drop is abandoned and nothing is written. The click is still
   * swallowed, because the pointer is mid-gesture and releasing it over a
   * header would otherwise sort the very column the person was rescuing. */
  ev.stopPropagation();
  const armed = dragState?.armed === true;
  endDrag();
  if (armed) {
    cancelled = true;
    dragEndedAt = Date.now();
  }
}

function endDrag() {
  dragState = null;
  dragCol.value = null;
  dropAt.value = null;
  window.removeEventListener('pointermove', onHeadPointerMove);
  window.removeEventListener('pointerup', onHeadPointerUp);
  window.removeEventListener('keydown', onDragKey, true);
}

/** Capture phase, on the header row: a click that ended a drag never reaches
 *  the sort button underneath it. */
function onHeadClickCapture(ev: MouseEvent) {
  if (Date.now() - dragEndedAt > CLICK_SWALLOW_MS) return;
  dragEndedAt = 0;
  ev.preventDefault();
  ev.stopPropagation();
}

// ── the column menu ───────────────────────────────────────────────────

/**
 * A small settings popover, opened by the header's own ⋮ or by right-clicking
 * the header — the two gestures the owner named.
 *
 * ⚠ Not `ContextMenu`, and that is not a second menu in the sense filex lesson
 * #67 warns about: nothing here is an action on a file, so there is no action
 * list to drift out of step with the row menu's. It is a panel of checkboxes,
 * which a menu cannot be — a menu closes on every pick, so turning four
 * columns off would be four separate right-clicks.
 */
const colMenuOpen = ref(false);
const colMenuX = ref(0);
const colMenuY = ref(0);
const colMenuEl = ref<HTMLElement | null>(null);

const prefersDark = ref(false);
onMounted(() => {
  try {
    prefersDark.value = window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
  } catch {
    /* jsdom / no matchMedia */
  }
});
const colMenuThemeClass = computed(
  () => `fe-ctx-backdrop--theme-${props.theme || 'auto'}`,
);

/** Everything the person may toggle here: the candidates, minus the ones that
 *  are not theirs to hide (the star follows the Starred view's own rule). */
const menuCols = computed(() =>
  /* In the person's OWN order, so this list reads as the list on screen — a
     settings panel that disagrees with the thing it configures is how someone
     ends up nudging the wrong column left. */
  columnOrder()
    .filter((id) => COLUMNS.find((c) => c.id === id)?.hideable && candidateCols.value.includes(id))
    /* ⚠ No `squeezed` flag any more, and no "no room" note beside a ticked
       row. It existed because a column could be ON and still not drawn, for
       want of width; nothing is dropped for width now, so a ticked column is
       on screen — possibly past the right edge, where the table scrolls to
       reach it. A note that can no longer be true is worse than no note. */
    .map((id) => ({
      id,
      label: colLabel(id),
      on: !columnHidden(id),
      canLeft: canMoveColumn(id, -1),
      canRight: canMoveColumn(id, 1),
    })),
);

/** The same move the drag performs, reachable from the keyboard. A reorder
 *  only a mouse can do is a reorder half the people using this cannot do. */
function nudgeCol(id: ColumnId, delta: -1 | 1) {
  moveColumnBy(id, delta);
}

const canResetCols = computed(() => columnsCustomised());
const rememberedFolders = computed(() => rememberedCount());
const thisFolderRemembered = computed(() => !!props.folderKey && folderIsRemembered(props.folderKey));

async function openColMenu(ev: MouseEvent) {
  ev.preventDefault();
  ev.stopPropagation();
  const el = ev.currentTarget as HTMLElement | null;
  const box = el?.getBoundingClientRect();
  /* Anchored to the BUTTON's corner when it came from the ⋮, to the pointer
   * when it came from a right-click — the two gestures mean different things
   * about where the person is looking. */
  colMenuX.value = ev.clientX || (box ? box.left : 0);
  colMenuY.value = ev.clientY || (box ? box.bottom : 0);
  colMenuOpen.value = true;
  await nextTick();
  clampColMenu();
  colMenuEl.value?.focus();
}

/** Keep it on screen. Opened from a header at the right edge it would
 *  otherwise hang off the window with no way to reach the last row. */
function clampColMenu() {
  const el = colMenuEl.value;
  if (!el) return;
  const box = el.getBoundingClientRect();
  const pad = 8;
  if (box.right > window.innerWidth - pad) {
    colMenuX.value = Math.max(pad, window.innerWidth - pad - box.width);
  }
  if (box.bottom > window.innerHeight - pad) {
    colMenuY.value = Math.max(pad, window.innerHeight - pad - box.height);
  }
}

function closeColMenu() {
  colMenuOpen.value = false;
}

function toggleCol(id: ColumnId) {
  setColumnHidden(id, !columnHidden(id));
}

function onResetColumns() {
  resetColumns();
}

/**
 * "Apply to all folders" — the escape hatch.
 *
 * ⚠ It only FORGETS. It does not need to write anything, because the global
 * default is already this folder's setup: every deliberate change writes both
 * the folder's memory and the global preference (`filex.view-mode`,
 * `filex.list-sort`), so throwing the per-folder map away leaves exactly one
 * answer standing — the one on screen. That property is the reason the rule
 * fits in a sentence.
 */
function onApplyToAll() {
  forgetAllFolders();
  closeColMenu();
}

function onForgetFolder() {
  if (props.folderKey) forgetFolder(props.folderKey);
  closeColMenu();
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
</script>

<template>
  <!-- wiring:c4 — grid semantics: container role + localized labels; rows
       carry aria-selected below. Structure/layout untouched. -->
  <div
    ref="listEl"
    class="fe-list"
    :class="{
      'is-loading': loading,
      'has-star-col': !!apiBase || apiBase === '',
      'is-scrolled-x': scrolledX,
    }"
    role="grid"
    :aria-label="t('list.aria')"
    :aria-busy="loading ? 'true' : undefined"
    @scroll.passive="onListScroll"
  >
    <!-- tablo:t2 — every width is INLINE, computed from the person's own
         columns and, until they touch one, from the pane (lib/viewPrefs
         .tableLayout). The head and the rows carry the SAME width object, so a
         table scrolled sideways cannot drift out of line with its header. -->
    <div
      class="fe-list__head"
      role="row"
      :class="{ 'is-dragging-col': !!dragCol }"
      :style="tableStyle"
      @contextmenu="openColMenu"
      @click.capture="onHeadClickCapture"
    >
      <!-- gorunum:v1 — the checkbox column's header stays empty on purpose: a
           select-all tick here is a real affordance with real consequences
           (Delete acts on the selection) and the spec does not call for one. -->
      <div class="fe-list__col fe-list__col--check" role="columnheader"></div>
      <div
        class="fe-list__col fe-list__col--name"
        role="columnheader"
        :style="colStyle('name')"
        :aria-sort="ariaSort('name')"
      >
        <button type="button" class="fe-list__sort" :disabled="ranked" :title="sortTitle" @click="toggleSort('name')">
          {{ t('col.name') }}
          <span v-if="!ranked && sortKey ==='name'" class="fe-list__sort-arrow" aria-hidden="true">{{ sortDir === 'asc' ? '↑' : '↓' }}</span>
        </button>
        <!-- Name's own handle. It used to have none, because Name was the
             flexible track and "dragging Name" could only mean dragging
             everything else; it has a width of its own now, so it drags like
             any other column — in both directions. -->
        <span
          class="fe-list__resize"
          role="separator"
          aria-orientation="vertical"
          tabindex="0"
          :aria-label="t('cols.resize', { col: t('col.name') })"
          :title="t('cols.resize', { col: t('col.name') })"
          @pointerdown="onResizeStart('name', $event)"
          @dblclick.stop="onResizeReset('name')"
          @keydown="onResizeKey('name', $event)"
        ></span>
      </div>
      <!-- surucu:d1-sort — Type / Modified / Size sort; Location and the star
           do not, because there is no SortKey for either and inventing one
           would put a word in the Sort-by menu that means nothing in nine
           listings out of ten. Owner is the same case: the People chip is how
           you narrow by person. -->
      <div
        v-for="(id, ci) in visibleCols"
        :key="id"
        :class="[
          colClass(id),
          {
            'is-col-dragging': dragCol === id,
            'is-drop-before': dropAt === ci && !!dragCol && dragCol !== id,
            'is-drop-after': dropAt === visibleCols.length && ci === visibleCols.length - 1 && !!dragCol,
            'is-movable': isMovable(id),
          },
        ]"
        role="columnheader"
        :style="colStyle(id)"
        :aria-sort="SORT_OF[id] ? ariaSort(SORT_OF[id]!) : undefined"
        :aria-label="id === 'star' ? t('col.star') : undefined"
        @pointerdown="onHeadPointerDown(id, $event)"
      >
        <button
          v-if="SORT_OF[id]"
          type="button"
          class="fe-list__sort"
          :disabled="ranked"
          :title="sortTitle"
          @click="toggleSort(SORT_OF[id]!)"
        >
          {{ colLabel(id) }}
          <span v-if="!ranked && sortKey === SORT_OF[id]" class="fe-list__sort-arrow" aria-hidden="true">{{ sortDir === 'asc' ? '↑' : '↓' }}</span>
        </button>
        <span v-else-if="id !== 'star'" class="fe-list__head-label">{{ colLabel(id) }}</span>
        <!-- The drag handle straddles the divider on this column's trailing
             edge. `role="separator"` and focusable, so the arrow keys move it
             too — a handle you can only reach with a mouse is a handle half
             the people using this cannot reach at all. -->
        <span
          v-if="isResizable(id)"
          class="fe-list__resize"
          role="separator"
          aria-orientation="vertical"
          tabindex="0"
          :aria-label="t('cols.resize', { col: colLabel(id) })"
          :title="t('cols.resize', { col: colLabel(id) })"
          @pointerdown="onResizeStart(id, $event)"
          @dblclick.stop="onResizeReset(id)"
          @keydown="onResizeKey(id, $event)"
        ></span>
      </div>
      <div class="fe-list__col fe-list__col--menu" role="columnheader">
        <button
          type="button"
          class="fe-list__menu fe-list__colmenu-btn"
          :title="t('cols.menu')"
          :aria-label="t('cols.menu')"
          aria-haspopup="dialog"
          :aria-expanded="colMenuOpen ? 'true' : 'false'"
          @click="openColMenu"
        ><span aria-hidden="true">&#8942;</span></button>
      </div>
    </div>
    <div class="fe-list__body" role="rowgroup">
      <template v-for="seg in segments" :key="seg.id">
      <div v-if="seg.label" class="fe-list__group" role="presentation">{{ seg.label }}</div>
      <div
        v-for="n in seg.items"
        :key="n.path"
        class="fe-list__row"
        :class="{
          'is-selected': isSelected(n),
          'is-dir': n.type === 'dir',
          'is-trash': n.trashed,
          'is-clipped': clipped?.has(n.path),
          'is-droptarget': dropTargetPath === n.path /* wiring:c4 */,
        }"
        role="row"
        tabindex="0"
        :style="tableStyle /* tablo:t2 — the head's own width, so the two cannot drift */"
        :aria-selected="isSelected(n) ? 'true' : 'false'"
        :aria-label="nodeDisplayName(n) /* wiring:c4 */"
        :data-fe-path="n.path /* wiring:d1 — middle-click open-in-new-tab delegation */"
        draggable="true"
        @click="onRowClick(n, $event)"
        @dblclick="onRowDbl(n)"
        @contextmenu="onRowCtx(n, $event)"
        @dragstart="onItemDragStart(n, $event)"
        @dragover="onItemDragOver(n, $event)"
        @dragleave="onItemDragLeave(n) /* wiring:c4 */"
        @drop="onItemDrop(n, $event)"
        @touchstart.passive="onTouchStart(n, $event)"
        @touchend="cancelPress"
        @touchmove="cancelPress"
      >
        <!-- gorunum:v1 — the tick drives the SAME selection the row click
             does (onCheckClick → click-row → useSelection.click), so shift
             ranges and ctrl toggles are one implementation, not two.
             ⚠ A button with role="checkbox", NOT an <input type="checkbox">.
             The input owns a `checked` state of its own: the browser flips it
             before the click handler runs and, if the default is prevented,
             flips it back AFTER Vue has patched — so the row went selected
             while the box stayed empty (measured: rowSelected=true,
             domChecked=false). Drawing the state from the selection leaves
             nothing to drift. Space and Enter still tick it, because a button
             fires `click` for both. -->
        <div class="fe-list__col fe-list__col--check" role="gridcell" @click.stop @dblclick.stop>
          <button
            type="button"
            class="fe-list__check"
            :class="{ 'is-on': isSelected(n) }"
            role="checkbox"
            :aria-checked="isSelected(n) ? 'true' : 'false'"
            :aria-label="nodeDisplayName(n)"
            :title="nodeDisplayName(n)"
            @click.stop="onCheckClick(n, $event)"
          ></button>
        </div>
        <div class="fe-list__col fe-list__col--name" role="gridcell" :style="colStyle('name')">
          <!-- ikon:emoji — an encrypted folder is still a FOLDER: it keeps the
               folder's shape and colour with the padlock cut out of it, and the
               span carries the name the bare lock emoji never had. One definition
               for all three views, in lib/fileIcons. -->
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
          <span
            v-if="isEncryptedFolder(n)"
            class="fe-list__icon fe-list__icon--svg"
            role="img"
            :aria-label="t('e2e.badge')"
            v-html="encryptedFolderTile()"
          ></span>
          <!-- tablo:t1 — the real thumbnail, at the tile's own size so the row
               height does not move. `draggable="false"` for the reason the
               grid gives: dragging an <img> puts a 'Files' MIME on the
               dataTransfer and the parent's upload handler re-uploads the
               thing you were only moving. -->
          <img
            v-else-if="thumbOf(n)"
            class="fe-list__icon fe-list__icon--img"
            :class="{ 'fe-thumb--page': drawsAsPage(n) }"
            :src="thumbOf(n)!"
            alt=""
            aria-hidden="true"
            loading="lazy"
            draggable="false"
          />
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/fileIcons -->
          <span v-else class="fe-list__icon fe-list__icon--svg" aria-hidden="true" v-html="fileIconTile(n)"></span>
          <div class="fe-list__name-wrap">
            <span class="fe-list__name" :title="nameTitle(n)">
              {{ nodeDisplayName(n) }}
              <!-- bul:s3 — content-match badge -->
              <span v-if="rowInContent(n)" class="fe-list__badge">{{ t('search.in_content') }}</span>
              <span
                v-if="keepBadgeFor && keepBadgeFor(n)"
                :class="['fe-keepbadge', 'fe-keepbadge--' + keepBadgeFor(n)]"
                :title="t('keep.badge_' + keepBadgeFor(n))"
                role="img"
                :aria-label="t('keep.badge_' + keepBadgeFor(n))"
              >{{ keepGlyph(keepBadgeFor(n)!) }}</span>
            </span>
            <!-- tablo:t1 — the parent path used to be a second line HERE, and
                 printed an em dash when a row had no sub-folder. That is the
                 dash the owner reported under the filenames; it is a Location
                 COLUMN now ("her değer kendi sütununda gözükmesi gerekmekte"),
                 and a row at a storage's root simply has an empty cell. -->
            <!-- bul:s3 — content snippet («» → <mark> via TEXT segments, no innerHTML) -->
            <span v-if="rowSnippet(n)" class="fe-list__snippet">
              <template v-for="(seg, si) in snippetSegments(rowSnippet(n))" :key="si">
                <mark v-if="seg.match" class="fe-list__mark">{{ seg.text }}</mark>
                <template v-else>{{ seg.text }}</template>
              </template>
            </span>
          </div>
          <!-- gorunum:v1 — the kind, said in words beside the name. OUTSIDE
               `.fe-list__name`: the shot scripts read that element's
               textContent as the filename (e2e/shots/starstags.mjs), and a
               row whose name reads "Reports Folder" breaks them silently. -->
          <span v-if="isPlainDir(n)" class="fe-list__kind">{{ t('node.folder') }}</span>
        </div>
        <!-- tablo:t1 — one loop over the SAME list the header draws, so a
             column cannot exist in one and not the other, and the values land
             in the tracks the inline template allocated. -->
        <template v-for="id in visibleCols" :key="id">
          <div
            v-if="id === 'star'"
            class="fe-list__col fe-list__col--star"
            role="gridcell"
            :style="colStyle(id)"
            @click.stop
          >
            <StarButton
              v-if="starEnabled !== false && typeof n.id === 'number' && n.type === 'file'"
              :starred="!!starredIds?.has(n.id)"
              :node-id="n.id"
              :api-base="apiBase"
              :auth-headers="authHeaders"
              :auth-credentials="authCredentials"
              :locale="locale"
              compact
              @change="(val: boolean) => emit('star-change', n, val)"
            />
          </div>
          <div v-else :class="colClass(id)" role="gridcell" :style="colStyle(id)" :title="cellTitle(id, n)">
            {{ cellText(id, n) }}
          </div>
        </template>
        <div class="fe-list__col fe-list__col--menu" role="gridcell" @click.stop @dblclick.stop>
          <button
            type="button"
            class="fe-list__menu"
            :title="t('toolbar.more')"
            :aria-label="t('toolbar.more')"
            @click="onRowMenu(n, $event)"
          ><span aria-hidden="true">&#8942;</span></button>
        </div>
      </div>
      </template>
      <div v-if="!loading && rows.length === 0" class="fe-list__empty">
        {{ t('empty.folder') }}
      </div>
    </div>
  </div>
  <!-- tablo:t1 — the column menu. Teleported to <body> for the reason
       ContextMenu gives: inside the tree, an ancestor with `overflow: hidden`
       or its own transform clips or displaces it, and the header sits inside a
       scrolling region. It therefore carries the theme class `variables.css`
       re-applies dark values under, because the `.fe` ancestor that normally
       scopes the tokens is not above it any more. -->
  <Teleport to="body">
    <div
      v-if="colMenuOpen"
      class="fe-colmenu__backdrop"
      :class="colMenuThemeClass"
      :data-prefers-dark="prefersDark ? '1' : '0'"
      @click="closeColMenu"
      @contextmenu.prevent="closeColMenu"
    >
      <div
        ref="colMenuEl"
        class="fe-colmenu"
        role="dialog"
        tabindex="-1"
        :aria-label="t('cols.menu')"
        :style="{ top: colMenuY + 'px', left: colMenuX + 'px' }"
        @click.stop
        @keydown.esc.stop="closeColMenu"
      >
        <p class="fe-colmenu__title">{{ t('cols.title') }}</p>
        <div v-for="c in menuCols" :key="c.id" class="fe-colmenu__line">
          <button
            type="button"
            class="fe-colmenu__row"
            role="checkbox"
            :aria-checked="c.on ? 'true' : 'false'"
            @click="toggleCol(c.id)"
          >
            <span class="fe-list__check fe-colmenu__tick" :class="{ 'is-on': c.on }" aria-hidden="true"></span>
            <span class="fe-colmenu__label">{{ c.label }}</span>
          </button>
          <!-- The drag, reachable from a keyboard. Same move, same model — a
               reorder only a mouse can perform is one half the people using
               this cannot perform at all. -->
          <button
            type="button"
            class="fe-colmenu__nudge"
            :disabled="!c.canLeft"
            :title="t('cols.move_left', { col: c.label })"
            :aria-label="t('cols.move_left', { col: c.label })"
            @click="nudgeCol(c.id, -1)"
          ><span aria-hidden="true">&#8592;</span></button>
          <button
            type="button"
            class="fe-colmenu__nudge"
            :disabled="!c.canRight"
            :title="t('cols.move_right', { col: c.label })"
            :aria-label="t('cols.move_right', { col: c.label })"
            @click="nudgeCol(c.id, 1)"
          ><span aria-hidden="true">&#8594;</span></button>
        </div>
        <div v-if="canResetCols" class="fe-colmenu__sep" role="separator"></div>
        <button v-if="canResetCols" type="button" class="fe-colmenu__row" @click="onResetColumns">
          <span class="fe-colmenu__label">{{ t('cols.reset') }}</span>
        </button>
        <div v-if="thisFolderRemembered || rememberedFolders > 0" class="fe-colmenu__sep" role="separator"></div>
        <p v-if="thisFolderRemembered || rememberedFolders > 0" class="fe-colmenu__title">{{ t('cols.folder_title') }}</p>
        <!-- The rule, in one sentence, next to the two buttons that act on it.
             A per-folder memory nobody can explain is the difference between a
             feature and a haunting. -->
        <p v-if="thisFolderRemembered || rememberedFolders > 0" class="fe-colmenu__hint">{{ t('cols.folder_hint') }}</p>
        <button v-if="thisFolderRemembered" type="button" class="fe-colmenu__row" @click="onForgetFolder">
          <span class="fe-colmenu__label">{{ t('cols.forget_folder') }}</span>
        </button>
        <button v-if="rememberedFolders > 0" type="button" class="fe-colmenu__row" @click="onApplyToAll">
          <span class="fe-colmenu__label">{{ t('cols.apply_all', { count: rememberedFolders }) }}</span>
        </button>
      </div>
    </div>
  </Teleport>
</template>
