<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { RouterLink, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import {
  Database,
  Users,
  HardDrive,
  Files,
  RefreshCcw,
  Layers,
  PlugZap,
} from 'lucide-vue-next';

import { DashboardApi } from '@/api/dashboard';
import type { DashboardStats } from '@/api/types';
import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatBytes, formatDate, formatNumber, formatRelative } from '@/lib/format';

import StatCard from '@/components/ui/StatCard.vue';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t, locale } = useI18n();
const router = useRouter();
const storages = useStoragesStore();
const toast = useToastStore();

const stats = ref<DashboardStats | null>(null);
const loading = ref(true);
const syncingId = ref<number | null>(null);

const driverIcon = (driver: string): string => {
  switch (driver) {
    case 'local':
      return '\uD83D\uDCC1';
    case 's3':
      return 'S3';
    case 'sftp':
      return 'SFTP';
    case 'webdav':
      return 'DAV';
    default:
      return '\uD83D\uDCBE';
  }
};

async function load() {
  loading.value = true;
  try {
    const [s, _] = await Promise.all([fetchStats(), storages.fetch()]);
    stats.value = s;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

async function fetchStats(): Promise<DashboardStats | null> {
  try {
    return await DashboardApi.stats();
  } catch {
    return null;
  }
}

async function syncOne(id: number) {
  syncingId.value = id;
  try {
    await storages.syncNow(id);
    toast.success(t('storages.syncStarted'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    syncingId.value = null;
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

const totalBytesLabel = computed(() => formatBytes(stats.value?.total_bytes ?? 0, locale.value));
const totalFilesLabel = computed(() => formatNumber(stats.value?.total_files ?? 0, locale.value));

onMounted(load);
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold text-zinc-900 dark:text-zinc-100">
          {{ t('dashboard.title') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('dashboard.subtitle') }}</p>
      </div>
      <Button variant="outline" size="sm" @click="load" :loading="loading">
        <RefreshCcw class="h-4 w-4" />
        {{ t('common.refresh') }}
      </Button>
    </div>

    <!-- Stats grid -->
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
      <StatCard
        :label="t('dashboard.stats.storages')"
        :value="storages.count"
        :icon="Database"
        icon-tone="brand"
        :loading="loading"
      />
      <StatCard
        :label="t('dashboard.stats.users')"
        :value="stats?.user_count ?? '—'"
        :icon="Users"
        icon-tone="sky"
        :loading="loading"
      />
      <StatCard
        :label="t('dashboard.stats.files')"
        :value="totalFilesLabel"
        :icon="Files"
        icon-tone="sky"
        :loading="loading"
      />
      <StatCard
        :label="t('dashboard.stats.totalSize')"
        :value="totalBytesLabel"
        :icon="HardDrive"
        icon-tone="emerald"
        :loading="loading"
      />
      <StatCard
        :label="t('dashboard.stats.activeSyncs')"
        :value="stats?.active_sync_count ?? 0"
        :icon="RefreshCcw"
        icon-tone="amber"
        :loading="loading"
      />
      <StatCard
        :label="t('dashboard.stats.queueDepth')"
        :value="stats?.queue_depth ?? 0"
        :icon="Layers"
        icon-tone="zinc"
        :loading="loading"
      />
    </div>

    <!-- First-run / no-storages -->
    <div v-if="!loading && storages.empty" class="card">
      <EmptyState
        :icon="Database"
        :title="t('dashboard.noStorages')"
        :description="t('storages.subtitle')"
      >
        <template #action>
          <Button @click="router.push({ name: 'storages.new' })">
            <PlugZap class="h-4 w-4" />
            {{ t('dashboard.noStoragesCta') }}
          </Button>
        </template>
      </EmptyState>
    </div>

    <!-- Sync status per storage -->
    <section v-if="!storages.empty" class="space-y-3">
      <h2 class="text-sm font-medium uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
        {{ t('dashboard.lastSync') }}
      </h2>
      <div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        <div v-for="s in storages.items" :key="s.id" class="card card-body">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <RouterLink
                :to="{ name: 'storages.edit', params: { id: s.id } }"
                class="block truncate text-sm font-semibold text-zinc-900 dark:text-zinc-100 hover:text-brand-600 dark:hover:text-brand-400"
              >
                {{ s.name }}
              </RouterLink>
              <p class="text-xs text-zinc-500 dark:text-zinc-400 mt-0.5">
                <span class="font-mono">{{ driverIcon(s.driver) }}</span>
                · {{ s.driver }}
                · {{ formatBytes(s.total_bytes ?? 0, locale) }}
                · {{ formatNumber(s.file_count ?? 0, locale) }}
              </p>
            </div>
            <Badge :tone="syncTone(s.last_sync_state)" dot>
              {{
                s.last_sync_state === 'running'
                  ? t('common.running')
                  : s.last_sync_state ?? t('common.neverRan')
              }}
            </Badge>
          </div>
          <p class="mt-3 text-xs text-zinc-500 dark:text-zinc-400">
            <template v-if="s.last_sync_at">
              {{ t('dashboard.lastSync') }}: {{ formatRelative(s.last_sync_at, locale) }}
            </template>
            <template v-else>
              {{ t('common.neverRan') }}
            </template>
          </p>
          <div class="mt-3 flex items-center gap-2">
            <Button
              size="sm"
              variant="outline"
              :loading="syncingId === s.id"
              @click="syncOne(s.id)"
            >
              <RefreshCcw class="h-3.5 w-3.5" />
              {{ t('common.syncNow') }}
            </Button>
            <RouterLink
              :to="{ name: 'storages.edit', params: { id: s.id } }"
              class="text-xs text-zinc-500 hover:text-brand-600 dark:hover:text-brand-400"
            >
              {{ t('common.details') }}
            </RouterLink>
          </div>
          <p
            v-if="s.last_sync_error"
            class="mt-2 text-xs text-rose-600 dark:text-rose-400 line-clamp-2"
          >
            {{ s.last_sync_error }}
          </p>
        </div>
      </div>
    </section>

    <!-- Two-column footer: recent activity + recent syncs -->
    <div class="grid gap-4 lg:grid-cols-2">
      <div class="card">
        <header class="card-header flex items-center justify-between">
          <h2 class="text-sm font-semibold">{{ t('dashboard.recentActivity') }}</h2>
          <RouterLink
            :to="{ name: 'audit' }"
            class="text-xs text-brand-600 dark:text-brand-400 hover:underline"
          >
            {{ t('common.more') }}
          </RouterLink>
        </header>
        <div v-if="loading" class="card-body text-center text-zinc-500"><Spinner /></div>
        <ul
          v-else-if="stats?.recent_audit?.length"
          class="divide-y divide-zinc-200 dark:divide-zinc-800"
        >
          <li v-for="row in stats.recent_audit" :key="row.id" class="px-4 py-2 text-sm">
            <div class="flex items-center justify-between gap-2">
              <span class="font-medium">{{ row.action }}</span>
              <span class="text-xs text-zinc-500">{{ formatRelative(row.at, locale) }}</span>
            </div>
            <p class="text-xs text-zinc-500 dark:text-zinc-400 truncate">
              {{ row.user_email ?? '—' }}
              <template v-if="row.target_type">
                · {{ row.target_type }}{{ row.target_id ? `:${row.target_id}` : '' }}
              </template>
            </p>
          </li>
        </ul>
        <EmptyState v-else :title="t('dashboard.noActivity')" size="sm" />
      </div>

      <div class="card">
        <header class="card-header flex items-center justify-between">
          <h2 class="text-sm font-semibold">{{ t('dashboard.recentSyncs') }}</h2>
          <RouterLink
            :to="{ name: 'sync' }"
            class="text-xs text-brand-600 dark:text-brand-400 hover:underline"
          >
            {{ t('common.more') }}
          </RouterLink>
        </header>
        <div v-if="loading" class="card-body text-center text-zinc-500"><Spinner /></div>
        <ul
          v-else-if="stats?.recent_syncs?.length"
          class="divide-y divide-zinc-200 dark:divide-zinc-800"
        >
          <li v-for="r in stats.recent_syncs" :key="r.id" class="px-4 py-2 text-sm">
            <div class="flex items-center justify-between gap-2">
              <span class="truncate font-medium">{{ r.storage_name }}</span>
              <Badge :tone="syncTone(r.state)" size="xs">{{ r.state }}</Badge>
            </div>
            <p class="text-xs text-zinc-500 dark:text-zinc-400">
              <span title="added">+{{ r.added }}</span>
              · <span title="updated">~{{ r.updated }}</span>
              · <span title="deleted">-{{ r.deleted }}</span>
              · {{ formatDate(r.started_at, locale) }}
            </p>
          </li>
        </ul>
        <EmptyState v-else :title="t('sync.noResults')" size="sm" />
      </div>
    </div>
  </div>
</template>
