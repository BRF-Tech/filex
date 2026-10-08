import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  absorbEvent,
  absorbStderr,
  absorbStdout,
  anyError,
  ENGINE_PROTOCOL,
  LineReader,
  markExited,
  newStatus,
  pairView,
  parseEvent,
  refusalApplies,
  retainPairs,
  SIGNED_OUT_EXIT,
  takeHolds,
  type EngineEvent,
} from '../src/syncstatus.ts';

// The engine's events → what the sync card says, pair by pair.
//
// ⚠⚠ #213 (A9): until 0.54 this module read the engine's ENGLISH lines with
// regular expressions — a sentence reworded in Go silently broke the status
// under a folder, and the engine's errors reached a Turkish window in
// English. The engine (`filex sync run --json`) now writes one JSON event per
// line, its `message` said in the app's language; these tests feed events,
// the shape backend/cmd/filex/syncevents.go writes.
//
// Before these rules an error was copied into ONE account-wide field and
// never cleared: with the live engine running a pass every few seconds, a
// failure from minutes ago sat under every synced folder of the account and
// read as a current one.

type Ev = Partial<EngineEvent> & { event: string };
const ev = (e: Ev): EngineEvent => ({ code: e.event, params: {}, message: '', ...e });
const feed = (st: ReturnType<typeof newStatus>, e: Ev) => absorbStdout(st, JSON.stringify(ev(e)));

const pass = (pair: string, failed = 0, message = failed ? 'a pass with failures' : 'already in step') =>
  ({ event: 'pass', pair, code: failed ? 'pass.done_failed' : 'pass.in_step', params: { failed }, message });
const error = (pair: string | undefined, message: string, code = 'pass.action_failed') =>
  ({ event: 'error', pair, code, params: { error: message }, message });

test('the engine is started for the event stream, and the app reads no English line', () => {
  // The source no longer carries a pattern of the engine's sentences.
  const src = readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'syncstatus.ts'), 'utf8');
  for (const gone of ['already in step$', 'lock: (?:', 'local: (?:', 'item\\(s\\) here', 'change\\(s\\) to make', 'waiting for the sync window', 'HTTP 401(?=']) {
    assert.equal(src.includes(gone), false, `syncstatus.ts still matches the engine's words: ${gone}`);
  }
  assert.equal(ENGINE_PROTOCOL, 1);
});

test('a stdout line is an event or nothing', () => {
  assert.deepEqual(parseEvent('{"event":"pass","pair":"pair-1","code":"pass.in_step","params":{"failed":0},"message":"zaten eşitlenmiş"}'), {
    event: 'pass', pair: 'pair-1', code: 'pass.in_step', params: { failed: 0 }, message: 'zaten eşitlenmiş',
  });
  for (const not of ['', 'pair-1: already in step', '{', '{"code":"x"}', '[1,2]', 'null', '{"event":1,"code":"x"}']) {
    assert.equal(parseEvent(not), null, not);
  }
  const st = newStatus('acc');
  absorbStdout(st, 'Watching: changes on either side are synced as they happen');
  assert.deepEqual(st.pairs, {}, 'a plain line changes nothing');
  assert.equal(st.events, 0);
});

test('hello says which stream the engine speaks', () => {
  const st = newStatus('acc');
  feed(st, { event: 'hello', params: { protocol: 1, version: '0.54.0', lang: 'tr' } });
  assert.equal(st.protocol, 1);
});

test('a pair\'s error is cleared by its next clean pass', () => {
  const st = newStatus('acc');
  feed(st, pass('pair-1', 1));
  feed(st, error('pair-1', 'Bir öğe eşitlenemedi: download a.txt: connection reset'));
  assert.equal(pairView(st, 'pair-1').error, 'Bir öğe eşitlenemedi: download a.txt: connection reset');

  feed(st, pass('pair-1'));
  assert.equal(pairView(st, 'pair-1').error, null, 'a clean pass must clear the pair\'s error');
  assert.equal(st.lastError, null, 'and nothing account-wide may keep it');

  feed(st, error('pair-1', 'Bu klasör eşitlenemedi: list docs://work: HTTP 502', 'pass.failed'));
  assert.equal(pairView(st, 'pair-1').error, 'Bu klasör eşitlenemedi: list docs://work: HTTP 502');
  feed(st, pass('pair-1'));
  assert.equal(pairView(st, 'pair-1').error, null);
});

