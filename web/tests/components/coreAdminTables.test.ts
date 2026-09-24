/**
 * THE TABLES THAT ARE NOT IN `web/src` — drawn by the ONE table too.
 *
 * Five of the admin panel's tables live in `packages/core`:
 *
 *   · the connection panels (S3 keys, SSH keys, API tokens, NFS exports) —
 *     /admin/connections, and the explorer's own connection guides;
 *   · the plugin surfaces' `list` node — /admin/apps/<plugin>/<view>, which is
 *     the Signatures menu.
 *
 * Their history is why this file exists. On 2026-09-20 they were moved onto
 * "THE admin table" — a second table (`ui/Table.vue`, `.tbl`) that imitated
 * the explorer's list — and were measured for its classes. The owner then
 * found that none of them could resize a column or sort (2026-09-21, "Admin
 * tabloları hâlâ explore tablolarıyla AYNI KODDA DEĞİL"), because those
 * capabilities live in the explorer's code and the imitation never had them.
 *
 * So what is pinned here is not a class but the COMPONENT: each of these is
 * `DataTable`, the explorer's own table, with the capabilities that come with
 * it (a sortable header, a resize handle, the column menu), its own table id
 * (where its arrangement is remembered), ONE Actions control per row, and the
 * shared empty state.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';

import S3KeysPanel from '@brftech/filex-core/src/components/S3KeysPanel.vue';
import NFSExportsPanel from '@brftech/filex-core/src/components/NFSExportsPanel.vue';
import SurfaceList from '@brftech/filex-core/src/components/plugin/nodes/SurfaceList.vue';
import DataTable from '@brftech/filex-core/src/components/DataTable.vue';
import { __resetViewPrefs } from '@brftech/filex-core/src/lib/viewPrefs';
import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

const CONFIG = { apiBase: '', locale: 'en', endpoint: '/api/files/manager' } as never;

const KEYS = [
  {
    id: 1,
    label: 'laptop',
    access_key_id: 'AKIAEXAMPLE1',
    bucket: 'media',
    prefix: 'raw',
    last_used_at: '2026-09-01T10:00:00Z',
  },
  {
    id: 2,
    label: 'backup box',
    access_key_id: 'AKIAEXAMPLE2',
    bucket: '',
    prefix: '',
    disabled_at: '2026-09-02T10:00:00Z',
    last_used_at: null,
  },
];

function fetchStub(body: unknown) {
  return vi.fn(async () =>
    Promise.resolve({
      ok: true,
      status: 200,
      headers: { get: () => 'application/json' },
      json: async () => body,
      text: async () => JSON.stringify(body),
    }),
  );
}

/** The lead column's text in each row, in the order drawn. */
function leads(w: VueWrapper): string[] {
  return w.findAll('.fe-list__row').map((r) => r.find('.fe-list__col--lead').text());
}

beforeEach(() => {
  closeRowMenus();
  /* ⚠ A table REMEMBERS its sort (lib/tablePrefs, in the per-person view
     document — in memory here). Without this, one test's header click would
     decide the next test's row order. */
  __resetViewPrefs();
});
afterEach(() => {
  closeRowMenus();
  vi.unstubAllGlobals();
});

