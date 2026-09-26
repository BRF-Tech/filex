/**
 * "Catalog everything", followed to its end.
 *
 * ⚠ The explorer's button started the storage's full scan (the admin panel's
 * "Sync now"), flashed "started" for 2.5 s, and then the banner and the button
 * stayed exactly as they were for the hours a large storage takes: no sign it
 * was running, and no word when it ended or how. This follows the scan through
 * the storage's newest runs (`GET …/sync-runs?limit=5`) until it ends.
 *
 * The run followed is the one already walking the storage when the button
 * was pressed (the server starts no second one), or else the first run newer
 * than every run before the press. An older server opens it a moment AFTER
 * answering, so a run that has not appeared yet is waited for, but not for
 * ever.
 *
 * ⚠ A run is told by its id. The server lists runs by their start time, to
 * the second, so two that started in the same second come in either order;
 * and the runs before the press are needed to tell the new one from an old
 * one, so without them nothing is followed.
 */
export interface CatalogRun {
  id: number;
  status: string;
  error?: string;
}

export interface CatalogRunIO {
  /** The storage's newest runs, in any order. */
  runs(): Promise<CatalogRun[]>;
  /** Starts the scan (POST …/sync). */
  start(): Promise<void>;
  /** True once nobody is watching any more (the view went away). */
  stopped?(): boolean;
  /** The pause between looks (a seam for tests). */
  sleep?(ms: number): Promise<void>;
}

/** How the scan ended; `unknown` when it could not be followed to its end. */
export type CatalogEnd = { status: 'ok' | 'failed' | 'aborted' | 'unknown'; error?: string };

const ENDS = new Set(['ok', 'failed', 'aborted']);

export async function catalogAndFollow(
  io: CatalogRunIO,
  opts: { pollMs?: number; giveUpMs?: number } = {},
): Promise<CatalogEnd | null> {
  const pollMs = opts.pollMs ?? 3000;
  const giveUpMs = opts.giveUpMs ?? 60_000;
  const sleep = io.sleep ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)));
  const before = await io.runs().catch(() => null);
  await io.start();
  if (!before) return { status: 'unknown' };
  const running = before.filter((r) => r.status === 'running').map((r) => r.id);
  let following: number | null = running.length ? Math.max(...running) : null;
  const newerThan = before.length ? Math.max(...before.map((r) => r.id)) : 0;
  // Time spent learning nothing: the run not there yet, or the list unreadable.
  let quiet = 0;
  for (;;) {
    await sleep(pollMs);
    if (io.stopped?.()) return null;
    const runs = await io.runs().catch(() => null);
    if (runs && following === null) {
      const fresh = runs.filter((r) => r.id > newerThan).map((r) => r.id);
      if (fresh.length) following = Math.min(...fresh);
    }
    const run = runs && following !== null ? runs.find((r) => r.id === following) : undefined;
    if (run) {
      quiet = 0;
      if (run.status === 'running') continue;
      if (!ENDS.has(run.status)) return { status: 'unknown' };
      const status = run.status as CatalogEnd['status'];
      return run.error ? { status, error: run.error } : { status };
    }
    quiet += pollMs;
    if (quiet >= giveUpMs) return { status: 'unknown' };
  }
}
