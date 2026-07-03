import { defineStore } from 'pinia';
import { ref } from 'vue';
import { SettingsApi } from '@/api/settings';
import type { SettingsMap } from '@/api/types';
import { extractError } from '@/api/client';

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
      error.value = extractError(e, 'Failed to load settings');
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
      error.value = extractError(e, 'Failed to save settings');
      throw e;
    } finally {
      saving.value = false;
    }
  }

  return { data, loading, saving, error, fetch, update };
});
