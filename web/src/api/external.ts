import { api } from './client';
import type { ExternalService } from './types';

export interface ExternalServiceUpdate {
  url?: string | null;
  jwt_secret?: string | null;
  enabled?: boolean;
}

// Backend wire shape — Go struct without json tags, so fields land
// PascalCase: `{Name, Enabled, URL, SecretEnc, OptionsJSON, LastCheck,
// LastState}`. Wrapped in `{entries: [...]}` (or `{entries: null}` when
// the table is empty). Newer handlers may switch to lowercase tags;
// guard against both.
interface BackendExternal {
  Name?: string;
  name?: string;
  Enabled?: boolean;
  enabled?: boolean;
  URL?: string;
  url?: string;
  SecretEnc?: string;
  secret_enc?: string;
  OptionsJSON?: string;
  options_json?: string;
  LastCheck?: string | null;
  last_check?: string | null;
  LastState?: string;
  last_state?: string;
}
interface ListResponse {
  entries: BackendExternal[] | null;
}

const KNOWN_IDS: ReadonlyArray<ExternalService['id']> = ['onlyoffice', 'drawio'];

function pickName(b: BackendExternal): string {
  return (b.Name ?? b.name ?? '').toLowerCase();
}

// Backend uses `ok | unreachable | disabled | unconfigured | unknown`.
// The frontend's ExternalService type encodes the i18n keys
// (`healthy | configured-unreachable | disabled | unconfigured`).
function mapState(s: string): ExternalService['last_state'] {
  switch (s) {
    case 'ok':
    case 'healthy':
      return 'healthy';
    case 'unreachable':
    case 'configured-unreachable':
      return 'configured-unreachable';
    case 'disabled':
      return 'disabled';
    default:
      return 'unconfigured';
  }
}

function toExternal(b: BackendExternal): ExternalService {
  const name = pickName(b);
  // Coerce arbitrary names to the closed enum the UI expects; unknown
  // services still render with the right shape but the i18n key for
  // their state label may fall through to the zinc default.
  const id = (KNOWN_IDS.includes(name as ExternalService['id'])
    ? name
    : name) as ExternalService['id'];
  const url = (b.URL ?? b.url ?? '') || null;
  const secretEnc = b.SecretEnc ?? b.secret_enc ?? '';
  const rawState = b.LastState ?? b.last_state ?? '';
  const lastCheck = b.LastCheck ?? b.last_check ?? null;
  return {
    id,
    url,
    jwt_secret_set: secretEnc !== '',
    enabled: b.Enabled ?? b.enabled ?? false,
    last_checked_at: lastCheck,
    last_state: mapState(rawState),
    last_error: null,
  };
}

export const ExternalApi = {
  async list(): Promise<ExternalService[]> {
    const { data } = await api.get<ListResponse | ExternalService[] | BackendExternal[]>(
      '/admin/external',
    );
    if (Array.isArray(data)) {
      // Could be either already-normalized ExternalService[] or raw
      // BackendExternal[]; sniff by id key presence.
      return data.map((row) =>
        'id' in row && 'jwt_secret_set' in row
          ? (row as ExternalService)
          : toExternal(row as BackendExternal),
      );
    }
    return (data.entries ?? []).map(toExternal);
  },

  async update(id: ExternalService['id'], patch: ExternalServiceUpdate): Promise<ExternalService> {
    // Backend Update handler expects {enabled, url, secret, options_json}
    // and returns {ok: true} (not the row). Re-fetch so callers get the
    // fresh state to mutate the store.
    const body: Record<string, unknown> = {};
    if (patch.enabled !== undefined) body.enabled = patch.enabled;
    if (patch.url !== undefined) body.url = patch.url;
    if (patch.jwt_secret !== undefined) body.secret = patch.jwt_secret;
    await api.patch(`/admin/external/${id}`, body);
    const all = await ExternalApi.list();
    const found = all.find((s) => s.id === id);
    if (found) return found;
    // Fallback — synthesize a minimal record so the caller's map still works.
    return {
      id,
      url: patch.url ?? null,
      jwt_secret_set: !!patch.jwt_secret,
      enabled: !!patch.enabled,
      last_checked_at: null,
      last_state: 'unconfigured',
      last_error: null,
    };
  },

  async test(id: ExternalService['id']): Promise<ExternalService> {
    // Test handler returns {ok, name, reachable, url, state, error?},
    // not a full ExternalService row. Use the response for last_state
    // + error then re-fetch the list so jwt_secret_set/enabled stay
    // accurate.
    const { data } = await api.post<{
      ok: boolean;
      name: string;
      reachable?: boolean;
      url?: string;
      state?: string;
      error?: string;
    }>(`/admin/external/${id}/test`);
    const all = await ExternalApi.list();
    const found = all.find((s) => s.id === id);
    const mapped = data.state ? mapState(data.state) : undefined;
    if (found) {
      return {
        ...found,
        last_state: mapped ?? found.last_state,
        last_error: data.error ?? null,
        last_checked_at: new Date().toISOString(),
      };
    }
    return {
      id,
      url: data.url ?? null,
      jwt_secret_set: false,
      enabled: true,
      last_checked_at: new Date().toISOString(),
      last_state: mapped ?? 'unconfigured',
      last_error: data.error ?? null,
    };
  },
};
