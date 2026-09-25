import { api } from './client';
import { normalizeOp, type PendingOp } from '@brftech/filex-core';

/* The admin tray reads ops through the SAME normalizer as the explorer's
 * (core usePendingOps): #48 dropped the private copy this file carried. It is
 * re-exported so callers and tests keep importing it from here. */
export { normalizeOp };
export type { PendingOp } from '@brftech/filex-core';

export interface OpsListResponse {
  ops: PendingOp[];
}


export const opsApi = {
  /**
   * List pending ops. Optional `status` filter mirrors the SFC's poll
   * shape. Returns an empty array when the endpoint is missing
   * (404 swallowed) so callers don't spam errors.
   */
  async list(params: { status?: 'running' | 'pending' | 'cancelling' | 'done' | 'error' | 'cancelled' } = {}): Promise<PendingOp[]> {
    try {
      const res = await api.get<OpsListResponse | { ops: Array<Record<string, unknown>> }>(
        '/files/ops',
        { params },
      );
      const data = res.data;
      const arr = Array.isArray((data as OpsListResponse).ops) ? (data as OpsListResponse).ops : [];
      return arr.map((row) => normalizeOp(row as unknown as Record<string, unknown>));
    } catch (e: unknown) {
      // 404/405/501 — backend hasn't wired the list yet OR is on an
      // older release where chi answers "method not allowed" because
      // only POST /ops is registered. Silent fallback in all three
      // cases so the tray polls forever without spamming the console.
      const status = (e as { response?: { status?: number } }).response?.status;
      if (status === 404 || status === 405 || status === 501) return [];
      throw e;
    }
  },

  /** Get one op by id. */
  async get(id: number): Promise<PendingOp | null> {
    try {
      const res = await api.get<Record<string, unknown>>(`/files/ops/${id}`);
      if (!res.data) return null;
      return normalizeOp(res.data);
    } catch (e: unknown) {
      const status = (e as { response?: { status?: number } }).response?.status;
      if (status === 404) return null;
      throw e;
    }
  },

  /** Request cooperative cancellation and return the updated operation. */
  async cancel(id: number): Promise<PendingOp> {
    const res = await api.post<{ op: Record<string, unknown> }>(`/files/ops/${id}/cancel`);
    return normalizeOp(res.data.op);
  },
};
