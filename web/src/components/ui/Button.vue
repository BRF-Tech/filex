<script setup lang="ts">
import { computed } from 'vue';
import Spinner from './Spinner.vue';

interface Props {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost' | 'outline';
  size?: 'xs' | 'sm' | 'md' | 'lg';
  type?: 'button' | 'submit' | 'reset';
  loading?: boolean;
  disabled?: boolean;
  block?: boolean;
  as?: 'button' | 'a';
  href?: string;
  to?: string | object;
}

const props = withDefaults(defineProps<Props>(), {
  variant: 'primary',
  size: 'md',
  type: 'button',
  loading: false,
  disabled: false,
  block: false,
  as: 'button',
});

const baseClasses =
  'inline-flex items-center justify-center gap-2 font-medium rounded-md ' +
  'transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 ' +
  'disabled:opacity-50 disabled:cursor-not-allowed select-none';

const variantClasses = computed(() => {
  switch (props.variant) {
    case 'primary':
      return 'bg-brand-600 text-white hover:bg-brand-700 focus-visible:ring-brand-500 dark:focus-visible:ring-offset-zinc-900';
    case 'secondary':
      return 'bg-zinc-100 text-zinc-900 hover:bg-zinc-200 focus-visible:ring-zinc-400 dark:bg-zinc-800 dark:text-zinc-100 dark:hover:bg-zinc-700 dark:focus-visible:ring-offset-zinc-900';
    case 'danger':
      return 'bg-rose-600 text-white hover:bg-rose-700 focus-visible:ring-rose-500 dark:focus-visible:ring-offset-zinc-900';
    case 'ghost':
      return 'text-zinc-700 hover:bg-zinc-100 focus-visible:ring-zinc-400 dark:text-zinc-200 dark:hover:bg-zinc-800';
    case 'outline':
      return 'border border-zinc-300 text-zinc-800 hover:bg-zinc-50 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:text-zinc-100 dark:hover:bg-zinc-800';
    default:
      return '';
  }
});

const sizeClasses = computed(() => {
  switch (props.size) {
    case 'xs':
      return 'text-xs px-2 py-1';
    case 'sm':
      return 'text-sm px-2.5 py-1.5';
    case 'md':
      return 'text-sm px-3.5 py-2';
    case 'lg':
      return 'text-base px-4 py-2.5';
    default:
      return '';
  }
});

const widthClasses = computed(() => (props.block ? 'w-full' : ''));
</script>

<template>
  <component
    :is="as === 'a' ? 'a' : 'button'"
    :type="as === 'button' ? type : undefined"
    :href="as === 'a' ? href : undefined"
    :disabled="disabled || loading"
    :aria-busy="loading || undefined"
    :class="[baseClasses, variantClasses, sizeClasses, widthClasses]"
  >
    <Spinner v-if="loading" :size="size === 'lg' ? 'sm' : 'xs'" />
    <slot />
  </component>
</template>
