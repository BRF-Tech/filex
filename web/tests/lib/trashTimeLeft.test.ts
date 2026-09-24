// The Trash's "time left" — ONE sentence for one fact, on both Trash screens.
//
// ⚠ Merge seam (feat/043-w2-explorer × feat/043-srvtext): the explorer's
// Trash view added `trash.days_left(_one)` / `trash.days_left_due` to its
// table while srvtext had just renamed the admin panel's `trash.days_left` to
// `trash.days_remaining` — two key families for one fact, and the admin page
// said "0 days" where the explorer said "Due for deletion". Now both call
// core `trashTimeLeft`, over the explorer table's `trash.days_remaining*`.
import { describe, expect, it } from 'vitest';

import { trashTimeLeft, translate } from '@brftech/filex-core';
import { en } from '@brftech/filex-core/src/locales/en';
import enJson from '@/locales/en.json';

const inLang = (lang: string) => (key: string, vars?: Record<string, string | number>) => translate(lang, key, vars);

describe('trashTimeLeft', () => {
  it('says the days, one day, due, and nothing without a count', () => {
    expect(trashTimeLeft(30, inLang('en'))).toBe('30 days');
    expect(trashTimeLeft(1, inLang('en'))).toBe('1 day');
    expect(trashTimeLeft(0, inLang('en'))).toBe('Due for deletion');
    expect(trashTimeLeft(null, inLang('en'))).toBe('—');
    expect(trashTimeLeft(undefined, inLang('en'))).toBe('—');
    expect(trashTimeLeft(30, inLang('tr'))).toBe('30 gün');
    expect(trashTimeLeft(0, inLang('tr'))).toBe('Silinmek üzere');
  });

  it('is one key family, in the explorer table only', () => {
    expect(Object.keys(en).filter((k) => k.startsWith('trash.days_')).sort()).toEqual([
      'trash.days_remaining',
      'trash.days_remaining_due',
      'trash.days_remaining_one',
    ]);
    const adminTrash = (enJson as { trash: Record<string, unknown> }).trash;
    expect(Object.keys(adminTrash).filter((k) => k.startsWith('days_'))).toEqual([]);
  });
});
