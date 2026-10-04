<script setup lang="ts">
/**
 * EncryptionTenantsPanel — the platform operator's ceiling over every tenant
 * (`providers.e2e_allowed`; backend internal/e2epolicy asks it first). Off,
 * nobody in that tenant — its administrators included — can start encrypting,
 * and the explorer stops offering it there. What is already encrypted keeps
 * working: it opens, takes new files and can be decrypted.
 *
 * The switch lives on the page of what it caps, beside each tenant's own
 * policy (read only here: the policy is the tenant administrator's), rather
 * than on Admin → Tenants: whoever decides about encryption finds every layer
 * of it in one place. Encryption.vue draws this for the platform operator
 * only; the server refuses everybody else (`requireSupertenant`), and a 403
 * here draws nothing.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Building2 } from 'lucide-vue-next';
import { DataTable, type DataColumn } from '@brftech/filex-core';

import { extractError } from '@/api/client';
import { E2EPolicyApi, type E2ETenantRow } from '@/api/e2ePolicy';
import { useToastStore } from '@/stores/toast';
import Toggle from '@/components/ui/Toggle.vue';

const emit = defineEmits<{
  /** A tenant's ceiling changed (its id) — the page's own answer may be that tenant's. */
  (e: 'changed', id: number): void;
  /** The tenants were read: the requests table names each row's tenant from this list. */
  (e: 'loaded', rows: E2ETenantRow[]): void;
}>();

const { t } = useI18n();
const toast = useToastStore();

const tenants = ref<E2ETenantRow[]>([]);
const loading = ref(false);
const refused = ref(false);
/** The tenant whose switch is on its way to the server. */
const saving = ref<number | null>(null);

onMounted(async () => {
  loading.value = true;
  try {
    tenants.value = await E2EPolicyApi.tenants();
    emit('loaded', tenants.value);
  } catch (e: unknown) {
    if ((e as { response?: { status?: number } })?.response?.status === 403) refused.value = true;
    else toast.error(extractError(e, t('errors.loadFailed')));
  } finally {
    loading.value = false;
  }
});

const columns = computed<DataColumn<E2ETenantRow>[]>(() => [
  { id: 'name', label: t('encryption.tenants.fields.name'), sortable: true, width: 240, sortValue: (r) => r.name },
  {
    id: 'policy',
    label: t('encryption.tenants.fields.policy'),
    sortable: true,
    width: 280,
    format: (r) => t(`encryption.policy.options.${r.e2e_policy}`),
  },
  {
    id: 'available',
    label: t('encryption.tenants.fields.available'),
    sortable: true,
    width: 200,
    sortValue: (r) => (r.e2e_allowed ? 1 : 0),
  },
]);

async function setAllowed(row: E2ETenantRow, allowed: boolean): Promise<void> {
  saving.value = row.id;
  try {
    const next = await E2EPolicyApi.updateTenant(row.id, allowed);
    tenants.value = tenants.value.map((x) => (x.id === next.id ? next : x));
    toast.success(allowed ? t('encryption.tenants.on', { name: next.name }) : t('encryption.tenants.off', { name: next.name }));
    emit('changed', next.id);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.saveFailed')));
  } finally {
    saving.value = null;
  }
}
</script>

<template>
  <section
    v-if="!refused"
    class="space-y-3 rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950"
    data-testid="encryption-tenants"
  >
    <header class="flex items-center gap-2">
      <Building2 class="h-5 w-5 text-brand-600 dark:text-brand-400" />
      <h2 class="text-base font-semibold">
        {{ t('encryption.tenants.title') }}
      </h2>
    </header>
    <p class="text-sm text-zinc-600 dark:text-zinc-400">
      {{ t('encryption.tenants.subtitle') }}
    </p>
    <DataTable
      table-id="admin.encryptionTenants"
      :columns="columns"
      :rows="tenants"
      row-key="id"
      :loading="loading"
    >
      <!-- ⚠ ONE wrapper per cell that stacks lines: a DataTable cell is a flex row. -->
      <template #cell-name="{ row }">
        <div
          class="min-w-0 py-1"
          :data-testid="`e2e-tenant-${row.id}`"
        >
          <div class="truncate font-medium">
            {{ row.name }}
          </div>
          <div class="text-[11px] text-zinc-500">
            <span class="font-mono">{{ row.slug }}</span>
            <template v-if="row.is_supertenant">
              · {{ t('encryption.tenants.supertenant') }}
            </template>
          </div>
        </div>
      </template>
      <template #cell-available="{ row }">
        <Toggle
          :model-value="row.e2e_allowed"
          :name="`e2e-tenant-allowed-${row.id}`"
          :label="row.e2e_allowed ? t('encryption.tenants.state.on') : t('encryption.tenants.state.off')"
          :disabled="saving === row.id"
          @update:model-value="(v: boolean) => setAllowed(row, v)"
        />
      </template>
    </DataTable>
  </section>
</template>
