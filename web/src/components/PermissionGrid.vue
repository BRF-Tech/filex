<script setup lang="ts">
/**
 * The permission catalogue as a grid, grouped the way the catalogue groups
 * it. Two modes:
 *
 *  - `effects` (tri-state): each permission is Inherit / Allow / Deny. The
 *    model is a map holding only the permissions that are set — what an
 *    account's overrides and a rule's effects both are on the wire.
 *  - `set` (two-state): each permission is on or off. The model is the list
 *    of keys that are on — the install defaults.
 *
 * `effective`, when given, is drawn beside each row: the result and where it
 * came from (default, a named rule, this account, the role), so "why can't
 * they do this?" is answered on the page. A named custom role is named in
 * the panel's language when `rules` carries it (lib/roleName); otherwise by
 * the name the server sent.
 *
 * `apps` adds one more group, "Apps": the permissions installed apps let the
 * administrator hand out (backend perm/app.go — "Request signatures" for the
 * signing app), under each app's name. They are always tri-state, in either
 * mode — Default / Allow / Deny, where "Default" says what it comes to
 * (`appDefaults`: the role's answer, or for a person the answer without their
 * own exception) — and their model is `appEffects`, apart from the
 * catalogue's: a built-in role keeps them in `apps`, a custom role in
 * `settings.apps`, a person among their exceptions. No app declares any: no
 * group. The group has its own "Reset to defaults": the app decisions are
 * cleared HERE and only here — a preset or an editor's "Clear" is about the
 * catalogue and leaves them as they are.
 *
 * ⚠ ONE grid: every place that edits permissions draws them through this
 * component, app permissions included — no second list that looks like it
 * (docs/CONTRIBUTING.md → "UI rules").
 */
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ChevronDown, ChevronRight } from 'lucide-vue-next';
import { pluginLabelOf } from '@brftech/filex-core';

import type {
  AppPermDef,
  AppPermEffective,
  EffectivePermissions,
  PermDef,
  PermEffect,
  PermGroup,
  PermKey,
  PermSource,
} from '@/api/roles';
import { roleName, type NamedRole } from '@/lib/roleName';

const props = withDefaults(
  defineProps<{
    catalogue: PermDef[];
    mode: 'effects' | 'set';
    /** mode=effects */
    effects?: Record<PermKey, PermEffect>;
    /** mode=set */
    set?: PermKey[];
    effective?: EffectivePermissions | null;
    /** Only these keys may be changed (a rule limited to places); others are shown disabled. */
    only?: PermKey[] | null;
    disabled?: boolean;
    /** Keys that can never be held (a viewer's capped ones) — shown, never offered. */
    locked?: PermKey[];
    /** The installed apps' permissions (catalogue `apps`); none: no Apps group. */
    apps?: AppPermDef[];
    /** Decisions about app permissions: app.<app>.<id> → allow | deny. */
    appEffects?: Record<string, PermEffect>;
    /** What "Default" comes to, per app permission key (absent: not known). */
    appDefaults?: Record<string, boolean>;
    /** The app permissions are shown but cannot be changed here… */
    appsReadOnly?: boolean;
    /** …and why (drawn under the Apps heading). */
    appsReadOnlyNote?: string;
    /** The custom roles an `effective` source may name, to name them in the panel's language. */
    rules?: (NamedRole & { id?: number })[];
  }>(),
  {
    effects: () => ({}),
    set: () => [],
    effective: null,
    only: null,
    disabled: false,
    locked: () => [],
    apps: () => [],
    appEffects: () => ({}),
    appDefaults: () => ({}),
    appsReadOnly: false,
    appsReadOnlyNote: '',
    rules: () => [],
  },
);

const emit = defineEmits<{
  (e: 'update:effects', v: Record<PermKey, PermEffect>): void;
  (e: 'update:set', v: PermKey[]): void;
  (e: 'update:appEffects', v: Record<string, PermEffect>): void;
}>();

const { t, te, locale } = useI18n();

const GROUPS: PermGroup[] = ['files', 'sharing', 'access', 'account', 'admin'];

