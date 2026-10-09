// What a store sends is text, wherever the panel draws it: the store's
// address and key ids on the trust question, the app's name, repository and
// release on the review, the licensee and the last error on an app's License
// section, the app names on the warning band. None of it is markup and none
// of it is an address the browser loads: the store surfaces have no v-html,
// no innerHTML and no image or icon taken from a store (a store's media is
// its own site's business, not the panel's).
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const SRC = path.resolve(__dirname, '../../src');
const SURFACES = [
  'views/StoreInstall.vue',
  // The review a store link opens: the app's name, repository, release and
  // the store's origin (store fe review #9a).
  'components/plugins/AppPluginInstallWizard.vue',
  // A storage plugin's review (#215): its name, its build, the server's
  // sentences. The release notes go through ReleaseNotes, the sanitized
  // Markdown every feed's notes take.
  'components/plugins/StorageStoreReview.vue',
  'components/plugins/AppPluginLicense.vue',
  'components/plugins/AppStoresPanel.vue',
  'components/AppLicenseAlert.vue',
  'lib/storeLink.ts',
  'lib/storeRefusal.ts',
  'api/appStore.ts',
];

const EVIL = '<img src=x onerror="window.__pwned=1">';

let licenseAnswer: Record<string, unknown> = {};
let intentAnswer: { status: number; data: unknown } = { status: 200, data: {} };

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url.endsWith('/license')) return { data: licenseAnswer };
      if (url.endsWith('/licenses')) return { data: { licenses: [licenseAnswer] } };
      if (url.endsWith('/stores')) return { data: { stores: [{ origin: EVIL, source: 'admin', approved_by_name: EVIL, keys: [{ id: EVIL, use: 'index', fingerprint: 'ab'.repeat(32) }] }] } };
      throw new Error('unexpected GET ' + url);
    }),
    post: vi.fn(async () => {
      if (intentAnswer.status !== 200) {
        throw Object.assign(new Error('refused'), { isAxiosError: true, response: intentAnswer });
      }
      return { data: intentAnswer.data };
    }),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

import StoreInstall from '@/views/StoreInstall.vue';
import AppPluginLicense from '@/components/plugins/AppPluginLicense.vue';
import AppStoresPanel from '@/components/plugins/AppStoresPanel.vue';
import AppLicenseAlert from '@/components/AppLicenseAlert.vue';
import { unmountAll } from '../helpers/teardown';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

function i18n() {
  return createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en, tr } });
}

const global = () => ({ plugins: [i18n()], stubs: { RouterLink: { template: '<a><slot /></a>' } } });

function noInjectedMarkup() {
  expect(document.body.querySelector('img')).toBeNull();
  expect((window as unknown as { __pwned?: number }).__pwned).toBeUndefined();
}

