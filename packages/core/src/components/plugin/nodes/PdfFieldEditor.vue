<script setup lang="ts">
/**
 * PdfFieldEditor — what a box IS, as opposed to where it sits.
 *
 * ⚠⚠ One editor, two screens. The request wizard asks for a box in two
 * steps now — "define the boxes" (this, one card per box, no document in
 * sight) and "place them" (the document, with these boxes handed out one at
 * a time) — and the one-screen `edit` mode still shows it for whichever box
 * is selected. If the controls were written twice they would drift, and the
 * person would meet two different ideas of what naming a box means.
 *
 * Nothing here knows about pages, rectangles or pointers: placing is the
 * parent's job. That split is the whole point of the component.
 */
import { computed } from 'vue';
import type { LocaleCode } from '../../../types/ExplorerConfig';
import { useLocale } from '../../../composables/useLocale';
import { appTextOr, labelOf } from '../../../lib/pluginLabel';
import {
  dateChoices,
  dateExample,
  formatFor,
  isDrawnType,
  linesOf,
  normalizeDateLabels,
  normalizeFormats,
  normalizeStampLines,
  splitFormat,
  stampExample,
  type PdfField,
  type PdfSigner,
  type PdfSignatureStyle,
} from '../../../lib/pdfFields';
import { PDF_RULE_KINDS, type PdfFieldRule, type PdfRuleKind } from '../../../lib/pdfFieldRules';
import { SIGN_FONTS, type SignFontKey } from '../../../lib/signFonts';
import ChoiceButtons, { type ChoiceOption } from '../../ChoiceButtons.vue';

const props = defineProps<{
  field: PdfField;
  signers: PdfSigner[];
  /**
   * The date layouts this surface offers (`[{id, label, example}]`, the
   * node's own `formats` prop). Empty or absent: no layout control is
   * drawn at all, because there is nothing to choose between.
   */
  formats?: unknown;
  /**
   * The date control's three captions as the PLUGIN words them
   * (`date_labels`): the two questions and the word over the example.
   * Absent: this package's own catalogue stands in.
   */
  dateLabels?: unknown;
  locale: LocaleCode;
  disabled?: boolean;
  /** `surface-pdf` on the one-screen editor; the define cards pass their own. */
  testidPrefix?: string;
  /**
   * The lines a plugin can print under a signature (the node's
   * `stamp_lines`, `lib/pdfFields.normalizeStampLines`). Absent or empty:
   * the plugin offers no choice, and no control is drawn.
   */
  stampLines?: unknown;
}>();

const emit = defineEmits<{
  /** A changed copy of the field. */
  (e: 'patch', f: PdfField): void;
  (e: 'remove'): void;
}>();

const { t } = useLocale(() => props.locale);
const prefix = computed(() => props.testidPrefix || 'surface-pdf');

function typeLabel(type: PdfField['type']): string {
  return t(`plugin.pdf.type_${type}`);
}

/** Change the field through a copy — the parent owns the list. */
function patch(fn: (f: PdfField) => void): void {
  const next = { ...props.field };
  fn(next);
  emit('patch', next);
}

/**
 * The box's NAME, exactly as it was typed — spaces and all.
 *
 * ⚠⚠ NEVER trim here. `:value` is bound to the field, so a trim on every
 * keystroke writes the trimmed name straight back into the input: the space
 * typed after a word is taken off again before the next letter arrives, and
 * a two-word name becomes impossible to type (the owner, 2026-09-23:
 * "isimde boşluk bırakamıyorum hiç izin vermiyor"). A name is free text in
 * any script; the ASCII IDENTITY the PDF names the field by is derived from
 * it by the plugin (`fields.Slug`) and shown to nobody. The record is
 * trimmed once, where the record is written.
 */
function setLabel(v: string): void {
  patch((f) => {
    if (v) f.label = v;
    else delete f.label;
  });
}

function assign(assignee: string): void {
  patch((f) => {
    if (assignee) f.assignee = assignee;
    else delete f.assignee;
  });
}

function setRequired(on: boolean): void {
  patch((f) => {
    if (on) f.required = true;
    else delete f.required;
  });
}

