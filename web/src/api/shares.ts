import { api } from './client';
import type { PaginatedResponse, Share } from './types';

export interface ShareListParams {
  storage_id?: number;
  q?: string;
  active_only?: boolean;
  page?: number;
  page_size?: number;
}

export const SharesApi = {
  async list(params: ShareListParams = {}): Promise<PaginatedResponse<Share>> {
    const { data } = await api.get<PaginatedResponse<Share>>('/admin/shares', { params });
    return data;
  },

  async revoke(id: number): Promise<void> {
    await api.post(`/admin/shares/${id}/revoke`);
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/shares/${id}`);
  },
};
