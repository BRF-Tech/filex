/**
 * useSSHKeys — the one client for the SSH key surface.
 *
 * Same shape as useS3Keys and for the same reason: the panel, the guide and
 * the buttons exist once in `packages/core`, and the desktop app and the web
 * app mount the same component. A credential surface with two implementations
 * eventually hands out access one of them cannot take back.
 *
 * ⚠ There is no secret to reveal here. The user pastes the PUBLIC half; the
 * private key never touches filex, which is the entire point of preferring
 * keys over a password on a file server.
 */

import { computed, ref, shallowRef } from 'vue';
import type { ExplorerConfig } from '../types/ExplorerConfig';
import type { SSHConnection, SSHPublicKey } from '../types/SSHKeys';
import { connectionsBase } from './useConnections';
import { useFileApi } from './useFileApi';

export function useSSHKeys(config: ExplorerConfig) {
  const api = useFileApi(config);
  const base = connectionsBase(config);
  const url = (path: string) => `${base}${path}`;

  const keys = shallowRef<SSHPublicKey[]>([]);
  const connection = ref<SSHConnection | null>(null);
  const loading = ref(false);
  const loaded = ref(false);
  const error = ref<string | null>(null);
  const canAdd = ref<boolean | null>(null);

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
      const body = await api.jsonFetch<{ keys: SSHPublicKey[] } & SSHConnection>(
        url('/api/auth/ssh-keys'),
      );
      keys.value = Array.isArray(body?.keys) ? body.keys : [];
      connection.value = {
        enabled: body?.enabled !== false,
        host: body?.host ?? '',
        port: body?.port ?? 2022,
        login: body?.login ?? '',
        ftps: body?.ftps,
      };
      canAdd.value = true;
    } catch (e) {
      keys.value = [];
      canAdd.value = false;
      if (statusOf(e) !== 401 && statusOf(e) !== 403) error.value = messageOf(e);
    } finally {
      loading.value = false;
      loaded.value = true;
    }
  }

  async function add(key: string, name?: string): Promise<boolean> {
    error.value = null;
    try {
      await api.jsonFetch(url('/api/auth/ssh-keys'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key, name }),
      });
      await load();
      return true;
    } catch (e) {
      error.value = messageOf(e);
      return false;
    }
  }

  async function setDisabled(id: number, disabled: boolean): Promise<void> {
    error.value = null;
    try {
      await api.jsonFetch(url(`/api/auth/ssh-keys/${id}/state`), {
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
      await api.jsonFetch(url(`/api/auth/ssh-keys/${id}`), { method: 'DELETE' });
      await load();
    } catch (e) {
      error.value = messageOf(e);
    }
  }

  /** True when at least one key can actually be used to sign in. */
  const hasUsableKey = computed(() => keys.value.some((k) => !k.disabled_at));

  return { keys, connection, loading, loaded, error, canAdd, hasUsableKey, load, add, setDisabled, remove };
}
