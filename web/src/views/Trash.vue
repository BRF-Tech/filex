<script setup lang="ts">
/**
 * Trash view — admin overview of every soft-deleted node across storages.
 * Actions: restore (clears deleted_at), purge (hard-delete one), empty
 * (purge all in a storage, optionally limited by age).
 */
import { ref, onMounted, onBeforeUnmount, computed, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useToastStore } from '@/stores/toast';
import { useStoragesStore } from '@/stores/storages';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { usePendingOpsStore } from '@/stores/pendingOps';
import type { PendingOp } from '@/api/ops';
import { trashApi, type TrashEntry, type TrashEmptyStatus } from '@/api/trash';
import Button from '@/components/ui/Button.vue';
import Modal from '@/components/ui/Modal.vue';
import Select from '@/components/ui/Select.vue';
import { DataTable, translate, trashTimeLeft, type ContextAction, type DataColumn } from '@brftech/filex-core';
import { Trash2, RotateCcw, AlertTriangle } from 'lucide-vue-next';
import { formatBytes, formatDate, formatNumber } from '@/lib/format';

const { t, locale } = useI18n();

/** The item's time left — the explorer's own sentence for it (core
 *  `trashTimeLeft`, the `trash.days_remaining*` keys of its table), so the two
 *  Trash screens never word one fact two ways ("0 days" here, "Due for
 *  deletion" there, as they did). */
function timeLeft(ttl: number | null | undefined): string {
  return trashTimeLeft(ttl, (key, vars) => translate(locale.value, key, vars));
}
const toast = useToastStore();
const storages = useStoragesStore();
const caps = useCapabilitiesStore();
const pendingOps = usePendingOpsStore();

const entries = ref<TrashEntry[]>([]);
const total = ref(0);
const loading = ref(false);
const selectedStorage = ref<number | undefined>(undefined);
const limit = ref(50);
const offset = ref(0);

const showEmptyDialog = ref(false);
/** The days box: a number, or '' once it has been typed in and cleared. */
const olderThanDays = ref<number | string | undefined>(undefined);

async function load() {
  loading.value = true;
  try {
    const res = await trashApi.list({
      storage_id: selectedStorage.value,
      limit: limit.value,
      offset: offset.value,
    });
    entries.value = res.entries;
    total.value = res.total;
  } catch (err: any) {
    toast.error(err?.response?.data?.error ?? String(err));
  } finally {
    loading.value = false;
  }
}

/* ── Restore and purge ────────────────────────────────────────────────────
 *
 * ⚠⚠ Both ran inside the request. A folder is moved back, or purged, one
 * object at a time, and this page's HTTP client gives up after 30 s: the admin
 * read "the server could not be reached" beside a raw "AxiosError: timeout of
 * 30000ms exceeded" while the server carried on, and a second press was
 * refused by the half that had already come back. A server that runs them as
 * jobs of its queue says so (`capabilities.queued`): the page asks for a job,
 * the row says what is happening to it and takes no second press, and the page
 * says how it ended when the job does. The tray follows the same job. */
type RowJob = 'restore' | 'purge';
/** The rows being worked on, by entry id. */
const working = ref<Record<number, RowJob>>({});
/** The queued jobs this page is waiting for, by op id. */
const following = new Map<number, { entry: TrashEntry; job: RowJob }>();

function serverQueues(job: RowJob): boolean {
  return caps.data.queued?.includes(job) === true;
}

function markWorking(id: number, job: RowJob | null) {
  const next = { ...working.value };
  if (job) next[id] = job;
  else delete next[id];
  working.value = next;
}

function follow(entry: TrashEntry, job: RowJob, ops: PendingOp[]) {
  if (ops.length === 0) {
    markWorking(entry.id, null);
    void load();
    return;
  }
  for (const op of ops) {
    following.set(op.id, { entry, job });
    pendingOps.track(op.id);
  }
}

watch(
  () => pendingOps.items,
  (items) => {
    for (const it of items) {
      const f = following.get(it.op.id);
      if (!f || !['done', 'error', 'cancelled'].includes(it.op.status)) continue;
      following.delete(it.op.id);
      markWorking(f.entry.id, null);
      sayJobEnd(f.entry, f.job, it.op);
      void load();
    }
  },
);

