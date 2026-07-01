<script setup lang="ts">
// SelfTokensModal — lets a non-admin (user/viewer) manage their OWN API tokens
// from the explorer. Scopes are capped server-side to the account role; the UI
// only offers what the role can hold (viewer → read-only). Opened from the
// Explore header "Hesabım" menu.
import { ref, computed, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { SelfTokensApi } from '@/api/self-tokens';
import type { AIToken } from '@/api/ai-tokens';
import { useAuthStore } from '@/stores/auth';
import { extractError } from '@/api/client';

const emit = defineEmits<{ (e: 'close'): void }>();
const { t } = useI18n();
const auth = useAuthStore();

const tokens = ref<AIToken[]>([]);
const loading = ref(true);
const err = ref('');

const label = ref('');
const rootPath = ref('');
const expiresInDays = ref<number | null>(null);
const created = ref<{ token: string } | null>(null);
const busy = ref(false);

const isViewer = computed(() => auth.user?.role === 'viewer');
// Scope checkboxes the current role may hold. Viewer accounts are read-only.
const scopeState = ref<Record<string, boolean>>({ read: true, write: false, delete: false, mcp: false });
const availableScopes = computed(() =>
  isViewer.value ? ['read', 'mcp'] : ['read', 'write', 'delete', 'mcp'],
);

async function reload() {
  loading.value = true;
  err.value = '';
  try {
    tokens.value = await SelfTokensApi.list();
  } catch (e) {
    err.value = extractError(e, 'error');
  } finally {
    loading.value = false;
  }
}
onMounted(reload);

function buildScopes(): string {
  const verbs = availableScopes.value.filter((s) => scopeState.value[s]);
  const parts = [...verbs];
  if (rootPath.value.trim()) parts.push('root:' + rootPath.value.trim());
  return parts.join(',');
}

async function create() {
  busy.value = true;
  err.value = '';
  created.value = null;
  try {
    const res = await SelfTokensApi.create({
      label: label.value.trim(),
      scopes: buildScopes(),
      expires_in_days: expiresInDays.value ?? undefined,
    });
    created.value = { token: res.token };
    label.value = '';
    rootPath.value = '';
    await reload();
  } catch (e) {
    err.value = extractError(e, 'error');
  } finally {
    busy.value = false;
  }
}

async function remove(id: number) {
  busy.value = true;
  try {
    await SelfTokensApi.remove(id);
    await reload();
  } catch (e) {
    err.value = extractError(e, 'error');
  } finally {
    busy.value = false;
  }
}

function copyToken() {
  if (created.value) navigator.clipboard?.writeText(created.value.token);
}
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/45" @click.self="emit('close')">
    <div class="w-[min(560px,94vw)] max-h-[88vh] overflow-auto rounded-xl bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 p-5 shadow-2xl">
      <div class="flex items-center justify-between mb-3">
        <h3 class="text-base font-semibold">{{ t('selfTokens.title') }}</h3>
        <button class="text-zinc-500 hover:text-zinc-800 dark:hover:text-zinc-200" @click="emit('close')">✕</button>
      </div>

      <p v-if="isViewer" class="text-xs text-amber-600 dark:text-amber-400 mb-2">
        {{ t('selfTokens.viewerNote') }}
      </p>

      <!-- Create -->
      <div class="space-y-2 border-b border-zinc-200 dark:border-zinc-800 pb-4 mb-4">
        <input v-model="label" :placeholder="t('selfTokens.label')"
          class="w-full rounded-lg border border-zinc-300 dark:border-zinc-700 bg-transparent px-3 py-2 text-sm" />
        <div class="flex flex-wrap gap-3 text-sm">
          <label v-for="s in availableScopes" :key="s" class="inline-flex items-center gap-1">
            <input type="checkbox" v-model="scopeState[s]" /> {{ s }}
          </label>
        </div>
        <input v-model="rootPath" :placeholder="t('selfTokens.rootPath')"
          class="w-full rounded-lg border border-zinc-300 dark:border-zinc-700 bg-transparent px-3 py-2 text-sm" />
        <div class="flex items-center gap-2">
          <input v-model.number="expiresInDays" type="number" min="0" :placeholder="t('selfTokens.expiry')"
            class="w-40 rounded-lg border border-zinc-300 dark:border-zinc-700 bg-transparent px-3 py-2 text-sm" />
          <button :disabled="busy" @click="create"
            class="ml-auto rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white px-3 py-2 text-sm disabled:opacity-50">
            {{ t('selfTokens.create') }}
          </button>
        </div>
        <div v-if="created" class="mt-2 rounded-lg bg-emerald-50 dark:bg-emerald-950/40 p-2 text-sm">
          <p class="text-xs text-emerald-700 dark:text-emerald-400 mb-1">{{ t('selfTokens.copyOnce') }}</p>
          <div class="flex items-center gap-2">
            <code class="flex-1 break-all text-xs">{{ created.token }}</code>
            <button class="text-xs underline" @click="copyToken">{{ t('selfTokens.copy') }}</button>
          </div>
        </div>
      </div>

      <div v-if="err" class="text-sm text-rose-600 dark:text-rose-400 mb-2">{{ err }}</div>
      <div v-if="loading" class="text-sm text-zinc-500">…</div>

      <!-- List -->
      <ul v-else class="space-y-1">
        <li v-for="tok in tokens" :key="tok.id"
          class="flex items-center gap-2 text-sm py-1 border-b border-zinc-100 dark:border-zinc-800/60">
          <span class="flex-1 truncate">{{ tok.label || '(no label)' }}</span>
          <span class="text-xs text-zinc-500 truncate max-w-[45%]">{{ tok.scopes || 'all' }}</span>
          <button class="text-rose-500 text-xs" :disabled="busy" @click="remove(tok.id)">✕</button>
        </li>
        <li v-if="!tokens.length" class="text-sm text-zinc-500 py-1">{{ t('selfTokens.empty') }}</li>
      </ul>
    </div>
  </div>
</template>
