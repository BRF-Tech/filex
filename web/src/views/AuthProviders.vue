<script setup lang="ts">
/**
 * Admin → Identity providers — who can sign in, and how: one tab per kind of
 * sign-in (LDAP, local, OIDC, reverse-proxy header, Windows, Linux PAM), each
 * a set of summary cards. A card says whether the provider works and what it
 * reaches; it opens the provider's own page (AuthProviderEdit.vue) for
 * everything else - its settings, test, tenants and, for LDAP, directory
 * sync. API keys are not here: they are always accepted and issued on
 * API / MCP.
 *
 * A provider is an INSTANCE of a driver (docs/TENANT-ADMIN.md): the first of
 * each kind is named by the driver (`ldap`), and "Add a provider" makes
 * another (a second directory, an SSO for some tenants only).
 *
 * Why nothing here can lock the instance out is said at the top, before
 * anything is changed (internal/authsetup; docs/SSO.md).
 */
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue';
import { RouterLink, useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { ChevronRight, Lock, Plus, RefreshCw, ShieldCheck } from 'lucide-vue-next';

import { AuthProvidersApi, type DirectorySyncStatus } from '@/api/auth-providers';
import type { AuthProvider } from '@/api/types';
import { extractError } from '@/api/client';
import { formatInterval, formatRelative } from '@/lib/format';
import { useToastStore } from '@/stores/toast';
import { useTenancy } from '@/composables/useTenancy';

import Badge from '@/components/ui/Badge.vue';
import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Modal from '@/components/ui/Modal.vue';
import Select from '@/components/ui/Select.vue';
import Spinner from '@/components/ui/Spinner.vue';

const { t, locale } = useI18n();
const toast = useToastStore();
const route = useRoute();
const router = useRouter();

const items = ref<AuthProvider[]>([]);
const loading = ref(false);
const recoveryLogin = ref(false);
const secretKey = ref(true);
// Whether anything about tenants is drawn: the server's one answer
// (composables/useTenancy), not this page's own reading of its list.
const { enabled: multiTenant } = useTenancy();
const reviewPending = ref(false);
/** Each LDAP provider's sync, for its card. */
const syncs = reactive<Record<string, DirectorySyncStatus | undefined>>({});

type Tab = 'ldap' | 'local' | 'oidc' | 'proxy-header' | 'windows' | 'pam';
const TABS: Tab[] = ['ldap', 'local', 'oidc', 'proxy-header', 'windows', 'pam'];
const fromAddress = String(route.query.tab ?? '') as Tab;
const activeTab = ref<Tab>(TABS.includes(fromAddress) ? fromAddress : 'ldap');
function setTab(tab: Tab) {
  activeTab.value = tab;
  void router.replace({ query: { ...route.query, tab } });
}

/** The driver a provider runs: `ldap` for every LDAP instance. */
function kindOf(p: AuthProvider): string {
  return p.driver || p.id;
}
function tabOf(p: AuthProvider): Tab | null {
  const k = kindOf(p);
  return (TABS as string[]).includes(k) ? (k as Tab) : null;
}
const cards = computed(() => items.value.filter((p) => tabOf(p) === activeTab.value));
function tabRunning(tab: Tab): boolean {
  return items.value.some((p) => tabOf(p) === tab && p.status === 'ok');
}

/** A card's title: the instance's own name, else the kind's. */
function titleOf(p: AuthProvider): string {
  const own = p.label?.trim() || (p.id !== kindOf(p) ? p.id : '');
  return own || t(`authProviders.providers.${kindOf(p)}` as never);
}
function cfg(p: AuthProvider): Record<string, unknown> {
  return (p.config_redacted ?? {}) as Record<string, unknown>;
}
function val(p: AuthProvider, k: string): string {
  const v = cfg(p)[k];
  return v == null ? '' : String(v).trim();
}
const stateTone = (s: AuthProvider['status']) => (s === 'ok' ? 'emerald' : s === 'misconfigured' ? 'rose' : 'zinc');

/** What a card says a provider reaches, a line or two, by kind. */
function summary(p: AuthProvider): string[] {
  switch (kindOf(p)) {
    case 'ldap':
      return [
        [val(p, 'url'), val(p, 'base_dn')].filter(Boolean).join(' · '),
        val(p, 'email_domains') ? t('authProviders.card.domains', { list: val(p, 'email_domains') }) : t('authProviders.card.anyDomain'),
      ].filter(Boolean);
    case 'oidc':
      return [val(p, 'issuer'), val(p, 'client_id') ? t('authProviders.card.client', { id: val(p, 'client_id') }) : ''].filter(Boolean);
    case 'proxy-header':
      return [val(p, 'trusted_proxies') ? t('authProviders.card.proxies', { list: val(p, 'trusted_proxies') }) : ''].filter(Boolean);
    case 'local':
      return [p.enabled ? t('authProviders.card.local') : t('authProviders.card.localOff')];
  }
  return [];
}

/** "every 6 hr" / "on request only", in the viewer's language. */
function schedule(s?: DirectorySyncStatus): string {
  const sec = s?.interval_seconds ?? 0;
  if (!sec) return t('authProviders.card.manualOnly');
  return t('authProviders.card.every', { every: formatInterval(sec, locale.value) });
}

async function load() {
  loading.value = true;
  try {
    const o = await AuthProvidersApi.overview();
    items.value = o.providers;
    recoveryLogin.value = o.recoveryLogin;
    secretKey.value = o.secretKey;
    reviewPending.value = o.reviewPending === true;
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  } finally {
    loading.value = false;
  }
  await Promise.all(items.value.filter((p) => kindOf(p) === 'ldap' && p.state === 'running').map((p) => loadSync(p.id)));
}

const timers: Record<string, ReturnType<typeof setTimeout>> = {};
async function loadSync(name: string) {
  try {
    const s = await AuthProvidersApi.syncStatus(name);
    syncs[name] = s;
    if (s.running) timers[name] = setTimeout(() => loadSync(name), 2000);
  } catch {
    // An older server has no sync: the card says nothing about it.
  }
}
onBeforeUnmount(() => Object.values(timers).forEach(clearTimeout));

async function syncNow(p: AuthProvider) {
  try {
    await AuthProvidersApi.syncStart(p.id);
    await loadSync(p.id);
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

async function dismissReview() {
  try {
    await AuthProvidersApi.dismissReview();
    reviewPending.value = false;
  } catch (e) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

const passwordEnv = computed(() => items.value.find((p) => p.id === 'local' && p.enabled) ?? null);

// ── another provider of a kind: made switched off, then filled in on its page ──
const KINDS = ['ldap', 'oidc', 'proxy-header', 'windows', 'pam'];
const adding = ref(false);
const addKind = ref('ldap');
const addSlug = ref('');
const addLabel = ref('');
const addError = ref('');
const addSaving = ref(false);
const kindOptions = computed(() => KINDS.map((k) => ({ value: k, label: t(`authProviders.providers.${k}` as never) })));
function openAdd() {
  addKind.value = KINDS.includes(activeTab.value) ? activeTab.value : 'ldap';
  addSlug.value = '';
  addLabel.value = '';
  addError.value = '';
  adding.value = true;
}
async function createProvider() {
  addSaving.value = true;
  addError.value = '';
  try {
    const made = await AuthProvidersApi.create({
      driver: addKind.value,
      slug: addSlug.value.trim() || undefined,
      label: addLabel.value.trim() || undefined,
      enabled: false,
    });
    adding.value = false;
    toast.success(t('authProviders.created'));
    if (made?.id) {
      void router.push({ name: 'auth-providers.edit', params: { name: made.id } });
    } else {
      await load();
    }
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

onMounted(load);
</script>

<template>
  <div class="space-y-4">
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
      class="max-w-3xl rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200 flex items-start justify-between gap-3"
      role="status"
      data-testid="auth-providers-review"
    >
      <p>{{ t('authProviders.review') }}</p>
      <Button size="sm" variant="outline" data-testid="auth-providers-review-done" @click="dismissReview">
        {{ t('authProviders.reviewDone') }}
      </Button>
    </div>

    <!-- What this page can and cannot do to sign-in - said before anything
         is changed, because it is why a mistake here cannot lock anyone out. -->
    <div class="card card-body flex items-start gap-3 text-sm max-w-3xl" data-testid="auth-providers-lockout-note">
      <ShieldCheck class="h-5 w-5 shrink-0 text-emerald-600" aria-hidden="true" />
      <div class="space-y-1">
        <p>{{ t('authProviders.lockoutNote') }}</p>
        <p v-if="passwordEnv" class="text-zinc-500">{{ t('authProviders.passwordOn', { from: passwordEnv.from }) }}</p>
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
      class="max-w-3xl rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-200"
      role="status"
      data-testid="auth-providers-no-secret-key"
    >
      <i18n-t keypath="authProviders.noSecretKey" tag="span"><template #env><code>FILEX_SECRET_KEY</code></template></i18n-t>
    </p>

    <div v-if="loading" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <template v-else>
      <nav class="flex flex-wrap gap-1 border-b border-zinc-200 dark:border-zinc-800" role="tablist" data-testid="auth-provider-tabs">
        <button
          v-for="tab in TABS"
          :key="tab"
          type="button"
          role="tab"
          :aria-selected="activeTab === tab"
          :data-testid="`auth-tab-${tab}`"
          class="-mb-px inline-flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium transition"
          :class="activeTab === tab
            ? 'border-brand-600 text-brand-600 dark:border-brand-400 dark:text-brand-400'
            : 'border-transparent text-zinc-500 hover:text-zinc-900 dark:text-zinc-400 dark:hover:text-zinc-100'"
          @click="setTab(tab)"
        >
          <span
            class="h-1.5 w-1.5 rounded-full"
            :class="tabRunning(tab) ? 'bg-emerald-500' : 'bg-zinc-300 dark:bg-zinc-600'"
            :title="tabRunning(tab) ? t('authProviders.status.ok') : t('authProviders.status.disabled')"
            aria-hidden="true"
          />
          {{ t(`authProviders.tabs.${tab}` as never) }}
        </button>
      </nav>

      <p v-if="!cards.length" class="card card-body text-sm text-zinc-500" data-testid="auth-provider-none">
        {{ t('authProviders.noneOfKind') }}
      </p>
      <ul v-else class="grid gap-3 lg:grid-cols-2" role="tabpanel" data-testid="auth-provider-cards">
        <!-- ⚠ Sync now is NOT inside the link: a button in an <a> is invalid
             HTML, and a screen reader reads the whole card as one link that
             both opens the provider and syncs it. The link holds every word
             of the card; the button sits under it, in the same card. -->
        <li
          v-for="p in cards"
          :key="p.id"
          class="card min-w-0 h-full flex flex-col hover:bg-[var(--fe-bg-hover)] transition-colors"
          :class="p.status === 'misconfigured' || syncs[p.id]?.last?.error ? 'border-rose-300 dark:border-rose-800' : ''"
        >
          <RouterLink
            :to="{ name: 'auth-providers.edit', params: { name: p.id } }"
            class="flex flex-1 flex-col gap-2 px-4 py-3 rounded-[inherit]"
            :data-testid="`auth-card-${p.id}`"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="flex flex-wrap items-center gap-2 min-w-0">
                <span class="font-medium truncate">{{ titleOf(p) }}</span>
                <Badge :tone="stateTone(p.status)" dot size="xs" :data-testid="`auth-card-state-${p.id}`">
                  {{ t(`authProviders.status.${p.status}` as never) }}
                </Badge>
                <Badge v-if="p.origin === 'environment'" tone="zinc" size="xs">
                  <Lock class="h-3 w-3" aria-hidden="true" />
                  {{ t('authProviders.origin.environment') }}
                </Badge>
              </div>
              <ChevronRight class="h-4 w-4 shrink-0 mt-0.5 text-zinc-400 rtl:rotate-180" />
            </div>

            <p v-for="(line, i) in summary(p)" :key="i" class="text-xs text-zinc-600 dark:text-zinc-300 truncate font-mono" :title="line">{{ line }}</p>
            <p v-if="p.last_error" class="text-xs text-rose-600 break-all">{{ t('authProviders.failedReason', { reason: p.last_error }) }}</p>
            <p v-if="multiTenant && p.tenants" class="text-xs text-zinc-500" :data-testid="`auth-card-tenants-${p.id}`">
              {{ t('authProviders.card.tenants', { n: p.tenants.length }, p.tenants.length) }}
            </p>

            <template v-if="kindOf(p) === 'ldap' && syncs[p.id]">
              <div class="mt-auto pt-1 text-xs text-zinc-500 space-y-0.5 min-w-0" :data-testid="`auth-card-sync-${p.id}`">
                <p>
                  <template v-if="syncs[p.id]!.running">{{ t('authProviders.sync.running') }}</template>
                  <template v-else-if="syncs[p.id]!.last">{{ t('authProviders.card.synced', { when: formatRelative(syncs[p.id]!.last!.finished_at || syncs[p.id]!.last!.started_at, locale) }) }}</template>
                  <template v-else>{{ t('authProviders.card.neverSynced') }}</template>
                  · {{ schedule(syncs[p.id]) }}
                </p>
                <p v-if="syncs[p.id]!.last && !syncs[p.id]!.last!.error">
                  {{ t('authProviders.sync.found', { n: syncs[p.id]!.last!.found }, syncs[p.id]!.last!.found) }}
                  <template v-if="syncs[p.id]!.last!.groups_found"> · {{ t('authProviders.sync.groupsFound', { n: syncs[p.id]!.last!.groups_found }, syncs[p.id]!.last!.groups_found) }}</template>
                  <template v-if="syncs[p.id]!.last!.missing"> · {{ t('authProviders.sync.missing', { n: syncs[p.id]!.last!.missing }, syncs[p.id]!.last!.missing) }}</template>
                </p>
                <p v-else-if="syncs[p.id]!.last?.error" class="text-rose-600 truncate" :title="syncs[p.id]!.last!.error">{{ syncs[p.id]!.last!.error }}</p>
              </div>
            </template>
          </RouterLink>
          <div v-if="kindOf(p) === 'ldap' && syncs[p.id]" class="flex justify-end px-4 pb-3">
            <Button
              variant="outline"
              size="sm"
              class="whitespace-nowrap"
              :loading="syncs[p.id]!.running"
              :disabled="syncs[p.id]!.running"
              :data-testid="`auth-card-syncnow-${p.id}`"
              @click="syncNow(p)"
            >
              <RefreshCw class="h-4 w-4" /> {{ t('authProviders.sync.now') }}
            </Button>
          </div>
        </li>
      </ul>
    </template>

    <!-- Another provider of a kind: made switched off, then filled in on its page. -->
    <Modal v-model="adding" :title="t('authProviders.addTitle')" size="sm">
      <form class="space-y-3" data-testid="auth-provider-add-form" @submit.prevent="createProvider">
        <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ multiTenant ? t('authProviders.addHint') : t('authProviders.addHintSingle') }}</p>
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
