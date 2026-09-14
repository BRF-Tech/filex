import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { SyncApi, type SyncRunListParams } from '@/api/sync';
import type { PaginatedResponse, SyncRun } from '@/api/types';
import { extractError } from '@/api/client';
import { useStoragesStore } from '@/stores/storages';

const EMPTY: PaginatedResponse<SyncRun> = { items: [], total: 0, page: 1, page_size: 25 };

export const useSyncStore = defineStore('sync', () => {
  const storages = useStoragesStore();
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

  /**
   * The runs, each carrying its storage's NAME.
   *
   * ⚠ The run rows the handler sends have `storage_id` and no name, so the
   * Storage column printed nothing. The name is taken from the storages store
   * at render time rather than copied in at fetch time: the two lists load in
   * parallel, and whichever lands second must not decide whether names show.
   */
  // ⚠ Not annotated `SyncRun[]`: the UI Table's rows are `Record<string, unknown>`
  // and an interface type is not assignable to an index signature; the spread
  // below is an object type, which is — the same reason `runs.items` passes.
  const items = computed(() =>
    runs.value.items.map((r) => ({
      ...r,
      storage_name: r.storage_name || storages.find(r.storage_id)?.name || `#${r.storage_id}`,
    })),
  );

  return { runs, items, loading, error, fetch };
});
