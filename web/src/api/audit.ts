import { api } from './client';
import type { AuditEntry, PaginatedResponse } from './types';

export interface AuditListParams {
  user_id?: number;
  action?: string;
  target_type?: string;
  from?: string; // ISO datetime
  to?: string;
  page?: number;
  page_size?: number;
}

export const AuditApi = {
  async list(params: AuditListParams = {}): Promise<PaginatedResponse<AuditEntry>> {
    const { data } = await api.get<PaginatedResponse<AuditEntry>>('/admin/audit', { params });
    return data;
  },
};
