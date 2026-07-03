<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue';

interface Props {
  modelValue: boolean;
  title?: string;
  size?: 'sm' | 'md' | 'lg' | 'xl';
  closeOnBackdrop?: boolean;
  preventClose?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  size: 'md',
  closeOnBackdrop: true,
});

const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void;
  (e: 'close'): void;
}>();

const dialog = ref<HTMLDialogElement | null>(null);

function close() {
  if (props.preventClose) return;
  emit('update:modelValue', false);
  emit('close');
}

watch(
  () => props.modelValue,
  (v) => {
    const el = dialog.value;
    if (!el) return;
    if (v && !el.open) {
      el.showModal();
    } else if (!v && el.open) {
      el.close();
    }
  },
  { flush: 'post' },
);

function onCancel(ev: Event) {
  // The native <dialog> 'cancel' event fires on Esc; honor preventClose.
  if (props.preventClose) {
    ev.preventDefault();
    return;
  }
  emit('update:modelValue', false);
  emit('close');
}

function onBackdropClick(ev: MouseEvent) {
  if (!props.closeOnBackdrop) return;
  if (ev.target === dialog.value) close();
}

onBeforeUnmount(() => {
  if (dialog.value?.open) dialog.value.close();
});

const sizeClass = {
  sm: 'max-w-sm',
  md: 'max-w-md',
  lg: 'max-w-2xl',
  xl: 'max-w-4xl',
}[props.size];
</script>

<template>
  <dialog
    ref="dialog"
    class="m-0 w-full rounded-lg bg-transparent p-0 backdrop:bg-zinc-950/60 backdrop:backdrop-blur-sm"
    @cancel="onCancel"
    @click="onBackdropClick"
  >
    <div
      class="card mx-auto my-10 w-full animate-scale-in"
      :class="sizeClass"
      role="dialog"
      aria-modal="true"
    >
      <header v-if="title || $slots.header" class="card-header flex items-start justify-between gap-4">
        <div class="min-w-0">
          <slot name="header">
            <h2 class="text-base font-semibold text-zinc-900 dark:text-zinc-100 truncate">
              {{ title }}
            </h2>
          </slot>
        </div>
        <button
          v-if="!preventClose"
          type="button"
          class="rounded-md p-1 text-zinc-500 hover:text-zinc-800 hover:bg-zinc-100 dark:hover:text-zinc-100 dark:hover:bg-zinc-800"
          aria-label="Close"
          @click="close"
        >
          <svg class="h-5 w-5" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
            <path
              fill-rule="evenodd"
              d="M4.22 4.22a.75.75 0 011.06 0L10 8.94l4.72-4.72a.75.75 0 111.06 1.06L11.06 10l4.72 4.72a.75.75 0 11-1.06 1.06L10 11.06l-4.72 4.72a.75.75 0 01-1.06-1.06L8.94 10 4.22 5.28a.75.75 0 010-1.06z"
              clip-rule="evenodd"
            />
          </svg>
        </button>
      </header>

      <div class="card-body">
        <slot />
      </div>

      <footer v-if="$slots.footer" class="card-header flex items-center justify-end gap-2 border-t border-zinc-200 dark:border-zinc-800 border-b-0">
        <slot name="footer" />
      </footer>
    </div>
  </dialog>
</template>
