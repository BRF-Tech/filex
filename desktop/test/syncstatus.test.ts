import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  absorbLine,
  anyError,
  LineReader,
  markExited,
  newStatus,
  pairView,
  parseEta,
  parseLiveLine,
  refusalApplies,
  retainPairs,
  SIGNED_OUT_EXIT,
  takeHolds,
} from '../src/syncstatus.ts';

// The engine's output → what the sync card says, pair by pair.
//
// Before these rules an error line was copied into ONE account-wide field and
// never cleared: with the live engine running a pass every few seconds, a
// failure from minutes ago sat under every synced folder of the account and
// read as a current one.

const out = (st: ReturnType<typeof newStatus>, line: string) => absorbLine(st, line, false);
const err = (st: ReturnType<typeof newStatus>, line: string) => absorbLine(st, line, true);

test('a pair\'s error is cleared by its next clean pass', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: 1/2 done — 0 up, 1 down, 0 removed here, 0 removed on the server, 1 failed  (5ms)');
  err(st, 'pair-1: ! download a.txt: connection reset');
  assert.equal(pairView(st, 'pair-1').error, 'download a.txt: connection reset');

  out(st, 'pair-1: 1/1 done — 0 up, 1 down, 0 removed here, 0 removed on the server  (4ms)');
  assert.equal(pairView(st, 'pair-1').error, null, 'a clean pass must clear the pair\'s error');
  assert.equal(st.lastError, null, 'and nothing account-wide may keep it');

  err(st, 'pair-1: list docs://work: HTTP 502: bad gateway');
  assert.equal(pairView(st, 'pair-1').error, 'list docs://work: HTTP 502: bad gateway');
  out(st, 'pair-1: already in step');
  assert.equal(pairView(st, 'pair-1').error, null);
});

test('an error for one pair never shows on another', () => {
  const st = newStatus('acc');
  err(st, 'pair-1: ! upload big.bin: HTTP 413: quota exceeded');
  assert.equal(pairView(st, 'pair-2').error, null);
  assert.ok(pairView(st, 'pair-1').error?.includes('quota exceeded'));
});

// stdout and stderr are two pipes; which one Node reads first is not
// guaranteed. A pass that failed says so in its own summary, so the order in
// which the summary and the error text arrive cannot clear a fresh failure.
test('a failed pass keeps its error whichever pipe is read first', () => {
  const summary = 'pair-1: 0/1 done — 0 up, 0 down, 0 removed here, 0 removed on the server, 1 failed  (5ms)';
  const detail = 'pair-1: ! download a.txt: connection reset';
  const a = newStatus('acc');
  err(a, detail);
  out(a, summary);
  assert.equal(pairView(a, 'pair-1').error, 'download a.txt: connection reset');
  const b = newStatus('acc');
  out(b, summary);
  err(b, detail);
  assert.equal(pairView(b, 'pair-1').error, 'download a.txt: connection reset');
});

// A raced action (the other side changed mid-pass; both versions are kept on
// the next pass) is not a failure and must not paint the folder red.
test('a raced pass is not an error', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: 0/1 done — 0 up, 0 down, 0 removed here, 0 removed on the server  (5ms)');
  out(st, 'pair-1: ~ download note.txt: changed on this computer while it was being synced — both versions are kept on the next pass');
  assert.equal(pairView(st, 'pair-1').error, null);
});

test('an error that is not about a pair shows on every pair until the engine works again', () => {
  const st = newStatus('acc');
  err(st, 'Error: HTTP 401: unauthorized — token missing/expired; run `filex client login`');
  assert.match(pairView(st, 'pair-1').error ?? '', /401/);
  assert.match(pairView(st, 'pair-2').error ?? '', /401/);
  out(st, 'pair-2: already in step');
  assert.equal(pairView(st, 'pair-1').error, null);
  assert.equal(st.lastError, null);
});

test('notes are not errors', () => {
  const st = newStatus('acc');
  err(st, 'pair-1: note: skipped 1 unreadable or non-regular item(s), e.g. link-to-elsewhere');
  assert.equal(pairView(st, 'pair-1').error, null);
  assert.equal(st.lastError, null);
});