test('an error for one pair never shows on another', () => {
  const st = newStatus('acc');
  feed(st, error('pair-1', 'An item could not be synced: upload big.bin: HTTP 413: quota exceeded'));
  assert.equal(pairView(st, 'pair-2').error, null);
  assert.ok(pairView(st, 'pair-1').error?.includes('quota exceeded'));
});

// One ordered stream: the summary comes first, its errors right after it.
// (Two pipes read in no guaranteed order used to need a verdict in the
// summary itself; the verdict is still there, as params.failed.)
test('a pass with failures keeps the error that follows it, and a later one too', () => {
  const st = newStatus('acc');
  feed(st, error('pair-1', 'old'));
  feed(st, pass('pair-1', 1));
  assert.equal(pairView(st, 'pair-1').error, 'old', 'a failed pass does not clear');
  feed(st, error('pair-1', 'new'));
  assert.equal(pairView(st, 'pair-1').error, 'new');
});

test('notes are not errors', () => {
  const st = newStatus('acc');
  feed(st, { event: 'note', pair: 'pair-1', code: 'pass.skipped', params: { count: 1 }, message: '1 unreadable or special item was skipped: link' });
  feed(st, { event: 'note', pair: 'pair-1', code: 'pass.raced', message: 'Changed on both sides…' });
  assert.equal(pairView(st, 'pair-1').error, null);
  assert.equal(st.lastError, null);
});

test('an error that is about no pair shows on every pair until the engine works again', () => {
  const st = newStatus('acc');
  feed(st, error(undefined, 'The list of synced folders could not be read: permission denied', 'pairs_unreadable'));
  assert.match(pairView(st, 'pair-1').error ?? '', /permission denied/);
  assert.match(pairView(st, 'pair-2').error ?? '', /permission denied/);
  feed(st, pass('pair-2'));
  assert.equal(pairView(st, 'pair-1').error, null);
  assert.equal(st.lastError, null);
});

test('local watching state is per pair, in the engine\'s words, and clears when watching resumes', () => {
  const st = newStatus('acc');
  const tooLarge = 'Bu bilgisayarda yapılan değişiklikler 30 saniyelik kontrolde bulunur - klasörde izlenemeyecek kadar çok öğe var';
  feed(st, { event: 'local', pair: 'pair-1', code: 'local.too_large', params: { state: 'too-large', detail: 'more than 4000 items' }, message: tooLarge });
  assert.deepEqual(pairView(st, 'pair-1').local, { code: 'too-large', detail: 'more than 4000 items', message: tooLarge });
  assert.equal(pairView(st, 'pair-2').local, null);
  feed(st, { event: 'local', pair: 'pair-1', code: 'local.unavailable', params: { state: 'unavailable', detail: 'too many open files' }, message: 'x' });
  assert.deepEqual(pairView(st, 'pair-1').local, { code: 'unavailable', detail: 'too many open files', message: 'x' });
  feed(st, { event: 'local', pair: 'pair-1', code: 'local.watched', params: { state: 'watched' }, message: 'y' });
  assert.equal(pairView(st, 'pair-1').local, null);
  assert.equal(pairView(st, 'pair-1').line, null, 'a state is not what the engine last DID');
});

// Another process on this computer holds a pair (backend/cmd/filex
// synclock.go): the engine leaves it alone and says so, and takes it over by
// itself when the other process stops. Not an error, and only that pair.
test('a pair another filex syncs is busy — not failing — until this engine takes it', () => {
  const st = newStatus('acc');
  const detail = 'another filex on this computer is syncing this pair (process 4242, C:\\Program Files\\WindowsApps\\filex\\filex.exe)';
  const message = 'Another filex on this computer is syncing this folder - this copy takes over when that one stops';
  feed(st, { event: 'lock', pair: 'pair-1', code: 'lock.busy', params: { state: 'busy', detail }, message });
  assert.deepEqual(pairView(st, 'pair-1').busy, { detail, message });
  assert.equal(pairView(st, 'pair-1').error, null, 'busy is not an error');
  assert.equal(anyError(st), false, 'and does not turn the rail dot red');
  assert.equal(pairView(st, 'pair-2').busy, null, 'only that pair');
  assert.equal(pairView(st, 'pair-1').line, null, 'a state is not what the engine last DID');

  feed(st, { event: 'lock', pair: 'pair-1', code: 'lock.acquired', params: { state: 'acquired' }, message: 'z' });
  assert.equal(pairView(st, 'pair-1').busy, null);
});

