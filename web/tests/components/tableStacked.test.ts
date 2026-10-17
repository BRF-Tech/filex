// THE table where it cannot be a table: stacked rows (DataTable `is-stacked`).
//
// The owner, 2026-10-11, on the e-Signature details panel: the signer table's
// State and When were cut to "Invited ..." and "until S..." in the 300px
// panel. His decision: the ONE table lays its cells out under the lead in a
// narrow place, every narrow table at once, and COMPACT. Below
// `TableLayout.stackBelow` (the lead and the first three columns side by side
// at their minimums, lib/tableColumns) each row is its tick, its lead and its
// actions on one line, and every other cell after them on one line that wraps
// when it must, each with its column's name small in front of it and " · "
// between them: "Type Plain text · Owner You · Size 10 B".
//
// What this file holds the component to:
//   1. it is the TABLE's own width that decides, and it decides both ways —
//      a pane that holds the table keeps the table, with its widths and its
//      resize handles;
//   2. stacked, the rows are the SAME rows: the same cells, in the same order,
//      with the same roles (so every cell still answers to its column header)
//      and the same text — the label and the " · " are drawn by CSS (from
//      `data-label`, and after every fact but the last, `is-stack-last`), not
//      written into the cell;
//   3. stacked, everything a row did it still does: the tick selects, a click
//      and a right-click reach the caller, the Actions menu is there, the
//      header still sorts;
//   4. the threshold is the columns' own minimums, not a pixel width: a
//      forty-column spreadsheet in a wide viewer is still a table.
//
// What is PAINTED (the label before the value, the separators, the facts
// flowing under the lead, nothing cut, the panel's State and When readable)
// is measured in a browser:
// e2e/tests/117-narrow-table-columns.spec.ts, e2e/tests/99-app-plugin-sign-round.spec.ts
// and e2e/shots/signing.mjs.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import { createCommentVNode, h } from 'vue';

import DataTable from '@brftech/filex-core/src/components/DataTable.vue';
import {
  createColumnStore,
  memoryBacking,
  STACK_SPAN,
  type LayoutMetrics,
  type TableColumnSpec,
} from '@brftech/filex-core/src/lib/tableColumns';
import { __resetViewPrefs } from '@brftech/filex-core/src/lib/viewPrefs';
import { en } from '@brftech/filex-core/src/locales/en';

type Row = { id: string; who: string; state: string; when: string };
const rows: Row[] = [
  { id: 'a', who: 'Alex Morgan', state: 'Invited to sign', when: '-' },
  { id: 'b', who: 'Sam Carter', state: 'Waiting', when: 'until Sep 22, 2026' },
];
/** An app's signer list, as SurfaceList hands it over: a lead and two facts. */
const columns = [
  { id: 'who', label: 'Identity', sortable: true },
  { id: 'state', label: 'State', sortable: true },
  { id: 'when', label: 'When', sortable: true },
];

/* The width the table is told it has, the way the browser tells it: through
   its ResizeObserver, applied on the next frame (DataTable). */
let report: ((width: number) => void) | null = null;
const realRO = window.ResizeObserver;

const mounted: VueWrapper[] = [];
function table(props: Record<string, unknown> = {}) {
  const w = mount(DataTable, {
    props: {
      rows,
      columns,
      rowKey: 'id',
      locale: 'en',
      tableId: 'test.stacked',
      rowAttrs: (r: Row) => ({ 'data-testid': `row-${r.id}` }),
      ...props,
    },
    attachTo: document.body,
  });
  mounted.push(w);
  return w;
}

async function paneOf(w: VueWrapper, width: number) {
  await w.vm.$nextTick();
  report!(width);
  await new Promise((r) => requestAnimationFrame(() => r(null)));
  await w.vm.$nextTick();
}

const list = (w: VueWrapper) => w.get('.fe-list');
const isStacked = (w: VueWrapper) => list(w).classes().includes('is-stacked');

beforeEach(() => {
  __resetViewPrefs();
  localStorage.clear();
  window.ResizeObserver = class {
    constructor(cb: ResizeObserverCallback) {
      report = (width) => cb([{ contentRect: { width } } as ResizeObserverEntry], this as unknown as ResizeObserver);
    }
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
});
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
  window.ResizeObserver = realRO;
  report = null;
  __resetViewPrefs();
});

