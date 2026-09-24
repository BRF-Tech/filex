/**
 * tableColumns — THE column model of the one table this product has.
 *
 * ⚠⚠ Read this before writing anything tabular. filex has exactly ONE table:
 * the explorer's list view, extracted into `components/DataTable.vue`. Every
 * table in the product — the explorer's own listing, every admin page, the
 * connection panels, My shares, the notifications list and an app's `list`
 * node — renders through that component, and every one of them therefore gets
 * what the explorer's list can do: resize a column, sort by it, hide it, move
 * it, and have all of that remembered.
 *
 * The owner, 2026-09-21, after a round that had built a SECOND table which
 * imitated the explorer (the same pinned edges, the same Actions menu) and left
 * everything else behind: "Admin tabloları hâlâ explore tablolarıyla AYNI
 * KODDA DEĞİL. LÜTFEN AYNI KODA ALALIM. Örnek veriyorum: tablo sütunları
 * düzenlenebilir değil, büyütme küçültme yok, sıralama yok." An imitation gets
 * the parts somebody remembered to copy. The same code gets all of it, and
 * keeps getting whatever is added next.
 *
 * This file is the half of that table that has no DOM: which columns exist,
 * how wide each one is, which ones are hidden, in what order they stand, and
 * how wide the whole row therefore is. It used to live inside `lib/viewPrefs`
 * as the explorer's private column state, typed to the explorer's seven
 * columns; it is written here once, generic over the column ids, and the
 * explorer is one caller of it among many.
 *
 * ⚠ It holds NO state of its own. A store is handed a BACKING — get the
 * state, replace the state — and the backing decides where that lives: the
 * explorer's lives in the per-folder view document (`lib/viewPrefs`), every
 * other table's in the same per-person document under its own table id
 * (`lib/tablePrefs`). That split is the reason the arithmetic can be shared
 * without the storage being shared.
 */
import { computed, shallowRef } from 'vue';

/** One column the table knows how to draw. */
export interface TableColumnSpec<Id extends string = string> {
  id: Id;
  /** Shipped width in px. */
  width: number;
  min: number;
  max: number;
  /** May the person hide it from the column menu? Also what makes it MOVABLE:
   *  a column the person cannot hide is pinned where the table put it (the
   *  explorer's Name first, its star last). */
  hideable: boolean;
  /** May the person drag its edge? */
  resizable: boolean;
}

/**
 * What is remembered about one table's columns. Short keys on purpose — the
 * explorer keeps one of these per folder and the whole document is
 * re-serialised on every save.
 */
export interface ColsState<Id extends string = string> {
  /** Widths the person set, px. A column absent here is at its shipped width. */
  w: Partial<Record<Id, number>>;
  /** Columns the person switched off. */
  hidden: Id[];
  /** The person's order. Absent = the order the columns were declared in. */
  o?: Id[];
}

export function emptyCols<Id extends string = string>(): ColsState<Id> {
  return { w: {}, hidden: [] };
}

/**
 * Read a stored column state against the columns that exist TODAY.
 *
 * ⚠ Clamped and filtered on the way IN, not only on the way out: a width
 * stored by an older build (or hand-edited) outside today's bounds would draw
 * a column nobody can reach the edge of, and a hidden id that no longer names
 * a column would sit in the document forever. Nothing in the UI could explain
 * either, so neither is allowed through.
 */
export function readCols<Id extends string>(
  raw: unknown,
  specs: readonly TableColumnSpec<Id>[],
): ColsState<Id> {
  const fallback = emptyCols<Id>();
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return fallback;
  const specOf = (id: unknown) => specs.find((c) => c.id === id);
  const p = raw as { w?: unknown; hidden?: unknown; o?: unknown };
  const w: Partial<Record<Id, number>> = {};
  if (p.w && typeof p.w === 'object') {
    for (const [k, v] of Object.entries(p.w as Record<string, unknown>)) {
      const spec = specOf(k);
      if (!spec || typeof v !== 'number' || !Number.isFinite(v)) continue;
      w[spec.id] = Math.min(spec.max, Math.max(spec.min, Math.round(v)));
    }
  }
  const hidden = Array.isArray(p.hidden)
    ? [...new Set(p.hidden.filter((x) => specOf(x)?.hideable) as Id[])]
    : [];
  const o = Array.isArray(p.o) ? ([...new Set(p.o.filter((x) => !!specOf(x)))] as Id[]) : undefined;
  return o && o.length ? { w, hidden, o } : { w, hidden };
}

