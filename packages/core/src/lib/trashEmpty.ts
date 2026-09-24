/**
 * trashEmpty — "empty the trash" from the explorer's trash view, followed to
 * the end.
 *
 * ⚠⚠ POST /api/admin/trash/empty used to purge inside the request, so for a
 * large trash it never answered: nginx gave up with a 504 at sixty seconds and
 * the view reported "504". The endpoint now answers within seconds — 200 with
 * the final counts when the purge is done, 202 `{running: true, …}` while it
 * goes on in the background — and GET on the same path reports the run until
 * it ends. A 2xx is therefore not "emptied" any more; a status whose
 * `running` is false is.
 */

/** One run as the endpoint reports it. `{running: false}` alone (no
 *  `started_at`) is "no run known" — the server restarted under it. */
export interface TrashEmptyStatus {
  running: boolean;
  total?: number;
  scanned?: number;
  purged?: number;
  failed?: number;
  bytes?: number;
  error?: string;
  started_at?: string;
  finished_at?: string;
}

export interface TrashEmptyIO {
  /** POST /api/admin/trash/empty. */
  start(): Promise<Response>;
  /** GET /api/admin/trash/empty. */
  status(): Promise<Response>;
  /** Each look at a run that is still going. */
  onProgress?(st: TrashEmptyStatus): void;
  /** Another press had already started this caller's run; it is followed. */
  onBusy?(): void;
  /** True once nobody is watching any more — the view went away. */
  stopped?(): boolean;
  /** The pause between looks (a seam for tests). */
  sleep?(ms: number): Promise<void>;
}

/** A purge holds the trash and it is not one this caller may follow. */
export class TrashEmptyBusy extends Error {}

export const TRASH_EMPTY_POLL_MS = 1500;

type Json = Record<string, unknown>;

async function body(res: Response): Promise<Json> {
  try {
    const parsed: unknown = await res.json();
    return parsed && typeof parsed === 'object' ? (parsed as Json) : {};
  } catch {
    return {};
  }
}

/**
 * Starts the purge and resolves with its final status — or `null` once
 * `stopped()` says nobody is watching, which ends the watching, never the
 * purge. Throws TrashEmptyBusy, or the server's own sentence for any other
 * refusal.
 */
export async function emptyTrashAndFollow(
  io: TrashEmptyIO,
  pollMs = TRASH_EMPTY_POLL_MS,
): Promise<TrashEmptyStatus | null> {
  const sleep = io.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
  const res = await io.start();
  const first = await body(res);
  let st: TrashEmptyStatus;
  if (res.status === 409 && first.code === 'BUSY') {
    // The server hands back the caller's own run when it is that one; a run
    // it does not describe (another tenant's, the nightly sweep) cannot be
    // followed from here, and must not be reported as this caller's ending.
    const job = first.job as TrashEmptyStatus | undefined;
    if (!job?.running) throw new TrashEmptyBusy(String(first.error ?? 'busy'));
    io.onBusy?.();
    st = job;
  } else if (!res.ok) {
    throw new Error(typeof first.error === 'string' && first.error ? first.error : String(res.status));
  } else {
    st = first as unknown as TrashEmptyStatus;
  }
  while (st.running) {
    io.onProgress?.(st);
    await sleep(pollMs);
    if (io.stopped?.()) return null;
    try {
      const look = await io.status();
      const next = look.ok ? await body(look) : {};
      // Only an answer that says whether it is running moves the state on;
      // anything else is a look that failed.
      if (typeof next.running === 'boolean') st = next as unknown as TrashEmptyStatus;
    } catch {
      // One look that failed is not the end of the run: look again.
    }
  }
  return st;
}
