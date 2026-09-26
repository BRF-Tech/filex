// Is this response the file? — the rule every desktop download goes through.
//
// Run:  node --experimental-strip-types --test desktop/test/download-guard.test.ts
//
// The failure this exists for: a 202 "preparing" JSON body written to disk as
// the file (v0.20–v0.42 servers, big file, slow storage). It must never pass.

import assert from 'node:assert/strict';
import test from 'node:test';

import { WHOLE_FILE_RANGE, wholeFileVerdict, DownloadTally, downloadEnding } from '../src/download-guard.ts';

test('every download asks for the whole range', () => {
  assert.equal(WHOLE_FILE_RANGE, 'bytes=0-');
});

test('a 200 is the whole object', () => {
  assert.deepEqual(wholeFileVerdict(200), { ok: true });
});

test('a 206 covering the whole object is the file', () => {
  assert.deepEqual(wholeFileVerdict(206, 'bytes 0-1023/1024'), { ok: true });
  assert.deepEqual(wholeFileVerdict(206, ['bytes 0-0/1']), { ok: true });
});

test('a 202 "preparing" answer is never the file', () => {
  const v = wholeFileVerdict(202);
  assert.equal(v.ok, false);
  assert.match((v as { reason: string }).reason, /preparing/);
});

test('a partial or offset 206 is not the file', () => {
  assert.equal(wholeFileVerdict(206, 'bytes 0-9/100').ok, false);
  assert.equal(wholeFileVerdict(206, 'bytes 5-14/15').ok, false);
  assert.equal(wholeFileVerdict(206, 'bytes 0-9/*').ok, false);
  assert.equal(wholeFileVerdict(206).ok, false);
});

test('any other status is refused with the status in the reason', () => {
  for (const s of [201, 203, 204, 301, 401, 404, 500]) {
    const v = wholeFileVerdict(s);
    assert.equal(v.ok, false, String(s));
    assert.match((v as { reason: string }).reason, new RegExp(String(s)));
  }
});

// ── a download to disk says how far it has got, and how it ended (Y10) ──
//
// ⚠ A download to disk (the explorer's Download, ⌘K) went through the
// window's own download and nothing listened to it: no progress, no end, and
// a failure said nothing at all.

test('the progress bar adds every download up, by bytes', () => {
  const t = new DownloadTally();
  assert.equal(t.fraction(), -1, 'no download, no bar');
  t.update('a', 25, 100);
  assert.equal(t.fraction(), 0.25);
  t.update('b', 75, 100);
  assert.equal(t.fraction(), 0.5, 'two downloads: 100 of 200 bytes');
  t.finish('a');
  assert.equal(t.fraction(), 0.75, 'a finished download leaves the sum');
  t.finish('b');
  assert.equal(t.fraction(), -1);
});

test('a download of unknown size shows a moving bar, not 0%', () => {
  const t = new DownloadTally();
  t.update('a', 5_000, 0);
  assert.equal(t.fraction(), 2, 'Electron reads a value above 1 as "indeterminate"');
});

test('how a download ended decides what is said', () => {
  assert.equal(downloadEnding('completed'), 'done');
  assert.equal(downloadEnding('interrupted'), 'failed');
  assert.equal(downloadEnding('cancelled'), null, 'the person cancelled it: nothing to say');
});
