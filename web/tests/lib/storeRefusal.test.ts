// A store refusal is said in the SERVER's sentence (backend
// handlers/app_store_words.go, `server.store.*`): the panel prints `message`
// as it came and keeps no table of codes or sentences of its own.
//
// RED before 0.55: lib/storeRefusal.ts kept STORE_CODES and built every
// sentence from the code and the detail with the locales' appStore.err.*,
// throwing the server's `message` away - a second copy of each sentence, one
// the command line and an agent never had (store fe review #8 wrote the
// table; #215 already let a storage plugin's refusal through as it came).
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import * as storeRefusal from '@/lib/storeRefusal';
import { storeSentence, storeSource } from '@/lib/storeRefusal';

function tIn(locale: 'en' | 'tr') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr }, missingWarn: false, fallbackWarn: false });
  return (key: string, values?: Record<string, unknown>) => i18n.global.t(key, values ?? {});
}

describe('store refusals in the server’s sentence', () => {
  it('an app’s refusal: the server’s message, as it came', () => {
    const message = '1.1.0 kurulu; bağlantı 1.0.0 sürümü için ve bu daha yeni değil.';
    expect(
      storeSentence({ error: 'intent_version_rollback', message, detail: { kind: 'language-pack', installed: '1.1.0', link: '1.0.0', reason: 'lang-eo 1.1.0 is installed' } }),
    ).toBe(message);
  });

  it('a refusal with no detail at all (a review no longer open, the trust, a license key): its message', () => {
    expect(storeSentence({ error: 'intent_session_unknown', message: 'Bu inceleme artık açık değil.' })).toBe('Bu inceleme artık açık değil.');
    expect(storeSentence({ error: 'license_key_invalid', message: 'That license key cannot be used.' })).toBe('That license key cannot be used.');
  });

  it('a storage plugin’s refusal (#215): the same rule, never an app sentence', () => {
    expect(storeSentence({ error: 'store_source_changed', message: 'remove the installed plugin first', detail: { kind: 'storage' } })).toBe(
      'remove the installed plugin first',
    );
  });

  it('no message: nothing - the caller says the status in its own words', () => {
    expect(storeSentence({ error: 'store_unreachable' })).toBe('');
    expect(storeSentence({ error: 'intent_pin_mismatch', message: '   ' })).toBe('');
  });

  it('keeps no table of codes, and the locales no copy of the sentences', () => {
    expect(Object.keys(storeRefusal)).not.toContain('STORE_CODES');
    expect((en as { appStore: Record<string, unknown> }).appStore.err).toBeUndefined();
    expect((tr as { appStore: Record<string, unknown> }).appStore.err).toBeUndefined();
    const src = readFileSync(resolve(__dirname, '../../src/lib/storeRefusal.ts'), 'utf8');
    expect(src).not.toMatch(/appStore\.err/);
  });

  it('where an installed app came from is still said for the upgrade review', () => {
    const up = storeSource({ store: '', repo: '', version: '1', source_url: 'https://x.example/app.json' }, tIn('en'));
    expect(up).toBe('an upload or an address, without a store (https://x.example/app.json)');
    expect(storeSource({ store: 'https://a.example', repo: 'Owner/app' }, tIn('tr'))).toBe('https://a.example mağazasından (Owner/app)');
  });
});
