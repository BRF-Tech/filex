<script setup lang="ts">
/**
 * Plugins — storage drivers that live outside the filex binary.
 *
 * What this page is for: somebody wrote a driver for their own system and
 * wants filex to speak it. They install it here; from then on it appears in
 * the ordinary storage picker as `plugin:<driver>` with the config form the
 * plugin itself describes, and behaves like any other storage.
 *
 * ⚠ Instance-wide, not per-tenant: a plugin is a program filex runs (or a
 * service it hands storage credentials to). In multi-tenant mode the server
 * answers 403 to anyone but the platform operator, and this page shows that
 * answer rather than an empty list.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import {
  ArrowUpFromLine,
  Blocks,
  Plus,
  RefreshCcw,
  RotateCcw,
  ShieldCheck,
  Trash2,
  TriangleAlert,
  Upload,
} from 'lucide-vue-next';

import {
  PluginsApi,
  rolledBackPlugin,
  type ConformanceMode,
  type Plugin,
  type PluginProbe,
  type PluginState,
  type ProbeStatus,
} from '@/api/plugins';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { formatDate } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';
import Modal from '@/components/ui/Modal.vue';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

const { t, locale } = useI18n();
const toast = useToastStore();

const items = ref<Plugin[]>([]);
const pluginDir = ref('');
const loading = ref(false);
const busyId = ref<number | null>(null);
const forbidden = ref(false);
const disabledMsg = ref('');
const requiresSignature = ref(false);
const conformanceMode = ref<ConformanceMode>('enforce');

const showForm = ref(false);
const source = ref<'file' | 'url' | 'remote'>('file');
const formName = ref('');
const formFile = ref<File | null>(null);
const formUrl = ref('');
const formSha = ref('');
const formAddress = ref('');
const formToken = ref('');
const formSignature = ref('');
const saving = ref(false);

const reportOf = ref<Plugin | null>(null);

const upgradeOf = ref<Plugin | null>(null);
const upgradeFile = ref<File | null>(null);
const upgradeSignature = ref('');
const upgrading = ref(false);
const upgradeFailure = ref('');
/** What the rollback left running, shown next to the failure that caused it. */
const upgradeRestored = ref<Plugin | null>(null);

async function load() {
  loading.value = true;
  forbidden.value = false;
  disabledMsg.value = '';
  try {
    const res = await PluginsApi.list();
    items.value = res.plugins;
    pluginDir.value = res.dir;
    requiresSignature.value = res.requires_signature;
    conformanceMode.value = res.conformance_mode;
  } catch (e: unknown) {
    // The two "not a bug" answers get their own screens: a tenant admin is
    // told whose surface this is, and an operator who turned the subsystem
    // off is told which setting did it.
    const err = e as { response?: { status?: number; data?: { error?: string; message?: string } } };
    if (err?.response?.status === 403) {
      forbidden.value = true;
    } else if (err?.response?.status === 503) {
      disabledMsg.value = err.response?.data?.message ?? t('plugins.disabled');
    } else {
      toast.error(extractError(e, t('errors.loadFailed')));
    }
  } finally {
    loading.value = false;
  }
}

onMounted(load);

function openCreate() {
  source.value = 'file';
  formName.value = '';
  formFile.value = null;
  formUrl.value = '';
  formSha.value = '';
  formAddress.value = '';
  formToken.value = '';
  formSignature.value = '';
  showForm.value = true;
}

function onFile(e: Event) {
  const input = e.target as HTMLInputElement;
  formFile.value = input.files?.[0] ?? null;
  // A plugin's name defaults to its file name, lower-cased and cleaned to the
  // server's rule — one fewer field to fill in, and it can still be edited.
  if (formFile.value && !formName.value) {
    formName.value = formFile.value.name
      .replace(/\.(exe|bin)$/i, '')
      .toLowerCase()
      .replace(/[^a-z0-9_-]+/g, '-')
      .replace(/^[^a-z0-9]+/, '')
      .slice(0, 32);
  }
}

