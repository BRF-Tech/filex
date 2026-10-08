import axios from 'axios';

import type { PluginText } from '@brftech/filex-core';

import { api } from './client';
import { getApiBaseUrl } from './runtimeConfig';
import { INSTALL_TIMEOUT_MS, type AppPlugin, type AppPluginDryRun, type AppPluginPlacement } from './appPlugins';
import type { PluginRequest } from './pluginRequests';

// Installing an app from a store's install link, the stores this filex trusts,
// and the licenses of paid apps (/api/admin/app-plugins/stores, …/store-intent,
// …/licenses, …/{id}/license; backend handlers/app_store_intent.go,
// internal/appstore; docs/APP-PLUGINS.md → Installing from a store).
//
// ⚠ Every route here needs an administrator signed in to the panel: an API
// key of any kind is refused (session_required). A license key never comes
// back from any of them - its prefix does.

const BASE = '/admin/app-plugins';

/** A store key as the trust question shows it. */
export interface StoreKey {
  id: string;
  use: 'index' | 'license' | string;
  status?: string;
  /** Lower-hex sha256 of the 32 raw key bytes. */
  fingerprint: string;
}

/** A trusted store. `config`: FILEX_APP_STORE_URLS, not the panel's to remove. */
export interface TrustedStore {
  origin: string;
  source: 'admin' | 'config';
  keys: StoreKey[];
  approved_by_name?: string;
  approved_at?: string;
}

/** What a store link stands for, as the review shows it (never the key). */
export interface StoreIntentView {
  store: string;
  token_id: string;
  app: string;
  kind: string;
  version: string;
  repo: string;
  ref: string;
  commit?: string;
  paid: boolean;
  license_key_prefix?: string;
  expires_at: string;
}

/**
 * Where an installed app came from, or where a link would take it: the store
 * ("" = none - a repository installed directly, an upload or an address),
 * the GitHub repository it follows ("" = not one), its version.
 */
export interface StoreSourceRef {
  store: string;
  repo: string;
  version: string;
  source_url?: string;
}

/** A link that passed every check: its review, and the handle to install it. */
export interface StoreReview {
  handle: string;
  store: string;
  store_trust: 'admin' | 'config' | '';
  intent: StoreIntentView;
  review: AppPluginDryRun;
  /** The installed app this link upgrades, and where it came from. */
  upgrade_of?: StoreSourceRef & { id: number };
}

/** A license as the panel shows it. */
export interface AppLicense {
  app: string;
  required: boolean;
  status: string;
  held: boolean;
  store?: string;
  key_prefix?: string;
  licensee?: string;
  seats?: number;
  seats_used?: number;
  valid_until?: string;
  updates_until?: string;
  checked_at?: string;
  next_check_by?: string;
  grace_until?: string;
  last_attempt_at?: string;
  last_error_code?: string;
  last_error?: string;
  store_trusted: boolean;
}

/** One pin the repository does not keep. */
export interface StorePinMismatch {
  field: string;
  link: string;
  source: string;
}

/** A store refusal: its code, its sentence, and what to draw. */
export interface StoreRefusal {
  error: string;
  message?: string;
  detail?: {
    store?: string;
    keys?: StoreKey[];
    previous_keys?: StoreKey[];
    fingerprints?: string[];
    mismatches?: StorePinMismatch[];
    /** intent_version_rollback: the versions; store_source_changed: the sources. */
    installed?: string | StoreSourceRef;
    link?: string | StoreSourceRef;
    /** intent_wrong_instance: the filex the link was made for, and this one -
     *  or, FILEX_PUBLIC_URL being unusable, `public_url_invalid`. */
    filex_origin?: string;
    this_filex?: string;
    public_url_invalid?: boolean;
  };
}

/** The refusal an axios error carries, if it is one. */
export function storeRefusal(err: unknown): StoreRefusal | null {
  if (!axios.isAxiosError(err)) return null;
  const d = err.response?.data as StoreRefusal | undefined;
  return d && typeof d.error === 'string' ? d : null;
}

/** The codes the trust question answers. */
export const TRUST_CODES = ['store_trust_required', 'store_key_changed'];

