<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { GitBranch, RefreshCcw, Wrench, Plus, Save, Trash2, FileText, Settings as SettingsIcon, ListTree, ArrowRightLeft, Database, X } from 'lucide-vue-next';

import { useReplicaStore } from '@/stores/replica';
import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { StoragesApi } from '@/api/storages';
import { ReplicationTargetsApi } from '@/api/replicationTargets';
import type { StorageRef, ReplicationTarget, StorageDriver } from '@/api/types';
import Modal from '@/components/ui/Modal.vue';
import StorageDriverFields from '@/components/StorageDriverFields.vue';
import { extractError } from '@/api/client';
import { formatDate } from '@/lib/format';
import type { ReplicaMode, ReplicaRule, ReplicaRuleInput, ReplicaSettings } from '@/api/types';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';

type Tab = 'rules' | 'failures' | 'report' | 'settings';

const { t, locale } = useI18n();
const replica = useReplicaStore();
const toast = useToastStore();

const activeTab = ref<Tab>('rules');
const refreshing = ref(false);

// Replica targets — separate entity in the new `replication_targets`
// table. NOT a regular storage (Burak: "replika bir depo değil"):
// no Depolar page entry, no file-explorer presence, no write API.
// Primaries link to one via `storages.replica_target_id`.
const storages = useStoragesStore();
const caps = useCapabilitiesStore();
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
const targetDraftConfig = ref<Record<string, unknown>>({
  bucket: '', region: '', endpoint: '', access_key: '', secret_key: '',
});
const targetDraftMode = ref<'async' | 'sync'>('async');
const targetSaving = ref(false);

const targetDriverOptions = computed(() =>
  (['s3', 'local', 'sftp', 'webdav'] as StorageDriver[])
    .filter((d) => caps.data.storage_drivers.length === 0 || caps.data.storage_drivers.includes(d))
    .map((d) => ({ value: d, label: t(`storages.driver.${d}`) })),
);

function openNewTargetForm() {
  targetDraftName.value = '';
  targetDraftDriver.value = 's3';
  targetDraftConfig.value = { bucket: '', region: '', endpoint: '', access_key: '', secret_key: '' };
  targetDraftMode.value = 'async';
  showTargetForm.value = true;
}

function onDraftDriverChange(d: StorageDriver) {
  targetDraftDriver.value = d;
  switch (d) {
    case 'local': targetDraftConfig.value = { path: '' }; break;
    case 's3':    targetDraftConfig.value = { bucket: '', region: '', endpoint: '', access_key: '', secret_key: '' }; break;
    case 'sftp':  targetDraftConfig.value = { host: '', port: 22, username: '', password: '', root: '/' }; break;
    case 'webdav':targetDraftConfig.value = { url: '', username: '', password: '' }; break;
    default:      targetDraftConfig.value = {};
  }
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
      caps.fetch(),
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
    toast.error(extractError(e, 'Save failed'));
  }
}

async function deleteRule(r: ReplicaRule) {
  if (!confirm(t('replica.rules.confirmDelete', { p: r.path_pattern }))) return;
  try {
    await replica.deleteRule(r.id);
    toast.success(t('replica.rules.deleted'));
  } catch (e: unknown) {
    toast.error(extractError(e, 'Delete failed'));
  }
}

// ── Failures ───────────────────────────────────────────
async function fixAll() {
  try {
    const r = await replica.fixAll();
    toast.success(t('replica.failures.queued', { n: r.queued }));
  } catch (e: unknown) {
    toast.error(extractError(e, 'Fix all failed'));
  }
}

async function fixOne(path: string, op: string) {
  try {
    await replica.fixOne(path, op);
    toast.success(t('replica.failures.queuedOne'));
  } catch (e: unknown) {
    toast.error(extractError(e, 'Fix failed'));
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
    toast.error(extractError(e, 'Run report failed'));
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
    toast.error(extractError(e, 'Save failed'));
  }
}