async function save() {
  const name = formName.value.trim();
  if (!name) {
    toast.error(t('plugins.errName'));
    return;
  }
  saving.value = true;
  try {
    if (source.value === 'file') {
      if (!formFile.value) {
        toast.error(t('plugins.errFile'));
        return;
      }
      await PluginsApi.upload(name, formFile.value, formSignature.value.trim());
    } else if (source.value === 'url') {
      await PluginsApi.fromUrl(name, formUrl.value.trim(), formSha.value.trim(), formSignature.value.trim());
    } else {
      await PluginsApi.remote(name, formAddress.value.trim(), formToken.value);
    }
    toast.success(t('plugins.installed', { name }));
    showForm.value = false;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.installFailed')));
  } finally {
    saving.value = false;
  }
}

async function toggleEnabled(p: Plugin, enabled: boolean) {
  busyId.value = p.id;
  try {
    const updated = await PluginsApi.setEnabled(p.id, enabled);
    Object.assign(p, updated);
    // Starting is asynchronous: re-read shortly so the row settles on its
    // real state instead of sitting on "starting" until the next refresh.
    window.setTimeout(load, 1200);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.updateFailed')));
    await load();
  } finally {
    busyId.value = null;
  }
}

async function restart(p: Plugin) {
  busyId.value = p.id;
  try {
    const updated = await PluginsApi.restart(p.id);
    Object.assign(p, updated);
    window.setTimeout(load, 1200);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  } finally {
    busyId.value = null;
  }
}

async function remove(p: Plugin) {
  // Say what will stop working, with the number, before it happens.
  const msg = p.in_use > 0
    ? t('plugins.deleteConfirmInUse', { name: p.name, count: p.in_use }, p.in_use)
    : t('plugins.deleteConfirm', { name: p.name });
  if (!window.confirm(msg)) return;
  busyId.value = p.id;
  try {
    await PluginsApi.remove(p.id);
    toast.success(t('plugins.deleted', { name: p.name }));
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.deleteFailed')));
  } finally {
    busyId.value = null;
  }
}

function openUpgrade(p: Plugin) {
  upgradeOf.value = p;
  upgradeFile.value = null;
  upgradeSignature.value = '';
  upgradeFailure.value = '';
  upgradeRestored.value = null;
}

function onUpgradeFile(e: Event) {
  upgradeFile.value = (e.target as HTMLInputElement).files?.[0] ?? null;
}

async function doUpgrade() {
  const p = upgradeOf.value;
  if (!p) return;
  if (!upgradeFile.value) {
    toast.error(t('plugins.upgrade.errFile'));
    return;
  }
  upgrading.value = true;
  upgradeFailure.value = '';
  try {
    const updated = await PluginsApi.upgrade(p.id, upgradeFile.value, upgradeSignature.value.trim());
    Object.assign(p, updated);
    toast.success(t('plugins.upgrade.done', { name: p.name }));
    upgradeOf.value = null;
    await load();
  } catch (e: unknown) {
    // A rejected upgrade is not a lost plugin: the server restored the
    // previous binary and sent back what is running now. Say both, and keep
    // the modal open — the message is what tells the operator which build to
    // fix, and a toast is gone before it can be read twice.
    upgradeFailure.value = extractError(e, t('errors.installFailed'));
    upgradeRestored.value = rolledBackPlugin(e);
    await load();
  } finally {
    upgrading.value = false;
  }
}

function stateTone(state: PluginState): 'emerald' | 'amber' | 'rose' | 'zinc' {
  switch (state) {
    case 'running':
      return 'emerald';
    case 'starting':
      return 'amber';
    case 'failed':
    case 'refused':
      return 'rose';
    default:
      return 'zinc';
  }
}