describe('store surfaces draw a store’s words as text', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    sessionStorage.clear();
    unmountAll();
  });

  it('no v-html, innerHTML, image or icon address in the store surfaces', () => {
    for (const f of SURFACES) {
      const src = readFileSync(path.join(SRC, f), 'utf8');
      expect(src, f).not.toMatch(/v-html|innerHTML|outerHTML|insertAdjacentHTML|<img\b|:src=|url\(/);
    }
  });

  it('the trust question', async () => {
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://s.example', token: 'tok_0123456789', at: Date.now() }));
    intentAnswer = {
      status: 409,
      data: { error: 'store_key_changed', message: EVIL, detail: { store: EVIL, keys: [{ id: EVIL, use: EVIL, status: EVIL, fingerprint: EVIL }], previous_keys: [{ id: EVIL, use: 'index', fingerprint: EVIL }], fingerprints: [] } },
    };
    const w = mount(StoreInstall, { global: global(), attachTo: document.body });
    await flushPromises();
    expect(w.find('[data-testid="store-trust-origin"]').text()).toBe(EVIL);
    expect(w.text()).toContain(EVIL);
    noInjectedMarkup();
  });

  it('a storage plugin’s review (#215)', async () => {
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://s.example', token: 'tok_0123456789', at: Date.now() }));
    intentAnswer = {
      status: 200,
      data: {
        handle: 'h', store: EVIL, store_trust: 'admin',
        intent: { store: EVIL, token_id: 't', app: EVIL, kind: 'storage', version: EVIL, repo: EVIL, ref: EVIL, paid: false, expires_at: '2026-10-08T18:30:00Z' },
        storage_review: {
          name: EVIL, version: EVIL, platform: EVIL, platforms: [EVIL], sha256: EVIL, url: EVIL, feed_url: EVIL, source: EVIL, paid: false,
          capabilities: [{ id: 'write', label: EVIL }],
          signature: { store_signed: true, publisher_signed: false, required: false, verifies: false },
          notices: [{ level: 'warning', text: EVIL }],
          can_install: true,
        },
      },
    };
    mount(StoreInstall, { global: global(), attachTo: document.body });
    await flushPromises();
    expect(document.body.querySelector('[data-testid="storage-store-notices"]')?.textContent).toContain(EVIL);
    expect(document.body.querySelector('[data-testid="storage-store-sha256"]')?.textContent).toBe(EVIL);
    noInjectedMarkup();
  });

  it('a refusal', async () => {
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://s.example', token: 'tok_0123456789', at: Date.now() }));
    intentAnswer = { status: 409, data: { error: 'intent_pin_mismatch', message: EVIL, detail: { mismatches: [{ field: EVIL, link: EVIL, source: EVIL }] } } };
    const w = mount(StoreInstall, { global: global(), attachTo: document.body });
    await flushPromises();
    expect(w.find('[data-testid="store-install-error"]').text()).toContain(EVIL);
    noInjectedMarkup();
  });

  it('the review a store link opens, an upgrade’s sources included', async () => {
    const dry = JSON.parse(readFileSync(path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire/app-plugin-dry-run.json'), 'utf8'));
    delete dry.installed;
    delete dry.engines_missing;
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://s.example', token: 'tok_0123456789', at: Date.now() }));
    intentAnswer = {
      status: 200,
      data: {
        handle: 'h-1', store: EVIL, store_trust: 'admin',
        intent: { store: EVIL, token_id: 't', app: EVIL, kind: 'app', version: EVIL, repo: EVIL, ref: EVIL, commit: EVIL, paid: true, license_key_prefix: EVIL, expires_at: '2026-10-04T18:30:00Z' },
        review: dry,
        upgrade_of: { id: 4, version: EVIL, store: EVIL, repo: EVIL, source_url: EVIL },
      },
    };
    mount(StoreInstall, { global: global(), attachTo: document.body });
    await flushPromises();
    const wizard = document.body.querySelector('[data-testid="app-plugin-wizard"]');
    expect(wizard).not.toBeNull();
    expect(document.body.querySelector('[data-testid="app-plugin-from-store"]')?.textContent).toContain(EVIL);
    expect(document.body.querySelector('[data-testid="app-plugin-store-source"]')?.textContent).toContain(EVIL);
    expect(document.body.querySelector('[data-testid="app-plugin-store-upgrade-from"]')?.textContent).toContain(EVIL);
    expect(document.body.querySelector('[data-testid="app-plugin-store-license-prefix"]')?.textContent).toContain(EVIL);
    noInjectedMarkup();
  });

  it('an app’s License section and the warning band', async () => {
    licenseAnswer = {
      app: EVIL, required: true, status: 'revoked', held: true, store: EVIL, key_prefix: EVIL, licensee: EVIL,
      last_error: EVIL, last_attempt_at: '2026-10-04T10:00:00Z', store_trusted: false,
    };
    const a = mount(AppPluginLicense, { props: { pluginId: 4 }, global: global(), attachTo: document.body });
    const b = mount(AppLicenseAlert, { global: global(), attachTo: document.body });
    const c = mount(AppStoresPanel, { global: global(), attachTo: document.body });
    await flushPromises();
    expect(a.text()).toContain(EVIL);
    expect(b.text()).toContain(EVIL);
    expect(c.text()).toContain(EVIL);
    noInjectedMarkup();
  });
});
