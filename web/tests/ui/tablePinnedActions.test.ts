/**
 * Every admin table pins its actions column to the right edge, the way the
 * explorer's list view pins its ⋮ column.
 *
 * The owner asked for it verbatim (2026-09-19): "admin panelinde bütün
 * tablolarımızda işlemler bölgesi sağda sabit kalsın, file explorer içindeki
 * tablolarımız gibi". Until then a table wider than its card scrolled the
 * buttons out of view first — at 700px the Users list needed a sideways
 * scroll to reach Edit/Delete at all.
 *
 * Three things have to stay true, and each has a test below:
 *   1. ui/Table.vue marks the `actions` column pinned by default (header,
 *      filter row and body cells alike), lets a column opt in with
 *      `pinned: 'right'` and lets `actions` opt out with `pinned: null`.
 *   2. Its wrapper is the shared TableScroll, which flips `is-scrolled-x` on
 *      real sideways scroll — the only time the pinned cell draws its edge.
 *   3. The hand-rolled admin tables ride the same two classes, so the shared
 *      table and a `<table>` written by hand cannot drift apart. That half is
 *      a source scan: a view with an actions column and no `tbl-actions` is a
 *      regression, whatever it looks like.
 *
 * The geometry itself (sticky cell inside the viewport at 700px, still
 * clickable) is measured in a real browser: cypress/e2e/41-users-crud.cy.ts.
 */
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import Table, { type Column } from '@/components/ui/Table.vue';
import TableScroll from '@/components/ui/TableScroll.vue';

type Row = { id: number; name: string; actions?: unknown };

const rows: Row[] = [
  { id: 1, name: 'alpha' },
  { id: 2, name: 'beta' },
];

function mountTable(columns: Column<Row>[], extra: Record<string, unknown> = {}) {
  return mount(Table, {
    props: { columns, rows, rowKey: 'id', ...extra },
    slots: {
      'cell-actions': '<button type="button" class="row-action">edit</button>',
      'filter-name': '<input class="name-filter" />',
      'filter-actions': '<span class="actions-filter" />',
      filters: '<span />',
    },
  });
}

describe('ui/Table.vue — pinned actions column', () => {
  it('pins the `actions` column by default: header, filter row and every body cell', () => {
    const w = mountTable([
      { key: 'name', label: 'Name' },
      { key: 'actions', label: 'Actions', cell: 'slot', align: 'right', width: '180px' },
    ]);

    const ths = w.findAll('thead tr:first-child th');
    expect(ths).toHaveLength(2);
    expect(ths[0].classes()).not.toContain('tbl-actions');
    expect(ths[1].classes()).toContain('tbl-actions');
    // The header keeps its alignment and width — pinning changes neither.
    expect(ths[1].classes()).toContain('text-right');
    expect(ths[1].attributes('style')).toContain('180px');

    const filterThs = w.findAll('thead tr:nth-child(2) th');
    expect(filterThs).toHaveLength(2);
    expect(filterThs[1].classes()).toContain('tbl-actions');
    expect(filterThs[0].classes()).not.toContain('tbl-actions');

    const bodyRows = w.findAll('tbody tr');
    expect(bodyRows).toHaveLength(rows.length);
    for (const tr of bodyRows) {
      const tds = tr.findAll('td');
      expect(tds[0].classes()).not.toContain('tbl-actions');
      expect(tds[1].classes()).toContain('tbl-actions');
      expect(tds[1].find('button.row-action').exists()).toBe(true);
      // The pinned cell paints with the row's own ground, so the row has to
      // have one — and an opaque one (a translucent hover lets the columns
      // sliding underneath show through).
      expect(tr.classes()).toContain('bg-white');
      expect(tr.classes()).toContain('dark:bg-zinc-900');
      expect(tr.classes().join(' ')).not.toMatch(/\/\d+\b/);
    }
  });

  it('`pinned: "right"` pins any column; `pinned: null` unpins `actions`', () => {
    const w = mountTable([
      { key: 'name', label: 'Name', pinned: 'right' },
      { key: 'actions', label: 'Actions', cell: 'slot', pinned: null },
    ]);
    const ths = w.findAll('thead tr:first-child th');
    expect(ths[0].classes()).toContain('tbl-actions');
    expect(ths[1].classes()).not.toContain('tbl-actions');
    const tds = w.findAll('tbody tr:first-child td');
    expect(tds[0].classes()).toContain('tbl-actions');
    expect(tds[1].classes()).not.toContain('tbl-actions');
  });

  it('wraps the table in the shared TableScroll and lets it grow past the card', () => {
    const w = mountTable([
      { key: 'name', label: 'Name' },
      { key: 'actions', label: 'Actions', cell: 'slot' },
    ]);
    const scroll = w.findComponent(TableScroll);
    expect(scroll.exists()).toBe(true);
    expect(scroll.classes()).toContain('tbl-scroll');
    expect(scroll.classes()).not.toContain('is-scrolled-x');
    // The table is a direct child of the scroll container: the stylesheet's
    // `.tbl-scroll > table { min-width: max-content }` is what lets it be
    // wider than the card instead of squeezing its columns.
    expect(Array.from(scroll.element.children).some((c) => c.tagName === 'TABLE')).toBe(true);
  });

  it('empty and loading rows span every column and are never pinned', () => {
    const cols: Column<Row>[] = [
      { key: 'name', label: 'Name' },
      { key: 'actions', label: 'Actions', cell: 'slot' },
    ];
    const empty = mount(Table, { props: { columns: cols, rows: [], empty: 'nothing' } });
    const td = empty.find('tbody td');
    expect(td.attributes('colspan')).toBe('2');
    expect(td.classes()).not.toContain('tbl-actions');
    expect(td.text()).toBe('nothing');

    const loading = mount(Table, { props: { columns: cols, rows: [], loading: true } });
    expect(loading.find('tbody td').classes()).not.toContain('tbl-actions');
  });
});

