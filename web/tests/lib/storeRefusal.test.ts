// A store install's refusals, said as sentences (lib/storeRefusal.ts; store
// fe review #8): every code the store routes answer has one in English and
// Turkish, and the codes the 0.52.0 review added or changed say what they
// carry - both sources and the way out for store_source_changed (an app
// installed straight from its repository said apart), both filex addresses or
// the setting to fix for intent_wrong_instance, a moved tag for a `commit`
// pin.
import { describe, expect, it } from 'vitest';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { STORE_CODES, storeSentence, storeSource } from '@/lib/storeRefusal';

function tIn(locale: 'en' | 'tr') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr }, missingWarn: false, fallbackWarn: false });
  return (key: string, values?: Record<string, unknown>) => i18n.global.t(key, values ?? {});
}

const STORE_A = 'https://a.example';
const STORE_B = 'https://b.example';

describe('store refusals in a sentence', () => {
  it.each(['en', 'tr'] as const)('every code has its own sentence (%s), with no long dash in Turkish', (locale) => {
    const t = tIn(locale);
    for (const code of STORE_CODES) {
      const s = storeSentence({ error: code }, t);
      expect(s, code).not.toBe('');
      expect(s, code).not.toContain('appStore.err');
      if (locale === 'tr') expect(s, code).not.toMatch(/[\u2013\u2014]/);
    }
  });

  it('an app from another store or repository: both sources, and remove it first', () => {
    const r = {
      error: 'store_source_changed',
      detail: {
        installed: { store: STORE_A, repo: 'Owner/app', version: '1.0.0', source_url: 'https://github.com/Owner/app@v1.0.0' },
        link: { store: STORE_B, repo: 'Other/app', version: '1.2.0' },
      },
    };
    const e = storeSentence(r, tIn('en'));
    expect(e).toContain(`the store ${STORE_A} (Owner/app)`);
    expect(e).toContain(`the store ${STORE_B} (Other/app)`);
    expect(e).toContain('1.0.0');
    expect(e).toMatch(/Remove the installed app first/);
    const s = storeSentence(r, tIn('tr'));
    expect(s).toContain(`${STORE_A} mağazasından (Owner/app)`);
    expect(s).toContain('kaldırın');
  });

  it('an app installed straight from its repository and a store’s paid link for it: said apart, with the way out', () => {
    const r = {
      error: 'store_source_changed',
      detail: {
        installed: { store: '', repo: 'Owner/app', version: '1.0.0', source_url: 'https://github.com/Owner/app@v1.0.0' },
        link: { store: STORE_B, repo: 'owner/APP', version: '1.2.0' },
      },
    };
    const e = storeSentence(r, tIn('en'));
    expect(e).toContain('straight from its repository (Owner/app), not from a store');
    expect(e).toContain(`The paid link of ${STORE_B}`);
    expect(e).toContain('remove the installed app first');
    const s = storeSentence(r, tIn('tr'));
    expect(s).toContain('bir mağazadan değil, doğrudan deposundan (Owner/app) kuruldu');
    expect(s).toContain('ücretli sürümüne geçmek için önce kurulu uygulamayı kaldırın');
  });

  it('installed without a store from another repository: the general sentence, naming where it came from', () => {
    const e = storeSentence(
      { error: 'store_source_changed', detail: { installed: { store: '', repo: 'Owner/app', version: '1.0.0' }, link: { store: STORE_B, repo: 'Other/app', version: '1.2.0' } } },
      tIn('en'),
    );
    expect(e).toContain('GitHub directly, without a store (Owner/app)');
    expect(e).toContain(`the store ${STORE_B} (Other/app)`);
    const up = storeSource({ store: '', repo: '', version: '1', source_url: 'https://x.example/app.json' }, tIn('en'));
    expect(up).toBe('an upload or an address, without a store (https://x.example/app.json)');
  });

  it('a link made for another filex names both; an unusable FILEX_PUBLIC_URL names the setting', () => {
    const e = storeSentence({ error: 'intent_wrong_instance', detail: { filex_origin: 'https://other.example', this_filex: 'https://files.example' } }, tIn('en'));
    expect(e).toContain('https://other.example');
    expect(e).toContain('https://files.example');
    const tr1 = storeSentence({ error: 'intent_wrong_instance', detail: { filex_origin: 'https://other.example', this_filex: 'https://files.example' } }, tIn('tr'));
    expect(tr1).toContain('başka bir filex için oluşturuldu (https://other.example)');
    const p = storeSentence({ error: 'intent_wrong_instance', detail: { public_url_invalid: true } }, tIn('en'));
    expect(p).toContain('FILEX_PUBLIC_URL');
    expect(p).not.toContain('another filex');
    expect(storeSentence({ error: 'intent_wrong_instance', detail: { public_url_invalid: true } }, tIn('tr'))).toContain('FILEX_PUBLIC_URL ayarı');
  });

  it('a moved tag (`commit`) says so; the other pins keep their sentence', () => {
    const c = storeSentence({ error: 'intent_pin_mismatch', detail: { mismatches: [{ field: 'commit', link: 'v1@abc', source: 'x' }] } }, tIn('en'));
    expect(c).toContain('tag no longer serves');
    expect(c).toContain('(commit)');
    const m = storeSentence({ error: 'intent_pin_mismatch', detail: { mismatches: [{ field: 'manifest_sha256', link: 'a', source: 'b' }] } }, tIn('en'));
    expect(m).toContain('(manifest_sha256)');
    expect(m).not.toContain('tag');
  });

  it('the changed codes say what they now mean', () => {
    const t = tIn('en');
    expect(storeSentence({ error: 'store_not_allowed' }, t)).toContain('FILEX_APP_STORE_URLS');
    expect(storeSentence({ error: 'store_bad_answer' }, t)).toMatch(/redirect.*clock/);
    expect(storeSentence({ error: 'intent_invalid' }, t)).toMatch(/the filex it was made for, the commit, the manifest/);
    expect(storeSentence({ error: 'intent_session_unknown' }, t)).toContain('installed or closed already');
    expect(storeSentence({ error: 'intent_version_rollback', detail: { installed: '1.2.0', link: '1.0.0' } }, t)).toContain('1.2.0 is installed');
  });
});
