<script setup lang="ts">
/**
 * ChoiceSelect — one answer from a list, without the browser's own dropdown.
 *
 * ⚠⚠ filex draws NO native select element, anywhere (the owner, 2026-10-04:
 * "hiç bir yerde öyle basit bir dropdown kullanma"). The browser's list is
 * painted by the operating system: it ignores the palette, opens white over a
 * dark panel, is a different control on every platform, and on a phone it is
 * a sheet nobody designed. So the choice is ours, in two shapes:
 *
 *   - two to four answers that fit on the line: `ChoiceButtons` (with
 *     `segmented` when it sits inline beside other fields) — every answer is
 *     readable without a click;
 *   - a longer list, or one whose length the data decides (storages, people,
 *     buckets, a plugin's options): this control.
 *
 * `web/tests/ui/noNativeSelect.test.ts` is the gate over both trees, and
 * `web/src/components/ui/Select.vue` is the admin panel's labelled frame
 * around this same component — there is no second implementation.
 *
 * The WAI-ARIA "select-only combobox" pattern: a button-like combobox that
 * KEEPS the focus, and a listbox it points into with `aria-activedescendant`.
 *
 * - v-model is a string or a number: the chosen option's own `value` comes
 *   back, so a numeric list needs no `.number` and a mixed one keeps its types;
 * - `options` [{ value, label, help?, disabled? }] — `help` is a second line
 *   under the label (what choosing this one means); a disabled option stays
 *   in the list, greyed, and cannot be chosen by click, arrows or typing;
 * - `id` lands on the combobox, so `<label for="id">` (or a wrapping label)
 *   names it;
 * - `required` / `name`: a hidden input carries the native constraint and the
 *   name into a surrounding form. The form's own check stops on it, the
 *   combobox takes the focus and is marked, and `invalid` is emitted so a host
 *   can say why in its own words (web ui/Select does, through lib/formCheck);
 * - keyboard: ArrowDown / ArrowUp / Enter / Space / Alt+ArrowDown open;
 *   arrows, Home / End and PageUp / PageDown move; typing jumps to the next
 *   label that starts with what was typed; Enter or Space choose; Escape
 *   closes without a change (and is not passed on, so the dialog around it
 *   stays open); Tab chooses and moves on;
 * - the list is teleported to <body> — or into the open <dialog> the field
 *   sits in, because a modal dialog lives in the browser's top layer, above
 *   anything <body> can stack — and placed under the field, or above it when
 *   there is no room below. It starts at the field's inline-start edge (the
 *   right edge in a right-to-left interface) and stays inside the window
 *   (lib/listPlacement), follows scroll and resize, takes its layer from what
 *   it hangs over (lib/popupLayer) and closes on a press anywhere outside it.
 *
 * ⚠ The list leaves the tree that scoped the theme (the explorer's `.fe`, a
 * dialog's theme class, a host's own `--fe-*` overrides), so it carries the
 * field's COMPUTED tokens with it rather than falling back to the light ones
 * on `:root`. Its styles live in base.css, beside ChoiceButtons', for the
 * reason written there (both published stylesheets must carry them).
 *
 * Moved here from the Apps store (filex-apps `FaSelect`, 2026-10-04), which
 * wrote it to come here: Vue, the lucide icons core already ships and the
 * `--fe-*` tokens, every text through props.
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { Check, ChevronDown } from 'lucide-vue-next';
import { dirOfElement, type TextDirection } from '../lib/direction';
import { placeListUnder, type ListPlacement } from '../lib/listPlacement';
import { POPUP_BASE_Z, popupLayer } from '../lib/popupLayer';

export interface SelectOption {
  value: string | number;
  label: string;
  /** A second line under the label — what choosing this one means. */
  help?: string;
  /** Shown, greyed, and not choosable. */
  disabled?: boolean;
}

type Value = string | number;

