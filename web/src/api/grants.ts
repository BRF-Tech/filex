import { api } from './client';

export interface AdminGrant {
  id: number;
  storage_id: number;
  storage_name: string;
  path: string;
  path_prefix: string;
  is_dir: boolean;
  user_id: number;
  user_email: string;
  level: 'viewer' | 'editor' | 'owner';
  created_at: string;
}

export const AdminGrantsApi = {
  async list(): Promise<AdminGrant[]> {
    const { data } = await api.get<{ grants: AdminGrant[] }>('/admin/grants');
    return data.grants ?? [];
  },
  async remove(id: number): Promise<void> {
    await api.delete(`/admin/grants/${id}`);
  },
};
