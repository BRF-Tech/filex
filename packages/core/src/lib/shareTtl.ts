// The server caps how long a new share link may live (`share_max_ttl_days` in
// /api/capabilities, set by the admin under Protection; default 7 days). The
// dialogs read it so they only OFFER expiries the server will honour — a
// "30 days" option that quietly becomes 7 days is a lie on the screen, and a
// "Never" option that becomes a week is a worse one.
//
// One helper for every surface: the Share / Permissions panel, the standalone
// share dialog, the desktop app and the embeds all render the same choices
// from the same rule. A surface that clamps differently would mean the same
// product behaves two ways.

import { formatWhen, translate } from '../composables/useLocale'; /* zaman:z1 */

/**
 * A catalogue string for a bare locale code — this is a module, not a
 * component, so it cannot call `useLocale`. ⚠ Through `translate`, which is
 * `t()`'s own lookup: a language pack's words reach these lines too (they were
 * `tr ? … : …` pairs, which a pack could not translate and which printed
 * TURKISH under any language that was not English), and "{days} days" takes
 * its form by the pack language's plural rule, not English's. It had a copy
 * of `t()`'s lookup, and the copy read English's `_one` under a pack that had
 * written only the plain form.
 */
function say(locale: string, key: string, vars: Record<string, string | number> = {}): string {
  return translate(locale, key, vars);
}

export interface ExpiryOption {
  /** Days; 0 = never. */
  v: number;
  l: string;
}

/** The stock expiry choices before the ceiling is applied (days; 0 = never). */
export const STOCK_EXPIRY_DAYS = [0, 1, 7, 30];

/**
 * clampExpiryOptions returns the options a dialog may show under a ceiling
 * of `maxDays` (0/undefined = no ceiling → every option as is). Options past
 * the ceiling and "never" are dropped; the ceiling itself is added as the
 * longest choice when the stock list does not already contain it.
 */
export function clampExpiryOptions(
  days: number[],
  maxDays: number | undefined,
  label: (days: number) => string,
): ExpiryOption[] {
  const max = maxDays && maxDays > 0 ? Math.floor(maxDays) : 0;
  let list = max ? days.filter((d) => d > 0 && d <= max) : days.slice();
  if (max && !list.includes(max)) list.push(max);
  list = Array.from(new Set(list)).sort((a, b) => (a === 0 ? -1 : b === 0 ? 1 : a - b));
  return list.map((d) => ({ v: d, l: label(d) }));
}

/**
 * defaultExpiryDays is what a fresh dialog preselects: the ceiling when there
 * is one (the server would apply it anyway — showing it up front is honest),
 * otherwise "never".
 */
export function defaultExpiryDays(maxDays: number | undefined): number {
  return maxDays && maxDays > 0 ? Math.floor(maxDays) : 0;
}

/**
 * clampExpiryDate pulls a free-form expiry (datetime input) under the ceiling.
 * Returns the ISO string to send, or null for "never" when there is no
 * ceiling. `now` is injectable for tests.
 */
export function clampExpiryDate(
  chosen: Date | null,
  maxDays: number | undefined,
  now: Date = new Date(),
): { iso: string | null; clamped: boolean } {
  const max = maxDays && maxDays > 0 ? Math.floor(maxDays) : 0;
  if (!max) return { iso: chosen ? chosen.toISOString() : null, clamped: false };
  const limit = new Date(now.getTime() + max * 86400000);
  if (!chosen || chosen.getTime() > limit.getTime()) return { iso: limit.toISOString(), clamped: true };
  return { iso: chosen.toISOString(), clamped: false };
}

/** Value for a `<input type="datetime-local" max=…>` under the ceiling (local time, minute precision). */
export function expiryInputMax(maxDays: number | undefined, now: Date = new Date()): string | undefined {
  const max = maxDays && maxDays > 0 ? Math.floor(maxDays) : 0;
  if (!max) return undefined;
  const limit = new Date(now.getTime() + max * 86400000);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${limit.getFullYear()}-${pad(limit.getMonth() + 1)}-${pad(limit.getDate())}T${pad(limit.getHours())}:${pad(limit.getMinutes())}`;
}

/** "Valid until 30 Aug 2026, 14:05" / "Does not expire" from the server's `expires_at`. */
export function validUntilLine(
  expiresAt: string | null | undefined,
  locale: string,
  /** The explorer whose clock applies (lib/timezone EXPLORER_CLOCK). */
  clock?: symbol,
): string {
  if (!expiresAt) return say(locale, 'share.ttl.never');
  const d = new Date(expiresAt);
  // zaman:z1 — the viewer's chosen clock, not the browser's. "Valid until
  // 14:05" is a deadline, and a deadline printed in somebody else's zone is
  // the worst kind of wrong: it looks actionable. Both halves of that — the
  // zone AND the locale tag — come from `formatInstant`, which is the one
  // place either is decided. This file used to pass the zone correctly and
  // then map the tag itself, to 'en-GB', while useLocale mapped it to
  // 'en-US': the same deadline was spelled "20 Sept 2026, 13:53" in the share
  // dialog and "Sep 20, 2026, 1:53 PM" in the listing behind it.
  //
  // ⚠ And the FORMAT is the explorer's (`formatWhen`), not a `dateStyle:
  // 'medium'` pair of its own: that printed "21 Eyl 2026 15:02" beside the
  // listing's "21 Eyl 2026, 14:50" (QA, 2026-09-21).
  const when = Number.isNaN(d.getTime()) ? expiresAt : formatWhen(d, locale, { time: true }, clock);
  return say(locale, 'share.ttl.until', { when });
}

/** The hint under an expiry control: what the server will allow at most. */
export function ttlCeilingHint(maxDays: number | undefined, locale: string): string {
  const max = maxDays && maxDays > 0 ? Math.floor(maxDays) : 0;
  if (!max) return '';
  return say(locale, 'share.ttl.ceiling', { days: max });
}

/**
 * The share dialog's muted detail line: its facts joined with " · ".
 *
 * ⚠ The first fact is a whole sentence ("This link is valid until …, 10:00
 * AM.") and the ones after it are fragments ("3 downloads"), so a plain join
 * printed "10:00 AM. · 3 downloads" — a full stop in the middle of a line. A
 * fact that is followed by another loses its closing full stop; the last one
 * keeps whatever it has.
 */
export function shareDetailLine(facts: Array<string | null | undefined>): string {
  const kept = facts.filter((f): f is string => !!f);
  return kept.map((f, i) => (i < kept.length - 1 ? f.replace(/\.\s*$/, '') : f)).join(' · ');
}
