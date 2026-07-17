// koru:k3 — data-protection settings adapter.
//
// Frozen contract (koru wave #1):
//   GET   /api/admin/protection  → ProtectionSettings
//   PATCH /api/admin/protection  → partial {trash_retention_days?, versions_keep_n?}
//
// The backend half ships in the same wave; when the endpoint is missing
// (older server → 404/405) the Protection view shows an "unsupported" band
// instead of erroring, so the SPA stays deployable ahead of the backend.
import { api } from './client';

export interface ProtectionAntivirus {
  enabled: boolean;
  /** Resolved scanner binary ("clamscan" | "clamdscan" | ""). */
  binary: string;
}

export interface ProtectionSettings {
  /** Days a soft-deleted node survives in the trash before the purge job
   *  hard-deletes it. Backend floor is 1 (values <= 0 fall back to 30). */
  trash_retention_days: number;
  /** Versions kept per node by the daily cleanup. 0 = unlimited (no cleanup). */
  versions_keep_n: number;
  antivirus: ProtectionAntivirus;
}

export interface ProtectionPatch {
  trash_retention_days?: number;
  versions_keep_n?: number;
}

export const ProtectionApi = {
  async get(): Promise<ProtectionSettings> {
    const { data } = await api.get<ProtectionSettings>('/admin/protection');
    return data;
  },

  async update(patch: ProtectionPatch): Promise<ProtectionSettings> {
    const { data } = await api.patch<ProtectionSettings>('/admin/protection', patch);
    return data;
  },
};
