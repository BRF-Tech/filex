// A restore from the Trash says everything that happened to the selection.
//
// ⚠ #59's restore said only "taken" when one item's name was taken and
// another item failed for another reason: the failure — and how many came
// back — went unsaid. Its partial sentence also ended on an elliptical
// "…, 1 could not be: …".
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { sayRestore } from '@brftech/filex-core/src/lib/restoreWords';
import { useLocale } from '@brftech/filex-core/src/composables/useLocale';

// The explorer's own translator: it picks `_one` by the count.
const en = useLocale(() => 'en').t;
const tr = useLocale(() => 'tr').t;

describe('what a restore says', () => {
  it('a mixed result says both: what failed and why, and whose name is taken', () => {
    const out = sayRestore({ restored: 1, taken: ['Kayıtlar'], failed: 1, reason: 'Not found' }, en);
    expect(out.failure).toBe(true);
    expect(out.message).toContain('1 item restored; 1 could not be restored: Not found');
    expect(out.message).toContain('“Kayıtlar” was not restored');
  });

  it('in Turkish too', () => {
    const out = sayRestore({ restored: 2, taken: ['Arşiv'], failed: 1, reason: 'Bulunamadı' }, tr);
    expect(out.message).toContain('2 öğe geri getirildi, 1 öğe getirilemedi: Bulunamadı');
    expect(out.message).toContain('“Arşiv” geri getirilmedi');
  });

  it('a taken name alone, a failure alone, and a clean restore', () => {
    expect(sayRestore({ restored: 0, taken: ['a.txt'], failed: 0 }, en).message).toBe(en('toast.restore_taken', { n: 1, name: 'a.txt' }));
    expect(sayRestore({ restored: 2, taken: [], failed: 1, reason: 'Timed out' }, en).message).toBe(
      '2 items restored; 1 could not be restored: Timed out',
    );
    expect(sayRestore({ restored: 3, taken: [], failed: 0 }, en)).toEqual({ message: '3 items restored', failure: false });
  });
});

describe('the explorer', () => {
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
  const fn = src.slice(src.indexOf('async function restoreSelection'), src.indexOf('function previewNode'));
  it('says a restore in these words', () => {
    expect(fn).toMatch(/sayRestore\(\{ restored, taken, failed, reason: failure === undefined \? undefined : failureText\(failure\) \}, t\)/);
    expect(fn, 'a taken name hid the failure beside it').not.toMatch(/if \(taken\.length\) \{\s*showToast/);
  });
});
