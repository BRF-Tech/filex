// An embed's own language setting (task #275, the owner's rule 2026-10-19):
// "⋯" → Language opens a dialog whose list names every offered language in
// its own words, under a first row that names what the embed speaks with no
// pick - the browser's language, a personal embed's account language, the
// site's default or English. The row is drawn only where the explorer asks for
// it (an embed); the web and desktop apps choose the language in their own
// settings.
//
// RED on the code before: the toolbar had no `languageRow`, there was no
// LanguageDialog and no `lang.*` strings.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import Toolbar from '@brftech/filex-core/src/components/Toolbar.vue';
import LanguageDialog from '@brftech/filex-core/src/components/LanguageDialog.vue';
import ChoiceSelect from '@brftech/filex-core/src/components/ChoiceSelect.vue';
import { resetLocales, setLocalesFromBranding } from '@brftech/filex-core/src/lib/uiLocales';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';

const REPO = path.resolve(__dirname, '../../..');

afterEach(() => {
  resetLocales();
});

const toolbarProps = {
  viewMode: 'list' as const,
  searchQuery: '',
  trashActive: false,
  actions: [],
  locale: 'en' as const,
};

/** The "⋯" settings rows the toolbar announces (`fe:header-menu`). */
async function menuKeys(props: Record<string, unknown>): Promise<{ keys: string[]; run: (k: string) => void; w: ReturnType<typeof mount> }> {
  let detail: { items: { key: string }[]; run: (k: string) => void } | null = null;
  const listen = (e: Event) => {
    detail = (e as CustomEvent).detail;
  };
  document.addEventListener('fe:header-menu', listen);
  const w = mount(Toolbar, { props: { ...toolbarProps, ...props }, attachTo: document.body });
  await nextTick();
  await nextTick();
  document.removeEventListener('fe:header-menu', listen);
  expect(detail, 'the toolbar announced its rows').toBeTruthy();
  return { keys: detail!.items.map((i) => i.key), run: detail!.run, w };
}

describe('the "⋯" menu', () => {
  it('has a Language row, beside Time zone, only where the explorer asks for it', async () => {
    const without = await menuKeys({});
    expect(without.keys).toContain('timezone');
    expect(without.keys).not.toContain('language');
    without.w.unmount();

    const embed = await menuKeys({ languageRow: true });
    expect(embed.keys.indexOf('language')).toBe(embed.keys.indexOf('timezone') + 1);
    embed.run('language');
    expect(embed.w.emitted('open-language')).toHaveLength(1);
  });

  it('says it in the explorer language', () => {
    expect(en['lang.menu']).toBe('Language');
    expect(tr['lang.menu']).toBe('Dil');
  });
});

describe('the Language dialog', () => {
  function dialog(props: Record<string, unknown> = {}) {
    return mount(LanguageDialog, {
      props: {
        open: true,
        locale: 'en',
        chosen: '',
        fallback: { locale: 'en', tier: 'browser' },
        ...props,
      },
      attachTo: document.body,
    });
  }
  const optionsOf = (w: ReturnType<typeof dialog>) =>
    w.findComponent(ChoiceSelect).props('options') as { value: string; label: string }[];

  it('lists every offered language by its own name, a pack included, under a first row that names the default', async () => {
    setLocalesFromBranding({ ui_locales: [{ code: 'es', source: 'plugin', plugin: 'lang-es', label: 'Español' }] } as never);
    const w = dialog();
    await flushPromises();
    expect(optionsOf(w)).toEqual([
      { value: '', label: 'Browser language (English)' },
      { value: 'en', label: 'English' },
      { value: 'tr', label: 'Türkçe' },
      { value: 'es', label: 'Español' },
    ]);
    expect(w.findComponent(ChoiceSelect).props('modelValue')).toBe('');
  });

  it("the first row names where the default comes from, in the explorer's language", async () => {
    const cases: Array<[string, string, string, string]> = [
      ['en', 'browser', 'tr', 'Browser language (Türkçe)'],
      ['en', 'account', 'tr', "Your account's language (Türkçe)"],
      ['en', 'host', 'tr', "This site's default (Türkçe)"],
      ['en', 'default', 'en', 'Default (English)'],
      ['tr', 'browser', 'en', 'Tarayıcının dili (English)'],
      ['tr', 'account', 'tr', 'Hesabınızın dili (Türkçe)'],
      ['tr', 'host', 'en', 'Bu sitenin varsayılanı (English)'],
      ['tr', 'default', 'en', 'Varsayılan (English)'],
    ];
    for (const [locale, tier, lang, label] of cases) {
      const w = dialog({ locale, fallback: { locale: lang, tier } });
      await flushPromises();
      expect(optionsOf(w)[0], `${locale}/${tier}`).toEqual({ value: '', label });
      w.unmount();
    }
  });

  it('shows the pick, emits a new one, and the first row emits "" (back to the default)', async () => {
    const w = dialog({ chosen: 'tr', fallback: { locale: 'en', tier: 'browser' } });
    await flushPromises();
    const select = w.findComponent(ChoiceSelect);
    expect(select.props('modelValue')).toBe('tr');
    select.vm.$emit('update:modelValue', 'en');
    select.vm.$emit('update:modelValue', '');
    expect(w.emitted('pick')).toEqual([['en'], ['']]);
  });

  it('says where the pick is kept: this browser, or this browser and the account for a personal embed', async () => {
    const plain = dialog();
    await flushPromises();
    expect(plain.find('[data-testid="fe-lang-hint"]').text()).toBe(en['lang.hint']);
    const personal = dialog({ personal: true, locale: 'tr' });
    await flushPromises();
    expect(personal.find('[data-testid="fe-lang-hint"]').text()).toBe(tr['lang.hint_account']);
  });
});

describe('the web component', () => {
  const wc = readFileSync(path.join(REPO, 'packages/webcomponent/src/index.ts'), 'utf8');

  it("hands a host the SAME language registry the bundled explorer reads, typed by core's package", () => {
    // The value from core's source (the module FileExplorer.vue imports), not
    // `@brftech/filex-core`, which resolves to core's dist - a second registry.
    expect(wc).toMatch(/import \{ loadLocales as sourceLoadLocales \} from '\.\.\/\.\.\/core\/src\/lib\/uiLocales';/);
    expect(wc).toMatch(/import type \{ loadLocales as coreLoadLocales \} from '@brftech\/filex-core';/);
    expect(wc).toMatch(/export const loadLocales: typeof coreLoadLocales = sourceLoadLocales;/);
  });

  it('re-exports no value straight from a path outside its own src (it would be rolled into index.d.ts as source)', () => {
    expect(wc).not.toMatch(/export \{[^}]*\} from '\.\.\//);
  });

  it("takes any language tag on the `locale` attribute, a language pack's included (it took only en and tr)", () => {
    expect(wc).toMatch(/if \(attrs\.locale && plausibleLocale\(attrs\.locale\)\) base\.locale = attrs\.locale as LocaleCode;/);
    expect(wc).not.toMatch(/attrs\.locale === 'tr' \|\| attrs\.locale === 'en'/);
  });
});
