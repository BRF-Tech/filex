import { api } from './client';
import type { AuditEntry, DashboardStats, SyncRun } from './types';

// Backend wire shape — `{storages:[{...}], total_users, active_sessions,
// queue_depth, recent_activity, capabilities}`. The frontend
// DashboardStats type expects `{storage_count, user_count, total_files,
// total_bytes, active_sync_count, queue_depth, last_sync_at,
// recent_audit, recent_syncs}` — completely different keys. Normalize
// at the boundary so the Dashboard cards render real numbers instead
// of "—".
interface BackendStorageRow {
  id: number;
  name?: string;
  driver?: string;
  enabled?: boolean;
  total_files?: number;
  total_bytes?: number;
  last_sync_at?: string | null;
  state?: string;
}

interface BackendDashboard {
  storages?: BackendStorageRow[];
  total_users?: number;
  active_sessions?: number;
  queue_depth?: number;
  recent_activity?: AuditEntry[];
  recent_syncs?: SyncRun[];
  capabilities?: Record<string, unknown>;
}

function normalize(d: BackendDashboard): DashboardStats {
  const storages = d.storages ?? [];
  const totalFiles = storages.reduce((acc, s) => acc + (s.total_files ?? 0), 0);
  const totalBytes = storages.reduce((acc, s) => acc + (s.total_bytes ?? 0), 0);
  const activeSync = storages.filter((s) => (s.state ?? '') === 'running').length;
  const lastSync = storages
    .map((s) => s.last_sync_at ?? '')
    .filter((x) => x !== '')
    .sort()
    .pop() ?? null;
  return {
    storage_count: storages.length,
    user_count: d.total_users ?? 0,
    total_files: totalFiles,
    total_bytes: totalBytes,
    active_sync_count: activeSync,
    queue_depth: d.queue_depth ?? 0,
    last_sync_at: lastSync,
    recent_audit: d.recent_activity ?? [],
    recent_syncs: d.recent_syncs ?? [],
  };
}

export const DashboardApi = {
  async stats(): Promise<DashboardStats> {
    const { data } = await api.get<BackendDashboard | DashboardStats>('/admin/dashboard');
    // If the backend already returns the normalized shape (e.g. a
    // future patched server) just pass through.
    if ((data as DashboardStats).storage_count !== undefined) {
      return data as DashboardStats;
    }
    return normalize(data as BackendDashboard);
  },
};
