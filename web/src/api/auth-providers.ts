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
    // Backend Update writes settings + returns `{ok, warning}` (no
    // row). Send the patch.config map at the top level since the
    // handler iterates the JSON body keys directly. Then re-fetch
    // the list so the caller gets a fresh row.
    const body: Record<string, unknown> = {
      ...(patch.config ?? {}),
    };
    if (patch.enabled !== undefined) body.enabled = patch.enabled;
    await api.patch(`/admin/auth-providers/${id}`, body);
    const all = await AuthProvidersApi.list();
    const found = all.find((p) => p.id === id);
    if (found) return found;
    return {
      id,
      enabled: !!patch.enabled,
      config: patch.config ?? {},
      config_redacted: {},
      status: patch.enabled ? 'ok' : 'disabled',
      last_error: null,
    };
  },

  async test(id: AuthProvider['id']): Promise<{ ok: boolean; error?: string }> {
    const { data } = await api.post<{ ok: boolean; error?: string; note?: string }>(
      `/admin/auth-providers/${id}/test`,
    );
    return { ok: !!data.ok, error: data.error ?? data.note };
  },
};
