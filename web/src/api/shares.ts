import { api } from './client';
import type { PaginatedResponse, Share } from './types';

export interface ShareListParams {
  storage_id?: number;
  q?: string;
  active_only?: boolean;
  page?: number;
  page_size?: number;
}

// Backend returns `{entries: [...], total, page, page_size}` (see
// handlers/shares_admin.go); the rest of the panel expects PaginatedResponse
// with `items`. Normalize at the boundary so callers stay terse.
interface SharesBackendShape {
  entries?: Share[];
  items?: Share[];
  total?: number;
  page?: number;
  page_size?: number;
}

export const SharesApi = {
  async list(params: ShareListParams = {}): Promise<PaginatedResponse<Share>> {
    const { data } = await api.get<SharesBackendShape>('/admin/shares', { params });
    return {
      items: data.items ?? data.entries ?? [],
      total: data.total ?? 0,
      page: data.page ?? params.page ?? 1,
      page_size: data.page_size ?? params.page_size ?? 25,
    };
  },

  async revoke(id: number): Promise<void> {
    await api.post(`/admin/shares/${id}/revoke`);
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/shares/${id}`);
  },
};
