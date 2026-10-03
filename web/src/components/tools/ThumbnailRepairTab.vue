<script setup lang="ts">
/**
 * Admin → Tools → Thumbnail repair.
 *
 * A repair draws thumbnails again for one file, one folder with everything in
 * it, one storage, or every storage this administrator reaches. `Fix` draws
 * what is missing, failed, skipped or out of date; `Rebuild` draws all of it.
 * It runs as an ops job (in the tray, stoppable, carried on after a restart)
 * and the page follows it until it ends, then says what it did. The walk is
 * the one `filex thumb backfill` takes.
 *
 * Below it: the SVG limits (ThumbLimitsCard, the same card Settings shows),
 * who drew the thumbnails (0.50: filex's own drawer, or an app, counted) and
 * the files that have no thumbnail - or got one only after an earlier
 * handler failed - each with why and the handlers asked, in order. A row's
 * "Try again" repairs that one file.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Play, RefreshCcw } from 'lucide-vue-next';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

import {
  toolsApi,
  type ThumbAttempt,
  type ThumbGenerator,
  type ThumbProblem,
  type ThumbRepairMode,
  type ThumbRepairStatus,
} from '@/api/tools';
import { extractError } from '@/api/client';
import { useStoragesStore } from '@/stores/storages';
import { useToastStore } from '@/stores/toast';
import { formatBytes, formatDate, formatNumber } from '@/lib/format';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Badge from '@/components/ui/Badge.vue';
import ThumbLimitsCard from './ThumbLimitsCard.vue';

const { t, locale } = useI18n();
const toast = useToastStore();
const storages = useStoragesStore();

/* ── the ask ─────────────────────────────────────────────────────────── */

/** '' is every storage; otherwise the storage NAME (the path's adapter). */
const storageName = ref('');
const folder = ref<string | number | null>('');
const mode = ref<ThumbRepairMode>('fix');

const storageOptions = computed(() => [
  { value: '', label: t('tools.thumbs.all_storages') },
  ...storages.items.map((s) => ({ value: s.name, label: s.name })),
]);
const modeOptions = computed(() => [
  { value: 'fix', label: t('tools.thumbs.mode_fix') },
  { value: 'rebuild', label: t('tools.thumbs.mode_rebuild') },
]);
const modeHint = computed(() => (mode.value === 'fix' ? t('tools.thumbs.mode_fix_hint') : t('tools.thumbs.mode_rebuild_hint')));

/** The qualified path the server is asked with. */
const askedPath = computed(() => {
  if (!storageName.value) return '';
  const rel = String(folder.value ?? '').trim().replace(/^\/+/, '');
  return `${storageName.value}://${rel}`;
});

/* ── the run ─────────────────────────────────────────────────────────── */

const run = ref<ThumbRepairStatus | null>(null);
const last = ref<ThumbRepairStatus | null>(null);
const starting = ref(false);
const stopping = ref(false);
const POLL_MS = 1500;
let poll: ReturnType<typeof setTimeout> | undefined;
let alive = true;

const running = computed(() => run.value?.running === true);
const pct = computed(() => {
  const total = run.value?.total ?? 0;
  return total > 0 ? Math.min(100, Math.floor(((run.value?.processed ?? 0) / total) * 100)) : 0;
});

async function start(path = askedPath.value, m: ThumbRepairMode = mode.value) {
  if (starting.value || running.value) return;
  starting.value = true;
  try {
    await follow(await toolsApi.thumbRepair({ path, mode: m }));
  } catch (err: any) {
    const data = err?.response?.data;
    if (err?.response?.status === 409 && data?.code === 'BUSY') {
      toast.error(t('tools.thumbs.busy'));
      if (data.job?.running) await follow(data.job);
      return;
    }
    if (data?.code === 'PATH_NOT_FOUND') {
      toast.error(t('tools.thumbs.path_not_found'));
      return;
    }
    toast.error(extractError(err, t('errors.actionFailed')));
  } finally {
    starting.value = false;
  }
}

/** Takes a run's status (POST, 409, poll) and keeps following it or ends it. */
async function follow(st: ThumbRepairStatus) {
  if (st.running) {
    run.value = st;
    schedule();
    return;
  }
  run.value = null;
  if (!st.started_at) return;
  last.value = st;
  if (st.cancelled) toast.warn(t('tools.thumbs.stopped'));
  else if (st.status === 'failed' && st.error) toast.error(t('tools.thumbs.run_failed', { error: st.error }));
  else toast.success(t('tools.thumbs.done'));
  await Promise.all([loadProblems(), loadGenerators()]);
}

