// A plugin's Text in the viewer's language, with the same fallback the Go
// side uses (wire.Text.Get): the language asked for, then en, then anything.
import { describe, expect, it } from 'vitest';

import { appTextOr, labelIn, labelOf } from '@brftech/filex-core/src/lib/pluginLabel';

describe('labelOf', () => {
  const text = { en: 'Sign…', tr: 'İmzala…' };

  it('reads the requested language', () => {
    expect(labelOf(text, 'tr')).toBe('İmzala…');
    expect(labelOf(text, 'en')).toBe('Sign…');
  });
  it('matches on the primary subtag', () => {
    expect(labelOf(text, 'tr-TR')).toBe('İmzala…');
  });
  it('falls back to en, then to any non-empty value', () => {
    expect(labelOf({ en: 'Signatures' }, 'tr')).toBe('Signatures');
    expect(labelOf({ de: 'Signaturen' }, 'tr')).toBe('Signaturen');
    expect(labelOf({ en: '', tr: 'İmza' }, 'en')).toBe('İmza');
  });
  it('is empty for nothing, and passes a plain string through', () => {
    expect(labelOf(null, 'en')).toBe('');
    expect(labelOf(undefined, 'tr')).toBe('');
    expect(labelOf({}, 'en')).toBe('');
    expect(labelOf('as is', 'tr')).toBe('as is');
  });
});

// v0.43.0: an app shipping en/tr/es/de/fr, a reader in Arabic, and a language
// pack holding filex's OWN Arabic for the very same captions — and the date
// box came out with three English words in the middle of an Arabic screen.
// `labelOf(app) || t(host)` cannot do otherwise: `labelOf` answers English,
// English is truthy, and the host string is never asked for.
describe('labelIn — the app’s words for THIS reader only', () => {
  const text = { en: 'Date order', tr: 'Tarih sırası' };

  it('answers the language asked for, and its base language', () => {
    expect(labelIn(text, 'tr')).toBe('Tarih sırası');
    expect(labelIn(text, 'tr-TR')).toBe('Tarih sırası');
  });

  it('does NOT fall back — that is the whole difference from labelOf', () => {
    expect(labelIn(text, 'ar')).toBe('');
    expect(labelOf(text, 'ar')).toBe('Date order');
    expect(labelIn({ en: '' }, 'en')).toBe('');
    expect(labelIn(null, 'ar')).toBe('');
  });

  it('passes a plain string through, as labelOf does', () => {
    expect(labelIn('as is', 'ar')).toBe('as is');
  });
});

describe('appTextOr — an app’s text where filex has words of its own', () => {
  const app = { en: 'Date order', tr: 'Tarih sırası' };
  const hostArabic = () => 'ترتيب التاريخ';

  it('gives the app its say when the app speaks this language', () => {
    expect(appTextOr({ ...app, ar: 'ترتيب المورد' }, 'ar', hostArabic)).toBe('ترتيب المورد');
    expect(appTextOr(app, 'tr', () => 'Tarih düzeni')).toBe('Tarih sırası');
  });

  it('prefers FILEX’s own words to the app’s other languages', () => {
    // The defect, in one line: this used to be "Date order".
    expect(appTextOr(app, 'ar', hostArabic)).toBe('ترتيب التاريخ');
  });

  it('falls back to the app’s other language when filex has no key', () => {
    // The last resort, and it is a real one: an app may caption something
    // filex has never had a word for.
    expect(appTextOr(app, 'ar', () => '')).toBe('Date order');
    expect(appTextOr({ de: 'Datumsreihenfolge' }, 'ar', () => '')).toBe('Datumsreihenfolge');
  });

  it('is filex’s own words when the app said nothing at all', () => {
    expect(appTextOr(undefined, 'ar', hostArabic)).toBe('ترتيب التاريخ');
    expect(appTextOr({}, 'ar', hostArabic)).toBe('ترتيب التاريخ');
  });

  it('asks the host only when it has to — the caller may be doing work', () => {
    let asked = 0;
    const host = () => {
      asked += 1;
      return 'host';
    };
    appTextOr({ ar: 'عربي' }, 'ar', host);
    expect(asked).toBe(0);
    appTextOr({ en: 'x' }, 'ar', host);
    expect(asked).toBe(1);
  });
});
