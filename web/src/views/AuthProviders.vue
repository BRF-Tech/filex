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
 *
 * An operating-system provider (windows, pam — `test_account_required`) can
 * only be proved by signing somebody in. Its card asks for a test account
 * beside the buttons; the account travels on the one request that tests or
 * saves, and the account that passed becomes a super administrator (the
 * server says so: `super_admin`). Its failing test is never confirmed away —
 * the server answers `confirm_allowed: false` and the page offers no
 * "switch on anyway", only the steps and what to do.
 *
 * A provider is an INSTANCE of a driver (docs/TENANT-ADMIN.md): the first of
 * each kind is named by the driver (`ldap`), and "Add a provider" makes
 * another (a second directory, an SSO for some tenants only). On a
 * multi-tenant install each card says which tenants sign in through it; a
 * tenant not ticked gets nothing from it.
 */
import { computed, onMounted, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { Save, Activity, Lock, ShieldCheck, Plus, Trash2, Building2 } from 'lucide-vue-next';
import { foreignText } from '@brftech/filex-core';

import { AuthProvidersApi } from '@/api/auth-providers';
import type {
  AuthProvider,
  AuthProviderCheck,
  AuthProviderField,
  AuthProviderTenant,
  AuthProviderTestAccount,
  AuthProviderTestResult,
} from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { extractError } from '@/api/client';

import Button from '@/components/ui/Button.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Input from '@/components/ui/Input.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Modal from '@/components/ui/Modal.vue';
import CodeText from '@/components/ui/CodeText.vue';
import ProviderFields from '@/components/ProviderFields.vue';
import ProviderChecks from '@/components/ProviderChecks.vue';
import { providerCheckText, providerFieldLabel } from '@/lib/providerChecks';

const { t, te, locale } = useI18n();
const toast = useToastStore();

interface Draft {
  enabled: boolean;
  label: string;
  values: Record<string, string | boolean>;
}

const items = ref<AuthProvider[]>([]);
const loading = ref(false);
const passwordSignIn = ref(true);
const recoveryLogin = ref(false);
const secretKey = ref(true);
const multiTenant = ref(false);
const reviewPending = ref(false);
const tenantList = ref<AuthProviderTenant[]>([]);
/** The tenants ticked on each card, as the operator left them (multi-tenant). */
const bound = reactive<Record<string, number[]>>({});
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

/**
 * The test account of each operating-system provider, as typed.
 *
 * ⚠⚠ The password lives here only until the request that uses it is answered,
 * then it is cleared (forgetPassword) — whatever the answer. It is never put in
 * a store, in browser storage, in a toast or in a log line, and the server
 * uses it for that one request only (auth.TestAccount).
 */
const accounts = reactive<Record<string, AuthProviderTestAccount>>({});

/** The question asked before switching on a provider whose test failed. */
const confirming = ref<{ provider: AuthProvider; checks: AuthProviderCheck[]; message: string } | null>(null);

const passwordEnv = computed(() => items.value.find((p) => p.id === 'local' && p.enabled) ?? null);

function fieldsOf(p: AuthProvider): AuthProviderField[] {
  return p.fields ?? [];
}


function accountOf(p: AuthProvider): AuthProviderTestAccount {
  if (!accounts[p.id]) accounts[p.id] = { username: '', password: '' };
  return accounts[p.id];
}

/** The account a request carries: only a provider that asks for one, only with a name. */
function accountToSend(p: AuthProvider): AuthProviderTestAccount | undefined {
  if (!p.test_account_required) return undefined;
  const a = accountOf(p);
  const username = a.username.trim();
  return username ? { username, password: a.password } : undefined;
}

function forgetPassword(p: AuthProvider) {
  if (accounts[p.id]) accounts[p.id].password = '';
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
    drafts[p.id] = { enabled: p.enabled, label: p.label ?? '', values };
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
    multiTenant.value = o.multiTenant === true;
    reviewPending.value = o.reviewPending === true;
    tenantList.value = o.tenants ?? [];
    for (const k of Object.keys(bound)) delete bound[k];
    for (const p of o.providers) if (p.tenants) bound[p.id] = [...p.tenants];
    // Fresh drafts: a reload re-hydrates from the server (a secret that just
    // became "set", a provider that just started).
    for (const k of Object.keys(drafts)) delete drafts[k];
    for (const p of items.value) {
      ensureDraft(p);
      if (p.test_account_required) accountOf(p);
    }
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
  refusals[p.id] = undefined;
  const d = ensureDraft(p);
  // Only a save that leaves the provider ON runs its test with the account; a
  // save that switches it off needs none, and is not sent one.
  const account = d.enabled ? accountToSend(p) : undefined;
  if (p.test_account_required && d.enabled && !account?.password) {
    // Switched on, an operating-system provider is tested by a real sign-in;
    // without an account the server could only refuse. Said here, unsent.
    refusals[p.id] = t('authProviders.testAccount.needed');
    return;
  }
  savingId.value = p.id;
  try {
    const res = await AuthProvidersApi.update(p.id, {
      enabled: d.enabled,
      label: p.instance_id ? d.label.trim() : undefined,
      config: draftConfig(p),
      confirm_failed_test: confirmFailedTest || undefined,
      test_account: account,
    });
    if (res.status === 'test_failed') {
      results[p.id] = { testable: true, ok: false, checks: res.checks };
      if (res.confirmAllowed !== false) {
        confirming.value = { provider: p, checks: res.checks.filter((c) => c.status === 'fail'), message: res.message };
      } else {
        // There is no "switch on anyway" for it: the steps above say what
        // failed and what to do, the server's sentence why nothing was saved.
        refusals[p.id] = res.message ? foreignText(String(locale.value), res.message) : t('authProviders.testFail');
      }
      return;
    }
    results[p.id] = res.checks.length ? { testable: true, ok: res.testOk, checks: res.checks } : undefined;
    toast.success(t('authProviders.savedApplied'));
    if (res.superAdmin && account) {
      toast.success(t('authProviders.testAccount.madeSuperAdmin', { account: account.username }));
    }
    await load();
  } catch (e: unknown) {
    refusals[p.id] = extractError(e, t('errors.generic'));
  } finally {
    forgetPassword(p);
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
    results[p.id] = await AuthProvidersApi.test(p.id, p.managed ? draftConfig(p) : {}, accountToSend(p));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    forgetPassword(p);
    testingId.value = null;
  }
}

/** One step of a test, in words (lib/providerChecks: one wording for every page). */
const checkText = (c: AuthProviderCheck) => providerCheckText(c, t as never, te as never);
const fieldLabel = (key: string) => providerFieldLabel(key, t as never, te as never);

/** A card's title: the kind, and the instance's own name when it is not the kind's first. */
function providerTitle(p: AuthProvider): string {
  const driver = p.driver || p.id;
  const kind = t(`authProviders.providers.${driver}` as never);
  const own = p.label?.trim() || (p.id !== driver ? p.id : '');
  return own ? `${kind} · ${own}` : kind;
}

/** A provider made with "Add a provider": deleted, not just switched off. */
const deletable = (p: AuthProvider) => !!p.managed && !!p.driver && p.id !== p.driver && p.origin !== 'environment';

function tenantName(id: number): string {
  const tn = tenantList.value.find((x) => x.id === id);
  if (!tn) return `#${id}`;
  return tn.is_supertenant ? t('authProviders.platformTenant') : tn.name;
}

function toggleTenant(p: AuthProvider, id: number, on: boolean) {
  const list = bound[p.id] ?? [];
  bound[p.id] = on ? [...new Set([...list, id])] : list.filter((x) => x !== id);
}

/** A tenant-lockout refusal (409 tenant_lockout): asked, and sent again on a yes. */
function lockoutAsked(e: unknown): boolean {
  const res = (e as { response?: { status?: number; data?: { error?: string; message?: string } } }).response;
  if (res?.status !== 409 || res.data?.error !== 'tenant_lockout') return false;
  return confirm(foreignText(String(locale.value), res.data.message ?? '') || t('authProviders.lockoutConfirm'));
}

async function saveTenants(p: AuthProvider, confirmLockout = false): Promise<void> {
  refusals[p.id] = undefined;
  savingId.value = p.id;
  try {
    await AuthProvidersApi.setTenants(p.id, bound[p.id] ?? [], confirmLockout);
    toast.success(t('authProviders.tenantsSaved'));
    await load();
  } catch (e: unknown) {
    if (!confirmLockout && lockoutAsked(e)) {
      savingId.value = null;
      await saveTenants(p, true);
      return;
    }
    refusals[p.id] = extractError(e, t('errors.generic'));
  } finally {
    savingId.value = null;
  }
}

async function removeProvider(p: AuthProvider, confirmLockout = false): Promise<void> {
  if (!confirmLockout && !confirm(t('authProviders.deleteConfirm', { name: providerTitle(p) }))) return;
  refusals[p.id] = undefined;
  try {
    await AuthProvidersApi.remove(p.id, confirmLockout);
    toast.success(t('authProviders.deleted'));
    await load();
  } catch (e: unknown) {
    if (!confirmLockout && lockoutAsked(e)) {
      await removeProvider(p, true);
      return;
    }
    refusals[p.id] = extractError(e, t('errors.generic'));
  }
}

async function dismissReview() {
  try {
    await AuthProvidersApi.dismissReview();
    reviewPending.value = false;
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

// ── another provider of a kind ──
const KINDS = ['oidc', 'ldap', 'proxy-header', 'windows', 'pam'];
const adding = ref(false);
const addKind = ref('ldap');
const addSlug = ref('');
const addLabel = ref('');
const addError = ref('');
const addSaving = ref(false);
const kindOptions = computed(() => KINDS.map((k) => ({ value: k, label: t(`authProviders.providers.${k}` as never) })));
function openAdd() {
  addKind.value = 'ldap';
  addSlug.value = '';
  addLabel.value = '';
  addError.value = '';
  adding.value = true;
}
async function createProvider() {
  addSaving.value = true;
  addError.value = '';
  try {
    // Created switched off: its card is where it is filled in, tested and
    // switched on, like every other provider.
    await AuthProvidersApi.create({
      driver: addKind.value,
      slug: addSlug.value.trim() || undefined,
      label: addLabel.value.trim() || undefined,
      enabled: false,
    });
    adding.value = false;
    toast.success(t('authProviders.created'));
    await load();
  } catch (e: unknown) {
    const code = (e as { response?: { data?: { error?: string } } }).response?.data?.error;
    addError.value =
      code === 'slug_taken' || code === 'slug_invalid'
        ? t(`authProviders.addErrors.${code}`)
        : extractError(e, t('errors.generic'));
  } finally {
    addSaving.value = false;
  }
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
  return v == null || v === '' ? '-' : String(v);
}

onMounted(load);
</script>

<template>
  <div class="space-y-4 max-w-3xl">
    <div class="flex items-start justify-between gap-3 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold">{{ t('authProviders.title') }}</h1>
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('authProviders.subtitle') }}</p>
      </div>
      <Button variant="outline" size="sm" data-testid="auth-provider-add" @click="openAdd">
        <Plus class="h-4 w-4" /> {{ t('authProviders.add') }}
      </Button>
    </div>

    <!-- The upgrade bound every provider to every tenant (nothing changed for
         anyone): said until the operator has looked. -->
    <div
      v-if="multiTenant && reviewPending"
      class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200 flex items-start justify-between gap-3"
      role="status"
      data-testid="auth-providers-review"
    >
      <p>{{ t('authProviders.review') }}</p>
      <Button size="sm" variant="outline" data-testid="auth-providers-review-done" @click="dismissReview">
        {{ t('authProviders.reviewDone') }}
      </Button>
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
              {{ providerTitle(p) }}
              <Badge :tone="stateTone(p.status)" dot :data-testid="`auth-provider-state-${p.id}`">
                {{ t(`authProviders.status.${p.status}` as never) }}
              </Badge>
              <Badge v-if="p.origin === 'environment'" tone="zinc" :data-testid="`auth-provider-origin-${p.id}`">
                <Lock class="h-3 w-3" aria-hidden="true" />
                {{ t('authProviders.origin.environment') }}
              </Badge>
            </h2>
            <!-- The reason may carry the server's backtick-marked command (an
                 operating-system provider whose setup broke since). -->
            <i18n-t
              v-if="p.last_error"
              keypath="authProviders.failedReason"
              tag="p"
              class="text-xs text-rose-600 break-words"
              :data-testid="`auth-provider-error-${p.id}`"
            >
              <template #reason><CodeText :text="p.last_error" /></template>
            </i18n-t>
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
          <Input
            v-if="p.instance_id"
            :model-value="ensureDraft(p).label"
            :label="t('authProviders.label')"
            :hint="t('authProviders.labelHint')"
            :name="`auth-provider-label-${p.id}`"
            class="sm:col-span-2"
            @update:model-value="(v) => (ensureDraft(p).label = String(v ?? ''))"
          />
          <ProviderFields
            :fields="fieldsOf(p)"
            :values="ensureDraft(p).values"
            :secrets-set="p.secrets_set"
            :set-by-upgrade="p.set_by_upgrade"
            :prefix="p.id"
            @update="(k: string, v: string | boolean) => (ensureDraft(p).values[k] = v)"
          />
        </div>

        <!-- An operating-system provider is proved by signing a real account
             in: the account goes with "Test now" and with a save that leaves
             it on. ⚠ The password box is emptied when that request is
             answered, and its value goes nowhere else. -->
        <fieldset
          v-if="p.test_account_required && p.testable"
          class="space-y-2 rounded-lg border border-zinc-200 p-3 dark:border-zinc-700"
          :data-testid="`auth-provider-test-account-${p.id}`"
        >
          <legend class="px-1 text-sm font-medium">{{ t('authProviders.testAccount.title') }}</legend>
          <p class="text-xs text-zinc-500 dark:text-zinc-400">
            {{ p.managed ? t('authProviders.testAccount.about') : t('authProviders.testAccount.aboutTestOnly') }}
          </p>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Input
              :model-value="accountOf(p).username"
              :label="t('authProviders.testAccount.username')"
              :name="`auth-test-account-${p.id}-username`"
              autocomplete="off"
              @update:model-value="(v) => (accountOf(p).username = String(v ?? ''))"
            />
            <!-- ⚠ new-password, not "off": browsers ignore "off" on a password
                 box and would fill in the administrator's own saved filex
                 password. -->
            <Input
              :model-value="accountOf(p).password"
              type="password"
              :label="t('authProviders.testAccount.password')"
              :name="`auth-test-account-${p.id}-password`"
              autocomplete="new-password"
              @update:model-value="(v) => (accountOf(p).password = String(v ?? ''))"
            />
          </div>
        </fieldset>

        <!-- The last test, step by step (one drawing: ProviderChecks). -->
        <ProviderChecks v-if="results[p.id]" :result="results[p.id]!" :testid="`auth-provider-test-${p.id}`" />

        <!-- Which tenants sign in through it (multi-tenant installs). A
             tenant's own provider serves that tenant only. -->
        <fieldset
          v-if="multiTenant && p.instance_id && p.tenants"
          class="space-y-2 rounded-lg border border-zinc-200 p-3 dark:border-zinc-700"
          :data-testid="`auth-provider-tenants-${p.id}`"
        >
          <legend class="px-1 text-sm font-medium flex items-center gap-1">
            <Building2 class="h-4 w-4" aria-hidden="true" /> {{ t('authProviders.tenantsTitle') }}
          </legend>
          <p v-if="p.owner_provider_id" class="text-xs text-zinc-500 dark:text-zinc-400">
            {{ t('authProviders.tenantsOwned', { tenant: tenantName(p.owner_provider_id) }) }}
          </p>
          <template v-else>
            <p class="text-xs text-zinc-500 dark:text-zinc-400">{{ t('authProviders.tenantsHint') }}</p>
            <div class="grid grid-cols-1 gap-1 sm:grid-cols-2">
              <Checkbox
                v-for="tn in tenantList"
                :key="tn.id"
                :model-value="(bound[p.id] ?? []).includes(tn.id)"
                :label="tn.is_supertenant ? t('authProviders.platformTenant') : `${tn.name} (${tn.realm})`"
                :name="`auth-provider-tenant-${p.id}-${tn.id}`"
                @update:model-value="(v: boolean) => toggleTenant(p, tn.id, v)"
              />
            </div>
            <div class="flex justify-end">
              <Button
                size="sm"
                variant="outline"
                :loading="savingId === p.id"
                :data-testid="`auth-provider-tenants-save-${p.id}`"
                @click="saveTenants(p)"
              >
                {{ t('authProviders.tenantsSave') }}
              </Button>
            </div>
          </template>
        </fieldset>

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
          <span class="flex items-center gap-2">
            <Button
              v-if="deletable(p)"
              variant="ghost"
              size="sm"
              :data-testid="`auth-provider-delete-${p.id}`"
              @click="removeProvider(p)"
            >
              <Trash2 class="h-4 w-4" />
              {{ t('authProviders.delete') }}
            </Button>
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
          </span>
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
        <p>{{ t('authProviders.confirmBody', { provider: providerTitle(confirming.provider) }) }}</p>
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

    <!-- Another provider of a kind: made switched off, then filled in on its card. -->
    <Modal v-model="adding" :title="t('authProviders.addTitle')" size="sm">
      <form class="space-y-3" data-testid="auth-provider-add-form" @submit.prevent="createProvider">
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('authProviders.addHint') }}</p>
        <Select v-model="addKind" :options="kindOptions" :label="t('authProviders.kind')" data-testid="auth-provider-add-kind" />
        <Input v-model="addLabel" :label="t('authProviders.label')" :hint="t('authProviders.labelHint')" data-testid="auth-provider-add-label" />
        <Input v-model="addSlug" :label="t('authProviders.slug')" :hint="t('authProviders.slugHint')" monospace data-testid="auth-provider-add-slug" />
        <p v-if="addError" class="error-text" role="alert">{{ addError }}</p>
      </form>
      <template #footer>
        <Button variant="ghost" @click="adding = false">{{ t('common.cancel') }}</Button>
        <Button :loading="addSaving" data-testid="auth-provider-add-create" @click="createProvider">{{ t('authProviders.create') }}</Button>
      </template>
    </Modal>
  </div>
</template>
