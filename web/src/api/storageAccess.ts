import { personName } from '@brftech/filex-core';
import { api } from './client';

/**
 * #117 — a whole storage given to people and groups.
 *
 * An assignment is a grant on the storage's ROOT: the path `<storage>://`,
 * stored with the path prefix "" (backend model.FileGrant). Nothing here is
 * a new endpoint. These are the permission routes the explorer's access
 * dialog uses (`/api/files/permissions`), addressed at the root, so the same
 * rules answer everywhere: per-item access control has to be on before
 * anybody can be given anything (409), a viewer account takes viewer only
 * (400), and an administrator - or an owner of the root - manages them.
 *
 * Folder grants are other rows. Nothing on this page reads, changes or
 * revokes them; they are listed under Folder access.
 */

export type AccessLevel = 'viewer' | 'editor' | 'owner';

/** One storage-wide assignment: to a person or (`kind: 'group'`) to a group,
 *  every member of which holds it. A group's grant has its own id space. */
export interface StorageAssignment {
  id: number;
  kind: 'user' | 'group';
  level: AccessLevel;
  user_id?: number;
  user_email?: string;
  user_name?: string;
  user_display_name?: string;
  group_id?: number;
  group_name?: string;
  created_at?: string;
}

/** Someone the picker offers: a person - with their account role, the
 *  ceiling on what they can be given - or a group. */
export interface AccessCandidate {
  kind: 'user' | 'group';
  id: number;
  name: string;
  /** A person's e-mail address, a group's description. */
  detail?: string;
  role?: string;
}

/** `alpha` → `alpha://`: the storage's root on the wire. */
export function storageRootPath(storage: string): string {
  return `${storage}://`;
}

/** The level `who` can be given: a viewer account takes viewer only (the
 *  account role is the ceiling; the server refuses more with a 400). A group
 *  takes any level, and each member is held to their own role. */
export function levelFor(who: Pick<AccessCandidate, 'kind' | 'role'>, level: AccessLevel): AccessLevel {
  return who.kind === 'user' && who.role === 'viewer' ? 'viewer' : level;
}

/** Same key for a row and a candidate, so the picker can leave out who is
 *  already there. */
export function accessKey(kind: 'user' | 'group', id: number | undefined): string {
  return `${kind === 'group' ? 'g' : 'u'}${id ?? 0}`;
}

/** The person or group an assignment names, the way every screen names one. */
export function assigneeName(a: StorageAssignment): string {
  if (a.kind === 'group') return a.group_name || `#${a.group_id}`;
  return personName({ name: a.user_name, display_name: a.user_display_name, email: a.user_email }) || `#${a.user_id}`;
}

function grantRoute(a: Pick<StorageAssignment, 'id' | 'kind'>): string {
  return a.kind === 'group' ? `/files/permissions/groups/${a.id}` : `/files/permissions/${a.id}`;
}

interface PermissionsWire {
  storage_rbac?: boolean;
  direct?: Array<Omit<StorageAssignment, 'kind'> & { kind?: string }>;
}

interface UserWire {
  id: number;
  email: string;
  display_name?: string;
  username?: string;
  role?: string;
}

interface GroupWire {
  id: number;
  name: string;
  description?: string;
}

export const StorageAccessApi = {
  /**
   * The storage's own assignments (the grants on its root) and whether
   * per-item access control is on. The root's `direct` grants are exactly
   * the storage-wide ones: nothing sits above a root to be inherited.
   */
  async list(storage: string): Promise<{ assignments: StorageAssignment[]; rbac: boolean }> {
    const { data } = await api.get<PermissionsWire>('/files/permissions', {
      params: { path: storageRootPath(storage) },
    });
    const rows = Array.isArray(data?.direct) ? data.direct : [];
    return {
      rbac: data?.storage_rbac === true,
      assignments: rows.map((g): StorageAssignment => ({ ...g, kind: g.kind === 'group' ? 'group' : 'user' })),
    };
  },

  async assign(storage: string, who: AccessCandidate, level: AccessLevel): Promise<void> {
    await api.post('/files/permissions', {
      path: storageRootPath(storage),
      ...(who.kind === 'group' ? { group_id: who.id } : { user_id: who.id }),
      level: levelFor(who, level),
    });
  },

  async setLevel(a: Pick<StorageAssignment, 'id' | 'kind'>, level: AccessLevel): Promise<void> {
    await api.patch(grantRoute(a), { level });
  },

  async revoke(a: Pick<StorageAssignment, 'id' | 'kind'>): Promise<void> {
    await api.delete(grantRoute(a));
  },

  /**
   * Every storage-wide assignment taken back, one request each (there is no
   * batch route). Folder grants are other rows and stay. Answers the ones
   * that could not be taken back, so the page can say so and keep them.
   */
  async revokeAll(list: StorageAssignment[]): Promise<StorageAssignment[]> {
    const left: StorageAssignment[] = [];
    for (const a of list) {
      try {
        await StorageAccessApi.revoke(a);
      } catch {
        left.push(a);
      }
    }
    return left;
  },

  /** People and groups matching `q` - the same two lookups the explorer's
   *  access dialog makes. Groups first: a storage is usually a team's. */
  async search(q: string): Promise<AccessCandidate[]> {
    const [people, teams] = await Promise.all([
      api.get<{ users?: UserWire[] }>('/files/permissions/users', { params: { q } }),
      api.get<{ groups?: GroupWire[] }>('/files/permissions/groups', { params: { q } }),
    ]);
    const groups: AccessCandidate[] = (teams.data?.groups ?? []).map((g) => ({
      kind: 'group',
      id: g.id,
      name: g.name,
      detail: g.description || undefined,
    }));
    const users: AccessCandidate[] = (people.data?.users ?? []).map((u) => ({
      kind: 'user',
      id: u.id,
      name: personName(u) || u.email,
      detail: u.email,
      role: u.role,
    }));
    return [...groups, ...users];
  },
};
