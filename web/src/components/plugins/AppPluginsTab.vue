<script setup lang="ts">
/**
 * AppPluginsTab — the installed WebAssembly apps and the runtime they run in.
 *
 * The runtime banner comes first because it decides whether anything below
 * can work: a disabled runtime, a CPU the engine cannot run on, a
 * signed-only instance. Then the list (the explorer's own table, DataTable —
 * resizable, sortable, its Actions control pinned right), the install wizard
 * and the detail drawer.
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { ArrowUpFromLine, Blocks, Info, Plus, RefreshCcw, Trash2, TriangleAlert } from 'lucide-vue-next';

import { AppPluginsApi, engineName, type AppPlugin, type AppPluginRuntime, type AppPluginState } from '@/api/appPlugins';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { pluginLabelOf, invalidatePluginActions } from '@brftech/filex-core';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Toggle from '@/components/ui/Toggle.vue';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';
import EmptyState from '@/components/ui/EmptyState.vue';
import AppPluginInstallWizard from './AppPluginInstallWizard.vue';
import AppPluginLanguages from './AppPluginLanguages.vue';
import { loadOfferedLocales } from '@/i18n';

const emit = defineEmits<{
  /** The list answered — the page uses it to pick the default tab. */
  (e: 'loaded', info: { enabled: boolean; count: number }): void;
}>();

const { t, locale } = useI18n();
const toast = useToastStore();
const router = useRouter();

const items = ref<AppPlugin[]>([]);
const runtime = ref<AppPluginRuntime | null>(null);
const loading = ref(false);
const forbidden = ref(false);
const busyId = ref<number | null>(null);

const wizardOpen = ref(false);
const upgradeOf = ref<AppPlugin | null>(null);

async function load() {
  loading.value = true;
  forbidden.value = false;
  try {
    const res = await AppPluginsApi.list();
    items.value = res.plugins;
    runtime.value = res.runtime;
    emit('loaded', { enabled: res.runtime.enabled, count: res.plugins.length });
  } catch (e: unknown) {
    const err = e as { response?: { status?: number } };
    if (err?.response?.status === 403) forbidden.value = true;
    else toast.error(extractError(e, t('errors.loadFailed')));
    emit('loaded', { enabled: false, count: 0 });
  } finally {
    loading.value = false;
  }
}

onMounted(load);

/** The list changed: the explorer's cached menu rows are stale too. */
async function reload() {
  invalidatePluginActions();
  // An app may add (or take away) a LANGUAGE: re-read the offered list so
  // every picker on the page follows at once, not after a reload.
  void loadOfferedLocales();
  await load();
}

function labelFor(p: AppPlugin): string {
  return pluginLabelOf(p.label, locale.value) || p.name;
}

function stateTone(state: AppPluginState): 'emerald' | 'amber' | 'rose' | 'zinc' {
  if (state === 'running') return 'emerald';
  if (state === 'failed' || state === 'refused') return 'rose';
  return 'zinc';
}

function stateLabel(state: AppPluginState): string {
  const known = ['running', 'disabled', 'refused', 'failed'];
  return known.includes(state) ? t(`appPlugins.state.${state}`) : state;
}

async function toggleEnabled(p: AppPlugin, enabled: boolean) {
  busyId.value = p.id;
  try {
    const updated = await AppPluginsApi.setEnabled(p.id, enabled);
    Object.assign(p, updated);
    invalidatePluginActions();
    void loadOfferedLocales(); // a pack switched off takes its language with it
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.updateFailed')));
    await load();
  } finally {
    busyId.value = null;
  }
}

async function remove(p: AppPlugin) {
  if (!window.confirm(t('appPlugins.deleteConfirm', { name: labelFor(p) }))) return;
  busyId.value = p.id;
  try {
    await AppPluginsApi.remove(p.id);
    toast.success(t('appPlugins.deleted', { name: labelFor(p) }));
    await reload();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.deleteFailed')));
  } finally {
    busyId.value = null;
  }
}

