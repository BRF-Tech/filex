<script setup lang="ts">
/**
 * Admin → Identity providers → one provider (/admin/auth-providers/:name):
 * its whole configuration, its test, the tenants that sign in through it and
 * - an LDAP directory - its sync. The overview (AuthProviders.vue) lists
 * every provider as a card that opens here.
 *
 * It can never lock the instance out (internal/authsetup):
 *
 *   - a provider the ENVIRONMENT defines is shown read-only, with where it is
 *     defined - never an editable form that only looks like it took effect;
 *   - password sign-in and the installation administrator's recovery sign-in
 *     are the environment's; this page has no switch for either;
 *   - Save runs the real test first. Switching a provider ON while its test
 *     fails asks first, naming the steps that failed; a provider saved OFF
 *     saves whatever its test says;
 *   - the last way an administrator (or a tenant) can sign in cannot be
 *     switched off or removed (the server refuses it and says why);
 *   - a secret is never sent back: the box says "set - type to replace";
 *   - a save is applied at once - no restart.
 *
 * An operating-system provider (windows, pam - `test_account_required`) can
 * only be proved by signing somebody in: the page asks for a test account,
 * which travels on the one request that tests or saves.
 */
import { computed, onMounted, reactive, ref } from 'vue';
import { RouterLink, useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { Save, Activity, Lock, Trash2, ArrowLeft, Building2 } from 'lucide-vue-next';
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
import { useTenancy } from '@/composables/useTenancy';
import { extractError } from '@/api/client';

import Button from '@/components/ui/Button.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Input from '@/components/ui/Input.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';
import Modal from '@/components/ui/Modal.vue';
import CodeText from '@/components/ui/CodeText.vue';
import ProviderFields from '@/components/ProviderFields.vue';
import ProviderChecks from '@/components/ProviderChecks.vue';
import DirectorySyncPanel from '@/components/DirectorySyncPanel.vue';

const { t, te, locale } = useI18n();
const toast = useToastStore();

interface Draft {
  enabled: boolean;
  label: string;
  values: Record<string, string | boolean>;
}

const items = ref<AuthProvider[]>([]);
const loading = ref(false);
const secretKey = ref(true);
// Whether the tenant bindings are drawn: the server's one answer
// (composables/useTenancy), not this page's own reading of its list.
const { enabled: multiTenant } = useTenancy();
const tenantList = ref<AuthProviderTenant[]>([]);
/** The tenants ticked, as the operator left them (multi-tenant). */
const bound = reactive<Record<string, number[]>>({});
const drafts = reactive<Record<string, Draft>>({});
const savingId = ref<string | null>(null);
const testingId = ref<string | null>(null);
/** A refusal the server gave a save, shown on the card it is about. */
const refusals = reactive<Record<string, string | undefined>>({});
/**
 * The last test of the provider, step by step. ⚠ Shown on the card, not in a
 * toast: a test is a list of what was reached and what was not, and the one
 * that failed is the one the operator has to read and act on.
 */
const results = reactive<Record<string, AuthProviderTestResult | undefined>>({});
/**
 * The test account of an operating-system provider, as typed.
 *
 * ⚠⚠ The password lives here only until the request that uses it is answered,
 * then it is cleared (forgetPassword) - whatever the answer. It is never put in
 * a store, in browser storage, in a toast or in a log line, and the server
 * uses it for that one request only (auth.TestAccount).
 */
const accounts = reactive<Record<string, AuthProviderTestAccount>>({});

/** The question asked before switching on a provider whose test failed. */
const confirming = ref<{ provider: AuthProvider; checks: AuthProviderCheck[]; message: string } | null>(null);

const route = useRoute();
const router = useRouter();
/** The provider this page is about, by its slug in the address. */
const name = computed(() => String(route.params.name ?? ''));
const current = computed(() => items.value.find((p) => p.id === name.value) ?? null);
/** The tab of the overview it belongs under, for the way back. */
const backTab = computed(() => (current.value ? kindOf(current.value) : 'ldap'));
/** Settings or - an LDAP directory - its sync. */
const section = ref<'settings' | 'sync'>(route.query.section === 'sync' ? 'sync' : 'settings');
function setSection(v: 'settings' | 'sync') {
  section.value = v;
  void router.replace({ query: { ...route.query, section: v === 'sync' ? 'sync' : undefined } });
}

/** The driver a provider runs: `ldap` for every LDAP instance. */
function kindOf(p: AuthProvider): string {
  return p.driver || p.id;
}

/** The title: the kind, and the instance's own name when it has one. */
function titleOf(p: AuthProvider): string {
  const kind = t(`authProviders.providers.${kindOf(p)}` as never);
  const own = p.label?.trim() || (p.id !== kindOf(p) ? p.id : '');
  return own ? `${kind} · ${own}` : kind;
}

/**
 * The LDAP form's sections: connection, who can sign in, their groups, and
 * directory sync - seventeen fields in one grid read as a form nobody could
 * follow. A field no section names goes in the first.
 */
const SECTIONS: Record<string, string[]> = {
  ldap: ['connection', 'people', 'groups', 'sync'],
};
const SECTION_OF: Record<string, string> = {
  url: 'connection', base_dn: 'connection', bind_dn: 'connection', bind_password: 'connection',
  start_tls: 'connection', ca_file: 'connection', ca_pem: 'connection',
  user_filter: 'people', email_attr: 'people', email_domains: 'people', protocol_login: 'people',
  auto_create: 'people', allowed_groups: 'people', show_refusal_reason: 'people',
  group_attr: 'groups', group_filter: 'groups', group_base_dn: 'groups',
  sync_interval: 'sync', sync_filter: 'sync', sync_disable_missing: 'sync', sync_groups: 'sync', sync_group_filter: 'sync',
};
function sectionOf(p: AuthProvider, key: string): string {
  const order = SECTIONS[kindOf(p)];
  if (!order) return '';
  const s = SECTION_OF[key];
  return s && order.includes(s) ? s : order[0];
}
/** The provider's sections, each with its fields (schema order within one);
 *  one unnamed section for a kind that has none. */
function sectionsOf(p: AuthProvider): { key: string; fields: AuthProviderField[] }[] {
  const fields = p.fields ?? [];
  const order = SECTIONS[kindOf(p)];
  if (!order) return [{ key: '', fields }];
  return order.map((key) => ({ key, fields: fields.filter((f) => sectionOf(p, f.key) === key) })).filter((s) => s.fields.length);
}

/** One step of a test, in words: the server's sentence (backend
 *  auth/probe_say.go), the same on every page that tests a provider. */
const checkText = (c: AuthProviderCheck) => c.text || `${c.id}: ${c.status}`;
/** A configuration field's name in words (`authProviders.fields.<key>`), else the key. */
function fieldLabel(key: string): string {
  const k = `authProviders.fields.${key}`;
  return te(k) || te(k, 'en') ? t(k) : key;
}

/** An environment setting as the page shows it: its value, or what an unset
 *  one means ("memberOf (default)"). */
function envValue(p: AuthProvider, f: AuthProviderField): string {
  const v = (p.config_redacted ?? {})[f.key];
  if (v === undefined || v === null || v === '') {
    if (f.default !== undefined && f.default !== '') {
      const d = f.kind === 'bool' ? shown(f.default === 'true') : f.default;
      return t('authProviders.defaultIs', { value: d });
    }
    return '-';
  }
  return shown(v);
}
/** Environment settings the field list does not name (multi_tenant…). */
function envExtras(p: AuthProvider): [string, unknown][] {
  const named = new Set((p.fields ?? []).map((f) => f.key));
  return Object.entries(p.config_redacted ?? {}).filter(([k]) => !named.has(k));
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
    for (const f of p.fields ?? []) {
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
    secretKey.value = o.secretKey;
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
 * The form as it stands, as the config map Save writes and Test checks - one
 * builder for both, so the test is of exactly what would be saved. A secret
 * left blank is not sent (the stored one is kept).
 */
function draftConfig(p: AuthProvider): Record<string, unknown> {
  const d = ensureDraft(p);
  const config: Record<string, unknown> = {};
  for (const f of p.fields ?? []) {
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

// ── tenants (multi-tenant installs) ──
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

// ── delete: a provider made with "Add a provider" (a kind's first is switched off instead) ──
const deletable = (p: AuthProvider) => !!p.managed && !!p.driver && p.id !== p.driver && p.origin !== 'environment';
const removing = ref<AuthProvider | null>(null);
const removingBusy = ref(false);
async function removeProvider(confirmLockout = false): Promise<void> {
  const p = removing.value;
  if (!p) return;
  removingBusy.value = true;
  refusals[p.id] = undefined;
  try {
    await AuthProvidersApi.remove(p.id, confirmLockout);
    removing.value = null;
    toast.success(t('authProviders.deleted'));
    void router.push({ name: 'auth-providers', query: { tab: kindOf(p) } });
  } catch (e: unknown) {
    if (!confirmLockout && lockoutAsked(e)) {
      removingBusy.value = false;
      await removeProvider(true);
      return;
    }
    removing.value = null;
    refusals[p.id] = extractError(e, t('errors.generic'));
  } finally {
    removingBusy.value = false;
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
    <RouterLink
      :to="{ name: 'auth-providers', query: { tab: backTab } }"
      class="inline-flex items-center gap-1 text-sm text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100"
      data-testid="auth-provider-back"
    >
      <ArrowLeft class="h-4 w-4 rtl:rotate-180" /> {{ t('authProviders.title') }}
    </RouterLink>

    <p
      v-if="!secretKey && current?.managed"
      class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
      role="status"
      data-testid="auth-providers-no-secret-key"
    >
      <i18n-t keypath="authProviders.noSecretKey" tag="span"><template #env><code>FILEX_SECRET_KEY</code></template></i18n-t>
    </p>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <h1 v-if="!loading && current" class="text-xl font-semibold" data-testid="auth-provider-title">{{ titleOf(current) }}</h1>

    <p v-if="!loading && !current" class="card card-body text-sm text-zinc-500" data-testid="auth-provider-missing">
      {{ t('authProviders.notFound') }}
    </p>

    <nav
      v-if="!loading && current && kindOf(current) === 'ldap'"
      class="flex gap-1 border-b border-zinc-200 dark:border-zinc-800"
      role="tablist"
      data-testid="auth-provider-sections"
    >
      <button
        v-for="sec in (['settings', 'sync'] as const)"
        :key="sec"
        type="button"
        role="tab"
        :aria-selected="section === sec"
        :data-testid="`auth-section-tab-${sec}`"
        class="-mb-px border-b-2 px-3 py-2 text-sm font-medium transition"
        :class="section === sec
          ? 'border-brand-600 text-brand-600 dark:border-brand-400 dark:text-brand-400'
          : 'border-transparent text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100'"
        @click="setSection(sec)"
      >
        {{ t(`authProviders.editSections.${sec}` as never) }}
      </button>
    </nav>

    <div v-if="!loading && current" class="space-y-3" role="tabpanel">
      <div
        v-for="p in [current]"
        v-show="kindOf(p) !== 'ldap' || section === 'settings'"
        :key="p.id"
        class="card card-body space-y-3"
        :data-testid="`auth-provider-${p.id}`"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="space-y-1">
            <div class="text-sm flex flex-wrap items-center gap-2">
              <Badge :tone="stateTone(p.status)" dot :data-testid="`auth-provider-state-${p.id}`">
                {{ t(`authProviders.status.${p.status}` as never) }}
              </Badge>
              <Badge v-if="p.origin === 'environment'" tone="zinc" :data-testid="`auth-provider-origin-${p.id}`">
                <Lock class="h-3 w-3" aria-hidden="true" />
                {{ t('authProviders.origin.environment') }}
              </Badge>
            </div>
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

        <!-- Defined by the environment: read-only, and says where - in the
             same order and sections as a page provider's form. -->
        <template v-if="p.origin === 'environment' && p.id !== 'local'">
          <p class="text-sm text-zinc-600 dark:text-zinc-400" :data-testid="`auth-provider-env-${p.id}`">
            {{ t('authProviders.envReadOnly', { from: p.from }) }}
          </p>
          <dl v-if="p.fields?.length" class="grid grid-cols-1 gap-x-4 gap-y-1 text-sm sm:grid-cols-[auto_1fr]" :data-testid="`auth-provider-envfields-${p.id}`">
            <template v-for="s in sectionsOf(p)" :key="s.key">
              <dt v-if="s.key" class="sm:col-span-2 pt-2 text-xs font-semibold uppercase tracking-wide text-zinc-500">
                {{ t(`authProviders.sections.${s.key}` as never) }}
              </dt>
              <template v-for="f in s.fields" :key="f.key">
                <dt class="text-zinc-500">{{ fieldLabel(f.key) }}</dt>
                <dd v-if="f.kind === 'secret'" class="text-xs">{{ p.secrets_set?.[f.key] ? t('authProviders.secretSetShort') : '-' }}</dd>
                <dd v-else class="break-all font-mono text-xs">{{ envValue(p, f) }}</dd>
              </template>
            </template>
            <template v-for="[k, v] in envExtras(p)" :key="`x-${k}`">
              <dt class="text-zinc-500">{{ fieldLabel(k) }}</dt>
              <dd class="break-all font-mono text-xs">{{ shown(v) }}</dd>
            </template>
          </dl>
          <dl v-else class="grid grid-cols-1 gap-x-4 gap-y-1 text-sm sm:grid-cols-[auto_1fr]">
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

        <!-- Managed on this page: the instance's name, then its fields by
             section (one drawing: ProviderFields). -->
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
          <template v-for="s in sectionsOf(p)" :key="s.key">
            <h3
              v-if="s.key"
              class="sm:col-span-2 pt-2 text-xs font-semibold uppercase tracking-wide text-zinc-500"
              :data-testid="`auth-section-${p.id}-${s.key}`"
            >
              {{ t(`authProviders.sections.${s.key}` as never) }}
            </h3>
            <ProviderFields
              :fields="s.fields"
              :values="ensureDraft(p).values"
              :secrets-set="p.secrets_set"
              :set-by-upgrade="p.set_by_upgrade"
              :prefix="p.id"
              @update="(k: string, v: string | boolean) => (ensureDraft(p).values[k] = v)"
            />
          </template>
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
          <div class="flex items-center gap-2">
            <Button
              v-if="deletable(p)"
              variant="ghost"
              size="sm"
              :data-testid="`auth-provider-delete-${p.id}`"
              @click="removing = p"
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
          </div>
        </div>
      </div>
      <div v-if="kindOf(current) === 'ldap' && section === 'sync'" class="space-y-3" data-testid="auth-provider-sync">
        <DirectorySyncPanel v-if="current.state === 'running'" :name="current.id" />
        <p v-else class="card card-body text-sm text-zinc-500">{{ t('authProviders.syncNeedsRunning') }}</p>
      </div>
    </div>

    <Modal :model-value="removing !== null" :title="t('authProviders.delete')" size="sm" @update:model-value="(v: boolean) => { if (!v) removing = null; }">
      <p v-if="removing" class="text-sm" data-testid="auth-provider-delete-body">{{ t('authProviders.deleteConfirm', { name: titleOf(removing) }) }}</p>
      <template #footer>
        <Button variant="ghost" @click="removing = null">{{ t('common.cancel') }}</Button>
        <Button variant="danger" :loading="removingBusy" data-testid="auth-provider-delete-confirm" @click="removeProvider()">{{ t('authProviders.delete') }}</Button>
      </template>
    </Modal>

    <!-- Switching ON a provider whose test failed: asked, never assumed. -->
    <Modal
      :model-value="confirming !== null"
      :title="t('authProviders.confirmTitle')"
      size="md"
      @update:model-value="(v: boolean) => { if (!v) confirming = null; }"
    >
      <div v-if="confirming" class="space-y-2 text-sm" data-testid="auth-provider-confirm">
        <p>{{ t('authProviders.confirmBody', { provider: titleOf(confirming.provider) }) }}</p>
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
