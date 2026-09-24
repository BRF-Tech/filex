<script setup lang="ts">
/**
 * Admin → Identity providers — the sign-in providers this instance runs.
 *
 * ⚠⚠ Until v0.43.0 this page saved settings nothing read: sign-in was built
 * from the environment only, and "restart the server for the change to take
 * effect" was a restart that changed nothing (release-candidate sweep,
 * 2026-09-21). It now really manages sign-in, and it can never lock the
 * instance out (internal/authsetup):
 *
 *   - a provider the ENVIRONMENT defines is shown read-only, with where it is
 *     defined — never an editable form that only looks like it took effect;
 *   - password sign-in and the installation administrator's recovery sign-in
 *     are the environment's; this page has no switch for either;
 *   - Save runs the real test first. Switching a provider ON while its test
 *     fails asks first, naming the steps that failed; a provider saved OFF
 *     saves whatever its test says;
 *   - the last way an administrator can sign in cannot be switched off (the
 *     server refuses it and says why);
 *   - a secret is never sent back: the box says "set — type to replace";
 *   - a save is applied at once — no restart.
 */
import { computed, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, Activity, Lock, ShieldCheck } from 'lucide-vue-next';

import { AuthProvidersApi } from '@/api/auth-providers';
import type { AuthProvider, AuthProviderCheck, AuthProviderField, AuthProviderTestResult } from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';

import Button from '@/components/ui/Button.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Input from '@/components/ui/Input.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Modal from '@/components/ui/Modal.vue';

const { t } = useI18n();
const toast = useToastStore();

/** How a field is drawn — the list of fields and their kinds is the server's. */
const PRESENTATION: Record<string, { placeholder?: string; monospace?: boolean }> = {
  issuer: { placeholder: 'https://auth.example.com/realms/main', monospace: true },
  redirect_url: { placeholder: 'https://files.example.com/api/auth/oidc/callback', monospace: true },
  scopes: { placeholder: 'groups offline_access' },
  role_claim: { placeholder: 'realm_access.roles', monospace: true },
  admin_group: { placeholder: 'filex-admin' },
  url: { placeholder: 'ldaps://dc.example.com:636', monospace: true },
  base_dn: { placeholder: 'dc=example,dc=com', monospace: true },
  bind_dn: { placeholder: 'cn=svc,dc=example,dc=com', monospace: true },
  user_filter: { placeholder: '(mail=%s)', monospace: true },
  email_attr: { placeholder: 'mail' },
  ca_file: { placeholder: '/etc/filex/ldap-ca.pem', monospace: true },
  trusted_proxies: { placeholder: '10.0.0.0/8, 172.16.0.0/12', monospace: true },
  header_user: { placeholder: 'X-Auth-User', monospace: true },
  header_email: { placeholder: 'X-Auth-Email', monospace: true },
  header_name: { placeholder: 'X-Auth-Name', monospace: true },
  header_roles: { placeholder: 'X-Auth-Roles', monospace: true },
  admin_role: { placeholder: 'admin' },
};

interface Draft {
  enabled: boolean;
  values: Record<string, string | boolean>;
}

const items = ref<AuthProvider[]>([]);
const loading = ref(false);
const passwordSignIn = ref(true);
const recoveryLogin = ref(false);
const secretKey = ref(true);
const drafts = reactive<Record<string, Draft>>({});
const savingId = ref<string | null>(null);
const testingId = ref<string | null>(null);
/** A refusal the server gave a save, shown on the card it is about. */
const refusals = reactive<Record<string, string | undefined>>({});
/**
 * The last test of each provider, step by step. ⚠ Shown on the card, not in
 * a toast: a test is a list of what was reached and what was not, and the
 * one that failed is the one the operator has to read and act on.
 */
const results = reactive<Record<string, AuthProviderTestResult | undefined>>({});

/** The question asked before switching on a provider whose test failed. */
const confirming = ref<{ provider: AuthProvider; checks: AuthProviderCheck[]; message: string } | null>(null);