describe('ui/TableScroll.vue — the divider only while scrolled sideways', () => {
  it('flips `is-scrolled-x` with scrollLeft', async () => {
    const w = mount(TableScroll, { slots: { default: '<table><tbody><tr><td>x</td></tr></tbody></table>' } });
    const el = w.element as HTMLElement;
    expect(w.classes()).toContain('tbl-scroll');
    expect(w.classes()).not.toContain('is-scrolled-x');

    // happy-dom has no layout, so scrollLeft is set by hand; the component
    // reads it off the event target, which is all a real scroll does too.
    Object.defineProperty(el, 'scrollLeft', { value: 40, configurable: true, writable: true });
    el.dispatchEvent(new Event('scroll'));
    await nextTick();
    expect(w.classes()).toContain('is-scrolled-x');

    Object.defineProperty(el, 'scrollLeft', { value: 0, configurable: true, writable: true });
    el.dispatchEvent(new Event('scroll'));
    await nextTick();
    expect(w.classes()).not.toContain('is-scrolled-x');
  });
});

describe('hand-rolled admin tables ride the same classes', () => {
  const views = path.resolve(__dirname, '../../src/views');
  const read = (f: string) => readFileSync(path.join(views, f), 'utf8');

  /** Views with an actions column: each pinned <th> AND <td> is marked and the
   *  table sits in TableScroll. The counts are per table (Replica has two). */
  const withActions: Array<[file: string, tables: number]> = [
    ['AdminGrants.vue', 1],
    ['ApiMcp.vue', 1],
    ['Webhooks.vue', 1],
    ['Plugins.vue', 1],
    ['Queue.vue', 1],
    ['Replica.vue', 2],
    ['Trash.vue', 1],
    ['FileVersions.vue', 1],
  ];

  it.each(withActions)('%s: pinned header + cell per table, inside TableScroll', (file, tables) => {
    const src = read(file);
    const ths = src.match(/<th[^>]*\btbl-actions\b/g) ?? [];
    const tds = src.match(/<td[^>]*\btbl-actions\b/g) ?? [];
    expect(ths, 'pinned <th> per table').toHaveLength(tables);
    expect(tds, 'pinned <td> per table').toHaveLength(tables);
    expect((src.match(/<TableScroll\b/g) ?? []).length, '<TableScroll> per table').toBe(tables);
    expect(src).toContain("import TableScroll from '@/components/ui/TableScroll.vue'");
    // The old wrapper is gone: a second scroll container around TableScroll
    // would scroll the pinned cell out of view with everything else.
    expect(src).not.toMatch(/class="[^"]*overflow-x-auto[^"]*"\s*>\s*\n?\s*<table/);
  });

  it('a pinned header that has no visible text carries a screen-reader label', () => {
    for (const [file] of withActions) {
      const src = read(file);
      for (const th of src.match(/<th[^>]*\btbl-actions\b[^>]*>[\s\S]*?<\/th>/g) ?? []) {
        const inner = th.replace(/^<th[^>]*>/, '').replace(/<\/th>$/, '').trim();
        expect(inner, `${file}: ${th}`).not.toBe('');
      }
    }
  });

  it.each(['Notifications.vue', 'Duplicates.vue', 'Usage.vue', 'Audit.vue', 'Sync.vue'])(
    '%s (no actions column) still scrolls sideways rather than squeezing',
    (file) => {
      const src = read(file);
      // Either the shared Table (which brings TableScroll) or the bare class.
      const shared = src.includes("from '@/components/ui/Table.vue'");
      expect(shared || /\btbl-scroll\b/.test(src)).toBe(true);
      expect(src).not.toContain('tbl-actions');
    },
  );
});
