// A tenant running itself (components/TenantSelfService.vue,
// docs/TENANT-ADMIN.md). What is pinned:
//   · its own providers are drawn from the server's fields with the shared
//     field form, and "Add own LDAP" makes one switched off;
//   · the providers the platform gives it are named, never configured;
//   · the operator's insecure switch is offered to the operator only;
//   · own domains are THE table (admin.tenant.domains): state, what to point
//     at, who certifies; a refusal is said in the reader's language;
//   · a tenant with no platform subdomain is told why there are no domains.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const api = vi.hoisted(() => ({
  get: vi.fn(),
  createProvider: vi.fn(async () => undefined),
  updateProvider: vi.fn(async () => ({ checks: [] })),
  deleteProvider: vi.fn(async () => undefined),
  testProvider: vi.fn(),
  addDomain: vi.fn(),
  checkDomain: vi.fn(),
  setCertificate: vi.fn(),
  removeCertificate: vi.fn(),
  deleteDomain: vi.fn(),
  setInsecure: vi.fn(async () => undefined),
}));

function overview(extra: Record<string, unknown> = {}) {
  return {
    tenant: { id: 2, name: 'Acme', slug: 'acme', realm: 'acme', allow_insecure_auth: false },
    platform_subdomain: 'acme.tenants.files.example',
    tenant_domain: 'tenants.files.example',
    tls_mode: 'proxy',
    drivers: ['oidc', 'ldap'],
    fields: {
      ldap: [
        { key: 'url', kind: 'text', required: true },
        { key: 'base_dn', kind: 'text', required: true },
        { key: 'ca_pem', kind: 'multiline' },
      ],
      oidc: [{ key: 'issuer', kind: 'text', required: true }],
    },
    providers: [
      {
        id: 'acme-ldap', driver: 'ldap', label: 'Acme directory', enabled: false, config: {},
        config_redacted: { url: 'ldaps://dir.acme.example', base_dn: 'dc=acme' }, status: 'disabled', state: 'off',
        secrets_set: {}, fields: [], managed: true, origin: 'tenant',
      },
    ],
    shared_providers: [{ name: 'ldap', driver: 'ldap', label: 'Corporate directory', state: 'running' }],
    domains: [
      { id: 7, provider_id: 2, domain: 'files.acme.example', status: 'active', active_since: '2026-10-01T10:00:00Z', target: 'acme.tenants.files.example', own_certificate: false },
      {
        id: 8, provider_id: 2, domain: 'old.acme.example', status: 'suspended',
        last_error: 'points at other.example, not acme.tenants.files.example',
        last_error_code: 'points_elsewhere', last_error_params: { found: 'other.example', target: 'acme.tenants.files.example' },
        target: 'acme.tenants.files.example', own_certificate: false,
      },
      { id: 11, provider_id: 2, domain: 'new.acme.example', status: 'pending', last_error: 'something a newer server says', last_error_code: 'not_known_here', target: 'acme.tenants.files.example', own_certificate: false },
      { id: 9, provider_id: 2, domain: 'www.acme.example', status: 'active', target: 'acme.tenants.files.example', own_certificate: true, tls_not_after: new Date(Date.now() + 5 * 24 * 3600 * 1000).toISOString() },
      { id: 10, provider_id: 2, domain: 'cdn.acme.example', status: 'active', target: 'acme.tenants.files.example', own_certificate: true, tls_not_after: new Date(Date.now() + 90 * 24 * 3600 * 1000).toISOString() },
    ],
    ...extra,
  };
}

vi.mock('@/api/tenantSelf', async (orig) => ({ ...(await orig<object>()), TenantSelfApi: api }));

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

import TenantSelfService from '@/components/TenantSelfService.vue';
import { DataTable } from '@brftech/filex-core';

function mountIt(props: Record<string, unknown> = {}, locale: 'en' | 'tr' = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(TenantSelfService, { props, global: { plugins: [i18n] }, attachTo: document.body });
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  api.get.mockReset();
  api.get.mockImplementation(async () => overview());
  api.addDomain.mockReset();
});

