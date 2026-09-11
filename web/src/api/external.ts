import { api } from './client';
import type { ExternalAdvisory, ExternalService } from './types';

export interface ExternalServiceUpdate {
  url?: string | null;
  jwt_secret?: string | null;
  enabled?: boolean;
  /** The address the document server reaches filex at. '' clears it. */
  callback_url?: string | null;
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
  env_managed?: boolean;
  advisories?: ExternalAdvisory[] | null;
  callback_url?: string;
}
interface ListResponse {
  entries: BackendExternal[] | null;
  /**
   * The address filex hands the document server for the fetch and the save
   * callback. The third address in the three-address problem, and the one
   * nothing used to show anywhere.
   */
  public_url?: string;
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
    env_managed: b.env_managed === true,
    advisories: b.advisories ?? [],
    callback_url: b.callback_url ?? '',
  };
}

/** What the server-side Test probe answered, plus what it did NOT cover. */
export interface ExternalTestResult {
  service: ExternalService['id'];
  /** True when the filex PROCESS reached the service. Says nothing else. */
  serverReachable: boolean;
  state: ExternalService['last_state'];
  error: string | null;
  advisories: ExternalAdvisory[];
  publicURL: string;
  /**
   * The third leg: did the DOCUMENT SERVER's request actually reach filex?
   * `checked: false` means the question could not be put — never render that
   * as a broken route.
   */
  serviceToFilex: {
    checked: boolean;
    ok: boolean;
    url?: string;
    code?: number;
    detail?: string;
  };
}

/**
 * FILEX_PUBLIC_URL as the server reports it, captured on the last list()/test()
 * call. The admin page shows it next to the document-server field because it
 * is the address the operator has no other way to see.
 */
let lastPublicURL = '';
export function externalPublicURL(): string {
  return lastPublicURL;
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
    if (typeof data.public_url === 'string') lastPublicURL = data.public_url;
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
    if (patch.callback_url !== undefined) body.callback_url = patch.callback_url ?? '';
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

  /**
   * Run the SERVER-side probe. ⚠ It answers the filex→service leg directly,
   * and now carries the third leg with it: `serviceToFilex` is the document
   * server's own attempt to fetch a one-shot URL from filex. The browser leg
   * is separate — see `probeExternalFromBrowser`.
   */
  async test(id: ExternalService['id']): Promise<ExternalTestResult> {
    const { data } = await api.post<{
      ok: boolean;
      name: string;
      reachable?: boolean;
      url?: string;
      state?: string;
      error?: string;
      checked_from?: string;
      not_checked?: string[];
      public_url?: string;
      advisories?: ExternalAdvisory[] | null;
      server_reachable?: boolean;
      has_warnings?: boolean;
      callback_url?: string;
      service_to_filex?: {
        checked?: boolean;
        ok?: boolean;
        url?: string;
        code?: number;
        detail?: string;
      };
    }>(`/admin/external/${id}/test`);
    if (typeof data.public_url === 'string') lastPublicURL = data.public_url;
    return {
      service: id,
      serverReachable: data.server_reachable ?? data.reachable === true,
      state: data.state ? mapState(data.state) : 'unconfigured',
      error: data.error ?? null,
      advisories: data.advisories ?? [],
      publicURL: data.public_url ?? lastPublicURL,
      serviceToFilex: {
        checked: data.service_to_filex?.checked === true,
        ok: data.service_to_filex?.ok === true,
        url: data.service_to_filex?.url,
        code: data.service_to_filex?.code,
        detail: data.service_to_filex?.detail,
      },
    };
  },
};
