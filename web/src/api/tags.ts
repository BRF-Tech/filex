import { api } from './client';
import type { SearchHit } from './types';
import { tagItemsOf, type TagItem, type TagKind } from '@brftech/filex-core';

// Tags are PERSONAL (the caller's own, like a star) or TEAM (the tenant's,
// shared with everyone who can see the file) since v0.43.0 — before that they
// were one kind, shared with every account on the server although this very
// comment called them "shared across users (node_meta)". These two endpoints
// power the "Tagged files" page:
//   GET /api/files/manager/tags/all → {tags: string[], items: [{name, kind}]}
//   GET /api/files/manager/tagged?tag=…&kind=… → {nodes: Node[], tag, kind}
//
// ⚠ Like search.ts, the backend returns raw Node rows (name/updated_at, no
// score), NOT a SearchHit envelope. Adapt the shape here or the page goes
// blank on `hit.filename`/`hit.score`.

export const TagsApi = {
  /** Every tag the caller can see, both kinds (an older server's plain
   *  names arrive as team — they were shared with everybody). */
  async listAllTags(): Promise<TagItem[]> {
    const { data } = await api.get<unknown>('/files/manager/tags/all');
    return tagItemsOf(data);
  },

  /** Files carrying `tag`; `kind` narrows to one kind, '' = both. */
  async filesByTag(tag: string, kind: TagKind | '' = ''): Promise<SearchHit[]> {
    const { data } = await api.get<{
      tag: string;
      nodes: Array<{
        id: number;
        storage_id: number;
        /** The storage's NAME, attached by the server (handlers/meta.go → rows). */
        storage?: string;
        name: string;
        path: string;
        size?: number;
        mime?: string;
        backend_mtime?: string | null;
        updated_at?: string;
      }> | null;
    }>('/files/manager/tagged', { params: kind ? { tag, kind } : { tag } });
    const nodes = data.nodes ?? [];
    return nodes.map((n) => ({
      id: String(n.id),
      storage_id: n.storage_id,
      // ⚠ The server already says which storage each row is in — `rows()` in
      // handlers/meta.go attaches the NAME for exactly this reason, and it is
      // the same field Recent and Starred read. Blanking it here left the
      // Tagged files page drawing an em dash in every Storage cell, which
      // reads as "this file belongs to no storage" (seen taking the v0.43.0
      // tags picture).
      storage_name: n.storage ?? '',
      path: n.path,
      filename: n.name,
      size: n.size ?? 0,
      mime: n.mime ?? '',
      modified_at: n.backend_mtime || n.updated_at || '',
      score: 0,
    }));
  },
};
