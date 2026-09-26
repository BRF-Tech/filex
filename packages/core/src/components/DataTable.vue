<script setup lang="ts">
/**
 * DataTable — THE table. There is no other.
 *
 * ⚠⚠ This is the explorer's list view with the files taken out of it. The
 * explorer's own listing (`ListView`) renders through it, and so does every
 * other table in the product: every admin page, the connection panels, My
 * shares, the notifications list and an app's `list` node. A raw `<table>` or
 * a second table component anywhere in `web/src` or `packages/core/src` fails
 * `web/tests/ui/tablePinnedActions.test.ts`, and `docs/CONTRIBUTING.md` → "UI
 * rules" says why.
 *
 * The owner, 2026-09-21, verbatim: "Artık explore tablomuz bizim her yerde
 * kullanacağımız tablo yapısıdır; bir yere tablo gerekiyorsa bu tabloyu koymak
 * zorundayız." And, about the round before: "Admin tabloları hâlâ explore
 * tablolarıyla AYNI KODDA DEĞİL … tablo sütunları düzenlenebilir değil,
 * büyütme küçültme yok, sıralama yok." That round had built `ui/Table.vue`, a
 * second table that imitated this one — the same frozen edges, the same
 * Actions menu — and got exactly the parts somebody remembered to copy. The
 * rest (resizing, sorting, the column menu, persistence) lived here and never
 * reached the admin panel. The imitation is deleted; this is the one table.
 *
 * WHAT EVERY TABLE THEREFORE GETS, whoever draws it:
 *
 *   · resizable columns — drag the edge, arrow keys on the focused handle,
 *     double-click to put a column back to its shipped width;
 *   · sorting — click a header, click it again to reverse; or, over rows that
 *     are one page of many, headers that are CLOSED and say why, because
 *     sorting one page of a paginated list would lie about the order of the
 *     rest (the same shape as the explorer's ranked search results);
 *   · a column menu (the header's ⋮ or a right-click on the header): show and
 *     hide columns, step them left and right, reset;
 *   · reordering by dragging a header;
 *   · all of it REMEMBERED — on the account, per table (`tableId` →
 *     `lib/tablePrefs`), or per folder for the explorer (its own store);
 *   · the lead column frozen on the left and ONE actions control frozen on
 *     the right while the rest scrolls sideways — the table is as wide as its
 *     columns and scrolls, it never sheds a column for want of room.
 *
 * ⚠ The markup and every class name are the explorer's (`fe-list__*`), on
 * purpose: its stylesheet in `styles/base.css` is this table's stylesheet,
 * and the end-to-end suites that address the explorer's rows keep addressing
 * them. Nothing here carries a colour of its own — every value is a `--fe-*`
 * token, so every table follows the palette the person picked.
 */
