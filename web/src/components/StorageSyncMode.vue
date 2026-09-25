<script setup lang="ts">
/**
 * How filex keeps a storage's catalog current — the sync mode — and, for the
 * lazy catalog (docs/LAZY-CATALOGUE.md), its behavior and watch budget.
 *
 * One component for both storage forms (new and edit), so they cannot offer
 * different modes. The lazy settings are drawn by StorageDriverFields from the
 * descriptor's `lazy_fields`, like every other storage setting, and the mode
 * itself is offered only for a driver whose descriptor carries them: the
 * server refuses `lazy` anywhere else, and a choice the server will refuse is
 * not a choice.
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';

import type { StorageDriver, SyncMode } from '@/api/types';
import { useStorageDriversStore } from '@/stores/storageDrivers';
import Select from './ui/Select.vue';
import StorageDriverFields from './StorageDriverFields.vue';

const props = defineProps<{
  driver: StorageDriver | undefined;
  /** The storage's sync mode; '' reads as the server default (poll). */
  mode: SyncMode | 'push' | '' | undefined;
  config: Record<string, unknown>;
}>();

const emit = defineEmits<{
  (e: 'update:mode', v: SyncMode): void;
  (e: 'update:config', v: Record<string, unknown>): void;
}>();

const { t } = useI18n();
const drivers = useStorageDriversStore();

const lazyFields = computed(() => drivers.lazyFields(props.driver));
const current = computed<string>(() => props.mode || 'poll');

const options = computed(() => {
  const modes: string[] = ['poll', 'fsnotify', 'ondemand'];
  if (lazyFields.value.length || current.value === 'lazy') modes.push('lazy');
  // A row written before `push` was refused still shows what it says.
  if (current.value === 'push') modes.push('push');
  return modes.map((m) => ({ value: m, label: t('storages.modeLabel.' + m) }));
});

const hint = computed(() => (current.value === 'push' ? '' : t('storages.modeHelp.' + current.value)));
</script>

<template>
  <div class="space-y-3" data-testid="storage-sync-mode">
    <Select
      :model-value="current"
      :options="options"
      :label="t('storages.fields.syncMode')"
      :hint="hint"
      name="sync_mode"
      @update:model-value="(v) => emit('update:mode', v as SyncMode)"
    />
    <StorageDriverFields
      v-if="current === 'lazy' && lazyFields.length"
      :model-value="config"
      :fields="lazyFields"
      data-testid="storage-lazy-fields"
      @update:model-value="(v) => emit('update:config', v)"
    />
  </div>
</template>
