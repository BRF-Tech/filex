<script setup lang="ts">
// Sign-in security — the administrator's side of the sign-in attempt limit:
// its numbers, the addresses that are exempt, the proxies whose word about a
// client's address is believed, the locks in force and the recent trail.
//
// The trusted proxies default to `auto` (filex works out where it runs): the
// page shows what it resolved to and why, keeps the class switches and the
// address list as manual settings, and names every peer that sends a
// forwarded address without being trusted, with a one-click "trust it" that
// keeps the list's own words (`auto` stays `auto`).
//
// ⚠ Instance-wide. One set of settings for every tenant, so a tenant's
// administrator gets the server's `supertenant_only` sentence and no form.
//
// ⚠ Nothing here is checked by guessing: the bounds come from the server's
// `limits`, an entry's validity is the server's answer (400 `invalid_setting`
// with the field that was wrong), and a raw wire value (an audit reason, a
// door) is never printed — an unknown one says "Other".
//
// ⚠ On a public demo the server writes "hidden on the demo" in place of every
// address (handlers/demo_redact.go): the page says it in the reader's
// language (shownAddress), keys the locks by their `id` - two masked
// addresses read alike and are still two rows - and offers no action on a
// masked row, which names nothing that could be unlocked.
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import axios from 'axios';
import { RefreshCcw, Save, ShieldAlert } from 'lucide-vue-next';

import {
  LoginSecurityApi,
  trustedProxyList,
  type LoginAttempt,
  type LoginLock,
  type LoginScope,
  type LoginSecurityNumberField,
  type LoginSecurityPatch,
  type LoginSecurityState,
  type TrustedClasses,
  type UntrustedForwarder,
} from '@/api/loginSecurity';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { DEMO_MASKED_ADDRESS, formatDate, formatDuration, shownAddress } from '@/lib/format';

import Button from '@/components/ui/Button.vue';
import Input from '@/components/ui/Input.vue';
import Select from '@/components/ui/Select.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Checkbox from '@/components/ui/Checkbox.vue';
import Badge from '@/components/ui/Badge.vue';
import Spinner from '@/components/ui/Spinner.vue';
import AddressListEditor from '@/components/loginSecurity/AddressListEditor.vue';
import AutoResolution from '@/components/loginSecurity/AutoResolution.vue';
import UntrustedForwarders from '@/components/loginSecurity/UntrustedForwarders.vue';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

const { t, locale } = useI18n();
const toast = useToastStore();

const NUMBER_FIELDS: LoginSecurityNumberField[] = [
  'account_max_fails',
  'ip_max_fails',
  'window_seconds',
  'lock_base_seconds',
  'lock_max_seconds',
];
const IS_SECONDS = new Set<LoginSecurityNumberField>(['window_seconds', 'lock_base_seconds', 'lock_max_seconds']);
/** The trail's events and their catalogue keys (a dot is a path separator there):
 *  the limiter's four, and a change to these settings. */
const ACTION_KEYS: Record<string, string> = {
  'login.failed': 'failed',
  'login.locked': 'locked',
  'login.unlocked': 'unlocked',
  'login.allowlist_pass': 'allowlist_pass',
  'login_security.update': 'settings_changed',
};
const ACTIONS = Object.keys(ACTION_KEYS);
const REASONS = ['invalid_credentials', 'invalid_totp', 'expired', 'admin'];
const DOORS = ['web', 'dav', 'ftp', 'sftp', 's3'];
/** The doors an ADMINISTRATOR's row came through (`via`): a session, an admin API key, an MCP tool. */
const VIAS = ['panel', 'api', 'mcp'];

/** An address as shown: the demo's mask in the reader's language. */
const addr = (v: string | undefined) => shownAddress(v, t('demo.hiddenAddress'));
/** A value that is an address only when masked (a lock's subject, an identifier typed as one). */
const unmasked = (v: string | undefined) => (v === DEMO_MASKED_ADDRESS ? t('demo.hiddenAddress') : (v ?? ''));
const isMasked = (v: string | undefined) => v === DEMO_MASKED_ADDRESS;

/* ── settings ────────────────────────────────────────────────────────────── */

const loading = ref(true);
const loadError = ref<string | null>(null);
const state = ref<LoginSecurityState | null>(null);

