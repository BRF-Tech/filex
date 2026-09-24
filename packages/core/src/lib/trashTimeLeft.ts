/**
 * The Trash's "time left" for one item, in words: the server's own count of
 * whole days before it is purged (`ttl_days`, trash/service.go — retention
 * minus the days since deletion, never below zero).
 *
 *   30 → "30 days"   1 → "1 day"   0 → "Due for deletion"   none → "—"
 *
 * ⚠ ONE sentence for one concept, shared by the explorer's Trash view
 * (ListView) and the admin panel's Trash page. They used to carry two key
 * families for it — `trash.days_left` in the explorer's table and
 * `trash.days_remaining` in the admin panel's — and the admin page printed
 * "0 days" for an item the explorer called due for deletion. The keys live in
 * the explorer's table (`trash.days_remaining`, `_one`, `_due`); a caller
 * hands its own `t` so a surface keeps its own reactivity.
 */
export function trashTimeLeft(
  ttl: number | null | undefined,
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  if (typeof ttl !== 'number' || !Number.isFinite(ttl)) return '—';
  if (ttl <= 0) return t('trash.days_remaining_due');
  return t('trash.days_remaining', { n: ttl });
}