const passwordEnv = computed(() => items.value.find((p) => p.id === 'local' && p.enabled) ?? null);

function fieldsOf(p: AuthProvider): AuthProviderField[] {
  return p.fields ?? [];
}

function fieldLabel(key: string): string {
  const k = `authProviders.fields.${key}`;
  const label = t(k as never);
  return label === k ? key : label;
}

function boolOf(v: unknown, dflt?: string): boolean {
  if (v === undefined || v === null || v === '') return dflt === 'true';
  return v === true || v === 'true' || v === '1' || v === 'yes' || v === 'on';
}

function ensureDraft(p: AuthProvider): Draft {
  if (!drafts[p.id]) {
    const cfg = (p.config_redacted ?? {}) as Record<string, unknown>;
    const values: Record<string, string | boolean> = {};
    for (const f of fieldsOf(p)) {
      if (f.kind === 'bool') values[f.key] = boolOf(cfg[f.key], f.default);
      // A secret is never sent to the browser: the box starts empty and says
      // whether one is set.
      else if (f.kind === 'secret') values[f.key] = '';
      else values[f.key] = cfg[f.key] != null ? String(cfg[f.key]) : '';
    }
    drafts[p.id] = { enabled: p.enabled, values };
  }
  return drafts[p.id];
}

async function load() {
  loading.value = true;
  try {
    const o = await AuthProvidersApi.overview();
    items.value = o.providers;
    passwordSignIn.value = o.passwordSignIn;
    recoveryLogin.value = o.recoveryLogin;
    secretKey.value = o.secretKey;
    // Fresh drafts: a reload re-hydrates from the server (a secret that just
    // became "set", a provider that just started).
    for (const k of Object.keys(drafts)) delete drafts[k];
    for (const p of items.value) ensureDraft(p);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
}

/**
 * The form as it stands, as the config map Save writes and Test checks — one
 * builder for both, so the test is of exactly what would be saved. A secret
 * left blank is not sent (the stored one is kept).
 */
function draftConfig(p: AuthProvider): Record<string, unknown> {
  const d = ensureDraft(p);
  const config: Record<string, unknown> = {};
  for (const f of fieldsOf(p)) {
    const v = d.values[f.key];
    if (f.kind === 'bool') config[f.key] = !!v;
    else if (f.kind === 'secret') {
      const s = String(v ?? '').trim();
      if (s !== '') config[f.key] = s;
    } else config[f.key] = String(v ?? '').trim();
  }
  return config;
}

async function save(p: AuthProvider, confirmFailedTest = false) {
  savingId.value = p.id;
  refusals[p.id] = undefined;
  try {
    const d = ensureDraft(p);
    const res = await AuthProvidersApi.update(p.id, {
      enabled: d.enabled,
      config: draftConfig(p),
      confirm_failed_test: confirmFailedTest || undefined,
    });
    if (res.status === 'test_failed') {
      confirming.value = { provider: p, checks: res.checks.filter((c) => c.status === 'fail'), message: res.message };
      results[p.id] = { testable: true, ok: false, checks: res.checks };
      return;
    }
    results[p.id] = res.checks.length ? { testable: true, ok: res.testOk, checks: res.checks } : undefined;
    toast.success(t('authProviders.savedApplied'));
    await load();
  } catch (e: unknown) {
    refusals[p.id] = extractError(e, t('errors.generic'));
  } finally {
    savingId.value = null;
  }
}

async function confirmEnable() {
  const c = confirming.value;
  confirming.value = null;
  if (c) await save(c.provider, true);
}

async function test(p: AuthProvider) {
  testingId.value = p.id;
  results[p.id] = undefined;
  try {
    results[p.id] = await AuthProvidersApi.test(p.id, p.managed ? draftConfig(p) : {});
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    testingId.value = null;
  }
}

/**
 * One step of a test, in words. The sentence names what was reached (a
 * host, a DN, an address) and, when a step failed, why — the reason
 * classified by the server (refused, timeout, dns, tls, credentials …) and
 * said here in the reader's language. The server's own error text is kept
 * for the "technical detail" line, never as the sentence.
 */
function checkText(c: AuthProviderCheck): string {
  const params: Record<string, string> = { ...(c.params ?? {}) };
  if (params.fields) {
    params.fields = params.fields.split(',').map((k) => fieldLabel(k)).join(', ');
  }
  if (params.reason) {
    const k = `authProviders.reasons.${params.reason}`;
    const said = t(k as never, params);
    params.reason = said === k ? params.reason : said;
  }
  // ⚠ The one env name this page cannot draw as <code>: a check line is one
  // text span built from a computed key, so the name is filled as a value
  // instead — out of the translatable sentence either way.
  params.env = 'FILEX_SECRET_KEY';
  const key = `authProviders.checks.${c.id}.${c.status}`;
  const text = t(key as never, params);
  return text === key ? `${c.id}: ${c.status}` : text;
}

const stateTone = (s: AuthProvider['status']) => {
  if (s === 'ok') return 'emerald';
  if (s === 'misconfigured') return 'rose';
  return 'zinc';
};

/** A read-only value from the environment, as it is shown. */
function shown(v: unknown): string {
  if (typeof v === 'boolean') return v ? t('common.yes') : t('common.no');
  if (Array.isArray(v)) return v.join(', ');
  return v == null || v === '' ? '—' : String(v);
}

onMounted(load);
</script>

<template>
  <div class="space-y-4 max-w-3xl">
    <div>
      <h1 class="text-xl font-semibold">{{ t('authProviders.title') }}</h1>
      <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('authProviders.subtitle') }}</p>
    </div>

    <!-- What this page can and cannot do to sign-in — said before anything
         is changed, because it is why a mistake here cannot lock anyone out. -->
    <div class="card card-body flex items-start gap-3 text-sm" data-testid="auth-providers-lockout-note">
      <ShieldCheck class="h-5 w-5 shrink-0 text-emerald-600" aria-hidden="true" />
      <div class="space-y-1">
        <p>{{ t('authProviders.lockoutNote') }}</p>
        <p v-if="passwordEnv" class="text-zinc-500">{{ t('authProviders.passwordOn', { from: passwordEnv.from }) }}</p>
        <!-- ⚠ An environment variable's NAME is a `{env}` slot drawn as
             <code>, never letters inside the translated sentence — the
             pattern login.noProviders and editor.missingPath already use. -->
        <i18n-t v-else keypath="authProviders.passwordOff" tag="p" class="text-zinc-500">
          <template #env><code>FILEX_AUTH_DRIVERS</code></template>
        </i18n-t>
        <i18n-t
          v-if="!passwordEnv"
          :keypath="recoveryLogin ? 'authProviders.recoveryOn' : 'authProviders.recoveryOff'"
          tag="p"
          class="text-zinc-500"
        >
          <template #env><code>FILEX_AUTH_RECOVERY_LOGIN</code></template>
        </i18n-t>
      </div>
    </div>

    <p
      v-if="!secretKey"
      class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
      role="status"
      data-testid="auth-providers-no-secret-key"
    >
      <i18n-t keypath="authProviders.noSecretKey" tag="span"><template #env><code>FILEX_SECRET_KEY</code></template></i18n-t>
    </p>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div v-else class="space-y-3">
      <div v-for="p in items" :key="p.id" class="card card-body space-y-3" :data-testid="`auth-provider-${p.id}`">
        <div class="flex items-start justify-between gap-3">
          <div class="space-y-1">
            <h2 class="text-sm font-semibold flex flex-wrap items-center gap-2">
              {{ t(`authProviders.providers.${p.id}` as any) }}
              <Badge :tone="stateTone(p.status)" dot :data-testid="`auth-provider-state-${p.id}`">
                {{ t(`authProviders.status.${p.status}` as never) }}
              </Badge>
              <Badge v-if="p.origin === 'environment'" tone="zinc" :data-testid="`auth-provider-origin-${p.id}`">
                <Lock class="h-3 w-3" aria-hidden="true" />
                {{ t('authProviders.origin.environment') }}
              </Badge>
            </h2>
            <p v-if="p.last_error" class="text-xs text-rose-600 break-all" :data-testid="`auth-provider-error-${p.id}`">
              {{ t('authProviders.failedReason', { reason: p.last_error }) }}
            </p>
          </div>
          <Toggle
            v-if="p.managed"
            v-model="ensureDraft(p).enabled"
            :label="t('common.enabled')"
            :name="`auth-provider-enabled-${p.id}`"
          />
        </div>

        <!-- Saved before v0.43.0 and never applied: imported switched off. -->
        <p
          v-if="p.legacy"
          class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
          :data-testid="`auth-provider-legacy-${p.id}`"
        >
          {{ t('authProviders.legacy') }}
        </p>

        <!-- Defined by the environment: read-only, and says where. -->
        <template v-if="p.origin === 'environment' && p.id !== 'local'">
          <p class="text-sm text-zinc-600 dark:text-zinc-400" :data-testid="`auth-provider-env-${p.id}`">
            {{ t('authProviders.envReadOnly', { from: p.from }) }}
          </p>
          <dl class="grid grid-cols-1 gap-x-4 gap-y-1 text-sm sm:grid-cols-[auto_1fr]">
            <template v-for="(v, k) in p.config_redacted" :key="k">
              <dt class="text-zinc-500">{{ fieldLabel(String(k)) }}</dt>
              <dd class="break-all font-mono text-xs">{{ shown(v) }}</dd>
            </template>
            <template v-for="(on, k) in p.secrets_set" :key="`s-${k}`">
              <dt v-if="on" class="text-zinc-500">{{ fieldLabel(String(k)) }}</dt>
              <dd v-if="on" class="text-xs">{{ t('authProviders.secretSetShort') }}</dd>
            </template>
          </dl>
          <p v-if="p.shadowed" class="text-xs text-zinc-500" :data-testid="`auth-provider-shadowed-${p.id}`">
            {{ t('authProviders.shadowed') }}
          </p>
        </template>

        <template v-else-if="p.id === 'local'">
          <p v-if="p.enabled" class="text-sm text-zinc-600 dark:text-zinc-400">
            {{ t('authProviders.envReadOnly', { from: p.from }) }}
          </p>
          <i18n-t v-else keypath="authProviders.passwordOff" tag="p" class="text-sm text-zinc-600 dark:text-zinc-400">
            <template #env><code>FILEX_AUTH_DRIVERS</code></template>
          </i18n-t>
        </template>
        <p v-else-if="p.id === 'api-token'" class="text-sm text-zinc-600 dark:text-zinc-400">
          {{ t('authProviders.apiTokenAlways') }}
        </p>

        <!-- Managed on this page. -->
        <div v-else-if="p.managed" class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <template v-for="f in fieldsOf(p)" :key="f.key">
            <div v-if="f.kind === 'bool'" class="sm:col-span-2 pt-1">
              <Toggle
                :model-value="(ensureDraft(p).values[f.key] as boolean)"
                :label="fieldLabel(f.key)"
                :name="`auth-field-${p.id}-${f.key}`"
                @update:model-value="(v) => (ensureDraft(p).values[f.key] = v)"
              />
            </div>
            <Input
              v-else
              :model-value="(ensureDraft(p).values[f.key] as string)"
              :type="f.kind === 'secret' ? 'password' : 'text'"
              :label="fieldLabel(f.key)"
              :required="f.required"
              :name="`auth-field-${p.id}-${f.key}`"
              :placeholder="f.kind === 'secret' && p.secrets_set?.[f.key] ? '••••••••' : PRESENTATION[f.key]?.placeholder"
              :hint="f.kind === 'secret' && p.secrets_set?.[f.key] ? t('authProviders.secretSet') : f.key === 'redirect_url' ? t('authProviders.redirectDefault') : undefined"
              :monospace="PRESENTATION[f.key]?.monospace"
              autocomplete="off"
              @update:model-value="(v) => (ensureDraft(p).values[f.key] = (v ?? '') as string)"
            />
          </template>
        </div>

        <!-- The last test, step by step: what was reached, what was not and
             why, and what cannot be known without a person signing in. -->
        <div
          v-if="results[p.id]"
          class="rounded-lg border p-3 text-sm"
          :class="results[p.id]!.ok
            ? 'border-emerald-200 bg-emerald-50 dark:border-emerald-900/50 dark:bg-emerald-950/30'
            : 'border-rose-200 bg-rose-50 dark:border-rose-900/50 dark:bg-rose-950/30'"
          role="status"
          :data-testid="`auth-provider-test-${p.id}`"
        >
          <p class="font-medium">
            {{ results[p.id]!.ok ? t('authProviders.testOk') : t('authProviders.testFail') }}
          </p>
          <ul class="mt-2 space-y-1">
            <li
              v-for="(c, i) in results[p.id]!.checks"
              :key="i"
              class="flex items-start gap-2"
              :data-testid="`auth-provider-check-${c.id}`"
              :data-status="c.status"
            >
              <span aria-hidden="true" class="w-4 shrink-0 font-semibold">{{ c.status === 'ok' ? '✓' : c.status === 'fail' ? '✗' : '–' }}</span>
              <span>
                {{ checkText(c) }}
                <span v-if="c.status === 'fail' && c.params?.detail" class="mt-0.5 block break-all font-mono text-[11px] opacity-70">
                  {{ t('authProviders.technicalDetailIs', { detail: c.params.detail }) }}
                </span>
              </span>
            </li>
          </ul>
        </div>

        <p v-if="refusals[p.id]" class="error-text" role="alert" :data-testid="`auth-provider-refusal-${p.id}`">
          {{ refusals[p.id] }}
        </p>

        <div v-if="p.testable || p.managed" class="flex items-center justify-between pt-1 gap-2">
          <!-- Only where there is something to reach. Local accounts and API
               tokens have no server to test; a button that can only say
               "OK" is worse than none. -->
          <Button
            v-if="p.testable"
            variant="outline"
            size="sm"
            :loading="testingId === p.id"
            :data-testid="`auth-provider-test-button-${p.id}`"
            @click="test(p)"
          >
            <Activity class="h-4 w-4" />
            {{ t('common.testNow') }}
          </Button>
          <span v-else />
          <Button
            v-if="p.managed"
            size="sm"
            :loading="savingId === p.id"
            :data-testid="`auth-provider-save-${p.id}`"
            @click="save(p)"
          >
            <Save class="h-4 w-4" />
            {{ t('authProviders.saveApply') }}
          </Button>
        </div>
      </div>
    </div>

    <!-- Switching ON a provider whose test failed: asked, never assumed. -->
    <Modal
      :model-value="confirming !== null"
      :title="t('authProviders.confirmTitle')"
      size="md"
      @update:model-value="(v: boolean) => { if (!v) confirming = null; }"
    >
      <div v-if="confirming" class="space-y-2 text-sm" data-testid="auth-provider-confirm">
        <p>{{ t('authProviders.confirmBody', { provider: t(`authProviders.providers.${confirming.provider.id}` as any) }) }}</p>
        <ul class="space-y-1">
          <li v-for="(c, i) in confirming.checks" :key="i" class="flex gap-2" :data-testid="`auth-provider-confirm-${c.id}`">
            <span aria-hidden="true" class="font-semibold">✗</span>
            <span>{{ checkText(c) }}</span>
          </li>
        </ul>
        <p class="text-zinc-500">{{ t('authProviders.confirmNote') }}</p>
      </div>
      <template #footer>
        <Button variant="ghost" @click="confirming = null">{{ t('common.cancel') }}</Button>
        <Button variant="danger" data-testid="auth-provider-confirm-enable" @click="confirmEnable">
          {{ t('authProviders.confirmEnable') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>
