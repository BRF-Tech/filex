// Display helpers — bytes, dates, durations.
//
// Locale-aware, and — zaman:z1 — ZONE-aware: every date here is drawn in the
// clock the viewer chose, which lib/timezone reads out of
// `@brftech/filex-core`. That is the SAME singleton `useLocale.formatDate`
// reads inside the explorer, on purpose: the admin panel and the file listing
// sit on the same screen, and one product must not print an instant two ways.
//
// The functions stay pure in the sense that matters (no I/O, no state of their
// own); the zone is read at call time so a change repaints without a reload.

import { formatByteSize } from '@brftech/filex-core';

import { activeTimeZone, deviceTimeZone } from './timezone';

/**
 * A byte count for the admin panel.
 *
 * The arithmetic is `formatByteSize` in @brftech/filex-core — the same call
 * the explorer makes, so the two halves of the product cannot drift again.
 * There were four implementations of this and two bases; the reasoning for
 * the surviving one (decimal, 1000) is on `formatByteSize` itself, and it is
 * the base the quota input on UserEdit.vue and the B2 usage report already
 * assume. Output is unchanged from the copy that used to live here.
 */
export function formatBytes(n: number, locale = 'en'): string {
  return formatByteSize(n, { numberLocale: locale });
}

export function formatNumber(n: number | null | undefined, locale = 'en'): string {
  if (n == null || !Number.isFinite(n)) return '—';
  return new Intl.NumberFormat(locale).format(n);
}

export function formatDate(input: string | Date | null | undefined, locale = 'en'): string {
  if (!input) return '—';
  const d = input instanceof Date ? input : new Date(input);
  if (Number.isNaN(d.getTime())) return '—';
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    timeZone: activeTimeZone(),
  }).format(d);
}

/**
 * The whole instant, named with the clock it is read against — for a tooltip
 * behind a short date. Mirrors core's `useLocale.formatDateFull`, because the
 * question a hover answers ("whose 14:05 is this?") is the same question on
 * both sides of the app.
 */
export function formatDateFull(
  input: string | Date | null | undefined,
  locale = 'en',
): string {
  if (!input) return '—';
  const d = input instanceof Date ? input : new Date(input);
  if (Number.isNaN(d.getTime())) return '—';
  const zone = activeTimeZone() ?? deviceTimeZone();
  try {
    const stamp = new Intl.DateTimeFormat(locale, {
      dateStyle: 'full',
      timeStyle: 'long',
      timeZone: zone,
    }).format(d);
    return `${stamp} (${zone})`;
  } catch {
    return d.toISOString();
  }
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

  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });

  if (abs < minute) return rtf.format(Math.round(diffMs / 1000), 'second');
  if (abs < hour) return rtf.format(Math.round(diffMs / minute), 'minute');
  if (abs < day) return rtf.format(Math.round(diffMs / hour), 'hour');
  if (abs < week) return rtf.format(Math.round(diffMs / day), 'day');
  return formatDate(d, locale);
}

export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—';
  if (seconds < 60) return `${Math.round(seconds)}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

export function truncate(s: string, max = 40): string {
  if (!s) return '';
  return s.length <= max ? s : s.slice(0, max - 1) + '…';
}
