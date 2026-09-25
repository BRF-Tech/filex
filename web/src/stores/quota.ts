/**
 * quota — the storage line of the top bar: the current user's usage + limit
 * (Snapshot from `GET /api/files/quota/me`) and, for a person without a
 * limit, how full the drives they can open are (`GET /api/files/quota/storages`).
 *
 * WHICH of the two the chip prints is core `storageLine`'s decision — the
 * rule the explorer panel's line follows. Without a quota the person's own
 * figure is only their upload counter (every file a sync discovered counts for
 * nobody), which put "523.5 MB" above a dashboard whose drive held 245.3 GB
 * (production, 2026-09-25).
 *
 * ⚠ One rule, not one input. The chip sums the endpoint's rows, which are the
 * ENABLED drives the person may open; an administrator's explorer panel lists
 * the admin storage list, disabled drives included, and so does the
 * dashboard's total. And the endpoint leaves out a drive whose count failed:
 * the panel, holding the names, draws its total as "at least", but the chip has
 * no list to miss the drive from.
 *
 * The TopNav widget polls every 60s; views that need a one-shot snapshot can
 * call `fetch()` themselves. Failure is silent (the last answer stays) so a
 * 401/network-blip never breaks the chrome.
 */
import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { needsMeasuredDrives, storageLine, type MeasuredDrive } from '@brftech/filex-core';
import { quotaApi, type QuotaSnapshot } from '@/api/quota';
import { extractError } from '@/api/client';
import { t } from '@/i18n';

export const useQuotaStore = defineStore('quota', () => {
  const snapshot = ref<QuotaSnapshot | null>(null);
  const drives = ref<MeasuredDrive[] | null>(null);
  const loading = ref(false);
  const error = ref<string | null>(null);
  const lastFetched = ref<number>(0);

  /** What the chip says; null = nothing trustworthy to say. */
  const line = computed(() => storageLine(snapshot.value, [], drives.value));
  // ⚠ All three from `line`, so the chip draws what the rule decided: a
  // snapshot of `quota_bytes: 0, unlimited: false` is still no ceiling.
  const used = computed(() => line.value?.used ?? 0);
  const limit = computed(() => line.value?.total ?? 0);
  const unlimited = computed(() => line.value?.unlimited ?? false);
  /** `used` is a lower bound — a drive's catalogue does not cover it yet. */
  const partial = computed(() => line.value?.partial ?? false);
  // server returns 0..100; clamp for display so a brief over-quota race
  // doesn't render a 110%-wide bar.
  const percent = computed(() => {
    const p = snapshot.value?.percent_used ?? 0;
    if (!Number.isFinite(p)) return 0;
    return Math.max(0, Math.min(100, p));
  });
  const ready = computed(() => line.value !== null);

  async function fetch(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      const snap = await quotaApi.me();
      // The drives are asked only without a ceiling — with one, the person's
      // share of it is the whole answer. ⚠ A failed read keeps the last
      // answer, as a failed `me()` keeps the last snapshot: a poll that
      // blinks must not take the chip away with it.
      drives.value = needsMeasuredDrives(snap, [])
        ? await quotaApi.storages().catch(() => drives.value)
        : null;
      snapshot.value = snap;
      lastFetched.value = Date.now();
    } catch (e: unknown) {
      error.value = extractError(e, t('errors.loadFailed'));
    } finally {
      loading.value = false;
    }
  }

  return {
    snapshot,
    drives,
    loading,
    error,
    lastFetched,
    line,
    used,
    limit,
    unlimited,
    partial,
    percent,
    ready,
    fetch,
  };
});
