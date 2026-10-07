// The tenant screen (views/Tenants.vue, views/TenantEdit.vue;
// docs/TENANT-ADMIN.md). What is pinned:
//   1. the list is THE table (admin.tenants), the platform's own tenant first
//      and named as such, every tenant's realm, address and state on its row;
//   2. a new tenant's realm is the SERVER's suggestion for the slug
//      (GET /providers/realm-suggestion) and follows the slug until a person
//      types a realm of their own, and what is created is what was shown;
//   3. a refusal lands on the field it is about, in the reader's language;
//   4. on a tenant's page the realm is shown, read-only, with the reason, and
//      a save never sends it; the platform's own tenant offers neither
//      suspension nor deletion;
//   5. deleting a tenant that has accounts needs the "accounts too" box.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const { tenantsApi, rows } = vi.hoisted(() => {
  const rows = [
    {
      id: 2,
      slug: 'acme',
      realm: 'acme',
      name: 'Acme',
      host: 'files.acme.example',
      auth_type: 'oidc',
      oidc_issuer: 'https://id.acme.example/realms/acme',
      oidc_client_id: 'filex',
      oidc_client_secret_set: true,
      is_supertenant: false,
      enabled: true,
      storage_ids: [5],
      user_count: 3,
    },
    {
      id: 1,
      slug: 'default',
      realm: '',
      name: 'Platform',
      auth_type: 'local',
      is_supertenant: true,
      enabled: true,
      storage_ids: [],
      user_count: 1,
    },
    {
      id: 3,
      slug: 'beta',
      realm: 'beta',
      name: 'Beta',
      auth_type: 'local',
      is_supertenant: false,
      enabled: false,
      storage_ids: [],
      user_count: 0,
    },
  ];
  const tenantsApi = {
    list: vi.fn(async () => ({ providers: structuredClone(rows), multi_tenant: true })),
    get: vi.fn(async (id: number) => structuredClone(rows.find((r) => r.id === id))),
    suggestRealm: vi.fn(async (slug: string) => {
      const base = slug.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-');
      return { realm: base, base, available: true };
    }),
    create: vi.fn(async (input: Record<string, unknown>) => ({ ...rows[0], id: 9, ...input })),
    update: vi.fn(async (id: number, input: Record<string, unknown>) => ({ ...structuredClone(rows.find((r) => r.id === id)), ...input })),
    remove: vi.fn(async () => ({ ok: true, deleted_users: 3 })),
    linkStorage: vi.fn(async () => structuredClone(rows[0])),
    unlinkStorage: vi.fn(async () => ({ ...structuredClone(rows[0]), storage_ids: [] })),
  };
  return { tenantsApi, rows };
});

vi.mock('@/api/tenants', async (orig) => ({ ...(await orig<object>()), TenantsApi: tenantsApi }));
vi.mock('@/api/storages', () => ({
  StoragesApi: {
    list: vi.fn(async () => [
      { id: 5, name: 'acme-files' },
      { id: 6, name: 'shared' },
    ]),
  },
}));
// A tenant's page carries the tenant's own sign-in and domains
// (TenantSelfService, pinned in tenantSelfService.test.ts); here only that it
// is there, for the tenant shown.
const selfApi = vi.hoisted(() => ({
  get: vi.fn(async (id?: number) => ({
    tenant: { id, name: 'Acme', slug: 'acme', realm: 'acme', allow_insecure_auth: false },
    platform_subdomain: '',
    tenant_domain: '',
    tls_mode: 'proxy',
    drivers: ['oidc', 'ldap'],
    fields: { oidc: [], ldap: [] },
    providers: [],
    shared_providers: [],
    domains: [],
  })),
}));
vi.mock('@/api/tenantSelf', async (orig) => ({ ...(await orig<object>()), TenantSelfApi: selfApi }));

import Tenants from '@/views/Tenants.vue';
import TenantEdit from '@/views/TenantEdit.vue';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { normalizeTenant, type Tenant } from '@/api/tenants';
import { DataTable } from '@brftech/filex-core';
import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
import { optionLabels, pickOption } from '../helpers/choiceSelect';

function setup(locale: 'en' | 'tr' = 'en', multiTenant = true) {
  setActivePinia(createPinia());
  // The server's one answer the page follows (composables/useTenancy, #167).
  const caps = useCapabilitiesStore();
  caps.data = { ...caps.data, multi_tenant: multiTenant };
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/tenants', name: 'tenants', component: Tenants },
      { path: '/tenants/:id', name: 'tenants.edit', component: TenantEdit },
    ],
  });
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return { router, plugins: [router, i18n] };
}

