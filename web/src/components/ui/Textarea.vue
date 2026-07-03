<script setup lang="ts">
import { computed, useId } from 'vue';

interface Props {
  modelValue?: string | null;
  label?: string;
  placeholder?: string;
  hint?: string;
  error?: string | null;
  required?: boolean;
  disabled?: boolean;
  rows?: number;
  monospace?: boolean;
  name?: string;
}

const props = withDefaults(defineProps<Props>(), { rows: 4 });
const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>();

const fallback = useId();
const id = computed(() => props.name ?? fallback);
</script>

<template>
  <div class="space-y-1">
    <label v-if="label" :for="id" class="label-base">
      {{ label }}
      <span v-if="required" class="text-rose-500" aria-hidden="true">*</span>
    </label>
    <textarea
      :id="id"
      :name="name"
      :value="modelValue ?? ''"
      :placeholder="placeholder"
      :required="required"
      :disabled="disabled"
      :rows="rows"
      :class="[
        'input-base px-3 py-2 text-sm',
        monospace && 'font-mono',
        error && 'border-rose-500 focus:border-rose-500 focus:ring-rose-500/30',
      ]"
      @input="(e) => emit('update:modelValue', (e.target as HTMLTextAreaElement).value)"
    />
    <p v-if="error" class="error-text">{{ error }}</p>
    <p v-else-if="hint" class="help-text">{{ hint }}</p>
  </div>
</template>
