<script setup lang="ts">
/**
 * Admin → Encryption — who may START encrypting here (backend
 * internal/e2epolicy; docs/E2E-ENCRYPTION.md → "Who may encrypt").
 *
 * Three answers on one page, because they are one decision in three layers:
 *   - the tenant's policy (EncryptionPolicyCard) — the tenant administrator's;
 *   - the requests the `approval` policy leaves (EncryptionRequestsPanel);
 *   - every tenant's ceiling (EncryptionTenantsPanel) — the platform
 *     operator's alone. It lives here, next to what it caps, rather than on
 *     Admin → Tenants.
 *
 * ⚠ The tenants panel is drawn for the platform operator of a multi-tenant
 * install: `caller_admin` (the capabilities' "may configure the instance",
 * false for a tenant's administrator) AND a policy answer scoped to a tenant
 * (`scope: 'tenant'`; a single-tenant install answers `instance`). The web app
 * knows no other "multi-tenant" flag, and the server refuses everybody else
 * anyway (requireSupertenant).
 *
 * The ceiling panel's tenant list is also how the requests table names each
 * request's tenant for the operator (`tenantNames`).
 *
 * No `adminPerm` on the route: an account that holds an admin.* permission
 * without the role is not the tenant's administrator, and the policy binds
 * administrators too.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Lock } from 'lucide-vue-next';

import { extractError } from '@/api/client';
import { E2EPolicyApi, type E2EPolicyState, type E2ETenantRow } from '@/api/e2ePolicy';
import { useCapabilitiesStore } from '@/stores/capabilities';
import Spinner from '@/components/ui/Spinner.vue';
import EncryptionPolicyCard from '@/components/encryption/EncryptionPolicyCard.vue';
import EncryptionRequestsPanel from '@/components/encryption/EncryptionRequestsPanel.vue';
import EncryptionTenantsPanel from '@/components/encryption/EncryptionTenantsPanel.vue';

const { t } = useI18n();
const caps = useCapabilitiesStore();

const state = ref<E2EPolicyState | null>(null);
const loading = ref(true);
const error = ref('');

onMounted(async () => {
  // ⚠ `caller_admin` is per caller, but App.vue fetches the capabilities once
  // per document, and a password sign-in never reloads it: the snapshot here
  // may be the one taken while nobody was signed in (false for the operator
  // who has just signed in), or the previous account's (true for a tenant's
  // administrator who signs in after an operator). Ask again; `operator` is a
  // computed, so the tenants panel follows the fresh answer when it lands.
  // (The store's fetch() always asks the server: it has no cache to get around.)
  void caps.fetch();
  try {
    state.value = await E2EPolicyApi.get();
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.loadFailed'));
  } finally {
    loading.value = false;
  }
});

/** The platform operator of a multi-tenant install (see the header). */
const operator = computed(() => caps.data.caller_admin === true && state.value?.scope === 'tenant');

/**
 * Tenant names by id, from the list the ceiling panel reads anyway. The
 * operator's requests list mixes every tenant's, and each row carries only its
 * `tenant_id`; the requests table names it. Null until the list is read, and
 * for everybody who is not the operator: a tenant's administrator reads one
 * tenant's requests, and a column of one name is noise.
 */
const tenantNames = ref<Record<number, string> | null>(null);
function onTenantsLoaded(rows: E2ETenantRow[]): void {
  tenantNames.value = Object.fromEntries(rows.map((r) => [r.id, r.name]));
}

/** A tenant's ceiling moved: when it is this page's own tenant, its banner follows. */
async function onTenantChanged(id: number): Promise<void> {
  if (state.value?.tenant?.id !== id) return;
  try {
    state.value = await E2EPolicyApi.get();
  } catch {
    /* the switch itself was saved and said; the banner catches up on the next visit */
  }
}
</script>

<template>
  <div
    class="max-w-5xl space-y-4"
    data-testid="encryption-page"
  >
    <div class="flex items-center gap-2">
      <Lock class="h-6 w-6 text-brand-600 dark:text-brand-400" />
      <div>
        <h1 class="text-xl font-semibold">
          {{ t('encryption.title') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">
          {{ t('encryption.subtitle') }}
        </p>
      </div>
    </div>

    <div
      v-if="loading"
      class="flex justify-center py-16"
    >
      <Spinner />
    </div>
    <div
      v-else-if="!state"
      class="rounded-lg border border-rose-200 bg-rose-50 p-4 text-sm text-rose-700"
      role="alert"
    >
      {{ error }}
    </div>
    <template v-else>
      <EncryptionPolicyCard
        :state="state"
        @saved="(next) => (state = next)"
      />
      <EncryptionRequestsPanel :tenant-names="operator ? tenantNames : null" />
      <EncryptionTenantsPanel
        v-if="operator"
        @changed="onTenantChanged"
        @loaded="onTenantsLoaded"
      />
    </template>
  </div>
</template>
