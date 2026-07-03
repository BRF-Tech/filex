<script setup lang="ts">
import { computed, useId } from 'vue';

interface Props {
  modelValue: boolean;
  label?: string;
  description?: string;
  disabled?: boolean;
  name?: string;
}

const props = defineProps<Props>();
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void }>();

const fallback = useId();
const id = computed(() => props.name ?? fallback);
</script>

<template>
  <label :for="id" class="flex items-start gap-2 cursor-pointer">
    <input
      :id="id"
      :name="name"
      type="checkbox"
      class="mt-0.5 h-4 w-4 rounded border-zinc-300 text-brand-600 focus:ring-brand-500 dark:border-zinc-600 dark:bg-zinc-900"
      :checked="modelValue"
      :disabled="disabled"
      @change="(e) => emit('update:modelValue', (e.target as HTMLInputElement).checked)"
    />
    <span v-if="label || description" class="flex flex-col leading-tight">
      <span v-if="label" class="text-sm text-zinc-800 dark:text-zinc-100">{{ label }}</span>
      <span v-if="description" class="text-xs text-zinc-500 dark:text-zinc-400">{{
        description
      }}</span>
    </span>
  </label>
</template>
