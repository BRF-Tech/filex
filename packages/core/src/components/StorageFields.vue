<script setup lang="ts">
/**
 * StorageFields — a driver's config form, rendered from the driver's own
 * descriptor (`GET /api/admin/storage-drivers`) and from nothing else.
 *
 * ⚠ There is no per-driver template here and there must never be one. The
 * form this replaces on the web was a `v-if` chain that drifted away from
 * the backend it posted to — it asked for `base_path` where the driver read
 * `root` and never offered the s3 `prefix` at all, so three of the four
 * drivers it offered answered 400 ROOT_PATH_FORBIDDEN on every submit. The
 * descriptor fixes the class; a hand-written form re-opens it.
 *
 * It lives in the shared package so the desktop app and the web app render
 * the same fields, in the same order, with the same help text, from the
 * same declaration — which is the only way "the same form on every surface"
 * survives the next driver.
 */
import { computed, ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import type { StorageField, StorageFieldOption } from '../types/Connections';
import { useLocale } from '../composables/useLocale';
import { actionIconSvg } from '../lib/actionIcons'; /* ikon:emoji */
import ChoiceButtons, { type ChoiceOption } from './ChoiceButtons.vue';

const props = defineProps<{
  fields: StorageField[];
  modelValue: Record<string, unknown>;
  locale: LocaleCode;
  /** Keys to mark as failing validation (missing required). */
  invalid?: string[];
  /**
   * Field-level messages, keyed by field key — what a server (an app
   * plugin's `surface.errors`, a driver's 400) said about the value. A key
   * here is invalid too, and its own words replace the generic "required".
   */
  errors?: Record<string, string>;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: Record<string, unknown>): void;
}>();

const { t } = useLocale(() => props.locale);

const showAdvanced = ref(false);
const revealed = ref<Record<string, boolean>>({});

const basic = computed(() => props.fields.filter((f) => !f.advanced));
const advanced = computed(() => props.fields.filter((f) => f.advanced));

/**
 * Translation first, the driver's English fallback second.
 *
 * A driver shipped after this bundle carries labels the catalogue has never
 * heard of. Falling back to the descriptor's own English is how the field
 * still reads as a field instead of as `storages.fields.something`.
 */
function label(f: StorageField): string {
  const s = t(f.i18n_key);
  return s === f.i18n_key ? f.label : s;
}

function help(f: StorageField): string {
  if (!f.help_i18n_key) return f.help ?? '';
  const s = t(f.help_i18n_key);
  return s === f.help_i18n_key ? (f.help ?? '') : s;
}

function optionLabel(o: StorageFieldOption): string {
  if (!o.i18n_key) return o.label;
  const s = t(o.i18n_key);
  return s === o.i18n_key ? o.label : s;
}

function set(key: string, value: unknown) {
  emit('update:modelValue', { ...props.modelValue, [key]: value });
}

/** Current value, honouring the legacy spellings the driver still reads —
 *  an old row keyed `base_path` shows up in the `root` box rather than as
 *  an empty field the operator would refill by hand. */
function raw(f: StorageField): unknown {
  const v = props.modelValue?.[f.key];
  if (v !== undefined && v !== null) return v;
  for (const alias of f.aliases ?? []) {
    const a = props.modelValue?.[alias];
    if (a !== undefined && a !== null) return a;
  }
  return f.default ?? undefined;
}

function str(f: StorageField): string {
  const v = raw(f);
  return v === undefined || v === null ? '' : String(v);
}

function num(f: StorageField): string {
  const v = raw(f);
  return v === '' || v === undefined || v === null ? '' : String(v);
}

function bool(f: StorageField): boolean {
  return raw(f) === true;
}

function isInvalid(f: StorageField): boolean {
  return (props.invalid ?? []).includes(f.key) || !!props.errors?.[f.key];
}

function errorText(f: StorageField): string {
  return props.errors?.[f.key] || (isInvalid(f) ? t('conn.form.required') : '');
}

/**
 * The options a `choice` field draws as buttons.
 *
 * A `bool` has two by definition and the catalogue names them, unless the
 * declaration named them itself — "Keep the original / Replace it" reads
 * better than "Yes / No" and is the plugin author's call, not ours.
 */
function choiceOptions(f: StorageField): ChoiceOption[] {
  if (f.type === 'bool') {
    const given = (f.options ?? []).filter((o) => o.value === 'true' || o.value === 'false');
    const yes = given.find((o) => o.value === 'true');
    const no = given.find((o) => o.value === 'false');
    return [
      { value: 'true', label: yes ? optionLabel(yes) : t('conn.form.yes') },
      { value: 'false', label: no ? optionLabel(no) : t('conn.form.no') },
    ];
  }
  return (f.options ?? []).map((o) => ({ value: o.value, label: optionLabel(o) }));
}

