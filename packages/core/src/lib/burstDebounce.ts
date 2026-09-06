// burstDebounce: a trailing debounce that cannot be starved.
//
// The explorer re-lists a folder when a change frame says it changed, debounced
// so a burst of frames costs one listing instead of N. A PLAIN trailing
// debounce has a failure mode that only shows up under a sustained stream:
// every frame clears the pending timer, so while frames keep arriving closer
// together than `wait`, the reload never happens at all. That is not "one
// reload instead of many" — it is zero, and the person watching the folder sees
// nothing change until the job that is writing the files finishes.
//
// Measured in a real browser on 2026-09-06 against a 5 000-file zip extraction
// (frames roughly every 40 ms for 195 s): the folder's first re-listing came
// 114 s in, with a 40 s gap after it.
//
// `maxWait` is the ceiling: however long the stream goes on, the call happens
// at most `maxWait` after the FIRST frame of the run, and the run then starts
// again. A quiet folder is untouched — one frame still calls `wait` later.
export interface BurstDebounceOptions {
  /** Quiet period after the last call before firing. */
  wait: number;
  /** Ceiling from the first call of a run. Must be >= wait to mean anything. */
  maxWait: number;
  /** Clock, injectable for tests. */
  now?: () => number;
}

export interface BurstDebounced {
  (): void;
  /** Drop any pending call (unmount). */
  cancel(): void;
}

export function burstDebounce(fn: () => void, opts: BurstDebounceOptions): BurstDebounced {
  const now = opts.now ?? (() => Date.now());
  let timer: ReturnType<typeof setTimeout> | null = null;
  let runStartedAt: number | null = null;

  const call = () => {
    const t = now();
    if (runStartedAt === null) runStartedAt = t;
    // Never later than the ceiling, never sooner than 0.
    const untilCeiling = runStartedAt + opts.maxWait - t;
    const delay = Math.max(0, Math.min(opts.wait, untilCeiling));
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      runStartedAt = null;
      fn();
    }, delay);
  };
  call.cancel = () => {
    if (timer) clearTimeout(timer);
    timer = null;
    runStartedAt = null;
  };
  return call;
}
