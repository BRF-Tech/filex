// Staying awake while sync moves files, and not swapping the app under it.
//
// Run:  node --experimental-strip-types --test desktop/test/power.test.ts
//
// Measured: a first sync of 11,704 files / 52.6 GiB took about nine hours, and
// Windows slept for 1 h 40 min of them overnight — nothing moved while it
// slept. The app now holds a 'prevent-app-suspension' blocker while any pair
// is being worked on and releases it as soon as none is. The idle-time update
// install, which stops every watcher, also waits for a quiet engine.

import assert from 'node:assert/strict';
import test from 'node:test';

import { SleepGuard, quietMomentForUpdate, syncBusy } from '../src/power.ts';

const busy = { running: true, active: { pairId: 'pair-1', phase: 'transfer', done: 1, total: 9 } };
const idle = { running: true, active: null };
const dead = { running: false, active: { pairId: 'pair-1', phase: 'transfer', done: 1, total: 9 } };

test('busy = some running watcher is working on a pair right now', () => {
  assert.equal(syncBusy([idle, busy]), true);
  assert.equal(syncBusy([idle, idle]), false);
  assert.equal(syncBusy([]), false);
  // A process that is gone is not keeping anything busy, whatever it last said.
  assert.equal(syncBusy([dead]), false);
});

function fakeBlocker() {
  const calls: string[] = [];
  let next = 41;
  return {
    calls,
    start(type: 'prevent-app-suspension') {
      calls.push(`start:${type}`);
      return ++next;
    },
    stop(id: number) {
      calls.push(`stop:${id}`);
    },
  };
}

test('the blocker is taken once while busy and released when idle', () => {
  const b = fakeBlocker();
  const g = new SleepGuard(b);
  assert.equal(g.update(true), true);
  assert.equal(g.update(true), false, 'a second busy tick must not stack blockers');
  assert.equal(g.holding, true);
  assert.equal(g.update(false), true);
  assert.equal(g.holding, false);
  assert.deepEqual(b.calls, ['start:prevent-app-suspension', 'stop:42']);
});

test('release() lets go on quit and before an update — and is harmless when idle', () => {
  const b = fakeBlocker();
  const g = new SleepGuard(b);
  g.release();
  assert.deepEqual(b.calls, []);
  g.update(true);
  g.release();
  g.release();
  assert.deepEqual(b.calls, ['start:prevent-app-suspension', 'stop:42']);
});

test('the quiet-moment update waits for the engine, not only for the human', () => {
  const base = { ready: true, applying: false, windowOpen: false, idleSeconds: 3600, idleThreshold: 600 };
  assert.equal(quietMomentForUpdate({ ...base, syncBusy: false }), true);
  // Nobody at the keyboard, no window — but a transfer in flight: not now.
  assert.equal(quietMomentForUpdate({ ...base, syncBusy: true }), false);
  assert.equal(quietMomentForUpdate({ ...base, syncBusy: false, windowOpen: true }), false);
  assert.equal(quietMomentForUpdate({ ...base, syncBusy: false, idleSeconds: 60 }), false);
  assert.equal(quietMomentForUpdate({ ...base, syncBusy: false, ready: false }), false);
  assert.equal(quietMomentForUpdate({ ...base, syncBusy: false, applying: true }), false);
});
