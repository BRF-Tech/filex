<script setup lang="ts">
/**
 * Admin → Settings → Security & data → Bot protection (#294): which provider
 * checks the anonymous forms (Cloudflare Turnstile, Google reCAPTCHA, or
 * none), its keys, which forms it guards, and what happens when the server
 * cannot reach it.
 *
 * ⚠ The panel outranks the environment (CAPTCHA_PROVIDER, CAPTCHA_SITE_KEY,
 * CAPTCHA_SECRET, CAPTCHA_FORMS): every field says where the value in force
 * came from, and one set here can be handed back to the environment. An
 * empty panel field reads the environment LIVE, not once at the first start.
 *
 * ⚠ The secret is written, never read (`has_secret`). The page sends only
 * the fields that differ from what is in force, so saving one change does not
 * copy the environment's other values into the panel.
 */
import { computed, onMounted, ref } from 'vue';
import { Bot, CheckCircle2, KeyRound, RotateCcw, Save, TestTube2, XCircle } from 'lucide-vue-next';
import { useI18n } from 'vue-i18n';
import { ChoiceButtons, type CaptchaForm, type CaptchaProvider, type ChoiceOption } from '@brftech/filex-core';
import {
  CaptchaAdminApi,
  type CaptchaAdminState,
  type CaptchaFallback,
  type CaptchaPatch,
  type CaptchaSource,
  type CaptchaTestResult,
} from '@/api/captcha';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import Button from '@/components/ui/Button.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Input from '@/components/ui/Input.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Toggle from '@/components/ui/Toggle.vue';

const { t, te } = useI18n();
const toast = useToastStore();

const loading = ref(true);
const saving = ref(false);
const testing = ref(false);
const error = ref('');
const state = ref<CaptchaAdminState | null>(null);
const result = ref<CaptchaTestResult | null>(null);

/* The page's draft: what is in force until somebody changes it. */
const provider = ref<CaptchaProvider>('none');
const siteKey = ref('');
/** A secret typed on the page; empty = keep the one in force. */
const secret = ref('');
const clearSecret = ref(false);
const forms = ref<CaptchaForm[]>([]);
const fallback = ref<CaptchaFallback>('local');
const timeoutMs = ref<number | string>(4000);

function adopt(s: CaptchaAdminState): void {
  state.value = s;
  provider.value = s.effective.provider;
  siteKey.value = s.effective.site_key ?? '';
  secret.value = '';
  clearSecret.value = false;
  forms.value = [...(s.effective.forms ?? [])];
  fallback.value = s.effective.fallback;
  timeoutMs.value = s.effective.verify_timeout_ms;
}

/* The provider's name as its maker writes it - a name, not a sentence. */
const PROVIDER_NAMES: Record<string, string> = { turnstile: 'Cloudflare Turnstile', recaptcha: 'Google reCAPTCHA' };
const providerOptions = computed<ChoiceOption[]>(() =>
  (state.value?.providers ?? ['none', 'turnstile', 'recaptcha']).map((p) => ({
    value: p,
    label: PROVIDER_NAMES[p] ?? t('botProtection.provider.none'),
    help: t(`botProtection.provider.help.${p}`),
  })),
);
const fallbackOptions = computed<ChoiceOption[]>(() => [
  { value: 'local', label: t('botProtection.fallback.local'), help: t('botProtection.fallback.localHelp') },
  { value: 'off', label: t('botProtection.fallback.off'), help: t('botProtection.fallback.offHelp') },
]);

const on = computed(() => provider.value !== 'none');

/** Where a value in force came from, said under its field. */
function sourceOf(field: keyof CaptchaAdminState['sources']): CaptchaSource | undefined {
  return state.value?.sources?.[field];
}
function sourceLine(field: keyof CaptchaAdminState['sources'], env: string): string {
  const s = sourceOf(field);
  if (s === 'db') return t('botProtection.source.db');
  if (s === 'env') return t('botProtection.source.env', { env });
  if (s === 'default') return t('botProtection.source.default');
  return '';
}