function onInstalled(p: AppPlugin) {
  toast.success(t('appPlugins.installed', { name: labelFor(p) }));
  wizardOpen.value = false;
  upgradeOf.value = null;
  void reload();
}

function onUpgraded(p: AppPlugin) {
  toast.success(t('appPlugins.upgraded', { name: labelFor(p) }));
  upgradeOf.value = null;
  void reload();
}

const engines = computed(() => Object.entries(runtime.value?.engines ?? {}));

/* The explorer's table (DataTable), remembered under `admin.apps`. Every
 * installed app arrives in one answer, so the table sorts them itself; the
 * label sorts by the words on screen (the viewer's language), the state by
 * trouble first when reversed, the permissions by how many. */
const STATE_RANK: Record<string, number> = { running: 0, disabled: 1, refused: 2, failed: 3 };
const columns = computed<DataColumn<AppPlugin>[]>(() => [
  { id: 'name', label: t('appPlugins.fields.name'), sortable: true, width: 200 },
  {
    /* ⚠ 240, not the 200 the other name-ish columns get: a language pack's
     * cell is the widest thing this table draws — the pack's name AND the
     * "Language pack" badge on one line — and at 180 the name was cut to
     * "Spanish la…" in the one row the Apps list exists to explain. The
     * truncation stays as the fallback for an extreme name; it is no longer
     * what an ordinary pack gets. (Measured at 958, 1280 and 1440 by
     * e2e/shots/langpack.mjs, which refuses the picture if the cell overlaps
     * itself or the table outgrows its box.) */
    id: 'label',
    label: t('appPlugins.fields.label'),
    sortable: true,
    width: 240,
    sortValue: (p) => labelFor(p),
  },
  {
    id: 'version',
    label: t('appPlugins.fields.version'),
    sortable: true,
    width: 100,
    format: (p) => p.version || '—',
    sortValue: (p) => p.version || null,
  },
  {
    id: 'state',
    label: t('appPlugins.fields.state'),
    sortable: true,
    width: 170,
    sortValue: (p) => STATE_RANK[p.state] ?? 9,
  },
  /* uyan:s1 — a column of its own, and deliberately not a second thing
   * stuffed into `state`. "Wakes hourly" is not a state (a scheduled app is
   * `running` like any other) and it is not an action, so it belongs in
   * neither the state cell nor the row's one pinned menu — it is a fact about
   * the app, in the cell whose header says what the fact is. */
  {
    id: 'schedule',
    label: t('appPlugins.fields.schedule'),
    sortable: true,
    sortDir: 'desc',
    width: 150,
    sortValue: (p) => (p.scheduled ? 1 : 0),
  },
  {
    id: 'permissions',
    label: t('appPlugins.fields.permissions'),
    sortable: true,
    sortDir: 'desc',
    width: 130,
    sortValue: (p) => p.permissions.length,
  },
  {
    id: 'enabled',
    label: t('appPlugins.fields.enabled'),
    sortable: true,
    width: 100,
    sortValue: (p) => (p.enabled ? 1 : 0),
  },
]);

/** The row's verbs, behind its one pinned `Actions` control. They were two
 *  outline buttons and a bare bin icon in a 220px column; `Remove` was the
 *  only one of the three with no name on screen. `remove()` keeps its own
 *  confirmation. */
function rowActions(row: AppPlugin): ContextAction[] {
  return [
    { key: 'details', label: t('appPlugins.actions.details'), icon: 'details' },
    { key: 'upgrade', label: t('appPlugins.actions.upgrade'), icon: 'upload' },
    {
      key: 'remove',
      label: t('appPlugins.actions.remove'),
      icon: 'delete',
      danger: true,
      disabled: busyId.value === row.id,
    },
  ];
}

/** "Details" is the app's own PAGE (views/AppPluginPage.vue), not a dialog. */
function onRowAction(key: string, row: AppPlugin) {
  if (key === 'details') void router.push({ name: 'plugins.app', params: { name: row.name } });
  else if (key === 'upgrade') upgradeOf.value = row;
  else if (key === 'remove') void remove(row);
}
</script>

