<script setup lang="ts">
/**
 * Admin → Plugins → Default apps (filex 0.50, docs/APP-PLUGINS.md → Default
 * apps): which handler opens each kind of file, and which draws its
 * thumbnail.
 *
 * One row per kind something besides filex handles, and per kind the
 * administrator already changed - its extension and type, who opens it and
 * who draws it, in order, and whether that order is the default or a choice.
 * The row's Actions: Edit (FileTypeEditor: both lists, on/off, up/down) and
 * Back to the default.
 *
 * ⚠ The explorer's table (DataTable), like every table of the product - one
 * root per cell. ⚠ Instance-wide, like the apps: a tenant administrator gets
 * 403 and is told so. A caller who may only read (an API key, the demo) gets
 * the table and an editor without its controls.
 *
 * A change invalidates the explorer's cached actions list: it carries the
 * open rules (`open_rules`), so an explorer on this page opens the next file
 * by the new order.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { FileCog, RefreshCcw } from 'lucide-vue-next';
import { DataTable, invalidatePluginActions, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { FileTypesApi, type FileTypeCap, type FileTypeChange, type FileTypeKind } from '@/api/fileTypes';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { handlerLabel } from '@/lib/fileTypes';

import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import FileTypeEditor from './FileTypeEditor.vue';

const props = withDefaults(defineProps<{
  /** The tab is the one on screen: its list is read again each time it is
   *  opened (an install on the Apps tab changes it). */
  active?: boolean;
}>(), { active: true });

const { t, locale } = useI18n();
const toast = useToastStore();

const kinds = ref<FileTypeKind[]>([]);
const enabled = ref(true);
const editable = ref(false);
const loading = ref(false);
const forbidden = ref(false);
const busy = ref(false);

const editing = ref<FileTypeKind | null>(null);
const editorOpen = ref(false);

async function load() {
  loading.value = true;
  forbidden.value = false;
  try {
    apply(await FileTypesApi.list());
  } catch (e: unknown) {
    const err = e as { response?: { status?: number } };
    if (err?.response?.status === 403) forbidden.value = true;
    else toast.error(extractError(e, t('errors.loadFailed')));
  } finally {
    loading.value = false;
  }
}

function apply(ans: { kinds: FileTypeKind[]; enabled: boolean; editable: boolean }) {
  kinds.value = ans.kinds;
  enabled.value = ans.enabled;
  editable.value = ans.editable;
  if (editing.value) editing.value = ans.kinds.find((k) => k.ext === editing.value?.ext) ?? null;
}

watch(
  () => props.active,
  (on) => {
    if (on) void load();
  },
  { immediate: true },
);

/** A capability's handlers that are on, as one line: "1. draw.io · 2. filex". */
function onLine(c: FileTypeCap): string {
  return c.on.map((h, i) => `${i + 1}. ${handlerLabel(h, t, locale.value)}`).join(' · ');
}

function offLine(c: FileTypeCap): string {
  return c.off.length ? t('defaultApps.offList', { names: c.off.map((h) => handlerLabel(h, t, locale.value)).join(', ') }) : '';
}

const columns = computed<DataColumn<FileTypeKind>[]>(() => [
  { id: 'ext', label: t('defaultApps.col.kind'), sortable: true, width: 170, lead: true, sortValue: (r) => r.ext },
  { id: 'open', label: t('defaultApps.col.open'), width: 300, sortValue: (r) => onLine(r.open) },
  { id: 'thumbnail', label: t('defaultApps.col.thumbnail'), width: 300, sortValue: (r) => onLine(r.thumbnail) },
  {
    id: 'status',
    label: t('defaultApps.col.status'),
    sortable: true,
    width: 130,
    sortValue: (r) => (r.open.custom || r.thumbnail.custom ? 1 : 0),
  },
]);

function rowActions(row: FileTypeKind): ContextAction[] {
  const custom = row.open.custom || row.thumbnail.custom;
  return [
    { key: 'edit', label: editable.value ? t('defaultApps.actions.edit') : t('defaultApps.actions.view'), icon: 'details' },
    {
      key: 'reset',
      label: t('defaultApps.actions.reset'),
      icon: 'restore',
      hidden: !editable.value,
      disabled: !custom || busy.value,
      title: custom ? undefined : t('defaultApps.actions.resetNothing'),
    },
  ];
}

function onRowAction(key: string, row: FileTypeKind) {
  if (key === 'edit') {
    editing.value = row;
    editorOpen.value = true;
  } else if (key === 'reset') void reset(row.ext);
}