function schedule() {
  unschedule();
  if (alive) poll = setTimeout(tick, POLL_MS);
}
function unschedule() {
  if (poll !== undefined) clearTimeout(poll);
  poll = undefined;
}
async function tick() {
  poll = undefined;
  try {
    await follow(await toolsApi.thumbRepairStatus());
  } catch {
    schedule();
  }
}

async function stop() {
  const id = run.value?.op_id;
  if (!id || stopping.value) return;
  stopping.value = true;
  try {
    await toolsApi.cancel(id);
  } catch (err) {
    toast.error(extractError(err, t('errors.actionFailed')));
  } finally {
    stopping.value = false;
  }
}

function refusalText(code: string): string {
  return ['sync_running', 'sync_aborted', 'never_synced'].includes(code)
    ? t(`tools.thumbs.refused.${code}`)
    : code;
}

/* ── the files without a thumbnail ───────────────────────────────────── */

const problems = ref<ThumbProblem[]>([]);
const truncated = ref(false);
const loadingProblems = ref(false);

async function loadProblems() {
  loadingProblems.value = true;
  try {
    const res = await toolsApi.thumbProblems();
    problems.value = res.items;
    truncated.value = res.truncated;
  } catch (err) {
    toast.error(extractError(err, t('errors.actionFailed')));
  } finally {
    loadingProblems.value = false;
  }
}

/* heic_codec: ImageMagick is there and cannot decode a HEIC (libheif without
   its HEVC decoder); the server measures it with a sample. */
const NO_TOOL_KINDS = ['video', 'audio', 'pdf', 'office', 'heif', 'heic_codec'];

/** A reason's parts, as a problem row and each of its attempts carry them. */
interface ReasonParts {
  code?: string;
  limit?: number;
  tool?: string;
  app?: string;
  detail?: string;
  tries?: number;
}

/** The tries the OnlyOffice document server gets at a document (thumb/office.go). */
const OFFICE_TRIES = 6;

/** Megabytes (2^20) and seconds, in the units the limit settings use. */
const mb = (bytes: number | undefined) => formatNumber(Math.round((bytes ?? 0) / 1048576), locale.value);
const seconds = (ms: number | undefined) => formatNumber(Math.round((ms ?? 0) / 1000), locale.value);

function reasonText(p: ReasonParts): string {
  switch (p.code) {
    case 'svg_too_large':
      // In the settings' own unit (MB = 2^20 bytes), so it reads as the number
      // the limits card shows.
      return t('tools.thumbs.reason.svg_too_large', { limit: mb(p.limit) });
    case 'svg_timeout':
      return t('tools.thumbs.reason.svg_timeout', { limit: seconds(p.limit) });
    case 'no_engine':
      return t('tools.thumbs.reason.no_engine');
    case 'archive_encrypted':
      return t('tools.thumbs.reason.archive_encrypted');
    case 'archive_too_large':
      return t('tools.thumbs.reason.archive_too_large');
    case 'no_tool':
      // The kind whose program is missing (thumb/notool.go); a kind this
      // client does not know yet still says what happened.
      return NO_TOOL_KINDS.includes(p.tool ?? '')
        ? t(`tools.thumbs.reason.no_tool.${p.tool}`)
        : t('tools.thumbs.reason.no_tool.other');
    // Thumbnails drawn by apps (0.50, docs/thumbnails.md).
    case 'no_handler':
      return t('tools.thumbs.reason.no_handler');
    case 'app_failed':
      return t('tools.thumbs.reason.app_failed', { app: p.app ?? '' });
    case 'app_timeout':
      return t('tools.thumbs.reason.app_timeout', { app: p.app ?? '', limit: seconds(p.limit) });
    case 'app_too_large':
      return t('tools.thumbs.reason.app_too_large', { app: p.app ?? '', limit: mb(p.limit) });
    // The OnlyOffice document server's answers (0.50, docs/thumbnails.md).
    case 'oo_corrupt':
      return t('tools.thumbs.reason.oo_corrupt');
    case 'oo_password':
      return t('tools.thumbs.reason.oo_password');
    case 'oo_too_large':
      // 0: the document server's own limit, which filex does not know.
      return p.limit ? t('tools.thumbs.reason.oo_too_large', { limit: mb(p.limit) }) : t('tools.thumbs.reason.oo_too_large_ds');
    case 'oo_retry':
      return (p.tries ?? 0) >= OFFICE_TRIES
        ? t('tools.thumbs.reason.oo_retry_done', { tries: p.tries ?? 0, detail: p.detail ?? '' })
        : t('tools.thumbs.reason.oo_retry', { tries: p.tries ?? 0, detail: p.detail ?? '' });
    default:
      return t('tools.thumbs.reason.failed');
  }
}

