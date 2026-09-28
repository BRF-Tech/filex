import { api } from './client';

/**
 * Roles (backend internal/perm): the permission catalogue, the built-in
 * roles, custom roles (the backend's permission rules), and one account's
 * roles and exceptions beside its effective answer. The server enforces every
 * one of these; the admin pages only edit them and the explorer only uses
 * them to hide what would 403.
 *
 * Not to be confused with grants.ts — the per-file/per-folder sharing panel
 * (/api/files/permissions), which says who may reach a path, not what an
 * account may do.
 */

export type PermKey = string;
export type PermGroup = 'files' | 'sharing' | 'access' | 'account' | 'admin';
export type PermEffect = 'allow' | 'deny';
/** The two built-in roles whose permissions can be edited (Administrator holds everything). */
export type BuiltinRole = 'user' | 'viewer';

export interface PermDef {
  key: PermKey;
  group: PermGroup;
  /** Held only through the account role (admin.full) — never editable. */
  role_only?: boolean;
  /** A read-only (viewer) account can never hold it. */
  viewer_capped?: boolean;
}

export interface PermPreset {
  name: 'full_admin' | 'standard' | 'read_only' | 'upload_only' | 'guest' | string;
  permissions: PermKey[];
}

export interface PermCatalogue {
  permissions: PermDef[];
  presets: PermPreset[];
}

export type PermSourceKind = 'role' | 'base' | 'rule' | 'override' | 'viewer_ceiling' | 'role_off';

export interface PermSource {
  kind: PermSourceKind;
  rule_id?: number;
  rule_name?: string;
}

export interface PermRuleSettings {
  share_link_max_days?: number | null;
  share_link_password_required?: boolean;
  blocked_extensions?: string[];
  max_upload_bytes?: number | null;
  require_2fa?: boolean;
}

export interface EffectivePermissions {
  permissions: { key: PermKey; allowed: boolean; source: PermSource }[];
  allowed: PermKey[];
  /** The preset the allowed set equals, or "" (custom). */
  preset: string;
  settings: PermRuleSettings;
  rules: number[];
  /** Rules that apply only on some storages or paths — not in the answers above. */
  conditional_rules: number[];
}

export interface UserPermissions {
  user_id: number;
  role: string;
  overrides: Record<PermKey, PermEffect>;
  effective: EffectivePermissions;
}

/** A role's targets only name SSO groups: the role a NEW account starts with
 *  when its first sign-in carries one. People are given a role on their own
 *  page, one each. */
export type PermTargetKind = 'sso_group';

export interface PermRuleTarget {
  kind: PermTargetKind;
  value?: string;
}

export interface PermRuleConditions {
  storage_ids?: number[];
  paths?: string[];
}

export interface PermissionRule {
  id: number;
  name: string;
  description: string;
  enabled: boolean;
  /** What the role may do everywhere — its own list, like a built-in role's. */
  permissions: PermKey[];
  provider_id?: number | null;
  targets: PermRuleTarget[];
  /** "Different in some folders": Allow/Deny for file actions where
   *  `conditions` match. Empty when the role has no folder part. */
  effects: Record<PermKey, PermEffect>;
  settings: PermRuleSettings;
  conditions: PermRuleConditions;
  created_by?: number | null;
  created_at?: string;
  updated_at?: string;
}

export type PermissionRuleInput = Omit<PermissionRule, 'id' | 'created_by' | 'created_at' | 'updated_at'>;

export const RolesApi = {
  async catalogue(): Promise<PermCatalogue> {
    const { data } = await api.get<PermCatalogue>('/admin/roles/catalogue');
    return data;
  },

  /** A built-in role's permissions: the User role's (the defaults every
   *  account starts from) or the Viewer role's. */
  async getDefaults(role: BuiltinRole = 'user'): Promise<{ permissions: PermKey[]; preset: string }> {
    const { data } = await api.get<{ permissions: PermKey[]; preset: string }>('/admin/roles/builtin', { params: { role } });
    return data;
  },

  async putDefaults(permissions: PermKey[], role: BuiltinRole = 'user'): Promise<{ permissions: PermKey[]; preset: string }> {
    const { data } = await api.put<{ permissions: PermKey[]; preset: string }>('/admin/roles/builtin', { permissions }, { params: { role } });
    return data;
  },

  /** The one custom role an account holds, or null. */
  async userRole(id: number): Promise<number | null> {
    const { data } = await api.get<{ role_id: number | null }>(`/admin/users/${id}/roles`);
    return data.role_id ?? null;
  },

  /** Sets the account's ONE role in one call: a custom role (its id), or a
   *  built-in one ("admin" | "user" | "viewer"), which ends the custom one. */
  async setUserRole(id: number, role: number | BuiltinRole | 'admin'): Promise<{ role_id: number | null; role: string }> {
    const body = typeof role === 'number' ? { role_id: role } : { role };
    const { data } = await api.put<{ role_id: number | null; role: string }>(`/admin/users/${id}/roles`, body);
    return data;
  },

  /** Every custom role, who holds which (user id → role id), and how many
   *  people are on each built-in role itself. */
  async listRules(): Promise<{
    rules: PermissionRule[];
    assignments: Record<string, number>;
    builtinMembers?: Partial<Record<'admin' | BuiltinRole, number>>;
  }> {
    const { data } = await api.get<{
      rules: PermissionRule[];
      assignments?: Record<string, number>;
      builtin_members?: Partial<Record<'admin' | BuiltinRole, number>>;
    }>('/admin/roles');
    return { rules: data.rules ?? [], assignments: data.assignments ?? {}, builtinMembers: data.builtin_members ?? {} };
  },

  async createRule(rule: PermissionRuleInput): Promise<PermissionRule> {
    const { data } = await api.post<PermissionRule>('/admin/roles', rule);
    return data;
  },

  async updateRule(id: number, rule: PermissionRuleInput): Promise<PermissionRule> {
    const { data } = await api.put<PermissionRule>(`/admin/roles/${id}`, rule);
    return data;
  },

  /** Deletes a role. People holding it move to `to`: "user", "viewer" or
   *  another role's id (the server refuses without it when anyone holds it). */
  async deleteRule(id: number, to?: string): Promise<void> {
    await api.delete(`/admin/roles/${id}`, { params: to ? { to } : {} });
  },

  async forUser(id: number): Promise<UserPermissions> {
    const { data } = await api.get<UserPermissions>(`/admin/users/${id}/exceptions`);
    return data;
  },

  async setOverrides(id: number, overrides: Record<PermKey, PermEffect>): Promise<UserPermissions> {
    const { data } = await api.put<UserPermissions>(`/admin/users/${id}/exceptions`, { overrides });
    return data;
  },

  /** user id → overrides, for the Users list's "Custom" marker. */
  async allOverrides(): Promise<Record<string, Record<PermKey, PermEffect>>> {
    const { data } = await api.get<{ overrides: Record<string, Record<PermKey, PermEffect>> }>('/admin/roles/exceptions');
    return data.overrides ?? {};
  },
};
