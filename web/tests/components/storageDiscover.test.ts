// "Add new storage" can mount several folders at once.
//
// Issue #31: the root of a bucket is never mounted as one storage
// (ROOT_PATH_FORBIDDEN), so an operator with a bucket of N top-level folders
// filled the form N times. The page now lists the folders under the root typed
// in the form and creates one storage per ticked folder — same credentials,
// the folder as that storage's root, through the ordinary create.
//
// Through the REAL api modules and stores with only the HTTP layer faked, so
// the request the server receives is the one asserted here.
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import StorageNew from '@/views/StorageNew.vue';
import en from '@/locales/en.json';

const posts: Array<{ url: string; body: Record<string, unknown> }> = [];

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/storage-drivers') {
        return {
          data: [
            {
              driver: 's3',
              label: 'S3',
              i18n_key: 'storages.driver.s3',
              capabilities: {},
              fields: [
                { key: 'bucket', type: 'string', label: 'Bucket', i18n_key: 'storages.fields.bucket', required: true },
                { key: 'prefix', type: 'string', label: 'Prefix', i18n_key: 'storages.fields.prefix', required: true, root: true },
                { key: 'access_key', type: 'string', label: 'Access key', i18n_key: 'storages.fields.accessKey', required: true },
              ],
            },
          ],
        };
      }
      return { data: [] };
    }),
    post: vi.fn(async (url: string, body: Record<string, unknown>) => {
      posts.push({ url, body });
      if (url === '/admin/storages/discover') {
        return {
          data: {
            ok: true,
            root_key: 'prefix',
            folders: [
              { name: 'arsiv', root: 'arsiv' },
              { name: 'belgeler', root: 'belgeler' },
              { name: 'fotograf', root: 'fotograf' },
            ],
          },
        };
      }
      if (url === '/admin/storages') {
        if (body.name === 'belgeler') throw new Error('boom');
        return { data: { id: posts.length, ...body } };
      }
      return { data: {} };
    }),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

function mountPage() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/storages', name: 'storages', component: { template: '<div />' } },
      { path: '/storages/new', name: 'storages.new', component: StorageNew },
      { path: '/storages/:id', name: 'storages.edit', component: { template: '<div />' } },
    ],
  });
  const pushSpy = vi.spyOn(router, 'push').mockResolvedValue(undefined);
  const wrapper = mount(StorageNew, { global: { plugins: [i18n, router] } });
  return { wrapper, pushSpy };
}

describe('StorageNew — mount several folders at once', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    posts.length = 0;
  });

  it('lists the folders under the typed root and creates one storage per ticked folder', async () => {
    const { wrapper, pushSpy } = mountPage();
    await flushPromises();

    // The driver on offer declares a root field, so the card is there.
    const card = wrapper.find('[data-testid="discover-card"]');
    expect(card.exists()).toBe(true);
    expect(card.text()).toContain(en.storages.discover.title);

    await card.find('[data-testid="discover-list"]').trigger('click');
    await flushPromises();

    expect(posts[0]?.url).toBe('/admin/storages/discover');
    expect(posts[0]?.body.driver).toBe('s3');

    const rows = wrapper.findAll('[data-testid="discover-folders"] li');
    expect(rows.map((r) => r.attributes('data-folder'))).toEqual(['arsiv', 'belgeler', 'fotograf']);
    expect(wrapper.find('[data-testid="discover-create"]').text()).toContain('3');

    // Untick one: it is not created. Rename another: the sidebar name is the
    // typed one, the root stays the folder.
    await rows[2].find('input[type="checkbox"]').setValue(false);
    const nameInputs = rows[0].findAll('input').filter((i) => i.attributes('type') !== 'checkbox');
    await nameInputs[0].setValue('Arşiv');

    await wrapper.find('[data-testid="discover-create"]').trigger('click');
    await flushPromises();

    const creates = posts.filter((p) => p.url === '/admin/storages');
    expect(creates.map((c) => c.body.name)).toEqual(['Arşiv', 'belgeler']);
    expect(creates[0].body.driver).toBe('s3');
    expect((creates[0].body.config as Record<string, unknown>).prefix).toBe('arsiv');
    expect((creates[1].body.config as Record<string, unknown>).prefix).toBe('belgeler');
    // "belgeler" failed on the fake server: no navigation, and only the one
    // that failed stays ticked for a retry.
    expect(pushSpy).not.toHaveBeenCalledWith({ name: 'storages' });
    const after = wrapper.findAll('[data-testid="discover-folders"] li');
    const checked = after.map((r) => (r.find('input[type="checkbox"]').element as HTMLInputElement).checked);
    expect(checked).toEqual([false, true, false]);
  });

  it('navigates to the list once every ticked folder landed', async () => {
    const { wrapper, pushSpy } = mountPage();
    await flushPromises();
    await wrapper.find('[data-testid="discover-list"]').trigger('click');
    await flushPromises();
    const rows = wrapper.findAll('[data-testid="discover-folders"] li');
    await rows[1].find('input[type="checkbox"]').setValue(false); // the one the fake server refuses
    await wrapper.find('[data-testid="discover-create"]').trigger('click');
    await flushPromises();

    const creates = posts.filter((p) => p.url === '/admin/storages');
    expect(creates.map((c) => c.body.name)).toEqual(['arsiv', 'fotograf']);
    expect(pushSpy).toHaveBeenCalledWith({ name: 'storages' });
  });
});
