// sync_mode `lazy` on the storage forms (issue #45, docs/LAZY-CATALOGUE.md).
// The descriptor endpoint serves the lazy catalog's settings as `lazy_fields`
// for a driver a storage may be cataloged lazily on (local), and the forms
// draw them with the same field component as every other storage setting. The
// mode itself is offered only where the settings are: the server refuses it
// anywhere else. The storage page shows the lazy engine's `catalogue` block.
//
// Through the REAL views, api modules and stores; only the HTTP layer is fake,
// so what is asserted is the request the server receives.
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import StorageNew from '@/views/StorageNew.vue';
import StorageEdit from '@/views/StorageEdit.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const calls: Array<{ method: string; url: string; body: Record<string, unknown> }> = [];

const lazyFields = [
  {
    key: 'lazy_fill',
    type: 'select',
    label: 'Catalog behavior',
    i18n_key: 'storages.fields.lazyFill',
    help: 'Click first…',
    help_i18n_key: 'storages.fieldHelp.lazyFill',
    required: false,
    secret: false,
    default: 'background',
    options: [
      { value: 'background', label: 'Click first, fill in the background', i18n_key: 'storages.lazyFill.background' },
      { value: 'on_open', label: 'Only on open', i18n_key: 'storages.lazyFill.on_open' },
    ],
  },
  {
    key: 'lazy_max_watches',
    type: 'int',
    label: 'Watched folders (at most)',
    i18n_key: 'storages.fields.lazyMaxWatches',
    help_i18n_key: 'storages.fieldHelp.lazyMaxWatches',
    required: false,
    secret: false,
    default: 1024,
    min: 1,
    max: 1000000,
    advanced: true,
  },
];

let editRow: Record<string, unknown> = {};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/storage-drivers') {
        return {
          data: [
            {
              driver: 'local',
              label: 'Local',
              i18n_key: 'storages.driver.local',
              capabilities: {},
              fields: [{ key: 'path', type: 'string', label: 'Filesystem path', i18n_key: 'storages.fields.path', required: true, root: true }],
              lazy_fields: lazyFields,
            },
            {
              driver: 's3',
              label: 'S3',
              i18n_key: 'storages.driver.s3',
              capabilities: {},
              fields: [{ key: 'prefix', type: 'string', label: 'Prefix', i18n_key: 'storages.fields.prefix', required: true, root: true }],
            },
          ],
        };
      }
      if (url === '/admin/storages/7') return { data: editRow };
      return { data: [] };
    }),
    post: vi.fn(async (url: string, body: Record<string, unknown>) => {
      calls.push({ method: 'post', url, body });
      return { data: { id: 9, ...body } };
    }),
    patch: vi.fn(async (url: string, body: Record<string, unknown>) => {
      calls.push({ method: 'patch', url, body });
      return { data: { ...editRow, ...body } };
    }),
    delete: vi.fn(),
  },
}));

async function mountPage(component: unknown, path: string, locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/storages', name: 'storages', component: { template: '<div />' } },
      { path: '/storages/new', name: 'storages.new', component: { template: '<div />' } },
      { path: '/storages/:id', name: 'storages.edit', component: { template: '<div />' } },
    ],
  });
  await router.push(path);
  await router.isReady();
  vi.spyOn(router, 'push').mockResolvedValue(undefined);
  vi.spyOn(router, 'replace').mockResolvedValue(undefined);
  const w = mount(component as never, { global: { plugins: [i18n, router] } });
  await flushPromises();
  return w;
}

function modeSelect(w: ReturnType<typeof mount>) {
  return w.find('[data-testid="storage-sync-mode"] select');
}

function modeValues(w: ReturnType<typeof mount>): string[] {
  return modeSelect(w)
    .findAll('option')
    .map((o) => (o.element as HTMLOptionElement).value)
    .filter((v) => v !== '');
}

describe('the lazy catalog on the storage forms (issue #45)', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    calls.length = 0;
    editRow = {
      id: 7, name: 'arsiv', driver: 'local', enabled: true, read_only: false, rbac_enabled: false,
      sync_interval_s: 900, sync_mode: 'poll', config: { path: '/srv/arsiv' },
    };
  });

  it('offers `lazy` only for a driver whose descriptor carries its settings', async () => {
    const w = await mountNew();
    expect(modeValues(w)).toEqual(['poll', 'fsnotify', 'ondemand', 'lazy']);
    const driver = w.findAll('select').find((s) => s.findAll('option').some((o) => o.attributes('value') === 's3'));
    expect(driver, 'the driver picker').toBeTruthy();
    await modeSelect(w).setValue('lazy');
    await driver!.setValue('s3');
    await flushPromises();
    expect(modeValues(w)).toEqual(['poll', 'fsnotify', 'ondemand']);
    expect((modeSelect(w).element as HTMLSelectElement).value, 'a refused mode is not kept').toBe('poll');
  });

  it('draws the behavior choice in Turkish and sends mode + settings on create', async () => {
    const w = await mountNew('tr');
    expect(w.find('[data-testid="storage-lazy-fields"]').exists()).toBe(false);
    await modeSelect(w).setValue('lazy');
    await flushPromises();
    const box = w.find('[data-testid="storage-lazy-fields"]');
    expect(box.exists(), 'the lazy settings appear with the mode').toBe(true);
    expect(box.text()).toContain('Kataloglama davranışı');
    const behavior = box.find('select');
    expect(behavior.findAll('option').map((o) => o.text())).toContain('Yalnız açıldıkça');
    await behavior.setValue('on_open');
    await w.find('input[required]').setValue('arsiv');
    await w.find('form').trigger('submit');
    await flushPromises();
    const post = calls.find((c) => c.method === 'post' && c.url === '/admin/storages');
    expect(post, 'the create request').toBeTruthy();
    expect(post!.body.sync_mode).toBe('lazy');
    expect((post!.body.config as Record<string, unknown>).lazy_fill).toBe('on_open');
  });

  it('the storage page saves the mode and shows the catalog block', async () => {
    editRow = {
      ...editRow,
      sync_mode: 'lazy',
      config: { path: '/srv/arsiv', lazy_fill: 'background' },
      catalogue: {
        complete: false, reason: 'lazy_filling', mode: 'lazy', fill: 'background',
        catalogued_folders: 300, pending_folders: 100, watched_folders: 12, max_watches: 1024,
        filler: 'paused', held_back_folders: 2,
      },
    };
    const w = await mountEdit('tr');
    const status = w.find('[data-testid="storage-catalog-status"]');
    expect(status.exists(), 'the catalog block').toBe(true);
    expect(w.find('[data-testid="storage-catalog-summary"]').text()).toContain('%75');
    expect(w.find('[data-testid="storage-catalog-filler"]').text()).toBe('Yavaşladı, depo kullanılıyor');
    expect(w.find('[data-testid="storage-catalog-watched"]').text()).toBe('12 / 1.024');
    await modeSelect(w).setValue('ondemand');
    await w.find('form').trigger('submit');
    await flushPromises();
    const patch = calls.find((c) => c.method === 'patch');
    expect(patch?.body.sync_mode).toBe('ondemand');
  });

  it('a storage that is not lazy has no catalog block', async () => {
    const w = await mountEdit();
    expect(w.find('[data-testid="storage-catalog-status"]').exists()).toBe(false);
    expect((modeSelect(w).element as HTMLSelectElement).value).toBe('poll');
  });
});

const mountNew = (locale = 'en') => mountPage(StorageNew, '/storages/new', locale);
const mountEdit = (locale = 'en') => mountPage(StorageEdit, '/storages/7', locale);
