// How the desktop app reads the sync engine's output.
//
// Run:  node --experimental-strip-types --test desktop/test/sync-output.test.ts
//
// The watcher's stdout and stderr are the ONLY channel between the engine and
// the app: every badge, the bottom strip, the rail dot and the Settings line
// are parsed out of them. A pipe delivers bytes, not lines, so a chunk can end
// anywhere — in the middle of a progress line, or of a Turkish file name.

import assert from 'node:assert/strict';
import test from 'node:test';

import { LineBuffer, SyncStatusTracker, parseEngineLine, parseEta } from '../src/sync-output.ts';

test('a line split across two chunks is read as ONE line', () => {
  const b = new LineBuffer();
  assert.deepEqual(b.push('pair-1: transfer: 1'), []);
  assert.deepEqual(b.push('20/300\npair-1: tra'), ['pair-1: transfer: 120/300']);
  assert.deepEqual(b.push('nsfer: 130/300\n'), ['pair-1: transfer: 130/300']);
});

test('…a multi-byte character split between chunks survives', () => {
  const b = new LineBuffer();
  const bytes = Buffer.from('  ! upload Türkçe adlı dosya.txt: HTTP 500\n', 'utf8');
  // Cut inside the two-byte "ü" (0xC3 0xBC).
  const cut = bytes.indexOf(0xbc);
  assert.deepEqual(b.push(bytes.subarray(0, cut)), []);
  assert.deepEqual(b.push(bytes.subarray(cut)), ['  ! upload Türkçe adlı dosya.txt: HTTP 500']);
});

test('…a Windows line ending is not part of the line, and the tail is kept for end()', () => {
  const b = new LineBuffer();
  assert.deepEqual(b.push('a\r\nb\r\nlast without newline'), ['a', 'b']);
  assert.deepEqual(b.end(), ['last without newline']);
  assert.deepEqual(b.end(), []);
});

test('the engine line grammar', () => {
  assert.deepEqual(parseEngineLine('pair-1: transfer: 20/300', 'out'), {
    kind: 'progress', pairId: 'pair-1', phase: 'transfer', detail: '20/300',
  });
  assert.deepEqual(parseEngineLine('pair-1: already in step', 'out'), {
    kind: 'settled', pairId: 'pair-1', complete: true,
  });
  assert.deepEqual(parseEngineLine('pair-1: 12/12 done — 3 up, 9 down  (1.2s)', 'out'), {
    kind: 'settled', pairId: 'pair-1', complete: true,
  });
  assert.deepEqual(parseEngineLine('pair-1: 10/12 done — 3 up, 7 down  (1.2s)', 'out'), {
    kind: 'settled', pairId: 'pair-1', complete: false,
  });
  assert.deepEqual(parseEngineLine('pair-2: list docs://x: HTTP 502', 'err'), {
    kind: 'pair-error', pairId: 'pair-2', message: 'pair-2: list docs://x: HTTP 502',
  });
  assert.deepEqual(parseEngineLine('  ! upload a.txt: HTTP 413', 'err'), {
    kind: 'error', message: '! upload a.txt: HTTP 413',
  });
  assert.deepEqual(parseEngineLine('Watching 2 pair(s); checking every 30s.', 'out'), {
    kind: 'info', text: 'Watching 2 pair(s); checking every 30s.',
  });
  assert.equal(parseEngineLine('   ', 'out'), null);
});

test('a progress line cut by the pipe no longer reads as "transfer 0/0"', () => {
  const t = new SyncStatusTracker('acc');
  t.feed(Buffer.from('pair-1: transfer: 1'), 'out');
  // Nothing complete yet — the half line must not be parsed on its own.
  assert.equal(t.status.active, null);
  t.feed(Buffer.from('20/300\n'), 'out');
  assert.deepEqual(t.status.active, { pairId: 'pair-1', phase: 'transfer', done: 120, total: 300 });
  assert.equal(t.status.lastLine, 'pair-1: transfer: 120/300');
});

test('the run summary ends the activity; a pair error ends it too and is reported', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: inventory: 12 item(s) here, listing the server…\n', 'out');
  assert.equal(t.status.active?.phase, 'inventory');
  t.feed('pair-1: already in step\n', 'out');
  assert.equal(t.status.active, null);

  t.feed('pair-2: plan: 4 change(s) to make\n', 'out');
  t.feed('pair-2: list docs://x: HTTP 502\n', 'err');
  assert.equal(t.status.active, null);
  assert.equal(t.status.lastError, 'pair-2: list docs://x: HTTP 502');
});