/**
 * THE REORDER ARITHMETIC, on its own and with no state attached.
 *
 * ⚠⚠ `to` is the index the item ENDS UP AT — equivalently, the insertion index
 * in the list with the item already taken out. The two readings coincide for
 * every `to`, and that equivalence is the whole point of writing it down:
 * every reorder bug this function exists to close was a caller mixing the two
 * index spaces. A caller that computes its target against the list WITH the
 * column still in it hands over an index one too large whenever the column
 * moves right — which is exactly how "drag one column to the right" became
 * "two columns to the right". (Owner, 2026-09-20: "bir adım ileri çekince iki
 * adım ilerliyor".)
 *
 * `from === to` is a no-op and returns a copy, so a drop on the column's own
 * place writes nothing.
 */
export function reorderColumns<T>(list: readonly T[], from: number, to: number): T[] {
  const next = [...list];
  if (from < 0 || from >= next.length) return next;
  const target = Math.max(0, Math.min(next.length - 1, to));
  if (target === from) return next;
  const [item] = next.splice(from, 1);
  next.splice(target, 0, item);
  return next;
}

/**
 * The stretch of the order a movable column may occupy — `[lo, hi]`, both
 * inclusive indices into `order`.
 *
 * ⚠ The pinned columns are not just un-draggable, they are also un-passable:
 * the lead is the row's handle and stays first, the explorer's star is a
 * control that lives beside the row menu and stays last. Measured 2026-09-20:
 * dropping Type at the end of the drawn columns put it PAST the star, so the
 * header read `… Size ★ Type ⋮` and the Type label stood over a 24px control
 * column. The band is derived rather than hard-coded so a pinned column at
 * either end of ANY table is honoured without another rule remembering to.
 */
export function columnDropBand<Id extends string>(
  order: readonly Id[],
  specs: readonly TableColumnSpec<Id>[],
): { lo: number; hi: number } {
  const movable = (id: Id) => specs.find((c) => c.id === id)?.hideable === true;
  let lo = 0;
  while (lo < order.length && !movable(order[lo])) lo++;
  let hi = order.length - 1;
  while (hi >= 0 && !movable(order[hi])) hi--;
  return { lo, hi: Math.max(lo, hi) };
}

/** Where a store reads and writes its state. */
export interface ColumnBacking<Id extends string = string> {
  /** The stored state, or anything that does not parse as one (read as empty). */
  get(): unknown;
  /** Replace it. The store always hands over a complete, normalised state. */
  set(next: ColsState<Id>): void;
  /**
   * Is there anything here the person could RESET? Optional — by default,
   * "the state says something". The explorer's folder backing answers "does
   * THIS folder have columns of its own", because a folder that shows the
   * person's default columns has nothing of its own to reset, and a "Reset
   * columns" row that does nothing when clicked reads as broken.
   */
  customised?(): boolean;
}

/** What one table looks like right now. */
export interface TableLayout<Id extends string = string> {
  /** The columns drawn after the lead, in the person's order. Never shortened
   *  for width — a table wider than its pane scrolls. */
  visible: Id[];
  /** Every drawn track's width in px, the lead's included. */
  widths: Record<Id, number>;
  /** The table's own width: every track, every gap, both paddings. */
  total: number;
  /** True while the widths are derived from the pane rather than stored. */
  auto: boolean;
}

export interface LayoutMetrics {
  /** Space between cells (`--fe-gap-sm`). */
  gap?: number;
  /** Row padding, both sides together (`--fe-gap` × 2). */
  padding?: number;
  /** The tick column's track, or 0 for a table with no tick. */
  check?: number;
  /** The trailing actions column's track (the explorer's ⋮ is 28). */
  menu?: number;
  /** What the lead opens at when nobody has sized anything. */
  leadAuto?: number;
}

