<script setup lang="ts">
import { computed, ref, useId } from 'vue';
import { onFieldInvalid } from '@/lib/formCheck';

interface Option {
  value: string | number;
  label: string;
  disabled?: boolean;
}

interface Props {
  modelValue?: string | number | null;
  options: Option[];
  label?: string;
  placeholder?: string;
  hint?: string;
  error?: string | null;
  required?: boolean;
  disabled?: boolean;
  size?: 'sm' | 'md' | 'lg';
  name?: string;
}

const props = withDefaults(defineProps<Props>(), { size: 'md' });

const emit = defineEmits<{
  (e: 'update:modelValue', v: string | number | null): void;
  (e: 'change', v: string | number | null): void;
}>();

const fallback = useId();
const selectId = computed(() => props.name ?? fallback);

/* The browser's verdict, in the panel's language (see ui/Input). */
const nativeError = ref('');
const shownError = computed(() => props.error || nativeError.value);

function onChange(ev: Event) {
  nativeError.value = '';
  const v = (ev.target as HTMLSelectElement).value;
  // Coerce back to number if every option is numeric.
  const allNumeric = props.options.every((o) => typeof o.value === 'number');
  const out: string | number = allNumeric ? Number(v) : v;
  emit('update:modelValue', out);
  emit('change', out);
}

const padding = computed(() => {
  switch (props.size) {
    case 'sm':
      return 'px-2.5 py-1 text-sm';
    case 'lg':
      return 'px-4 py-2.5 text-base';
    default:
      return 'px-3 py-2 text-sm';
  }
});
</script>

<template>
  <div class="space-y-1">
    <label v-if="label" :for="selectId" class="label-base">
      {{ label }}
      <span v-if="required" class="text-rose-500" aria-hidden="true">*</span>
    </label>
    <div class="relative">
      <select
        :id="selectId"
        :name="name"
        :value="modelValue ?? ''"
        :required="required"
        :disabled="disabled"
        :aria-invalid="shownError ? 'true' : undefined"
        :class="[
          'input-base appearance-none pe-9',
          padding,
          shownError && 'border-rose-500 focus:border-rose-500 focus:ring-rose-500/30',
        ]"
        @invalid="(e) => (nativeError = onFieldInvalid(e))"
        @change="onChange"
      >
        <option v-if="placeholder" value="" disabled>{{ placeholder }}</option>
        <option
          v-for="opt in options"
          :key="opt.value"
          :value="opt.value"
          :disabled="opt.disabled"
        >
          {{ opt.label }}
        </option>
      </select>
      <svg
        class="pointer-events-none absolute end-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-zinc-500"
        viewBox="0 0 20 20"
        fill="currentColor"
        aria-hidden="true"
      >
        <path
          fill-rule="evenodd"
          d="M5.23 7.21a.75.75 0 011.06.02L10 11.06l3.71-3.83a.75.75 0 111.08 1.04l-4.24 4.38a.75.75 0 01-1.08 0L5.21 8.27a.75.75 0 01.02-1.06z"
          clip-rule="evenodd"
        />
      </svg>
    </div>
    <p v-if="shownError" class="error-text" data-testid="field-error">{{ shownError }}</p>
    <p v-else-if="hint" class="help-text">{{ hint }}</p>
  </div>
</template>