test('feed() says whether anything changed, so an empty chunk does not repaint the app', () => {
  const t = new SyncStatusTracker('acc');
  assert.equal(t.feed('', 'out'), false);
  assert.equal(t.feed('pair-1: transf', 'out'), false);
  assert.equal(t.feed('er: 1/2\n', 'out'), true);
});

test('what is still buffered when the process ends is read, not dropped', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: list docs://x: HTTP 502', 'err'); // no trailing newline
  assert.equal(t.status.lastError, null);
  t.end();
  assert.equal(t.status.lastError, 'pair-1: list docs://x: HTTP 502');
});

test('an unexpected exit is said out loud; a requested stop is not an error', () => {
  const a = new SyncStatusTracker('acc');
  a.exited(1, false);
  assert.equal(a.status.running, false);
  assert.equal(a.status.lastError, 'sync stopped unexpectedly (exit 1)');

  const b = new SyncStatusTracker('acc');
  b.exited(null, true);
  assert.equal(b.status.running, false);
  assert.equal(b.status.lastError, null);
});

// ── an error is a statement about a round, not a verdict for the process ──
//
// lastError used to be sticky: set by any stderr line and never cleared until
// the watcher restarted. One network blip at 09:00 kept the rail dot red and
// the folder line saying "HTTP 502" all day while every later round
// succeeded. It now clears when the SAME pair settles again.

test('an error clears once the same pair settles again', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: list docs://x: HTTP 502\n', 'err');
  assert.equal(t.status.lastError, 'pair-1: list docs://x: HTTP 502');
  t.feed('pair-1: inventory: 3 item(s) here, listing the server…\n', 'out');
  t.feed('pair-1: already in step\n', 'out');
  assert.equal(t.status.lastError, null);
  assert.deepEqual(t.status.errors, {});
});

test('…but not when a DIFFERENT pair settles', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: list docs://x: HTTP 502\n', 'err');
  t.feed('pair-2: already in step\n', 'out');
  assert.equal(t.status.lastError, 'pair-1: list docs://x: HTTP 502');
  assert.deepEqual(Object.keys(t.status.errors ?? {}), ['pair-1']);
});

test('a round that finished with failures keeps them, attributed to its pair', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: transfer: 12/12\n', 'out');
  t.feed('pair-1: 10/12 done — 3 up, 7 down, 0 removed here, 0 removed on the server  (2s)\n', 'out');
  t.feed('  ! upload a.txt: HTTP 413\n', 'err');
  assert.equal(t.status.lastError, '! upload a.txt: HTTP 413');
  assert.deepEqual(t.status.errors, { 'pair-1': '! upload a.txt: HTTP 413' });
  // The next round goes through: the failure is history.
  t.feed('pair-1: 2/2 done — 2 up, 0 down, 0 removed here, 0 removed on the server  (1s)\n', 'out');
  assert.equal(t.status.lastError, null);
});

test('…whichever of the two pipes the parent happens to read first', () => {
  // stdout and stderr are separate pipes: the "  !" lines the engine writes
  // AFTER the summary can reach us before it.
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: transfer: 12/12\n', 'out');
  t.feed('  ! upload a.txt: HTTP 413\n', 'err');
  t.feed('pair-1: 11/12 done — 11 up  (2s)\n', 'out');
  assert.equal(t.status.lastError, '! upload a.txt: HTTP 413');
  assert.deepEqual(Object.keys(t.status.errors ?? {}), ['pair-1']);
});

test('an incomplete round with no detail line yet still says it was incomplete', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: 10/12 done — 10 up  (2s)\n', 'out');
  assert.equal(t.status.lastError, 'pair-1: 10/12 done — 10 up  (2s)');
});

test('lastError is the most recent error still standing', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: list docs://a: HTTP 502\n', 'err');
  t.feed('pair-2: list docs://b: HTTP 503\n', 'err');
  assert.equal(t.status.lastError, 'pair-2: list docs://b: HTTP 503');
  t.feed('pair-2: already in step\n', 'out');
  assert.equal(t.status.lastError, 'pair-1: list docs://a: HTTP 502');
});

test('an unpaired folder takes its error with it', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: list docs://a: HTTP 502\n', 'err');
  t.feed('pair-2: list docs://b: HTTP 503\n', 'err');
  assert.equal(t.retainPairs(new Set(['pair-1'])), true);
  assert.equal(t.status.lastError, 'pair-1: list docs://a: HTTP 502');
  assert.equal(t.retainPairs(new Set(['pair-1'])), false);
});