describe('DataTable — the pane decides, both ways', () => {
  it('keeps the table in a pane that holds it, and stacks in the details panel', async () => {
    const w = table();
    // Unmeasured: the first paint is the table, as it always was.
    expect(isStacked(w)).toBe(false);

    await paneOf(w, 900);
    expect(isStacked(w), 'a wide pane stacked the table').toBe(false);
    expect((w.get('.fe-list__head').element as HTMLElement).style.width).not.toBe('');
    expect(w.findAll('.fe-list__head .fe-list__resize').length).toBe(3);

    // The e-Signature details panel.
    await paneOf(w, 280);
    expect(isStacked(w), 'the 280px panel kept a table of 56px columns').toBe(true);
    // Stacked rows are the pane's width: no table width, no column widths,
    // no frozen edges, no handles.
    expect((w.get('.fe-list__head').element as HTMLElement).style.width).toBe('');
    for (const r of w.findAll('.fe-list__row')) expect((r.element as HTMLElement).style.width).toBe('');
    for (const c of w.findAll('.fe-list__row [role="gridcell"]')) {
      expect((c.element as HTMLElement).style.flex, c.attributes('data-col')).toBe('');
    }
    expect(list(w).classes()).not.toContain('is-pin-lead');
    expect(list(w).classes()).not.toContain('is-pin-menu');
    expect(w.findAll('.fe-list__head .fe-list__resize').length).toBe(0);

    // …and back, when the panel is closed and the pane is wide again.
    await paneOf(w, 900);
    expect(isStacked(w)).toBe(false);
    expect(w.findAll('.fe-list__head .fe-list__resize').length).toBe(3);
  });

  it('a column the person widened does not keep a narrow pane from stacking', async () => {
    const w = table();
    await paneOf(w, 900);
    const handle = w.get('.fe-list__head [data-col="state"] [role="separator"]');
    for (let i = 0; i < 10; i++) await handle.trigger('keydown', { key: 'ArrowRight' });
    await paneOf(w, 280);
    expect(isStacked(w)).toBe(true);
  });
});

describe('DataTable — stacked rows are the same rows', () => {
  it('the same cells in the same order and roles, each fact labelled by its column', async () => {
    const w = table();
    await paneOf(w, 280);
    expect(list(w).attributes('role')).toBe('grid');
    const headers = w.findAll('.fe-list__head [role="columnheader"]');
    for (const r of w.findAll('.fe-list__row')) {
      expect(r.attributes('role')).toBe('row');
      const cells = r.findAll('[role="gridcell"]');
      // One cell per column header, in the same order: a screen reader still
      // names each cell by the header above it.
      expect(cells.length).toBe(headers.length);
    }
    const first = w.get('[data-testid="row-a"]');
    const lead = first.get('.fe-list__col--lead');
    expect(lead.attributes('data-label'), 'the lead names the row; it needs no label').toBeUndefined();
    expect(lead.classes()).not.toContain('is-stack-fact');
    for (const [id, label, value] of [
      ['state', 'State', 'Invited to sign'],
      ['when', 'When', '-'],
    ] as const) {
      const cell = first.get(`[data-col="${id}"]`);
      expect(cell.classes(), id).toContain('is-stack-fact');
      expect(cell.attributes('data-label'), id).toBe(label);
      expect(cell.find('.fe-list__stack-value').exists(), id).toBe(true);
      // ⚠ The label is the stylesheet's (`::before`), so the cell's text is
      // exactly what it was in the table.
      expect(cell.text(), id).toBe(value);
    }
  });

  it('draws the values whole, where the table showed them in a 56px track', async () => {
    const w = table();
    await paneOf(w, 280);
    expect(w.get('[data-testid="row-b"] [data-col="when"]').text()).toBe('until Sep 22, 2026');
    expect(w.get('[data-testid="row-b"] [data-col="when"]').attributes('title')).toBe('until Sep 22, 2026');
  });

  it('a column with no header label stays on the first line as a control', async () => {
    const w = table({
      columns: [...columns, { id: 'star', label: 'Star', headerLabel: false }],
    });
    await paneOf(w, 280);
    const star = w.get('[data-testid="row-a"] [data-col="star"]');
    expect(star.classes()).toContain('is-stack-tail');
    expect(star.classes()).not.toContain('is-stack-fact');
    expect(star.attributes('data-label')).toBeUndefined();
    // …and is not the last FACT either, so When keeps no " · " after it.
    expect(w.get('[data-testid="row-a"] [data-col="when"]').classes()).toContain('is-stack-last');
  });

  it('only the last fact goes without a " · " after it, and the dot is never text', async () => {
    const w = table();
    await paneOf(w, 280);
    for (const r of w.findAll('.fe-list__row')) {
      expect(r.get('[data-col="state"]').classes()).not.toContain('is-stack-last');
      expect(r.get('[data-col="when"]').classes()).toContain('is-stack-last');
      // The separator is CSS (`::after`, empty alternative text): nothing in
      // the row's text a screen reader or a test would read.
      expect(r.text()).not.toContain('\u00b7');
    }
    // A hidden last column hands "last" to the one before it.
    const store = (w.vm as unknown as { store: { setHidden(id: string, h: boolean): void } }).store;
    store.setHidden('when', true);
    // ⚠ One column fewer is a narrower table (244px at the minimums), so the
    // 280px pane holds it as a table again; a narrower one stacks it.
    await paneOf(w, 200);
    expect(isStacked(w)).toBe(true);
    expect(w.get('[data-testid="row-a"] [data-col="state"]').classes()).toContain('is-stack-last');
  });

  it('wide again, a cell is the table cell it was: no label, no extra box', async () => {
    const w = table();
    await paneOf(w, 280);
    await paneOf(w, 900);
    const cell = w.get('[data-testid="row-a"] [data-col="state"]');
    expect(cell.attributes('data-label')).toBeUndefined();
    expect(cell.classes()).not.toContain('is-stack-fact');
    expect(w.findAll('.is-stack-last').length).toBe(0);
    expect(cell.find('.fe-list__stack-value').exists()).toBe(false);
    expect(cell.get('.fe-list__cell-text').text()).toBe('Invited to sign');
  });
});

