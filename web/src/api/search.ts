import { api } from './client';
import type { PaginatedResponse, SearchHit } from './types';

/* bul:s3 — v0.2 search contract additions (older backends omit both). */
export type SearchScope = 'name' | 'content' | 'all';
export type SearchHitEx = SearchHit & {
  /** Plain-text content snippet; matched words wrapped in «» (never HTML). */
  snippet?: string;
  /** Where the hit matched: name | content | both. */
  matched?: 'name' | 'content' | 'both';
  /** A folder (the node's `type` is `dir`). */
  is_dir?: boolean;
};

/**
 * What `/api/files/search` reads (docs/SEARCH.md). ⚠ No page / page_size: the
 * server never read them (audit D7) - it answers ONE page of `limit` rows and
 * says whether more matched (`truncated`).
 */
export interface SearchParams {
  q: string;
  storage_id?: number;
  limit?: number;
  /** bul:s3 — name | content | all (backend default: all). */
  scope?: SearchScope;
  /** The server's narrowing (applied before the limit): `file` / `dir` or a
   *  kind (`image`, `spreadsheet`...), a mime prefix. */
  type?: string;
  mime?: string;
}

/** One answer of the search, as the server gives it. */
export interface SearchPage extends PaginatedResponse<SearchHitEx> {
  /** More matched than came back; `total` is then a lower bound. */
  truncated: boolean;
}

export interface SearchIndexStats {
  /** Every indexed node, folders included. */
  document_count: number;
  /** Files and folders apart (the Panel counts files); absent on an older server. */
  file_count?: number;
  folder_count?: number;
  index_size_bytes: number;
  last_built_at: string | null;
  rebuilding: boolean;
  /** When the latest rebuild of the running server ended; absent before any
   *  has. `rebuilding` turns false on a failure too — read this pair. */
  last_rebuild_finished_at?: string | null;
  /** Why that rebuild did not go live; empty when it did. */
  last_rebuild_error?: string;
}

export const SearchApi = {
  async query(params: SearchParams): Promise<SearchPage> {
    // The backend exposes search at `/api/files/search` (admin route
    // `/admin/search` only carries stats + rebuild).
    //
    // ⚠ It returns `{results: Node[], truncated, total}` — not a paginated
    // `{items}` envelope, and a Node (name/updated_at) rather than a SearchHit
    // (filename). Adapted here; every NUMBER is the server's (audit D7): this
    // used to make up `total: items.length`, `score: 0` and an empty storage
    // name, and drop `truncated`, so the admin search test's "more hits than
    // shown" guard never fired and every score read 0.000.
    const { data } = await api.get<{
      results: Array<{
        id: number;
        storage_id: number;
        storage?: string;
        name: string;
        path: string;
        size?: number;
        mime?: string;
        backend_mtime?: string | null;
        updated_at?: string;
        snippet?: string;
        matched?: 'name' | 'content' | 'both';
        type?: string;
        score?: number;
      }>;
      truncated?: boolean;
      total?: number;
    }>('/files/search', { params });
    const nodes = data.results ?? [];
    const items: SearchHitEx[] = nodes.map((n) => ({
      id: String(n.id),
      storage_id: n.storage_id,
      storage_name: n.storage ?? '',
      path: n.path,
      filename: n.name,
      size: n.size ?? 0,
      mime: n.mime ?? '',
      modified_at: n.backend_mtime || n.updated_at || '',
      score: typeof n.score === 'number' ? n.score : 0,
      // bul:s3 — contract fields, undefined-safe on older backends.
      snippet: typeof n.snippet === 'string' ? n.snippet : undefined,
      matched: n.matched,
      is_dir: n.type === 'dir',
    }));
    return {
      items,
      total: typeof data.total === 'number' ? data.total : items.length,
      truncated: data.truncated === true,
      page: 1,
      page_size: params.limit ?? items.length,
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