async function save(change: FileTypeChange) {
  const ext = editing.value?.ext;
  if (!ext) return;
  busy.value = true;
  try {
    apply(await FileTypesApi.put(ext, change));
    invalidatePluginActions();
    toast.success(t('defaultApps.saved', { ext }));
    editorOpen.value = false;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  } finally {
    busy.value = false;
  }
}

async function reset(ext: string) {
  busy.value = true;
  try {
    apply(await FileTypesApi.reset(ext));
    invalidatePluginActions();
    toast.success(t('defaultApps.resetDone', { ext }));
    editorOpen.value = false;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section class="space-y-4" data-testid="default-apps-tab">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <FileCog class="h-6 w-6 text-brand-600 dark:text-brand-400" />
        <h1 class="text-xl font-semibold">{{ t('defaultApps.title') }}</h1>
      </div>
      <Button v-if="!forbidden" variant="outline" size="sm" :loading="loading" data-testid="default-apps-refresh" @click="load">
        <RefreshCcw class="h-4 w-4" />
        {{ t('common.refresh') }}
      </Button>
    </header>

    <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('defaultApps.desc') }}</p>

    <div
      v-if="forbidden"
      class="rounded-xl border border-zinc-200 bg-white p-6 text-sm text-zinc-600 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400"
      data-testid="default-apps-forbidden"
    >
      {{ t('defaultApps.supertenantOnly') }}
    </div>

    <template v-else>
      <p
        v-if="!enabled"
        class="rounded-lg border border-zinc-200 bg-zinc-50 p-3 text-sm text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300"
        data-testid="default-apps-off"
      >
        {{ t('defaultApps.off') }}
      </p>
      <p
        v-else-if="!editable && !loading"
        class="rounded-lg border border-zinc-200 bg-zinc-50 p-3 text-xs text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300"
        data-testid="default-apps-readonly"
      >
        {{ t('defaultApps.readonly') }}
      </p>

      <DataTable
        table-id="admin.plugins.defaults"
        :columns="columns"
        :rows="kinds"
        row-key="ext"
        :loading="loading"
        :aria-label="t('defaultApps.title')"
        :row-actions="(row: FileTypeKind) => rowActions(row)"
        :row-actions-test-id="(row: FileTypeKind) => `default-apps-edit-${row.ext}`"
        @row-action="(key: string, row: FileTypeKind) => onRowAction(key, row)"
      >
        <template #empty>
          <EmptyState :icon="FileCog" :title="t('defaultApps.empty')" size="sm" />
        </template>
        <template #cell-ext="{ row }">
          <div class="min-w-0 max-w-full" :data-testid="`default-apps-row-${row.ext}`">
            <bdi dir="ltr" class="tbl-clamp font-mono">.{{ row.ext }}</bdi>
            <span v-if="row.mime" class="tbl-sub tbl-clamp" :title="row.mime"><bdi dir="ltr">{{ row.mime }}</bdi></span>
          </div>
        </template>
        <template #cell-open="{ row }">
          <div :data-testid="`default-apps-open-${row.ext}`">
            <span class="tbl-clamp" :title="onLine(row.open)">{{ onLine(row.open) || t('defaultApps.none') }}</span>
            <span v-if="row.open.off.length" class="tbl-sub" :title="offLine(row.open)">{{ offLine(row.open) }}</span>
          </div>
        </template>
        <template #cell-thumbnail="{ row }">
          <div :data-testid="`default-apps-thumbnail-${row.ext}`">
            <span class="tbl-clamp" :title="onLine(row.thumbnail)">{{ onLine(row.thumbnail) || t('defaultApps.none') }}</span>
            <span v-if="row.thumbnail.off.length" class="tbl-sub" :title="offLine(row.thumbnail)">{{ offLine(row.thumbnail) }}</span>
          </div>
        </template>
        <template #cell-status="{ row }">
          <Badge :tone="row.open.custom || row.thumbnail.custom ? 'brand' : 'zinc'" size="xs" :data-testid="`default-apps-status-${row.ext}`">
            {{ row.open.custom || row.thumbnail.custom ? t('defaultApps.status.custom') : t('defaultApps.status.default') }}
          </Badge>
        </template>
      </DataTable>
    </template>

    <FileTypeEditor
      v-model="editorOpen"
      :kind="editing"
      :editable="editable"
      :busy="busy"
      @save="save"
      @reset="editing && reset(editing.ext)"
    />
  </section>
</template>