describe('A tenant running itself', () => {
  it('draws its own provider with the server’s fields and adds another, switched off', async () => {
    const w = mountIt();
    await flushPromises();
    const card = w.get('[data-testid="tenant-provider-acme-ldap"]');
    expect(card.text()).toContain('Acme directory');
    expect((card.get('input[name="auth-field-tenant-acme-ldap-url"]').element as HTMLInputElement).value).toBe('ldaps://dir.acme.example');
    expect(card.find('textarea[name="auth-field-tenant-acme-ldap-ca_pem"]').exists(), 'a pasted PEM is a multi-line box').toBe(true);
    expect(card.find('[name="auth-field-tenant-acme-ldap-ca_file"]').exists(), 'never a file on the server').toBe(false);

    await w.get('[data-testid="tenant-provider-add-ldap"]').trigger('click');
    await flushPromises();
    expect(api.createProvider).toHaveBeenCalledWith(undefined, { driver: 'ldap', enabled: false });
    w.unmount();
  });

  it('names the platform’s providers and offers the insecure switch to the operator only', async () => {
    const w = mountIt();
    await flushPromises();
    expect(w.get('[data-testid="tenant-self-shared"]').text()).toContain('Corporate directory');
    expect(w.find('[name="tenant-self-insecure"]').exists()).toBe(false);
    expect(w.text()).toContain(en.tenantSelf.guarded);
    w.unmount();

    const op = mountIt({ tenantId: 2, operator: true });
    await flushPromises();
    expect(api.get).toHaveBeenLastCalledWith(2);
    await op.get('button[role="switch"][name="tenant-self-insecure"], #tenant-self-insecure').trigger('click');
    await flushPromises();
    expect(api.setInsecure).toHaveBeenCalledWith(2, true);
    op.unmount();
  });

  it('lists its domains in THE table, with what to point at and who certifies', async () => {
    const w = mountIt({}, 'tr');
    await flushPromises();
    expect(w.findAllComponents(DataTable).map((t) => t.props('tableId'))).toEqual(['admin.tenant.domains']);
    const box = w.get('[data-testid="tenant-self-domains"]');
    expect(box.get('[data-testid="tenant-self-subdomain"]').text()).toBe('acme.tenants.files.example');
    expect(box.text()).toContain(tr.tenantSelf.status.active);
    expect(box.text()).toContain(tr.tenantSelf.status.suspended);
    // What the check found, in the reader's language - not the server's
    // English sentence; a code this screen does not know keeps the sentence.
    expect(box.get('[data-testid="tenant-domain-detail-8"]').text()).toBe('CNAME kaydı acme.tenants.files.example yerine other.example adresini gösteriyor');
    expect(box.text()).not.toContain('points at other.example');
    expect(box.get('[data-testid="tenant-domain-detail-11"]').text()).toBe('something a newer server says');
    expect(box.text()).toContain(tr.tenantSelf.certByProxy);
    // A brought certificate near its end is said in warning colours; one far from it is not.
    expect(box.find('[data-testid="tenant-domain-cert-soon-9"]').exists()).toBe(true);
    expect(box.find('[data-testid="tenant-domain-cert-soon-10"]').exists()).toBe(false);
    w.unmount();
  });

  // filex's own ACME (tls_mode acme): the certificate cell says what it did,
  // never "By filex (ACME)" over a certificate it could not obtain (measured
  // against Pebble, e2e/realenv). Three states, the authority's English
  // reason quoted under our own words.
  it('says what filex’s own ACME did for each domain: nothing yet, until when, or not obtained and why', async () => {
    const reason = 'DNS problem: NXDOMAIN looking up A for files.acme.example';
    api.get.mockImplementation(async () =>
      overview({
        tls_mode: 'acme',
        domains: [
          { id: 7, provider_id: 2, domain: 'files.acme.example', status: 'active', target: 'acme.tenants.files.example', own_certificate: false, acme: { state: 'failed', reason, at: '2026-10-02T09:00:00Z' } },
          { id: 12, provider_id: 2, domain: 'docs.acme.example', status: 'active', target: 'acme.tenants.files.example', own_certificate: false, acme: { state: 'obtained', not_after: '2026-12-31T10:00:00Z', at: '2026-10-02T09:00:00Z' } },
          { id: 13, provider_id: 2, domain: 'new.acme.example', status: 'pending', target: 'acme.tenants.files.example', own_certificate: false, acme: { state: 'none' } },
          { id: 10, provider_id: 2, domain: 'cdn.acme.example', status: 'active', target: 'acme.tenants.files.example', own_certificate: true, tls_not_after: new Date(Date.now() + 90 * 24 * 3600 * 1000).toISOString() },
        ],
      }),
    );
    const w = mountIt({}, 'tr');
    await flushPromises();
    const box = w.get('[data-testid="tenant-self-domains"]');
    const failed = box.get('[data-testid="tenant-domain-acme-7"]').text();
    expect(failed).toContain('filex (ACME): alınamadı');
    expect(box.get('[data-testid="tenant-domain-acme-reason-7"]').text()).toBe(`ACME otoritesinin yanıtı: "${reason}"`);
    expect(box.get('[data-testid="tenant-domain-acme-12"]').text()).toMatch(/^filex \(ACME\), .+ tarihine kadar$/);
    expect(box.get('[data-testid="tenant-domain-acme-13"]').text()).toBe(tr.tenantSelf.acmeNone);
    expect(box.find('[data-testid="tenant-domain-acme-reason-12"]').exists()).toBe(false);
    // A brought certificate is the tenant's, not ACME's.
    expect(box.find('[data-testid="tenant-domain-acme-10"]').exists()).toBe(false);
    w.unmount();
  });

  it('adds a domain and says a refusal in the reader’s language', async () => {
    api.addDomain.mockRejectedValueOnce({ response: { status: 409, data: { error: 'domain_taken' } } });
    const w = mountIt({}, 'tr');
    await flushPromises();
    await w.get('[data-testid="tenant-self-domain-input"] input').setValue('files.acme.example');
    await w.get('[data-testid="tenant-self-domain-add"]').trigger('click');
    await flushPromises();
    expect(api.addDomain).toHaveBeenCalledWith(undefined, 'files.acme.example');
    expect(w.get('[data-testid="tenant-self-domains"]').text()).toContain(tr.tenantSelf.domainErrors.domain_taken);
    w.unmount();
  });

  it('says why there are no domains without a platform subdomain', async () => {
    api.get.mockImplementation(async () => overview({ platform_subdomain: '', tenant_domain: '', domains: [] }));
    const w = mountIt();
    await flushPromises();
    expect(w.get('[data-testid="tenant-self-no-subdomain"]').text()).toContain('FILEX_TENANT_DOMAIN');
    expect(w.find('[data-testid="tenant-self-domain-input"]').exists()).toBe(false);
    w.unmount();
  });
});
