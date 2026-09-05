// The embed's default language.
//
// It used to be `tr`, in eleven places across the shared component, so
// `<filex-explorer api-base="…">` with no `locale` rendered a Turkish UI to
// whoever installed filex — and the web component defaults `locale` to `''`,
// which made "no locale" the ordinary case for an embed rather than an edge
// one. The admin SPA never showed it because it always passes a locale.
//
// The rule now: what the host asked for, else what the browser asks for, else
// English. These cases pin all three, plus the runtime guards — a resolver
// that throws in SSR or in a locked-down browser would take the explorer down
// over a language choice.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { resolveLocale, detectLocale } from '@brftech/filex-core';

/** Replace `navigator` for one case. Restored in afterEach. */
function withNavigator(value: unknown) {
  vi.stubGlobal('navigator', value);
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('locale resolution', () => {
  it('an explicit locale always wins, whatever the browser prefers', () => {
    withNavigator({ languages: ['tr-TR', 'tr'], language: 'tr-TR' });
    expect(resolveLocale('en')).toBe('en');
    withNavigator({ languages: ['en-GB'], language: 'en-GB' });
    expect(resolveLocale('tr')).toBe('tr');
  });

  it('falls back to the browser when the host says nothing', () => {
    withNavigator({ languages: ['tr-TR', 'tr'], language: 'tr-TR' });
    expect(resolveLocale(undefined)).toBe('tr');
    // The web component passes '' rather than undefined, so that has to count
    // as "the host said nothing" too — it is the ordinary embed case.
    expect(resolveLocale('')).toBe('tr');
  });

  it('reads the primary subtag, so en-GB and tr-TR both resolve', () => {
    withNavigator({ languages: ['en-GB'], language: 'en-GB' });
    expect(detectLocale()).toBe('en');
    withNavigator({ languages: ['TR-tr'], language: 'TR-tr' });
    expect(detectLocale()).toBe('tr');
  });

  it('walks the preference list past languages it has no catalogue for', () => {
    withNavigator({ languages: ['de-DE', 'fr', 'tr-TR'], language: 'de-DE' });
    expect(detectLocale()).toBe('tr');
  });

  it('is English when the browser prefers a language filex does not speak', () => {
    withNavigator({ languages: ['de-DE', 'fr-FR'], language: 'de-DE' });
    expect(detectLocale()).toBe('en');
  });

  it('is English with no navigator at all — SSR and unit tests', () => {
    withNavigator(undefined);
    expect(detectLocale()).toBe('en');
    expect(resolveLocale(undefined)).toBe('en');
  });

  it('is English rather than an exception when navigator throws', () => {
    withNavigator({
      get languages(): string[] {
        throw new Error('blocked');
      },
      get language(): string {
        throw new Error('blocked');
      },
    });
    expect(() => detectLocale()).not.toThrow();
    expect(detectLocale()).toBe('en');
  });

  it('ignores a locale it has no catalogue for and asks the browser', () => {
    withNavigator({ languages: ['tr'], language: 'tr' });
    expect(resolveLocale('de' as never)).toBe('tr');
  });
});
