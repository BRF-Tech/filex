/**
 * The storage form speaks minutes; the row and the API speak seconds
 * (`sync_interval_s`). One conversion in one place, used by the create and
 * the edit page alike, so the two cannot round differently.
 *
 * The server treats anything under 5 s as "not set" and uses its default
 * (15 min, or FILEX_SYNC_INTERVAL). The form shows that as an empty field
 * rather than a number nobody typed.
 */

/** Row seconds → form minutes ('' when the row has no cadence of its own). */
export function minutesFromSeconds(seconds: number | undefined | null): number | '' {
  if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds < 5) return '';
  return Math.max(1, Math.round(seconds / 60));
}

/** Form minutes → row seconds (0 = let the server pick). */
export function secondsFromMinutes(minutes: number | string | '' | null | undefined): number {
  if (minutes === '' || minutes === null || minutes === undefined) return 0;
  const n = typeof minutes === 'number' ? minutes : Number(minutes);
  if (!Number.isFinite(n) || n <= 0) return 0;
  return Math.round(n) * 60;
}
