<script setup lang="ts">
/**
 * AppPluginDetail — one installed app: what its manifest says, what it was
 * granted, its settings, its per-action overrides and its log.
 *
 * ⚠⚠ The SECTIONS of a page (views/AppPluginPage.vue), not a dialog. It was
 * a modal holding all of this — three tables and a live log in a popup that
 * scrolled inside itself — and the owner's rule for exactly that (said about
 * the Signatures screen) is that a screen that big is its own page with
 * sections (release-candidate sweep, 2026-09-21). The page draws the heading
 * and the way back; each section here is one card.
 *
 * Said in words, not keys (same sweep): the permissions are the sentences
 * the install review showed (`permission_rows`, one source — the server's
 * PermissionRows), not `engines:libreoffice` chips; the app's hidden actions
 * are not offered as switches (a hidden action is not a person's to switch
 * off; the server refuses the override too); the log's time is the
 * product's date format, not raw ISO.
 *
 *   • Settings are the manifest's `Field[]`, mapped by the ONE mapper every
 *     plugin surface uses (`storageFieldOf`) and drawn by the shared
 *     `StorageFields` — so an app's settings obey the surface rules here too:
 *     a `select` is a row of buttons, never a dropdown, and there is no
 *     collapsed "advanced" block, because the contract has no `advanced`.
 *     ⚠ NOT `StorageDriverFields`: that one is the storage DRIVER form, which
 *     is admin chrome of ours and keeps its dropdown and its advanced block.
 *     A secret comes back as `***`; sending `***` leaves it unchanged (the
 *     contract), so the form never has to know which values are real.
 *   • Overrides: enabled / admins-only per action, and an `applies` editor —
 *     `null` means "as the manifest says", anything else replaces the rule.
 *   • Locks: the files this app has frozen (`files:lock`). ⚠ A lock caps
 *     EVERY caller at viewer — the owner and the administrator included — so
 *     when an app dies mid-flow, or a signer walks away, an administrator
 *     lifting it here is the only way the document ever moves again. That is
 *     why the list lives beside the app rather than on a page of its own:
 *     "which app is holding my file" is the question, and the app is the
 *     answer.
 *   • Schedule (uyan:s1): only for an app that is woken once an hour. One
 *     line for the wake-up itself — when the next one is and what the last
 *     one decided — and a table of the work it asked for. Read-only: the
 *     schedule is the app's own, and an administrator watching it is not the
 *     same as an administrator editing it.
 *   • The log polls `?after=<next>` every 2 s while the drawer is open.
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { RouterLink } from 'vue-router';
import { ExternalLink, LockOpen, RotateCcw, Save } from 'lucide-vue-next';

import {
  AppPluginsApi,
  engineName,
  type AppPlugin,
  type AppPluginActionOverride,
  type AppPluginDetail,
  type AppPluginManifestAction,
  type AppPluginLogLine,
  type AppPluginLock,
  type AppPluginScheduleItem,
  type PluginApplies,
} from '@/api/appPlugins';
import { extractError } from '@/api/client';
import { useToastStore } from '@/stores/toast';
import { formatDate, formatDateFull } from '@/lib/format';
import { lockReasonText, pluginLabelOf, splitList, storageFieldOf, StorageFields } from '@brftech/filex-core';

import Button from '@/components/ui/Button.vue';
import Badge from '@/components/ui/Badge.vue';
import Toggle from '@/components/ui/Toggle.vue';
import Select from '@/components/ui/Select.vue';
import Input from '@/components/ui/Input.vue';
import ChipInput from '@/components/ui/ChipInput.vue';
import Modal from '@/components/ui/Modal.vue';
import { DataTable, type ContextAction, type DataColumn } from '@brftech/filex-core';

const props = defineProps<{
  plugin: AppPlugin | null;
  /** Engine id → the name a person reads (the list answer's `engine_names`). */
  engineNames?: Record<string, string>;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  /** Something that changes the list or the explorer's menu was saved. */
  (e: 'changed'): void;
}>();

const { t, locale } = useI18n();
const toast = useToastStore();

const detail = ref<AppPluginDetail | null>(null);
const loading = ref(false);

const settings = ref<Record<string, unknown>>({});
const savingSettings = ref(false);

/** One editable row per manifest action, seeded from the saved overrides. */
interface OverrideRow extends Record<string, unknown> {
  id: string;
  label: string;
  enabled: boolean;
  admin_only: boolean;
  /** null = manifest default. */
  applies: PluginApplies | null;
  manifest: PluginApplies;
}
const overrides = ref<OverrideRow[]>([]);
const savingOverrides = ref(false);

/** The files THIS app has frozen (the endpoint answers instance-wide). */
const locks = ref<AppPluginLock[]>([]);
const locksLoading = ref(false);
const unlocking = ref('');
const confirmUnlock = ref<AppPluginLock | null>(null);