export const AppStoreApi = {
  async stores(): Promise<TrustedStore[]> {
    const { data } = await api.get<{ stores: TrustedStore[] }>(`${BASE}/stores`);
    return data.stores ?? [];
  },

  /** Trust a store with the keys the administrator was shown. */
  async trust(store: string, fingerprints: string[]): Promise<TrustedStore> {
    const { data } = await api.post<TrustedStore>(`${BASE}/stores`, { store, fingerprints });
    return data;
  },

  async untrust(store: string): Promise<void> {
    await api.delete(`${BASE}/stores`, { params: { store } });
  },

  /** Read an install link: its review, or the trust question (409). */
  async intent(store: string, token: string): Promise<StoreReview> {
    const { data } = await api.post<StoreReview>(`${BASE}/store-intent`, { store, token }, { timeout: INSTALL_TIMEOUT_MS });
    return data;
  },

  /** Install what a reviewed link names. `licenseKey` empty = the link's own. */
  async install(
    handle: string,
    permissions: string[],
    associations: AppPluginPlacement[] = [],
    licenseKey = '',
  ): Promise<{ plugin: AppPlugin; license?: AppLicense; association_errors?: string[] }> {
    const body: Record<string, unknown> = { handle, permissions };
    if (associations.length) body.associations = associations;
    if (licenseKey) body.license_key = licenseKey;
    const { data } = await api.post(`${BASE}/store-intent/install`, body, { timeout: INSTALL_TIMEOUT_MS });
    return data;
  },

  /** The administrator closed the review: the store is told, the link is used up. */
  async cancel(handle: string): Promise<void> {
    await api.post(`${BASE}/store-intent/cancel`, { handle });
  },

  async licenses(): Promise<AppLicense[]> {
    const { data } = await api.get<{ licenses: AppLicense[] }>(`${BASE}/licenses`);
    return data.licenses ?? [];
  },

  async license(id: number): Promise<AppLicense> {
    const { data } = await api.get<AppLicense>(`${BASE}/${id}/license`);
    return data;
  },

  async setLicenseKey(id: number, key: string): Promise<AppLicense> {
    const { data } = await api.put<AppLicense>(`${BASE}/${id}/license`, { key });
    return data;
  },

  async verifyLicense(id: number): Promise<AppLicense> {
    const { data } = await api.post<AppLicense>(`${BASE}/${id}/license/verify`, {});
    return data;
  },

  // ── The embedded store (#162) ─────────────────────────────────────────

  /** Whether this filex is connected to a store (its one-time code). */
  async connection(store: string): Promise<StoreConnection> {
    const { data } = await api.get<StoreConnection>(`${BASE}/stores/connection`, { params: { store } });
    return data;
  },

  /** Connect with the code the store's "My instances" page made. */
  async connect(store: string, code: string): Promise<StoreConnection> {
    const { data } = await api.post<StoreConnection>(`${BASE}/stores/connection`, { store, code });
    return data;
  },

  async disconnect(store: string): Promise<void> {
    await api.delete(`${BASE}/stores/connection`, { params: { store } });
  },

  /** Who sees the store screen in a scope (`tenant`: multi-tenant mode). */
  async view(tenant?: number): Promise<StoreViewAnswer> {
    const { data } = await api.get<StoreViewAnswer>(`${BASE}/store-view`, { params: tenant ? { tenant } : {} });
    return data;
  },

  async saveView(settings: StoreViewSettings, tenant?: number): Promise<StoreViewAnswer> {
    const body: Record<string, unknown> = { settings };
    if (tenant) body.tenant = String(tenant);
    const { data } = await api.put<StoreViewAnswer>(`${BASE}/store-view`, body);
    return data;
  },
};

// ── The embedded store (#162) ────────────────────────────────────────────

/** Whether filex holds a key for a store (never the key). */
export interface StoreConnection {
  store: string;
  connected: boolean;
  instance_id?: string;
  key_fingerprint?: string;
  connected_at?: string;
  connected_by_name?: string;
}

/** Who sees the store screen: everyone, some built-in roles, some groups. */
export type StoreAudience = 'everyone' | 'roles' | 'groups';

export interface StoreViewSettings {
  enabled: boolean;
  /** The trusted stores the screen shows. */
  stores: string[];
  audience: StoreAudience;
  /** Built-in roles: admin, user, viewer. */
  roles: string[];
  /** Group ids. */
  groups: number[];
  updated_at?: string;
  updated_by_name?: string;
}

export interface StoreViewAnswer {
  multi_tenant: boolean;
  /** The tenant the settings are for (multi-tenant mode). */
  tenant: number;
  settings: StoreViewSettings;
  /** The trusted stores, and whether this filex is connected to each. */
  stores?: Array<{ origin: string; source: string; connected: boolean }>;
}

/** An app of a store's catalog, as filex verified it. */
export interface CatalogApp {
  name: string;
  kind: 'app' | 'language_pack' | string;
  label: PluginText;
  summary?: PluginText;
  publisher: string;
  publisher_verified?: boolean;
  publisher_official?: boolean;
  categories: string[];
  repo: string;
  version: string;
  published_at?: string;
  filex_range: string;
  permissions: string[];
  /** An icon's file name, served through filex (StoreScreenApi.iconUrl). */
  icon?: string;
  /** The version installed here, when it is. */
  installed_version?: string;
}

export interface StoreCatalog {
  store: string;
  serial: number;
  fetched_at: string;
  /** The store could not be reached: the last catalog that verified. */
  stale: boolean;
  apps: CatalogApp[];
}

/**
 * A person's store screen (/api/app-store): what a person sees and asks for.
 * ⚠ Nothing here installs: a request lands on the Install requests list and
 * an administrator decides.
 */
export const StoreScreenApi = {
  async status(): Promise<{ visible: boolean; stores: string[] }> {
    const { data } = await api.get<{ visible: boolean; stores: string[] }>('/app-store');
    return { visible: data.visible === true, stores: data.stores ?? [] };
  },

  async catalog(store: string): Promise<StoreCatalog> {
    const { data } = await api.get<StoreCatalog>('/app-store/catalog', { params: { store } });
    return { ...data, apps: data.apps ?? [] };
  },

  /** An icon's address: through filex, never the store's own. */
  iconUrl(store: string, file: string): string {
    const q = new URLSearchParams({ store, file });
    return `${getApiBaseUrl()}/app-store/media?${q.toString()}`;
  },

  async requests(): Promise<PluginRequest[]> {
    const { data } = await api.get<{ requests: PluginRequest[] }>('/app-store/requests');
    return data.requests ?? [];
  },

  /** Ask for an app: 201 a new request, 200 the one already waiting. */
  async request(store: string, app: string, reason: string): Promise<{ request: PluginRequest; created: boolean }> {
    const { data } = await api.post<{ request: PluginRequest; created: boolean }>('/app-store/requests', { store, app, reason });
    return data;
  },
};

/** A fingerprint as a person compares it: groups of four, the first 32 digits. */
export function groupedFingerprint(f: string): string {
  return (f.slice(0, 32).match(/.{1,4}/g) ?? []).join(' ');
}
