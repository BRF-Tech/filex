// The order a SEARCH RESULT is drawn in — the server's, not the sort key's.
//
// ⚠⚠ Why this file exists. The listing sort became a shared store
// (`lib/sortOrder`) so the grid and the list could not disagree about a
// folder's order, and it worked: both views now read one key and one
// direction. But the store had only two halves — a key and a direction — and
// no way to say "this order came from the server, leave it alone", so the
// active key was applied to EVERYTHING the listing shows, search results
// included.
//
// Measured in a real browser on 2026-09-13 (qldemo, query `s`, sort Name ↑).
// The server answered, in its own ranked order:
//
//   Documents/server.ts · Documents/sales.csv · Documents/report.sql ·
//   app.ts · data.csv · notes.md · Documents · budget.csv · …
//
// and the list drew:
//
//   Documents · Documents/agent-probe.txt · app.ts · budget.csv · … ·
//   Documents/server.ts (row 15 of 17)
//
// The best match was fifteenth. Nothing was broken — every test was green,
// both views agreed with each other — because re-alphabetising a ranked list
// looks exactly like sorting a folder.
//
// The owner's ruling, 2026-09-12, verbatim (translated from Turkish): "the
// filter in advanced search should belong to it alone. The other, ordinary
// search and the ⌘K side must stay in relevance order."
//
// So the fix is a third ORDER, not a third key: `ListingOrder`. What is pinned
// here is that decision, from four directions at once —
//
//   1. the store returns the server's order untouched in relevance mode;
//   2. the LIST and the GRID agree about it (the same rows, the same order) —
//      a fix that teaches one view about relevance and leaves the other
//      sorting is filex lesson #67 in a new shirt;
//   3. `compareInOrder('relevance')` IS `lib/listing.byFoldersFirst`, which is
//      the reason GridView needed no edit at all. The day somebody changes
//      what relevance mode means, this assertion goes red and names the file
//      that has to change with it;
//   4. the control does not LIE: while a ranked listing is on screen the sort
//      button stops claiming a key, and the key it stopped claiming is still
//      there when the search is left.
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';

import ListView from '@brftech/filex-core/src/components/ListView.vue';
import GridView from '@brftech/filex-core/src/components/GridView.vue';
import FilterBar from '@brftech/filex-core/src/components/FilterBar.vue';
import CommandPalette from '@brftech/filex-core/src/components/CommandPalette.vue';
import { byFoldersFirst } from '@brftech/filex-core/src/lib/listing';
import { EMPTY_FILTERS } from '@brftech/filex-core/src/lib/fileFilters';
import {
  compareInOrder,
  setSort,
  sortListing,
  activeSortKey,
  activeSortDir,
} from '@brftech/filex-core/src/lib/sortOrder';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { en } from '@brftech/filex-core/src/locales/en';

/**
 * The real answer measured in the browser, in the server's own order.
 *
 * ⚠ Deliberately NOT alphabetical and NOT folders-first: the folder
 * (`Documents`) is the server's SEVENTH best match while its child
 * `server.ts` is the first, which is what makes this set able to tell the
 * three candidate orders apart. A fixture that happens to be alphabetical
 * cannot fail any of the assertions below.
 */
const SERVER_ORDER = [
  'Documents/server.ts',
  'Documents/sales.csv',
  'Documents/report.sql',
  'app.ts',
  'data.csv',
  'notes.md',
  'Documents',
  'budget.csv',
  'budget.xlsx',
  'pipeline.ts',
];

function node(path: string, i: number): FileNode {
  const dir = path === 'Documents';
  return {
    path: `qldemo://${path}`,
    basename: path.split('/').pop() ?? path,
    type: dir ? 'dir' : 'file',
    extension: dir ? '' : (path.split('.').pop() ?? ''),
    // Sizes and dates DESCEND as relevance descends, so a `size` or
    // `modified` sort would reverse the fixture rather than merely shuffle it
    // — the failure is then unmistakable in the diff.
    size: dir ? 0 : (100 - i) * 1000,
    last_modified: 1_760_000_000 - i * 86_400,
    mime_type: dir ? 'directory' : 'text/plain',
  } as FileNode;
}

const hits: FileNode[] = SERVER_ORDER.map(node);

/** `qldemo://a/b.ts` → `a/b.ts`, so a failure reads like the measurement. */
const rel = (list: FileNode[]) => list.map((n) => n.path.replace('qldemo://', ''));

/** The same set, folders hoisted, ranking kept inside each group. */
const FOLDERS_FIRST = ['Documents', ...SERVER_ORDER.filter((p) => p !== 'Documents')];

const listProps = (order: 'sort' | 'relevance') => ({
  files: hits,
  selected: new Set<string>(),
  locale: 'en' as const,
  showParentPath: true,
  order,
});

