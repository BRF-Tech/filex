import { defineStore } from 'pinia';
import { ref } from 'vue';
import { ExternalApi, type ExternalServiceUpdate } from '@/api/external';
import type { ExternalService } from '@/api/types';
import { extractError } from '@/api/client';

export const useExternalServicesStore = defineStore('external-services', () => {
  const items = ref<ExternalService[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function fetch(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      items.value = await ExternalApi.list();
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load external services');
    } finally {
      loading.value = false;
    }
  }

  async function update(id: ExternalService['id'], patch: ExternalServiceUpdate): Promise<void> {
    const updated = await ExternalApi.update(id, patch);
    items.value = items.value.map((s) => (s.id === id ? updated : s));
  }

  async function test(id: ExternalService['id']): Promise<ExternalService> {
    const updated = await ExternalApi.test(id);
    items.value = items.value.map((s) => (s.id === id ? updated : s));
    return updated;
  }

  return { items, loading, error, fetch, update, test };
});
