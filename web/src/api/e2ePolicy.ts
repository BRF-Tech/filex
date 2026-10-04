import { api } from './client';

// Who may START encrypting (backend internal/e2epolicy; docs/E2E-ENCRYPTION.md
// → "Who may encrypt"). Three layers, all of which must say yes: the platform
// operator's ceiling per tenant (`e2e_allowed`), the tenant's policy, and
// `files.encrypt` in the roles. Under the `approval` policy people ask first
// (the explorer's "Request encryption…"), and the answers are given here.
//
// ⚠ Writes need an administrator signed in to this panel: an API key is
// refused (`session_required`).

export const E2E_POLICIES = ['off', 'admins', 'permitted', 'approval'] as const;
export type E2EPolicy = (typeof E2E_POLICIES)[number];

export function isE2EPolicy(v: unknown): v is E2EPolicy {
  return typeof v === 'string' && (E2E_POLICIES as readonly string[]).includes(v);
}

export interface E2EPolicyState {
  /** The platform operator's ceiling for this tenant — always true on a single-tenant install. */
  available: boolean;
  policy: E2EPolicy;
  /** `tenant` on a multi-tenant install; `instance` on a single-tenant one (setting `e2e.policy`). */
  scope: 'tenant' | 'instance';
  tenant: { id: number; name: string } | null;
  /** Requests waiting for an answer. */
  pending: number;
}

export interface E2ETenantRow {
  id: number;
  slug: string;
  name: string;
  is_supertenant: boolean;
  e2e_allowed: boolean;
  e2e_policy: E2EPolicy;
}

export type E2ERequestStatus = 'pending' | 'approved' | 'rejected' | 'expired' | 'used';

/** What a request asks for, and all its approval opens (backend
 *  internal/e2epolicy, operator decision 2026-10-03): `folder` - that folder,
 *  encrypted where it is; `new_folder` - one new encrypted folder directly
 *  inside it; `file` - one new encrypted file in it. */
export type E2ERequestKind = 'folder' | 'new_folder' | 'file';

export interface E2ERequest {
  id: number;
  /** `<storage>://<rel>` - the folder the request is kept under. An approval
   *  opens its kind there and nothing else: not a folder below it, not another
   *  kind. A single file's request is kept under the folder its `.fxe` lands
   *  in. */
  path: string;
  storage: string;
  kind: E2ERequestKind;
  /** The requester's own words. */
  reason: string;
  status: E2ERequestStatus;
  /** Who asked, named as the account was when it asked. */
  requester: string;
  requester_id: number;
  decider?: string;
  decided_at?: string | null;
  /** The approver's note, or why it was rejected. */
  decision_note?: string;
  expires_at: string;
  used_at?: string | null;
  created_at: string;
  tenant_id?: number | null;
  /** The administrator reading the list may answer it. False on the platform
   *  operator's list for another tenant's request: that tenant's
   *  administrators decide it. Missing (an older server): true. */
  decidable?: boolean;
}

export interface E2ERequestList {
  requests: E2ERequest[];
  /** How many days a request waits, and an approval lasts unused. */
  ttl_days: number;
}

export const E2EPolicyApi = {
  async get(): Promise<E2EPolicyState> {
    const { data } = await api.get<E2EPolicyState>('/admin/e2e');
    return data;
  },

  async update(policy: E2EPolicy): Promise<E2EPolicyState> {
    const { data } = await api.patch<E2EPolicyState>('/admin/e2e', { policy });
    return data;
  },

  /** Every tenant's ceiling — the platform operator only (`supertenant_only` otherwise). */
  async tenants(): Promise<E2ETenantRow[]> {
    const { data } = await api.get<{ tenants?: E2ETenantRow[] }>('/admin/e2e/tenants');
    return data.tenants ?? [];
  },

  async updateTenant(id: number, allowed: boolean): Promise<E2ETenantRow> {
    const { data } = await api.patch<E2ETenantRow>(`/admin/e2e/tenants/${id}`, { e2e_allowed: allowed });
    return data;
  },

  async requests(status: 'pending' | 'all' = 'pending'): Promise<E2ERequestList> {
    const { data } = await api.get<Partial<E2ERequestList>>('/admin/e2e/requests', { params: { status } });
    return { requests: data.requests ?? [], ttl_days: data.ttl_days ?? 7 };
  },

  /** 409 `not_pending` when it was answered (or lapsed) meanwhile. */
  async approve(id: number, note = ''): Promise<E2ERequest> {
    const { data } = await api.post<{ request: E2ERequest }>(`/admin/e2e/requests/${id}/approve`, { note: note.trim() });
    return data.request;
  },

  async reject(id: number, reason: string): Promise<E2ERequest> {
    const { data } = await api.post<{ request: E2ERequest }>(`/admin/e2e/requests/${id}/reject`, {
      reason: reason.trim(),
    });
    return data.request;
  },
};

/** The code a refused call answered with (`not_pending`, `not_found`,
 *  `invalid_policy`, `supertenant_only`…), or ''. */
export function e2ePolicyRefusal(err: unknown): string {
  const code = (err as { response?: { data?: { error?: unknown } } })?.response?.data?.error;
  return typeof code === 'string' ? code : '';
}
