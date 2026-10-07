<script setup lang="ts">
/**
 * AppStoreViewPanel - who sees the store screen (#162, docs/APP-PLUGINS.md →
 * The store screen, docs/ADMIN-PANEL.md → Plugins).
 *
 * The screen is filex's own page (the navigation panel's "App store" row):
 * a trusted store's catalog, read and verified by the server, and a "Request"
 * that lands on the Install requests list above. Here the platform operator
 * turns it on, picks which trusted stores it shows and who sees it -
 * everyone, some built-in roles, or the members of some groups - and, in
 * multi-tenant mode, does so tenant by tenant (a tenant with no setting sees
 * nothing).
 *
 * ⚠ Choices are buttons the reader can see all of (core ChoiceButtons, multi
 * for a list), never a native select; the tenant is the panel's Select (core
 * ChoiceSelect).
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { Eye } from 'lucide-vue-next';
import { ChoiceButtons, type ChoiceOption } from '@brftech/filex-core';

import { AppStoreApi, storeRefusal, type StoreAudience, type StoreViewAnswer, type StoreViewSettings } from '@/api/appStore';
import { GroupsApi, type Group } from '@/api/groups';
import { TenantsApi, type Tenant } from '@/api/tenants';
import { extractError } from '@/api/client';
import { formatDate } from '@/lib/format';
import { storeSentence } from '@/lib/storeRefusal';
import { useToastStore } from '@/stores/toast';

import Button from '@/components/ui/Button.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';

const props = defineProps<{
  /** Bumped by the page when the trusted stores changed (AppStoresPanel). */
  generation?: number;
}>();

const { t, locale } = useI18n();
const toast = useToastStore();

const answer = ref<StoreViewAnswer | null>(null);
const draft = ref<StoreViewSettings | null>(null);
const tenantId = ref<number>(0);
const tenants = ref<Tenant[]>([]);
const groups = ref<Group[]>([]);
const loading = ref(false);
const saving = ref(false);
const failure = ref('');

async function load() {
  loading.value = true;
  failure.value = '';
  try {
    const got = await AppStoreApi.view(tenantId.value || undefined);
    answer.value = got;
    tenantId.value = got.tenant;
    draft.value = { ...got.settings, stores: [...got.settings.stores], roles: [...got.settings.roles], groups: [...got.settings.groups] };
    if (got.multi_tenant && !tenants.value.length) {
      try {
        tenants.value = (await TenantsApi.list()).providers;
      } catch {
        tenants.value = [];
      }
    }
    try {
      groups.value = await GroupsApi.list();
    } catch {
      groups.value = [];
    }
  } catch (e: unknown) {
    answer.value = null;
    const status = (e as { response?: { status?: number } })?.response?.status;
    if (status !== 403 && status !== 503) failure.value = extractError(e, t('errors.loadFailed'));
  } finally {
    loading.value = false;
  }
}
onMounted(load);
watch(() => props.generation, () => void load());

const storeOptions = computed<ChoiceOption[]>(() =>
  (answer.value?.stores ?? []).map((s) => ({
    value: s.origin,
    label: s.origin,
    help: s.connected ? t('appStore.view.connected') : t('appStore.view.notConnected'),
  })),
);

const audienceOptions = computed<ChoiceOption[]>(() => [
  { value: 'everyone', label: t('appStore.view.audience.everyone'), help: t('appStore.view.audience.everyoneHelp') },
  { value: 'roles', label: t('appStore.view.audience.roles'), help: t('appStore.view.audience.rolesHelp') },
  { value: 'groups', label: t('appStore.view.audience.groups'), help: t('appStore.view.audience.groupsHelp') },
]);

const roleOptions = computed<ChoiceOption[]>(() => [
  { value: 'admin', label: t('appStore.view.role.admin') },
  { value: 'user', label: t('appStore.view.role.user') },
  { value: 'viewer', label: t('appStore.view.role.viewer') },
]);

/** The groups a setting may name: in multi-tenant mode, this tenant's and the install-wide ones. */
const groupOptions = computed<ChoiceOption[]>(() =>
  groups.value
    .filter((g) => !answer.value?.multi_tenant || g.provider_id == null || g.provider_id === tenantId.value)
    .map((g) => ({ value: String(g.id), label: g.name })),
);

const tenantOptions = computed(() => tenants.value.map((p) => ({ value: p.id, label: p.name || p.slug })));

/** A store the screen shows but this filex is not connected to: requests can be left, not approved. */
const unconnected = computed(() =>
  (draft.value?.stores ?? []).filter((o) => !(answer.value?.stores ?? []).find((s) => s.origin === o)?.connected),
);

