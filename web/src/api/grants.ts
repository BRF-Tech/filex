import { api } from './client';

export interface AdminGrant {
  id: number;
  storage_id: number;
  storage_name: string;
  path: string;
  path_prefix: string;
  is_dir: boolean;
  /** "user" — a person's grant — or "group"; absent from older servers. */
  kind?: 'user' | 'group';
  user_id?: number;
  user_email?: string;
  /** The person as every screen names them (server model.PersonLabel). */
  user_name?: string;
  group_id?: number;
  group_name?: string;
  level: 'viewer' | 'editor' | 'owner';
  created_at: string;
}

export const AdminGrantsApi = {
  async list(): Promise<AdminGrant[]> {
    const { data } = await api.get<{ grants: AdminGrant[] }>('/admin/grants');
    return data.grants ?? [];
  },
  /** A group's grant has its own id space, so it goes by its own route. */
  async remove(id: number, kind: AdminGrant['kind'] = 'user'): Promise<void> {
    await api.delete(kind === 'group' ? `/admin/grants/groups/${id}` : `/admin/grants/${id}`);
  },
};
