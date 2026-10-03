<script setup lang="ts">
/**
 * EncryptionRequestsPanel — the requests the `approval` policy leaves (the
 * explorer's "Request encryption…"), and the administrator's answer to each.
 *
 * An approval is for ONE person and is used once, by the first encryption it
 * lets through: a folder request's covers that folder itself or one folder
 * directly inside it (new or existing); a file request's covers one new
 * encrypted file in the folder the file is in. Unused it lapses after
 * `ttl_days`, as does a request nobody answers. A NO must say why: the
 * requester is told, and "rejected" alone leaves them guessing whether to ask
 * again.
 *
 * A tenant's administrator sees their tenant's requests; the platform operator
 * and a single-tenant administrator see every one (the server decides). The
 * list is the explorer's table (DataTable, `admin.encryptionRequests`).
 *
 * The operator's list mixes tenants, so for the operator alone (the page hands
 * over `tenantNames`, read by the ceiling panel) a Tenant column names whose
 * request each row is. Once a request is answered, the status says who
 * approved or rejected it and what they said, as text: the history is where a
 * person comes to read it. A list that could not be read says so, never "no
 * request is waiting".
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Check, Inbox, RefreshCcw, X } from 'lucide-vue-next';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

import { extractError } from '@/api/client';
import { E2EPolicyApi, e2ePolicyRefusal, type E2ERequest, type E2ERequestStatus } from '@/api/e2ePolicy';
import { formatDate } from '@/lib/format';
import { useToastStore } from '@/stores/toast';
import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Modal from '@/components/ui/Modal.vue';
import Textarea from '@/components/ui/Textarea.vue';

const props = withDefaults(
  defineProps<{
    /** Tenant names by id — given to the platform operator only, whose list
     *  mixes tenants. Null draws no Tenant column. */
    tenantNames?: Record<number, string> | null;
  }>(),
  { tenantNames: null },
);

const { t, locale } = useI18n();
const toast = useToastStore();

const rows = ref<E2ERequest[]>([]);
const ttlDays = ref(7);
const loading = ref(false);
/** The last read failed: an empty table is not "nothing is waiting". */
const loadFailed = ref(false);
/** Answered requests too, not only the waiting ones. */
const answeredToo = ref(false);
const waiting = computed(() => rows.value.filter((r) => r.status === 'pending').length);

/** Reads the list; false when the read failed (said in a toast). */
async function refresh(): Promise<boolean> {
  loading.value = true;
  try {
    const list = await E2EPolicyApi.requests(answeredToo.value ? 'all' : 'pending');
    rows.value = list.requests;
    ttlDays.value = list.ttl_days;
    loadFailed.value = false;
    return true;
  } catch (e: unknown) {
    loadFailed.value = true;
    toast.error(extractError(e, t('errors.loadFailed')));
    return false;
  } finally {
    loading.value = false;
  }
}
onMounted(refresh);

/** A list that could not be read leaves the button on the list still shown. */
async function setAnsweredToo(on: boolean): Promise<void> {
  const was = answeredToo.value;
  answeredToo.value = on;
  if (!(await refresh())) answeredToo.value = was;
}

/** What the table says with no rows. */
const emptyText = computed(() => {
  if (loadFailed.value) return t('encryption.requests.loadFailed');
  return answeredToo.value ? t('encryption.requests.emptyHistory') : t('encryption.requests.empty');
});

/** Who answered, and how: under a Used or an Expired badge the line is the
 *  approval's, so it names it, or the reader takes the approver for the
 *  person who used it or let it lapse. */
function answeredBy(r: E2ERequest): string {
  const key = r.status === 'rejected' ? 'encryption.requests.rejectedBy' : 'encryption.requests.approvedBy';
  return t(key, { who: r.decider });
}

const TONE: Record<E2ERequestStatus, 'amber' | 'emerald' | 'sky' | 'rose' | 'zinc'> = {
  pending: 'amber',
  approved: 'emerald',
  used: 'sky',
  rejected: 'rose',
  expired: 'zinc',
};
/** Waiting first, then what can still be used, then what is over. */
const RANK: Record<E2ERequestStatus, number> = { pending: 0, approved: 1, used: 2, rejected: 3, expired: 4 };

/** ⚠ A function, not `TONE[row.status]` in the template: a DataTable slot's
 *  `row` is `any`, and indexing a Record with it fails `noImplicitAny`. */
function toneOf(s: E2ERequestStatus): 'amber' | 'emerald' | 'sky' | 'rose' | 'zinc' {
  return TONE[s] ?? 'zinc';
}

function statusText(s: E2ERequestStatus): string {
  return t(`encryption.requests.status.${s}`);
}

/** The tenant a row belongs to, for the operator's Tenant column. */
function tenantOf(r: E2ERequest): string {
  return r.tenant_id != null ? (props.tenantNames?.[r.tenant_id] ?? '') : '';
}

