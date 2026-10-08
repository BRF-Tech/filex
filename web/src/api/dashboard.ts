import { api } from './client';
import { toAuditEntry } from './audit';
import { langParam } from './screenLang';
import type { AuditEntry, DashboardStats } from './types';

// Backend wire shape — `{storages:[{...}], total_users, active_sessions,
// queue_depth, total_files, total_bytes, active_syncs, last_sync_at,
// recent_activity, capabilities}`. The frontend DashboardStats type names
// some of them differently; this renames at the boundary and adds up
// NOTHING: the totals, the running count and the newest scan are the
// server's (handlers/dashboard.go). ⚠ Before 0.54 this summed the storage
// rows again and found the newest scan by sorting the timestamps as text.
//
// ⚠ There is no run list in this payload. The Recent syncs card reads the
// sync-run list (stores/sync) — the same source as the Sync page.
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
  total_files?: number;
  total_bytes?: number;
  active_syncs?: number;
  last_sync_at?: string | null;
  recent_activity?: AuditEntry[];
  capabilities?: Record<string, unknown>;
}

function normalize(d: BackendDashboard): DashboardStats {
  return {
    storage_count: (d.storages ?? []).length,
    user_count: d.total_users ?? 0,
    total_files: d.total_files ?? 0,
    total_bytes: d.total_bytes ?? 0,
    active_sync_count: d.active_syncs ?? 0,
    queue_depth: d.queue_depth ?? 0,
    last_sync_at: d.last_sync_at ?? null,
    recent_audit: (d.recent_activity ?? []).map(toAuditEntry),
  };
}

export const DashboardApi = {
  async stats(): Promise<DashboardStats> {
    // The activity rows' words are said in the screen's language.
    const { data } = await api.get<BackendDashboard>('/admin/dashboard', { params: langParam() });
    return normalize(data);
  },
};
