<script setup lang="ts">
/**
 * PluginRequestsPanel — the install requests an API key left, and the
 * administrator's decision on each (Plugins page, above both tabs: a request
 * is for an app or for a storage plugin).
 *
 * ⚠⚠ Why this exists (owner, 2026-09-28): an API key — an agent, a script,
 * the CLI — cannot install, upgrade or remove a plugin any more. It leaves a
 * request; the decision is made HERE, by a signed-in administrator. What the
 * source answered when the request was made is frozen on it (manifest,
 * SHA-256, permissions) and "Approve and install" installs exactly that — the
 * server refuses (`superseded`) when the source serves other bytes by now.
 *
 * The review uses the same permission list the install wizard shows
 * (AppPluginPermissionList: filex's label, the app's own reason) and asks for
 * the same "I understand" before an approval. The list is the explorer's
 * table (DataTable, `admin.pluginRequests`).
 */
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Check, Inbox, RefreshCcw, TriangleAlert, X } from 'lucide-vue-next';

import { extractError } from '@/api/client';
import {
  PluginRequestsApi,
  pluginRequestError,
  type PluginRequest,
  type PluginRequestStatus,
} from '@/api/pluginRequests';
import type { AppPluginDryRun } from '@/api/appPlugins';
import { formatDate } from '@/lib/format';
import { useToastStore } from '@/stores/toast';
import { DataTable, pluginLabelOf, type ContextAction, type DataColumn } from '@brftech/filex-core';

import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Modal from '@/components/ui/Modal.vue';
import Textarea from '@/components/ui/Textarea.vue';
import AppPluginPermissionList from './AppPluginPermissionList.vue';

const emit = defineEmits<{
  /** A request was approved: the page redraws the installed plugins. */
  (e: 'installed'): void;
}>();

const { t, locale } = useI18n();
const toast = useToastStore();

const requests = ref<PluginRequest[]>([]);
const ttlDays = ref(14);
const loading = ref(false);
/** A tenant administrator: the panel draws nothing (the tabs say why). */
const forbidden = ref(false);
/** Decided requests too, not only the pending ones. */
const history = ref(false);

async function load() {
  loading.value = true;
  try {
    const res = await PluginRequestsApi.list(history.value ? 'all' : 'pending');
    requests.value = res.requests;
    ttlDays.value = res.ttl_days;
    forbidden.value = false;
  } catch (e: unknown) {
    const status = (e as { response?: { status?: number } })?.response?.status;
    // 403: not the platform operator. 503: plugins are off here — the tabs
    // say that too. Neither is worth a toast on every visit.
    if (status === 403 || status === 503) forbidden.value = true;
    else toast.error(extractError(e, t('errors.loadFailed')));
  } finally {
    loading.value = false;
  }
}

onMounted(load);

function toggleHistory() {
  history.value = !history.value;
  void load();
}

const pendingCount = computed(() => requests.value.filter((r) => r.status === 'pending').length);

function labelOf(r: PluginRequest): string {
  return pluginLabelOf(r.label, locale.value) || r.name;
}

function statusTone(s: PluginRequestStatus): 'amber' | 'emerald' | 'rose' | 'zinc' {
  if (s === 'pending') return 'amber';
  if (s === 'approved') return 'emerald';
  if (s === 'rejected' || s === 'superseded') return 'rose';
  return 'zinc';
}

/** Where the plugin comes from, in one line. */
function sourceText(r: PluginRequest): string {
  const s = r.source ?? {};
  if (s.github_repo) return `github.com/${s.github_repo}${s.ref ? `@${s.ref}` : ''}`;
  if (s.manifest_url) return s.manifest_url;
  if (s.source) return s.source;
  if (s.url) return s.url;
  if (s.from_source) return t('pluginRequests.source.own');
  return '—';
}

