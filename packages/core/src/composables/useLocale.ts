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
import { localeOwnTable, localesVersion, type OwnLocaleTable } from '../lib/uiLocales';
import { pluralCategory } from '../lib/plural';
// ⚠ The table itself, not `../locales` (its index re-exports `locales/resolve`,
// which reads `lib/uiLocales` — the cycle uiLocales avoids the same way).
import { en as EN } from '../locales/en';
import { foreignText, isolateLtrRuns, isolateValue, localeDir } from '../lib/direction';
import { EXPLORER_CLOCK, activeTimeZoneFor, deviceTimeZone } from '../lib/timezone';

/* ── the tag ──────────────────────────────────────────────────────────── */

/**
 * The BCP-47 tag a catalogue code means. ONE mapping, for every surface.
 *
 * ⚠ `tr` → `tr-TR` and `en` (or nothing) → `en-US` are pinned — the two
 * catalogues the product ships, and what every date and size on screen has
 * been measured against. Every OTHER code is a language pack's, and it is
 * its own tag: it used to fall through to `en-US`, so a Spanish interface
 * printed "Sep 12, 2026, 3:25 PM" beside Spanish words (measured 2026-09-21).
 * A pack that needs a region's conventions names itself with the region
 * (`es-mx`, `pt-br`) and gets them; `Intl` canonicalises the case.
 */