const form = reactive<Record<LoginSecurityNumberField, number | null> & { enabled: boolean }>({
  enabled: true,
  account_max_fails: null,
  ip_max_fails: null,
  window_seconds: null,
  lock_base_seconds: null,
  lock_max_seconds: null,
});
const allowDraft = ref<string[]>([]);
/** The trusted proxies as edited: the automatic switch, the three class
 *  switches and the addresses written out - all started from the list IN
 *  FORCE, whichever source it came from, so what the page shows is what filex
 *  believes right now. */
const proxyClasses = reactive<TrustedClasses>({ auto: false, loopback: false, private: false, link_local: false });
const proxyDraft = ref<string[]>([]);
const fieldError = reactive<Record<string, string | null>>({});
const saving = reactive({ limit: false, allowlist: false, proxies: false });

const NO_CLASSES: TrustedClasses = { auto: false, loopback: false, private: false, link_local: false };
/** The three classes of address, one switch each (the automatic switch stands apart). */
const CLASS_KEYS: ('loopback' | 'private' | 'link_local')[] = ['loopback', 'private', 'link_local'];
const ALL_KEYS: (keyof TrustedClasses)[] = ['auto', ...CLASS_KEYS];

function adopt(s: LoginSecurityState) {
  state.value = s;
  form.enabled = s.settings.enabled;
  for (const f of NUMBER_FIELDS) form[f] = s.settings[f];
  allowDraft.value = [...s.settings.ip_allowlist];
  Object.assign(proxyClasses, { ...NO_CLASSES, ...(s.trusted_defaults ?? {}) });
  proxyDraft.value = [...(s.trusted_addresses ?? [])];
}

async function loadState() {
  loading.value = true;
  loadError.value = null;
  try {
    adopt(await LoginSecurityApi.get());
  } catch (e: unknown) {
    state.value = null;
    loadError.value = extractError(e, t('errors.generic'));
  } finally {
    loading.value = false;
  }
}

const limitLabels = computed<Record<LoginSecurityNumberField, string>>(() => ({
  account_max_fails: t('loginSecurity.limit.accountMaxFails'),
  ip_max_fails: t('loginSecurity.limit.ipMaxFails'),
  window_seconds: t('loginSecurity.limit.windowSeconds'),
  lock_base_seconds: t('loginSecurity.limit.lockBaseSeconds'),
  lock_max_seconds: t('loginSecurity.limit.lockMaxSeconds'),
}));

function limitOf(f: LoginSecurityNumberField) {
  return state.value?.limits[f] ?? { min: 1, max: 1_000_000, default: 0 };
}

/** The line under a number box: its bounds and default — and, for a length of
 *  time, what the number is in words ("10 min"). */
function hintOf(f: LoginSecurityNumberField): string {
  const l = limitOf(f);
  if (!IS_SECONDS.has(f)) return t('loginSecurity.limit.hintAttempts', { min: l.min, max: l.max, default: l.default });
  const shown = form[f];
  const human = typeof shown === 'number' ? formatDuration(shown, locale.value) : '-';
  return t('loginSecurity.limit.hintSeconds', {
    human,
    min: l.min,
    max: l.max,
    default: l.default,
  });
}

/** The server's word about which field was wrong, else the sentence alone. */
function failField(e: unknown): { field: string | null; message: string } {
  const data = axios.isAxiosError(e) ? (e.response?.data as { field?: unknown } | undefined) : undefined;
  return {
    field: typeof data?.field === 'string' ? data.field : null,
    message: extractError(e, t('errors.generic')),
  };
}

async function saveLimit() {
  for (const f of NUMBER_FIELDS) fieldError[f] = null;
  let bad = false;
  for (const f of NUMBER_FIELDS) {
    const v = form[f];
    const l = limitOf(f);
    if (typeof v !== 'number' || !Number.isInteger(v) || v < l.min || v > l.max) {
      fieldError[f] = t('loginSecurity.limit.errRange', { min: l.min, max: l.max });
      bad = true;
    }
  }
  if (!bad && (form.lock_max_seconds as number) < (form.lock_base_seconds as number)) {
    fieldError.lock_max_seconds = t('loginSecurity.limit.errOrder');
    bad = true;
  }
  if (bad) return;
  saving.limit = true;
  try {
    const patch: LoginSecurityPatch = { enabled: form.enabled };
    for (const f of NUMBER_FIELDS) patch[f] = form[f] as number;
    adopt(await LoginSecurityApi.update(patch));
    toast.success(t('loginSecurity.limit.saved'));
  } catch (e: unknown) {
    const { field, message } = failField(e);
    if (field && (NUMBER_FIELDS as string[]).includes(field)) fieldError[field] = message;
    else toast.error(message);
  } finally {
    saving.limit = false;
  }
}

