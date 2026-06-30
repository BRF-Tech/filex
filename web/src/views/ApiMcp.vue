<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Plus, Trash2, RefreshCcw, KeyRound } from 'lucide-vue-next';

import { AITokensApi, type AIToken } from '@/api/ai-tokens';
import { StoragesApi } from '@/api/storages';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import { formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Badge from '@/components/ui/Badge.vue';
import Modal from '@/components/ui/Modal.vue';
import CopyButton from '@/components/ui/CopyButton.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t, locale } = useI18n();
const toast = useToastStore();

const tokens = ref<AIToken[]>([]);
const loading = ref(false);

const SCOPES = ['read', 'write', 'delete', 'mcp', 'admin'] as const;
type Scope = (typeof SCOPES)[number];

const showCreate = ref(false);
const showDelete = ref<AIToken | null>(null);
const creating = ref(false);
const deleting = ref(false);

const newLabel = ref('');
const newScopes = ref<Record<Scope, boolean>>({
  read: true,
  write: true,
  delete: false,
  mcp: true,
  admin: false,
});
const newExpiry = ref<number | null>(null);
const newRootStorage = ref('');
const newRootPath = ref('');
const createdToken = ref<string | null>(null);

const storages = ref<{ value: string; label: string }[]>([]);
const storageOptions = computed(() => [
  { value: '', label: t('apiMcp.fields.rootNone') },
  ...storages.value,
]);

const origin = window.location.origin;
const mcpUrl = `${origin}/api/ai/mcp`;
const restBase = `${origin}/api/ai`;

const claudeSnippet = computed(
  () =>
    `claude mcp add --transport http filex ${mcpUrl} --header "X-Filex-Token: ${
      createdToken.value ?? '<TOKEN>'
    }"`,
);

function scopeList(s: string): string[] {
  const v = (s ?? '').trim();
  return v ? v.split(',') : [];
}

// A token's scope string mixes verb scopes (read/write/…) with at most one
// `root:<adapter>://<rel>` confinement scope. Split them for display.
function verbScopes(s: string): string[] {
  return scopeList(s).filter((x) => !x.startsWith('root:'));
}
function rootScope(s: string): string | null {
  const r = scopeList(s).find((x) => x.startsWith('root:'));
  return r ? r.slice('root:'.length) : null;
}

async function load() {
  loading.value = true;
  try {
    tokens.value = await AITokensApi.list();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

async function loadStorages() {
  try {
    const list = await StoragesApi.list();
    storages.value = list.map((s) => ({ value: s.name, label: `${s.name} (${s.driver})` }));
  } catch {
    /* tolerated — root selection simply stays empty (full disk) */
  }
}

function openCreate() {
  newLabel.value = '';
  newScopes.value = { read: true, write: true, delete: false, mcp: true, admin: false };
  newExpiry.value = null;
  newRootStorage.value = '';
  newRootPath.value = '';
  createdToken.value = null;
  showCreate.value = true;
}

async function submitCreate() {
  creating.value = true;
  try {
    const parts: string[] = SCOPES.filter((s) => newScopes.value[s]);
    if (newRootStorage.value) {
      const rel = newRootPath.value.trim().replace(/^\/+|\/+$/g, '');
      parts.push(`root:${newRootStorage.value}://${rel}`);
    }
    const res = await AITokensApi.create({
      label: newLabel.value.trim(),
      scopes: parts.join(','),
      expires_in_days: newExpiry.value && newExpiry.value > 0 ? newExpiry.value : undefined,
    });
    createdToken.value = res.token;
    toast.success(t('apiMcp.createdOk'));
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    creating.value = false;
  }
}

function closeCreate() {
  showCreate.value = false;
  createdToken.value = null;
}

async function confirmDelete() {
  if (!showDelete.value) return;
  deleting.value = true;
  try {
    await AITokensApi.remove(showDelete.value.id);
    toast.success(t('apiMcp.deletedOk'));
    showDelete.value = null;
    await load();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    deleting.value = false;
  }
}

onMounted(() => {
  load();
  loadStorages();
});
</script>

<template>
  <div class="space-y-4 max-w-4xl">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('apiMcp.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('apiMcp.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="outline" size="sm" :loading="loading" @click="load">
          <RefreshCcw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
        <Button @click="openCreate">
          <Plus class="h-4 w-4" />
          {{ t('apiMcp.newToken') }}
        </Button>
      </div>
    </div>

    <!-- Connection info -->
    <div class="card card-body space-y-3">
      <h2 class="text-sm font-semibold flex items-center gap-2">
        <KeyRound class="h-4 w-4" />
        {{ t('apiMcp.connectTitle') }}
      </h2>
      <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('apiMcp.connectHint') }}</p>
      <div class="space-y-2">
        <div>
          <p class="text-xs text-zinc-500 mb-1">{{ t('apiMcp.mcpEndpoint') }}</p>
          <div class="flex items-center gap-2">
            <code
              class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-xs font-mono break-all"
            >{{ mcpUrl }}</code>
            <CopyButton :value="mcpUrl" />
          </div>
        </div>
        <div>
          <p class="text-xs text-zinc-500 mb-1">{{ t('apiMcp.restBase') }}</p>
          <div class="flex items-center gap-2">
            <code
              class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-xs font-mono break-all"
            >{{ restBase }}</code>
            <CopyButton :value="restBase" />
          </div>
        </div>
      </div>
    </div>

    <!-- Tokens table -->
    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>
    <div v-else class="card overflow-hidden">
      <table class="w-full text-sm">
        <thead class="bg-zinc-50 dark:bg-zinc-800/50 text-left text-xs text-zinc-500">
          <tr>
            <th class="px-4 py-2 font-medium">{{ t('apiMcp.cols.label') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('apiMcp.cols.scopes') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('apiMcp.cols.root') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('apiMcp.cols.lastUsed') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('apiMcp.cols.expires') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('apiMcp.cols.created') }}</th>
            <th class="px-4 py-2"></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-zinc-200 dark:divide-zinc-800">
          <tr v-for="tok in tokens" :key="tok.id">
            <td class="px-4 py-2 font-medium">{{ tok.label || '—' }}</td>
            <td class="px-4 py-2">
              <div class="flex flex-wrap gap-1">
                <Badge v-if="!scopeList(tok.scopes).length" tone="amber" size="xs">all</Badge>
                <Badge
                  v-for="s in verbScopes(tok.scopes)"
                  :key="s"
                  :tone="s === 'admin' ? 'rose' : 'zinc'"
                  size="xs"
                >{{ s }}</Badge>
              </div>
            </td>
            <td class="px-4 py-2 text-xs">
              <span v-if="rootScope(tok.scopes)" class="font-mono text-violet-600 dark:text-violet-400 break-all">
                📁 {{ rootScope(tok.scopes) }}
              </span>
              <span v-else class="text-zinc-400">{{ t('apiMcp.fullDisk') }}</span>
            </td>
            <td class="px-4 py-2 text-xs text-zinc-500">
              {{ tok.last_used_at ? formatRelative(tok.last_used_at, locale) : '—' }}
            </td>
            <td class="px-4 py-2 text-xs text-zinc-500">
              {{ tok.expires_at ? formatRelative(tok.expires_at, locale) : t('apiMcp.never') }}
            </td>
            <td class="px-4 py-2 text-xs text-zinc-500">
              {{ formatRelative(tok.created_at, locale) }}
            </td>
            <td class="px-4 py-2 text-right">
              <Button size="xs" variant="ghost" @click="showDelete = tok" :title="t('common.delete')">
                <Trash2 class="h-3.5 w-3.5 text-rose-500" />
              </Button>
            </td>
          </tr>
          <tr v-if="!tokens.length">
            <td colspan="7" class="px-4 py-8 text-center text-zinc-500 text-sm">
              {{ t('apiMcp.empty') }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Create / reveal modal -->
    <Modal
      :model-value="showCreate"
      :title="createdToken ? t('apiMcp.tokenCreated') : t('apiMcp.newToken')"
      size="md"
      :prevent-close="creating"
      @update:model-value="(v) => (v ? null : closeCreate())"
    >
      <!-- Step 1: form -->
      <form v-if="!createdToken" class="space-y-3" @submit.prevent="submitCreate">
        <Input v-model="newLabel" :label="t('apiMcp.fields.label')" :placeholder="t('apiMcp.fields.labelPlaceholder')" required />
        <div>
          <p class="label-base mb-1">{{ t('apiMcp.fields.scopes') }}</p>
          <div class="grid grid-cols-2 gap-2">
            <label
              v-for="s in SCOPES"
              :key="s"
              class="flex items-start gap-2 rounded-md border border-zinc-200 dark:border-zinc-700 p-2 cursor-pointer hover:bg-zinc-50 dark:hover:bg-zinc-800/50"
            >
              <input type="checkbox" v-model="newScopes[s]" class="mt-0.5" />
              <span>
                <span class="text-sm font-mono">{{ s }}</span>
                <span class="block text-xs text-zinc-500">{{ t(`apiMcp.scopeDesc.${s}` as any) }}</span>
              </span>
            </label>
          </div>
          <p class="help-text mt-1">{{ t('apiMcp.fields.scopesHint') }}</p>
        </div>

        <!-- Root confinement (optional) -->
        <div>
          <p class="label-base mb-1">{{ t('apiMcp.fields.root') }}</p>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
            <Select v-model="newRootStorage" :options="storageOptions" />
            <Input
              v-model="newRootPath"
              :placeholder="t('apiMcp.fields.rootPathPlaceholder')"
              :disabled="!newRootStorage"
              monospace
            />
          </div>
          <p class="help-text mt-1">{{ t('apiMcp.fields.rootHint') }}</p>
        </div>

        <Input
          v-model.number="newExpiry"
          type="number"
          :min="0"
          :label="t('apiMcp.fields.expiry')"
          :hint="t('apiMcp.fields.expiryHint')"
        />
      </form>

      <!-- Step 2: reveal -->
      <div v-else class="space-y-3">
        <p class="text-sm text-zinc-600 dark:text-zinc-400">{{ t('apiMcp.tokenOnce') }}</p>
        <div class="flex items-center gap-2">
          <code
            class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-sm font-mono break-all"
          >{{ createdToken }}</code>
          <CopyButton :value="createdToken" />
        </div>
        <div>
          <p class="text-xs text-zinc-500 mb-1">{{ t('apiMcp.addToClaude') }}</p>
          <div class="flex items-start gap-2">
            <code
              class="flex-1 select-all rounded-md border border-zinc-200 dark:border-zinc-700 bg-zinc-50 dark:bg-zinc-800 p-2 text-xs font-mono break-all"
            >{{ claudeSnippet }}</code>
            <CopyButton :value="claudeSnippet" />
          </div>
        </div>
      </div>

      <template #footer>
        <template v-if="!createdToken">
          <Button variant="ghost" @click="closeCreate">{{ t('common.cancel') }}</Button>
          <Button :loading="creating" :disabled="!newLabel.trim()" @click="submitCreate">
            {{ t('common.create') }}
          </Button>
        </template>
        <Button v-else @click="closeCreate">{{ t('common.close') }}</Button>
      </template>
    </Modal>

    <!-- Delete modal -->
    <Modal
      :model-value="showDelete !== null"
      :title="t('common.delete')"
      size="sm"
      @update:model-value="(v) => (v ? null : (showDelete = null))"
    >
      <p class="text-sm">{{ t('apiMcp.deleteConfirm', { label: showDelete?.label || showDelete?.id }) }}</p>
      <template #footer>
        <Button variant="ghost" @click="showDelete = null">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="deleting" @click="confirmDelete">
          {{ t('common.yesDelete') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>
