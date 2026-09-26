// The main process's own two-language strings: tray, native dialogs,
// notifications.
//
// Run:  node --experimental-strip-types --test desktop/test/strings.test.ts

import assert from 'node:assert/strict';
import test from 'node:test';

import { bilingual } from '../src/strings.ts';

const TABLE = {
  moved: ['Moved {name} to {where}', '{name}, {where} konumuna taşındı'],
  twice: ['{n} of {n}', '{n} / {n}'],
} as const;

// ⚠ This was written four times in main.ts — trayText, syncText, openText and
// a downloadText that PR #68 added — one per table, identical but for it.
test('one string, in the chosen language, with its placeholders filled', () => {
  assert.equal(bilingual(TABLE, 'moved', 'en', { name: 'a.txt', where: 'D:' }), 'Moved a.txt to D:');
  assert.equal(bilingual(TABLE, 'moved', 'tr', { name: 'a.txt', where: 'D:' }), 'a.txt, D: konumuna taşındı');
  assert.equal(bilingual(TABLE, 'twice', 'en', { n: '3' }), '3 of 3', 'every occurrence is filled');
  assert.equal(bilingual(TABLE, 'moved', 'en'), 'Moved {name} to {where}', 'no values: the template as it is');
});

test('a key the table does not have is said as the key, not a crash in the middle of a menu', () => {
  assert.equal(bilingual(TABLE, 'nope', 'tr'), 'nope');
});
