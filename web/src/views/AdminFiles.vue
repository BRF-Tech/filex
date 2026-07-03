<script setup lang="ts">
/**
 * AdminFiles — small lookup page that takes a node ID and routes the
 * caller to the version history.
 *
 * Why this and not a hover-action on Explore? The FileExplorer SFC
 * (`@brftech/filex-core`) owns its own context menu / row hover state
 * and the embedder can't extend it without forking the SFC. Until the
 * SFC exposes a slot or event for "open versions", this dedicated page
 * is the least-invasive way to surface version history from the admin
 * SPA. Users find a node ID via the API (or the SFC's own info panel)
 * and paste it here.
 *
 * v1 deliberately scopes itself to "I know the node ID" — a fuzzy
 * search-by-path UI can land in v2 once the manager listing returns
 * version_count alongside the row data.
 */
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { History, Search } from 'lucide-vue-next';

import Button from '@/components/ui/Button.vue';
import EmptyState from '@/components/ui/EmptyState.vue';

const { t } = useI18n();
const router = useRouter();

const nodeIdInput = ref('');
const error = ref<string | null>(null);

const parsedId = computed(() => {
  const v = nodeIdInput.value.trim();
  if (!v) return null;
  const n = Number(v);
  if (!Number.isFinite(n) || n <= 0 || !Number.isInteger(n)) return null;
  return n;
});

function go() {
  error.value = null;
  if (parsedId.value === null) {
    error.value = t('adminFiles.invalidId');
    return;
  }
  router.push({ name: 'files.versions', params: { nodeId: parsedId.value } });
}
</script>

<template>
  <section class="space-y-4">
    <header class="space-y-1">
      <h1 class="text-2xl font-semibold text-zinc-900 dark:text-zinc-100">
        {{ t('adminFiles.title') }}
      </h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">
        {{ t('adminFiles.subtitle') }}
      </p>
    </header>

    <div class="card">
      <div class="card-body space-y-3">
        <label for="node-id" class="block text-sm font-medium text-zinc-800 dark:text-zinc-200">
          {{ t('adminFiles.nodeIdLabel') }}
        </label>
        <div class="flex gap-2">
          <input
            id="node-id"
            v-model="nodeIdInput"
            type="text"
            inputmode="numeric"
            pattern="[0-9]*"
            :placeholder="t('adminFiles.nodeIdPlaceholder')"
            class="flex-1 rounded-md border border-zinc-300 bg-white px-3 py-2 text-sm text-zinc-900 placeholder:text-zinc-400 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/30 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-100 dark:placeholder:text-zinc-500"
            @keydown.enter="go"
          />
          <Button variant="primary" :disabled="parsedId === null" @click="go">
            <History class="h-4 w-4" />
            {{ t('adminFiles.viewVersions') }}
          </Button>
        </div>
        <p v-if="error" class="text-sm text-rose-600 dark:text-rose-400">{{ error }}</p>
        <p class="text-xs text-zinc-500 dark:text-zinc-400">
          {{ t('adminFiles.hint') }}
        </p>
      </div>
    </div>

    <EmptyState
      :icon="Search"
      :title="t('adminFiles.helperTitle')"
      :description="t('adminFiles.helperDescription')"
    />
  </section>
</template>
