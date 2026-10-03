import { api } from './client';

// Storage plugins — drivers that live outside the filex binary
// (/api/admin/plugins, backend/internal/plugin, docs/PLUGINS.md).
//
// ⚠ Tokens are write-only here too: a remote plugin's bearer token is sealed
// server-side and never comes back in a response.

/** What a plugin says it can do, as it described itself. */
export interface PluginCapabilities {
  range: boolean;
  write: boolean;
  delete: boolean;
  move: boolean;
  copy: boolean;
  mkdir: boolean;
  set_mtime: boolean;
  watch: boolean;
  /** Hands the client a URL that skips filex entirely. */
  presign: boolean;
  /** Resumable uploads assembled from parts. */
  multipart: boolean;
}

/** Runtime states the server reports. */
export type PluginState = 'disabled' | 'starting' | 'running' | 'failed' | 'refused';

/** How the server treats a plugin that fails its own claims. */
export type ConformanceMode = 'enforce' | 'warn' | 'off';

export type ProbeStatus = 'pass' | 'fail' | 'skip';

/** One capability's verdict from a conformance run. */
export interface PluginProbe {
  /** The capability or behaviour probed: `list`, `write`, `range`… */
  name: string;
  status: ProbeStatus;
  /**
   * For a failure, the message that tells the plugin's author what to fix —
   * so it is shown in full, never truncated.
   */
  detail?: string;
  /** How long the probe took, in milliseconds. */
  took_ms: number;
}

/** Where a conformance run did its work. */
export type ProbeScratch = 'selftest' | 'storage';

/**
 * The last verification of a plugin's own claims. Absent when the plugin has
 * never been probed — that is "unverified", which is not the same as failed.
 */
export interface PluginConformance {
  /** True when every declared capability passed. */
  verified: boolean;
  scratch: ProbeScratch;
  results: PluginProbe[];
  ran_at: string;
}

/** What a plugin is doing right now, and whether callers are being refused. */
export interface PluginLoad {
  in_flight: number;
  /** Calls that had to wait for a slot. */
  waited: number;
  /** Calls that gave up waiting — anything above 0 is a user meeting an error. */
  rejected: number;
  max_in_flight: number;
}

export interface Plugin {
  id: number;
  name: string;
  kind: 'binary' | 'remote';
  binary?: string;
  sha256?: string;
  address?: string;
  enabled: boolean;
  version?: string;
  /** The driver name the plugin described; the storage driver is `plugin:<driver>`. */
  driver?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;

  state: PluginState;
  state_error?: string;
  restarts: number;
  capabilities?: PluginCapabilities;
  label?: string;
  field_count: number;
  /** Storages currently using this plugin's driver. */
  in_use: number;
  conformance?: PluginConformance;
  load: PluginLoad;
  /**
   * Where newer versions of a binary plugin are published: `owner/name` (the
   * filex-storage.json attached to the repository's latest release) or the
   * https address of a filex-storage.json. Absent = none.
   */
  source?: string;
  /** What the last check of the source found; absent before the first. */
  update?: PluginUpdate;
}

/**
 * What the last check of a plugin's source found (plugin.UpdateInfo).
 * ⚠⚠ Nothing updates itself: `available` waits for an administrator's
 * "Review update".
 */
export interface PluginUpdate {
  checked_at?: string;
  status: 'current' | 'available' | 'incompatible' | 'check_failed' | string;
  version?: string;
  requires?: string;
  /** The feed's notes, plain text. */
  notes?: string;
  /** The build for this server's platform. */
  sha256?: string;
  platform?: string;
  error?: string;
}

/** `POST /admin/plugins/updates/check` — one check, as it went. */
export interface PluginUpdateReport {
  checked_at: string;
  checked: number;
  available: string[];
  failed: string[];
}

export interface PluginList {
  plugins: Plugin[];
  /** Where binary plugins live on the server, shown in the empty state. */
  dir: string;
  /** The instance trusts a set of keys: an installed binary must be signed. */
  requires_signature: boolean;
  /**
   * The server calls this `conformance` at the top level while each plugin's
   * REPORT is also `conformance`; renamed here so a mode can never be read as
   * a report.
   */
  conformance_mode: ConformanceMode;
  /** The daily check of the plugins' sources runs. */
  update_check: boolean;
  /** When the sources were last checked; null = never. */
  updates_checked_at: string | null;
}

/** The list endpoint's raw shape, before the rename above. */
interface PluginListResponse {
  plugins?: Plugin[];
  dir?: string;
  requires_signature?: boolean;
  conformance?: ConformanceMode;
  update_check?: boolean;
  updates_checked_at?: string | null;
}

/**
 * The plugin as it is running AFTER a failed upgrade. The server rolls back
 * to the previous binary and returns it alongside the error, so the page can
 * say what is running now instead of leaving the operator guessing.
 */
export function rolledBackPlugin(err: unknown): Plugin | null {
  const res = (err as { response?: { data?: { plugin?: Plugin | null } } })?.response;
  return res?.data?.plugin ?? null;
}

/**
 * How long installing, upgrading or restarting a storage plugin may take.
 *
 * ⚠ Not the client's 30 s default. The server runs the plugin's conformance
 * suite after the binary arrives (up to 90 s) and waits for a restarted plugin
 * to come up (30 s) and answer its health check (15 s); at 30 s the page said
 * "timeout of 30000ms exceeded" about an install that was working. The app
 * plugins' install has the same budget (INSTALL_TIMEOUT_MS) for the same
 * reason.
 */
export const PLUGIN_INSTALL_TIMEOUT_MS = 180_000;

