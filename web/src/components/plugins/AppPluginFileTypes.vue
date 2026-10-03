<script setup lang="ts">
/**
 * The File types group (filex 0.50, docs/APP-PLUGINS.md → Default apps):
 * where an app goes for each kind it opens or draws thumbnails of - first,
 * after the ones there now, or off - with the server's default picked.
 *
 * ONE group, three places it is asked (the maintainer, 2026-10-01): an install's
 * review, an upgrade's review (only the kinds the new version ADDS; the
 * order the administrator has for the others is not reopened) and the
 * review of an install request an administrator approves. Each of them sends
 * only the rows changed from the default (lib/fileTypes `changedPlacements`).
 *
 * ⚠ The explorer's table (DataTable), like every table of the product.
 */
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { ChoiceButtons, DataTable, type DataColumn } from '@brftech/filex-core';

import type { AppPluginInstallKind, AppPluginPlace } from '@/api/appPlugins';
import { fileTypeKey, handlerLabel } from '@/lib/fileTypes';

const props = defineProps<{
  rows: AppPluginInstallKind[];
  /** The choice per row (`fileTypeKey`). */
  modelValue: Record<string, AppPluginPlace>;
  /** The reason it is asked: an install (and an approved request) or an upgrade. */
  mode?: 'install' | 'upgrade';
}>();
const emit = defineEmits<{ (e: 'update:modelValue', v: Record<string, AppPluginPlace>): void }>();

const { t, locale } = useI18n();

const columns = computed<DataColumn<AppPluginInstallKind>[]>(() => [
  { id: 'ext', label: t('appPlugins.wizard.fileTypes.col.kind'), width: 130, lead: true, sortValue: (r) => r.ext },
  { id: 'capability', label: t('appPlugins.wizard.fileTypes.col.capability'), width: 120 },
  { id: 'current', label: t('appPlugins.wizard.fileTypes.col.current'), width: 190 },
  { id: 'place', label: t('appPlugins.wizard.fileTypes.col.place'), width: 300, hideable: false },
]);

const placeOptions = computed(() =>
  (['first', 'last', 'off'] as AppPluginPlace[]).map((p) => ({ value: p, label: t(`appPlugins.wizard.fileTypes.place.${p}`) })),
);

function currentLine(r: AppPluginInstallKind): string {
  return r.current.length ? r.current.map((h) => handlerLabel(h, t, locale.value)).join(', ') : t('appPlugins.wizard.fileTypes.nobody');
}

function choose(r: AppPluginInstallKind, v: string | string[]) {
  emit('update:modelValue', { ...props.modelValue, [fileTypeKey(r)]: v as AppPluginPlace });
}
</script>

<template>
  <div
    v-if="rows.length"
    class="space-y-2 rounded-lg border border-zinc-200 p-3 text-sm dark:border-zinc-800"
    data-testid="install-file-types"
  >
    <h3 class="text-sm font-semibold">{{ t('appPlugins.wizard.fileTypes.title') }}</h3>
    <p class="text-xs text-zinc-600 dark:text-zinc-400" data-testid="install-file-types-desc">
      {{ mode === 'upgrade' ? t('appPlugins.wizard.fileTypes.descUpgrade') : t('appPlugins.wizard.fileTypes.desc') }}
    </p>
    <DataTable
      table-id="admin.apps.install.filetypes"
      :columns="columns"
      :rows="rows"
      :row-key="fileTypeKey"
      :aria-label="t('appPlugins.wizard.fileTypes.title')"
    >
      <template #cell-ext="{ row }">
        <div class="min-w-0 max-w-full">
          <bdi dir="ltr" class="tbl-clamp font-mono">.{{ row.ext }}</bdi>
          <span v-if="row.mime" class="tbl-sub tbl-clamp" :title="row.mime"><bdi dir="ltr">{{ row.mime }}</bdi></span>
        </div>
      </template>
      <template #cell-capability="{ row }">
        <span class="tbl-clamp">{{ t(`appPlugins.wizard.fileTypes.capability.${row.capability}`) }}</span>
      </template>
      <template #cell-current="{ row }">
        <span class="tbl-clamp" :title="currentLine(row)">{{ currentLine(row) }}</span>
      </template>
      <template #cell-place="{ row }">
        <div :data-testid="`install-file-type-${row.capability}-${row.ext}`">
          <ChoiceButtons
            :model-value="modelValue[fileTypeKey(row)]"
            :options="placeOptions"
            :aria-label="t('appPlugins.wizard.fileTypes.col.place')"
            :testid-prefix="`install-place-${row.capability}-${row.ext}`"
            @update:model-value="(v: string | string[]) => choose(row, v)"
          />
        </div>
      </template>
    </DataTable>
  </div>
</template>
