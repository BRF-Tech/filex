import { api } from './client';
import { toAuthProvider, type BackendProvider } from './auth-providers';
import type { AuthProvider, AuthProviderCheck, AuthProviderField, AuthProviderTestResult } from './types';

/**
 * A tenant runs itself (backend handlers/tenant_self.go, docs/TENANT-ADMIN.md):
 * its own OIDC and LDAP, its own domains. A tenant's administrator acts on
 * their own tenant; the platform operator names one (`tenantId`).
 */

export type DomainStatus = 'pending' | 'active' | 'suspended';

export interface TenantDomain {
  id: number;
  provider_id: number;
  domain: string;
  status: DomainStatus;
  /** What the last check found when it did not find the CNAME. */
  last_error?: string;
  /**
   * The same as last_error, for the screen to say in the reader's language:
   * no_record, no_cname, points_elsewhere, wildcard_cname, dns_failed, with
   * its names (domain, found, target, tenant_domain, detail). A code this
   * screen does not know falls back to last_error.
   */
  last_error_code?: string;
  last_error_params?: Record<string, string>;
  checked_at?: string | null;
  active_since?: string | null;
  /** The tenant's own certificate expires then (none: the installation's way). */
  tls_not_after?: string | null;
  /** What its CNAME must point at: the tenant's platform subdomain. */
  target: string;
  own_certificate: boolean;
  /**
   * What filex's own ACME last did for it (FILEX_TLS_MODE=acme, no
   * certificate of its own): `none` nothing asked yet, `obtained` until
   * `not_after`, `failed` at `at` with the authority's `reason` (English, its
   * own words). The memory of the process that answered.
   */
  acme?: TenantDomainACME;
}

export interface TenantDomainACME {
  state: 'none' | 'obtained' | 'failed';
  not_after?: string;
  reason?: string;
  at?: string;
}

/** A provider the operator bound to the tenant: named, never configured here. */
export interface SharedProvider {
  name: string;
  driver: string;
  label?: string;
  state: 'running' | 'failed' | 'off';
}

export interface TenantOverview {
  tenant: {
    id: number;
    name: string;
    slug: string;
    realm: string;
    host?: string;
    allow_insecure_auth: boolean;
  };
  /** `<realm>.<tenant domain>`, "" when the installation gives none. */
  platform_subdomain: string;
  tenant_domain: string;
  /** Who issues certificates: the proxy in front, or filex itself (acme). */
  tls_mode: 'proxy' | 'acme' | string;
  /** The kinds a tenant may add (oidc, ldap) and their fields. */
  drivers: string[];
  fields: Record<string, AuthProviderField[]>;
  providers: AuthProvider[];
  shared_providers: SharedProvider[];
  domains: TenantDomain[];
}

export interface OwnProviderSave {
  enabled?: boolean;
  label?: string;
  config?: Record<string, unknown>;
  confirm_failed_test?: boolean;
  confirm_tenant_lockout?: boolean;
}

const q = (tenantId?: number) => (tenantId ? { params: { tenant: tenantId } } : {});

export const TenantSelfApi = {
  async get(tenantId?: number): Promise<TenantOverview> {
    const { data } = await api.get<Omit<TenantOverview, 'providers'> & { providers?: BackendProvider[] }>('/admin/tenant/', q(tenantId));
    return {
      ...data,
      drivers: data.drivers ?? [],
      fields: data.fields ?? {},
      providers: (data.providers ?? []).map(toAuthProvider),
      shared_providers: data.shared_providers ?? [],
      domains: data.domains ?? [],
    };
  },
  async createProvider(tenantId: number | undefined, body: OwnProviderSave & { driver: string; slug?: string }): Promise<void> {
    await api.post('/admin/tenant/auth-providers', body, q(tenantId));
  },
  async updateProvider(tenantId: number | undefined, name: string, body: OwnProviderSave): Promise<{ checks: AuthProviderCheck[] }> {
    const { data } = await api.patch<{ checks?: AuthProviderCheck[] }>(`/admin/tenant/auth-providers/${name}`, body, q(tenantId));
    return { checks: Array.isArray(data.checks) ? data.checks : [] };
  },
  async deleteProvider(tenantId: number | undefined, name: string): Promise<void> {
    await api.delete(`/admin/tenant/auth-providers/${name}`, q(tenantId));
  },
  async testProvider(tenantId: number | undefined, name: string, draft: Record<string, unknown>): Promise<AuthProviderTestResult> {
    const { data } = await api.post<Partial<AuthProviderTestResult>>(`/admin/tenant/auth-providers/${name}/test`, { config: draft }, q(tenantId));
    return { testable: data.testable === true, ok: data.ok === true, checks: Array.isArray(data.checks) ? data.checks : [] };
  },
  async addDomain(tenantId: number | undefined, domain: string): Promise<TenantDomain> {
    const { data } = await api.post<TenantDomain>('/admin/tenant/domains', { domain }, q(tenantId));
    return data;
  },
  async checkDomain(tenantId: number | undefined, id: number): Promise<TenantDomain> {
    const { data } = await api.post<{ domain: TenantDomain }>(`/admin/tenant/domains/${id}/check`, {}, q(tenantId));
    return data.domain;
  },
  async setCertificate(tenantId: number | undefined, id: number, certPem: string, keyPem: string): Promise<TenantDomain> {
    const { data } = await api.put<TenantDomain>(`/admin/tenant/domains/${id}/certificate`, { cert_pem: certPem, key_pem: keyPem }, q(tenantId));
    return data;
  },
  async removeCertificate(tenantId: number | undefined, id: number): Promise<TenantDomain> {
    const { data } = await api.delete<TenantDomain>(`/admin/tenant/domains/${id}/certificate`, q(tenantId));
    return data;
  },
  async deleteDomain(tenantId: number | undefined, id: number): Promise<void> {
    await api.delete(`/admin/tenant/domains/${id}`, q(tenantId));
  },
  async setInsecure(tenantId: number, allow: boolean): Promise<void> {
    await api.put('/admin/tenant/insecure', { allow }, q(tenantId));
  },
};

/** The refusal code of a domain or certificate request, when it is one. */
export function domainRefusal(err: unknown): string | null {
  const code = (err as { response?: { data?: { error?: unknown } } })?.response?.data?.error;
  return typeof code === 'string' && /^(domain_|certificate_|no_tenant_domain)/.test(code) ? code : null;
}
