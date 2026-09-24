<script setup lang="ts">
/**
 * SurfaceSections — a home page's menu: one entry per section the plugin
 * declared (`surface.sections`), the open one marked.
 *
 * ⚠⚠ Drawn by the FRAME, never by a node. The owner, 2026-09-21: the
 * Signatures screen should be "its own page … each in a separate menu",
 * with the browser's Back working and a notification landing on the right
 * section. A node can draw tabs, but only the frame owns the address bar,
 * so the frame keeps the open section in its URL and this component only
 * says which entry was chosen (`select`). Every frame that can show a home
 * view draws the same menu: the in-app page, the admin panel's Apps page,
 * and the dialog an embed without a page falls back to.
 *
 * The look is the product's own underlined tab strip (the Connections and
 * Plugins screens), scrolled sideways on a phone rather than wrapped.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../../types/ExplorerConfig';
import type { SurfaceSection } from '../../types/Plugins';
import { labelOf } from '../../lib/pluginLabel';

const props = defineProps<{
  sections: SurfaceSection[];
  /** The section on screen. */
  active?: string;
  locale: LocaleCode | string;
  /** A label for the menu itself — the page's title. */
  label?: string;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: 'select', id: string): void;
}>();

const items = computed(() =>
  props.sections
    .filter((s) => s && typeof s.id === 'string' && s.id)
    .map((s) => ({
      id: s.id,
      label: labelOf(s.label, props.locale as LocaleCode) || s.id,
      count: typeof s.count === 'number' && Number.isFinite(s.count) ? s.count : null,
    })),
);

function pick(id: string): void {
  if (props.disabled || id === props.active) return;
  emit('select', id);
}

/** Arrow keys move along the strip, as a tab list does. */
function onKey(e: KeyboardEvent, index: number): void {
  const keys = ['ArrowRight', 'ArrowLeft', 'Home', 'End'];
  if (!keys.includes(e.key)) return;
  e.preventDefault();
  const n = items.value.length;
  if (!n) return;
  let next = index;
  if (e.key === 'ArrowRight') next = (index + 1) % n;
  else if (e.key === 'ArrowLeft') next = (index - 1 + n) % n;
  else if (e.key === 'Home') next = 0;
  else if (e.key === 'End') next = n - 1;
  const buttons = (e.currentTarget as HTMLElement).parentElement?.querySelectorAll<HTMLElement>('[role="tab"]');
  buttons?.[next]?.focus();
  pick(items.value[next].id);
}
</script>

<template>
  <nav class="fe-sections" role="tablist" :aria-label="label" data-testid="surface-sections">
    <button
      v-for="(s, i) in items"
      :key="s.id"
      type="button"
      role="tab"
      class="fe-sections__tab"
      :class="{ 'is-active': s.id === active }"
      :aria-selected="s.id === active ? 'true' : 'false'"
      :tabindex="s.id === active || (!active && i === 0) ? 0 : -1"
      :disabled="disabled"
      :data-testid="`surface-section-${s.id}`"
      :data-section="s.id"
      @click="pick(s.id)"
      @keydown="onKey($event, i)"
    >
      <span class="fe-sections__label">{{ s.label }}</span>
      <span v-if="s.count !== null" class="fe-sections__count" :data-testid="`surface-section-count-${s.id}`">{{ s.count }}</span>
    </button>
  </nav>
</template>
