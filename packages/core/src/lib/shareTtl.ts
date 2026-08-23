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
export function validUntilLine(expiresAt: string | null | undefined, locale: 'tr' | 'en'): string {
  if (!expiresAt) return locale === 'tr' ? 'Bu bağlantının süresi yoktur.' : 'This link does not expire.';
  const d = new Date(expiresAt);
  const when = Number.isNaN(d.getTime())
    ? expiresAt
    : d.toLocaleString(locale === 'tr' ? 'tr-TR' : 'en-GB', { dateStyle: 'medium', timeStyle: 'short' });
  return locale === 'tr' ? `Bu bağlantı ${when} tarihine kadar geçerli.` : `This link is valid until ${when}.`;
}

/** The hint under an expiry control: what the server will allow at most. */
export function ttlCeilingHint(maxDays: number | undefined, locale: 'tr' | 'en'): string {
  const max = maxDays && maxDays > 0 ? Math.floor(maxDays) : 0;
  if (!max) return '';
  return locale === 'tr'
    ? `Bağlantılar en fazla ${max} gün geçerli olabilir (sunucu ayarı).`
    : `Links can be valid for at most ${max} day${max === 1 ? '' : 's'} (server setting).`;
}
