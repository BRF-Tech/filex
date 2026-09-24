// An app's settings, on the app's PAGE, in the reader's language.
//
// ⚠ Merge seam (feat/043-signing × feat/043-apps-polish). Signing made a
// manifest setting's `label`, `help` and `placeholder` (and an option's
// `label`) accept `{"en": …, "tr": …}` — the signing app's settings were
// English inside the Turkish admin panel. Apps-polish replaced the Apps
// detail dialog with a page of its own (`/plugins/apps/<name>`). The page
// must draw every one of those texts through the localising mapper
// (`storageFieldOf` → `labelOf`) — a raw map on screen reads
// "[object Object]", and a hard-coded `.en` reads English to a Turkish admin.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const app = {
  id: 7,
  name: 'sign',
  version: '0.1.0',
  label: { en: 'Sign', tr: 'İmzala' },
  enabled: true,
  state: 'running',
  source: 'github',
  signed: true,
  permissions: [],
  scheduled: false,
  actions: 0,
  views: 0,
  public_pages: 0,
  created_at: '2026-09-01T09:00:00Z',
  updated_at: '2026-09-01T09:00:00Z',
};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url.endsWith('/logs')) return { data: { lines: [], next: 0 } };
      if (url.endsWith('/locks')) return { data: { locks: [] } };
      if (url === '/admin/app-plugins') {
        return { data: { runtime: { enabled: true, arch_ok: true, disabled_reason: '', requires_signature: false, engines: {} }, plugins: [app] } };
      }
      if (/\/admin\/app-plugins\/\d+$/.test(url)) {
        return {
          data: {
            plugin: app,
            manifest: {
              manifest_version: 1,
              name: 'sign',
              version: '0.1.0',
              label: { en: 'Sign', tr: 'İmzala' },
              permissions: [],
              actions: [],
              views: [],
              settings: [
                {
                  key: 'reason',
                  type: 'string',
                  label: { en: 'Reason shown to signers', tr: 'İmzacılara gösterilen gerekçe' },
                  help: { en: 'One short sentence.', tr: 'Kısa bir cümle.' },
                  placeholder: { en: 'e.g. contract approval', tr: 'ör. sözleşme onayı' },
                },
                {
                  key: 'mode',
                  type: 'select',
                  label: { en: 'Where the signed copy goes', tr: 'İmzalı kopyanın gideceği yer' },
                  options: [
                    { value: 'version', label: { en: 'A new version', tr: 'Yeni bir sürüm' } },
                    { value: 'sibling', label: { en: 'A file beside it', tr: 'Yanında bir dosya' } },
                  ],
                  default: 'version',
                },
              ],
            },
            granted: [],
            permissions: [],
            overrides: [],
            settings: {},
            schedule: [],
          },
        };
      }
      return { data: {} };
    }),
    post: vi.fn(async () => ({ data: {} })),
    patch: vi.fn(async () => ({ data: {} })),
    put: vi.fn(async () => ({ data: {} })),
    delete: vi.fn(async () => ({ data: {} })),
  },
}));

import AppPluginPage from '@/views/AppPluginPage.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

async function openPage(locale: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/plugins', name: 'plugins', component: { template: '<div />' } },
      { path: '/plugins/apps/:name', name: 'plugins.app', component: AppPluginPage },
    ],
  });
  await router.push('/plugins/apps/sign');
  await router.isReady();
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(AppPluginPage, { global: { plugins: [i18n, router] }, attachTo: document.body });
  await flushPromises();
  await flushPromises();
  return w;
}

beforeEach(() => {
  setActivePinia(createPinia());
  document.body.innerHTML = '';
});

describe('the app page draws its settings in the reader’s language', () => {
  it('Turkish: label, help, placeholder and option labels', async () => {
    const w = await openPage('tr');
    const text = document.body.textContent ?? '';
    expect(text).toContain('İmzacılara gösterilen gerekçe');
    expect(text).toContain('Kısa bir cümle.');
    expect(text).toContain('İmzalı kopyanın gideceği yer');
    expect(text).toContain('Yeni bir sürüm');
    expect(text).toContain('Yanında bir dosya');
    expect(text).not.toContain('[object Object]');
    expect(text).not.toContain('Reason shown to signers');
    const inputs = Array.from(document.querySelectorAll('input')).map((i) => i.getAttribute('placeholder') ?? '');
    expect(inputs).toContain('ör. sözleşme onayı');
    w.unmount();
  });

  it('English: the same fields in English', async () => {
    const w = await openPage('en');
    const text = document.body.textContent ?? '';
    expect(text).toContain('Reason shown to signers');
    expect(text).toContain('One short sentence.');
    expect(text).toContain('A new version');
    expect(text).not.toContain('[object Object]');
    w.unmount();
  });
});
