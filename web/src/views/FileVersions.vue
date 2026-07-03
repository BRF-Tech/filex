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
import { versionsApi, type NodeVersion } from '@/api/versions';
import { extractError } from '@/api/client';
import { formatBytes, formatDate } from '@/lib/format';
import Button from '@/components/ui/Button.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Badge from '@/components/ui/Badge.vue';
import Modal from '@/components/ui/Modal.vue';

const { t, locale } = useI18n();
const route = useRoute();
const router = useRouter();
const toast = useToastStore();
const auth = useAuthStore();

const nodeId = computed(() => Number(route.params.nodeId));
const versions = ref<NodeVersion[]>([]);
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

async function load() {
  if (!Number.isFinite(nodeId.value) || nodeId.value <= 0) {
    error.value = t('versions.badNodeId');
    return;
  }
  loading.value = true;
  error.value = null;
  try {
    versions.value = await versionsApi.list(nodeId.value);
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
          <span class="truncate">{{ t('versions.nodeLabel', { id: nodeId }) }}</span>
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

    <div v-if="loading" class="flex justify-center py-12">
      <Spinner size="md" class="text-brand-600" />
    </div>

    <EmptyState
      v-else-if="!versions.length && !error"
      :icon="History"
      :title="t('versions.empty')"
      :description="t('versions.emptyDescription')"
    />

    <div
      v-else-if="versions.length"
      class="overflow-x-auto rounded-lg border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-900"
    >
      <table class="w-full text-sm">
        <thead class="bg-zinc-50 dark:bg-zinc-900/50 text-left text-zinc-600 dark:text-zinc-400">
          <tr>
            <th class="px-4 py-2 font-medium">{{ t('versions.col.version') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('versions.col.size') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('versions.col.createdAt') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('versions.col.etag') }}</th>
            <th class="px-4 py-2 font-medium text-right">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
          <tr
            v-for="(v, idx) in versions"
            :key="v.id"
            class="hover:bg-zinc-50 dark:hover:bg-zinc-900/50"
          >
            <td class="px-4 py-2">
              <div class="flex items-center gap-2">
                <Badge :tone="idx === 0 ? 'brand' : 'zinc'" size="sm">
                  v{{ v.version_n }}
                </Badge>
                <span v-if="idx === 0" class="text-xs text-zinc-500">{{ t('versions.newest') }}</span>
              </div>
            </td>
            <td class="px-4 py-2 tabular-nums">{{ formatBytes(v.size, locale) }}</td>
            <td class="px-4 py-2 text-zinc-500 dark:text-zinc-400">
              {{ formatDate(v.created_at, locale) }}
            </td>
            <td class="px-4 py-2 font-mono text-xs text-zinc-500 dark:text-zinc-400">
              <span v-if="v.etag" :title="v.etag">{{ v.etag.slice(0, 12) }}…</span>
              <span v-else>—</span>
            </td>
            <td class="px-4 py-2 text-right">
              <div class="inline-flex gap-1">
                <Button
                  size="xs"
                  variant="ghost"
                  :title="t('versions.downloadDisabled')"
                  disabled
                >
                  <Download class="h-3.5 w-3.5" />
                  {{ t('versions.download') }}
                </Button>
                <Button size="xs" variant="outline" @click="confirmRestore(v)">
                  <RotateCcw class="h-3.5 w-3.5" />
                  {{ t('versions.restore') }}
                </Button>
                <Button
                  v-if="isAdmin"
                  size="xs"
                  variant="danger"
                  :title="t('versions.purgeTooltip')"
                  @click="confirmPurge(v)"
                >
                  <Trash2 class="h-3.5 w-3.5" />
                </Button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

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
