<script setup lang="ts">
// Release awareness page (docs/UPDATES.md).
//
// The whole point of this screen is that an operator should never have to ask
// "am I current, and if not, what happens next?". It answers three things at a
// glance: the running version, whether something newer exists, and who acts —
// filex itself (patch under an allowing policy), one click here, or a manual
// upgrade with the commands spelled out.
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import axios from 'axios';
import {
  ArrowUpCircle, CheckCircle2, RefreshCw, ShieldAlert, Terminal, Copy, AlertTriangle,
} from 'lucide-vue-next';

import { UpdatesApi, type UpdateStatus } from '@/api/updates';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t } = useI18n();
const toast = useToastStore();

const loading = ref(true);
const checking = ref(false);
const applying = ref(false);
// A backend older than this feature has no /admin/update; say so calmly
// instead of rendering an error.
const unsupported = ref(false);
const loadError = ref<string | null>(null);
const status = ref<UpdateStatus | null>(null);

function isMissingEndpoint(e: unknown): boolean {
  if (!axios.isAxiosError(e)) return false;
  const s = e.response?.status;
  return s === 404 || s === 405 || s === 501;
}

async function load() {
  loading.value = true;
  loadError.value = null;
  unsupported.value = false;
  try {
    status.value = await UpdatesApi.status();
  } catch (e: unknown) {
    if (isMissingEndpoint(e)) unsupported.value = true;
    else loadError.value = extractError(e, t('errors.generic'));
  } finally {
    loading.value = false;
  }
}
onMounted(load);

