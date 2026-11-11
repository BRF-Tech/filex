// The question asked before an app's data is erased (filex 0.60): by
// Reconfigure, and by removing the app. What goes is COUNTED in the server's
// numbers, the app's own warning is said beside it, and the administrator
// TYPES the app's name before the button does anything.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

let eraseAnswer: Record<string, unknown> = {};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/app-plugins/7/erase') return { data: eraseAnswer };
      return { data: {} };
    }),
    post: vi.fn(async (url: string) => {
      if (url === '/admin/app-plugins/7/reconfigure') return { data: { plugin: { ...plugin, state: 'setup' } } };
      return { data: {} };
    }),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(async () => ({ data: {} })),
  },
}));

import AppEraseDialog from '@/components/plugins/AppEraseDialog.vue';

enableAutoUnmount(afterEach);

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

const plugin = {
  id: 7, name: 'shortlink', version: '0.1.0', label: { en: 'Short links', tr: 'Kısa bağlantılar' }, enabled: true,
  state: 'running', source: 'github', signed: true, permissions: ['data'], scheduled: false, actions: 0, views: 2,
  public_pages: 0, created_at: '', updated_at: '',
};

function mountDialog(mode: 'reconfigure' | 'remove', locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(AppEraseDialog, {
    props: { modelValue: true, plugin, mode },
    global: { plugins: [i18n] },
    attachTo: document.body,
  });
}

describe('AppEraseDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    eraseAnswer = {
      counts: { data_keys: 1523, data_bytes: 2_400_000, state_rows: 0, locks: 0, links: 3 },
      warning: { en: 'Every short link stops working.', tr: 'Bütün kısa bağlantılar çalışmaz olur.' },
      confirm: 'shortlink',
    };
  });

  it('counts what goes, says the app’s own warning, and leaves out what is not there', async () => {
    const w = mountDialog('reconfigure');
    await flushPromises();
    expect(w.get('[data-testid="app-erase-data"]').text()).toContain('1,523');
    expect(w.get('[data-testid="app-erase-data"]').text()).toContain('2.4 MB');
    expect(w.get('[data-testid="app-erase-links"]').text()).toContain('3 links');
    expect(w.find('[data-testid="app-erase-state"]').exists()).toBe(false);
    expect(w.find('[data-testid="app-erase-locks"]').exists()).toBe(false);
    expect(w.get('[data-testid="app-erase-warning"]').text()).toBe('Every short link stops working.');
  });

  it('the button does nothing until the name is typed exactly', async () => {
    const { api } = await import('@/api/client');
    const w = mountDialog('reconfigure');
    await flushPromises();
    const button = w.get('[data-testid="app-erase-confirm"]');
    expect(button.attributes('disabled')).toBeDefined();
    const box = w.get('input[name="app-erase-confirm"]');
    for (const almost of ['short', 'Shortlink', 'shortlink ', ' shortlink']) {
      await box.setValue(almost);
      expect(w.get('[data-testid="app-erase-confirm"]').attributes('disabled'), `"${almost}"`).toBeDefined();
    }
    await w.get('[data-testid="app-erase-confirm"]').trigger('click');
    expect(api.post).not.toHaveBeenCalled();

    await box.setValue('shortlink');
    expect(w.get('[data-testid="app-erase-confirm"]').attributes('disabled')).toBeUndefined();
    await w.get('[data-testid="app-erase-confirm"]').trigger('click');
    await flushPromises();
    expect(api.post).toHaveBeenCalledWith('/admin/app-plugins/7/reconfigure', { confirm: 'shortlink' });
    expect(w.emitted('done')?.[0]?.[0]).toMatchObject({ name: 'shortlink', state: 'setup' });
  });

  it('removing sends the typed name with the delete', async () => {
    const { api } = await import('@/api/client');
    const w = mountDialog('remove');
    await flushPromises();
    await w.get('input[name="app-erase-confirm"]').setValue('shortlink');
    await w.get('[data-testid="app-erase-confirm"]').trigger('click');
    await flushPromises();
    expect(api.delete).toHaveBeenCalledWith('/admin/app-plugins/7', { data: { confirm: 'shortlink' } });
    expect(w.emitted('done')?.[0]?.[0]).toBeNull();
  });

  it('an app with nothing kept says so, and still asks for the name', async () => {
    eraseAnswer = { counts: { data_keys: 0, data_bytes: 0, state_rows: 0, locks: 0, links: 0 }, warning: null, confirm: 'shortlink' };
    const w = mountDialog('remove', 'tr');
    await flushPromises();
    expect(w.get('[data-testid="app-erase-nothing"]').text()).toBe(tr.appPlugins.erase.nothing);
    expect(w.get('[data-testid="app-erase-confirm"]').attributes('disabled')).toBeDefined();
  });
});
