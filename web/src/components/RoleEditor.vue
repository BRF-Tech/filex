<script setup lang="ts">
/**
 * Create or edit one custom role — a stand-alone role, ticked like the
 * built-in ones: what it may do (its own list), the limits it sets, an
 * optional "different in some folders" part (Allow/Deny for file actions in
 * chosen storages and folders), and the SSO groups that start a new account
 * on it. People are given a role on their own page (one each), not here. The
 * server validates everything (backend perm.NormalizeRule).
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

import { RolesApi, type PermCatalogue, type PermissionRule, type PermissionRuleInput } from '@/api/roles';
import type { StorageRef } from '@/api/types';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import PermissionGrid from '@/components/PermissionGrid.vue';
import Modal from '@/components/ui/Modal.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';
import ChipInput from '@/components/ui/ChipInput.vue';

const props = defineProps<{
  modelValue: boolean;
  rule: PermissionRule | null;
  catalogue: PermCatalogue;
  storages: StorageRef[];
}>();
const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void;
  (e: 'saved', r: PermissionRule): void;
}>();

const { t } = useI18n();
const toast = useToastStore();
const MB = 1_000_000;

function blank(): PermissionRuleInput {
  return {
    name: '',
    description: '',
    enabled: true,
    // Starts from the Standard user preset, so a role made in a hurry does
    // not lock its people out of the desktop app or their own profile.
    permissions: (props.catalogue.presets.find((p) => p.name === 'standard')?.permissions ?? []).filter((k) => k !== 'admin.full'),
    targets: [],
    effects: {},
    settings: {},
    conditions: {},
  };
}

const form = ref<PermissionRuleInput>(blank());
const maxUploadMB = ref<number | null>(null);
const saving = ref(false);

watch(
  () => [props.modelValue, props.rule] as const,
  ([open, r]) => {
    if (!open) return;
    form.value = r
      ? JSON.parse(JSON.stringify({ ...r, permissions: r.permissions ?? [], conditions: r.conditions ?? {}, settings: r.settings ?? {} }))
      : blank();
    maxUploadMB.value = form.value.settings.max_upload_bytes ? form.value.settings.max_upload_bytes / MB : null;
  },
  { immediate: true },
);

const hasPlace = computed(
  () => (form.value.conditions.storage_ids?.length ?? 0) > 0 || (form.value.conditions.paths?.length ?? 0) > 0,
);
// Only file actions and links can differ by folder.
const CONDITIONABLE = [
  'files.download',
  'files.create',
  'files.modify',
  'files.rename',
  'files.move',
  'files.delete',
  'files.purge',
  'share.links',
  'share.upload_links',
];

const presets = computed(() => props.catalogue.presets.filter((p) => p.name !== 'full_admin'));
function applyPreset(name: string) {
  const p = props.catalogue.presets.find((x) => x.name === name);
  if (p) form.value.permissions = p.permissions.filter((k) => k !== 'admin.full');
}

/** The SSO groups that start a new account on this role. */
const ssoGroups = computed({
  get: () => form.value.targets.filter((x) => x.kind === 'sso_group').map((x) => x.value ?? ''),
  set: (v: string[]) => (form.value.targets = v.map((value) => ({ kind: 'sso_group' as const, value }))),
});

function toggleStorage(id: number, on: boolean) {
  const cur = form.value.conditions.storage_ids ?? [];
  form.value.conditions = {
    ...form.value.conditions,
    storage_ids: on ? [...cur.filter((x) => x !== id), id] : cur.filter((x) => x !== id),
  };
}

const paths = computed({
  get: () => form.value.conditions.paths ?? [],
  set: (v: string[]) => (form.value.conditions = { ...form.value.conditions, paths: v }),
});
const blockedExt = computed({
  get: () => form.value.settings.blocked_extensions ?? [],
  set: (v: string[]) => (form.value.settings = { ...form.value.settings, blocked_extensions: v }),
});

