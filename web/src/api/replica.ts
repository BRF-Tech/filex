import { api } from './client';
import type {
  ReplicaFailureListResponse,
  ReplicaRule,
  ReplicaRuleInput,
  ReplicaSettings,
  ReplicaStatusReport,
} from './types';

export const ReplicaApi = {
  // Rules
  async listRules(): Promise<ReplicaRule[]> {
    const { data } = await api.get<{ items: ReplicaRule[] }>('/admin/replica/rules');
    return data.items ?? [];
  },

  async createRule(payload: ReplicaRuleInput): Promise<ReplicaRule> {
    const { data } = await api.post<ReplicaRule>('/admin/replica/rules', payload);
    return data;
  },

  async updateRule(id: number, payload: ReplicaRuleInput): Promise<ReplicaRule> {
    const { data } = await api.patch<ReplicaRule>(`/admin/replica/rules/${id}`, payload);
    return data;
  },

  async deleteRule(id: number): Promise<void> {
    await api.delete(`/admin/replica/rules/${id}`);
  },

  // Failures
  async listFailures(params: { unresolved?: boolean; limit?: number; offset?: number } = {}): Promise<ReplicaFailureListResponse> {
    const { data } = await api.get<ReplicaFailureListResponse>('/admin/replica/failures', {
      params: { ...params, unresolved: params.unresolved ? 'true' : undefined },
    });
    return data;
  },

  async failureCount(): Promise<number> {
    const { data } = await api.get<{ count: number }>('/admin/replica/failures/count');
    return data.count;
  },

  /** `already_queued`: failures whose retry was still waiting in the queue,
   *  left alone (an older server does not send it). */
  async fixAll(): Promise<{ queued: number; already_queued?: number }> {
    const { data } = await api.post<{ queued: number; already_queued?: number }>('/admin/replica/fix');
    return data;
  },

  /** `queued: false`: a retry of this failure was already waiting. */
  async fixOne(path: string, op: string): Promise<{ ok: boolean; queued?: boolean }> {
    const { data } = await api.post<{ ok: boolean; queued?: boolean }>('/admin/replica/fix-one', { path, op });
    return data;
  },

  // Status report
  async getReport(): Promise<ReplicaStatusReport | null> {
    const res = await api.get<ReplicaStatusReport>('/admin/replica/report', {
      validateStatus: (s) => s === 200 || s === 204,
    });
    if (res.status === 204) return null;
    return res.data;
  },

  async runReportNow(): Promise<{ ok: boolean }> {
    const { data } = await api.post<{ ok: boolean }>('/admin/replica/report/run-now');
    return data;
  },

  // Settings
  async getSettings(): Promise<ReplicaSettings> {
    const { data } = await api.get<ReplicaSettings>('/admin/replica/settings');
    return data;
  },

  async updateSettings(payload: ReplicaSettings): Promise<ReplicaSettings> {
    const { data } = await api.patch<ReplicaSettings>('/admin/replica/settings', payload);
    return data;
  },
};