const logs = ref<AppPluginLogLine[]>([]);
const logNext = ref(0);
let logTimer: ReturnType<typeof setInterval> | undefined;

const LOG_POLL_MS = 2000;
const LOG_CAP = 500;

function stopLogs() {
  if (logTimer) {
    clearInterval(logTimer);
    logTimer = undefined;
  }
}

async function pollLogs() {
  const p = props.plugin;
  if (!p) return;
  try {
    const res = await AppPluginsApi.logs(p.id, logNext.value);
    if (res.lines.length) logs.value = [...logs.value, ...res.lines].slice(-LOG_CAP);
    logNext.value = res.next;
  } catch {
    /* a missed poll is not an error worth a toast; the next tick retries */
  }
}

function startLogs() {
  stopLogs();
  logs.value = [];
  logNext.value = 0;
  void pollLogs();
  logTimer = setInterval(() => void pollLogs(), LOG_POLL_MS);
}

/**
 * Stored settings are a `map[string]string` on the wire (the contract) and
 * the controls hold TYPED values — so the strings are read back into the
 * shapes their fields declare, once, here.
 *
 * ⚠ Without this a stored `"true"` arrives at a `bool` as a string, which is
 * neither `true` nor `false` to the control: the toggle drew itself off for a
 * setting that was on, and a `multi` select would read its whole answer as
 * one unknown option. `saveSettings` is the same conversion backwards.
 */
function seedSettings(d: AppPluginDetail): Record<string, unknown> {
  const stored = d.settings ?? {};
  const out: Record<string, unknown> = { ...stored };
  for (const f of d.manifest.settings ?? []) {
    const v = stored[f.key];
    if (v === undefined) continue;
    if (f.type === 'bool') out[f.key] = v === 'true' || v === '1';
    else if (f.type === 'select' && f.multi === true) out[f.key] = splitMulti(v);
  }
  return out;
}

/** A `multi` select's answer, stored comma-separated (one string field).
 *  ⚠ Through the shared splitter (core lib/listInput), so a value typed with
 *  an Arabic "،" or a fullwidth "，" still splits. */
function splitMulti(v: string): string[] {
  return splitList(v);
}

function seedOverrides(d: AppPluginDetail): OverrideRow[] {
  const saved = new Map<string, AppPluginActionOverride>((d.overrides ?? []).map((o) => [o.id, o]));
  // ⚠ A hidden action is the app's machinery, not a menu row — it is not
  // listed here (and the server would drop its override anyway).
  return (d.manifest.actions ?? []).filter((a) => !a.hidden).map((a: AppPluginManifestAction) => {
    const o = saved.get(a.id);
    return {
      id: a.id,
      label: pluginLabelOf(a.label, locale.value) || a.id,
      enabled: o?.enabled ?? true,
      admin_only: o?.admin_only ?? false,
      applies: o?.applies ? { ...o.applies } : null,
      manifest: a.applies ?? {},
    };
  });
}

async function load() {
  const p = props.plugin;
  if (!p) return;
  loading.value = true;
  try {
    const d = await AppPluginsApi.get(p.id);
    detail.value = d;
    settings.value = seedSettings(d);
    overrides.value = seedOverrides(d);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.loadFailed')));
  } finally {
    loading.value = false;
  }
  void loadLocks();
}

/* ── locks ─────────────────────────────────────────────────────────────── */

async function loadLocks() {
  const p = props.plugin;
  if (!p) return;
  locksLoading.value = true;
  try {
    const all = await AppPluginsApi.locks();
    // ⚠ Filtered HERE by name, not by id: a lock names the app it belongs to
    // the way the listing badge does (`lock.plugin`), and the row id is the
    // install's, which the endpoint never returns.
    locks.value = all.filter((l) => l.plugin === p.name);
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.loadFailed')));
  } finally {
    locksLoading.value = false;
  }
}

function lockKey(l: AppPluginLock): string {
  return l.storage_id + ':' + l.path;
}

async function doUnlock(l: AppPluginLock) {
  confirmUnlock.value = null;
  unlocking.value = lockKey(l);
  try {
    await AppPluginsApi.unlock(l.storage_id, l.path);
    toast.success(t('appPlugins.detail.locks.lifted', { path: l.path }));
    await loadLocks();
    // The explorer caches the actions list, not the listing, so nothing else
    // has to be invalidated here — the next folder load carries the new perm.
    emit('changed');
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.actionFailed')));
  } finally {
    unlocking.value = '';
  }
}

watch(
  () => props.plugin,
  (p) => {
    detail.value = null;
    if (p) {
      void load();
      startLogs();
    } else {
      stopLogs();
    }
  },
  { immediate: true },
);

onBeforeUnmount(stopLogs);

/**
 * What the app was granted, in words: the rows the install review showed
 * (label = filex's sentence for the permission, reason = the app's own), for
 * exactly the permissions this install holds. A granted id the rows do not
 * cover (a manifest from before a permission had words) falls back to the id,
 * rather than vanishing.
 */
