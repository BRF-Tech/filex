<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, Activity } from 'lucide-vue-next';

import { AuthProvidersApi } from '@/api/auth-providers';
import type { AuthProvider } from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';

import Button from '@/components/ui/Button.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Textarea from '@/components/ui/Textarea.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t } = useI18n();
const toast = useToastStore();

const items = ref<AuthProvider[]>([]);
const loading = ref(false);
const drafts = reactive<Record<string, { enabled: boolean; configJson: string }>>({});
const savingId = ref<string | null>(null);
const testingId = ref<string | null>(null);

function ensureDraft(p: AuthProvider) {
  if (!drafts[p.id]) {
    drafts[p.id] = {
      enabled: p.enabled,
      configJson: JSON.stringify(p.config_redacted ?? p.config ?? {}, null, 2),
    };
  }
  return drafts[p.id];
}

async function load() {
  loading.value = true;
  try {
    items.value = await AuthProvidersApi.list();
    for (const p of items.value) ensureDraft(p);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

async function save(p: AuthProvider) {
  savingId.value = p.id;
  try {
    const d = ensureDraft(p);
    let cfg: Record<string, unknown> | undefined = undefined;
    try {
      cfg = d.configJson ? JSON.parse(d.configJson) : undefined;
    } catch (err) {
      toast.error(`Invalid JSON: ${(err as Error).message}`);
      return;
    }
    const updated = await AuthProvidersApi.update(p.id, {
      enabled: d.enabled,
      config: cfg,
    });
    items.value = items.value.map((x) => (x.id === updated.id ? updated : x));
    drafts[p.id].configJson = JSON.stringify(updated.config_redacted ?? updated.config ?? {}, null, 2);
    toast.success(t('authProviders.savedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingId.value = null;
  }
}

async function test(p: AuthProvider) {
  testingId.value = p.id;
  try {
    const res = await AuthProvidersApi.test(p.id);
    if (res.ok) toast.success(t('authProviders.testOk'));
    else toast.warn(res.error ?? t('authProviders.testFail'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    testingId.value = null;
  }
}

const stateTone = (s: AuthProvider['status']) => {
  if (s === 'ok') return 'emerald';
  if (s === 'misconfigured') return 'rose';
  return 'zinc';
};

onMounted(load);
</script>

<template>
  <div class="space-y-4 max-w-3xl">
    <div>
      <h1 class="text-xl font-semibold">{{ t('authProviders.title') }}</h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('authProviders.subtitle') }}</p>
    </div>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div v-else class="space-y-3">
      <div v-for="p in items" :key="p.id" class="card card-body space-y-3">
        <div class="flex items-start justify-between gap-3">
          <div>
            <h2 class="text-sm font-semibold flex items-center gap-2">
              {{ t(`authProviders.providers.${p.id}` as any) }}
              <Badge :tone="stateTone(p.status)" dot>{{ p.status }}</Badge>
            </h2>
            <p v-if="p.last_error" class="text-xs text-rose-600 mt-1 font-mono">
              {{ p.last_error }}
            </p>
          </div>
          <Toggle v-model="ensureDraft(p).enabled" :label="t('common.enabled')" />
        </div>

        <Textarea
          v-model="ensureDraft(p).configJson"
          :rows="8"
          :label="t('common.details')"
          monospace
        />

        <div class="flex items-center justify-between pt-1 gap-2">
          <Button
            variant="outline"
            size="sm"
            :loading="testingId === p.id"
            @click="test(p)"
          >
            <Activity class="h-4 w-4" />
            {{ t('common.testNow') }}
          </Button>
          <Button size="sm" :loading="savingId === p.id" @click="save(p)">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>