import { computed, getCurrentInstance, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import {
  createColumnStore,
  memoryBacking,
  type ColumnStore,
  type LayoutMetrics,
  type TableColumnSpec,
} from '../lib/tableColumns';
import { setTableSort, tableColumnBacking, tableSort } from '../lib/tablePrefs';
import { useTableEnv } from '../lib/tableEnv';
import { clampAlongInline, dirOfElement, inlineKeyStep, inlineSign, inlineStartX } from '../lib/direction';
import ItemCheck from './ItemCheck.vue';
import RowActions from './RowActions.vue';
import type { ContextAction } from './ContextMenu.vue';

/** One column of a table. */
export interface DataColumn<Row = any> {
  /** Stable id — the cell slot is `cell-<id>`, and it is what is remembered. */
  id: string;
  /** Header text (translated by the caller). */
  label: string;
  /** Shipped width, px. */
  width?: number;
  min?: number;
  max?: number;
  /** May the person hide it (and therefore move it)? Default true. */
  hideable?: boolean;
  /** May the person drag its edge? Default true. */
  resizable?: boolean;
  /** Does a header click sort by it? Default false. */
  sortable?: boolean;
  /** The first direction a click sorts in. Default 'asc'. */
  sortDir?: 'asc' | 'desc';
  /** What the column sorts by when the TABLE sorts (uncontrolled). Default:
   *  `format(row)`, else `row[id]`. Numbers sort as numbers, strings in the
   *  viewer's collation with digits read as numbers, empty values last. */
  sortValue?: (row: Row) => string | number | boolean | null | undefined;
  /** The cell's text when no `cell-<id>` slot is given. Default `row[id]`. */
  format?: (row: Row) => string | number | null | undefined;
  /** Hover text for the cell. Default: the text itself when it may be cut. */
  title?: (row: Row) => string | undefined;
  align?: 'left' | 'right' | 'center';
  /** Extra class on the header cell and every body cell of the column. */
  class?: string;
  /** Draw the header label? The explorer's star column draws none. */
  headerLabel?: boolean;
  /** The column that says WHICH ROW this is: pinned first, frozen left, never
   *  hidden or moved. Default: the first column. */
  lead?: boolean;
}

interface Group {
  id: string;
  label?: string;
  items: any[];
}

const props = withDefaults(
  defineProps<{
    rows: any[];
    columns: DataColumn[];
    /** How a row is keyed: a property name or a function. Default: its index. */
    rowKey?: string | ((row: any) => string | number);
    /** The name its arrangement is remembered under (`lib/tablePrefs`).
     *  Every table in the product passes one; the guard test refuses a table
     *  without. Absent = remembered for this mount only. */
    tableId?: string;
    /** The explorer passes its own per-folder store; everybody else gets one
     *  built from `tableId`. */
    columnStore?: ColumnStore;
    /** Which non-lead columns this listing will draw at all (the explorer's
     *  Location only in cross-folder views). Default: every column. */
    candidates?: string[];
    /** Table arithmetic, for a caller whose tracks differ (the explorer). */
    metrics?: LayoutMetrics;
    locale?: LocaleCode;
    theme?: ThemeMode;
    loading?: boolean;
    /** What an empty table says. */
    empty?: string;
    ariaLabel?: string;
    /** A tick column that selects (issue #26). */
    selectable?: boolean;
    isSelected?: (row: any) => boolean;
    /**
     * CONTROLLED sort: pass it (null = "no sort") and handle `sort`; the
     * caller orders the rows (a server, or the explorer's sort store). Leave
     * it out and the table sorts the rows itself and remembers the choice.
     */
    sort?: { key: string; dir: 'asc' | 'desc' } | null;
    /** Headers closed, with this sentence as the reason (the explorer's
     *  ranked search results). */
    sortClosed?: string | null;
    /** Rows in groups under headings (the explorer's date headings). The
     *  caller has already ordered them; the table draws them as given. */
    groups?: Group[];
    rowClass?: (row: any, index: number) => string | Record<string, boolean> | undefined;
    /** Attributes for a row (`data-*`, `aria-*`, `draggable`, `tabindex`). */
    rowAttrs?: (row: any) => Record<string, unknown>;
    /** The row's verbs — drawn as ONE labelled Actions control frozen right. */
    rowActions?: (row: any) => ContextAction[];
    /** A test hook for a row's Actions control. */
    rowActionsTestId?: (row: any) => string | undefined;
    /** The trailing column's content width, px (the explorer's ⋮ is 28). */
    actionsWidth?: number;
    /** A card around the table, with the toolbar and pager inside it. The
     *  explorer's listing is not framed — its pane is the frame. */
    framed?: boolean;
    page?: number;
    pageSize?: number;
    total?: number;
    /** Pages, for an endpoint that answers "there is another page" and no
     *  total. */
    pages?: number;
    /** A line in the footer (a count, a note). */
    footNote?: string;
  }>(),
  {
    locale: undefined,
    framed: true,
    rowKey: undefined,
    tableId: undefined,
    columnStore: undefined,
    candidates: undefined,
    metrics: undefined,
    theme: undefined,
    empty: undefined,
    ariaLabel: undefined,
    isSelected: undefined,
    sort: undefined,
    sortClosed: undefined,
    groups: undefined,
    rowClass: undefined,
    rowAttrs: undefined,
    rowActions: undefined,
    rowActionsTestId: undefined,
    actionsWidth: undefined,
    page: undefined,
    pageSize: undefined,
    total: undefined,
    pages: undefined,
    footNote: undefined,
  },
);

const emit = defineEmits<{
  (e: 'sort', payload: { key: string; dir: 'asc' | 'desc' }): void;
  (e: 'row-click', row: any, ev: MouseEvent): void;
  (e: 'row-dblclick', row: any, ev: MouseEvent): void;
  (e: 'row-contextmenu', row: any, ev: MouseEvent): void;
  (e: 'check-click', row: any, ev: MouseEvent): void;
  (e: 'row-action', key: string, row: any, action: ContextAction): void;
  (e: 'row-dragstart', row: any, ev: DragEvent): void;
  (e: 'row-dragover', row: any, ev: DragEvent): void;
  (e: 'row-dragleave', row: any, ev: DragEvent): void;
  (e: 'row-drop', row: any, ev: DragEvent): void;
  (e: 'row-touchstart', row: any, ev: TouchEvent): void;
  (e: 'row-touchend', ev: TouchEvent): void;
  (e: 'row-touchmove', ev: TouchEvent): void;
  (e: 'page', page: number): void;
}>();

/* ⚠ Two roots (the table and the teleported column menu), so a class or a
 * test id the caller puts on <DataTable> would otherwise go nowhere. */
defineOptions({ inheritAttrs: false });

/* The host's language and light/dark mode (lib/tableEnv) unless this table
 * was handed its own. */
const env = useTableEnv();
const lang = computed<LocaleCode>(() => props.locale ?? env?.locale.value ?? 'en');
const mode = computed<ThemeMode | undefined>(() => props.theme ?? env?.theme?.value);

// ⚠ RTL: `dir` (the table's language) goes on the teleported column menu; the
// table's own maths reads the direction it is actually DRAWN in
// (`dirOfElement(listEl)`), because a pointer only knows physical x.
const { t, dir } = useLocale(() => lang.value);

// ── the columns ───────────────────────────────────────────────────────

const colById = computed(() => {
  const m = new Map<string, DataColumn>();
  for (const c of props.columns) m.set(c.id, c);
  return m;
});

const leadId = computed(() => (props.columns.find((c) => c.lead) ?? props.columns[0])?.id ?? '');

/** The columns as the store sees them. */
const specs = computed<TableColumnSpec[]>(() =>
  props.columns.map((c) => {
    const lead = c.id === leadId.value;
    const width = c.width ?? (lead ? 240 : 140);
    return {
      id: c.id,
      width,
      min: Math.min(c.min ?? (lead ? 120 : 56), width),
      max: Math.max(c.max ?? 900, width),
      hideable: lead ? false : c.hideable !== false,
      resizable: c.resizable !== false,
    };
  }),
);

/**
 * THE store: the caller's (the explorer's per-folder one), else one of this
 * table's own, remembered under `tableId` on the account.
 *
 * ⚠ Built once per mount. `tableId` does not change under a mounted table in
 * any caller, and a store rebuilt on every render would throw away the
 * `computed` that keeps the parse to once per change.
 */
const ownStore = createColumnStore(
  () => specs.value,
  props.tableId ? tableColumnBacking(props.tableId) : memoryBacking(),
  { lead: leadId.value },
);
const store = computed<ColumnStore>(() => props.columnStore ?? ownStore);

/** The ids this listing is willing to draw at all, lead excluded. */
const candidateCols = computed<string[]>(() =>
  (props.candidates ?? props.columns.map((c) => c.id)).filter((id) => id !== store.value.lead),
);

// ── the geometry ──────────────────────────────────────────────────────

/** `--fe-gap-sm` between cells and `--fe-gap` down each side. */
const CELL_GAP = 8;
const ROW_PAD = 12;
/** The tick's frozen track: its 28px plus the row padding that moves INTO it. */
const CHECK_TRACK = 28;
const LEAD_CHECK = CHECK_TRACK + ROW_PAD;

/** The trailing column's content width. The explorer's ⋮ is 28; a labelled
 *  "Actions" control needs room for its word. */
const menuContent = computed(() => props.actionsWidth ?? (props.rowActions ? 92 : 28));

const metrics = computed<LayoutMetrics>(() => ({
  gap: CELL_GAP,
  padding: ROW_PAD * 2,
  check: props.selectable ? CHECK_TRACK : 0,
  menu: menuContent.value,
  ...(props.metrics ?? {}),
}));

/**
 * The width the table actually has — its OWN scrollport, watched with a
 * ResizeObserver, not the window's. An embed in a side panel is narrow on a
 * wide screen. 0 until the first observation, which the layout reads as
 * "keep the shipped widths" so the first paint is not a guess that jumps.
 */
const listEl = ref<HTMLElement | null>(null);
const listWidth = ref(0);
let ro: ResizeObserver | null = null;

/** Is the table scrolled off its START edge? Draws the frozen edges' dividers —
 *  a permanent divider would claim a column floats when it simply fits. */
const scrolledX = ref(false);

function onListScroll(ev: Event) {
  const el = ev.currentTarget as HTMLElement | null;
  /* ⚠⚠ RTL: `scrollLeft` is 0 at the start edge and goes NEGATIVE as a
     right-to-left table scrolls toward its end (the CSSOM rule every current
     engine follows). `> 0` was never true there, so the frozen Name and ⋮
     never drew their edges. Distance from the start, either way. */
  const on = Math.abs(el?.scrollLeft ?? 0) > 0;
  if (on !== scrolledX.value) scrolledX.value = on;
}

/* ⚠ The observer is (re)attached whenever the element appears, not only at
 * mount (filex lesson #226): a table that mounts inside a `v-if` that opens
 * later would otherwise lay out against 0px forever. */
watch(
  listEl,
  (el) => {
    ro?.disconnect();
    ro = null;
    if (!el || typeof ResizeObserver === 'undefined') return;
    /* ⚠ Straight in, no filter (PR #39's "settler" was taken back out). The
       loop it guarded against — this width feeding a layout that summons the
       vertical scrollbar that changes this width — cannot turn over any more:
       `.fe-list` reserves the scrollbar's room (`scrollbar-gutter: stable`,
       base.css), so the scrollbar never changes what is observed here. The
       filter itself held a REAL resize that came back within 500 ms (a panel
       toggled open and shut) at the narrower width: measured, the table stayed
       300px narrower than its pane (e2e 139). */
    ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect?.width ?? 0;
      if (Math.abs(w - listWidth.value) >= 1) listWidth.value = w;
    });
    ro.observe(el);
  },
  { flush: 'post' },
);

