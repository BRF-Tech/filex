// Is this response the file? — the rule every desktop download goes through.
//
// Run:  node --experimental-strip-types --test desktop/test/download-guard.test.ts
//
// The failure this exists for: a 202 "preparing" JSON body written to disk as
// the file (v0.20–v0.42 servers, big file, slow storage). It must never pass.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { Readable } from 'node:stream';
import test from 'node:test';

import {
  WHOLE_FILE_RANGE,
  wholeFileVerdict,
  DownloadTally,
  downloadEnding,
  landViaPart,
} from '../src/download-guard.ts';

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

// ── a file on its way into a dropped folder can be stopped (Y11) ──
//
// ⚠ The drop's Stop was only looked at BETWEEN two files, and the download
// itself had no way to be stopped: dropping one big file, Stop did nothing at
// all, and inside a folder nothing changed until the file in flight had come
// down — minutes, for a large one. Every download to disk lands through here.

function tmpDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'filex-land-'));
}

/** A download that sends one chunk and then hangs, the way a big file does
 *  for as long as it takes. */
function hangingBody(): Readable & { destroyedByUs: () => boolean } {
  let destroyed = false;
  const r = new Readable({
    read() {
      /* nothing more, until destroyed */
    },
    destroy(err, cb) {
      destroyed = true;
      cb(err);
    },
  });
  r.push(Buffer.alloc(64 * 1024, 1));
  return Object.assign(r, { destroyedByUs: () => destroyed });
}

test('a file lands whole under its name, with no part file left beside it', async () => {
  const dir = tmpDir();
  try {
    const dest = path.join(dir, 'Rapor.pdf');
    const size = await landViaPart(Readable.from([Buffer.from('abc'), Buffer.from('def')]), dest, { suffix: '.filexpart' });
    assert.equal(size, 6);
    assert.equal(fs.readFileSync(dest, 'utf8'), 'abcdef');
    assert.deepEqual(fs.readdirSync(dir), ['Rapor.pdf']);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('Stop in the middle of a file ends it at once: the part file goes and nothing wears the name', async () => {
  const dir = tmpDir();
  try {
    const dest = path.join(dir, 'Büyük dosya.iso');
    const body = hangingBody();
    const ctrl = new AbortController();
    const landing = landViaPart(body, dest, { suffix: '.filexpart', signal: ctrl.signal });
    // Bytes are arriving: the part file is there.
    for (let i = 0; i < 100 && !fs.existsSync(`${dest}.filexpart`); i++) await new Promise((r) => setTimeout(r, 10));
    assert.ok(fs.existsSync(`${dest}.filexpart`), 'the download started');
    ctrl.abort();
    const outcome = await Promise.race([
      landing.then(() => 'landed', (e: Error) => e.message),
      new Promise((r) => setTimeout(() => r('still running 2 s after Stop'), 2000)),
    ]);
    assert.equal(outcome, 'cancelled');
    assert.ok(body.destroyedByUs(), 'the download itself was not ended');
    assert.deepEqual(fs.readdirSync(dir), [], 'a half file was left in the folder');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('a download that fails halfway leaves nothing behind, and says why', async () => {
  const dir = tmpDir();
  try {
    const dest = path.join(dir, 'a.bin');
    const body = new Readable({ read() {} });
    body.push(Buffer.from('half'));
    setTimeout(() => body.destroy(new Error('connection reset')), 20);
    await assert.rejects(landViaPart(body, dest, { suffix: '.filexpart' }), /connection reset/);
    assert.deepEqual(fs.readdirSync(dir), []);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('a Stop that came before the first byte fetches nothing', async () => {
  const dir = tmpDir();
  try {
    const ctrl = new AbortController();
    ctrl.abort();
    const dest = path.join(dir, 'a.bin');
    await assert.rejects(landViaPart(hangingBody(), dest, { suffix: '.part', signal: ctrl.signal }), /cancelled/);
    assert.deepEqual(fs.readdirSync(dir), []);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
