// The warning under every setting that turns a protection off (#298): one
// sentence, one component, and each setting's own explanation. Pinned on what
// the forms draw, in both languages, and on the catalogues: a setting that
// declares itself on the server has its words here.
import fs from 'node:fs';
import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import ProviderFields from '@/components/ProviderFields.vue';
import StorageDriverFields from '@/components/StorageDriverFields.vue';
import InstanceSettingsCard from '@/components/settings/InstanceSettingsCard.vue';
import SecurityOffNote from '@/components/ui/SecurityOffNote.vue';
import { useStorageDriversStore } from '@/stores/storageDrivers';
import type { StorageDriverDescriptor, StorageField } from '@/api/types';
import type { InstanceSettingItem } from '@/api/instanceSettings';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const skipTLS: StorageField = {
  key: 'insecure_skip_tls_verify',
  type: 'bool',
  label: 'Skip certificate verification (insecure)',
  i18n_key: 'storages.fields.insecureSkipTlsVerify',
  required: false,
  secret: false,
  advanced: false,
  security_off: { risk: 'fallback', risk_i18n_key: 'storages.fieldRisk.insecureSkipTlsVerify' },
};

const tlsField: StorageField = {
  key: 'tls',
  type: 'bool',
  label: 'FTPS (explicit AUTH TLS)',
  i18n_key: 'storages.fields.tls',
  required: false,
  secret: false,
  security_off: { risk: 'fallback', risk_i18n_key: 'storages.fieldRisk.plainFtp', when_off: true },
};

const descriptors: StorageDriverDescriptor[] = [
  {
    driver: 'webdav',
    label: 'WebDAV',
    i18n_key: 'storages.driver.webdav',
    fields: [skipTLS],
    capabilities: {},
  } as unknown as StorageDriverDescriptor,
  {
    driver: 'ftp',
    label: 'FTP',
    i18n_key: 'storages.driver.ftp',
    fields: [tlsField, { ...skipTLS, requires: 'tls' }],
    capabilities: {},
  } as unknown as StorageDriverDescriptor,
  {
    driver: 'future',
    label: 'A driver this catalogue has not heard of',
    i18n_key: 'storages.driver.future',
    fields: [
      {
        key: 'x', type: 'bool', label: 'X', i18n_key: 'storages.fields.future', required: false, secret: false,
        security_off: { risk: 'The driver says this in English.', risk_i18n_key: 'storages.fieldRisk.future' },
      },
    ],
    capabilities: {},
  } as unknown as StorageDriverDescriptor,
];

vi.mock('@/api/storageDrivers', () => ({
  StorageDriversApi: { list: vi.fn(async () => descriptors) },
}));

const ITEMS: InstanceSettingItem[] = [
  { key: 'ftps.allow_plain', group: 'connections', kind: 'bool', value: false, default: false, env: 'FILEX_FTPS_ALLOW_PLAIN', restart: true, restart_pending: false, security_off: true },
  { key: 'notify.enabled', group: 'connections', kind: 'bool', value: true, default: true, restart: true, restart_pending: false },
];
vi.mock('@/api/instanceSettings', async (orig) => ({
  ...(await orig<typeof import('@/api/instanceSettings')>()),
  InstanceSettingsApi: { list: vi.fn(async () => ITEMS), save: vi.fn(async () => ITEMS) },
}));

function i18n(locale = 'en') {
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
}

async function storageForm(driver: string, modelValue: Record<string, unknown> = {}, locale = 'en') {
  const w = mount(StorageDriverFields, { props: { driver, modelValue }, global: { plugins: [i18n(locale)] } });
  await useStorageDriversStore().fetch(true);
  await w.vm.$nextTick();
  return w;
}

const notes = (w: { findAll: (s: string) => { text: () => string }[] }) =>
  w.findAll('[data-testid="security-off-note"]').map((n) => n.text());

