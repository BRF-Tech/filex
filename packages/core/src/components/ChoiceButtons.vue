<script setup lang="ts">
/**
 * ChoiceButtons — a choice the person can READ without clicking.
 *
 * ⚠⚠ This is the replacement for `<select>` in every plugin surface (v3
 * §2): "no dropdowns anywhere in a surface". A dropdown hides its options
 * until it is opened, so a wizard step asking "where does the signed
 * document go?" showed one answer and concealed the other two — and the one
 * it showed could contradict a field sitting right above it. Buttons put
 * every option on the step, which is also why the renderer enforces this
 * rather than each plugin choosing.
 *
 * One control, two behaviours, because they are the same question asked
 * once or several times:
 *   - single (`multi: false`) — radio semantics: `role="radiogroup"`,
 *     arrow keys move the choice, exactly one is `aria-checked`;
 *   - several (`multi: true`) — toggle semantics: `role="group"`, each
 *     button its own `aria-pressed`, the value is a list.
 *
 * ⚠ Not "a nicer select". A `<select>` is still the right control for a
 * long list (a time zone, a country), and this one is deliberately bad at
 * that — it draws every option. The rule it serves is about SURFACES, where
 * a choice has two to six answers and the person is being asked to decide.
 */
import { computed } from 'vue';
import { dirOfElement, inlineKeyStep } from '../lib/direction';

export interface ChoiceOption {
  value: string;
  label: string;
  /** A second line under the label — what choosing this one means. */
  help?: string;
  /** This one answer is not available (the others are). The button stays on
   *  screen — a choice that vanishes cannot tell the person it exists — and
   *  `help` is where to say why. The tag picker's "Team" for a viewer is the
   *  first user. */
  disabled?: boolean;
}

const props = defineProps<{
  options: ChoiceOption[];
  /** A string (single) or a list of strings (several). */
  modelValue: string | string[] | null | undefined;
  multi?: boolean;
  disabled?: boolean;
  invalid?: boolean;
  /** Ties the group to its own label for a screen reader. */
  ariaLabelledby?: string;
  ariaLabel?: string;
  /** `surface-choice` → `data-testid="surface-choice-<value>"`. */
  testidPrefix?: string;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: string | string[]): void;
}>();

const picked = computed<string[]>(() => {
  const v = props.modelValue;
  if (Array.isArray(v)) return v.map((x) => String(x));
  if (v === undefined || v === null || v === '') return [];
  return [String(v)];
});

function isOn(value: string): boolean {
  return picked.value.includes(value);
}

function optionOff(value: string): boolean {
  return !!props.disabled || !!props.options.find((o) => o.value === value)?.disabled;
}

function choose(value: string): void {
  if (optionOff(value)) return;
  if (props.multi) {
    const next = isOn(value) ? picked.value.filter((v) => v !== value) : [...picked.value, value];
    // ⚠ The order the OPTIONS are declared in, not the order they were
    // clicked in: a plugin that renders the answer back reads a stable list,
    // and two people who picked the same two options send the same value.
    const order = props.options.map((o) => o.value);
    emit('update:modelValue', order.filter((v) => next.includes(v)));
    return;
  }
  emit('update:modelValue', value);
}

/**
 * Arrow keys, for the single case only.
 *
 * A radio group is ONE tab stop: Tab reaches it, the arrows move inside it.
 * With `multi` each button is its own control and the browser's own Tab
 * order is already right, so nothing is intercepted there.
 */
function onKey(e: KeyboardEvent, index: number): void {
  if (props.multi || props.disabled) return;
  const keys = ['ArrowRight', 'ArrowDown', 'ArrowLeft', 'ArrowUp'];
  if (!keys.includes(e.key)) return;
  e.preventDefault();
  // ⚠ RTL: ← / → move the way they point — in a right-to-left row the NEXT
  // option is to the left (lib/direction, read from where the group is drawn).
  const step =
    e.key === 'ArrowDown' ? 1 : e.key === 'ArrowUp' ? -1 : inlineKeyStep(e.key, dirOfElement(e.currentTarget as Element));
  const n = props.options.length;
  if (!n) return;
  const next = props.options[(index + step + n) % n];
  if (next.disabled) return;
  choose(next.value);
  const el = e.currentTarget as HTMLElement;
  const group = el.parentElement;
  const buttons = group?.querySelectorAll<HTMLElement>('[data-choice]');
  buttons?.[(index + step + n) % n]?.focus();
}

/** The tab stop of a radio group: the chosen one, or the first. */
function tabIndexOf(value: string, index: number): number {
  if (props.multi) return 0;
  if (picked.value.length) return isOn(value) ? 0 : -1;
  return index === 0 ? 0 : -1;
}
</script>

<template>
  <div
    class="fe-choice"
    :class="{ 'is-invalid': invalid, 'is-disabled': disabled }"
    :role="multi ? 'group' : 'radiogroup'"
    :aria-labelledby="ariaLabelledby"
    :aria-label="ariaLabel"
    data-testid="choice-buttons"
  >
    <button
      v-for="(o, i) in options"
      :key="o.value"
      type="button"
      data-choice
      class="fe-choice__btn"
      :class="{ 'is-on': isOn(o.value) }"
      :role="multi ? undefined : 'radio'"
      :aria-checked="multi ? undefined : isOn(o.value) ? 'true' : 'false'"
      :aria-pressed="multi ? (isOn(o.value) ? 'true' : 'false') : undefined"
      :tabindex="tabIndexOf(o.value, i)"
      :disabled="disabled || o.disabled"
      :data-testid="testidPrefix ? `${testidPrefix}-${o.value}` : undefined"
      @click="choose(o.value)"
      @keydown="onKey($event, i)"
    >
      <span class="fe-choice__label">{{ o.label }}</span>
      <span v-if="o.help" class="fe-choice__help">{{ o.help }}</span>
    </button>
  </div>
</template>

