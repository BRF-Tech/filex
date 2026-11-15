import { api } from './client';

/**
 * #352 - a storage's filex-watch card (components/StorageWatchAgent.vue): the
 * agent that runs on the machine that owns the storage's disk and reports the
 * folders that change there (docs/FILEX-WATCH.md).
 *
 * A key is an API key of the storage page's own (the server keeps it in the
 * API key table, bound to this storage and to one door), shown ONCE - in the
 * answer that made it - and listed afterwards without it. The install
 * commands are the server's (`setup`): the page shows them as they come and
 * builds none of its own.
 */

/** What a key's agent last said about itself, since filex started. */
export interface WatchAgentStatus {
  agent: {
    id: string;
    version: string;
    host?: string;
    os?: string;
    arch?: string;
    root?: string;
  };
  seen_at: string;
  seq: number;
  reports: number;
  folders: number;
  resyncs: number;
  last_resync_at?: string;
}

export interface WatchKey {
  id: number;
  name: string;
  created_at: string;
  last_used_at?: string;
  created_by?: string;
  agent?: WatchAgentStatus;
}

/** The install commands, one per platform, built by the server. */
export interface WatchSetup {
  server: string;
  storage_id: number;
  release: string;
  image: string;
  linux: string;
  macos: string;
  windows: string;
  docker: string;
}

export interface WatchStatus {
  storage_id: number;
  storage: string;
  server: string;
  release: string;
  keys: WatchKey[];
  max_keys: number;
  setup?: WatchSetup;
}

export interface WatchKeyMade {
  /** The key itself: in this answer and never again. */
  key: string;
  row: WatchKey;
  setup: WatchSetup;
}

/** The platforms the card offers install commands for, in its order. */
export const WATCH_PLATFORMS = ['linux', 'docker', 'windows', 'macos'] as const;
export type WatchPlatform = (typeof WATCH_PLATFORMS)[number];

export const StorageWatchApi = {
  /** The card: keys, their agents, the install commands. A server or a mock
   *  that answers something else reads as "no keys yet". */
  async status(storageId: number): Promise<WatchStatus> {
    const { data } = await api.get<Partial<WatchStatus> | unknown>(`/admin/storages/${storageId}/watch`);
    const d = (data && typeof data === 'object' && !Array.isArray(data) ? data : {}) as Partial<WatchStatus>;
    return {
      storage_id: d.storage_id ?? storageId,
      storage: d.storage ?? '',
      server: d.server ?? '',
      release: d.release ?? '',
      keys: Array.isArray(d.keys) ? d.keys : [],
      max_keys: d.max_keys ?? 0,
      setup: d.setup,
    };
  },

  async createKey(storageId: number, name: string): Promise<WatchKeyMade> {
    const { data } = await api.post<WatchKeyMade>(`/admin/storages/${storageId}/watch/keys`, { name });
    return data;
  },

  async revokeKey(storageId: number, keyId: number): Promise<void> {
    await api.delete(`/admin/storages/${storageId}/watch/keys/${keyId}`);
  },
};
