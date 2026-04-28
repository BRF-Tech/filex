import { api } from './client';
import type { PaginatedResponse, SyncRun } from './types';

export interface SyncRunListParams {
  storage_id?: number;
  state?: SyncRun['state'];
  page?: number;
  page_size?: number;
}

export const SyncApi = {
  async list(params: SyncRunListParams = {}): Promise<PaginatedResponse<SyncRun>> {
    const { data } = await api.get<PaginatedResponse<SyncRun>>('/admin/sync-runs', { params });
    return data;
  },

  async get(id: number): Promise<SyncRun> {
    const { data } = await api.get<SyncRun>(`/admin/sync-runs/${id}`);
    return data;
  },
};