function versionText(r: PluginRequest): string {
  if (r.op === 'upgrade' && r.from_version) return t('pluginRequests.jump', { from: r.from_version, to: r.version || '—' });
  return r.version || '—';
}

const STATUS_RANK: Record<string, number> = { pending: 0, superseded: 1, rejected: 2, expired: 3, approved: 4 };

/* The explorer's table (DataTable), remembered under `admin.pluginRequests`.
 * Every request of the shown state arrives in one answer, so the table sorts
 * them itself.
 *
 * ⚠ The widths are a PROPORTION: DataTable shrinks every column alike when
 * they are wider than the box, which in this panel (beside the side
 * navigation, at 1440) is about 0.85 of these numbers. `created` is sized for
 * a whole "Sep 28, 2026, 1:05 PM" after that (the first take cut it to
 * "1:0…"), `permissions` for "14 permissions" on one line. */
const columns = computed<DataColumn<PluginRequest>[]>(() => [
  { id: 'plugin', label: t('pluginRequests.fields.plugin'), sortable: true, width: 200, sortValue: (r) => labelOf(r) },
  { id: 'version', label: t('pluginRequests.fields.version'), sortable: true, width: 100, format: (r) => versionText(r) },
  {
    id: 'requester',
    label: t('pluginRequests.fields.requester'),
    sortable: true,
    width: 160,
    sortValue: (r) => r.requester,
  },
  { id: 'reason', label: t('pluginRequests.fields.reason'), width: 220 },
  {
    id: 'permissions',
    label: t('pluginRequests.fields.permissions'),
    sortable: true,
    sortDir: 'desc',
    width: 130,
    sortValue: (r) => r.permissions.length,
  },
  {
    id: 'created',
    label: t('pluginRequests.fields.requested'),
    sortable: true,
    sortDir: 'desc',
    width: 200,
    sortValue: (r) => r.created_at,
    format: (r) => formatDate(r.created_at, locale.value),
  },
  {
    id: 'status',
    label: t('pluginRequests.fields.status'),
    sortable: true,
    width: 120,
    sortValue: (r) => STATUS_RANK[r.status] ?? 9,
  },
]);

function rowActions(r: PluginRequest): ContextAction[] {
  const out: ContextAction[] = [{ key: 'review', label: t('pluginRequests.actions.review'), icon: 'details' }];
  if (r.status === 'pending') out.push({ key: 'reject', label: t('pluginRequests.actions.reject'), danger: true });
  return out;
}

// ── The review ─────────────────────────────────────────────────────────

const open = ref<PluginRequest | null>(null);
const opening = ref(false);
const understood = ref(false);
const rejecting = ref(false);
const rejectReason = ref('');
const busy = ref(false);
const failure = ref('');

async function review(r: PluginRequest, reject = false) {
  opening.value = true;
  failure.value = '';
  understood.value = false;
  rejecting.value = reject;
  rejectReason.value = '';
  try {
    open.value = await PluginRequestsApi.get(r.id);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.loadFailed')));
  } finally {
    opening.value = false;
  }
}

function onRowAction(key: string, r: PluginRequest) {
  if (key === 'review') void review(r);
  else if (key === 'reject') void review(r, true);
}

function close() {
  open.value = null;
}

/** The app review the dry run gave, when the request is an app's. */
const appReview = computed<AppPluginDryRun | null>(() =>
  open.value?.kind === 'app' && open.value.review ? (open.value.review as AppPluginDryRun) : null,
);

/** The refusal of the last approval attempt, kept on a pending request. */
const lastError = computed(() => {
  const r = open.value;
  if (!r || r.status !== 'pending' || !r.result || typeof r.result !== 'object') return '';
  const err = (r.result as { error?: unknown }).error;
  return typeof err === 'string' ? err : '';
});

