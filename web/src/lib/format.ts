// Display helpers — bytes, numbers, dates, durations.
//
// ⚠⚠ Not formatters of their own: each one is `@brftech/filex-core`'s, the
// same call the explorer makes, bound to the admin panel's locale argument.
// The admin panel and the file listing sit on the same screen, and one
// product must not print an instant or a size two ways — which it did
// (QA, 2026-09-21): "21 Eyl 2026, 14:50" in the explorer beside "21 Eyl 2026
// 15:02" in an admin table, "2:50 PM" beside "02:51 PM", and "GB" under a
// French interface that says "Go" everywhere else. docs/CONTRIBUTING.md →
// "Dates and numbers: the explorer's format, everywhere".
//
// The zone is read at call time (core `lib/timezone`, the page-wide answer),
// so a change repaints without a reload.

import { formatByteSize, formatWhen, formatWhenFull, localeTag, translate } from '@brftech/filex-core';

/**
 * A byte count for the admin panel — `formatByteSize`, decimal (1000), with
 * the viewer's number format ("1,43 MB") and the catalogue's unit words
 * (`unit.*`, so a language pack's "Go" reaches the admin tables too).
 */
export function formatBytes(n: number, locale = 'en'): string {
  return formatByteSize(n, { numberLocale: localeTag(locale), unit: (key) => translate(locale, key) });
}

export function formatNumber(n: number | null | undefined, locale = 'en'): string {
  if (n == null || !Number.isFinite(n)) return '—';
  return new Intl.NumberFormat(localeTag(locale)).format(n);
}

/**
 * A share of a whole, 0–100, as the viewer's language writes a percentage:
 * "12.5%" / "%12,5" / "12,5 %". One decimal below 10, none above — the
 * precision the quota bars always used.
 */
export function formatPercent(pct: number | null | undefined, locale = 'en'): string {
  if (pct == null || !Number.isFinite(pct)) return '—';
  const digits = Math.abs(pct) < 10 ? 1 : 0;
  return new Intl.NumberFormat(localeTag(locale), {
    style: 'percent',
    maximumFractionDigits: digits,
    minimumFractionDigits: 0,
  }).format(pct / 100);
}

/**
 * How many files a storage counts — the number its "{n} files" label agrees
 * with (the plural form is picked on it). The storage's scan stats first, the
 * flat `file_count` the list endpoint also carries second, 0 when neither.
 *
 * ⚠ One definition: the Storages page and the Dashboard's storage cards both
 * print this count, and each used to carry its own copy.
 */
export function fileCountOf(s: { stats?: { file_count?: number } | null; file_count?: number }): number {
  return s.stats?.file_count ?? s.file_count ?? 0;
}

/** An instant with its clock — the explorer's listing format (`formatWhen`). */
export function formatDate(input: string | Date | null | undefined, locale = 'en'): string {
  return formatWhen(input, locale, { time: true }) || '—';
}

/**
 * The whole instant, named with the clock it is read against — for a tooltip
 * behind a short date (`formatWhenFull`, what the explorer's hover shows).
 */
export function formatDateFull(input: string | Date | null | undefined, locale = 'en'): string {
  return formatWhenFull(input, locale) || '—';
}

export function formatRelative(input: string | Date | null | undefined, locale = 'en'): string {
  if (!input) return '—';
  const d = input instanceof Date ? input : new Date(input);
  if (Number.isNaN(d.getTime())) return '—';

  const diffMs = d.getTime() - Date.now();
  const abs = Math.abs(diffMs);
  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;
  const week = 7 * day;

  const rtf = new Intl.RelativeTimeFormat(localeTag(locale), { numeric: 'auto' });

  if (abs < minute) return rtf.format(Math.round(diffMs / 1000), 'second');
  if (abs < hour) return rtf.format(Math.round(diffMs / minute), 'minute');
  if (abs < day) return rtf.format(Math.round(diffMs / hour), 'hour');
  if (abs < week) return rtf.format(Math.round(diffMs / day), 'day');
  return formatDate(d, locale);
}

/**
 * A client address without its port. ⚠ The audit log stored
 * `127.0.0.1:54452` — the client's source port, a different number on every
 * connection — until v0.43.0; rows written before still carry it.
 */
export function ipOnly(ip: string | null | undefined): string {
  const v = (ip ?? '').trim();
  if (!v) return '';
  const bracket = /^\[([^\]]+)\](?::\d+)?$/.exec(v);
  if (bracket) return bracket[1];
  const v4 = /^(\d{1,3}(?:\.\d{1,3}){3}):\d+$/.exec(v);
  if (v4) return v4[1];
  return v;
}

/**
 * A length of time, in the largest unit that fits, in the viewer's language
 * ("45 sec" / "45 sn"). It printed "45s" / "3m" / "2h" — English letters under
 * every language.
 */
export function formatDuration(seconds: number, locale = 'en'): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—';
  const [value, unit] =
    seconds < 60
      ? [seconds, 'second']
      : seconds < 3600
        ? [seconds / 60, 'minute']
        : seconds < 86400
          ? [seconds / 3600, 'hour']
          : [seconds / 86400, 'day'];
  return new Intl.NumberFormat(localeTag(locale), {
    style: 'unit',
    unit,
    unitDisplay: 'short',
    maximumFractionDigits: 0,
  }).format(Math.round(value));
}

export function truncate(s: string, max = 40): string {
  if (!s) return '';
  return s.length <= max ? s : s.slice(0, max - 1) + '…';
}