const props = withDefaults(
  defineProps<{
    modelValue?: Value | null;
    options: SelectOption[];
    id?: string;
    name?: string;
    /** Shown in the field while no option is chosen. */
    placeholder?: string;
    required?: boolean;
    disabled?: boolean;
    invalid?: boolean;
    /** The field's height: the `--fe-h-sm|md|lg` control heights. */
    size?: 'sm' | 'md' | 'lg';
    ariaLabel?: string;
    ariaLabelledby?: string;
    ariaDescribedby?: string;
    /** The message a form's own validation carries when a required value is missing. */
    requiredMessage?: string;
    /** `data-testid` of the combobox; the list is `<testid>-list`, an option `<testid>-option-<value>`. */
    testid?: string;
  }>(),
  {
    modelValue: null,
    id: undefined,
    name: undefined,
    placeholder: '',
    required: false,
    disabled: false,
    invalid: false,
    size: 'md',
    ariaLabel: undefined,
    ariaLabelledby: undefined,
    ariaDescribedby: undefined,
    requiredMessage: '',
    testid: undefined,
  },
);
const emit = defineEmits<{
  (e: 'update:modelValue', v: Value): void;
  (e: 'change', v: Value): void;
  /** The surrounding form's check refused the field (required, nothing chosen). */
  (e: 'invalid', ev: Event): void;
}>();

/* Random, not a counter: two of these on one page (a test, a host drawing
   two explorers) must not share ids, and `useId` is Vue 3.5 while the
   package asks 3.4 (the same reason as MegaMenu). */
const uid = `fe-sel-${Math.random().toString(36).slice(2, 10)}`;
const comboId = computed(() => props.id ?? uid);
const listId = `${uid}-list`;
const optId = (i: number) => `${uid}-opt-${i}`;

const open = ref(false);
const active = ref(-1);
const trigger = ref<HTMLButtonElement | null>(null);
const panel = ref<HTMLElement | null>(null);
const hidden = ref<HTMLInputElement | null>(null);
const target = ref<HTMLElement | null>(null);
/** Where the list is (lib/listPlacement), the layer it sits on and the direction it is laid out in. */
const place = ref<ListPlacement & { z: number; dir: TextDirection }>({
  top: 0,
  bottom: 0,
  start: 0,
  width: 0,
  maxHeight: 280,
  up: false,
  z: POPUP_BASE_Z,
  dir: 'ltr',
});
/** The field's tokens and type, carried onto the teleported list. */
const carried = ref<Record<string, string>>({});
const flagged = ref(false);
// What was typed for the jump to a matching option, and when.
let typed = '';
let typedAt = 0;

const isEmpty = (v: Value | null | undefined) => v === null || v === undefined || v === '';
const selectedIndex = computed(() => props.options.findIndex((o) => o.value === props.modelValue));
const selected = computed(() => props.options[selectedIndex.value]);
const shown = computed(() => selected.value?.label ?? props.placeholder);
const isInvalid = computed(() => props.invalid || flagged.value);
/** The hidden input exists only for a form to read: a name to send or a value it requires. */
const formBound = computed(() => props.required || !!props.name);

function syncValidity() {
  hidden.value?.setCustomValidity(props.required && isEmpty(props.modelValue) ? props.requiredMessage || ' ' : '');
}
watch(
  () => props.modelValue,
  (v) => {
    if (!isEmpty(v)) flagged.value = false;
    void nextTick(syncValidity);
  },
  { immediate: true },
);
watch(() => props.required, () => void nextTick(syncValidity));
watch(
  () => props.disabled,
  (d) => {
    if (d) close(false);
  },
);

/** The first choosable option from `start`, walking by `step`; -1 when there is none. */
function enabledFrom(start: number, step: number): number {
  const n = props.options.length;
  for (let k = 0, i = start; k < n; k++, i += step) {
    if (i < 0 || i >= n) return -1;
    if (!props.options[i].disabled) return i;
  }
  return -1;
}

/** The tokens the list paints with, as the FIELD resolves them. */
const CARRIED_TOKENS = [
  '--fe-bg',
  '--fe-bg-hover',
  '--fe-border',
  '--fe-text',
  '--fe-text-muted',
  '--fe-primary',
  '--fe-primary-ink',
  '--fe-radius',
  '--fe-radius-sm',
  '--fe-shadow',
  '--fe-text-xs',
];
function readCarried(el: Element): Record<string, string> {
  const out: Record<string, string> = {};
  try {
    const cs = window.getComputedStyle(el);
    for (const name of CARRIED_TOKENS) {
      const v = cs.getPropertyValue(name).trim();
      if (v) out[name] = v;
    }
    // The list reads in the type of the field it drops from.
    if (cs.fontFamily) out['font-family'] = cs.fontFamily;
    if (cs.fontSize) out['font-size'] = cs.fontSize;
  } catch {
    /* A detached node: the stylesheet's own values stand. */
  }
  return out;
}

