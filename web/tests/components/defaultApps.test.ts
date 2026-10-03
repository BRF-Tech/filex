// Admin → Plugins → Default apps (filex 0.50, docs/APP-PLUGINS.md → Default
// apps): which handler opens each kind of file, and which draws its thumbnail.
//
// What is pinned here:
//   1. The kinds are the explorer's table (DataTable, never a raw <table>),
//      each with who opens it and who draws it in order, the ones off, and
//      whether that is the default or a change.
//   2. The editor sends only the capability whose list changed, as the whole
//      rule (order + off); "Back to the default" is a DELETE.
//   3. A caller who may only read (an API key, the demo) gets no controls;
//      a tenant administrator is told it is the platform's; with apps off the
//      tab says so.
//   4. The Plugins page opens it from the address (`?tab=defaults`).
//   5. Turkish, with its own letters.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

const api = vi.hoisted(() => ({ list: vi.fn(), put: vi.fn(), reset: vi.fn() }));
vi.mock('@/api/fileTypes', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/fileTypes')>()),
  FileTypesApi: api,
}));
const invalidate = vi.hoisted(() => vi.fn());
vi.mock('@brftech/filex-core', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@brftech/filex-core')>()),
  invalidatePluginActions: invalidate,
}));
// The other two tabs of the Plugins page are not what this file tests.
vi.mock('@/components/plugins/StoragePluginsTab.vue', () => ({ default: { template: '<div data-testid="storage-tab" />' } }));
vi.mock('@/components/plugins/AppPluginsTab.vue', () => ({ default: { template: '<div data-testid="apps-tab" />' } }));
vi.mock('@/components/plugins/PluginRequestsPanel.vue', () => ({ default: { template: '<div />' } }));

import DefaultAppsTab from '@/components/plugins/DefaultAppsTab.vue';
import Plugins from '@/views/Plugins.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

/** GET /api/admin/file-types as the server writes it (backend
 *  file_types_wire_test.go): draw.io and Zeta open .drawio, an app draws
 *  .jar and .apk thumbnails, and the administrator switched an app off for
 *  PNG thumbnails. */
const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
function answer(over: Record<string, unknown> = {}) {
  return { ...JSON.parse(readFileSync(path.join(WIRE, 'admin-file-types.json'), 'utf8')), ...over };
}

function i18n(locale: 'en' | 'tr' = 'en') {
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } as never });
}

async function mountTab(locale: 'en' | 'tr' = 'en') {
  const w = mount(DefaultAppsTab, { global: { plugins: [i18n(locale)] }, attachTo: document.body });
  await flushPromises();
  return w;
}

beforeEach(() => {
  setActivePinia(createPinia());
  closeRowMenus();
  for (const f of Object.values(api)) f.mockReset();
  invalidate.mockReset();
  api.list.mockResolvedValue(answer());
});
afterEach(() => {
  closeRowMenus();
});

