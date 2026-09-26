// A drop being filled in after a drag-out (dragout.ts fulfilDrop): what it
// tells the window, and how often.
//
// ⚠ No `electron` import, so node:test can drive it (same boundary as
// src/sync-policy.ts). main.ts owns the window and the IPC; the rules are here.

import { CANCELLED } from './download-guard.ts';

/** The clock and timers the throttle runs on — the real ones, or a test's. */
export interface Timers {
  now: () => number;
  setTimer: (fn: () => void, ms: number) => unknown;
  clearTimer: (t: unknown) => void;
}

const REAL_TIMERS: Timers = {
  now: () => Date.now(),
  setTimer: (fn, ms) => setTimeout(fn, ms),
  clearTimer: (t) => clearTimeout(t as ReturnType<typeof setTimeout>),
};

/** Four reports a second: a count that moves, not a count nobody can read. */
export const REPORT_INTERVAL_MS = 250;

export interface ReportThrottle<T> {
  /** One report; `last` for the one that ends the work. */
  push(p: T, last?: boolean): void;
  /** The work is over without a last report: a held one is dropped, and
   *  nothing more is said. */
  close(): void;
  /** The last report has been said (or close() called): nothing more will be. */
  readonly finished: boolean;
}

/**
 * Hands `send` at most one report every `intervalMs`.
 *
 * ⚠ The drop's filling-in reports every file as it lands, inside folders too,
 * and each report was one IPC message and one repaint of the explorer's toast:
 * a folder of small files is hundreds a second, a line nobody can read and a
 * renderer kept busy repainting it. What gets through:
 *
 *   - the FIRST report, at once — the drop has started filling in;
 *   - within an interval, only the newest report, said when the interval is
 *     up — a burst followed by one big file must not leave the line on the
 *     count before the burst ended;
 *   - the LAST report (`last`), at once, whatever is held — and nothing after
 *     it.
 */
export function throttleReports<T>(
  send: (p: T) => void,
  intervalMs = REPORT_INTERVAL_MS,
  timers: Timers = REAL_TIMERS,
): ReportThrottle<T> {
  let lastSentAt = Number.NEGATIVE_INFINITY;
  let held: { p: T } | null = null;
  let timer: unknown = null;
  let finished = false;

  const flush = () => {
    timer = null;
    if (!held || finished) return;
    const { p } = held;
    held = null;
    lastSentAt = timers.now();
    send(p);
  };

  return {
    push(p: T, last = false) {
      if (finished) return;
      if (last) {
        if (timer !== null) timers.clearTimer(timer);
        timer = null;
        held = null;
        finished = true;
        send(p);
        return;
      }
      const wait = lastSentAt + intervalMs - timers.now();
      if (wait <= 0 && timer === null) {
        lastSentAt = timers.now();
        send(p);
        return;
      }
      held = { p };
      if (timer === null) timer = timers.setTimer(flush, Math.max(0, wait));
    },
    close() {
      if (timer !== null) timers.clearTimer(timer);
      timer = null;
      held = null;
      finished = true;
    },
    get finished() {
      return finished;
    },
  };
}

// ── the drops being filled in, each with its own Stop ────────────────────────

/** One report of a drop's filling-in, as the window receives it
 *  (dragout.ts DragProgress, with the folder it was dropped in). */
export interface FillReport {
  done: number;
  total: number;
  /** The folder the drop landed in. */
  dropped: string;
  name?: string;
  finished?: boolean;
  error?: string;
  files?: number;
}

/** A drop being filled in, as main.ts holds it. */
export interface Fill {
  readonly id: number;
  /** Aborted by Stop: fulfilDrop and the download in flight end on it. */
  readonly signal: AbortSignal;
  /** A report of this drop — to the window through its own throttle. */
  report(p: FillReport): void;
  /** fulfilDrop has returned: nothing of this drop is left to stop. */
  end(): void;
}

interface FillEntry {
  ctrl: AbortController;
  throttle: ReportThrottle<FillReport>;
  /** The newest report of this drop, sent or still held. */
  latest: FillReport | null;
}

/**
 * Every drop being filled in, each with its own AbortController.
 *
 * ⚠ One variable held "the drop being filled in": a second drop overwrote it,
 * so Stop reached only the newest, and once that one ended the first could not
 * be stopped at all.
 *
 * The explorer's Stop names no drop — the explorer shows ONE line for a
 * drop being filled in, the one it heard from last — so Stop stops that one:
 * the drop whose report reached the window last and has not ended. And it is
 * said at once: the stopped report goes out the moment Stop is pressed (the
 * download in flight aborts within milliseconds — download-guard.ts
 * landViaPart), and nothing of that drop is said after it.
 */
export class DragFills {
  private seq = 0;
  /** Drops the window has heard from and that have not ended, the one it
   *  heard from last at the end. */
  private readonly shown = new Map<number, FillEntry>();
  private readonly send: (p: FillReport) => void;
  private readonly timers: Timers;

  constructor(send: (p: FillReport) => void, timers: Timers = REAL_TIMERS) {
    this.send = send;
    this.timers = timers;
  }

  begin(): Fill {
    const id = ++this.seq;
    const entry: FillEntry = {
      ctrl: new AbortController(),
      latest: null,
      throttle: throttleReports<FillReport>(
        (p) => {
          this.shown.delete(id);
          if (!p.finished) this.shown.set(id, entry);
          this.send(p);
        },
        REPORT_INTERVAL_MS,
        this.timers,
      ),
    };
    return {
      id,
      signal: entry.ctrl.signal,
      report: (p) => {
        if (entry.throttle.finished) return;
        entry.latest = p;
        entry.throttle.push(p, p.finished === true);
      },
      end: () => {
        // A report still held would go out after the end and put the drop
        // back among those Stop can reach.
        entry.throttle.close();
        this.shown.delete(id);
      },
    };
  }

  /** The explorer's Stop: see the class. What it stopped, or null. */
  stop(): { id: number; dropped: string; files?: number } | null {
    const last = [...this.shown.entries()].at(-1);
    if (!last) return null;
    const [id, entry] = last;
    entry.ctrl.abort();
    const said: FillReport = { ...(entry.latest ?? { done: 0, total: 0, dropped: '' }), finished: true, error: CANCELLED };
    entry.throttle.push(said, true);
    return { id, dropped: said.dropped, files: said.files };
  }
}