const tagCache = new Map<string, string>();
export function localeTag(code: LocaleCode | string | undefined): string {
  if (!code || code === 'en') return 'en-US';
  if (code === 'tr') return 'tr-TR';
  let tag = tagCache.get(code);
  if (!tag) {
    try {
      tag = Intl.getCanonicalLocales(code)[0] || 'en-US';
    } catch {
      tag = 'en-US';
    }
    tagCache.set(code, tag);
  }
  return tag;
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
 * program already printed. Nothing in the product needs it today — the share
 * message used it to copy the server's e-mail (`humanSize()`, base 1024), and
 * the server writes sizes by THESE rules now (backend/internal/srvtext
 * `Bytes`), so the message, the mail and the listing say one thing. If a
 * mirror is ever needed again it is an argument here, not a fifth copy.
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

const DAY = /^(\d{4})-(\d{2})-(\d{2})$/;
const dayFormatters = new Map<string, Intl.DateTimeFormat>();

/**
 * A calendar DAY (`YYYY-MM-DD`) for a human: the same words formatDate's date
 * part prints ("29 Eyl 2026"), but read as that day, not as an instant — it is
 * formatted in UTC, so no viewer's time zone can move a due date to the day
 * before. '' for anything that is not such a day.
 *
 * ⚠ Here, beside formatInstant, because this file is the home of "a date for
 * a human" (web/tests/quality/duplication.test.ts, `datetime-format`): an
 * app's date column (lib/surfaceCell) must not grow a formatter of its own.
 */
export function formatCalendarDay(day: string, code: LocaleCode | string | undefined): string {
  const m = DAY.exec(day.trim());
  if (!m) return '';
  const d = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
  if (Number.isNaN(d.getTime())) return '';
  const tag = localeTag(code);
  let fmt = dayFormatters.get(tag);
  try {
    if (!fmt) {
      fmt = new Intl.DateTimeFormat(tag, { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' });
      dayFormatters.set(tag, fmt);
    }
    return fmt.format(d);
  } catch {
    return '';
  }
}

/** Seconds or milliseconds — the backend has sent both. Normalised once. */
function toDate(ms: number | undefined | null): Date | null {
  if (!ms) return null;
  const d = new Date(ms * (ms < 1e12 ? 1000 : 1));
  return Number.isNaN(d.getTime()) ? null : d;
}

/** Anything a date arrives as: epoch seconds or milliseconds, an ISO string,
 *  a Date. Null for nothing or for something that is not an instant. */
export type WhenInput = number | string | Date | null | undefined;

function instantOf(value: WhenInput): Date | null {
  if (value == null || value === '') return null;
  if (value instanceof Date) return Number.isNaN(value.getTime()) ? null : value;
  if (typeof value === 'number') return toDate(value);
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

const DATE_PART: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric', year: 'numeric' };
// ⚠ The clock is the LANGUAGE's short time, not a field list: `hour: 'numeric'`
//   printed Turkish "8:05" where Turkish writes "08:05", and `'2-digit'` would
//   print English "08:05 AM". Only noticed on CI: its zone is UTC, so the
//   test's 08:05Z stayed before ten there and became 11:05 at +3 (2026-09-24).
const TIME_PART: Intl.DateTimeFormatOptions = { timeStyle: 'short' };

/**
 * An instant as a person reads it — THE date format of the product: the
 * explorer's, "Sep 21, 2026, 2:50 PM" / "21 Eyl 2026, 14:50", in the viewer's
 * language and on the viewer's chosen clock. `time: true` adds the clock.
 *
 * ⚠⚠ One definition for every surface (QA, 2026-09-21): the explorer printed
 * "21 Eyl 2026, 14:50", the admin tables and the share dialog "21 Eyl 2026
 * 15:02" (a `dateStyle: 'medium'` pair and a `day: '2-digit', hour: '2-digit'`
 * set of their own), and English times read "2:50 PM" on one screen and
 * "02:51 PM" on the next. The admin panel's `formatDate`, `useLocale`'s
 * `formatDate` and the share lines all call this now; nothing else builds a
 * date formatter for a person to read (scripts/dup-scan.mjs, `datetime-format`).
 */
export function formatWhen(
  value: WhenInput,
  code: LocaleCode | string | undefined,
  opts: { time?: boolean } = {},
  /** The explorer whose clock applies (EXPLORER_CLOCK); omitted = page-wide. */
  owner?: symbol,
): string {
  const d = instantOf(value);
  if (!d) return '';
  const date = formatInstant(d, code, DATE_PART, owner);
  return opts.time ? `${date}, ${formatInstant(d, code, TIME_PART, owner)}` : date;
}

/**
 * The whole truth, for a tooltip behind a short date: full date, seconds, the
 * zone's name AND its IANA id — "whose 14:05 is this?" answered. The IANA id
 * rides along because half the world's zones display as "GMT+3".
 */
export function formatWhenFull(
  value: WhenInput,
  code: LocaleCode | string | undefined,
  owner?: symbol,
): string {
  const d = instantOf(value);
  if (!d) return '';
  const zone = activeTimeZoneFor(owner) ?? deviceTimeZone();
  return `${formatInstant(d, code, { dateStyle: 'full', timeStyle: 'long' }, owner)} (${zone})`;
}

/* ── counted messages ─────────────────────────────────────────────────── */

/**
 * The variables that COUNT something, in the order they are asked. The first
 * one a call passes decides the form of the sentence.
 */
export const COUNT_VARS = ['count', 'n', 'days'] as const;

/**
 * Which key of ONE table a counted message reads: `key_<category>` when the
 * count falls in a category other than `other` in `lang` (CLDR, via
 * `lib/plural`) and the table has that form, otherwise the plain `key` — which
 * IS the `other` form (there is no `_other` key).
 *
 * The forms: `_zero`, `_one`, `_two`, `_few`, `_many`. A language writes the
 * ones it has — English and Turkish `_one`, Russian `_one` `_few` `_many`,
 * Arabic all five — and never `_other`.
 *
 * ⚠⚠ ONE rule, here, for every counted string in the package. The catalogue
 * is a flat table with no plural machinery, so a `{n} items` message printed
 * "1 items" — measured in the details panel's header on a folder with one
 * file (v0.41.0 screenshot pass, 2026-09-14). Two call sites had already
 * worked around it by choosing a `_one` key by hand (the advanced-search count
 * and the empty-trash confirmation); every other counted message had not, and
 * a few hedged with "item(s)". A rule each caller has to remember is a rule
 * the next caller forgets, so `t()` applies it and the catalogue only has to
 * SAY the forms.
 *
 * ⚠ Until v0.43.0 the rule was English's alone ("`_one` for exactly 1"),
 * which no language with few/many can be written in — the Arabic pack could
 * not say "2 files" at all.
 *
 * ⚠ Turkish does not inflect a noun after a number ("1 öğe", "5 öğe"), so its
 * `_one` entries read exactly like the plain ones. They exist for key parity,
 * not because the language needs them. `web/tests/i18n/plurals.test.ts`
 * fails an English counted message that has no singular.
 *
 * `lang` defaults to English — the rule this function had before it had the
 * argument. `t()` does not call this on a merged table; see `pickMessage`.
 */
export function countedKey(
  key: string,
  vars: Record<string, string | number>,
  has: (key: string) => boolean,
  lang = 'en',
): string {
  const counter = COUNT_VARS.find((v) => v in vars);
  if (counter === undefined) return key;
  const cat = pluralCategory(lang, Number(vars[counter]));
  if (cat === 'other') return key;
  const form = `${key}_${cat}`;
  return has(form) ? form : key;
}

/** A catalogue value, or undefined — never an `Object.prototype` member. */
function entry(table: Readonly<Record<string, string>>, key: string): string | undefined {
  const v = table[key];
  return typeof v === 'string' && v ? v : undefined;
}

/**
 * The raw (uninterpolated) message a key reads in a language.
 *
 *   1. the form the count's category calls for, if the language WROTE it;
 *   2. else the language's own plain `key` (its `other` form);
 *   3. else English as a whole — English's form by English's rule
 *      (`key_one` for 1), then English's plain `key`;
 *   4. else the key itself.
 *
 * ⚠⚠ Step 2 before step 3, and `own` must be the language's OWN words
 * (`localeOwnTable`), not the English-under-pack merge (`localeTable`). With
 * the merge, a Spanish pack that wrote only `toast.restored` still "had"
 * `toast.restored_one` — the English one underneath — and a count of one
 * printed "1 item restored" in the middle of a Spanish screen. A language's
 * own plain form, even if it reads as a plural for 1, is its translator's
 * choice; an English word in its place is nobody's.
 */
export function pickMessage(
  own: OwnLocaleTable,
  key: string,
  vars: Record<string, string | number> = {},
): string {
  const mine = own.strings;
  const form = countedKey(key, vars, (k) => entry(mine, k) !== undefined, own.lang);
  const said = entry(mine, form) ?? entry(mine, key);
  if (said !== undefined) return said;
  if (mine === EN) return key;
  const enForm = countedKey(key, vars, (k) => entry(EN, k) !== undefined, 'en');
  return entry(EN, enForm) ?? entry(EN, key) ?? key;
}

/** `{name}` placeholders, replaced verbatim. */
function interpolate(raw: string, vars: Record<string, string | number>): string {
  return Object.entries(vars).reduce(
    (acc, [k, v]) => acc.replaceAll(`{${k}}`, String(v)),
    raw,
  );
}

/**
 * One message, rendered: the plural form picked on the ORIGINAL vars (a count
 * wrapped in isolation marks is no longer a number to pick on), the values
 * put in, and — RTL only — a name interpolated into the sentence isolated so
 * its own letters decide its direction (`isolateValue`, strings only), and a
 * `3 / 10` or a `tag:…` kept reading left to right (`isolateLtrRuns`). A
 * left-to-right string comes back exactly as it always did.
 *
 * ⚠ The one path for `t()` and `translate()`, so a sentence built outside a
 * component (lib/shareTtl) reads the same way under Arabic as one inside.
 */
function render(
  table: OwnLocaleTable,
  key: string,
  vars: Record<string, string | number>,
  rtl: boolean,
): string {
  const raw = pickMessage(table, key, vars);
  if (!rtl) return interpolate(raw, vars);
  const isolated: Record<string, string | number> = {};
  for (const [k, v] of Object.entries(vars)) isolated[k] = typeof v === 'string' ? isolateValue(v) : v;
  return isolateLtrRuns(interpolate(raw, isolated));
}

/**
 * `t()` for code that is not a component (`lib/shareTtl`): the same lookup,
 * the same plural rule, the same placeholders. ⚠ Not reactive — a caller that
 * renders the result in a template must read `localesVersion` itself.
 */
export function translate(
  code: LocaleCode | string | undefined,
  key: string,
  vars: Record<string, string | number> = {},
): string {
  return render(localeOwnTable(code), key, vars, localeDir(code) === 'rtl');
}

export function useLocale(
  /**
   * ⚠ Widened past `LocaleCode` on purpose (v3 §5). The list of languages
   * the interface offers is no longer the two this package ships: an app
   * plugin may add one (`lib/uiLocales`), so the active code can be a tag
   * this file has never heard of. It resolves through `localeOwnTable`, and
   * every key the language does not have reads English.
   */
  localeRef: Ref<LocaleCode | string> | (() => LocaleCode | string),
) {
  const code = (): string =>
    typeof localeRef === 'function' ? localeRef() : localeRef.value;

  /**
   * ⚠ `localesVersion` is READ here so the table is re-derived when an app
   * installs or removes a language. Without it a screen already on the page
   * would keep the table it was mounted with — an app's own words would
   * arrive one navigation late, which is the kind of miss nobody reports as
   * a bug and everybody works around.
   */
  const lookup = computed(() => {
    void localesVersion.value;
    return localeOwnTable(code());
  });

  // The explorer this component sits in, whose clock its dates are read on
  // (lib/timezone, timeZoneSourcesFor). Outside a component, or outside any
  // explorer, the page-wide answer.
  const clock = getCurrentInstance() ? inject(EXPLORER_CLOCK, undefined) : undefined;

  /** ⚠ RTL — which way this surface's language is written (lib/direction). */
  const dir = computed(() => localeDir(code()));

  function t(key: string, vars: Record<string, string | number> = {}): string {
    return render(lookup.value, key, vars, dir.value === 'rtl');
  }
  /**
   * ⚠⚠ Text filex did NOT write, said the way this surface's language needs:
   * the server's own sentence, an installed app's words, a notification built
   * from what happened. None of it passes through `t()`, so none of it was
   * isolated — see lib/direction `foreignText`. It rides ON `t` so that the
   * helpers a component hands its translator to (lib/errorWords `sayFailure`,
   * `jobFailure`, `opFailure`) get the reader's direction without five more
   * signatures to thread a locale code through.
   */
  t.foreign = (text: string): string => foreignText(code(), text);

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
   * A listed item's size, the way every view draws it (list, inspector,
   * multi-selection total).
   *
   * ⚠ `size_partial` is the server saying the catalog does not cover all of a
   * folder yet (docs/LAZY-CATALOGUE.md): a lazily cataloged folder that is not
   * cataloged below, or any folder while its storage's first sync runs. Its
   * size is then a lower bound — "≥ 1.2 GB" — or, when nothing below it is
   * known, not a number at all ("—"): printing "0 B" for a folder full of
   * files nobody has cataloged yet would be a fact that is false. One helper,
   * so the list and the inspector cannot disagree about it.
   */
  function formatNodeSize(n: { size?: number | null; size_partial?: boolean } | null | undefined): string {
    if (!n) return formatSize(null);
    if (!n.size_partial) return formatSize(n.size);
    return typeof n.size === 'number' && n.size > 0 ? t('size.at_least', { size: formatSize(n.size) }) : '—';
  }

  /** The hover text of a size `formatNodeSize` drew as partial, or undefined. */
  function nodeSizeHint(n: { size?: number | null; size_partial?: boolean } | null | undefined): string | undefined {
    if (!n?.size_partial) return undefined;
    return typeof n.size === 'number' && n.size > 0 ? t('size.partial_hint') : t('size.unknown_hint');
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
    return formatWhen(ms, code(), opts, clock);
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
    return formatWhenFull(ms, code(), clock);
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
    formatNodeSize,
    nodeSizeHint,
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
    /**
     * ⚠ RTL — which way this surface's language is written (`lib/direction`,
     * the server's one list). Bind it as `dir` on anything Teleported out of
     * the explorer's root: under `<body>` a menu inherits the HOST page's
     * direction, not the language its own words are in.
     */
    dir,
  };
}