/** What a `choice` field holds right now, in the shape ChoiceButtons reads. */
function choiceValue(f: StorageField): string | string[] {
  const v = raw(f);
  if (f.type === 'bool') return v === true || v === 'true' ? 'true' : v === false || v === 'false' ? 'false' : '';
  if (f.multi) return Array.isArray(v) ? v.map((x) => String(x)) : v === undefined || v === null || v === '' ? [] : [String(v)];
  return v === undefined || v === null ? '' : String(v);
}

/** …and back: a bool becomes a boolean again, everything else stays as declared. */
function setChoice(f: StorageField, v: string | string[]): void {
  if (f.type === 'bool') {
    set(f.key, v === 'true');
    return;
  }
  set(f.key, v);
}

/** A field drawn as buttons — never as a dropdown, never as a lone checkbox. */
function isChoice(f: StorageField): boolean {
  return f.choice === true && (f.type === 'select' || f.type === 'bool');
}

function inputType(f: StorageField): string {
  if (f.type === 'int') return 'number';
  if (f.type === 'password' && !revealed.value[f.key]) return 'password';
  return 'text';
}

function toggleReveal(key: string) {
  revealed.value = { ...revealed.value, [key]: !revealed.value[key] };
}
</script>

<template>
  <div class="fe-cfield">
    <p v-if="!fields.length" class="fe-cfield__empty">{{ t('conn.form.noFields') }}</p>

    <template v-for="f in basic" :key="f.key">
      <div class="fe-cfield__row" :class="{ 'is-invalid': isInvalid(f) }">
        <!-- bool: the label belongs next to the box, not above it — unless
             the field is a DECISION, which is drawn as two buttons under a
             label of its own (v3 §2.2). -->
        <label v-if="f.type === 'bool' && !isChoice(f)" class="fe-cfield__check">
          <input
            type="checkbox"
            :checked="bool(f)"
            :disabled="disabled"
            @change="set(f.key, ($event.target as HTMLInputElement).checked)"
          />
          <span>{{ label(f) }}</span>
        </label>
        <label v-else class="fe-cfield__label" :for="`fe-cf-${f.key}`">
          {{ label(f) }}<span v-if="f.required" class="fe-cfield__req" aria-hidden="true">*</span>
        </label>

        <ChoiceButtons
          v-if="isChoice(f)"
          :options="choiceOptions(f)"
          :model-value="choiceValue(f)"
          :multi="f.multi === true"
          :disabled="disabled"
          :invalid="isInvalid(f)"
          :aria-label="label(f)"
          :testid-prefix="`fe-choice-${f.key}`"
          @update:model-value="(v: string | string[]) => setChoice(f, v)"
        />

        <select
          v-else-if="f.type === 'select'"
          :id="`fe-cf-${f.key}`"
          class="fe-cfield__input"
          :value="str(f)"
          :disabled="disabled"
          @change="set(f.key, ($event.target as HTMLSelectElement).value)"
        >
          <option v-for="o in f.options ?? []" :key="o.value" :value="o.value">
            {{ optionLabel(o) }}
          </option>
        </select>

        <textarea
          v-else-if="f.multiline"
          :id="`fe-cf-${f.key}`"
          class="fe-cfield__input fe-cfield__input--area"
          :class="{ 'fe-cfield__input--mono': f.monospace }"
          rows="4"
          :value="str(f)"
          :placeholder="f.placeholder"
          :disabled="disabled"
          @input="set(f.key, ($event.target as HTMLTextAreaElement).value)"
        ></textarea>

        <div v-else-if="f.type === 'password'" class="fe-cfield__secret">
          <input
            :id="`fe-cf-${f.key}`"
            class="fe-cfield__input fe-cfield__input--mono"
            :type="inputType(f)"
            autocomplete="new-password"
            spellcheck="false"
            :value="str(f)"
            :placeholder="f.placeholder"
            :disabled="disabled"
            @input="set(f.key, ($event.target as HTMLInputElement).value)"
          />
          <button
            type="button"
            class="fe-cfield__eye"
            :title="revealed[f.key] ? t('conn.form.hide') : t('conn.form.reveal')"
            :aria-label="revealed[f.key] ? t('conn.form.hide') : t('conn.form.reveal')"
            @click="toggleReveal(f.key)"
          >
            <!-- ikon:emoji — the two states of a secret field. 👁/🙈 were a
                 hairline dingbat beside a full-colour monkey, i.e. two
                 different fonts for one toggle; these are the same eye the
                 context menu's Preview row uses, with and without its slash.
                 The state is named on the button (title + aria-label). -->
            <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
            <span
              aria-hidden="true"
              v-html="actionIconSvg(revealed[f.key] ? 'toggle-hidden' : 'preview')"
            ></span>
          </button>
        </div>

        <input
          v-else-if="f.type !== 'bool'"
          :id="`fe-cf-${f.key}`"
          class="fe-cfield__input"
          :class="{ 'fe-cfield__input--mono': f.monospace }"
          :type="inputType(f)"
          :value="f.type === 'int' ? num(f) : str(f)"
          :placeholder="f.placeholder"
          :min="f.min"
          :max="f.max"
          :disabled="disabled"
          @input="
            set(
              f.key,
              f.type === 'int'
                ? ($event.target as HTMLInputElement).value === ''
                  ? null
                  : Number(($event.target as HTMLInputElement).value)
                : ($event.target as HTMLInputElement).value,
            )
          "
        />

        <p v-if="help(f)" class="fe-cfield__help">{{ help(f) }}</p>
        <p v-if="errorText(f)" class="fe-cfield__error">{{ errorText(f) }}</p>
      </div>
    </template>

    <div v-if="advanced.length" class="fe-cfield__adv">
      <button type="button" class="fe-cfield__advtoggle" @click="showAdvanced = !showAdvanced">
        {{ showAdvanced ? '▾' : '▸' }} {{ t('conn.form.advanced') }}
      </button>
      <div v-if="showAdvanced" class="fe-cfield__advbody">
        <div v-for="f in advanced" :key="f.key" class="fe-cfield__row">
          <label v-if="f.type === 'bool'" class="fe-cfield__check">
            <input
              type="checkbox"
              :checked="bool(f)"
              :disabled="disabled"
              @change="set(f.key, ($event.target as HTMLInputElement).checked)"
            />
            <span>{{ label(f) }}</span>
          </label>
          <template v-else>
            <label class="fe-cfield__label" :for="`fe-cfa-${f.key}`">{{ label(f) }}</label>
            <input
              :id="`fe-cfa-${f.key}`"
              class="fe-cfield__input"
              :class="{ 'fe-cfield__input--mono': f.monospace }"
              :type="inputType(f)"
              :value="f.type === 'int' ? num(f) : str(f)"
              :placeholder="f.placeholder"
              :disabled="disabled"
              @input="
                set(
                  f.key,
                  f.type === 'int'
                    ? ($event.target as HTMLInputElement).value === ''
                      ? null
                      : Number(($event.target as HTMLInputElement).value)
                    : ($event.target as HTMLInputElement).value,
                )
              "
            />
          </template>
          <p v-if="help(f)" class="fe-cfield__help">{{ help(f) }}</p>
          <p v-if="errorText(f)" class="fe-cfield__error">{{ errorText(f) }}</p>
        </div>
      </div>
    </div>
  </div>
