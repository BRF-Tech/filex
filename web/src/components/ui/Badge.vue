<script setup lang="ts">
import { computed } from 'vue';

interface Props {
  tone?: 'brand' | 'emerald' | 'rose' | 'amber' | 'sky' | 'zinc' | 'violet';
  size?: 'xs' | 'sm' | 'md';
  dot?: boolean;
  pill?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  tone: 'zinc',
  size: 'sm',
  pill: true,
});

const toneClass = computed(() => {
  return {
    brand: 'bg-brand-50 text-brand-700 ring-brand-600/20 dark:bg-brand-500/10 dark:text-brand-300 dark:ring-brand-500/30',
    emerald: 'bg-emerald-50 text-emerald-700 ring-emerald-600/20 dark:bg-emerald-500/10 dark:text-emerald-300 dark:ring-emerald-500/30',
    rose: 'bg-rose-50 text-rose-700 ring-rose-600/20 dark:bg-rose-500/10 dark:text-rose-300 dark:ring-rose-500/30',
    amber: 'bg-amber-50 text-amber-700 ring-amber-600/20 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/30',
    sky: 'bg-sky-50 text-sky-700 ring-sky-600/20 dark:bg-sky-500/10 dark:text-sky-300 dark:ring-sky-500/30',
    zinc: 'bg-zinc-100 text-zinc-700 ring-zinc-300 dark:bg-zinc-800 dark:text-zinc-300 dark:ring-zinc-700',
    violet: 'bg-violet-50 text-violet-700 ring-violet-600/20 dark:bg-violet-500/10 dark:text-violet-300 dark:ring-violet-500/30',
  }[props.tone];
});

const dotColor = computed(
  () =>
    ({
      brand: 'bg-brand-500',
      emerald: 'bg-emerald-500',
      rose: 'bg-rose-500',
      amber: 'bg-amber-500',
      sky: 'bg-sky-500',
      zinc: 'bg-zinc-400',
      violet: 'bg-violet-500',
    })[props.tone],
);

const sizeClass = computed(() => {
  switch (props.size) {
    case 'xs':
      return 'text-[10px] px-1.5 py-0.5 gap-1';
    case 'md':
      return 'text-sm px-2.5 py-1 gap-1.5';
    default:
      return 'text-xs px-2 py-0.5 gap-1.5';
  }
});
</script>

<template>
  <span
    class="inline-flex items-center font-medium ring-1 ring-inset"
    :class="[toneClass, sizeClass, pill ? 'rounded-full' : 'rounded-md']"
  >
    <span v-if="dot" class="h-1.5 w-1.5 rounded-full" :class="dotColor" />
    <slot />
  </span>
</template>