function setRuleKind(kind: string): void {
  patch((f) => {
    const k = (PDF_RULE_KINDS as readonly string[]).includes(kind) ? (kind as PdfRuleKind) : 'any';
    const rule: PdfFieldRule = { ...(f.rule ?? { kind: 'any' }), kind: k };
    if (k === 'any' && !rule.min && !rule.max) delete f.rule;
    else f.rule = rule;
  });
}

function setRuleBound(which: 'min' | 'max', raw: unknown): void {
  const n = Math.trunc(Number(String(raw ?? '').trim()));
  patch((f) => {
    const rule: PdfFieldRule = { ...(f.rule ?? { kind: 'any' }) };
    if (Number.isFinite(n) && n > 0) rule[which] = n;
    else delete rule[which];
    if (rule.kind === 'any' && !rule.min && !rule.max) delete f.rule;
    else f.rule = rule;
  });
}

function setFormat(id: string): void {
  patch((f) => {
    if (id) f.format = id;
    else delete f.format;
  });
}

function setFont(key: SignFontKey): void {
  patch((f) => {
    f.font = key;
  });
}

/** A signature or initials box: drawn by hand, or a name typed in a face. */
const drawn = computed(() => isDrawnType(props.field.type));

/** How this signature box is given. Absent means drawn — what a signature is. */
const style = computed<PdfSignatureStyle>(() => props.field.style ?? 'drawn');

function setStyle(v: string): void {
  patch((f) => {
    if (v === 'typed') f.style = 'typed';
    else delete f.style;
  });
}

const styleOptions = computed<ChoiceOption[]>(() => [
  { value: 'drawn', label: t('plugin.pdf.style_drawn') },
  { value: 'typed', label: t('plugin.pdf.style_typed') },
]);

/**
 * Is the face a question here at all?
 *
 * ⚠ The owner, 2026-09-21: "text seçersem font çıkar, text seçmezsem font
 * seçimi çıkmaz". A drawn signature is somebody's hand; a face offered for it
 * was a control that changed nothing the signer would ever see. Text and
 * date boxes are words, so they always have one; a checkbox never does.
 */
const asksFont = computed(() => {
  if (props.field.type === 'checkbox') return false;
  if (drawn.value) return style.value === 'typed';
  return true;
});

/* ── what is printed under a signature ────────────────────────────── */

const lineCatalogue = computed(() => normalizeStampLines(props.stampLines));
/** The ids this box prints, in the catalogue's order (its default set when it has none). */
const pickedLines = computed(() => linesOf(props.field, lineCatalogue.value));
const lineOptions = computed<ChoiceOption[]>(() =>
  lineCatalogue.value.map((l) => ({ value: l.id, label: labelOf(l.label, props.locale) || l.id })),
);

function setLines(v: string | string[]): void {
  const ids = (Array.isArray(v) ? v : [v]).filter(Boolean);
  patch((f) => {
    // ⚠ An EMPTY list is kept, and means "nothing under the signature". It is
    // not the same as absent (the plugin's default set), and folding the two
    // together would put back the very lines the person just took off.
    f.lines = ids;
  });
}

/**
 * The words the plugin will print, one per line, for this box's signer —
 * the plugin's own example for each chosen line, so the card previews its
 * wording rather than this package guessing at it.
 */
const linePreview = computed(() =>
  pickedLines.value
    .map((id) => lineCatalogue.value.find((l) => l.id === id))
    .filter((l): l is NonNullable<typeof l> => !!l)
    .map((l) => labelOf(stampExample(l, props.field.assignee), props.locale) || labelOf(l.label, props.locale)),
);

/**
 * Who fills this box, or who signs it.
 *
 * ⚠ The owner, 2026-09-21: "tarih, metin, onay kutusu gibi şeylerde
 * 'imzacı' yazısı yerine 'dolduran' yazısı koyalım". A date or a tick is
 * FILLED; calling its owner "the signer" asked the requester a question
 * about the wrong act.
 */
const assigneeLabel = computed(() => t(drawn.value ? 'plugin.pdf.assignee' : 'plugin.pdf.filled_by'));

/**
 * "Anyone"'s value on the buttons. ⚠ Not `''`: the choice control reads an
 * empty value as "nothing picked", so a box that belonged to anyone showed
 * NO button pressed — measured on the define step, 2026-09-21, three
 * unpressed buttons under every new box of a two-signer request. The
 * sentinel lives only between this control and `assign`; the field still
 * carries no `assignee` at all.
 */
