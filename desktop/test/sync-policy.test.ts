// Which sync watchers run, and with what.
//
// Run:  node --experimental-strip-types --test desktop/test/sync-policy.test.ts

import assert from 'node:assert/strict';
import test from 'node:test';

import { EMPTY_STATE } from '../src/account-state.ts';
import { newStatus, type SyncStatus } from '../src/syncstatus.ts';
import {
  LIMIT_PRESETS_KIB,
  WINDOW_PRESETS,
  answerHold,
  normLimit,
  normWindow,
  heldItems,
  folderView,
  watchArgs,
  watchPrefsKey,
  watcherAccounts,
  wantedWatchers,
  windowContains,
} from '../src/sync-policy.ts';

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

// ── signed out by the server ──

test('an account the server signed out gets no watcher until it reconnects', () => {
  const out = { id: 'a', signedOut: '2026-09-22T10:00:00.000Z' };
  assert.deepEqual(watcherAccounts([out, B], {}), [B]);
  assert.deepEqual([...wantedWatchers(watcherAccounts([out, B], {}), PAIRS)], ['b']);
  const back = { id: 'a' };
  assert.deepEqual([...wantedWatchers(watcherAccounts([back, B], {}), PAIRS)].sort(), ['a', 'b']);
});

// ── bandwidth limits and the sync window ──
//
// A first sync filled the server's ~18 Mbit line for nine hours and everyone
// else on that server slowed down. Settings offers presets; they become the
// engine's --limit-down / --limit-up (KiB/s) and --window HH:MM-HH:MM.

test('with nothing chosen the watcher is started exactly as before', () => {
  // ⚠ No flag at all rather than `--limit-down 0`: an engine that predates the
  // flags refuses unknown ones and would not start.
  assert.deepEqual(watchArgs('acc-1', {}, '30s'), ['sync', 'run', '--account', 'acc-1', '--watch', '30s', '--quiet']);
  assert.deepEqual(
    watchArgs('acc-1', { limitDownKiB: 0, limitUpKiB: 0, syncWindow: '' }, '30s'),
    ['sync', 'run', '--account', 'acc-1', '--watch', '30s', '--quiet'],
  );
});

test('limits and a window become the engine flags, verbatim', () => {
  assert.deepEqual(
    watchArgs('acc-1', { limitDownKiB: 10240, limitUpKiB: 1024, syncWindow: '22:00-07:00' }, '30s'),
    [
      'sync', 'run', '--account', 'acc-1', '--watch', '30s', '--quiet',
      '--limit-down', '10240', '--limit-up', '1024', '--window', '22:00-07:00',
    ],
  );
});

test('a limit is a whole, non-negative KiB/s; anything else means no limit', () => {
  assert.equal(normLimit(5120), 5120);
  assert.equal(normLimit(1.9), 1);
  for (const bad of [-1, Number.NaN, Number.POSITIVE_INFINITY, '5120', null, undefined, {}]) {
    assert.equal(normLimit(bad), 0, String(bad));
  }
});

test('a window is HH:MM-HH:MM that does not start where it ends; anything else is "any time"', () => {
  assert.equal(normWindow('22:00-07:00'), '22:00-07:00');
  assert.equal(normWindow(' 19:00-08:00 '), '19:00-08:00');
  for (const bad of ['', '22:00-22:00', '24:00-07:00', '22:60-07:00', '7:00-8:00', '22:00', 42, null]) {
    assert.equal(normWindow(bad), '', String(bad));
  }
});

test('every preset Settings offers survives its own normalisation', () => {
  for (const v of LIMIT_PRESETS_KIB) assert.equal(normLimit(v), v);
  for (const w of WINDOW_PRESETS) assert.equal(normWindow(w), w);
});

test('a window may wrap midnight, and its end is exclusive', () => {
  const at = (h: number, m = 0) => h * 60 + m;
  assert.equal(windowContains('22:00-07:00', at(23)), true);
  assert.equal(windowContains('22:00-07:00', at(6, 59)), true);
  assert.equal(windowContains('22:00-07:00', at(7)), false);
  assert.equal(windowContains('22:00-07:00', at(12)), false);
  assert.equal(windowContains('09:00-17:00', at(9)), true);
  assert.equal(windowContains('09:00-17:00', at(17)), false);
  assert.equal(windowContains('', at(12)), true, 'no window is any time');
});

test('only a change the engine would see restarts the watchers', () => {
  assert.equal(watchPrefsKey({}), watchPrefsKey({ limitDownKiB: 0, limitUpKiB: 0, syncWindow: '' }));
  assert.notEqual(watchPrefsKey({}), watchPrefsKey({ limitDownKiB: 1024 }));
  assert.notEqual(watchPrefsKey({ limitUpKiB: 1024 }), watchPrefsKey({ limitUpKiB: 5120 }));
  assert.notEqual(watchPrefsKey({}), watchPrefsKey({ syncWindow: '22:00-07:00' }));
});

// ── the line under each folder in Settings ──

const NOON = 12 * 60;
// A status shaped exactly as the supervisor keeps it (src/syncstatus.ts):
// errors live per pair, the engine's own in lastError.
const running = (over: Partial<SyncStatus> = {}): SyncStatus => ({ ...newStatus('acc'), ...over });
const failing = (pairId: string, error: string) => ({ [pairId]: { error, line: null, local: null, busy: null } });

test('the folder line: pause and sign-out come first', () => {
  const st = running({ pairs: failing('pair-1', 'boom'), lastError: 'boom' });
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: true, signedOut: true, status: st, minuteOfDay: NOON }), { kind: 'paused' });
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: false, signedOut: true, status: st, minuteOfDay: NOON }), { kind: 'signed-out' });
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: false, signedOut: false, status: null, minuteOfDay: NOON }), { kind: 'starting' });
});

