<script setup lang="ts">
/**
 * Tenants (the platform operator's; docs/TENANT-ADMIN.md). One row per tenant:
 * its sign-in name (realm), its own address, its storages and its people. A
 * new tenant gets a name, a slug and a realm here, and everything else on its
 * own page. The realm is suggested from the slug while the tenant is being
 * created and never changes afterwards, so it is shown before it is given.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { RouterLink, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Building2, Plus } from 'lucide-vue-next';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { TenantsApi, tenantRefusal, type Tenant } from '@/api/tenants';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Input from '@/components/ui/Input.vue';
import Modal from '@/components/ui/Modal.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t } = useI18n();
const toast = useToastStore();
const router = useRouter();

const tenants = ref<Tenant[]>([]);
const multiTenant = ref(true);
const loading = ref(true);
const q = ref('');

async function load() {
  loading.value = true;
  try {
    const list = await TenantsApi.list();
    tenants.value = list.providers;
    multiTenant.value = list.multi_tenant;
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

const shown = computed(() => {
  const term = q.value.trim().toLocaleLowerCase();
  const list = !term
    ? tenants.value
    : tenants.value.filter((p) =>
        [p.name, p.slug, p.realm, p.host ?? ''].some((s) => s.toLocaleLowerCase().includes(term)),
      );
  // The platform's own tenant first, then by name.
  return [...list].sort(
    (a, b) => Number(b.is_supertenant) - Number(a.is_supertenant) || a.name.localeCompare(b.name),
  );
});

/* The explorer's table (DataTable), remembered under `admin.tenants`
 * (docs/CONTRIBUTING.md → "One table"). */
const columns = computed<DataColumn<Tenant>[]>(() => [
  { id: 'name', label: t('tenants.fields.name'), sortable: true, width: 240, sortValue: (p) => p.name.toLocaleLowerCase() },
  { id: 'realm', label: t('tenants.fields.realm'), sortable: true, width: 160, sortValue: (p) => p.realm || null },
  { id: 'host', label: t('tenants.fields.host'), sortable: true, width: 220, sortValue: (p) => p.host || null },
  { id: 'signin', label: t('tenants.signinColumn'), sortable: true, width: 140, sortValue: (p) => (p.oidc_issuer ? 1 : 0) },
  { id: 'storages', label: t('tenants.storagesTitle'), sortable: true, width: 120, sortValue: (p) => p.storage_ids.length },
  { id: 'people', label: t('tenants.peopleColumn'), sortable: true, width: 120, sortValue: (p) => p.user_count },
  { id: 'state', label: t('tenants.stateColumn'), sortable: true, width: 140, sortValue: (p) => (p.enabled ? 1 : 0) },
]);
function rowActions(_p: Tenant): ContextAction[] {
  return [{ key: 'edit', label: t('common.edit'), icon: 'rename' }];
}
function onRowAction(key: string, p: Tenant) {
  if (key === 'edit') router.push({ name: 'tenants.edit', params: { id: p.id } });
}

// ── new tenant: a name, a slug and the realm suggested from it ──
const creating = ref(false);
const saving = ref(false);
const newName = ref('');
const newSlug = ref('');
const newRealm = ref('');
/** The realm was typed by hand: the suggestion stops following the slug. */
const realmTouched = ref(false);
const suggesting = ref(false);
const errors = ref<Record<string, string>>({});
let suggestTimer: ReturnType<typeof setTimeout> | undefined;
let suggestSeq = 0;
onBeforeUnmount(() => suggestTimer && clearTimeout(suggestTimer));

function openNew() {
  newName.value = '';
  newSlug.value = '';
  newRealm.value = '';
  realmTouched.value = false;
  errors.value = {};
  creating.value = true;
}

/* The suggestion is the server's (one rule, tenant.SuggestRealm, shared with
 * the MCP tool): asked a moment after the slug stops changing, and only while
 * nobody has typed a realm of their own. */
watch(newSlug, (slug) => {
  errors.value = { ...errors.value, slug: '' };
  if (realmTouched.value) return;
  if (suggestTimer) clearTimeout(suggestTimer);
  if (!slug.trim()) {
    newRealm.value = '';
    return;
  }
  suggestTimer = setTimeout(() => void suggest(slug), 250);
});

async function suggest(slug: string) {
  const seq = ++suggestSeq;
  suggesting.value = true;
  try {
    const s = await TenantsApi.suggestRealm(slug);
    if (seq !== suggestSeq || realmTouched.value) return;
    newRealm.value = s.realm;
    errors.value = { ...errors.value, realm: s.realm ? '' : t('tenants.errors.realm_none') };
  } catch {
    /* No suggestion: the field stays as it is and the person types one. */
  } finally {
    if (seq === suggestSeq) suggesting.value = false;
  }
}

function onRealmInput(v: string | number | null) {
  newRealm.value = String(v ?? '');
  realmTouched.value = true;
  errors.value = { ...errors.value, realm: '' };
}

