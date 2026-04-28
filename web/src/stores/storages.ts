import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { StoragesApi } from '@/api/storages';
import type { StorageCreateRequest, StorageRef, StorageUpdateRequest } from '@/api/types';
import { extractError } from '@/api/client';

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
      error.value = extractError(e, 'Failed to load storages');
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

  async function remove(id: number): Promise<void> {
    await StoragesApi.remove(id);
    items.value = items.value.filter((s) => s.id !== id);
  }

  async function syncNow(id: number): Promise<void> {
    await StoragesApi.syncNow(id);
    // Optimistic state flip; backend will report final via /sync-runs.
    items.value = items.value.map((s) =>
      s.id === id ? { ...s, last_sync_state: 'running' as const } : s,
    );
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
    find,
  };
});