/** The capabilities the plugin declared, as short chips. */
function capList(p: Plugin): string[] {
  const c = p.capabilities;
  if (!c) return [];
  const out: string[] = [t('plugins.caps.read')];
  if (c.write) out.push(t('plugins.caps.write'));
  else out.push(t('plugins.caps.readOnly'));
  if (c.range) out.push(t('plugins.caps.range'));
  if (c.move) out.push(t('plugins.caps.move'));
  if (c.copy) out.push(t('plugins.caps.copy'));
  if (c.set_mtime) out.push(t('plugins.caps.mtime'));
  if (c.watch) out.push(t('plugins.caps.watch'));
  // presign and multipart were probed by conformance long before this list
  // mentioned them, which made a report show a probe for a capability the
  // row claimed nothing about.
  if (c.presign) out.push(t('plugins.caps.presign'));
  if (c.multipart) out.push(t('plugins.caps.multipart'));
  return out;
}

const anyRunning = computed(() => items.value.some((p) => p.state === 'running'));

/* The explorer's table (DataTable), remembered under `admin.plugins.storage`.
 * Every plugin arrives in one answer, so the table sorts them itself. The
 * conformance column sorts by verdict (failed, never probed, verified) and the
 * load column by what is in flight right now — the two orderings an operator
 * chasing a misbehaving plugin actually wants. */
const STATE_RANK: Record<PluginState, number> = { running: 0, starting: 1, disabled: 2, refused: 3, failed: 4 };
const columns = computed<DataColumn<Plugin>[]>(() => [
  { id: 'name', label: t('common.name'), sortable: true, width: 220 },
  {
    id: 'driver',
    label: t('plugins.fields.driver'),
    sortable: true,
    width: 170,
    sortValue: (p) => p.driver || null,
  },
  {
    id: 'state',
    label: t('plugins.fields.state'),
    sortable: true,
    width: 170,
    sortValue: (p) => STATE_RANK[p.state] ?? 9,
  },
  {
    id: 'capabilities',
    label: t('plugins.fields.capabilities'),
    sortable: true,
    sortDir: 'desc',
    width: 220,
    sortValue: (p) => capList(p).length,
  },
  {
    id: 'conformance',
    label: t('plugins.fields.conformance'),
    sortable: true,
    width: 170,
    sortValue: (p) => (!p.conformance ? 1 : p.conformance.verified ? 2 : 0),
  },
  {
    id: 'load',
    label: t('plugins.fields.load'),
    sortable: true,
    sortDir: 'desc',
    width: 150,
    sortValue: (p) => p.load?.in_flight ?? 0,
  },
  {
    id: 'in_use',
    label: t('plugins.fields.inUse'),
    sortable: true,
    sortDir: 'desc',
    width: 90,
  },
  {
    id: 'enabled',
    label: t('plugins.fields.enabled'),
    sortable: true,
    width: 100,
    sortValue: (p) => (p.enabled ? 1 : 0),
  },
]);

// ── Conformance: did the plugin do what it said it does? ────────────────────

function probeCounts(p: Plugin) {
  const r = p.conformance?.results ?? [];
  return {
    pass: r.filter((x) => x.status === 'pass').length,
    fail: r.filter((x) => x.status === 'fail').length,
    skip: r.filter((x) => x.status === 'skip').length,
  };
}

/**
 * Three outcomes, three tones — and "never probed" is amber, not red: a
 * plugin that ships no self-test has not failed anything, it has simply not
 * been asked, and colouring that like a failure would teach operators to
 * ignore the colour.
 */
function conformanceTone(p: Plugin): 'emerald' | 'amber' | 'rose' {
  if (!p.conformance) return 'amber';
  return p.conformance.verified ? 'emerald' : 'rose';
}

function conformanceLabel(p: Plugin): string {
  if (!p.conformance) return t('plugins.conformance.unverified');
  if (p.conformance.verified) return t('plugins.conformance.verified');
  return t('plugins.conformance.failed', { count: probeCounts(p).fail }, probeCounts(p).fail);
}

function probeTone(status: ProbeStatus): 'emerald' | 'rose' | 'zinc' {
  if (status === 'pass') return 'emerald';
  return status === 'fail' ? 'rose' : 'zinc';
}

/**
 * Failures first. The server sorts them that way on its full path but returns
 * early — unsorted — when a plugin cannot even be listed, which is exactly
 * the report whose failure must not be buried at the bottom.
 */