// ── a token the server no longer accepts ──
//
// A revoked token used to mean a watcher printing `HTTP 401` every 30 seconds
// forever, and an app that retried it forever — even across reboots. The
// engine now exits with status 3 on a 401 and says so; an older engine that
// keeps looping is recognised by its 401 line.

test('exit status 3 is "signed out", not a crash', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('filex: signed out: the server no longer accepts this token (HTTP 401)\n', 'err');
  t.exited(3, false);
  assert.equal(t.status.signedOut, true);
  assert.equal(t.status.running, false);
  assert.equal(t.status.lastError, 'filex: signed out: the server no longer accepts this token (HTTP 401)');
});

test('…even when the process said nothing first', () => {
  const t = new SyncStatusTracker('acc');
  t.exited(3, false);
  assert.equal(t.status.signedOut, true);
  assert.ok(t.status.lastError);
});

test('an older engine that keeps looping on 401 is recognised from its output', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: list docs://work: HTTP 401: unauthorized\n', 'err');
  assert.equal(t.status.signedOut, true);
});

test('…but not from a 403, a 5xx, or a file that happens to be called "HTTP 401"', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: list docs://work: HTTP 403: this account is disabled\n', 'err');
  t.feed('pair-1: list docs://work: HTTP 502\n', 'err');
  t.feed('  ! upload HTTP 401.txt: HTTP 500\n', 'err');
  assert.notEqual(t.status.signedOut, true);
  t.exited(1, false);
  assert.notEqual(t.status.signedOut, true);
});

// ── the sync window ──
//
// Outside its window the watcher says `sync: waiting for the sync window …`
// once and starts no rounds. A round still busy when the window closes is
// cancelled like Ctrl-C and says `sync: the sync window … closed; …` — with NO
// summary line after it, so that line is what ends the activity. Missing it
// would leave a transfer "active" all day, and the sleep guard holding with it.

test('waiting for the sync window is a state, and it is not activity', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('sync: waiting for the sync window 22:00-07:00\n', 'out');
  assert.equal(t.status.waitingWindow, '22:00-07:00');
  assert.equal(t.status.active, null);
  // The window opens and a round starts.
  t.feed('pair-1: inventory: 3 item(s) here, listing the server…\n', 'out');
  assert.equal(t.status.waitingWindow, null);
});

test('a window that closes mid-transfer ends the activity even without a summary', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: transfer: 40/900 (1.2 GiB of 52.6 GiB, about 8h 10m left)\n', 'out');
  assert.equal(t.status.active?.pairId, 'pair-1');
  t.feed('sync: the sync window 22:00-07:00 closed; the rest continues when it opens\n', 'out');
  assert.equal(t.status.active, null);
  assert.equal(t.status.waitingWindow, '22:00-07:00');
  assert.equal(t.status.lastError, null, 'a closing window is not an error');
});

// ── bytes and the time left ──
//
// "transfer: 120/11704" said nothing about the nine hours ahead. The engine
// now appends `(<done> of <total>, about <eta> left)` — the bytes once there
// are bytes to move, the estimate once it has one — and keeps the leading
// "done/total" exactly as it was.

test('the transfer line carries bytes and an estimate', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: transfer: 120/11704 (1.2 GiB of 52.6 GiB, about 8h 10m left)\n', 'out');
  assert.deepEqual(t.status.active, {
    pairId: 'pair-1', phase: 'transfer', done: 120, total: 11704,
    bytesDone: '1.2 GiB', bytesTotal: '52.6 GiB', eta: '8h 10m', etaSeconds: 8 * 3600 + 10 * 60,
  });
});

test('…the bytes without an estimate in the first seconds', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: transfer: 3/40 (512 B of 12.0 MiB)\n', 'out');
  assert.deepEqual(t.status.active, {
    pairId: 'pair-1', phase: 'transfer', done: 3, total: 40, bytesDone: '512 B', bytesTotal: '12.0 MiB',
  });
});

test('…and an older engine\'s bare line still reads as before', () => {
  const t = new SyncStatusTracker('acc');
  t.feed('pair-1: transfer: 20/300\n', 'out');
  assert.deepEqual(t.status.active, { pairId: 'pair-1', phase: 'transfer', done: 20, total: 300 });
});

test('the estimate is read into seconds, whatever unit the engine chose', () => {
  assert.equal(parseEta('8h 10m'), 29400);
  assert.equal(parseEta('1h 0m'), 3600);
  assert.equal(parseEta('12m'), 720);
  assert.equal(parseEta('45s'), 45);
  assert.equal(parseEta('soon'), null);
  assert.equal(parseEta(''), null);
});
