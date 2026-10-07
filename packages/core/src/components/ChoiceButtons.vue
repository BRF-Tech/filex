<script setup lang="ts">
/**
 * ChoiceButtons — a choice the person can READ without clicking.
 *
 * ⚠⚠ This is the replacement for a dropdown in every plugin surface (v3
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
 *     arrow keys move the choice (Home / End jump to the first / last), the
 *     chosen one is the group's only tab stop, exactly one is `aria-checked`;
 *   - several (`multi: true`) — toggle semantics: `role="group"`, each
 *     button its own `aria-pressed`, the value is a list.
 *
 * Two looks for the same control:
 *   - the default: a row of separate buttons, each with an optional second
 *     line (`help`) — a question a surface asks;
 *   - `segmented`: one strip with the chosen cell filled, the shape for two
 *     to four short answers sitting INLINE beside other fields (a unit after
 *     a number, a filter in a toolbar, "user" or "viewer", an access level).
 *     With `iconOnly` the cells show only their option's `icon`, the label
 *     being the accessible name (the light / dark / system switch). The
 *     segmented look came from the Apps store's `FaSegmented` (2026-10-04)
 *     and lives HERE rather than as a second radio group beside this one.
 *
 * A segmented cell's `help` (what choosing it means) is not a second line —
 * a strip has no room for one. It is (the owner, 2026-10-06, on the sharing
 * dialog's access levels: "yan yana düğmeler, üzerine gelince açıklaması
 * yazar"):
 *   - the cell's hover tip, shown on a resting pointer AND on keyboard focus
 *     — the header's hover-tip look (`.fe-toolbar__soon-tip` in base.css),
 *     one rule for both, not a tooltip of its own;
 *   - the cell's `aria-describedby`, so a screen reader hears it with the
 *     cell (the tip itself is `aria-hidden`, or it would be read twice);
 *   - on a screen that cannot hover (`hover: none`: a phone, a tablet), the
 *     CHOSEN cell's meaning as one small line under the strip, since a tip
 *     that needs a hover never shows there.
 *
 * ⚠ Not "a nicer dropdown". A long list (storages, people, a time zone) is
 * `ChoiceSelect`, and this one is deliberately bad at that — it draws every
 * option. Neither is ever a native select element: filex draws none
 * (web/tests/ui/noNativeSelect.test.ts).
 */
import { computed, type Component } from 'vue';
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
  /** An icon drawn before the label (and instead of it with `iconOnly`). */
  icon?: Component;
}

const props = defineProps<{
  options: ChoiceOption[];
  /** A string (single) or a list of strings (several). */
  modelValue: string | string[] | null | undefined;
  multi?: boolean;
  disabled?: boolean;
  invalid?: boolean;
  /** One strip with the chosen cell filled: two to four short answers inline. */
  segmented?: boolean;
  /** Segmented, and each cell shows only its icon (the label names it). */
  iconOnly?: boolean;
  /** Ties the group to its own label for a screen reader. */
  ariaLabelledby?: string;
  ariaLabel?: string;
  /** `surface-choice` → `data-testid="surface-choice-<value>"`. */
  testidPrefix?: string;
  /** The group's own `data-testid` (`choice-buttons` when not given). */
  testid?: string;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: string | string[]): void;
}>();

const strip = computed(() => !!props.segmented || !!props.iconOnly);

/* Ids that tie a cell to its tip. Random, not a counter (the MegaMenu reason:
   two groups on one page must not share them). */
const uid = `fe-choice-${Math.random().toString(36).slice(2, 10)}`;
const tipId = (i: number) => `${uid}-tip-${i}`;

/** The words a segmented cell's tip shows: its meaning, or (icon-only) its name. */
function tipOf(o: ChoiceOption): string {
  if (!strip.value) return '';
  return o.help || (props.iconOnly ? o.label : '');
}

/** The chosen cell's meaning, for the line under the strip on a touch screen. */
const chosenHelp = computed(() => {
  if (!strip.value || props.multi) return '';
  return props.options.find((o) => isOn(o.value))?.help ?? '';
});

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