function sayJobEnd(entry: TrashEntry, job: RowJob, op: PendingOp) {
  if (op.status === 'done') {
    toast.success(t(job === 'restore' ? 'trash.restored' : 'trash.purged', { name: entry.name }));
    return;
  }
  // Stopped from the tray before it ran: the reloaded list says what is left.
  if (op.status === 'cancelled') return;
  const reason = op.error_message ?? '';
  if (job === 'restore' && /already exists/i.test(reason)) {
    toast.error(t('trash.restore_taken', { name: entry.name }));
    return;
  }
  toast.error(t(job === 'restore' ? 'trash.restore_failed' : 'trash.purge_failed', { name: entry.name, reason }));
}

/** A refused request, said. One that got no answer at all has been said
 *  already, once for the page, by the client (errors.network); printing it here
 *  put "AxiosError: timeout of 30000ms exceeded" beside that. */
function sayRefusal(err: any) {
  if (!err?.response) return;
  toast.error(err.response.data?.error ?? t('errors.generic'));
}

async function restore(entry: TrashEntry) {
  if (working.value[entry.id]) return;
  markWorking(entry.id, 'restore');
  try {
    if (serverQueues('restore')) {
      const { ops } = await trashApi.restoreQueued([entry.id]);
      follow(entry, 'restore', ops);
      return;
    }
    await trashApi.restore(entry.id);
    markWorking(entry.id, null);
    toast.success(t('trash.restored', { name: entry.name }));
    await load();
  } catch (err: any) {
    markWorking(entry.id, null);
    // 409 EXISTS: something holds the original path, and the server refused
    // rather than overwrite it. Said in the reader's language — the server's
    // own sentence is English and names no remedy.
    if (err?.response?.status === 409 && err?.response?.data?.code === 'EXISTS') {
      toast.error(t('trash.restore_taken', { name: err.response.data.name || entry.name }));
      return;
    }
    sayRefusal(err);
  }
}

async function purge(entry: TrashEntry) {
  if (working.value[entry.id]) return;
  if (!confirm(t('trash.purge_confirm', { name: entry.name }))) return;
  markWorking(entry.id, 'purge');
  try {
    if (serverQueues('purge')) {
      const { op } = await trashApi.purgeQueued(entry.id);
      follow(entry, 'purge', op ? [op] : []);
      return;
    }
    await trashApi.purge(entry.id);
    markWorking(entry.id, null);
    toast.success(t('trash.purged', { name: entry.name }));
    await load();
  } catch (err: any) {
    markWorking(entry.id, null);
    sayRefusal(err);
  }
}

/* ── Empty trash ─────────────────────────────────────────────────────────
 *
 * ⚠⚠ This used to close the dialog, send one request and wait on it — and a
 * large trash is not emptied inside any request. 61,844 files took the server
 * the better part of an hour; this page's HTTP client gave up at thirty
 * seconds, nginx at sixty, and the admin saw no change, no progress and at
 * best an English timeout toast, so they pressed the button again. The server
 * now answers within seconds — the final count if the purge is done, its
 * progress if it is still going (`running: true`) — and the page follows a
 * running purge until it ends, on GET /admin/trash/empty. */
const emptyRun = ref<TrashEmptyStatus | null>(null);
const emptyStarting = ref(false);
const emptying = computed(() => emptyRun.value?.running === true);
const EMPTY_POLL_MS = 1500;
let emptyPoll: ReturnType<typeof setTimeout> | undefined;
let alive = true;

/** The days box as the server reads it: undefined is "any age", null is a
 *  value that cannot be sent.
 *
 *  ⚠ `v-model.number` hands back '' once the box has been typed in and
 *  cleared, and '' went out as `"older_than_days": ""` — a value the server
 *  could not read, and then did not read the storage_id beside it either:
 *  "empty this storage's trash" emptied every storage's. Cleared means any
 *  age, so it is sent as nothing. A number that is not a whole count of days
 *  is not quietly dropped, because dropped also means any age — the widest
 *  purge there is, in answer to somebody asking for a narrower one. */
const emptyDays = computed<number | undefined | null>(() => {
  const v = olderThanDays.value;
  if (v === undefined || v === null || v === '') return undefined;
  return typeof v === 'number' && Number.isInteger(v) && v >= 0 ? v : null;
});
const emptyDaysInvalid = computed(() => emptyDays.value === null);