/** A handler of the chain as a person reads it: filex's own, or the app with
 *  its version. */
function handlerName(a: { handler?: string; app?: string; version?: string }): string {
  if (!a.handler || a.handler === 'builtin') return t('defaultApps.builtin');
  if (a.handler === 'onlyoffice') return t('defaultApps.onlyoffice');
  return [a.app || a.handler, a.version].filter(Boolean).join(' ');
}

function reason(p: ThumbProblem): string {
  if (p.code === 'fell_back') {
    const first = (p.attempts ?? []).find((a) => !a.ok);
    const drew = (p.attempts ?? []).find((a) => a.ok) ?? { handler: p.generator };
    return t('tools.thumbs.reason.fell_back', { generator: handlerName(drew), first: first ? handlerName(first) : '' });
  }
  return reasonText(p);
}

/** One handler asked, and what it answered. */
function attemptText(a: ThumbAttempt): string {
  return `${handlerName(a)}: ${a.ok ? t('tools.thumbs.attempt.ok') : reasonText(a)}`;
}

/** The handlers asked, in order: "PNG+ 1.2: could not draw it → filex (built-in): drew it". */
function chainText(p: ThumbProblem): string {
  return (p.attempts ?? []).map(attemptText).join(' → ');
}

/* ── who drew the thumbnails (0.50) ──────────────────────────────────── */

const generators = ref<ThumbGenerator[]>([]);

async function loadGenerators() {
  try {
    generators.value = (await toolsApi.thumbGenerators()) ?? [];
  } catch {
    // The page works without it.
    generators.value = [];
  }
}

function generatorName(g: ThumbGenerator): string {
  if (!g.generator) return t('tools.thumbs.generators.legacy');
  const [handler, version] = g.generator.split('@');
  return handlerName({ handler, app: g.app, version: g.version ?? version });
}

const columns = computed<DataColumn<ThumbProblem>[]>(() => [
  { id: 'name', label: t('tools.thumbs.col.name'), sortable: true, width: 260, lead: true, sortValue: (r) => r.path },
  { id: 'storage', label: t('tools.thumbs.col.storage'), sortable: true, width: 140 },
  { id: 'reason', label: t('tools.thumbs.col.reason'), sortable: true, width: 280, sortValue: (r) => r.code },
  { id: 'handlers', label: t('tools.thumbs.col.handlers'), width: 280, sortValue: (r) => chainText(r) },
  { id: 'size', label: t('tools.thumbs.col.size'), sortable: true, align: 'right', width: 100, sortValue: (r) => r.size },
  {
    id: 'attempted_at',
    label: t('tools.thumbs.col.attempted'),
    sortable: true,
    sortDir: 'desc',
    width: 160,
    sortValue: (r) => (r.attempted_at ? Date.parse(r.attempted_at) : null),
  },
]);

function rowActions(): ContextAction[] {
  return [{ key: 'retry', label: t('tools.thumbs.retry'), icon: 'refresh' }];
}
function onRowAction(key: string, row: ThumbProblem) {
  if (key === 'retry') void start(row.path, 'fix');
}

onMounted(async () => {
  void storages.fetch();
  void loadProblems();
  void loadGenerators();
  try {
    const st = await toolsApi.thumbRepairStatus();
    if (st.running) await follow(st);
    else if (st.started_at) last.value = st;
  } catch {
    // The page works without it.
  }
});
onBeforeUnmount(() => {
  alive = false;
  unschedule();
});
</script>