test('local watching state is per pair and clears when watching resumes', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: local: poll-only — too-large — more than 4000 items (macOS watches every file separately)');
  assert.deepEqual(pairView(st, 'pair-1').local, {
    code: 'too-large',
    detail: 'more than 4000 items (macOS watches every file separately)',
  });
  assert.equal(pairView(st, 'pair-2').local, null);
  out(st, 'pair-1: local: poll-only — unavailable — too many open files');
  assert.deepEqual(pairView(st, 'pair-1').local, { code: 'unavailable', detail: 'too many open files' });
  out(st, 'pair-1: local: watched');
  assert.equal(pairView(st, 'pair-1').local, null);
  assert.equal(pairView(st, 'pair-1').line, null, 'a state line is not what the engine last DID');
});

// Another process on this computer holds a pair (backend/cmd/filex
// synclock.go): the engine leaves it alone and says so, and takes it over by
// itself when the other process stops. Not an error, and only that pair.
test('a pair another filex syncs is busy — not failing — until this engine takes it', () => {
  const st = newStatus('acc');
  const detail = 'another filex on this computer is syncing this pair (process 4242, C:\\Program Files\\WindowsApps\\filex\\filex.exe)';
  out(st, `pair-1: lock: busy — ${detail}`);
  assert.deepEqual(pairView(st, 'pair-1').busy, { detail });
  assert.equal(pairView(st, 'pair-1').error, null, 'busy is not an error');
  assert.equal(anyError(st), false, 'and does not turn the rail dot red');
  assert.equal(pairView(st, 'pair-2').busy, null, 'only that pair');
  assert.equal(pairView(st, 'pair-1').line, null, 'a state line is not what the engine last DID');

  out(st, 'pair-1: lock: acquired');
  assert.equal(pairView(st, 'pair-1').busy, null);
});

test('a one-shot run says busy on stderr; it is still busy, not an error', () => {
  const st = newStatus('acc');
  err(st, 'pair-1: lock: busy — another filex on this computer is syncing this pair');
  assert.deepEqual(pairView(st, 'pair-1').busy, { detail: 'another filex on this computer is syncing this pair' });
  assert.equal(pairView(st, 'pair-1').error, null);
  assert.equal(st.lastError, null);
});

test('a pass of the pair ends busy even without the acquired line', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: lock: busy — another filex on this computer is syncing this pair');
  out(st, 'pair-1: inventory: 3 item(s) here, listing the server…');
  assert.equal(pairView(st, 'pair-1').busy, null);
  out(st, 'pair-2: lock: busy — another filex on this computer is syncing this pair');
  out(st, 'pair-2: already in step');
  assert.equal(pairView(st, 'pair-2').busy, null);
});

test('a watcher that exits is not waiting for any pair', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: lock: busy — another filex on this computer is syncing this pair');
  markExited(st, 1, false);
  assert.equal(pairView(st, 'pair-1').busy, null);
});

test('each pair shows its own last line', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: transfer: 3/9');
  out(st, 'pair-2: already in step');
  assert.equal(pairView(st, 'pair-1').line, 'transfer: 3/9');
  assert.equal(pairView(st, 'pair-2').line, 'already in step');
});

// A pipe delivers bytes, not lines. A line cut across two reads used to be
// parsed as two nonsense lines — half a summary cannot clear an error, and
// half an error line becomes the error text.
test('a line split across two reads is read whole', () => {
  const st = newStatus('acc');
  err(st, 'pair-1: ! download a.txt: connection reset');
  const r = new LineReader((l) => out(st, l));
  r.push('pair-1: already in st');
  assert.notEqual(pairView(st, 'pair-1').error, null, 'nothing is decided on half a line');
  r.push('ep\npair-2: transfer: 1/');
  assert.equal(pairView(st, 'pair-1').error, null);
  r.push('4\n');
  assert.equal(pairView(st, 'pair-2').line, 'transfer: 1/4');
});

