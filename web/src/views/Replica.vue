<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { GitBranch, RefreshCcw, Wrench, Plus, Save, Trash2, FileText, Settings as SettingsIcon, ListTree, ArrowRightLeft, Database, X } from 'lucide-vue-next';

import { useReplicaStore } from '@/stores/replica';
import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';
import { useStorageDriversStore } from '@/stores/storageDrivers';
import { StoragesApi } from '@/api/storages';
import { ReplicationTargetsApi } from '@/api/replicationTargets';
import type { StorageRef, ReplicationTarget, StorageDriver } from '@/api/types';
import Modal from '@/components/ui/Modal.vue';
import StorageDriverFields from '@/components/StorageDriverFields.vue';
import { extractError } from '@/api/client';
import { formatDate } from '@/lib/format';
import type {
  ReplicaFailure,
  ReplicaMode,
  ReplicaRule,
  ReplicaRuleInput,
  ReplicaSettings,
} from '@/api/types';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';
import { DataTable, StorageTags, type ContextAction, type DataColumn } from '@brftech/filex-core';
import { driverName } from '@/lib/storageWords';

type Tab = 'rules' | 'failures' | 'report' | 'settings';

const { t, te, locale } = useI18n();
const replica = useReplicaStore();
const toast = useToastStore();

const activeTab = ref<Tab>('rules');
const refreshing = ref(false);

// Replica targets — separate entity in the new `replication_targets`
// table. NOT a regular storage (Ada, translated from Turkish: "a replica is
// not a storage"): no Storages page entry, no file-explorer presence, no
// write API.
// Primaries link to one via `storages.replica_target_id`.
const storages = useStoragesStore();
const drivers = useStorageDriversStore();
const replicaTargets = ref<ReplicationTarget[]>([]);

const primaryStorages = computed(() => storages.items);

