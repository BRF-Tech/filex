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

watch(
  () => props.open,
  (v) => {
    if (v) {
      document.addEventListener('keydown', onKey);
      setTimeout(() => {
        const focusable = cardEl.value?.querySelector<HTMLElement>(
          'input,select,textarea,button,[tabindex]:not([tabindex="-1"])',
        );
        focusable?.focus();
      }, 30);
    } else {
      document.removeEventListener('keydown', onKey);
    }
  },
);

onBeforeUnmount(() => document.removeEventListener('keydown', onKey));

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close');
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
        @click.stop
      >
        <header v-if="title && !chromeless" class="fe-modal__head">
          <h2 class="fe-modal__title">{{ title }}</h2>
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
