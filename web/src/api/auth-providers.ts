import { api } from './client';
import type { AuthProvider } from './types';

export interface AuthProviderUpdate {
  enabled?: boolean;
  config?: Record<string, unknown>;
}

// Backend wire shape — `{providers: [{name, enabled, capabilities,
// config_redacted}, ...]}`. We normalize to the AuthProvider type the
// view consumes (id-keyed, with status fallback).
interface BackendProvider {
  name: string;
  enabled: boolean;
  capabilities?: Record<string, boolean>;
  config_redacted?: Record<string, unknown>;
}
interface ListResponse {
  providers: BackendProvider[];
}

function toAuthProvider(p: BackendProvider): AuthProvider {
  // Map backend `name` → frontend `id`. The status field doesn't
  // exist on the backend yet (auth_providers handler doesn't compute
  // it); fall back to enabled/disabled so the UI never renders an
  // "ok" badge for a misconfigured provider by accident.
  return {
    id: p.name as AuthProvider['id'],
    enabled: p.enabled,
    config: {},
    config_redacted: p.config_redacted ?? {},
    status: p.enabled ? 'ok' : 'disabled',
    last_error: null,
  };
}

export const AuthProvidersApi = {
  async list(): Promise<AuthProvider[]> {
    const { data } = await api.get<ListResponse | AuthProvider[]>('/admin/auth-providers');
    if (Array.isArray(data)) return data;
    return (data.providers ?? []).map(toAuthProvider);
  },

  async update(id: AuthProvider['id'], patch: AuthProviderUpdate): Promise<AuthProvider> {
    const { data } = await api.patch<BackendProvider | AuthProvider>(
      `/admin/auth-providers/${id}`,
      patch,
    );
    return 'name' in data ? toAuthProvider(data) : data;
  },

  async test(id: AuthProvider['id']): Promise<{ ok: boolean; error?: string }> {
    const { data } = await api.post<{ ok: boolean; error?: string }>(
      `/admin/auth-providers/${id}/test`,
    );
    return data;
  },
};
