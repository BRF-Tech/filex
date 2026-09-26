/**
 * What a permanent delete from the explorer's Trash says — once, and in words
 * that end.
 *
 * ⚠ On a server that runs purges as jobs, it said "Deleting 3 items
 * permanently…" when the server had refused one of the three: the refusal was
 * counted, then not said. Jobs of the queue are being deleted (the operations
 * centre says when each ends); inside the request they are deleted. Either
 * way a refusal is said with its count AND its reason — #69's sentences ended
 * on a dangling "…; 1 could not be".
 */
export function sayPurge(
  out: { queued: boolean; purged: number; failed: number; reason?: string },
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  // Nothing went: the reason is the whole sentence ("0 items deleted" says
  // less than why).
  if (out.purged === 0) return out.reason || t('toast.failed');
  const reason = out.reason || t('toast.failed');
  if (out.queued) {
    return out.failed
      ? t('toast.purging_partly', { n: out.purged, failed: out.failed, reason })
      : t('toast.purging', { n: out.purged });
  }
  return out.failed
    ? t('toast.purged_partly', { n: out.purged, failed: out.failed, reason })
    : t('toast.purged', { n: out.purged });
}

/** How a queued purge ended, once its last job has. */
export interface PurgeSummary {
  purged: number;
  failed: number;
  /** The first failure's words (a refusal at the request, or a job's). */
  reason?: string;
}

/**
 * The queued purges this explorer started, followed as batches: ONE summary
 * when a batch's last job ends, instead of a toast, a reading of the trash and
 * a probe of the trash policy per job (#69: a purge of N items said N toasts
 * and read the trash N times).
 */
export function createPurgeBatches() {
  interface Batch {
    jobs: Set<number>;
    purged: number;
    failed: number;
    reason?: string;
  }
  const batches = new Set<Batch>();

  function batchOf(id: number): Batch | undefined {
    for (const b of batches) if (b.jobs.has(id)) return b;
    return undefined;
  }

  return {
    /** A batch: its queue jobs, and what the server refused at once. */
    start(jobs: number[], opts: { refused: number; reason?: string }): void {
      if (jobs.length === 0) return;
      batches.add({ jobs: new Set(jobs), purged: 0, failed: opts.refused, reason: opts.reason });
    },
    /** Is this job one of a batch's? */
    owns(id: number): boolean {
      return batchOf(id) !== undefined;
    },
    /**
     * A job ended: the batch's summary when it was the last, null while the
     * batch goes on, undefined for a job that is no batch's.
     */
    settle(job: { id: number; ok: boolean; reason?: string }): PurgeSummary | null | undefined {
      const b = batchOf(job.id);
      if (!b) return undefined;
      b.jobs.delete(job.id);
      if (job.ok) b.purged++;
      else {
        b.failed++;
        if (!b.reason && job.reason) b.reason = job.reason;
      }
      if (b.jobs.size > 0) return null;
      batches.delete(b);
      return b.reason === undefined
        ? { purged: b.purged, failed: b.failed }
        : { purged: b.purged, failed: b.failed, reason: b.reason };
    },
  };
}