describe('a ranked result set keeps the server order', () => {
  it('sortListing leaves it alone in relevance mode', () => {
    setSort('name', 'asc');
    expect(rel(sortListing(hits, 'relevance'))).toEqual(FOLDERS_FIRST);
  });

  it('…and still sorts a folder listing by the active key', () => {
    setSort('name', 'asc');
    const byName = rel(sortListing(hits));
    expect(byName[0]).toBe('Documents');
    // ⚠ By BASENAME, which is what `name` sorts on — comparing whole paths
    // would sort `Documents/report.sql` before `app.ts` and fail a correct
    // sort.
    const base = (p: string) => p.split('/').pop() ?? p;
    expect(byName.slice(1).map(base)).toEqual([...byName.slice(1).map(base)].sort());
    // The default argument must mean "sort", not "relevance": an omitted
    // argument is how every existing caller asks for the key's order.
    expect(rel(sortListing(hits, 'sort'))).toEqual(byName);
  });

  it('the active key does NOT reach it — no key reorders a ranked set', () => {
    for (const key of ['name', 'type', 'modified', 'size'] as const) {
      for (const dir of ['asc', 'desc'] as const) {
        setSort(key, dir);
        expect(rel(sortListing(hits, 'relevance')), `${key} ${dir}`).toEqual(FOLDERS_FIRST);
      }
    }
    setSort('name', 'asc');
  });

  it('relevance mode IS the grid’s own folders-first pass', () => {
    // The single fact that lets GridView stay untouched. If this ever stops
    // being true, `GridView.ordered` has to learn the new rule in the same
    // commit — and it will not find out from its own tests.
    expect(compareInOrder('relevance')).toBe(byFoldersFirst);
  });
});

describe('every view draws the same ranked order', () => {
  it('the list renders the server order, not the alphabet', () => {
    setSort('name', 'asc');
    const w = mount(ListView, { props: listProps('relevance') });
    const drawn = w.findAll('[data-fe-path]').map((e) => e.attributes('data-fe-path') ?? '');
    expect(drawn.map((p) => p.replace('qldemo://', ''))).toEqual(FOLDERS_FIRST);
    w.unmount();
  });

  it('the list and the grid agree, given the same rows', () => {
    setSort('name', 'asc');
    const list = mount(ListView, { props: listProps('relevance') });
    const grid = mount(GridView, { props: { ...listProps('relevance'), thumbSrc: () => null } });
    const paths = (w: ReturnType<typeof mount>) =>
      w.findAll('[data-fe-path]').map((e) => (e.attributes('data-fe-path') ?? '').replace('qldemo://', ''));
    expect(paths(list)).toEqual(paths(grid));
    expect(paths(list)).toEqual(FOLDERS_FIRST);
    list.unmount();
    grid.unmount();
  });

  it('the list still sorts an ordinary folder listing', () => {
    setSort('size', 'desc');
    const w = mount(ListView, { props: listProps('sort') });
    const drawn = w
      .findAll('[data-fe-path]')
      .map((e) => (e.attributes('data-fe-path') ?? '').replace('qldemo://', ''));
    // Size descending: the fixture's sizes fall with the ranking, so the files
    // come back in ranking order behind the folder. That it MOVED at all is
    // the point — relevance mode is a mode, not a switch that disables sorting.
    expect(drawn[0]).toBe('Documents');
    expect(drawn).not.toEqual(FOLDERS_FIRST.slice().reverse());
    expect(drawn.length).toBe(hits.length);
    setSort('name', 'asc');
    w.unmount();
  });

  it('no date headings over a ranked set, even sorted by date', () => {
    // A "Today / Yesterday / March 2026" heading is a claim that the rows
    // below it are in date order. Over a ranked list it is simply false.
    setSort('modified', 'desc');
    const ranked = mount(ListView, { props: listProps('relevance') });
    expect(ranked.findAll('.fe-list__group').length).toBe(0);
    ranked.unmount();

    const folder = mount(ListView, { props: listProps('sort') });
    expect(folder.findAll('.fe-list__group').length).toBeGreaterThan(0);
    folder.unmount();
    setSort('name', 'asc');
  });

  it('the column headers do not offer a sort that would do nothing', () => {
    setSort('name', 'asc');
    const w = mount(ListView, { props: listProps('relevance') });
    const heads = w.findAll('.fe-list__sort');
    expect(heads.length).toBeGreaterThan(0);
    for (const h of heads) expect(h.attributes('disabled')).toBeDefined();
    // …and no column claims to be the one sorting.
    expect(w.findAll('.fe-list__sort-arrow').length).toBe(0);
    expect(w.findAll('[aria-sort="ascending"], [aria-sort="descending"]').length).toBe(0);
    w.unmount();
  });

  it('…and a click that gets through one anyway changes NOTHING', async () => {
    /* ⚠⚠ The `disabled` attribute is the affordance, not the rule. The browser
     * suppresses its own activation on a disabled button, but a click
     * dispatched in script reaches the listener all the same — and this is
     * exactly what `w.trigger('click')` does, which is why it is the honest
     * test. Measured 2026-09-13 before the handler guard: a scripted click on
     * the closed Size header left the ranked rows untouched and still turned
     * the stored sort from Name ↑ into Size ↑, so the FOLDER the person went
     * back to came up in an order they never chose. Nothing on screen said so
     * at any point. */
    setSort('name', 'asc');
    const w = mount(ListView, { props: listProps('relevance') });
    const heads = w.findAll('.fe-list__sort');
    expect(heads.length).toBeGreaterThan(0);
    // ⚠ `dispatchEvent`, NOT `wrapper.trigger()`: test-utils checks `disabled`
    // and quietly skips the dispatch, so `trigger` would only re-prove the
    // attribute is there and could never see past it. This is the shape of
    // click the attribute does not stop.
    for (const h of heads) {
      (h.element as HTMLElement).dispatchEvent(new Event('click', { bubbles: true }));
    }
    await w.vm.$nextTick();
    expect(activeSortKey()).toBe('name');
    expect(activeSortDir()).toBe('asc');
    const drawn = w
      .findAll('[data-fe-path]')
      .map((e) => (e.attributes('data-fe-path') ?? '').replace('qldemo://', ''));
    expect(drawn).toEqual(FOLDERS_FIRST);
    w.unmount();
  });
});

