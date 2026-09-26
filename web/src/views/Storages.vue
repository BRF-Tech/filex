<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue';
import { useRouter, RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Database, GripVertical, Plus, RefreshCcw, RotateCcw } from 'lucide-vue-next';

import { useStoragesStore } from '@/stores/storages';
import { useSyncNow } from '@/composables/useSyncNow';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { StorageRef } from '@/api/types';
import { fileCountOf, formatBytes, formatNumber, formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Modal from '@/components/ui/Modal.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import Spinner from '@/components/ui/Spinner.vue';
import { syncStateLabel, syncTone } from '@/lib/syncTone';
import {
  DataTable,
  StorageTags,
  coveragePercent,
  dropStorageAt,
  moveStorage,
  useReorderDrag,
  type ContextAction,
  type DataColumn,
} from '@brftech/filex-core';

const { t, locale } = useI18n();
const router = useRouter();
const storages = useStoragesStore();
const toast = useToastStore();

const deleteTarget = ref<StorageRef | null>(null);
const deleting = ref(false);

// Replica targets live in their own `replication_targets` table now
// (v0.1.18+). `storages.items` only contains primaries; no client-side
// filtering needed.

async function load() {
  await storages.fetch();
}

const syncNow = useSyncNow();

function syncOne(s: StorageRef) {
  void syncNow.press(s.id, s.name);
}

async function confirmDelete() {
  if (!deleteTarget.value) return;
  deleting.value = true;
  try {
    const target = deleteTarget.value;
    if ((await storages.remove(target.id)) === 'deleted') toast.success(t('storages.deletedOk'));
    else toast.info(t('storages.deleteStillRunning', { name: target.name }));
    deleteTarget.value = null;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    deleting.value = false;
  }
}

/* === #57 — the order everybody's navigation panel starts from ============
 * The storages were cards in a grid, in creation order, with no way to say
 * which one belongs at the top. They are rows of THE table now (DataTable, the
 * explorer's — never a second table), in the ADMINISTRATOR's order, which is
 * what every person's navigation panel shows until they arrange their own
 * (packages/core lib/storageOrder).
 *
 * Three ways to move a row, one answer (`PUT /api/admin/storages/order` with
 * the whole list):
 *   · drag it by its handle (`useReorderDrag`, the same gesture the
 *     navigation panel's rows use; a finger drags the handle at once);
 *   · Move up / Move down in its Actions menu — the keyboard's way, and the
 *     way for anybody who cannot drag;
 *   · the arrow keys on the focused handle.
 * "Reset to default order" puts every storage back in creation order.
 *
 * ⚠ The columns do not sort. A table that could be sorted by size would show
 * an order that is NOT the one being edited, and a drag in it would mean
 * nothing anybody could predict. */

/** The rows in the order the server keeps (it lists by position, then creation). */
const rows = computed(() => storages.items);
/** The rows as the shared order helpers see them: keyed by id. */
const keyed = computed(() => rows.value.map((s) => ({ name: String(s.id) })));

/** Somebody has placed at least one storage — "Reset to default order" has something to undo. */
const hasAdminOrder = computed(() => rows.value.some((s) => typeof s.sort_order === 'number'));

const tableEl = ref<HTMLElement | null>(null);
const handleEl = (id: string) =>
  tableEl.value?.querySelector<HTMLElement>(`[data-testid="storage-order-handle-${id}"]`) ?? null;

async function saveOrder(keys: string[] | null, focusId?: string) {
  if (keys === null) return;
  try {
    await storages.setOrder(keys.map(Number));
    toast.success(t('storages.order.saved'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
  // ⚠ The row's element moved (the list is keyed), and a moved node loses
  // focus: a keyboard user who pressed ↓ on a handle keeps pressing it.
  if (focusId) {
    await nextTick();
    handleEl(focusId)?.focus();
  }
}

async function resetOrder() {
  try {
    await storages.setOrder([]);
    toast.success(t('storages.order.resetDone'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

const reorder = useReorderDrag({
  keys: () => keyed.value.map((k) => k.name),
  rowEl: (key) => tableEl.value?.querySelector(`[data-storage-row="${key}"]`),
  // A handle is an explicit "move me": a finger drags it at once, and its
  // `touch-action: none` means it never scrolls the page instead.
  touchDelayMs: 0,
  onDrop: (key, gap) => void saveOrder(dropStorageAt(keyed.value, key, gap), key),
});
const { dragKey, dropGap, onClickCapture } = reorder;

function onHandleKey(s: StorageRef, ev: KeyboardEvent) {
  const delta = ev.key === 'ArrowUp' ? -1 : ev.key === 'ArrowDown' ? 1 : 0;
  if (!delta) return;
  ev.preventDefault();
  void saveOrder(moveStorage(keyed.value, String(s.id), delta), String(s.id));
}

const columns = computed<DataColumn<StorageRef>[]>(() => [
  { id: 'name', label: t('common.name'), lead: true, width: 320 },
  {
    id: 'size',
    label: t('storages.columns.size'),
    width: 200,
    format: (s) =>
      `${formatBytes(s.stats?.total_size_bytes ?? s.total_bytes ?? 0, locale.value)} · ${t(
        'storages.fileCount',
        { n: formatNumber(fileCountOf(s), locale.value) },
        fileCountOf(s),
      )}`,
  },
  { id: 'sync', label: t('storages.columns.sync'), width: 150 },
  { id: 'last_sync', label: t('storages.columns.lastSync'), width: 300 },
]);

function rowActions(s: StorageRef): ContextAction[] {
  const i = rows.value.findIndex((r) => r.id === s.id);
  return [
    { key: 'sync', label: t('common.syncNow'), icon: 'refresh', disabled: syncNow.isBusy(s.id) },
    { key: 'edit', label: t('common.edit'), icon: 'rename' },
    { divider: true, key: 'order-sep', label: '' },
    { key: 'move-up', label: t('storages.order.moveUp'), icon: 'move-up', disabled: i <= 0 },
    { key: 'move-down', label: t('storages.order.moveDown'), icon: 'move-down', disabled: i >= rows.value.length - 1 },
    { divider: true, key: 'delete-sep', label: '' },
    { key: 'delete', label: t('common.delete'), icon: 'delete', danger: true },
  ];
}

function onRowAction(key: string, s: StorageRef) {
  if (key === 'sync') syncOne(s);
  else if (key === 'edit') void router.push({ name: 'storages.edit', params: { id: s.id } });
  else if (key === 'move-up') void saveOrder(moveStorage(keyed.value, String(s.id), -1));
  else if (key === 'move-down') void saveOrder(moveStorage(keyed.value, String(s.id), 1));
  else if (key === 'delete') deleteTarget.value = s;
}

function rowClass(s: StorageRef, i: number) {
  return {
    'fe-list__row--dragging': dragKey.value === String(s.id),
    'fe-list__row--drop-before': dropGap.value === i,
    'fe-list__row--drop-after': dropGap.value === rows.value.length && i === rows.value.length - 1,
  };
}

onMounted(load);
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('storages.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('storages.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="outline" size="sm" @click="load" :loading="storages.loading">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button @click="router.push({ name: 'storages.new' })">
          <Plus class="h-4 w-4" />
          {{ t('storages.addNew') }}
        </Button>
      </div>
    </div>

    <div v-if="storages.loading && storages.empty" class="card card-body text-center text-zinc-500">
      <Spinner />
    </div>

    <EmptyState
      v-else-if="storages.empty"
      :icon="Database"
      :title="t('dashboard.noStorages')"
      :description="t('storages.subtitle')"
    >
      <template #action>
        <Button @click="router.push({ name: 'storages.new' })">
          <Plus class="h-4 w-4" />
          {{ t('storages.addNew') }}
        </Button>
      </template>
    </EmptyState>

    <!-- The click a drag's release produces is not "open this storage":
         swallowed on its way down (useReorderDrag). -->
    <div v-else ref="tableEl" @click.capture="onClickCapture">
      <DataTable
        table-id="admin.storages"
        :columns="columns"
        :rows="rows"
        row-key="id"
        :loading="storages.loading"
        data-testid="storages-table"
        :row-class="rowClass"
        :row-attrs="(s: StorageRef) => ({ 'data-storage-row': String(s.id) })"
        :row-actions="(s: StorageRef) => rowActions(s)"
        :row-actions-test-id="(s: StorageRef) => `storage-actions-${s.id}`"
        @row-action="(key: string, s: StorageRef) => onRowAction(key, s)"
      >
        <template #toolbar>
          <p class="text-xs text-zinc-500 dark:text-zinc-400 min-w-0 flex-1" data-testid="storages-order-hint">
            {{ t('storages.order.hint') }}
          </p>
          <Button
            size="xs"
            variant="outline"
            :disabled="!hasAdminOrder"
            :title="hasAdminOrder ? undefined : t('storages.order.resetNone')"
            data-testid="storages-order-reset"
            @click="resetOrder"
          >
            <RotateCcw class="h-3.5 w-3.5" />
            {{ t('storages.order.reset') }}
          </Button>
        </template>

        <template #cell-name="{ row }">
          <div class="flex items-center gap-2 min-w-0">
            <button
              type="button"
              class="fe-reorder-handle"
              :title="t('storages.order.handle', { name: row.name })"
              :aria-label="t('storages.order.handle', { name: row.name })"
              :data-testid="`storage-order-handle-${row.id}`"
              @pointerdown="reorder.onPointerDown(String(row.id), $event)"
              @keydown="onHandleKey(row, $event)"
            >
              <GripVertical class="h-4 w-4" aria-hidden="true" />
            </button>
            <RouterLink
              :to="{ name: 'storages.edit', params: { id: row.id } }"
              class="truncate font-semibold text-zinc-900 dark:text-zinc-100 hover:text-brand-600 dark:hover:text-brand-400"
            >
              {{ row.name }}
            </RouterLink>
            <!-- ⚠ One vocabulary with Connections → Storages: this list
                 said "RO" and "local", that one "SALT OKUNUR" and "LOCAL"
                 (QA #34). Both draw the same StorageTags now. -->
            <StorageTags :driver="row.driver" :read-only="row.read_only" :enabled="row.enabled" :locale="locale" />
          </div>
        </template>

        <template #cell-sync="{ row }">
          <!-- ⚠ `poll`, not `ondemand`: an unset sync_mode is defaulted to poll
               by the backend (handlers/storages.go), so the badge was naming
               the OPPOSITE mode to the one running. The state in words
               (syncStateLabel), not the wire value "ok". -->
          <Badge :tone="syncTone(row.last_sync_state)" dot data-testid="storage-sync-state">
            {{ syncStateLabel(row.last_sync_state, t) || t('storages.modeLabel.' + (row.sync_mode || 'poll')) }}
          </Badge>
        </template>

        <template #cell-last_sync="{ row }">
          <!-- ONE root: a cell slot is a flex row, and a second root with a
               top margin would draw over the first (lesson #377). -->
          <div class="min-w-0 text-xs text-zinc-500 dark:text-zinc-400">
            <p class="truncate">
              {{
                row.last_sync_at
                  ? t('dashboard.lastSyncAt', { when: formatRelative(row.last_sync_at, locale) })
                  : t('storages.notYetSynced')
              }}
            </p>
            <!-- A lazily cataloged storage says how far its catalog has come
                 (docs/LAZY-CATALOGUE.md); the storage page has the detail. -->
            <p v-if="row.catalogue && !row.catalogue.complete" class="truncate" data-testid="storage-catalog-line">
              {{
                row.catalogue.fill === 'on_open'
                  ? t('storages.catalog.listOnOpen')
                  : t('storages.catalog.listFilling', { pct: coveragePercent(row.catalogue) })
              }}
            </p>
            <p v-if="row.last_sync_error" class="truncate text-rose-600 dark:text-rose-400" :title="row.last_sync_error">
              {{ row.last_sync_error }}
            </p>
          </div>
        </template>
      </DataTable>
    </div>

    <Modal
      :model-value="deleteTarget !== null"
      :title="t('common.delete')"
      size="sm"
      @update:model-value="(v) => (v ? null : (deleteTarget = null))"
    >
      <p class="text-sm text-zinc-700 dark:text-zinc-300">
        {{ t('storages.deleteConfirm', { name: deleteTarget?.name }) }}
      </p>
      <template #footer>
        <Button variant="ghost" @click="deleteTarget = null">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="deleting" @click="confirmDelete">
          {{ t('common.yesDelete') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>
