import { api } from './client';
import type { SearchHit } from './types';

// Tags are shared across users (stored on node_meta). These two endpoints
// power the "Tagged files" page:
//   GET /api/files/manager/tags/all → {tags: string[]}
//   GET /api/files/manager/tagged?tag=… → {nodes: Node[], tag}
//
// ⚠ Like search.ts, the backend returns raw Node rows (name/updated_at, no
// score), NOT a SearchHit envelope. Adapt the shape here or the page goes
// blank on `hit.filename`/`hit.score`.

export const TagsApi = {
  async listAllTags(): Promise<string[]> {
    const { data } = await api.get<{ tags: string[] }>('/files/manager/tags/all');
    return data.tags ?? [];
  },

  async filesByTag(tag: string): Promise<SearchHit[]> {
    const { data } = await api.get<{
      tag: string;
      nodes: Array<{
        id: number;
        storage_id: number;
        name: string;
        path: string;
        size?: number;
        mime?: string;
        backend_mtime?: string | null;
        updated_at?: string;
      }> | null;
    }>('/files/manager/tagged', { params: { tag } });
    const nodes = data.nodes ?? [];
    return nodes.map((n) => ({
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
  },
};
