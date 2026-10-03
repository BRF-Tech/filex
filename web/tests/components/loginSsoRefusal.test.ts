// The sign-in page after a refused SSO sign-in says WHY (0.50).
//
// Red proof for what this replaced: every refusal came back as `?error=oidc`
// and the page said "SSO sign-in failed" - whether no account is opened at a
// first sign-in, the person is in no allowed group, or the identity provider
// cancelled - and `?maintenance=1` (a disabled tenant, maintenance mode) said
// nothing at all. The server now adds a reason code; the page turns it into a
// sentence. The one refusal that keeps no code (an e-mail address with an
// account in another tenant) must read exactly like any failure.
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import Login from '@/views/Login.vue';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { AuthApi } from '@/api/auth';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

vi.mock('@/api/branding', () => ({
  BrandingApi: { get: vi.fn().mockResolvedValue({}) },
}));

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

async function mountLogin(locale: 'en' | 'tr' = 'en') {
  const caps = useCapabilitiesStore();
  // SSO-first: the page would start SSO by itself on an ordinary visit.
  caps.data = { ...caps.data, auth_drivers: ['local', 'oidc'], oidc_auto_redirect: true };
  caps.loaded = true;
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const wrapper = mount(Login, {
    global: {
      plugins: [i18n],
      stubs: { LogoMark: true, LocaleSwitcher: true, DarkModeToggle: true, Input: true, Checkbox: true },
    },
  });
  await flushPromises();
  return wrapper;
}

// The page's SSO alert (the one above the form; the form's own is
// `lg-alert--form`). Selected the way the page before 0.50 drew it too, so the
// generic case below holds on both sides of the change.
const alertText = (w: Awaited<ReturnType<typeof mountLogin>>) =>
  w.get('p.lg-alert[role="alert"]:not(.lg-alert--form)').text();

describe('sign-in page after a refused SSO sign-in', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  const said: Array<[Record<string, string>, string]> = [
    [{ error: 'oidc', reason: 'auto_create_off' }, en.login.ssoRefused.auto_create_off],
    [{ error: 'oidc', reason: 'group_not_allowed' }, en.login.ssoRefused.group_not_allowed],
    [{ error: 'oidc', reason: 'no_email' }, en.login.ssoRefused.no_email],
    [{ error: 'oidc', reason: 'idp_denied' }, en.login.ssoRefused.idp_denied],
    [{ error: 'oidc', reason: 'idp_error' }, en.login.ssoRefused.idp_error],
    [{ error: 'oidc', reason: 'expired' }, en.login.ssoRefused.expired],
    [{ error: 'oidc', reason: 'account_disabled' }, en.login.ssoRefused.account_disabled],
    [{ error: 'oidc', reason: 'busy' }, en.login.ssoRefused.busy],
    // Which account an SSO sign-in opens (0.50): an unverified address and an
    // unbound account, an account opened waiting for approval, an account
    // bound to another SSO identity - each in the tenant the sign-in is for.
    [{ error: 'oidc', reason: 'email_unverified' }, en.login.ssoRefused.email_unverified],
    [{ error: 'oidc', reason: 'account_pending' }, en.login.ssoRefused.account_pending],
    [{ error: 'oidc', reason: 'identity_mismatch' }, en.login.ssoRefused.identity_mismatch],
    [{ maintenance: '1', reason: 'tenant_suspended' }, en.login.ssoRefused.tenant_suspended],
    [{ maintenance: '1', reason: 'maintenance' }, en.login.ssoRefused.maintenance],
  ];
  it.each(said)('%o says what happened and what to do', async (q, sentence) => {
    query = q;
    const wrapper = await mountLogin();
    expect(alertText(wrapper)).toBe(sentence);
    expect(alertText(wrapper)).not.toBe(en.login.errOidc);
    expect(AuthApi.oidcStartUrl, 'a refused sign-in must not loop back to the IdP').not.toHaveBeenCalled();
  });

  it('a held sign-in with no code still says something (it used to say nothing)', async () => {
    query = { maintenance: '1' };
    const wrapper = await mountLogin();
    expect(alertText(wrapper)).toBe(en.login.ssoRefused.maintenance);
  });

  it('in Turkish', async () => {
    query = { error: 'oidc', reason: 'group_not_allowed' };
    const wrapper = await mountLogin('tr');
    expect(alertText(wrapper)).toBe(tr.login.ssoRefused.group_not_allowed);
  });

  // ⚠⚠ An account in another tenant comes back with no code: the page says
  // exactly what it says for any failure, and an unknown or crafted code reads
  // the same - the code itself never reaches the screen.
  it.each([{ error: 'oidc' }, { error: 'oidc', reason: 'other_tenant' }, { error: 'oidc', reason: '<b>acme</b>' }])(
    '%o is the generic sentence',
    async (q) => {
      query = q;
      const wrapper = await mountLogin();
      expect(alertText(wrapper)).toBe(en.login.errOidc);
      expect(wrapper.html()).not.toContain('acme');
      expect(wrapper.html()).not.toContain('other_tenant');
    },
  );
});