function sortedProbes(p: Plugin): PluginProbe[] {
  const rank: Record<ProbeStatus, number> = { fail: 0, pass: 1, skip: 2 };
  return [...(p.conformance?.results ?? [])].sort((a, b) => rank[a.status] - rank[b.status]);
}

/** How long one probe took, for the report modal. */
function probeTook(probe: PluginProbe): string {
  return t('plugins.conformance.took', { ms: probe.took_ms });
}

function scratchLabel(p: Plugin): string {
  const s = p.conformance?.scratch;
  return s ? t(`plugins.conformance.scratch.${s}`) : '';
}

// ── Load: what the plugin is doing, and who it is turning away ──────────────

/** Named up front so a saturated plugin is not something you scroll to find. */
const rejecting = computed(() => items.value.filter((p) => p.load.rejected > 0));

/** The row's verbs, behind its one pinned `Actions` control. `Upgrade` only
 *  applies to a binary plugin and `Restart` only to an enabled one, so both
 *  are state-bound exactly as their buttons were; `remove()` keeps its own
 *  confirmation. */
function rowActions(row: Plugin): ContextAction[] {
  return [
    /* ⚠ The conformance report is in the menu too. The badge in the
       Conformance column is still clickable — a value that opens its own
       detail, the way a file name opens its file — but the owner's ruling on
       2026-09-20 was "hepsi menüde olsun": every verb a row has is reachable
       from the one control, so every table reads the same. A row with no
       report has nothing to open, so the entry is not offered. */
    {
      key: 'report',
      label: t('plugins.conformance.view'),
      icon: 'details',
      hidden: !row.conformance,
    },
    {
      key: 'upgrade',
      label: t('plugins.upgrade.action'),
      icon: 'upload',
      hidden: row.kind !== 'binary',
    },
    {
      key: 'restart',
      label: t('plugins.restart'),
      icon: 'refresh',
      disabled: !row.enabled || busyId.value === row.id,
    },
    { key: 'delete', label: t('common.delete'), icon: 'delete', danger: true },
  ];
}