</template>

<style>
.fe-cfield {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.fe-cfield__empty {
  margin: 0;
  color: var(--fe-text-muted);
  font-size: 13px;
}
.fe-cfield__row {
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.fe-cfield__label {
  font-size: 13px;
  font-weight: 600;
  color: var(--fe-text);
}
.fe-cfield__req {
  color: var(--fe-danger);
  margin-inline-start: 3px;
}
.fe-cfield__input {
  font: inherit;
  font-size: 13.5px;
  padding: 8px 10px;
  border-radius: var(--fe-radius);
  border: 1px solid var(--fe-border-strong);
  background: var(--fe-bg);
  color: var(--fe-text);
  width: 100%;
  /* ⚠ Explicit, because the host's own form styling reaches in here
     (shadowRoot: false). The desktop shell sets `input[type="text"]
     { min-width: 260px }` for its settings rows; inherited, that floors
     every field in this form and overflows a narrow embed. */
  min-width: 0;
  box-sizing: border-box;
}
.fe-cfield__input:focus {
  outline: 2px solid var(--fe-primary);
  outline-offset: -1px;
}
.fe-cfield__input--mono {
  font-family: var(--fe-font-mono);
  font-size: 12.5px;
}
.fe-cfield__input--area {
  resize: vertical;
}
.fe-cfield__row.is-invalid .fe-cfield__input {
  border-color: var(--fe-danger);
}
.fe-cfield__secret {
  display: flex;
  gap: 6px;
  align-items: stretch;
}
.fe-cfield__eye {
  flex: 0 0 auto;
  /* ikon:emoji — it holds an SVG now, not a 👁, so it centres the box
     instead of a text baseline. */
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--fe-border-strong);
  background: var(--fe-bg-elev);
  color: var(--fe-text);
  border-radius: var(--fe-radius);
  padding: 0 10px;
  cursor: pointer;
  font-size: 14px;
}
.fe-cfield__eye .fe-aicon {
  color: currentColor;
}
.fe-cfield__check {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  color: var(--fe-text);
  cursor: pointer;
}
.fe-cfield__help {
  margin: 0;
  font-size: 12px;
  color: var(--fe-text-muted);
  line-height: 1.45;
}
.fe-cfield__error {
  margin: 0;
  font-size: 12px;
  color: var(--fe-danger);
}
.fe-cfield__advtoggle {
  border: 0;
  background: none;
  color: var(--fe-text-muted);
  font: inherit;
  font-size: 12.5px;
  font-weight: 600;
  cursor: pointer;
  padding: 2px 0;
}
.fe-cfield__advtoggle:hover {
  color: var(--fe-text);
}
.fe-cfield__advbody {
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin-top: 12px;
}
</style>
