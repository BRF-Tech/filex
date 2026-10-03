// The sign-in page's SSO buttons (docs/TENANT-ADMIN.md):
//   - the address's own list arrives with the page (`auth_sso`): one button
//     per identity provider, each starting ITS instance;
//   - on the platform's page, typing a realm asks for THAT realm's buttons
//     (GET /api/auth/methods) and the start carries the realm, so a tenant
//     with no address of its own signs in through its own SSO;
//   - an older server that says only `auth_drivers` keeps its one button.
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

vi.mock('@/api/branding', () => ({ BrandingApi: { get: vi.fn().mockResolvedValue({}) } }));
let published: Record<string, unknown> = {};
vi.mock('@/api/capabilities', () => ({ CapabilitiesApi: { fetch: vi.fn(async () => published) } }));
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ push: vi.fn() }) }));

const authApi = vi.hoisted(() => ({
  methods: vi.fn(),
  // startOidc() assigns this to window.location.href; a hash keeps happy-dom
  // on the page.
  oidcStartUrl: vi.fn((_p: string, _r: string, pick: { instance?: string; realm?: string } = {}) =>
    `#sso-${pick.instance ?? ''}-${pick.realm ?? ''}`),
  login: vi.fn(),
  me: vi.fn(),
  handoff: vi.fn(),
  logout: vi.fn(),
}));
vi.mock('@/api/auth', () => ({ AuthApi: authApi }));

import Login from '@/views/Login.vue';
import { useCapabilitiesStore } from '@/stores/capabilities';

function mountLogin() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en, tr } });
  return mount(Login, { global: { plugins: [i18n], stubs: { LogoMark: true, Input: true, Checkbox: true } } });
}

function withCaps(extra: Record<string, unknown>) {
  const caps = useCapabilitiesStore();
  caps.data = { ...caps.data, auth_drivers: ['local', 'oidc'], ...extra };
  published = caps.data;
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
});

describe('SSO buttons', () => {
  it('draws one button per provider of the address, each starting its own', async () => {
    withCaps({ auth_sso: [{ id: 'oidc', label: '' }, { id: 'partner-sso', label: 'Partner SSO' }] });
    const w = mountLogin();
    await flushPromises();
    const first = w.get('[data-testid="login-sso-oidc"]');
    const second = w.get('[data-testid="login-sso-partner-sso"]');
    expect(first.text()).toContain(en.login.oidc);
    expect(second.text()).toContain('Partner SSO');
    await second.trigger('click');
    expect(authApi.oidcStartUrl).toHaveBeenLastCalledWith('oidc', '/admin/', { instance: 'partner-sso', realm: undefined });
    w.unmount();
  });

  it('asks a typed realm for its buttons and starts with the realm', async () => {
    vi.useFakeTimers();
    withCaps({ realm: { enabled: true, locked_realm: null }, auth_sso: [] });
    authApi.methods.mockResolvedValue({ password: true, recovery: false, sso: [{ id: 'tenant', label: 'Acme' }] });
    const w = mountLogin();
    await flushPromises();
    expect(w.find('[data-testid^="login-sso-"]').exists()).toBe(false);
    await w.get('#realm').setValue('acme');
    vi.advanceTimersByTime(350);
    await flushPromises();
    expect(authApi.methods).toHaveBeenCalledWith('acme');
    const btn = w.get('[data-testid="login-sso-tenant"]');
    expect(btn.text()).toContain('Acme');
    await btn.trigger('click');
    expect(authApi.oidcStartUrl).toHaveBeenLastCalledWith('oidc', '/admin/', { instance: 'tenant', realm: 'acme' });

    // Cleared: back to the platform's own (none).
    await w.get('#realm').setValue('');
    await flushPromises();
    expect(w.find('[data-testid^="login-sso-"]').exists()).toBe(false);
    vi.useRealTimers();
    w.unmount();
  });

  it('keeps one button for a server that says only auth_drivers', async () => {
    withCaps({});
    const caps = useCapabilitiesStore();
    delete (caps.data as Record<string, unknown>).auth_sso;
    published = caps.data;
    const w = mountLogin();
    await flushPromises();
    const btn = w.get('[data-testid="login-sso-default"]');
    await btn.trigger('click');
    expect(authApi.oidcStartUrl).toHaveBeenLastCalledWith('oidc', '/admin/', { instance: undefined, realm: undefined });
    w.unmount();
  });
});
