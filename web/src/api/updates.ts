// Release awareness adapter (see docs/UPDATES.md).
//
// Contract:
//   GET  /api/admin/update        → UpdateStatus   (cached; never hits the network)
//   POST /api/admin/update/check  → UpdateStatus   (forces a fetch)
//   POST /api/admin/update/apply  → { ok, applied, restart_required } | 409
//
// A 409 from /apply is not an error to shout about: it is the container case,
// where filex cannot replace its own image. The payload then carries the
// instructions to show instead.
import { api } from './client';

/** What filex intends to do about the newest release it knows of. */
export type UpdateAction = 'none' | 'auto' | 'confirm' | 'instruct';
/** Which part of x.y.z moved. */
export type UpdateStep = 'none' | 'patch' | 'minor' | 'major';

export interface UpdateRelease {
  version: string;
  date?: string;
  notes?: string;
  notes_url?: string;
  security?: boolean;
  migrations?: boolean;
}

export interface UpdateStatus {
  current: string;
  enabled: boolean;
  /** off | manual | patch | minor */
  policy: string;
  /** binary | docker */
  mode: string;
  can_self_apply: boolean;
  /** A new binary is on disk but this process is still the old one. */
  restart_required: boolean;
  checked_at?: string;
  check_error?: string;
  action: UpdateAction;
  step: UpdateStep;
  reason?: string;
  latest?: UpdateRelease;
  /** Releases between the running version and the target. */
  skipped?: UpdateRelease[];
  /** Copy-paste upgrade steps, rendered server-side for this install shape. */
  instructions?: string[];
  last_applied?: string;
  last_apply_error?: string;
}

export interface ApplyResult {
  ok?: boolean;
  applied?: string;
  restart_required?: boolean;
  message?: string;
  error?: string;
  instructions?: string[];
}

export const UpdatesApi = {
  async status(): Promise<UpdateStatus> {
    const { data } = await api.get<UpdateStatus>('/admin/update');
    return data;
  },

  async check(): Promise<UpdateStatus> {
    const { data } = await api.post<UpdateStatus>('/admin/update/check');
    return data;
  },

  /** Installs the pending release. Resolves with the 409 body (instructions)
   *  rather than throwing when the install cannot upgrade itself. */
  async apply(): Promise<ApplyResult> {
    try {
      const { data } = await api.post<ApplyResult>('/admin/update/apply');
      return data;
    } catch (e: any) {
      if (e?.response?.status === 409) return e.response.data as ApplyResult;
      throw e;
    }
  },
};
