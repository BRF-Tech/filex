<script setup lang="ts">
/**
 * Edit a built-in role's permissions: User (what every regular account
 * starts from — the install defaults) or Viewer (capped: a viewer can never
 * add, change, delete or share files, whatever is ticked here).
 * Administrator is not editable — it holds everything.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

import { RolesApi, type BuiltinRole, type PermCatalogue, type PermKey } from '@/api/roles';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import PermissionGrid from '@/components/PermissionGrid.vue';
import Modal from '@/components/ui/Modal.vue';
import Button from '@/components/ui/Button.vue';
import Spinner from '@/components/ui/Spinner.vue';

const props = defineProps<{ modelValue: boolean; role: BuiltinRole; catalogue: PermCatalogue }>();
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void; (e: 'saved'): void }>();

const { t } = useI18n();
const toast = useToastStore();

const set = ref<PermKey[]>([]);
const loading = ref(false);
const saving = ref(false);

const locked = computed<PermKey[]>(() =>
  props.role === 'viewer' ? props.catalogue.permissions.filter((d) => d.viewer_capped).map((d) => d.key) : [],
);
const presets = computed(() => props.catalogue.presets.filter((p) => p.name !== 'full_admin'));

watch(
  () => [props.modelValue, props.role] as const,
  async ([open]) => {
    if (!open) return;
    loading.value = true;
    try {
      set.value = [...(await RolesApi.getDefaults(props.role)).permissions];
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
  saving.value = true;
  try {
    await RolesApi.putDefaults(set.value, props.role);
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
    :title="t('permissions.rules.editBuiltin', { role: t(`users.roles.${role}`) })"
    size="xl"
    @update:model-value="(v: boolean) => emit('update:modelValue', v)"
  >
    <div v-if="loading" class="text-center text-zinc-500"><Spinner /></div>
    <div v-else class="space-y-3" :data-testid="`builtin-role-${role}`">
      <p v-if="role === 'viewer'" class="text-xs text-amber-700 dark:text-amber-400">{{ t('permissions.rules.viewerCeilingNote') }}</p>
      <div class="flex items-center gap-2 flex-wrap text-sm">
        <span class="text-zinc-500">{{ t('permissions.presets.label') }}:</span>
        <Button v-for="p in presets" :key="p.name" type="button" size="sm" variant="outline" @click="applyPreset(p.name)">
          {{ t(`permissions.presets.${p.name}`) }}
        </Button>
      </div>
      <PermissionGrid v-model:set="set" mode="set" :catalogue="catalogue.permissions" :locked="locked" />
    </div>
    <template #footer>
      <Button variant="ghost" @click="emit('update:modelValue', false)">{{ t('common.cancel') }}</Button>
      <Button :loading="saving" data-testid="builtin-role-save" @click="save">{{ t('common.save') }}</Button>
    </template>
  </Modal>
</template>