describe('SecurityOffNote', () => {
  it('says the fixed sentence and the setting\'s own risk', () => {
    const w = mount(SecurityOffNote, { props: { risk: 'What it exposes.' }, global: { plugins: [i18n('tr')] } });

    expect(w.text()).toContain('Bu ayar güvenli değildir ve önerilmez. Ne yaptığınızı biliyorsanız uygulayın.');
    expect(w.text()).toContain('What it exposes.');
  });
});

describe('the storage form', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('stands under a switch that takes a protection away, on or off', async () => {
    for (const value of [false, true]) {
      const w = await storageForm('webdav', { insecure_skip_tls_verify: value });
      expect(notes(w)).toHaveLength(1);
      expect(notes(w)[0]).toContain(en.securityOff.note);
      expect(notes(w)[0]).toContain(en.storages.fieldRisk.insecureSkipTlsVerify);
    }
  });

  it('is Turkish in Turkish', async () => {
    const w = await storageForm('webdav', {}, 'tr');
    expect(notes(w)[0]).toContain(tr.securityOff.note);
    expect(notes(w)[0]).toContain(tr.storages.fieldRisk.insecureSkipTlsVerify);
  });

  it('stands under a switch that IS the protection only while it is off', async () => {
    const off = await storageForm('ftp', { tls: false });
    expect(notes(off)).toHaveLength(1);
    expect(notes(off)[0]).toContain(en.storages.fieldRisk.plainFtp);

    const on = await storageForm('ftp', { tls: true });
    expect(notes(on).join(' ')).not.toContain(en.storages.fieldRisk.plainFtp);
  });

  it('offers the certificate switch on FTP only with FTPS', async () => {
    const plain = await storageForm('ftp', { tls: false });
    expect(plain.text()).not.toContain(en.storages.fields.insecureSkipTlsVerify);

    const ftps = await storageForm('ftp', { tls: true });
    expect(ftps.text()).toContain(en.storages.fields.insecureSkipTlsVerify);
    expect(notes(ftps).join(' ')).toContain(en.storages.fieldRisk.insecureSkipTlsVerify);
  });

  it('falls back to the driver\'s English for a risk the catalogue lacks', async () => {
    const w = await storageForm('future');
    expect(notes(w)[0]).toContain('The driver says this in English.');
    expect(notes(w)[0]).toContain(en.securityOff.note);
  });
});

describe('the provider form', () => {
  it('stands under a field the server marks, in its own words', () => {
    const w = mount(ProviderFields, {
      props: {
        fields: [
          { key: 'trust_email', kind: 'bool', security_off: true },
          { key: 'show_refusal_reason', kind: 'bool', security_off: true },
          { key: 'auto_create', kind: 'bool' },
        ],
        values: { trust_email: false, show_refusal_reason: false, auto_create: true },
        prefix: 't',
      },
      global: { plugins: [i18n()] },
    });

    expect(notes(w)).toHaveLength(2);
    expect(notes(w)[0]).toContain(en.authProviders.fieldRisks.trust_email);
    expect(notes(w)[1]).toContain(en.authProviders.fieldRisks.show_refusal_reason);
  });
});

// The server asked for a saved secret again (SECRET_NEEDED): said under the
// box it is about, until something is typed in it (#298).
describe('a secret the server asks for again', () => {
  const said = { field: 'bind_password', message: 'The address changed: type bind_password again.' };
  const mountWith = (values: Record<string, string | boolean>) =>
    mount(ProviderFields, {
      props: {
        fields: [
          { key: 'url', kind: 'text' },
          { key: 'bind_password', kind: 'secret' },
        ],
        values,
        secretsSet: { bind_password: true },
        prefix: 'p',
        secretNeeded: said,
      },
      global: { plugins: [i18n()] },
    });

  it('stands under the secret box', () => {
    const w = mountWith({ url: 'ldaps://new.example.com', bind_password: '' });
    const errors = w.findAll('[data-testid="field-error"]').map((e) => e.text());
    expect(errors).toEqual([said.message]);
  });

  it('goes once something is typed there', () => {
    const w = mountWith({ url: 'ldaps://new.example.com', bind_password: 'typed' });
    expect(w.find('[data-testid="field-error"]').exists()).toBe(false);
  });
});

