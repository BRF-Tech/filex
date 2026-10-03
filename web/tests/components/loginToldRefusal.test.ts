// The two other ways a refusal's reason reaches the sign-in page (0.50):
//
//   - the password form: a provider whose operator switched
//     show_refusal_reason on answers a person whose password was right 403
//     `{reason}` (and the account gates carry theirs);
//   - the header proxy: /api/auth/me's 401 carries the first-login rule's
//     reason, read by the auth store (`signInReason`).
//
// Both say the same sentences as the SSO page (lib/ssoRefusal). Red proof:
// before, the form showed the server's English words ("sign-in refused",
// "this account is disabled") or nothing, and the proxy's refusal was a bare
// sign-in page.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import Login from '@/views/Login.vue';
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { AuthApi } from '@/api/auth';
import { readLoginRefusal } from '@/lib/loginRefusal';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

let loginAnswer: { status: number; data: Record<string, unknown> } | null = null;
let meAnswer: { status: number; data: Record<string, unknown> } | null = null;

const refusal = (a: { status: number; data: Record<string, unknown> } | null) =>
  Object.assign(new Error('refused'), {
    isAxiosError: true,
    response: { status: a?.status, data: a?.data, headers: {} },
  });

vi.mock('@/api/branding', () => ({ BrandingApi: { get: vi.fn().mockResolvedValue({}) } }));
vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock('@/api/auth', () => ({
  AuthApi: {
    login: vi.fn(async () => {
      throw refusal(loginAnswer);
    }),
    me: vi.fn(async () => {
      throw refusal(meAnswer);
    }),
    oidcStartUrl: vi.fn(() => '#sso-started'),
  },
}));
vi.mock('@/api/client', () => ({
  extractError: (e: unknown, f: string) =>
    (e as { response?: { data?: { error?: string } } }).response?.data?.error ?? f,
  api: { get: vi.fn(), post: vi.fn() },
}));
vi.mock('axios', async (orig) => {
  const real = await orig<typeof import('axios')>();
  const isAxiosError = (e: unknown) => !!(e as { isAxiosError?: boolean })?.isAxiosError;
  return { ...real, isAxiosError, default: { ...real.default, isAxiosError } };
});

describe('readLoginRefusal reads a told reason', () => {
  it('from a 403 that carries a known code, and from nothing else', () => {
    expect(readLoginRefusal(refusal({ status: 403, data: { error: 'sign-in refused', reason: 'group_not_allowed' } }))).toEqual({
      status: 403, locked: false, reason: 'group_not_allowed',
    });
    expect(readLoginRefusal(refusal({ status: 403, data: { reason: 'other_tenant' } }))).toBeNull();
    expect(readLoginRefusal(refusal({ status: 403, data: { error: 'this account is disabled', disabled: true } }))).toBeNull();
    expect(readLoginRefusal(refusal({ status: 401, data: { error: 'invalid credentials', reason: 'auto_create_off' } }))?.reason).toBeUndefined();
  });
});

describe('the sign-in page', () => {
  const mounted: VueWrapper[] = [];

  async function mountLogin(locale: 'en' | 'tr' = 'en', autoRedirect = false) {
    const caps = useCapabilitiesStore();
    caps.data = { ...caps.data, auth_drivers: ['local', 'oidc'], oidc_auto_redirect: autoRedirect };
    caps.loaded = true;
    const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
    const w = mount(Login, {
      global: {
        plugins: [i18n],
        stubs: { LogoMark: true, LocaleSwitcher: true, DarkModeToggle: true, RouterLink: true },
      },
    });
    mounted.push(w);
    await flushPromises();
    return w;
  }

  async function attempt(w: VueWrapper) {
    await w.get('input#email').setValue('can');
    await w.get('input#password').setValue('right-pw');
    await w.get('form').trigger('submit');
    await flushPromises();
  }

  const formError = (w: VueWrapper) => w.get('p.lg-alert--form[role="alert"]').text();
  const ssoAlert = (w: VueWrapper) => w.find('p.lg-alert[role="alert"]:not(.lg-alert--form)');

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });
  afterEach(() => {
    while (mounted.length) mounted.pop()!.unmount();
    loginAnswer = null;
    meAnswer = null;
  });

  it.each([
    [{ error: 'sign-in refused', reason: 'auto_create_off' }, en.login.ssoRefused.auto_create_off],
    [{ error: 'sign-in refused', reason: 'group_not_allowed' }, en.login.ssoRefused.group_not_allowed],
    [{ error: 'sign-in refused', reason: 'forbidden_account' }, en.login.ssoRefused.forbidden_account],
    [{ error: 'this account is disabled', disabled: true, reason: 'account_disabled' }, en.login.ssoRefused.account_disabled],
    [{ error: 'sign-in is temporarily limited to the platform operator', maintenance: true, reason: 'tenant_suspended' }, en.login.ssoRefused.tenant_suspended],
  ])('the password form says why for %o', async (data, sentence) => {
    loginAnswer = { status: 403, data };
    const w = await mountLogin();
    await attempt(w);
    expect(formError(w)).toBe(sentence);
  });

  it('in Turkish', async () => {
    loginAnswer = { status: 403, data: { error: 'sign-in refused', reason: 'group_not_allowed' } };
    const w = await mountLogin('tr');
    await attempt(w);
    expect(formError(w)).toBe(tr.login.ssoRefused.group_not_allowed);
  });

  it('a header-proxy refusal on /api/auth/me is said on the page, and SSO-first does not jump away', async () => {
    meAnswer = { status: 401, data: { error: 'unauthorized', reason: 'auto_create_off' } };
    await useAuthStore().fetchMe();
    expect(useAuthStore().signInReason).toBe('auto_create_off');
    const w = await mountLogin('en', true);
    expect(ssoAlert(w).text()).toBe(en.login.ssoRefused.auto_create_off);
    expect(AuthApi.oidcStartUrl).not.toHaveBeenCalled();
  });

  it('a plain 401 on /api/auth/me says nothing', async () => {
    meAnswer = { status: 401, data: { error: 'unauthorized' } };
    await useAuthStore().fetchMe();
    expect(useAuthStore().signInReason).toBeNull();
    const w = await mountLogin();
    expect(ssoAlert(w).exists()).toBe(false);
  });
});
