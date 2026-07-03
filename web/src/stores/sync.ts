import { defineStore } from 'pinia';
import { ref } from 'vue';
import { SyncApi, type SyncRunListParams } from '@/api/sync';
import type { PaginatedResponse, SyncRun } from '@/api/types';
import { extractError } from '@/api/client';

const EMPTY: PaginatedResponse<SyncRun> = { items: [], total: 0, page: 1, page_size: 25 };

export const useSyncStore = defineStore('sync', () => {
  const runs = ref<PaginatedResponse<SyncRun>>(EMPTY);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function fetch(params: SyncRunListParams = {}): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      runs.value = await SyncApi.list(params);
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load sync runs');
    } finally {
      loading.value = false;
    }
  }

  return { runs, loading, error, fetch };
});
