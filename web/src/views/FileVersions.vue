<script setup lang="ts">
/**
 * FileVersions — version history of one node (file).
 *
 * Route: /admin/files/:nodeId/versions
 *
 * Backend (`internal/api/handlers/versions.go`):
 *   GET  /api/files/versions?node_id=N           → { versions, node_id }
 *   POST /api/files/versions/restore             → { ok: true }
 *
 * Linking back into Explore: the SFC's right-click context menu is owned
 * by `packages/core/` and we can't extend it from the embedder. Instead
 * users reach this page through the small lookup form on /admin/files.
 * Restoring redirects back to /admin/explore so the user can verify in
 * the file tree.
 *
 * Download note: `versions.go` does not currently expose a streamed
 * download for a recorded version (there is no `/api/files/read?version=N`).
 * The Download button is therefore disabled with an explanatory tooltip;
 * once the backend ships a stream endpoint we'll wire it through here.
 */
import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ArrowLeft, History, RotateCcw, Trash2, Download, ChevronRight } from 'lucide-vue-next';

import { useToastStore } from '@/stores/toast';
import { useAuthStore } from '@/stores/auth';
import { versionsApi, type NodeVersion, type VersionedFile } from '@/api/versions';
import { extractError } from '@/api/client';
import { formatBytes, formatDate } from '@/lib/format';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Modal from '@/components/ui/Modal.vue';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

const { t, locale } = useI18n();
const route = useRoute();
const router = useRouter();
const toast = useToastStore();
const auth = useAuthStore();

const nodeId = computed(() => Number(route.params.nodeId));
const versions = ref<NodeVersion[]>([]);
/** The file the history is of — named on the page, not "Node #31". */
const file = ref<VersionedFile | null>(null);
const loading = ref(false);
const error = ref<string | null>(null);

const restoreTarget = ref<NodeVersion | null>(null);
const snapshotCurrent = ref(true);
const restoring = ref(false);
const restoreOpen = computed({
  get: () => restoreTarget.value !== null,
  set: (v: boolean) => {
    if (!v) restoreTarget.value = null;
  },
});

const purgeTarget = ref<NodeVersion | null>(null);
const purging = ref(false);
const purgeOpen = computed({
  get: () => purgeTarget.value !== null,
  set: (v: boolean) => {
    if (!v) purgeTarget.value = null;
  },
});

const isAdmin = computed(() => auth.isAdmin);

/* The explorer's table (DataTable), remembered under `admin.file-versions`.
 * A file's versions arrive all at once, so the table's own sort is honest. */
const columns = computed<DataColumn<NodeVersion>[]>(() => [
  { id: 'version_n', label: t('versions.col.version'), sortable: true, sortDir: 'desc', width: 160 },
  { id: 'size', label: t('versions.col.size'), sortable: true, align: 'right', width: 110 },
  {
    id: 'created_at',
    label: t('versions.col.createdAt'),
    sortable: true,
    sortDir: 'desc',
    width: 180,
    sortValue: (v) => (v.created_at ? Date.parse(v.created_at) : null),
  },
  { id: 'etag', label: t('versions.col.etag'), sortable: true, width: 160 },
]);

async function load() {
  if (!Number.isFinite(nodeId.value) || nodeId.value <= 0) {
    error.value = t('versions.notFound');
    return;
  }
  loading.value = true;
  error.value = null;
  try {
    const h = await versionsApi.history(nodeId.value);
    versions.value = h.versions;
    file.value = h.file;
  } catch (e: unknown) {
    error.value = extractError(e, t('versions.loadFailed'));
  } finally {
    loading.value = false;
  }
}

function confirmRestore(v: NodeVersion) {
  restoreTarget.value = v;
  snapshotCurrent.value = true;
}

async function doRestore() {
  if (!restoreTarget.value) return;
  restoring.value = true;
  try {
    await versionsApi.restore(nodeId.value, restoreTarget.value.id, snapshotCurrent.value);
    toast.success(t('versions.restored', { n: restoreTarget.value.version_n }));
    restoreTarget.value = null;
    // The user wants to verify the live file — bounce them back to Explore.
    router.push({ name: 'explore' });
  } catch (e: unknown) {
    toast.error(extractError(e, t('versions.restoreFailed')));
  } finally {
    restoring.value = false;
  }
}

function confirmPurge(v: NodeVersion) {
  purgeTarget.value = v;
}

async function doPurge() {
  if (!purgeTarget.value) return;
  purging.value = true;
  try {
    await versionsApi.hardDelete(purgeTarget.value.id);
    toast.success(t('versions.purged', { n: purgeTarget.value.version_n }));
    purgeTarget.value = null;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('versions.purgeFailed')));
  } finally {
    purging.value = false;
  }
}

onMounted(load);

/** The row's verbs, behind its one pinned `Actions` control.
 *
 * ⚠ Download stays VISIBLE and disabled, with the reason in its title, rather
 * than being dropped from the menu: a verb that is currently impossible reads
 * as a rule when it is greyed and as a missing feature when it is absent —
 * the same rule ContextMenu states for the explorer. Purge stays admin-only
 * (`hidden`), which is a permission, not a state. */
