<script setup lang="ts">
/**
 * FilterBar — surucu:d1, the chip row under the breadcrumb in the `drive`
 * profile.
 *
 * Three chips: Type · Modified · Size. Each opens a small single-choice
 * popover; a chip with a choice made carries that choice as its label, so the
 * row says what is being filtered without opening anything.
 *
 * ⚠ THREE, not the four the mockup draws. There is no **People** chip because
 * there is nothing behind one: a listing row carries no owner
 * (`projectFileNodes` emits id/path/basename/type/extension/size/mime_type/
 * storage/etag/perm/thumb_url/last_modified and nothing else), `nodes.owner_id`
 * is quota bookkeeping that is nil for everything a sync discovered, and the
 * listing endpoint reads no owner parameter. A People chip would be a control
 * that opens, offers names, and changes nothing — see docs and the report on
 * this branch; the advanced-search round owns that question.
 *
 * ⚠ Popovers are TELEPORTED to <body>, like ContextMenu, and positioned
 * `fixed`. An absolutely-positioned panel inside the explorer is clipped by
 * `.fe__body`'s own scroll container, which is how a menu ends up half visible
 * with its own scrollbar.
 *
 * ⚠ No `<style>` block. Package CSS lives in `styles/base.css` — a scoped block
 * compiles to `.cls[data-v-HASH]` and the hash does not match in the
 * web-component build, so the rules silently stop applying in every embed.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { useLocale } from '../composables/useLocale';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import type { DriveFilters, ModifiedFilter, SizeFilter, TypeFilter } from '../lib/fileFilters';
import { activeFilterCount } from '../lib/fileFilters';

const props = defineProps<{
  value: DriveFilters;
  locale: LocaleCode;
  /** Resolved theme — the teleported popover leaves the `.fe` variable scope. */
  theme?: ThemeMode;
  /** Rows currently shown / rows the folder holds, for the count chip. */
  shown?: number;
  total?: number;
}>();

const emit = defineEmits<{ (e: 'update:value', v: DriveFilters): void }>();

const { t } = useLocale(() => props.locale);

type Group = 'type' | 'modified' | 'size';

const TYPE_OPTIONS: TypeFilter[] = [
  'any', 'folder', 'document', 'spreadsheet', 'presentation', 'pdf',
  'image', 'video', 'audio', 'archive', 'code',
];
const MODIFIED_OPTIONS: ModifiedFilter[] = ['any', 'today', '7d', '30d', 'year'];
const SIZE_OPTIONS: SizeFilter[] = ['any', 'lt1', '1to10', '10to100', 'gt100'];

function optionsFor(g: Group): string[] {
  if (g === 'type') return TYPE_OPTIONS;
  if (g === 'modified') return MODIFIED_OPTIONS;
  return SIZE_OPTIONS;
}

function optionLabel(g: Group, v: string): string {
  return t(`filter.${g}.${v}`);
}

/** The chip's own label: the group name until a choice is made, then the
 *  choice — a row of three identical words tells the reader nothing. */
function chipLabel(g: Group): string {
  const v = props.value[g];
  return v === 'any' ? t(`filter.${g}`) : optionLabel(g, v);
}

const activeCount = computed(() => activeFilterCount(props.value));

// ── the popover ──────────────────────────────────────────────────────
const openGroup = ref<Group | null>(null);
const pos = ref({ x: 0, y: 0 });
const panelEl = ref<HTMLElement | null>(null);
const chipEls = ref<Record<string, HTMLElement | null>>({});

function setChipEl(g: Group, el: unknown) {
  chipEls.value[g] = (el as HTMLElement | null) ?? null;
}

// The open popover's data, precomputed. Reading `value[openGroup]` straight
// from the template would lean on template narrowing across a Teleport, and
// `vue-tsc --noEmit` is part of this package's build.
const popOptions = computed<string[]>(() => (openGroup.value ? optionsFor(openGroup.value) : []));
const popTitle = computed(() => (openGroup.value ? t(`filter.${openGroup.value}`) : ''));
function popChecked(opt: string): boolean {
  return !!openGroup.value && props.value[openGroup.value] === opt;
}
function popLabel(opt: string): string {
  return openGroup.value ? optionLabel(openGroup.value, opt) : opt;
}
function popPick(opt: string): void {
  if (openGroup.value) pick(openGroup.value, opt);
}

