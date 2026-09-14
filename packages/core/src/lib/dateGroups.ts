/**
 * dateGroups — ONE date ladder, for every view that draws one.
 *
 * ⚠⚠ WHY THIS FILE EXISTS. The ladder used to live inside `ListView.vue` as a
 * private `bucketFor()`, and only the list drew headings at all: the grid and
 * the gallery showed the same rows in the same date order with nothing naming
 * the days. The owner asked for the headings in every view ("Ve bunu her
 * görünümde de yapması lazım"), and the shape of that request is exactly the
 * shape this repository keeps getting wrong — a rule copied into a second view
 * agrees with the first on the day it is written and on no day after it.
 * `lib/sortOrder` and `lib/listing.byFoldersFirst` are here for the same
 * reason, after the grid and the list had already drifted once (filex lesson
 * #67). So the ladder is written once, and List, Grid and Gallery all read it.
 *
 * ⚠ THE HEADING IS A CLAIM ABOUT THE ORDER. "Today / Yesterday / March 2026"
 * over rows that are not in date order is not a grouping, it is a lie: the
 * same heading would reappear wherever the real order happened to interleave
 * two days. `groupingActive()` is that rule, and it is the reason grouping
 * follows the SORT KEY rather than a switch of its own — a switch would create
 * a state in which the key says one thing and the grouping another, and no
 * view could honestly draw both (`lib/viewPrefs`'s header says the same).
 *
 * ⚠ zaman:z1 — every boundary here is a boundary in the VIEWER's zone, never
 * the device's. A file touched at 01:00 in Istanbul belongs under "Yesterday"
 * for a reader on UTC and under "Today" for a reader on Istanbul, and the
 * header has to agree with the date the cell four pixels away prints. That is
 * why the labels and the month id are taken from the caller (`useLocale`,
 * which reads `lib/timezone`) instead of being formatted here.
 */
import { zonedDayNumber } from './timezone';

import type { ListingOrder, SortKey } from './sortOrder';

/** One rung: a stable id to compare runs by, and the words to draw. */
export interface DateBucket {
  id: string;
  label: string;
}

/**
 * What a view lends this module so the headings speak its language and read
 * its clock. All three come straight off `useLocale(...)`.
 */
export interface DateGroupLabels {
  t: (key: string) => string;
  /** "September 2026", in the viewer's locale and zone. */
  formatMonthYear: (value: number | Date | undefined | null) => string;
  /** `YYYY-MM` in the viewer's zone — a bucket id, not a label. */
  zonedYearMonth: (value: number | Date | undefined | null) => string;
}

/**
 * THE LADDER.
 *
 * Today · Yesterday · This Week · This Month · then one rung per month,
 * named ("September 2026"). Undated rows land in "No date", which the sort
 * already parks at the end in both directions (`lib/sortOrder.byActiveKey`).
 *
 * Measured against the reference the owner pointed at — filex.blipnova.com,
 * 2026-09-13, the `/recent` page, which is the only surface there that groups.
 * Its ladder is `Today · Yesterday · <that day's date>`: one heading per
 * calendar day for ever, taken from the browser's own clock. Ours keeps its
 * first two rungs verbatim and then collapses, deliberately:
 *
 *   - a heading per day is not a grouping once a listing is long — a folder
 *     touched file-by-file over a month draws thirty headings over thirty
 *     single rows;
 *   - the owner's own words name the rungs we keep: "son bir hafta, dün,
 *     bugün, bu ay içinde vb." (last week, yesterday, today, within this
 *     month, and so on);
 *   - and the reference reads the DEVICE's calendar, which is the bug
 *     `lib/timezone` exists to have fixed here.
 *
 * ⚠ "This Week" is the six days before yesterday (`day > today - 7`), NOT a
 * calendar week: a Monday listing with a calendar rule would put Sunday —
 * eighteen hours ago — under a month heading.
 *
 * ⚠ A stamp in the future reads best as "today". Clock skew between a storage
 * and the reader is ordinary, and "September 2027" over the file somebody just
 * saved is worse than a day's imprecision.
 */
