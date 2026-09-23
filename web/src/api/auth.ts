import { api } from './client';
import type { LoginRequest, LoginResponse, MeResponse, User } from './types';

export const AuthApi = {
  async me(): Promise<MeResponse> {
    const { data } = await api.get<MeResponse>('/auth/me');
    return data;
  },

  async login(payload: LoginRequest): Promise<LoginResponse> {
    const { data } = await api.post<LoginResponse>('/auth/login', payload);
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

  oidcStartUrl(provider: string = 'oidc', returnTo: string = '/admin/'): string {
    const qs = new URLSearchParams({ provider, return_to: returnTo });
    return `/api/auth/oidc/start?${qs.toString()}`;
  },

  async updateProfile(patch: Partial<User> & { password?: string }): Promise<User> {
    const { data } = await api.patch<User>('/auth/profile', patch);
    return data;
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