const grantedRows = computed(() => {
  const d = detail.value;
  if (!d) return [];
  const ids = d.granted ?? d.permissions ?? [];
  const rows = new Map((d.permission_rows ?? []).map((r) => [r.id, r]));
  return ids.map((id) => {
    const r = rows.get(id);
    return {
      id,
      label: r?.label || id,
      reason: pluginLabelOf(r?.reason, locale.value),
    };
  });
});

const uiLocale = computed<'en' | 'tr'>(() => (locale.value === 'tr' ? 'tr' : 'en'));

/**
 * ⚠⚠ `storageFieldOf` and nothing else. It is the same call
 * `components/plugin/nodes/SurfaceForm.vue` makes for a plugin's own screen,
 * which is the whole point: a manifest field cannot mean a dropdown here and
 * a row of buttons there. It sets `choice` for every `select`, carries
 * `multi`, and never carries `advanced`.
 */
const settingsFields = computed(() =>
  (detail.value?.manifest.settings ?? []).map((f) => storageFieldOf(f, uiLocale.value)),
);
const description = computed(() => pluginLabelOf(detail.value?.manifest.description, locale.value));

const KNOWN_SOURCES = ['upload', 'url', 'github', 'bundle'];
function sourceLabel(source: string): string {
  return KNOWN_SOURCES.includes(source) ? t(`appPlugins.source.${source}`) : source;
}

async function saveSettings() {
  const p = props.plugin;
  if (!p) return;
  savingSettings.value = true;
  try {
    const values: Record<string, string> = {};
    for (const [k, v] of Object.entries(settings.value)) {
      if (v === undefined || v === null) continue;
      // ⚠ A list is joined EXPLICITLY. `String(['a','b'])` happens to give
      // "a,b" today, which is the sort of accident that stops being true the
      // moment somebody wraps the value; `seedSettings` splits it back.
      values[k] = Array.isArray(v) ? v.join(',') : typeof v === 'string' ? v : String(v);
    }
    await AppPluginsApi.putSettings(p.id, values);
    toast.success(t('appPlugins.detail.settingsSaved'));
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.saveFailed')));
  } finally {
    savingSettings.value = false;
  }
}

/* ── overrides ─────────────────────────────────────────────────────────── */

const KINDS = ['file', 'dir', 'any'] as const;
const kindOptions = computed(() => KINDS.map((k) => ({ value: k, label: t(`appPlugins.detail.overrides.kinds.${k}`) })));

/**
 * Extensions the manifest offers only while an engine is on the server
 * (`applies.engine_ext`, which the signing work adds: `{libreoffice: [docx,
 * odt]}` beside `ext: [pdf]`). Read defensively — a host without the field
 * sends none.
 *
 * ⚠⚠ The editor lists them WITH the rest and the server stores only what the
 * admin changed, resolving engines at every read
 * (backend/internal/wasmplugin/applies_override.go). Copying the list the
 * host had computed would freeze it: an override saved before LibreOffice was
 * installed never offered .docx, one saved with it kept offering .docx after
 * it was removed.
 */
type EngineGated = PluginApplies & { engine_ext?: Record<string, string[]> };
function engineExtOf(a: PluginApplies): Record<string, string[]> {
  return (a as EngineGated).engine_ext ?? {};
}
function engineNotes(a: PluginApplies): string[] {
  return Object.entries(engineExtOf(a))
    .filter(([, exts]) => exts.length)
    .sort(([x], [y]) => x.localeCompare(y))
    .map(([engine, exts]) =>
      t('appPlugins.detail.overrides.engineOnly', {
        engine: engineName(engine, props.engineNames),
        exts: exts.map((e) => `.${e}`).join(' '),
      }),
    );
}

/** Start customising: copy the manifest rule so the editor has something to change. */
function customize(row: OverrideRow) {
  row.applies = {
    kind: row.manifest.kind || 'file',
    ext: [...new Set([...(row.manifest.ext ?? []), ...Object.values(engineExtOf(row.manifest)).flat()])],
    mime: [...(row.manifest.mime ?? [])],
    multi: row.manifest.multi === true,
    min: row.manifest.min ?? 0,
    max: row.manifest.max ?? 0,
  };
}

function resetToManifest(row: OverrideRow) {
  row.applies = null;
}

function setApplies(row: OverrideRow, patch: Partial<PluginApplies>) {
  row.applies = { ...(row.applies ?? {}), ...patch };
}

const normalizeExt = (raw: string) => raw.toLowerCase().replace(/^\.+/, '');
const normalizeMime = (raw: string) => raw.toLowerCase();

function describeApplies(a: PluginApplies): string {
  const parts: string[] = [t(`appPlugins.detail.overrides.kinds.${KINDS.includes(a.kind as (typeof KINDS)[number]) ? a.kind : 'file'}`)];
  if (a.ext?.length) parts.push(a.ext.map((e) => `.${e}`).join(' '));
  if (a.mime?.length) parts.push(a.mime.join(' '));
  if (a.multi) parts.push(t('appPlugins.detail.overrides.multi'));
  return parts.join(' · ');
}