describe('connection panels draw THE table (the explorer’s own)', () => {
  async function mountKeys(keys: unknown[] = KEYS) {
    vi.stubGlobal(
      'fetch',
      fetchStub({ keys, endpoint: 'https://s3.example', enabled: true, path_style: false }),
    );
    const w = mount(S3KeysPanel, { props: { config: CONFIG, storages: ['media'] } });
    await flushPromises();
    await flushPromises();
    return w;
  }

  it('is DataTable with its own table id — not a <table> of its own', async () => {
    const w = await mountKeys();
    const table = w.findComponent(DataTable);
    expect(table.exists()).toBe(true);
    expect(table.props('tableId')).toBe('conn.s3keys');
    // ⚠ Both earlier shapes are the regression: the panel's private
    // `.fe-s3keys__table`, and the imitation's `<table class="tbl">`.
    expect(w.find('table').exists()).toBe(false);
    expect(w.find('.fe-s3keys__table').exists()).toBe(false);
  });

  it('has what the explorer’s table has: sorting headers and resize handles', async () => {
    const w = await mountKeys();
    const head = w.find('.fe-list__head');
    expect(head.findAll('button.fe-list__sort').length).toBeGreaterThanOrEqual(3);
    expect(head.findAll('.fe-list__resize').length).toBe(4);
    // …and the column menu is there to hide and move them.
    expect(head.find('.fe-list__colmenu-btn').exists()).toBe(true);
  });

  it('sorts its rows when a header is clicked, and reverses on the second click', async () => {
    const w = await mountKeys();
    expect(leads(w)).toEqual(['laptop', 'backup box']);
    const labelSort = w.find('.fe-list__head [data-col="label"] button.fe-list__sort');
    await labelSort.trigger('click');
    expect(leads(w)).toEqual(['backup box', 'laptop']);
    expect(w.find('.fe-list__head [data-col="label"]').attributes('aria-sort')).toBe('ascending');
    await labelSort.trigger('click');
    expect(leads(w)).toEqual(['laptop', 'backup box']);
    expect(w.find('.fe-list__head [data-col="label"]').attributes('aria-sort')).toBe('descending');
  });

  it('a disabled key is quietened with the shared class', async () => {
    const w = await mountKeys();
    const rows = w.findAll('.fe-list__row');
    expect(rows[0].classes()).not.toContain('is-muted');
    expect(rows[1].classes()).toContain('is-muted');
  });

  it('no keys draws the shared empty state, and the header still says what it would hold', async () => {
    const w = await mountKeys([]);
    expect(w.find('.fe-list__empty').exists()).toBe(true);
    expect(w.findAll('.fe-list__head [role="columnheader"]').length).toBe(5);
  });

  it('a row ends in ONE Actions control, and its menu holds both verbs', async () => {
    const w = await mountKeys();
    expect(w.findAll('.fe-list__row .tbl-rowactions')).toHaveLength(KEYS.length);
    // The control lives in the frozen trailing column.
    for (const r of w.findAll('.fe-list__row')) {
      expect(r.find('.fe-list__col--menu .tbl-rowactions').exists()).toBe(true);
    }

    await openRowMenu(w, 's3-key-actions-1');
    const entries = menuEntries();
    expect(entries.map((e) => e.label)).toEqual(['Disable', 'Revoke']);
    expect(entries[1].danger).toBe(true);
    closeRowMenus();

    await openRowMenu(w, 's3-key-actions-2');
    expect(menuEntries().map((e) => e.label)).toEqual(['Enable', 'Revoke']);
    closeRowMenus();
  });

  it('revoke keeps its two-step confirmation — one pick does not destroy a key', async () => {
    const w = await mountKeys();
    await openRowMenu(w, 's3-key-actions-1');
    await pickMenuItem('s3-key-actions-1-revoke');
    closeRowMenus();
    await flushPromises();
    const calls = (globalThis.fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls;
    expect(calls.some((c) => (c[1] as { method?: string } | undefined)?.method === 'DELETE')).toBe(
      false,
    );
    await openRowMenu(w, 's3-key-actions-1');
    expect(menuEntries().map((e) => e.label)).toEqual(['Disable', 'Sure?']);
    closeRowMenus();
  });

  it('NFS exports are the same table, with a table id of their own', async () => {
    vi.stubGlobal(
      'fetch',
      fetchStub({ exports: [], host: 'nfs.example', port: 2049, enabled: true }),
    );
    const w = mount(NFSExportsPanel, { props: { config: CONFIG, storages: ['media'] } });
    await flushPromises();
    await flushPromises();
    const table = w.findComponent(DataTable);
    expect(table.exists()).toBe(true);
    expect(table.props('tableId')).toBe('conn.nfs');
    expect(w.find('.fe-list__empty').exists()).toBe(true);
  });
});

describe("the plugin surfaces' list node is THE table too", () => {
  const COLUMNS = [
    { key: 'who', label: { en: 'Who' } },
    { key: 'when', label: { en: 'When' } },
  ];
  const ROWS = [
    {
      id: 'r1',
      cells: { who: 'Zeynep', when: '2026-09-01' },
      actions: [
        { id: 'open', label: { en: 'Open' } },
        { id: 'void', label: { en: 'Void' }, danger: true },
      ],
    },
    { id: 'r2', cells: { who: 'Ayşe', when: '2026-09-02' } },
  ];

  function draw(props: Record<string, unknown>) {
    return mount(SurfaceList, {
      props: { locale: 'en', columns: COLUMNS, rows: ROWS, tableId: 'app.sign.envelopes', ...props },
    });
  }

  it('is DataTable, remembered per app and node', () => {
    const w = draw({});
    const table = w.findComponent(DataTable);
    expect(table.exists()).toBe(true);
    expect(table.props('tableId')).toBe('app.sign.envelopes');
    expect(w.find('table').exists()).toBe(false);
  });

  it('sorts by default — the node carries every row, so sorting is honest', async () => {
    const w = draw({});
    expect(leads(w)).toEqual(['Zeynep', 'Ayşe']);
    await w.find('.fe-list__head [data-col="who"] button.fe-list__sort').trigger('click');
    // ⚠ The viewer's collation: "Ayşe" before "Zeynep".
    expect(leads(w)).toEqual(['Ayşe', 'Zeynep']);
  });

  it('`sortable: false` opts a column out, and `row.sort` is what a formatted cell sorts by', async () => {
    const w = draw({
      columns: [
        { key: 'who', label: { en: 'Who' }, sortable: false },
        { key: 'size', label: { en: 'Size' } },
      ],
      rows: [
        { id: 'a', cells: { who: 'a', size: '2 MB' }, sort: { size: 2_000_000 } },
        { id: 'b', cells: { who: 'b', size: '900 KB' }, sort: { size: 900_000 } },
      ],
    });
    expect(w.find('.fe-list__head [data-col="who"] button.fe-list__sort').exists()).toBe(false);
    await w.find('.fe-list__head [data-col="size"] button.fe-list__sort').trigger('click');
    // By the raw bytes — the text alone would put "2 MB" before "900 KB".
    expect(leads(w)).toEqual(['b', 'a']);
  });

  it('a plugin’s buttons become ONE control whose menu keeps every verb', async () => {
    const w = draw({});
    const rows = w.findAll('.fe-list__row');
    const byLead = (name: string) =>
      rows.find((r) => r.find('.fe-list__col--lead').text() === name)!;
    // One control on the row that has verbs, NONE on the row that has none.
    expect(byLead('Zeynep').findAll('.tbl-rowactions')).toHaveLength(1);
    expect(byLead('Ayşe').findAll('.tbl-rowactions')).toHaveLength(0);

    await openRowMenu(w, 'surface-list-actions-r1');
    const entries = menuEntries();
    expect(entries.map((e) => e.label)).toEqual(['Open', 'Void']);
    expect(entries[1].danger, 'a plugin’s `danger` no longer paints the entry apart').toBe(true);

    await pickMenuItem('surface-list-action-r1-void');
    expect(w.emitted('action')).toEqual([[{ action_id: 'void', row_id: 'r1' }]]);
    closeRowMenus();
  });

  it('no rows draws the shared empty state with the plugin’s own words', () => {
    const w = draw({ rows: [], empty: { en: 'No envelopes' } });
    expect(w.find('.fe-list__empty').text()).toBe('No envelopes');
  });

  // v0.43.0 wave 2 (2026-09-22): the Signatures page's "Son tarih" read
  // "2026-09-29" beside the explorer's "22 Eyl 2026". A column that says it
  // holds dates is printed the explorer's way and sorted by the value sent.
  it('`format: "date"` prints a day the explorer’s way, sorts by the value, leaves a dash alone', async () => {
    const w = draw({
      locale: 'tr',
      columns: [
        { key: 'who', label: { en: 'Who' } },
        { key: 'due', label: { en: 'Due' }, format: 'date' },
      ],
      rows: [
        { id: 'a', cells: { who: 'a', due: '2026-10-02' } },
        { id: 'b', cells: { who: 'b', due: '2026-09-29' } },
        { id: 'c', cells: { who: 'c', due: '—' } },
      ],
    });
    /** The row's cells after its lead, as drawn. */
    const cellOf = (who: string) =>
      w
        .findAll('.fe-list__row')
        .find((r) => r.find('.fe-list__col--lead').text() === who)!
        .findAll('.fe-list__cell')
        .map((c) => c.text())
        .filter((s) => s && s !== who);
    expect(cellOf('b')).toEqual(['29 Eyl 2026']);
    expect(cellOf('a')).toEqual(['2 Eki 2026']);
    expect(cellOf('c')).toEqual(['—']);
    await w.find('.fe-list__head [data-col="due"] button.fe-list__sort').trigger('click');
    // By the day, not by the printed words ("2 Eki" would sort before "29 Eyl").
    const order = leads(w);
    expect(order.indexOf('b'), JSON.stringify(order)).toBeLessThan(order.indexOf('a'));
  });

  it('a column with no format shows its cells exactly as sent', () => {
    const w = draw({ locale: 'tr' });
    expect(w.text()).toContain('2026-09-01');
  });
});
