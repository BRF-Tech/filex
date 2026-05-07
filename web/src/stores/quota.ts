/**
 * quota — current user's usage + limit (Snapshot from `GET /api/files/quota/me`).
 *
 * The TopNav widget polls every 60s; views that need a one-shot snapshot can
 * call `fetch()` themselves. Failure is silent (snapshot stays null) so a
 * 401/network-blip never breaks the chrome.
 */
import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { quotaApi, type QuotaSnapshot } from '@/api/quota';
import { extractError } from '@/api/client';

export const useQuotaStore = defineStore('quota', () => {
  const snapshot = ref<QuotaSnapshot | null>(null);
  const loading = ref(false);
  const error = ref<string | null>(null);
  const lastFetched = ref<number>(0);

  const used = computed(() => snapshot.value?.used_bytes ?? 0);
  const limit = computed(() => snapshot.value?.quota_bytes ?? 0);
  const unlimited = computed(() => snapshot.value?.unlimited ?? false);
  // server returns 0..100; clamp for display so a brief over-quota race
  // doesn't render a 110%-wide bar.
  const percent = computed(() => {
    const p = snapshot.value?.percent_used ?? 0;
    if (!Number.isFinite(p)) return 0;
    return Math.max(0, Math.min(100, p));
  });
  const ready = computed(() => snapshot.value !== null);

  async function fetch(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      snapshot.value = await quotaApi.me();
      lastFetched.value = Date.now();
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load quota');
    } finally {
      loading.value = false;
    }
  }

  return {
    snapshot,
    loading,
    error,
    lastFetched,
    used,
    limit,
    unlimited,
    percent,
    ready,
    fetch,
  };
});
