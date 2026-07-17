import { api } from './client';

// Backend `internal/quota.Snapshot`. Note: percent_used is 0..100 (not 0..1)
// and is server-computed; quota_bytes == 0 means unlimited (`unlimited: true`).
export interface QuotaSnapshot {
  used_bytes: number;
  quota_bytes: number;
  percent_used: number;
  unlimited: boolean;
}

export const quotaApi = {
  async me(): Promise<QuotaSnapshot> {
    const res = await api.get<QuotaSnapshot>('/files/quota/me');
    return res.data;
  },

  // ── koru:k3 — admin quota surface (Users → edit) ─────────────────
  // Registered backend routes (internal/api/routes.go):
  //   POST /api/admin/quota/{user_id}            → set quota_bytes, returns Snapshot
  //   POST /api/admin/quota/{user_id}/recompute  → rescan usage, returns {used_bytes}
  // There is no admin GET today; adminGet is speculative so the edit page can
  // show current usage once the backend adds it — callers must tolerate 404/405.

  async adminGet(userId: number): Promise<QuotaSnapshot> {
    const res = await api.get<QuotaSnapshot>(`/admin/quota/${userId}`);
    return res.data;
  },

  async adminSet(userId: number, quotaBytes: number): Promise<QuotaSnapshot> {
    const res = await api.post<QuotaSnapshot>(`/admin/quota/${userId}`, {
      quota_bytes: quotaBytes,
    });
    return res.data;
  },

  async adminRecompute(userId: number): Promise<number> {
    const res = await api.post<{ ok: boolean; used_bytes: number }>(
      `/admin/quota/${userId}/recompute`,
    );
    return res.data.used_bytes;
  },
};
