<script setup lang="ts">
/**
 * One account's permissions on its edit page: the role it holds (picked in
 * the account details above — one per person), the exceptions an
 * administrator sets for this person alone (Inherit / Allow / Deny per
 * permission), and beside each permission the answer that results and where
 * it comes from. The server holds the lines — a delegated administrator
 * cannot allow what they do not hold, nobody edits an administrator's — and a
 * refusal comes back as a toast.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { ShieldCheck, Save, Eraser } from 'lucide-vue-next';

import {
  RolesApi,
  type PermCatalogue,
  type PermEffect,
  type PermKey,
  type UserPermissions,
} from '@/api/roles';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { permissionLimitLines } from '@/lib/permissionLimits';
import PermissionGrid from '@/components/PermissionGrid.vue';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';

const props = defineProps<{
  userId: number;
  /** The built-in role the server holds for the account. */
  role: string;
  /** The custom role it holds, if any. */
  customRole?: { name: string } | null;
}>();

const { t, locale } = useI18n();
const toast = useToastStore();

const catalogue = ref<PermCatalogue | null>(null);
const data = ref<UserPermissions | null>(null);
const overrides = ref<Record<PermKey, PermEffect>>({});
const loading = ref(true);
const saving = ref(false);

const isAdminRole = computed(() => props.role === 'admin');
const locked = computed<PermKey[]>(() =>
  props.role === 'viewer' ? (catalogue.value?.permissions ?? []).filter((d) => d.viewer_capped).map((d) => d.key) : [],
);
const dirty = computed(() => JSON.stringify(overrides.value) !== JSON.stringify(data.value?.overrides ?? {}));

async function load() {
  loading.value = true;
  try {
    const [cat, perms] = await Promise.all([RolesApi.catalogue(), RolesApi.forUser(props.userId)]);
    catalogue.value = cat;
    apply(perms);
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

function apply(p: UserPermissions) {
  data.value = p;
  overrides.value = { ...(p.overrides ?? {}) };
}

async function save() {
  saving.value = true;
  try {
    apply(await RolesApi.setOverrides(props.userId, overrides.value));
    toast.success(t('permissions.card.saved'));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

/** A preset pins every permission it can: allowed if in the preset, denied
 *  if not — so the account ends up exactly there whatever the defaults and
 *  rules say. "Clear" goes back to inheriting everything. */
function applyPreset(name: string) {
  const preset = catalogue.value?.presets.find((p) => p.name === name);
  if (!preset || !catalogue.value) return;
  const next: Record<PermKey, PermEffect> = {};
  for (const d of catalogue.value.permissions) {
    if (d.role_only || locked.value.includes(d.key)) continue;
    next[d.key] = preset.permissions.includes(d.key) ? 'allow' : 'deny';
  }
  overrides.value = next;
}

const presetNames = computed(() => (catalogue.value?.presets ?? []).map((p) => p.name).filter((n) => n !== 'full_admin'));

const currentPreset = computed(() => data.value?.effective.preset || 'custom');

const limits = computed(() => permissionLimitLines(data.value?.effective.settings, t, locale.value));

onMounted(load);
// The role is changed above; when it is saved, redraw what it means here.
watch(() => [props.userId, props.role, props.customRole?.name], load);
</script>

<template>
  <div class="card card-body space-y-3" data-testid="user-permissions-card">
    <div class="flex items-center justify-between gap-2 flex-wrap">
      <h2 class="flex items-center gap-2 text-base font-semibold">
        <ShieldCheck class="h-4 w-4" /> {{ t('permissions.card.title') }}
      </h2>
      <Badge v-if="data && customRole" tone="brand" data-testid="user-permissions-preset">{{ customRole.name }}</Badge>
      <Badge v-else-if="data" :tone="currentPreset === 'custom' ? 'amber' : 'zinc'" data-testid="user-permissions-preset">
        {{ t(`permissions.presets.${currentPreset}`) }}
      </Badge>
    </div>

    <div v-if="loading" class="text-center text-zinc-500"><Spinner /></div>

    <template v-else-if="catalogue && data">
      <p v-if="isAdminRole" class="text-sm text-zinc-600 dark:text-zinc-300">{{ t('permissions.card.adminNote') }}</p>
      <template v-else>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('permissions.card.subtitle') }}</p>
        <p class="text-sm" data-testid="user-role-line">
          {{ t('permissions.card.builtinRole', { role: customRole ? customRole.name : t(`users.roles.${role}`) }) }}
        </p>
        <p v-if="role === 'viewer' && !customRole" class="text-xs text-amber-700 dark:text-amber-400">{{ t('permissions.card.viewerNote') }}</p>
        <p v-if="role === 'viewer' && customRole" class="text-xs text-amber-700 dark:text-amber-400" data-testid="read-only-role-note">
          {{ t('permissions.card.readOnlyRoleNote', { role: customRole.name }) }}
        </p>

        <h3 class="text-sm font-medium pt-2">{{ t('permissions.card.exceptionsTitle') }}</h3>

        <div class="flex items-center gap-2 flex-wrap text-sm">
          <span class="text-zinc-500">{{ t('permissions.presets.label') }}:</span>
          <Button
            v-for="name in presetNames"
            :key="name"
            type="button"
            size="sm"
            variant="outline"
            :data-testid="`preset-${name}`"
            @click="applyPreset(name)"
          >
            {{ t(`permissions.presets.${name}`) }}
          </Button>
        </div>

        <PermissionGrid
          v-model:effects="overrides"
          mode="effects"
          :catalogue="catalogue.permissions"
          :effective="data.effective"
          :locked="locked"
        />

        <p v-if="data.effective.conditional_rules.length" class="text-xs text-zinc-500">
          {{ t('permissions.card.conditional', { count: data.effective.conditional_rules.length }, data.effective.conditional_rules.length) }}
        </p>
        <div v-if="limits.length" class="text-xs text-zinc-600 dark:text-zinc-300">
          <div class="font-medium">{{ t('permissions.card.settingsTitle') }}</div>
          <ul class="list-disc ps-5">
            <li v-for="l in limits" :key="l">{{ l }}</li>
          </ul>
        </div>

        <div class="flex justify-end gap-2">
          <Button type="button" variant="ghost" :disabled="Object.keys(overrides).length === 0" @click="overrides = {}">
            <Eraser class="h-4 w-4" /> {{ t('permissions.card.clear') }}
          </Button>
          <Button type="button" :loading="saving" :disabled="!dirty" data-testid="user-permissions-save" @click="save">
            <Save class="h-4 w-4" /> {{ t('common.save') }}
          </Button>
        </div>
      </template>
    </template>
  </div>
</template>