async function saveOverrides() {
  const p = props.plugin;
  if (!p) return;
  savingOverrides.value = true;
  try {
    await AppPluginsApi.putOverrides(
      p.id,
      overrides.value.map((r) => ({ id: r.id, enabled: r.enabled, admin_only: r.admin_only, applies: r.applies })),
    );
    toast.success(t('appPlugins.detail.overrides.saved'));
    emit('changed');
  } catch (e: unknown) {
    toast.error(extractError(e, t('errors.saveFailed')));
  } finally {
    savingOverrides.value = false;
  }
}

/* The drawer's three tables are the explorer's own table (DataTable), each
 * remembered on the account under its own id. Every row of each arrives in
 * one answer, so the tables sort themselves.
 *
 * ⚠ The overrides table is an EDITOR: its toggles and its `applies` fields
 * write into the rows in place. Sorting it re-orders the rows on screen and
 * nothing else — the save sends the rows by id, not by position. */
const overrideColumns = computed<DataColumn<OverrideRow>[]>(() => [
  { id: 'label', label: t('appPlugins.detail.overrides.action'), sortable: true, width: 200 },
  {
    id: 'enabled',
    label: t('appPlugins.detail.overrides.enabled'),
    sortable: true,
    width: 90,
    sortValue: (r) => (r.enabled ? 1 : 0),
  },
  {
    id: 'admin_only',
    label: t('appPlugins.detail.overrides.adminOnly'),
    sortable: true,
    width: 110,
    sortValue: (r) => (r.admin_only ? 1 : 0),
  },
  {
    id: 'applies',
    label: t('appPlugins.detail.overrides.applies'),
    sortable: true,
    width: 420,
    // Customised rows together, then the manifest defaults.
    sortValue: (r) => (r.applies === null ? 1 : 0),
  },
]);

const lockColumns = computed<DataColumn<AppPluginLock>[]>(() => [
  { id: 'path', label: t('appPlugins.detail.locks.path'), sortable: true, width: 260 },
  {
    id: 'reason',
    label: t('appPlugins.detail.locks.reason'),
    sortable: true,
    width: 220,
    sortValue: (l) => lockReasonText(l, locale.value) || null,
  },
  {
    id: 'until',
    label: t('appPlugins.detail.locks.until'),
    sortable: true,
    width: 180,
    sortValue: (l) => (l.until ? Date.parse(l.until) : null),
  },
]);

/* ── schedule (uyan:s1) ─────────────────────────────────────────────────── */

/**
 * Is this app woken on its own?
 *
 * ⚠ Read from the LIST row (`props.plugin`), not from `detail`: the row is
 * there the moment the drawer opens, so the section does not appear one
 * network round-trip late, and it is the same flag the badge in the table
 * behind is drawn from — one fact, one source.
 *
 * ⚠ It is ALSO the safe side of an envelope. The detail endpoint answers
 * `{ plugin: {…}, manifest, granted, settings, overrides, schedule }` — the
 * app's own fields one level down, `schedule` at the top — while
 * `AppPluginDetail` is declared as though all of it were flat. `AppPluginsApi
 * .get` now flattens it (see the note there, and what a browser measured
 * before it did), but a reader that does not depend on that cannot be broken
 * by it either.
 */
const scheduled = computed(() => props.plugin?.scheduled === true);

/**
 * The wake-up row: `key === ''` (model.AppPluginScheduleWakeupKey). It is the
 * heartbeat, not a piece of work — `due_at` is the NEXT wake-up and `error`
 * is what the LAST one decided.
 */
const wakeup = computed<AppPluginScheduleItem | null>(
  () => (detail.value?.schedule ?? []).find((i) => i.key === '') ?? null,
);

/** Everything the last wake-up asked for — the wake-up row itself removed. */
const scheduleItems = computed<(AppPluginScheduleItem & Record<string, unknown>)[]>(
  () =>
    (detail.value?.schedule ?? []).filter((i) => i.key !== '') as (AppPluginScheduleItem &
      Record<string, unknown>)[],
);

/**
 * What the last wake-up decided, verbatim.
 *
 * ⚠ It is NOT an error message even though it arrives in `error`: a healthy
 * hour reads "3 scheduled", and a bad one "0 scheduled, 2 refused (unknown
 * action)". The host writes the app's own note in front of its tally, so the
 * string is the app speaking and is shown as it came — inventing a
 * translation for it would mean inventing the app's words too.
 */
const lastDecision = computed(() => (wakeup.value?.error || '').trim());

