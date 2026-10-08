// What the desktop window says about sync, read from the page itself
// (ui/app.html) — its STRINGS table and its own functions.
//
// Run:  node --experimental-strip-types --test desktop/test/app-strings.test.ts

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { appStrings } from './ui-strings.ts';

// ⚠ "{n} change(s) to make", "{n} item(s) so far": the window counted in
// brackets. One and many are said as such — the way sync.held_one already
// was.
test('no "(s)" in what the window says', () => {
  const bracketed = Object.entries(appStrings())
    .filter(([, pair]) => pair.some((s) => s.includes('(s)')))
    .map(([key]) => key);
  assert.deepEqual(bracketed, []);
});

// ⚠⚠ #213 (A9): the window worded the engine's reports itself — its phases,
// the bytes and the time left, how changes reach it, a folder another filex
// syncs, the sync window — in two languages of its own, from figures it
// recovered out of the engine's English lines. The engine says each of them
// now (`filex sync run --json --lang …`, backend/cmd/filex/syncevents.go,
// the server catalogue's `server.sync.*`), a language pack's language too,
// and the window shows its sentence as it is. A copy here would be a second
// translation that drifts from the first.
test('the window keeps no wording of the engine\'s reports', () => {
  const keys = Object.keys(appStrings());
  const copies = keys.filter((k) => /^sync\.(live\.|local\.|phase_|eta|busy$|window_wait$)/.test(k));
  assert.deepEqual(copies, []);
  const html = fs.readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'ui', 'app.html'), 'utf8');
  for (const fn of ['activityLine', 'localEta', 'localSize']) {
    assert.equal(html.includes(`function ${fn}(`), false, `ui/app.html still words the engine's reports: ${fn}`);
  }
  // …and shows the engine's own sentences instead.
  assert.match(html, /case 'active': line = v\.message/);
  assert.match(html, /case 'busy': line = v\.message/);
  assert.match(html, /case 'window': line = v\.message/);
  assert.match(html, /st\.liveMessage/);
  assert.match(html, /h\.local\.message/);
});

test('the words the window keeps are its own states, in both languages', () => {
  const s = appStrings();
  for (const k of ['sync.paused', 'sync.signed_out', 'sync.watching', 'sync.pending', 'sync.moving', 'sync.exited', 'sync.no_stream', 'sync.restarting']) {
    assert.ok(s[k], `${k} is missing`);
    assert.ok(s[k][0] && s[k][1], `${k} needs English and Turkish`);
  }
  assert.match(s['sync.no_stream'][1], /eşitleme motoru başlatılamadı/);
});
