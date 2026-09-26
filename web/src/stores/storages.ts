import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { StoragesApi, type SyncStartResponse } from '@/api/storages';
import type { StorageCreateRequest, StorageRef, StorageUpdateRequest } from '@/api/types';
import { extractError } from '@/api/client';
import { t } from '@/i18n';

/** A proxy's own answer for a request it stopped waiting on (nginx 502/504,
 *  Cloudflare 520/524), or no answer at all (the client's own time limit, a
 *  dropped connection). */
function answerNeverCame(e: unknown): boolean {
  const err = e as { response?: { status?: number }; request?: unknown; code?: string };
  if (!err || typeof err !== 'object') return false;
  if (err.response) return [502, 504, 520, 524].includes(err.response.status ?? 0);
  return err.code === 'ECONNABORTED' || err.request !== undefined;
}

export const useStoragesStore = defineStore('storages', () => {
  const items = ref<StorageRef[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);

  const count = computed(() => items.value.length);
  const empty = computed(() => items.value.length === 0);

  async function fetch(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      items.value = await StoragesApi.list();
    } catch (e: unknown) {
      error.value = extractError(e, t('errors.loadFailed'));
    } finally {
      loading.value = false;
    }
  }

  async function create(payload: StorageCreateRequest): Promise<StorageRef> {
    const created = await StoragesApi.create(payload);
    items.value = [...items.value, created];
    return created;
  }

  async function update(id: number, payload: StorageUpdateRequest): Promise<StorageRef> {
    const updated = await StoragesApi.update(id, payload);
    items.value = items.value.map((s) => (s.id === id ? updated : s));
    return updated;
  }

  /**
   * Deletes a storage: 'deleted', or 'pending' when the server is still at it.
   *
   * ⚠ A delete whose answer never came is not a failed delete. The server
   * finishes it whoever stops waiting (a large storage takes minutes, and a
   * proxy gives up at 60–100 s), so the list is read again and says which it
   * is. A refusal the server answered still throws.
   */
  async function remove(id: number): Promise<'deleted' | 'pending'> {
    try {
      await StoragesApi.remove(id);
    } catch (e: unknown) {
      if (!answerNeverCame(e)) throw e;
      try {
        await fetch();
      } catch {
        return 'pending';
      }
      return find(id) ? 'pending' : 'deleted';
    }
    items.value = items.value.filter((s) => s.id !== id);
    return 'deleted';
  }

  async function syncNow(id: number): Promise<SyncStartResponse> {
    // Optimistic state flip so the row reacts to the click; the refresh below
    // replaces it with what actually happened.
    items.value = items.value.map((s) =>
      s.id === id ? { ...s, last_sync_state: 'running' as const } : s,
    );
    try {
      return await StoragesApi.syncNow(id);
    } finally {
      // ⚠ The endpoint answers 202 as soon as the run has STARTED (it walks in
      // the background), so what the refetch shows is the run's real state —
      // running, or finished if it was quick. Without this refetch the
      // optimistic 'running' was the LAST thing the store ever wrote: the
      // storage kept reading "Never ran" until a full page reload, which is
      // exactly what issue #16 reported. Refresh on failure too, so a failed
      // request shows the storage's state instead of spinning for ever.
      await fetch();
    }
  }

  /**
   * #57 — put the storages in this order (ids, first = top; `[]` = back to
   * the default order).
   *
   * ⚠ The rows move at ONCE, before the server answers: a dragged row that
   * snapped back for a round trip and then jumped to where it was dropped
   * reads as a failed drop. The refetch afterwards replaces the optimistic
   * list with the server's (positions and all) — on failure too, so a refused
   * order shows the order that is really stored.
   */
  async function setOrder(ids: number[]): Promise<void> {
    if (ids.length) {
      const byId = new Map(items.value.map((s) => [s.id, s]));
      const placed = ids.map((id) => byId.get(id)).filter((s): s is StorageRef => !!s);
      const rest = items.value.filter((s) => !ids.includes(s.id));
      items.value = [...placed, ...rest];
    }
    try {
      await StoragesApi.setOrder(ids);
    } finally {
      await fetch();
    }
  }

  function find(id: number): StorageRef | undefined {
    return items.value.find((s) => s.id === id);
  }

  return {
    items,
    loading,
    error,
    count,
    empty,
    fetch,
    create,
    update,
    remove,
    syncNow,
    setOrder,
    find,
  };
});
