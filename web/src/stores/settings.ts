import { defineStore } from 'pinia';
import { ref } from 'vue';
import { SettingsApi } from '@/api/settings';
import type { SettingsMap } from '@/api/types';
import { extractError } from '@/api/client';
import { t } from '@/i18n';

export const useSettingsStore = defineStore('settings', () => {
  const data = ref<SettingsMap>({});
  const loading = ref(false);
  const saving = ref(false);
  const error = ref<string | null>(null);

  async function fetch(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      data.value = await SettingsApi.get();
    } catch (e: unknown) {
      error.value = extractError(e, t('errors.loadFailed'));
    } finally {
      loading.value = false;
    }
  }

  async function update(patch: Partial<SettingsMap>): Promise<void> {
    saving.value = true;
    error.value = null;
    try {
      data.value = await SettingsApi.update(patch);
    } catch (e: unknown) {
      error.value = extractError(e, t('errors.saveFailed'));
      throw e;
    } finally {
      saving.value = false;
    }
  }

  return { data, loading, saving, error, fetch, update };
});