async function emptyTrash() {
  if (emptyStarting.value || emptying.value || emptyDaysInvalid.value) return;
  emptyStarting.value = true;
  try {
    const res = await trashApi.empty({
      storage_id: selectedStorage.value,
      older_than_days: emptyDays.value ?? undefined,
    });
    showEmptyDialog.value = false;
    await followEmpty(res);
  } catch (err: any) {
    showEmptyDialog.value = false;
    const data = err?.response?.data;
    // Another tab, or another admin of this tenant, already started one.
    if (err?.response?.status === 409 && data?.code === 'BUSY') {
      toast.error(t('trash.empty_busy'));
      if (data.job?.running) await followEmpty(data.job);
      return;
    }
    toast.error(data?.error ?? String(err));
  } finally {
    emptyStarting.value = false;
  }
}

/** Takes a run's status — from the POST, the 409, or a poll — and either keeps
 *  following it or says how it ended. */
async function followEmpty(st: TrashEmptyStatus) {
  if (st.running) {
    emptyRun.value = st;
    scheduleEmptyPoll();
    return;
  }
  emptyRun.value = null;
  // `{running: false}` with no start is a run the server no longer knows — it
  // restarted under it. The reloaded list says what is left; nothing is
  // claimed about the rest.
  if (!st.started_at) {
    await load();
    return;
  }
  if (st.error) {
    toast.error(t('trash.empty_stopped', { error: st.error }));
  } else if (st.cancelled) {
    const n = st.purged ?? 0;
    toast.warn(t('trash.stopped_after', { count: formatNumber(n, locale.value) }, n));
  } else {
    const n = st.purged ?? 0;
    toast.success(t('trash.empty_done', { count: formatNumber(n, locale.value) }, n));
  }
  if (st.failed) {
    toast.warn(t('trash.empty_failed', { count: formatNumber(st.failed, locale.value) }, st.failed));
  }
  await load();
}

function scheduleEmptyPoll() {
  stopEmptyPoll();
  if (alive) emptyPoll = setTimeout(pollEmpty, EMPTY_POLL_MS);
}

function stopEmptyPoll() {
  if (emptyPoll !== undefined) clearTimeout(emptyPoll);
  emptyPoll = undefined;
}

async function pollEmpty() {
  emptyPoll = undefined;
  try {
    await followEmpty(await trashApi.emptyStatus());
  } catch {
    // One look that failed is not the end of the run: look again.
    scheduleEmptyPoll();
  }
}

/** On arrival, pick up a run that is still going — started in another tab, or
 *  before the admin left and came back. One that has ended was reported to
 *  whoever was watching it, and is not announced again. */
async function resumeEmpty() {
  try {
    const st = await trashApi.emptyStatus();
    if (st.running) await followEmpty(st);
  } catch {
    // The page works without it.
  }
}

/** Stop the run — the ops queue's cancel. The poll then sees it end and says
 *  how far it got. */
const emptyStopping = ref(false);
async function stopEmpty() {
  const id = emptyRun.value?.op_id;
  if (!id || emptyStopping.value) return;
  emptyStopping.value = true;
  try {
    await trashApi.cancelEmpty(id);
  } catch (err: any) {
    toast.error(err?.response?.data?.error ?? String(err));
  } finally {
    emptyStopping.value = false;
  }
}

const emptyDone = computed(() => emptyRun.value?.scanned ?? 0);
const emptyTotal = computed(() => emptyRun.value?.total ?? 0);
const emptyPct = computed(() =>
  emptyTotal.value > 0 ? Math.min(100, Math.floor((emptyDone.value / emptyTotal.value) * 100)) : 0,
);

/* The size column goes through the app's one byte formatter, imported from
 * the same module as fmtDate below. There was a private copy here — a
 * different base and different rounding from every other admin page, in a
 * file that was already importing its neighbour. */
function fmtBytes(n: number) {
  return formatBytes(n, locale.value);
}

/* zaman:z1 — the app's one date formatter. This was a bare `toLocaleString()`:
 * the browser's locale and the browser's zone, so "when was this deleted" was
 * answered on a clock nobody chose. */
function fmtDate(s: string) {
  return formatDate(s, locale.value);
}

const hasItems = computed(() => entries.value.length > 0);

/* The explorer's table (DataTable): every column resizes, hides, moves and
 * sorts, remembered on the account under `admin.trash`.
 *
 * ⚠ The endpoint answers the first `limit` entries and a TOTAL, and this page
 * draws no pager — so `total` is handed to the table. While more entries exist
 * than are on screen the table closes its headers and says why: sorting 50 of
 * 900 by size would put the 50th-largest file on top and call it the largest. */
