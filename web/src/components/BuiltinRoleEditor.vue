<script setup lang="ts">
/**
 * Edit a built-in role's permissions: User (what every regular account
 * starts from — the install defaults) or Viewer (capped: a viewer can never
 * add, change, delete or share files, whatever is ticked here).
 * Administrator is not editable — it holds everything.
 *
 * The installed apps' permissions (backend perm/app.go) are in the same grid,
 * each Default / Allow / Deny: "Default" is the app's own default for this
 * role (as the server says it: the catalogue's `default_for`), and a role's
 * decision is saved with its permissions (`apps`).
 *
 * `readonly`: a tenant's admin on a multi-tenant install. A built-in role is
 * one row for the whole platform and the server refuses their save
 * (requireSupertenant in PutDefaults), so they see it without a Save — and
 * are told a role of their own is a custom role.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

import { RolesApi, type BuiltinRole, type PermCatalogue, type PermEffect, type PermKey } from '@/api/roles';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { builtinAppDefaults } from '@/lib/appPermissions';
import PermissionGrid from '@/components/PermissionGrid.vue';
import Modal from '@/components/ui/Modal.vue';
import Button from '@/components/ui/Button.vue';
import Spinner from '@/components/ui/Spinner.vue';

const props = defineProps<{ modelValue: boolean; role: BuiltinRole; catalogue: PermCatalogue; readonly?: boolean }>();
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void; (e: 'saved'): void }>();

const { t } = useI18n();
const toast = useToastStore();

const set = ref<PermKey[]>([]);
/** The role's decisions about app permissions (app.<app>.<id> → allow | deny). */
const apps = ref<Record<string, PermEffect>>({});
const loading = ref(false);
const saving = ref(false);

const locked = computed<PermKey[]>(() =>
  props.role === 'viewer' ? props.catalogue.permissions.filter((d) => d.viewer_capped).map((d) => d.key) : [],
);
const presets = computed(() => props.catalogue.presets.filter((p) => p.name !== 'full_admin'));
/** "Default" for this role: what the app's own default gives it — the
 *  server's answer (catalogue `default_for`) — which this role's decision
 *  replaces. */
const appDefaults = computed<Record<string, boolean>>(() => builtinAppDefaults(props.catalogue.apps, props.role));

watch(
  () => [props.modelValue, props.role] as const,
  async ([open]) => {
    if (!open) return;
    loading.value = true;
    try {
      const got = await RolesApi.getDefaults(props.role);
      set.value = [...got.permissions];
      apps.value = { ...(got.apps ?? {}) };
    } catch (e) {
      toast.error(extractError(e, t('errors.generic')));
    } finally {
      loading.value = false;
    }
  },
  { immediate: true },
);

function applyPreset(name: string) {
  const p = props.catalogue.presets.find((x) => x.name === name);
  if (p) set.value = p.permissions.filter((k) => k !== 'admin.full' && !locked.value.includes(k));
}

async function save() {
  if (props.readonly) return;
  saving.value = true;
  try {
    // Every app decision goes with it: a choice put back to Default is a key
    // left out, and {} returns them all to the apps' defaults.
    await RolesApi.putDefaults(set.value, props.role, apps.value);
    toast.success(t('permissions.rules.builtinSaved'));
    emit('saved');
    emit('update:modelValue', false);
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <Modal
    :model-value="modelValue"
    :title="t(readonly ? 'permissions.rules.viewBuiltin' : 'permissions.rules.editBuiltin', { role: t(`users.roles.${role}`) })"
    size="xl"
    @update:model-value="(v: boolean) => emit('update:modelValue', v)"
  >
    <div v-if="loading" class="text-center text-zinc-500"><Spinner /></div>
    <div v-else class="space-y-3" :data-testid="`builtin-role-${role}`">
      <p v-if="readonly" class="text-sm text-zinc-600 dark:text-zinc-400" data-testid="builtin-role-platform-note">
        {{ t('permissions.rules.builtinPlatformOwned') }}
      </p>
      <p v-if="role === 'viewer'" class="text-xs text-amber-700 dark:text-amber-400">{{ t('permissions.rules.viewerCeilingNote') }}</p>
      <div v-if="!readonly" class="flex items-center gap-2 flex-wrap text-sm">
        <span class="text-zinc-500">{{ t('permissions.presets.label') }}:</span>
        <Button v-for="p in presets" :key="p.name" type="button" size="sm" variant="outline" @click="applyPreset(p.name)">
          {{ t(`permissions.presets.${p.name}`) }}
        </Button>
      </div>
      <PermissionGrid
        v-model:set="set"
        v-model:app-effects="apps"
        mode="set"
        :catalogue="catalogue.permissions"
        :locked="locked"
        :apps="catalogue.apps"
        :app-defaults="appDefaults"
        :disabled="readonly"
      />
    </div>
    <template #footer>
      <Button variant="ghost" @click="emit('update:modelValue', false)">{{ t(readonly ? 'common.close' : 'common.cancel') }}</Button>
      <Button v-if="!readonly" :loading="saving" data-testid="builtin-role-save" @click="save">{{ t('common.save') }}</Button>
    </template>
  </Modal>
</template>
