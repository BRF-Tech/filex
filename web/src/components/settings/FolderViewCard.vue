<script setup lang="ts">
/**
 * tablo:t3 - the INSTANCE default folder view.
 *
 * What a folder opens as for a person who has neither changed that folder
 * nor set a default of their own. The owner's model, 2026-09-21: "person AND
 * instance default, the same model as the theme" - folder → person → THIS →
 * filex's own (list, name ↑).
 *
 * It lives on Admin → Settings → Appearance (task #258, the owner's grouping
 * of 2026-10-15): a default look is a look, beside the default theme, not a
 * general setting. ⚠ The card draws nothing that the page's stylesheet guard
 * depends on (views/Appearance.vue suspends the operator's CSS on mount); it
 * is an ordinary form inside that page.
 *
 * ⚠ Changing it never touches a folder anybody changed, nor a person's own
 * default: it only moves what untouched folders open as, for the people who
 * have not chosen. And it is the OPERATOR's answer: the explorer is handed it
 * beside the person's document and never saves it into that document (the
 * palette made exactly that mistake once - web/src/lib/instanceThemes.ts
 * `applyInstanceDefault`). The server refuses the key to a tenant admin
 * (`allowSettingWrite`), like `ui.default_theme`, so the host draws the card
 * only for the administrator who may configure the instance.
 */
import { computed, reactive, watchEffect } from 'vue';
import { useI18n } from 'vue-i18n';
import { FolderOpen, Save } from 'lucide-vue-next';
import { readInstanceFolderDefault, setInstanceFolderDefault } from '@brftech/filex-core';

import { useSettingsStore } from '@/stores/settings';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';

import Button from '@/components/ui/Button.vue';
import Select from '@/components/ui/Select.vue';
import Checkbox from '@/components/ui/Checkbox.vue';

const { t } = useI18n();
const settings = useSettingsStore();
const toast = useToastStore();

const FV_KEY = 'ui.default_folder_view';
const fv = reactive<{ v: string; k: string; d: string; hidden: string[] }>({
  v: '',
  k: '',
  d: 'asc',
  hidden: [],
});
watchEffect(() => {
  const cur = readInstanceFolderDefault(settings.data[FV_KEY]);
  fv.v = cur.v ?? '';
  fv.k = cur.k ?? '';
  fv.d = cur.d ?? (cur.k === 'modified' ? 'desc' : 'asc');
  fv.hidden = [...(cur.hidden ?? [])];
});
const fvViewOptions = computed(() => [
  { value: '', label: t('settings.folderView.builtin', { value: t('settings.folderView.view_list') }) },
  { value: 'list', label: t('settings.folderView.view_list') },
  { value: 'grid', label: t('settings.folderView.view_grid') },
  { value: 'gallery', label: t('settings.folderView.view_gallery') },
]);
const fvSortOptions = computed(() => [
  { value: '', label: t('settings.folderView.builtin', { value: `${t('settings.folderView.sort_name')} ↑` }) },
  { value: 'name', label: t('settings.folderView.sort_name') },
  { value: 'type', label: t('settings.folderView.sort_type') },
  { value: 'modified', label: t('settings.folderView.sort_modified') },
  { value: 'size', label: t('settings.folderView.sort_size') },
]);
const fvDirOptions = computed(() => [
  { value: 'asc', label: t('settings.folderView.dir_asc') },
  { value: 'desc', label: t('settings.folderView.dir_desc') },
]);
const FV_COLUMNS = ['type', 'location', 'owner', 'modified', 'size'] as const;
function fvToggleHidden(col: string, hide: boolean) {
  const set = new Set(fv.hidden);
  if (hide) set.add(col);
  else set.delete(col);
  fv.hidden = FV_COLUMNS.filter((c) => set.has(c));
}
async function saveFolderView() {
  const value: Record<string, unknown> = {};
  if (fv.v) value.v = fv.v;
  if (fv.k) {
    value.k = fv.k;
    value.d = fv.d;
  }
  if (fv.hidden.length) value.hidden = fv.hidden;
  const raw = Object.keys(value).length ? JSON.stringify(value) : '';
  try {
    await settings.update({ [FV_KEY]: raw });
    /* This admin's own explorer follows at once; everybody else picks it up
       on their next load (it travels with the view-prefs answer). */
    setInstanceFolderDefault(raw);
    toast.success(t('settings.savedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}
</script>

<template>
  <form
    id="folder-view"
    class="card card-body space-y-3"
    data-testid="settings-folder-view"
    @submit.prevent="saveFolderView"
  >
    <h2 class="text-base font-semibold flex items-center gap-2">
      <FolderOpen class="h-4 w-4" /> {{ t('settings.folderView.title') }}
    </h2>
    <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('settings.folderView.help') }}</p>
    <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
      <Select
        :model-value="fv.v"
        :options="fvViewOptions"
        :label="t('settings.folderView.view')"
        data-testid="settings-folder-view-mode"
        @update:model-value="(v) => (fv.v = v as string)"
      />
      <Select
        :model-value="fv.k"
        :options="fvSortOptions"
        :label="t('settings.folderView.sort')"
        data-testid="settings-folder-view-sort"
        @update:model-value="(v) => ((fv.k = v as string), (fv.d = v === 'modified' ? 'desc' : 'asc'))"
      />
      <Select
        v-if="fv.k"
        :model-value="fv.d"
        :options="fvDirOptions"
        :label="t('settings.folderView.dir')"
        data-testid="settings-folder-view-dir"
        @update:model-value="(v) => (fv.d = v as string)"
      />
    </div>
    <div class="space-y-1">
      <span class="text-sm font-medium">{{ t('settings.folderView.hiddenTitle') }}</span>
      <div class="flex flex-wrap gap-4">
        <Checkbox
          v-for="c in FV_COLUMNS"
          :key="c"
          :name="`fv-hidden-${c}`"
          :model-value="fv.hidden.includes(c)"
          :label="t(`settings.folderView.col_${c}`)"
          @update:model-value="(on: boolean) => fvToggleHidden(c, on)"
        />
      </div>
    </div>
    <div class="flex justify-end pt-2">
      <Button type="submit" :loading="settings.saving">
        <Save class="h-4 w-4" />
        {{ t('common.save') }}
      </Button>
    </div>
  </form>
</template>