const scheduleColumns = computed<DataColumn<AppPluginScheduleItem>[]>(() => [
  {
    id: 'action_id',
    label: t('appPlugins.detail.schedule.action'),
    sortable: true,
    width: 200,
    sortValue: (i) => i.action_id || null,
  },
  {
    id: 'due_at',
    label: t('appPlugins.detail.schedule.due'),
    sortable: true,
    width: 180,
    sortValue: (i) => (i.due_at ? Date.parse(i.due_at) : null),
  },
  {
    id: 'status',
    label: t('appPlugins.detail.schedule.status'),
    sortable: true,
    width: 130,
    sortValue: (i) => scheduleStatusLabel(i.status),
  },
  {
    id: 'job_id',
    label: t('appPlugins.detail.schedule.job'),
    sortable: true,
    width: 160,
    sortValue: (i) => i.job_id || null,
  },
]);

const SCHEDULE_STATUSES = ['due', 'running', 'queued', 'failed', 'skipped'];

/** A status the server does not know is printed raw rather than guessed at. */
function scheduleStatusLabel(status: string): string {
  return SCHEDULE_STATUSES.includes(status)
    ? t(`appPlugins.detail.schedule.statuses.${status}`)
    : status;
}

function scheduleTone(status: string): 'emerald' | 'sky' | 'rose' | 'amber' | 'zinc' {
  if (status === 'failed') return 'rose';
  if (status === 'running') return 'sky';
  if (status === 'queued') return 'emerald';
  if (status === 'due') return 'amber';
  return 'zinc';
}

/** The job id is a UUID; the row has 160px. The whole of it stays in `title`. */
function shortJob(id: string): string {
  return id.length > 12 ? id.slice(0, 8) + '…' : id;
}

function levelTone(level: string): 'rose' | 'amber' | 'zinc' {
  if (level === 'error') return 'rose';
  if (level === 'warn' || level === 'warning') return 'amber';
  return 'zinc';
}

/** An override row's one verb, behind its one pinned `Actions` control. The
 *  row offers `Customise` until it HAS an override and `Reset to manifest`
 *  once it does — one verb at a time, as before, now named in a menu rather
 *  than swapped underneath a button. */
function overrideActions(row: OverrideRow): ContextAction[] {
  return row.applies === null
    ? [{ key: 'customize', label: t('appPlugins.detail.overrides.customize'), icon: 'rename' }]
    : [{ key: 'reset', label: t('appPlugins.detail.overrides.reset'), icon: 'restore' }];
}

function onOverrideAction(key: string, row: OverrideRow) {
  if (key === 'customize') customize(row);
  else if (key === 'reset') resetToManifest(row);
}

/** A lock's one verb. Lifting a lock is confirmed by `confirmUnlock`, exactly
 *  as it was when this was a loose button. */
function lockActions(row: AppPluginLock): ContextAction[] {
  return [
    {
      key: 'unlock',
      label: t('appPlugins.detail.locks.unlock'),
      icon: 'lock',
      disabled: unlocking.value === lockKey(row),
    },
  ];
}

function onLockAction(key: string, row: AppPluginLock) {
  if (key === 'unlock') confirmUnlock.value = row;
}
</script>

