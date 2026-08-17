/**
 * useTokens — the one client for the self-service API-token surface.
 *
 * It goes through `useFileApi`, the same auth plumbing the explorer uses, so
 * the desktop app's function-token, the web app's cookie and an embedder's
 * bearer all work here without this file knowing which is which.
 *
 * ⚠ `/api/tokens`, NOT `/api/admin/ai-tokens`. The admin route needs an admin;
 * this one is open to every account and caps what it hands out to the caller's
 * own role and grants. Pointing this at the admin route would put the panel on
 * three surfaces and have it fail with 403 on two of them.
 *
 * ⚠ Every surface mounts THIS, not a copy — the standing rule applied to a
 * credential surface, where a divergence would mean one surface minting
 * tokens the other cannot see or revoke.
 */

import { ref, shallowRef } from 'vue';
import type { ExplorerConfig } from '../types/ExplorerConfig';
import type { ApiToken, ApiTokenCreated, ApiTokenRequest } from '../types/Tokens';
import { connectionsBase } from './useConnections';
import { useFileApi } from './useFileApi';

export function useTokens(config: ExplorerConfig) {
  const api = useFileApi(config);
  const base = connectionsBase(config);
  const url = (path: string) => `${base}${path}`;

  const tokens = shallowRef<ApiToken[]>([]);
  const loading = ref(false);
  const loaded = ref(false);
  const error = ref<string | null>(null);
  /**
   * null until the first load answers. False means the caller may not mint
   * one (anonymous, or a share/proxy session) — the guide around it is still
   * worth showing, so this is a fact rather than an error.
   */
  const canMint = ref<boolean | null>(null);

  /** The secret, held in memory for exactly as long as the user is looking. */
  const revealed = ref<ApiTokenCreated | null>(null);

  function messageOf(e: unknown): string {
    const err = e as { message?: string; detail?: string } | null;
    // The backend's own words beat a status line: "scope 'admin' is not
    // available here" tells the user what to change; "403" does not.
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
      const body = await api.jsonFetch<{ tokens: ApiToken[] }>(url('/api/tokens'));
      tokens.value = Array.isArray(body?.tokens) ? body.tokens : [];
      canMint.value = true;
    } catch (e) {
      tokens.value = [];
      const st = statusOf(e);
      canMint.value = false;
      // 401/403 is "not for you", which the panel says in its own words. Any
      // other status is a fault worth showing verbatim.
      if (st !== 401 && st !== 403) error.value = messageOf(e);
    } finally {
      loading.value = false;
      loaded.value = true;
    }
  }

  /**
   * Mint a token. The secret comes back once and is held in `revealed` until
   * the caller dismisses it — there is no second chance, so the UI must not
   * navigate away from it on its own.
   */
  async function create(req: ApiTokenRequest): Promise<ApiTokenCreated | null> {
    error.value = null;
    try {
      const body = await api.jsonFetch<ApiTokenCreated>(url('/api/tokens'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      });
      revealed.value = body;
      await load();
      return body;
    } catch (e) {
      error.value = messageOf(e);
      return null;
    }
  }

  async function remove(id: number): Promise<void> {
    error.value = null;
    try {
      await api.jsonFetch(url(`/api/tokens/${id}`), { method: 'DELETE' });
      // A revealed secret belonging to the token just revoked goes with it.
      if (revealed.value?.row?.id === id) revealed.value = null;
      await load();
    } catch (e) {
      error.value = messageOf(e);
    }
  }

  function dismiss(): void {
    revealed.value = null;
  }

  return { tokens, loading, loaded, error, canMint, revealed, load, create, remove, dismiss };
}