test('a pass of the pair ends busy even without lock.acquired', () => {
  const st = newStatus('acc');
  feed(st, { event: 'lock', pair: 'pair-1', code: 'lock.busy', params: { detail: 'x' }, message: 'm' });
  feed(st, { event: 'progress', pair: 'pair-1', code: 'progress.inventory', params: { phase: 'inventory', here: 3 }, message: '3 items' });
  assert.equal(pairView(st, 'pair-1').busy, null);
  feed(st, { event: 'lock', pair: 'pair-2', code: 'lock.busy', params: { detail: 'x' }, message: 'm' });
  feed(st, pass('pair-2'));
  assert.equal(pairView(st, 'pair-2').busy, null);
});

test('a watcher that exits is not waiting for any pair', () => {
  const st = newStatus('acc');
  feed(st, { event: 'lock', pair: 'pair-1', code: 'lock.busy', params: { detail: 'x' }, message: 'm' });
  markExited(st, 1, false);
  assert.equal(pairView(st, 'pair-1').busy, null);
});

test('each pair shows its own last line, in the engine\'s words', () => {
  const st = newStatus('acc');
  feed(st, { event: 'progress', pair: 'pair-1', code: 'progress.transfer', params: { phase: 'transfer', done: 3, total: 9 }, message: 'dosyalar aktarılıyor - 3/9' });
  feed(st, pass('pair-2', 0, 'zaten eşitlenmiş'));
  assert.equal(pairView(st, 'pair-1').line, 'dosyalar aktarılıyor - 3/9');
  assert.equal(pairView(st, 'pair-2').line, 'zaten eşitlenmiş');
});

// A pipe delivers bytes, not lines. Half an event is no JSON at all.
test('an event split across two reads is read whole', () => {
  const st = newStatus('acc');
  feed(st, error('pair-1', 'boom'));
  const r = new LineReader((l) => absorbStdout(st, l));
  const line = JSON.stringify(ev(pass('pair-1')));
  r.push(line.slice(0, 20));
  assert.equal(pairView(st, 'pair-1').error, 'boom', 'nothing is decided on half a line');
  r.push(line.slice(20) + '\n');
  assert.equal(pairView(st, 'pair-1').error, null);
});

test('the live state is the engine\'s, with its sentence', () => {
  const st = newStatus('acc');
  feed(st, { event: 'live', code: 'live.connected', params: { state: 'connected', detail: 'watching 3 folder(s)' }, message: 'Canlı - değişiklikler anında geliyor' });
  assert.equal(st.live, 'connected');
  assert.equal(st.liveMessage, 'Canlı - değişiklikler anında geliyor');
  feed(st, { event: 'live', code: 'live.unknown', params: { state: 'teleporting' }, message: '?' });
  assert.equal(st.live, 'connected', 'a state this app does not know changes nothing');
  assert.deepEqual(st.pairs, {}, 'live is about no pair');
  markExited(st, 0, true);
  assert.equal(st.live, null);
  assert.equal(st.liveMessage, null);
});

test('a multi-byte character split between reads survives', () => {
  const lines: string[] = [];
  const r = new LineReader((l) => lines.push(l));
  const bytes = Buffer.from('{"event":"error","pair":"pair-1","code":"pass.action_failed","message":"Bir öğe eşitlenemedi"}\n', 'utf8');
  // Cut inside the two-byte "ö" (0xC3 0xB6).
  const cut = bytes.indexOf(0xb6);
  r.push(bytes.subarray(0, cut));
  assert.deepEqual(lines, []);
  r.push(bytes.subarray(cut));
  assert.equal(parseEvent(lines[0])?.message, 'Bir öğe eşitlenemedi');
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
  feed(st, error('pair-1', 'list docs://a: HTTP 502', 'pass.failed'));
  feed(st, error('pair-2', 'list docs://b: HTTP 503', 'pass.failed'));
  assert.equal(retainPairs(st, new Set(['pair-1'])), true);
  assert.equal(pairView(st, 'pair-2').error, null);
  assert.equal(pairView(st, 'pair-1').error, 'list docs://a: HTTP 502');
  assert.equal(retainPairs(st, new Set(['pair-1'])), false);
});

