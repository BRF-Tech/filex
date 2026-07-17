<script setup lang="ts">
/**
 * Modal — tiny headless wrapper. Backdrop + centered card + ESC + autofocus.
 */
import { watch, onBeforeUnmount, ref } from 'vue';

const props = defineProps<{
  open: boolean;
  title?: string;
  size?: 'sm' | 'md' | 'lg' | 'xl';
  closeOnBackdrop?: boolean;
  /** When true, drop the dialog chrome (backdrop tint, header, footer,
   *  centered card with border-radius) and render the slot full-bleed.
   *  Used by the standalone /files/edit route where the browser tab IS
   *  the container — a modal frame on top of it just steals real estate
   *  from the editor. ESC + emit('close') still wire through so the
   *  parent route can window.close() the tab. */
  chromeless?: boolean;
  /** Explicit theme. When set the modal stamps the appropriate
   *  `.fe--theme-{light,dark}` class on its `.fe` backdrop so the CSS
   *  variable cascade matches the host shell regardless of OS
   *  preference (Modal is a portal-shaped descendant of `<body>`
   *  rather than a child of the parent component, so plain DOM
   *  inheritance isn't always enough). */
  theme?: 'light' | 'dark' | 'auto';
}>();

const emit = defineEmits<{
  (e: 'close'): void;
}>();

const cardEl = ref<HTMLElement | null>(null);

/* wiring:c4 — a11y: unique title id for aria-labelledby + focus restore.
 * The element focused before the modal opened gets focus back on close so
 * keyboard users return to where they were instead of <body>. */
let modalSeq = 0;
const titleId = `fe-modal-title-${++modalSeq}-${Math.random().toString(36).slice(2, 7)}`;
let prevFocus: HTMLElement | null = null;

const FOCUSABLE =
  'input:not([disabled]),select:not([disabled]),textarea:not([disabled]),' +
  'button:not([disabled]),a[href],[tabindex]:not([tabindex="-1"])';

watch(
  () => props.open,
  (v) => {
    if (v) {
      prevFocus = (document.activeElement as HTMLElement | null) ?? null; /* wiring:c4 */
      document.addEventListener('keydown', onKey);
      setTimeout(() => {
        const focusable = cardEl.value?.querySelector<HTMLElement>(FOCUSABLE);
        focusable?.focus();
      }, 30);
    } else {
      document.removeEventListener('keydown', onKey);
      /* wiring:c4 — return focus to the opener. */
      prevFocus?.focus?.();
      prevFocus = null;
    }
  },
);

onBeforeUnmount(() => document.removeEventListener('keydown', onKey));

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    emit('close');
    return;
  }
  /* wiring:c4 — focus trap: Tab cycles inside the card (WAI-ARIA dialog).
   * Without it, Tab walked out into the host page behind the backdrop. */
  if (e.key === 'Tab' && cardEl.value) {
    const nodes = Array.from(cardEl.value.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
      (el) => el.offsetParent !== null || el === document.activeElement,
    );
    if (nodes.length === 0) return;
    const first = nodes[0];
    const last = nodes[nodes.length - 1];
    const active = document.activeElement as HTMLElement | null;
    if (e.shiftKey && (active === first || !cardEl.value.contains(active))) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && (active === last || !cardEl.value.contains(active))) {
      e.preventDefault();
      first.focus();
    }
  }
}

function onBackdrop() {
  if (props.closeOnBackdrop !== false) emit('close');
}
</script>

<template>
  <transition name="fe-modal">
    <div
      v-if="open"
      class="fe fe-modal__backdrop"
      :class="[
        { 'fe-modal__backdrop--chromeless': chromeless },
        theme === 'light' ? 'fe--theme-light' : '',
        theme === 'dark' ? 'fe--theme-dark' : '',
      ]"
      role="presentation"
      @click="onBackdrop"
    >
      <div
        ref="cardEl"
        class="fe-modal__card"
        :class="[
          `fe-modal__card--${size || 'md'}`,
          chromeless && 'fe-modal__card--chromeless',
        ]"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="title && !chromeless ? titleId : undefined"
        :aria-label="!title || chromeless ? title || undefined : undefined"
        @click.stop
      >
        <header v-if="title && !chromeless" class="fe-modal__head">
          <h2 :id="titleId" class="fe-modal__title">{{ title }}</h2>
          <button
            type="button"
            class="fe-modal__close"
            aria-label="Close"
            @click="emit('close')"
          >×</button>
        </header>
        <div class="fe-modal__body">
          <slot />
        </div>
        <footer v-if="$slots.actions && !chromeless" class="fe-modal__actions">
          <slot name="actions" />
        </footer>
      </div>
    </div>
  </transition>
</template>