describe('the instance settings card', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('draws the row with its title, its risk and its variable', async () => {
    const w = mount(InstanceSettingsCard, { props: { group: 'connections' }, global: { plugins: [i18n()] } });
    await flushPromises();

    expect(w.text()).toContain(en.instanceSettings.groups.connections.title);
    expect(w.text()).toContain('FILEX_FTPS_ALLOW_PLAIN');
    expect(notes(w)).toHaveLength(1);
    expect(notes(w)[0]).toContain(en.instanceSettings.items.ftps_allow_plain.risk);
  });

  // ⚠ The note must not sit inside the row's v-if chain: there it restarted the
  // chain, and every switch without a note drew the language select under it
  // too, with a raw key for a label. Seen in the browser, not in a test.
  it('draws a switch and nothing else under it', async () => {
    const w = mount(InstanceSettingsCard, { props: { group: 'connections' }, global: { plugins: [i18n()] } });
    await flushPromises();

    expect(w.findAll('[role="switch"]')).toHaveLength(2);
    expect(w.findAll('[role="combobox"]')).toHaveLength(0);
    expect(w.findAll('input')).toHaveLength(0);
    expect(w.text()).not.toContain('instanceSettings.items');
  });
});

// The server declares; these hold the two sides together.
describe('what the server declares', () => {
  const backend = path.resolve(__dirname, '../../../backend/internal');
  const read = (rel: string) => fs.readFileSync(path.join(backend, rel), 'utf8');
  const lookup = (table: unknown, dotted: string): unknown =>
    dotted.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown> | undefined)?.[k], table);

  const DRIVERS = ['storage/tlsverify.go', 'storage/drivers/ftp/descriptor.go', 'storage/drivers/sftp/descriptor.go', 'storage/drivers/local/descriptor.go'];

  it('every risk a driver names is in both catalogues, and its English is the driver\'s', () => {
    const seen: string[] = [];
    for (const file of DRIVERS) {
      const src = read(file).replace(/"\s*\+\s*\n\s*"/g, '');
      for (const m of src.matchAll(/Risk:\s+"((?:[^"\\]|\\.)*)",\s*\n\s*RiskI18nKey:\s+"([^"]+)"/g)) {
        const [, english, key] = m;
        seen.push(key);
        expect(lookup(en, key), key).toBe(english);
        expect(lookup(tr, key), key).toBeTruthy();
        expect(lookup(tr, key), key).not.toBe(english);
      }
    }
    expect(seen.sort()).toEqual([
      'storages.fieldRisk.followSymlinks',
      'storages.fieldRisk.insecureSkipHostKey',
      'storages.fieldRisk.insecureSkipTlsVerify',
      'storages.fieldRisk.plainFtp',
    ]);
  });

  it('every provider field the schema marks has its risk in both catalogues', () => {
    const src = read('authsetup/page.go');
    const keys = new Set([...src.matchAll(/Key: "(\w+)",[^\n]*SecurityOff: true/g)].map((m) => m[1]));
    expect([...keys].sort()).toEqual(['show_refusal_reason', 'trust_email']);
    for (const k of keys) {
      expect(lookup(en, `authProviders.fieldRisks.${k}`), k).toBeTruthy();
      expect(lookup(tr, `authProviders.fieldRisks.${k}`), k).toBeTruthy();
    }
  });

  it('every instance setting marked has a risk in both catalogues', () => {
    const src = read('instancesettings/instancesettings.go');
    const keys = [...src.matchAll(/Key: "([\w.]+)",[^\n]*SecurityOff: true/g)].map((m) => m[1]);
    expect(keys).toEqual(['ftps.allow_plain']);
    for (const k of keys) {
      const slug = k.replace(/\./g, '_');
      expect(lookup(en, `instanceSettings.items.${slug}.risk`), k).toBeTruthy();
      expect(lookup(tr, `instanceSettings.items.${slug}.risk`), k).toBeTruthy();
    }
  });
});