const layout = computed(() => store.value.layout(listWidth.value, candidateCols.value, metrics.value));
const visibleCols = computed<string[]>(() => layout.value.visible);

/** ONE object, bound to the header and to every row, so the two cannot drift
 *  out of line with each other when the table is scrolled sideways. */
const tableStyle = computed(() => ({ width: `${layout.value.total}px` }));

/**
 * THE FROZEN LEAD — only while it leaves room to scroll.
 *
 * ⚠⚠ A sticky cell wider than the pane covers the pane: 900px of Name frozen
 * in a 706px list paints over every column it was frozen to keep company. So
 * at least `PIN_LEAD_ROOM` of the pane has to be left for the columns that
 * slide under it, or nothing is pinned and the table scrolls as one piece.
 */
const PIN_LEAD_ROOM = 160;

const leadWidth = computed(
  () =>
    (props.selectable ? LEAD_CHECK + CELL_GAP : ROW_PAD) +
    (layout.value.widths[store.value.lead] ?? store.value.width(store.value.lead)),
);

const pinLead = computed(
  () => listWidth.value > 0 && listWidth.value - leadWidth.value >= PIN_LEAD_ROOM,
);

/** The trailing control's own track: its content plus the row padding. */
const menuWidth = computed(() => menuContent.value + ROW_PAD);

/**
 * THE FROZEN CONTROL — the same rule as the lead's, for the same reason.
 *
 * ⚠⚠ A sticky cell paints over whatever slides beneath it, and it carries
 * an opaque ground so it must. A labelled `Actions` control is 104px; in a
 * 265px inspector panel it covered the two columns an app was drawing
 * (State, When) so completely that its signer table read as names and
 * buttons with a gap between them, while the person's promised "Waiting /
 * Signed / Refused" was under the pinned cell (v0.43.0). So a control is
 * frozen only while it leaves the columns sliding under it somewhere to be:
 * `PIN_MENU_ROOM` of readable middle beside the lead. Below that nothing is
 * pinned, the row scrolls as one piece, and the person sees the columns
 * first and reaches the verbs with the same swipe the table already asks
 * for.
 *
 * ⚠ Unmeasured (0 on the first tick, a test with no layout) keeps it
 * pinned: that is what every table did before this rule, so nothing flashes.
 */
const PIN_MENU_ROOM = 160;

const pinMenu = computed(
  () =>
    listWidth.value <= 0 ||
    listWidth.value - leadWidth.value - menuWidth.value >= PIN_MENU_ROOM,
);

/** The lead's sticky offset: after the tick when there is one, the edge when
 *  there is not. */
const rootStyle = computed(() => ({
  '--fe-list-lead': props.selectable ? `${LEAD_CHECK + CELL_GAP}px` : '0px',
}));

/** A drawn track's width, as the inline style the cell carries. `flex-basis`,
 *  not a grid track: a sticky GRID item is confined to its own grid area and
 *  cannot move at all. */
function colStyle(id: string) {
  let w = layout.value.widths[id] ?? store.value.width(id);
  /* Frozen with no tick in front of it, the lead takes the row's leading
     padding INTO itself — `left: 0` pulls it to the glass, and a 12px inset
     left on the row would scroll away and put the text against the edge. */
  if (id === store.value.lead && pinLead.value && !props.selectable) w += ROW_PAD;
  return { flex: `0 0 ${w}px` };
}

/** The trailing cell's track: content plus the 12px of row padding it absorbs. */
const menuStyle = computed(() =>
  menuContent.value === 28 ? undefined : { flex: `0 0 ${menuContent.value + ROW_PAD}px` },
);

function colClass(id: string): string[] {
  const c = colById.value.get(id);
  const out = ['fe-list__col', 'fe-list__cell'];
  /* ⚠ The column's OWN class before the lead role. CSS does not care about
     the order, but anything that names a cell by its first
     `fe-list__col--<id>` (the e2e column specs, a person's user stylesheet
     inspected in devtools) read "lead" for the Name column instead of
     "name" once the lead marker came first — 108-column-reorder went red
     on every case on a table that was working. */
  if (c?.class) out.push(c.class);
  if (id === store.value.lead) out.push('fe-list__col--lead');
  if (c?.align === 'right') out.push('is-right');
  else if (c?.align === 'center') out.push('is-center');
  return out;
}

function label(id: string): string {
  return colById.value.get(id)?.label ?? id;
}

// ── sorting ───────────────────────────────────────────────────────────

const controlled = computed(() => props.sort !== undefined);

/** The table's own sort, when nobody controls it — remembered per table. */
const localSort = ref<{ key: string; dir: 'asc' | 'desc' } | null>(
  props.tableId ? tableSort(props.tableId) : null,
);

const activeSort = computed(() => (controlled.value ? (props.sort ?? null) : localSort.value));

/** More rows exist than are on screen: the list is one page of several. */
const paged = computed(
  () => (props.pages ?? 1) > 1 || (props.total != null && props.total > props.rows.length),
);

