<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { RefreshCcw, Trash2, Ban } from 'lucide-vue-next';

import { SharesApi } from '@/api/shares';
import type { PaginatedResponse, Share } from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatDate, formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Input from '@/components/ui/Input.vue';
import Table, { type Column } from '@/components/ui/Table.vue';
import Modal from '@/components/ui/Modal.vue';
import CopyButton from '@/components/ui/CopyButton.vue';

const { t, locale } = useI18n();
const toast = useToastStore();

const shares = ref<PaginatedResponse<Share>>({
  items: [],
  total: 0,
  page: 1,
  page_size: 25,
});
const loading = ref(false);
const q = ref('');
const page = ref(1);
const pageSize = 50;

const showRevoke = ref<Share | null>(null);
const showDelete = ref<Share | null>(null);
const busyId = ref<number | null>(null);

// Admin list rows come back as { share: Share, creator_email, node_path,
// storage_name } from the backend's `ShareWithMeta` envelope. Helper
// unwraps either shape so the template can stay terse.
interface ShareRow {
  share?: Share;
  creator_email?: string;
  node_path?: string;
  storage_name?: string;
  [k: string]: unknown;
}
function shareOf(row: unknown): Share {
  const r = row as ShareRow & Share;
  return (r.share ?? (r as unknown as Share));
}

