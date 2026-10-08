import { api } from './client';
import { getApiBaseUrl } from './runtimeConfig';
import type { AccountCheckAnswer, AccountCheckQuery } from '@brftech/filex-core';

import type { LoginMethods, LoginRequest, LoginResponse, MeResponse, User } from './types';

/** The request options that say which language is on screen, or none. */
function screenLanguage(lang?: string): { headers: Record<string, string> } | undefined {
  const code = (lang ?? '').trim();
  return code ? { headers: { 'Accept-Language': code } } : undefined;
}

export const AuthApi = {
  async me(): Promise<MeResponse> {
    const { data } = await api.get<MeResponse>('/auth/me');
    return data;
  },

  /**
   * `lang` is the language on screen. It travels as Accept-Language: an
   * account that holds no language yet is given it by the server at this
   * sign-in (handlers/auth.go adoptSignInLanguage) - the panel writes no
   * language afterwards (0.54).
   */
  async login(payload: LoginRequest, lang?: string): Promise<LoginResponse> {
    const { data } = await api.post<LoginResponse>('/auth/login', payload, screenLanguage(lang));
    return data;
  },

  /** Redeems a sign-in handed over from the platform's address (see
   *  LoginHandoff): the session is opened on THIS address. `lang` as for
   *  `login`. */
  async handoff(code: string, lang?: string): Promise<LoginResponse> {
    const { data } = await api.post<LoginResponse>('/auth/handoff', { code }, screenLanguage(lang));
    return data;
  },

  /**
   * Ends filex's session. For an SSO session the answer also carries
   * `logout_url` — the IdP's end-session URL, where the browser continues so
   * the IdP's session ends too. `returnTo` is the sign-in page the IdP sends
   * the browser back to (`/admin/login` or `/drive/login`; lib/signOut).
   */
  async logout(returnTo?: string): Promise<{ ok?: boolean; logout_url?: string }> {
    const { data } = await api.post<{ ok?: boolean; logout_url?: string }>(
      '/auth/logout',
      returnTo ? { return_to: returnTo } : undefined,
    );
    return data ?? {};
  },

  /**
   * How a sign-in for a realm may go: its SSO buttons, whether the password
   * form answers (docs/TENANT-ADMIN.md). The page asks when a realm is typed;
   * a realm nobody has answers like a tenant with no SSO.
   */
  async methods(realm: string): Promise<LoginMethods> {
    const { data } = await api.get<Partial<LoginMethods>>('/auth/methods', { params: realm ? { realm } : undefined });
    return {
      password: data.password !== false,
      recovery: data.recovery === true,
      sso: Array.isArray(data.sso) ? data.sso : [],
    };
  },

  /** A navigation, not a request — under the API base (and so under the base
   *  path a sub-path deployment serves filex at). `instance` and `realm` pick
   *  one SSO of one tenant (a tenant with no address of its own signs in on
   *  the platform's). */
  oidcStartUrl(
    provider: string = 'oidc',
    returnTo: string = '/admin/',
    pick: { instance?: string; realm?: string } = {},
  ): string {
    const qs = new URLSearchParams({ provider, return_to: returnTo });
    if (pick.instance) qs.set('instance', pick.instance);
    if (pick.realm) qs.set('realm', pick.realm);
    return `${getApiBaseUrl()}/auth/oidc/start?${qs.toString()}`;
  },

  async updateProfile(patch: Partial<User> & { password?: string }): Promise<User> {
    const { data } = await api.patch<User>('/auth/profile', patch);
    return data;
  },

  /** Would this address / username be accepted? The server's rules and its
   *  words (POST /api/auth/account/check, core lib/accountRules). */
  async checkAccount(q: AccountCheckQuery): Promise<AccountCheckAnswer> {
    const { data } = await api.post<AccountCheckAnswer>('/auth/account/check', q);
    return data ?? {};
  },

  async changePassword(current: string, next: string): Promise<void> {
    await api.post('/auth/password', { current_password: current, new_password: next });
  },

  // ⚠ `recovery_codes` is part of the response and must stay part of the type.
  // The backend generates ten codes, STORES them against the pending secret
  // (auth_self.go:189) and hands them back exactly once. Dropping them from
  // the type is how they stopped being rendered: a user who lost their
  // authenticator had ten valid codes that nobody had ever shown them.
  async enrollTotp(): Promise<{
    secret: string;
    otpauth_url: string;
    qr_svg: string;
    recovery_codes: string[];
  }> {
    const { data } = await api.post('/auth/totp/enroll');
    return data;
  },

  async verifyTotp(code: string): Promise<void> {
    await api.post('/auth/totp/verify', { code });
  },

  async disableTotp(code: string): Promise<void> {
    await api.post('/auth/totp/disable', { code });
  },
};
