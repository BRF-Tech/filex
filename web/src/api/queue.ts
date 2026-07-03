import { api } from './client';
import type { QueueListResponse, QueueOp, QueueStats } from './types';

// QueueApi wraps the /admin/api/queue/... endpoints. Returns raw axios
// data; stores wrap the calls and surface errors via toast.
export const QueueApi = {
  async stats(): Promise<QueueStats> {
    const { data } = await api.get<QueueStats>('/admin/queue/stats');
    return data;
  },

  async list(params: { status?: string; limit?: number; offset?: number } = {}): Promise<QueueListResponse> {
    const { data } = await api.get<QueueListResponse>('/admin/queue', { params });
    return data;
  },

  async get(id: string): Promise<QueueOp> {
    const { data } = await api.get<QueueOp>(`/admin/queue/${id}`);
    return data;
  },

  async retry(id: string): Promise<void> {
    await api.post(`/admin/queue/${id}/retry`);
  },

  async cancel(id: string): Promise<void> {
    await api.delete(`/admin/queue/${id}`);
  },
};
