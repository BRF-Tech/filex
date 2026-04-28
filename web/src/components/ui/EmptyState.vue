<script setup lang="ts">
import type { Component } from 'vue';

interface Props {
  icon?: Component;
  title: string;
  description?: string;
  size?: 'sm' | 'md' | 'lg';
}

withDefaults(defineProps<Props>(), { size: 'md' });
</script>

<template>
  <div
    class="flex flex-col items-center justify-center text-center py-12 px-6 text-zinc-600 dark:text-zinc-400"
    :class="size === 'sm' ? 'py-8' : size === 'lg' ? 'py-16' : ''"
  >
    <div
      v-if="icon"
      class="mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-zinc-100 dark:bg-zinc-800 text-zinc-500 dark:text-zinc-400"
      aria-hidden="true"
    >
      <component :is="icon" class="h-6 w-6" />
    </div>
    <h3 class="text-base font-semibold text-zinc-900 dark:text-zinc-100">{{ title }}</h3>
    <p v-if="description" class="mt-1 max-w-md text-sm">{{ description }}</p>
    <div v-if="$slots.action" class="mt-4">
      <slot name="action" />
    </div>
  </div>
</template>
