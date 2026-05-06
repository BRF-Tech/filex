import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { QueueApi } from '@/api/queue';
import type { QueueOp, QueueOpStatus, QueueStats } from '@/api/types';
import { extractError } from '@/api/client';

export const useQueueStore = defineStore('queue', () => {
  const stats = ref<QueueStats>({ pending: 0, running: 0, failed: 0, done_24h: 0, cancelled: 0 });
  const items = ref<QueueOp[]>([]);
  const total = ref(0);
  const limit = ref(50);
  const offset = ref(0);
  const filter = ref<QueueOpStatus | ''>('');
  const loading = ref(false);
  const error = ref<string | null>(null);

  const totalPages = computed(() => Math.max(1, Math.ceil(total.value / limit.value)));
  const currentPage = computed(() => Math.floor(offset.value / limit.value) + 1);

  async function fetchStats(): Promise<void> {
    try {
      stats.value = await QueueApi.stats();
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load queue stats');
    }
  }

  async function fetchList(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      const res = await QueueApi.list({
        status: filter.value || undefined,
        limit: limit.value,
        offset: offset.value,
      });
      items.value = res.items ?? [];
      total.value = res.total;
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load queue');
    } finally {
      loading.value = false;
    }
  }

  async function refresh(): Promise<void> {
    await Promise.all([fetchStats(), fetchList()]);
  }

  async function retry(id: string): Promise<void> {
    await QueueApi.retry(id);
    await refresh();
  }

  async function cancel(id: string): Promise<void> {
    await QueueApi.cancel(id);
    await refresh();
  }

  function setStatus(s: QueueOpStatus | ''): void {
    filter.value = s;
    offset.value = 0;
  }

  function setPage(p: number): void {
    offset.value = Math.max(0, (p - 1) * limit.value);
  }

  return {
    stats,
    items,
    total,
    limit,
    offset,
    filter,
    loading,
    error,
    totalPages,
    currentPage,
    fetchStats,
    fetchList,
    refresh,
    retry,
    cancel,
    setStatus,
    setPage,
  };
});
