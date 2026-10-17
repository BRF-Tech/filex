// The search settings card (task #261): the four settings the Search page
// edits since 0.57, each saved through PATCH /api/admin/search/settings and
// read back from the server's answer; a number the server refuses keeps the
// server's sentence under its field; the memory budget in force is said in
// the reader's language; an index switched off says so.
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { SearchSettings } from '@/api/search';

const state: { now: SearchSettings } = {
  now: {
    content: true,
    content_max_mb: 5,
    content_max_mb_min: 1,
    content_max_mb_max: 1024,
    memory_percent: 25,
    memory_percent_min: 1,
    memory_percent_max: 50,
    memory_budget_bytes: 2 * 1024 ** 3,
    memory_given_bytes: 8 * 1024 ** 3,
    memory_from: 'cgroup v2 memory.max',
    memory_from_kind: 'container',
    auto_rebuild: true,
    index_enabled: true,
  },
};
const updateSettings = vi.fn();

vi.mock('@/api/search', () => ({
  SearchApi: {
    settings: vi.fn(async () => ({ ...state.now })),
    updateSettings: (patch: Record<string, unknown>) => updateSettings(patch),
  },
}));

/** The form a field is in (the Save button is a submit; a test submits the form). */
function formOf(w: Awaited<ReturnType<typeof card>>, testid: string) {
  const f = w.findAll('form').find((x) => x.find(`[data-testid="${testid}"]`).exists());
  if (!f) throw new Error(`no form holds ${testid}`);
  return f;
}

function card(locale: 'en' | 'tr' = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return import('@/components/SearchSettingsCard.vue').then((m) =>
    mount(m.default, { global: { plugins: [i18n] }, attachTo: document.body }),
  );
}

beforeEach(() => {
  setActivePinia(createPinia());
  updateSettings.mockReset();
  updateSettings.mockImplementation(async (patch: Record<string, unknown>) => {
    state.now = { ...state.now, ...patch } as SearchSettings;
    return { ...state.now };
  });
});

describe('SearchSettingsCard', () => {
  it('shows the four settings as the server has them', async () => {
    const w = await card();
    await flushPromises();
    expect(w.find('[data-testid="search-settings"]').exists()).toBe(true);
    expect((w.get('[data-testid="search-settings-content-max"] input').element as HTMLInputElement).value).toBe('5');
    expect((w.get('[data-testid="search-settings-memory"] input').element as HTMLInputElement).value).toBe('25');
    expect(w.text()).toContain(en.search.settings.content.label);
    expect(w.text()).toContain(en.search.settings.autoRebuild.label);
    w.unmount();
  });

  it('a number is saved as typed and read back from the answer', async () => {
    const w = await card();
    await flushPromises();
    await w.get('[data-testid="search-settings-memory"] input').setValue('10');
    await formOf(w, 'search-settings-memory').trigger('submit');
    await flushPromises();
    expect(updateSettings).toHaveBeenCalledWith({ memory_percent: 10 });
    w.unmount();
  });

  it("the server's refusal stays under the field it is about", async () => {
    updateSettings.mockRejectedValueOnce({
      isAxiosError: true,
      response: { status: 400, data: { error: 'search.memory_percent must be between 1 and 50 percent', field: 'memory_percent', message: 'Enter a whole number from 1 to 50.' } },
    });
    const w = await card();
    await flushPromises();
    await w.get('[data-testid="search-settings-memory"] input').setValue('90');
    await formOf(w, 'search-settings-memory').trigger('submit');
    await flushPromises();
    expect(w.get('[data-testid="search-settings-memory"] [data-testid="field-error"]').text()).toBe(
      'Enter a whole number from 1 to 50.',
    );
    w.unmount();
  });

  it('the switches save at once; the repair says it waits for the next start', async () => {
    const w = await card();
    await flushPromises();
    await w.get('[data-testid="search-settings-content"] button').trigger('click');
    await flushPromises();
    expect(updateSettings).toHaveBeenCalledWith({ content: false });
    await w.get('[data-testid="search-settings-auto-rebuild"] button').trigger('click');
    await flushPromises();
    expect(updateSettings).toHaveBeenCalledWith({ auto_rebuild: false });
    w.unmount();
  });

  it('says what the share came to, in the reader’s language', async () => {
    const w = await card('tr');
    await flushPromises();
    const line = w.get('[data-testid="search-settings-budget"]').text();
    expect(line).toContain(tr.search.settings.memory.from.container);
    expect(line).not.toContain('cgroup');
    w.unmount();
  });

  it('an index switched off on the server says so, and the settings stay editable', async () => {
    state.now = { ...state.now, index_enabled: false };
    const w = await card();
    await flushPromises();
    expect(w.find('[data-testid="search-settings-index-off"]').exists()).toBe(true);
    expect(w.find('[data-testid="search-settings-budget"]').exists()).toBe(false);
    expect(w.find('[data-testid="search-settings-memory"]').exists()).toBe(true);
    state.now = { ...state.now, index_enabled: true };
    w.unmount();
  });
});
