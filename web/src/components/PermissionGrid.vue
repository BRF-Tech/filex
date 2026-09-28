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
 * they do this?" is answered on the page.
 */
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { ChevronDown, ChevronRight } from 'lucide-vue-next';

import type { EffectivePermissions, PermDef, PermEffect, PermGroup, PermKey, PermSource } from '@/api/roles';

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
  }>(),
  { effects: () => ({}), set: () => [], effective: null, only: null, disabled: false, locked: () => [] },
);

const emit = defineEmits<{
  (e: 'update:effects', v: Record<PermKey, PermEffect>): void;
  (e: 'update:set', v: PermKey[]): void;
}>();

const { t, te } = useI18n();

const GROUPS: PermGroup[] = ['files', 'sharing', 'access', 'account', 'admin'];

const grouped = computed(() =>
  GROUPS.map((g) => ({ group: g, defs: props.catalogue.filter((d) => d.group === g) })).filter((g) => g.defs.length > 0),
);

// Files open by default; the rest collapse to a count, so the card stays short.
const open = ref<Record<string, boolean>>({ files: true });
function toggleGroup(g: string) {
  open.value = { ...open.value, [g]: !open.value[g] };
}

function label(key: PermKey): string {
  const k = `permissions.items.${key}.label`;
  return te(k) ? t(k) : key;
}
function hint(key: PermKey): string {
  const k = `permissions.items.${key}.hint`;
  return te(k) ? t(k) : '';
}

function editable(d: PermDef): boolean {
  if (props.disabled || d.role_only) return false;
  if (props.locked.includes(d.key)) return false;
  if (props.only && !props.only.includes(d.key)) return false;
  return true;
}

function effectOf(key: PermKey): 'inherit' | PermEffect {
  return props.effects[key] ?? 'inherit';
}
function setEffect(key: PermKey, v: 'inherit' | PermEffect) {
  const next = { ...props.effects };
  if (v === 'inherit') delete next[key];
  else next[key] = v;
  emit('update:effects', next);
}

function isOn(key: PermKey): boolean {
  return props.set.includes(key);
}
function setOn(key: PermKey, on: boolean) {
  const next = props.set.filter((k) => k !== key);
  if (on) next.push(key);
  emit('update:set', next);
}

/** Group-wide shortcuts: all on / all off (set), all allow / clear (effects). */
function groupAll(defs: PermDef[], on: boolean) {
  const keys = defs.filter(editable).map((d) => d.key);
  if (props.mode === 'set') {
    const rest = props.set.filter((k) => !keys.includes(k));
    emit('update:set', on ? [...rest, ...keys] : rest);
  } else {
    const next = { ...props.effects };
    for (const k of keys) {
      if (on) next[k] = 'allow';
      else next[k] = 'deny';
    }
    emit('update:effects', next);
  }
}

function effectiveRow(key: PermKey) {
  return props.effective?.permissions.find((p) => p.key === key);
}
function sourceText(src: PermSource | undefined): string {
  if (!src) return '';
  if (src.kind === 'rule' || src.kind === 'role_off') {
    return t(`permissions.source.${src.kind}`, { name: src.rule_name ?? `#${src.rule_id}` });
  }
  return t(`permissions.source.${src.kind}`);
}

/** "4/7" on a collapsed group: how many are allowed (or on). */
function count(defs: PermDef[]): string {
  const n = defs.filter((d) =>
    props.effective ? effectiveRow(d.key)?.allowed : props.mode === 'set' ? isOn(d.key) : props.effects[d.key] === 'allow',
  ).length;
  return `${n}/${defs.length}`;
}
</script>

<template>
  <ul class="rule-list rounded-lg">
    <li v-for="g in grouped" :key="g.group" :data-testid="`perm-group-${g.group}`">
      <div class="flex items-center justify-between gap-2 px-3 py-2">
        <button
          type="button"
          class="flex items-center gap-1.5 text-sm font-medium"
          :aria-expanded="!!open[g.group]"
          @click="toggleGroup(g.group)"
        >
          <component :is="open[g.group] ? ChevronDown : ChevronRight" class="h-4 w-4" />
          {{ t(`permissions.groups.${g.group}`) }}
          <span class="text-xs font-normal text-zinc-500 tabular-nums">({{ count(g.defs) }})</span>
        </button>
        <div v-if="open[g.group] && !disabled" class="flex gap-2 text-xs">
          <button type="button" class="text-brand-600 hover:underline" @click="groupAll(g.defs, true)">
            {{ mode === 'set' ? t('permissions.all') : t('permissions.effect.allow') }}
          </button>
          <button type="button" class="text-brand-600 hover:underline" @click="groupAll(g.defs, false)">
            {{ mode === 'set' ? t('permissions.none') : t('permissions.effect.deny') }}
          </button>
        </div>
      </div>

      <ul v-if="open[g.group]" class="pb-2">
        <li
          v-for="d in g.defs"
          :key="d.key"
          class="grid grid-cols-[1fr_auto] items-center gap-x-3 gap-y-0.5 px-3 py-1.5"
          :data-testid="`perm-row-${d.key}`"
        >
          <div class="min-w-0">
            <div class="text-sm" :class="editable(d) ? '' : 'text-zinc-500'">{{ label(d.key) }}</div>
            <div class="text-xs text-zinc-500 dark:text-zinc-400">{{ hint(d.key) }}</div>
            <div v-if="effective && effectiveRow(d.key)" class="mt-0.5 text-xs">
              <span :class="effectiveRow(d.key)!.allowed ? 'text-emerald-600 dark:text-emerald-400' : 'text-rose-600 dark:text-rose-400'">
                {{ effectiveRow(d.key)!.allowed ? t('permissions.effect.allow') : t('permissions.effect.deny') }}
              </span>
              <span class="text-zinc-500"> · {{ sourceText(effectiveRow(d.key)!.source) }}</span>
            </div>
          </div>

          <!-- two-state -->
          <input
            v-if="mode === 'set'"
            type="checkbox"
            class="h-4 w-4"
            :checked="isOn(d.key)"
            :disabled="!editable(d)"
            :aria-label="label(d.key)"
            @change="setOn(d.key, ($event.target as HTMLInputElement).checked)"
          />

          <!-- tri-state -->
          <div
            v-else
            role="radiogroup"
            :aria-label="label(d.key)"
            class="inline-flex overflow-hidden rounded-md border border-zinc-300 text-xs dark:border-zinc-700"
          >
            <button
              v-for="opt in (['inherit', 'allow', 'deny'] as const)"
              :key="opt"
              type="button"
              role="radio"
              :aria-checked="effectOf(d.key) === opt"
              :disabled="!editable(d)"
              class="px-2 py-1 disabled:opacity-40"
              :class="
                effectOf(d.key) === opt
                  ? opt === 'allow'
                    ? 'bg-emerald-600 text-white'
                    : opt === 'deny'
                      ? 'bg-rose-600 text-white'
                      : 'bg-zinc-200 dark:bg-zinc-700'
                  : 'hover:bg-zinc-100 dark:hover:bg-zinc-800'
              "
              :data-testid="`perm-${d.key}-${opt}`"
              @click="setEffect(d.key, opt)"
            >
              {{ t(`permissions.effect.${opt}`) }}
            </button>
          </div>
        </li>
      </ul>
    </li>
  </ul>
</template>