/** One sentence for why the setup cannot verify a token. */
const misconfigured = computed(() => {
  const m = state.value?.misconfigured ?? '';
  if (!m) return '';
  const key = `botProtection.mis.${m.replace(/-/g, '_')}`;
  return te(key) ? t(key) : t('botProtection.mis.other', { code: m });
});

const status = computed<'off' | 'on' | 'misconfigured' | 'fallback'>(() => {
  const s = state.value;
  if (!s || s.effective.provider === 'none') return 'off';
  if (s.misconfigured) return 'misconfigured';
  if (s.fallback_active) return 'fallback';
  return 'on';
});
const statusClass = computed(() => {
  switch (status.value) {
    case 'on':
      return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300';
    case 'misconfigured':
    case 'fallback':
      return 'bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300';
  }
  return 'bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300';
});

function formOn(f: CaptchaForm): boolean {
  return forms.value.includes(f);
}
function setForm(f: CaptchaForm, v: boolean): void {
  const all = state.value?.all_forms ?? [];
  const next = new Set(forms.value);
  if (v) next.add(f);
  else next.delete(f);
  // the server's order, so a save does not look like a change of order
  forms.value = all.filter((x) => next.has(x));
}

function sameList(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((x) => b.includes(x));
}

/** What changed against what is in force. */
function patchOf(s: CaptchaAdminState): CaptchaPatch {
  const eff = s.effective;
  const p: CaptchaPatch = {};
  if (provider.value !== eff.provider) p.provider = provider.value;
  if (siteKey.value.trim() !== (eff.site_key ?? '')) p.site_key = siteKey.value.trim();
  if (secret.value.trim()) p.secret = secret.value.trim();
  if (clearSecret.value) p.clear_secret = true;
  if (!sameList(forms.value, eff.forms ?? [])) p.forms = [...forms.value];
  if (fallback.value !== eff.fallback) p.fallback = fallback.value;
  if (Number(timeoutMs.value) !== eff.verify_timeout_ms) p.verify_timeout_ms = Number(timeoutMs.value);
  return p;
}

const dirty = computed(() => !!state.value && Object.keys(patchOf(state.value)).length > 0);

async function load(): Promise<void> {
  loading.value = true;
  error.value = '';
  try {
    adopt(await CaptchaAdminApi.get());
  } catch (err) {
    error.value = extractError(err, t('errors.generic'));
  } finally {
    loading.value = false;
  }
}

async function send(p: CaptchaPatch, saved: string): Promise<void> {
  saving.value = true;
  error.value = '';
  try {
    adopt(await CaptchaAdminApi.update(p));
    result.value = null;
    toast.success(saved);
  } catch (err) {
    error.value = extractError(err, t('errors.generic'));
  } finally {
    saving.value = false;
  }
}

async function save(): Promise<void> {
  if (!state.value) return;
  const p = patchOf(state.value);
  if (!Object.keys(p).length) return;
  await send(p, t('botProtection.saved'));
}

/** Hands one field back to the environment (or the default): the panel forgets it. */
async function handBack(field: 'provider' | 'forms' | 'fallback'): Promise<void> {
  const p: CaptchaPatch = field === 'forms' ? { clear_forms: true } : { [field]: '' };
  await send(p, t('botProtection.handedBack'));
}

async function test(): Promise<void> {
  testing.value = true;
  error.value = '';
  result.value = null;
  try {
    result.value = await CaptchaAdminApi.test({
      provider: on.value ? provider.value : undefined,
      site_key: siteKey.value.trim() || undefined,
      ...(secret.value.trim() ? { secret: secret.value.trim() } : {}),
    });
  } catch (err) {
    error.value = extractError(err, t('errors.generic'));
  } finally {
    testing.value = false;
  }
}

const testOk = computed(() => !!result.value && result.value.reachable && result.value.secret_ok && !result.value.misconfigured);

function bound(i: 0 | 1): number | undefined {
  return state.value?.bounds?.verify_timeout_ms?.[i];
}

onMounted(load);
</script>

