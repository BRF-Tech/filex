import { api } from './client';
import type { AuthProvider } from './types';

export interface AuthProviderUpdate {
  enabled?: boolean;
  config?: Record<string, unknown>;
}

export const AuthProvidersApi = {
  async list(): Promise<AuthProvider[]> {
    const { data } = await api.get<AuthProvider[]>('/admin/auth-providers');
    return data;
  },

  async update(id: AuthProvider['id'], patch: AuthProviderUpdate): Promise<AuthProvider> {
    const { data } = await api.patch<AuthProvider>(`/admin/auth-providers/${id}`, patch);
    return data;
  },

  async test(id: AuthProvider['id']): Promise<{ ok: boolean; error?: string }> {
    const { data } = await api.post<{ ok: boolean; error?: string }>(
      `/admin/auth-providers/${id}/test`,
    );
    return data;
  },
};
