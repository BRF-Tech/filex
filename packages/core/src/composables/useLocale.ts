/**
 * useLocale — tr/en string table with a tiny `t()` helper, and the three
 * things every surface in this product has to agree about: which BCP-47 tag
 * the viewer's language means, what a byte count reads as, and what an
 * instant reads as.
 *
 * No i18n library — the catalogue is small enough to ship inline (see
 * src/locales).
 *
 * ⚠ The four exports ABOVE the composable are module-level on purpose. A
 * plain module (`lib/shareTtl`), the admin app's own helpers
 * (`web/src/lib/format`) and a share message that has to read like the one
 * the server sends all need these same rules, and none of them can call a
 * composable. Before this they each wrote their own, and the copies did not
 * agree:
 *
 *   - three locale → tag mappings. This file said `en-US`, `InspectorPanel`
 *     said `en-GB`, `lib/shareTtl` said `en-GB`. `en-GB` renders a medium
 *     date as "12 Sep 2026, 15:25" and `en-US` as "Sep 12, 2026, 3:25 PM", so
 *     one file's timestamp read two ways depending on which panel you were
 *     looking at.
 *   - four byte formatters, two of them disagreeing about the base.
 *   - and an `InspectorPanel.formatDate()` that passed NO time zone at all,
 *     so the details panel printed the BROWSER's clock beside a listing row
 *     printing the viewer's chosen one.
 */