type Choice = 'inherit' | PermEffect;

/** One row of the grid: a catalogue permission, or an app's. */
interface Row {
  key: string;
  kind: 'perm' | 'app';
  label: string;
  hint: string;
  /** The catalogue entry (kind perm). */
  def?: PermDef;
  /** The app permission (kind app). */
  app?: AppPermDef;
  /** Kind app: the app's name, on the first of its rows. */
  heading?: string;
}
interface Group {
  id: PermGroup | 'apps';
  rows: Row[];
}

function label(key: PermKey): string {
  const k = `permissions.items.${key}.label`;
  return te(k) ? t(k) : key;
}
function hint(key: PermKey): string {
  const k = `permissions.items.${key}.hint`;
  return te(k) ? t(k) : '';
}

const grouped = computed<Group[]>(() => {
  const out: Group[] = GROUPS.map((g) => ({
    id: g,
    rows: props.catalogue
      .filter((d) => d.group === g)
      .map((d): Row => ({ key: d.key, kind: 'perm', label: label(d.key), hint: hint(d.key), def: d })),
  })).filter((g) => g.rows.length > 0);
  if (props.apps.length > 0) {
    let last = '';
    out.push({
      id: 'apps',
      rows: props.apps.map((a): Row => {
        const row: Row = {
          key: a.key,
          kind: 'app',
          label: pluginLabelOf(a.label, locale.value) || a.id,
          hint: pluginLabelOf(a.description, locale.value),
          app: a,
        };
        if (a.app !== last) row.heading = pluginLabelOf(a.app_label, locale.value) || a.app;
        last = a.app;
        return row;
      }),
    });
  }
  return out;
});

// Files open by default; the rest collapse to a count, so the card stays short.
const open = ref<Record<string, boolean>>({ files: true });
function toggleGroup(g: string) {
  open.value = { ...open.value, [g]: !open.value[g] };
}

function editable(row: Row): boolean {
  if (props.disabled) return false;
  if (row.kind === 'app') return !props.appsReadOnly;
  const d = row.def!;
  if (d.role_only) return false;
  if (props.locked.includes(d.key)) return false;
  if (props.only && !props.only.includes(d.key)) return false;
  return true;
}

/** App permissions are always Default / Allow / Deny; the catalogue's follow the mode. */
function triState(row: Row): boolean {
  return row.kind === 'app' || props.mode === 'effects';
}

function effectOf(row: Row): Choice {
  return (row.kind === 'app' ? props.appEffects[row.key] : props.effects[row.key]) ?? 'inherit';
}
function setEffect(row: Row, v: Choice) {
  const next = { ...(row.kind === 'app' ? props.appEffects : props.effects) };
  if (v === 'inherit') delete next[row.key];
  else next[row.key] = v;
  if (row.kind === 'app') emit('update:appEffects', next);
  else emit('update:effects', next);
}

function isOn(key: PermKey): boolean {
  return props.set.includes(key);
}
function setOn(key: PermKey, on: boolean) {
  const next = props.set.filter((k) => k !== key);
  if (on) next.push(key);
  emit('update:set', next);
}

/** What an app permission's Default comes to: allowed, denied, or not known. */
function defaultWord(row: Row): 'allowed' | 'denied' | 'unknown' {
  const d = props.appDefaults[row.key];
  return d === true ? 'allowed' : d === false ? 'denied' : 'unknown';
}

/** The words on a choice: an app permission's Default says what it comes to. */
function choiceLabel(row: Row, opt: Choice): string {
  if (row.kind === 'app' && opt === 'inherit') {
    const d = defaultWord(row);
    if (d === 'allowed') return t('permissions.apps.defaultAllowed');
    if (d === 'denied') return t('permissions.apps.defaultDenied');
    return t('permissions.apps.default');
  }
  return t(`permissions.effect.${opt}`);
}

