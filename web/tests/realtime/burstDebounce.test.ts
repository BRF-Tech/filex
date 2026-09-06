// burstDebounce is the explorer's reaction to change frames: one folder
// re-listing per burst, never zero.
//
// The bug these tests pin is a starving trailing debounce. While change frames
// arrive closer together than the debounce window, each one cancels the pending
// reload, so the folder is never re-listed until the burst ends. It is the
// failure mode of the ONE thing this layer exists to do, and it is invisible in
// every short test — it needs a sustained stream to show up. Measured in a real
// browser on 2026-09-06 against a 5 000-file zip extraction: the folder's first
// re-listing came 114 s into the job, with a 40 s gap after it.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { burstDebounce } from '../../../packages/core/src/lib/burstDebounce';

describe('burstDebounce', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('a single change still waits exactly the debounce — no added latency', () => {
    const fn = vi.fn();
    const d = burstDebounce(fn, { wait: 200, maxWait: 2000 });
    d();
    vi.advanceTimersByTime(199);
    expect(fn).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('a short burst collapses into one call', () => {
    const fn = vi.fn();
    const d = burstDebounce(fn, { wait: 200, maxWait: 2000 });
    for (let i = 0; i < 6; i++) {
      d();
      vi.advanceTimersByTime(30);
    }
    vi.advanceTimersByTime(200);
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('a sustained stream is not starved — the ceiling still fires', () => {
    const fn = vi.fn();
    const d = burstDebounce(fn, { wait: 200, maxWait: 2000 });
    // 10 s of calls 50 ms apart: always closer than `wait`, so a plain trailing
    // debounce fires exactly zero times over the whole run.
    for (let i = 0; i < 200; i++) {
      d();
      vi.advanceTimersByTime(50);
    }
    expect(fn.mock.calls.length).toBeGreaterThanOrEqual(4);
  });

  it('the last call lands after the last change — never a stale folder', () => {
    // The anti-staleness promise: whatever the ceiling does mid-burst, the
    // final call must happen at or after the burst's last change, or the
    // explorer keeps showing the state before it for ever.
    const fires: number[] = [];
    let t = 0;
    const d = burstDebounce(() => fires.push(t), {
      wait: 200,
      maxWait: 2000,
      now: () => t,
    });
    let lastChangeAt = 0;
    for (let i = 0; i < 47; i++) {
      d();
      lastChangeAt = t;
      t += 50;
      vi.advanceTimersByTime(50);
    }
    t += 500;
    vi.advanceTimersByTime(500);
    expect(fires.length).toBeGreaterThan(0);
    expect(fires[fires.length - 1]).toBeGreaterThanOrEqual(lastChangeAt);
  });

  it('cancel drops a pending call — unmount must not reload a dead explorer', () => {
    const fn = vi.fn();
    const d = burstDebounce(fn, { wait: 200, maxWait: 2000 });
    d();
    d.cancel();
    vi.advanceTimersByTime(5000);
    expect(fn).not.toHaveBeenCalled();
  });
});
