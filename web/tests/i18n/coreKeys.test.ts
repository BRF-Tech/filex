// Translation parity for the @brftech/filex-core catalogue.
//
// packages/core ships its own en.ts/tr.ts but has no test runner of its own
// (no vitest, no `test` script in its package.json), so the gate lives here —
// web already depends on the package, and a missing key on either side has the
// same consequence in both: the raw key string renders in the UI.
//
// This is the guard that would have caught `convert.*`: that modal shipped
// ~10 labels whose keys existed in neither catalogue, so nothing could
// translate them and the Turkish fallback reached every user.
import { describe, it, expect } from 'vitest';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';

describe('filex-core i18n key parity', () => {
  const enKeys = Object.keys(en);
  const trKeys = Object.keys(tr);

  it('every en.ts key is present in tr.ts', () => {
    const missing = enKeys.filter((k) => !(k in tr));
    expect(missing, `keys missing from core tr.ts: ${missing.join(', ')}`).toEqual([]);
  });

  it('every tr.ts key is present in en.ts', () => {
    const missing = trKeys.filter((k) => !(k in en));
    expect(missing, `keys missing from core en.ts: ${missing.join(', ')}`).toEqual([]);
  });

  it('no empty translation values', () => {
    const empties = [
      ...enKeys.filter((k) => en[k].trim() === '').map((k) => `en.ts:${k}`),
      ...trKeys.filter((k) => tr[k].trim() === '').map((k) => `tr.ts:${k}`),
    ];
    expect(empties).toEqual([]);
  });

  it('no English value is left as raw Turkish', () => {
    // A key that exists in both files but whose "English" value is still the
    // Turkish sentence is exactly the bug this branch fixes, wearing a key.
    // Turkish-only letters are the cheap, reliable tell.
    const turkishOnly = /[çÇğĞıİşŞ]/;
    const suspects = enKeys.filter(
      (k) => turkishOnly.test(en[k]) && !k.startsWith('lang.'),
    );
    expect(suspects, `core en.ts values that look Turkish: ${suspects.join(', ')}`).toEqual([]);
  });
});
