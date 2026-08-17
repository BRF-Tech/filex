/**
 * useNFSExports — the one client for the NFS export surface.
 *
 * Same shape as useS3Keys and useSSHKeys, and mounted by every surface from
 * packages/core for the same reason: a credential screen with two
 * implementations eventually hands out access one of them cannot revoke.
 */

import { computed, ref, shallowRef } from 'vue';
import type { ExplorerConfig } from '../types/ExplorerConfig';
import type { NFSConnection, NFSExport, NFSExportCreated } from '../types/NFSExports';
import { connectionsBase } from './useConnections';
import { useFileApi } from './useFileApi';

export function useNFSExports(config: ExplorerConfig) {
  const api = useFileApi(config);
  const base = connectionsBase(config);
  const url = (path: string) => `${base}${path}`;

  const exports = shallowRef<NFSExport[]>([]);
  const connection = ref<NFSConnection | null>(null);
  const loading = ref(false);
  const loaded = ref(false);
  const error = ref<string | null>(null);
  const canMint = ref<boolean | null>(null);
  /** The path, held only while the user is looking at it. */
  const revealed = ref<NFSExportCreated | null>(null);

  function messageOf(e: unknown): string {
    const err = e as { message?: string; detail?: string } | null;
    if (err?.detail) {
      try {
        const parsed = JSON.parse(err.detail) as { error?: string };
        if (parsed?.error) return parsed.error;
      } catch {
        /* not JSON */
      }
    }
    return err?.message || String(e);
  }

  function statusOf(e: unknown): number | undefined {
    return (e as { status?: number } | null)?.status;
  }

  async function load(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      const body = await api.jsonFetch<{ exports: NFSExport[] } & NFSConnection>(
        url('/api/auth/nfs-exports'),
      );
      exports.value = Array.isArray(body?.exports) ? body.exports : [];
      connection.value = {
        enabled: body?.enabled !== false,
        host: body?.host ?? '',
        port: body?.port ?? 2049,
      };
      canMint.value = true;
    } catch (e) {
      exports.value = [];
      canMint.value = false;
      if (statusOf(e) !== 401 && statusOf(e) !== 403) error.value = messageOf(e);
    } finally {
      loading.value = false;
      loaded.value = true;
    }
  }

  async function create(req: {
    label: string;
    storage?: string;
    prefix?: string;
    read_only?: boolean;
    allow_cidrs?: string;
  }): Promise<NFSExportCreated | null> {
    error.value = null;
    try {
      const body = await api.jsonFetch<NFSExportCreated>(url('/api/auth/nfs-exports'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      });
      revealed.value = body;
      if (body?.host) {
        connection.value = { enabled: body.enabled !== false, host: body.host, port: body.port };
      }
      await load();
      return body;
    } catch (e) {
      error.value = messageOf(e);
      return null;
    }
  }

  async function setDisabled(id: number, disabled: boolean): Promise<void> {
    error.value = null;
    try {
      await api.jsonFetch(url(`/api/auth/nfs-exports/${id}/state`), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ disabled }),
      });
      await load();
    } catch (e) {
      error.value = messageOf(e);
    }
  }

  async function remove(id: number): Promise<void> {
    error.value = null;
    try {
      await api.jsonFetch(url(`/api/auth/nfs-exports/${id}`), { method: 'DELETE' });
      if (revealed.value?.export?.id === id) revealed.value = null;
      await load();
    } catch (e) {
      error.value = messageOf(e);
    }
  }

  function dismissPath() {
    revealed.value = null;
  }

  /** The export a guide should render a mount line for. */
  const guideExport = computed<NFSExport | null>(() => {
    if (revealed.value?.export) return revealed.value.export;
    const usable = exports.value.filter((e) => !e.disabled_at);
    if (!usable.length) return null;
    return usable.reduce((a, b) => (a.created_at >= b.created_at ? a : b));
  });

  return {
    exports,
    connection,
    loading,
    loaded,
    error,
    canMint,
    revealed,
    guideExport,
    load,
    create,
    setDisabled,
    remove,
    dismissPath,
  };
}