async function toggle(g: Group) {
  if (openGroup.value === g) {
    close();
    return;
  }
  const r = chipEls.value[g]?.getBoundingClientRect();
  pos.value = { x: r ? r.left : 8, y: r ? r.bottom + 6 : 8 };
  openGroup.value = g;
  await nextTick();
  // Keep it on screen: a chip near the right edge would otherwise open a panel
  // that runs off it, and the row is right beside the info panel.
  const panel = panelEl.value;
  if (panel) {
    const box = panel.getBoundingClientRect();
    if (box.right > window.innerWidth - 8) {
      pos.value = { ...pos.value, x: Math.max(8, window.innerWidth - 8 - box.width) };
    }
    if (box.bottom > window.innerHeight - 8) {
      const r2 = chipEls.value[g]?.getBoundingClientRect();
      pos.value = { ...pos.value, y: Math.max(8, (r2 ? r2.top : 0) - box.height - 6) };
    }
    panel.querySelector<HTMLElement>('[data-checked="true"]')?.focus();
  }
}

function close(restoreFocus = false) {
  const g = openGroup.value;
  openGroup.value = null;
  if (restoreFocus && g) chipEls.value[g]?.focus();
}

function pick(g: Group, v: string) {
  emit('update:value', { ...props.value, [g]: v } as DriveFilters);
  close(true);
}

function clearAll() {
  emit('update:value', { type: 'any', modified: 'any', size: 'any' });
}

function onDocPointer(ev: PointerEvent) {
  if (!openGroup.value) return;
  const el = ev.target as Node;
  if (panelEl.value?.contains(el)) return;
  if (Object.values(chipEls.value).some((c) => c?.contains(el))) return;
  close();
}

function onKey(ev: KeyboardEvent) {
  if (ev.key === 'Escape' && openGroup.value) {
    ev.stopPropagation();
    close(true);
  }
}

onMounted(() => {
  document.addEventListener('pointerdown', onDocPointer, true);
  document.addEventListener('keydown', onKey, true);
});
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocPointer, true);
  document.removeEventListener('keydown', onKey, true);
});
</script>

<template>
  <div class="fe-filterbar" role="group" :aria-label="t('filter.aria')" data-testid="filterbar">
    <button
      v-for="g in (['type', 'modified', 'size'] as Group[])"
      :key="g"
      :ref="(el) => setChipEl(g, el)"
      type="button"
      class="fe-filterbar__chip"
      :class="{ 'is-set': value[g] !== 'any', 'is-open': openGroup === g }"
      :aria-expanded="openGroup === g"
      aria-haspopup="listbox"
      :data-testid="`filter-${g}`"
      @click="toggle(g)"
    >
      <span class="fe-filterbar__label">{{ chipLabel(g) }}</span>
      <svg
        class="fe-ficon fe-filterbar__caret"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
        focusable="false"
      >
        <path d="M7 10l5 5 5-5" />
      </svg>
    </button>

    <button
      v-if="activeCount > 0"
      type="button"
      class="fe-filterbar__clear"
      data-testid="filter-clear"
      @click="clearAll"
    >
      {{ t('filter.clear') }}
    </button>

    <span
      v-if="activeCount > 0 && typeof shown === 'number' && typeof total === 'number'"
      class="fe-filterbar__count"
      role="status"
      data-testid="filter-count"
      >{{ t('filter.count', { shown: String(shown), total: String(total) }) }}</span
    >

    <Teleport to="body">
      <div
        v-if="openGroup"
        ref="panelEl"
        class="fe-filterpop"
        :class="{
          'fe--theme-light': theme === 'light',
          'fe--theme-dark': theme === 'dark',
        }"
        role="listbox"
        :aria-label="popTitle"
        :style="{ left: pos.x + 'px', top: pos.y + 'px' }"
      >
        <button
          v-for="opt in popOptions"
          :key="opt"
          type="button"
          class="fe-filterpop__item"
          role="option"
          :aria-selected="popChecked(opt)"
          :data-checked="popChecked(opt) ? 'true' : 'false'"
          :data-testid="`filter-opt-${opt}`"
          @click="popPick(opt)"
        >
          <span class="fe-filterpop__tick" aria-hidden="true">{{ popChecked(opt) ? '✓' : '' }}</span>
          <span>{{ popLabel(opt) }}</span>
        </button>
      </div>
    </Teleport>
  </div>
</template>