function setStores(v: string | string[]) {
  if (draft.value) draft.value.stores = Array.isArray(v) ? v : [v];
}
function setAudience(v: string | string[]) {
  if (draft.value) draft.value.audience = (Array.isArray(v) ? v[0] : v) as StoreAudience;
}
function setRoles(v: string | string[]) {
  if (draft.value) draft.value.roles = Array.isArray(v) ? v : [v];
}
function setGroups(v: string | string[]) {
  if (draft.value) draft.value.groups = (Array.isArray(v) ? v : [v]).map(Number);
}

async function pickTenant(v: string | number | null) {
  tenantId.value = Number(v) || 0;
  await load();
}

async function save() {
  if (!draft.value) return;
  saving.value = true;
  failure.value = '';
  try {
    const got = await AppStoreApi.saveView(draft.value, answer.value?.multi_tenant ? tenantId.value : undefined);
    if (answer.value) answer.value = { ...answer.value, settings: got.settings };
    draft.value = { ...got.settings };
    toast.success(t('appStore.view.saved'));
  } catch (e: unknown) {
    const r = storeRefusal(e);
    failure.value = (r && storeSentence(r, t)) || r?.message || extractError(e, t('errors.generic'));
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section v-if="answer && draft" class="card card-body space-y-4" data-testid="app-store-view">
    <h3 class="flex items-center gap-2 text-sm font-semibold">
      <Eye class="h-4 w-4" />
      {{ t('appStore.view.title') }}
    </h3>
    <p class="text-xs text-zinc-600 dark:text-zinc-400">{{ t('appStore.view.intro') }}</p>

    <Select
      v-if="answer.multi_tenant && tenantOptions.length"
      :model-value="tenantId"
      :options="tenantOptions"
      :label="t('appStore.view.tenant')"
      :hint="t('appStore.view.tenantHint')"
      name="app-store-view-tenant"
      data-testid="app-store-view-tenant"
      @update:model-value="pickTenant"
    />

    <Toggle
      v-model="draft.enabled"
      name="app-store-view-enabled"
      :label="t('appStore.view.enabled')"
      :description="t('appStore.view.enabledHelp')"
      data-testid="app-store-view-enabled"
    />

    <template v-if="draft.enabled">
      <div class="space-y-1">
        <p class="text-sm font-medium" id="app-store-view-stores">{{ t('appStore.view.stores') }}</p>
        <p v-if="!storeOptions.length" class="text-xs text-zinc-500">{{ t('appStore.view.noStores') }}</p>
        <ChoiceButtons
          v-else
          :model-value="draft.stores"
          :options="storeOptions"
          multi
          aria-labelledby="app-store-view-stores"
          testid-prefix="app-store-view-store"
          @update:model-value="setStores"
        />
        <p v-if="unconnected.length" class="text-xs text-amber-700 dark:text-amber-300" data-testid="app-store-view-unconnected">
          {{ t('appStore.view.unconnected', { stores: unconnected.join(', ') }) }}
        </p>
      </div>

      <div class="space-y-1">
        <p class="text-sm font-medium" id="app-store-view-audience">{{ t('appStore.view.audience.title') }}</p>
        <ChoiceButtons
          :model-value="draft.audience"
          :options="audienceOptions"
          segmented
          aria-labelledby="app-store-view-audience"
          testid-prefix="app-store-view-audience"
          @update:model-value="setAudience"
        />
      </div>

      <div v-if="draft.audience === 'roles'" class="space-y-1">
        <p class="text-sm font-medium" id="app-store-view-roles">{{ t('appStore.view.roles') }}</p>
        <ChoiceButtons
          :model-value="draft.roles"
          :options="roleOptions"
          multi
          aria-labelledby="app-store-view-roles"
          testid-prefix="app-store-view-role"
          @update:model-value="setRoles"
        />
      </div>

      <div v-if="draft.audience === 'groups'" class="space-y-1">
        <p class="text-sm font-medium" id="app-store-view-groups">{{ t('appStore.view.groups') }}</p>
        <p v-if="!groupOptions.length" class="text-xs text-zinc-500">{{ t('appStore.view.noGroups') }}</p>
        <ChoiceButtons
          v-else
          :model-value="draft.groups.map(String)"
          :options="groupOptions"
          multi
          aria-labelledby="app-store-view-groups"
          testid-prefix="app-store-view-group"
          @update:model-value="setGroups"
        />
      </div>
    </template>

    <p v-if="failure" class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200" role="alert" data-testid="app-store-view-error">
      {{ failure }}
    </p>
    <div class="flex flex-wrap items-center justify-between gap-2">
      <span v-if="answer.settings.updated_at" class="text-xs text-zinc-500">
        {{ t('appStore.view.updated', { who: answer.settings.updated_by_name || '-', when: formatDate(answer.settings.updated_at, locale) }) }}
      </span>
      <span v-else />
      <Button size="sm" variant="primary" :loading="saving || loading" data-testid="app-store-view-save" @click="save">
        {{ t('common.save') }}
      </Button>
    </div>
  </section>
</template>
