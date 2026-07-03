<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ExternalLink, Save, Activity } from 'lucide-vue-next';

import { useExternalServicesStore } from '@/stores/external';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';
import type { ExternalService } from '@/api/types';
import { formatRelative } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t, locale } = useI18n();
const ext = useExternalServicesStore();
const toast = useToastStore();

interface Draft {
  url: string;
  jwt_secret: string;
  enabled: boolean;
}

const drafts = reactive<Record<string, Draft>>({});
const testingId = ref<string | null>(null);
const savingId = ref<string | null>(null);

function ensureDraft(s: ExternalService): Draft {
  if (!drafts[s.id]) {
    drafts[s.id] = {
      url: s.url ?? '',
      jwt_secret: '',
      enabled: s.enabled,
    };
  }
  return drafts[s.id];
}

async function load() {
  await ext.fetch();
  for (const s of ext.items) ensureDraft(s);
}

async function save(s: ExternalService) {
  const d = ensureDraft(s);
  savingId.value = s.id;
  try {
    await ext.update(s.id, {
      url: d.url || null,
      enabled: d.enabled,
      jwt_secret: d.jwt_secret || undefined,
    });
    d.jwt_secret = ''; // never echo back
    toast.success(t('external.savedOk'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    savingId.value = null;
  }
}

async function test(s: ExternalService) {
  testingId.value = s.id;
  try {
    const updated = await ext.test(s.id);
    if (updated.last_state === 'healthy') {
      toast.success(t('external.testOk'));
    } else {
      toast.warn(updated.last_error || t('external.testFail'));
    }
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    testingId.value = null;
  }
}

const stateTone = (s: ExternalService['last_state']) => {
  switch (s) {
    case 'healthy':
      return 'emerald';
    case 'configured-unreachable':
      return 'amber';
    case 'unconfigured':
      return 'zinc';
    default:
      return 'zinc';
  }
};

onMounted(load);
</script>

<template>
  <div class="space-y-4 max-w-3xl">
    <div>
      <h1 class="text-xl font-semibold">{{ t('external.title') }}</h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('external.subtitle') }}</p>
    </div>

    <div v-if="ext.loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div v-else class="space-y-3">
      <div v-for="s in ext.items" :key="s.id" class="card card-body space-y-3">
        <div class="flex items-start justify-between gap-3">
          <div>
            <h2 class="text-sm font-semibold capitalize flex items-center gap-2">
              {{ s.id }}
              <Badge :tone="stateTone(s.last_state)" dot>
                {{ t(`external.states.${s.last_state}` as any) }}
              </Badge>
            </h2>
            <p
              v-if="s.last_checked_at && !s.last_checked_at.startsWith('0001-01-01') && !s.last_checked_at.startsWith('0000')"
              class="text-xs text-zinc-500 mt-0.5"
            >
              {{ formatRelative(s.last_checked_at, locale) }}
            </p>
            <p v-if="s.last_error" class="text-xs text-rose-600 dark:text-rose-400 font-mono mt-1">
              {{ s.last_error }}
            </p>
          </div>
          <a
            v-if="ensureDraft(s).url"
            :href="ensureDraft(s).url"
            target="_blank"
            rel="noopener"
            class="text-zinc-500 hover:text-brand-600 dark:hover:text-brand-400"
          >
            <ExternalLink class="h-4 w-4" />
          </a>
        </div>

        <Input
          v-model="ensureDraft(s).url"
          :label="t('external.fields.url')"
          monospace
          placeholder="https://example.com"
        />
        <Input
          v-model="ensureDraft(s).jwt_secret"
          type="password"
          :label="t('external.fields.jwtSecret')"
          autocomplete="off"
          monospace
          :hint="s.jwt_secret_set ? t('external.fields.jwtSecretHint') : undefined"
        />
        <Toggle v-model="ensureDraft(s).enabled" :label="t('common.enabled')" />

        <div class="flex items-center justify-between gap-2 pt-1">
          <Button
            type="button"
            variant="outline"
            size="sm"
            :loading="testingId === s.id"
            @click="test(s)"
          >
            <Activity class="h-4 w-4" />
            {{ t('common.testNow') }}
          </Button>
          <Button size="sm" :loading="savingId === s.id" @click="save(s)">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>
