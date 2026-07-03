<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import {
  ArrowLeft,
  Save,
  RefreshCcw,
  Trash2,
  Activity,
  AlertTriangle,
} from 'lucide-vue-next';

import { StoragesApi } from '@/api/storages';
import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { DriftReport, StorageRef, SyncRun } from '@/api/types';
import { formatBytes, formatDate, formatDuration, formatNumber, formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';
import Modal from '@/components/ui/Modal.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Table from '@/components/ui/Table.vue';
import StorageDriverFields from '@/components/StorageDriverFields.vue';

const { t, locale } = useI18n();
const route = useRoute();
const router = useRouter();
const storages = useStoragesStore();
const toast = useToastStore();

const id = computed(() => Number(route.params.id));
const item = ref<StorageRef | null>(null);
const loading = ref(true);

const name = ref('');
const enabled = ref(true);
const readOnly = ref(false);
const rbacEnabled = ref(false);
const config = ref<Record<string, unknown>>({});

const saving = ref(false);
const showDelete = ref(false);
const deleting = ref(false);

const testing = ref(false);
const testResult = ref<{ ok: boolean; error?: string } | null>(null);

const runs = ref<SyncRun[]>([]);
const runsLoading = ref(false);
const drift = ref<DriftReport | null>(null);
const driftLoading = ref(false);

async function load() {
  loading.value = true;
  try {
    const s = await StoragesApi.get(id.value);
    item.value = s;
    name.value = s.name;
    enabled.value = s.enabled;
    readOnly.value = s.read_only;
    rbacEnabled.value = s.rbac_enabled ?? false;
    config.value = { ...(s.config ?? {}) };
    await Promise.allSettled([loadRuns(), loadDrift()]);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
    router.replace({ name: 'storages' });
  } finally {
    loading.value = false;
  }
}

async function loadRuns() {
  runsLoading.value = true;
  try {
    runs.value = await StoragesApi.syncHistory(id.value, 10);
  } finally {
    runsLoading.value = false;
  }
}

async function loadDrift() {
  driftLoading.value = true;
  try {
    drift.value = await StoragesApi.drift(id.value);
  } catch {
    drift.value = null;
  } finally {
    driftLoading.value = false;
  }
}

async function save() {
  saving.value = true;
  try {
    const updated = await storages.update(id.value, {
      name: name.value.trim(),
      enabled: enabled.value,
      read_only: readOnly.value,
      rbac_enabled: rbacEnabled.value,
      config: config.value,
    });
    item.value = updated;
    toast.success(t('storages.updatedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    saving.value = false;
  }
}

async function syncNow() {
  try {
    await storages.syncNow(id.value);
    toast.success(t('storages.syncStarted'));
    await loadRuns();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

async function confirmDelete() {
  deleting.value = true;
  try {
    await storages.remove(id.value);
    toast.success(t('storages.deletedOk'));
    router.replace({ name: 'storages' });
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    deleting.value = false;
    showDelete.value = false;
  }
}

async function test() {
  if (!item.value) return;
  testing.value = true;
  testResult.value = null;
  try {
    testResult.value = await StoragesApi.testConnection({
      name: name.value,
      driver: item.value.driver,
      config: config.value,
      read_only: readOnly.value,
    });
  } catch (e: unknown) {
    testResult.value = { ok: false, error: extractError(e, t('errors.generic')) };
  } finally {
    testing.value = false;
  }
}

const stateTone = (s: string) => {
  switch (s) {
    case 'ok':
      return 'emerald';
    case 'error':
      return 'rose';
    case 'running':
      return 'sky';
    case 'aborted':
      return 'amber';
    default:
      return 'zinc';
  }
};

function rowDuration(r: SyncRun): string {
  if (!r.finished_at) return '—';
  const ms = new Date(r.finished_at).getTime() - new Date(r.started_at).getTime();
  return formatDuration(ms / 1000);
}

// Defensive zero-fill so rows with missing fields render "0"
// instead of literal "undefined".
function num(n: number | null | undefined): string {
  return typeof n === 'number' && Number.isFinite(n) ? String(n) : '0';
}

const runColumns = computed(() => [
  { key: 'started_at', label: t('sync.fields.started'), format: (r: SyncRun) => r.started_at ? formatDate(r.started_at, locale.value) : '—' },
  { key: 'duration', label: t('sync.fields.duration'), format: rowDuration },
  { key: 'state', label: t('sync.fields.state'), cell: 'slot' as const },
  { key: 'added', label: '+', align: 'right' as const, format: (r: SyncRun) => num(r.added) },
  { key: 'updated', label: '~', align: 'right' as const, format: (r: SyncRun) => num(r.updated) },
  { key: 'deleted', label: '-', align: 'right' as const, format: (r: SyncRun) => num(r.deleted) },
]);

onMounted(load);
</script>

<template>
  <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>
  <div v-else-if="item" class="space-y-5">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div class="min-w-0">
        <h1 class="text-xl font-semibold flex items-center gap-2">
          {{ item.name }}
          <Badge size="xs">{{ item.driver }}</Badge>
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">
          {{ formatBytes(item.stats?.total_size_bytes ?? item.total_bytes ?? 0, locale) }} ·
          {{ formatNumber(item.stats?.file_count ?? item.file_count ?? 0, locale) }}
          {{ t('storages.filesUnit') }}
        </p>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="ghost" size="sm" @click="router.push({ name: 'storages' })">
          <ArrowLeft class="h-4 w-4" />
          {{ t('common.back') }}
        </Button>
        <Button variant="outline" size="sm" @click="syncNow">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.syncNow') }}
        </Button>
      </div>
    </div>

    <form class="card card-body space-y-3" @submit.prevent="save">
      <Input v-model="name" :label="t('storages.fields.name')" required />
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Toggle v-model="enabled" :label="t('common.enabled')" />
        <Toggle v-model="readOnly" :label="t('storages.fields.readOnly')" />
        <Toggle v-model="rbacEnabled" :label="t('storages.fields.rbac')" />
        <p class="text-xs text-zinc-500 dark:text-zinc-400 -mt-1">
          {{ t('storages.fields.rbacHint') }}
        </p>
      </div>
      <hr class="divider" />
      <StorageDriverFields v-model="config" :driver="item.driver" />

      <div v-if="testResult" class="rounded-md border p-3 text-sm" :class="testResult.ok ? 'border-emerald-300 bg-emerald-50 dark:bg-emerald-500/10 dark:border-emerald-500/30 text-emerald-700 dark:text-emerald-300' : 'border-rose-300 bg-rose-50 dark:bg-rose-500/10 dark:border-rose-500/30 text-rose-700 dark:text-rose-300'">
        <p v-if="testResult.ok">{{ t('storages.actions.ok') }}</p>
        <p v-else class="font-mono break-all">{{ testResult.error }}</p>
      </div>

      <div class="flex flex-wrap items-center justify-between gap-2 pt-2">
        <Button type="button" variant="outline" :loading="testing" @click="test">
          <Activity class="h-4 w-4" />
          {{ t('storages.actions.test') }}
        </Button>
        <div class="flex items-center gap-2">
          <Button type="button" variant="danger" @click="showDelete = true">
            <Trash2 class="h-4 w-4" />
            {{ t('common.delete') }}
          </Button>
          <Button type="submit" :loading="saving">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </div>
    </form>

    <section class="card">
      <header class="card-header flex items-center justify-between">
        <h2 class="text-sm font-semibold">{{ t('storages.actions.syncHistory') }}</h2>
        <Button variant="ghost" size="xs" :loading="runsLoading" @click="loadRuns">
          <RefreshCcw class="h-3.5 w-3.5" />
        </Button>
      </header>
      <Table :columns="runColumns" :rows="runs" :loading="runsLoading" :empty="t('sync.noResults')">
        <template #cell-state="{ row }">
          <Badge :tone="stateTone((row as SyncRun).state)" size="xs">
            {{ (row as SyncRun).state }}
          </Badge>
        </template>
      </Table>
    </section>

    <section class="card">
      <header class="card-header flex items-center justify-between">
        <h2 class="text-sm font-semibold">{{ t('storages.actions.drift') }}</h2>
        <Button variant="ghost" size="xs" :loading="driftLoading" @click="loadDrift">
          <RefreshCcw class="h-3.5 w-3.5" />
        </Button>
      </header>
      <div class="card-body">
        <div v-if="driftLoading" class="text-center text-zinc-500"><Spinner /></div>
        <div v-else-if="!drift" class="text-sm text-zinc-500">—</div>
        <div v-else class="grid grid-cols-2 sm:grid-cols-4 gap-3 text-sm">
          <div>
            <p class="text-xs text-zinc-500">Missing in DB</p>
            <p class="font-semibold">{{ drift.missing_in_db }}</p>
          </div>
          <div>
            <p class="text-xs text-zinc-500">Missing in storage</p>
            <p class="font-semibold">{{ drift.missing_in_storage }}</p>
          </div>
          <div>
            <p class="text-xs text-zinc-500">Size mismatch</p>
            <p class="font-semibold">{{ drift.size_mismatch }}</p>
          </div>
          <div>
            <p class="text-xs text-zinc-500">Hash mismatch</p>
            <p class="font-semibold">{{ drift.hash_mismatch }}</p>
          </div>
          <div class="col-span-full text-xs text-zinc-500">
            {{ formatRelative(drift.generated_at, locale) }}
          </div>
        </div>
      </div>
    </section>

    <Modal v-model="showDelete" :title="t('common.delete')" size="sm">
      <p class="text-sm text-zinc-700 dark:text-zinc-300 flex items-start gap-2">
        <AlertTriangle class="h-4 w-4 text-amber-500 shrink-0 mt-0.5" />
        <span>{{ t('storages.deleteConfirm', { name: item.name }) }}</span>
      </p>
      <template #footer>
        <Button variant="ghost" @click="showDelete = false">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="deleting" @click="confirmDelete">
          {{ t('common.yesDelete') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>
