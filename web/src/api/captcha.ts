// Bot protection (#294) - the administrator's side of the captcha.
//
// Contract (the instance's, so the whole surface answers 403
// `supertenant_only` to a tenant's administrator):
//
//   GET   /api/admin/captcha         the view below
//   PATCH /api/admin/captcha         partial body; '' clears a panel value
//                                    (the environment's, or the default, is
//                                    then in force); 400 captcha_bad_value
//                                    {field}, 409 captcha_no_secret_key
//   POST  /api/admin/captcha/test    {provider?, site_key?, secret?}: can the
//                                    server reach the provider, does it take
//                                    the secret
//
// ⚠ The secret is written, never read: every answer says only whether one is
// stored (`has_secret`). Order of precedence: this panel > the environment
// (CAPTCHA_PROVIDER, CAPTCHA_SITE_KEY, CAPTCHA_SECRET, CAPTCHA_FORMS) > off.
import { api } from './client';
import { readCaptchaConfig, type CaptchaForm, type CaptchaProvider, type CaptchaPublicConfig } from '@brftech/filex-core';

export type CaptchaFallback = 'local' | 'off';
/** Where a value in force came from. */
export type CaptchaSource = 'db' | 'env' | 'default';

export interface CaptchaSettingsView {
  provider: CaptchaProvider;
  site_key: string;
  has_secret: boolean;
  /** null = not set at this level. */
  forms: CaptchaForm[] | null;
  fallback: CaptchaFallback;
  verify_timeout_ms: number;
}

export interface CaptchaAdminState {
  /** What the panel itself holds ('' / null where it holds nothing). */
  stored: CaptchaSettingsView;
  /** What is in force: the panel, else the environment, else the default. */
  effective: CaptchaSettingsView;
  sources: Partial<Record<'provider' | 'site_key' | 'secret' | 'forms' | 'fallback' | 'verify_timeout_ms', CaptchaSource>>;
  /** '' or why the setup cannot verify a token (the fallback answers meanwhile). */
  misconfigured: string;
  /** The server can seal a secret (FILEX_SECRET_KEY): without it none can be stored here. */
  secret_key_configured: boolean;
  /** In an outage window right now. */
  fallback_active: boolean;
  /** How many requests the fallback has answered since the server started. */
  fallback_total: number;
  all_forms: CaptchaForm[];
  providers: CaptchaProvider[];
  bounds: { verify_timeout_ms: [number, number] };
}

export interface CaptchaPatch {
  provider?: CaptchaProvider | '';
  site_key?: string;
  secret?: string;
  clear_secret?: boolean;
  forms?: CaptchaForm[];
  clear_forms?: boolean;
  fallback?: CaptchaFallback | '';
  verify_timeout_ms?: number;
}

export interface CaptchaTestResult {
  reachable: boolean;
  secret_ok: boolean;
  codes: string[];
  misconfigured: string;
  cause: string;
  /** The server's sentence, in the reader's language. */
  message: string;
}

/** The sign-in page's side: which forms the server protects (GET /api/captcha/config, no session). */
export const CaptchaPublicApi = {
  async config(): Promise<CaptchaPublicConfig | null> {
    // ⚠ No header of its own: a custom one is a CORS preflight for a
    // cross-origin client. The server answers no-store.
    const { data } = await api.get<unknown>('/captcha/config');
    return readCaptchaConfig(data);
  },
};

export const CaptchaAdminApi = {
  async get(): Promise<CaptchaAdminState> {
    const { data } = await api.get<CaptchaAdminState>('/admin/captcha');
    return data;
  },
  async update(patch: CaptchaPatch): Promise<CaptchaAdminState> {
    const { data } = await api.patch<CaptchaAdminState>('/admin/captcha', patch);
    return data;
  },
  async test(body: { provider?: CaptchaProvider; site_key?: string; secret?: string }): Promise<CaptchaTestResult> {
    const { data } = await api.post<CaptchaTestResult>('/admin/captcha/test', body);
    return data;
  },
};
