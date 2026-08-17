/**
 * useS3Keys — the one client for the S3 access-key surface.
 *
 * It goes through `useFileApi`, the same auth plumbing the explorer uses, so
 * the desktop app's function-token, the web app's cookie and an embedder's
 * bearer all work here without this file knowing which is which.
 *
 * ⚠ Every surface mounts THIS, not a copy. The keys panel, the guide and the
 * copy buttons exist once in `packages/core`; the web app and the desktop app
 * render the same component. That is the standing rule ("never write
 * surface-specific behaviour") applied to a credential surface, where a
 * divergence would mean one surface handing out keys the other cannot revoke.
 */

import { computed, ref, shallowRef } from 'vue';
import type { ExplorerConfig } from '../types/ExplorerConfig';
import type { S3AccessKey, S3Connection, S3KeyCreated, S3KeyRequest } from '../types/S3Keys';
import { connectionsBase } from './useConnections';
import { useFileApi } from './useFileApi';

export function useS3Keys(config: ExplorerConfig) {
  const api = useFileApi(config);
  const base = connectionsBase(config);
  const url = (path: string) => `${base}${path}`;

  const keys = shallowRef<S3AccessKey[]>([]);
  const connection = ref<S3Connection | null>(null);
  const loading = ref(false);
  const loaded = ref(false);
  const error = ref<string | null>(null);
  /**
   * null until the first load answers. False means the caller may not mint
   * keys (anonymous, or a token without the right to) — the guide is still
   * worth showing, so this is a fact rather than an error.
   */
  const canMint = ref<boolean | null>(null);

  /** The secret, held in memory for exactly as long as the user is looking. */
  const revealed = ref<S3KeyCreated | null>(null);

  function messageOf(e: unknown): string {
    const err = e as { message?: string; detail?: string } | null;
    // The backend's own words beat a status line: "access keys are not
    // available on this install" tells an operator what to fix; "503" does not.
    if (err?.detail) {
      try {
        const parsed = JSON.parse(err.detail) as { error?: string };
        if (parsed?.error) return parsed.error;
      } catch {
        /* not JSON — fall through */
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
      const body = await api.jsonFetch<{ keys: S3AccessKey[] } & S3Connection>(
        url('/api/auth/s3-keys'),
      );
      keys.value = Array.isArray(body?.keys) ? body.keys : [];
      connection.value = {
        endpoint: body?.endpoint ?? '',
        enabled: body?.enabled !== false,
        path_style: body?.path_style !== false,
      };
      canMint.value = true;
    } catch (e) {
      keys.value = [];
      const st = statusOf(e);
      if (st === 401 || st === 403) {
        canMint.value = false;
      } else {
        canMint.value = false;
        error.value = messageOf(e);
      }
    } finally {
      loading.value = false;
      loaded.value = true;
    }
  }

  /**
   * Mint a key. The secret comes back once and is held in `revealed` until
   * the caller dismisses it — there is no second chance, so the UI must not
   * navigate away from it on its own.
   */
  async function create(req: S3KeyRequest): Promise<S3KeyCreated | null> {
    error.value = null;
    try {
      const body = await api.jsonFetch<S3KeyCreated>(url('/api/auth/s3-keys'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      });
      revealed.value = body;
      if (body?.endpoint) {
        connection.value = {
          endpoint: body.endpoint,
          enabled: body.enabled !== false,
          path_style: body.path_style !== false,
        };
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
      await api.jsonFetch(url(`/api/auth/s3-keys/${id}/state`), {
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
      await api.jsonFetch(url(`/api/auth/s3-keys/${id}`), { method: 'DELETE' });
      // A revealed secret belonging to the key just revoked must go with it.
      if (revealed.value?.key?.id === id) revealed.value = null;
      await load();
    } catch (e) {
      error.value = messageOf(e);
    }
  }

  function dismissSecret() {
    revealed.value = null;
  }

  /**
   * The key a guide should be rendered with: the one just minted, else the
   * newest usable one. A guide showing a DISABLED key's id would produce a
   * paste that authenticates as nothing.
   */
  const guideKey = computed<S3AccessKey | null>(() => {
    if (revealed.value?.key) return revealed.value.key;
    const usable = keys.value.filter((k) => !k.disabled_at);
    if (!usable.length) return null;
    return usable.reduce((a, b) => (a.created_at >= b.created_at ? a : b));
  });

  return {
    keys,
    connection,
    loading,
    loaded,
    error,
    canMint,
    revealed,
    guideKey,
    load,
    create,
    setDisabled,
    remove,
    dismissSecret,
  };
}
