<script setup lang="ts">
// koru:k3 — Protection settings page (trash retention, version policy,
// antivirus status). Follows the Settings.vue card/form pattern.
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import axios from 'axios';
import { Save, Shield, ShieldAlert, History, Trash2, ExternalLink } from 'lucide-vue-next';

import { ProtectionApi, type ProtectionAntivirus } from '@/api/protection';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';

const DOCS_URL = 'https://github.com/BRF-Tech/filex/blob/main/docs/PROTECTION.md';

const { t } = useI18n();
const toast = useToastStore();

const loading = ref(true);
// Older backends don't expose /admin/protection yet — show a calm
// "server doesn't support this" band instead of a scary error.
const unsupported = ref(false);
const loadError = ref<string | null>(null);

const trashDays = ref<number>(30);
const versionsKeepN = ref<number>(0);
const antivirus = ref<ProtectionAntivirus | null>(null);

const savingTrash = ref(false);
const savingVersions = ref(false);
const errTrash = ref<string | null>(null);
const errVersions = ref<string | null>(null);

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
    const s = await ProtectionApi.get();
    trashDays.value = s.trash_retention_days;
    versionsKeepN.value = s.versions_keep_n;
    antivirus.value = s.antivirus ?? { enabled: false, binary: '' };
  } catch (e: unknown) {
    if (isMissingEndpoint(e)) {
      unsupported.value = true;
    } else {
      loadError.value = extractError(e, t('errors.generic'));
    }
  } finally {
    loading.value = false;
  }
}

onMounted(load);

function validInt(v: unknown, min: number): boolean {
  return typeof v === 'number' && Number.isFinite(v) && Number.isInteger(v) && v >= min;
}

async function saveTrash() {
  errTrash.value = null;
  // Backend floor: trash.retention_days <= 0 silently falls back to the
  // default (30), so reject anything below 1 up front.
  if (!validInt(trashDays.value, 1)) {
    errTrash.value = t('protection.trash.errMin');
    return;
  }
  savingTrash.value = true;
  try {
    const s = await ProtectionApi.update({ trash_retention_days: trashDays.value });
    trashDays.value = s.trash_retention_days;
    toast.success(t('protection.savedOk'));
  } catch (e: unknown) {
    errTrash.value = extractError(e, t('errors.generic'));
  } finally {
    savingTrash.value = false;
  }
}

async function saveVersions() {
  errVersions.value = null;
  if (!validInt(versionsKeepN.value, 0)) {
    errVersions.value = t('protection.versions.errMin');
    return;
  }
  savingVersions.value = true;
  try {
    const s = await ProtectionApi.update({ versions_keep_n: versionsKeepN.value });
    versionsKeepN.value = s.versions_keep_n;
    toast.success(t('protection.savedOk'));
  } catch (e: unknown) {
    errVersions.value = extractError(e, t('errors.generic'));
  } finally {
    savingVersions.value = false;
  }
}
</script>

<template>
  <div class="space-y-4 max-w-2xl">
    <div class="flex items-center gap-2">
      <Shield class="h-6 w-6 text-brand-600 dark:text-brand-400" />
      <div>
        <h1 class="text-xl font-semibold">{{ t('protection.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('protection.subtitle') }}</p>
      </div>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <!-- Backend predates the protection API -->
    <div
      v-else-if="unsupported"
      class="flex items-start gap-3 rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
    >
      <ShieldAlert class="mt-0.5 h-5 w-5 shrink-0" />
      <p>{{ t('protection.unsupported') }}</p>
    </div>

    <div
      v-else-if="loadError"
      class="space-y-3 rounded-xl border border-rose-300 bg-rose-50 p-4 text-sm text-rose-700 dark:border-rose-700/60 dark:bg-rose-900/20 dark:text-rose-200"
    >
      <p>{{ loadError }}</p>
      <Button size="sm" variant="outline" @click="load">{{ t('common.refresh') }}</Button>
    </div>

    <template v-else>
      <!-- Trash retention. `novalidate` so our under-field i18n errors
           render instead of the browser's native min/step bubbles. -->
      <form class="card card-body space-y-3" novalidate @submit.prevent="saveTrash">
        <h2 class="flex items-center gap-2 text-base font-semibold">
          <Trash2 class="h-4 w-4" /> {{ t('protection.trash.title') }}
        </h2>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('protection.trash.desc') }}</p>
        <Input
          :model-value="trashDays"
          type="number"
          :min="1"
          :step="1"
          :label="t('protection.trash.label')"
          :hint="t('protection.trash.hint')"
          :error="errTrash"
          class="max-w-xs"
          @update:model-value="(v) => ((trashDays = v as number), (errTrash = null))"
        />
        <div class="flex justify-end pt-1">
          <Button type="submit" :loading="savingTrash">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <!-- Version policy -->
      <form class="card card-body space-y-3" novalidate @submit.prevent="saveVersions">
        <h2 class="flex items-center gap-2 text-base font-semibold">
          <History class="h-4 w-4" /> {{ t('protection.versions.title') }}
        </h2>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('protection.versions.desc') }}</p>
        <Input
          :model-value="versionsKeepN"
          type="number"
          :min="0"
          :step="1"
          :label="t('protection.versions.label')"
          :hint="t('protection.versions.hint')"
          :error="errVersions"
          class="max-w-xs"
          @update:model-value="(v) => ((versionsKeepN = v as number), (errVersions = null))"
        />
        <div class="flex justify-end pt-1">
          <Button type="submit" :loading="savingVersions">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <!-- Antivirus (read-only status) -->
      <div class="card card-body space-y-3">
        <div class="flex items-center justify-between gap-2">
          <h2 class="flex items-center gap-2 text-base font-semibold">
            <Shield class="h-4 w-4" /> {{ t('protection.av.title') }}
          </h2>
          <Badge :tone="antivirus?.enabled ? 'emerald' : 'zinc'">
            {{ antivirus?.enabled ? t('common.enabled') : t('common.disabled') }}
          </Badge>
        </div>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('protection.av.desc') }}</p>
        <div v-if="antivirus?.enabled" class="text-sm">
          <span class="text-zinc-500 dark:text-zinc-400">{{ t('protection.av.binary') }}:</span>
          <code class="ml-2 rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-xs dark:bg-zinc-800">
            {{ antivirus.binary || '—' }}
          </code>
        </div>
        <p v-else class="text-sm text-zinc-600 dark:text-zinc-300">
          {{ t('protection.av.setupHint') }}
          <a
            :href="DOCS_URL"
            target="_blank"
            rel="noopener"
            class="inline-flex items-center gap-1 text-brand-600 hover:underline dark:text-brand-400"
          >
            docs/PROTECTION.md
            <ExternalLink class="h-3.5 w-3.5" />
          </a>
        </p>
      </div>
    </template>
  </div>
</template>
