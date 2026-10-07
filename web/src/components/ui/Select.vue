<script setup lang="ts">
/**
 * The panel's select field: a label, the control, and a hint or an error
 * under it.
 *
 * ⚠⚠ The control is core's `ChoiceSelect`, not a native select element
 * (#160; the owner, 2026-10-04: "hiç bir yerde öyle basit bir dropdown
 * kullanma"). This file is only the panel's frame around it - the label, the
 * hint, the browser's verdict said in the panel's words (lib/formCheck) - so
 * every select in the admin panel and every one in the explorer is ONE
 * control: one keyboard, one look, one dark mode, one right-to-left layout.
 * Do not grow behaviour here; it belongs in the core component.
 *
 * The props and events are what they were when this wrapped a native select,
 * so no page changed to follow it. Two differences a page may notice, both
 * deliberate:
 *   - the value that comes back is the chosen option's own value (a number
 *     stays a number in a mixed list, where the native one handed back a
 *     string);
 *   - `aria-label` names the control itself now (it used to land on the
 *     wrapper, where a screen reader never read it).
 */
import { computed, ref, useId } from 'vue';
import { ChoiceSelect } from '@brftech/filex-core';
import { validityMessage } from '@/lib/formCheck';

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
  /** The control's accessible name when there is no visible label. */
  ariaLabel?: string;
}

const props = withDefaults(defineProps<Props>(), { size: 'md' });

const emit = defineEmits<{
  (e: 'update:modelValue', v: string | number | null): void;
  (e: 'change', v: string | number | null): void;
}>();

const fallback = useId();
const selectId = computed(() => props.name ?? fallback);
const hintId = computed(() => `${selectId.value}-hint`);

/* The browser's verdict, in the panel's language (see ui/Input). */
const nativeError = ref('');
const shownError = computed(() => props.error || nativeError.value);

function onPick(v: string | number) {
  nativeError.value = '';
  emit('update:modelValue', v);
  emit('change', v);
}

/* ChoiceSelect has already suppressed the browser's bubble and moved the
   focus (the first refused field of the form takes it); what is left is the
   sentence under the field. */
function onInvalid(ev: Event) {
  nativeError.value = validityMessage(ev.target as HTMLInputElement);
}

/* The panel's type sizes, which the control inherits (its height comes from
   core's `--fe-h-*` control heights through `size`). */
const textSize = computed(() => (props.size === 'lg' ? 'text-base' : 'text-sm'));
</script>

<template>
  <div class="space-y-1">
    <label v-if="label" :for="selectId" class="label-base">
      {{ label }}
      <span v-if="required" class="text-rose-500" aria-hidden="true">*</span>
    </label>
    <div :class="textSize">
      <ChoiceSelect
        :id="selectId"
        :name="name"
        :model-value="modelValue ?? ''"
        :options="options"
        :placeholder="placeholder"
        :required="required"
        :disabled="disabled"
        :invalid="!!shownError"
        :size="size"
        :aria-label="ariaLabel"
        :aria-describedby="shownError || hint ? hintId : undefined"
        @update:model-value="onPick"
        @invalid="onInvalid"
      />
    </div>
    <p v-if="shownError" :id="hintId" class="error-text" data-testid="field-error">{{ shownError }}</p>
    <p v-else-if="hint" :id="hintId" class="help-text">{{ hint }}</p>
  </div>
</template>
