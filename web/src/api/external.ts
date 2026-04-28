import { api } from './client';
import type { ExternalService } from './types';

export interface ExternalServiceUpdate {
  url?: string | null;
  jwt_secret?: string | null;
  enabled?: boolean;
}

export const ExternalApi = {
  async list(): Promise<ExternalService[]> {
    const { data } = await api.get<ExternalService[]>('/admin/external');
    return data;
  },

  async update(id: ExternalService['id'], patch: ExternalServiceUpdate): Promise<ExternalService> {
    const { data } = await api.patch<ExternalService>(`/admin/external/${id}`, patch);
    return data;
  },

  async test(id: ExternalService['id']): Promise<ExternalService> {
    const { data } = await api.post<ExternalService>(`/admin/external/${id}/test`);
    return data;
  },
};