async function save() {
  const body: PermissionRuleInput = JSON.parse(JSON.stringify(form.value));
  if (hasPlace.value) {
    body.effects = Object.fromEntries(Object.entries(body.effects).filter(([k]) => CONDITIONABLE.includes(k)));
  } else {
    // No folders picked: there is no folder part.
    body.conditions = {};
    body.effects = {};
  }
  body.settings.max_upload_bytes = maxUploadMB.value && maxUploadMB.value > 0 ? Math.round(maxUploadMB.value * MB) : null;
  if (!body.settings.share_link_max_days) body.settings.share_link_max_days = null;
  saving.value = true;
  try {
    const saved = props.rule
      ? await RolesApi.updateRule(props.rule.id, body)
      : await RolesApi.createRule(body);
    toast.success(t('permissions.rules.saved'));
    emit('saved', saved);
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
    :title="rule ? t('permissions.rules.edit') : t('permissions.rules.add')"
    size="xl"
    @update:model-value="(v: boolean) => emit('update:modelValue', v)"
  >
    <form class="space-y-4" data-testid="rule-editor" @submit.prevent="save">
      <div class="grid gap-3 sm:grid-cols-[1fr_auto] items-end">
        <Input v-model="form.name" :label="t('permissions.rules.name')" required data-testid="rule-name" />
        <Toggle v-model="form.enabled" :label="t('permissions.rules.enabled')" />
      </div>
      <Input v-model="form.description" :label="t('permissions.rules.description')" />

      <!-- what the role may do -->
      <section class="space-y-2">
        <h3 class="text-sm font-medium">{{ t('permissions.rules.effects') }}</h3>
        <p class="text-xs text-zinc-500">{{ t('permissions.rules.permissionsHint') }}</p>
        <div class="flex items-center gap-2 flex-wrap text-sm">
          <span class="text-zinc-500">{{ t('permissions.presets.label') }}:</span>
          <Button v-for="p in presets" :key="p.name" type="button" size="sm" variant="outline" @click="applyPreset(p.name)">
            {{ t(`permissions.presets.${p.name}`) }}
          </Button>
        </div>
        <PermissionGrid v-model:set="form.permissions" mode="set" :catalogue="catalogue.permissions" data-testid="role-permissions" />
      </section>

      <!-- different in some folders -->
      <section class="space-y-2">
        <h3 class="text-sm font-medium">{{ t('permissions.rules.folders') }}</h3>
        <p class="text-xs text-zinc-500">{{ t('permissions.rules.foldersHint') }}</p>
        <div v-if="storages.length" class="flex flex-wrap gap-3 text-sm">
          <span class="text-zinc-500">{{ t('permissions.rules.storages') }}:</span>
          <label v-for="s in storages" :key="s.id" class="inline-flex items-center gap-1">
            <input
              type="checkbox"
              :checked="(form.conditions.storage_ids ?? []).includes(s.id)"
              @change="toggleStorage(s.id, ($event.target as HTMLInputElement).checked)"
            />
            {{ s.name }}
          </label>
        </div>
        <ChipInput v-model="paths" :label="t('permissions.rules.paths')" />
        <PermissionGrid
          v-if="hasPlace"
          v-model:effects="form.effects"
          mode="effects"
          :catalogue="catalogue.permissions"
          :only="CONDITIONABLE"
          data-testid="role-folder-effects"
        />
      </section>

      <!-- limits -->
      <section class="space-y-2">
        <h3 class="text-sm font-medium">{{ t('permissions.rules.settings') }}</h3>
        <div class="grid gap-3 sm:grid-cols-2">
          <Input
            :model-value="form.settings.share_link_max_days ?? null"
            type="number"
            :min="1"
            :label="t('permissions.rules.maxDays')"
            @update:model-value="(v) => (form.settings = { ...form.settings, share_link_max_days: (v as number) || null })"
          />
          <Input v-model="maxUploadMB" type="number" :min="0" :step="1" :label="t('permissions.rules.maxUpload')" />
        </div>
        <ChipInput v-model="blockedExt" :label="t('permissions.rules.blockedExt')" :placeholder="t('permissions.rules.blockedExtHint')" />
        <Toggle
          :model-value="!!form.settings.share_link_password_required"
          :label="t('permissions.rules.passwordRequired')"
          @update:model-value="(v: boolean) => (form.settings = { ...form.settings, share_link_password_required: v })"
        />
        <Toggle
          :model-value="!!form.settings.require_2fa"
          :label="t('permissions.rules.require2fa')"
          @update:model-value="(v: boolean) => (form.settings = { ...form.settings, require_2fa: v })"
        />
      </section>

      <!-- starting role for SSO groups -->
      <section class="space-y-1">
        <ChipInput v-model="ssoGroups" :label="t('permissions.rules.ssoGroups')" data-testid="role-sso-groups" />
        <p class="text-xs text-zinc-500">{{ t('permissions.rules.ssoGroupsHint') }}</p>
      </section>
    </form>

    <template #footer>
      <Button variant="ghost" @click="emit('update:modelValue', false)">{{ t('common.cancel') }}</Button>
      <Button :loading="saving" data-testid="rule-save" @click="save">{{ t('common.save') }}</Button>
    </template>
  </Modal>
</template>
