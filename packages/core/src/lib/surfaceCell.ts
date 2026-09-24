/**
 * surfaceCell — a plugin list's cell, as the reader reads it.
 *
 * A `list` column may say what its cells ARE (`format: "date" | "datetime"`),
 * and then the app hands over the machine's value — `2026-09-29`,
 * `2026-09-29T14:03:00Z` — and the host prints it the way the explorer prints
 * every date: in the reader's language, on the reader's clock. The value stays
 * what the column sorts by.
 *
 * ⚠⚠ Why the host and not the app (v0.43.0 wave 2, 2026-09-22): the
 * Signatures page's "Son tarih" read "2026-09-29" a column away from the
 * explorer's "22 Eyl 2026, 14:01". An app formatting dates itself means one
 * more date format per app — the one thing "one format, the explorer's" rules
 * out — and it cannot know the reader's chosen time zone.
 *
 * ⚠ No formatter of its own: a calendar DAY goes through useLocale's
 * `formatCalendarDay` (UTC, so no time zone can move a due date to the day
 * before), an instant through the reader's own `formatDate`. Anything the
 * format does not recognise is shown as it came (a dash, a word).
 */
import { formatCalendarDay } from '../composables/useLocale';

export type SurfaceCellFormat = 'date' | 'datetime';

/**
 * The cell's text. `formatInstant` is the reader's own date formatter
 * (useLocale().formatDate), which knows their language AND their clock.
 */
export function formatSurfaceCell(
  raw: string,
  format: string | undefined,
  locale: string,
  formatInstant: (ms: number, opts?: { time?: boolean }) => string,
): string {
  if (!raw || (format !== 'date' && format !== 'datetime')) return raw;
  const day = formatCalendarDay(raw, locale);
  if (day) return day;
  const ms = Date.parse(raw.trim());
  if (Number.isNaN(ms)) return raw;
  return formatInstant(ms, { time: format === 'datetime' }) || raw;
}
