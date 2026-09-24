<script setup lang="ts">
import { computed, ref, useId } from 'vue';
import { onFieldInvalid } from '@/lib/formCheck';

interface Props {
  modelValue?: string | number | null;
  type?: string;
  label?: string;
  placeholder?: string;
  hint?: string;
  error?: string | null;
  required?: boolean;
  disabled?: boolean;
  readonly?: boolean;
  autocomplete?: string;
  inputmode?: string;
  size?: 'sm' | 'md' | 'lg';
  prefixIcon?: string;
  suffixIcon?: string;
  monospace?: boolean;
  min?: number | string;
  max?: number | string;
  step?: number | string;
  name?: string;
}

const props = withDefaults(defineProps<Props>(), {
  type: 'text',
  size: 'md',
});

const emit = defineEmits<{
  (e: 'update:modelValue', v: string | number | null): void;
  (e: 'blur', ev: FocusEvent): void;
  (e: 'focus', ev: FocusEvent): void;
  (e: 'enter'): void;
}>();

const fallbackId = useId();
const inputId = computed(() => props.name ?? fallbackId);

/* The browser's verdict on this box (`required`, `type="url"`, `min`…), said
 * in the panel's language under the box — its own bubble is suppressed
 * (lib/formCheck). Cleared as soon as the person types. */
const nativeError = ref('');
const shownError = computed(() => props.error || nativeError.value);

function onInput(ev: Event) {
  nativeError.value = '';
  const target = ev.target as HTMLInputElement;
  let v: string | number = target.value;
  if (props.type === 'number' && target.value !== '') v = target.valueAsNumber;
  emit('update:modelValue', v);
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
    <label v-if="label" :for="inputId" class="label-base">
      {{ label }}
      <span v-if="required" class="text-rose-500" aria-hidden="true">*</span>
    </label>
    <input
      :id="inputId"
      :name="name"
      :type="type"
      :value="modelValue ?? ''"
      :placeholder="placeholder"
      :required="required"
      :disabled="disabled"
      :readonly="readonly"
      :autocomplete="autocomplete"
      :inputmode="inputmode as any"
      :min="min"
      :max="max"
      :step="step"
      :aria-invalid="shownError ? 'true' : undefined"
      :aria-describedby="shownError ? `${inputId}-err` : hint ? `${inputId}-hint` : undefined"
      :class="[
        'input-base',
        padding,
        monospace && 'font-mono',
        shownError && 'border-rose-500 focus:border-rose-500 focus:ring-rose-500/30',
      ]"
      @invalid="(e) => (nativeError = onFieldInvalid(e))"
      @input="onInput"
      @blur="(e) => emit('blur', e)"
      @focus="(e) => emit('focus', e)"
      @keydown.enter="emit('enter')"
    />
    <p v-if="shownError" :id="`${inputId}-err`" class="error-text" data-testid="field-error">{{ shownError }}</p>
    <p v-else-if="hint" :id="`${inputId}-hint`" class="help-text">{{ hint }}</p>
  </div>
</template>
