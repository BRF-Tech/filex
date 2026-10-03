// "Trust this provider's email addresses" (0.50, docs/SSO.md) on the screens
// that set it. One setting, one sentence: the provider form (ProviderFields,
// Admin → Identity providers and a tenant's own providers) and the tenant
// page's own OIDC read the same keys. When the upgrade to 0.50 chose the value
// (an OIDC that existed before it keeps its old behaviour), the line says so
// first - until somebody saves it.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import ProviderFields from '@/components/ProviderFields.vue';

const { tenantsApi, tenantRow } = vi.hoisted(() => {
  const tenantRow = {
    id: 2,
    slug: 'acme',
    realm: 'acme',
    name: 'Acme',
    auth_type: 'oidc',
    oidc_issuer: 'https://id.acme.example/realms/acme',
    oidc_client_id: 'filex',
    is_supertenant: false,
    enabled: true,
    storage_ids: [],
    user_count: 0,
    oidc_trust_email: false,
    oidc_trust_email_by_upgrade: false,
  };
  const tenantsApi = {
    get: vi.fn(async () => structuredClone(tenantRow)),
    update: vi.fn(async (_id: number, input: Record<string, unknown>) => ({ ...structuredClone(tenantRow), ...input })),
  };
  return { tenantsApi, tenantRow };
});
vi.mock('@/api/tenants', async (orig) => ({ ...(await orig<object>()), TenantsApi: tenantsApi }));
vi.mock('@/api/storages', () => ({ StoragesApi: { list: vi.fn(async () => []) } }));
vi.mock('@/api/tenantSelf', async (orig) => ({
  ...(await orig<object>()),
  TenantSelfApi: {
    get: vi.fn(async (id?: number) => ({
      tenant: { id, name: 'Acme', slug: 'acme', realm: 'acme', allow_insecure_auth: false },
      platform_subdomain: '', tenant_domain: '', tls_mode: 'proxy', drivers: ['oidc'], fields: { oidc: [] },
      providers: [], shared_providers: [], domains: [],
    })),
  },
}));

import TenantEdit from '@/views/TenantEdit.vue';

function i18n(locale: 'en' | 'tr' = 'en') {
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
}

const trustField = { key: 'trust_email', kind: 'bool' as const, default: 'false' };

describe('the provider form', () => {
  it('names the setting and says what it costs', () => {
    const w = mount(ProviderFields, {
      props: { fields: [trustField], values: { trust_email: false }, prefix: 'corp' },
      global: { plugins: [i18n()] },
    });
    expect(w.text()).toContain(en.authProviders.fields.trust_email);
    expect(w.text()).toContain(en.authProviders.fieldHints.trust_email);
    expect(w.text()).not.toContain(en.authProviders.upgradeHints.trust_email);
  });

  it('says first that the upgrade set it, when it did', () => {
    const w = mount(ProviderFields, {
      props: { fields: [trustField], values: { trust_email: true }, prefix: 'corp', setByUpgrade: ['trust_email'] },
      global: { plugins: [i18n('tr')] },
    });
    const line = w.get('p').text();
    expect(line.startsWith(tr.authProviders.upgradeHints.trust_email)).toBe(true);
    expect(line).toContain(tr.authProviders.fieldHints.trust_email);
  });
});

describe('a tenant’s own OIDC', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  async function open(row: Partial<typeof tenantRow>) {
    tenantsApi.get.mockImplementationOnce(async () => ({ ...structuredClone(tenantRow), ...row }));
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/tenants', name: 'tenants', component: { template: '<div />' } },
        { path: '/tenants/:id', name: 'tenants.edit', component: TenantEdit },
      ],
    });
    await router.push('/tenants/2');
    const w = mount(TenantEdit, { global: { plugins: [router, i18n()] } });
    await flushPromises();
    return w;
  }

  it('has the same setting, off, and a save sends it', async () => {
    const w = await open({});
    const sw = w.get('#tenant-oidc-trust-email');
    expect(sw.attributes('aria-checked')).toBe('false');
    expect(w.text()).toContain(en.authProviders.fieldHints.trust_email);
    expect(w.text()).not.toContain(en.authProviders.upgradeHints.trust_email);
    await sw.trigger('click');
    await w.get('[data-testid="tenant-form"]').trigger('submit');
    await flushPromises();
    expect(tenantsApi.update.mock.calls[0][1]).toMatchObject({ oidc_trust_email: true });
  });

  it('says the upgrade set it while it is the upgrade’s', async () => {
    const w = await open({ oidc_trust_email: true, oidc_trust_email_by_upgrade: true });
    expect(w.get('#tenant-oidc-trust-email').attributes('aria-checked')).toBe('true');
    expect(w.text()).toContain(en.authProviders.upgradeHints.trust_email);
  });
});