async function load() {
  loading.value = true;
  try {
    shares.value = await SharesApi.list({
      q: q.value || undefined,
      page: page.value,
      page_size: pageSize,
    });
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

watch(q, () => {
  page.value = 1;
  load();
});

async function revoke() {
  if (!showRevoke.value) return;
  busyId.value = showRevoke.value.id;
  try {
    await SharesApi.revoke(showRevoke.value.id);
    toast.success(t('shares.revokedOk'));
    showRevoke.value = null;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busyId.value = null;
  }
}

async function remove() {
  if (!showDelete.value) return;
  busyId.value = showDelete.value.id;
  try {
    await SharesApi.remove(showDelete.value.id);
    toast.success(t('shares.deletedOk'));
    showDelete.value = null;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    busyId.value = null;
  }
}

// Build the public share URL the recipient would actually use. We
// hit `/s/<token>` on the same origin as the panel — that's how
// nginx is configured + the backend's share viewer is mounted.
function shareUrl(s: Share): string {
  if (typeof window === 'undefined') return `/s/${s.token}`;
  return `${window.location.origin}/s/${s.token}`;
}

const columns = computed<Column<Share>[]>(() => [
  { key: 'token', label: t('shares.fields.token'), cell: 'slot' },
  {
    key: 'storage_name',
    label: t('shares.fields.storage'),
    format: (r) => (r as unknown as ShareRow).storage_name || '—',
  },
  { key: 'path', label: t('shares.fields.path'), cell: 'slot' },
  { key: 'created_at', label: t('shares.fields.created'), cell: 'slot' },
  { key: 'expires_at', label: t('shares.fields.expires'), cell: 'slot' },
  {
    key: 'download_count',
    label: t('shares.fields.downloads'),
    align: 'right',
    format: (r) => {
      const s = shareOf(r);
      const max = s.max_downloads ?? null;
      const cur = s.download_count ?? 0;
      return max ? `${cur} / ${max}` : String(cur);
    },
  },
  { key: 'creator', label: t('shares.fields.creator'), cell: 'slot' },
  { key: 'actions', label: t('common.actions'), cell: 'slot', align: 'right', width: '160px' },
]);

onMounted(load);
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('shares.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('shares.subtitle') }}</p>
      </div>
      <Button variant="outline" size="sm" @click="load" :loading="loading">
        <RefreshCcw class="h-4 w-4" />
        {{ t('common.refresh') }}
      </Button>
    </div>

    <Table
      :columns="columns"
      :rows="shares.items"
      :loading="loading"
      :empty="t('shares.noResults')"
      :page="page"
      :page-size="pageSize"
      :total="shares.total"
      row-key="id"
      @page="(p) => ((page = p), load())"
    >
      <template #toolbar>
        <Input v-model="q" :placeholder="t('common.search')" size="sm" class="w-60" />
      </template>

      <template #cell-token="{ row }">
        <div class="flex items-center gap-2">
          <code class="text-xs font-mono text-zinc-700 dark:text-zinc-300">
            {{ shareOf(row).token.slice(0, 10) }}…
          </code>
          <CopyButton
            :value="shareUrl(shareOf(row))"
            size="xs"
            :title="t('shares.copyLink')"
          />
          <CopyButton
            :value="shareOf(row).token"
            size="xs"
            :title="t('shares.copyToken')"
            variant="ghost"
          />
          <Badge v-if="shareOf(row).has_pin || shareOf(row).pin_set" tone="amber" size="xs">PIN</Badge>
          <Badge v-if="shareOf(row).revoked_at || shareOf(row).revoked" tone="rose" size="xs">revoked</Badge>
        </div>
      </template>

      <template #cell-created_at="{ row }">
        <span class="text-xs whitespace-nowrap" :title="formatDate(shareOf(row).created_at, locale)">
          {{ formatDate(shareOf(row).created_at, locale) }}
        </span>
      </template>

      <template #cell-path="{ row }">
        <span class="text-xs font-mono text-zinc-500 truncate max-w-xs inline-block">
          {{ (row as ShareRow).node_path || shareOf(row).path }}
        </span>
      </template>

      <template #cell-creator="{ row }">
        <span class="text-xs text-zinc-500 dark:text-zinc-400">
          {{ (row as ShareRow).creator_email || ('#' + (shareOf(row).created_by ?? '?')) }}
          <!-- token username the creating API call acted under ("work", "fishapp"…) -->
          <span
            v-if="shareOf(row).created_via"
            class="ml-1 rounded bg-violet-100 dark:bg-violet-900/40 px-1 py-0.5 font-mono text-[10px] text-violet-700 dark:text-violet-300"
          >{{ shareOf(row).created_via }}</span>
        </span>
      </template>

      <template #cell-expires_at="{ row }">
        <span class="text-xs whitespace-nowrap" :title="shareOf(row).expires_at ? formatDate(shareOf(row).expires_at, locale) : ''">
          <template v-if="shareOf(row).expires_at">
            {{ formatDate(shareOf(row).expires_at, locale) }}
            <span class="text-zinc-400">·</span>
            {{ formatRelative(shareOf(row).expires_at, locale) }}
          </template>
          <template v-else>{{ t('shares.neverExpires') }}</template>
        </span>
      </template>

      <template #cell-actions="{ row }">
        <div class="flex items-center justify-end gap-1">
          <Button
            v-if="!shareOf(row).revoked_at && !shareOf(row).revoked"
            size="xs"
            variant="ghost"
            :title="t('shares.revokedOk')"
            @click="showRevoke = shareOf(row)"
          >
            <Ban class="h-3.5 w-3.5 text-amber-500" />
          </Button>
          <Button
            size="xs"
            variant="ghost"
            :title="t('common.delete')"
            @click="showDelete = shareOf(row)"
          >
            <Trash2 class="h-3.5 w-3.5 text-rose-500" />
          </Button>
        </div>
      </template>
    </Table>

    <Modal
      :model-value="showRevoke !== null"
      :title="t('shares.revokedOk')"
      size="sm"
      @update:model-value="(v) => (v ? null : (showRevoke = null))"
    >
      <p class="text-sm">{{ t('shares.revokeConfirm') }}</p>
      <template #footer>
        <Button variant="ghost" @click="showRevoke = null">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="busyId === showRevoke?.id" @click="revoke">
          {{ t('common.confirm') }}
        </Button>
      </template>
    </Modal>

    <Modal
      :model-value="showDelete !== null"
      :title="t('common.delete')"
      size="sm"
      @update:model-value="(v) => (v ? null : (showDelete = null))"
    >
      <p class="text-sm">{{ t('shares.deleteConfirm') }}</p>
      <template #footer>
        <Button variant="ghost" @click="showDelete = null">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="busyId === showDelete?.id" @click="remove">
          {{ t('common.yesDelete') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>