<template>
  <div class="app-detail-page">
    <div v-if="plugin" class="space-y-6" data-testid="app-plugin-detail">
      <p v-if="loading && !detail" class="text-sm text-zinc-500">{{ t('common.loading') }}</p>

      <template v-if="detail">
        <!-- Facts -->
        <section class="space-y-2">
          <p v-if="description" class="text-sm text-zinc-600 dark:text-zinc-400">{{ description }}</p>
          <dl class="grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-4">
            <dt class="text-zinc-500">{{ t('appPlugins.fields.name') }}</dt>
            <dd class="font-mono">{{ detail.name }}</dd>
            <dt class="text-zinc-500">{{ t('appPlugins.fields.version') }}</dt>
            <dd>{{ detail.version }}</dd>
            <dt class="text-zinc-500">{{ t('appPlugins.detail.source') }}</dt>
            <dd class="break-all">
              {{ sourceLabel(detail.source) }}
              <template v-if="detail.source_url"> · {{ detail.source_url }}</template>
            </dd>
            <dt class="text-zinc-500">{{ t('appPlugins.detail.signature') }}</dt>
            <dd>{{ detail.signed ? t('appPlugins.detail.signed') : t('appPlugins.detail.unsigned') }}</dd>
            <dt class="text-zinc-500">{{ t('appPlugins.detail.sha256') }}</dt>
            <dd class="break-all font-mono sm:col-span-3">{{ detail.sha256 || '—' }}</dd>
            <dt class="text-zinc-500">{{ t('appPlugins.detail.installed') }}</dt>
            <dd>{{ formatDate(detail.created_at, locale) }}</dd>
            <dt class="text-zinc-500">{{ t('appPlugins.detail.updated') }}</dt>
            <dd>{{ formatDate(detail.updated_at, locale) }}</dd>
            <template v-if="detail.manifest.homepage">
              <dt class="text-zinc-500">{{ t('appPlugins.detail.homepage') }}</dt>
              <dd class="break-all sm:col-span-3">
                <a :href="detail.manifest.homepage" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-1 text-brand-600 hover:underline dark:text-brand-400">
                  {{ detail.manifest.homepage }}
                  <ExternalLink class="h-3 w-3" />
                </a>
              </dd>
            </template>
          </dl>
          <div class="pt-1" data-testid="app-plugin-granted">
            <h4 class="text-xs font-medium text-zinc-500">{{ t('appPlugins.detail.grantedTitle') }}</h4>
            <p v-if="!grantedRows.length" class="text-xs text-zinc-500">{{ t('appPlugins.wizard.noPermissions') }}</p>
            <ul v-else class="mt-1 space-y-1">
              <li
                v-for="row in grantedRows"
                :key="row.id"
                class="text-sm"
                :title="row.id"
                :data-testid="`granted-${row.id}`"
              >
                <span>{{ row.label }}</span>
                <span v-if="row.reason" class="block text-xs text-zinc-500">{{ row.reason }}</span>
              </li>
            </ul>
          </div>
        </section>

        <!-- Settings -->
        <section class="space-y-3">
          <h3 class="text-sm font-semibold">{{ t('appPlugins.detail.settings') }}</h3>
          <p v-if="!settingsFields.length" class="text-sm text-zinc-500">{{ t('appPlugins.detail.noSettings') }}</p>
          <template v-else>
            <StorageFields v-model="settings" :fields="settingsFields" :locale="uiLocale" />
            <p class="text-xs text-zinc-500">{{ t('appPlugins.detail.secretHint') }}</p>
            <div class="flex justify-end">
              <Button size="sm" variant="primary" :loading="savingSettings" data-testid="app-plugin-save-settings" @click="saveSettings">
                <Save class="h-4 w-4" />
                {{ t('common.save') }}
              </Button>
            </div>
          </template>
        </section>

        <!-- Action overrides -->
        <section class="space-y-3">
          <h3 class="text-sm font-semibold">{{ t('appPlugins.detail.overrides.title') }}</h3>
          <DataTable
            table-id="admin.apps.detail.overrides"
            :columns="overrideColumns"
            :rows="overrides"
            row-key="id"
            :empty="t('appPlugins.detail.overrides.empty')"
            :row-actions="(row: OverrideRow) => overrideActions(row)"
            :row-actions-test-id="(row: OverrideRow) => `override-actions-${row.id}`"
            @row-action="(key: string, row: OverrideRow) => onOverrideAction(key, row)"
          >
            <!-- ⚠ Two-line cells get ONE wrapper: a DataTable cell is a flex
                 row, and the lines would otherwise sit side by side. -->
            <template #cell-label="{ row }">
              <div>
                <div class="font-medium">{{ row.label }}</div>
                <div class="font-mono text-[11px] text-zinc-500">{{ row.id }}</div>
              </div>
            </template>
            <template #cell-enabled="{ row }">
              <Toggle :model-value="row.enabled" :name="`override-enabled-${row.id}`" @update:model-value="(v: boolean) => (row.enabled = v)" />
            </template>
            <template #cell-admin_only="{ row }">
              <Toggle :model-value="row.admin_only" :name="`override-admin-${row.id}`" @update:model-value="(v: boolean) => (row.admin_only = v)" />
            </template>
            <template #cell-applies="{ row }">
              <div v-if="row.applies === null" class="text-xs">
                <Badge tone="zinc" size="xs">{{ t('appPlugins.detail.overrides.manifestDefault') }}</Badge>
                <span class="ms-2 text-zinc-500">{{ describeApplies(row.manifest) }}</span>
                <span v-for="note in engineNotes(row.manifest)" :key="note" class="block text-zinc-500">{{ note }}</span>
              </div>
              <div v-else class="grid gap-2 sm:grid-cols-2" :data-testid="`applies-editor-${row.id}`">
                <Select
                  :model-value="row.applies.kind || 'file'"
                  :options="kindOptions"
                  :label="t('appPlugins.detail.overrides.kind')"
                  size="sm"
                  @update:model-value="(v) => setApplies(row, { kind: String(v) })"
                />
                <Toggle
                  :model-value="row.applies.multi === true"
                  :label="t('appPlugins.detail.overrides.multi')"
                  @update:model-value="(v: boolean) => setApplies(row, { multi: v })"
                />
                <ChipInput
                  :model-value="row.applies.ext ?? []"
                  :label="t('appPlugins.detail.overrides.ext')"
                  placeholder="pdf"
                  :normalize="normalizeExt"
                  @update:model-value="(v) => setApplies(row, { ext: v })"
                />
                <ChipInput
                  :model-value="row.applies.mime ?? []"
                  :label="t('appPlugins.detail.overrides.mime')"
                  placeholder="image/*"
                  :normalize="normalizeMime"
                  @update:model-value="(v) => setApplies(row, { mime: v })"
                />
                <Input
                  :model-value="row.applies.min ?? 0"
                  type="number"
                  :min="0"
                  size="sm"
                  :label="t('appPlugins.detail.overrides.min')"
                  @update:model-value="(v) => setApplies(row, { min: Number(v) || 0 })"
                />
                <Input
                  :model-value="row.applies.max ?? 0"
                  type="number"
                  :min="0"
                  size="sm"
                  :label="t('appPlugins.detail.overrides.max')"
                  @update:model-value="(v) => setApplies(row, { max: Number(v) || 0 })"
                />
                <p
                  v-for="note in engineNotes(row.manifest)"
                  :key="note"
                  class="text-xs text-zinc-500 sm:col-span-2"
                  :data-testid="`applies-engine-note-${row.id}`"
                >
                  {{ note }}
                </p>
              </div>
            </template>
          </DataTable>
          <div class="flex justify-end">
            <Button size="sm" variant="primary" :loading="savingOverrides" :disabled="!overrides.length" data-testid="app-plugin-save-overrides" @click="saveOverrides">
              <Save class="h-4 w-4" />
              {{ t('common.save') }}
            </Button>
          </div>
        </section>

        <!-- uyan:s1 — Schedule. Only for an app that IS woken: a heading over
             "never" for the other five apps is a promise the deployment is
             not making. Read-only, and beside Locks on purpose — both answer
             "what is this app doing while nobody is looking". -->
        <section v-if="scheduled" class="space-y-3" data-testid="app-plugin-schedule">
          <div class="flex items-baseline justify-between gap-3">
            <h3 class="text-sm font-semibold">{{ t('appPlugins.detail.schedule.title') }}</h3>
            <Badge tone="sky" size="xs">{{ t('appPlugins.scheduledBadge') }}</Badge>
          </div>

          <!-- ONE line: when the next wake-up is, and what the last one
               decided. `attempts` and `claimed_by` ride along quietly — an
               app whose hour keeps being taken by a different node, or whose
               count stopped climbing, is diagnosed from exactly those two. -->
          <p class="text-xs text-zinc-600 dark:text-zinc-400" data-testid="app-plugin-schedule-wakeup">
            <span class="font-medium">
              {{ t('appPlugins.detail.schedule.next', { when: wakeup ? formatDate(wakeup.due_at, locale) : '—' }) }}
            </span>
            <template v-if="lastDecision">
              ·
              <span data-testid="app-plugin-schedule-decision">
                {{ t('appPlugins.detail.schedule.decided', { note: lastDecision }) }}
              </span>
            </template>
            <template v-else> · {{ t('appPlugins.detail.schedule.neverWoken') }}</template>
            <template v-if="wakeup && wakeup.attempts > 0">
              · {{ t('appPlugins.detail.schedule.woken', { count: wakeup.attempts }) }}
            </template>
            <template v-if="wakeup?.claimed_by">
              · <span class="tbl-mono">{{ t('appPlugins.detail.schedule.node', { node: wakeup.claimed_by }) }}</span>
            </template>
          </p>

          <DataTable
            table-id="admin.apps.detail.schedule"
            :columns="scheduleColumns"
            :rows="scheduleItems"
            row-key="key"
            :empty="t('appPlugins.detail.schedule.empty')"
          >
            <template #cell-action_id="{ row }">
              <div>
                <div class="font-medium">{{ row.action_id || '—' }}</div>
                <div class="font-mono text-[11px] text-zinc-500">{{ row.key }}</div>
              </div>
            </template>
            <template #cell-due_at="{ row }">
              <span class="whitespace-nowrap text-xs">{{ formatDate(row.due_at, locale) }}</span>
            </template>
            <template #cell-status="{ row }">
              <div>
                <Badge :tone="scheduleTone(row.status)" size="xs" :data-testid="`schedule-status-${row.key}`">
                  {{ scheduleStatusLabel(row.status) }}
                </Badge>
                <!-- A refusal names itself. `error` on a WORK row really is one
                     (unlike the wake-up row's decision line above). -->
                <div
                  v-if="row.error"
                  class="mt-0.5 max-w-xs break-words text-[11px] text-rose-500"
                  :data-testid="`schedule-error-${row.key}`"
                >
                  {{ row.error }}
                </div>
              </div>
            </template>
            <!-- Once an item reaches the queue the ops row owns its outcome,
                 so the id is a way OUT of this drawer rather than a string to
                 copy. ⚠ It points at the queue page, not at a row inside it:
                 `/admin/queue` takes no per-op deep link and the endpoint has
                 no job filter, so a `?job=` would be a promise the page
                 cannot keep. The full id is in `title`. -->
            <template #cell-job_id="{ row }">
              <RouterLink
                v-if="row.job_id"
                :to="{ name: 'queue' }"
                class="tbl-mono text-brand-600 hover:underline dark:text-brand-400"
                :title="`${t('appPlugins.detail.schedule.openQueue')} — ${row.job_id}`"
                :data-testid="`schedule-job-${row.key}`"
              >{{ shortJob(row.job_id) }}</RouterLink>
              <span v-else class="text-[11px] text-zinc-500">{{ t('appPlugins.detail.schedule.notQueued') }}</span>
            </template>
          </DataTable>
        </section>

        <!-- Locks -->
        <section class="space-y-3" data-testid="app-plugin-locks">
          <div class="flex items-baseline justify-between">
            <h3 class="text-sm font-semibold">{{ t('appPlugins.detail.locks.title') }}</h3>
            <span class="text-[11px] text-zinc-500">{{ t('appPlugins.detail.locks.hint') }}</span>
          </div>
          <p v-if="locksLoading && !locks.length" class="text-sm text-zinc-500">{{ t('common.loading') }}</p>
          <DataTable
            v-else
            table-id="admin.apps.detail.locks"
            :columns="lockColumns"
            :rows="locks"
            row-key="path"
            :empty="t('appPlugins.detail.locks.empty')"
            :row-actions="(row: AppPluginLock) => lockActions(row)"
            :row-actions-test-id="(row: AppPluginLock) => `app-plugin-lock-actions-${row.path}`"
            @row-action="(key: string, row: AppPluginLock) => onLockAction(key, row)"
          >
            <template #cell-path="{ row }">
              <div>
                <div class="max-w-xs break-all font-mono text-[11px]">{{ row.path }}</div>
                <div class="text-[11px] text-zinc-500" data-testid="app-plugin-lock-storage">
                  {{ row.storage || t('appPlugins.detail.locks.storage', { id: row.storage_id }) }}
                </div>
              </div>
            </template>
            <template #cell-reason="{ row }">
              <div class="max-w-xs break-words text-xs" data-testid="app-plugin-lock-reason">{{ lockReasonText(row, locale) || '—' }}</div>
            </template>
            <template #cell-until="{ row }">
              <span class="text-xs">{{ row.until ? formatDate(row.until, locale) : t('appPlugins.detail.locks.noEnd') }}</span>
            </template>
          </DataTable>
        </section>
      </template>

      <!-- Log -->
      <section class="space-y-2">
        <div class="flex items-baseline justify-between">
          <h3 class="text-sm font-semibold">{{ t('appPlugins.detail.logs.title') }}</h3>
          <span class="text-[11px] text-zinc-500">{{ t('appPlugins.detail.logs.live') }}</span>
        </div>
        <div class="max-h-64 overflow-auto rounded-lg border border-zinc-200 bg-zinc-50 p-2 font-mono text-[11px] dark:border-zinc-800 dark:bg-zinc-950" data-testid="app-plugin-logs">
          <p v-if="!logs.length" class="text-zinc-500">{{ t('appPlugins.detail.logs.empty') }}</p>
          <div v-for="line in logs" :key="line.seq" class="flex gap-2 whitespace-pre-wrap break-words">
            <span class="shrink-0 text-zinc-500" :title="formatDateFull(line.ts, locale)">{{ formatDate(line.ts, locale) }}</span>
            <Badge :tone="levelTone(line.level)" size="xs">{{ line.level }}</Badge>
            <span>{{ line.msg }}</span>
          </div>
        </div>
      </section>
    </div>

    <!-- ⚠ Confirmed, never one click: lifting a lock overrules an app that is
         in the middle of something (a document out for signature), and the app
         is not told. The sentence has to name the file, because the drawer
         lists several. -->
    <Modal
      :model-value="confirmUnlock !== null"
      :title="t('appPlugins.detail.locks.confirmTitle')"
      size="sm"
      @update:model-value="(v: boolean) => !v && (confirmUnlock = null)"
    >
      <p v-if="confirmUnlock" class="text-sm">
        {{ t('appPlugins.detail.locks.confirmText', { path: confirmUnlock.path, app: confirmUnlock.plugin }) }}
      </p>
      <template #footer>
        <Button size="sm" variant="outline" @click="confirmUnlock = null">{{ t('common.cancel') }}</Button>
        <Button
          size="sm"
          variant="primary"
          data-testid="app-plugin-unlock-confirm"
          @click="confirmUnlock && doUnlock(confirmUnlock)"
        >
          {{ t('appPlugins.detail.locks.unlock') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
/* Each section of the page is a card — the panel's own `.card` look (same
   tokens, so it follows the palette). Applied from here rather than by
   classes on each <section>, so the sections' markup, and the tables inside
   them, stay exactly as the shared-table work left them. */
.app-detail-page [data-testid='app-plugin-detail'] > section {
  background: var(--fe-bg);
  border: 1px solid var(--fe-border);
  border-radius: var(--fe-radius-lg);
  padding: 1rem;
}
.app-detail-page [data-testid='app-plugin-detail'] > section h3 {
  font-size: 1rem;
  font-weight: 600;
}
</style>
