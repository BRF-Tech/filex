// The sign-in form's Realm field (#128, docs/MULTI-TENANCY.md → Realms).
//
//   - a single-tenant server publishes no `realm`: no field, and no realm sent;
//   - the platform's page of a multi-tenant server: an empty, free field; what
//     is typed is sent;
//   - a tenant's own page: the field arrives filled with that tenant's realm
//     and read-only, and that realm is sent;
//   - a sign-in handed to a tenant's address goes there, the ticket in the
//     fragment; the tenant's page redeems a ticket it finds in its fragment and
//     takes it out of the address bar first.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import Login from '@/views/Login.vue';
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

vi.mock('@/api/branding', () => ({
  BrandingApi: { get: vi.fn().mockResolvedValue({}) },
}));

let published: Record<string, unknown> = {};
vi.mock('@/api/capabilities', () => ({
  CapabilitiesApi: { fetch: vi.fn(async () => published) },
}));

const push = vi.fn();
vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ push }),
}));

function mountLogin(locale: 'en' | 'tr' = 'en') {
  const i18n = createI18n({ legacy: false, locale, messages: { en, tr } });
  return mount(Login, {
    global: {
      plugins: [i18n],
      stubs: { LogoMark: true, Input: true, Checkbox: true },
    },
  });
}

function withCaps(extra: Record<string, unknown>) {
  const caps = useCapabilitiesStore();
  caps.data = { ...caps.data, auth_drivers: ['local'], ...extra };
  published = caps.data;
}

async function fillAndSubmit(wrapper: ReturnType<typeof mountLogin>, realm?: string) {
  if (realm !== undefined) await wrapper.get('#realm').setValue(realm);
  await wrapper.get('#email').setValue('alex');
  await wrapper.get('#password').setValue('pw');
  await wrapper.get('form').trigger('submit');
  await flushPromises();
}

describe('the Realm field', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    push.mockClear();
  });

  it('is not there on a single-tenant server, and no realm is sent', async () => {
    withCaps({});
    const auth = useAuthStore();
    const login = vi.spyOn(auth, 'login').mockResolvedValue(true);
    const wrapper = mountLogin();
    await flushPromises();
    expect(wrapper.find('#realm').exists()).toBe(false);
    await fillAndSubmit(wrapper);
    expect(login).toHaveBeenCalledWith(expect.objectContaining({ realm: undefined }));
  });

  // #167: `multi_tenant` is the one answer (composables/useTenancy): off, no
  // field and no realm sent, whatever else the answer carries; and no word of
  // tenants or realms on the page.
  it('is not there when the server says multi-tenant mode is off', async () => {
    withCaps({ multi_tenant: false, realm: { enabled: true, locked_realm: 'acme' } });
    const auth = useAuthStore();
    const login = vi.spyOn(auth, 'login').mockResolvedValue(true);
    const wrapper = mountLogin();
    await flushPromises();
    expect(wrapper.find('#realm').exists()).toBe(false);
    expect(wrapper.text()).not.toMatch(/realm|tenant/i);
    await fillAndSubmit(wrapper);
    expect(login).toHaveBeenCalledWith(expect.objectContaining({ realm: undefined }));
  });

  it('is empty and free on the platform page, and sends what was typed', async () => {
    withCaps({ realm: { enabled: true, locked_realm: null } });
    const auth = useAuthStore();
    const login = vi.spyOn(auth, 'login').mockResolvedValue(true);
    const wrapper = mountLogin();
    await flushPromises();
    const field = wrapper.get<HTMLInputElement>('#realm');
    expect(field.element.value).toBe('');
    expect(field.attributes('readonly')).toBeUndefined();
    expect(wrapper.text()).toContain(en.login.realmHint);
    await fillAndSubmit(wrapper, ' acme ');
    expect(login).toHaveBeenCalledWith(expect.objectContaining({ realm: 'acme', email: 'alex' }));
  });

  it('an empty field sends no realm: the platform own accounts', async () => {
    withCaps({ realm: { enabled: true, locked_realm: null } });
    const auth = useAuthStore();
    const login = vi.spyOn(auth, 'login').mockResolvedValue(true);
    const wrapper = mountLogin();
    await flushPromises();
    await fillAndSubmit(wrapper, '');
    expect(login).toHaveBeenCalledWith(expect.objectContaining({ realm: undefined }));
  });

  it('is filled and read-only on a tenant own page, in both languages', async () => {
    withCaps({ realm: { enabled: true, locked_realm: 'acme' } });
    const auth = useAuthStore();
    const login = vi.spyOn(auth, 'login').mockResolvedValue(true);
    const wrapper = mountLogin('tr');
    await flushPromises();
    const field = wrapper.get<HTMLInputElement>('#realm');
    expect(field.element.value).toBe('acme');
    expect(field.attributes('readonly')).toBeDefined();
    expect(wrapper.text()).toContain('Bu adres acme realm');
    await fillAndSubmit(wrapper);
    expect(login).toHaveBeenCalledWith(expect.objectContaining({ realm: 'acme' }));
  });
});

const navigateTo = vi.fn();
vi.mock('@/lib/realmHandoff', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/realmHandoff')>()),
  navigateTo: (url: string) => navigateTo(url),
}));

describe('the handoff to a tenant own address', () => {
  let replaceState: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    setActivePinia(createPinia());
    push.mockClear();
    navigateTo.mockClear();
    window.history.replaceState({}, '', '/admin/login?redirect=%2Fadmin%2F');
    replaceState = vi.spyOn(window.history, 'replaceState');
  });
  afterEach(() => {
    replaceState.mockRestore();
    window.history.replaceState({}, '', '/');
  });

  it('goes to the tenant sign-in page with the ticket in the fragment', async () => {
    withCaps({ realm: { enabled: true, locked_realm: null } });
    const auth = useAuthStore();
    vi.spyOn(auth, 'login').mockImplementation(async () => {
      auth.handoff = { origin: 'https://files.acme.test', code: 'T1' };
      return false;
    });
    const wrapper = mountLogin();
    await flushPromises();
    await fillAndSubmit(wrapper, 'acme');
    expect(navigateTo).toHaveBeenCalledWith('https://files.acme.test/admin/login?redirect=%2Fadmin%2F#handoff=T1');
    expect(push).not.toHaveBeenCalled();
    expect(wrapper.get('[data-testid="login-redirecting"]').text()).toContain(en.login.handoffGoing);
  });

  it('redeems a ticket in its own fragment, after taking it out of the address bar', async () => {
    window.location.hash = '#handoff=T2';
    withCaps({ realm: { enabled: true, locked_realm: 'acme' } });
    const auth = useAuthStore();
    const redeem = vi.spyOn(auth, 'redeemHandoff').mockResolvedValue(true);
    mountLogin();
    await flushPromises();
    expect(replaceState).toHaveBeenCalled();
    expect(window.location.hash).toBe('');
    expect(window.location.href).not.toContain('T2');
    expect(redeem).toHaveBeenCalledWith('T2');
    expect(push).toHaveBeenCalledWith('/');
  });

  it('says so when the ticket was spent or expired', async () => {
    window.location.hash = '#handoff=T3';
    withCaps({ realm: { enabled: true, locked_realm: 'acme' } });
    const auth = useAuthStore();
    vi.spyOn(auth, 'redeemHandoff').mockResolvedValue(false);
    const wrapper = mountLogin();
    await flushPromises();
    expect(push).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain(en.login.errHandoff);
  });
});
