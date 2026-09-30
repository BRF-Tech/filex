import type { PluginText } from '@brftech/filex-core';

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

/** Who holds an app permission nobody has decided (the app's manifest
 *  picks it): every account, accounts that can change files, or
 *  administrators only. Backend perm/app.go (AppDefault). */
export type AppPermDefault = 'viewer' | 'user' | 'admin';

/**
 * One permission an installed app lets the administrator hand out per role
 * and per person (manifest `user_permissions`) — "Request signatures" for the
 * signing app. Not in the catalogue: it comes and goes with the app. Its key
 * is `app.<app>.<id>`; decisions are stored by that key, allow or deny, in a
 * built-in role's `apps`, a custom role's `settings.apps` and a person's
 * exceptions.
 */
export interface AppPermDef {
  key: string;
  app: string;
  app_label: PluginText;
  id: string;
  label: PluginText;
  description?: PluginText;
  default: AppPermDefault;
}

export interface PermCatalogue {
  permissions: PermDef[];
  presets: PermPreset[];
  /** The installed apps' permissions, app by app; empty when none declares any. */
  apps?: AppPermDef[];
}

/** A built-in role's permissions, and its decisions about app permissions. */
export interface BuiltinRoleAnswer {
  permissions: PermKey[];
  preset: string;
  /** app.<app>.<id> → allow | deny; a key it does not name follows the app's default. */
  apps?: Record<string, PermEffect>;
}

export type PermSourceKind = 'role' | 'base' | 'rule' | 'override' | 'viewer_ceiling' | 'role_off' | 'app_default';

export interface PermSource {
  kind: PermSourceKind;
  rule_id?: number;
  rule_name?: string;
  /** The custom role is the account's through this group (it has none of
   *  its own). */
  group_id?: number;
  group_name?: string;
}

/** The role an account has through a group, when it has none of its own. */
export interface GroupRole {
  role_id: number;
  group_id: number;
  group_name: string;
}

export interface PermRuleSettings {
  share_link_max_days?: number | null;
  share_link_password_required?: boolean;
  blocked_extensions?: string[];
  max_upload_bytes?: number | null;
  require_2fa?: boolean;
  /** The role's decisions about app permissions (app.<app>.<id> → allow | deny). */
  apps?: Record<string, PermEffect>;
}

/** One app permission's answer for one account (the exceptions answer only):
 *  the result and where it comes from, and the result WITHOUT the person's own
 *  exception — what the editor's "Default" choice stands for. */
export interface AppPermEffective {
  key: string;
  allowed: boolean;
  source: PermSource;
  inherited: { allowed: boolean; source: PermSource };
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
  /** The installed apps' permissions (an administrator's exceptions answer only). */
  apps?: AppPermEffective[];
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
  /** The role's own name: required, and what a language without an entry in `names` shows. */
  name: string;
  description: string;
  /** The name in other interface languages (code → text). Read through `roleName` (lib/roleName). */
  names?: Record<string, string>;
  /** The description in other interface languages. Read through `roleDescription`. */
  descriptions?: Record<string, string>;
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
  async getDefaults(role: BuiltinRole = 'user'): Promise<BuiltinRoleAnswer> {
    const { data } = await api.get<BuiltinRoleAnswer>('/admin/roles/builtin', { params: { role } });
    return data;
  },

  /** Replaces a built-in role's permissions. `apps` — its decisions about app
   *  permissions — replaces them when given ({} returns every one to the app's
   *  default) and leaves them as they are when omitted. */
  async putDefaults(
    permissions: PermKey[],
    role: BuiltinRole = 'user',
    apps?: Record<string, PermEffect>,
  ): Promise<BuiltinRoleAnswer> {
    const body = apps ? { permissions, apps } : { permissions };
    const { data } = await api.put<BuiltinRoleAnswer>('/admin/roles/builtin', body, { params: { role } });
    return data;
  },

  /** The one custom role an account holds, or null. */
  async userRole(id: number): Promise<number | null> {
    return (await this.userRoleDetail(id)).role_id;
  },

  /** The account's own custom role, and — when it has none — the role a
   *  group gives it. */
  async userRoleDetail(id: number): Promise<{ role_id: number | null; group_role: GroupRole | null }> {
    const { data } = await api.get<{ role_id: number | null; group_role?: GroupRole | null }>(`/admin/users/${id}/roles`);
    return { role_id: data.role_id ?? null, group_role: data.group_role ?? null };
  },

  /** Sets the account's ONE role in one call: a custom role (its id), or a
   *  built-in one ("admin" | "user" | "viewer"), which ends the custom one. */
  /** `group_role` is set when, with no custom role of their own, a group's
   *  role still decides — a built-in role picked here does not replace it. */
  async setUserRole(
    id: number,
    role: number | BuiltinRole | 'admin',
  ): Promise<{ role_id: number | null; role: string; group_role?: GroupRole | null }> {
    const body = typeof role === 'number' ? { role_id: role } : { role };
    const { data } = await api.put<{ role_id: number | null; role: string; group_role?: GroupRole | null }>(`/admin/users/${id}/roles`, body);
    return data;
  },

  /** Every custom role, who holds which (user id → role id), and how many
   *  people are on each built-in role itself. */
  async listRules(): Promise<{
    rules: PermissionRule[];
    assignments: Record<string, number>;
    /** user id → the role a group gives them (people with none of their own). */
    groupAssignments?: Record<string, GroupRole>;
    builtinMembers?: Partial<Record<'admin' | BuiltinRole, number>>;
  }> {
    const { data } = await api.get<{
      rules: PermissionRule[];
      assignments?: Record<string, number>;
      group_assignments?: Record<string, GroupRole>;
      builtin_members?: Partial<Record<'admin' | BuiltinRole, number>>;
    }>('/admin/roles');
    return {
      rules: data.rules ?? [],
      assignments: data.assignments ?? {},
      groupAssignments: data.group_assignments ?? {},
      builtinMembers: data.builtin_members ?? {},
    };
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