function placePanel() {
  const t = trigger.value;
  if (!t) return;
  const dir = dirOfElement(t);
  const at = placeListUnder(
    t.getBoundingClientRect(),
    { width: window.innerWidth, height: window.innerHeight },
    dir,
    props.options.length,
  );
  place.value = { ...at, z: place.value.z, dir };
}

function onOutside(e: Event) {
  const n = e.target as Node | null;
  if (!n || trigger.value?.contains(n) || panel.value?.contains(n)) return;
  close(false);
}

function listen(on: boolean) {
  if (on) {
    window.addEventListener('resize', placePanel);
    window.addEventListener('scroll', placePanel, true);
    document.addEventListener('pointerdown', onOutside, true);
  } else {
    window.removeEventListener('resize', placePanel);
    window.removeEventListener('scroll', placePanel, true);
    document.removeEventListener('pointerdown', onOutside, true);
  }
}

async function show(at?: number) {
  if (props.disabled || open.value) return;
  const t = trigger.value;
  target.value = (t?.closest('dialog[open]') as HTMLElement | null) ?? document.body;
  if (t) {
    carried.value = readCarried(t);
    place.value = { ...place.value, z: popupLayer(t) };
  }
  placePanel();
  open.value = true;
  active.value = at ?? (selectedIndex.value >= 0 ? selectedIndex.value : enabledFrom(0, 1));
  listen(true);
  await nextTick();
  scrollActive();
}

function close(refocus = true) {
  if (!open.value) return;
  open.value = false;
  typed = '';
  listen(false);
  if (refocus) trigger.value?.focus();
}

function choose(i: number, refocus = true) {
  const o = props.options[i];
  if (!o || o.disabled) return;
  if (o.value !== props.modelValue) {
    emit('update:modelValue', o.value);
    emit('change', o.value);
  }
  close(refocus);
}

function scrollActive() {
  const el = document.getElementById(optId(active.value));
  if (el && typeof el.scrollIntoView === 'function') el.scrollIntoView({ block: 'nearest' });
}

function move(to: number) {
  if (to < 0) return;
  active.value = to;
  void nextTick(scrollActive);
}

// Typing jumps to the next option whose label starts with what was typed.
function typeahead(ch: string): number {
  const now = Date.now();
  typed = now - typedAt > 600 ? ch : typed + ch;
  typedAt = now;
  const n = props.options.length;
  const from = typed.length > 1 ? Math.max(active.value, 0) : active.value + 1;
  const want = typed.toLocaleLowerCase();
  for (let k = 0; k < n; k++) {
    const i = (from + k + n) % n;
    const o = props.options[i];
    if (!o.disabled && o.label.toLocaleLowerCase().startsWith(want)) return i;
  }
  return -1;
}

function onKey(e: KeyboardEvent) {
  if (props.disabled) return;
  const k = e.key;
  if (!open.value) {
    if (k === 'ArrowDown' || k === 'ArrowUp' || k === 'Enter' || k === ' ') {
      e.preventDefault();
      void show();
    } else if (k === 'Home' || k === 'End') {
      e.preventDefault();
      void show(k === 'Home' ? enabledFrom(0, 1) : enabledFrom(props.options.length - 1, -1));
    } else if (k.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const i = typeahead(k);
      if (i >= 0) void show(i);
    }
    return;
  }
  switch (k) {
    case 'ArrowDown':
      e.preventDefault();
      if (e.altKey) return;
      move(enabledFrom(active.value + 1, 1));
      return;
    case 'ArrowUp':
      e.preventDefault();
      if (e.altKey) return choose(active.value);
      move(enabledFrom(active.value - 1, -1));
      return;
    case 'Home':
      e.preventDefault();
      move(enabledFrom(0, 1));
      return;
    case 'End':
      e.preventDefault();
      move(enabledFrom(props.options.length - 1, -1));
      return;
    case 'PageDown':
      e.preventDefault();
      move(enabledFrom(Math.min(active.value + 10, props.options.length - 1), -1));
      return;
    case 'PageUp':
      e.preventDefault();
      move(enabledFrom(Math.max(active.value - 10, 0), 1));
      return;
    case 'Enter':
    case ' ':
      e.preventDefault();
      choose(active.value);
      return;
    case 'Escape':
      // Not passed on: the dialog this field sits in closes on Escape too,
      // and one press must close the list only.
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    case 'Tab':
      choose(active.value, false);
      return;
  }
  if (k.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
    const i = typeahead(k);
    if (i >= 0) move(i);
  }
}

