<script setup lang="ts">
/**
 * AppEraseDialog — the one question asked before an app's data is erased:
 * by Reconfigure (the data goes, the setup wizard opens again) and by
 * removing the app (the data goes with it). docs/APP-PLUGINS.md → Setting an
 * app up.
 *
 * ⚠⚠ The owner's rule (2026-11-11): erasing an app's data is said with what
 * it erases, COUNTED, in the server's numbers (`GET …/erase`), beside the
 * app's own warning, and is confirmed by TYPING the app's name. The button
 * stays disabled until the name matches exactly; a click is not consent to
 * deleting a few thousand short links.
 */
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { AlertTriangle } from 'lucide-vue-next';

import { AppPluginsApi, type AppEraseAnswer, type AppPlugin } from '@/api/appPlugins';
import { extractError } from '@/api/client';
import { formatBytes, formatNumber } from '@/lib/format';
import { pluginLabelOf } from '@brftech/filex-core';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Modal from '@/components/ui/Modal.vue';
import Spinner from '@/components/ui/Spinner.vue';

const props = defineProps<{
  modelValue: boolean;
  plugin: AppPlugin | null;
  mode: 'reconfigure' | 'remove';
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void;
  /** Reconfigure: the app, back in `setup`. Remove: null. */
  (e: 'done', plugin: AppPlugin | null): void;
}>();

const { t, locale } = useI18n();

const answer = ref<AppEraseAnswer | null>(null);
const loading = ref(false);
const busy = ref(false);
const error = ref('');
const typed = ref('');

const label = computed(() => (props.plugin ? pluginLabelOf(props.plugin.label, locale.value) || props.plugin.name : ''));
/** What the administrator types: the server's word, else the app's name. */
const expected = computed(() => answer.value?.confirm || props.plugin?.name || '');
const matches = computed(() => typed.value === expected.value && expected.value !== '');

/** The lines of what goes, only the ones with something in them. */
const lines = computed(() => {
  const c = answer.value?.counts;
  if (!c) return [] as { key: string; text: string }[];
  const n = (v: number) => formatNumber(v, locale.value);
  const out: { key: string; text: string }[] = [];
  if (c.data_keys > 0) {
    out.push({ key: 'data', text: t('appPlugins.erase.data', { n: n(c.data_keys), size: formatBytes(c.data_bytes, locale.value) }, c.data_keys) });
  }
  if (c.links > 0) out.push({ key: 'links', text: t('appPlugins.erase.links', { n: n(c.links) }, c.links) });
  if (c.state_rows > 0) out.push({ key: 'state', text: t('appPlugins.erase.state', { n: n(c.state_rows) }, c.state_rows) });
  if (c.locks > 0) out.push({ key: 'locks', text: t('appPlugins.erase.locks', { n: n(c.locks) }, c.locks) });
  return out;
});
const warning = computed(() => (answer.value?.warning ? pluginLabelOf(answer.value.warning, locale.value) : ''));

async function load() {
  const p = props.plugin;
  if (!p) return;
  loading.value = true;
  error.value = '';
  answer.value = null;
  typed.value = '';
  try {
    answer.value = await AppPluginsApi.erase(p.id);
  } catch (e: unknown) {
    error.value = extractError(e, t('errors.loadFailed'));
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.modelValue, props.plugin?.id] as const,
  ([open]) => {
    if (open) void load();
  },
  { immediate: true },
);

function close() {
  if (busy.value) return;
  emit('update:modelValue', false);
}

async function confirm() {
  const p = props.plugin;
  if (!p || !matches.value || busy.value) return;
  busy.value = true;
  error.value = '';
  try {
    if (props.mode === 'reconfigure') {
      const next = await AppPluginsApi.reconfigure(p.id, typed.value);
      emit('done', next);
    } else {
      await AppPluginsApi.remove(p.id, typed.value);
      emit('done', null);
    }
    emit('update:modelValue', false);
  } catch (e: unknown) {
    error.value = extractError(e, t(props.mode === 'reconfigure' ? 'errors.updateFailed' : 'errors.deleteFailed'));
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <Modal
    :model-value="modelValue"
    :title="t(mode === 'reconfigure' ? 'appPlugins.erase.titleReconfigure' : 'appPlugins.erase.titleRemove', { name: label })"
    size="md"
    :prevent-close="busy"
    @update:model-value="(v: boolean) => !v && close()"
  >
    <div class="space-y-4 text-sm" data-testid="app-erase">
      <div v-if="loading" class="text-center text-zinc-500"><Spinner /></div>
      <template v-else-if="answer">
        <div
          class="flex gap-2 rounded-lg border border-rose-200 bg-rose-50 p-3 text-rose-900 dark:border-rose-900/50 dark:bg-rose-950/30 dark:text-rose-200"
          role="alert"
        >
          <AlertTriangle class="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <div class="space-y-1">
            <p class="font-medium">
              {{ t(mode === 'reconfigure' ? 'appPlugins.erase.leadReconfigure' : 'appPlugins.erase.leadRemove', { name: label }) }}
            </p>
            <p>{{ t('appPlugins.erase.final') }}</p>
          </div>
        </div>
        <ul v-if="lines.length" class="list-disc space-y-1 ps-5" data-testid="app-erase-counts">
          <li v-for="l in lines" :key="l.key" :data-testid="`app-erase-${l.key}`">{{ l.text }}</li>
        </ul>
        <p v-else class="text-zinc-600 dark:text-zinc-400" data-testid="app-erase-nothing">{{ t('appPlugins.erase.nothing') }}</p>
        <p v-if="warning" class="text-zinc-700 dark:text-zinc-300" data-testid="app-erase-warning">
          <bdi>{{ warning }}</bdi>
        </p>
        <p v-if="mode === 'reconfigure'" class="text-zinc-600 dark:text-zinc-400">{{ t('appPlugins.erase.thenWizard') }}</p>
        <Input
          v-model="typed"
          name="app-erase-confirm"
          autocomplete="off"
          monospace
          :label="t('appPlugins.erase.typeLabel', { name: expected })"
          :placeholder="expected"
          data-testid="app-erase-type"
          @enter="confirm"
        />
      </template>
      <p v-if="error" class="text-rose-700 dark:text-rose-300" role="alert" data-testid="app-erase-error">{{ error }}</p>
    </div>
    <template #footer>
      <Button variant="ghost" size="sm" :disabled="busy" data-testid="app-erase-cancel" @click="close">
        {{ t('common.cancel') }}
      </Button>
      <Button
        variant="danger"
        size="sm"
        :disabled="!matches || !answer"
        :loading="busy"
        data-testid="app-erase-confirm"
        @click="confirm"
      >
        {{ t(mode === 'reconfigure' ? 'appPlugins.erase.doReconfigure' : 'appPlugins.erase.doRemove') }}
      </Button>
    </template>
  </Modal>
</template>