<template>
  <div class="max-w-3xl space-y-4" data-testid="bot-protection">
    <div class="flex items-center gap-2">
      <Bot class="h-6 w-6 text-brand-600 dark:text-brand-400" />
      <div>
        <h1 class="text-xl font-semibold">{{ t('botProtection.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('botProtection.subtitle') }}</p>
      </div>
    </div>

    <div v-if="loading" class="flex justify-center py-16"><Spinner /></div>
    <div v-else-if="!state" class="rounded-lg border border-rose-200 bg-rose-50 p-4 text-sm text-rose-700">{{ error }}</div>
    <template v-else>
      <div v-if="error" class="rounded-lg border border-rose-200 bg-rose-50 p-3 text-sm text-rose-700" data-testid="bot-protection-error">
        {{ error }}
      </div>

      <!-- What is in force right now. -->
      <section class="card card-body space-y-3">
        <div class="flex items-center justify-between gap-4">
          <h2 class="font-semibold text-zinc-900 dark:text-zinc-100">{{ t('botProtection.status.title') }}</h2>
          <span class="rounded-full px-2.5 py-0.5 text-xs font-medium" :class="statusClass" data-testid="bot-protection-state" :data-state="status">
            {{ t(`botProtection.status.${status}`) }}
          </span>
        </div>
        <p class="text-sm text-zinc-600 dark:text-zinc-300">
          {{
            status === 'off'
              ? t('botProtection.status.offLine')
              : t('botProtection.status.onLine', {
                  provider: PROVIDER_NAMES[state.effective.provider] ?? state.effective.provider,
                  count: state.effective.forms?.length ?? 0,
                })
          }}
        </p>
        <p v-if="misconfigured" class="rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-950 dark:text-amber-200" data-testid="bot-protection-misconfigured">
          {{ misconfigured }}
        </p>
        <p v-else-if="state.fallback_active" class="text-sm text-amber-700 dark:text-amber-300">{{ t('botProtection.status.fallbackLine') }}</p>
        <p class="text-xs text-zinc-500 dark:text-zinc-400" data-testid="bot-protection-fallback-total">
          {{ t('botProtection.status.fallbackTotal', { count: state.fallback_total }) }}
        </p>
      </section>

      <!-- The provider, its two keys - and the test. -->
      <section class="card card-body space-y-4">
        <div class="flex items-center justify-between gap-3">
          <h2 class="font-semibold text-zinc-900 dark:text-zinc-100">{{ t('botProtection.provider.title') }}</h2>
          <button
            v-if="sourceOf('provider') === 'db'"
            type="button"
            class="inline-flex items-center gap-1 text-xs text-brand-600 hover:underline dark:text-brand-400"
            data-testid="bot-protection-handback-provider"
            :disabled="saving"
            @click="handBack('provider')"
          >
            <RotateCcw class="h-3.5 w-3.5" />{{ t('botProtection.handBack') }}
          </button>
        </div>
        <ChoiceButtons
          :model-value="provider"
          :options="providerOptions"
          :aria-label="t('botProtection.provider.title')"
          testid-prefix="bot-protection-provider"
          @update:model-value="(v: string | string[]) => (provider = v as CaptchaProvider)"
        />
        <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ sourceLine('provider', 'CAPTCHA_PROVIDER') }}</p>

        <template v-if="on">
          <div class="space-y-1">
            <Input
              v-model="siteKey"
              name="bot-protection-site-key"
              :label="t('botProtection.siteKey')"
              :hint="t('botProtection.siteKeyHint')"
              autocomplete="off"
              monospace
            />
            <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ sourceLine('site_key', 'CAPTCHA_SITE_KEY') }}</p>
          </div>
          <div class="space-y-2">
            <Input
              v-model="secret"
              name="bot-protection-secret"
              type="password"
              autocomplete="off"
              :label="t('botProtection.secret')"
              :placeholder="state.effective.has_secret ? t('botProtection.secretStored') : ''"
              :hint="t('botProtection.secretHint')"
              :disabled="!state.secret_key_configured"
            />
            <p v-if="!state.secret_key_configured" class="text-xs text-amber-600" data-testid="bot-protection-no-secret-key">
              <KeyRound class="inline h-3.5 w-3.5" /> {{ t('botProtection.noSecretKey') }}
            </p>
            <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ sourceLine('secret', 'CAPTCHA_SECRET') }}</p>
            <Checkbox v-if="state.stored.has_secret" v-model="clearSecret" name="bot-protection-clear-secret" :label="t('botProtection.clearSecret')" />
          </div>
          <div class="flex flex-wrap items-center gap-3">
            <Button variant="secondary" :loading="testing" data-testid="bot-protection-test" @click="test">
              <TestTube2 class="h-4 w-4" />{{ t('botProtection.test') }}
            </Button>
            <p
              v-if="result"
              class="flex items-center gap-1.5 text-sm"
              :class="testOk ? 'text-emerald-600' : 'text-rose-600'"
              data-testid="bot-protection-test-result"
              :data-ok="testOk"
            >
              <CheckCircle2 v-if="testOk" class="h-4 w-4" />
              <XCircle v-else class="h-4 w-4" />
              {{ result.message || (testOk ? t('botProtection.testOk') : t('botProtection.testFailed')) }}
            </p>
          </div>
          <p v-if="result && result.codes?.length" class="font-mono text-xs text-zinc-500">{{ result.codes.join(', ') }}</p>
        </template>
      </section>

      <!-- Which forms it guards. -->
      <section v-if="on" class="card card-body space-y-4">
        <div class="flex items-center justify-between gap-3">
          <h2 class="font-semibold text-zinc-900 dark:text-zinc-100">{{ t('botProtection.forms.title') }}</h2>
          <button
            v-if="sourceOf('forms') === 'db'"
            type="button"
            class="inline-flex items-center gap-1 text-xs text-brand-600 hover:underline dark:text-brand-400"
            :disabled="saving"
            @click="handBack('forms')"
          >
            <RotateCcw class="h-3.5 w-3.5" />{{ t('botProtection.handBack') }}
          </button>
        </div>
        <Toggle
          v-for="f in state.all_forms"
          :key="f"
          :model-value="formOn(f)"
          :name="`bot-protection-form-${f}`"
          :label="t(`botProtection.forms.${f}`)"
          :description="t(`botProtection.forms.${f}Hint`)"
          @update:model-value="(v: boolean) => setForm(f, v)"
        />
        <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ sourceLine('forms', 'CAPTCHA_FORMS') }}</p>
      </section>

      <!-- An outage: the provider cannot be reached. -->
      <section v-if="on" class="card card-body space-y-4">
        <div class="flex items-center justify-between gap-3">
          <h2 class="font-semibold text-zinc-900 dark:text-zinc-100">{{ t('botProtection.fallback.title') }}</h2>
          <button
            v-if="sourceOf('fallback') === 'db'"
            type="button"
            class="inline-flex items-center gap-1 text-xs text-brand-600 hover:underline dark:text-brand-400"
            :disabled="saving"
            @click="handBack('fallback')"
          >
            <RotateCcw class="h-3.5 w-3.5" />{{ t('botProtection.handBack') }}
          </button>
        </div>
        <p class="text-sm text-zinc-600 dark:text-zinc-300">{{ t('botProtection.fallback.lead') }}</p>
        <ChoiceButtons
          :model-value="fallback"
          :options="fallbackOptions"
          segmented
          :aria-label="t('botProtection.fallback.title')"
          testid-prefix="bot-protection-fallback"
          @update:model-value="(v: string | string[]) => (fallback = v as CaptchaFallback)"
        />
        <Input
          v-model="timeoutMs"
          type="number"
          name="bot-protection-timeout"
          :min="bound(0)"
          :max="bound(1)"
          step="500"
          :label="t('botProtection.timeout')"
          :hint="t('botProtection.timeoutHint')"
        />
      </section>

      <div class="flex justify-end">
        <Button :loading="saving" :disabled="!dirty" data-testid="bot-protection-save" @click="save">
          <Save class="h-4 w-4" />{{ t('common.save') }}
        </Button>
      </div>
    </template>
  </div>
</template>