// The engine's live-state lines are the whole contract between
// backend/cmd/filex/synclive.go and the sync panel's state word.
test('parses every state the engine prints', () => {
  assert.deepEqual(parseLiveLine('live: connected — watching 3 folder(s)'), { live: 'connected', detail: 'watching 3 folder(s)' });
  assert.deepEqual(parseLiveLine('live: polling — --live=false; changes are found by the interval poll only'),
    { live: 'polling', detail: '--live=false; changes are found by the interval poll only' });
  assert.deepEqual(parseLiveLine('live: offline — connect: refused; retrying in 4s'), { live: 'offline', detail: 'connect: refused; retrying in 4s' });
  assert.deepEqual(parseLiveLine('  live: connected  '), { live: 'connected', detail: null });
});

// ⚠ Sync progress and results must NOT be read as a state: a pair id that
// happens to be "live" would otherwise flip the word, and a pair's LOCAL
// watching state (LocalNote) is not how server changes arrive.
test('ignores everything else', () => {
  for (const line of [
    'pair-1: transfer: 1/1',
    'pair-1: 1/1 done — 0 up, 1 down, 0 removed here, 0 removed on the server  (10ms)',
    'pair-1: local: poll-only — unavailable — too many open files',
    'live: connectedish',
    'Watching: changes on either side are synced as they happen; a full check every 30s. Ctrl-C to stop.',
  ]) {
    assert.equal(parseLiveLine(line), null, line);
  }
});

// ── carried over from PR #35 (src/sync-output.ts, folded into this module) ──

test('a multi-byte character split between reads survives', () => {
  const lines: string[] = [];
  const r = new LineReader((l) => lines.push(l));
  const bytes = Buffer.from('pair-1: ! upload Türkçe adlı dosya.txt: HTTP 500\n', 'utf8');
  // Cut inside the two-byte "ü" (0xC3 0xBC).
  const cut = bytes.indexOf(0xbc);
  r.push(bytes.subarray(0, cut));
  assert.deepEqual(lines, []);
  r.push(bytes.subarray(cut));
  assert.deepEqual(lines, ['pair-1: ! upload Türkçe adlı dosya.txt: HTTP 500']);
});

test('a Windows line ending is not part of the line, and the tail is read on flush', () => {
  const lines: string[] = [];
  const r = new LineReader((l) => lines.push(l));
  r.push('a\r\nb\r\nlast without newline');
  assert.deepEqual(lines, ['a', 'b']);
  r.flush();
  assert.deepEqual(lines, ['a', 'b', 'last without newline']);
  r.flush();
  assert.equal(lines.length, 3);
});

test('an unexpected exit is said out loud; a requested stop is not an error', () => {
  const a = newStatus('acc');
  markExited(a, 1, false);
  assert.equal(a.running, false);
  assert.equal(a.lastError, 'sync stopped unexpectedly (exit 1)');
  const b = newStatus('acc');
  markExited(b, null, true);
  assert.equal(b.running, false);
  assert.equal(b.lastError, null);
});

test('an unpaired folder takes its state with it', () => {
  const st = newStatus('acc');
  err(st, 'pair-1: list docs://a: HTTP 502');
  err(st, 'pair-2: list docs://b: HTTP 503');
  assert.equal(retainPairs(st, new Set(['pair-1'])), true);
  assert.equal(pairView(st, 'pair-2').error, null);
  assert.equal(pairView(st, 'pair-1').error, 'list docs://a: HTTP 502');
  assert.equal(retainPairs(st, new Set(['pair-1'])), false);
});

// A revoked token used to mean a watcher printing `HTTP 401` every 30 seconds
// forever, and an app that retried it forever — even across reboots. The
// engine now exits with status 3 on a 401 and says so; an older engine that
// keeps looping is recognised by its 401 line.
test('exit status 3 is "signed out", not a crash', () => {
  const st = newStatus('acc');
  markExited(st, SIGNED_OUT_EXIT, false);
  assert.equal(st.signedOut, true);
  assert.equal(st.running, false);
  assert.ok(st.lastError);
});

test('an older engine that keeps looping on 401 is recognised from its output', () => {
  const st = newStatus('acc');
  err(st, 'pair-1: list docs://work: HTTP 401: unauthorized');
  assert.equal(st.signedOut, true);
});

