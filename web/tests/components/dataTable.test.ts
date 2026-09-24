// THE table (core DataTable — the explorer's list, extracted) as every other
// table uses it: what it promises beyond drawing rows.
//
//   1. It never sorts ONE PAGE of a longer list and calls that sorted. While
//      the rows are a page of several, the headers close and say why
//      (`table.sort_paged`) — and a scripted click on a closed header changes
//      nothing, because `disabled` is the affordance, not the rule.
//   2. With every row on screen, a header click sorts, and the choice is
//      remembered PER TABLE (`tableId`): the same table opens sorted the same
//      way, a different table does not inherit it.
//   3. A row with no verbs draws no Actions control — a greyed "Actions" over
//      nothing is a promise the row cannot keep.
//
// packages/core has no runner of its own; the gate lives here.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';

import DataTable from '@brftech/filex-core/src/components/DataTable.vue';
import { __resetViewPrefs } from '@brftech/filex-core/src/lib/viewPrefs';
import { en } from '@brftech/filex-core/src/locales/en';

type Row = { id: number; name: string };
const rows: Row[] = [
  { id: 1, name: 'charlie' },
  { id: 2, name: 'alpha' },
  { id: 3, name: 'bravo' },
];
const columns = [{ id: 'name', label: 'Name', sortable: true }];

const mounted: VueWrapper[] = [];
function table(props: Record<string, unknown> = {}) {
  const w = mount(DataTable, {
    props: {
      rows,
      columns,
      rowKey: (r: Row) => r.id,
      locale: 'en',
      rowAttrs: (r: Row) => ({ 'data-testid': `row-${r.id}` }),
      ...props,
    },
    attachTo: document.body,
  });
  mounted.push(w);
  return w;
}
const order = (w: VueWrapper) =>
  w.findAll('[data-testid^="row-"]').map((r) => r.attributes('data-testid'));
const header = (w: VueWrapper) => w.get('.fe-list__head [data-col="name"] button');

beforeEach(() => {
  __resetViewPrefs();
  localStorage.clear();
});
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
  __resetViewPrefs();
});

describe('DataTable — sorting is honest', () => {
  it('closes the headers, and says why, while the rows are one page of several', async () => {
    const w = table({ tableId: 'test.paged', pages: 3, page: 1 });
    expect(header(w).attributes('disabled')).toBeDefined();
    expect(header(w).attributes('title')).toBe(en['table.sort_paged']);
    // ⚠ A scripted click still reaches the listener: it must change nothing.
    (header(w).element as HTMLButtonElement).disabled = false;
    await header(w).trigger('click');
    expect(order(w)).toEqual(['row-1', 'row-2', 'row-3']);
    expect(w.emitted('sort')).toBeUndefined();
  });

  it('`total` above the rows on screen is paged too', () => {
    const w = table({ tableId: 'test.total', total: 40 });
    expect(header(w).attributes('title')).toBe(en['table.sort_paged']);
  });

  it('with every row on screen, sorts — and the table remembers it, per table', async () => {
    const w = table({ tableId: 'test.sorts' });
    await header(w).trigger('click');
    expect(order(w)).toEqual(['row-2', 'row-3', 'row-1']);
    w.unmount();
    mounted.pop();

    // The same table, opened again, is still sorted…
    const again = table({ tableId: 'test.sorts' });
    expect(order(again)).toEqual(['row-2', 'row-3', 'row-1']);
    // …and another table is not.
    const other = table({ tableId: 'test.other' });
    expect(order(other)).toEqual(['row-1', 'row-2', 'row-3']);
  });
});

describe('DataTable — a cell is named by its column before its role', () => {
  it('the lead column’s first `fe-list__col--<id>` is its own id, not "lead"', () => {
    // ⚠ Whatever reads a cell by its first `fe-list__col--<id>` (the e2e
    // column specs do) read "lead" for Name once the role came first — all
    // of 108-column-reorder went red on a table that worked.
    const w = table({
      tableId: 'test.classes',
      columns: [
        { id: 'name', label: 'Name', sortable: true, lead: true, class: 'fe-list__col--name' },
        { id: 'size', label: 'Size', class: 'fe-list__col--size' },
      ],
    });
    const first = (sel: string) => (w.get(sel).classes().join(' ').match(/fe-list__col--(\w+)/) ?? [])[1];
    expect(first('.fe-list__head [data-col="name"]')).toBe('name');
    expect(w.get('.fe-list__head [data-col="name"]').classes()).toContain('fe-list__col--lead');
    expect(first('.fe-list__head [data-col="size"]')).toBe('size');
  });
});

describe('DataTable — one Actions control, only where there is something to do', () => {
  it('a row with no verbs draws no control', () => {
    const w = table({
      tableId: 'test.actions',
      rowActions: (r: Row) => (r.id === 2 ? [] : [{ key: 'open', label: 'Open' }]),
      rowActionsTestId: (r: Row) => `actions-${r.id}`,
    });
    expect(w.find('[data-testid="actions-1"]').exists()).toBe(true);
    expect(w.find('[data-testid="actions-2"]').exists()).toBe(false);
    expect(w.find('[data-testid="actions-3"]').exists()).toBe(true);
  });
});
