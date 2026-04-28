<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { RefreshCcw } from 'lucide-vue-next';

import { useAuditStore } from '@/stores/audit';
import type { AuditEntry } from '@/api/types';
import { formatDate } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Table, { type Column } from '@/components/ui/Table.vue';
import Modal from '@/components/ui/Modal.vue';
import Badge from '@/components/ui/Badge.vue';

const { t, locale } = useI18n();
const audit = useAuditStore();

const action = ref('');
const targetType = ref('');
const from = ref('');
const to = ref('');
const page = ref(1);
const pageSize = 50;

const detail = ref<AuditEntry | null>(null);

async function load() {
  await audit.fetch({
    action: action.value || undefined,
    target_type: targetType.value || undefined,
    from: from.value || undefined,
    to: to.value || undefined,
    page: page.value,
    page_size: pageSize,
  });
}

watch([action, targetType, from, to], () => {
  page.value = 1;
  load();
});

const columns = computed<Column<AuditEntry>[]>(() => [
  { key: 'at', label: t('common.created'), format: (r) => formatDate(r.at, locale.value) },
  { key: 'user_email', label: t('audit.fields.user'), format: (r) => r.user_email ?? '—' },
  { key: 'action', label: t('audit.fields.action'), cell: 'slot' },
  { key: 'target_type', label: t('audit.fields.target'), cell: 'slot' },
  { key: 'ip', label: t('audit.fields.ip'), format: (r) => r.ip ?? '—' },
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

    <Table
      :columns="columns"
      :rows="audit.page.items"
      :loading="audit.loading"
      :empty="t('audit.noResults')"
      :page="page"
      :page-size="pageSize"
      :total="audit.page.total"
      row-key="id"
      @page="(p) => ((page = p), load())"
      @row-click="(r) => (detail = r as AuditEntry)"
    >
      <template #toolbar>
        <Input
          v-model="action"
          :placeholder="t('audit.fields.action')"
          size="sm"
          class="w-48"
          autocomplete="off"
        />
        <Input
          v-model="targetType"
          :placeholder="t('audit.fields.target')"
          size="sm"
          class="w-36"
          autocomplete="off"
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
        <Badge size="xs" tone="violet">{{ (row as AuditEntry).action }}</Badge>
      </template>
      <template #cell-target_type="{ row }">
        <span class="text-xs text-zinc-500">
          {{ (row as AuditEntry).target_type ?? '—' }}
          <template v-if="(row as AuditEntry).target_id">
            :<span class="font-mono">{{ (row as AuditEntry).target_id }}</span>
          </template>
        </span>
      </template>
    </Table>

    <Modal
      :model-value="detail !== null"
      :title="t('common.details')"
      size="lg"
      @update:model-value="(v) => (v ? null : (detail = null))"
    >
      <pre
        v-if="detail"
        class="overflow-auto rounded-md bg-zinc-50 dark:bg-zinc-800 p-3 text-xs font-mono"
      >{{ JSON.stringify(detail, null, 2) }}</pre>
    </Modal>
  </div>
</template>
