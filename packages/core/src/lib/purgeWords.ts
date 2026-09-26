/**
 * What a permanent delete from the explorer's Trash says.
 *
 * ⚠ On a server that runs purges as jobs, it said "Deleting 3 items
 * permanently…" when the server had refused one of the three: the refusal was
 * counted, then not said. Jobs of the queue are being deleted (the operations
 * centre says when each ends); inside the request they are deleted. Either
 * way a refusal is said with its count.
 */
export function sayPurge(
  out: { queued: boolean; purged: number; failed: number },
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  if (out.queued) {
    return out.failed
      ? t('toast.purging_partly', { n: out.purged, failed: out.failed })
      : t('toast.purging', { n: out.purged });
  }
  return out.failed
    ? t('toast.purged_partly', { n: out.purged, failed: out.failed })
    : t('toast.purged', { n: out.purged });
}
