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
import { useSyncStore } from '@/stores/sync';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { fileCountOf, formatBytes, formatDate, formatNumber, formatRelative } from '@/lib/format';

import StatCard from '@/components/ui/StatCard.vue';
import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import { DataTable, StorageTags, personName, type DataColumn } from '@brftech/filex-core';
import EmptyState from '@/components/ui/EmptyState.vue';
import Spinner from '@/components/ui/Spinner.vue';
import OnlyOfficeSecretAlert from '@/components/OnlyOfficeSecretAlert.vue';
import { syncStateLabel, syncTone } from '@/lib/syncTone';
import { auditActionLabel, auditTargetLabel } from '@/lib/auditLabel';

const { t, te, locale } = useI18n();
const router = useRouter();
const storages = useStoragesStore();
const sync = useSyncStore();
const toast = useToastStore();

/** How many runs the Recent syncs card lists. */
const RECENT_SYNCS = 5;

const stats = ref<DashboardStats | null>(null);
const loading = ref(true);
const syncingId = ref<number | null>(null);

async function load() {
  loading.value = true;
  try {
    // ⚠⚠ Recent syncs come from the sync-run list — the SAME source as the
    // Sync page's table — not from the dashboard payload. That payload has no
    // run list at all (handlers/dashboard.go → Response), so the card read an
    // always-empty field and said "No sync runs recorded" directly beside a
    // storage card saying "Last sync: 11 seconds ago" (v0.41.0 screenshot
    // pass). The storage card's time is the latest run's start; now both are
    // read from runs that exist.
    const [s] = await Promise.all([fetchStats(), storages.fetch(), sync.fetch({ page_size: RECENT_SYNCS })]);
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



const totalBytesLabel = computed(() => formatBytes(stats.value?.total_bytes ?? 0, locale.value));
const totalFilesLabel = computed(() => formatNumber(stats.value?.total_files ?? 0, locale.value));

onMounted(load);

/* ⚠ The two footer cards were `<ul class="divide-y divide-zinc-200
   dark:divide-zinc-800">` — an action with a time on the right and a second
   line underneath, which is three columns pretending to be a list, in two
   frozen zinc hexes the palette cannot move. They are the one table now —
   DataTable, the explorer's own — with the card's heading and its "More" link
   in the table's toolbar slot, so the dashboard's lists are the same object as
   every other listing in the product: they resize, hide, move and sort, and
   the arrangement is remembered on the account. Both lists are a finished
   "latest N", so sorting them in the browser is honest. */
type AuditRow = NonNullable<NonNullable<typeof stats.value>['recent_audit']>[number];
type SyncRow = (typeof sync.items)[number];

const activityColumns = computed<DataColumn<AuditRow>[]>(() => [
  {
    id: 'action',
    label: t('audit.fields.action'),
    sortable: true,
    width: 240,
    sortValue: (r) => auditActionLabel(r.action, t, te),
  },
  {
    id: 'user_email',
    label: t('audit.fields.user'),
    sortable: true,
    width: 180,
    sortValue: (r) => personName({ name: r.user_name, email: r.user_email }) || null,
  },
  {
    id: 'at',
    label: t('common.when'),
    sortable: true,
    sortDir: 'desc',
    width: 130,
    sortValue: (r) => (r.at ? Date.parse(r.at) : null),
  },
]);

const syncColumns = computed<DataColumn<SyncRow>[]>(() => [
  { id: 'storage_name', label: t('sync.fields.storage'), sortable: true, width: 180 },
  { id: 'state', label: t('sync.fields.state'), sortable: true, width: 110, sortValue: (r) => syncStateLabel(r.state, t) },
  {
    id: 'changes',
    label: t('common.changes'),
    sortable: true,
    sortDir: 'desc',
    width: 150,
    sortValue: (r) => (r.added ?? 0) + (r.updated ?? 0) + (r.deleted ?? 0),
  },
  {
    id: 'started_at',
    label: t('sync.fields.started'),
    sortable: true,
    sortDir: 'desc',
    width: 170,
    format: (r) => formatDate(r.started_at, locale.value),
    sortValue: (r) => (r.started_at ? Date.parse(r.started_at) : null),
  },
]);

const recentSyncs = computed(() => sync.items.slice(0, RECENT_SYNCS));
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

    <!-- ⚠ Persistent, not dismissible: see OnlyOfficeSecretAlert. -->
    <OnlyOfficeSecretAlert />

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
              <!-- ⚠ The driver by NAME, and read-only / disabled in the same
                   words as every other storage list (StorageTags, QA #34) —
                   it printed the raw id ("local") behind an emoji. -->
              <p class="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs text-zinc-500 dark:text-zinc-400">
                <StorageTags :driver="s.driver" :read-only="s.read_only" :enabled="s.enabled" :locale="locale" />
                <span>
                  {{ formatBytes(s.stats?.total_size_bytes ?? s.total_bytes ?? 0, locale) }}
                  · {{
                    t('dashboard.fileCount', { n: formatNumber(fileCountOf(s), locale) }, fileCountOf(s))
                  }}
                </span>
              </p>
            </div>
            <!-- ⚠ In words: this printed the wire value ("ok") in every
                 language (release-candidate sweep, 2026-09-21). -->
            <Badge :tone="syncTone(s.last_sync_state)" dot data-testid="dashboard-storage-state">
              {{ syncStateLabel(s.last_sync_state, t) || t('common.neverRan') }}
            </Badge>
          </div>
          <p class="mt-3 text-xs text-zinc-500 dark:text-zinc-400">
            <!-- ⚠ The colon is IN the message: French puts a space before it
                 (translator report, 2026-09-22), which a colon glued on in
                 the template could never do. -->
            <template v-if="s.last_sync_at">
              {{ t('dashboard.lastSyncAt', { when: formatRelative(s.last_sync_at, locale) }) }}
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
      <DataTable
        table-id="admin.dashboard.activity"
        :columns="activityColumns"
        :rows="stats?.recent_audit ?? []"
        :loading="loading"
        :empty="t('dashboard.noActivity')"
        row-key="id"
        data-testid="dashboard-recent-activity"
      >
        <template #toolbar>
          <h2 class="text-sm font-semibold">{{ t('dashboard.recentActivity') }}</h2>
          <RouterLink
            :to="{ name: 'audit' }"
            class="ms-auto text-xs text-brand-600 dark:text-brand-400 hover:underline"
          >
            {{ t('common.more') }}
          </RouterLink>
        </template>
        <template #cell-action="{ row }">
          <span class="font-medium" :title="row.action" data-testid="dashboard-activity-action">{{
            auditActionLabel(row.action, t, te)
          }}</span>
          <span v-if="row.target_type || row.target_name" class="tbl-sub" data-testid="dashboard-activity-target">
            {{ auditTargetLabel(row.target_type, row.target_id, t, te, row.target_name) }}
          </span>
        </template>
        <template #cell-user_email="{ row }">{{ personName({ name: row.user_name, email: row.user_email }) || '—' }}</template>
        <template #cell-at="{ row }">{{ formatRelative(row.at, locale) }}</template>
      </DataTable>

      <DataTable
        table-id="admin.dashboard.syncs"
        :columns="syncColumns"
        :rows="recentSyncs"
        :loading="loading"
        :empty="t('sync.noResults')"
        row-key="id"
        data-testid="dashboard-recent-syncs"
      >
        <template #toolbar>
          <h2 class="text-sm font-semibold">{{ t('dashboard.recentSyncs') }}</h2>
          <RouterLink
            :to="{ name: 'sync' }"
            class="ms-auto text-xs text-brand-600 dark:text-brand-400 hover:underline"
          >
            {{ t('common.more') }}
          </RouterLink>
        </template>
        <template #cell-storage_name="{ row }">
          <span class="font-medium">{{ row.storage_name }}</span>
        </template>
        <template #cell-state="{ row }">
          <Badge :tone="syncTone(row.state)" size="xs" data-testid="dashboard-sync-state">{{
            syncStateLabel(row.state, t)
          }}</Badge>
        </template>
        <template #cell-changes="{ row }">
          <span :title="t('sync.fields.added')">+{{ row.added }}</span>
          · <span :title="t('sync.fields.updated')">~{{ row.updated }}</span>
          · <span :title="t('sync.fields.deleted')">-{{ row.deleted }}</span>
        </template>
      </DataTable>
    </div>
  </div>
</template>
