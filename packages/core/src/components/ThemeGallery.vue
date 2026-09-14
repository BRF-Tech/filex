<script setup lang="ts">
/**
 * ThemeGallery — wiring:c1 theme picker modal.
 *
 * A grid of theme cards, each rendering a mini live preview (surface +
 * toolbar chips + sample rows) painted with that theme's tokens for the
 * CURRENTLY resolved light/dark mode — so the card shows exactly what
 * you'd get. Click applies instantly (live preview behind the modal) and
 * persists; the selected card carries a ✓. "Reset to default" resets.
 *
 * Presentational only — selection state + persistence live in
 * lib/themes.ts (shared across instances); the parent forwards `select`.
 *
 * ⚠ The cards themselves live in `ThemePalette.vue` and are mounted, not
 * copied: the web app's user-settings modal shows the same grid inside a pane
 * and cannot mount this component, because this one IS a modal. See that
 * file's header.
 */
import { computed } from 'vue';
import type { LocaleCode, ThemeMode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { DEFAULT_THEME_ID, type ThemeModePref } from '../lib/themes';
import Modal from '../modals/Modal.vue';
import ThemePalette from './ThemePalette.vue';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  /** Host mode (forwarded to Modal so the backdrop themes correctly). */
  theme?: ThemeMode;
  /** Resolved mode — decides which variant the preview cards paint. */
  dark: boolean;
  /** Currently selected theme id. */
  current: string;
  /** The user's own light/dark preference; 'host' = never chosen. */
  mode: ThemeModePref;
  /** What the embedder asked for — what 'host' resolves to. */
  hostMode: ThemeMode;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'select', id: string): void;
  (e: 'mode', mode: ThemeModePref): void;
}>();

const { t } = useLocale(() => props.locale);

/* ---- light / dark / auto -------------------------------------------
 * A theme says which palette; this says whether it paints its light or its
 * dark variant. Two separate questions, one place to answer both — the theme
 * button is where people come looking for "night mode".
 *
 * Only three choices are ever offered. 'host' is an internal default, not an
 * option: when it is still in force the control highlights whichever of the
 * three the embedder asked for, so the strip always shows the truth, and the
 * first click pins an explicit choice. */
const MODES: { id: Exclude<ThemeModePref, 'host'>; labelKey: string }[] = [
  { id: 'light', labelKey: 'theme.mode.light' },
  { id: 'dark', labelKey: 'theme.mode.dark' },
  { id: 'auto', labelKey: 'theme.mode.auto' },
];

const shownMode = computed(() => (props.mode === 'host' ? props.hostMode : props.mode));

const DEFAULT_ID = DEFAULT_THEME_ID;
</script>

<template>
  <Modal :open="open" :title="t('theme.title')" size="lg" :theme="theme" @close="emit('close')">
    <div class="fe-thememode" role="group" :aria-label="t('theme.mode.label')">
      <span class="fe-thememode__label">{{ t('theme.mode.label') }}</span>
      <div class="fe-thememode__seg">
        <button
          v-for="m in MODES"
          :key="m.id"
          type="button"
          class="fe-thememode__opt"
          :class="{ 'is-active': shownMode === m.id }"
          :aria-pressed="shownMode === m.id"
          @click="emit('mode', m.id)"
        >
          <svg
            class="fe-ficon"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.8"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
            focusable="false"
          >
            <template v-if="m.id === 'light'">
              <circle cx="12" cy="12" r="4" />
              <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
            </template>
            <template v-else-if="m.id === 'dark'">
              <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />
            </template>
            <template v-else>
              <!-- Half-filled disc: the mode that is neither, decided elsewhere. -->
              <circle cx="12" cy="12" r="9" />
              <path d="M12 3a9 9 0 0 1 0 18z" fill="currentColor" stroke="none" />
            </template>
          </svg>
          <span>{{ t(m.labelKey) }}</span>
        </button>
      </div>
    </div>
    <ThemePalette
      :locale="locale"
      :dark="dark"
      :current="current"
      @select="(id: string) => emit('select', id)"
    />
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
