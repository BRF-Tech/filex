<script setup lang="ts">
/**
 * Trash view — admin overview of every soft-deleted node across storages.
 * Actions: restore (clears deleted_at), purge (hard-delete one), empty
 * (purge all in a storage, optionally limited by age).
 */
import { ref, onMounted, onBeforeUnmount, computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { useToastStore } from '@/stores/toast';
import { useStoragesStore } from '@/stores/storages';
import { trashApi, type TrashEntry, type TrashEmptyStatus } from '@/api/trash';
import Button from '@/components/ui/Button.vue';
import EmptyState from '@/components/ui/EmptyState.vue';
import Modal from '@/components/ui/Modal.vue';
import TableScroll from '@/components/ui/TableScroll.vue';
import { Trash2, RotateCcw, AlertTriangle } from 'lucide-vue-next';
import { formatBytes, formatDate } from '@/lib/format';

const { t, locale } = useI18n();
const toast = useToastStore();
const storages = useStoragesStore();

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

async function restore(entry: TrashEntry) {
  try {
    await trashApi.restore(entry.id);
    toast.success(t('trash.restored', { name: entry.name }));
    await load();
  } catch (err: any) {
    // 409 EXISTS: something holds the original path, and the server refused
    // rather than overwrite it. Said in the reader's language — the server's
    // own sentence is English and names no remedy.
    if (err?.response?.status === 409 && err?.response?.data?.code === 'EXISTS') {
      toast.error(t('trash.restore_taken', { name: err.response.data.name || entry.name }));
      return;
    }
    toast.error(err?.response?.data?.error ?? String(err));
  }
}

async function purge(entry: TrashEntry) {
  if (!confirm(t('trash.purge_confirm', { name: entry.name }))) return;
  try {
    await trashApi.purge(entry.id);
    toast.success(t('trash.purged', { name: entry.name }));
    await load();
  } catch (err: any) {
    toast.error(err?.response?.data?.error ?? String(err));
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
  } else {
    const n = st.purged ?? 0;
    toast.success(t('trash.empty_done', { count: fmt.value.format(n) }, n));
  }
  if (st.failed) {
    toast.warn(t('trash.empty_failed', { count: fmt.value.format(st.failed) }, st.failed));
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

const emptyDone = computed(() => emptyRun.value?.scanned ?? 0);
const emptyTotal = computed(() => emptyRun.value?.total ?? 0);
const emptyPct = computed(() =>
  emptyTotal.value > 0 ? Math.min(100, Math.floor((emptyDone.value / emptyTotal.value) * 100)) : 0,
);

/* Counts in the page's language, not the browser's — the same rule as the
 * dates below. */
const fmt = computed(() => new Intl.NumberFormat(locale.value));

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

onMounted(async () => {
  await storages.fetch();
  await Promise.all([load(), resumeEmpty()]);
});

onBeforeUnmount(() => {
  // Leaving the page stops the watching, never the purge.
  alive = false;
  stopEmptyPoll();
});
</script>

<template>
  <section class="space-y-4">
    <header class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-semibold">{{ t('trash.title') }}</h1>
        <p class="text-sm text-zinc-500">{{ t('trash.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <select
          v-model.number="selectedStorage"
          class="rounded border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2 py-1 text-sm"
          @change="load"
        >
          <option :value="undefined">{{ t('trash.all_storages') }}</option>
          <option v-for="s in storages.items" :key="s.id" :value="s.id">{{ s.name }}</option>
        </select>
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

    <div
      v-if="emptyRun?.running"
      data-testid="trash-emptying"
      role="status"
      aria-live="polite"
      class="rounded-lg border border-rose-200 dark:border-rose-900/60 bg-rose-50/60 dark:bg-rose-950/30 px-3 py-2"
    >
      <div class="flex items-center justify-between gap-3 text-sm">
        <span class="font-medium">{{ t('trash.emptying') }}</span>
        <span class="tabular-nums text-zinc-600 dark:text-zinc-400">
          {{ t('trash.emptying_progress', { done: fmt.format(emptyDone), total: fmt.format(emptyTotal) }) }}
        </span>
      </div>
      <div
        class="mt-2 h-1.5 overflow-hidden rounded-full bg-rose-100 dark:bg-rose-950"
        role="progressbar"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-valuenow="emptyPct"
        :aria-label="t('trash.emptying')"
      >
        <div class="h-full rounded-full bg-rose-500 transition-[width] duration-500" :style="{ width: `${emptyPct}%` }" />
      </div>
      <p class="mt-1.5 text-xs text-zinc-500">
        {{ t('trash.emptying_freed', { size: fmtBytes(emptyRun.bytes ?? 0) }) }} · {{ t('trash.emptying_note') }}
      </p>
    </div>

    <EmptyState
      v-if="!loading && !hasItems"
      :title="t('trash.empty_title')"
      :description="t('trash.empty_description')"
    />

    <div v-else class="rounded-lg border border-zinc-200 dark:border-zinc-800 bg-white dark:bg-zinc-950">
      <TableScroll>
        <table class="w-full text-sm">
          <thead class="bg-zinc-50 dark:bg-zinc-900 text-left">
            <tr>
              <th class="px-3 py-2">{{ t('trash.col_name') }}</th>
              <th class="px-3 py-2">{{ t('trash.col_storage') }}</th>
              <th class="px-3 py-2">{{ t('trash.col_size') }}</th>
              <th class="px-3 py-2">{{ t('trash.col_deleted_at') }}</th>
              <th class="px-3 py-2">{{ t('trash.col_ttl') }}</th>
              <th class="px-3 py-2 text-right tbl-actions">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="e in entries"
              :key="e.id"
              class="border-t border-zinc-100 dark:border-zinc-800 bg-white dark:bg-zinc-950 hover:bg-zinc-50 dark:hover:bg-zinc-900"
            >
              <td class="px-3 py-2 font-medium">
                <div>{{ e.name }}</div>
                <div class="text-xs text-zinc-500">{{ e.path }}</div>
              </td>
              <td class="px-3 py-2 text-zinc-600 dark:text-zinc-400">{{ e.storage_name ?? `#${e.storage_id}` }}</td>
              <td class="px-3 py-2 tabular-nums">{{ fmtBytes(e.size) }}</td>
              <td class="px-3 py-2 text-zinc-500">{{ fmtDate(e.deleted_at) }}</td>
              <td class="px-3 py-2 text-zinc-500">
                <span v-if="e.ttl_days !== undefined && e.ttl_days <= 3" class="inline-flex items-center gap-1 text-rose-600">
                  <AlertTriangle :size="12" /> {{ e.ttl_days }} {{ t('trash.days_left') }}
                </span>
                <span v-else>{{ e.ttl_days ?? '—' }} {{ t('trash.days_left') }}</span>
              </td>
              <td class="px-3 py-2 text-right tbl-actions">
                <div class="inline-flex gap-2">
                  <Button size="sm" variant="ghost" @click="restore(e)">
                    <RotateCcw :size="12" /> {{ t('trash.restore') }}
                  </Button>
                  <Button size="sm" variant="danger" @click="purge(e)">
                    <Trash2 :size="12" /> {{ t('trash.purge') }}
                  </Button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </TableScroll>
      <footer class="px-3 py-2 text-xs text-zinc-500 flex justify-between">
        <span>{{ t('trash.total_count', { n: fmt.format(total) }) }}</span>
        <span v-if="loading">{{ t('common.loading') }}</span>
      </footer>
    </div>

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
          class="mt-1 w-full rounded border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2 py-1"
          :class="{ 'border-rose-500 dark:border-rose-500': emptyDaysInvalid }"
          :aria-invalid="emptyDaysInvalid"
          :placeholder="t('trash.all_ages')"
        />
      </label>
      <p v-if="emptyDaysInvalid" class="mt-1 text-xs text-rose-600">{{ t('trash.older_than_days_invalid') }}</p>
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
