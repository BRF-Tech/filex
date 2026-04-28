<script setup lang="ts">
/**
 * Trash view — admin overview of every soft-deleted node across storages.
 * Actions: restore (clears deleted_at), purge (hard-delete one), empty
 * (purge all in a storage, optionally limited by age).
 */
import { ref, onMounted, computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { useToastStore } from '@/stores/toast';
import { useStoragesStore } from '@/stores/storages';
import { trashApi, type TrashEntry } from '@/api/trash';
import Button from '@/components/ui/Button.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import Modal from '@/components/ui/Modal.vue';
import { Trash2, RotateCcw, AlertTriangle } from 'lucide-vue-next';

const { t } = useI18n();
const toast = useToastStore();
const storages = useStoragesStore();

const entries = ref<TrashEntry[]>([]);
const total = ref(0);
const loading = ref(false);
const selectedStorage = ref<number | undefined>(undefined);
const limit = ref(50);
const offset = ref(0);

const showEmptyDialog = ref(false);
const olderThanDays = ref<number | undefined>(undefined);

async function load() {
  loading.value = true;
  try {
    const res = await trashApi.list({
      storage_id: selectedStorage.value,
      limit: limit.value,
      offset: offset.value,
    });
    entries.value = res.entries;
    total.value = res.total;
  } catch (err: any) {
    toast.error(err?.response?.data?.error ?? String(err));
  } finally {
    loading.value = false;
  }
}

async function restore(entry: TrashEntry) {
  try {
    await trashApi.restore(entry.id);
    toast.success(t('trash.restored', { name: entry.name }));
    await load();
  } catch (err: any) {
    toast.error(err?.response?.data?.error ?? String(err));
  }
}

async function purge(entry: TrashEntry) {
  if (!confirm(t('trash.purge_confirm', { name: entry.name }))) return;
  try {
    await trashApi.purge(entry.id);
    toast.success(t('trash.purged', { name: entry.name }));
    await load();
  } catch (err: any) {
    toast.error(err?.response?.data?.error ?? String(err));
  }
}

async function emptyTrash() {
  showEmptyDialog.value = false;
  try {
    const res = await trashApi.empty({
      storage_id: selectedStorage.value,
      older_than_days: olderThanDays.value,
    });
    toast.success(t('trash.empty_done', { count: res.purged ?? '?' }));
    await load();
  } catch (err: any) {
    toast.error(err?.response?.data?.error ?? String(err));
  }
}

const fmt = new Intl.NumberFormat();
function fmtBytes(n: number) {
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i ? 1 : 0)} ${u[i]}`;
}

function fmtDate(s: string) {
  return new Date(s).toLocaleString();
}

const hasItems = computed(() => entries.value.length > 0);

onMounted(async () => {
  await storages.fetch();
  await load();
});
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-semibold">{{ t('trash.title') }}</h1>
        <p class="text-sm text-zinc-500">{{ t('trash.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <select
          v-model.number="selectedStorage"
          class="rounded border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2 py-1 text-sm"
          @change="load"
        >
          <option :value="undefined">{{ t('trash.all_storages') }}</option>
          <option v-for="s in storages.items" :key="s.id" :value="s.id">{{ s.name }}</option>
        </select>
        <Button variant="ghost" @click="load" :disabled="loading">↻</Button>
        <Button variant="danger" :disabled="!hasItems" @click="showEmptyDialog = true">
          <Trash2 :size="14" /> {{ t('trash.empty') }}
        </Button>
      </div>
    </header>

    <EmptyState
      v-if="!loading && !hasItems"
      :title="t('trash.empty_title')"
      :description="t('trash.empty_description')"
    />

    <div v-else class="overflow-x-auto rounded-lg border border-zinc-200 dark:border-zinc-800">
      <table class="w-full text-sm">
        <thead class="bg-zinc-50 dark:bg-zinc-900 text-left">
          <tr>
            <th class="px-3 py-2">{{ t('trash.col_name') }}</th>
            <th class="px-3 py-2">{{ t('trash.col_storage') }}</th>
            <th class="px-3 py-2">{{ t('trash.col_size') }}</th>
            <th class="px-3 py-2">{{ t('trash.col_deleted_at') }}</th>
            <th class="px-3 py-2">{{ t('trash.col_ttl') }}</th>
            <th class="px-3 py-2 text-right">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="e in entries"
            :key="e.id"
            class="border-t border-zinc-100 dark:border-zinc-800 hover:bg-zinc-50 dark:hover:bg-zinc-900/50"
          >
            <td class="px-3 py-2 font-medium">
              <div>{{ e.name }}</div>
              <div class="text-xs text-zinc-500">{{ e.path }}</div>
            </td>
            <td class="px-3 py-2 text-zinc-600 dark:text-zinc-400">{{ e.storage_name ?? `#${e.storage_id}` }}</td>
            <td class="px-3 py-2 tabular-nums">{{ fmtBytes(e.size) }}</td>
            <td class="px-3 py-2 text-zinc-500">{{ fmtDate(e.deleted_at) }}</td>
            <td class="px-3 py-2 text-zinc-500">
              <span v-if="e.ttl_days !== undefined && e.ttl_days <= 3" class="inline-flex items-center gap-1 text-rose-600">
                <AlertTriangle :size="12" /> {{ e.ttl_days }} {{ t('trash.days_left') }}
              </span>
              <span v-else>{{ e.ttl_days ?? '—' }} {{ t('trash.days_left') }}</span>
            </td>
            <td class="px-3 py-2 text-right">
              <div class="inline-flex gap-2">
                <Button size="sm" variant="ghost" @click="restore(e)">
                  <RotateCcw :size="12" /> {{ t('trash.restore') }}
                </Button>
                <Button size="sm" variant="danger" @click="purge(e)">
                  <Trash2 :size="12" /> {{ t('trash.purge') }}
                </Button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
      <footer class="px-3 py-2 text-xs text-zinc-500 flex justify-between">
        <span>{{ t('trash.total_count', { n: fmt.format(total) }) }}</span>
        <span v-if="loading">{{ t('common.loading') }}</span>
      </footer>
    </div>

    <Modal v-model="showEmptyDialog" :title="t('trash.empty_modal_title')">
      <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('trash.empty_modal_body') }}</p>
      <label class="mt-3 block text-sm">
        {{ t('trash.older_than_days') }}
        <input
          v-model.number="olderThanDays"
          type="number"
          min="0"
          class="mt-1 w-full rounded border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2 py-1"
          :placeholder="t('trash.all_ages')"
        />
      </label>
      <template #footer>
        <Button variant="ghost" @click="showEmptyDialog = false">{{ t('common.cancel') }}</Button>
        <Button variant="danger" @click="emptyTrash">{{ t('trash.empty') }}</Button>
      </template>
    </Modal>
  </section>
</template>
