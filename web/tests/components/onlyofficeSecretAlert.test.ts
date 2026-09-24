// ONLYOFFICE with an address but no JWT secret: a red warning that stays.
//
// ⚠⚠ The owner's decision (2026-09-22): not a toast, not a dismissible banner —
// a warning on the admin Panel and on External services until a secret is
// set. The save callback is a public route and the secret is the only thing
// that tells a genuine save from a forged one (the v0.43.0 security fix).
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f?: string) => f ?? 'error',
  api: { get: vi.fn(async () => ({ data: { entries: [] } })) },
}));

import OnlyOfficeSecretAlert from '@/components/OnlyOfficeSecretAlert.vue';
import { useExternalServicesStore } from '@/stores/external';
import { onlyofficeWithoutSecret } from '@/lib/onlyofficeSecret';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { ExternalService } from '@/api/types';

const oo = (over: Partial<ExternalService>): ExternalService =>
  ({
    id: 'onlyoffice',
    url: 'https://office.example.com',
    jwt_secret_set: false,
    enabled: true,
    last_checked_at: null,
    last_state: 'unconfigured',
    last_error: null,
    ...over,
  }) as ExternalService;

async function mountAlert(items: ExternalService[], props: Record<string, unknown> = {}) {
  const pinia = createPinia();
  setActivePinia(pinia);
  useExternalServicesStore().items = items;
  const router = createRouter({
    history: createMemoryHistory('/admin/'),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/external', name: 'external', component: { template: '<div />' } },
    ],
  });
  await router.push('/');
  const i18n = createI18n({ legacy: false, locale: 'tr', fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(OnlyOfficeSecretAlert, { props, global: { plugins: [pinia, router, i18n] } });
  await flushPromises();
  return w;
}

describe('ONLYOFFICE without a JWT secret', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('is decided by one rule: switched on, has an address, holds no secret', () => {
    expect(onlyofficeWithoutSecret([oo({})])).toBe(true);
    expect(onlyofficeWithoutSecret([oo({ jwt_secret_set: true })])).toBe(false);
    expect(onlyofficeWithoutSecret([oo({ enabled: false })])).toBe(false);
    expect(onlyofficeWithoutSecret([oo({ url: '' })])).toBe(false);
    expect(onlyofficeWithoutSecret([])).toBe(false);
  });

  it('is shown in red, in the panel language, with no way to dismiss it', async () => {
    const w = await mountAlert([oo({})]);
    const alert = w.find('[data-testid="onlyoffice-no-secret"]');
    expect(alert.exists()).toBe(true);
    expect(alert.attributes('role')).toBe('alert');
    expect(alert.text()).toContain('JWT secret');
    expect(alert.text()).toContain('üzerine yazabilir');
    expect(alert.findAll('button').length, 'nothing closes it').toBe(0);
    expect(alert.find('a').exists(), 'the Panel copy links to where the secret is set').toBe(true);
  });

  it('carries no link on External services itself', async () => {
    const w = await mountAlert([oo({})], { noLink: true });
    expect(w.find('[data-testid="onlyoffice-no-secret"] a').exists()).toBe(false);
  });

  it('is gone once a secret is set', async () => {
    const w = await mountAlert([oo({ jwt_secret_set: true })]);
    expect(w.find('[data-testid="onlyoffice-no-secret"]').exists()).toBe(false);
  });
});
