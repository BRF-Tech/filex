/**
 * useConnections — the one client for the storage-connection surface.
 *
 * It talks to the same server the explorer does, through the same auth
 * plumbing (`useFileApi` owns bearer/CSRF/basic resolution, including the
 * function-token the desktop app hands down), so there is no second place
 * that knows how to authenticate.
 *
 * Two audiences, deliberately separated:
 *
 *   • an ADMIN gets the driver descriptors and full CRUD over storages
 *     (`/api/admin/*`);
 *   • everybody else gets `visible` — the storage names the manager root
 *     already returns to them — plus their own identity, which is all the
 *     "how to connect" pages need.
 *
 * ⚠ Permission is decided by ASKING THE SERVER, never by reading a role
 * off `/api/auth/me`. An API token whose scopes exclude `admin` belongs to
 * an admin account and still gets 403 on `/api/admin/storages`; trusting
 * the role would render a form whose every submit fails.
 */

import { computed, ref, shallowRef } from 'vue';
import type { ExplorerConfig } from '../types/ExplorerConfig';
import type {
  ConnectionsUser,
  ManageDenial,
  StorageDriverDescriptor,
  StorageField,
  StorageRow,
  StorageTestResult,
  StorageWrite,
} from '../types/Connections';
import { useFileApi } from './useFileApi';

/**
 * The URL prefix the `/api/...` routes hang off.
 *
 * `apiBase: ''` is a legitimate value (the admin SPA is same-origin), so
 * only `undefined`/`null` means "not given" — a falsy check would send the
 * SPA's requests to the wrong place. Legacy embedders configure `endpoint`
 * instead of `apiBase`; the prefix is recovered from it so they are not
 * excluded.
 */
export function connectionsBase(config: ExplorerConfig): string {
  if (config.apiBase != null) return config.apiBase.replace(/\/+$/, '');
  const m = config.endpoint ?? '';
  const cut = m.indexOf('/api/');
  return cut >= 0 ? m.slice(0, cut) : '';
}

/**
 * The origin a client program should be pointed at.
 *
 * The instruction pages are only worth anything if they name the real
 * deployment, so this resolves to an absolute origin: the configured
 * apiBase when there is one (the desktop app, embeds), otherwise the page's
 * own origin (the admin SPA, which is served by the same binary).
 */
export function connectionsOrigin(config: ExplorerConfig): string {
  const base = connectionsBase(config);
  if (/^https?:\/\//i.test(base)) {
    try {
      return new URL(base).origin;
    } catch {
      return base;
    }
  }
  if (typeof window !== 'undefined' && window.location) return window.location.origin;
  return base;
}

