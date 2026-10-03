import { api } from './client';

/**
 * Groups (backend internal/group): named sets of people in one tenant. A
 * group can be given folder access (in the explorer's sharing panel, like a
 * person) and a custom role — the role of every member who has none of their
 * own. People are in a group because an administrator added them, or because
 * the group is linked to an SSO group their sign-in carries.
 */

/** "manual": added by an administrator; "sso": through an SSO link, and
 *  kept in step with the identity provider at every sign-in. */
export type GroupSource = 'manual' | 'sso' | string;

/** An outside directory's group whose people are members too. */
export interface GroupLink {
  kind: 'sso' | string;
  value: string;
}

export interface Group {
  id: number;
  name: string;
  description: string;
  provider_id?: number | null;
  role_id: number | null;
  /** Whose role a member of several groups gets: the highest first. */
  priority: number;
  links: GroupLink[];
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

export type GroupInput = Pick<Group, 'name' | 'description' | 'role_id' | 'links'> & { priority?: number };

/** One account's group, as its page lists it. */
export interface UserGroup {
  id: number;
  name: string;
  role_id: number | null;
  source: GroupSource;
}

export const GroupsApi = {
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
