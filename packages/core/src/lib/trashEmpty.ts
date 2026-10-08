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
 *
 * ⚠ What the run says is the SERVER's (0.54, finding A4): every status carries
 * `summary`, the server's sentence for where the run stands in the screen's
 * language, and the view shows it. A refusal is thrown as a RequestFailure:
 * the server's own sentence when it wrote one, else the status in the
 * reader's words — never the bare "504" a proxy's page used to become.
 */
import { requestFailure } from './errorWords';

/** One run as the endpoint reports it. `{running: false}` alone (no
 *  `started_at`) is "no run known" — the server restarted under it. */
export interface TrashEmptyStatus {
  /** The ops row behind the run (GET /api/files/ops/{id}, and its cancel). */
  op_id?: number;
  running: boolean;
  /** Waiting its turn behind another purge; `running` is true meanwhile. */
  queued?: boolean;
  /** Somebody stopped it; what it had not reached is still in the trash. */
  cancelled?: boolean;
  total?: number;
  scanned?: number;
  purged?: number;
  failed?: number;
  bytes?: number;
  error?: string;
  started_at?: string;
  finished_at?: string;
  /** Where the run stands, said by the server in the screen's language:
   *  "Emptying the trash… 120 of 61,844", "Trash emptied: …". */
  summary?: string;
}

export interface TrashEmptyIO {
  /** POST /api/admin/trash/empty. */
  start(): Promise<Response>;
  /** GET /api/admin/trash/empty. */
  status(): Promise<Response>;
  /** The run the server started (or, on BUSY, the caller's own run it is
   *  following) — once, whether or not it is still going. */
  onStart?(st: TrashEmptyStatus): void;
  /** Each look at a run that is still going. */
  onProgress?(st: TrashEmptyStatus): void;
  /** Another press had already started this caller's run; it is followed.
   *  `said` is the server's sentence for it ("The trash is already being
   *  emptied."). */
  onBusy?(said: string): void;
  /** True once nobody is watching any more — the view went away. */
  stopped?(): boolean;
  /** The pause between looks (a seam for tests). */
  sleep?(ms: number): Promise<void>;
  /** The screen's language, for a refusal the server did not word itself. */
  locale?: string;
}

/** A purge holds the trash and it is not one this caller may follow. */
export class TrashEmptyBusy extends Error {}

export const TRASH_EMPTY_POLL_MS = 1500;

type Json = Record<string, unknown>;

function parse(text: string): Json {
  try {
    const parsed: unknown = JSON.parse(text);
    return parsed && typeof parsed === 'object' ? (parsed as Json) : {};
  } catch {
    return {};
  }
}

async function body(res: Response): Promise<Json> {
  return parse(await res.text().catch(() => ''));
}

/**
 * Starts the purge and resolves with its final status — or `null` once
 * `stopped()` says nobody is watching, which ends the watching, never the
 * purge. Throws TrashEmptyBusy (its message the server's sentence), or a
 * RequestFailure for any other refusal (lib/errorWords: say it with
 * `serverWords`).
 */
export async function emptyTrashAndFollow(
  io: TrashEmptyIO,
  pollMs = TRASH_EMPTY_POLL_MS,
): Promise<TrashEmptyStatus | null> {
  const sleep = io.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
  const res = await io.start();
  const text = await res.text().catch(() => '');
  const first = parse(text);
  let st: TrashEmptyStatus;
  if (res.status === 409 && first.code === 'BUSY') {
    // The server hands back the caller's own run when it is that one; a run
    // it does not describe (another tenant's, the nightly sweep) cannot be
    // followed from here, and must not be reported as this caller's ending.
    const job = first.job as TrashEmptyStatus | undefined;
    if (!job?.running) {
      throw new TrashEmptyBusy(typeof first.message === 'string' && first.message ? first.message : String(first.error ?? 'busy'));
    }
    io.onBusy?.(typeof first.message === 'string' ? first.message : '');
    st = job;
  } else if (!res.ok) {
    throw requestFailure(res.status, text, io.locale);
  } else {
    st = first as unknown as TrashEmptyStatus;
  }
  io.onStart?.(st);
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