describe('DataTable — a stacked row does everything the row did', () => {
  it('the tick selects, a click and a right-click reach the caller, Actions is there', async () => {
    const w = table({
      selectable: true,
      isSelected: (r: Row) => r.id === 'b',
      rowActions: () => [{ key: 'remind', label: 'Remind' }],
      rowActionsTestId: (r: Row) => `actions-${r.id}`,
      onRowClick: () => {},
    });
    await paneOf(w, 280);
    expect(isStacked(w)).toBe(true);

    const a = w.get('[data-testid="row-a"]');
    expect(a.attributes('aria-selected')).toBe('false');
    expect(w.get('[data-testid="row-b"]').attributes('aria-selected')).toBe('true');

    await a.get('.fe-list__col--check').trigger('click');
    expect(w.emitted('check-click')?.[0]?.[0]).toMatchObject({ id: 'a' });
    // The tick's click is the tick's: it does not also open the row.
    expect(w.emitted('row-click')).toBeUndefined();

    await a.get('[data-col="state"]').trigger('click');
    expect(w.emitted('row-click')?.[0]?.[0]).toMatchObject({ id: 'a' });
    await a.get('[data-col="when"]').trigger('contextmenu');
    expect(w.emitted('row-contextmenu')?.[0]?.[0]).toMatchObject({ id: 'a' });

    expect(w.find('[data-testid="actions-a"]').exists(), 'the Actions control is gone when stacked').toBe(true);
  });

  it('the header still sorts, and the column menu is still offered', async () => {
    const w = table();
    await paneOf(w, 280);
    const order = () => w.findAll('.fe-list__row').map((r) => r.attributes('data-testid'));
    expect(order()).toEqual(['row-a', 'row-b']);
    await w.get('.fe-list__head [data-col="state"] button').trigger('click');
    // "Invited to sign" < "Waiting".
    expect(order()).toEqual(['row-a', 'row-b']);
    await w.get('.fe-list__head [data-col="state"] button').trigger('click');
    expect(order()).toEqual(['row-b', 'row-a']);
    expect(w.find('.fe-list__head .fe-list__colmenu-btn').exists()).toBe(true);
    // Dragging a header to move a column is the table's, not the stack's.
    expect(w.findAll('.fe-list__head .is-movable').length).toBe(0);
  });
});