export interface ColumnStore<Id extends string = string> {
  readonly lead: Id;
  specs(): readonly TableColumnSpec<Id>[];
  spec(id: Id): TableColumnSpec<Id> | undefined;
  /** The full order, lead first, reconciled with the columns that exist. */
  order(): Id[];
  width(id: Id): number;
  setWidth(id: Id, px: number): void;
  hidden(id: Id): boolean;
  setHidden(id: Id, hidden: boolean): void;
  /** Put `id` at `index` in the order — `index` is where it ENDS UP. */
  move(id: Id, index: number): void;
  /** Has anybody ever sized this table? (the lead's width is the bit) */
  widthsAreAuto(): boolean;
  /** Commit the widths on screen for every column that has none stored. */
  freeze(widths: Partial<Record<Id, number>>): void;
  /** Back to the shipped widths, order and visibility. */
  reset(): void;
  /** Has anything about the columns been changed by hand? */
  customised(): boolean;
  /** The state as it resolves right now — what "Apply to all folders" copies. */
  state(): ColsState<Id>;
  layout(available: number, candidates: readonly Id[], metrics?: LayoutMetrics): TableLayout<Id>;
}

/**
 * A column store over `specs`, reading and writing through `backing`.
 *
 * `lead` is the column pinned first: never hidden, never moved, always
 * resizable. It is the one that says WHICH ROW this is — the explorer's Name,
 * an admin table's email or path — and the table freezes it on the left edge
 * while the others scroll.
 *
 * ⚠ The state is read through a `computed`, so a store created inside a
 * component re-renders it when the backing changes (the explorer's backing
 * changes every time the folder does) and parses the stored document once per
 * change rather than once per cell.
 */
