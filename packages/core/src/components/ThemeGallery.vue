<script setup lang="ts">
/**
 * ThemeGallery — wiring:c1 theme picker modal.
 *
 * A grid of theme cards, each rendering a mini live preview (surface +
 * toolbar chips + sample rows) painted with that theme's tokens for the
 * CURRENTLY resolved light/dark mode — so the card shows exactly what
 * you'd get. Click applies instantly (live preview behind the modal) and
 * persists; the selected card carries a ✓. "Varsayılana dön" resets.
 *
 * Presentational only — selection state + persistence live in
 * lib/themes.ts (shared across instances); the parent forwards `select`.
 */
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { THEMES, DEFAULT_THEME_ID, type ThemeDef } from '../lib/themes';
import Modal from '../modals/Modal.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** Host mode (forwarded to Modal so the backdrop themes correctly). */
  theme?: ThemeMode;
  /** Resolved mode — decides which variant the preview cards paint. */
  dark: boolean;
  /** Currently selected theme id. */
  current: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'select', id: string): void;
}>();

const { t } = useLocale(() => props.locale);

/** Inline variables for one card — a self-contained mini `--fe-*` scope,
 *  so the preview markup can use the exact same tokens the real UI uses. */
function cardVars(th: ThemeDef): Record<string, string> {
  return props.dark ? th.dark : th.light;
}

const DEFAULT_ID = DEFAULT_THEME_ID;
</script>

<template>
  <Modal :open="open" :title="t('theme.title')" size="lg" :theme="theme" @close="emit('close')">
    <p class="fe-themes__hint">{{ t('theme.hint') }}</p>
    <div class="fe-themes" role="listbox" :aria-label="t('theme.title')">
      <button
        v-for="th in THEMES"
        :key="th.id"
        type="button"
        class="fe-themecard"
        :class="{ 'is-selected': th.id === current }"
        role="option"
        :aria-selected="th.id === current"
        :style="cardVars(th)"
        @click="emit('select', th.id)"
      >
        <span class="fe-themecard__preview" aria-hidden="true">
          <span class="fe-themecard__topbar">
            <span class="fe-themecard__chip fe-themecard__chip--primary"></span>
            <span class="fe-themecard__chip"></span>
            <span class="fe-themecard__chip fe-themecard__chip--ghost"></span>
          </span>
          <span class="fe-themecard__row">
            <span class="fe-themecard__dot"></span>
            <span class="fe-themecard__line" style="width: 62%"></span>
          </span>
          <span class="fe-themecard__row fe-themecard__row--selected">
            <span class="fe-themecard__dot fe-themecard__dot--primary"></span>
            <span class="fe-themecard__line" style="width: 44%"></span>
          </span>
          <span class="fe-themecard__row">
            <span class="fe-themecard__dot fe-themecard__dot--muted"></span>
            <span class="fe-themecard__line fe-themecard__line--muted" style="width: 74%"></span>
          </span>
        </span>
        <span class="fe-themecard__name">
          {{ t(th.nameKey) }}
          <span v-if="th.id === current" class="fe-themecard__check" :title="t('theme.selected')">✓</span>
        </span>
      </button>
    </div>
    <template #actions>
      <button
        type="button"
        class="fe-btn"
        :disabled="current === DEFAULT_ID"
        @click="emit('select', DEFAULT_ID)"
      >
        {{ t('theme.reset') }}
      </button>
      <button type="button" class="fe-btn fe-btn--primary" @click="emit('close')">
        {{ t('theme.close') }}
      </button>
    </template>
  </Modal>
</template>

<!-- Deliberately NOT scoped: the webcomponent injects the CORE package's
     built style.css, whose data-v scope ids don't match the ids the
     webcomponent's own compile assigns (verified: fe-themecard got
     data-v-99a95ed7 in core vs data-v-5d4cc1fd in the wc bundle) — scoped
     rules would silently not apply in embeds. Class names are fe-*
     namespaced, so global is collision-safe (same approach as base.css). -->
<style>
.fe-themes__hint {
  margin: 0 0 12px;
  font-size: 12.5px;
  color: var(--fe-text-muted);
}

.fe-themes {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 12px;
}

/* Each card is its own mini token scope (inline --fe-* vars from the theme
   definition), so every color below refers to the CARD's theme — not the
   active page theme. */
.fe-themecard {
  display: flex;
  flex-direction: column;
  padding: 0;
  border: 2px solid var(--fe-border-strong);
  border-radius: var(--fe-radius, 8px);
  background: var(--fe-bg);
  cursor: pointer;
  overflow: hidden;
  text-align: left;
  transition: transform 0.12s ease, box-shadow 0.12s ease;
  font: inherit;
}

.fe-themecard:hover {
  transform: translateY(-2px);
  box-shadow: var(--fe-shadow-sm, 0 2px 6px rgba(15, 23, 42, 0.08));
}

.fe-themecard:focus-visible {
  outline: 2px solid var(--fe-primary);
  outline-offset: 2px;
}

.fe-themecard.is-selected {
  border-color: var(--fe-primary);
  box-shadow: 0 0 0 2px var(--fe-primary);
}

.fe-themecard__preview {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px;
  background: var(--fe-bg);
  border-bottom: 1px solid var(--fe-border);
  min-height: 84px;
}

.fe-themecard__topbar {
  display: flex;
  gap: 4px;
  margin-bottom: 2px;
}

.fe-themecard__chip {
  height: 8px;
  width: 22px;
  border-radius: 4px;
  background: var(--fe-bg-hover);
  border: 1px solid var(--fe-border);
}

.fe-themecard__chip--primary {
  background: var(--fe-primary);
  border-color: var(--fe-primary);
}

.fe-themecard__chip--ghost {
  background: var(--fe-bg-elev);
}

.fe-themecard__row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 4px;
  border-radius: 4px;
}

.fe-themecard__row--selected {
  background: var(--fe-bg-selected);
}

.fe-themecard__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--fe-text);
  opacity: 0.75;
  flex: none;
}

.fe-themecard__dot--primary {
  background: var(--fe-primary);
  opacity: 1;
}

.fe-themecard__dot--muted {
  background: var(--fe-text-muted);
}

.fe-themecard__line {
  height: 6px;
  border-radius: 3px;
  background: var(--fe-text);
  opacity: 0.55;
}

.fe-themecard__line--muted {
  background: var(--fe-text-muted);
}

.fe-themecard__name {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--fe-text);
  background: var(--fe-bg-elev);
  font-family: var(--fe-font, inherit);
}

.fe-themecard__check {
  flex: none;
  width: 18px;
  height: 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: var(--fe-primary);
  color: var(--fe-text-on-primary);
  font-size: 11px;
  line-height: 1;
}
</style>
