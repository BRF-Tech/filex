import { api } from './client';
import type { ReplicationTarget, ReplicationTargetInput } from './types';

export const ReplicationTargetsApi = {
  async list(): Promise<ReplicationTarget[]> {
    const { data } = await api.get<ReplicationTarget[]>('/admin/replication-targets');
    return data;
  },
  async get(id: number): Promise<ReplicationTarget> {
    const { data } = await api.get<ReplicationTarget>(`/admin/replication-targets/${id}`);
    return data;
  },
  async create(payload: ReplicationTargetInput): Promise<ReplicationTarget> {
    const { data } = await api.post<ReplicationTarget>('/admin/replication-targets', payload);
    return data;
  },
  async update(id: number, payload: Partial<ReplicationTargetInput>): Promise<ReplicationTarget> {
    const { data } = await api.patch<ReplicationTarget>(`/admin/replication-targets/${id}`, payload);
    return data;
  },
  async remove(id: number): Promise<void> {
    await api.delete(`/admin/replication-targets/${id}`);
  },
};
