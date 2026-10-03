// When store-e2e's activation checks may only warn (scripts/lib/store-activation.mjs).
//
// Run:  node --experimental-strip-types --test desktop/test/store-activation.test.ts
//
// ⚠ The rule is narrow on purpose: a warning only on CI AND only while nothing
// goes to the Microsoft Store. On a developer machine, or on a run that uploads
// the package, a failed activation is a failed release.
//
// The two window-watch checks ("no window came on screen") are the one
// exception to the second half: they guard a person's screen, so on CI they
// only warn even when the package is uploaded — and on a desktop they fail.

import assert from 'node:assert/strict';
import test from 'node:test';

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { activationWaitFactor, softActivation, softWindowChecks, windowVerdicts } from '../scripts/lib/store-activation.mjs';

test('a developer machine never softens a failure', () => {
  assert.equal(softActivation({}), false);
  assert.equal(softActivation({ STORE_E2E_STRICT: '0' }), false);
  assert.equal(softActivation({ CI: 'false' }), false);
});

test('CI without a Store upload warns instead of failing', () => {
  assert.equal(softActivation({ CI: 'true' }), true);
  assert.equal(softActivation({ CI: 'true', STORE_E2E_STRICT: '0' }), true);
  assert.equal(softActivation({ CI: 'true', STORE_E2E_STRICT: '' }), true);
});

test('CI that uploads to the Store keeps every activation check a failure', () => {
  assert.equal(softActivation({ CI: 'true', STORE_E2E_STRICT: '1' }), false);
});

test('CI waits longer for a cold runner, a desktop does not', () => {
  assert.equal(activationWaitFactor({}), 1);
  assert.equal(activationWaitFactor({ CI: 'true' }), 3);
});

// ── the window watch: a person's screen, not a runner's ────────────────────

const appWindow = { at: '14:18:32.343', kind: 'hidden', pid: '46776', cls: 'Chrome_WidgetWin_1', size: '720x620', cloaked: '0', title: 'filex - Connect' };
const shownWindow = { ...appWindow, kind: 'visible', onScreen: true };
const clean = { ran: true, error: '', onScreen: [], hidden: [appWindow] };
const watchFailed = { ran: false, error: 'Add-Type : Cannot add type. Compilation errors occurred.', onScreen: [], hidden: [] };

test('on CI a watch that could not run only warns, both checks', () => {
  for (const env of [{ CI: 'true' }, { GITHUB_ACTIONS: 'true' }, { CI: 'true', GITHUB_ACTIONS: 'true' }]) {
    const v = windowVerdicts(watchFailed, env);
    assert.equal(v.length, 2);
    for (const x of v) {
      assert.equal(x.ok, false, x.name);
      assert.equal(x.soft, true, `${x.name} must only warn on CI (${JSON.stringify(env)})`);
      assert.match(x.detail, /did not run to the end: Add-Type/);
    }
  }
});

test('on CI even a window on the screen only warns — nobody is looking at it', () => {
  const [onScreen] = windowVerdicts({ ...clean, onScreen: [shownWindow] }, { CI: 'true' });
  assert.equal(onScreen.ok, false);
  assert.equal(onScreen.soft, true);
});

test('a Store upload on CI does not make the screen checks strict', () => {
  assert.equal(softWindowChecks({ CI: 'true', STORE_E2E_STRICT: '1' }), true);
  // …while the activation checks do turn strict there, as before.
  assert.equal(softActivation({ CI: 'true', STORE_E2E_STRICT: '1' }), false);
});

test('on a developer machine (the release pretag) both stay failures', () => {
  for (const env of [{}, { CI: 'false' }, { GITHUB_ACTIONS: '' }, { STORE_E2E_STRICT: '0' }]) {
    assert.equal(softWindowChecks(env), false, JSON.stringify(env));
    for (const x of windowVerdicts(watchFailed, env)) {
      assert.equal(x.ok, false);
      assert.equal(x.soft, false, `${x.name} must fail on a desktop (${JSON.stringify(env)})`);
    }
  }
  const [onScreen] = windowVerdicts({ ...clean, onScreen: [shownWindow] }, {});
  assert.equal(onScreen.ok, false);
  assert.equal(onScreen.soft, false);
  assert.match(onScreen.detail, /visible Chrome_WidgetWin_1 720x620 "filex - Connect"/);
});

test('a clean watch passes both, and a watch that saw no app window proves nothing', () => {
  assert.deepEqual(windowVerdicts(clean, {}).map((x) => x.ok), [true, true]);
  const blind = windowVerdicts({ ...clean, hidden: [{ ...appWindow, title: 'Default IME' }] }, {});
  assert.deepEqual(blind.map((x) => x.ok), [true, false]);
  assert.equal(blind[1].detail, 'no window of the app was seen at all');
});

test('store-e2e reports the window watch through these verdicts', () => {
  const dir = path.dirname(fileURLToPath(import.meta.url));
  const src = fs.readFileSync(path.join(dir, '..', 'scripts', 'store-e2e.mjs'), 'utf8');
  assert.match(src, /for \(const v of windowVerdicts\(seen, process\.env\)\) \{\s*softCheck\(v\.name, v\.ok, v\.detail, v\.soft,/);
  // …and nowhere else: no second, strict copy of either check.
  assert.equal(src.match(/no window of the Store copy came on screen/g), null);
  assert.equal(src.match(/kept off screen'/g), null);
});
