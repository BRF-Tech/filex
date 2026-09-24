import { api } from './client';
import type {
  AuthProvider,
  AuthProviderCheck,
  AuthProviderField,
  AuthProviderTestResult,
  AuthProvidersOverview,
} from './types';

export interface AuthProviderUpdate {
  enabled?: boolean;
  config?: Record<string, unknown>;
  /** Switch it on although its test failed — only after the operator agreed. */
  confirm_failed_test?: boolean;
}

// Backend wire shape (handlers/auth_providers.go → providerView).
interface BackendProvider {
  name: string;
  enabled: boolean;
  capabilities?: Record<string, boolean>;
  config_redacted?: Record<string, unknown>;
  testable?: boolean;
  managed?: boolean;
  origin?: 'environment' | 'page' | 'builtin';
  from?: string;
  state?: 'running' | 'failed' | 'off';
  error?: string;
  legacy?: boolean;
  shadowed?: boolean;
  secrets_set?: Record<string, boolean>;
  fields?: AuthProviderField[];
}
interface ListResponse {
  providers: BackendProvider[];
  password_sign_in?: boolean;
  recovery_login?: boolean;
  secret_key?: boolean;
}

/**
 * The server says how a provider stands (`state`: running | failed | off);
 * the badge's `status` follows it, so a provider that is switched on but
 * could not start is never shown as "Active".
 */
function toAuthProvider(p: BackendProvider): AuthProvider {
  const state = p.state ?? (p.enabled ? 'running' : 'off');
  return {
    id: p.name as AuthProvider['id'],
    enabled: p.enabled,
    config: {},
    config_redacted: p.config_redacted ?? {},
    status: state === 'running' ? 'ok' : state === 'failed' ? 'misconfigured' : 'disabled',
    last_error: p.error || null,
    testable: p.testable === true,
    managed: p.managed === true,
    origin: p.origin ?? 'page',
    from: p.from ?? '',
    state,
    legacy: p.legacy === true,
    shadowed: p.shadowed === true,
    secrets_set: p.secrets_set ?? {},
    fields: p.fields ?? [],
  };
}

/** What a save answered: applied, or refused because the test failed. */
export type AuthProviderSaveResult =
  | { status: 'saved'; provider: AuthProvider | null; checks: AuthProviderCheck[]; testOk: boolean }
  | { status: 'test_failed'; message: string; checks: AuthProviderCheck[]; failed: string[] };

export const AuthProvidersApi = {
  async overview(): Promise<AuthProvidersOverview> {
    const { data } = await api.get<ListResponse>('/admin/auth-providers');
    return {
      providers: (data.providers ?? []).map(toAuthProvider),
      passwordSignIn: data.password_sign_in !== false,
      recoveryLogin: data.recovery_login === true,
      secretKey: data.secret_key !== false,
    };
  },

  async list(): Promise<AuthProvider[]> {
    return (await AuthProvidersApi.overview()).providers;
  },

  /**
   * Save and apply. ⚠ A 409 `test_failed` is not an error to toast: it is
   * the server asking whether to switch the provider on although its test
   * failed, with the steps that failed — the page asks the operator and
   * sends `confirm_failed_test` only on a yes. Every other refusal throws,
   * carrying the server's sentence.
   */
  async update(id: AuthProvider['id'], patch: AuthProviderUpdate): Promise<AuthProviderSaveResult> {
    try {
      const { data } = await api.patch<{
        provider?: BackendProvider;
        checks?: AuthProviderCheck[];
        test_ok?: boolean;
      }>(`/admin/auth-providers/${id}`, patch);
      return {
        status: 'saved',
        provider: data.provider ? toAuthProvider(data.provider) : null,
        checks: Array.isArray(data.checks) ? data.checks : [],
        testOk: data.test_ok === true,
      };
    } catch (e: unknown) {
      const res = (e as { response?: { status?: number; data?: Record<string, unknown> } }).response;
      if (res?.status === 409 && res.data?.error === 'test_failed') {
        return {
          status: 'test_failed',
          message: String(res.data.message ?? ''),
          checks: Array.isArray(res.data.checks) ? (res.data.checks as AuthProviderCheck[]) : [],
          failed: Array.isArray(res.data.failed) ? (res.data.failed as string[]) : [],
        };
      }
      throw e;
    }
  },

  /**
   * Test the configuration ON THE SCREEN — `draft` is the form as it stands,
   * unsaved edits included; a secret left blank is the stored one. A
   * provider the environment defines is tested on the environment's
   * configuration. The answer is step by step (auth.ProbeCheck).
   */
  async test(id: AuthProvider['id'], draft: Record<string, unknown>): Promise<AuthProviderTestResult> {
    const { data } = await api.post<Partial<AuthProviderTestResult>>(`/admin/auth-providers/${id}/test`, draft);
    return {
      testable: data.testable === true,
      ok: data.ok === true,
      checks: Array.isArray(data.checks) ? data.checks : [],
    };
  },
};