export function createColumnStore<Id extends string>(
  specsOf: () => readonly TableColumnSpec<Id>[],
  backing: ColumnBacking<Id>,
  opts: { lead?: Id } = {},
): ColumnStore<Id> {
  const lead = (opts.lead ?? specsOf()[0]?.id) as Id;
  const state = computed<ColsState<Id>>(() => readCols(backing.get(), specsOf()));
  const specOf = (id: Id) => specsOf().find((c) => c.id === id);

  function write(next: ColsState<Id>): void {
    backing.set(next);
  }

  /**
   * THE COLUMN ORDER — the person's, reconciled with the columns that exist.
   *
   * ⚠⚠ Reconciled, not trusted. A stored order was written by an older build,
   * so it can be missing a column added since and can name one removed since.
   * Returning it as-is would make a new column INVISIBLE to everyone who had
   * ever touched the order — a feature that ships and then does not appear for
   * exactly the users engaged enough to have customised something.
   *
   * ⚠ The lead first, whatever the document says: it is pinned in the table,
   * so an order that claimed otherwise would be a sequence nothing on screen
   * obeys.
   *
   * ⚠ A column the stored list never heard of goes to the END, not to its own
   * shipped index. Measured: inserting at the shipped index walked newcomers in
   * FRONT of the columns the person had deliberately moved — somebody who had
   * put Size first would open a later release to find two new columns ahead of
   * it and their arrangement silently rewritten.
   */
  function order(): Id[] {
    const known = specsOf().map((c) => c.id);
    const stored = state.value.o;
    if (!stored || stored.length === 0) {
      return [lead, ...known.filter((id) => id !== lead)];
    }
    const out = stored.filter((id) => known.includes(id) && id !== lead);
    out.unshift(lead);
    for (const id of known) if (!out.includes(id)) out.push(id);
    return out;
  }

  function width(id: Id): number {
    const spec = specOf(id);
    if (!spec) return 0;
    return state.value.w[id] ?? spec.width;
  }

  function setWidth(id: Id, px: number): void {
    const spec = specOf(id);
    if (!spec || !spec.resizable) return;
    const next = Math.min(spec.max, Math.max(spec.min, Math.round(px)));
    if (state.value.w[id] === next) return;
    write({ ...state.value, w: { ...state.value.w, [id]: next } });
  }

  /**
   * HAS ANYBODY EVER SIZED THIS TABLE — the one bit that separates "I have
   * never touched this" from "I deliberately made the lead narrow".
   *
   * ⚠⚠ Without it the two are indistinguishable and the first resize is undone
   * by the next pane resize: an untouched table is sized to the pane it is in,
   * so a person who drags the lead to 140 and then opens a side panel would get
   * it recomputed back to whatever the narrower pane suggests. The bit is the
   * LEAD's width rather than "the map is empty": documents written before the
   * explorer's Name became a sized column carry widths but have never been
   * asked about Name, and reading those as "configured" would open the table
   * at a width nobody chose. The first drag writes every width at once
   * (`freeze`), so a table somebody has actually sized can never fall back.
   */
  function widthsAreAuto(): boolean {
    return state.value.w[lead] === undefined;
  }

  /**
   * ⚠⚠ Called at the START of a resize gesture, and it has to write EVERY
   * drawn column, not the one being dragged. Writing only the dragged column
   * would leave the lead auto — and the auto lead takes the slack, so
   * narrowing Size would widen Name, which is the exact behaviour the owner
   * called wrong ("küçültme yapınca name kısmı büyüyormuş gibi davranıyor").
   */
  function freeze(widths: Partial<Record<Id, number>>): void {
    const w: Partial<Record<Id, number>> = { ...state.value.w };
    let changed = false;
    for (const [k, v] of Object.entries(widths) as [Id, number | undefined][]) {
      if (typeof v !== 'number' || !Number.isFinite(v)) continue;
      if (w[k] !== undefined) continue;
      const spec = specOf(k);
      if (!spec || !spec.resizable) continue;
      w[k] = Math.min(spec.max, Math.max(spec.min, Math.round(v)));
      changed = true;
    }
    if (changed) write({ ...state.value, w });
  }

  function hidden(id: Id): boolean {
    return state.value.hidden.includes(id);
  }

  function setHidden(id: Id, hide: boolean): void {
    const spec = specOf(id);
    if (!spec || !spec.hideable || id === lead) return;
    if (hidden(id) === hide) return;
    const set = new Set(state.value.hidden);
    if (hide) set.add(id);
    else set.delete(id);
    write({ ...state.value, hidden: [...set] });
  }

  function move(id: Id, index: number): void {
    const spec = specOf(id);
    /* A column the person cannot hide is not theirs to move either: it is
       pinned where the table put it (the lead first, a control column last). */
    if (!spec || !spec.hideable || id === lead) return;
    const cur = order();
    const from = cur.indexOf(id);
    if (from === -1) return;
    const band = columnDropBand(cur, specsOf());
    const to = Math.max(band.lo, Math.min(band.hi, index));
    const next = reorderColumns(cur, from, to);
    if (next.join() === cur.join()) return;
    write({ ...state.value, o: next });
  }

  function reset(): void {
    write(emptyCols<Id>());
  }

  function customised(): boolean {
    if (backing.customised) return backing.customised();
    return (
      Object.keys(state.value.w).length > 0 ||
      state.value.hidden.length > 0 ||
      (state.value.o?.length ?? 0) > 0
    );
  }

  /**
   * THE TABLE'S WIDTHS — one answer, used by the header and by every row.
   *
   *   · nothing is ever dropped for want of room — `visible` is the person's
   *     choice and the caller's candidates, and width has no vote. The owner
   *     rejected the table that shed columns and said "no room": "no room
   *     demesin, kenara devam eden bir scroll getirsin";
   *   · the table's width is the SUM of its columns, so when that exceeds the
   *     pane the table scrolls sideways instead of shrinking;
   *   · with nothing stored the widths are derived from the pane, so a table
   *     nobody has configured opens sensibly at 1440 and at 390 alike: the lead
   *     asks for `leadAuto`, the others give up room toward their minimums to
   *     pay for it, and the lead takes any slack that is left. The instant one
   *     width is stored the pane stops having an opinion.
   *
   * ⚠ `candidates` is what the CALLER is willing to draw at all — the
   * explorer's Location only where rows come from more than one folder, its
   * star only where starring is offered. Passing them keeps those facts with
   * the component that knows them.
   */
  function layout(
    available: number,
    candidates: readonly Id[],
    metrics: LayoutMetrics = {},
  ): TableLayout<Id> {
    const gap = metrics.gap ?? 8;
    const padding = metrics.padding ?? 24;
    const check = metrics.check ?? 0;
    const menu = metrics.menu ?? 28;
    const leadSpec = specOf(lead);

    const visible = order().filter((id) => id !== lead && candidates.includes(id) && !hidden(id));

    const widths = {} as Record<Id, number>;
    for (const id of visible) widths[id] = width(id);
    widths[lead] = width(lead);

    /* Cells: [tick] + lead + the others + the actions column, so one gap
       fewer than that. */
    const cells = (check > 0 ? 1 : 0) + 1 + visible.length + 1;
    const chrome = check + menu + padding + gap * (cells - 1);
    const auto = widthsAreAuto();

    /* A container we cannot measure yet (0 on the first tick, or a test with
       no layout) keeps the shipped widths — the observer corrects it one frame
       later, and a table that guessed would flash. */
    if (auto && available > 0 && leadSpec) {
      const leadAuto = metrics.leadAuto ?? leadSpec.width;
      const room = available - chrome;
      let rest = visible.reduce((s, id) => s + widths[id], 0);
      const target = room - leadAuto;
      if (rest > target) {
        const give = visible.reduce((s, id) => s + (widths[id] - (specOf(id)?.min ?? widths[id])), 0);
        if (give > 0) {
          const f = Math.min(1, (rest - target) / give);
          for (const id of visible) {
            const min = specOf(id)?.min ?? widths[id];
            /* ⚠ `ceil` on the reduction, not `floor`: rounding the other way
               leaves the row a pixel or two over the pane and hands an
               otherwise fitting table a horizontal scrollbar on first sight —
               measured at 960, 712 in a 711px pane. */
            widths[id] = Math.max(min, widths[id] - Math.ceil((widths[id] - min) * f));
          }
          rest = visible.reduce((s, id) => s + widths[id], 0);
        }
      }
      /* Slack goes to the lead, and only to the lead — and where there is
         none to give, the lead gives way too, down to its own minimum.

         ⚠⚠ It used to stop at `leadAuto`, and `leadAuto` is a DESKTOP
         default (240px for a table that names nothing else). In a 265px side
         panel that IS the pane: an app's list drew its Identity column and
         nothing else, and the State and When columns it was sent sat under
         the pinned Actions cell, so a signer table read as names, buttons,
         and a blank gap between them (v0.43.0). Standing at 240 does not
         make anything fit — the table is wider than the pane either way and
         scrolls, which is the owner's rule — it only decides what the person
         is shown FIRST, and one wide column plus nothing is the worse
         answer. Nothing is ever dropped here; the lead simply stops taking
         the whole pane. */
      const leadFloor = Math.min(leadAuto, leadSpec.min);
      widths[lead] = Math.round(Math.max(leadFloor, Math.min(leadSpec.max, room - rest)));
    }

    const total = chrome + widths[lead] + visible.reduce((s, id) => s + widths[id], 0);
    return { visible, widths, total, auto };
  }

  return {
    lead,
    specs: specsOf,
    spec: specOf,
    order,
    width,
    setWidth,
    hidden,
    setHidden,
    move,
    widthsAreAuto,
    freeze,
    reset,
    customised,
    state: () => state.value,
    layout,
  };
}

/**
 * A backing that lives in this component's memory only — for a table on a
 * surface with no account behind it (a public page), and for tests.
 */
export function memoryBacking<Id extends string = string>(
  initial: ColsState<Id> = emptyCols<Id>(),
): ColumnBacking<Id> {
  /* A ref, or the store's `computed` would never see a write and the table
     would not move when a column is dragged. */
  const box = shallowRef<ColsState<Id>>(initial);
  return {
    get: () => box.value,
    set: (next) => {
      box.value = next;
    },
  };
}
