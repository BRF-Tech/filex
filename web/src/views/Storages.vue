<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRouter, RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Database, Plus, RefreshCcw, Trash2, Pencil } from 'lucide-vue-next';

import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { StorageRef } from '@/api/types';
import { formatBytes, formatNumber, formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Modal from '@/components/ui/Modal.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t, locale } = useI18n();
const router = useRouter();
const storages = useStoragesStore();
const toast = useToastStore();

const syncingId = ref<number | null>(null);
const deleteTarget = ref<StorageRef | null>(null);
const deleting = ref(false);

// Replica targets live in their own `replication_targets` table now
// (v0.1.18+). `storages.items` only contains primaries; no client-side
// filtering needed.

async function load() {
  await storages.fetch();
}

async function syncOne(s: StorageRef) {
  syncingId.value = s.id;
  try {
    await storages.syncNow(s.id);
    toast.success(t('storages.syncStarted'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    syncingId.value = null;
  }
}

async function confirmDelete() {
  if (!deleteTarget.value) return;
  deleting.value = true;
  try {
    await storages.remove(deleteTarget.value.id);
    toast.success(t('storages.deletedOk'));
    deleteTarget.value = null;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    deleting.value = false;
  }
}

const syncTone = (s: string | undefined) => {
  switch (s) {
    case 'ok':
      return 'emerald';
    case 'error':
      return 'rose';
    case 'running':
      return 'sky';
    case 'pending':
      return 'amber';
    default:
      return 'zinc';
  }
};

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

    <div v-else class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
      <div v-for="s in storages.items" :key="s.id" class="card card-body">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <RouterLink
                :to="{ name: 'storages.edit', params: { id: s.id } }"
                class="truncate text-sm font-semibold text-zinc-900 dark:text-zinc-100 hover:text-brand-600 dark:hover:text-brand-400"
              >
                {{ s.name }}
              </RouterLink>
              <Badge size="xs" tone="zinc">{{ s.driver }}</Badge>
              <Badge v-if="s.read_only" size="xs" tone="amber">RO</Badge>
              <Badge v-if="!s.enabled" size="xs" tone="rose">{{ t('common.disabled') }}</Badge>
            </div>
            <p class="text-xs text-zinc-500 dark:text-zinc-400 mt-1">
              {{ formatBytes(s.stats?.total_size_bytes ?? s.total_bytes ?? 0, locale) }} ·
              {{ formatNumber(s.stats?.file_count ?? s.file_count ?? 0, locale) }}
              {{ t('storages.filesUnit') }}
            </p>
          </div>
          <Badge :tone="syncTone(s.last_sync_state)" dot>
            {{
              s.last_sync_state === 'running'
                ? t('common.running')
                : s.last_sync_state ?? t('storages.modeLabel.' + (s.sync_mode || 'ondemand'))
            }}
          </Badge>
        </div>

        <p class="mt-3 text-xs text-zinc-500 dark:text-zinc-400">
          {{ t('dashboard.lastSync') }}:
          {{ s.last_sync_at ? formatRelative(s.last_sync_at, locale) : t('storages.notYetSynced') }}
        </p>
        <p
          v-if="s.last_sync_error"
          class="mt-1 text-xs text-rose-600 dark:text-rose-400 line-clamp-2"
        >
          {{ s.last_sync_error }}
        </p>

        <div class="mt-3 flex items-center justify-between gap-2">
          <div class="flex items-center gap-1.5">
            <Button
              size="sm"
              variant="outline"
              :loading="syncingId === s.id"
              @click="syncOne(s)"
            >
              <RefreshCcw class="h-3.5 w-3.5" />
              {{ t('common.syncNow') }}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              @click="router.push({ name: 'storages.edit', params: { id: s.id } })"
            >
              <Pencil class="h-3.5 w-3.5" />
              {{ t('common.edit') }}
            </Button>
          </div>
          <Button size="sm" variant="ghost" @click="deleteTarget = s">
            <Trash2 class="h-3.5 w-3.5 text-rose-500" />
          </Button>
        </div>
      </div>
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