import { computed, getCurrentInstance, inject, type Ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { messages } from '../locales';
import { EXPLORER_CLOCK, activeTimeZoneFor, deviceTimeZone } from '../lib/timezone';

/* ── the tag ──────────────────────────────────────────────────────────── */

/** The BCP-47 tag a catalogue code means. ONE mapping, for every surface. */
export function localeTag(code: LocaleCode | string | undefined): string {
  return code === 'tr' ? 'tr-TR' : 'en-US';
}

/* ── bytes ────────────────────────────────────────────────────────────── */

export type ByteUnitKey =
  | 'unit.bytes'
  | 'unit.kb'
  | 'unit.mb'
  | 'unit.gb'
  | 'unit.tb'
  | 'unit.pb';

const BYTE_UNIT_KEYS: ByteUnitKey[] = [
  'unit.bytes',
  'unit.kb',
  'unit.mb',
  'unit.gb',
  'unit.tb',
  'unit.pb',
];

/** The catalogue's own English values, for callers with no `t()` to hand. */
const BYTE_UNIT_EN: Record<ByteUnitKey, string> = {
  'unit.bytes': 'B',
  'unit.kb': 'KB',
  'unit.mb': 'MB',
  'unit.gb': 'GB',
  'unit.tb': 'TB',
  'unit.pb': 'PB',
};

export interface ByteSizeOptions {
  /** Resolve a unit key to its label. Default: the English short forms. */
  unit?: (key: ByteUnitKey) => string;
  /**
   * Tag for the NUMBER itself — decimal separator and grouping. `null` (the
   * default) is the C locale: a dot, no grouping, deterministic. Pass the
   * viewer's tag on any surface a person reads, so Turkish gets "1,43 MB".
   */
  numberLocale?: string | null;
  /**
   * 1000 (the default) or 1024. See the note on `formatByteSize` — 1024 is
   * for mirroring some OTHER system's arithmetic, never a style choice.
   */
  base?: 1000 | 1024;
  /** `fixed1` always shows one decimal above B, like the server's humanSize(). */
  digits?: 'auto' | 'fixed1';
  /** What "no size at all" renders as. */
  empty?: string;
}

/**
 * A byte count as a string — the ONE implementation.
 *
 * There were four: this file (1024, i18n units), `web/src/lib/format`
 * (1000, English units), `SideNav` (1024, hardcoded English units, next to a
 * quota line that `HomeView` rendered through the shared one), and
 * `Trash.vue` (1024, in a file already importing the admin helpers). A quota
 * of 10 GB read "10 GB" in the admin panel, "9.3 GB" in the side nav and
 * "9.31 GB" on the home screen — three spellings of one number.
 *
 * ⚠⚠ THE BASE IS 1000, and that is a decision, not an accident:
 *
 *   - "KB" / "MB" / "GB" are SI prefixes and mean powers of 1000. Rendering
 *     1024-arithmetic under those letters is simply mislabelled; being right
 *     at 1024 would mean printing KiB/MiB/GiB everywhere instead.
 *   - Both places in this product that CONVERT between a unit and bytes are
 *     already decimal, and both say so in a comment: the per-user quota input
 *     (`web/src/views/UserEdit.vue`, `GB = 1_000_000_000`) and the usage/cost
 *     path (`backend/internal/usage/b2.go`: "B2 bills in decimal GB, not GiB
 *     — using 1024³ here would quietly overstate every upload by 7%").
 *   - So decimal is the only base under which a quota typed as "10 GB" reads
 *     back as "10 GB" and a stored volume matches the invoice beside it.
 *
 * `base: 1024` exists for exactly one reason: mirroring a number some other
 * program already printed. Today that is the share message, which has to read
 * like `humanSize()` in backend/internal/api/handlers/mail_templates.go.
 * That is an argument to this function, not a fifth copy of it.
 */
export function formatByteSize(
  bytes: number | null | undefined,
  opts: ByteSizeOptions = {},
): string {
  const empty = opts.empty ?? '—';
  if (bytes == null || !Number.isFinite(bytes) || bytes < 0) return empty;

  const base = opts.base ?? 1000;
  const label = opts.unit ?? ((k: ByteUnitKey) => BYTE_UNIT_EN[k]);

  let idx = 0;
  let value = bytes;
  while (value >= base && idx < BYTE_UNIT_KEYS.length - 1) {
    value /= base;
    idx += 1;
  }

  // Whole bytes are never fractional; above that, two digits while the
  // number is small enough for them to mean something, one after that.
  const digits = idx === 0 ? 0 : opts.digits === 'fixed1' ? 1 : value < 10 ? 2 : 1;

  let num: string;
  if (opts.numberLocale == null) {
    num = value.toFixed(digits);
  } else {
    const fmt = numberFormatter(opts.numberLocale, digits, opts.digits === 'fixed1' ? digits : 0);
    num = fmt ? fmt.format(value) : value.toFixed(digits);
  }
  return `${num} ${label(BYTE_UNIT_KEYS[idx])}`;
}

/**
 * ⚠ Cached. This runs once per row per repaint in a listing, and constructing
 * an `Intl.NumberFormat` is the expensive part of it — the old copy in this
 * file used `toFixed` and paid nothing, so switching to a locale-aware number
 * without a cache would be a visible cost on a folder with a few thousand
 * files. There are three or four distinct keys in a session.
 */
const nfCache = new Map<string, Intl.NumberFormat | null>();
function numberFormatter(tag: string, max: number, min: number): Intl.NumberFormat | null {
  const key = `${tag}|${max}|${min}`;
  if (!nfCache.has(key)) {
    try {
      nfCache.set(key, new Intl.NumberFormat(tag, {
        maximumFractionDigits: max,
        minimumFractionDigits: min,
      }));
    } catch {
      nfCache.set(key, null);
    }
  }
  return nfCache.get(key) ?? null;
}

/* ── instants ─────────────────────────────────────────────────────────── */

const dtfCache = new Map<string, Intl.DateTimeFormat>();

/**
 * A formatter for `tag` in the VIEWER's zone.
 *
 * ⚠ The cache key names the device zone when the viewer follows the device:
 * `Intl` resolves `timeZone: undefined` at CONSTRUCTION and freezes it, so a
 * cache keyed only on "device" would keep answering in whatever zone the tab
 * was opened in. Reading `activeTimeZone()` on every call is also what keeps
 * a template that formats a date reactive to the preference changing.
 */
function zonedFormatter(
  tag: string,
  opts: Intl.DateTimeFormatOptions,
  owner?: symbol,
): Intl.DateTimeFormat {
  const zone = activeTimeZoneFor(owner);
  const key = `${tag}|${zone ?? `device:${deviceTimeZone()}`}|${JSON.stringify(opts)}`;
  let fmt = dtfCache.get(key);
  if (!fmt) {
    fmt = new Intl.DateTimeFormat(tag, { ...opts, timeZone: zone });
    dtfCache.set(key, fmt);
  }
  return fmt;
}

/**
 * Render an instant for a human: the viewer's language AND the viewer's
 * chosen clock, together, in one call. Every date on every surface goes
 * through here — that is the whole point of it being here.
 */
export function formatInstant(
  d: Date,
  code: LocaleCode | string | undefined,
  opts: Intl.DateTimeFormatOptions,
  /** The explorer whose clock applies (EXPLORER_CLOCK); omitted = page-wide. */
  owner?: symbol,
): string {
  try {
    return zonedFormatter(localeTag(code), opts, owner).format(d);
  } catch {
    return d.toISOString();
  }
}

/** Seconds or milliseconds — the backend has sent both. Normalised once. */
function toDate(ms: number | undefined | null): Date | null {
  if (!ms) return null;
  const d = new Date(ms * (ms < 1e12 ? 1000 : 1));
  return Number.isNaN(d.getTime()) ? null : d;
}

/* ── counted messages ─────────────────────────────────────────────────── */

/**
 * The variables that COUNT something, in the order they are asked. The first
 * one a call passes decides the form of the sentence.
 */
export const COUNT_VARS = ['count', 'n', 'days'] as const;

/**
 * Which catalogue key a counted message reads: `key_one` when the count is
 * exactly one and the catalogue has that form, otherwise `key`.
 *
 * ⚠⚠ ONE rule, here, for every counted string in the package. The catalogue
 * is a flat table with no plural machinery, so a `{n} items` message printed
 * "1 items" — measured in the details panel's header on a folder with one
 * file (v0.41.0 screenshot pass, 2026-09-14). Two call sites had already
 * worked around it by choosing a `_one` key by hand (the advanced-search count
 * and the empty-trash confirmation); every other counted message had not, and
 * a few hedged with "item(s)". A rule each caller has to remember is a rule
 * the next caller forgets, so `t()` applies it and the catalogue only has to
 * SAY the singular.
 *
 * ⚠ Turkish does not inflect a noun after a number ("1 öğe", "5 öğe"), so its
 * `_one` entries read exactly like the plain ones. They exist for key parity,
 * not because the language needs them. `web/tests/i18n/corePlurals.test.ts`
 * fails an English counted message that has no singular.
 */
export function countedKey(
  key: string,
  vars: Record<string, string | number>,
  has: (key: string) => boolean,
): string {
  const counter = COUNT_VARS.find((v) => v in vars);
  if (counter === undefined || Number(vars[counter]) !== 1) return key;
  const one = `${key}_one`;
  return has(one) ? one : key;
}

export function useLocale(localeRef: Ref<LocaleCode> | (() => LocaleCode)) {
  const code = (): LocaleCode =>
    typeof localeRef === 'function' ? localeRef() : localeRef.value;

  const lookup = computed(() => messages[code()] ?? messages.en);

  // The explorer this component sits in, whose clock its dates are read on
  // (lib/timezone, timeZoneSourcesFor). Outside a component, or outside any
  // explorer, the page-wide answer.
  const clock = getCurrentInstance() ? inject(EXPLORER_CLOCK, undefined) : undefined;

  function t(key: string, vars: Record<string, string | number> = {}): string {
    const table = lookup.value;
    const raw = table[countedKey(key, vars, (k) => k in table)] ?? key;
    return Object.entries(vars).reduce(
      (acc, [k, v]) => acc.replaceAll(`{${k}}`, String(v)),
      raw,
    );
  }

  /** A byte count in the viewer's language and number format. */
  function formatSize(bytes: number | undefined | null): string {
    // A real zero (empty file / empty folder) is information, not absence —
    // `formatByteSize` renders it "0 B" rather than the em dash.
    return formatByteSize(bytes, {
      unit: (key) => t(key),
      numberLocale: localeTag(code()),
    });
  }

  /**
   * Render a FileNode's basename with locale-aware overrides:
   *   - `.trash` directory → "Çöp Kutusu" / "Trash"
   *   - Trash entries are stored as `<Ymd-His>-<rand>__<original>` so
   *     listings inside .trash/ would otherwise show timestamps. Strip
   *     that prefix so the user sees the original basename.
   */
  function nodeDisplayName(node: { basename: string }): string {
    if (node.basename === '.trash') return t('node.trash');
    const m = node.basename.match(/^\d{8}-\d{6}-[A-Za-z0-9]+__(.+)$/);
    if (m) return m[1];
    return node.basename;
  }

  /**
   * gorunum:v1 — one date format for the whole explorer.
   *
   * There were two: the grid drew "Sep 12, 2026" from the explorer's own
   * locale tag and the list drew "12.09.2026 15:25:04" from
   * `toLocaleString()`, i.e. from the BROWSER's locale. Two views of the same
   * folder, side by side in the same product, disagreeing about what day a
   * file was touched and in whose language. The epoch normalisation is the
   * same in both (the backend has sent seconds and milliseconds) and is kept
   * here so a third caller cannot get it wrong.
   *
   * `time: true` is the listing's variant — a column wide enough to carry the
   * clock, which is the one place the hour actually helps. It is also what
   * the details panel shows, so the panel and the row it describes print the
   * same characters.
   *
   * zaman:z1 — and the clock is the VIEWER's, from `lib/timezone`. The instant
   * on the wire is not touched; only which zone it is read against. Before
   * this, every date here was drawn in the browser's own zone, so a file
   * uploaded at 03:00 Istanbul read "03:00" to a colleague on GMT as well —
   * same digits, different instant. `undefined` (the default) still means the
   * device's zone, resolved live by Intl rather than frozen at first run.
   */
  function formatDate(ms: number | undefined | null, opts: { time?: boolean } = {}): string {
    const d = toDate(ms);
    if (!d) return '';
    const date = formatInstant(
      d,
      code(),
      {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
      },
      clock,
    );
    if (!opts.time) return date;
    return `${date}, ${formatInstant(d, code(), { hour: 'numeric', minute: '2-digit' }, clock)}`;
  }

  /**
   * The whole truth, for a tooltip: full date, seconds, the zone's name AND
   * its IANA id.
   *
   * The listing's cell is short by necessity ("Sep 12, 2026, 3:14 AM") and a
   * short date is exactly where the zone question bites — it looks like a
   * fact and it is a fact *about a clock nobody named*. Hovering says which.
   * The id is appended to the zone's display name because the display name
   * alone is ambiguous in the direction that matters here: half the world's
   * zones render as "GMT+3".
   */
  function formatDateFull(ms: number | undefined | null): string {
    const d = toDate(ms);
    if (!d) return '';
    const zone = activeTimeZoneFor(clock) ?? deviceTimeZone();
    const stamp = formatInstant(d, code(), { dateStyle: 'full', timeStyle: 'long' }, clock);
    return `${stamp} (${zone})`;
  }

  /**
   * "September 2026" — the header over a listing's date group.
   *
   * Here rather than in the view that draws it: the header answers "what
   * month is this row from", and it has to answer it on the same clock the
   * cell four pixels away prints. `ListView` built its own `Intl` formatter
   * for this, which is how the two came to be able to disagree.
   */
  function formatMonthYear(value: number | Date | undefined | null): string {
    const d = value instanceof Date ? value : toDate(value);
    if (!d) return '';
    return formatInstant(d, code(), { month: 'long', year: 'numeric' }, clock);
  }

  /**
   * `YYYY-MM` in the viewer's zone — a bucket id, not a label.
   *
   * `en-CA` is not a locale choice, it is the shortest route to ISO order out
   * of `Intl`; same trick as `lib/timezone.zonedDayNumber`, and for the same
   * reason: the month a row is grouped under must be the month its own cell
   * prints, which the DEVICE's calendar cannot tell you.
   */
  function zonedYearMonth(value: number | Date | undefined | null): string {
    const d = value instanceof Date ? value : toDate(value);
    if (!d) return '';
    try {
      return zonedFormatter('en-CA', { year: 'numeric', month: '2-digit' }, clock).format(d);
    } catch {
      return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
    }
  }

  return {
    t,
    formatSize,
    formatDate,
    formatDateFull,
    formatMonthYear,
    zonedYearMonth,
    /**
     * zaman:z1 — the wire's `last_modified` as a `Date`, or null.
     *
     * Handed out because "seconds or milliseconds" is a fact about the WIRE,
     * not about formatting, and every caller that needs the instant itself
     * (rather than a string) was otherwise re-typing `v * (v < 1e12 ? 1000 :
     * 1)`. ListView had the third copy and used it to group rows by day — so
     * the grouping and the cell four pixels away were reading the same number
     * through two different converters.
     */
    toDate,
    nodeDisplayName,
  };
}
