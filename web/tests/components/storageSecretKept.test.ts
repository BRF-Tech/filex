// A saved credential is never sent back by the server (it answers "***"), and
// the storage form shows it the way that means: an empty field that says it is
// kept - type to change it. Left alone, or emptied again, it goes back as "***",
// which the server reads as "keep the saved one".
//
// ⚠ Drawn as a value, the mask would sit in the password box as three
// characters; somebody typing one more would save "***x" as the password.
// And the edit page's "Test connection" names the storage, so a kept password
// is tested with the one the server holds.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import StorageDriverFields from '@/components/StorageDriverFields.vue';
import { useStorageDriversStore } from '@/stores/storageDrivers';
import type { StorageDriverDescriptor } from '@/api/types';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const descriptors: StorageDriverDescriptor[] = [
  {
    driver: 'smb',
    label: 'SMB',
    i18n_key: 'storages.driver.smb',
    capabilities: { read: true, write: true },
    fields: [
      { key: 'host', type: 'string', label: 'Host', i18n_key: 'storages.fields.host', required: true, secret: false },
      { key: 'password', type: 'password', label: 'Password', i18n_key: 'storages.fields.password', required: true, secret: true },
    ],
  },
];

vi.mock('@/api/storageDrivers', () => ({
  StorageDriversApi: { list: vi.fn(async () => descriptors) },
}));

async function mountFields(modelValue: Record<string, unknown>, locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(StorageDriverFields, { props: { driver: 'smb', modelValue }, global: { plugins: [i18n] } });
  await useStorageDriversStore().fetch(true);
  await w.vm.$nextTick();
  return w;
}

function passwordInput(w: Awaited<ReturnType<typeof mountFields>>) {
  return w.get('input[type="password"]');
}

describe('a saved credential on the storage form', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('is an empty field that says it is kept, never the mask as a value', async () => {
    const w = await mountFields({ host: 'u1.your-storagebox.de', password: '***' });
    const input = passwordInput(w);
    expect((input.element as HTMLInputElement).value).toBe('');
    expect(input.attributes('placeholder')).toBe(en.storages.secretKept);
    expect(input.attributes('required'), 'a kept password is not a missing one').toBeUndefined();
  });

  it('takes a new value, and goes back to "keep" when emptied again', async () => {
    const w = await mountFields({ host: 'h', password: '***' });
    await passwordInput(w).setValue('yeni-parola');
    expect(w.emitted('update:modelValue')!.at(-1)![0]).toMatchObject({ password: 'yeni-parola' });
    await w.setProps({ modelValue: { host: 'h', password: 'yeni-parola' } });
    await passwordInput(w).setValue('');
    expect(w.emitted('update:modelValue')!.at(-1)![0]).toMatchObject({ password: '***' });
  });

  it('says it in Turkish with its own letters', async () => {
    const w = await mountFields({ host: 'h', password: '***' }, 'tr');
    expect(passwordInput(w).attributes('placeholder')).toBe('Kayıtlı - değiştirmek için yazın');
  });

  it('a field that never had a value is still required and empty', async () => {
    const w = await mountFields({ host: 'h' });
    expect(passwordInput(w).attributes('placeholder')).not.toBe(en.storages.secretKept);
  });
});