export function dateBucketFor(d: Date | null, l: DateGroupLabels): DateBucket {
  if (!d) return { id: 'none', label: l.t('group.no_date') };
  const now = new Date();
  const today = zonedDayNumber(now);
  const day = zonedDayNumber(d);
  if (day >= today) return { id: 'today', label: l.t('group.today') };
  if (day === today - 1) return { id: 'yesterday', label: l.t('group.yesterday') };
  if (day > today - 7) return { id: 'week', label: l.t('group.this_week') };
  const ym = l.zonedYearMonth(d);
  if (ym === l.zonedYearMonth(now)) return { id: 'month', label: l.t('group.this_month') };
  return { id: `m-${ym}`, label: l.formatMonthYear(d) };
}

/**
 * Is a date heading honest over THIS listing?
 *
 * Both halves are required and neither is cosmetic:
 *   - the key has to be `modified`, because that is the only order in which
 *     consecutive rows share a day;
 *   - the listing must not be the server's RANKED answer, where the key is
 *     still `modified` (nothing cleared it) and the rows simply are not
 *     obeying it.
 *
 * Sorting by name with date headings on would be exactly the lie above, so
 * every other key draws no headings at all — in all three views.
 */
export function groupingActive(key: SortKey, order: ListingOrder | undefined): boolean {
  return order !== 'relevance' && key === 'modified';
}

/** A contiguous run of items that share a heading. `label: null` = no heading. */
export interface DateRun<T> {
  id: string;
  label: string | null;
  items: T[];
}

/** An item that is not filed under a date, and the run it belongs to instead. */
export interface AsideRun {
  id: string;
  /** A heading for that run, or null/absent for a run drawn without one. */
  label?: string | null;
}

export interface DateGroupOptions<T> {
  /** `groupingActive(...)`. False → one unlabelled run holding everything. */
  active: boolean;
  /** The instant this item is filed under, or null for "no date". */
  dateOf: (item: T) => Date | null;
  /**
   * Items that are NOT filed under a date — return the run they belong to, or
   * null to bucket them by date like everything else.
   *
   * ⚠ This is how "folders first" survives a date sort. Folders are given a
   * run of their own at the top rather than floating to the top of each
   * bucket, because floating would split a bucket in two and draw "Today"
   * twice — once over the folders, once over the files. A folder has no
   * meaningful modification date to file under anyway. The list uses it for
   * its pinned storage rows as well.
   */
  aside?: (item: T) => AsideRun | null;
  labels: DateGroupLabels;
}

/**
 * The grouping, in the two shapes the three views need.
 *
 * `runs` is for a view that renders segment by segment (the list, whose rows
 * are already nested under a heading element). `headingBefore` is for a view
 * that renders one flat sequence of cards and inserts a full-width heading
 * item in front of the card that starts a run (the grid and the gallery).
 *
 * ⚠ They are two readings of ONE pass, not two implementations: the map is
 * built from the runs. A view that used its own "did the bucket change since
 * the previous card" test would be the second copy this module exists to
 * prevent.
 */
export interface DateGrouping<T> {
  /** Mirrors the `active` that was asked for — views branch on it. */
  active: boolean;
  runs: DateRun<T>[];
  /** The heading to draw immediately before this item, or null. */
  headingBefore: (item: T) => string | null;
}

export function groupByDate<T>(items: readonly T[], opts: DateGroupOptions<T>): DateGrouping<T> {
  const runs: DateRun<T>[] = [];
  if (!opts.active) {
    runs.push({ id: 'all', label: null, items: [...items] });
  } else {
    let cur: DateRun<T> | null = null;
    for (const item of items) {
      const side = opts.aside?.(item) ?? null;
      const b: { id: string; label: string | null } = side
        ? { id: side.id, label: side.label ?? null }
        : dateBucketFor(opts.dateOf(item), opts.labels);
      if (!cur || cur.id !== b.id) {
        cur = { id: b.id, label: b.label, items: [] };
        runs.push(cur);
      }
      cur.items.push(item);
    }
  }
  /* Identity-keyed: the items are the very objects the template loops over in
   * the same render, so no key function has to be invented — and inventing one
   * is how two views end up disagreeing about what "the same row" means. */
  const firsts = new Map<T, string>();
  for (const r of runs) {
    if (r.label && r.items.length) firsts.set(r.items[0], r.label);
  }
  return {
    active: opts.active,
    runs,
    headingBefore: (item: T) => firsts.get(item) ?? null,
  };
}
