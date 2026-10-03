<script setup lang="ts">
/**
 * One tenant (the platform operator's; docs/TENANT-ADMIN.md): its name and
 * slug, its realm (read-only, with the reason), its own address, its own OIDC,
 * whether it may sign in, the storages its people reach, and deleting it. The
 * server holds every line (handlers/providers.go) and a refusal comes back on
 * the field it is about.
 */
import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ArrowLeft, Building2, Database, Save, Trash2, Link2 } from 'lucide-vue-next';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { TenantsApi, tenantRefusal, type Tenant, type TenantInput } from '@/api/tenants';
import { StoragesApi } from '@/api/storages';
import type { StorageRef } from '@/api/types';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Modal from '@/components/ui/Modal.vue';
import Spinner from '@/components/ui/Spinner.vue';
import TenantSelfService from '@/components/TenantSelfService.vue';

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const toast = useToastStore();

const id = computed(() => Number(route.params.id));
const tenant = ref<Tenant | null>(null);
const storages = ref<StorageRef[]>([]);
const loading = ref(true);
const saving = ref(false);
const errors = ref<Record<string, string>>({});

const form = ref({
  name: '',
  slug: '',
  host: '',
  cookie_domain: '',
  oidc_issuer: '',
  oidc_client_id: '',
  oidc_client_secret: '',
  oidc_redirect_url: '',
  role_claim: '',
  admin_group: '',
  oidc_trust_email: false,
  enabled: true,
});

async function load() {
  loading.value = true;
  try {
    const [p, ss] = await Promise.all([
      TenantsApi.get(id.value),
      // Best effort: without it a linked storage shows as its number.
      StoragesApi.list().catch(() => [] as StorageRef[]),
    ]);
    storages.value = ss;
    apply(p);
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
    router.replace({ name: 'tenants' });
  } finally {
    loading.value = false;
  }
}
onMounted(load);

function apply(p: Tenant) {
  tenant.value = p;
  form.value = {
    name: p.name,
    slug: p.slug,
    host: p.host ?? '',
    cookie_domain: p.cookie_domain ?? '',
    oidc_issuer: p.oidc_issuer ?? '',
    oidc_client_id: p.oidc_client_id ?? '',
    oidc_client_secret: '',
    oidc_redirect_url: p.oidc_redirect_url ?? '',
    role_claim: p.role_claim ?? '',
    admin_group: p.admin_group ?? '',
    oidc_trust_email: p.oidc_trust_email === true,
    enabled: p.enabled,
  };
}

const platform = computed(() => tenant.value?.is_supertenant === true);

/** The trust setting's line: the upgrade's note first when it set the value. */
const trustHint = computed(() => {
  const own = t('authProviders.fieldHints.trust_email');
  return tenant.value?.oidc_trust_email_by_upgrade && form.value.oidc_trust_email
    ? `${t('authProviders.upgradeHints.trust_email')} ${own}`
    : own;
});

