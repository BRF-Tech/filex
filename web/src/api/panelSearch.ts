/**
 * The admin panel's search, server side (task #168, docs/ADMIN-PANEL.md →
 * Search): what the server finds for a query, the person's recent searches,
 * and the file index's hits.
 */
import { api } from './client';
import type { PanelHit } from '@/lib/adminSearch';

/** One of the person's recent searches, newest first. */
export interface RecentSearchRow {
  id: number;
  query: string;
  searched_at: string;
}

/** A hit of the file index, as `/api/files/search` answers it. */
export interface FileSearchHit {
  id?: number;
  storage_id?: number;
  storage?: string;
  name?: string;
  path?: string;
  type?: string;
}

export const PanelSearchApi = {
  /**
   * People, groups, API keys, apps, storages and shares the reader may see,
   * matching `q`. `kinds` narrows to some of them (a prefix), `limit` caps
   * each kind (the server's default is 8); the server leaves out every kind
   * the reader's role does not open.
   */
  async search(q: string, kinds?: string[], limit?: number): Promise<PanelHit[]> {
    const params: Record<string, string> = { q };
    if (kinds && kinds.length) params.kinds = kinds.join(',');
    if (limit) params.limit = String(limit);
    const { data } = await api.get<{ results?: PanelHit[] }>('/admin/panel-search', { params });
    return Array.isArray(data?.results) ? data.results : [];
  },

  async recent(): Promise<RecentSearchRow[]> {
    const { data } = await api.get<{ searches?: RecentSearchRow[] }>('/admin/panel-search/recent');
    return Array.isArray(data?.searches) ? data.searches : [];
  },

  async addRecent(query: string): Promise<void> {
    await api.post('/admin/panel-search/recent', { query });
  },

  async removeRecent(id: number | string): Promise<void> {
    await api.delete(`/admin/panel-search/recent/${encodeURIComponent(String(id))}`);
  },

  async clearRecent(): Promise<void> {
    await api.delete('/admin/panel-search/recent');
  },

  /**
   * Files and folders matching `q`, the caller's own reach (the explorer's
   * search: `/api/files/search`, which applies every grant and the tenant).
   */
  async files(q: string, limit: number): Promise<FileSearchHit[]> {
    const { data } = await api.get<{ results?: FileSearchHit[] }>('/files/search', { params: { q, limit } });
    return Array.isArray(data?.results) ? data.results : [];
  },
};