/**
 * WHY the headers are closed, or null when they are open.
 *
 * ⚠⚠ A table that sorts its own rows while holding ONE PAGE of a longer list
 * would put "A" at the top of page 2 and "B" at the top of page 1 and call the
 * result sorted. The honest answer is the one the explorer already gives over
 * a ranked search: the headers close and say why. A caller whose SERVER sorts
 * passes `sort` and is never closed for this.
 */
const closedWhy = computed<string | null>(() => {
  if (props.sortClosed) return props.sortClosed;
  if (!controlled.value && paged.value) return t('table.sort_paged');
  return null;
});

function isSortable(id: string): boolean {
  return colById.value.get(id)?.sortable === true;
}

function toggleSort(id: string) {
  /* ⚠⚠ The `disabled` attribute is the AFFORDANCE, not the rule: a click
   * dispatched in script still runs this listener. Measured on the explorer
   * 2026-09-13: a scripted click on a closed header re-ordered nothing on
   * screen and still wrote a new stored sort. */
  if (closedWhy.value || !isSortable(id)) return;
  const cur = activeSort.value;
  const col = colById.value.get(id);
  const next =
    cur && cur.key === id
      ? { key: id, dir: cur.dir === 'asc' ? ('desc' as const) : ('asc' as const) }
      : { key: id, dir: col?.sortDir ?? ('asc' as const) };
  if (!controlled.value) {
    localSort.value = next;
    if (props.tableId) setTableSort(props.tableId, next);
  }
  emit('sort', next);
}

function sortArrow(id: string): string {
  const s = activeSort.value;
  if (closedWhy.value || !s || s.key !== id) return '';
  return s.dir === 'asc' ? '↑' : '↓';
}

function ariaSort(id: string): 'ascending' | 'descending' | 'none' | undefined {
  if (!isSortable(id)) return undefined;
  const s = activeSort.value;
  if (closedWhy.value || !s || s.key !== id) return 'none';
  return s.dir === 'asc' ? 'ascending' : 'descending';
}

const sortTitle = computed(() => closedWhy.value ?? t('col.sort'));

function valueOf(row: any, id: string): unknown {
  const c = colById.value.get(id);
  if (c?.sortValue) return c.sortValue(row);
  if (c?.format) return c.format(row);
  return row?.[id];
}

/** Empty last in both directions; numbers as numbers; words in the viewer's
 *  collation with digits read as numbers ("file 9" before "file 10"). */
function compareValues(a: unknown, b: unknown): number {
  const ae = a === null || a === undefined || a === '';
  const be = b === null || b === undefined || b === '';
  if (ae && be) return 0;
  if (ae) return 1;
  if (be) return -1;
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  if (typeof a === 'boolean' && typeof b === 'boolean') return Number(a) - Number(b);
  return String(a).localeCompare(String(b), lang.value, { numeric: true, sensitivity: 'base' });
}

const sortedRows = computed<any[]>(() => {
  const s = activeSort.value;
  if (controlled.value || closedWhy.value || !s || !colById.value.has(s.key)) return props.rows;
  const dir = s.dir === 'asc' ? 1 : -1;
  return props.rows
    .map((row, i) => ({ row, i, v: valueOf(row, s.key) }))
    .sort((x, y) => {
      const ae = x.v === null || x.v === undefined || x.v === '';
      const be = y.v === null || y.v === undefined || y.v === '';
      // ⚠ Empties go last in BOTH directions, so the arrow never floats a
      // column of dashes to the top.
      if (ae !== be) return ae ? 1 : -1;
      return dir * compareValues(x.v, y.v) || x.i - y.i;
    })
    .map((x) => x.row);
});

const segments = computed<Group[]>(() => props.groups ?? [{ id: 'all', items: sortedRows.value }]);

const rowCount = computed(() => segments.value.reduce((s, g) => s + g.items.length, 0));

// ── rows ──────────────────────────────────────────────────────────────

function keyOf(row: any, i: number): string | number {
  if (typeof props.rowKey === 'function') return props.rowKey(row);
  if (props.rowKey) return row?.[props.rowKey] as string | number;
  return i;
}

function selected(row: any): boolean {
  return props.isSelected ? props.isSelected(row) : false;
}

function cellValue(row: any, id: string): unknown {
  return row?.[id];
}

function cellText(row: any, id: string): string {
  const c = colById.value.get(id);
  const v = c?.format ? c.format(row) : row?.[id];
  if (v === null || v === undefined || v === '') return '—';
  return String(v);
}

function cellTitle(row: any, id: string): string | undefined {
  const c = colById.value.get(id);
  if (c?.title) return c.title(row);
  const text = cellText(row, id);
  return text === '—' ? undefined : text;
}

function rowClasses(row: any, i: number) {
  return [
    'fe-list__row',
    { 'is-selected': selected(row), 'is-clickable': !!rowClickable.value },
    props.rowClass?.(row, i),
  ];
}

/** Rows answer a click only when somebody listens for one. */
const rowClickable = ref(false);

// ── resizing ──────────────────────────────────────────────────────────

/** `sign` is the gesture's direction, read ONCE at pointerdown: +1 when the
 *  column's trailing edge is its right (LTR), -1 when it is its left (RTL). */
let resizing: { id: string; startX: number; startW: number; sign: 1 | -1 } | null = null;

/**
 * THE FIRST DRAG FREEZES THE WHOLE ROW. Until somebody sizes a column the
 * widths are derived from the pane and the auto lead takes the slack — so
 * narrowing one column would widen the lead, which is the behaviour the owner
 * called wrong. Committing every drawn width at the start of the gesture
 * turns the drag into what it looks like.
 */
function freezeBeforeEdit() {
  if (!store.value.widthsAreAuto()) return;
  store.value.freeze(layout.value.widths);
}

function isResizable(id: string): boolean {
  return store.value.spec(id)?.resizable === true;
}

function onResizeStart(id: string, ev: PointerEvent) {
  if (!isResizable(id)) return;
  ev.preventDefault();
  ev.stopPropagation();
  freezeBeforeEdit();
  /* ⚠⚠ RTL: the handle sits on the column's inline END edge — its LEFT side in
   * a right-to-left table — so dragging it LEFT widens the column. A column
   * edge follows the pointer in both directions; without the sign it grew
   * when the pointer went the other way. */
  resizing = { id, startX: ev.clientX, startW: store.value.width(id), sign: inlineSign(dirOfElement(listEl.value)) };
  /* ⚠ On the WINDOW, not a pointer capture on the handle: the pointer leaves
   * the 9px handle within a few pixels, and a capture on an element Vue
   * re-renders mid-drag is released the moment it is patched. */
  window.addEventListener('pointermove', onResizeMove);
  window.addEventListener('pointerup', onResizeEnd);
}