describe('Default apps', () => {
  it('lists the kinds in the explorer table, in order, with what is off and what was changed', async () => {
    const w = await mountTab();
    expect(w.find('table').exists()).toBe(false);
    expect(w.find('[data-testid="default-apps-row-drawio"]').text()).toContain('.drawio');
    expect(w.find('[data-testid="default-apps-row-drawio"]').text()).toContain('application/vnd.jgraph.mxfile');
    expect(w.find('[data-testid="default-apps-open-drawio"]').text()).toContain('1. draw.io · 2. Zeta viewer · 3. filex (built-in)');
    expect(w.find('[data-testid="default-apps-thumbnail-drawio"]').text()).toBe('None');
    expect(w.find('[data-testid="default-apps-thumbnail-jar"]').text()).toBe('1. Package contents');
    const png = w.find('[data-testid="default-apps-thumbnail-png"]').text();
    expect(png).toContain('1. filex (built-in)');
    expect(png).toContain('Off: PNG+');
    expect(w.find('[data-testid="default-apps-status-png"]').text()).toBe('Changed');
    expect(w.find('[data-testid="default-apps-status-jar"]').text()).toBe('Default');
    expect(w.find('[data-testid="default-apps-readonly"]').exists()).toBe(false);
  });

  it('the editor sends only the list that changed, whole: the order and the ones off', async () => {
    api.put.mockResolvedValue(answer());
    const w = await mountTab();
    await openRowMenu(w, 'default-apps-edit-drawio');
    expect(menuEntries().map((e) => e.label)).toEqual(['Edit', 'Back to the default']);
    expect(menuEntries()[1].disabled, 'nothing to go back from').toBe(true);
    await pickMenuItem('default-apps-edit-drawio-edit');
    await flushPromises();
    const editor = w.find('[data-testid="file-type-editor"]');
    expect(editor.exists()).toBe(true);
    expect(w.find('[data-testid="file-type-editor-thumbnail-none"]').exists()).toBe(true);

    // filex's own viewer first, Zeta off.
    await w.find('[data-testid="file-type-up-open-builtin"]').trigger('click');
    await w.find('[data-testid="file-type-up-open-builtin"]').trigger('click');
    await w.find('[data-testid="file-type-handler-open-app:zeta/viewer"] [role="switch"]').trigger('click');
    await w.find('[data-testid="file-type-editor-save"]').trigger('click');
    await flushPromises();
    expect(api.put).toHaveBeenCalledWith('drawio', {
      open: { order: ['builtin', 'app:drawio/editor'], off: ['app:zeta/viewer'] },
    });
    expect(invalidate, 'the explorer reads the new order').toHaveBeenCalled();
  });

  it('says when every opener is off, and saves nothing that did not change', async () => {
    const w = await mountTab();
    await openRowMenu(w, 'default-apps-edit-jar');
    await pickMenuItem('default-apps-edit-jar-edit');
    await flushPromises();
    expect(w.find('[data-testid="file-type-editor-save"]').attributes('disabled')).toBeDefined();
    await w.find('[data-testid="file-type-handler-thumbnail-app:pkglist"] [role="switch"]').trigger('click');
    expect(w.find('[data-testid="file-type-editor-thumbnail-alloff"]').text()).toContain('no thumbnail');
    await w.find('[data-testid="file-type-editor-save"]').trigger('click');
    await flushPromises();
    expect(api.put).toHaveBeenCalledWith('jar', { thumbnail: { order: [], off: ['app:pkglist'] } });
  });

  it('back to the default is a DELETE of the kind', async () => {
    api.reset.mockResolvedValue(answer());
    const w = await mountTab();
    await openRowMenu(w, 'default-apps-edit-png');
    expect(menuEntries()[1].disabled).toBe(false);
    await pickMenuItem('default-apps-edit-png-reset');
    await flushPromises();
    expect(api.reset).toHaveBeenCalledWith('png');
    expect(invalidate).toHaveBeenCalled();
  });

  it('a caller who may only read gets the lists and no controls', async () => {
    api.list.mockResolvedValue(answer({ editable: false }));
    const w = await mountTab();
    expect(w.find('[data-testid="default-apps-readonly"]').exists()).toBe(true);
    await openRowMenu(w, 'default-apps-edit-png');
    expect(menuEntries().map((e) => e.label)).toEqual(['Show']);
    await pickMenuItem('default-apps-edit-png-edit');
    await flushPromises();
    expect(w.find('[data-testid="file-type-editor-readonly"]').exists()).toBe(true);
    expect(w.find('[data-testid="file-type-editor-save"]').exists()).toBe(false);
    expect(w.find('[role="switch"]').exists()).toBe(false);
  });

  it('a tenant administrator is told it is the platform\'s; with apps off the tab says so', async () => {
    api.list.mockRejectedValue({ response: { status: 403, data: { error: 'supertenant_only' } } });
    const w = await mountTab();
    expect(w.find('[data-testid="default-apps-forbidden"]').exists()).toBe(true);

    api.list.mockResolvedValue({ enabled: false, editable: false, kinds: [] });
    const v = await mountTab();
    expect(v.find('[data-testid="default-apps-off"]').text()).toContain('filex alone');
  });

  it('in Turkish, with its own letters', async () => {
    const w = await mountTab('tr');
    expect(w.find('h1').text()).toBe('Varsayılan uygulamalar');
    expect(w.find('[data-testid="default-apps-open-drawio"]').text()).toContain('Zeta görüntüleyici');
    expect(w.find('[data-testid="default-apps-open-drawio"]').text()).toContain('filex (yerleşik)');
    expect(w.find('[data-testid="default-apps-status-png"]').text()).toBe('Değiştirildi');
  });
});

describe('Plugins page', () => {
  it('opens Default apps from the address and keeps the choice there', async () => {
    const r = createRouter({ history: createMemoryHistory(), routes: [{ path: '/plugins', name: 'plugins', component: { template: '<div />' } }] });
    await r.push('/plugins?tab=defaults');
    await r.isReady();
    const w = mount(Plugins, { global: { plugins: [r, i18n()] } });
    await flushPromises();
    const tab = w.find('[data-testid="plugins-tab-defaults"]');
    expect(tab.text()).toBe('Default apps');
    expect(tab.attributes('aria-selected')).toBe('true');
    expect(w.find('[data-testid="default-apps-tab"]').exists()).toBe(true);
    expect(api.list).toHaveBeenCalledTimes(1);

    await w.find('[data-testid="plugins-tab-apps"]').trigger('click');
    await flushPromises();
    expect(r.currentRoute.value.query.tab).toBe('apps');
    await w.find('[data-testid="plugins-tab-defaults"]').trigger('click');
    await flushPromises();
    expect(api.list, 'read again when opened again: an install changes it').toHaveBeenCalledTimes(2);
  });
});
