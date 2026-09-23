// SSO-first sign-in page, right after signing out (`?signed_out`).
//
// Red proof for the defect this replaced: the page started SSO on its own the
// moment it mounted, including straight after "Sign out". Wherever the IdP's
// session survived that (an IdP without RP-initiated logout, or
// FILEX_OIDC_LOGOUT=local), the same account was signed back in without a
// form — sign-out looked like a page flash. After signing out the page now
// says so and waits for the person to choose.
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import Login from '@/views/Login.vue';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { AuthApi } from '@/api/auth';
import en from '@/locales/en.json';

vi.mock('@/api/branding', () => ({
  BrandingApi: { get: vi.fn().mockResolvedValue({}) },
}));

// startOidc() assigns this to window.location.href; a hash keeps happy-dom on
// the page.
vi.mock('@/api/auth', () => ({
  AuthApi: {
    me: vi.fn(),
    login: vi.fn(),
    logout: vi.fn(),
    oidcStartUrl: vi.fn(() => '#sso-started'),
  },
}));

let query: Record<string, string> = {};
vi.mock('vue-router', () => ({
  useRoute: () => ({ query }),
  useRouter: () => ({ push: vi.fn() }),
}));

async function mountLogin() {
  const caps = useCapabilitiesStore();
  caps.data = { ...caps.data, auth_drivers: ['local', 'oidc'], oidc_auto_redirect: true };
  caps.loaded = true;
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  const wrapper = mount(Login, {
    global: {
      plugins: [i18n],
      stubs: { LogoMark: true, LocaleSwitcher: true, DarkModeToggle: true, Input: true, Checkbox: true },
    },
  });
  await flushPromises();
  return wrapper;
}

describe('SSO-first sign-in page after signing out', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it('starts SSO by itself on an ordinary visit', async () => {
    query = {};
    await mountLogin();
    expect(AuthApi.oidcStartUrl).toHaveBeenCalledTimes(1);
  });

  it('does not start SSO by itself straight after signing out, and says so', async () => {
    query = { signed_out: '1' };
    const wrapper = await mountLogin();

    expect(AuthApi.oidcStartUrl).not.toHaveBeenCalled();
    expect(wrapper.get('[data-testid="login-signed-out"]').text()).toBe(en.login.signedOut);
    // …and SSO is one click away for whoever signs in next.
    const sso = wrapper.findAll('button').find((b) => b.text().includes(en.login.oidc));
    expect(sso, 'SSO button rendered').toBeTruthy();
    await sso!.trigger('click');
    expect(AuthApi.oidcStartUrl).toHaveBeenCalledTimes(1);
  });
});