test('…but not from a 403, a 5xx, or a file that happens to be called "HTTP 401"', () => {
  const st = newStatus('acc');
  err(st, 'pair-1: list docs://work: HTTP 403: this account is disabled');
  err(st, 'pair-1: list docs://work: HTTP 502');
  err(st, 'pair-1: ! upload HTTP 401.txt: HTTP 500');
  assert.notEqual(st.signedOut, true);
  markExited(st, 1, false);
  assert.notEqual(st.signedOut, true);
});

// Outside its window the watcher says `sync: waiting for the sync window …`
// once and runs nothing. A pass still busy when the window closes is cancelled
// like Ctrl-C and says `sync: the sync window … closed; …` — with NO summary
// line after it, so that line is what ends the activity. Missing it would leave
// a transfer "active" all day, and the sleep guard holding with it.
test('waiting for the sync window is a state, and it is not activity', () => {
  const st = newStatus('acc');
  out(st, 'sync: waiting for the sync window 22:00-07:00');
  assert.equal(st.waitingWindow, '22:00-07:00');
  assert.equal(st.active, null);
  assert.deepEqual(st.pairs, {}, '"sync" is not a pair');
  out(st, 'pair-1: inventory: 3 item(s) here, listing the server…');
  assert.equal(st.waitingWindow, null);
});

test('a window that closes mid-transfer ends the activity even without a summary', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: transfer: 40/900 (1.2 GiB of 52.6 GiB, about 8h 10m left)');
  assert.equal(st.active?.pairId, 'pair-1');
  out(st, 'sync: the sync window 22:00-07:00 closed; the rest continues when it opens');
  assert.equal(st.active, null);
  assert.equal(st.waitingWindow, '22:00-07:00');
  assert.equal(st.lastError, null, 'a closing window is not an error');
});

// "transfer: 120/11704" said nothing about the nine hours ahead. The engine
// appends `(<done> of <total>, about <eta> left)` — the bytes once there are
// bytes to move, the estimate once it has one — and keeps the leading
// "done/total" exactly as it was.
test('the transfer line carries bytes and an estimate', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: transfer: 120/11704 (1.2 GiB of 52.6 GiB, about 8h 10m left)');
  assert.deepEqual(st.active, {
    pairId: 'pair-1', phase: 'transfer', done: 120, total: 11704,
    bytesDone: '1.2 GiB', bytesTotal: '52.6 GiB', eta: '8h 10m', etaSeconds: 8 * 3600 + 10 * 60,
  });
});

test('…the bytes without an estimate in the first seconds', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: transfer: 3/40 (512 B of 12.0 MiB)');
  assert.deepEqual(st.active, {
    pairId: 'pair-1', phase: 'transfer', done: 3, total: 40, bytesDone: '512 B', bytesTotal: '12.0 MiB',
  });
});

test('…and an older engine\'s bare line still reads as before', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: transfer: 20/300');
  assert.deepEqual(st.active, { pairId: 'pair-1', phase: 'transfer', done: 20, total: 300 });
});

test('the estimate is read into seconds, whatever unit the engine chose', () => {
  assert.equal(parseEta('8h 10m'), 29400);
  assert.equal(parseEta('1h 0m'), 3600);
  assert.equal(parseEta('12m'), 720);
  assert.equal(parseEta('45s'), 45);
  assert.equal(parseEta('soon'), null);
  assert.equal(parseEta(''), null);
});

// A first run that would push a stale mirror's worth of local-only files into a
// server folder with content HOLDS them and waits for a decision. The count the
// notice shows comes from `sync list --json` (hold_new / held); the progress
// line is only the cue to re-read it, so only its number is parsed.
test('a hold line is handed over once, and changes nothing about the activity', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: plan: 150 change(s) to make');
  out(
    st,
    'pair-1: hold: 7 item(s) here are not on the server — waiting for a decision ' +
      '(`filex sync confirm pair-1` sends them, `filex sync discard pair-1` moves them to the local sync trash)',
  );
  assert.equal(st.active?.phase, 'plan');
  assert.deepEqual(takeHolds(st), [{ pairId: 'pair-1', count: 7 }]);
  assert.deepEqual(takeHolds(st), []);
  assert.equal(st.lastError, null);
});

// ── found in the review of PR #35 ──