beforeEach(() => {
  tenantsApi.list.mockReset();
  tenantsApi.list.mockImplementation(async () => ({ providers: structuredClone(rows), multi_tenant: true }));
  tenantsApi.create.mockReset();
  tenantsApi.create.mockImplementation(async (input: Record<string, unknown>) => ({ ...rows[0], id: 9, ...input }));
  vi.clearAllMocks();
});

describe('Tenants list', () => {
  it('is THE table, the platform first, with each tenant’s realm, address and state', async () => {
    const { router, plugins } = setup();
    await router.push('/tenants');
    const w = mount(Tenants, { global: { plugins }, attachTo: document.body });
    await flushPromises();
    expect(w.findAllComponents(DataTable).map((t) => t.props('tableId'))).toEqual(['admin.tenants']);
    expect(w.find('table').exists(), 'no table drawn by the page').toBe(false);

    const order = w
      .findAll('[data-testid^="tenant-"]')
      .map((e) => e.attributes('data-testid'))
      .filter((id) => /^tenant-\d+$/.test(id ?? ''));
    expect(order).toEqual(['tenant-1', 'tenant-2', 'tenant-3']);
    const text = w.text();
    expect(text).toContain(en.tenants.platform);
    expect(text).toContain('files.acme.example');
    expect(text).toContain(en.tenants.signin.ownOidc);
    expect(text).toContain(en.tenants.state.suspended);
    expect(text).toContain('3 accounts');
    expect(w.find('[data-testid="tenants-mode-off"]').exists()).toBe(false);

    await openRowMenu(w, 'tenant-actions-2');
    await pickMenuItem('tenant-actions-2-edit');
    await flushPromises();
    expect(router.currentRoute.value.name).toBe('tenants.edit');
    closeRowMenus();
    w.unmount();
  });

  // #167: with the mode off the page has nothing to show - the router sends
  // the reader to the dashboard, and the page itself neither draws a tenant
  // nor asks the server for the list.
  it('draws nothing and asks nothing while multi-tenant mode is off', async () => {
    const { router, plugins } = setup('en', false);
    await router.push('/tenants');
    const w = mount(Tenants, { global: { plugins } });
    await flushPromises();
    expect(tenantsApi.list).not.toHaveBeenCalled();
    expect(w.text()).toBe('');
    expect(w.find('[data-testid="tenant-new"]').exists()).toBe(false);
  });
});

describe('New tenant', () => {
  async function openNew() {
    vi.useFakeTimers();
    const { router, plugins } = setup();
    await router.push('/tenants');
    const w = mount(Tenants, { global: { plugins }, attachTo: document.body });
    await flushPromises();
    await w.get('[data-testid="tenant-new"]').trigger('click');
    await flushPromises();
    return { w, router };
  }
  const field = (id: string) => document.querySelector(`[data-testid="${id}"] input`) as HTMLInputElement;
  async function type(id: string, value: string) {
    const el = field(id);
    el.value = value;
    el.dispatchEvent(new Event('input'));
    await flushPromises();
  }

  it('suggests the realm from the slug, until a realm is typed, and creates what was shown', async () => {
    const { w, router } = await openNew();
    await type('tenant-new-slug', 'Acme Corp');
    vi.advanceTimersByTime(300);
    await flushPromises();
    expect(tenantsApi.suggestRealm).toHaveBeenCalledWith('Acme Corp');
    expect(field('tenant-new-realm').value).toBe('acme-corp');

    // A realm typed by hand stops following the slug.
    await type('tenant-new-realm', 'acme');
    await type('tenant-new-slug', 'Acme Corporation');
    vi.advanceTimersByTime(300);
    await flushPromises();
    expect(tenantsApi.suggestRealm).toHaveBeenCalledTimes(1);
    expect(field('tenant-new-realm').value).toBe('acme');

    (document.querySelector('[data-testid="tenant-new-create"]') as HTMLButtonElement).click();
    await flushPromises();
    expect(tenantsApi.create).toHaveBeenCalledWith({ slug: 'Acme Corporation', name: 'Acme Corporation', realm: 'acme' });
    expect(router.currentRoute.value.name).toBe('tenants.edit');
    expect(router.currentRoute.value.params.id).toBe('9');
    vi.useRealTimers();
    w.unmount();
  });

  it('puts a refusal on the field it is about', async () => {
    tenantsApi.create.mockRejectedValueOnce({ response: { status: 409, data: { error: 'realm_taken', field: 'realm', message: 'another tenant already has this realm' } } });
    const { w } = await openNew();
    await type('tenant-new-slug', 'acme');
    vi.advanceTimersByTime(300);
    await flushPromises();
    (document.querySelector('[data-testid="tenant-new-create"]') as HTMLButtonElement).click();
    await flushPromises();
    const realmBox = document.querySelector('[data-testid="tenant-new-realm"]') as HTMLElement;
    expect(realmBox.textContent).toContain(en.tenants.errors.realm_taken);
    vi.useRealTimers();
    w.unmount();
  });
});

