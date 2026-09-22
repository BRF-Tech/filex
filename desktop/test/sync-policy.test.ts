// Which sync watchers run, and with what.
//
// Run:  node --experimental-strip-types --test desktop/test/sync-policy.test.ts

import assert from 'node:assert/strict';
import test from 'node:test';

import { EMPTY_STATE } from '../src/account-state.ts';
import { watcherAccounts, wantedWatchers } from '../src/sync-policy.ts';

const A = { id: 'a' };
const B = { id: 'b' };
const PAIRS = [
  { id: 'pair-1', account: 'a' },
  { id: 'pair-2', account: 'b' },
];

// ── pause ──
//
// A client in a broken state could not be stopped from the app: quitting it
// lasted until the next sign-in, and there was no switch that kept sync off.
// Pause is that switch, and it is stored — so it survives a restart, a reboot
// and the login item that starts the app hidden.

test('paused: no account gets a watcher, whatever is paired', () => {
  assert.deepEqual(watcherAccounts([A, B], { paused: true }), []);
  assert.deepEqual([...wantedWatchers(watcherAccounts([A, B], { paused: true }), PAIRS)], []);
});

test('resumed: every signed-in account with pairs gets its watcher back', () => {
  assert.deepEqual(watcherAccounts([A, B], { paused: false }), [A, B]);
  assert.deepEqual([...wantedWatchers(watcherAccounts([A, B], {}), PAIRS)].sort(), ['a', 'b']);
});

test('a fresh install is not paused', () => {
  assert.notEqual(EMPTY_STATE.syncPaused, true);
});