function onRowAction(key: string, row: Plugin) {
  if (key === 'report') reportOf.value = row;
  else if (key === 'upgrade') openUpgrade(row);
  else if (key === 'restart') void restart(row);
  else if (key === 'delete') void remove(row);
}
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <Blocks class="h-6 w-6 text-brand-600 dark:text-brand-400" />
        <h1 class="text-xl font-semibold">{{ t('plugins.title') }}</h1>
      </div>
      <div v-if="!forbidden && !disabledMsg" class="flex items-center gap-2">
        <Button variant="outline" size="sm" :loading="loading" @click="load">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button variant="primary" size="sm" @click="openCreate">
          <Plus class="h-4 w-4" />
          {{ t('plugins.add') }}
        </Button>
      </div>
    </header>

    <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('plugins.subtitle') }}</p>

    <div
      v-if="forbidden"
      class="rounded-xl border border-zinc-200 bg-white p-6 text-sm text-zinc-600 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400"
      data-testid="plugins-forbidden"
    >
      {{ t('plugins.supertenantOnly') }}
    </div>

    <div
      v-else-if="disabledMsg"
      class="rounded-xl border border-zinc-200 bg-white p-6 text-sm text-zinc-600 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400"
      data-testid="plugins-disabled"
    >
      {{ disabledMsg }}
    </div>

    <template v-else>
      <!-- A saturated plugin is an error every one of its users is already
           meeting; it does not wait to be found in a column. -->
      <div
        v-if="rejecting.length"
        class="flex items-start gap-2 rounded-xl border border-rose-200 bg-rose-50 p-3 text-sm text-rose-900 dark:border-rose-900/50 dark:bg-rose-950/40 dark:text-rose-200"
        data-testid="plugins-rejecting"
      >
        <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" />
        <span>{{ t('plugins.load.rejectingBanner', { names: rejecting.map((p) => p.name).join(', ') }) }}</span>
      </div>

      <DataTable
        table-id="admin.plugins.storage"
        :columns="columns"
        :rows="items"
        :loading="loading"
        row-key="id"
        :row-attrs="(p: Plugin) => ({ 'data-testid': `plugin-${p.name}` })"
        :row-actions="(row: Plugin) => rowActions(row)"
        :row-actions-test-id="(row: Plugin) => `plugin-actions-${row.name}`"
        @row-action="(key: string, row: Plugin) => onRowAction(key, row)"
      >
        <template #empty>
          <p>{{ t('plugins.empty') }}</p>
          <p v-if="pluginDir" class="tbl-sub tbl-mono">{{ pluginDir }}</p>
        </template>

        <!-- ⚠ Multi-line cells get ONE wrapper: a DataTable cell is a flex
             row, and a name and the line under it would otherwise sit side
             by side (and a run of badges would run off the edge instead of
             wrapping). -->
        <template #cell-name="{ row }">
          <div>
            <span class="font-medium">{{ row.name }}</span>
            <span class="tbl-sub">
              {{ row.kind === 'remote' ? row.address : row.binary }}
              <template v-if="row.version"> · v{{ row.version }}</template>
            </span>
          </div>
        </template>

        <template #cell-driver="{ row }">
          <div>
            <span v-if="row.driver" class="tbl-mono">plugin:{{ row.driver }}</span>
            <span v-else>—</span>
            <span v-if="row.label" class="tbl-sub">{{ row.label }}</span>
          </div>
        </template>

        <template #cell-state="{ row }">
          <div>
            <Badge :tone="stateTone(row.state)" :title="row.state_error || ''">
              {{ t(`plugins.state.${row.state}`) }}
            </Badge>
            <span v-if="row.restarts > 0" class="tbl-sub">
              {{ t('plugins.restarts', { count: row.restarts }) }}
            </span>
            <!-- The failure is shown, not hidden behind a tooltip: it is the
                 only thing that tells the operator what to fix. -->
            <span
              v-if="row.state_error"
              class="tbl-sub tbl-clamp text-rose-500"
              :title="row.state_error"
              data-testid="plugin-error"
            >
              {{ row.state_error }}
            </span>
          </div>
        </template>

        <template #cell-capabilities="{ row }">
          <div>
            <Badge v-for="c in capList(row)" :key="c" tone="zinc" size="xs" class="me-1">{{ c }}</Badge>
            <span v-if="!row.capabilities">—</span>
          </div>
        </template>

        <template #cell-conformance="{ row }">
          <button
            v-if="row.conformance"
            type="button"
            class="cursor-pointer"
            :data-testid="`plugin-conformance-${row.name}`"
            @click="reportOf = row"
          >
            <Badge :tone="conformanceTone(row)" dot>{{ conformanceLabel(row) }}</Badge>
          </button>
          <Badge
            v-else
            :tone="conformanceTone(row)"
            dot
            :title="t('plugins.conformance.unverifiedHint')"
            :data-testid="`plugin-conformance-${row.name}`"
          >
            {{ conformanceLabel(row) }}
          </Badge>
        </template>

        <template #cell-load="{ row }">
          <div>
            <span class="whitespace-nowrap" :data-testid="`plugin-load-${row.name}`">
              {{ t('plugins.load.inFlight', { current: row.load.in_flight, max: row.load.max_in_flight }) }}
            </span>
            <Badge
              v-if="row.load.rejected > 0"
              tone="rose"
              size="xs"
              :title="t('plugins.load.rejectedTitle')"
              :data-testid="`plugin-rejected-${row.name}`"
            >
              {{ t('plugins.load.rejected', { count: row.load.rejected }) }}
            </Badge>
            <span
              v-else-if="row.load.waited > 0"
              class="tbl-sub whitespace-nowrap text-amber-600 dark:text-amber-400"
              :title="t('plugins.load.waitedTitle')"
            >
              {{ t('plugins.load.waited', { count: row.load.waited }) }}
            </span>
          </div>
        </template>

        <template #cell-in_use="{ row }">
          <span :class="row.in_use > 0 ? 'font-medium' : undefined">{{ row.in_use }}</span>
        </template>

        <template #cell-enabled="{ row }">
          <Toggle
            :model-value="row.enabled"
            :disabled="busyId === row.id"
            @update:model-value="(v: boolean) => toggleEnabled(row, v)"
          />
        </template>

      </DataTable>

      <p v-if="anyRunning" class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('plugins.whereNext') }}</p>
    </template>

    <Modal v-model="showForm" :title="t('plugins.add')" size="lg">
      <form class="space-y-4" @submit.prevent="save">
        <div class="flex gap-2">
          <Button
            v-for="s in (['file', 'url', 'remote'] as const)"
            :key="s"
            type="button"
            size="sm"
            :variant="source === s ? 'primary' : 'outline'"
            :data-testid="`plugin-source-${s}`"
            @click="source = s"
          >
            {{ t(`plugins.source.${s}`) }}
          </Button>
        </div>

        <Input v-model="formName" :label="t('common.name')" placeholder="myfs" />
        <p class="-mt-2 text-xs text-zinc-500">{{ t('plugins.nameHint') }}</p>

        <template v-if="source === 'file'">
          <label class="block text-sm font-medium text-zinc-800 dark:text-zinc-100">
            {{ t('plugins.fields.binary') }}
          </label>
          <input
            type="file"
            class="block w-full text-sm text-zinc-600 file:me-3 file:rounded-lg file:border-0 file:bg-zinc-100 file:px-3 file:py-1.5 file:text-sm dark:text-zinc-300 dark:file:bg-zinc-800"
            data-testid="plugin-file"
            @change="onFile"
          />
          <p class="text-xs text-zinc-500">{{ t('plugins.fileHint') }}</p>
        </template>

        <template v-else-if="source === 'url'">
          <Input v-model="formUrl" label="URL" placeholder="https://example.com/downloads/myfs-linux-amd64" />
          <Input v-model="formSha" label="SHA256" placeholder="a1b2c3…" />
          <p class="-mt-2 text-xs text-zinc-500">{{ t('plugins.shaHint') }}</p>
        </template>

        <template v-else>
          <Input v-model="formAddress" :label="t('plugins.fields.address')" placeholder="http://myfs-plugin:8080" />
          <Input v-model="formToken" :label="t('plugins.fields.token')" type="password" />
          <p class="-mt-2 text-xs text-zinc-500">{{ t('plugins.remoteHint') }}</p>
        </template>

        <template v-if="requiresSignature && source !== 'remote'">
          <Input v-model="formSignature" :label="t('plugins.signature.label')" data-testid="plugin-signature" />
          <p class="-mt-2 text-xs text-zinc-500">{{ t('plugins.signature.required') }}</p>
        </template>

        <p class="rounded-lg bg-amber-50 p-3 text-xs text-amber-900 dark:bg-amber-950/40 dark:text-amber-200">
          {{ t('plugins.trustWarning') }}
        </p>

        <!-- Under warn/off nothing stops a plugin that fails its own claims,
             so the operator learns it here rather than from a broken storage. -->
        <p
          v-if="conformanceMode !== 'enforce'"
          class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200"
          data-testid="plugins-conformance-mode"
        >
          {{ t(`plugins.conformance.mode.${conformanceMode}`) }}
        </p>

        <div class="flex justify-end gap-2">
          <Button type="button" size="sm" variant="ghost" @click="showForm = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" size="sm" variant="primary" :loading="saving" data-testid="plugin-install">
            <Upload class="h-4 w-4" />
            {{ t('plugins.install') }}
          </Button>
        </div>
      </form>
    </Modal>

    <Modal
      :model-value="!!upgradeOf"
      :title="upgradeOf ? t('plugins.upgrade.title', { name: upgradeOf.name }) : ''"
      @update:model-value="upgradeOf = null"
    >
      <form v-if="upgradeOf" class="space-y-4" @submit.prevent="doUpgrade">
        <p class="text-xs text-zinc-500">{{ t('plugins.upgrade.hint') }}</p>

        <label class="block text-sm font-medium text-zinc-800 dark:text-zinc-100">
          {{ t('plugins.fields.binary') }}
        </label>
        <input
          type="file"
          class="block w-full text-sm text-zinc-600 file:me-3 file:rounded-lg file:border-0 file:bg-zinc-100 file:px-3 file:py-1.5 file:text-sm dark:text-zinc-300 dark:file:bg-zinc-800"
          data-testid="plugin-upgrade-file"
          @change="onUpgradeFile"
        />

        <template v-if="requiresSignature">
          <Input
            v-model="upgradeSignature"
            :label="t('plugins.signature.label')"
            data-testid="plugin-upgrade-signature"
          />
          <p class="-mt-2 text-xs text-zinc-500">{{ t('plugins.signature.required') }}</p>
        </template>

        <div
          v-if="upgradeFailure"
          class="space-y-1 rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200"
          data-testid="plugin-upgrade-failed"
        >
          <p class="font-medium">{{ t('plugins.upgrade.rollback') }}</p>
          <p class="whitespace-pre-wrap break-words">{{ upgradeFailure }}</p>
          <p v-if="upgradeRestored">
            {{
              t('plugins.upgrade.restored', {
                state: t(`plugins.state.${upgradeRestored.state}`),
                binary: upgradeRestored.binary || '—',
              })
            }}
          </p>
        </div>

        <div class="flex justify-end gap-2">
          <Button type="button" size="sm" variant="ghost" @click="upgradeOf = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" size="sm" variant="primary" :loading="upgrading" data-testid="plugin-upgrade-submit">
            <ArrowUpFromLine class="h-4 w-4" />
            {{ t('plugins.upgrade.action') }}
          </Button>
        </div>
      </form>
    </Modal>

    <Modal
      :model-value="!!reportOf"
      :title="reportOf ? t('plugins.conformance.reportTitle', { name: reportOf.name }) : ''"
      size="lg"
      @update:model-value="reportOf = null"
    >
      <div v-if="reportOf?.conformance" class="space-y-3" data-testid="plugin-report">
        <div class="flex flex-wrap items-center gap-2">
          <ShieldCheck class="h-4 w-4 text-zinc-400" />
          <Badge :tone="conformanceTone(reportOf)" dot>{{ conformanceLabel(reportOf) }}</Badge>
          <span class="text-xs text-zinc-500">{{ t('plugins.conformance.summary', probeCounts(reportOf)) }}</span>
        </div>

        <p class="text-xs text-zinc-500">
          {{ t('plugins.conformance.ranAt', { when: formatDate(reportOf.conformance.ran_at, locale) }) }} ·
          {{ t('plugins.conformance.scratchLabel', { where: scratchLabel(reportOf) }) }}
        </p>

        <p
          v-if="!reportOf.conformance.verified"
          class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200"
        >
          {{ t('plugins.conformance.refusedNote') }}
        </p>

        <ul class="rule-list rounded-lg">
          <li v-for="probe in sortedProbes(reportOf)" :key="probe.name" class="p-3" :data-testid="`probe-${probe.name}`">
            <div class="flex items-center justify-between gap-2">
              <span class="font-mono text-xs">{{ probe.name }}</span>
              <span class="flex items-center gap-2">
                <span class="text-[11px] text-zinc-500">{{ probeTook(probe) }}</span>
                <Badge :tone="probeTone(probe.status)" size="xs">
                  {{ t(`plugins.conformance.result.${probe.status}`) }}
                </Badge>
              </span>
            </div>
            <!-- Whole, wrapped, never truncated: for a failure this text is
                 what tells the plugin's author what to fix. -->
            <p
              v-if="probe.detail"
              class="mt-1 whitespace-pre-wrap break-words text-xs"
              :class="probe.status === 'fail' ? 'text-rose-600 dark:text-rose-400' : 'text-zinc-500'"
            >
              {{ probe.detail }}
            </p>
          </li>
        </ul>

        <div class="flex justify-end">
          <Button type="button" size="sm" variant="ghost" @click="reportOf = null">{{ t('common.close') }}</Button>
        </div>
      </div>
    </Modal>
  </section>
</template>
