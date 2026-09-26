// A drop being filled in after a drag-out: what it tells the window, and how
// often.
//
// Run:  node --experimental-strip-types --test desktop/test/dragfill.test.ts

import assert from 'node:assert/strict';
import test from 'node:test';

import { DragFills, throttleReports, type FillReport, type Timers } from '../src/dragfill.ts';

/** A clock the test moves by hand, with the timers that run on it. */
function clock(): Timers & { advance: (ms: number) => void } {
  let now = 0;
  let seq = 0;
  const due = new Map<number, { at: number; fn: () => void }>();
  return {
    now: () => now,
    setTimer: (fn, ms) => {
      const id = ++seq;
      due.set(id, { at: now + ms, fn });
      return id;
    },
    clearTimer: (id) => {
      due.delete(id as number);
    },
    advance(ms) {
      const end = now + ms;
      for (;;) {
        const next = [...due.entries()].filter(([, d]) => d.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
        if (!next) break;
        due.delete(next[0]);
        now = next[1].at;
        next[1].fn();
      }
      now = end;
    },
  };
}

// ── at most four reports a second ──
//
// ⚠ The shell reported every file of a dropped folder to the window as it
// landed: a folder of small files is hundreds of IPC messages a second, each
// one a toast repainted. Nobody reads a count that changes 300 times a second.

test('a folder of small files is said at most four times a second, its first and last report always', () => {
  const c = clock();
  const sent: number[] = [];
  const r = throttleReports<number>((n) => sent.push(n), 250, c);
  // 1000 files, one every 2 ms: two seconds of work.
  for (let n = 1; n <= 1000; n++) {
    r.push(n);
    c.advance(2);
  }
  r.push(1001, true);
  assert.equal(sent[0], 1, 'the first report is said at once');
  assert.equal(sent.at(-1), 1001, 'the last report is always said');
  // Two seconds at four a second, plus the first and the last.
  assert.ok(sent.length <= 10, `${sent.length} reports in two seconds`);
  for (let i = 2; i < sent.length - 1; i++) {
    assert.ok(sent[i] > sent[i - 1], 'reports keep their order');
  }
});

test('the count a burst ends on is said once the interval is up, not the one before it', () => {
  const c = clock();
  const sent: number[] = [];
  const r = throttleReports<number>((n) => sent.push(n), 250, c);
  r.push(1);
  r.push(2);
  r.push(3);
  assert.deepEqual(sent, [1], 'held while the interval runs');
  // A long file follows: no report for minutes. Its folder must not read "1".
  c.advance(250);
  assert.deepEqual(sent, [1, 3]);
  c.advance(60_000);
  assert.deepEqual(sent, [1, 3], 'nothing is said twice');
});

test('the last report goes at once, and nothing after it', () => {
  const c = clock();
  const sent: number[] = [];
  const r = throttleReports<number>((n) => sent.push(n), 250, c);
  r.push(1);
  r.push(2);
  r.push(3, true);
  assert.deepEqual(sent, [1, 3], 'the final report did not wait for the interval');
  c.advance(1_000);
  r.push(4);
  assert.deepEqual(sent, [1, 3], 'a held report or a late one came after the end');
  assert.equal(r.finished, true);
});

// ── each drop has its own Stop ──
//
// ⚠ One variable held "the drop being filled in": a second drop overwrote it,
// so Stop reached only the newest, and once that one ended the first could
// not be stopped at all. And Stop said nothing until the fill noticed it.

function fills(c = clock()) {
  const sent: FillReport[] = [];
  const reg = new DragFills((p) => sent.push(p), c);
  return { reg, sent, c };
}

const at = (dropped: string, files: number, extra: Partial<FillReport> = {}): FillReport => ({
  done: 0,
  total: 1,
  dropped,
  files,
  ...extra,
});

test("Stop stops the drop the window's line is about: the one that reported last", () => {
  const { reg, c } = fills();
  const a = reg.begin();
  a.report(at('/A', 0));
  const b = reg.begin();
  b.report(at('/B', 0));
  c.advance(300);
  a.report(at('/A', 5));
  const stopped = reg.stop();
  assert.equal(stopped?.dropped, '/A');
  assert.equal(a.signal.aborted, true, 'the drop on screen was not stopped');
  assert.equal(b.signal.aborted, false, 'the other drop was stopped with it');
  assert.equal(reg.stop()?.dropped, '/B', 'a second Stop reaches the other drop');
  assert.equal(b.signal.aborted, true);
});

test('a second drop does not take the first one’s Stop away', () => {
  const { reg } = fills();
  const a = reg.begin();
  a.report(at('/A', 0));
  const b = reg.begin();
  b.report(at('/B', 0));
  b.report(at('/B', 1, { finished: true }));
  b.end();
  assert.equal(reg.stop()?.dropped, '/A');
  assert.equal(a.signal.aborted, true, 'once the newer drop ended, the first could not be stopped');
});

test('Stop is said at once, and the stopped drop says nothing after it', () => {
  const { reg, sent } = fills();
  const a = reg.begin();
  a.report(at('/A', 0));
  a.report(at('/A', 3));
  reg.stop();
  assert.deepEqual(sent.at(-1), { done: 0, total: 1, dropped: '/A', files: 3, finished: true, error: 'cancelled' },
    'the explorer heard nothing until the fill noticed the Stop');
  const n = sent.length;
  a.report(at('/A', 4));
  a.report(at('/A', 4, { finished: true, error: 'cancelled' }));
  assert.equal(sent.length, n, 'the stopped drop was said to end twice');
});

test('a drop that has ended cannot be stopped, and Stop with nothing running does nothing', () => {
  const { reg } = fills();
  const a = reg.begin();
  a.report(at('/A', 1));
  a.report(at('/A', 2, { finished: true }));
  assert.equal(reg.stop(), null);
  assert.equal(a.signal.aborted, false, 'a finished drop was aborted');
  a.end();
  assert.equal(reg.stop(), null);
});

test('a drop that ended without a last report says nothing after it, and cannot be stopped', () => {
  const { reg, sent, c } = fills();
  const a = reg.begin();
  a.report(at('/A', 1));
  a.report(at('/A', 2));
  const n = sent.length;
  a.end();
  c.advance(1_000);
  assert.equal(sent.length, n, 'a held report went out after the drop had ended');
  assert.equal(reg.stop(), null, 'Stop reached a drop that had ended');
});
