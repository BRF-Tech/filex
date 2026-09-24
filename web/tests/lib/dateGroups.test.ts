// One date ladder, drawn by all three views.
//
// ⚠⚠ Why this file exists. The rungs used to be a private `bucketFor()` inside
// `ListView.vue`, and the list was the only view that drew them: the grid and
// the gallery showed the same rows in the same date order with nothing naming
// the days. The owner asked for the headings everywhere ("Ve bunu her
// görünümde de yapması lazım"), and the obvious way to do that — copy the
// ladder into two more components — is the exact shape this repository has
// been bitten by before (filex lesson #67: the grid and the list disagreeing
// about folders-first, because each had its own copy of the rule).
//
// So what is pinned here is not "there are headings". It is:
//
//   1. THE RUNGS THEMSELVES, with their boundaries, including the two that are
//      easy to get wrong — a six-day "This Week" that is not a calendar week,
//      and a future stamp reading as "today" rather than as next year.
//   2. THAT ALL THREE VIEWS DRAW THE SAME WORDS over the same rows. A test
//      that only measured the module would stay green while a view quietly
//      stopped calling it.
//   3. THAT A HEADING IS ONLY DRAWN WHEN IT IS TRUE. "Today / Yesterday /
//      March 2026" over rows sorted by NAME is not a grouping, it is a claim
//      about an order the rows are not in — and the same is true of a search's
//      ranked answer, where the key is still `modified` and the listing simply
//      is not obeying it.
//   4. THAT TWO HEADING SYSTEMS DO NOT STACK in the grid, which is the one
//      view that already had headings of its own.
//
// ⚠ The reference the owner pointed at (filex.blipnova.com, measured
// 2026-09-13) groups only its `/recent` page, by `openedAt ?? modifiedAt`, and
// its ladder is `Today · Yesterday · <that day's date>` — one heading per
// calendar day for ever, off the browser's own clock. Ours keeps the first two
// rungs verbatim and then collapses; `lib/dateGroups`'s header argues why, and
// the month rung below is what that difference looks like.
import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { mount } from '@vue/test-utils';

import ListView from '@brftech/filex-core/src/components/ListView.vue';
import GridView from '@brftech/filex-core/src/components/GridView.vue';
import GalleryView from '@brftech/filex-core/src/components/GalleryView.vue';
import { dateBucketFor, groupByDate, groupingActive } from '@brftech/filex-core/src/lib/dateGroups';
import { setSort } from '@brftech/filex-core/src/lib/sortOrder';
import { en } from '@brftech/filex-core/src/locales/en';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

/** The labels a view lends the module, spelled the way `useLocale` spells
 *  them — English catalogue, so the assertions can be read. */
const LABELS = {
  t: (k: string) => (en as Record<string, string>)[k] ?? k,
  formatMonthYear: (v: number | Date | null | undefined) =>
    new Intl.DateTimeFormat('en-US', { month: 'long', year: 'numeric' }).format(
      v instanceof Date ? v : new Date(Number(v)),
    ),
  zonedYearMonth: (v: number | Date | null | undefined) =>
    new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit' }).format(
      v instanceof Date ? v : new Date(Number(v)),
    ),
};

/* A fixed "now" so the ladder can be measured at all: every rung below is
 * relative to today, and a test that computed its own boundaries from the
 * clock would be re-implementing the thing it is checking. Noon, so that
 * "n days ago" cannot slip across a midnight while the suite runs. */
const NOW = new Date('2026-09-13T12:00:00Z');
const DAY = 86_400_000;
const at = (daysAgo: number) => new Date(NOW.getTime() - daysAgo * DAY);

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  // The views read the shared sort store; date headings hang off `modified`.
  setSort('modified', 'desc');
});
afterEach(() => {
  vi.useRealTimers();
});

describe('the ladder', () => {
  it('names today, yesterday, the six days before that, and this month', () => {
    expect(dateBucketFor(at(0), LABELS).label).toBe('Today');
    expect(dateBucketFor(at(1), LABELS).label).toBe('Yesterday');
    expect(dateBucketFor(at(2), LABELS).label).toBe('This week');
    // ⚠ The far edge of "This Week". Six days before yesterday, not seven, and
    // NOT a calendar week: on a Monday a calendar rule would file Sunday —
    // eighteen hours ago — under a month heading.
    expect(dateBucketFor(at(6), LABELS).label).toBe('This week');
    expect(dateBucketFor(at(7), LABELS).label).toBe('This month');
  });

  it('falls back to a named month once the current one is behind us', () => {
    // 2026-09-13 minus 20 days = 2026-08-24, a different calendar month.
    expect(dateBucketFor(at(20), LABELS).label).toBe('August 2026');
    expect(dateBucketFor(at(20), LABELS).id).toBe('m-2026-08');
    // …and two files from the same month share a rung, which is the whole
    // point of collapsing: one heading, not one per day.
    expect(dateBucketFor(at(21), LABELS).id).toBe(dateBucketFor(at(20), LABELS).id);
  });

  it('reads a stamp from the future as today rather than as next year', () => {
    // Clock skew between a storage and the reader is ordinary. "September
    // 2027" over the file somebody just saved is worse than a day's slack.
    expect(dateBucketFor(new Date(NOW.getTime() + 3 * DAY), LABELS).label).toBe('Today');
  });

  it('has somewhere to put a row with no date at all', () => {
    expect(dateBucketFor(null, LABELS)).toEqual({ id: 'none', label: 'No date' });
  });

  it('draws headings for the date key, and for nothing else', () => {
    expect(groupingActive('modified', 'sort')).toBe(true);
    expect(groupingActive('modified', undefined)).toBe(true);
    // ⚠ The ranked case. The key is still `modified` — nothing cleared it —
    // the server's answer simply is not in that order, so a date heading over
    // it would repeat itself wherever the ranking interleaved two days.
    expect(groupingActive('modified', 'relevance')).toBe(false);
    for (const k of ['name', 'size', 'type'] as const) {
      expect(groupingActive(k, 'sort')).toBe(false);
    }
  });

  it('keeps rows that are aside from the dates in one run of their own', () => {
    const items = [
      { n: 'dirA', dir: true, d: at(40) },
      { n: 'dirB', dir: true, d: at(0) },
      { n: 'f1', dir: false, d: at(0) },
      { n: 'f2', dir: false, d: at(1) },
    ];
    const g = groupByDate(items, {
      active: true,
      dateOf: (i) => i.d,
      aside: (i) => (i.dir ? { id: 'dirs' } : null),
      labels: LABELS,
    });
    expect(g.runs.map((r) => [r.id, r.label, r.items.length])).toEqual([
      // ⚠ ONE folder run, despite the two folders being 40 days apart — which
      // is what lets "folders before files" hold while sorted by date. Filing
      // them by date would split a bucket in two and draw "Today" twice, once
      // over the folders and once over the files.
      ['dirs', null, 2],
      ['today', 'Today', 1],
      ['yesterday', 'Yesterday', 1],
    ]);
    expect(g.headingBefore(items[0])).toBeNull();
    expect(g.headingBefore(items[2])).toBe('Today');
    expect(g.headingBefore(items[3])).toBe('Yesterday');
  });
});