function onResizeMove(ev: PointerEvent) {
  if (!resizing) return;
  store.value.setWidth(resizing.id, resizing.startW + resizing.sign * (ev.clientX - resizing.startX));
}

function onResizeEnd() {
  resizing = null;
  window.removeEventListener('pointermove', onResizeMove);
  window.removeEventListener('pointerup', onResizeEnd);
}

/** Keyboard resizing — the handle is focusable, so it has to do something.
 *  16px a step, about one character of the 13px body text. */
function onResizeKey(id: string, ev: KeyboardEvent) {
  /* ⚠ Ctrl/⌘ + arrow is the REORDER (onHeadKey), and the handle sits inside
     the header cell the reorder listens on. */
  if (ev.ctrlKey || ev.metaKey) return;
  /* The arrow moves the EDGE the way it points: → widens in LTR, ← in RTL. */
  const step = 16 * inlineKeyStep(ev.key, dirOfElement(listEl.value));
  if (!step) return;
  ev.preventDefault();
  freezeBeforeEdit();
  store.value.setWidth(id, store.value.width(id) + step);
}

/** Double-click a handle: back to this column's shipped width. */
function onResizeReset(id: string) {
  const spec = store.value.spec(id);
  if (!spec) return;
  freezeBeforeEdit();
  store.value.setWidth(id, spec.width);
}

// ── reordering ────────────────────────────────────────────────────────

/**
 * DRAG A HEADER TO MOVE ITS COLUMN.
 *
 * ⚠⚠ The threshold is the whole difficulty: clicking a header sorts and
 * dragging it moves, and both start with the same pointerdown. Nothing
 * happens until the pointer has travelled `DRAG_SLOP`; above it, the click
 * the browser fires on pointerup is swallowed on its way past
 * (`onHeadClickCapture`), or every completed drag would also re-sort.
 */
const DRAG_SLOP = 5;
let dragState: { id: string; startX: number; armed: boolean } | null = null;
const dragCol = ref<string | null>(null);
/**
 * Where it would land: a GAP index into the DRAWN columns (with the dragged
 * one still in them — that is the row on screen). ⚠⚠ The drop reads this
 * number against the SAME list; reading it against the list without the
 * dragged column once turned "one place right" into three (2026-09-20).
 * `null` means the release would change nothing, and the marker is absent.
 */
const dropAt = ref<number | null>(null);
/** When a real drag last ended — a TIMESTAMP, because a flag left armed by a
 *  drag that produced no click would eat the next, unrelated sort click. */
let dragEndedAt = 0;
const CLICK_SWALLOW_MS = 300;

function isMovable(id: string): boolean {
  return id !== store.value.lead && store.value.spec(id)?.hideable === true;
}

function onHeadPointerDown(id: string, ev: PointerEvent) {
  if (ev.button !== 0 || !isMovable(id)) return;
  dragState = { id, startX: ev.clientX, armed: false };
  window.addEventListener('pointermove', onHeadPointerMove);
  window.addEventListener('pointerup', onHeadPointerUp);
  window.addEventListener('keydown', onDragKey, true);
  /* ⚠ Every way a gesture can END, not just the happy one — a pointer the
     browser takes away fires neither pointerup nor keydown. */
  window.addEventListener('pointercancel', onDragAbort);
  window.addEventListener('blur', onDragAbort);
}

function onDragAbort() {
  const armed = dragState?.armed === true;
  endDrag();
  if (armed) dragEndedAt = Date.now();
}

/** The gaps a dragged column may be dropped into: the pinned columns at
 *  either end are un-passable (the explorer's star stays beside its ⋮). */
function dropGapRange(): { first: number; last: number } {
  const drawn = visibleCols.value;
  let first = 0;
  while (first < drawn.length && !isMovable(drawn[first])) first++;
  let last = drawn.length;
  while (last > first && !isMovable(drawn[last - 1])) last--;
  return { first, last };
}

function onHeadPointerMove(ev: PointerEvent) {
  if (!dragState) return;
  if (!dragState.armed) {
    if (Math.abs(ev.clientX - dragState.startX) < DRAG_SLOP) return;
    dragState.armed = true;
    dragCol.value = dragState.id;
  }
  const head = listEl.value?.querySelector('.fe-list__head');
  if (!head) return;
  /* The header's own cells. ⚠ `children`, not `:scope > …`: the same answer
     in a browser, and the one a DOM without `:scope` (happy-dom) gives too —
     the RTL drop test (web/tests/ui/rtlGeometry) measured nothing without it. */
  const cells = Array.from(head.children).filter((c) => c.classList.contains('fe-list__col')) as HTMLElement[];
  /* The tick (when there is one) and the lead come first, the actions column
     last; only the ones between are positions a column can take. */
  const lead = props.selectable ? 2 : 1;
  const drawnCells = cells.slice(lead, lead + visibleCols.value.length);
  /* ⚠ RTL: gap i is BEFORE cell i in reading order — its right-hand half in a
   * right-to-left table. The pointer is compared against each cell's middle
   * in the direction the columns run. */
  const sign = inlineSign(dirOfElement(listEl.value));
  let idx = drawnCells.length;
  for (let i = 0; i < drawnCells.length; i++) {
    const box = drawnCells[i].getBoundingClientRect();
    if (sign * (ev.clientX - (box.left + box.width / 2)) < 0) {
      idx = i;
      break;
    }
  }
  const { first, last } = dropGapRange();
  idx = Math.max(first, Math.min(last, idx));
  /* ⚠⚠ Its own two gaps are NOT a drop: landing in either leaves the order as
   * it was, so the marker hides and the release writes nothing. (Owner,
   * 2026-09-20: "bırakmadan kendi yerine geri getirip bırakınca yine bir adım
   * ileri gidiyor".) */
  const from = visibleCols.value.indexOf(dragState.id);
  dropAt.value = idx === from || idx === from + 1 ? null : idx;
}

function onHeadPointerUp() {
  const st = dragState;
  const target = dropAt.value;
  endDrag();
  if (!st?.armed) return;
  dragEndedAt = Date.now();
  if (target == null) return;
  applyColumnDrop(st.id, target);
}

/**
 * A gap among the DRAWN columns → a place in the full stored order, through
 * the NEIGHBOUR's identity: a column this listing does not draw still holds a
 * place in the stored sequence, and dropping between two visible columns must
 * not silently reshuffle the one hiding between them.
 */
