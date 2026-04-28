import { api } from './client';
import type { SettingsMap } from './types';

export const SettingsApi = {
  async get(): Promise<SettingsMap> {
    const { data } = await api.get<SettingsMap>('/admin/settings');
    return data;
  },

  async update(patch: Partial<SettingsMap>): Promise<SettingsMap> {
    const { data } = await api.patch<SettingsMap>('/admin/settings', patch);
    return data;
  },
};
