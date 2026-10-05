import { api } from './client';

/**
 * Groups (backend internal/group): named sets of people in one tenant. A
 * group can be given folder access (in the explorer's sharing panel, like a
 * person) and a custom role — the role of every member who has none of their
 * own. People are in a group because an administrator added them, or because
 * the group is linked to an SSO or LDAP group their sign-in carries.
 */

/** "manual": added by an administrator; "sso" / "ldap": through a link to
 *  an SSO or LDAP group, and kept in step with the directory at every
 *  sign-in. */
export type GroupSource = 'manual' | 'sso' | 'ldap' | string;

/** An outside directory's group whose people are members too. An LDAP group
 *  is named by its DN or its common name, compared without case. */
export interface GroupLink {
  kind: 'sso' | 'ldap' | string;
  value: string;
}

export interface Group {
  id: number;
  name: string;
  description: string;
  provider_id?: number | null;
  role_id: number | null;
  /** The group makes its members administrators (full access) instead of
   *  giving a role (migration 00086); role_id is then null. */
  gives_admin?: boolean;
  /** Whose role a member of several groups gets: the highest first. */
  priority: number;
  links: GroupLink[];
  /** A group directory sync brought in: its directory group's permanent id,
   *  the name it has there, and "removed" once the directory no longer has
   *  it. Absent for every other group. */
  directory_id?: string;
  directory_name?: string;
  directory_state?: '' | 'removed';
  created_by?: number | null;
  created_at?: string;
  updated_at?: string;
  /** In the list only. */
  member_count?: number;
  grant_count?: number;
}

export interface GroupMember {
  user_id: number;
  email: string;
  /** The person as every screen names them (server PersonLabel). */
  name: string;
  role: string;
  source: GroupSource;
  /** Where the account comes from (User.auth_source). */
  auth_source?: string;
  added_at: string;
}

export interface GroupGrant {
  id: number;
  storage_id: number;
  storage_name: string;
  path_prefix: string;
  path: string;
  is_dir: boolean;
  level: 'viewer' | 'editor' | 'owner';
}

export interface GroupDetail {
  group: Group;
  members: GroupMember[];
  grants: GroupGrant[];
}

export type GroupInput = Pick<Group, 'name' | 'description' | 'role_id' | 'links'> & { priority?: number; gives_admin?: boolean };

/** One account's group, as its page lists it. */
export interface UserGroup {
  id: number;
  name: string;
  role_id: number | null;
  source: GroupSource;
}

/** One of a person's groups, as the Users list shows it. */
export interface Membership {
  id: number;
  name: string;
  source: GroupSource;
  /** The group gives a role or folder access; such groups come first. */
  in_use?: boolean;
}

export const GroupsApi = {
  /** Every person's groups the caller may see, by user id. */
  async memberships(): Promise<Record<string, Membership[]>> {
    const { data } = await api.get<{ memberships: Record<string, Membership[]> }>('/admin/groups/memberships');
    return data.memberships ?? {};
  },

  async list(): Promise<Group[]> {
    const { data } = await api.get<{ groups: Group[] }>('/admin/groups');
    return data.groups ?? [];
  },

  async get(id: number): Promise<GroupDetail> {
    const { data } = await api.get<GroupDetail>(`/admin/groups/${id}`);
    return data;
  },

  async create(g: GroupInput): Promise<GroupDetail> {
    const { data } = await api.post<GroupDetail>('/admin/groups', g);
    return data;
  },

  async update(id: number, g: GroupInput): Promise<GroupDetail> {
    const { data } = await api.put<GroupDetail>(`/admin/groups/${id}`, g);
    return data;
  },

  /** Deletes the group, its memberships and its folder access. */
  /** Keeps a group whose directory group was removed as a filex group. */
  async detach(id: number): Promise<GroupDetail> {
    const { data } = await api.post<GroupDetail>(`/admin/groups/${id}/detach`, {});
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/groups/${id}`);
  },

  async addMembers(id: number, userIds: number[]): Promise<GroupDetail> {
    const { data } = await api.post<GroupDetail>(`/admin/groups/${id}/members`, { user_ids: userIds });
    return data;
  },

  async removeMember(id: number, userId: number): Promise<GroupDetail> {
    const { data } = await api.delete<GroupDetail>(`/admin/groups/${id}/members/${userId}`);
    return data;
  },

  async forUser(userId: number): Promise<UserGroup[]> {
    const { data } = await api.get<{ groups: UserGroup[] }>(`/admin/users/${userId}/groups`);
    return data.groups ?? [];
  },
};