const decidedLine = computed(() => {
  const r = open.value;
  if (!r || r.status === 'pending') return '';
  const status = t(`pluginRequests.status.${r.status}`);
  const when = formatDate(r.decided_at ?? r.expires_at, locale.value);
  return r.decider
    ? t('pluginRequests.review.decided', { status, who: r.decider, when })
    : t('pluginRequests.review.decidedNobody', { status, when });
});

async function approve() {
  const r = open.value;
  if (!r || !understood.value) return;
  busy.value = true;
  failure.value = '';
  try {
    const done = await PluginRequestsApi.approve(r.id);
    toast.success(t('pluginRequests.approved', { name: labelOf(done), version: done.version }));
    open.value = null;
    emit('installed');
    await load();
  } catch (e: unknown) {
    const err = pluginRequestError(e);
    if (err?.code === 'superseded') {
      toast.warn(t('pluginRequests.superseded', { name: labelOf(r) }));
      open.value = null;
      await load();
    } else {
      failure.value = err?.message || extractError(e, t('errors.generic'));
    }
  } finally {
    busy.value = false;
  }
}

async function reject() {
  const r = open.value;
  if (!r) return;
  busy.value = true;
  failure.value = '';
  try {
    await PluginRequestsApi.reject(r.id, rejectReason.value);
    toast.success(t('pluginRequests.rejected', { name: labelOf(r) }));
    open.value = null;
    await load();
  } catch (e: unknown) {
    failure.value = pluginRequestError(e)?.message || extractError(e, t('errors.generic'));
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section
    v-if="!forbidden"
    class="space-y-3 rounded-xl border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950"
    data-testid="plugin-requests"
  >
    <header class="flex flex-wrap items-center justify-between gap-2">
      <div class="flex items-center gap-2">
        <Inbox class="h-5 w-5 text-brand-600 dark:text-brand-400" />
        <h2 class="text-base font-semibold">{{ t('pluginRequests.title') }}</h2>
        <Badge v-if="pendingCount" tone="amber" size="xs" data-testid="plugin-requests-count">
          {{ t('pluginRequests.pendingCount', { count: pendingCount }, pendingCount) }}
        </Badge>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="ghost" size="sm" data-testid="plugin-requests-history" @click="toggleHistory">
          {{ history ? t('pluginRequests.hideHistory') : t('pluginRequests.showHistory') }}
        </Button>
        <Button variant="outline" size="sm" :loading="loading" @click="load">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
      </div>
    </header>
    <p class="text-sm text-zinc-600 dark:text-zinc-400">
      {{ t('pluginRequests.subtitle') }} {{ t('pluginRequests.expiresAfter', { count: ttlDays }, ttlDays) }}
    </p>

    <p
      v-if="!loading && !requests.length"
      class="text-sm text-zinc-500"
      data-testid="plugin-requests-empty"
    >
      {{ history ? t('pluginRequests.emptyHistory') : t('pluginRequests.empty') }}
    </p>

    <DataTable
      v-else
      table-id="admin.pluginRequests"
      :columns="columns"
      :rows="requests"
      row-key="id"
      :loading="loading"
      :row-actions="(row: PluginRequest) => rowActions(row)"
      :row-actions-test-id="(row: PluginRequest) => `plugin-request-actions-${row.id}`"
      @row-action="(key: string, row: PluginRequest) => onRowAction(key, row)"
    >
      <!-- ⚠ ONE wrapper per cell that stacks lines: a DataTable cell is a flex row. -->
      <template #cell-plugin="{ row }">
        <div class="min-w-0 py-1" :data-testid="`plugin-request-${row.id}`">
          <div class="truncate font-medium" :title="labelOf(row)">{{ labelOf(row) }}</div>
          <div class="text-[11px] text-zinc-500">
            {{ t(`pluginRequests.kind.${row.kind}`) }} · {{ t(`pluginRequests.op.${row.op}`) }}
            <template v-if="labelOf(row) !== row.name"> · <span class="font-mono">{{ row.name }}</span></template>
          </div>
        </div>
      </template>
      <template #cell-requester="{ row }">
        <div class="min-w-0 py-1">
          <div class="truncate">{{ row.requester || '—' }}</div>
          <div v-if="row.token_label" class="truncate text-[11px] text-zinc-500" :title="row.token_label">
            {{ t('pluginRequests.via', { key: row.token_label }) }}
          </div>
        </div>
      </template>
      <template #cell-reason="{ row }">
        <span class="line-clamp-2 break-words" :title="row.reason">{{ row.reason }}</span>
      </template>
      <template #cell-permissions="{ row }">
        <span :title="row.permissions.join(', ')">
          {{ t('appPlugins.permissionsCount', { count: row.permissions.length }, row.permissions.length) }}
        </span>
      </template>
      <template #cell-status="{ row }">
        <Badge :tone="statusTone(row.status)" dot :data-testid="`plugin-request-status-${row.id}`">
          {{ t(`pluginRequests.status.${row.status}`) }}
        </Badge>
      </template>
    </DataTable>

    <Modal
      :model-value="!!open"
      :title="open ? t('pluginRequests.review.title', { name: labelOf(open) }) : ''"
      size="lg"
      :prevent-close="busy"
      @update:model-value="(v: boolean) => { if (!v) close(); }"
    >
      <div v-if="open" class="space-y-4" data-testid="plugin-request-review">
        <div class="flex flex-wrap items-center gap-2">
          <Badge tone="brand" size="xs">{{ t(`pluginRequests.kind.${open.kind}`) }}</Badge>
          <Badge tone="sky" size="xs">{{ t(`pluginRequests.op.${open.op}`) }}</Badge>
          <Badge :tone="statusTone(open.status)" size="xs" dot>{{ t(`pluginRequests.status.${open.status}`) }}</Badge>
        </div>

        <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm">
          <dt class="text-zinc-500">{{ t('pluginRequests.fields.plugin') }}</dt>
          <dd><span class="font-medium">{{ labelOf(open) }}</span> <span class="font-mono text-xs text-zinc-500">{{ open.name }}</span></dd>
          <dt class="text-zinc-500">{{ t('pluginRequests.fields.version') }}</dt>
          <dd data-testid="plugin-request-version">{{ versionText(open) }}</dd>
          <dt class="text-zinc-500">{{ t('pluginRequests.fields.source') }}</dt>
          <dd class="break-all" data-testid="plugin-request-source">{{ sourceText(open) }}</dd>
          <dt class="text-zinc-500">{{ t('pluginRequests.review.sha256') }}</dt>
          <dd class="break-all font-mono text-xs" data-testid="plugin-request-sha256">{{ open.sha256 || '—' }}</dd>
          <template v-if="open.manifest_sha256 && open.manifest_sha256 !== open.sha256">
            <dt class="text-zinc-500">{{ t('pluginRequests.review.manifestSha256') }}</dt>
            <dd class="break-all font-mono text-xs">{{ open.manifest_sha256 }}</dd>
          </template>
          <dt class="text-zinc-500">{{ t('pluginRequests.fields.requester') }}</dt>
          <dd>
            {{ open.requester || '—' }}
            <span v-if="open.token_label" class="text-xs text-zinc-500"> · {{ t('pluginRequests.via', { key: open.token_label }) }}</span>
          </dd>
          <dt class="text-zinc-500">{{ t('pluginRequests.fields.requested') }}</dt>
          <dd>{{ formatDate(open.created_at, locale) }}</dd>
          <template v-if="open.status === 'pending'">
            <dt class="text-zinc-500">{{ t('pluginRequests.fields.expires') }}</dt>
            <dd>{{ formatDate(open.expires_at, locale) }}</dd>
          </template>
        </dl>

        <div>
          <h3 class="text-sm font-semibold">{{ t('pluginRequests.fields.reason') }}</h3>
          <!-- Plain text, as the requester wrote it: never rendered as markup. -->
          <p class="mt-1 whitespace-pre-wrap break-words text-sm" data-testid="plugin-request-reason">{{ open.reason }}</p>
        </div>

        <!-- A storage plugin runs as filex, with every storage's credentials. -->
        <div
          v-if="open.kind === 'storage'"
          class="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
        >
          <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" />
          <p>{{ t('plugins.trustWarning') }}</p>
        </div>

        <!-- An app: the permissions, as the install wizard shows them; an
             upgrade marks the ones it ADDS — what is being approved. -->
        <template v-if="open.kind === 'app'">
          <p
            v-if="appReview?.upgrade?.added?.length"
            class="text-sm text-amber-800 dark:text-amber-200"
            data-testid="plugin-request-added"
          >
            {{ t('appPlugins.wizard.addedPermissions', { permissions: appReview.upgrade.added.join(', ') }) }}
          </p>
          <AppPluginPermissionList
            :permissions="open.permission_rows"
            :reasons="appReview?.manifest?.permission_reasons"
            :added="appReview?.upgrade?.added"
          />
        </template>

        <p v-if="decidedLine" class="text-sm text-zinc-600 dark:text-zinc-400" data-testid="plugin-request-decided">
          {{ decidedLine }}
        </p>
        <p v-if="open.decision_note" class="whitespace-pre-wrap break-words text-sm" data-testid="plugin-request-note">
          <span class="font-medium">{{ t('pluginRequests.review.note') }}:</span> {{ open.decision_note }}
        </p>

        <template v-if="open.status === 'pending'">
          <p class="rounded-lg border border-zinc-200 bg-zinc-50 p-2 text-xs text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300">
            {{ t('pluginRequests.review.frozen') }}
          </p>
          <p v-if="lastError" class="rounded-lg bg-amber-50 p-3 text-xs text-amber-900 dark:bg-amber-950/40 dark:text-amber-200">
            {{ t('pluginRequests.review.lastError', { error: lastError }) }}
          </p>

          <div v-if="rejecting" class="space-y-2" data-testid="plugin-request-reject-form">
            <Textarea
              v-model="rejectReason"
              name="plugin-request-reject-reason"
              :label="t('pluginRequests.review.rejectReason')"
              :placeholder="t('pluginRequests.review.rejectPlaceholder')"
              :rows="3"
            />
          </div>
          <Checkbox v-else v-model="understood" :label="t('appPlugins.wizard.understand')" name="plugin-request-understand" />

          <p v-if="failure" class="rounded-lg bg-rose-50 p-3 text-xs text-rose-900 dark:bg-rose-950/40 dark:text-rose-200" role="alert" data-testid="plugin-request-error">
            {{ failure }}
          </p>

          <div class="flex flex-wrap justify-between gap-2">
            <Button
              v-if="!rejecting"
              type="button"
              size="sm"
              variant="ghost"
              :disabled="busy"
              data-testid="plugin-request-reject-open"
              @click="rejecting = true"
            >
              <X class="h-4 w-4" />
              {{ t('pluginRequests.actions.reject') }}
            </Button>
            <Button
              v-else
              type="button"
              size="sm"
              variant="danger"
              :loading="busy"
              data-testid="plugin-request-reject"
              @click="reject"
            >
              <X class="h-4 w-4" />
              {{ t('pluginRequests.actions.reject') }}
            </Button>
            <Button
              v-if="!rejecting"
              type="button"
              size="sm"
              variant="primary"
              :disabled="!understood || busy"
              :loading="busy"
              data-testid="plugin-request-approve"
              @click="approve"
            >
              <Check class="h-4 w-4" />
              {{ t('pluginRequests.actions.approve') }}
            </Button>
            <Button v-else type="button" size="sm" variant="ghost" :disabled="busy" @click="rejecting = false">
              {{ t('common.cancel') }}
            </Button>
          </div>
        </template>
      </div>
    </Modal>
  </section>
</template>
