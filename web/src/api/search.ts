import { api } from './client';
import type { PaginatedResponse, SearchHit } from './types';

/* bul:s3 — v0.2 search contract additions (older backends omit both). */
export type SearchScope = 'name' | 'content' | 'all';
export type SearchHitEx = SearchHit & {
  /** Plain-text content snippet; matched words wrapped in «» (never HTML). */
  snippet?: string;
  /** Where the hit matched: name | content | both. */
  matched?: 'name' | 'content' | 'both';
};

export interface SearchParams {
  q: string;
  storage_id?: number;
  mime?: string;
  page?: number;
  page_size?: number;
  /** bul:s3 — name | content | all (backend default: all). */
  scope?: SearchScope;
}

export interface SearchIndexStats {
  document_count: number;
  index_size_bytes: number;
  last_built_at: string | null;
  rebuilding: boolean;
}

export const SearchApi = {
  async query(params: SearchParams): Promise<PaginatedResponse<SearchHitEx>> {
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
        snippet?: string;
        matched?: 'name' | 'content' | 'both';
      }>;
    }>('/files/search', { params });
    const nodes = data.results ?? [];
    const items: SearchHitEx[] = nodes.map((n) => ({
      id: String(n.id),
      storage_id: n.storage_id,
      storage_name: '',
      path: n.path,
      filename: n.name,
      size: n.size ?? 0,
      mime: n.mime ?? '',
      modified_at: n.backend_mtime || n.updated_at || '',
      score: 0,
      // bul:s3 — contract fields, undefined-safe on older backends.
      snippet: typeof n.snippet === 'string' ? n.snippet : undefined,
      matched: n.matched,
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