async function saveAllowlist(list: string[] = allowDraft.value) {
  fieldError.ip_allowlist = null;
  saving.allowlist = true;
  try {
    adopt(await LoginSecurityApi.update({ ip_allowlist: list }));
    toast.success(t('loginSecurity.allowlist.saved'));
  } catch (e: unknown) {
    fieldError.ip_allowlist = failField(e).message;
  } finally {
    saving.allowlist = false;
  }
}

/** One click: the address this request comes from joins the list, saved. */
function addMine() {
  const ip = state.value?.your_ip;
  if (!ip) return;
  const next = allowDraft.value.includes(ip) ? allowDraft.value : [...allowDraft.value, ip];
  allowDraft.value = next;
  void saveAllowlist(next);
}

/** Saves the switches and the addresses as the `login.trusted_proxies` setting
 *  (`list` = [] removes the saved list: the environment's, or the default,
 *  is in force again). */
async function saveProxies(list: string[] = trustedProxyList(proxyClasses, proxyDraft.value)) {
  fieldError.trusted_proxies = null;
  saving.proxies = true;
  try {
    adopt(await LoginSecurityApi.update({ trusted_proxies: list }));
    toast.success(t(list.length ? 'loginSecurity.proxies.saved' : 'loginSecurity.proxies.resetDone'));
  } catch (e: unknown) {
    fieldError.trusted_proxies = failField(e).message;
  } finally {
    saving.proxies = false;
  }
}

function resetProxies() {
  if (!window.confirm(t('loginSecurity.proxies.resetConfirm'))) return;
  void saveProxies([]);
}

/** One click: a peer that sends forwarded addresses joins the list IN FORCE -
 *  its words kept, so a default `auto` is saved as "auto, <address>" - after a
 *  question that names the address and what trusting a stranger costs. */
async function trustForwarder(f: UntrustedForwarder) {
  const s = state.value;
  if (!s) return;
  const lines = [t('loginSecurity.proxies.forwarders.confirm', { address: f.address })];
  if (f.public) lines.push(t('loginSecurity.proxies.forwarders.confirmPublic', { address: f.address }));
  if (f.relay) lines.push(t('loginSecurity.proxies.forwarders.confirmRelay', { address: f.address }));
  if (!window.confirm(lines.join('\n\n'))) return;
  const inForce = { ...NO_CLASSES, ...(s.trusted_defaults ?? {}) };
  const addresses = s.trusted_addresses ?? [];
  const list = trustedProxyList(inForce, addresses.includes(f.address) ? addresses : [...addresses, f.address]);
  fieldError.trusted_proxies = null;
  saving.proxies = true;
  try {
    adopt(await LoginSecurityApi.update({ trusted_proxies: list }));
    toast.success(t('loginSecurity.proxies.forwarders.trusted', { address: f.address }));
  } catch (e: unknown) {
    fieldError.trusted_proxies = failField(e).message;
  } finally {
    saving.proxies = false;
  }
}

const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((v, i) => v === b[i]);
const allowDirty = computed(() => !!state.value && !sameList(allowDraft.value, state.value.settings.ip_allowlist));
const proxyDirty = computed(() => {
  const s = state.value;
  if (!s) return false;
  const inForce = { ...NO_CLASSES, ...(s.trusted_defaults ?? {}) };
  return ALL_KEYS.some((k) => proxyClasses[k] !== inForce[k]) || !sameList(proxyDraft.value, s.trusted_addresses ?? []);
});
/** Nothing switched on and nothing listed: no proxy is believed. */
const trustsNobody = computed(() => ALL_KEYS.every((k) => !proxyClasses[k]) && proxyDraft.value.length === 0);
const forwarders = computed(() => state.value?.untrusted_forwarders ?? []);