async function save() {
  if (!tenant.value) return;
  saving.value = true;
  errors.value = {};
  const f = form.value;
  const input: TenantInput = {
    name: f.name.trim(),
    slug: f.slug.trim(),
    host: f.host.trim(),
    cookie_domain: f.cookie_domain.trim(),
    oidc_issuer: f.oidc_issuer.trim(),
    oidc_client_id: f.oidc_client_id.trim(),
    oidc_redirect_url: f.oidc_redirect_url.trim(),
    role_claim: f.role_claim.trim(),
    admin_group: f.admin_group.trim(),
    oidc_trust_email: f.oidc_trust_email,
    // "" keeps the stored secret: the page never has it to send back.
    oidc_client_secret: f.oidc_client_secret,
  };
  if (!platform.value) input.enabled = f.enabled;
  try {
    apply(await TenantsApi.update(id.value, input));
    toast.success(t('tenants.savedOk'));
  } catch (e) {
    const r = tenantRefusal(e);
    if (r) errors.value = { [r.field]: t(`tenants.errors.${r.code}`) };
    else toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

// ── storages ──
const linked = computed(() =>
  (tenant.value?.storage_ids ?? []).map(
    (sid) => storages.value.find((s) => s.id === sid) ?? ({ id: sid, name: `#${sid}` } as StorageRef),
  ),
);
const linkable = computed(() => {
  const have = new Set(tenant.value?.storage_ids ?? []);
  return storages.value.filter((s) => !have.has(s.id));
});
const linkChoice = ref<string>('');
const linkOptions = computed(() => [
  { value: '', label: t('tenants.chooseStorage') },
  ...linkable.value.map((s) => ({ value: String(s.id), label: s.name })),
]);
async function link() {
  if (!linkChoice.value) return;
  try {
    apply(await TenantsApi.linkStorage(id.value, Number(linkChoice.value)));
    linkChoice.value = '';
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}
async function unlink(s: StorageRef) {
  if (!confirm(t('tenants.unlinkConfirm', { storage: s.name }))) return;
  try {
    apply(await TenantsApi.unlinkStorage(id.value, s.id));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}
const storageColumns = computed<DataColumn<StorageRef>[]>(() => [
  { id: 'name', label: t('common.name'), sortable: true, width: 280, sortValue: (s) => s.name.toLocaleLowerCase() },
]);
function storageActions(_s: StorageRef): ContextAction[] {
  return [{ key: 'unlink', label: t('tenants.unlink'), icon: 'delete', danger: true }];
}
function onStorageAction(key: string, s: StorageRef) {
  if (key === 'unlink') void unlink(s);
}

// ── delete ──
const showDelete = ref(false);
const deleting = ref(false);
const alsoAccounts = ref(false);
function openDelete() {
  alsoAccounts.value = false;
  showDelete.value = true;
}
async function confirmDelete() {
  if (!tenant.value) return;
  deleting.value = true;
  try {
    await TenantsApi.remove(id.value, alsoAccounts.value);
    toast.success(t('tenants.deletedOk'));
    router.replace({ name: 'tenants' });
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    deleting.value = false;
    showDelete.value = false;
  }
}
</script>

<template>
  <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>
  <div v-else-if="tenant" class="space-y-5 max-w-3xl">
    <div class="flex items-center justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          <Building2 class="h-5 w-5" /> {{ tenant.name }}
          <Badge v-if="platform" tone="brand">{{ t('tenants.platform') }}</Badge>
          <Badge v-else-if="!tenant.enabled" tone="amber">{{ t('tenants.state.suspended') }}</Badge>
        </h1>
        <p class="text-sm text-zinc-500">{{ t('tenants.peopleCount', { count: tenant.user_count }, tenant.user_count) }}</p>
      </div>
      <Button variant="ghost" size="sm" @click="router.push({ name: 'tenants' })">
        <ArrowLeft class="h-4 w-4 rtl:rotate-180" /> {{ t('common.back') }}
      </Button>
    </div>

    <form class="space-y-5" data-testid="tenant-form" @submit.prevent="save">
      <div class="card card-body space-y-3">
        <h2 class="text-base font-semibold">{{ t('tenants.generalTitle') }}</h2>
        <Input v-model="form.name" :label="t('tenants.fields.name')" required data-testid="tenant-name" />
        <Input
          v-model="form.slug"
          :label="t('tenants.fields.slug')"
          :hint="t('tenants.fields.slugEditHint')"
          :error="errors.slug || null"
          required
          monospace
          data-testid="tenant-slug"
        />
        <!-- The realm never changes: shown, with the reason, never editable. -->
        <Input
          :model-value="platform ? '' : tenant.realm"
          :label="t('tenants.fields.realm')"
          :placeholder="platform ? t('tenants.platformRealm') : undefined"
          :hint="platform ? t('tenants.platformRealmHint') : t('tenants.realmLockedHint', { realm: tenant.realm })"
          :error="errors.realm || null"
          readonly
          monospace
          data-testid="tenant-realm"
        />
        <Toggle
          v-model="form.enabled"
          :label="t('tenants.fields.enabled')"
          :description="platform ? t('tenants.platformAlwaysOn') : t('tenants.fields.enabledHint')"
          :disabled="platform"
          data-testid="tenant-enabled"
        />
      </div>

      <div class="card card-body space-y-3">
        <h2 class="text-base font-semibold">{{ t('tenants.addressTitle') }}</h2>
        <Input
          v-model="form.host"
          :label="t('tenants.fields.host')"
          :hint="t('tenants.fields.hostHint')"
          :error="errors.host || null"
          monospace
          data-testid="tenant-host"
        />
        <Input
          v-model="form.cookie_domain"
          :label="t('tenants.fields.cookieDomain')"
          :hint="t('tenants.fields.cookieDomainHint')"
          monospace
          data-testid="tenant-cookie-domain"
        />
      </div>

      <div class="card card-body space-y-3">
        <h2 class="text-base font-semibold">{{ t('tenants.oidcTitle') }}</h2>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tenants.oidcHint') }}</p>
        <Input v-model="form.oidc_issuer" :label="t('tenants.fields.oidcIssuer')" type="url" monospace data-testid="tenant-oidc-issuer" />
        <Input v-model="form.oidc_client_id" :label="t('tenants.fields.oidcClientId')" monospace />
        <Input
          v-model="form.oidc_client_secret"
          :label="t('tenants.fields.oidcClientSecret')"
          type="password"
          autocomplete="new-password"
          :placeholder="tenant.oidc_client_secret_set ? t('tenants.secretSet') : undefined"
          :hint="tenant.oidc_client_secret_set ? t('tenants.secretSetHint') : undefined"
        />
        <Input
          v-model="form.oidc_redirect_url"
          :label="t('tenants.fields.oidcRedirectUrl')"
          :hint="t('tenants.fields.oidcRedirectUrlHint')"
          type="url"
          monospace
        />
        <Input v-model="form.role_claim" :label="t('tenants.fields.roleClaim')" monospace />
        <Input v-model="form.admin_group" :label="t('tenants.fields.adminGroup')" :hint="t('tenants.fields.adminGroupHint')" />
        <!-- The words of every provider's trust_email (ProviderFields): one
             setting, one sentence, wherever it is set. -->
        <Toggle
          v-model="form.oidc_trust_email"
          :label="t('authProviders.fields.trust_email')"
          :description="trustHint"
          name="tenant-oidc-trust-email"
        />
      </div>

      <div class="flex justify-between items-center gap-2">
        <Button v-if="!platform" type="button" variant="danger" data-testid="tenant-delete" @click="openDelete">
          <Trash2 class="h-4 w-4" /> {{ t('common.delete') }}
        </Button>
        <span v-else class="text-sm text-zinc-500">{{ t('tenants.platformNoDelete') }}</span>
        <Button type="submit" :loading="saving" data-testid="tenant-save">
          <Save class="h-4 w-4" /> {{ t('common.save') }}
        </Button>
      </div>
    </form>

    <div class="card card-body space-y-3" data-testid="tenant-storages">
      <h2 class="flex items-center gap-2 text-base font-semibold"><Database class="h-4 w-4" /> {{ t('tenants.storagesTitle') }}</h2>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ platform ? t('tenants.platformStoragesHint') : t('tenants.storagesHint') }}</p>
      <div class="flex items-end gap-2 flex-wrap">
        <Select v-model="linkChoice" :options="linkOptions" :label="t('tenants.linkStorage')" class="min-w-64" data-testid="tenant-link-choice" />
        <Button variant="outline" :disabled="!linkChoice" data-testid="tenant-link" @click="link">
          <Link2 class="h-4 w-4" /> {{ t('tenants.link') }}
        </Button>
      </div>
      <DataTable
        table-id="admin.tenants.storages"
        :columns="storageColumns"
        :rows="linked"
        :empty="t('tenants.noStorages')"
        row-key="id"
        :row-actions="(s: StorageRef) => storageActions(s)"
        :row-actions-test-id="(s: StorageRef) => `tenant-storage-actions-${s.id}`"
        @row-action="(key: string, s: StorageRef) => onStorageAction(key, s)"
      >
        <template #cell-name="{ row }">
          <span class="font-medium"><bdi>{{ (row as StorageRef).name }}</bdi></span>
        </template>
      </DataTable>
    </div>

    <!-- Its own OIDC and LDAP, the providers bound to it, its own domains:
         the same component its administrator sees (TenantSelfService). -->
    <TenantSelfService v-if="!platform" :tenant-id="tenant.id" operator />

    <Modal v-model="showDelete" :title="t('common.delete')" size="sm">
      <div class="space-y-3 text-sm">
        <p>{{ t('tenants.deleteConfirm', { name: tenant.name }) }}</p>
        <Checkbox
          v-if="tenant.user_count > 0"
          v-model="alsoAccounts"
          :label="t('tenants.deleteAccounts', { count: tenant.user_count }, tenant.user_count)"
          data-testid="tenant-delete-accounts"
        />
      </div>
      <template #footer>
        <Button variant="ghost" @click="showDelete = false">{{ t('common.cancel') }}</Button>
        <Button
          variant="danger"
          :loading="deleting"
          :disabled="tenant.user_count > 0 && !alsoAccounts"
          data-testid="tenant-delete-confirm"
          @click="confirmDelete"
        >{{ t('common.yesDelete') }}</Button>
      </template>
    </Modal>
  </div>
</template>