function applyColumnDrop(id: string, gap: number) {
  const drawn = visibleCols.value;
  const from = drawn.indexOf(id);
  if (from === -1) return;
  if (gap === from || gap === from + 1) return;
  const restDrawn = drawn.filter((c) => c !== id);
  const beforeId = restDrawn[gap > from ? gap - 1 : gap];
  const restFull = store.value.order().filter((c) => c !== id);
  const index = beforeId ? restFull.indexOf(beforeId) : restFull.length;
  if (index === -1) return;
  store.value.move(id, index);
}

/**
 * The columns a keyboard step may land among: the movable ones THIS LISTING
 * offers, in the person's order. ⚠ Not the stored order — a column this
 * listing does not draw (the explorer's Location in an ordinary folder) would
 * be stepped over invisibly and the button would read as broken (measured
 * 2026-09-20).
 */
function steppableCols(): string[] {
  return store.value.order().filter((id) => isMovable(id) && candidateCols.value.includes(id));
}

function stepColumn(id: string, delta: -1 | 1) {
  const list = steppableCols();
  const at = list.indexOf(id);
  const to = at + delta;
  if (at === -1 || to < 0 || to >= list.length) return;
  const neighbour = list[to];
  const rest = store.value.order().filter((c) => c !== id);
  const k = rest.indexOf(neighbour);
  if (k === -1) return;
  store.value.move(id, delta === -1 ? k : k + 1);
}

function canStepColumn(id: string, delta: -1 | 1): boolean {
  const list = steppableCols();
  const at = list.indexOf(id);
  return at !== -1 && at + delta >= 0 && at + delta < list.length;
}

/** Ctrl/⌘ + ←/→ on a focused header moves its column one place — the drag,
 *  reachable without a pointer. ⚠ RTL: the column moves the way the arrow
 *  points, which is one place EARLIER for → in a right-to-left table. */
function onHeadKey(id: string, ev: KeyboardEvent) {
  if (!ev.ctrlKey && !ev.metaKey) return;
  const delta = inlineKeyStep(ev.key, dirOfElement(listEl.value));
  if (!delta || !isMovable(id)) return;
  ev.preventDefault();
  ev.stopPropagation();
  stepColumn(id, delta as -1 | 1);
}

function onDragKey(ev: KeyboardEvent) {
  if (ev.key !== 'Escape') return;
  ev.stopPropagation();
  onDragAbort();
}

/** THE ONE PLACE THE DRAG STATE IS TORN DOWN — the state the template reads
 *  and the window listeners, always both. */