function modeBadgeTone(m: ReplicaMode): 'emerald' | 'amber' | 'zinc' {
  if (m === 'mirror') return 'emerald';
  if (m === 'append_only') return 'amber';
  return 'zinc';
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
      <!-- Replika Depoları — dedicated entity. Operators add one or
           more storages here that act as backup-only targets; they
           never appear in the Depolar page (those are write-side
           primaries). -->
      <div class="rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900">
        <div class="flex items-center justify-between mb-3">
          <h2 class="flex items-center gap-2 text-sm font-semibold">
            <Database class="h-4 w-4" />
            {{ t('replica.targets.title') }}
          </h2>
          <Button size="xs" variant="primary" @click="openNewTargetForm">
            <Plus class="h-3.5 w-3.5" />
            {{ t('replica.targets.add') }}
          </Button>
        </div>
        <div v-if="!replicaTargets.length" class="text-xs text-zinc-500">
          {{ t('replica.targets.empty') }}
        </div>
        <ul v-else class="divide-y divide-zinc-100 dark:divide-zinc-800 text-xs">
          <li v-for="t_ in replicaTargets" :key="t_.id" class="flex items-center justify-between py-2">
            <div class="flex items-center gap-2">
              <Badge size="xs" tone="violet">replica</Badge>
              <strong>{{ t_.name }}</strong>
              <span class="text-zinc-500">{{ t_.driver }}</span>
            </div>
            <Button size="xs" variant="ghost" @click="removeReplica(t_)">
              <Trash2 class="h-3.5 w-3.5 text-rose-500" />
            </Button>
          </li>
        </ul>
      </div>

      <!-- Eşleştirmeler — each primary storage points at one replica
           target. PATCH /admin/storages/{primary-id} with
           replica_of_id sets the link. -->
      <div class="rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900">
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
            class="flex flex-wrap items-center gap-3 rounded-lg border border-zinc-100 bg-zinc-50 p-3 dark:border-zinc-800 dark:bg-zinc-950"
          >
            <div class="flex-1 min-w-[160px]">
              <div class="flex items-center gap-2">
                <strong class="text-sm">{{ prim.name }}</strong>
                <span class="text-xs text-zinc-500">{{ prim.driver }}</span>
              </div>
              <p class="text-[11px] text-zinc-500 mt-0.5">
                {{ t('replica.pair.targetLabel') }}:
                <strong v-if="prim.replica_target_id">
                  {{ replicaNameById(prim.replica_target_id) || '#' + prim.replica_target_id }}
                </strong>
                <span v-else>—</span>
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

      <div class="overflow-x-auto rounded-xl border border-zinc-200 dark:border-zinc-800">
        <table class="w-full text-sm">
          <thead class="bg-zinc-50 text-xs uppercase text-zinc-500 dark:bg-zinc-900 dark:text-zinc-400">
            <tr>
              <th class="px-3 py-2 text-right">{{ t('replica.rules.fields.priority') }}</th>
              <th class="px-3 py-2 text-left">{{ t('replica.rules.fields.pattern') }}</th>
              <th class="px-3 py-2 text-left">{{ t('replica.rules.fields.mode') }}</th>
              <th class="px-3 py-2 text-left">{{ t('replica.rules.fields.description') }}</th>
              <th class="px-3 py-2 text-left">{{ t('replica.rules.fields.enabled') }}</th>
              <th class="px-3 py-2 text-right"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
            <tr v-for="r in replica.rules" :key="r.id" class="bg-white dark:bg-zinc-950">
              <td class="px-3 py-2 text-right">{{ r.priority }}</td>
              <td class="px-3 py-2 font-mono text-xs">{{ r.path_pattern }}</td>
              <td class="px-3 py-2"><Badge :tone="modeBadgeTone(r.mode)">{{ r.mode }}</Badge></td>
              <td class="px-3 py-2 text-xs text-zinc-500 dark:text-zinc-400">{{ r.description }}</td>
              <td class="px-3 py-2">
                <Badge :tone="r.enabled ? 'emerald' : 'zinc'">{{ r.enabled ? 'on' : 'off' }}</Badge>
              </td>
              <td class="px-3 py-2 text-right">
                <div class="flex justify-end gap-1">
                  <Button size="xs" variant="outline" @click="openEditRule(r)">{{ t('common.edit') }}</Button>
                  <Button size="xs" variant="ghost" @click="deleteRule(r)">
                    <Trash2 class="h-3.5 w-3.5 text-rose-500" />
                  </Button>
                </div>
              </td>
            </tr>
            <tr v-if="!replica.rules.length">
              <td colspan="6" class="px-3 py-8 text-center text-sm text-zinc-500">{{ t('replica.rules.empty') }}</td>
            </tr>
          </tbody>
        </table>
      </div>
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

      <div class="overflow-x-auto rounded-xl border border-zinc-200 dark:border-zinc-800">
        <table class="w-full text-sm">
          <thead class="bg-zinc-50 text-xs uppercase text-zinc-500 dark:bg-zinc-900 dark:text-zinc-400">
            <tr>
              <th class="px-3 py-2 text-left">{{ t('replica.failures.fields.path') }}</th>
              <th class="px-3 py-2 text-left">{{ t('replica.failures.fields.op') }}</th>
              <th class="px-3 py-2 text-left">{{ t('replica.failures.fields.errorCode') }}</th>
              <th class="px-3 py-2 text-left">{{ t('replica.failures.fields.error') }}</th>
              <th class="px-3 py-2 text-right">{{ t('replica.failures.fields.attempts') }}</th>
              <th class="px-3 py-2 text-left">{{ t('replica.failures.fields.lastAttempt') }}</th>
              <th class="px-3 py-2 text-left">{{ t('replica.failures.fields.resolved') }}</th>
              <th class="px-3 py-2 text-right"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
            <tr v-for="f in replica.failures" :key="f.id" class="bg-white dark:bg-zinc-950">
              <td class="px-3 py-2 font-mono text-xs">{{ f.path }}</td>
              <td class="px-3 py-2">{{ f.op }}</td>
              <td class="px-3 py-2 font-mono text-xs">{{ f.error_code }}</td>
              <td class="px-3 py-2 text-xs text-rose-600 dark:text-rose-400 max-w-md truncate">{{ f.error_msg }}</td>
              <td class="px-3 py-2 text-right">{{ f.attempts }}</td>
              <td class="px-3 py-2 whitespace-nowrap text-xs">{{ formatDate(f.last_attempt_at, locale) }}</td>
              <td class="px-3 py-2 text-xs">
                <Badge v-if="f.resolved_at" tone="emerald">{{ t('replica.failures.resolvedYes') }}</Badge>
                <Badge v-else tone="rose">{{ t('replica.failures.resolvedNo') }}</Badge>
              </td>
              <td class="px-3 py-2 text-right">
                <Button v-if="!f.resolved_at" size="xs" variant="outline" @click="fixOne(f.path, f.op)">
                  <Wrench class="h-3.5 w-3.5" />
                  {{ t('replica.failures.fixOne') }}
                </Button>
              </td>
            </tr>
            <tr v-if="!replica.failures.length">
              <td colspan="8" class="px-3 py-8 text-center text-sm text-zinc-500">{{ t('replica.failures.empty') }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="replica.failurePages > 1" class="flex items-center justify-between text-xs">
        <span>{{ t('common.pageOf', { current: replica.failureCurrentPage, total: replica.failurePages }) }}</span>
        <div class="flex gap-2">
          <Button size="xs" variant="outline" :disabled="replica.failureCurrentPage <= 1" @click="gotoFailurePage(replica.failureCurrentPage - 1)">{{ t('common.prev') }}</Button>
          <Button size="xs" variant="outline" :disabled="replica.failureCurrentPage >= replica.failurePages" @click="gotoFailurePage(replica.failureCurrentPage + 1)">{{ t('common.next') }}</Button>
        </div>
      </div>
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
