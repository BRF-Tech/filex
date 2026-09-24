// Is this response the file? — the rule every desktop download goes through.
//
// Run:  node --experimental-strip-types --test desktop/test/download-guard.test.ts
//
// The failure this exists for: a 202 "preparing" JSON body written to disk as
// the file (v0.20–v0.42 servers, big file, slow storage). It must never pass.

import assert from 'node:assert/strict';
import test from 'node:test';

import { WHOLE_FILE_RANGE, wholeFileVerdict } from '../src/download-guard.ts';

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
