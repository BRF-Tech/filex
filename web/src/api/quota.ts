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
};
