/**
 * opProgress — the one rule for "how far along is this queued operation".
 *
 * Both progress surfaces read it: the explorer's operations center
 * (PendingOpsTray → OperationsCenter) and the admin app's bottom-right tray.
 * Two copies of this rule are how one tray starts saying something the other
 * does not (filex lesson #119).
 *
 * Issue #27: a queued op counts SOURCES, so moving one large file — or one
 * folder of a thousand files — is "0 of 1" until the very end. Drawn as a
 * percentage that is a bar frozen at 0% that then vanishes, which the reporter
 * read as stuck. So:
 *
 *   - a running cross-storage transfer reports bytes (`bytes_done` /
 *     `bytes_total`); when the total is known that is the percentage;
 *   - bytes moving with no total yet, or a single source with nothing but a
 *     source count, has NO honest percentage: `null`, and the surface shows
 *     an indeterminate indicator instead of a fake 0%;
 *   - a single source whose storage driver counts the objects it works
 *     through (`objects_done` / `objects_total`: one folder on an object store
 *     is minutes of objects) draws those;
 *   - several sources still report per source, as before.
 */

export interface OpProgressLike {
  status: string;
  progress_total: number;
  progress_done: number;
  bytes_total?: number;
  bytes_done?: number;
  objects_total?: number;
  objects_done?: number;
}

function clampPercent(n: number): number {
  return Math.max(0, Math.min(100, Math.round(n)));
}

/** 0–100, or `null` when there is no honest number to draw. */
export function opPercent(op: OpProgressLike): number | null {
  if (op.status === 'done') return 100;
  const bytesTotal = op.bytes_total ?? 0;
  const bytesDone = op.bytes_done ?? 0;
  if (bytesTotal > 0) return clampPercent((bytesDone / bytesTotal) * 100);
  if (bytesDone > 0) return null;
  if (op.progress_total <= 1) {
    const objectsTotal = op.objects_total ?? 0;
    if (objectsTotal > 0) return clampPercent(((op.objects_done ?? 0) / objectsTotal) * 100);
    return null;
  }
  return clampPercent((op.progress_done / op.progress_total) * 100);
}