async function checkNow() {
  checking.value = true;
  try {
    status.value = await UpdatesApi.check();
    if (status.value?.check_error) toast.error(status.value.check_error);
    else if (status.value?.action === 'none') toast.success(t('updates.upToDate'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    checking.value = false;
  }
}

async function applyNow() {
  applying.value = true;
  try {
    const res = await UpdatesApi.apply();
    if (res.error) toast.error(res.error);
    else if (res.applied) toast.success(t('updates.applied', { version: res.applied }));
    else if (res.message) toast.success(res.message);
    status.value = await UpdatesApi.status();
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    applying.value = false;
  }
}

async function copyInstructions() {
  const lines = status.value?.instructions ?? [];
  try {
    await navigator.clipboard.writeText(lines.join('\n'));
    toast.success(t('common.copied'));
  } catch {
    toast.error(t('errors.generic'));
  }
}

const s = computed(() => status.value);
const hasUpdate = computed(() => !!s.value && s.value.action !== 'none');
/** Only ActionConfirm gets a button — auto needs no prompt and instruct cannot
 *  be performed from here. */
const canApplyHere = computed(() => s.value?.action === 'confirm' && s.value?.can_self_apply);
const stepVariant = computed(() => {
  switch (s.value?.step) {
    case 'major': return 'warning';
    case 'minor': return 'info';
    default: return 'success';
  }
});
</script>

<template>
  <div class="space-y-4 max-w-2xl">
    <div class="flex items-center gap-2">
      <ArrowUpCircle class="h-6 w-6 text-brand-600 dark:text-brand-400" />
      <div>
        <h1 class="text-xl font-semibold">{{ t('updates.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('updates.subtitle') }}</p>
      </div>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div
      v-else-if="unsupported"
      class="flex items-start gap-3 rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
    >
      <ShieldAlert class="mt-0.5 h-5 w-5 shrink-0" />
      <p>{{ t('updates.unsupported') }}</p>
    </div>

    <div
      v-else-if="loadError"
      class="space-y-3 rounded-xl border border-rose-300 bg-rose-50 p-4 text-sm text-rose-700 dark:border-rose-700/60 dark:bg-rose-900/20 dark:text-rose-200"
    >
      <p>{{ loadError }}</p>
      <Button size="sm" variant="outline" @click="load">{{ t('common.refresh') }}</Button>
    </div>

    <template v-else-if="s">
      <!-- A binary was installed but this process is still the old one. This
           state is invisible everywhere else, so it leads. -->
      <div
        v-if="s.restart_required"
        class="flex items-start gap-3 rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
      >
        <AlertTriangle class="mt-0.5 h-5 w-5 shrink-0" />
        <p>{{ t('updates.restartRequired', { version: s.last_applied }) }}</p>
      </div>

      <div class="card card-body space-y-3">
        <div class="flex items-start justify-between gap-3">
          <div>
            <div class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('updates.current') }}</div>
            <div class="text-lg font-semibold">{{ s.current }}</div>
          </div>
          <div class="flex flex-wrap items-center justify-end gap-2">
            <Badge variant="default">{{ t('updates.mode.' + (s.mode === 'docker' ? 'docker' : 'binary')) }}</Badge>
            <Badge variant="default">{{ t('updates.policyLabel') }}: {{ s.policy }}</Badge>
          </div>
        </div>

        <!-- Up to date -->
        <div v-if="!hasUpdate" class="flex items-center gap-2 text-sm text-emerald-700 dark:text-emerald-300">
          <CheckCircle2 class="h-4 w-4" /> {{ t('updates.upToDate') }}
        </div>

        <!-- Something newer exists -->
        <div v-else class="space-y-3">
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('updates.available') }}</span>
            <span class="text-lg font-semibold">{{ s.latest?.version }}</span>
            <Badge :variant="stepVariant">{{ t('updates.step.' + s.step) }}</Badge>
            <Badge v-if="s.latest?.security" variant="danger">{{ t('updates.security') }}</Badge>
            <Badge v-if="s.latest?.migrations" variant="warning">{{ t('updates.migrations') }}</Badge>
          </div>

          <p v-if="s.reason" class="text-sm text-zinc-600 dark:text-zinc-300">{{ s.reason }}</p>

          <a
            v-if="s.latest?.notes_url"
            :href="s.latest.notes_url"
            target="_blank"
            rel="noopener"
            class="inline-block text-sm text-brand-600 hover:underline dark:text-brand-400"
          >{{ t('updates.releaseNotes') }}</a>

          <!-- Everything being skipped over, so a multi-version jump is never
               a surprise. -->
          <div v-if="s.skipped?.length" class="rounded-lg bg-zinc-50 p-3 text-sm dark:bg-zinc-800/50">
            <div class="mb-1 font-medium">{{ t('updates.includes') }}</div>
            <ul class="space-y-1">
              <li v-for="r in s.skipped" :key="r.version" class="flex flex-wrap items-center gap-2">
                <span class="font-mono text-xs">{{ r.version }}</span>
                <Badge v-if="r.migrations" variant="warning">{{ t('updates.migrations') }}</Badge>
                <Badge v-if="r.security" variant="danger">{{ t('updates.security') }}</Badge>
                <span class="text-zinc-500 dark:text-zinc-400">{{ r.notes }}</span>
              </li>
            </ul>
          </div>

          <div class="flex flex-wrap gap-2">
            <Button v-if="canApplyHere" :disabled="applying" @click="applyNow">
              <Spinner v-if="applying" class="mr-2 h-4 w-4" />
              {{ t('updates.applyNow') }}
            </Button>
            <span v-else-if="s.action === 'auto'" class="text-sm text-zinc-500 dark:text-zinc-400">
              {{ t('updates.willAutoApply') }}
            </span>
          </div>

          <!-- Manual path: the commands for THIS install shape, generated by
               the server (only it knows how filex was installed). -->
          <div v-if="s.instructions?.length" class="space-y-2">
            <div class="flex items-center justify-between">
              <div class="flex items-center gap-2 text-sm font-medium">
                <Terminal class="h-4 w-4" /> {{ t('updates.howTo') }}
              </div>
              <Button size="sm" variant="outline" @click="copyInstructions">
                <Copy class="mr-1 h-3 w-3" /> {{ t('common.copy') }}
              </Button>
            </div>
            <pre class="overflow-x-auto rounded-lg bg-zinc-900 p-3 text-xs text-zinc-100"><code>{{ s.instructions.join('\n') }}</code></pre>
          </div>
        </div>

        <div class="flex items-center justify-between border-t border-zinc-200 pt-3 text-xs text-zinc-500 dark:border-zinc-700 dark:text-zinc-400">
          <span v-if="s.checked_at">{{ t('updates.checkedAt') }}: {{ s.checked_at }}</span>
          <span v-else-if="!s.enabled">{{ t('updates.disabled') }}</span>
          <span v-else>—</span>
          <Button size="sm" variant="outline" :disabled="checking" @click="checkNow">
            <RefreshCw class="mr-1 h-3 w-3" :class="checking ? 'animate-spin' : ''" />
            {{ t('updates.checkNow') }}
          </Button>
        </div>

        <p v-if="s.check_error" class="text-xs text-amber-600 dark:text-amber-400">
          {{ t('updates.checkFailed') }}: {{ s.check_error }}
        </p>
        <p v-if="s.last_apply_error" class="text-xs text-rose-600 dark:text-rose-400">
          {{ t('updates.applyFailed') }}: {{ s.last_apply_error }}
        </p>
      </div>
    </template>
  </div>
</template>
