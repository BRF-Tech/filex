// When store-e2e's activation checks may only warn (scripts/lib/store-activation.mjs).
//
// Run:  node --experimental-strip-types --test desktop/test/store-activation.test.ts
//
// ⚠ The rule is narrow on purpose: a warning only on CI AND only while nothing
// goes to the Microsoft Store. On a developer machine, or on a run that uploads
// the package, a failed activation is a failed release.

import assert from 'node:assert/strict';
import test from 'node:test';

import { activationWaitFactor, softActivation } from '../scripts/lib/store-activation.mjs';

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

test('CI that uploads to the Store keeps every check a failure', () => {
  assert.equal(softActivation({ CI: 'true', STORE_E2E_STRICT: '1' }), false);
});

test('CI waits longer for a cold runner, a desktop does not', () => {
  assert.equal(activationWaitFactor({}), 1);
  assert.equal(activationWaitFactor({ CI: 'true' }), 3);
});
