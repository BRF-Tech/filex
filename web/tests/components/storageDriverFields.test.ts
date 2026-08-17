// Renders StorageDriverFields against a descriptor and asserts what ends
// up ON SCREEN — labels, input types, emitted config — not just the props
// it was handed. The form this replaced looked right in code review too;
// what it actually rendered was a field set the backend never read.
import { describe, it, expect, beforeEach, vi } from 'vitest';
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
    driver: 's3',
    label: 'S3 / Hetzner / MinIO',
    i18n_key: 'storages.driver.s3',
    capabilities: { read: true, write: true, presign: true },
    fields: [
      { key: 'bucket', type: 'string', label: 'Bucket', i18n_key: 'storages.fields.bucket', required: true, secret: false, placeholder: 'my-bucket' },
      { key: 'prefix', type: 'string', label: 'Prefix', i18n_key: 'storages.fields.prefix', required: true, secret: false, root: true, monospace: true },
      { key: 'secret_key', type: 'password', label: 'Secret key', i18n_key: 'storages.fields.secretKey', required: false, secret: true },
      { key: 'path_style', type: 'bool', label: 'Use path-style URLs', i18n_key: 'storages.fields.pathStyle', required: false, secret: false, default: true },
      { key: 'disable_presign', type: 'bool', label: 'Disable presigned URLs', i18n_key: 'storages.fields.disablePresign', required: false, secret: false, advanced: true },
    ],
  },
  {
    driver: 'sftp',
    label: 'SFTP',
    i18n_key: 'storages.driver.sftp',
    capabilities: { read: true, write: true },
    fields: [
      { key: 'host', type: 'string', label: 'Host', i18n_key: 'storages.fields.host', required: true, secret: false },
      { key: 'port', type: 'int', label: 'Port', i18n_key: 'storages.fields.port', required: false, secret: false, default: 22, min: 1, max: 65535 },
      { key: 'root', type: 'string', label: 'Base path', i18n_key: 'storages.fields.root', required: true, secret: false, root: true, aliases: ['base_path', 'remote_path'] },
      // A driver shipped after this release: no i18n key in the catalogue,
      // so the English fallback the backend sent must be rendered.
      { key: 'future_knob', type: 'string', label: 'Future knob', i18n_key: 'storages.fields.futureKnob', required: false, secret: false },
    ],
  },
];

vi.mock('@/api/storageDrivers', () => ({
  StorageDriversApi: { list: vi.fn(async () => descriptors) },
}));

function mountFields(driver: string, modelValue: Record<string, unknown> = {}, locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(StorageDriverFields, {
    props: { driver, modelValue },
    global: { plugins: [i18n] },
  });
}

async function ready(w: ReturnType<typeof mountFields>) {
  const store = useStorageDriversStore();
  await store.fetch(true);
  await w.vm.$nextTick();
  return w;
}

describe('StorageDriverFields', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it('renders the fields the driver declares, with translated labels', async () => {
    const w = await ready(mountFields('s3'));
    const text = w.text();
    expect(text).toContain('Bucket');
    expect(text).toContain('Prefix'); // the field the old form never had
    expect(text).toContain('Secret key');
    // Advanced fields stay collapsed until asked for.
    expect(text).not.toContain('Disable presigned URLs');
    expect(w.text()).toContain('Advanced settings');
  });

  it('renders Turkish labels when the locale is tr', async () => {
    const w = await ready(mountFields('s3', {}, 'tr'));
    expect(w.text()).toContain('Önek (prefix)');
    expect(w.text()).not.toContain('Prefix"'); // no raw key leakage
  });

  it('falls back to the backend English label when the catalogue lacks the key', async () => {
    const w = await ready(mountFields('sftp'));
    expect(w.text()).toContain('Future knob');
    expect(w.text()).not.toContain('storages.fields.futureKnob');
  });

  it('masks secrets and renders ints as number inputs', async () => {
    const s3 = await ready(mountFields('s3'));
    expect(s3.find('input[type="password"]').exists()).toBe(true);
    const sftp = await ready(mountFields('sftp'));
    const number = sftp.find('input[type="number"]');
    expect(number.exists()).toBe(true);
    expect(number.attributes('max')).toBe('65535');
  });

  it('shows advanced fields once expanded', async () => {
    const w = await ready(mountFields('s3'));
    // Not w.find('button') — the first button on screen is the path-style
    // toggle switch, which would have flipped a config value instead.
    const disclosure = w.findAll('button').find((b) => b.text().includes('Advanced settings'));
    expect(disclosure, 'advanced disclosure button').toBeTruthy();
    await disclosure!.trigger('click');
    expect(w.text()).toContain('Disable presigned URLs');
  });

  it('emits the config under the key the driver reads', async () => {
    const w = await ready(mountFields('s3'));
    const inputs = w.findAll('input[type="text"]');
    await inputs[1].setValue('fileman'); // prefix
    const emitted = w.emitted('update:modelValue');
    expect(emitted).toBeTruthy();
    expect(emitted!.at(-1)![0]).toMatchObject({ prefix: 'fileman' });
  });

  it('shows a legacy alias value in the field that replaced it', async () => {
    // A row saved by the old form carries base_path; the operator must
    // see it in "Base path", not an empty box.
    const w = await ready(mountFields('sftp', { base_path: '/srv/files' }));
    const values = w.findAll('input').map((i) => (i.element as HTMLInputElement).value);
    expect(values).toContain('/srv/files');
  });

  it('says so when the catalogue has nothing for a driver', async () => {
    const w = await ready(mountFields('unknown-driver'));
    expect(w.text()).toContain('unknown-driver');
  });
});

describe('storageDrivers store', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('seeds defaults from the descriptor and never carries keys across drivers', async () => {
    const store = useStorageDriversStore();
    await store.fetch(true);
    expect(store.defaults('s3')).toEqual({
      bucket: '',
      prefix: '',
      secret_key: '',
      path_style: true,
      disable_presign: false,
    });
    expect(store.defaults('sftp')).toEqual({
      host: '',
      port: 22,
      root: '',
      future_knob: '',
    });
    expect(Object.keys(store.defaults('sftp'))).not.toContain('bucket');
  });

  it('exposes every registered driver for the picker', async () => {
    const store = useStorageDriversStore();
    await store.fetch(true);
    expect(store.names).toEqual(['s3', 'sftp']);
  });
});
