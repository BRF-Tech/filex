import { api } from './client';

/**
 * Tenants (backend handlers/providers.go, docs/TENANT-ADMIN.md). A tenant is a
 * provider row: its sign-in name (realm), its own address, its storages and
 * its people. Managed by the platform operator only: an administrator of a
 * tenant is refused (403 supertenant_only).
 */
export interface Tenant {
  id: number;
  slug: string;
  /** The tenant's sign-in name. Given at creation, never changed; "" for the
   *  platform's own tenant (signed in to with an empty realm). */
  realm: string;
  name: string;
  host?: string;
  auth_type: 'oidc' | 'local' | string;
  oidc_issuer?: string;
  oidc_client_id?: string;
  oidc_redirect_url?: string;
  role_claim?: string;
  admin_group?: string;
  cookie_domain?: string;
  /** The tenant's OIDC client secret is stored (it is never sent back). */
  oidc_client_secret_set?: boolean;
  /** Trust every address the tenant's own OIDC sends as verified (docs/SSO.md). */
  oidc_trust_email?: boolean;
  /** The value above is the one the upgrade to 0.50 set; nobody saved it since. */
  oidc_trust_email_by_upgrade?: boolean;
  is_supertenant: boolean;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
  storage_ids: number[];
  user_count: number;
}

/** The fields a tenant is created or changed with; absent = unchanged. */
export interface TenantInput {
  slug?: string;
  /** Read on creation only: a different one on an update is refused. */
  realm?: string;
  name?: string;
  host?: string;
  cookie_domain?: string;
  auth_type?: string;
  oidc_issuer?: string;
  oidc_client_id?: string;
  /** "" keeps the stored secret. */
  oidc_client_secret?: string;
  oidc_redirect_url?: string;
  role_claim?: string;
  admin_group?: string;
  oidc_trust_email?: boolean;
  enabled?: boolean;
}

export interface TenantList {
  providers: Tenant[];
  /** FILEX_MULTI_TENANT. Off: only the platform's own tenant signs in. */
  multi_tenant: boolean;
}

/** The realm offered for a slug (one rule, the server's: tenant.SuggestRealm). */
export interface RealmSuggestion {
  /** The first free, valid variant; "" when nothing realm-shaped is left. */
  realm: string;
  /** The slug made realm-shaped, before any `-2` was added. */
  base: string;
  available: boolean;
}

/**
 * A tenant as the screens read it: the server answers `storage_ids: null` for
 * a tenant with no storage (a nil list in Go), and every reader counts it.
 */
export function normalizeTenant(t: Tenant): Tenant {
  return { ...t, storage_ids: Array.isArray(t.storage_ids) ? t.storage_ids : [] };
}

export const TenantsApi = {
  async list(): Promise<TenantList> {
    const { data } = await api.get<TenantList>('/admin/providers/');
    return { providers: (data.providers ?? []).map(normalizeTenant), multi_tenant: data.multi_tenant === true };
  },
  async get(id: number): Promise<Tenant> {
    const { data } = await api.get<Tenant>(`/admin/providers/${id}`);
    return normalizeTenant(data);
  },
  async suggestRealm(slug: string): Promise<RealmSuggestion> {
    const { data } = await api.get<RealmSuggestion>('/admin/providers/realm-suggestion', { params: { slug } });
    return data;
  },
  async create(input: TenantInput): Promise<Tenant> {
    const { data } = await api.post<Tenant>('/admin/providers/', input);
    return normalizeTenant(data);
  },
  async update(id: number, input: TenantInput): Promise<Tenant> {
    const { data } = await api.patch<Tenant>(`/admin/providers/${id}`, input);
    return normalizeTenant(data);
  },
  async remove(id: number, force = false): Promise<{ ok: boolean; deleted_users: number }> {
    const { data } = await api.delete(`/admin/providers/${id}`, { params: force ? { force: 1 } : undefined });
    return data;
  },
  async linkStorage(id: number, storageId: number): Promise<Tenant> {
    const { data } = await api.post<Tenant>(`/admin/providers/${id}/storages`, { storage_id: storageId });
    return normalizeTenant(data);
  },
  async unlinkStorage(id: number, storageId: number): Promise<Tenant> {
    const { data } = await api.delete<Tenant>(`/admin/providers/${id}/storages/${storageId}`);
    return normalizeTenant(data);
  },
};

/** The refusals a tenant form says on a field (handlers/providers.go). */
export const TENANT_FIELD_CODES = [
  'slug_taken',
  'host_taken',
  'realm_invalid',
  'realm_reserved',
  'realm_empty',
  'realm_taken',
  'realm_immutable',
] as const;
export type TenantFieldCode = (typeof TENANT_FIELD_CODES)[number];

/** The machine code of a refused tenant request, when it is one a form says on a field. */
export function tenantRefusal(err: unknown): { code: TenantFieldCode; field: string } | null {
  const data = (err as { response?: { data?: { error?: unknown; field?: unknown } } })?.response?.data;
  const code = typeof data?.error === 'string' ? data.error : '';
  if (!(TENANT_FIELD_CODES as readonly string[]).includes(code)) return null;
  const field = typeof data?.field === 'string' ? data.field : code.startsWith('realm') ? 'realm' : code.split('_')[0];
  return { code: code as TenantFieldCode, field };
}