/** The first (step 1) or last (step -1) option that can be chosen; -1 if none. */
function edgeEnabled(step: 1 | -1): number {
  const n = props.options.length;
  for (let k = 0; k < n; k++) {
    const i = step === 1 ? k : n - 1 - k;
    if (!props.options[i].disabled) return i;
  }
  return -1;
}

/**
 * Arrow keys (and Home / End), for the single case only.
 *
 * A radio group is ONE tab stop: Tab reaches it, the arrows move inside it.
 * With `multi` each button is its own control and the browser's own Tab
 * order is already right, so nothing is intercepted there.
 */
function onKey(e: KeyboardEvent, index: number): void {
  if (props.multi || props.disabled) return;
  const n = props.options.length;
  let to: number;
  if (e.key === 'Home' || e.key === 'End') {
    e.preventDefault();
    to = edgeEnabled(e.key === 'Home' ? 1 : -1);
    if (to < 0) return;
  } else {
    const keys = ['ArrowRight', 'ArrowDown', 'ArrowLeft', 'ArrowUp'];
    if (!keys.includes(e.key)) return;
    e.preventDefault();
    // ⚠ RTL: ← / → move the way they point — in a right-to-left row the NEXT
    // option is to the left (lib/direction, read from where the group is drawn).
    const step =
      e.key === 'ArrowDown' ? 1 : e.key === 'ArrowUp' ? -1 : inlineKeyStep(e.key, dirOfElement(e.currentTarget as Element));
    if (!n) return;
    to = (index + step + n) % n;
    if (props.options[to].disabled) return;
  }
  choose(props.options[to].value);
  // ⚠ The group, not the parent: a cell wraps each button (its tip beside it).
  const group = (e.currentTarget as HTMLElement).closest('.fe-choice');
  const buttons = group?.querySelectorAll<HTMLElement>('[data-choice]');
  buttons?.[to]?.focus();
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
    :class="{
      'is-invalid': invalid,
      'is-disabled': disabled,
      'fe-choice--segmented': strip,
      'fe-choice--icons': iconOnly,
    }"
    :role="multi ? 'group' : 'radiogroup'"
    :aria-labelledby="ariaLabelledby"
    :aria-label="ariaLabel"
    :data-testid="testid ?? 'choice-buttons'"
  >
    <!-- The strip and its cells are `display: contents` in the default look,
         so the buttons lay out exactly as direct children of the group. -->
    <div class="fe-choice__strip">
      <span v-for="(o, i) in options" :key="o.value" class="fe-choice__cell">
        <button
          type="button"
          data-choice
          class="fe-choice__btn"
          :class="{ 'is-on': isOn(o.value) }"
          :role="multi ? undefined : 'radio'"
          :aria-checked="multi ? undefined : isOn(o.value) ? 'true' : 'false'"
          :aria-pressed="multi ? (isOn(o.value) ? 'true' : 'false') : undefined"
          :aria-label="iconOnly ? o.label : undefined"
          :aria-describedby="strip && o.help ? tipId(i) : undefined"
          :tabindex="tabIndexOf(o.value, i)"
          :disabled="disabled || o.disabled"
          :data-testid="testidPrefix ? `${testidPrefix}-${o.value}` : undefined"
          @click="choose(o.value)"
          @keydown="onKey($event, i)"
        >
          <component :is="o.icon" v-if="o.icon" class="fe-choice__icon" aria-hidden="true" />
          <span v-if="!iconOnly" class="fe-choice__label">{{ o.label }}</span>
          <span v-if="o.help && !strip" class="fe-choice__help">{{ o.help }}</span>
        </button>
        <span
          v-if="tipOf(o)"
          :id="tipId(i)"
          class="fe-choice__tip"
          aria-hidden="true"
          :data-testid="testidPrefix ? `${testidPrefix}-${o.value}-tip` : undefined"
        >{{ tipOf(o) }}</span>
      </span>
    </div>
    <p
      v-if="chosenHelp"
      class="fe-choice__note"
      aria-hidden="true"
      :data-testid="testidPrefix ? `${testidPrefix}-note` : undefined"
    >{{ chosenHelp }}</p>
  </div>
</template>
