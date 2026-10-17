<script setup lang="ts">
/**
 * Admin → Settings → AI assistant (task #269): the provider, its address, the
 * key and the model, a connection test, the switches (on, hide the button),
 * the budgets and how long conversations are kept.
 *
 * ⚠ The API key is written, never read: the page shows whether one is stored
 * (`has_key`) and sends a new one only when somebody typed it. Who may use the
 * assistant is the ai.assistant permission, set on Roles - the page says so
 * and links there rather than keeping a second list.
 */
import { computed, onMounted, ref } from 'vue';
import { CheckCircle2, KeyRound, Save, Sparkles, TestTube2, XCircle } from 'lucide-vue-next';
import { useI18n } from 'vue-i18n';
import { ChoiceButtons, type ChoiceOption } from '@brftech/filex-core';
import { AssistantAdminApi, type AssistantSettings, type AssistantTestResult } from '@/api/assistant';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Input from '@/components/ui/Input.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Toggle from '@/components/ui/Toggle.vue';

const { t } = useI18n();
const toast = useToastStore();
const loading = ref(true);
const saving = ref(false);
const testing = ref(false);
const error = ref('');
const settings = ref<AssistantSettings | null>(null);
/** A key typed on the page; empty = keep the one stored. */
const apiKey = ref('');
const clearKey = ref(false);
const result = ref<AssistantTestResult | null>(null);

/* The provider's name as its maker writes it - a name, not a sentence. */
const providerNames: Record<string, string> = { anthropic: 'Anthropic', openai: 'OpenAI', ollama: 'Ollama' };
const providerOptions = computed<ChoiceOption[]>(() =>
  (settings.value?.providers ?? []).map((p) => ({
    value: p.kind,
    label: providerNames[p.kind] ?? p.kind,
    help: t(`assistantAdmin.providerHelp.${p.kind}`),
  })),
);
const current = computed(() => settings.value?.providers.find((p) => p.kind === settings.value?.provider) ?? null);
/** The models a person can pick with one click: the provider's own list once
 *  a test has fetched it, the suggested ones before. */
const modelChoices = computed(() => {
  const listed = result.value?.models ?? [];
  if (listed.length) return listed.slice(0, 40);
  return current.value?.suggested_models ?? [];
});

async function load() {
  loading.value = true;
  error.value = '';
  try {
    settings.value = await AssistantAdminApi.get();
  } catch (err) {
    error.value = extractError(err, t('errors.generic'));
  } finally {
    loading.value = false;
  }
}

async function save() {
  const s = settings.value;
  if (!s) return;
  saving.value = true;
  error.value = '';
  try {
    settings.value = await AssistantAdminApi.update({
      enabled: s.enabled,
      hide_button: s.hide_button,
      provider: s.provider,
      base_url: s.base_url,
      model: s.model,
      max_tool_rounds: Number(s.max_tool_rounds),
      max_output_tokens: Number(s.max_output_tokens),
      max_request_tokens: Number(s.max_request_tokens),
      timeout_seconds: Number(s.timeout_seconds),
      requests_per_minute: Number(s.requests_per_minute),
      retention_days: Number(s.retention_days),
      ...(apiKey.value.trim() ? { api_key: apiKey.value.trim() } : {}),
      ...(clearKey.value ? { clear_api_key: true } : {}),
    });
    apiKey.value = '';
    clearKey.value = false;
    toast.success(t('assistantAdmin.saved'));
  } catch (err) {
    error.value = extractError(err, t('errors.generic'));
  } finally {
    saving.value = false;
  }
}

async function test() {
  const s = settings.value;
  if (!s) return;
  testing.value = true;
  error.value = '';
  result.value = null;
  try {
    result.value = await AssistantAdminApi.test({
      provider: s.provider,
      base_url: s.base_url,
      model: s.model,
      ...(apiKey.value.trim() ? { api_key: apiKey.value.trim() } : {}),
    });
  } catch (err) {
    error.value = extractError(err, t('errors.generic'));
  } finally {
    testing.value = false;
  }
}

