// The Apps tab and an app that is set up before it runs (filex 0.60): its
// state reads "Needs setup", its row offers "Finish setup", installing one
// opens its wizard at once, and "Remove" asks the erase question (counts and
// the typed name) instead of a bare confirm.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import { menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const row = {
  id: 7, name: 'shortlink', version: '0.1.0', label: { en: 'Short links' }, enabled: true, state: 'setup', source: 'github',
  signed: true, permissions: ['data', 'public_routes'], scheduled: false, actions: 0, views: 2, public_pages: 0, created_at: '', updated_at: '',
};
const gets: string[] = [];

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      gets.push(url);
      if (url === '/admin/app-plugins') return { data: { plugins: [row], runtime: { enabled: true, engines: {}, engine_names: {}, said: {} } } };
      if (url === '/admin/app-plugins/7/setup') {
        return { data: { state: 'setup', needed: true, done: false, app_step: null, routes: [], permissions: [], own_hosts: [], multi_tenant: false } };
      }
      if (url === '/admin/app-plugins/7/erase') return { data: { counts: { data_keys: 2, data_bytes: 10, state_rows: 0, locks: 0, links: 0 }, confirm: 'shortlink' } };
      if (url.endsWith('/logs')) return { data: { lines: [], next: 0 } };
      return { data: {} };
    }),
    post: vi.fn(async () => ({ data: {} })),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(async () => ({ data: {} })),
  },
}));

vi.mock('@/i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/i18n')>()),
  loadOfferedLocales: vi.fn(async () => undefined),
}));

import AppPluginsTab from '@/components/plugins/AppPluginsTab.vue';

enableAutoUnmount(afterEach);

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

function mountTab(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(AppPluginsTab, { global: { plugins: [i18n] }, attachTo: document.body });
}

describe('AppPluginsTab: an app that needs setup', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    gets.length = 0;
  });

  it('reads "Needs setup", and its row offers "Finish setup", which opens the wizard', async () => {
    const w = mountTab('tr');
    await flushPromises();
    expect(w.text()).toContain(tr.appPlugins.state.setup);
    await openRowMenu(w, 'app-plugin-actions-shortlink');
    expect(menuEntries().map((e) => e.label)).toContain(tr.appPlugins.actions.setup);
    await pickMenuItem('app-plugin-actions-shortlink-setup');
    await flushPromises();
    expect(gets).toContain('/admin/app-plugins/7/setup');
    expect(document.body.querySelector('[data-testid="app-setup-wizard"]')).not.toBeNull();
  });

  it('"Remove" asks the erase question, with the counts, not a bare confirm', async () => {
    const ask = vi.spyOn(window, 'confirm');
    const w = mountTab();
    await flushPromises();
    await openRowMenu(w, 'app-plugin-actions-shortlink');
    await pickMenuItem('app-plugin-actions-shortlink-remove');
    await flushPromises();
    expect(ask).not.toHaveBeenCalled();
    expect(gets).toContain('/admin/app-plugins/7/erase');
    expect(document.body.querySelector('[data-testid="app-erase-data"]')?.textContent).toContain('2 entries');
    const { api } = await import('@/api/client');
    expect(api.delete).not.toHaveBeenCalled();
  });
});
