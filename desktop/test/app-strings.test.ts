// What the desktop window says about sync, read from the page itself
// (ui/app.html) — its STRINGS table and its own wording functions.
//
// Run:  node --experimental-strip-types --test desktop/test/app-strings.test.ts

import assert from 'node:assert/strict';
import test from 'node:test';

import { appFunctions, appStrings } from './ui-strings.ts';

const page = (lang: 'en' | 'tr') =>
  appFunctions(lang, ['localSize', 'localEta', 'localCount', 'Tn', 'activityLine']) as unknown as {
    activityLine: (v: Record<string, unknown>) => string;
  };

// ⚠ "{n} change(s) to make", "{n} item(s) so far": the window counted in
// brackets. One and many are said as such — the way sync.held_one already
// was — and Turkish, which puts no plural on a noun after a number, needs no
// second form.
test('no "(s)" in what the window says', () => {
  const bracketed = Object.entries(appStrings())
    .filter(([, pair]) => pair.some((s) => s.includes('(s)')))
    .map(([key]) => key);
  assert.deepEqual(bracketed, []);
});

test('a count of one is said in the singular, any other in the plural', () => {
  const en = page('en');
  assert.equal(en.activityLine({ phase: 'plan', done: 0, total: 1 }), '1 change to make');
  assert.equal(en.activityLine({ phase: 'plan', done: 0, total: 1204 }), '1,204 changes to make');
  assert.equal(en.activityLine({ phase: 'inventory', done: 0, total: 0, listed: 1 }), 'listing the server — 1 item so far');
  assert.equal(en.activityLine({ phase: 'inventory', done: 0, total: 0, listed: 7 }), 'listing the server — 7 items so far');
  const tr = page('tr');
  assert.equal(tr.activityLine({ phase: 'plan', done: 0, total: 1 }), '1 değişiklik yapılacak');
  assert.equal(tr.activityLine({ phase: 'plan', done: 0, total: 1204 }), '1.204 değişiklik yapılacak');
});

// ⚠ The engine's first inventory line counts what is here ("1204 item(s) here,
// listing the server…"), and syncstatus.ts parsed it into `here` — which the
// window never showed: a large folder read "listing the server…" with nothing
// moving until the server's own count began.
test('the inventory says what is on this computer, and keeps it while the server is listed', () => {
  const en = page('en');
  const inv = (v: Record<string, unknown>) => ({ phase: 'inventory', done: 0, total: 0, ...v });
  assert.equal(en.activityLine(inv({ here: 1204 })), '1,204 items on this computer — listing the server…');
  assert.equal(en.activityLine(inv({ here: 1 })), '1 item on this computer — listing the server…');
  assert.equal(
    en.activityLine(inv({ here: 1204, listed: 48211 })),
    'listing the server — 48,211 items so far (1,204 on this computer)',
  );
  assert.equal(en.activityLine(inv({})), 'listing the server…');
  const tr = page('tr');
  assert.equal(tr.activityLine(inv({ here: 1204 })), 'bu bilgisayarda 1.204 öğe — sunucu listeleniyor…');
  assert.equal(
    tr.activityLine(inv({ here: 1204, listed: 48211 })),
    'sunucu listeleniyor — şimdiye dek 48.211 öğe (bu bilgisayarda 1.204)',
  );
});
