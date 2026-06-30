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
    // `/admin/search` only carries stats + rebuild).
    //
    // ⚠ It returns `{results: Node[]}` — NOT a paginated `{items}` envelope,
    // and a Node (name/updated_at/no score) not a SearchHit
    // (filename/score). Adapt it here; otherwise SearchTest.vue blows up on
    // `results.items.length` (undefined) and the whole page goes blank.
    const { data } = await api.get<{
      results: Array<{
        id: number;
        storage_id: number;
        name: string;
        path: string;
        size?: number;
        mime?: string;
        backend_mtime?: string | null;
        updated_at?: string;
      }>;
    }>('/files/search', { params });
    const nodes = data.results ?? [];
    const items: SearchHit[] = nodes.map((n) => ({
      id: String(n.id),
      storage_id: n.storage_id,
      storage_name: '',
      path: n.path,
      filename: n.name,
      size: n.size ?? 0,
      mime: n.mime ?? '',
      modified_at: n.backend_mtime || n.updated_at || '',
      score: 0,
    }));
    return {
      items,
      total: items.length,
      page: params.page ?? 1,
      page_size: params.page_size ?? 25,
    };
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