async function create() {
  if (!newSlug.value.trim() || !newRealm.value.trim()) return;
  saving.value = true;
  errors.value = {};
  try {
    const p = await TenantsApi.create({
      slug: newSlug.value.trim(),
      name: newName.value.trim() || newSlug.value.trim(),
      realm: newRealm.value.trim(),
    });
    creating.value = false;
    toast.success(t('tenants.createdOk'));
    router.push({ name: 'tenants.edit', params: { id: p.id } });
  } catch (e) {
    const r = tenantRefusal(e);
    if (r) errors.value = { [r.field]: t(`tenants.errors.${r.code}`) };
    else toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-3 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          <Building2 class="h-5 w-5" /> {{ t('tenants.title') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-2xl">{{ t('tenants.subtitle') }}</p>
      </div>
      <Button data-testid="tenant-new" @click="openNew"><Plus class="h-4 w-4" /> {{ t('tenants.add') }}</Button>
    </div>

    <div v-if="!loading && !multiTenant" class="card card-body text-sm" role="note" data-testid="tenants-mode-off">
      {{ t('tenants.modeOff') }}
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <DataTable
      v-else
      table-id="admin.tenants"
      :columns="columns"
      :rows="shown"
      :loading="loading"
      :empty="tenants.length === 0 ? t('tenants.empty') : t('tenants.noMatch')"
      row-key="id"
      data-testid="tenants-list"
      :row-actions="(p: Tenant) => rowActions(p)"
      :row-actions-test-id="(p: Tenant) => `tenant-actions-${p.id}`"
      @row-action="(key: string, p: Tenant) => onRowAction(key, p)"
    >
      <template #toolbar>
        <Input v-model="q" type="search" :placeholder="t('tenants.search')" size="sm" class="w-64 max-w-full" />
      </template>
      <!-- ⚠ Each cell is ONE root: a DataTable cell is a flex row. -->
      <template #cell-name="{ row }">
        <div :data-testid="`tenant-${(row as Tenant).id}`">
          <RouterLink :to="{ name: 'tenants.edit', params: { id: (row as Tenant).id } }" class="font-medium hover:underline">{{ (row as Tenant).name }}</RouterLink>
          <span class="tbl-sub"><bdi>{{ (row as Tenant).slug }}</bdi></span>
        </div>
      </template>
      <template #cell-realm="{ row }">
        <div>
          <Badge v-if="(row as Tenant).is_supertenant" tone="brand" :title="t('tenants.platformHint')">{{ t('tenants.platform') }}</Badge>
          <span v-else class="tbl-mono"><bdi>{{ (row as Tenant).realm }}</bdi></span>
        </div>
      </template>
      <template #cell-host="{ row }">
        <span class="tbl-mono" :title="(row as Tenant).host || undefined"><bdi>{{ (row as Tenant).host || '-' }}</bdi></span>
      </template>
      <template #cell-signin="{ row }">
        <div>
          <Badge v-if="(row as Tenant).oidc_issuer" tone="sky">{{ t('tenants.signin.ownOidc') }}</Badge>
          <span v-else class="tbl-sub">{{ t('tenants.signin.shared') }}</span>
        </div>
      </template>
      <template #cell-storages="{ row }">
        <span>{{ t('tenants.storageCount', { count: (row as Tenant).storage_ids.length }, (row as Tenant).storage_ids.length) }}</span>
      </template>
      <template #cell-people="{ row }">
        <span>{{ t('tenants.peopleCount', { count: (row as Tenant).user_count }, (row as Tenant).user_count) }}</span>
      </template>
      <template #cell-state="{ row }">
        <div>
          <Badge :tone="(row as Tenant).enabled ? 'emerald' : 'amber'">{{ (row as Tenant).enabled ? t('tenants.state.active') : t('tenants.state.suspended') }}</Badge>
        </div>
      </template>
    </DataTable>

    <Modal v-model="creating" :title="t('tenants.add')" size="sm">
      <form class="space-y-3" data-testid="tenant-new-form" @submit.prevent="create">
        <Input v-model="newName" :label="t('tenants.fields.name')" data-testid="tenant-new-name" />
        <Input
          v-model="newSlug"
          :label="t('tenants.fields.slug')"
          :hint="t('tenants.fields.slugHint')"
          :error="errors.slug || null"
          required
          monospace
          data-testid="tenant-new-slug"
        />
        <Input
          :model-value="newRealm"
          :label="t('tenants.fields.realm')"
          :hint="suggesting ? t('tenants.realmSuggesting') : realmTouched ? t('tenants.realmNewHint') : t('tenants.realmSuggestedHint')"
          :error="errors.realm || null"
          required
          monospace
          data-testid="tenant-new-realm"
          @update:model-value="onRealmInput"
        />
      </form>
      <template #footer>
        <Button variant="ghost" @click="creating = false">{{ t('common.cancel') }}</Button>
        <Button
          :loading="saving"
          :disabled="!newSlug.trim() || !newRealm.trim()"
          data-testid="tenant-new-create"
          @click="create"
        >{{ t('tenants.create') }}</Button>
      </template>
    </Modal>
  </div>
</template>