/* ── the views ───────────────────────────────────────────────────────────
 * Mounted for real, because the module being right is only half of it: a view
 * that stopped calling it would leave every assertion above green. */

function node(name: string, daysAgo: number | null, dir = false): FileNode {
  return {
    type: dir ? 'dir' : 'file',
    path: `s://${name}`,
    basename: name,
    extension: dir ? '' : 'txt',
    storage: 's',
    visibility: 'private',
    size: 10,
    file_size: 10,
    mime_type: 'text/plain',
    last_modified: daysAgo === null ? undefined : at(daysAgo).getTime(),
    extra_metadata: {},
  } as unknown as FileNode;
}

/** Two folders and one file per rung, in the order a `modified ↓` sort leaves
 *  them: folders first (the pane's comparator hoists them), then newest file
 *  to oldest. */
const ROWS: FileNode[] = [
  node('Photos', 40, true),
  node('Docs', 0, true),
  node('today.txt', 0),
  node('yesterday.txt', 1),
  node('midweek.txt', 3),
  node('earlier.txt', 9),
  node('august.txt', 20),
];

const base = {
  selected: new Set<string>(),
  locale: 'en' as const,
};

/** Every heading a mounted view actually drew, in order. */
function headings(html: string, cls: string): string[] {
  const re = new RegExp(`class="${cls}"[^>]*>([^<]*)<`, 'g');
  return [...html.matchAll(re)].map((m) => m[1].trim());
}

describe('all three views draw the same headings', () => {
  const expected = ['Today', 'Yesterday', 'This week', 'This month', 'August 2026'];

  it('the list', () => {
    const w = mount(ListView, { props: { ...base, files: ROWS } });
    expect(headings(w.html(), 'fe-list__group')).toEqual(expected);
  });

  it('the grid — with "Folders" still naming the folder run', () => {
    const w = mount(GridView, { props: { ...base, files: ROWS, sections: true } });
    // ⚠⚠ "Files" is GONE, and that is the decision: while the rows are in date
    // order the date headings ARE the files' headings, so keeping "Files"
    // above them would name one group twice and tell the reader nothing the
    // second time. "Folders" stays — the folders are one run and no date can
    // name it.
    expect(headings(w.html(), 'fe-grid__heading')).toEqual(['Folders', ...expected]);
    expect(w.html()).not.toContain('>Files<');
  });

  it('the gallery', () => {
    const w = mount(GalleryView, { props: { ...base, files: ROWS } });
    // The gallery has never named its folder run and does not start now; it
    // gets the dates and nothing else.
    expect(headings(w.html(), 'fe-gal__heading')).toEqual(expected);
  });
});

describe('a heading is only drawn when it is true', () => {
  it('vanishes from all three views the moment the key is not the date', () => {
    setSort('name', 'asc');
    const list = mount(ListView, { props: { ...base, files: ROWS } });
    const grid = mount(GridView, { props: { ...base, files: ROWS, sections: true } });
    const gal = mount(GalleryView, { props: { ...base, files: ROWS } });

    expect(headings(list.html(), 'fe-list__group')).toEqual([]);
    expect(headings(gal.html(), 'fe-gal__heading')).toEqual([]);
    // ⚠ The grid does not go blank — it goes back to the sections it has
    // always drawn. Sorting by name is exactly when "Folders / Files" is the
    // only honest thing to say about the order.
    expect(headings(grid.html(), 'fe-grid__heading')).toEqual(['Folders', 'Files']);
  });

  it('vanishes over a search’s ranked answer, with the key untouched', () => {
    setSort('modified', 'desc');
    const props = { ...base, files: ROWS, order: 'relevance' as const };
    expect(headings(mount(ListView, { props }).html(), 'fe-list__group')).toEqual([]);
    expect(headings(mount(GalleryView, { props }).html(), 'fe-gal__heading')).toEqual([]);
    expect(
      headings(mount(GridView, { props: { ...props, sections: true } }).html(), 'fe-grid__heading'),
    ).toEqual(['Folders', 'Files']);
  });

  it('files an undated row under "No date" rather than under a day', () => {
    const rows = [node('dated.txt', 0), node('undated.txt', null)];
    const w = mount(ListView, { props: { ...base, files: rows } });
    expect(headings(w.html(), 'fe-list__group')).toEqual(['Today', 'No date']);
  });
});
