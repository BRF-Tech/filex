<script setup lang="ts">
/**
 * SurfacePinInput — a plugin surface's `pin-input` node: a short code, 4..8
 * characters, into `data.values[id]`. One input with the browser's one-time
 * code affordances rather than N boxes: paste works, screen readers read one
 * field, and the length is enforced by the same line that draws it.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../../../types/ExplorerConfig';
import { useLocale } from '../../../composables/useLocale';
import { pinLength } from '../../../lib/surfaceValues';

const props = defineProps<{
  id: string;
  length?: number | unknown;
  modelValue: string;
  locale: LocaleCode;
  disabled?: boolean;
  invalid?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: string): void;
}>();

const { t } = useLocale(() => props.locale);

const len = computed(() => pinLength(props.length));

function onInput(ev: Event) {
  const raw = (ev.target as HTMLInputElement).value.replace(/\s+/g, '').slice(0, len.value);
  (ev.target as HTMLInputElement).value = raw;
  emit('update:modelValue', raw);
}
</script>

<template>
  <div class="fe-spin" data-testid="surface-pin">
    <label class="fe-cfield__label" :for="`fe-spin-${id}`">{{ t('plugin.pin.label') }}</label>
    <input
      :id="`fe-spin-${id}`"
      class="fe-cfield__input fe-cfield__input--mono fe-spin__input"
      :class="{ 'is-invalid': invalid }"
      type="text"
      inputmode="numeric"
      autocomplete="one-time-code"
      spellcheck="false"
      :maxlength="len"
      :size="len"
      :value="modelValue"
      :disabled="disabled"
      :aria-invalid="invalid ? 'true' : undefined"
      :style="{ '--spin-len': len }"
      @input="onInput"
    />
  </div>
</template>
