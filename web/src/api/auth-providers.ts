import { api } from './client';
import type {
  AuthProvider,
  AuthProviderCheck,
  AuthProviderField,
  AuthProviderTestAccount,
  AuthProviderTenant,
  AuthProviderTestResult,
  AuthProvidersOverview,
} from './types';

export interface AuthProviderUpdate {
  enabled?: boolean;
  /** What the sign-in page calls it. */
  label?: string;
  config?: Record<string, unknown>;
  /** Switch it on although its test failed — only after the operator agreed. */
  confirm_failed_test?: boolean;
  /**
   * The account the test of an operating-system provider signs in with (see
   * AuthProvider.test_account_required). Used by the server for this one
   * request, never stored; it becomes the super administrator when the save
   * switches the provider on.
   */
  test_account?: AuthProviderTestAccount;
  /** The operator agreed that a tenant loses its last way in until another is bound. */
  confirm_tenant_lockout?: boolean;
}

/** Another instance of a driver (POST /admin/auth-providers). */
export interface AuthProviderCreate extends AuthProviderUpdate {
  driver: string;
  slug?: string;
  tenants?: number[];
}

// Backend wire shape (handlers/auth_providers.go → providerView).
export interface BackendProvider {
  name: string;
  driver?: string;
  instance_id?: number;
  label?: string;
  owner_provider_id?: number | null;
  tenants?: number[];
  enabled: boolean;
  capabilities?: Record<string, boolean>;
  config_redacted?: Record<string, unknown>;
  testable?: boolean;
  managed?: boolean;
  origin?: 'environment' | 'page' | 'tenant' | 'builtin';
  from?: string;
  state?: 'running' | 'failed' | 'off';
  error?: string;
  legacy?: boolean;
  shadowed?: boolean;
  secrets_set?: Record<string, boolean>;
  fields?: AuthProviderField[];
  test_account_required?: boolean;
  set_by_upgrade?: string[];
}
interface ListResponse {
  providers: BackendProvider[];
  password_sign_in?: boolean;
  recovery_login?: boolean;
  secret_key?: boolean;
  multi_tenant?: boolean;
  review_pending?: boolean;
  tenants?: AuthProviderTenant[];
}

/**
 * The server says how a provider stands (`state`: running | failed | off);
 * the badge's `status` follows it, so a provider that is switched on but
 * could not start is never shown as "Active".
 */
export function toAuthProvider(p: BackendProvider): AuthProvider {
  const state = p.state ?? (p.enabled ? 'running' : 'off');
  return {
    id: p.name,
    driver: p.driver ?? p.name,
    instance_id: p.instance_id ?? 0,
    label: p.label ?? '',
    owner_provider_id: p.owner_provider_id ?? null,
    tenants: p.tenants,
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
    test_account_required: p.test_account_required === true,
    set_by_upgrade: p.set_by_upgrade ?? [],
  };
}

/**
 * What a save answered: applied, or refused because the test failed.
 *
 * `superAdmin`: the test account of an operating-system provider passed and
 * is now a super administrator of the instance. `confirmAllowed` false (with
 * `strict`): the provider is one that is never switched on over a failing
 * test — there is no "switch on anyway" to offer.
 */
export type AuthProviderSaveResult =
  | { status: 'saved'; provider: AuthProvider | null; checks: AuthProviderCheck[]; testOk: boolean; superAdmin: boolean }
  | {
      status: 'test_failed';
      message: string;
      checks: AuthProviderCheck[];
      failed: string[];
      confirmAllowed: boolean;
      strict: boolean;
    };

export const AuthProvidersApi = {
  async overview(): Promise<AuthProvidersOverview> {
    const { data } = await api.get<ListResponse>('/admin/auth-providers');
    return {
      providers: (data.providers ?? []).map(toAuthProvider),
      passwordSignIn: data.password_sign_in !== false,
      recoveryLogin: data.recovery_login === true,
      secretKey: data.secret_key !== false,
      multiTenant: data.multi_tenant === true,
      reviewPending: data.review_pending === true,
      tenants: data.tenants ?? [],
    };
  },

  /** Another instance of a driver; created switched off unless `enabled`. */
  async create(input: AuthProviderCreate): Promise<AuthProvider | null> {
    const { data } = await api.post<{ provider?: BackendProvider }>('/admin/auth-providers', input);
    return data.provider ? toAuthProvider(data.provider) : null;
  },

  /** Delete an instance made with create (a driver's first is switched off instead). */
  async remove(id: string, confirmTenantLockout = false): Promise<void> {
    await api.delete(`/admin/auth-providers/${id}`, {
      params: confirmTenantLockout ? { confirm_tenant_lockout: 1 } : undefined,
    });
  },

  /** Which tenants sign in through it: the list replaces its bindings. */
  async setTenants(id: string, tenants: number[], confirmTenantLockout = false): Promise<AuthProvider | null> {
    const { data } = await api.put<{ provider?: BackendProvider }>(`/admin/auth-providers/${id}/tenants`, {
      tenants,
      confirm_tenant_lockout: confirmTenantLockout || undefined,
    });
    return data.provider ? toAuthProvider(data.provider) : null;
  },

  /** The operator reviewed the bindings the upgrade made. */
  async dismissReview(): Promise<void> {
    await api.patch('/admin/settings', { 'auth.instances.review_pending': '0' });
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
        super_admin?: boolean;
      }>(`/admin/auth-providers/${id}`, patch);
      return {
        status: 'saved',
        provider: data.provider ? toAuthProvider(data.provider) : null,
        checks: Array.isArray(data.checks) ? data.checks : [],
        testOk: data.test_ok === true,
        superAdmin: data.super_admin === true,
      };
    } catch (e: unknown) {
      const res = (e as { response?: { status?: number; data?: Record<string, unknown> } }).response;
      if (res?.status === 409 && res.data?.error === 'test_failed') {
        return {
          status: 'test_failed',
          message: String(res.data.message ?? ''),
          checks: Array.isArray(res.data.checks) ? (res.data.checks as AuthProviderCheck[]) : [],
          failed: Array.isArray(res.data.failed) ? (res.data.failed as string[]) : [],
          // A server that does not say is one from before strict providers:
          // every failing test could be confirmed there.
          confirmAllowed: res.data.confirm_allowed !== false,
          strict: res.data.strict === true,
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
   *
   * `testAccount` (an operating-system provider) travels BESIDE the draft —
   * `{config, test_account}`, the documented shape — and is used by the server
   * for this one request. Without one the draft is sent as it always was.
   */
  async test(
    id: AuthProvider['id'],
    draft: Record<string, unknown>,
    testAccount?: AuthProviderTestAccount,
  ): Promise<AuthProviderTestResult> {
    const body = testAccount ? { config: draft, test_account: testAccount } : draft;
    const { data } = await api.post<Partial<AuthProviderTestResult>>(`/admin/auth-providers/${id}/test`, body);
    return {
      testable: data.testable === true,
      ok: data.ok === true,
      checks: Array.isArray(data.checks) ? data.checks : [],
    };
  },
};