export function useConnections(config: ExplorerConfig) {
  const api = useFileApi(config);
  const base = connectionsBase(config);
  const url = (path: string) => `${base}${path}`;

  const drivers = shallowRef<StorageDriverDescriptor[]>([]);
  const storages = shallowRef<StorageRow[]>([]);
  /** Storage names a non-admin may see (manager root). */
  const visible = shallowRef<string[]>([]);
  const me = ref<ConnectionsUser | null>(null);

  const loading = ref(false);
  const loaded = ref(false);
  const error = ref<string | null>(null);
  /** null until the first load decides; then true, or a reason it is false. */
  const canManage = ref<boolean | null>(null);
  const denial = ref<ManageDenial | null>(null);

  function statusOf(e: unknown): number | undefined {
    return (e as { status?: number } | null)?.status;
  }

  function messageOf(e: unknown): string {
    const err = e as { message?: string } | null;
    return err?.message || String(e);
  }

  /** The full picture, in as few round-trips as the permission allows. */
  async function load(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      // Identity first: the guides need the caller's own e-mail (it IS the
      // WebDAV username), and it is the one call every role may make.
      try {
        const body = await api.jsonFetch<{ user: ConnectionsUser }>(url('/api/auth/me'));
        me.value = body?.user ?? null;
      } catch (e) {
        if (statusOf(e) === 401) {
          canManage.value = false;
          denial.value = 'anonymous';
        }
        me.value = null;
      }

      try {
        drivers.value = await api.jsonFetch<StorageDriverDescriptor[]>(
          url('/api/admin/storage-drivers'),
        );
        canManage.value = true;
        denial.value = null;
      } catch (e) {
        drivers.value = [];
        canManage.value = false;
        // 401/403 is a permission answer, not a fault: a viewer is meant to
        // land on the guides. Anything else is a real failure and is shown.
        const st = statusOf(e);
        if (st === 403) denial.value = 'none';
        else if (st === 401) denial.value = 'anonymous';
        else {
          denial.value = 'unreachable';
          error.value = messageOf(e);
        }
      }

      if (canManage.value) {
        try {
          const rows = await api.jsonFetch<StorageRow[]>(url('/api/admin/storages'));
          storages.value = Array.isArray(rows) ? rows : [];
          visible.value = storages.value.map((s) => s.name);
        } catch (e) {
          error.value = messageOf(e);
        }
      } else {
        // What a non-admin may see, from the endpoint they already use.
        try {
          const root = await api.index('');
          visible.value = Array.isArray(root?.storages) ? root.storages : [];
        } catch {
          visible.value = [];
        }
      }
      loaded.value = true;
    } finally {
      loading.value = false;
    }
  }

  async function createStorage(body: StorageWrite): Promise<StorageRow> {
    const created = await api.jsonFetch<StorageRow>(url('/api/admin/storages'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    await load();
    return created;
  }

  async function updateStorage(id: number, body: Partial<StorageWrite>): Promise<StorageRow> {
    const updated = await api.jsonFetch<StorageRow>(url(`/api/admin/storages/${id}`), {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    await load();
    return updated;
  }

  async function deleteStorage(id: number): Promise<void> {
    await api.jsonFetch(url(`/api/admin/storages/${id}`), { method: 'DELETE' });
    await load();
  }

  /**
   * Try a driver config without saving it.
   *
   * The endpoint answers 200 with `{ok:false,error}` for a connection that
   * did not work — a failed *test* is a successful *request* — so a thrown
   * error here means the call itself failed and is reported as such.
   */
  async function testStorage(body: {
    driver: string;
    config: Record<string, unknown>;
  }): Promise<StorageTestResult> {
    try {
      return await api.jsonFetch<StorageTestResult>(url('/api/admin/storages/test'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
    } catch (e) {
      return { ok: false, error: messageOf(e) };
    }
  }

  function descriptor(driver: string): StorageDriverDescriptor | undefined {
    return drivers.value.find((d) => d.driver === driver);
  }

  function fields(driver: string): StorageField[] {
    return descriptor(driver)?.fields ?? [];
  }

  /** The config a fresh form starts from: every declared default, nothing else. */
  function defaults(driver: string): Record<string, unknown> {
    const out: Record<string, unknown> = {};
    for (const f of fields(driver)) {
      if (f.default !== undefined && f.default !== null) out[f.key] = f.default;
    }
    return out;
  }

  /**
   * Required fields the config has not filled — checked HERE as well as on
   * the server so the user is told before a round-trip, and with the same
   * alias rules (`base_path` still counts as `root`).
   */
  function missingRequired(driver: string, cfg: Record<string, unknown>): StorageField[] {
    return fields(driver).filter((f) => {
      if (!f.required || f.default !== undefined) return false;
      for (const k of [f.key, ...(f.aliases ?? [])]) {
        const v = cfg[k];
        if (v === undefined || v === null) continue;
        if (typeof v === 'string' && v.trim() === '') continue;
        return false;
      }
      return true;
    });
  }

  const driverNames = computed(() => drivers.value.map((d) => d.driver));

  return {
    // state
    drivers,
    driverNames,
    storages,
    visible,
    me,
    loading,
    loaded,
    error,
    canManage,
    denial,
    // actions
    load,
    createStorage,
    updateStorage,
    deleteStorage,
    testStorage,
    // descriptor helpers
    descriptor,
    fields,
    defaults,
    missingRequired,
  };
}

export type ConnectionsApi = ReturnType<typeof useConnections>;