<template>
  <div class="space-y-4">
    <form class="card card-body space-y-3" data-testid="thumb-repair-form" @submit.prevent="start()">
      <h2 class="text-base font-semibold">{{ t('tools.thumbs.title') }}</h2>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tools.thumbs.desc') }}</p>
      <div class="grid gap-3 md:grid-cols-3">
        <Select
          v-model="storageName"
          :options="storageOptions"
          :label="t('tools.thumbs.storage')"
          data-testid="thumb-repair-storage"
        />
        <Input
          v-model="folder"
          :label="t('tools.thumbs.path')"
          :placeholder="t('tools.thumbs.path_placeholder')"
          :hint="storageName ? t('tools.thumbs.path_hint') : t('tools.thumbs.path_needs_storage')"
          :disabled="!storageName"
          data-testid="thumb-repair-path"
        />
        <Select
          v-model="mode"
          :options="modeOptions"
          :label="t('tools.thumbs.mode')"
          data-testid="thumb-repair-mode"
        />
      </div>
      <p class="text-xs text-zinc-500 dark:text-zinc-400" data-testid="thumb-repair-mode-hint">{{ modeHint }}</p>
      <div class="flex justify-end">
        <Button type="submit" :loading="starting" :disabled="running" data-testid="thumb-repair-start">
          <Play class="h-4 w-4" />
          {{ t('tools.thumbs.start') }}
        </Button>
      </div>
    </form>

    <div
      v-if="run?.running"
      class="card card-body space-y-2"
      role="status"
      aria-live="polite"
      data-testid="thumb-repair-running"
    >
      <div class="flex flex-wrap items-center justify-between gap-3 text-sm">
        <span class="font-medium">{{ run.queued ? t('tools.thumbs.queued') : t('tools.thumbs.running') }}</span>
        <div class="flex items-center gap-3">
          <span class="tabular-nums fx-tools-muted">
            {{ t('tools.thumbs.progress', { done: formatNumber(run.processed ?? 0, locale), total: formatNumber(run.total ?? 0, locale) }) }}
          </span>
          <Button v-if="run.op_id" variant="ghost" size="sm" :loading="stopping" data-testid="thumb-repair-stop" @click="stop">
            {{ t('tools.thumbs.stop') }}
          </Button>
        </div>
      </div>
      <div
        class="fx-tools-track"
        role="progressbar"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-valuenow="pct"
        :aria-label="t('tools.thumbs.running')"
      >
        <div class="fx-tools-fill" :style="{ width: `${pct}%` }" />
      </div>
      <p class="text-xs fx-tools-muted">{{ t('tools.thumbs.note') }}</p>
    </div>

    <div v-else-if="last" class="card card-body space-y-2" data-testid="thumb-repair-result">
      <div class="flex flex-wrap items-center gap-2 text-sm">
        <span class="font-medium">{{ t('tools.thumbs.last_run') }}</span>
        <bdi dir="ltr" class="tbl-mono">{{ last.path || t('tools.thumbs.all_storages') }}</bdi>
        <Badge tone="zinc">{{ last.mode === 'rebuild' ? t('tools.thumbs.mode_rebuild') : t('tools.thumbs.mode_fix') }}</Badge>
        <span v-if="last.finished_at" class="fx-tools-muted">{{ formatDate(last.finished_at, locale) }}</span>
      </div>
      <dl class="grid grid-cols-2 gap-2 text-sm sm:grid-cols-4">
        <div><dt class="fx-tools-muted">{{ t('tools.thumbs.count.processed') }}</dt><dd class="tabular-nums" data-testid="thumb-repair-processed">{{ formatNumber(last.processed ?? 0, locale) }}</dd></div>
        <div><dt class="fx-tools-muted">{{ t('tools.thumbs.count.ok') }}</dt><dd class="tabular-nums" data-testid="thumb-repair-ok">{{ formatNumber(last.ok ?? 0, locale) }}</dd></div>
        <div><dt class="fx-tools-muted">{{ t('tools.thumbs.count.failed') }}</dt><dd class="tabular-nums" data-testid="thumb-repair-failed">{{ formatNumber(last.failed ?? 0, locale) }}</dd></div>
        <div><dt class="fx-tools-muted">{{ t('tools.thumbs.count.skipped') }}</dt><dd class="tabular-nums" data-testid="thumb-repair-skipped">{{ formatNumber(last.skipped ?? 0, locale) }}</dd></div>
      </dl>
      <ul v-if="last.refused?.length" class="space-y-1 text-sm text-amber-700 dark:text-amber-400" data-testid="thumb-repair-refused">
        <li v-for="r in last.refused" :key="r.storage_id">
          <bdi>{{ r.storage || r.storage_id }}</bdi>: {{ refusalText(r.code) }}
        </li>
      </ul>
    </div>

    <ThumbLimitsCard @saved="loadProblems" />

    <!-- Who drew the thumbnails (0.50): filex's own drawer, or an app. -->
    <div class="card card-body space-y-2" data-testid="thumb-generators">
      <h2 class="text-base font-semibold">{{ t('tools.thumbs.generators.title') }}</h2>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('tools.thumbs.generators.desc') }}</p>
      <p v-if="!generators.length" class="text-sm fx-tools-muted" data-testid="thumb-generators-empty">
        {{ t('tools.thumbs.generators.empty') }}
      </p>
      <ul v-else class="flex flex-wrap gap-2 text-sm">
        <li
          v-for="g in generators"
          :key="g.generator"
          class="inline-flex items-center gap-2 rounded-lg border border-zinc-200 px-2 py-1 dark:border-zinc-800"
          :data-testid="`thumb-generator-${g.generator || 'legacy'}`"
        >
          <span>{{ generatorName(g) }}</span>
          <Badge tone="zinc" size="xs">{{ formatNumber(g.count, locale) }}</Badge>
        </li>
      </ul>
    </div>

    <DataTable
      table-id="admin.tools.thumbs"
      :columns="columns"
      :rows="problems"
      :loading="loadingProblems"
      :empty="t('tools.thumbs.problems_empty')"
      :aria-label="t('tools.thumbs.problems_title')"
      row-key="node_id"
      :foot-note="truncated ? t('tools.thumbs.problems_truncated') : undefined"
      :row-actions="rowActions"
      :row-actions-test-id="(row: ThumbProblem) => `thumb-problem-actions-${row.node_id}`"
      @row-action="(key: string, row: ThumbProblem) => onRowAction(key, row)"
    >
      <template #toolbar>
        <div class="flex w-full items-center justify-between gap-2">
          <h2 class="text-base font-semibold">{{ t('tools.thumbs.problems_title') }}</h2>
          <Button variant="outline" size="sm" :loading="loadingProblems" data-testid="thumb-problems-refresh" @click="loadProblems">
            <RefreshCcw class="h-4 w-4" />
            {{ t('common.refresh') }}
          </Button>
        </div>
      </template>
      <template #cell-name="{ row }">
        <div>
          <bdi class="tbl-clamp" :title="row.path">{{ row.name }}</bdi>
          <span class="tbl-sub"><bdi dir="ltr">{{ row.path }}</bdi></span>
        </div>
      </template>
      <template #cell-storage="{ row }"><bdi>{{ row.storage }}</bdi></template>
      <template #cell-reason="{ row }">
        <!-- The title is the engine's own words for a failure, and the whole
             reason otherwise: a narrow column cuts the limit off the text. -->
        <span class="tbl-clamp" :title="row.detail || reason(row)" data-testid="thumb-problem-reason">{{ reason(row) }}</span>
      </template>
      <template #cell-handlers="{ row }">
        <span class="tbl-clamp" :title="chainText(row)" data-testid="thumb-problem-handlers">{{ chainText(row) }}</span>
      </template>
      <template #cell-size="{ row }"><span class="tabular-nums">{{ formatBytes(row.size, locale) }}</span></template>
      <template #cell-attempted_at="{ row }">
        <span class="whitespace-nowrap">{{ row.attempted_at ? formatDate(row.attempted_at, locale) : '' }}</span>
      </template>
    </DataTable>
  </div>
</template>

<style scoped>
.fx-tools-muted {
  color: var(--fe-text-muted);
}
.fx-tools-track {
  height: 0.375rem;
  overflow: hidden;
  border-radius: 9999px;
  background: var(--fe-border-soft);
}
.fx-tools-fill {
  height: 100%;
  border-radius: 9999px;
  background: var(--fe-primary);
  transition: width 500ms ease;
}
@media (prefers-reduced-motion: reduce) {
  .fx-tools-fill {
    transition: none;
  }
}
</style>