describe('Tenant page', () => {
  it('shows the realm read-only, with why, and never sends it', async () => {
    const { router, plugins } = setup();
    await router.push('/tenants/2');
    const w = mount(TenantEdit, { global: { plugins } });
    await flushPromises();
    const realm = w.get('[data-testid="tenant-realm"] input');
    expect((realm.element as HTMLInputElement).value).toBe('acme');
    expect(realm.attributes('readonly')).toBeDefined();
    expect(w.get('[data-testid="tenant-realm"]').text()).toContain('acme/alex');

    await w.get('[data-testid="tenant-host"] input').setValue('files.acme.test');
    await w.get('[data-testid="tenant-form"]').trigger('submit');
    await flushPromises();
    const sent = tenantsApi.update.mock.calls[0][1] as Record<string, unknown>;
    expect(sent).toMatchObject({ host: 'files.acme.test', slug: 'acme', enabled: true, oidc_client_secret: '' });
    expect('realm' in sent, 'a save never sends the realm').toBe(false);
    // The tenant's own sign-in and domains, as the operator sees them.
    expect(w.find('[data-testid="tenant-self"]').exists()).toBe(true);
    expect(selfApi.get).toHaveBeenCalledWith(2);
  });

  it('offers the platform’s own tenant neither suspension nor deletion', async () => {
    const { router, plugins } = setup();
    await router.push('/tenants/1');
    const w = mount(TenantEdit, { global: { plugins } });
    await flushPromises();
    expect(w.find('[data-testid="tenant-delete"]').exists()).toBe(false);
    expect(w.get('[data-testid="tenant-enabled"] button[role="switch"]').attributes('disabled')).toBeDefined();
    expect(w.text()).toContain(en.tenants.platformRealmHint);
    expect(w.find('[data-testid="tenant-self"]').exists(), 'the platform’s own sign-in is on Identity providers').toBe(false);
    await w.get('[data-testid="tenant-form"]').trigger('submit');
    await flushPromises();
    expect('enabled' in (tenantsApi.update.mock.calls[0][1] as object)).toBe(false);
  });

  it('deletes a tenant with accounts only with the accounts-too box ticked', async () => {
    const { router, plugins } = setup();
    await router.push('/tenants/2');
    const w = mount(TenantEdit, { global: { plugins }, attachTo: document.body });
    await flushPromises();
    await w.get('[data-testid="tenant-delete"]').trigger('click');
    await flushPromises();
    const confirmBtn = document.querySelector('[data-testid="tenant-delete-confirm"]') as HTMLButtonElement;
    expect(confirmBtn.disabled).toBe(true);
    (document.querySelector('[data-testid="tenant-delete-accounts"] input') as HTMLInputElement).click();
    await flushPromises();
    expect(confirmBtn.disabled).toBe(false);
    confirmBtn.click();
    await flushPromises();
    expect(tenantsApi.remove).toHaveBeenCalledWith(2, true);
    expect(router.currentRoute.value.name).toBe('tenants');
    w.unmount();
  });

  it('names the linked storages and links another', async () => {
    const { router, plugins } = setup();
    await router.push('/tenants/2');
    const w = mount(TenantEdit, { global: { plugins } });
    await flushPromises();
    const box = w.get('[data-testid="tenant-storages"]');
    expect(box.text()).toContain('acme-files');
    const options = await optionLabels(box.get('[data-testid="tenant-link-choice"]'));
    expect(options).toContain('shared');
    expect(options, 'a linked storage is not offered again').not.toContain('acme-files');
    await pickOption(box.get('[data-testid="tenant-link-choice"]'), '6');
    await box.get('[data-testid="tenant-link"]').trigger('click');
    await flushPromises();
    expect(tenantsApi.linkStorage).toHaveBeenCalledWith(2, 6);
  });

  it('speaks Turkish on a Turkish panel', async () => {
    const { router, plugins } = setup('tr');
    await router.push('/tenants/2');
    const w = mount(TenantEdit, { global: { plugins } });
    await flushPromises();
    expect(w.text()).toContain('Realm asla değişmez');
    expect(w.text()).toContain(tr.tenants.storagesTitle);
  });
});

// Measured in the browser (2026-10-01): a tenant with no storage comes back as
// `storage_ids: null` (a nil list in Go), and the list page drew nothing at all
// ("Cannot read properties of null"). The API module reads it as none.
describe('A tenant as the screens read it', () => {
  it('reads a missing storage list as none', () => {
    const raw = { ...rows[1], storage_ids: null } as unknown as Tenant;
    expect(normalizeTenant(raw).storage_ids).toEqual([]);
    expect(normalizeTenant(rows[0] as Tenant).storage_ids).toEqual(rows[0].storage_ids);
  });
});