describe('DataTable — a control column draws nothing where it has nothing to control (task #249)', () => {
  // The explorer's star column, as ListView draws it: a header with no label,
  // and a cell slot whose root is `v-if` - a star on a file, NOTHING on a
  // folder. Vue renders a slot's fallback whenever the slot's content is only
  // comments, and the fallback was the facts' "no value" dash: every folder
  // row carried a hidden "-" that showed beside the ⋮ the moment the row was
  // hovered or focused (the demo at 1280px; the vault's 390px picture).
  const withStar = [...columns, { id: 'star', label: 'Star', headerLabel: false }];
  const starSlot = (p: { row: Row }) =>
    p.row.id === 'a' ? [h('span', { class: 'test-star' }, '★')] : [createCommentVNode('v-if')];

  function starTable(slots: Record<string, unknown> = { 'cell-star': starSlot }) {
    const w = mount(DataTable, {
      props: {
        rows,
        columns: withStar,
        rowKey: 'id',
        locale: 'en',
        tableId: 'test.stacked.star',
        rowAttrs: (r: Row) => ({ 'data-testid': `row-${r.id}` }),
      },
      slots: slots as never,
      attachTo: document.body,
    });
    mounted.push(w);
    return w;
  }

  for (const [where, width] of [['wide', 900], ['stacked', 280]] as const) {
    it(`${where}: the slot's own nothing stays nothing - no "-"`, async () => {
      const w = starTable();
      await paneOf(w, width);
      expect(isStacked(w)).toBe(width === 280);
      expect(w.get('[data-testid="row-a"] [data-col="star"] .test-star').exists()).toBe(true);
      const empty = w.get('[data-testid="row-b"] [data-col="star"]');
      expect(empty.text(), 'a control with nothing to control drew a dash').toBe('');
      expect(empty.find('.fe-list__cell-text').exists()).toBe(false);
    });
  }

  it('a control column with no slot at all draws nothing either', async () => {
    const w = starTable({});
    await paneOf(w, 900);
    for (const id of ['a', 'b']) expect(w.get(`[data-testid="row-${id}"] [data-col="star"]`).text()).toBe('');
  });

  it("a FACT with no value still says so: the dash is the facts' and stays", async () => {
    const w = mount(DataTable, {
      props: {
        rows: [{ id: 'c', who: 'Alice', state: '', when: '' }],
        columns,
        rowKey: 'id',
        locale: 'en',
        tableId: 'test.stacked.empty',
        rowAttrs: (r: Row) => ({ 'data-testid': `row-${r.id}` }),
      },
      attachTo: document.body,
    });
    mounted.push(w);
    for (const width of [900, 280]) {
      await paneOf(w, width);
      expect(w.get('[data-testid="row-c"] [data-col="state"]').text()).toBe('-');
    }
  });
});

describe('DataTable — stacked, the header says it is the sort (task #264)', () => {
  // At 340px "Name ↑ Type Owner Modified Size" ran on one line over rows whose
  // facts read "Type Folder · Owner System": the same words, the same grey -
  // "neither a header nor a row". Stacked, the band leads with "Sort by",
  // drawn by the stylesheet from this attribute (empty alternative text), so
  // the buttons keep their own names.
  const head = (w: VueWrapper) => w.get('.fe-list__head');

  it('stacked with a column that sorts: "Sort by"; a table: nothing', async () => {
    const w = table();
    await paneOf(w, 900);
    expect(head(w).attributes('data-sort-label')).toBeUndefined();
    await paneOf(w, 280);
    expect(head(w).attributes('data-sort-label')).toBe(en['sort.by']);
    // The words are CSS, not text: the header row's text is its buttons'.
    expect(head(w).text()).not.toContain(en['sort.by']);
    await paneOf(w, 900);
    expect(head(w).attributes('data-sort-label')).toBeUndefined();
  });

  it('says nothing when nothing in it can sort', async () => {
    const w = table({ columns: columns.map((c) => ({ ...c, sortable: false })) });
    await paneOf(w, 280);
    expect(isStacked(w)).toBe(true);
    expect(head(w).attributes('data-sort-label')).toBeUndefined();
  });

  it('says nothing while the sort is closed (a ranked search, a paged list)', async () => {
    const w = table({ sortClosed: 'Ranked by relevance' });
    await paneOf(w, 280);
    expect(isStacked(w)).toBe(true);
    expect(head(w).attributes('data-sort-label')).toBeUndefined();
  });

  it('the tick keeps its (empty) header: one column name per cell, still', async () => {
    const w = table({ selectable: true });
    await paneOf(w, 280);
    const headers = w.findAll('.fe-list__head [role="columnheader"]');
    expect(headers[0].classes()).toContain('fe-list__col--check');
    for (const r of w.findAll('.fe-list__row')) expect(r.findAll('[role="gridcell"]').length).toBe(headers.length);
  });
});

