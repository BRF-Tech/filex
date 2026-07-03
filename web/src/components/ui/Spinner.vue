<script setup lang="ts">
import { computed } from 'vue';

interface Props {
  size?: 'xs' | 'sm' | 'md' | 'lg';
  label?: string;
}

const props = withDefaults(defineProps<Props>(), { size: 'md' });

const sizeClass = computed(() => {
  switch (props.size) {
    case 'xs':
      return 'h-3 w-3 border-[1.5px]';
    case 'sm':
      return 'h-4 w-4 border-2';
    case 'md':
      return 'h-6 w-6 border-2';
    case 'lg':
      return 'h-10 w-10 border-[3px]';
    default:
      return '';
  }
});
</script>

<template>
  <span
    role="status"
    :aria-label="label"
    class="inline-block rounded-full border-current border-t-transparent animate-spin align-[-0.125em]"
    :class="sizeClass"
  />
</template>