test('the folder line: the pair being worked on says what is happening; the others are watching', () => {
  const st = running({ active: { pairId: 'pair-2', phase: 'transfer', done: 3, total: 9 } });
  assert.deepEqual(folderView({ pairId: 'pair-2', paused: false, signedOut: false, status: st, minuteOfDay: NOON }), {
    kind: 'active', phase: 'transfer', done: 3, total: 9,
  });
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: false, signedOut: false, status: st, minuteOfDay: NOON }), { kind: 'watching' });
});

test('the folder line: an error is shown on ITS folder; the engine\'s own on every folder', () => {
  const st = running({ pairs: failing('pair-1', 'list docs://a: HTTP 502') });
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: false, signedOut: false, status: st, minuteOfDay: NOON }), {
    kind: 'error', message: 'list docs://a: HTTP 502',
  });
  assert.deepEqual(folderView({ pairId: 'pair-2', paused: false, signedOut: false, status: st, minuteOfDay: NOON }), { kind: 'watching' });
  const proc = running({ running: false, lastError: 'sync stopped unexpectedly (exit 1)' });
  assert.deepEqual(folderView({ pairId: 'pair-2', paused: false, signedOut: false, status: proc, minuteOfDay: NOON }), {
    kind: 'error', message: 'sync stopped unexpectedly (exit 1)',
  });
});

test('the folder line: "waiting for the window" only while the clock is outside it', () => {
  const st = running({ waitingWindow: '22:00-07:00' });
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: false, signedOut: false, status: st, minuteOfDay: NOON }), {
    kind: 'window', window: '22:00-07:00',
  });
  // Inside the window a quiet watcher may print nothing at all: the stale
  // "waiting" line must not outlive the opening of the window.
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: false, signedOut: false, status: st, minuteOfDay: 23 * 60 }), { kind: 'watching' });
});

test('the folder line: a watcher that is gone without a word is "stopped"', () => {
  const st = running({ running: false });
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: false, signedOut: false, status: st, minuteOfDay: NOON }), { kind: 'stopped' });
});

// Another filex on this computer holds the folder (the other copy of this
// app, or the CLI): the folder is being synced — not by this copy — and that is
// what its line says, not an error left from before, and not "watching".
test('the folder line: a folder another filex syncs says so, ahead of an old error', () => {
  const detail = 'another filex on this computer is syncing this pair (process 42)';
  const st = running({ pairs: { 'pair-1': { error: 'list docs://a: HTTP 502', line: null, local: null, busy: { detail } } } });
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: false, signedOut: false, status: st, minuteOfDay: NOON }), {
    kind: 'busy', detail,
  });
  assert.deepEqual(folderView({ pairId: 'pair-2', paused: false, signedOut: false, status: st, minuteOfDay: NOON }), { kind: 'watching' },
    'only that folder');
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: true, signedOut: false, status: st, minuteOfDay: NOON }), { kind: 'paused' },
    'a pause still comes first');
  const gone = running({ running: false, pairs: st.pairs });
  assert.notEqual(folderView({ pairId: 'pair-1', paused: false, signedOut: false, status: gone, minuteOfDay: NOON }).kind, 'busy',
    'a watcher that is gone is not waiting for anything');
});

test("the folder line carries the transfer's bytes and estimate through", () => {
  const st = running({
    active: {
      pairId: 'pair-1', phase: 'transfer', done: 120, total: 11704,
      bytesDone: '1.2 GiB', bytesTotal: '52.6 GiB', eta: '8h 10m', etaSeconds: 29400,
    },
  });
  assert.deepEqual(folderView({ pairId: 'pair-1', paused: false, signedOut: false, status: st, minuteOfDay: NOON }), {
    kind: 'active', phase: 'transfer', done: 120, total: 11704,
    bytesDone: '1.2 GiB', bytesTotal: '52.6 GiB', eta: '8h 10m', etaSeconds: 29400,
  });
});

// ── held items ──

test('a pair shows held items only while it is holding AND holds some', () => {
  assert.equal(heldItems({ hold_new: true, held: 5 }), 5);
  assert.equal(heldItems({ hold_new: true }), 0, 'holding, but the last run held nothing');
  assert.equal(heldItems({ hold_new: true, held: 0 }), 0);
  assert.equal(heldItems({ held: 5 }), 0, 'a count without the flag is stale');
  assert.equal(heldItems({ hold_new: false, held: 3 }), 0);
  assert.equal(heldItems({}), 0);
});

// The answer to a hold stops the watcher FIRST: a pass still running would
// write the hold again when it finishes, and landing between the command's own
// read and write that undid the answer. The order used to be answer → stop.
test('answering a hold stops the watcher before the engine is asked', async () => {
  const order: string[] = [];
  const said = await answerHold({
    stop: () => order.push('stop'),
    act: async () => {
      order.push('act');
      return 'pair-1: 3 held item(s) go to the server on the next run.';
    },
    refresh: async () => {
      order.push('refresh');
    },
    onError: () => {
      order.push('error');
    },
  });
  assert.deepEqual(order, ['stop', 'act', 'refresh']);
  assert.match(said ?? '', /go to the server/);
});

test('…and a failed answer still restarts the watcher, and says so', async () => {
  const order: string[] = [];
  const said = await answerHold({
    stop: () => order.push('stop'),
    act: async () => {
      throw new Error('no such pair');
    },
    refresh: async () => {
      order.push('refresh');
    },
    onError: () => {
      order.push('error');
    },
  });
  assert.equal(said, null);
  assert.deepEqual(order, ['stop', 'error', 'refresh']);
});
