<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { RefreshCcw } from 'lucide-vue-next';

import { useAuditStore } from '@/stores/audit';
import type { AuditEntry } from '@/api/types';
import { formatDate, ipOnly } from '@/lib/format';
import { auditActionLabel, auditResourceOptions, auditTargetLabel } from '@/lib/auditLabel';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import { DataTable, personName, type DataColumn } from '@brftech/filex-core';
import Modal from '@/components/ui/Modal.vue';
import Badge from '@/components/ui/Badge.vue';

const { t, te, tm, locale } = useI18n();
const audit = useAuditStore();

/** The resource filter: `<resource>.` prefixes (auditResourceOptions). */
const action = ref('');
const from = ref('');
const to = ref('');
const page = ref(1);
const pageSize = 50;

const detail = ref<AuditEntry | null>(null);

async function load() {
  await audit.fetch({
    action: action.value || undefined,
    from: from.value || undefined,
    to: to.value || undefined,
    page: page.value,
    page_size: pageSize,
  });
}

watch([action, from, to], () => {
  page.value = 1;
  load();
});

/** "person · token username" for a token-authenticated write — one account's
 *  API keys stay distinguishable ("Ayşe · work" vs "Ayşe · fishapp"). The
 *  person is named the way every screen names them (core personName). */
function whoOf(r: AuditEntry): string {
  const who = personName({ name: r.user_name, email: r.user_email }) || '—';
  const via = r.metadata?.token_username;
  return typeof via === 'string' && via ? `${who} · ${via}` : who;
}

/* ⚠ What the filter offers is what the page SHOWS — the resources by name —
 * not the wire names it used to be matched against exactly ("user.create").
 * The "Target" box beside it filtered nothing at all (the handler has no such
 * parameter) and is gone. */
const resourceOptions = computed(() => [
  { value: '', label: t('common.all') },
  ...auditResourceOptions(tm('audit.resource') as Record<string, unknown>, t),
]);

/** The row's target in words (kind + which one). */
function targetOf(r: AuditEntry): string {
  return auditTargetLabel(r.target_type, r.target_id, t, te, r.target_name) || '—';
}

/* The explorer's table (DataTable), remembered under `admin.audit`.
 * ⚠ The log is paged by the SERVER (50 a page) and the endpoint has no sort
 * parameter, so while it spans more than one page the table closes its
 * headers and says why — re-ordering 50 entries of thousands would not be a
 * sorted log. Newest-first is the server's own order and stays the default. */
const columns = computed<DataColumn<AuditEntry>[]>(() => [
  {
    id: 'at',
    label: t('common.created'),
    sortable: true,
    sortDir: 'desc',
    width: 170,
    format: (r) => formatDate(r.at, locale.value),
    sortValue: (r) => (r.at ? Date.parse(r.at) : null),
  },
  {
    id: 'user_email',
    label: t('audit.fields.user'),
    sortable: true,
    width: 220,
    format: whoOf,
  },
  {
    id: 'action',
    label: t('audit.fields.action'),
    sortable: true,
    width: 180,
    sortValue: (r) => auditActionLabel(r.action, t, te),
  },
  {
    id: 'target_type',
    label: t('audit.fields.target'),
    sortable: true,
    width: 200,
    sortValue: (r) => targetOf(r),
  },
  /* ⚠ The address without the client's source port — rows written before
   * v0.43.0 stored "127.0.0.1:54452". */
  { id: 'ip', label: t('audit.fields.ip'), sortable: true, width: 130, format: (r) => ipOnly(r.ip) || '—' },
]);

onMounted(load);
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('audit.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('audit.subtitle') }}</p>
      </div>
      <Button variant="outline" size="sm" @click="load" :loading="audit.loading">
        <RefreshCcw class="h-4 w-4" />
        {{ t('common.refresh') }}
      </Button>
    </div>

    <DataTable
      table-id="admin.audit"
      :columns="columns"
      :rows="audit.page.items"
      :loading="audit.loading"
      :empty="t('audit.noResults')"
      :page="page"
      :page-size="pageSize"
      :total="audit.page.total"
      row-key="id"
      @page="(p: number) => ((page = p), load())"
      @row-click="(r: AuditEntry) => (detail = r)"
    >
      <template #toolbar>
        <Select
          :model-value="action"
          :options="resourceOptions"
          :aria-label="t('audit.filterResource')"
          size="sm"
          class="w-56"
          data-testid="audit-resource-filter"
          @update:model-value="(v) => (action = String(v ?? ''))"
        />
        <Input
          v-model="from"
          type="datetime-local"
          :label="undefined"
          size="sm"
          class="w-52"
          :placeholder="t('audit.fields.from')"
        />
        <Input
          v-model="to"
          type="datetime-local"
          size="sm"
          class="w-52"
          :placeholder="t('audit.fields.to')"
        />
      </template>

      <template #cell-action="{ row }">
        <Badge size="xs" tone="violet" :title="(row as AuditEntry).action">{{
          auditActionLabel((row as AuditEntry).action, t, te)
        }}</Badge>
      </template>
      <template #cell-target_type="{ row }">
        <span class="text-xs text-zinc-500" data-testid="audit-target">{{ targetOf(row as AuditEntry) }}</span>
      </template>
    </DataTable>

    <Modal
      :model-value="detail !== null"
      :title="t('common.details')"
      size="lg"
      @update:model-value="(v) => (v ? null : (detail = null))"
    >
      <!-- ⚠ The row in words first; the stored record (a JSON object with
           the wire names) stays below for whoever needs to quote it. -->
      <dl v-if="detail" class="grid grid-cols-[auto,1fr] gap-x-4 gap-y-1.5 text-sm" data-testid="audit-detail">
        <dt class="text-zinc-500">{{ t('common.created') }}</dt>
        <dd>{{ formatDate(detail.at, locale) }}</dd>
        <dt class="text-zinc-500">{{ t('audit.fields.user') }}</dt>
        <dd>{{ whoOf(detail) }}</dd>
        <dt class="text-zinc-500">{{ t('audit.fields.action') }}</dt>
        <dd>{{ auditActionLabel(detail.action, t, te) }}</dd>
        <dt class="text-zinc-500">{{ t('audit.fields.target') }}</dt>
        <dd class="break-all">{{ targetOf(detail) }}</dd>
        <dt class="text-zinc-500">{{ t('audit.fields.ip') }}</dt>
        <dd>{{ ipOnly(detail.ip) || '—' }}</dd>
      </dl>
      <details v-if="detail" class="mt-3 text-xs">
        <summary class="cursor-pointer text-zinc-500">{{ t('audit.rawRecord') }}</summary>
        <pre class="mt-2 overflow-auto rounded-md bg-zinc-50 dark:bg-zinc-800 p-3 font-mono">{{
          JSON.stringify(detail, null, 2)
        }}</pre>
      </details>
    </Modal>
  </div>
</template>