// A revoked token used to mean a watcher printing `HTTP 401` every 30 seconds
// forever. The engine exits with status 3 and says `signed_out` first.
test('signed_out and exit status 3 are "signed out", not a crash', () => {
  const st = newStatus('acc');
  feed(st, { event: 'fatal', code: 'signed_out', params: { exit: 3 }, message: 'Sunucu bu oturumu artık kabul etmiyor' });
  assert.equal(st.signedOut, true);
  assert.equal(st.lastError, 'Sunucu bu oturumu artık kabul etmiyor', 'in the engine\'s words');
  markExited(st, SIGNED_OUT_EXIT, false);
  assert.equal(st.running, false);
  assert.equal(st.exited ?? null, null);

  const bare = newStatus('acc');
  markExited(bare, SIGNED_OUT_EXIT, false);
  assert.equal(bare.signedOut, true, 'the status alone is enough');
  assert.ok(bare.lastError);
});

test('…and no other failure is: a 403, a 5xx, a file called "HTTP 401"', () => {
  const st = newStatus('acc');
  feed(st, error('pair-1', 'list docs://work: HTTP 403: this account is disabled', 'pass.failed'));
  feed(st, error('pair-1', 'upload HTTP 401.txt: HTTP 500'));
  feed(st, { event: 'fatal', code: 'fatal', params: { exit: 1 }, message: 'The sync engine stopped: x' });
  assert.notEqual(st.signedOut, true);
  markExited(st, 1, false);
  assert.notEqual(st.signedOut, true);
});

// Outside its window the watcher says `window.waiting` once and runs nothing.
// A pass still busy when the window closes is cancelled like Ctrl-C and says
// `window.closed` — with NO pass event after it, so that event is what ends
// the activity. Missing it would leave a transfer "active" all day, and the
// sleep guard holding with it.
test('waiting for the sync window is a state, it is not activity, and the engine says when it opens', () => {
  const st = newStatus('acc');
  feed(st, {
    event: 'window', code: 'window.waiting',
    params: { window: '22:00-07:00', opens_at: '2026-10-08T22:00:00+03:00' },
    message: 'eşitleme saatleri bekleniyor, 22:00-07:00',
  });
  assert.deepEqual(st.waitingWindow, {
    window: '22:00-07:00', opensAt: Date.parse('2026-10-08T22:00:00+03:00'), message: 'eşitleme saatleri bekleniyor, 22:00-07:00',
  });
  assert.equal(st.active, null);
  assert.deepEqual(st.pairs, {}, 'a window is about no pair');
  feed(st, { event: 'progress', pair: 'pair-1', code: 'progress.inventory', params: { phase: 'inventory', here: 3 }, message: '…' });
  assert.equal(st.waitingWindow, null);
});

test('a window that closes mid-transfer ends the activity even without a pass', () => {
  const st = newStatus('acc');
  feed(st, { event: 'progress', pair: 'pair-1', code: 'progress.transfer_eta', params: { phase: 'transfer', done: 40, total: 900 }, message: 'm' });
  assert.equal(st.active?.pairId, 'pair-1');
  feed(st, { event: 'window', code: 'window.closed', params: { window: '22:00-07:00', opens_at: 'not a time' }, message: 'closed' });
  assert.equal(st.active, null);
  assert.equal(st.waitingWindow?.window, '22:00-07:00');
  assert.equal(st.waitingWindow?.opensAt, null, 'an opening the engine did not say is not guessed here');
  assert.equal(st.lastError, null, 'a closing window is not an error');
});

// "transfer: 120/11704" said nothing about the nine hours ahead: the event
// carries the bytes and the estimate as numbers, and the engine's sentence
// says them in the reader's language.
test('the transfer carries its figures and its sentence', () => {
  const st = newStatus('acc');
  const message = 'moving files - 120/11,704, 1.2 GB of 52.6 GB - about 8 h 10 min left';
  feed(st, {
    event: 'progress', pair: 'pair-1', code: 'progress.transfer_eta',
    params: { phase: 'transfer', done: 120, total: 11704, bytes_done: 1_200_000_000, bytes_total: 52_600_000_000, eta_seconds: 29400 },
    message,
  });
  assert.deepEqual(st.active, {
    pairId: 'pair-1', phase: 'transfer', done: 120, total: 11704,
    bytesDone: 1_200_000_000, bytesTotal: 52_600_000_000, etaSeconds: 29400, message,
  });
});

