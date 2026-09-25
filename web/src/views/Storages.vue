<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRouter, RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Database, Plus, RefreshCcw, Trash2, Pencil } from 'lucide-vue-next';

import { useStoragesStore } from '@/stores/storages';
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
import { StorageTags, coveragePercent } from '@brftech/filex-core';

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
              <!-- ⚠ One vocabulary with Connections → Storages: this list
                   said "RO" and "local", that one "SALT OKUNUR" and "LOCAL"
                   (QA #34). Both draw the same StorageTags now. -->
              <StorageTags :driver="s.driver" :read-only="s.read_only" :enabled="s.enabled" :locale="locale" />
            </div>
            <p class="text-xs text-zinc-500 dark:text-zinc-400 mt-1">
              {{ formatBytes(s.stats?.total_size_bytes ?? s.total_bytes ?? 0, locale) }} ·
              <!-- ⚠ The count is IN the message ("{n} files"), with its plural
                   forms: a number followed by a separately translated "files"
                   could not agree with the number or move in a language whose
                   word order differs. -->
              {{ t('storages.fileCount', { n: formatNumber(fileCountOf(s), locale) }, fileCountOf(s)) }}
            </p>
          </div>
          <!-- ⚠ `poll`, not `ondemand`: an unset sync_mode is defaulted to poll
               by the backend (handlers/storages.go), so the badge was naming
               the OPPOSITE mode to the one running. The state in words
               (syncStateLabel), not the wire value "ok". -->
          <Badge :tone="syncTone(s.last_sync_state)" dot data-testid="storage-sync-state">
            {{ syncStateLabel(s.last_sync_state, t) || t('storages.modeLabel.' + (s.sync_mode || 'poll')) }}
          </Badge>
        </div>

        <p class="mt-3 text-xs text-zinc-500 dark:text-zinc-400">
          {{
            s.last_sync_at
              ? t('dashboard.lastSyncAt', { when: formatRelative(s.last_sync_at, locale) })
              : t('storages.notYetSynced')
          }}
        </p>
        <!-- A lazily cataloged storage says how far its catalog has come
             (docs/LAZY-CATALOGUE.md); the storage page has the detail. -->
        <p
          v-if="s.catalogue && !s.catalogue.complete"
          class="mt-1 text-xs text-zinc-500 dark:text-zinc-400"
          data-testid="storage-catalog-line"
        >
          {{
            s.catalogue.fill === 'on_open'
              ? t('storages.catalog.listOnOpen')
              : t('storages.catalog.listFilling', { pct: coveragePercent(s.catalogue) })
          }}
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
