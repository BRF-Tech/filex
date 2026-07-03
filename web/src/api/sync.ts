import { api } from './client';
import type { PaginatedResponse, SyncRun } from './types';

export interface SyncRunListParams {
  storage_id?: number;
  state?: SyncRun['state'];
  page?: number;
  page_size?: number;
}

interface BackendSyncRun {
  id: number;
  storage_id: number;
  storage_name?: string;
  started_at: string;
  finished_at?: string | null;
  status?: string;
  state?: SyncRun['state'];
  seen_count?: number;
  scanned?: number;
  added?: number;
  updated?: number;
  deleted?: number;
  error?: string | null;
}

interface BackendListResponse {
  entries: BackendSyncRun[] | null;
  total?: number;
  limit?: number;
  offset?: number;
}

function mapStatus(s: string | undefined): SyncRun['state'] {
  switch (s) {
    case 'ok':
    case 'partial':
      return 'ok';
    case 'failed':
      return 'error';
    case 'running':
      return 'running';
    case 'aborted':
      return 'aborted';
    default:
      return (s as SyncRun['state']) || 'ok';
  }
}

function toSyncRun(b: BackendSyncRun): SyncRun {
  return {
    id: b.id,
    storage_id: b.storage_id,
    storage_name: b.storage_name ?? '',
    started_at: b.started_at,
    finished_at: b.finished_at ?? null,
    state: b.state ?? mapStatus(b.status),
    added: b.added ?? 0,
    updated: b.updated ?? 0,
    deleted: b.deleted ?? 0,
    scanned: b.scanned ?? b.seen_count ?? 0,
    error: b.error ?? null,
  };
}

export const SyncApi = {
  async list(params: SyncRunListParams = {}): Promise<PaginatedResponse<SyncRun>> {
    // Backend admin handler returns `{entries, total, limit, offset}`
    // and uses `status` instead of `state`. Normalize both shapes.
    const { data } = await api.get<PaginatedResponse<SyncRun> | BackendListResponse>(
      '/admin/sync-runs',
      { params },
    );
    if ('items' in data && Array.isArray(data.items)) {
      return data as PaginatedResponse<SyncRun>;
    }
    const env = data as BackendListResponse;
    const items = (env.entries ?? []).map(toSyncRun);
    const limit = env.limit ?? items.length;
    const offset = env.offset ?? 0;
    return {
      items,
      total: env.total ?? items.length,
      page: limit > 0 ? Math.floor(offset / limit) + 1 : 1,
      page_size: limit || items.length || 25,
    };
  },

  async get(id: number): Promise<SyncRun> {
    const { data } = await api.get<SyncRun | { run: BackendSyncRun }>(`/admin/sync-runs/${id}`);
    if ('run' in (data as { run?: unknown }) && (data as { run: BackendSyncRun }).run) {
      return toSyncRun((data as { run: BackendSyncRun }).run);
    }
    return data as SyncRun;
  },
};
