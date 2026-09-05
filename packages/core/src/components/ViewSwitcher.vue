<script setup lang="ts">
/**
 * ViewSwitcher — the list / grid / gallery toggle.
 *
 * Lifted out of Toolbar.vue unchanged (same classes, same glyphs, same
 * `role="tablist"` semantics) because the `drive` profile puts it on the
 * breadcrumb row instead of in the header, and two copies of a switcher is
 * how the two rows end up offering different modes. One component, mounted
 * in whichever row the profile asks for.
 *
 * `modes` absent = all three, so every embedder that predates `uiProfile`
 * keeps the switcher it had.
 *
 * ⚠ No `<style>` block — package CSS lives in `styles/base.css` (a scoped
 * block's `data-v` hash does not match in the web-component build).
 */
import { useLocale } from '../composables/useLocale';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { ViewMode } from '../types/FileNode';

const props = defineProps<{
  viewMode: ViewMode;
  locale: LocaleCode;
  modes?: ViewMode[];
}>();

const emit = defineEmits<{ (e: 'update:viewMode', v: ViewMode): void }>();

const { t } = useLocale(() => props.locale);

function offers(v: ViewMode): boolean {
  return !props.modes || props.modes.includes(v);
}
</script>

<template>
  <div class="fe-toolbar__view" role="tablist" :aria-label="t('toolbar.view_label')">
    <button
      v-if="offers('list')"
      type="button"
      class="fe-btn fe-btn--icon-only"
      :class="{ 'is-active': viewMode === 'list' }"
      role="tab"
      :aria-selected="viewMode === 'list'"
      :title="t('toolbar.view.list')"
      :aria-label="t('toolbar.view.list')"
      data-testid="view-list"
      @click="emit('update:viewMode', 'list')"
    >
      <span class="fe-icon" aria-hidden="true">☰</span>
    </button>
    <button
      v-if="offers('grid')"
      type="button"
      class="fe-btn fe-btn--icon-only"
      :class="{ 'is-active': viewMode === 'grid' }"
      role="tab"
      :aria-selected="viewMode === 'grid'"
      :title="t('toolbar.view.grid')"
      :aria-label="t('toolbar.view.grid')"
      data-testid="view-grid"
      @click="emit('update:viewMode', 'grid')"
    >
      <span class="fe-icon" aria-hidden="true">▦</span>
    </button>
    <button
      v-if="offers('gallery')"
      type="button"
      class="fe-btn fe-btn--icon-only"
      :class="{ 'is-active': viewMode === 'gallery' }"
      role="tab"
      :aria-selected="viewMode === 'gallery'"
      :title="t('toolbar.view.gallery')"
      :aria-label="t('toolbar.view.gallery')"
      data-testid="view-gallery"
      @click="emit('update:viewMode', 'gallery')"
    >
      <span class="fe-icon" aria-hidden="true">▣</span>
    </button>
  </div>
</template>
