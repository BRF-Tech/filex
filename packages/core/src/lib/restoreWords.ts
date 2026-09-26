/**
 * What a restore from the Trash says — everything that happened to the
 * selection, in one notice.
 *
 * ⚠ It said only "something already has that name" when one item's name was
 * taken and another failed for another reason: the failure, its reason and
 * how many did come back went unsaid. Each part is a whole catalogue sentence;
 * two are joined with a dash, the way the catalogue joins clauses.
 */
export function sayRestore(
  out: { restored: number; taken: string[]; failed: number; reason?: string },
  t: (key: string, vars?: Record<string, string | number>) => string,
): { message: string; failure: boolean } {
  const parts: string[] = [];
  if (out.failed > 0) {
    parts.push(t('toast.restore_partial', { n: out.restored, failed: out.failed, reason: out.reason || t('toast.failed') }));
  }
  if (out.taken.length) parts.push(t('toast.restore_taken', { n: out.taken.length, name: out.taken[0] }));
  if (parts.length) return { message: parts.join(' — '), failure: true };
  return { message: t('toast.restored', { n: out.restored }), failure: false };
}