const columns = computed<DataColumn<E2ERequest>[]>(() => [
  { id: 'path', label: t('encryption.requests.fields.path'), sortable: true, width: 260, sortValue: (r) => r.path },
  ...(props.tenantNames
    ? [{ id: 'tenant', label: t('encryption.tenants.fields.name'), sortable: true, width: 130, sortValue: tenantOf }]
    : []),
  {
    id: 'requester',
    label: t('encryption.requests.fields.requester'),
    sortable: true,
    width: 160,
    sortValue: (r) => r.requester,
  },
  { id: 'reason', label: t('encryption.requests.fields.reason'), width: 240 },
  {
    id: 'created',
    label: t('encryption.requests.fields.created'),
    sortable: true,
    sortDir: 'desc',
    width: 200,
    // ⚠ The operator's Tenant column squeezes the others toward their minimums
    // (DataTable's auto layout); the English date and time need ~150px, and
    // a timestamp cut at the minutes says less than none.
    min: 160,
    sortValue: (r) => r.created_at,
    format: (r) => formatDate(r.created_at, locale.value),
  },
  {
    id: 'status',
    label: t('encryption.requests.fields.status'),
    sortable: true,
    width: 200,
    sortValue: (r) => RANK[r.status] ?? 9,
  },
]);

/** Only a waiting request can be answered. */
function actionsFor(r: E2ERequest): ContextAction[] {
  if (r.status !== 'pending') return [];
  return [
    { key: 'approve', label: t('encryption.requests.actions.approve'), icon: 'check' },
    { key: 'reject', label: t('encryption.requests.actions.reject'), icon: 'close', danger: true },
  ];
}

// ── The answer ───────────────────────────────────────────────────────────

const answering = ref<{ request: E2ERequest; approve: boolean } | null>(null);
const words = ref('');
const busy = ref(false);
const failure = ref('');

function startAnswer(key: string, r: E2ERequest): void {
  if (key !== 'approve' && key !== 'reject') return;
  answering.value = { request: r, approve: key === 'approve' };
  words.value = '';
  failure.value = '';
}