export const PluginsApi = {
  async list(): Promise<PluginList> {
    const { data } = await api.get<PluginListResponse>('/admin/plugins');
    return {
      plugins: data.plugins ?? [],
      dir: data.dir ?? '',
      requires_signature: data.requires_signature ?? false,
      // A server that does not report a mode gets the quiet one: the page
      // only speaks up to say the safety net is DOWN, and inventing that
      // warning would be worse than staying silent.
      conformance_mode: data.conformance ?? 'enforce',
      update_check: data.update_check ?? false,
      updates_checked_at: data.updates_checked_at ?? null,
    };
  },

  /** Read every binary plugin's source now. It installs nothing. */
  async checkUpdates(): Promise<{ report: PluginUpdateReport; plugins: Plugin[] }> {
    const { data } = await api.post<{ report: PluginUpdateReport; plugins?: Plugin[] }>(
      '/admin/plugins/updates/check',
      {},
      { timeout: PLUGIN_INSTALL_TIMEOUT_MS },
    );
    return { report: data.report, plugins: data.plugins ?? [] };
  },

  /** Install from a source: its build for this platform, held to the feed's SHA-256; the source is kept. */
  async fromSource(name: string, source: string): Promise<Plugin> {
    const { data } = await api.post<Plugin>('/admin/plugins', { name, source }, { timeout: PLUGIN_INSTALL_TIMEOUT_MS });
    return data;
  },

  /** Name (or, with '', clear) where newer versions are published. */
  async setSource(id: number, source: string): Promise<Plugin> {
    const { data } = await api.patch<Plugin>(`/admin/plugins/${id}`, { source });
    return data;
  },

  /**
   * The administrator's approval of the newer version the source has: the
   * build is held to the feed's SHA-256, then upgraded like an uploaded one
   * (a failure rolls back and answers 400 with the restored plugin).
   */
  async upgradeFromSource(id: number): Promise<Plugin> {
    const { data } = await api.post<Plugin>(
      `/admin/plugins/${id}/upgrade`,
      { from_source: true },
      { timeout: PLUGIN_INSTALL_TIMEOUT_MS },
    );
    return data;
  },

  /** Upload a plugin binary. `signature` is required when the instance trusts keys. */
  async upload(name: string, file: File, signature = '', source = ''): Promise<Plugin> {
    const form = new FormData();
    form.append('name', name);
    form.append('file', file);
    if (signature) form.append('signature', signature);
    if (source) form.append('source', source);
    const { data } = await api.post<Plugin>('/admin/plugins', form, { timeout: PLUGIN_INSTALL_TIMEOUT_MS });
    return data;
  },

  /** Download a plugin binary from a URL. sha256 is required by the server. */
  async fromUrl(name: string, url: string, sha256: string, signature = '', source = ''): Promise<Plugin> {
    const { data } = await api.post<Plugin>(
      '/admin/plugins',
      { name, url, sha256, signature, ...(source ? { source } : {}) },
      { timeout: PLUGIN_INSTALL_TIMEOUT_MS },
    );
    return data;
  },

  /**
   * Replace a binary plugin's file in place. The row, the driver name and
   * every storage built on it survive; a failed upgrade rolls back and
   * answers 400 with the restored plugin (see `rolledBackPlugin`).
   */
  async upgrade(id: number, file: File, signature = ''): Promise<Plugin> {
    const form = new FormData();
    form.append('file', file);
    if (signature) form.append('signature', signature);
    const { data } = await api.post<Plugin>(`/admin/plugins/${id}/upgrade`, form, { timeout: PLUGIN_INSTALL_TIMEOUT_MS });
    return data;
  },

  /** Register a plugin filex connects to instead of launching. */
  async remote(name: string, address: string, token: string): Promise<Plugin> {
    const { data } = await api.post<Plugin>('/admin/plugins', { name, kind: 'remote', address, token });
    return data;
  },

  async setEnabled(id: number, enabled: boolean): Promise<Plugin> {
    const { data } = await api.patch<Plugin>(`/admin/plugins/${id}`, { enabled });
    return data;
  },

  async restart(id: number): Promise<Plugin> {
    const { data } = await api.post<Plugin>(`/admin/plugins/${id}/restart`, undefined, {
      timeout: PLUGIN_INSTALL_TIMEOUT_MS,
    });
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/plugins/${id}`);
  },

  /** The plugin's log above the cursor (issue #104): its starts and failures,
   *  and the storage sync's answers it could not make sense of. The same shape
   *  as an app plugin's (PluginLogPanel reads both). */
  async logs(id: number, after = 0): Promise<PluginLogPage> {
    const { data } = await api.get<{ lines?: PluginLogLine[]; next?: number }>(`/admin/plugins/${id}/logs`, {
      params: { after },
    });
    return { lines: data.lines ?? [], next: data.next ?? after };
  },
};

/** One line of a plugin's log (backend internal/pluginlog). A line repeated
 *  comes back with the same `id`, a higher `count` and `last`. */
export interface PluginLogLine {
  /** Stays the same when the line repeats (absent from a server before 0.50). */
  id?: number;
  /** The cursor (`?after=`); it moves on when the line repeats. */
  seq: number;
  /** When it was first written. */
  ts: string;
  /** When it last repeated. */
  last?: string;
  /** How many times in a row (1 for most lines). */
  count?: number;
  level: string;
  msg: string;
}

/** A page of a plugin's log: the lines above the cursor and the next cursor. */
export interface PluginLogPage {
  lines: PluginLogLine[];
  next: number;
}