/** Group-wide shortcuts: all on / all off (set), all allow / all deny (tri-state). */
function groupAll(g: Group, on: boolean) {
  const rows = g.rows.filter(editable);
  if (g.id === 'apps') {
    const next = { ...props.appEffects };
    for (const r of rows) next[r.key] = on ? 'allow' : 'deny';
    emit('update:appEffects', next);
    return;
  }
  const keys = rows.map((r) => r.key);
  if (props.mode === 'set') {
    const rest = props.set.filter((k) => !keys.includes(k));
    emit('update:set', on ? [...rest, ...keys] : rest);
  } else {
    const next = { ...props.effects };
    for (const k of keys) next[k] = on ? 'allow' : 'deny';
    emit('update:effects', next);
  }
}
function groupAllLabel(g: Group, on: boolean): string {
  if (props.mode === 'set' && g.id !== 'apps') return on ? t('permissions.all') : t('permissions.none');
  return on ? t('permissions.effect.allow') : t('permissions.effect.deny');
}
function groupEditable(g: Group): boolean {
  return g.rows.some(editable);
}
/** The Apps group's own clear: every app decision back to Default. */
function appsDecided(g: Group): boolean {
  return g.rows.some((r) => editable(r) && props.appEffects[r.key] !== undefined);
}
function resetApps(g: Group) {
  const next = { ...props.appEffects };
  for (const r of g.rows.filter(editable)) delete next[r.key];
  emit('update:appEffects', next);
}

/** The account's answer for a row: the result and where it came from. */
function effectiveRow(row: Row): { allowed: boolean; source: PermSource } | undefined {
  if (!props.effective) return undefined;
  if (row.kind === 'app') return props.effective.apps?.find((p: AppPermEffective) => p.key === row.key);
  return props.effective.permissions.find((p) => p.key === row.key);
}
function sourceText(src: PermSource | undefined): string {
  if (!src) return '';
  if (src.kind === 'rule' || src.kind === 'role_off') {
    const rule = props.rules.find((r) => r.id != null && r.id === src.rule_id);
    return t(`permissions.source.${src.kind}`, { name: rule ? roleName(rule, locale.value) : (src.rule_name ?? `#${src.rule_id}`) });
  }
  return t(`permissions.source.${src.kind}`);
}

/** Does the row come out allowed, with no account to ask? (a role's editor) */
function resolvesAllowed(row: Row): boolean {
  if (row.kind === 'app') {
    const e = props.appEffects[row.key];
    return e ? e === 'allow' : props.appDefaults[row.key] === true;
  }
  return props.mode === 'set' ? isOn(row.key) : props.effects[row.key] === 'allow';
}

/** "4/7" on a collapsed group: how many are allowed (or on). */
function count(g: Group): string {
  const n = g.rows.filter((r) => (props.effective ? effectiveRow(r)?.allowed : resolvesAllowed(r))).length;
  return `${n}/${g.rows.length}`;
}
</script>