// The CLI's own exit line (`filex: <err>`, backend/cmd/filex/main.go) is about
// no folder. Read as a pair called `filex`, the reason the watcher died sat
// under a folder nobody has, every real folder said only "stopped", and the
// next reconcile dropped it.
test('the command\'s own exit line is the account\'s error, under every folder', () => {
  const st = newStatus('acc');
  err(st, 'filex: re-read pairs: open pairs.json: permission denied');
  markExited(st, 1, false);
  assert.deepEqual(st.pairs, {}, '"filex" is not a pair');
  assert.equal(pairView(st, 'pair-1').error, 'filex: re-read pairs: open pairs.json: permission denied');
  retainPairs(st, new Set(['pair-1']));
  assert.equal(pairView(st, 'pair-1').error, 'filex: re-read pairs: open pairs.json: permission denied',
    'a reconcile keeps it');
});

// Signing in again starts a new watcher with the new token; the old one's last
// words — a 401 — can arrive after it and must not sign the fixed account out.
test('a replaced watcher\'s refusal no longer speaks for the account', () => {
  const old = newStatus('acc');
  err(old, 'pair-1: list docs://work: HTTP 401: unauthorized');
  assert.equal(old.signedOut, true);
  const fresh = newStatus('acc');
  assert.equal(refusalApplies(old, old), true, 'the current watcher\'s refusal counts');
  assert.equal(refusalApplies(fresh, old), false, 'a replaced watcher\'s does not');
  assert.equal(refusalApplies(fresh, fresh), false, 'a watcher that was not refused says nothing');
});

// A process that writes without ever ending a line must not grow the buffer
// without bound.
test('a line that never ends is handed on once it is too long', () => {
  const lines: string[] = [];
  const r = new LineReader((l) => lines.push(l));
  r.push('x'.repeat(LineReader.MAX_LINE));
  assert.equal(lines.length, 0);
  r.push('y');
  assert.equal(lines.length, 1);
  assert.equal(lines[0].length, LineReader.MAX_LINE + 1);
});

// ── what a pass says while it runs, and whether one has finished (Y11) ──
//
// ⚠ The engine says how far a pass has got in every phase, and only the
// transfer's figures were kept: "listed 312 server folder(s), 48,211 item(s)
// so far" became "listing the server…", minutes on end, with nothing moving.
// And a folder no pass had finished yet read "watching for changes" beside a
// folder that was genuinely in step.

test('every phase keeps its figures', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: inventory: 1204 item(s) here, listing the server…');
  assert.deepEqual(st.active, { pairId: 'pair-1', phase: 'inventory', done: 0, total: 0, here: 1204 });
  out(st, 'pair-1: inventory: listed 312 server folder(s), 48211 item(s) so far');
  assert.deepEqual(st.active, { pairId: 'pair-1', phase: 'inventory', done: 0, total: 0, here: 1204, listed: 48211 });
  out(st, 'pair-1: plan: 97 change(s) to make');
  assert.deepEqual(st.active, { pairId: 'pair-1', phase: 'plan', done: 0, total: 97 });
  out(st, 'pair-1: settling: 40 of 97 change(s) recorded');
  assert.deepEqual(st.active, { pairId: 'pair-1', phase: 'settling', done: 40, total: 97 });
});

test('a pair has passed once a pass of it has finished, and not before', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: inventory: 3 item(s) here, listing the server…');
  assert.equal(pairView(st, 'pair-1').passed, false);
  out(st, 'pair-1: already in step');
  assert.equal(pairView(st, 'pair-1').passed, true);
  out(st, 'pair-2: 4/4 done — 4 uploaded (1.2s)');
  assert.equal(pairView(st, 'pair-2').passed, true);
});

test('an engine that stopped on its own says so as a code, and its pairs are to be checked again', () => {
  const st = newStatus('acc');
  out(st, 'pair-1: already in step');
  markExited(st, 1, false);
  assert.equal(st.exited, '1', 'the page cannot word "sync stopped unexpectedly (exit 1)" in Turkish');
  assert.equal(pairView(st, 'pair-1').passed, false, 'the next engine has not checked it yet');

  const stopped = newStatus('acc');
  markExited(stopped, 0, true);
  assert.equal(stopped.exited ?? null, null, 'a stop the app asked for is not a crash');
});
