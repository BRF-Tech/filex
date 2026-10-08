// External services → ONLYOFFICE → Editor language (GitHub Discussion #93).
//
// The administrator chooses what language the ONLYOFFICE editor opens in:
// automatic (each person's own filex language, the default) or one language
// for everybody. The languages are the SERVER's list, each named in itself
// (backend onlyoffice/lang.go EditorLanguages); the page keeps no copy and
// decides nothing - it shows the list, saves the choice, and says when
// FILEX_ONLYOFFICE_LANG pins it.
//
// Red before #214: there was no such field (and no native select may stand in
// for it - ui/Select is core's ChoiceSelect).
//
// Through the REAL view, store and api module; only the HTTP layer (and the
// browser probe, which loads a script) is faked.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import External from '@/views/External.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { chosenValue, listedOptions, pickOption } from '../helpers/choiceSelect';

const calls = { patches: [] as Array<{ url: string; body?: Record<string, unknown> }> };

const LANGUAGES = [
  { code: 'de', name: 'Deutsch' },
  { code: 'pt-PT', name: 'Português (Portugal)' },
  { code: 'tr', name: 'Türkçe' },
];

function onlyOfficeRow(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    Name: 'onlyoffice',
    Enabled: true,
    URL: 'https://office.example.com',
    SecretEnc: '***',
    OptionsJSON: '{}',
    LastCheck: null,
    LastState: 'ok',
    env_managed: false,
    advisories: [],
    callback_url: '',
    editor_lang: 'auto',
    editor_languages: LANGUAGES,
    editor_lang_env_managed: false,
    ...over,
  };
}

const DRAWIO = {
  Name: 'drawio',
  Enabled: true,
  URL: 'https://draw.example.com',
  SecretEnc: '',
  OptionsJSON: '{}',
  LastCheck: null,
  LastState: 'ok',
  env_managed: false,
  advisories: [],
};

let rows: Array<Record<string, unknown>> = [];

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f?: string) => f ?? 'error',
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/external') {
        return { data: { entries: rows, public_url: 'https://files.example.com' } };
      }
      throw new Error(`unexpected GET ${url}`);
    }),
    post: vi.fn(async () => ({
      data: { ok: true, name: 'onlyoffice', reachable: true, server_reachable: true, state: 'ok', advisories: [] },
    })),
    patch: vi.fn(async (url: string, body?: Record<string, unknown>) => {
      calls.patches.push({ url, body });
      return { data: { ok: true } };
    }),
  },
}));

vi.mock('@brftech/filex-core', async (importOriginal) => {
  const real = await importOriginal<Record<string, unknown>>();
  return {
    ...real,
    probeExternalFromBrowser: vi.fn(async (_id: string, url: string) => ({ state: 'ok', url })),
  };
});

async function mountExternal(locale: 'en' | 'tr') {
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = createRouter({
    history: createMemoryHistory('/admin/'),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/external', name: 'external', component: { template: '<div />' } },
    ],
  });
  await router.push('/');
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(External, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
  await flushPromises();
  return w;
}

const field = (id = 'onlyoffice') => document.querySelector<HTMLElement>(`[data-testid="external-editor-lang-${id}"]`);

beforeEach(() => {
  calls.patches.length = 0;
  rows = [onlyOfficeRow(), DRAWIO];
});

describe('ONLYOFFICE → Editor language', () => {
  it('offers automatic first, then the server’s languages named in themselves; automatic is the default', async () => {
    await mountExternal('en');
    const sel = field();
    expect(sel, 'the ONLYOFFICE card has the field').not.toBeNull();
    expect(sel!.textContent).toContain(en.external.fields.editorLang);
    expect(chosenValue(sel!)).toBe('auto');
    const options = await listedOptions(sel!);
    expect(options.map((o) => o.value)).toEqual(['auto', 'de', 'pt-PT', 'tr']);
    expect(options.map((o) => o.label)).toEqual([
      en.external.fields.editorLangAuto,
      'Deutsch',
      'Português (Portugal)',
      'Türkçe',
    ]);
    expect(sel!.querySelector('select'), 'no native select').toBeNull();
    expect(field('drawio'), 'draw.io has no editor language').toBeNull();
  });

  it('shows the saved language and saves the one chosen with the card', async () => {
    rows = [onlyOfficeRow({ editor_lang: 'tr' }), DRAWIO];
    await mountExternal('en');
    expect(chosenValue(field()!)).toBe('tr');

    await pickOption(field()!, 'de');
    const card = field()!.closest('.card') as HTMLElement;
    const save = Array.from(card.querySelectorAll('button')).find((b) => b.textContent?.includes(en.common.save));
    expect(save, 'the card’s Save button').toBeDefined();
    save!.click();
    await flushPromises();

    const sent = calls.patches.find((c) => c.url === '/admin/external/onlyoffice');
    expect(sent, 'the card was saved').toBeDefined();
    expect(sent!.body?.editor_lang).toBe('de');
    expect(calls.patches.find((c) => c.url === '/admin/external/drawio')).toBeUndefined();
  });

  it('says so when FILEX_ONLYOFFICE_LANG pins it', async () => {
    rows = [onlyOfficeRow({ editor_lang: 'de', editor_lang_env_managed: true }), DRAWIO];
    await mountExternal('en');
    const text = field()!.textContent ?? '';
    expect(text).toContain('FILEX_ONLYOFFICE_LANG');
    expect(text).toContain('until filex restarts');
  });

  it('in Turkish', async () => {
    await mountExternal('tr');
    const sel = field()!;
    expect(sel.textContent).toContain('Editör dili');
    expect(sel.textContent).toContain('Otomatik seçiliyken ONLYOFFICE editörü');
    const options = await listedOptions(sel);
    expect(options[0].label).toBe('Otomatik - herkesin kendi filex dili');
  });

  it('a server that sends no list (older than 0.54) gets no field, and nothing is sent', async () => {
    rows = [onlyOfficeRow({ editor_lang: undefined, editor_languages: undefined }), DRAWIO];
    await mountExternal('en');
    expect(field()).toBeNull();
    const card = document.querySelector('[data-testid="external-url-onlyoffice"]')!.closest('.card') as HTMLElement;
    const save = Array.from(card.querySelectorAll('button')).find((b) => b.textContent?.includes(en.common.save));
    save!.click();
    await flushPromises();
    const sent = calls.patches.find((c) => c.url === '/admin/external/onlyoffice');
    expect(sent).toBeDefined();
    expect(sent!.body).not.toHaveProperty('editor_lang');
  });
});