const SOURCES = ['setting', 'env', 'auto'];
const sourceText = computed(() => {
  const s = state.value?.trusted_proxies_source;
  return s && SOURCES.includes(s) ? t(`loginSecurity.proxies.source.${s}`) : '';
});
const classLabels = computed<Record<keyof TrustedClasses, { label: string; desc: string }>>(() => ({
  auto: { label: t('loginSecurity.proxies.class.auto'), desc: t('loginSecurity.proxies.class.autoHint') },
  loopback: { label: t('loginSecurity.proxies.class.loopback'), desc: t('loginSecurity.proxies.class.loopbackHint') },
  private: { label: t('loginSecurity.proxies.class.private'), desc: t('loginSecurity.proxies.class.privateHint') },
  link_local: { label: t('loginSecurity.proxies.class.linkLocal'), desc: t('loginSecurity.proxies.class.linkLocalHint') },
}));

/* ── locks ───────────────────────────────────────────────────────────────── */

const locks = ref<LoginLock[]>([]);
const locksLoading = ref(false);
const lockScope = ref<'' | LoginScope>('');
const onlyLocked = ref(false);
const lockedNow = computed(() => locks.value.filter((l) => l.locked).length);

async function loadLocks() {
  locksLoading.value = true;
  try {
    locks.value = await LoginSecurityApi.locks({
      scope: lockScope.value || undefined,
      locked: onlyLocked.value,
    });
  } catch (e: unknown) {
    if (state.value) toast.error(extractError(e, t('errors.loadFailed')));
  } finally {
    locksLoading.value = false;
  }
}
watch([lockScope, onlyLocked], loadLocks);

const scopeOptions = computed(() => [
  { value: '', label: t('common.all') },
  { value: 'account', label: t('loginSecurity.locks.scope.account') },
  { value: 'ip', label: t('loginSecurity.locks.scope.ip') },
]);

function scopeLabel(s: string): string {
  return s === 'account' || s === 'ip' ? t(`loginSecurity.locks.scope.${s}`) : t('loginSecurity.attempts.reason.other');
}

/** The row's id from the server (unique even when two masked subjects read alike). */
function lockKey(l: LoginLock): string {
  return l.id || `${l.scope}:${l.subject}`;
}

