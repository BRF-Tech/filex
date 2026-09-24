<script setup lang="ts">
// Admin Permissions — global overview of every per-file/folder RBAC grant: who has
// what level, on which path, in which storage. Admin can revoke any grant.
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Trash2, ShieldCheck } from 'lucide-vue-next';

import { AdminGrantsApi, type AdminGrant } from '@/api/grants';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import { DataTable, personName, type ContextAction, type DataColumn } from '@brftech/filex-core';

const { t } = useI18n();
const toast = useToastStore();

const grants = ref<AdminGrant[]>([]);
const loading = ref(true);
const q = ref('');

async function load() {
  loading.value = true;
  try {
    grants.value = await AdminGrantsApi.list();
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

const filtered = computed(() => {
  const term = q.value.trim().toLowerCase();
  const rows = !term
    ? [...grants.value]
    : grants.value.filter(
        (g) =>
          g.user_email.toLowerCase().includes(term) ||
          (g.user_name ?? '').toLowerCase().includes(term) ||
          g.path.toLowerCase().includes(term) ||
          g.storage_name.toLowerCase().includes(term) ||
          g.level.includes(term),
      );
  /* The order the table opens in before anybody picks a column: by person.
     A header click re-sorts on top of it (DataTable sorts itself — the
     endpoint answers with every grant at once, so a client sort is honest
     here) and is remembered under `admin.grants`. */
  return rows.sort((a, b) => userOf(a).localeCompare(userOf(b)));
});

function userOf(g: AdminGrant): string {
  return personName({ name: g.user_name, email: g.user_email }) || `#${g.user_id}`;
}

/** owner → editor → viewer, so "Level ↑" reads from the most to the least. */
const LEVEL_RANK: Record<string, number> = { owner: 0, editor: 1, viewer: 2 };

/* The explorer's table (DataTable): every column resizes, hides, moves and
 * sorts, and the arrangement is remembered on the account. */
const columns = computed<DataColumn<AdminGrant>[]>(() => [
  { id: 'user', label: t('grants.user'), sortable: true, width: 220, sortValue: userOf },
  { id: 'storage_name', label: t('grants.storage'), sortable: true, width: 150 },
  {
    id: 'path',
    label: t('grants.path'),
    sortable: true,
    width: 240,
    sortValue: (g) => g.path_prefix || '/',
  },
  {
    id: 'level',
    label: t('grants.level'),
    sortable: true,
    width: 110,
    sortValue: (g) => LEVEL_RANK[g.level] ?? 9,
  },
]);

function levelTone(l: string): 'rose' | 'amber' | 'zinc' {
  if (l === 'owner') return 'rose';
  if (l === 'editor') return 'amber';
  return 'zinc';
}

async function revoke(g: AdminGrant) {
  if (!confirm(t('grants.revokeConfirm', { email: g.user_email, path: g.path }))) return;
  try {
    await AdminGrantsApi.remove(g.id);
    grants.value = grants.value.filter((x) => x.id !== g.id);
    toast.success(t('grants.revokedOk'));
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

/** The row's one verb, behind its one pinned `Actions` control. */
function rowActions(row: AdminGrant): ContextAction[] {
  return [{ key: 'revoke', label: t('grants.revoke'), icon: 'delete', danger: true }];
}

function onRowAction(key: string, row: AdminGrant) {
  if (key === 'revoke') revoke(row);
}
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-3 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          <ShieldCheck class="h-5 w-5" /> {{ t('grants.title') }}
        </h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('grants.subtitle') }}</p>
      </div>
    </div>

    <DataTable
      table-id="admin.grants"
      :columns="columns"
      :rows="filtered"
      :loading="loading"
      :empty="t('grants.empty')"
      row-key="id"
      :row-actions="(row: AdminGrant) => rowActions(row)"
      :row-actions-test-id="(row: AdminGrant) => `grant-actions-${row.id}`"
      @row-action="(key: string, row: AdminGrant) => onRowAction(key, row)"
    >
      <template #toolbar>
        <Input v-model="q" type="search" :placeholder="t('grants.search')" size="sm" class="w-64 max-w-full" />
      </template>

      <template #cell-user="{ row }">
        <div>
          {{ userOf(row as AdminGrant) }}
          <span v-if="(row as AdminGrant).user_name && (row as AdminGrant).user_email" class="tbl-sub">{{ (row as AdminGrant).user_email }}</span>
        </div>
      </template>

      <template #cell-path="{ row }">
        <span class="tbl-mono">
          {{ (row as AdminGrant).path_prefix || '/'
          }}<span v-if="(row as AdminGrant).is_dir && (row as AdminGrant).path_prefix">/…</span>
        </span>
      </template>

      <template #cell-level="{ row }">
        <Badge :tone="levelTone((row as AdminGrant).level)">{{ (row as AdminGrant).level }}</Badge>
      </template>
    </DataTable>
  </div>
</template>
