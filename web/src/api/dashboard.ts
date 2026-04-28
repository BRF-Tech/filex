import { api } from './client';
import type { DashboardStats } from './types';

export const DashboardApi = {
  async stats(): Promise<DashboardStats> {
    const { data } = await api.get<DashboardStats>('/admin/dashboard');
    return data;
  },
};
