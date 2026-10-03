// Default apps: one name per handler wherever it is drawn (lib/fileTypes).
// 0.50 adds the OnlyOffice document server as a thumbnail handler of filex's
// own: it is "OnlyOffice" on the Default apps screen, its editor and the
// install review, never its bare id.
import { describe, expect, it } from 'vitest';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { handlerLabel, handlerLabelVersioned } from '@/lib/fileTypes';

function tOf(cat: Record<string, unknown>) {
  return (key: string) => {
    let v: unknown = cat;
    for (const part of key.split('.')) v = (v as Record<string, unknown> | undefined)?.[part];
    return typeof v === 'string' ? v : key;
  };
}

describe('handlerLabel', () => {
  it('names the document server, in either language', () => {
    expect(handlerLabel({ id: 'onlyoffice' }, tOf(en), 'en')).toBe('ONLYOFFICE');
    expect(handlerLabel({ id: 'onlyoffice' }, tOf(tr), 'tr')).toBe('ONLYOFFICE');
    expect(handlerLabelVersioned({ id: 'onlyoffice' }, tOf(en), 'en')).toBe('ONLYOFFICE');
  });

  it('filex\'s own and an app keep their names', () => {
    expect(handlerLabel({ id: 'builtin' }, tOf(en), 'en')).toBe('filex (built-in)');
    expect(handlerLabel({ id: 'app:pkglist', app: 'pkglist', label: { en: 'Package list' } }, tOf(en), 'en')).toBe('Package list');
  });
});
