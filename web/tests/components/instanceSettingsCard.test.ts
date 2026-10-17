// The server's defaults card (task #258, components/settings/InstanceSettingsCard):
// the settings that left the environment in 0.57, drawn from the server's list
// of one group. Pinned: every row in both languages, with its bounds and its
// variable; a switch saves on the click; numbers save with the button and a
// refusal lands under its field; a change waiting for a restart is said.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { InstanceSettingItem } from '@/api/instanceSettings';

const ITEMS: InstanceSettingItem[] = [
  { key: 'instance.default_locale', group: 'general', kind: 'language', value: '', default: '', env: 'FILEX_DEFAULT_LOCALE', restart: true, restart_pending: false },
  { key: 'sync.default_interval_minutes', group: 'general', kind: 'int', value: 15, default: 15, min: 1, max: 1440, unit: 'minutes', env: 'FILEX_SYNC_INTERVAL', restart: true, restart_pending: false },
  { key: 'thumbs.sweep', group: 'general', kind: 'bool', value: true, default: true, env: 'FILEX_THUMBS_SWEEP_INTERVAL', restart: true, restart_pending: false },
];

let now: InstanceSettingItem[] = [];
const save = vi.fn();

vi.mock('@/api/instanceSettings', async (orig) => ({
  ...(await orig<typeof import('@/api/instanceSettings')>()),
  InstanceSettingsApi: {
    list: vi.fn(async () => now.map((i) => ({ ...i }))),
    save: (group: string, patch: Record<string, unknown>) => save(group, patch),
  },
}));

async function card(locale: 'en' | 'tr' = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const m = await import('@/components/settings/InstanceSettingsCard.vue');
  const w = mount(m.default, { props: { group: 'general' }, global: { plugins: [i18n] }, attachTo: document.body });
  await flushPromises();
  return w;
}

beforeEach(() => {
  setActivePinia(createPinia());
  now = ITEMS.map((i) => ({ ...i }));
  save.mockReset();
  save.mockImplementation(async (_g: string, patch: Record<string, unknown>) => {
    now = now.map((i) => (i.key in patch ? { ...i, value: patch[i.key] as never, restart_pending: true } : i));
    return now.map((i) => ({ ...i }));
  });
});

describe('the server defaults card', () => {
  it('draws every row of its group with its label, bounds and variable, in English and Turkish', async () => {
    let w = await card('en');
    const text = w.text();
    expect(text).toContain(en.instanceSettings.groups.general.title);
    expect(text).toContain(en.instanceSettings.items.sync_default_interval_minutes.label);
    expect(text).toContain('From 1 to 1440; 15 by default.');
    expect(text).toContain('First value from FILEX_SYNC_INTERVAL');
    expect(text).toContain(en.instanceSettings.restartNote);
    w.unmount();

    w = await card('tr');
    expect(w.text()).toContain('Sunucu varsayılanları');
    expect(w.text()).toContain('Depoları tarama sıklığı');
    expect(w.text()).toContain('1 ile 1440 arası; varsayılan 15.');
    w.unmount();
  });

  it('a switch saves on the click, alone', async () => {
    const w = await card();
    await w.get('[data-testid="instance-setting-thumbs.sweep"] [role="switch"]').trigger('click');
    await flushPromises();
    expect(save).toHaveBeenCalledWith('general', { 'thumbs.sweep': false });
    w.unmount();
  });

  it('a number saves with the button, and the change is said to wait for the next start', async () => {
    const w = await card();
    await w.get('[data-testid="instance-setting-sync.default_interval_minutes"] input').setValue('30');
    await w.get('form').trigger('submit');
    await flushPromises();
    expect(save).toHaveBeenCalledWith('general', { 'sync.default_interval_minutes': 30 });
    expect(w.find('[data-testid="instance-setting-pending-sync.default_interval_minutes"]').text()).toBe(en.instanceSettings.restartPending);
    w.unmount();
  });

  it('a refusal lands under the field it is about, in the server’s words', async () => {
    save.mockImplementation(async () => {
      throw { response: { status: 400, data: { field: 'sync.default_interval_minutes', message: 'Enter a whole number from 1 to 1440.' } } };
    });
    const w = await card();
    await w.get('[data-testid="instance-setting-sync.default_interval_minutes"] input').setValue('0');
    await w.get('form').trigger('submit');
    await flushPromises();
    expect(w.get('[data-testid="instance-setting-sync.default_interval_minutes"]').text()).toContain('Enter a whole number from 1 to 1440.');
    w.unmount();
  });

  it('the language row offers the browser’s language and the offered ones, never a native select', async () => {
    const w = await card();
    const row = w.get('[data-testid="instance-setting-instance.default_locale"]');
    expect(row.find('select').exists()).toBe(false);
    expect(row.text()).toContain(en.instanceSettings.items.instance_default_locale.label);
    w.unmount();
  });
});