const columns = computed<DataColumn<TrashEntry>[]>(() => [
  { id: 'name', label: t('trash.col_name'), sortable: true, width: 260 },
  {
    id: 'storage',
    label: t('trash.col_storage'),
    sortable: true,
    width: 140,
    sortValue: (e) => e.storage_name ?? `#${e.storage_id}`,
  },
  {
    id: 'size',
    label: t('trash.col_size'),
    align: 'right',
    sortable: true,
    width: 100,
    sortValue: (e) => (typeof e.size === 'number' ? e.size : null),
  },
  {
    id: 'deleted_at',
    label: t('trash.col_deleted_at'),
    sortable: true,
    sortDir: 'desc',
    width: 170,
    sortValue: (e) => (e.deleted_at ? Date.parse(e.deleted_at) : null),
  },
  {
    id: 'ttl_days',
    label: t('trash.col_ttl'),
    sortable: true,
    width: 130,
    sortValue: (e) => (typeof e.ttl_days === 'number' ? e.ttl_days : null),
  },
]);

/* The storage filter, as options rather than a bare <select>: the panel has
   one control for this and it is `ui/Select`. An `undefined` value cannot
   round-trip through a <select>'s string value, so "all storages" is the
   empty string on the way in and back to `undefined` on the way out. */
const storageOptions = computed(() => [
  { value: '', label: t('trash.all_storages') },
  ...storages.items.map((s) => ({ value: String(s.id), label: s.name })),
]);

function pickStorage(v: string | number | null) {
  const id = v == null || v === '' ? undefined : Number(v);
  selectedStorage.value = id;
  offset.value = 0;
  load();
}

onMounted(async () => {
  await storages.fetch();
  await Promise.all([load(), resumeEmpty()]);
});

onBeforeUnmount(() => {
  // Leaving the page stops the watching, never the purge.
  alive = false;
  stopEmptyPoll();
});

/** The row's verbs, behind its one pinned `Actions` control. Purge keeps
 *  whatever confirmation `purge()` already puts in front of it. */
function rowActions(row: TrashEntry): ContextAction[] {
  // A row being worked on takes no second press, and says why.
  const busy = working.value[row.id] !== undefined;
  const title = busy ? t('trash.row_busy') : undefined;
  return [
    { key: 'restore', label: t('trash.restore'), icon: 'restore', disabled: busy, title },
    { key: 'purge', label: t('trash.purge'), icon: 'delete', danger: true, disabled: busy, title },
  ];
}