async function loadReplicaTargets() {
  try {
    replicaTargets.value = await ReplicationTargetsApi.list();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

function replicaNameById(id: number): string | undefined {
  return replicaTargets.value.find((r) => r.id === id)?.name;
}

async function setPrimaryTarget(prim: StorageRef, replicaId: number) {
  try {
    await StoragesApi.update(prim.id, {
      ...prim,
      replica_target_id: replicaId > 0 ? replicaId : null,
    });
    toast.success(t('replica.pair.savedOk'));
    await storages.fetch();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

async function removeReplica(target: ReplicationTarget) {
  if (!confirm(t('replica.targets.confirmDelete', { name: target.name }))) return;
  try {
    await ReplicationTargetsApi.remove(target.id);
    toast.success(t('replica.targets.deleted'));
    await Promise.all([loadReplicaTargets(), storages.fetch()]);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

// ── New target dialog ────────────────────────────────────────────
const showTargetForm = ref(false);
const targetDraftName = ref('');
const targetDraftDriver = ref<StorageDriver>('s3');
const targetDraftConfig = ref<Record<string, unknown>>({});
const targetDraftMode = ref<'async' | 'sync'>('async');
const targetSaving = ref(false);

// Same source as the storage form: the drivers' own descriptors. This
// dialog used to carry a second, independently drifted field list — it
// asked for `username` where every driver reads `user`, so a replication
// target created here connected as nobody.
const targetDriverOptions = computed(() =>
  drivers.items.map((d) => ({
    value: d.driver,
    label: d.i18n_key && te(d.i18n_key) ? t(d.i18n_key) : d.label,
  })),
);

function openNewTargetForm() {
  targetDraftName.value = '';
  targetDraftDriver.value = drivers.descriptor('s3') ? 's3' : (drivers.items[0]?.driver ?? 's3');
  targetDraftConfig.value = drivers.defaults(targetDraftDriver.value);
  targetDraftMode.value = 'async';
  showTargetForm.value = true;
}

function onDraftDriverChange(d: StorageDriver) {
  targetDraftDriver.value = d;
  targetDraftConfig.value = drivers.defaults(d);
}

async function submitNewTarget() {
  if (!targetDraftName.value.trim()) return;
  targetSaving.value = true;
  try {
    await ReplicationTargetsApi.create({
      name: targetDraftName.value.trim(),
      driver: targetDraftDriver.value,
      config: targetDraftConfig.value,
      mode: targetDraftMode.value,
      enabled: true,
    });
    toast.success(t('replica.targets.createdOk'));
    showTargetForm.value = false;
    await loadReplicaTargets();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    targetSaving.value = false;
  }
}

// Rule editor state
const editing = ref<ReplicaRule | null>(null);
const ruleDraft = ref<ReplicaRuleInput>({ path_pattern: '', mode: 'mirror', priority: 100, enabled: true, description: '' });

// Settings editor state
const settingsDraft = ref<ReplicaSettings>({ report_cron: '', report_enabled: false, default_mode: 'mirror' });
const cronPreset = ref<string>('custom');

const cronPresets = computed(() => [
  { value: 'custom', label: t('replica.cron.custom') },
  { value: '0 * * * *', label: t('replica.cron.hourly') },
  { value: '0 */6 * * *', label: t('replica.cron.every6h') },
  { value: '0 3 * * *', label: t('replica.cron.daily3am') },
  { value: '0 3 * * 0', label: t('replica.cron.weekly') },
]);

const modeOptions = computed(() => [
  { value: 'mirror', label: t('replica.mode.mirror') },
  { value: 'append_only', label: t('replica.mode.appendOnly') },
  { value: 'skip', label: t('replica.mode.skip') },
]);

async function loadAll() {
  refreshing.value = true;
  try {
    await Promise.all([
      replica.fetchRules(), replica.fetchFailures(), replica.fetchReport(), replica.fetchSettings(),
      storages.fetch(),
      loadReplicaTargets(),
      drivers.fetch(),
    ]);
    settingsDraft.value = { ...replica.settings };
    const matchPreset = cronPresets.value.find((p) => p.value === settingsDraft.value.report_cron);
    cronPreset.value = matchPreset ? matchPreset.value : 'custom';
    // primaryStorages / replicaTargets are computed from
    // storages.items, no extra seeding needed.
  } finally {
    refreshing.value = false;
  }
}

onMounted(loadAll);

function setTab(t: Tab) {
  activeTab.value = t;
}

// ── Rules ──────────────────────────────────────────────
function openNewRule() {
  editing.value = null;
  ruleDraft.value = { path_pattern: '', mode: 'mirror', priority: 100, enabled: true, description: '' };
}

function openEditRule(r: ReplicaRule) {
  editing.value = r;
  ruleDraft.value = {
    path_pattern: r.path_pattern,
    mode: r.mode,
    priority: r.priority,
    enabled: r.enabled,
    description: r.description,
  };
}

async function saveRule() {
  try {
    if (editing.value) {
      await replica.updateRule(editing.value.id, ruleDraft.value);
      toast.success(t('replica.rules.updated'));
    } else {
      await replica.createRule(ruleDraft.value);
      toast.success(t('replica.rules.created'));
    }
    editing.value = null;
    ruleDraft.value = { path_pattern: '', mode: 'mirror', priority: 100, enabled: true, description: '' };
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.saveFailed')));
  }
}

async function deleteRule(r: ReplicaRule) {
  if (!confirm(t('replica.rules.confirmDelete', { p: r.path_pattern }))) return;
  try {
    await replica.deleteRule(r.id);
    toast.success(t('replica.rules.deleted'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.deleteFailed')));
  }
}

// ── Failures ───────────────────────────────────────────
async function fixAll() {
  try {
    const r = await replica.fixAll();
    toast.success(t('replica.failures.queued', { n: r.queued }, r.queued));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  }
}

async function fixOne(path: string, op: string) {
  try {
    await replica.fixOne(path, op);
    toast.success(t('replica.failures.queuedOne'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  }
}

function setUnresolved(v: boolean) {
  replica.setUnresolvedFilter(v);
  replica.fetchFailures();
}

function gotoFailurePage(p: number) {
  replica.setFailuresPage(p);
  replica.fetchFailures();
}

// ── Report ─────────────────────────────────────────────
async function runReport() {
  try {
    await replica.runReportNow();
    toast.success(t('replica.report.ran'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  }
}

// ── Settings ───────────────────────────────────────────
function applyPreset() {
  if (cronPreset.value !== 'custom') {
    settingsDraft.value.report_cron = cronPreset.value;
  }
}

async function saveSettings() {
  try {
    await replica.updateSettings(settingsDraft.value);
    toast.success(t('replica.settings.saved'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.saveFailed')));
  }
}

function modeBadgeTone(m: ReplicaMode): 'emerald' | 'amber' | 'zinc' {
  if (m === 'mirror') return 'emerald';
  if (m === 'append_only') return 'amber';
  return 'zinc';
}

/* ⚠ The replication TARGETS were a `<ul>` with `divide-zinc-200` — a
   header row in bold, a value per line and a delete button on the right, i.e.
   a table that had opted out of being one, and out of the palette with it
   (those two zinc hexes cannot follow the theme). It is the explorer's table
   now (DataTable), with the title and the Add button in its toolbar slot. Each
   of the three tables on this page is remembered on the account under its own
   id (`admin.replica.targets` / `.rules` / `.failures`). */
const targetColumns = computed<DataColumn<ReplicationTarget>[]>(() => [
  { id: 'name', label: t('replica.targets.fields.name'), sortable: true, width: 240 },
  {
    id: 'driver',
    label: t('replica.targets.fields.driver'),
    sortable: true,
    width: 160,
    format: (r) => driverName(r.driver, t, te),
    sortValue: (r) => driverName(r.driver, t, te),
  },
]);

const ruleColumns = computed<DataColumn<ReplicaRule>[]>(() => [
  /* ⚠ The PATTERN is the lead, not the priority: the lead is the column that
     says which row this is, and a priority NUMBER does not. DataTable draws
     the lead first and freezes it, so the pattern now opens the row and the
     priority follows it — sortable, so "in priority order" is one click. */
  {
    id: 'path_pattern',
    label: t('replica.rules.fields.pattern'),
    lead: true,
    sortable: true,
    width: 240,
  },
  {
    id: 'priority',
    label: t('replica.rules.fields.priority'),
    align: 'right',
    sortable: true,
    width: 90,
    sortValue: (r) => r.priority,
  },
  { id: 'mode', label: t('replica.rules.fields.mode'), sortable: true, width: 130 },
  { id: 'description', label: t('replica.rules.fields.description'), sortable: true, width: 240 },
  {
    id: 'enabled',
    label: t('replica.rules.fields.enabled'),
    sortable: true,
    width: 90,
    sortValue: (r) => (r.enabled ? 0 : 1),
  },
]);

/* ⚠ Paged by the SERVER, which has no sort parameter: while the failures span
   more than one page DataTable closes the headers and says why rather than
   re-ordering one page and calling that sorted. */
const failureColumns = computed<DataColumn<ReplicaFailure>[]>(() => [
  { id: 'path', label: t('replica.failures.fields.path'), sortable: true, width: 240 },
  { id: 'op', label: t('replica.failures.fields.op'), sortable: true, width: 100 },
  { id: 'error_code', label: t('replica.failures.fields.errorCode'), sortable: true, width: 130 },
  { id: 'error_msg', label: t('replica.failures.fields.error'), width: 240 },
  {
    id: 'attempts',
    label: t('replica.failures.fields.attempts'),
    align: 'right',
    sortable: true,
    width: 90,
    sortValue: (r) => r.attempts,
  },
  {
    id: 'last_attempt_at',
    label: t('replica.failures.fields.lastAttempt'),
    sortable: true,
    sortDir: 'desc',
    width: 160,
    sortValue: (r) => (r.last_attempt_at ? Date.parse(r.last_attempt_at) : null),
  },
  {
    id: 'resolved_at',
    label: t('replica.failures.fields.resolved'),
    sortable: true,
    width: 120,
    sortValue: (r) => (r.resolved_at ? 1 : 0),
  },
]);

/** A replication target's one verb, behind its one pinned `Actions` control.
 *  `removeReplica` keeps its own confirmation. */
function targetActions(_row: ReplicationTarget): ContextAction[] {
  return [{ key: 'remove', label: t('common.remove'), icon: 'delete', danger: true }];
}

function onTargetAction(key: string, row: ReplicationTarget) {
  if (key === 'remove') removeReplica(row);
}

/** A rule's verbs, behind its one pinned `Actions` control. */
function ruleActions(_row: ReplicaRule): ContextAction[] {
  return [
    { key: 'edit', label: t('common.edit'), icon: 'rename' },
    { key: 'delete', label: t('common.delete'), icon: 'delete', danger: true },
  ];
}

function onRuleAction(key: string, row: ReplicaRule) {
  if (key === 'edit') openEditRule(row);
  else if (key === 'delete') deleteRule(row);
}

/** A failure's one verb. It only applies while the failure is unresolved, so
 *  a resolved row's control is disabled rather than absent — the column then
 *  reads the same all the way down instead of going ragged. */
function failureActions(row: ReplicaFailure): ContextAction[] {
  return [
    { key: 'fix', label: t('replica.failures.fixOne'), icon: 'refresh', hidden: !!row.resolved_at },
  ];
}

function onFailureAction(key: string, row: ReplicaFailure) {
  if (key === 'fix') fixOne(row.path, row.op);
}
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <GitBranch class="h-6 w-6 text-brand-600 dark:text-brand-400" />
        <h1 class="text-xl font-semibold">{{ t('replica.title') }}</h1>
      </div>
      <Button variant="outline" size="sm" @click="loadAll" :loading="refreshing">
        <RefreshCcw class="h-4 w-4" />
        {{ t('common.refresh') }}
      </Button>
    </header>

    <!-- Tab switcher -->
    <nav class="flex gap-1 border-b border-zinc-200 dark:border-zinc-800">
      <button
        v-for="tab in (['rules', 'failures', 'report', 'settings'] as Tab[])"
        :key="tab"
        type="button"
        class="-mb-px border-b-2 px-3 py-2 text-sm font-medium transition"
        :class="activeTab === tab
          ? 'border-brand-600 text-brand-600 dark:border-brand-400 dark:text-brand-400'
          : 'border-transparent text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100'"
        @click="setTab(tab)"
      >
        {{ t('replica.tabs.' + tab) }}
      </button>
    </nav>

    <!-- ── Rules ──────────────────────────────────────── -->
    <div v-show="activeTab === 'rules'" class="space-y-3">
      <!-- Replication targets — dedicated entity. Operators add one or
           more storages here that act as backup-only targets; they
           never appear on the Storages page (those are write-side
           primaries). -->
      <DataTable
        table-id="admin.replica.targets"
        :columns="targetColumns"
        :rows="replicaTargets"
        row-key="id"
        :empty="t('replica.targets.empty')"
        data-testid="replica-targets"
        :row-actions="(row: ReplicationTarget) => targetActions(row)"
        :row-actions-test-id="(row: ReplicationTarget) => `replica-target-actions-${row.id}`"
        @row-action="(key: string, row: ReplicationTarget) => onTargetAction(key, row)"
      >
        <template #toolbar>
          <h2 class="flex items-center gap-2 text-sm font-semibold">
            <Database class="h-4 w-4" />
            {{ t('replica.targets.title') }}
          </h2>
          <Button size="xs" variant="primary" class="ms-auto" @click="openNewTargetForm">
            <Plus class="h-3.5 w-3.5" />
            {{ t('replica.targets.add') }}
          </Button>
        </template>
        <template #cell-name="{ row }">
          <span class="inline-flex items-center gap-2">
            <Badge size="xs" tone="violet">replica</Badge>
            <strong>{{ row.name }}</strong>
          </span>
        </template>
      </DataTable>

      <!-- Pairings — each primary storage points at one replica
           target. PATCH /admin/storages/{primary-id} with
           replica_of_id sets the link. -->
      <!-- ⚠ `card card-body`, not `border-zinc-200 bg-white dark:…`: a
           Tailwind colour is a hex baked into the stylesheet and cannot follow
           the palette the person picked. `.card` is the same box in tokens. -->
      <div class="card card-body">
        <h2 class="flex items-center gap-2 text-sm font-semibold mb-3">
          <ArrowRightLeft class="h-4 w-4" />
          {{ t('replica.pair.title') }}
        </h2>
        <div v-if="!primaryStorages.length" class="text-xs text-zinc-500">
          {{ t('replica.pair.noStorages') }}
        </div>
        <ul v-else class="space-y-2">
          <li
            v-for="prim in primaryStorages"
            :key="prim.id"
            class="flex flex-wrap items-center gap-3 rounded-lg p-3 row-box"
          >
            <div class="flex-1 min-w-[160px]">
              <div class="flex items-center gap-2">
                <strong class="text-sm">{{ prim.name }}</strong>
                <StorageTags :driver="prim.driver" :read-only="prim.read_only" :locale="locale" />
              </div>
              <p class="text-[11px] text-zinc-500 mt-0.5">
                <i18n-t keypath="replica.pair.targetIs" tag="span">
                  <template #name>
                    <strong v-if="prim.replica_target_id">
                      {{ replicaNameById(prim.replica_target_id) || '#' + prim.replica_target_id }}
                    </strong>
                    <span v-else>—</span>
                  </template>
                </i18n-t>
              </p>
            </div>
            <Select
              :model-value="prim.replica_target_id ?? 0"
              :options="[{ value: 0, label: '—' }, ...replicaTargets.map((rt) => ({ value: rt.id, label: rt.name }))]"
              size="sm"
              class="min-w-[180px]"
              @update:model-value="(v) => setPrimaryTarget(prim, Number(v))"
            />
          </li>
        </ul>
      </div>

      <div class="flex items-center justify-between">
        <h2 class="flex items-center gap-2 text-sm font-semibold">
          <ListTree class="h-4 w-4" />
          {{ t('replica.rules.title') }}
        </h2>
        <Button size="sm" variant="primary" @click="openNewRule">
          <Plus class="h-4 w-4" />
          {{ t('replica.rules.add') }}
        </Button>
      </div>

      <!-- Edit form -->
      <div v-if="editing !== null || ruleDraft.path_pattern || activeTab === 'rules' && !replica.rules.length" class="rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900">
        <h3 class="mb-3 text-sm font-medium">
          {{ editing ? t('replica.rules.editTitle') : t('replica.rules.newTitle') }}
        </h3>
        <form class="grid gap-3 sm:grid-cols-2" @submit.prevent="saveRule">
          <Input v-model="ruleDraft.path_pattern" :label="t('replica.rules.fields.pattern')" placeholder="fileman/sensitive/*" required />
          <Select v-model="ruleDraft.mode" :label="t('replica.rules.fields.mode')" :options="modeOptions" />
          <Input v-model.number="ruleDraft.priority" type="number" :label="t('replica.rules.fields.priority')" />
          <Input v-model="ruleDraft.description" :label="t('replica.rules.fields.description')" />
          <div class="flex items-center gap-2 sm:col-span-2">
            <Toggle v-model="ruleDraft.enabled" :label="t('replica.rules.fields.enabled')" />
          </div>
          <div class="flex justify-end gap-2 sm:col-span-2">
            <Button type="button" size="sm" variant="ghost" @click="editing = null; ruleDraft.path_pattern = ''">{{ t('common.cancel') }}</Button>
            <Button type="submit" size="sm" variant="primary">
              <Save class="h-4 w-4" />
              {{ t('common.save') }}
            </Button>
          </div>
        </form>
      </div>

      <DataTable
        table-id="admin.replica.rules"
        :columns="ruleColumns"
        :rows="replica.rules"
        :empty="t('replica.rules.empty')"
        row-key="id"
        :row-actions="(row: ReplicaRule) => ruleActions(row)"
        :row-actions-test-id="(row: ReplicaRule) => `replica-rule-actions-${row.id}`"
        @row-action="(key: string, row: ReplicaRule) => onRuleAction(key, row)"
      >
        <template #cell-path_pattern="{ row }">
          <span class="tbl-mono">{{ row.path_pattern }}</span>
        </template>
        <template #cell-mode="{ row }">
          <Badge :tone="modeBadgeTone(row.mode)">{{ row.mode }}</Badge>
        </template>
        <template #cell-description="{ row }">
          <span class="tbl-clamp">{{ row.description }}</span>
        </template>
        <template #cell-enabled="{ row }">
          <Badge :tone="row.enabled ? 'emerald' : 'zinc'">{{ row.enabled ? 'on' : 'off' }}</Badge>
        </template>
      </DataTable>
    </div>

    <!-- ── Failures ───────────────────────────────────── -->
    <div v-show="activeTab === 'failures'" class="space-y-3">
      <div class="flex items-center justify-between">
        <h2 class="flex items-center gap-2 text-sm font-semibold">
          <Wrench class="h-4 w-4" />
          {{ t('replica.failures.title') }}
        </h2>
        <div class="flex items-center gap-3">
          <Toggle :model-value="replica.onlyUnresolved" :label="t('replica.failures.unresolvedOnly')" @update:model-value="setUnresolved" />
          <Button size="sm" variant="primary" @click="fixAll" :disabled="!replica.failures.length">
            <Wrench class="h-4 w-4" />
            {{ t('replica.failures.fixAll') }}
          </Button>
        </div>
      </div>

      <DataTable
        table-id="admin.replica.failures"
        :columns="failureColumns"
        :rows="replica.failures"
        :empty="t('replica.failures.empty')"
        row-key="id"
        :page="replica.failureCurrentPage"
        :page-size="replica.failuresLimit"
        :total="replica.failuresTotal"
        :row-actions="(row: ReplicaFailure) => failureActions(row)"
        :row-actions-test-id="(row: ReplicaFailure) => `replica-failure-actions-${row.id}`"
        @row-action="(key: string, row: ReplicaFailure) => onFailureAction(key, row)"
        @page="gotoFailurePage"
      >
        <template #cell-path="{ row }">
          <span class="tbl-mono">{{ row.path }}</span>
        </template>
        <template #cell-error_code="{ row }">
          <span class="tbl-mono">{{ row.error_code }}</span>
        </template>
        <template #cell-error_msg="{ row }">
          <span class="tbl-clamp text-rose-600 dark:text-rose-400" :title="row.error_msg">{{ row.error_msg }}</span>
        </template>
        <template #cell-last_attempt_at="{ row }">
          <span class="whitespace-nowrap">{{ formatDate(row.last_attempt_at, locale) }}</span>
        </template>
        <template #cell-resolved_at="{ row }">
          <Badge v-if="row.resolved_at" tone="emerald">{{ t('replica.failures.resolvedYes') }}</Badge>
          <Badge v-else tone="rose">{{ t('replica.failures.resolvedNo') }}</Badge>
        </template>
      </DataTable>
    </div>

    <!-- ── Report ─────────────────────────────────────── -->
    <div v-show="activeTab === 'report'" class="space-y-3">
      <div class="flex items-center justify-between">
        <h2 class="flex items-center gap-2 text-sm font-semibold">
          <FileText class="h-4 w-4" />
          {{ t('replica.report.title') }}
        </h2>
        <Button size="sm" variant="primary" @click="runReport">
          <RefreshCcw class="h-4 w-4" />
          {{ t('replica.report.runNow') }}
        </Button>
      </div>

      <div v-if="replica.report" class="rounded-xl border border-zinc-200 bg-white p-4 shadow-sm dark:border-zinc-800 dark:bg-zinc-900">
        <div class="grid gap-3 sm:grid-cols-4">
          <div>
            <span class="text-xs uppercase tracking-wide text-zinc-500">{{ t('replica.report.fields.generatedAt') }}</span>
            <div class="mt-0.5 text-sm">{{ formatDate(replica.report.generated_at, locale) }}</div>
          </div>
          <div>
            <span class="text-xs uppercase tracking-wide text-zinc-500">{{ t('replica.report.fields.totalFiles') }}</span>
            <div class="mt-0.5 text-lg font-semibold">{{ replica.report.total_files }}</div>
          </div>
          <div>
            <span class="text-xs uppercase tracking-wide text-zinc-500">{{ t('replica.report.fields.failedCount') }}</span>
            <div class="mt-0.5 text-lg font-semibold text-rose-600 dark:text-rose-400">{{ replica.report.failed_count }}</div>
          </div>
          <div>
            <span class="text-xs uppercase tracking-wide text-zinc-500">{{ t('replica.report.fields.repairedCount') }}</span>
            <div class="mt-0.5 text-lg font-semibold text-emerald-600 dark:text-emerald-400">{{ replica.report.repaired_count }}</div>
          </div>
        </div>
      </div>

      <div v-else class="rounded-xl border border-zinc-200 bg-white p-8 text-center text-sm text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900">
        {{ t('replica.report.empty') }}
      </div>
    </div>

    <!-- ── Settings ───────────────────────────────────── -->
    <div v-show="activeTab === 'settings'" class="space-y-3">
      <h2 class="flex items-center gap-2 text-sm font-semibold">
        <SettingsIcon class="h-4 w-4" />
        {{ t('replica.settings.title') }}
      </h2>

      <form class="grid gap-3 rounded-xl border border-zinc-200 bg-white p-4 sm:grid-cols-2 dark:border-zinc-800 dark:bg-zinc-900" @submit.prevent="saveSettings">
        <Toggle v-model="settingsDraft.report_enabled" :label="t('replica.settings.reportEnabled')" />
        <Select v-model="settingsDraft.default_mode" :label="t('replica.settings.defaultMode')" :options="modeOptions" />
        <Select v-model="cronPreset" :label="t('replica.settings.cronPreset')" :options="cronPresets" @update:model-value="applyPreset" />
        <Input v-model="settingsDraft.report_cron" :label="t('replica.settings.cronRaw')" placeholder="0 3 * * *" />
        <p class="text-xs text-zinc-500 sm:col-span-2">{{ t('replica.settings.cronHint') }}</p>
        <div class="flex justify-end gap-2 sm:col-span-2">
          <Button type="submit" size="sm" variant="primary">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>
    </div>

    <!-- Replica target create modal — its own form, NOT the Storage
         form. Operators never mix this with the Depolar flow. -->
    <Modal v-model="showTargetForm" size="lg" :title="t('replica.targets.newTitle')">
      <form class="space-y-3" @submit.prevent="submitNewTarget">
        <Input v-model="targetDraftName" :label="t('replica.targets.fields.name')" placeholder="dr-backup" required />
        <Select
          :model-value="targetDraftDriver"
          :label="t('replica.targets.fields.driver')"
          :options="targetDriverOptions"
          @update:model-value="(v) => onDraftDriverChange(String(v) as StorageDriver)"
        />
        <StorageDriverFields v-model="targetDraftConfig" :driver="targetDraftDriver" />
        <Select v-model="targetDraftMode" :label="t('replica.targets.fields.mode')" :options="[
          { value: 'async', label: t('replica.targets.modeAsync') },
          { value: 'sync',  label: t('replica.targets.modeSync')  },
        ]" />
        <div class="flex justify-end gap-2 pt-2">
          <Button type="button" variant="ghost" @click="showTargetForm = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" variant="primary" :loading="targetSaving">{{ t('common.save') }}</Button>
        </div>
      </form>
    </Modal>
  </section>
</template>
