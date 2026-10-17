// An app's Dictionaries section (filex 0.57, components/plugins/
// AppPluginDictionaries.vue): what an administrator adds for the office
// editor's languages, where each file is sent, and when nothing can be added.
//
// RED on the code before 0.57: the component and the API calls did not
// exist, and the app's page had no way to add a dictionary.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';

let extras: Record<string, unknown> = {};
let catalog: Record<string, unknown> = { kind: 'dictionaries', items: [] };
const put = vi.fn(async (url: string) => ({ data: { path: url, kind: 'dictionaries', size: 1, modified: '' } }));
const del = vi.fn(async () => ({}));
const post = vi.fn(async (_url: string) => ({ data: { files: [] } }));

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url.endsWith('/extras')) return { data: extras };
      if (url.endsWith('/extras-catalog/dictionaries')) return { data: catalog };
      throw new Error('unexpected GET ' + url);
    }),
    post: (url: string) => post(url),
    put: (url: string, ...rest: unknown[]) => put(url, ...(rest as [])),
    delete: (url: string) => del(url),
  },
}));

import AppPluginDictionaries from '@/components/plugins/AppPluginDictionaries.vue';

const global = () => ({
  plugins: [createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } })],
  stubs: {
    // The shared table and the dialog are measured where they live; here
    // the rows and the dialog's form are what is asked.
    DataTable: {
      props: ['rows', 'rowActions'],
      emits: ['row-action'],
      template:
        '<div data-testid="rows"><template v-for="r in rows" :key="r.lang"><div class="row">{{ r.lang }}:{{ r.parts.join(",") }}</div>' +
        `<button v-for="a in (rowActions ? rowActions(r) : [])" :key="a.key" class="act" :data-act="r.lang + ':' + a.key" @click="$emit('row-action', a.key, r)">{{ a.key }}</button></template></div>`,
    },
    Modal: { props: ['modelValue'], template: '<div v-if="modelValue"><slot /><slot name="footer" /></div>' },
  },
});

function answer(read: boolean, files: unknown[] = []) {
  return { files, bytes: 0, max_file_bytes: 128 << 20, max_bytes: 1 << 30, kinds: ['dictionaries'], read };
}

async function pick(w: ReturnType<typeof mount>, part: string, name: string) {
  const input = w.find(`[data-testid="app-dictionaries-${part}"]`);
  const file = new File(['x'], name, { type: '' });
  Object.defineProperty(input.element, 'files', { value: [file], configurable: true });
  await input.trigger('change');
}

describe('the Dictionaries section', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    put.mockClear();
    del.mockClear();
    post.mockClear();
    catalog = { kind: 'dictionaries', items: [] };
  });

  it('lists what was added, one row per language', async () => {
    extras = answer(true, [
      { path: 'dictionaries/pl_PL/pl_PL.aff.txt', kind: 'dictionaries', size: 1, modified: '2026-10-16T08:00:00Z' },
      { path: 'dictionaries/pl_PL/pl_PL.dic.txt', kind: 'dictionaries', size: 2, modified: '2026-10-16T08:00:00Z' },
    ]);
    const w = mount(AppPluginDictionaries, { props: { pluginId: 7 }, global: global() });
    await flushPromises();
    expect(w.findAll('.row').map((r) => r.text())).toEqual(['pl_PL:aff,dic']);
    expect(w.find('[data-testid="app-dictionaries-add"]').exists()).toBe(true);
  });

  it('sends the spelling rules first, then the word list, each where the editor reads it', async () => {
    extras = answer(true);
    const w = mount(AppPluginDictionaries, { props: { pluginId: 7 }, global: global() });
    await flushPromises();
    await w.find('[data-testid="app-dictionaries-add"]').trigger('click');
    await w.find('[data-testid="app-dictionaries-lang"] input').setValue('pl_PL');
    await pick(w, 'dic', 'pl_PL.dic');
    await pick(w, 'aff', 'pl_PL.aff');
    await w.find('[data-testid="app-dictionaries-save"]').trigger('click');
    await flushPromises();
    expect(put.mock.calls.map((c) => c[0])).toEqual([
      '/admin/app-plugins/7/extras/dictionaries/pl_PL/pl_PL.aff.txt',
      '/admin/app-plugins/7/extras/dictionaries/pl_PL/pl_PL.dic.txt',
    ]);
  });

  it('asks for both files and a language folder before it sends anything', async () => {
    extras = answer(true);
    const w = mount(AppPluginDictionaries, { props: { pluginId: 7 }, global: global() });
    await flushPromises();
    await w.find('[data-testid="app-dictionaries-add"]').trigger('click');
    await w.find('[data-testid="app-dictionaries-lang"] input').setValue('pl-PL');
    await pick(w, 'dic', 'pl_PL.dic');
    await w.find('[data-testid="app-dictionaries-save"]').trigger('click');
    await flushPromises();
    expect(put).not.toHaveBeenCalled();
    expect(w.find('[data-testid="app-dictionaries-error"]').text()).toBe(en.appPlugins.detail.dictionaries.needBoth);
    expect(w.text()).toContain(en.appPlugins.detail.dictionaries.langInvalid);
  });

  it('offers nothing to add where the interface cannot read it', async () => {
    extras = answer(false);
    const w = mount(AppPluginDictionaries, { props: { pluginId: 7 }, global: global() });
    await flushPromises();
    expect(w.find('[data-testid="app-dictionaries-add"]').exists()).toBe(false);
    expect(w.find('[data-testid="app-dictionaries-unread"]').exists()).toBe(true);
  });

  // filex 0.57: the release's catalog. Every language it offers is a row; one
  // click installs it (the server downloads and checks every file).
  it('lists the languages the release offers beside the added ones, and installs one with a click', async () => {
    extras = { ...answer(true, [
      { path: 'dictionaries/pl_PL/pl_PL.aff.txt', kind: 'dictionaries', size: 1, modified: '2026-10-16T08:00:00Z' },
      { path: 'dictionaries/pl_PL/pl_PL.dic.txt', kind: 'dictionaries', size: 2, modified: '2026-10-16T08:00:00Z' },
    ]), catalogs: ['dictionaries'] };
    catalog = { kind: 'dictionaries', items: [
      { id: 'de_AT', size: 10, files: 2, installed: false },
      { id: 'pl_PL', size: 3, files: 2, installed: true },
    ] };
    const w = mount(AppPluginDictionaries, { props: { pluginId: 7 }, global: global() });
    await flushPromises();
    expect(w.findAll('.row').map((r) => r.text())).toEqual(['de_AT:', 'pl_PL:aff,dic']);
    expect(w.find('[data-act="pl_PL:install"]').exists()).toBe(false);
    expect(w.find('[data-act="pl_PL:remove"]').exists()).toBe(true);
    expect(w.find('[data-act="de_AT:remove"]').exists(), 'nothing of it is added yet').toBe(false);
    await w.find('[data-act="de_AT:install"]').trigger('click');
    await flushPromises();
    expect(post.mock.calls.map((c) => c[0])).toEqual(['/admin/app-plugins/7/extras-catalog/dictionaries/de_AT']);
  });

  it('asks for no catalog where the release offers none', async () => {
    extras = answer(true);
    mount(AppPluginDictionaries, { props: { pluginId: 7 }, global: global() });
    await flushPromises();
    expect(post).not.toHaveBeenCalled();
  });
});
