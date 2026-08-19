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
}

/** Runtime states the server reports. */
export type PluginState = 'disabled' | 'starting' | 'running' | 'failed' | 'refused';

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
}

export interface PluginList {
  plugins: Plugin[];
  /** Where binary plugins live on the server, shown in the empty state. */
  dir: string;
}

export const PluginsApi = {
  async list(): Promise<PluginList> {
    const { data } = await api.get<PluginList>('/admin/plugins');
    return { plugins: data.plugins ?? [], dir: data.dir ?? '' };
  },

  /** Upload a plugin binary. */
  async upload(name: string, file: File): Promise<Plugin> {
    const form = new FormData();
    form.append('name', name);
    form.append('file', file);
    const { data } = await api.post<Plugin>('/admin/plugins', form);
    return data;
  },

  /** Download a plugin binary from a URL. sha256 is required by the server. */
  async fromUrl(name: string, url: string, sha256: string): Promise<Plugin> {
    const { data } = await api.post<Plugin>('/admin/plugins', { name, url, sha256 });
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
    const { data } = await api.post<Plugin>(`/admin/plugins/${id}/restart`);
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/plugins/${id}`);
  },
};
