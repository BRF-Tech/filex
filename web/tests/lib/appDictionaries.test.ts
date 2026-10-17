// The dictionaries an administrator adds to an app (filex 0.57; backend
// wasmplugin/extras.go): where each file goes, and the added files read back
// one row per language.
//
// RED on the code before 0.57: lib/appDictionaries did not exist, and no
// screen could add a dictionary at all.
import { describe, expect, it } from 'vitest';
import {
  DICTIONARY_LANG,
  dictionaryLanguageName,
  dictionaryPath,
  dictionaryRows,
  dictionaryTableRows,
  declaresExtra,
} from '@/lib/appDictionaries';

describe('where a dictionary file goes', () => {
  it('is where the office editor asks for it (the Document Server names, served as .txt)', () => {
    expect(dictionaryPath('pl_PL', 'aff')).toBe('dictionaries/pl_PL/pl_PL.aff.txt');
    expect(dictionaryPath('pl_PL', 'dic')).toBe('dictionaries/pl_PL/pl_PL.dic.txt');
    expect(dictionaryPath('pl_PL', 'hyph')).toBe('dictionaries/pl_PL/hyph_pl_PL.dic.txt');
    expect(dictionaryPath('sr_Latn_RS', 'dic')).toBe('dictionaries/sr_Latn_RS/sr_Latn_RS.dic.txt');
  });

  it('takes the server\'s language pattern and nothing wider', () => {
    for (const ok of ['pl_PL', 'ar', 'sr_Latn_RS', 'de_DE']) expect(DICTIONARY_LANG.test(ok), ok).toBe(true);
    for (const bad of ['p', 'pl-PL', 'pl/PL', '../x', 'pl PL', 'a'.repeat(17), 'çş_TR']) expect(DICTIONARY_LANG.test(bad), bad).toBe(false);
  });
});

describe('the added files, one row per language', () => {
  const files = [
    { path: 'dictionaries/pl_PL/pl_PL.dic.txt', kind: 'dictionaries', size: 300, modified: '2026-10-16T08:00:00Z' },
    { path: 'dictionaries/pl_PL/hyph_pl_PL.dic.txt', kind: 'dictionaries', size: 20, modified: '2026-10-16T08:02:00Z' },
    { path: 'dictionaries/pl_PL/pl_PL.aff.txt', kind: 'dictionaries', size: 100, modified: '2026-10-16T08:01:00Z' },
    { path: 'dictionaries/de_AT/de_AT.dic.txt', kind: 'dictionaries', size: 5, modified: '2026-10-15T08:00:00Z' },
    { path: 'fonts/Inter.ttf', kind: 'fonts', size: 9, modified: '2026-10-15T08:00:00Z' },
  ];

  it('groups by language, in its order of parts, with the size together and the newest time', () => {
    const rows = dictionaryRows(files);
    expect(rows.map((r) => r.lang)).toEqual(['de_AT', 'pl_PL']);
    const pl = rows[1];
    expect(pl.parts).toEqual(['aff', 'dic', 'hyph']);
    expect(pl.size).toBe(420);
    expect(pl.modified).toBe('2026-10-16T08:02:00Z');
    expect([...pl.paths].sort()).toEqual([
      'dictionaries/pl_PL/hyph_pl_PL.dic.txt',
      'dictionaries/pl_PL/pl_PL.aff.txt',
      'dictionaries/pl_PL/pl_PL.dic.txt',
    ]);
    expect(rows[0].parts).toEqual(['dic']);
  });

  it('leaves out what is not a dictionary', () => {
    expect(dictionaryRows([{ path: 'fonts/Inter.ttf', kind: 'fonts', size: 1, modified: '' }])).toEqual([]);
  });
});

describe('a language folder as a person reads it', () => {
  it('names it in the reader\'s language, and keeps the folder name when it cannot', () => {
    expect(dictionaryLanguageName('pl_PL', 'en')).toMatch(/Polish/);
    expect(dictionaryLanguageName('pl_PL', 'tr')).toMatch(/Lehçe/);
    expect(dictionaryLanguageName('abcdefghij', 'en'), 'not a language tag at all').toBe('abcdefghij');
    expect(dictionaryLanguageName('zz', 'en'), 'a tag the browser has no name for').toBe('zz');
  });
});

// filex 0.57: only an app that names the kind (`ui.extras`) and reads its
// package has the section (draw.io reads its package and names none).
describe('which app takes dictionaries', () => {
  it('is the one whose manifest names them, and reads its own package', () => {
    expect(declaresExtra({ ui: { package_fetch: true, extras: { dictionaries: {} } } }, 'dictionaries')).toBe(true);
    expect(declaresExtra({ ui: { package_fetch: true } }, 'dictionaries'), 'draw.io').toBe(false);
    expect(declaresExtra({ ui: { extras: { dictionaries: {} } } }, 'dictionaries'), 'no package_fetch').toBe(false);
    expect(declaresExtra({}, 'dictionaries')).toBe(false);
    expect(declaresExtra(null, 'dictionaries')).toBe(false);
  });
});

describe('the rows of the section: added and offered', () => {
  it('is one row a language, those of the catalog beside the added ones', () => {
    const added = dictionaryRows([
      { path: 'dictionaries/pl_PL/pl_PL.aff.txt', kind: 'dictionaries', size: 1, modified: '2026-10-16T08:00:00Z' },
      { path: 'dictionaries/sv_SE/sv_SE.dic.txt', kind: 'dictionaries', size: 2, modified: '2026-10-16T08:00:00Z' },
    ]);
    const rows = dictionaryTableRows(added, [
      { id: 'pl_PL', size: 9, files: 2, installed: false, partial: true },
      { id: 'de_AT', size: 7, files: 2, installed: false, license: 'GPL-2.0' },
    ]);
    expect(rows.map((r) => [r.lang, r.offered, r.installed, r.partial])).toEqual([
      ['de_AT', true, false, false],
      ['pl_PL', true, false, true],
      ['sv_SE', false, true, false],
    ]);
    expect(rows[0].offeredSize).toBe(7);
    expect(rows[0].license).toBe('GPL-2.0');
    expect(rows[0].paths).toEqual([]);
  });
});