<template>
  <section class="space-y-4" data-testid="app-plugins">
    <header class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <Blocks class="h-6 w-6 text-brand-600 dark:text-brand-400" />
        <h1 class="text-xl font-semibold">{{ t('appPlugins.title') }}</h1>
      </div>
      <div v-if="!forbidden" class="flex items-center gap-2">
        <Button variant="outline" size="sm" :loading="loading" @click="load">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button
          variant="primary"
          size="sm"
          :disabled="runtime !== null && !runtime.enabled"
          data-testid="app-plugin-add"
          @click="wizardOpen = true"
        >
          <Plus class="h-4 w-4" />
          {{ t('appPlugins.install') }}
        </Button>
      </div>
    </header>

    <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('appPlugins.subtitle') }}</p>

    <div
      v-if="forbidden"
      class="rounded-xl border border-zinc-200 bg-white p-6 text-sm text-zinc-600 dark:border-zinc-800 dark:bg-zinc-950 dark:text-zinc-400"
      data-testid="app-plugins-forbidden"
    >
      {{ t('appPlugins.supertenantOnly') }}
    </div>

    <template v-else>
      <!-- Runtime banner: can apps run here at all, and with which engines? -->
      <div
        v-if="runtime"
        class="flex flex-wrap items-start gap-3 rounded-xl border p-3 text-sm"
        :class="runtime.enabled && runtime.arch_ok
          ? 'border-emerald-200 bg-emerald-50 text-emerald-900 dark:border-emerald-900/50 dark:bg-emerald-950/30 dark:text-emerald-200'
          : 'border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200'"
        data-testid="app-plugins-runtime"
      >
        <component :is="runtime.enabled && runtime.arch_ok ? Info : TriangleAlert" class="mt-0.5 h-4 w-4 shrink-0" />
        <div class="min-w-0 flex-1 space-y-1">
          <p class="font-medium">
            {{ runtime.enabled ? t('appPlugins.runtime.on') : t('appPlugins.runtime.off') }}
          </p>
          <p v-if="!runtime.arch_ok">{{ t('appPlugins.runtime.archBad') }}</p>
          <p v-if="runtime.disabled_reason" class="break-words">{{ runtime.disabled_reason }}</p>
          <p v-if="runtime.requires_signature">{{ t('appPlugins.runtime.signature') }}</p>
          <div v-if="engines.length" class="flex flex-wrap items-center gap-1 pt-1">
            <span class="text-xs">{{ t('appPlugins.runtime.engines') }}:</span>
            <Badge
              v-for="[name, ok] in engines"
              :key="name"
              :tone="ok ? 'emerald' : 'zinc'"
              size="xs"
              :data-testid="`engine-${name}`"
            >{{ engineName(name, runtime?.engine_names) }}</Badge>
          </div>
        </div>
      </div>

      <DataTable
        table-id="admin.apps"
        :columns="columns"
        :rows="items"
        row-key="id"
        :loading="loading"
        :row-actions="(row: AppPlugin) => rowActions(row)"
        :row-actions-test-id="(row: AppPlugin) => `app-plugin-actions-${row.name}`"
        @row-action="(key: string, row: AppPlugin) => onRowAction(key, row)"
      >
        <template #empty>
          <EmptyState
            :icon="Blocks"
            :title="t('appPlugins.empty.title')"
            :description="t('appPlugins.empty.description')"
            size="sm"
          >
            <template #action>
              <Button
                variant="primary"
                size="sm"
                :disabled="runtime !== null && !runtime.enabled"
                @click="wizardOpen = true"
              >
                <Plus class="h-4 w-4" />
                {{ t('appPlugins.install') }}
              </Button>
            </template>
          </EmptyState>
        </template>

        <!-- ⚠ Two-line cells get ONE wrapper: a DataTable cell is a flex
             row, and the lines would otherwise sit side by side. -->
        <template #cell-name="{ row }">
          <div>
            <div class="font-mono text-xs" :data-testid="`app-plugin-${row.name}`">{{ row.name }}</div>
            <div class="text-[11px] text-zinc-500">
              {{ t(`appPlugins.source.${row.source}`, row.source) }}
              <template v-if="row.signed"> · {{ t('appPlugins.detail.signed') }}</template>
            </div>
          </div>
        </template>
        <!-- ⚠⚠ ONE wrapper again, and this cell is why the rule is written
             down: the label, the kind badge and the coverage lines were three
             SIBLINGS of a 180px flex cell, so at every width the badge was
             drawn over the label and the coverage line over the badge (QA,
             958px and 1440px, English too). `ms-1` and `mt-1` on siblings of a
             flex row are margins on flex items, not a second line.
             The label and the badge share a line; what the pack covers is the
             line under them.

             ⚠ `flex-wrap`, so the badge DROPS to its own line rather than
             taking room from the name. A flex container collects its items
             into lines before it shrinks anything, so a name whose own width
             plus the badge's would not fit gets the whole line to itself and
             is printed in full; `truncate` on the name is then only for a name
             too long for the line even alone. Without it the pack's name came
             out as "Spanish languag…" at every width, in the one row this
             table exists to explain (measured 2026-09-23: cell 192px at 1440,
             the name needs 135px, the badge 90px). -->
        <template #cell-label="{ row }">
          <div class="min-w-0 py-1" :data-testid="`app-plugin-cell-label-${row.name}`">
            <div class="flex min-w-0 flex-wrap items-center gap-1">
              <span class="truncate font-medium" :title="labelFor(row)" :data-testid="`app-plugin-label-${row.name}`">{{ labelFor(row) }}</span>
              <!-- dil:paket — a language pack says so, and says how much of THIS
                   filex's interface each of its languages covers. -->
              <Badge
                v-if="row.kind === 'language_pack'"
                tone="brand"
                size="xs"
                class="shrink-0"
                :data-testid="`app-plugin-kind-${row.name}`"
              >
                {{ t('appPlugins.kind.languagePack') }}
              </Badge>
            </div>
            <AppPluginLanguages v-if="row.languages?.length" class="mt-1" :languages="row.languages" />
          </div>
        </template>
        <template #cell-state="{ row }">
          <div>
            <Badge :tone="stateTone(row.state)" :title="row.state_error || ''" dot>
              {{ stateLabel(row.state) }}
            </Badge>
            <div
              v-if="row.state_error"
              class="mt-0.5 max-w-xs break-words text-[11px] text-rose-500"
              data-testid="app-plugin-error"
            >
              {{ row.state_error }}
            </div>
          </div>
        </template>
        <!-- uyan:s1 — the badge, and nothing at all for an app nobody wakes.
             An em dash in every other row would be six rows of noise for a
             fact that only matters where it is true. -->
        <template #cell-schedule="{ row }">
          <Badge
            v-if="row.scheduled"
            tone="sky"
            size="xs"
            :data-testid="`app-plugin-scheduled-${row.name}`"
          >
            {{ t('appPlugins.scheduledBadge') }}
          </Badge>
        </template>
        <template #cell-permissions="{ row }">
          <span :title="row.permissions.join(', ')">
            {{ t('appPlugins.permissionsCount', { count: row.permissions.length }, row.permissions.length) }}
          </span>
        </template>
        <template #cell-enabled="{ row }">
          <Toggle
            :model-value="row.enabled"
            :disabled="busyId === row.id"
            :name="`app-plugin-enabled-${row.id}`"
            @update:model-value="(v: boolean) => toggleEnabled(row, v)"
          />
        </template>
      </DataTable>
    </template>

    <AppPluginInstallWizard
      :model-value="wizardOpen || !!upgradeOf"
      :requires-signature="runtime?.requires_signature === true"
      :upgrade="upgradeOf"
      :installed="items"
      @update:model-value="(v: boolean) => { if (!v) { wizardOpen = false; upgradeOf = null; } }"
      @installed="onInstalled"
      @upgraded="onUpgraded"
    />
  </section>
</template>
