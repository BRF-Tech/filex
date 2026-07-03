<script setup lang="ts">
// Admin İzinler — global overview of every per-file/folder RBAC grant: who has
// what level, on which path, in which storage. Admin can revoke any grant.
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Trash2, ShieldCheck } from 'lucide-vue-next';

import { AdminGrantsApi, type AdminGrant } from '@/api/grants';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import Spinner from '@/components/ui/Spinner.vue';
import Badge from '@/components/ui/Badge.vue';

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
  if (!term) return grants.value;
  return grants.value.filter(
    (g) =>
      g.user_email.toLowerCase().includes(term) ||
      g.path.toLowerCase().includes(term) ||
      g.storage_name.toLowerCase().includes(term) ||
      g.level.includes(term),
  );
});

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
      <input
        v-model="q"
        type="search"
        :placeholder="t('grants.search')"
        class="rounded-lg border border-zinc-300 dark:border-zinc-700 bg-transparent px-3 py-2 text-sm w-64 max-w-full"
      />
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>
    <div v-else class="card overflow-x-auto">
      <table class="w-full text-sm">
        <thead class="text-left text-zinc-500 dark:text-zinc-400 border-b border-zinc-200 dark:border-zinc-800">
          <tr>
            <th class="px-4 py-2 font-medium">{{ t('grants.user') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('grants.storage') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('grants.path') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('grants.level') }}</th>
            <th class="px-4 py-2 font-medium text-right">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="g in filtered"
            :key="g.id"
            class="border-b border-zinc-100 dark:border-zinc-800/60"
          >
            <td class="px-4 py-2">{{ g.user_email || '#' + g.user_id }}</td>
            <td class="px-4 py-2">{{ g.storage_name }}</td>
            <td class="px-4 py-2 font-mono text-xs">{{ g.path_prefix || '/' }}<span v-if="g.is_dir && g.path_prefix" class="text-zinc-400">/…</span></td>
            <td class="px-4 py-2"><Badge :tone="levelTone(g.level)">{{ g.level }}</Badge></td>
            <td class="px-4 py-2 text-right">
              <button
                class="inline-flex items-center gap-1 text-rose-600 hover:text-rose-500 text-xs"
                @click="revoke(g)"
              >
                <Trash2 class="h-3.5 w-3.5" /> {{ t('grants.revoke') }}
              </button>
            </td>
          </tr>
          <tr v-if="!filtered.length">
            <td colspan="5" class="px-4 py-6 text-center text-zinc-500">{{ t('grants.empty') }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