async function unlockOne(l: LoginLock) {
  if (!window.confirm(t('loginSecurity.locks.unlockConfirm', { subject: l.subject }))) return;
  try {
    const n = await LoginSecurityApi.unlock(l.scope, l.subject);
    toast.success(t('loginSecurity.locks.unlocked', { n }));
    await Promise.all([loadLocks(), loadAttempts()]);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

async function unlockEverything() {
  if (!window.confirm(t('loginSecurity.locks.unlockAllConfirm'))) return;
  try {
    const n = await LoginSecurityApi.unlockAll();
    toast.success(t('loginSecurity.locks.unlocked', { n }));
    await Promise.all([loadLocks(), loadAttempts()]);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.generic')));
  }
}

const lockColumns = computed<DataColumn<LoginLock>[]>(() => [
  { id: 'subject', label: t('loginSecurity.locks.columns.subject'), sortable: true, width: 220, lead: true },
  {
    id: 'scope',
    label: t('loginSecurity.locks.columns.scope'),
    sortable: true,
    width: 110,
    sortValue: (l) => scopeLabel(l.scope),
  },
  {
    id: 'fails',
    label: t('loginSecurity.locks.columns.fails'),
    sortable: true,
    width: 160,
    format: (l) => t('loginSecurity.locks.attempts', { fails: l.fails, limit: l.limit }),
    sortValue: (l) => l.fails,
  },
  {
    id: 'status',
    label: t('loginSecurity.locks.columns.status'),
    sortable: true,
    sortDir: 'desc',
    width: 200,
    sortValue: (l) => (l.locked ? l.retry_after : -1),
  },
  {
    id: 'lock_level',
    label: t('loginSecurity.locks.columns.step'),
    sortable: true,
    width: 120,
    align: 'right',
  },
  {
    id: 'last_fail_at',
    label: t('loginSecurity.locks.columns.last'),
    sortable: true,
    width: 210,
    format: (l) => formatDate(l.last_fail_at, locale.value),
    sortValue: (l) => (l.last_fail_at ? Date.parse(l.last_fail_at) : null),
  },
  {
    id: 'last_protocol',
    label: t('loginSecurity.locks.columns.door'),
    sortable: true,
    width: 110,
    format: (l) => doorLabel(l.last_protocol),
  },
]);

/** A masked row names no address an unlock could take: it has no action. */
function lockActions(l: LoginLock): ContextAction[] {
  if (isMasked(l.subject)) return [];
  return [{ key: 'unlock', label: t('loginSecurity.locks.unlock'), icon: 'lock' }];
}

function onLockAction(key: string, row: LoginLock) {
  if (key === 'unlock') void unlockOne(row);
}

/* ── the trail ───────────────────────────────────────────────────────────── */

const attempts = ref<LoginAttempt[]>([]);
const attemptsTotal = ref(0);
const attemptsLoading = ref(false);
const attemptAction = ref('');
const page = ref(1);
const pageSize = 50;

async function loadAttempts() {
  attemptsLoading.value = true;
  try {
    const res = await LoginSecurityApi.attempts({
      action: attemptAction.value || undefined,
      limit: pageSize,
      offset: (page.value - 1) * pageSize,
    });
    attempts.value = res.items;
    attemptsTotal.value = res.total;
  } catch (e: unknown) {
    if (state.value) toast.error(extractError(e, t('errors.loadFailed')));
  } finally {
    attemptsLoading.value = false;
  }
}
watch(attemptAction, () => {
  page.value = 1;
  void loadAttempts();
});

const actionOptions = computed(() => [
  { value: '', label: t('common.all') },
  ...ACTIONS.map((a) => ({ value: a, label: t(`loginSecurity.attempts.action.${ACTION_KEYS[a]}`) })),
]);

function actionLabel(a: string): string {
  return ACTION_KEYS[a] ? t(`loginSecurity.attempts.action.${ACTION_KEYS[a]}`) : t('loginSecurity.attempts.reason.other');
}
function actionTone(a: string): 'amber' | 'rose' | 'emerald' | 'sky' | 'violet' | 'zinc' {
  const tones: Record<string, 'amber' | 'rose' | 'emerald' | 'sky' | 'violet'> = {
    'login.failed': 'amber',
    'login.locked': 'rose',
    'login.unlocked': 'emerald',
    'login.allowlist_pass': 'sky',
    'login_security.update': 'violet',
  };
  return tones[a] ?? 'zinc';
}
function reasonLabel(r: string | undefined): string {
  if (!r) return '-';
  return REASONS.includes(r) ? t(`loginSecurity.attempts.reason.${r}`) : t('loginSecurity.attempts.reason.other');
}
function doorLabel(p: string | undefined): string {
  if (!p) return '-';
  return DOORS.includes(p) ? t(`loginSecurity.attempts.door.${p}`) : t('loginSecurity.attempts.door.other');
}
function viaLabel(v: string): string {
  return VIAS.includes(v) ? t(`loginSecurity.attempts.via.${v}`) : t('loginSecurity.attempts.via.other');
}
/** The Door column: where a password was typed - or, for an administrator's
 *  row, which door the administrator used. */
function doorOf(a: LoginAttempt): string {
  if (a.protocol) return doorLabel(a.protocol);
  return a.via ? viaLabel(a.via) : '-';
}
/** A settings field by the name its form gives it. */
function fieldLabel(f: string): string {
  if ((NUMBER_FIELDS as string[]).includes(f)) return limitLabels.value[f as LoginSecurityNumberField];
  const named: Record<string, string> = {
    enabled: 'loginSecurity.limit.enabled',
    ip_allowlist: 'loginSecurity.allowlist.title',
    trusted_proxies: 'loginSecurity.proxies.title',
  };
  return named[f] ? t(named[f]) : t('loginSecurity.attempts.reason.other');
}
/** The Reason column: why an attempt failed or a lock ended - or what a settings change changed. */
function reasonOf(a: LoginAttempt): string {
  if (a.action === 'login_security.update') {
    const fields = [...new Set((a.changed_fields ?? []).map(fieldLabel))];
    return fields.length ? t('loginSecurity.attempts.changed', { fields: fields.join(', ') }) : '-';
  }
  return reasonLabel(a.reason);
}
/** The Account column; an unlock of everything says so. */
function identifierOf(a: LoginAttempt): string {
  if (a.metadata?.all === true) {
    const n = typeof a.metadata.unlocked === 'number' ? a.metadata.unlocked : 0;
    return t('loginSecurity.attempts.allLocks', { n });
  }
  return a.identifier ? unmasked(a.identifier) : '-';
}

const attemptColumns = computed<DataColumn<LoginAttempt>[]>(() => [
  {
    id: 'at',
    label: t('loginSecurity.attempts.columns.at'),
    sortable: true,
    sortDir: 'desc',
    width: 170,
    format: (a) => formatDate(a.at, locale.value),
    sortValue: (a) => (a.at ? Date.parse(a.at) : null),
  },
  {
    id: 'action',
    label: t('loginSecurity.attempts.columns.action'),
    sortable: true,
    width: 170,
    sortValue: (a) => actionLabel(a.action),
  },
  {
    id: 'identifier',
    label: t('loginSecurity.attempts.columns.identifier'),
    sortable: true,
    width: 220,
    format: identifierOf,
  },
  { id: 'ip', label: t('loginSecurity.attempts.columns.ip'), sortable: true, width: 150, format: (a) => addr(a.ip) || '-' },
  {
    id: 'protocol',
    label: t('loginSecurity.attempts.columns.door'),
    sortable: true,
    width: 110,
    format: doorOf,
  },
  {
    id: 'reason',
    label: t('loginSecurity.attempts.columns.reason'),
    sortable: true,
    width: 190,
    format: reasonOf,
  },
]);

/* ── page ────────────────────────────────────────────────────────────────── */

async function refresh() {
  await loadState();
  if (state.value) await Promise.all([loadLocks(), loadAttempts()]);
}

let timer: ReturnType<typeof setInterval> | undefined;
onMounted(async () => {
  await refresh();
  // A lock ends by itself, so what the table says goes stale on its own.
  timer = setInterval(() => {
    if (state.value && !document.hidden) void loadLocks();
  }, 15_000);
});
onBeforeUnmount(() => clearInterval(timer));

</script>

<template>
  <div class="space-y-4 max-w-5xl">
    <div class="flex items-end justify-between gap-4 flex-wrap">
      <div class="flex items-center gap-2">
        <ShieldAlert class="h-6 w-6 text-brand-600 dark:text-brand-400" />
        <div>
          <h1 class="text-xl font-semibold">{{ t('loginSecurity.title') }}</h1>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('loginSecurity.subtitle') }}</p>
        </div>
      </div>
      <Button variant="outline" size="sm" :loading="loading || locksLoading || attemptsLoading" @click="refresh">
        <RefreshCcw class="h-4 w-4" />
        {{ t('common.refresh') }}
      </Button>
    </div>

    <div v-if="loading && !state" class="card card-body text-center text-zinc-500"><Spinner /></div>

    <div
      v-else-if="loadError"
      class="space-y-3 rounded-xl border border-rose-300 bg-rose-50 p-4 text-sm text-rose-700 dark:border-rose-700/60 dark:bg-rose-900/20 dark:text-rose-200"
      data-testid="login-security-error"
    >
      <p>{{ loadError }}</p>
      <Button size="sm" variant="outline" @click="refresh">{{ t('common.refresh') }}</Button>
    </div>

    <template v-else-if="state">
      <!-- The numbers -->
      <form class="card card-body space-y-4" novalidate data-testid="login-limit-form" @submit.prevent="saveLimit">
        <div>
          <h2 class="text-base font-semibold">{{ t('loginSecurity.limit.title') }}</h2>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('loginSecurity.limit.desc') }}</p>
        </div>
        <Toggle
          v-model="form.enabled"
          name="login-throttle-enabled"
          :label="t('loginSecurity.limit.enabled')"
          :description="t('loginSecurity.limit.enabledHint')"
        />
        <div class="grid gap-x-4 gap-y-3 sm:grid-cols-2">
          <Input
            v-for="f in NUMBER_FIELDS"
            :key="f"
            :model-value="form[f]"
            type="number"
            :min="limitOf(f).min"
            :max="limitOf(f).max"
            :step="1"
            :name="`login-limit-${f}`"
            :label="limitLabels[f]"
            :hint="hintOf(f)"
            :error="fieldError[f]"
            :disabled="!form.enabled"
            @update:model-value="(v) => ((form[f] = v === '' || v == null ? null : Number(v)), (fieldError[f] = null))"
          />
        </div>
        <div class="flex justify-end">
          <Button type="submit" :loading="saving.limit">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <!-- Allowed addresses -->
      <form class="card card-body space-y-3" novalidate data-testid="login-allowlist-form" @submit.prevent="saveAllowlist()">
        <div>
          <h2 class="text-base font-semibold">{{ t('loginSecurity.allowlist.title') }}</h2>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('loginSecurity.allowlist.desc') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2 text-sm" data-testid="login-your-ip">
          <span class="text-zinc-500 dark:text-zinc-400">
            {{ t('loginSecurity.allowlist.yourAddress', { ip: state.your_ip || '-' }) }}
          </span>
          <Badge size="xs" :tone="state.your_ip_allowlisted ? 'emerald' : 'zinc'" data-testid="login-your-ip-state">
            {{ state.your_ip_allowlisted ? t('loginSecurity.allowlist.mineListed') : t('loginSecurity.allowlist.mineNotListed') }}
          </Badge>
          <Button
            v-if="state.your_ip && !state.your_ip_allowlisted"
            type="button"
            size="sm"
            variant="outline"
            :loading="saving.allowlist"
            data-testid="login-add-my-ip"
            @click="addMine"
          >
            {{ t('loginSecurity.allowlist.addMine', { ip: state.your_ip }) }}
          </Button>
        </div>
        <AddressListEditor
          v-model="allowDraft"
          :label="t('loginSecurity.allowlist.entry')"
          :placeholder="t('loginSecurity.allowlist.placeholder')"
          :error="fieldError.ip_allowlist"
          :empty="t('loginSecurity.allowlist.empty')"
          :remove-label="(entry: string) => t('loginSecurity.allowlist.remove', { entry })"
          test-id="login-allowlist"
        />
        <div class="flex justify-end">
          <Button type="submit" :loading="saving.allowlist" :disabled="!allowDirty">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <!-- Trusted proxies -->
      <form class="card card-body space-y-3" novalidate data-testid="login-proxies-form" @submit.prevent="saveProxies()">
        <div>
          <h2 class="text-base font-semibold">{{ t('loginSecurity.proxies.title') }}</h2>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('loginSecurity.proxies.desc') }}</p>
        </div>
        <UntrustedForwarders
          v-if="forwarders.length"
          :forwarders="forwarders"
          :total="state.untrusted_forwarders_total ?? forwarders.length"
          :busy="saving.proxies"
          @trust="trustForwarder"
        />
        <div class="flex flex-wrap items-center gap-2 text-sm" data-testid="login-proxies-effective">
          <span class="font-medium">{{ t('loginSecurity.proxies.inForce') }}</span>
          <Badge size="xs" tone="sky" data-testid="login-proxies-source">{{ sourceText }}</Badge>
        </div>
        <Toggle
          v-model="proxyClasses.auto"
          name="login-proxies-class-auto"
          :label="classLabels.auto.label"
          :description="classLabels.auto.desc"
        />
        <AutoResolution v-if="proxyClasses.auto && state.trusted_proxies_auto" :auto="state.trusted_proxies_auto" />
        <fieldset class="space-y-2" data-testid="login-proxies-classes">
          <legend class="mb-1 text-sm font-medium">{{ t('loginSecurity.proxies.classesTitle') }}</legend>
          <div class="grid gap-x-4 gap-y-3 sm:grid-cols-3">
            <Toggle
              v-for="k in CLASS_KEYS"
              :key="k"
              v-model="proxyClasses[k]"
              :name="`login-proxies-class-${k}`"
              :label="classLabels[k].label"
              :description="classLabels[k].desc"
            />
          </div>
        </fieldset>
        <AddressListEditor
          v-model="proxyDraft"
          :label="t('loginSecurity.proxies.entry')"
          :placeholder="t('loginSecurity.proxies.placeholder')"
          :hint="t('loginSecurity.proxies.addressesHint')"
          :error="fieldError.trusted_proxies"
          :empty="t('loginSecurity.proxies.noAddresses')"
          :remove-label="(entry: string) => t('loginSecurity.allowlist.remove', { entry })"
          test-id="login-proxies"
        />
        <p
          v-if="trustsNobody"
          class="rounded-lg border border-zinc-300 bg-zinc-50 px-3 py-2 text-sm text-zinc-700 dark:border-zinc-700 dark:bg-zinc-800/40 dark:text-zinc-200"
          data-testid="login-proxies-nobody"
        >
          {{ t('loginSecurity.proxies.trustsNobody') }}
        </p>
        <p
          class="rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-700/60 dark:bg-amber-900/20 dark:text-amber-200"
          data-testid="login-proxies-override"
        >
          {{ t('loginSecurity.proxies.override') }}
        </p>
        <div class="flex flex-wrap justify-end gap-2">
          <Button
            v-if="state.trusted_proxies_source === 'setting'"
            type="button"
            variant="outline"
            :disabled="saving.proxies"
            data-testid="login-proxies-reset"
            @click="resetProxies"
          >
            {{ t('loginSecurity.proxies.reset') }}
          </Button>
          <Button type="submit" :loading="saving.proxies" :disabled="!proxyDirty">
            <Save class="h-4 w-4" />
            {{ t('common.save') }}
          </Button>
        </div>
      </form>

      <!-- Locks -->
      <section class="space-y-2" data-testid="login-locks">
        <div>
          <h2 class="text-base font-semibold">{{ t('loginSecurity.locks.title') }}</h2>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('loginSecurity.locks.desc') }}</p>
        </div>
        <DataTable
          table-id="admin.loginLocks"
          :columns="lockColumns"
          :rows="locks"
          :loading="locksLoading"
          :empty="t('loginSecurity.locks.empty')"
          :row-key="lockKey"
          :row-actions="(row: LoginLock) => lockActions(row)"
          :row-actions-test-id="(row: LoginLock) => `login-lock-actions-${lockKey(row)}`"
          @row-action="(key: string, row: LoginLock) => onLockAction(key, row)"
        >
          <template #toolbar>
            <Select
              :model-value="lockScope"
              :options="scopeOptions"
              :aria-label="t('loginSecurity.locks.columns.scope')"
              size="sm"
              class="w-40"
              data-testid="login-lock-scope"
              @update:model-value="(v) => (lockScope = String(v ?? '') as '' | LoginScope)"
            />
            <Checkbox v-model="onlyLocked" :label="t('loginSecurity.locks.onlyLocked')" />
            <Button
              size="sm"
              variant="outline"
              :disabled="lockedNow === 0"
              data-testid="login-unlock-all"
              @click="unlockEverything"
            >
              {{ t('loginSecurity.locks.unlockAll') }}
            </Button>
          </template>

          <template #cell-scope="{ row }">
            <Badge size="xs" :tone="(row as LoginLock).scope === 'ip' ? 'sky' : 'violet'">{{
              scopeLabel((row as LoginLock).scope)
            }}</Badge>
          </template>
          <template #cell-subject="{ row }">
            <span
              :class="isMasked((row as LoginLock).subject) ? 'tbl-clamp italic text-zinc-500' : 'tbl-mono tbl-clamp'"
              :title="unmasked((row as LoginLock).subject)"
              >{{ unmasked((row as LoginLock).subject) }}</span
            >
          </template>
          <template #cell-status="{ row }">
            <Badge v-if="(row as LoginLock).locked" size="xs" tone="rose">{{
              t('loginSecurity.locks.status.locked', { time: formatDuration((row as LoginLock).retry_after, locale) })
            }}</Badge>
            <Badge v-else size="xs" tone="amber">{{ t('loginSecurity.locks.status.counting') }}</Badge>
          </template>
        </DataTable>
      </section>

      <!-- The trail -->
      <section class="space-y-2" data-testid="login-attempts">
        <div>
          <h2 class="text-base font-semibold">{{ t('loginSecurity.attempts.title') }}</h2>
          <p class="text-sm text-zinc-500 dark:text-zinc-400">{{ t('loginSecurity.attempts.desc') }}</p>
        </div>
        <DataTable
          table-id="admin.loginAttempts"
          :columns="attemptColumns"
          :rows="attempts"
          :loading="attemptsLoading"
          :empty="t('loginSecurity.attempts.empty')"
          :page="page"
          :page-size="pageSize"
          :total="attemptsTotal"
          row-key="id"
          @page="(p: number) => ((page = p), loadAttempts())"
        >
          <template #toolbar>
            <Select
              :model-value="attemptAction"
              :options="actionOptions"
              :aria-label="t('loginSecurity.attempts.filter')"
              size="sm"
              class="w-56"
              data-testid="login-attempt-filter"
              @update:model-value="(v) => (attemptAction = String(v ?? ''))"
            />
          </template>

          <template #cell-action="{ row }">
            <Badge size="xs" :tone="actionTone((row as LoginAttempt).action)">{{
              actionLabel((row as LoginAttempt).action)
            }}</Badge>
          </template>
        </DataTable>
      </section>
    </template>
  </div>
</template>