const ANYONE = '*';

/** Who the box belongs to, as buttons — "Anyone" first. */
const assigneeOptions = computed<ChoiceOption[]>(() => [
  { value: ANYONE, label: t('plugin.pdf.unassigned') },
  ...props.signers.map((sg) => ({ value: sg.id, label: labelOf(sg.label, props.locale) || sg.id })),
]);

/** What a text box accepts, as buttons. */
const ruleOptions = computed<ChoiceOption[]>(() =>
  PDF_RULE_KINDS.map((k) => ({ value: k, label: t(`plugin.pdf.rule_kind_${k}`) })),
);

/**
 * How a date box is written, as buttons — each one with an example under
 * it, because `MM/DD/YYYY` is a puzzle and `12/31/2000` is an answer.
 */
const dateFormats = computed(() => normalizeFormats(props.formats));
const formatOptions = computed<ChoiceOption[]>(() =>
  dateFormats.value.map((f) => ({ value: f.id, label: labelOf(f.label, props.locale) || f.id, help: f.example })),
);

/**
 * Which layout is shown as chosen. A box that has not been given one yet is
 * drawn on the FIRST the surface offers: a catalogue's first entry is what
 * the plugin falls back to, so showing nothing selected would be telling
 * the person their date has no layout when it has one.
 */
const pickedFormat = computed(() => props.field.format ?? dateFormats.value[0]?.id ?? '');

/* ── a date's layout: TWO questions ───────────────────────────────────
 *
 * ⚠⚠ The owner, 2026-09-23: "tarih biçimi ve ayraçlarını ayrı ayrı
 * seçebilir olalım … Tarih biçimi iki farklı seçimle tamamlandığında örnek
 * halini alta gösterelim". Twenty layouts in one row is a wall to hunt
 * through; the same twenty are five arrangements and four separators, and
 * nobody has to read a pattern to answer either one. The BOX still stores
 * the whole pattern, so a request made before this keeps its layout
 * exactly and no signed document is re-written under anybody.
 */
const dateParts = computed(() => dateChoices(dateFormats.value));
const dateCaptions = computed(() => normalizeDateLabels(props.dateLabels));
/* An app may caption these three itself; filex has its own words for all of
   them. ⚠ `appTextOr`, not `labelOf(…) || t(…)`: the app's caption for THIS
   reader wins, then filex's own, and the app's other languages last — or a
   reader in Arabic gets the app's English in the middle of an Arabic screen
   while the language pack holds the very same caption (v0.43.0). */
function caption(k: 'order' | 'separator' | 'example', fallback: string): string {
  return appTextOr(dateCaptions.value[k], props.locale, () => t(fallback));
}

const pickedPair = computed(() => splitFormat(dateFormats.value, pickedFormat.value));
const pickedOrder = computed(() => pickedPair.value?.order ?? dateParts.value?.orders[0]?.id ?? '');
const pickedSeparator = computed(() => pickedPair.value?.separator ?? dateParts.value?.separators[0]?.id ?? '');

const orderOptions = computed<ChoiceOption[]>(() =>
  (dateParts.value?.orders ?? []).map((o) => ({ value: o.id, label: labelOf(o.label, props.locale) || o.id })),
);
const separatorOptions = computed<ChoiceOption[]>(() =>
  (dateParts.value?.separators ?? []).map((s) => ({ value: s.id, label: labelOf(s.label, props.locale) || s.id })),
);

/** Today, written the way the two answers say — the whole point of asking. */
const dateShown = computed(() => dateExample(pickedOrder.value, pickedSeparator.value, new Date()));

function setOrder(v: string): void {
  setFormat(formatFor(dateFormats.value, v, pickedSeparator.value));
}
function setSeparator(v: string): void {
  setFormat(formatFor(dateFormats.value, pickedOrder.value, v));
}
</script>

