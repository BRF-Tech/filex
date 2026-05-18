import { api } from './client';
import type { PaginatedResponse, SearchHit } from './types';

export interface SearchParams {
  q: string;
  storage_id?: number;
  mime?: string;
  page?: number;
  page_size?: number;
}

export interface SearchIndexStats {
  document_count: number;
  index_size_bytes: number;
  last_built_at: string | null;
  rebuilding: boolean;
}

export const SearchApi = {
  async query(params: SearchParams): Promise<PaginatedResponse<SearchHit>> {
    // The backend exposes search at `/api/files/search` (admin route
    // `/admin/search` only carries stats + rebuild). The SPA used to
    // point at `/admin/search` and 404'd every query. Wire the
    // canonical endpoint here so SearchTest.vue can actually issue
    // queries.
    const { data } = await api.get<PaginatedResponse<SearchHit>>('/files/search', { params });
    return data;
  },

  async stats(): Promise<SearchIndexStats> {
    const { data } = await api.get<SearchIndexStats>('/admin/search/stats');
    return data;
  },

  async rebuild(): Promise<{ accepted: boolean }> {
    const { data } = await api.post<{ accepted: boolean }>('/admin/search/rebuild');
    return data;
  },
};
