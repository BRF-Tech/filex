import { defineStore } from 'pinia';
import { ref } from 'vue';
import { AuditApi, type AuditListParams } from '@/api/audit';
import type { AuditEntry, PaginatedResponse } from '@/api/types';
import { extractError } from '@/api/client';

const EMPTY: PaginatedResponse<AuditEntry> = { items: [], total: 0, page: 1, page_size: 25 };

export const useAuditStore = defineStore('audit', () => {
  const page = ref<PaginatedResponse<AuditEntry>>(EMPTY);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function fetch(params: AuditListParams = {}): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      page.value = await AuditApi.list(params);
    } catch (e: unknown) {
      error.value = extractError(e, 'Failed to load audit log');
    } finally {
      loading.value = false;
    }
  }

  return { page, loading, error, fetch };
});
