<script setup lang="ts">
/**
 * SurfacePinInput — a plugin surface's `pin-input` node: a short code, 4..8
 * characters, into `data.values[id]`. One input with the browser's one-time
 * code affordances rather than N boxes: paste works, screen readers read one
 * field.
 *
 * ⚠ The length is the HOST's: the server writes every `pin-input` node's
 * `length` (clamped to the contract's 4..8, 6 when the app named none;
 * wasmplugin.PinLength) before the screen leaves it, and refuses a code of
 * any other length at submit in its own words. This box only draws that
 * number — it does not decide it.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../../../types/ExplorerConfig';
import { useLocale } from '../../../composables/useLocale';

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

/** The host's length; nothing to hold the box to when a node carries none. */
const len = computed<number | undefined>(() => {
  const n = Number(props.length);
  return Number.isInteger(n) && n > 0 ? n : undefined;
});

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