function endDrag() {
  dragState = null;
  dragCol.value = null;
  dropAt.value = null;
  window.removeEventListener('pointermove', onHeadPointerMove);
  window.removeEventListener('pointerup', onHeadPointerUp);
  window.removeEventListener('keydown', onDragKey, true);
  window.removeEventListener('pointercancel', onDragAbort);
  window.removeEventListener('blur', onDragAbort);
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
 * A small settings popover, opened by the header's ⋮ or by right-clicking
 * the header. ⚠ Not `ContextMenu`: it is a panel of checkboxes, and a menu
 * closes on every pick, so turning four columns off would be four separate
 * right-clicks.
 */
const colMenuOpen = ref(false);
const colMenuX = ref(0);
const colMenuY = ref(0);
/** The x the menu was opened at — kept apart from `colMenuX`, which the clamp rewrites. */
let colMenuAnchor = 0;
const colMenuEl = ref<HTMLElement | null>(null);

const prefersDark = ref(false);
onMounted(() => {
  try {
    prefersDark.value = window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
  } catch {
    /* jsdom / no matchMedia */
  }
});
/** ⚠ The menu is teleported to <body>, outside the `.fe` ancestor that scopes
 *  the explorer's tokens, so it carries the theme class `variables.css`
 *  re-applies dark values under — the same reason ContextMenu takes one. */
const colMenuThemeClass = computed(() => `fe-ctx-backdrop--theme-${mode.value || 'auto'}`);

/**
 * ⚠ RTL — the two nudges follow READING order, like every other logical
 * thing: the first moves the column one place EARLIER (toward the table's
 * start edge), the second one place later, and they sit in that order. What
 * they SAY is physical — `cols.move_left` "Move {col} left" under ← — so in a
 * right-to-left table, where earlier is RIGHTWARD, the first one wears → and
 * `cols.move_right`, the second ← and `cols.move_left`. Arrow, label and
 * movement agree in both directions, and the tab order is the reading order
 * (a CSS `order` swap would have kept the picture and broken that).
 */
const nudge = computed(() =>
  dir.value === 'rtl'
    ? { earlier: { key: 'cols.move_right', arrow: '\u2192' }, later: { key: 'cols.move_left', arrow: '\u2190' } }
    : { earlier: { key: 'cols.move_left', arrow: '\u2190' }, later: { key: 'cols.move_right', arrow: '\u2192' } },
);

const menuCols = computed(() =>
  /* In the person's OWN order, so the list reads as the table on screen. */
  store.value
    .order()
    .filter((id) => isMovable(id) && candidateCols.value.includes(id))
    .map((id) => ({
      id,
      label: label(id),
      on: !store.value.hidden(id),
      canEarlier: canStepColumn(id, -1),
      canLater: canStepColumn(id, 1),
    })),
);

const canResetCols = computed(() => store.value.customised());

async function openColMenu(ev: MouseEvent) {
  ev.preventDefault();
  ev.stopPropagation();
  const el = ev.currentTarget as HTMLElement | null;
  const box = el?.getBoundingClientRect();
  // ⚠ RTL: a keyboard open (no pointer) hangs the menu from the ⋮'s START edge.
  colMenuAnchor = ev.clientX || (box ? inlineStartX(box, dir.value) : 0);
  colMenuX.value = colMenuAnchor;
  colMenuY.value = ev.clientY || (box ? box.bottom : 0);
  colMenuOpen.value = true;
  await nextTick();
  clampColMenu();
  colMenuEl.value?.focus();
}

function clampColMenu() {
  const el = colMenuEl.value;
  if (!el) return;
  const box = el.getBoundingClientRect();
  const pad = 8;
  /* ⚠ RTL: it hangs toward the inline END of its anchor — leftward in a
     right-to-left table — and is pushed back from THAT edge
     (lib/direction; the LTR arithmetic is unchanged). */
  colMenuX.value = clampAlongInline(colMenuAnchor, box.width, window.innerWidth, dir.value, pad);
  if (box.bottom > window.innerHeight - pad) {
    colMenuY.value = Math.max(pad, window.innerHeight - pad - box.height);
  }
}

function closeColMenu() {
  colMenuOpen.value = false;
}

/**
 * ESCAPE CLOSES IT, from wherever the focus happens to be. ⚠⚠ Half the
 * controls in the menu remove themselves when used ("Reset columns" vanishes
 * once there is nothing to reset), focus falls back to <body>, and a
 * full-screen backdrop that only heard Escape from inside itself became a
 * trap that swallowed every later pointer gesture (measured 2026-09-20).
 */
function onColMenuKey(ev: KeyboardEvent) {
  if (ev.key !== 'Escape') return;
  ev.stopPropagation();
  closeColMenu();
}

watch(colMenuOpen, (open) => {
  if (typeof window === 'undefined') return;
  if (open) window.addEventListener('keydown', onColMenuKey, true);
  else window.removeEventListener('keydown', onColMenuKey, true);
});

function toggleCol(id: string) {
  store.value.setHidden(id, !store.value.hidden(id));
}

function onResetColumns() {
  store.value.reset();
}

onBeforeUnmount(() => {
  ro?.disconnect();
  ro = null;
  onResizeEnd();
  endDrag();
  if (typeof window !== 'undefined') window.removeEventListener('keydown', onColMenuKey, true);
});

// ── the footer ────────────────────────────────────────────────────────

const totalPages = computed(() => {
  if (props.pages != null) return Math.max(1, props.pages);
  if (!props.total || !props.pageSize) return 1;
  return Math.max(1, Math.ceil(props.total / props.pageSize));
});
const currentPage = computed(() => props.page ?? 1);
const showPager = computed(() => totalPages.value > 1);

/** "26 – 50 / 58" when the endpoint knows the total. */
const rangeLabel = computed(() => {
  if (props.total != null && props.pageSize != null && props.total > 0) {
    const from = (currentPage.value - 1) * props.pageSize + 1;
    const to = Math.min(currentPage.value * props.pageSize, props.total);
    return `${from} – ${to} / ${props.total}`;
  }
  return '';
});

function go(p: number) {
  if (p < 1 || p > totalPages.value) return;
  emit('page', p);
}

// ── row events ────────────────────────────────────────────────────────

/* A row is "clickable" (pointer cursor) when the caller listens for clicks —
 * read off the vnode props the parent bound. */
const inst = getCurrentInstance();
rowClickable.value = !!(inst?.vnode.props as Record<string, unknown> | null)?.onRowClick;

function onRowClick(row: any, ev: MouseEvent) {
  emit('row-click', row, ev);
}

function onRowAction(key: string, row: any, action: ContextAction) {
  emit('row-action', key, row, action);
}

defineExpose({ store, layout, pinLead });
</script>

<template>
  <!-- ⚠ `display: contents` when unframed (the explorer): the listing's pane
       lays out `.fe-list` exactly as it did before there was a wrapper. -->
  <div v-bind="$attrs" class="fe-table" :class="{ 'fe-table--framed': framed, 'fe-table--bare': !framed }">
    <div v-if="$slots.toolbar" class="tbl-bar">
      <slot name="toolbar" />
    </div>
    <div
      ref="listEl"
      class="fe-list"
      :class="{
        'is-loading': loading,
        'is-scrolled-x': scrolledX,
        'is-pin-lead': pinLead,
        'is-pin-menu': pinMenu,
        'fe-list--nocheck': !selectable,
      }"
      :style="rootStyle"
      role="grid"
      :aria-label="ariaLabel"
      :aria-busy="loading ? 'true' : undefined"
      :data-table-id="tableId"
      @scroll.passive="onListScroll"
    >
      <!-- A listing read again keeps its rows until the answer comes (opening
           a folder, Refresh, a search): they are marked as the old ones, and
           a thin bar runs along the top. The list carried an `is-loading`
           class that nothing drew, so the previous folder's rows looked like
           the answer for as long as a large folder took. -->
      <div v-if="loading && rowCount > 0" class="fe-list__refreshing" role="status">
        <span class="fe-sr-only">{{ t('loading') }}</span>
      </div>
      <div
        class="fe-list__head"
        role="row"
        :class="{ 'is-dragging-col': !!dragCol }"
        :style="tableStyle"
        @contextmenu="openColMenu"
        @click.capture="onHeadClickCapture"
      >
        <!-- The tick column's header stays empty on purpose: a select-all tick
             is a real affordance with real consequences and none of the
             callers asks for one. -->
        <div v-if="selectable" class="fe-list__col fe-list__col--check" role="columnheader"></div>
        <div
          :class="colClass(store.lead)"
          role="columnheader"
          :style="colStyle(store.lead)"
          :aria-sort="ariaSort(store.lead)"
          :data-col="store.lead"
        >
          <button
            v-if="isSortable(store.lead)"
            type="button"
            class="fe-list__sort"
            :disabled="!!closedWhy"
            :title="sortTitle"
            @click="toggleSort(store.lead)"
          >
            {{ label(store.lead) }}
            <span v-if="sortArrow(store.lead)" class="fe-list__sort-arrow" aria-hidden="true">{{ sortArrow(store.lead) }}</span>
          </button>
          <span v-else class="fe-list__head-label">{{ label(store.lead) }}</span>
          <span
            v-if="isResizable(store.lead)"
            class="fe-list__resize"
            role="separator"
            aria-orientation="vertical"
            tabindex="0"
            :aria-label="t('cols.resize', { col: label(store.lead) })"
            :title="t('cols.resize', { col: label(store.lead) })"
            @pointerdown="onResizeStart(store.lead, $event)"
            @dblclick.stop="onResizeReset(store.lead)"
            @keydown="onResizeKey(store.lead, $event)"
          ></span>
        </div>
        <div
          v-for="(id, ci) in visibleCols"
          :key="id"
          :class="[
            colClass(id),
            {
              'is-col-dragging': dragCol === id,
              'is-drop-before': dropAt === ci,
              'is-drop-after': dropAt === visibleCols.length && ci === visibleCols.length - 1,
              'is-movable': isMovable(id),
            },
          ]"
          role="columnheader"
          :style="colStyle(id)"
          :aria-sort="ariaSort(id)"
          :aria-label="colById.get(id)?.headerLabel === false ? label(id) : undefined"
          :data-col="id"
          @pointerdown="onHeadPointerDown(id, $event)"
          @keydown="onHeadKey(id, $event)"
        >
          <button
            v-if="isSortable(id)"
            type="button"
            class="fe-list__sort"
            :disabled="!!closedWhy"
            :title="sortTitle"
            @click="toggleSort(id)"
          >
            {{ label(id) }}
            <span v-if="sortArrow(id)" class="fe-list__sort-arrow" aria-hidden="true">{{ sortArrow(id) }}</span>
          </button>
          <span v-else-if="colById.get(id)?.headerLabel !== false" class="fe-list__head-label">{{ label(id) }}</span>
          <span
            v-if="isResizable(id)"
            class="fe-list__resize"
            role="separator"
            aria-orientation="vertical"
            tabindex="0"
            :aria-label="t('cols.resize', { col: label(id) })"
            :title="t('cols.resize', { col: label(id) })"
            @pointerdown="onResizeStart(id, $event)"
            @dblclick.stop="onResizeReset(id)"
            @keydown="onResizeKey(id, $event)"
          ></span>
        </div>
        <div
          class="fe-list__col fe-list__col--menu"
          :class="{ 'is-wide': menuContent !== 28 }"
          :style="menuStyle"
          role="columnheader"
        >
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
            v-for="(row, ri) in seg.items"
            :key="keyOf(row, ri)"
            :class="rowClasses(row, ri)"
            role="row"
            :style="tableStyle"
            :aria-selected="selectable ? (selected(row) ? 'true' : 'false') : undefined"
            v-bind="rowAttrs ? rowAttrs(row) : {}"
            @click="onRowClick(row, $event)"
            @dblclick="emit('row-dblclick', row, $event)"
            @contextmenu="emit('row-contextmenu', row, $event)"
            @dragstart="emit('row-dragstart', row, $event)"
            @dragover="emit('row-dragover', row, $event)"
            @dragleave="emit('row-dragleave', row, $event)"
            @drop="emit('row-drop', row, $event)"
            @touchstart.passive="emit('row-touchstart', row, $event)"
            @touchend="emit('row-touchend', $event)"
            @touchmove.passive="emit('row-touchmove', $event)"
          >
            <!-- issue #26 — the tick is the one click that selects; the whole
                 cell is its target. `data-fe-control` keeps a finger on it
                 from counting as a tap on the row (useRowTouch). -->
            <div
              v-if="selectable"
              class="fe-list__col fe-list__col--check"
              role="gridcell"
              data-fe-control
              @click.stop="emit('check-click', row, $event)"
              @dblclick.stop
            >
              <ItemCheck :on="selected(row)" :label="cellText(row, store.lead)" />
            </div>
            <div :class="colClass(store.lead)" role="gridcell" :style="colStyle(store.lead)" :title="$slots[`cell-${store.lead}`] ? undefined : cellTitle(row, store.lead)">
              <slot :name="`cell-${store.lead}`" :row="row" :value="cellValue(row, store.lead)">
                <span class="fe-list__cell-text"><bdi>{{ cellText(row, store.lead) }}</bdi></span>
              </slot>
            </div>
            <div
              v-for="id in visibleCols"
              :key="id"
              :class="colClass(id)"
              role="gridcell"
              :style="colStyle(id)"
              :data-col="id"
              :title="$slots[`cell-${id}`] ? undefined : cellTitle(row, id)"
            >
              <slot :name="`cell-${id}`" :row="row" :value="cellValue(row, id)">
                <span class="fe-list__cell-text"><bdi>{{ cellText(row, id) }}</bdi></span>
              </slot>
            </div>
            <div
              class="fe-list__col fe-list__col--menu"
              :class="{ 'is-wide': menuContent !== 28 }"
              :style="menuStyle"
              role="gridcell"
              data-fe-control
              @click.stop
              @dblclick.stop
            >
              <slot name="actions" :row="row">
                <!-- A row with NO verbs at all draws no control (a disabled
                     "Actions" over nothing is a promise the row cannot keep);
                     a row whose verbs are all unavailable right now draws it
                     greyed, with each verb's reason — RowActions' own rule. -->
                <RowActions
                  v-if="rowActions && rowActions(row).length"
                  :locale="lang"
                  :theme="mode"
                  :actions="rowActions(row)"
                  :testid="rowActionsTestId?.(row)"
                  @select="(key: string, action: ContextAction) => onRowAction(key, row, action)"
                />
              </slot>
            </div>
          </div>
        </template>
        <div v-if="loading && rowCount === 0" class="fe-list__empty fe-list__empty--loading" role="status">
          {{ t('loading') }}
        </div>
        <div v-else-if="!loading && rowCount === 0" class="fe-list__empty">
          <slot name="empty">{{ empty ?? t('table.empty') }}</slot>
        </div>
      </div>
    </div>
    <div v-if="showPager || footNote || $slots.foot" class="tbl-foot">
      <span>
        <slot name="foot">{{ footNote || rangeLabel }}</slot>
      </span>
      <div v-if="showPager" class="tbl-pager">
        <button
          type="button"
          class="tbl-page"
          :disabled="currentPage <= 1"
          :aria-label="t('table.prev')"
          :title="t('table.prev')"
          @click="go(currentPage - 1)"
        >‹</button>
        <span class="tbl-pager__at" dir="ltr">{{ currentPage }} / {{ totalPages }}</span>
        <button
          type="button"
          class="tbl-page"
          :disabled="currentPage >= totalPages"
          :aria-label="t('table.next')"
          :title="t('table.next')"
          @click="go(currentPage + 1)"
        >›</button>
      </div>
    </div>
  </div>
  <Teleport to="body">
    <div
      v-if="colMenuOpen"
      class="fe-colmenu__backdrop"
      :dir="dir"
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
          <button
            type="button"
            class="fe-colmenu__nudge"
            :disabled="!c.canEarlier"
            :title="t(nudge.earlier.key, { col: c.label })"
            :aria-label="t(nudge.earlier.key, { col: c.label })"
            @click="stepColumn(c.id, -1)"
          ><span aria-hidden="true">{{ nudge.earlier.arrow }}</span></button>
          <button
            type="button"
            class="fe-colmenu__nudge"
            :disabled="!c.canLater"
            :title="t(nudge.later.key, { col: c.label })"
            :aria-label="t(nudge.later.key, { col: c.label })"
            @click="stepColumn(c.id, 1)"
          ><span aria-hidden="true">{{ nudge.later.arrow }}</span></button>
        </div>
        <div v-if="canResetCols" class="fe-colmenu__sep" role="separator"></div>
        <button v-if="canResetCols" type="button" class="fe-colmenu__row" @click="onResetColumns">
          <span class="fe-colmenu__label">{{ t('cols.reset') }}</span>
        </button>
        <slot name="colmenu-extra" :close="closeColMenu" />
      </div>
    </div>
  </Teleport>
</template>
