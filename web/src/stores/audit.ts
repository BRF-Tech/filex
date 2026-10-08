import { defineStore } from 'pinia';
import { ref } from 'vue';
import { AuditApi, type AuditListParams, type AuditPage } from '@/api/audit';
import { extractError } from '@/api/client';
import { t } from '@/i18n';

const EMPTY: AuditPage = { items: [], total: 0, page: 1, page_size: 25, resources: [] };

export const useAuditStore = defineStore('audit', () => {
  const page = ref<AuditPage>(EMPTY);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function fetch(params: AuditListParams = {}): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      page.value = await AuditApi.list(params);
    } catch (e: unknown) {
      error.value = extractError(e, t('errors.loadFailed'));
    } finally {
      loading.value = false;
    }
  }

  return { page, loading, error, fetch };
});