const stateClass = computed(() => {
  switch (settings.value?.state) {
    case 'on':
      return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300';
    case 'not_configured':
      return 'bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300';
  }
  return 'bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300';
});

function bound(field: string, i: 0 | 1): number | undefined {
  return settings.value?.bounds?.[field]?.[i];
}

onMounted(load);
</script>

<template>
  <div class="max-w-3xl space-y-4" data-testid="ai-assistant-admin">
    <div class="flex items-center gap-2">
      <Sparkles class="h-6 w-6 text-brand-600 dark:text-brand-400" />
      <div>
        <h1 class="text-xl font-semibold">{{ t('assistantAdmin.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('assistantAdmin.subtitle') }}</p>
      </div>
    </div>

    <div v-if="loading" class="flex justify-center py-16"><Spinner /></div>
    <div v-else-if="!settings" class="rounded-lg border border-rose-200 bg-rose-50 p-4 text-sm text-rose-700">{{ error }}</div>
    <template v-else>
      <div v-if="error" class="rounded-lg border border-rose-200 bg-rose-50 p-3 text-sm text-rose-700" data-testid="ai-assistant-error">{{ error }}</div>

      <!-- On, hidden, and what turning it on means. -->
      <section class="card card-body space-y-4">
        <div class="flex items-center justify-between gap-4">
          <h2 class="font-semibold text-zinc-900 dark:text-zinc-100">{{ t('assistantAdmin.status') }}</h2>
          <span class="rounded-full px-2.5 py-0.5 text-xs font-medium" :class="stateClass" data-testid="ai-assistant-state">
            {{ t(`assistantAdmin.state.${settings.state}`) }}
          </span>
        </div>
        <Toggle v-model="settings.enabled" :label="t('assistantAdmin.enabled')" :description="t('assistantAdmin.enabledHint')" />
        <Toggle v-model="settings.hide_button" :label="t('assistantAdmin.hideButton')" :description="t('assistantAdmin.hideButtonHint')" />
        <p class="text-sm text-zinc-500 dark:text-zinc-400">
          {{ t('assistantAdmin.whoMay') }}
          <RouterLink :to="{ name: 'roles' }" class="text-brand-600 hover:underline dark:text-brand-400">{{ t('assistantAdmin.whoMayLink') }}</RouterLink>
        </p>
      </section>

      <!-- The provider, its address, the key, the model - and the test. -->
      <section class="card card-body space-y-4">
        <h2 class="font-semibold text-zinc-900 dark:text-zinc-100">{{ t('assistantAdmin.provider') }}</h2>
        <ChoiceButtons
          :model-value="settings.provider"
          :options="providerOptions"
          :aria-label="t('assistantAdmin.provider')"
          testid-prefix="ai-assistant-provider"
          @update:model-value="(v: string | string[]) => settings && (settings.provider = v as AssistantSettings['provider'])"
        />
        <Input
          v-model="settings.base_url"
          name="ai-assistant-base-url"
          :label="t('assistantAdmin.baseUrl')"
          :placeholder="current?.default_base_url ?? ''"
          :hint="t('assistantAdmin.baseUrlHint')"
          monospace
        />
        <div class="space-y-2">
          <Input
            v-model="apiKey"
            name="ai-assistant-key"
            type="password"
            autocomplete="off"
            :label="t('assistantAdmin.apiKey')"
            :placeholder="settings.has_key ? t('assistantAdmin.apiKeyStored') : ''"
            :hint="settings.needs_key ? t('assistantAdmin.apiKeyNeeded') : t('assistantAdmin.apiKeyOptional')"
            :disabled="!settings.secret_key_configured"
          />
          <p v-if="!settings.secret_key_configured" class="text-xs text-amber-600" data-testid="ai-assistant-no-secret">
            <KeyRound class="inline h-3.5 w-3.5" /> {{ t('assistantAdmin.noSecretKey') }}
          </p>
          <Checkbox v-if="settings.has_key" v-model="clearKey" name="ai-assistant-clear-key" :label="t('assistantAdmin.clearKey')" />
        </div>
        <div class="space-y-2">
          <Input v-model="settings.model" name="ai-assistant-model" :label="t('assistantAdmin.model')" :hint="t('assistantAdmin.modelHint')" monospace />
          <div v-if="modelChoices.length" class="flex flex-wrap gap-1.5" data-testid="ai-assistant-models">
            <button
              v-for="m in modelChoices"
              :key="m"
              type="button"
              class="rounded-md border px-2 py-0.5 font-mono text-xs"
              :class="m === settings.model ? 'border-brand-500 bg-brand-50 text-brand-700 dark:bg-brand-950 dark:text-brand-300' : 'border-zinc-200 text-zinc-600 hover:bg-zinc-50 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-800'"
              @click="settings && (settings.model = m)"
            >
              {{ m }}
            </button>
          </div>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <Button variant="secondary" :loading="testing" data-testid="ai-assistant-test" @click="test">
            <TestTube2 class="h-4 w-4" />{{ t('assistantAdmin.test') }}
          </Button>
          <p v-if="result" class="flex items-center gap-1.5 text-sm" :class="result.ok ? 'text-emerald-600' : 'text-rose-600'" data-testid="ai-assistant-test-result">
            <CheckCircle2 v-if="result.ok" class="h-4 w-4" />
            <XCircle v-else class="h-4 w-4" />
            {{ result.message }}
          </p>
        </div>
        <p v-if="result && !result.ok && result.detail" class="font-mono text-xs text-zinc-500">{{ result.detail }}</p>
      </section>

      <!-- Budgets and keeping. -->
      <section class="card card-body space-y-4">
        <h2 class="font-semibold text-zinc-900 dark:text-zinc-100">{{ t('assistantAdmin.limits') }}</h2>
        <div class="grid gap-4 sm:grid-cols-2">
          <Input v-model="settings.max_tool_rounds" type="number" name="ai-assistant-rounds" :min="bound('max_tool_rounds', 0)" :max="bound('max_tool_rounds', 1)" :label="t('assistantAdmin.maxToolRounds')" :hint="t('assistantAdmin.maxToolRoundsHint')" />
          <Input v-model="settings.max_output_tokens" type="number" name="ai-assistant-output" :min="bound('max_output_tokens', 0)" :max="bound('max_output_tokens', 1)" :label="t('assistantAdmin.maxOutputTokens')" />
          <Input v-model="settings.max_request_tokens" type="number" name="ai-assistant-budget" :min="bound('max_request_tokens', 0)" :max="bound('max_request_tokens', 1)" :label="t('assistantAdmin.maxRequestTokens')" :hint="t('assistantAdmin.maxRequestTokensHint')" />
          <Input v-model="settings.timeout_seconds" type="number" name="ai-assistant-timeout" :min="bound('timeout_seconds', 0)" :max="bound('timeout_seconds', 1)" :label="t('assistantAdmin.timeout')" />
          <Input v-model="settings.requests_per_minute" type="number" name="ai-assistant-rpm" :min="bound('requests_per_minute', 0)" :max="bound('requests_per_minute', 1)" :label="t('assistantAdmin.perMinute')" />
          <Input v-model="settings.retention_days" type="number" name="ai-assistant-retention" :min="bound('retention_days', 0)" :max="bound('retention_days', 1)" :label="t('assistantAdmin.retention')" :hint="t('assistantAdmin.retentionHint')" />
        </div>
      </section>

      <div class="flex justify-end">
        <Button :loading="saving" data-testid="ai-assistant-save" @click="save"><Save class="h-4 w-4" />{{ t('common.save') }}</Button>
      </div>
    </template>
  </div>
</template>