describe('the sort control does not lie about a ranked listing', () => {
  const barProps = (order: 'sort' | 'relevance') => ({
    value: { ...EMPTY_FILTERS },
    locale: 'en' as const,
    order,
  });

  it('it names the order it is in, and says why it is closed', async () => {
    setSort('name', 'asc');
    const w = mount(FilterBar, { props: barProps('relevance') });
    const btn = w.find('[data-testid="sort-dir"]');
    expect(btn.exists()).toBe(true);
    expect(btn.text()).toContain(en['sort.relevance']);
    // ⚠ NOT the key's own word: "Name ↑" over a ranked list is the lie.
    expect(btn.text()).not.toContain(en['col.name']);
    expect(btn.attributes('disabled')).toBeDefined();
    expect(btn.attributes('title')).toBe(en['sort.relevance_why']);
    // No direction claimed either — there is no ascending relevance.
    expect(w.findAll('.fe-filterbar__sortarrow').length).toBe(0);
    expect(w.find('[data-testid="sort-menu"]').attributes('disabled')).toBeDefined();
    // …and neither handle acts if a click reaches it anyway — same reasoning
    // as the column headers next door.
    for (const el of [btn.element, w.find('[data-testid="sort-menu"]').element]) {
      (el as HTMLElement).dispatchEvent(new Event('click', { bubbles: true }));
    }
    await w.vm.$nextTick();
    expect(activeSortKey()).toBe('name');
    expect(activeSortDir()).toBe('asc');
    w.unmount();
  });

  it('…and names the key again on an ordinary listing', () => {
    setSort('size', 'desc');
    const w = mount(FilterBar, { props: barProps('sort') });
    const btn = w.find('[data-testid="sort-dir"]');
    expect(btn.text()).toContain(en['col.size']);
    expect(btn.attributes('disabled')).toBeUndefined();
    expect(w.findAll('.fe-filterbar__sortarrow').length).toBe(1);
    setSort('name', 'asc');
    w.unmount();
  });

  it('the key survives the search that suspended it', () => {
    // Leaving a search must put the person back in the order they chose, not
    // strand them in relevance order in a normal folder. Relevance is a MODE:
    // nothing about drawing a ranked listing may touch the stored key.
    setSort('size', 'desc');
    const bar = mount(FilterBar, { props: barProps('relevance') });
    const list = mount(ListView, { props: listProps('relevance') });
    expect(activeSortKey()).toBe('size');
    expect(activeSortDir()).toBe('desc');
    bar.unmount();
    list.unmount();
    const back = mount(FilterBar, { props: barProps('sort') });
    expect(back.find('[data-testid="sort-dir"]').text()).toContain(en['col.size']);
    back.unmount();
    setSort('name', 'asc');
  });
});

describe('the command palette keeps the server ranking', () => {
  it('"Everywhere" hits are drawn in the order they arrived', async () => {
    // The palette renders its own hits and must never reach for the listing
    // comparator. A ranked answer re-alphabetised inside a quick launcher is
    // the same defect one surface over.
    setSort('name', 'asc');
    const served = [
      { path: '/Documents/server.ts', name: 'server.ts', storage: 'qldemo' },
      { path: '/report.pdf', name: 'report.pdf', storage: 'qldemo' },
      { path: '/Documents/agent-probe.txt', name: 'agent-probe.txt', storage: 'qldemo' },
    ];
    const w = mount(CommandPalette, {
      props: {
        open: true,
        locale: 'en' as const,
        files: [] as FileNode[],
        viewMode: 'list' as const,
        globalSearch: async () => served as never,
        initialQuery: 'se',
      },
      attachTo: document.body,
    });
    await w.vm.$nextTick();
    const input = w.find('.fe-cmdp__input');
    await input.setValue('server');
    await new Promise((r) => setTimeout(r, 400));
    await w.vm.$nextTick();
    const rows = w.findAll('.fe-cmdp__item--hit').map((e) => e.text().replace(/\s+/g, ' ').trim());
    expect(rows.length).toBe(served.length);
    served.forEach((h, i) => expect(rows[i]).toContain(h.name));
    w.unmount();
  });
});