function rowActions(_row: NodeVersion): ContextAction[] {
  return [
    {
      key: 'download',
      label: t('versions.download'),
      icon: 'download',
      disabled: true,
      title: t('versions.downloadDisabled'),
    },
    { key: 'restore', label: t('versions.restore'), icon: 'restore' },
    {
      key: 'purge',
      label: t('versions.purge'),
      icon: 'delete',
      danger: true,
      hidden: !isAdmin.value,
      title: t('versions.purgeTooltip'),
    },
  ];
}

function onRowAction(key: string, row: NodeVersion) {
  if (key === 'restore') confirmRestore(row);
  else if (key === 'purge') confirmPurge(row);
}
</script>

<template>
  <section class="space-y-4">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <div class="flex items-center gap-2 text-sm text-zinc-500 dark:text-zinc-400">
          <button
            type="button"
            class="inline-flex items-center gap-1 hover:text-zinc-900 dark:hover:text-zinc-100"
            @click="router.push({ name: 'admin-files' })"
          >
            <ArrowLeft class="h-4 w-4" />
            {{ t('versions.backToFiles') }}
          </button>
          <ChevronRight class="h-3.5 w-3.5 opacity-60" />
          <!-- ⚠ The file by name and place — it said "Node #31", an id the
               operator had to have typed in (QA #30). -->
          <span v-if="file?.name" class="truncate" data-testid="versions-file">
            <bdi>{{ file.name }}</bdi>
            <span v-if="file.path" class="text-zinc-400"> · {{ file.storage_name ? `${file.storage_name}:` : '' }}<bdi>{{ file.path }}</bdi></span>
          </span>
        </div>
        <h1 class="mt-2 flex items-center gap-2 text-2xl font-semibold text-zinc-900 dark:text-zinc-100">
          <History class="h-6 w-6 text-zinc-500 dark:text-zinc-400" />
          {{ t('versions.title') }}
        </h1>
        <p class="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{{ t('versions.subtitle') }}</p>
      </div>
      <Button variant="ghost" size="sm" @click="load" :disabled="loading">
        {{ t('common.refresh') }}
      </Button>
    </header>

    <div v-if="error" class="rounded-md border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-700 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-300">
      {{ error }}
    </div>

    <DataTable
      table-id="admin.file-versions"
      :columns="columns"
      :rows="versions"
      :loading="loading"
      row-key="id"
      :row-actions="(row: NodeVersion) => rowActions(row)"
      :row-actions-test-id="(row: NodeVersion) => `version-actions-${row.id}`"
      @row-action="(key: string, row: NodeVersion) => onRowAction(key, row)"
    >
      <template #empty>
        <p class="font-medium">{{ t('versions.empty') }}</p>
        <p class="tbl-sub">{{ t('versions.emptyDescription') }}</p>
      </template>

      <!-- "Newest" is a property of the row's POSITION in the SERVER's
           answer, which arrives newest-first; `indexOf` asks that array rather
           than the order on screen, so a person who sorts by size still sees
           the badge on the newest version rather than on whichever row the
           sort put first. -->
      <template #cell-version_n="{ row }">
        <span class="inline-flex items-center gap-2">
          <Badge :tone="versions.indexOf(row) === 0 ? 'brand' : 'zinc'" size="sm">v{{ row.version_n }}</Badge>
          <span v-if="versions.indexOf(row) === 0" class="text-xs">{{ t('versions.newest') }}</span>
        </span>
      </template>
      <template #cell-size="{ row }">
        <span class="tabular-nums">{{ formatBytes(row.size, locale) }}</span>
      </template>
      <template #cell-created_at="{ row }">
        <span class="whitespace-nowrap">{{ formatDate(row.created_at, locale) }}</span>
      </template>
      <template #cell-etag="{ row }">
        <span v-if="row.etag" class="tbl-mono" :title="row.etag">{{ row.etag.slice(0, 12) }}…</span>
        <span v-else>—</span>
      </template>
    </DataTable>

    <Modal v-model="restoreOpen" :title="t('versions.restoreModalTitle')">
      <p v-if="restoreTarget" class="text-sm text-zinc-700 dark:text-zinc-300">
        {{ t('versions.restoreModalBody', { n: restoreTarget.version_n }) }}
      </p>
      <label class="mt-3 inline-flex items-center gap-2 text-sm text-zinc-700 dark:text-zinc-300">
        <input
          v-model="snapshotCurrent"
          type="checkbox"
          class="rounded border-zinc-300 dark:border-zinc-700"
        />
        {{ t('versions.snapshotCurrent') }}
      </label>
      <p class="mt-1 text-xs text-zinc-500">{{ t('versions.snapshotCurrentHint') }}</p>
      <template #footer>
        <Button variant="ghost" :disabled="restoring" @click="restoreTarget = null">
          {{ t('common.cancel') }}
        </Button>
        <Button variant="primary" :loading="restoring" @click="doRestore">
          {{ t('versions.restore') }}
        </Button>
      </template>
    </Modal>

    <Modal v-model="purgeOpen" :title="t('versions.purgeModalTitle')">
      <p v-if="purgeTarget" class="text-sm text-zinc-700 dark:text-zinc-300">
        {{ t('versions.purgeModalBody', { n: purgeTarget.version_n }) }}
      </p>
      <template #footer>
        <Button variant="ghost" :disabled="purging" @click="purgeTarget = null">
          {{ t('common.cancel') }}
        </Button>
        <Button variant="danger" :loading="purging" @click="doPurge">
          {{ t('versions.purge') }}
        </Button>
      </template>
    </Modal>
  </section>
</template>