async function sendAnswer(): Promise<void> {
  const a = answering.value;
  if (!a) return;
  const said = words.value.trim();
  if (!a.approve && !said) {
    failure.value = t('encryption.requests.reject.required');
    return;
  }
  busy.value = true;
  failure.value = '';
  try {
    if (a.approve) {
      await E2EPolicyApi.approve(a.request.id, said);
      toast.success(t('encryption.requests.approved', { who: a.request.requester }));
    } else {
      await E2EPolicyApi.reject(a.request.id, said);
      toast.success(t('encryption.requests.rejected', { who: a.request.requester }));
    }
    answering.value = null;
    await refresh();
  } catch (e: unknown) {
    if (e2ePolicyRefusal(e) === 'not_pending') {
      // Answered meanwhile — by another administrator, or it lapsed: say so,
      // and show what it is now.
      toast.warn(t('encryption.requests.notPending'));
      answering.value = null;
      await refresh();
    } else {
      failure.value = extractError(e, t('errors.saveFailed'));
    }
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section
    class="space-y-3 rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950"
    data-testid="encryption-requests"
  >
    <header class="flex flex-wrap items-center justify-between gap-2">
      <div class="flex items-center gap-2">
        <Inbox class="h-5 w-5 text-brand-600 dark:text-brand-400" />
        <h2 class="text-base font-semibold">
          {{ t('encryption.requests.title') }}
        </h2>
        <Badge
          v-if="waiting"
          tone="amber"
          size="xs"
          data-testid="encryption-requests-count"
        >
          {{ t('encryption.requests.pendingCount', { count: waiting }) }}
        </Badge>
      </div>
      <div class="flex items-center gap-2">
        <Button
          variant="ghost"
          size="sm"
          data-testid="encryption-requests-history"
          @click="setAnsweredToo(!answeredToo)"
        >
          {{ answeredToo ? t('encryption.requests.hideHistory') : t('encryption.requests.showHistory') }}
        </Button>
        <Button
          variant="outline"
          size="sm"
          :loading="loading"
          @click="refresh"
        >
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
      </div>
    </header>
    <p class="text-sm text-zinc-600 dark:text-zinc-400">
      {{ t('encryption.requests.subtitle') }} {{ t('encryption.requests.expiresAfter', { count: ttlDays }, ttlDays) }}
    </p>

    <DataTable
      table-id="admin.encryptionRequests"
      :columns="columns"
      :rows="rows"
      row-key="id"
      :loading="loading"
      :empty="emptyText"
      :row-actions="(row: E2ERequest) => actionsFor(row)"
      :row-actions-test-id="(row: E2ERequest) => `e2e-request-actions-${row.id}`"
      @row-action="(key: string, row: E2ERequest) => startAnswer(key, row)"
    >
      <!-- ⚠ ONE wrapper per cell that stacks lines: a DataTable cell is a flex row. -->
      <template #cell-path="{ row }">
        <div
          class="min-w-0 py-1"
          :data-testid="`e2e-request-${row.id}`"
        >
          <div
            class="truncate font-medium"
            :title="row.path"
          >
            {{ row.path }}
          </div>
          <div class="text-[11px] text-zinc-500">
            {{ row.kind === 'file' ? t('encryption.requests.kind.file') : t('encryption.requests.kind.folder') }}
          </div>
        </div>
      </template>
      <template #cell-tenant="{ row }">
        <span
          class="truncate"
          :title="tenantOf(row)"
          :data-testid="`e2e-request-tenant-${row.id}`"
        >{{ tenantOf(row) }}</span>
      </template>
      <template #cell-reason="{ row }">
        <span
          class="line-clamp-2 break-words"
          :title="row.reason"
        >{{ row.reason }}</span>
      </template>
      <template #cell-status="{ row }">
        <div class="min-w-0 py-1">
          <Badge
            :tone="toneOf(row.status)"
            dot
            :data-testid="`e2e-request-status-${row.id}`"
          >
            {{ statusText(row.status) }}
          </Badge>
          <!-- Who answered and what they said, as text: the history is where a
               person comes to read it, and a tooltip is not read. A note can run
               to 2000 characters, so it is cut to two lines and whole in the
               tooltip, as the reason column is. -->
          <div
            v-if="row.decider"
            class="mt-0.5 space-y-0.5 text-[11px] text-zinc-500"
            :data-testid="`e2e-request-answer-${row.id}`"
          >
            <div class="break-words">
              {{ answeredBy(row) }}
            </div>
            <div
              v-if="row.decision_note"
              class="line-clamp-2 break-words"
              :title="row.decision_note"
            >
              {{ row.decision_note }}
            </div>
          </div>
        </div>
      </template>
    </DataTable>

    <Modal
      :model-value="!!answering"
      :title="answering ? (answering.approve ? t('encryption.requests.approve.title') : t('encryption.requests.reject.title')) : ''"
      size="md"
      :prevent-close="busy"
      @update:model-value="(v: boolean) => { if (!v) answering = null; }"
    >
      <div
        v-if="answering"
        class="space-y-3"
        data-testid="e2e-answer"
      >
        <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm">
          <dt class="text-zinc-500">
            {{ t('encryption.requests.fields.path') }}
          </dt>
          <dd class="break-all">
            {{ answering.request.path }}
          </dd>
          <dt class="text-zinc-500">
            {{ t('encryption.requests.fields.requester') }}
          </dt>
          <dd>{{ answering.request.requester }}</dd>
          <dt class="text-zinc-500">
            {{ t('encryption.requests.fields.reason') }}
          </dt>
          <!-- Plain text, as the requester wrote it: never rendered as markup. -->
          <dd class="whitespace-pre-wrap break-words">
            {{ answering.request.reason }}
          </dd>
        </dl>
        <!-- A single file's request is kept under the folder its `.fxe` lands
             in (backend e2epolicy): the approval lets one new encrypted file
             be made there, and the sentence says so. A folder's covers the
             folder or one folder directly inside it. -->
        <p
          v-if="answering.approve"
          class="text-sm text-zinc-600 dark:text-zinc-400"
        >
          {{
            answering.request.kind === 'file'
              ? t('encryption.requests.approve.bodyFile', { who: answering.request.requester, path: answering.request.path })
              : t('encryption.requests.approve.body', { who: answering.request.requester, path: answering.request.path })
          }}
        </p>
        <Textarea
          v-model="words"
          name="e2e-answer-words"
          :label="answering.approve ? t('encryption.requests.approve.note') : t('encryption.requests.reject.reason')"
          :required="!answering.approve"
          :rows="3"
        />
        <p
          v-if="failure"
          class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200"
          role="alert"
          data-testid="e2e-answer-error"
        >
          {{ failure }}
        </p>
        <div class="flex flex-wrap justify-end gap-2">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            :disabled="busy"
            @click="answering = null"
          >
            {{ t('common.cancel') }}
          </Button>
          <Button
            type="button"
            size="sm"
            :variant="answering.approve ? 'primary' : 'danger'"
            :loading="busy"
            data-testid="e2e-answer-send"
            @click="sendAnswer"
          >
            <Check
              v-if="answering.approve"
              class="h-4 w-4"
            />
            <X
              v-else
              class="h-4 w-4"
            />
            {{ answering.approve ? t('encryption.requests.approve.confirm') : t('encryption.requests.reject.confirm') }}
          </Button>
        </div>
      </div>
    </Modal>
  </section>
</template>