test('every phase keeps its figures', () => {
  const st = newStatus('acc');
  feed(st, { event: 'progress', pair: 'pair-1', params: { phase: 'inventory', here: 1204 }, message: 'a' });
  assert.deepEqual(st.active, { pairId: 'pair-1', phase: 'inventory', done: 0, total: 0, here: 1204, message: 'a' });
  feed(st, { event: 'progress', pair: 'pair-1', params: { phase: 'inventory', here: 1204, listed: 48211, folders: 312 }, message: 'b' });
  assert.deepEqual(st.active, { pairId: 'pair-1', phase: 'inventory', done: 0, total: 0, here: 1204, listed: 48211, message: 'b' });
  feed(st, { event: 'progress', pair: 'pair-1', params: { phase: 'plan', total: 97 }, message: 'c' });
  assert.deepEqual(st.active, { pairId: 'pair-1', phase: 'plan', done: 0, total: 97, message: 'c' });
  feed(st, { event: 'progress', pair: 'pair-1', params: { phase: 'settling', done: 40, total: 97 }, message: 'd' });
  assert.deepEqual(st.active, { pairId: 'pair-1', phase: 'settling', done: 40, total: 97, message: 'd' });
  feed(st, { event: 'progress', pair: 'pair-1', params: { phase: 'warp' }, message: 'e' });
  assert.equal(st.active?.phase, 'settling', 'a phase this app does not know changes nothing');
});

// A first run that would push a stale mirror's worth of local-only files into a
// server folder with content HOLDS them and waits for a decision. The count the
// notice shows comes from `sync list --json` (hold_new / held); the event is
// only the cue to re-read it.
test('a hold is handed over once, and changes nothing about the activity', () => {
  const st = newStatus('acc');
  feed(st, { event: 'progress', pair: 'pair-1', params: { phase: 'plan', total: 150 }, message: '150 changes to make' });
  feed(st, { event: 'hold', pair: 'pair-1', code: 'hold', params: { count: 7 }, message: '7 items…' });
  assert.equal(st.active?.phase, 'plan');
  assert.deepEqual(takeHolds(st), [{ pairId: 'pair-1', count: 7 }]);
  assert.deepEqual(takeHolds(st), []);
  assert.equal(st.lastError, null);
});

// stderr is not the engine's stream: what lands there is a panic or the
// refusal of an engine that cannot speak it, and that is the account's error.
test('stderr is the account\'s error, as it is; an engine that never spoke the stream is marked', () => {
  const st = newStatus('acc');
  absorbStderr(st, 'Error: unknown flag: --json');
  absorbStderr(st, '   ');
  assert.equal(st.lastError, 'Error: unknown flag: --json');
  assert.deepEqual(st.pairs, {});
  retainPairs(st, new Set(['pair-1']));
  assert.equal(pairView(st, 'pair-1').error, 'Error: unknown flag: --json', 'under every folder');
  markExited(st, 1, false);
  assert.equal(st.noStream, true);

  const spoke = newStatus('acc');
  feed(spoke, { event: 'hello', params: { protocol: 1 } });
  markExited(spoke, 1, false);
  assert.notEqual(spoke.noStream, true, 'an engine that spoke the stream crashed; it is not too old');
});

// Signing in again starts a new watcher with the new token; the old one's last
// words — a 401 — can arrive after it and must not sign the fixed account out.
test('a replaced watcher\'s refusal no longer speaks for the account', () => {
  const old = newStatus('acc');
  feed(old, { event: 'fatal', code: 'signed_out', params: { exit: 3 }, message: 'x' });
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

test('a pair has passed once a pass of it has finished, and not before', () => {
  const st = newStatus('acc');
  feed(st, { event: 'progress', pair: 'pair-1', params: { phase: 'inventory', here: 3 }, message: 'a' });
  assert.equal(pairView(st, 'pair-1').passed, false);
  feed(st, pass('pair-1'));
  assert.equal(pairView(st, 'pair-1').passed, true);
});

test('an engine that stopped on its own says so as a code, and its pairs are to be checked again', () => {
  const st = newStatus('acc');
  feed(st, pass('pair-1'));
  markExited(st, 1, false);
  assert.equal(st.exited, '1', 'the page cannot word "sync stopped unexpectedly (exit 1)" in Turkish');
  assert.equal(pairView(st, 'pair-1').passed, false, 'the next engine has not checked it yet');

  const stopped = newStatus('acc');
  markExited(stopped, 0, true);
  assert.equal(stopped.exited ?? null, null, 'a stop the app asked for is not a crash');
});

test('absorbEvent counts what the engine said', () => {
  const st = newStatus('acc');
  absorbEvent(st, ev({ event: 'note', pair: 'pair-1' }));
  absorbEvent(st, ev({ event: 'something-new' }));
  assert.equal(st.events, 2, 'an event a newer engine adds is still an event');
});
