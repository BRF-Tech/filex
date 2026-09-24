// Issue #44: a storage's scan exclusions (`config.scan_exclude`). The
// descriptor endpoint serves them as `scan_fields`, beside the driver's own
// fields, and the storage pages draw them with the same field component — so
// the setting needs no hand-built form, and the replication-target dialog
// (which draws `fields` only) never offers it for a target nothing scans.
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

const scanFields = [
  {
    key: 'scan_exclude',
    type: 'string',
    label: 'Paths to exclude from scanning',
    i18n_key: 'storages.fields.scanExclude',
    help: 'One pattern per line…',
    help_i18n_key: 'storages.fieldHelp.scanExclude',
    required: false,
    secret: false,
    multiline: true,
    monospace: true,
  },
];

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
              scan_fields: scanFields,
            },
            {
              driver: 's3',
              label: 'S3',
              i18n_key: 'storages.driver.s3',
              capabilities: {},
              fields: [
                { key: 'bucket', type: 'string', label: 'Bucket', i18n_key: 'storages.fields.bucket', required: true },
                { key: 'prefix', type: 'string', label: 'Prefix', i18n_key: 'storages.fields.prefix', required: true, root: true },
              ],
              scan_fields: scanFields,
            },
          ],
        };
      }
      if (url === '/admin/storages/7') {
        return {
          data: {
            id: 7, name: 'medya', driver: 'local', enabled: true, read_only: false, rbac_enabled: false,
            sync_interval_s: 900, config: { path: '/srv/media', scan_exclude: '.snapshots' },
          },
        };
      }
      return { data: [] };
    }),
    post: vi.fn(async (url: string, body: Record<string, unknown>) => {
      calls.push({ method: 'post', url, body });
      return { data: { id: 9, ...body } };
    }),
    patch: vi.fn(async (url: string, body: Record<string, unknown>) => {
      calls.push({ method: 'patch', url, body });
      return { data: { id: 7, ...body } };
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

const mountNew = (locale = 'en') => mountPage(StorageNew, '/storages/new', locale);
const mountEdit = (locale = 'en') => mountPage(StorageEdit, '/storages/7', locale);

describe('storage scan exclusions (issue #44)', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    calls.length = 0;
  });

  it('the new-storage page draws the scan setting and sends it in config', async () => {
    const w = await mountNew('tr');
    const box = w.find('[data-testid="storage-scan-fields"] textarea');
    expect(box.exists(), 'the scan exclusions textarea').toBe(true);
    // Turkish label and help, from the catalogue, not the backend's English.
    const card = w.find('[data-testid="storage-scan-fields"]').text();
    expect(card).toContain(tr.storages.fields.scanExclude);
    expect(card).toContain('erişim denetimi değildir');

    await box.setValue('.*\ndownloads/incomplete/**');
    await w.find('form').trigger('submit');
    await flushPromises();
    const create = calls.find((c) => c.url === '/admin/storages');
    expect(create, 'POST /admin/storages').toBeTruthy();
    expect((create!.body.config as Record<string, unknown>).scan_exclude).toBe('.*\ndownloads/incomplete/**');
  });

  it('switching the driver keeps the scan setting (it belongs to the storage, not the driver)', async () => {
    const w = await mountNew();
    await w.find('[data-testid="storage-scan-fields"] textarea').setValue('.git');
    // The driver picker is the ui Select; drive it the way picking does.
    const picker = w.findAllComponents({ name: 'Select' }).find((c) => c.props('modelValue') === 'local');
    expect(picker, 'the driver picker').toBeTruthy();
    picker!.vm.$emit('update:modelValue', 's3');
    await flushPromises();
    expect((w.find('[data-testid="storage-scan-fields"] textarea').element as HTMLTextAreaElement).value).toBe('.git');
  });

  it('the storage editor shows the saved setting and saves an edit', async () => {
    const w = await mountEdit();
    const box = w.find('[data-testid="storage-scan-fields"] textarea');
    expect(box.exists(), 'the scan exclusions textarea').toBe(true);
    expect((box.element as HTMLTextAreaElement).value).toBe('.snapshots');
    await box.setValue('.snapshots\n*.tmp');
    await w.find('form').trigger('submit');
    await flushPromises();
    const patch = calls.find((c) => c.method === 'patch');
    expect(patch?.url).toBe('/admin/storages/7');
    expect((patch!.body.config as Record<string, unknown>).scan_exclude).toBe('.snapshots\n*.tmp');
    expect((patch!.body.config as Record<string, unknown>).path).toBe('/srv/media');
  });
});