<template>
  <ul class="rule-list rounded-lg">
    <li v-for="g in grouped" :key="g.id" :data-testid="`perm-group-${g.id}`">
      <div class="flex items-center justify-between gap-2 px-3 py-2">
        <button
          type="button"
          class="flex items-center gap-1.5 text-sm font-medium"
          :aria-expanded="!!open[g.id]"
          :data-testid="`perm-group-toggle-${g.id}`"
          @click="toggleGroup(g.id)"
        >
          <component :is="open[g.id] ? ChevronDown : ChevronRight" class="h-4 w-4" />
          {{ t(`permissions.groups.${g.id}`) }}
          <span class="text-xs font-normal text-zinc-500 tabular-nums">({{ count(g) }})</span>
        </button>
        <div v-if="open[g.id] && groupEditable(g)" class="flex gap-2 text-xs">
          <button type="button" class="text-brand-600 hover:underline" @click="groupAll(g, true)">
            {{ groupAllLabel(g, true) }}
          </button>
          <button type="button" class="text-brand-600 hover:underline" @click="groupAll(g, false)">
            {{ groupAllLabel(g, false) }}
          </button>
          <button
            v-if="g.id === 'apps'"
            type="button"
            class="text-brand-600 hover:underline disabled:opacity-40 disabled:no-underline"
            :disabled="!appsDecided(g)"
            data-testid="perm-apps-reset"
            @click="resetApps(g)"
          >
            {{ t('permissions.apps.resetAll') }}
          </button>
        </div>
      </div>

      <template v-if="open[g.id]">
        <p v-if="g.id === 'apps'" class="px-3 text-xs text-zinc-500 dark:text-zinc-400">{{ t('permissions.apps.hint') }}</p>
        <p
          v-if="g.id === 'apps' && appsReadOnly && appsReadOnlyNote"
          class="px-3 pt-1 text-xs text-amber-700 dark:text-amber-400"
          data-testid="perm-apps-read-only"
        >
          {{ appsReadOnlyNote }}
        </p>
        <ul class="pb-2">
          <template v-for="row in g.rows" :key="row.key">
            <li
              v-if="row.heading"
              class="px-3 pt-2 text-xs font-semibold text-zinc-600 dark:text-zinc-300"
              :data-testid="`perm-app-${row.app!.app}`"
            >
              {{ row.heading }}
            </li>
            <li
              class="grid grid-cols-[1fr_auto] items-center gap-x-3 gap-y-0.5 px-3 py-1.5"
              :data-testid="`perm-row-${row.key}`"
            >
              <div class="min-w-0">
                <div class="text-sm" :class="editable(row) ? '' : 'text-zinc-500'">{{ row.label }}</div>
                <div v-if="row.hint" class="text-xs text-zinc-500 dark:text-zinc-400">{{ row.hint }}</div>
                <div v-if="row.app" class="text-xs text-zinc-500 dark:text-zinc-400" :data-testid="`perm-app-default-${row.key}`">
                  {{ t(`permissions.apps.holders.${row.app.default}`) }}
                </div>
                <div
                  v-if="effectiveRow(row)"
                  class="mt-0.5 text-xs"
                  :data-testid="`perm-effective-${row.key}`"
                  :data-allowed="String(effectiveRow(row)!.allowed)"
                  :data-source="effectiveRow(row)!.source.kind"
                >
                  <span :class="effectiveRow(row)!.allowed ? 'text-emerald-600 dark:text-emerald-400' : 'text-rose-600 dark:text-rose-400'">
                    {{ effectiveRow(row)!.allowed ? t('permissions.result.allowed') : t('permissions.result.denied') }}
                  </span>
                  <span class="text-zinc-500"> · {{ sourceText(effectiveRow(row)!.source) }}</span>
                </div>
              </div>

              <!-- two-state -->
              <input
                v-if="!triState(row)"
                type="checkbox"
                class="h-4 w-4"
                :checked="isOn(row.key)"
                :disabled="!editable(row)"
                :aria-label="row.label"
                @change="setOn(row.key, ($event.target as HTMLInputElement).checked)"
              />

              <!-- tri-state -->
              <div
                v-else
                role="radiogroup"
                :aria-label="row.label"
                class="inline-flex overflow-hidden rounded-md border border-zinc-300 text-xs dark:border-zinc-700"
              >
                <button
                  v-for="opt in (['inherit', 'allow', 'deny'] as const)"
                  :key="opt"
                  type="button"
                  role="radio"
                  :aria-checked="effectOf(row) === opt"
                  :disabled="!editable(row)"
                  class="px-2 py-1 disabled:opacity-40"
                  :class="
                    effectOf(row) === opt
                      ? opt === 'allow'
                        ? 'bg-emerald-600 text-white'
                        : opt === 'deny'
                          ? 'bg-rose-600 text-white'
                          : 'bg-zinc-200 dark:bg-zinc-700'
                      : 'hover:bg-zinc-100 dark:hover:bg-zinc-800'
                  "
                  :data-testid="`perm-${row.key}-${opt}`"
                  :data-default="row.kind === 'app' && opt === 'inherit' ? defaultWord(row) : undefined"
                  @click="setEffect(row, opt)"
                >
                  {{ choiceLabel(row, opt) }}
                </button>
              </div>
            </li>
          </template>
        </ul>
      </template>
    </li>
  </ul>
</template>
