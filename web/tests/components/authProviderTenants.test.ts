// Admin → Identity providers on a multi-tenant install (docs/TENANT-ADMIN.md):
//   · a provider's own page says which tenants sign in through it, and saving
//     the ticks sends exactly that list;
//   · a tenant's own provider shows its owner and offers no ticks;
//   · the upgrade's "every provider bound to every tenant" is said until the
//     operator has looked;
//   · "Add a provider" makes another instance of a kind, switched off;
//   · a provider made that way is deleted, a kind's first one is not.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { AuthProvider } from '@/api/types';

const api = vi.hoisted(() => ({
  setTenants: vi.fn(async () => null),
  create: vi.fn(async () => null),
  remove: vi.fn(async () => undefined),
  dismissReview: vi.fn(async () => undefined),
  syncStatus: vi.fn(async () => ({ name: 'ldap', available: true, running: false, interval_seconds: 0, last: null })),
  update: vi.fn(),
  test: vi.fn(),
}));
const fx = vi.hoisted(() => ({ providers: [] as unknown[], review: true }));

vi.mock('@/api/auth-providers', () => ({
  AuthProvidersApi: {
    overview: vi.fn(async () => ({
      providers: fx.providers,
      passwordSignIn: true,
      recoveryLogin: false,
      secretKey: true,
      multiTenant: true,
      reviewPending: fx.review,
      tenants: [
        { id: 1, name: 'Platform', realm: '', is_supertenant: true },
        { id: 2, name: 'Acme', realm: 'acme', is_supertenant: false },
        { id: 3, name: 'Beta', realm: 'beta', is_supertenant: false },
      ],
    })),
    ...api,
  },
}));

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

import AuthProviders from '@/views/AuthProviders.vue';
import AuthProviderEdit from '@/views/AuthProviderEdit.vue';

function p(id: string, extra: Partial<AuthProvider> = {}): AuthProvider {
  return {
    id,
    driver: id,
    enabled: true,
    config: {},
    config_redacted: {},
    status: 'ok',
    last_error: null,
    testable: false,
    managed: true,
    origin: 'page',
    state: 'running',
    secrets_set: {},
    fields: [],
    ...extra,
  } as AuthProvider;
}

/** The overview, or - with a slug - that provider's own page. */
async function mountPage(slug = '', locale: 'en' | 'tr' = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/auth-providers', name: 'auth-providers', component: AuthProviders },
      { path: '/auth-providers/:name', name: 'auth-providers.edit', component: AuthProviderEdit },
    ],
  });
  await router.push(slug ? `/auth-providers/${slug}` : '/auth-providers');
  await router.isReady();
  const w = mount(slug ? AuthProviderEdit : AuthProviders, { global: { plugins: [i18n, router] }, attachTo: document.body });
  return { w, router };
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  fx.review = true;
  fx.providers = [
    p('ldap', { instance_id: 10, tenants: [1, 2] }),
    p('corp-sso', { driver: 'oidc', instance_id: 11, label: 'Corp SSO', tenants: [3] }),
    p('acme-oidc', { driver: 'oidc', instance_id: 12, origin: 'tenant', owner_provider_id: 2, tenants: [2] }),
  ];
});

describe('Identity providers - tenants', () => {
  it('ticks the tenants a provider serves and saves exactly those', async () => {
    const { w } = await mountPage('ldap');
    await flushPromises();
    const box = w.get('[data-testid="auth-provider-tenants-ldap"]');
    const inputs = box.findAll('input[type="checkbox"]');
    expect(inputs.map((i) => (i.element as HTMLInputElement).checked)).toEqual([true, true, false]);
    expect(box.text()).toContain(en.authProviders.platformTenant);
    expect(box.text()).toContain('Acme (acme)');

    await inputs[1].setValue(false);
    await inputs[2].setValue(true);
    await box.get('[data-testid="auth-provider-tenants-save-ldap"]').trigger('click');
    await flushPromises();
    expect(api.setTenants).toHaveBeenCalledWith('ldap', [1, 3], false);
  });

  it('shows a tenant’s own provider with its owner and no ticks', async () => {
    const { w } = await mountPage('acme-oidc');
    await flushPromises();
    const box = w.get('[data-testid="auth-provider-tenants-acme-oidc"]');
    expect(box.text()).toContain('Acme');
    expect(box.findAll('input[type="checkbox"]')).toHaveLength(0);
  });

  it('names an instance by its own name, on its card and on its page', async () => {
    const list = await mountPage();
    await flushPromises();
    await list.w.get('[data-testid="auth-tab-oidc"]').trigger('click');
    expect(list.w.get('[data-testid="auth-card-corp-sso"]').text()).toContain('Corp SSO');
    list.w.unmount();
    const { w } = await mountPage('corp-sso');
    await flushPromises();
    expect(w.get('[data-testid="auth-provider-title"]').text()).toContain('Corp SSO');
  });

  it('says the upgrade bound everything until the operator has looked', async () => {
    const { w } = await mountPage('', 'tr');
    await flushPromises();
    const note = w.get('[data-testid="auth-providers-review"]');
    expect(note.text()).toContain('gözden geçirin');
    await note.get('[data-testid="auth-providers-review-done"]').trigger('click');
    await flushPromises();
    expect(api.dismissReview).toHaveBeenCalledTimes(1);
    expect(w.find('[data-testid="auth-providers-review"]').exists()).toBe(false);
  });

  it('adds another provider of a kind, switched off, and opens its page', async () => {
    api.create.mockResolvedValueOnce(p('partner-sso', { driver: 'oidc', instance_id: 13 }) as never);
    const { w, router } = await mountPage();
    await flushPromises();
    await w.get('[data-testid="auth-provider-add"]').trigger('click');
    await flushPromises();
    const kind = document.querySelector('[data-testid="auth-provider-add-kind"] select') as HTMLSelectElement;
    kind.value = 'oidc';
    kind.dispatchEvent(new Event('change'));
    const label = document.querySelector('[data-testid="auth-provider-add-label"] input') as HTMLInputElement;
    label.value = 'Partner SSO';
    label.dispatchEvent(new Event('input'));
    await flushPromises();
    (document.querySelector('[data-testid="auth-provider-add-create"]') as HTMLButtonElement).click();
    await flushPromises();
    expect(api.create).toHaveBeenCalledWith({ driver: 'oidc', slug: undefined, label: 'Partner SSO', enabled: false });
    expect(router.currentRoute.value.name).toBe('auth-providers.edit');
    expect(router.currentRoute.value.params.name).toBe('partner-sso');
  });

  it('deletes a provider made that way, never a kind’s first', async () => {
    const first = await mountPage('ldap');
    await flushPromises();
    expect(first.w.find('[data-testid="auth-provider-delete-ldap"]').exists()).toBe(false);
    first.w.unmount();

    const { w, router } = await mountPage('corp-sso');
    await flushPromises();
    await w.get('[data-testid="auth-provider-delete-corp-sso"]').trigger('click');
    await flushPromises();
    (document.querySelector('[data-testid="auth-provider-delete-confirm"]') as HTMLButtonElement).click();
    await flushPromises();
    expect(api.remove).toHaveBeenCalledWith('corp-sso', false);
    expect(router.currentRoute.value.name).toBe('auth-providers');
    expect(router.currentRoute.value.query.tab).toBe('oidc');
  });
});
