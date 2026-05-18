import { api } from './client';
import type {
  DriftReport,
  StorageCreateRequest,
  StorageRef,
  StorageUpdateRequest,
  SyncRun,
} from './types';

interface BackendSyncRun {
  id: number;
  storage_id: number;
  storage_name?: string;
  started_at: string;
  finished_at?: string | null;
  // Backend uses `status` (running, ok, partial, failed, aborted),
  // frontend type expects `state` (ok | error | running | aborted).
  status?: string;
  state?: SyncRun['state'];
  // Backend writes `seen_count`; frontend wants `scanned`.
  seen_count?: number;
  scanned?: number;
  added?: number;
  updated?: number;
  deleted?: number;
  error?: string | null;
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

export const StoragesApi = {
  async list(opts: { role?: 'primary' | 'replica' } = {}): Promise<StorageRef[]> {
    const { data } = await api.get<StorageRef[]>('/admin/storages', {
      params: opts.role ? { role: opts.role } : {},
    });
    return data;
  },

  async get(id: number): Promise<StorageRef> {
    const { data } = await api.get<StorageRef>(`/admin/storages/${id}`);
    return data;
  },

  async create(payload: StorageCreateRequest): Promise<StorageRef> {
    const { data } = await api.post<StorageRef>('/admin/storages', payload);
    return data;
  },

  async update(id: number, payload: StorageUpdateRequest): Promise<StorageRef> {
    const { data } = await api.patch<StorageRef>(`/admin/storages/${id}`, payload);
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/storages/${id}`);
  },

  async syncNow(id: number): Promise<{ run_id: number }> {
    const { data } = await api.post<{ run_id: number }>(`/admin/storages/${id}/sync`);
    return data;
  },

  async syncHistory(id: number, limit = 50): Promise<SyncRun[]> {
    // Backend wraps the rows in `{entries, total}` and uses `status`
    // for the run state + `seen_count` for scanned. Normalize to the
    // SyncRun type the views consume so the table doesn't render
    // "undefined" for empty/missing fields.
    const { data } = await api.get<
      | SyncRun[]
      | {
          entries: BackendSyncRun[] | null;
          total?: number;
        }
    >(`/admin/storages/${id}/sync-runs`, {
      params: { limit },
    });
    const rows = Array.isArray(data) ? (data as BackendSyncRun[]) : (data.entries ?? []);
    return rows.map(toSyncRun);
  },

  async drift(id: number): Promise<DriftReport> {
    const { data } = await api.get<DriftReport>(`/admin/storages/${id}/drift`);
    return data;
  },

  async testConnection(payload: StorageCreateRequest): Promise<{ ok: boolean; error?: string }> {
    const { data } = await api.post<{ ok: boolean; error?: string }>(
      '/admin/storages/test',
      payload,
    );
    return data;
  },
};
