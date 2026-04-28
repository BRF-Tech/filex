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
      class="fe-modal__backdrop"
      role="presentation"
      @click="onBackdrop"
    >
      <div
        ref="cardEl"
        class="fe-modal__card"
        :class="`fe-modal__card--${size || 'md'}`"
        role="dialog"
        aria-modal="true"
        @click.stop
      >
        <header v-if="title" class="fe-modal__head">
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
        <footer v-if="$slots.actions" class="fe-modal__actions">
          <slot name="actions" />
        </footer>
      </div>
    </div>
  </transition>
</template>