describe('the threshold is the columns’ own minimums', () => {
  function layoutAt(specs: TableColumnSpec[], available: number, metrics: LayoutMetrics) {
    const store = createColumnStore(() => specs, memoryBacking(), { lead: specs[0].id });
    return store.layout(
      available,
      specs.map((c) => c.id),
      metrics,
    );
  }

  it('the signer list: its lead and both columns at their minimums, with the ⋮', () => {
    const specs: TableColumnSpec[] = [
      { id: 'who', width: 240, min: 120, max: 900, hideable: false, resizable: true },
      { id: 'state', width: 140, min: 56, max: 900, hideable: true, resizable: true },
      { id: 'when', width: 140, min: 56, max: 900, hideable: true, resizable: true },
    ];
    const metrics = { gap: 8, padding: 24, check: 0, menu: 28 };
    // 28 (⋮) + 24 (paddings) + 3 gaps of 8 + 120 + 56 + 56.
    expect(layoutAt(specs, 280, metrics).stackBelow).toBe(308);
  });

  it('the explorer: Name and the first three columns, not all seven', () => {
    const explorer: TableColumnSpec[] = [
      { id: 'name', width: 260, min: 120, max: 900, hideable: false, resizable: true },
      { id: 'type', width: 88, min: 64, max: 220, hideable: true, resizable: true },
      { id: 'owner', width: 104, min: 72, max: 260, hideable: true, resizable: true },
      { id: 'modified', width: 160, min: 96, max: 300, hideable: true, resizable: true },
      { id: 'size', width: 88, min: 64, max: 200, hideable: true, resizable: true },
      { id: 'star', width: 24, min: 24, max: 24, hideable: false, resizable: false },
    ];
    const metrics = { gap: 8, padding: 24, check: 28, menu: 28, leadAuto: 220 };
    const lay = layoutAt(explorer, 390, metrics);
    expect(STACK_SPAN).toBe(3);
    // tick 28 + ⋮ 28 + 24 + 5 gaps of 8 + Name 120 + Type 64 + Owner 72 + Modified 96.
    expect(lay.stackBelow).toBe(472);
    // A phone stacks; a pane that holds Name and three columns scrolls to the rest.
    expect(390 < lay.stackBelow).toBe(true);
    expect(520 < lay.stackBelow).toBe(false);
  });

  it('a spreadsheet of forty columns in a wide viewer is still a table', () => {
    const csv: TableColumnSpec[] = [
      { id: 'n', width: 64, min: 48, max: 900, hideable: false, resizable: true },
      ...Array.from({ length: 40 }, (_, i) => ({
        id: `c${i}`,
        width: 140,
        min: 56,
        max: 900,
        hideable: true,
        resizable: true,
      })),
    ];
    const lay = layoutAt(csv, 1200, { gap: 8, padding: 24, check: 0, menu: 28 });
    // Far wider than the viewer as a whole…
    expect(lay.total).toBeGreaterThan(1200);
    // …and still a table there: only a phone-sized pane stacks it.
    expect(lay.stackBelow).toBe(28 + 24 + 4 * 8 + 48 + 3 * 56);
    expect(1200 < lay.stackBelow).toBe(false);
  });

  it('does not move with the pane, or with the widths a person stored', () => {
    const specs: TableColumnSpec[] = [
      { id: 'who', width: 240, min: 120, max: 900, hideable: false, resizable: true },
      { id: 'state', width: 140, min: 56, max: 900, hideable: true, resizable: true },
    ];
    const metrics = { gap: 8, padding: 24, check: 0, menu: 28 };
    const store = createColumnStore(() => specs, memoryBacking(), { lead: 'who' });
    const at = (w: number) => store.layout(w, ['who', 'state'], metrics).stackBelow;
    const before = at(1000);
    expect(at(200)).toBe(before);
    store.freeze({ who: 600, state: 400 });
    expect(at(1000)).toBe(before);
  });
});