function onRowAction(key: string, row: TrashEntry) {
  if (key === 'restore') restore(row);
  else if (key === 'purge') purge(row);
}
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-semibold">{{ t('trash.title') }}</h1>
        <p class="text-sm text-zinc-500">{{ t('trash.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="ghost" @click="load" :disabled="loading">↻</Button>
        <Button
          data-testid="trash-empty-open"
          variant="danger"
          :disabled="!hasItems || emptying"
          @click="showEmptyDialog = true"
        >
          <Trash2 :size="14" /> {{ t('trash.empty') }}
        </Button>
      </div>
    </header>

    <!-- The run's own strip, in the panel's palette (--fe-* tokens): it is the
         ops row the tray also shows, so it can be stopped from here too. -->
    <div
      v-if="emptyRun?.running"
      data-testid="trash-emptying"
      role="status"
      aria-live="polite"
      class="card card-body space-y-2"
    >
      <div class="flex flex-wrap items-center justify-between gap-3 text-sm">
        <span class="font-medium">{{ emptyRun.queued ? t('trash.emptying_queued') : t('trash.empty_running') }}</span>
        <div class="flex items-center gap-3">
          <span class="tabular-nums fx-trash-run__muted">
            {{ t('trash.emptying_progress', { done: formatNumber(emptyDone, locale), total: formatNumber(emptyTotal, locale) }) }}
          </span>
          <Button
            v-if="emptyRun.op_id"
            data-testid="trash-empty-stop"
            variant="ghost"
            size="sm"
            :loading="emptyStopping"
            @click="stopEmpty"
          >
            {{ t('trash.empty_stop') }}
          </Button>
        </div>
      </div>
      <div
        class="fx-trash-run__track"
        role="progressbar"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-valuenow="emptyPct"
        :aria-label="t('trash.empty_running')"
      >
        <div class="fx-trash-run__fill" :style="{ width: `${emptyPct}%` }" />
      </div>
      <p class="text-xs fx-trash-run__muted">
        {{ t('trash.emptying_freed', { size: fmtBytes(emptyRun.bytes ?? 0) }) }} · {{ t('trash.emptying_note') }}
      </p>
    </div>

    <DataTable
      table-id="admin.trash"
      :columns="columns"
      :rows="entries"
      :loading="loading"
      row-key="id"
      :total="total"
      :foot-note="t('trash.total_count', { n: formatNumber(total, locale) })"
      :row-actions="(row: TrashEntry) => rowActions(row)"
      :row-actions-test-id="(row: TrashEntry) => `trash-actions-${row.id}`"
      @row-action="(key: string, row: TrashEntry) => onRowAction(key, row)"
    >
      <template #toolbar>
        <Select
          :model-value="selectedStorage == null ? '' : String(selectedStorage)"
          :options="storageOptions"
          size="sm"
          class="w-56"
          @update:model-value="pickStorage"
        />
      </template>

      <template #empty>
        <p class="font-medium">{{ t('trash.empty_title') }}</p>
        <p class="tbl-sub">{{ t('trash.empty_description') }}</p>
      </template>

      <!-- ⚠ One wrapper: a DataTable cell is a flex row, and the name and
           its path would otherwise sit side by side instead of stacked. -->
      <template #cell-name="{ row }">
        <div>
          <span class="font-medium">{{ row.name }}</span>
          <span
            v-if="working[row.id]"
            :data-testid="`trash-working-${row.id}`"
            class="fx-trash-row__working"
            role="status"
          >{{ working[row.id] === 'restore' ? t('trash.restoring') : t('trash.purging') }}</span>
          <span class="tbl-sub tbl-clamp" :title="row.path">{{ row.path }}</span>
        </div>
      </template>
      <template #cell-storage="{ row }">{{ row.storage_name ?? `#${row.storage_id}` }}</template>
      <template #cell-size="{ row }">
        <span class="tabular-nums">{{ fmtBytes(row.size) }}</span>
      </template>
      <template #cell-deleted_at="{ row }">
        <span class="whitespace-nowrap">{{ fmtDate(row.deleted_at) }}</span>
      </template>
      <template #cell-ttl_days="{ row }">
        <span
          v-if="row.ttl_days !== undefined && row.ttl_days <= 3"
          class="inline-flex items-center gap-1 whitespace-nowrap text-rose-600 dark:text-rose-400"
        >
          <AlertTriangle :size="12" /> {{ timeLeft(row.ttl_days) }}
        </span>
        <!-- ⚠ The count is IN the message ("{n} days"), with its plural forms:
             a number followed by a separately translated "days" read "1 days"
             and could not agree in any language that inflects. No number, no
             message: a bare dash, not "— days". -->
        <span v-else class="whitespace-nowrap">{{ timeLeft(row.ttl_days) }}</span>
      </template>
    </DataTable>

    <Modal v-model="showEmptyDialog" :title="t('trash.empty_modal_title')">
      <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('trash.empty_modal_body') }}</p>
      <label class="mt-3 block text-sm">
        {{ t('trash.older_than_days') }}
        <input
          v-model.number="olderThanDays"
          data-testid="trash-empty-days"
          type="number"
          min="0"
          step="1"
          class="input-base mt-1 px-2 py-1"
          :class="{ 'border-rose-500': emptyDaysInvalid }"
          :aria-invalid="emptyDaysInvalid"
          :placeholder="t('trash.all_ages')"
        />
      </label>
      <p v-if="emptyDaysInvalid" class="error-text">{{ t('trash.older_than_days_invalid') }}</p>
      <template #footer>
        <Button variant="ghost" @click="showEmptyDialog = false">{{ t('common.cancel') }}</Button>
        <Button
          data-testid="trash-empty-confirm"
          variant="danger"
          :loading="emptyStarting"
          :disabled="emptyDaysInvalid || emptyStarting"
          @click="emptyTrash"
        >
          {{ t('trash.empty') }}
        </Button>
      </template>
    </Modal>
  </section>
</template>

<style scoped>
.fx-trash-row__working {
  display: block;
  font-size: 0.75rem;
  color: var(--fe-primary);
}
.fx-trash-run__muted {
  color: var(--fe-text-muted);
}
.fx-trash-run__track {
  height: 0.375rem;
  overflow: hidden;
  border-radius: 9999px;
  background: var(--fe-border-soft);
}
.fx-trash-run__fill {
  height: 100%;
  border-radius: 9999px;
  background: var(--fe-danger);
  transition: width 500ms ease;
}
@media (prefers-reduced-motion: reduce) {
  .fx-trash-run__fill {
    transition: none;
  }
}
</style>