<template>
  <div class="fe-spdf__props" :data-testid="`${prefix}-editor`">
    <span class="fe-spdf__propname">{{ typeLabel(field.type) }}</span>

    <!-- v3 §3.1 — the box's own name. Offered with the type's name already
         in it, so a page of boxes is never a page of "Signature". -->
    <label class="fe-spdf__prop">
      <span>{{ t('plugin.pdf.label') }}</span>
      <input
        class="fe-cfield__input fe-spdf__name"
        type="text"
        :value="field.label ?? ''"
        :placeholder="typeLabel(field.type)"
        :disabled="disabled"
        :data-testid="`${prefix}-label`"
        @input="setLabel(($event.target as HTMLInputElement).value)"
      />
    </label>

    <div v-if="signers.length" class="fe-spdf__prop">
      <span :id="`${prefix}-${field.id}-assignee-lbl`" :data-testid="`${prefix}-assignee-label`">{{ assigneeLabel }}</span>
      <ChoiceButtons
        :options="assigneeOptions"
        :model-value="field.assignee || ANYONE"
        :disabled="disabled"
        :aria-labelledby="`${prefix}-${field.id}-assignee-lbl`"
        :testid-prefix="`${prefix}-assignee`"
        @update:model-value="(v: string | string[]) => assign(String(v) === ANYONE ? '' : String(v))"
      />
    </div>

    <!-- A signature is drawn, or it is a name typed in a face — the
         requester decides which, and only the typed one has a face to pick. -->
    <div v-if="drawn" class="fe-spdf__prop" :data-testid="`${prefix}-style`">
      <span :id="`${prefix}-${field.id}-style-lbl`">{{ t('plugin.pdf.style') }}</span>
      <ChoiceButtons
        :options="styleOptions"
        :model-value="style"
        :disabled="disabled"
        :aria-labelledby="`${prefix}-${field.id}-style-lbl`"
        :testid-prefix="`${prefix}-style`"
        @update:model-value="(v: string | string[]) => setStyle(String(v))"
      />
    </div>

    <label class="fe-spdf__prop fe-spdf__prop--check">
      <input
        type="checkbox"
        :checked="field.required === true"
        :disabled="disabled"
        :data-testid="`${prefix}-required`"
        @change="setRequired(($event.target as HTMLInputElement).checked)"
      />
      <span>{{ t('plugin.pdf.required') }}</span>
    </label>

    <!-- A text box says what it will take. ⚠ Only `text`: a date field
         already IS a date, a checkbox is a boolean, and a signature is a
         picture — a rule on any of them would be a control that does
         nothing. -->
    <template v-if="field.type === 'text'">
      <div class="fe-spdf__prop">
        <span :id="`${prefix}-${field.id}-rule-lbl`">{{ t('plugin.pdf.rule') }}</span>
        <ChoiceButtons
          :options="ruleOptions"
          :model-value="field.rule?.kind ?? 'any'"
          :disabled="disabled"
          :aria-labelledby="`${prefix}-${field.id}-rule-lbl`"
          :testid-prefix="`${prefix}-rule`"
          @update:model-value="(v: string | string[]) => setRuleKind(String(v))"
        />
      </div>
      <label class="fe-spdf__prop">
        <span>{{ t('plugin.pdf.rule_min_label') }}</span>
        <input
          class="fe-cfield__input fe-spdf__num"
          type="number"
          min="0"
          :value="field.rule?.min ?? ''"
          :disabled="disabled"
          :data-testid="`${prefix}-rule-min`"
          @input="setRuleBound('min', ($event.target as HTMLInputElement).value)"
        />
      </label>
      <label class="fe-spdf__prop">
        <span>{{ t('plugin.pdf.rule_max_label') }}</span>
        <input
          class="fe-cfield__input fe-spdf__num"
          type="number"
          min="0"
          :value="field.rule?.max ?? ''"
          :disabled="disabled"
          :data-testid="`${prefix}-rule-max`"
          @input="setRuleBound('max', ($event.target as HTMLInputElement).value)"
        />
      </label>
    </template>

    <!-- ⚠ A date box's layout is its `rule`: it is the one thing about the
         box that is still an open question once it has a name, and leaving
         it unasked meant the plugin's own default was the only answer
         anybody could give. Buttons, never a dropdown (v3 §2). -->
    <template v-if="field.type === 'date' && dateParts">
      <div class="fe-spdf__prop">
        <span :id="`${prefix}-${field.id}-order-lbl`">{{ caption('order', 'plugin.pdf.date_order') }}</span>
        <ChoiceButtons
          :options="orderOptions"
          :model-value="pickedOrder"
          :disabled="disabled"
          :aria-labelledby="`${prefix}-${field.id}-order-lbl`"
          :testid-prefix="`${prefix}-order`"
          @update:model-value="(v: string | string[]) => setOrder(String(v))"
        />
      </div>
      <div class="fe-spdf__prop">
        <span :id="`${prefix}-${field.id}-sep-lbl`">{{ caption('separator', 'plugin.pdf.date_separator') }}</span>
        <ChoiceButtons
          :options="separatorOptions"
          :model-value="pickedSeparator"
          :disabled="disabled"
          :aria-labelledby="`${prefix}-${field.id}-sep-lbl`"
          :testid-prefix="`${prefix}-sep`"
          @update:model-value="(v: string | string[]) => setSeparator(String(v))"
        />
      </div>
      <p class="fe-spdf__dateex" :data-testid="`${prefix}-date-example`">
        <span class="fe-spdf__dateex-title">{{ caption('example', 'plugin.pdf.date_example') }}</span>
        <bdi class="fe-spdf__dateex-value">{{ dateShown }}</bdi>
      </p>
    </template>
    <div v-else-if="field.type === 'date' && formatOptions.length" class="fe-spdf__prop">
      <span :id="`${prefix}-${field.id}-format-lbl`">{{ t('plugin.pdf.format') }}</span>
      <ChoiceButtons
        :options="formatOptions"
        :model-value="pickedFormat"
        :disabled="disabled"
        :aria-labelledby="`${prefix}-${field.id}-format-lbl`"
        :testid-prefix="`${prefix}-format`"
        @update:model-value="(v: string | string[]) => setFormat(String(v))"
      />
    </div>

    <!-- The face the stamper draws this field in. Each button is set in the
         face it offers: a font's NAME tells nobody what it looks like. -->
    <div
      v-if="asksFont"
      class="fe-signfonts fe-spdf__prop"
      role="group"
      :aria-label="t('plugin.pdf.font')"
      :data-testid="`${prefix}-fonts`"
    >
      <button
        v-for="f in SIGN_FONTS"
        :key="f.key"
        type="button"
        class="fe-signfonts__btn"
        :class="[f.className, { 'is-on': (field.font ?? 'caveat') === f.key }]"
        :title="f.name"
        :disabled="disabled"
        :aria-pressed="(field.font ?? 'caveat') === f.key ? 'true' : 'false'"
        :data-testid="`${prefix}-font-${f.key}`"
        @click="setFont(f.key)"
      >
        Aa
      </button>
    </div>

    <!-- What is printed under the signature: chosen per box, from the
         lines the plugin can really fill in (its signing record), with the
         plugin's own wording previewed underneath. ⚠ Nothing typed here
         reaches the paper — the requester picks WHICH facts, never their
         values. -->
    <div v-if="drawn && lineOptions.length" class="fe-spdf__prop fe-spdf__lines" :data-testid="`${prefix}-lines`">
      <span :id="`${prefix}-${field.id}-lines-lbl`">{{ t('plugin.pdf.lines') }}</span>
      <ChoiceButtons
        :options="lineOptions"
        :model-value="pickedLines"
        multi
        :disabled="disabled"
        :aria-labelledby="`${prefix}-${field.id}-lines-lbl`"
        :testid-prefix="`${prefix}-line`"
        @update:model-value="setLines"
      />
      <div class="fe-spdf__linepreview" :data-testid="`${prefix}-lines-preview`">
        <span class="fe-spdf__linepreview-title">{{ t('plugin.pdf.lines_preview') }}</span>
        <template v-if="linePreview.length">
          <span v-for="(ln, i) in linePreview" :key="i" class="fe-spdf__linepreview-line">{{ ln }}</span>
        </template>
        <span v-else class="fe-spdf__linepreview-line is-none">{{ t('plugin.pdf.lines_none') }}</span>
      </div>
    </div>

    <slot name="extra"></slot>

    <button
      type="button"
      class="fe-btn fe-btn--sm fe-btn--danger"
      :disabled="disabled"
      :data-testid="`${prefix}-delete`"
      @click="emit('remove')"
    >
      {{ t('plugin.pdf.delete') }}
    </button>
  </div>
</template>
