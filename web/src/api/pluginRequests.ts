import type { PluginText } from '@brftech/filex-core';

import { api } from './client';
import type { AppPluginDryRun, AppPluginPermissionReview } from './appPlugins';

// Plugin install requests (/api/admin/plugin-requests, internal/pluginreq,
// docs/APP-PLUGINS.md → Install requests).
//
// ⚠⚠ An API key (an agent, a script, the CLI) cannot install, upgrade or
// remove a plugin: it LEAVES A REQUEST, and an administrator signed in here
// approves or rejects it. What the source answered when the request was made
// is frozen on it — the manifest, the SHA-256, the permissions — and approval
// installs exactly that; a source that serves other bytes by then makes the
// request `superseded` and nothing is installed.

export type PluginRequestStatus = 'pending' | 'approved' | 'rejected' | 'expired' | 'superseded';

/** Where the plugin comes from, as the requester named it. */
export interface PluginRequestSource {
  github_repo?: string;
  ref?: string;
  url?: string;
  manifest_url?: string;
  sha256?: string;
  /** A storage plugin's feed: `owner/name` or a filex-storage.json address. */
  source?: string;
  /** An upgrade from the installed plugin's own source. */
  from_source?: boolean;
}

export interface PluginRequest {
  id: number;
  kind: 'app' | 'storage';
  op: 'install' | 'upgrade';
  /** The plugin's name (an app's manifest name; a storage plugin's). */
  name: string;
  /** An app's label, every language at once; resolve with `pluginLabelOf`. */
  label?: PluginText;
  /** An upgrade: the installed plugin it replaces. */
  plugin_id?: number;
  source_kind: 'github' | 'url' | 'source' | 'from_source' | string;
  source: PluginRequestSource;
  version: string;
  /** An upgrade: the version it replaces. */
  from_version?: string;
  /** What approval holds the bytes to (an app's module, or its manifest when it has none; a storage plugin's binary). */
  sha256: string;
  manifest_sha256?: string;
  /** The permissions it asks to grant, frozen. */
  permissions: string[];
  /** The same, with filex's label in the reader's language and the app's reason. */
  permission_rows: AppPluginPermissionReview[];
  requested_by?: number;
  /** Who asked, named as the account was when it asked. */
  requester: string;
  /** The API key it came through (absent from a signed-in session). */
  token_label?: string;
  /** The requester's own words. */
  reason: string;
  status: PluginRequestStatus;
  decided_by?: number;
  decider?: string;
  decided_at?: string;
  /** A rejection's reason, or why the request was superseded. */
  decision_note?: string;
  /** Approved: the installed plugin; pending: the last attempt's refusal ({error, at}). */
  result?: unknown;
  expires_at: string;
  created_at: string;
  /** One request's own answer only: the frozen manifest (an app's filex-app.json, a storage plugin's feed)… */
  manifest?: unknown;
  /** …and the review the dry run gave (an app's: the install review's answer). */
  review?: AppPluginDryRun | Record<string, unknown>;
}

export interface PluginRequestList {
  requests: PluginRequest[];
  /** How many days a request waits before it expires. */
  ttl_days: number;
}

/**
 * How long an approval may take: it installs — fetches the source again,
 * compiles an app's module, starts a storage plugin and probes it. The same
 * budget as the install endpoints (PLUGIN_INSTALL_TIMEOUT_MS).
 */
export const PLUGIN_REQUEST_APPROVE_TIMEOUT_MS = 180_000;

export const PluginRequestsApi = {
  async list(status: PluginRequestStatus | 'all' = 'pending'): Promise<PluginRequestList> {
    const { data } = await api.get<Partial<PluginRequestList>>('/admin/plugin-requests', { params: { status } });
    return { requests: data.requests ?? [], ttl_days: data.ttl_days ?? 14 };
  },

  async get(id: number): Promise<PluginRequest> {
    const { data } = await api.get<{ request: PluginRequest }>(`/admin/plugin-requests/${id}`);
    return data.request;
  },

  /** Install what the request froze. 409 `superseded` when the source changed. */
  async approve(id: number): Promise<PluginRequest> {
    const { data } = await api.post<{ request: PluginRequest }>(
      `/admin/plugin-requests/${id}/approve`,
      {},
      { timeout: PLUGIN_REQUEST_APPROVE_TIMEOUT_MS },
    );
    return data.request;
  },

  async reject(id: number, reason: string): Promise<PluginRequest> {
    const { data } = await api.post<{ request: PluginRequest }>(`/admin/plugin-requests/${id}/reject`, {
      reason: reason.trim(),
    });
    return data.request;
  },
};

/** The refusal an approval answered (`superseded` carries the closed request). */
export function pluginRequestError(err: unknown): { code: string; message: string; request?: PluginRequest } | null {
  const data = (err as { response?: { data?: { error?: string; message?: string; request?: PluginRequest } } })
    ?.response?.data;
  if (!data?.error) return null;
  return { code: data.error, message: data.message ?? '', request: data.request };
}
