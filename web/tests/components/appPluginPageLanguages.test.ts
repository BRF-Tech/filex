// A language pack's own page says what the Apps list says: it is a language
// pack, and how much of THIS filex each of its languages covers.
//
// ⚠ Merge seam (feat/043-lang × feat/043-apps-polish). Lang put a pack's
// coverage in `plugin.languages` and drew it in the Apps list row and the
// install review; apps-polish replaced the details drawer with a page
// (`/plugins/apps/<name>`). Without this the page an administrator opens
// from the row said nothing about the one thing a pack is judged by.
//
// The row is the server's own bytes (testdata/wire/app-plugin-language-pack.json,
// written by handlers.TestAppPluginWireFixtures).
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
const packRow = JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-language-pack.json'), 'utf8')).row;

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url.endsWith('/logs')) return { data: { lines: [], next: 0 } };
      if (url.endsWith('/locks')) return { data: { locks: [] } };
      if (url === '/admin/app-plugins') {
        return { data: { runtime: { enabled: true, arch_ok: true, disabled_reason: '', requires_signature: false, engines: {} }, plugins: [packRow] } };
      }
      if (/\/admin\/app-plugins\/\d+$/.test(url)) {
        return {
          data: {
            plugin: packRow,
            manifest: { manifest_version: 1, name: packRow.name, version: packRow.version, label: packRow.label, permissions: [], actions: [], views: [], settings: [] },
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

async function openPage(locale: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/plugins', name: 'plugins', component: { template: '<div />' } },
      { path: '/plugins/apps/:name', name: 'plugins.app', component: AppPluginPage },
    ],
  });
  await router.push(`/plugins/apps/${packRow.name}`);
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

describe('a language pack’s page', () => {
  it('is marked as a language pack and lists each language with its coverage', async () => {
    const w = await openPage('tr');
    expect(w.get('[data-testid="app-plugin-page-kind"]').text()).toBe(tr.appPlugins.kind.languagePack);
    const section = w.get('[data-testid="app-plugin-page-languages"]');
    expect(section.text()).toContain(tr.appPlugins.lang.heading);
    for (const l of packRow.languages as Array<{ code: string; percent: number; rtl: boolean }>) {
      expect(section.get(`[data-testid="app-plugin-language-${l.code}-coverage"]`).text()).toContain(String(l.percent));
      // No "right-to-left comes later" line: the layout ships in this release.
      expect(section.find(`[data-testid="app-plugin-language-${l.code}-rtl"]`).exists()).toBe(false);
    }
    w.unmount();
  });
});
