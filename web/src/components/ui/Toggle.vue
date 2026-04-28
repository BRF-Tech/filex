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

function toggle() {
  if (props.disabled) return;
  emit('update:modelValue', !props.modelValue);
}
</script>

<template>
  <div class="flex items-start gap-3">
    <button
      :id="id"
      type="button"
      role="switch"
      :aria-checked="modelValue"
      :aria-disabled="disabled || undefined"
      :disabled="disabled"
      :class="[
        'relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors',
        modelValue ? 'bg-brand-600' : 'bg-zinc-300 dark:bg-zinc-700',
        disabled && 'opacity-50 cursor-not-allowed',
      ]"
      @click="toggle"
    >
      <span
        class="inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform"
        :class="modelValue ? 'translate-x-4' : 'translate-x-0.5'"
      />
    </button>
    <div v-if="label || description" class="flex flex-col">
      <label v-if="label" :for="id" class="text-sm font-medium text-zinc-800 dark:text-zinc-100 cursor-pointer">
        {{ label }}
      </label>
      <p v-if="description" class="text-xs text-zinc-500 dark:text-zinc-400">
        {{ description }}
      </p>
    </div>
  </div>
</template>