function onTrigger() {
  if (open.value) close();
  else void show();
}

/**
 * The form's own validation found the required value missing. The browser's
 * bubble is suppressed (it would speak the BROWSER's language), the field is
 * marked, the host is told, and the combobox takes the focus — only the FIRST
 * refused field of the form does, the rule web/src/lib/formCheck keeps with
 * the same `fxInvalidFocus` mark, so a form refusing two fields at once lands
 * on the first one.
 */
function onInvalid(e: Event) {
  e.preventDefault();
  flagged.value = true;
  emit('invalid', e);
  const form = hidden.value?.form ?? null;
  if (form) {
    if (form.dataset.fxInvalidFocus) return;
    form.dataset.fxInvalidFocus = '1';
    queueMicrotask(() => {
      delete form.dataset.fxInvalidFocus;
    });
  }
  trigger.value?.focus();
}

onBeforeUnmount(() => listen(false));
defineExpose({ focus: () => trigger.value?.focus(), open: show, close });
</script>

<template>
  <span
    class="fe-select"
    :class="[
      size !== 'md' ? `fe-select--${size}` : '',
      { 'is-open': open, 'is-disabled': disabled, 'is-invalid': isInvalid },
    ]"
  >
    <button
      :id="comboId"
      ref="trigger"
      type="button"
      class="fe-select__trigger"
      role="combobox"
      :disabled="disabled"
      aria-haspopup="listbox"
      :aria-expanded="open ? 'true' : 'false'"
      :aria-controls="listId"
      :aria-activedescendant="open && active >= 0 ? optId(active) : undefined"
      :aria-label="ariaLabel"
      :aria-labelledby="ariaLabelledby"
      :aria-describedby="ariaDescribedby"
      :aria-required="required ? 'true' : undefined"
      :aria-invalid="isInvalid ? 'true' : undefined"
      :data-testid="testid"
      :data-value="modelValue ?? ''"
      @click="onTrigger"
      @keydown="onKey"
      @keyup.space.prevent
    >
      <span class="fe-select__value" :class="{ 'is-placeholder': !selected }">{{ shown }}</span>
      <ChevronDown class="fe-select__chev" aria-hidden="true" />
    </button>
    <input
      v-if="formBound"
      ref="hidden"
      class="fe-select__native"
      tabindex="-1"
      aria-hidden="true"
      :name="name"
      :value="modelValue ?? ''"
      :required="required"
      :disabled="disabled"
      @invalid="onInvalid"
    />
    <Teleport v-if="open && target" :to="target">
      <ul
        :id="listId"
        ref="panel"
        class="fe-select__panel"
        :class="{ 'is-up': place.up }"
        :dir="place.dir"
        role="listbox"
        :aria-labelledby="ariaLabelledby ?? comboId"
        :style="[
          carried,
          {
            insetInlineStart: place.start + 'px',
            width: place.width + 'px',
            maxHeight: place.maxHeight + 'px',
            top: place.up ? undefined : place.top + 'px',
            bottom: place.up ? place.bottom + 'px' : undefined,
            zIndex: place.z,
          },
        ]"
        :data-testid="testid ? `${testid}-list` : undefined"
        @mousedown.prevent
      >
        <li
          v-for="(o, i) in options"
          :id="optId(i)"
          :key="String(o.value)"
          class="fe-select__option"
          :class="{ 'is-active': i === active, 'is-selected': o.value === modelValue }"
          role="option"
          :aria-selected="o.value === modelValue ? 'true' : 'false'"
          :aria-disabled="o.disabled ? 'true' : undefined"
          :data-value="o.value"
          :data-testid="testid ? `${testid}-option-${o.value === '' ? 'none' : o.value}` : undefined"
          @mousemove="!o.disabled && (active = i)"
          @click="choose(i)"
        >
          <span class="fe-select__label">
            <span class="fe-select__text">{{ o.label }}</span>
            <span v-if="o.help" class="fe-select__help">{{ o.help }}</span>
          </span>
          <Check v-if="o.value === modelValue" class="fe-select__check" aria-hidden="true" />
        </li>
      </ul>
    </Teleport>
  </span>
</template>
