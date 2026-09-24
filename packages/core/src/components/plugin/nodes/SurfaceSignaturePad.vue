<script setup lang="ts">
/**
 * SurfaceSignaturePad — a plugin surface's `signature-pad` node: a
 * signature as a transparent PNG into `data.values[id]` (`{png_b64, mode}`).
 *
 * Three ways in, the node picks which: draw (pointer strokes on a canvas,
 * DPR-aware so a phone's signature is not a blur), type (a name set in one of
 * the five shipped faces, rendered to the same canvas size), upload (a
 * PNG/JPEG of at most 200 KB, fitted into 600×200). Every way ends in the
 * same value; Clear ends in `null`. The ink is black whatever the theme: the
 * picture is bound for a document, not for this screen.
 *
 * ⚠⚠ The typed face is CHOSEN and the choice travels (`value.font`). It
 * used to be one CSS stack of fonts the machine might happen to have, so the
 * same name signed on a Mac, on Windows and on a phone came out in three
 * different hands — and on a machine with none of them, in the browser's
 * generic cursive. The five are shipped with the package (`lib/signFonts`,
 * `styles/sign-fonts.css`) and the same five are offered on a PDF text field,
 * so a document signed here and filled there matches.
 *
 * The rules (modes, sizes, verdicts) are in `lib/signaturePad`; this file
 * is the canvas work.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { LocaleCode } from '../../../types/ExplorerConfig';
import { useLocale } from '../../../composables/useLocale';
import { SIGN_FONTS, isSignFontKey, signFont, type SignFontKey } from '../../../lib/signFonts';
import {
  SIGNATURE_FONT_DEFAULT,
  SIGNATURE_INK,
  SIGNATURE_MAX_HEIGHT,
  SIGNATURE_MAX_WIDTH,
  fitFontSize,
  fitWithin,
  isSignatureValue,
  padSize,
  signatureFontOf,
  signatureModes,
  stripDataUrl,
  uploadVerdict,
  type SignatureMode,
  type SignatureValue,
} from '../../../lib/signaturePad';

const props = defineProps<{
  id: string;
  modes?: unknown;
  width?: unknown;
  height?: unknown;
  modelValue: SignatureValue | null | undefined;
  locale: LocaleCode;
  disabled?: boolean;
  invalid?: boolean;
  /** The face a typed signature starts in (a `lib/signFonts` key). */
  font?: unknown;
  /**
   * The faces a typed signature may use; one face means no picker at all.
   * A box whose face the REQUESTER chose passes just that one, so what the
   * signer sees is what the stamper prints.
   */
  fonts?: unknown;
  /**
   * The box's name, drawn as a field label — with the same `*` a required
   * form field carries (v3). ⚠ The owner, 2026-09-21: "imza zorunluydu ama
   * imzanın zorunlu olduğunu `*` ile belirtmiyoruz". The signature was the
   * one required box on the screen that did not say so, because it was a
   * plain text line above the pad rather than a label.
   */
  label?: string;
  required?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: SignatureValue | null): void;
}>();

const { t } = useLocale(() => props.locale);

const modes = computed(() => signatureModes(props.modes));
const size = computed(() => padSize(props.width, props.height));
const mode = ref<SignatureMode>(isSignatureValue(props.modelValue) && modes.value.includes(props.modelValue.mode) ? props.modelValue.mode : modes.value[0]);

/** The faces this pad offers — all five unless the node narrowed them. */
const offeredFonts = computed(() => {
  const want = Array.isArray(props.fonts) ? (props.fonts as unknown[]).filter(isSignFontKey) : [];
  return want.length ? SIGN_FONTS.filter((f) => want.includes(f.key)) : SIGN_FONTS;
});

function startFont(): SignFontKey {
  const offered = offeredFonts.value.map((f) => f.key);
  const fromValue = isSignatureValue(props.modelValue) ? signatureFontOf(props.modelValue) : undefined;
  if (fromValue && offered.includes(fromValue)) return fromValue;
  if (isSignFontKey(props.font) && offered.includes(props.font)) return props.font;
  return offered.includes(SIGNATURE_FONT_DEFAULT) ? SIGNATURE_FONT_DEFAULT : offered[0];
}

const canvas = ref<HTMLCanvasElement | null>(null);
const typed = ref('');
/** The face the typed signature is set in — seeded from an arriving value. */
const font = ref<SignFontKey>(startFont());
const uploadError = ref<'' | 'too_big' | 'bad_type' | 'unreadable'>('');
/** What the pad currently shows for `type` / `upload` (a data URL). */
const preview = ref('');

const hasValue = computed(() => isSignatureValue(props.modelValue));
/** The picture the value holds, for the non-draw modes and for a value that arrived with the surface. */
const valueUrl = computed(() => (isSignatureValue(props.modelValue) ? `data:image/png;base64,${props.modelValue.png_b64}` : ''));

function ctxOf(c: HTMLCanvasElement | null): CanvasRenderingContext2D | null {
  if (!c) return null;
  try {
    return c.getContext('2d');
  } catch {
    return null;
  }
}

function dpr(): number {
  return typeof window !== 'undefined' && window.devicePixelRatio > 0 ? Math.min(3, window.devicePixelRatio) : 1;
}

/** The drawing canvas sized for the pad (CSS px × DPR), cleared, ink ready. */
function prepareCanvas(): void {
  const c = canvas.value;
  if (!c) return;
  const k = dpr();
  c.width = Math.round(size.value.width * k);
  c.height = Math.round(size.value.height * k);
  const ctx = ctxOf(c);
  if (!ctx) return;
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.clearRect(0, 0, c.width, c.height);
  ctx.scale(k, k);
  ctx.lineWidth = 2.2;
  ctx.lineCap = 'round';
  ctx.lineJoin = 'round';
  ctx.strokeStyle = SIGNATURE_INK;
}

/* ── draw ─────────────────────────────────────────────────────────────── */

let drawing = false;
let last: { x: number; y: number } | null = null;

function pointOf(ev: PointerEvent): { x: number; y: number } {
  const c = canvas.value;
  const r = c?.getBoundingClientRect();
  if (!c || !r || !r.width) return { x: ev.clientX, y: ev.clientY };
  // The canvas may be drawn narrower than its logical size on a phone.
  const kx = size.value.width / r.width;
  const ky = size.value.height / r.height;
  return { x: (ev.clientX - r.left) * kx, y: (ev.clientY - r.top) * ky };
}

function onDown(ev: PointerEvent): void {
  if (props.disabled) return;
  const ctx = ctxOf(canvas.value);
  if (!ctx) return;
  drawing = true;
  last = pointOf(ev);
  try {
    (ev.target as Element).setPointerCapture?.(ev.pointerId);
  } catch {
    /* not every runtime has pointer capture */
  }
  // A dot: a tap must leave a mark too.
  ctx.beginPath();
  ctx.moveTo(last.x, last.y);
  ctx.lineTo(last.x + 0.1, last.y + 0.1);
  ctx.stroke();
  ev.preventDefault?.();
}

function onMove(ev: PointerEvent): void {
  if (!drawing || !last) return;
  const ctx = ctxOf(canvas.value);
  if (!ctx) return;
  const p = pointOf(ev);
  ctx.beginPath();
  ctx.moveTo(last.x, last.y);
  ctx.lineTo(p.x, p.y);
  ctx.stroke();
  last = p;
  ev.preventDefault?.();
}

function onUp(): void {
  if (!drawing) return;
  drawing = false;
  last = null;
  exportCanvas(canvas.value, 'draw');
}

function exportCanvas(c: HTMLCanvasElement | null, m: SignatureMode): void {
  if (!c) return;
  let url = '';
  try {
    url = c.toDataURL('image/png');
  } catch {
    url = '';
  }
  const b64 = stripDataUrl(url);
  if (!b64) return;
  if (m !== 'draw') preview.value = url;
  // ⚠ The face rides ONLY with a typed signature: on a drawn or uploaded
  // one it would be a fact about nothing, and a plugin reading `font` would
  // re-render somebody's handwriting as text.
  emit('update:modelValue', m === 'type' ? { png_b64: b64, mode: m, font: font.value } : { png_b64: b64, mode: m });
}

/* ── type ─────────────────────────────────────────────────────────────── */

/**
 * Make sure the chosen face is actually LOADED before the canvas uses it.
 *
 * ⚠⚠ A canvas does not wait. `ctx.font = "48px Caveat"` on a face the
 * browser has not fetched yet silently falls back to the generic, the PNG is
 * exported from that, and the signature is wrong in a way nothing on screen
 * shows — the preview repaints correctly a moment later, when the font
 * arrives, while the exported bytes keep the fallback. `document.fonts.load`
 * is the only honest gate; a browser without the Font Loading API just
 * renders as it always did.
 */
async function ensureFont(px: number, stack: string): Promise<void> {
  const fonts = (document as Document & { fonts?: FontFaceSet }).fonts;
  if (!fonts || typeof fonts.load !== 'function') return;
  try {
    await fonts.load(`${px}px ${stack}`, 'Aa');
  } catch {
    /* an unknown descriptor is not worth failing a signature over */
  }
}

async function renderTyped(): Promise<void> {
  const text = typed.value.trim();
  if (!text) {
    preview.value = '';
    emit('update:modelValue', null);
    return;
  }
  const stack = signFont(font.value).stack;
  await ensureFont(Math.round(size.value.height * 0.6), stack);
  // The person may have cleared the field while the face was loading.
  if (typed.value.trim() !== text) return;
  const c = document.createElement('canvas');
  const k = 2;
  const { width, height } = size.value;
  c.width = width * k;
  c.height = height * k;
  const ctx = ctxOf(c);
  if (!ctx) return;
  ctx.scale(k, k);
  ctx.fillStyle = SIGNATURE_INK;
  ctx.textBaseline = 'middle';
  const px = fitFontSize(
    (n) => {
      ctx.font = `${n}px ${stack}`;
      return ctx.measureText(text).width;
    },
    width - 24,
    height,
  );
  ctx.font = `${px}px ${stack}`;
  ctx.fillText(text, 12, height / 2);
  exportCanvas(c, 'type');
}

/** Another face for the same name: re-render, so the value matches the pick. */
function pickFont(key: SignFontKey): void {
  if (props.disabled || key === font.value) return;
  font.value = key;
  if (typed.value.trim()) void renderTyped();
}

/* ── upload ───────────────────────────────────────────────────────────── */

const fileInput = ref<HTMLInputElement | null>(null);

function onFile(ev: Event): void {
  const input = ev.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file) return;
  const verdict = uploadVerdict(file);
  if (verdict !== 'ok') {
    uploadError.value = verdict;
    return;
  }
  uploadError.value = '';
  const url = URL.createObjectURL(file);
  const img = new Image();
  img.onload = () => {
    URL.revokeObjectURL(url);
    const fit = fitWithin(img.naturalWidth, img.naturalHeight, SIGNATURE_MAX_WIDTH, SIGNATURE_MAX_HEIGHT);
    const c = document.createElement('canvas');
    c.width = fit.width;
    c.height = fit.height;
    const ctx = ctxOf(c);
    if (!ctx) return;
    ctx.drawImage(img, 0, 0, fit.width, fit.height);
    exportCanvas(c, 'upload');
  };
  img.onerror = () => {
    URL.revokeObjectURL(url);
    uploadError.value = 'unreadable';
  };
  img.src = url;
}

/* ── shared ───────────────────────────────────────────────────────────── */

function clear(): void {
  typed.value = '';
  preview.value = '';
  uploadError.value = '';
  prepareCanvas();
  emit('update:modelValue', null);
}

function pick(m: SignatureMode): void {
  if (m === mode.value) return;
  mode.value = m;
}

watch(mode, async (m) => {
  if (m === 'draw') {
    await nextTick();
    prepareCanvas();
  }
});

watch(size, () => {
  if (mode.value === 'draw') prepareCanvas();
});

onMounted(() => {
  if (mode.value === 'draw') prepareCanvas();
});

onBeforeUnmount(() => {
  drawing = false;
});

const uploadErrorText = computed(() => {
  switch (uploadError.value) {
    case 'too_big':
      return t('plugin.sig.upload_too_big', { kb: 200 });
    case 'bad_type':
      return t('plugin.sig.upload_bad_type');
    case 'unreadable':
      return t('plugin.sig.upload_unreadable');
    default:
      return '';
  }
});
</script>

<template>
  <div
    class="fe-ssig"
    data-testid="surface-signature"
    :class="{ 'is-invalid': invalid, 'is-disabled': disabled }"
    :data-mode="mode"
    :role="label ? 'group' : undefined"
    :aria-labelledby="label ? `fe-ssig-${id}-lbl` : undefined"
  >
    <!-- The same label a form field wears, star included. -->
    <span v-if="label" :id="`fe-ssig-${id}-lbl`" class="fe-cfield__label" data-testid="surface-signature-label">
      {{ label }}<span v-if="required" class="fe-cfield__req" aria-hidden="true">*</span>
    </span>
    <div v-if="modes.length > 1" class="fe-ssig__modes" role="tablist">
      <button
        v-for="m in modes"
        :key="m"
        type="button"
        class="fe-btn fe-btn--sm"
        :class="{ 'fe-btn--primary': m === mode }"
        role="tab"
        :aria-selected="m === mode ? 'true' : 'false'"
        :disabled="disabled"
        :data-testid="`surface-signature-mode-${m}`"
        @click="pick(m)"
      >
        {{ t(`plugin.sig.mode_${m}`) }}
      </button>
    </div>

    <div v-if="mode === 'draw'" class="fe-ssig__padwrap" :style="{ maxWidth: `${size.width}px` }">
      <canvas
        ref="canvas"
        class="fe-ssig__canvas"
        :style="{ aspectRatio: `${size.width} / ${size.height}` }"
        :aria-label="t('plugin.sig.draw_hint')"
        data-testid="surface-signature-canvas"
        @pointerdown="onDown"
        @pointermove="onMove"
        @pointerup="onUp"
        @pointercancel="onUp"
        @pointerleave="onUp"
      ></canvas>
      <p v-if="!hasValue" class="fe-ssig__hint">{{ t('plugin.sig.draw_hint') }}</p>
    </div>

    <div v-else-if="mode === 'type'" class="fe-ssig__typewrap">
      <input
        :id="`fe-ssig-${id}`"
        class="fe-cfield__input fe-ssig__typed"
        type="text"
        :placeholder="t('plugin.sig.type_placeholder')"
        :disabled="disabled"
        :value="typed"
        autocomplete="name"
        data-testid="surface-signature-typed"
        @input="typed = ($event.target as HTMLInputElement).value; void renderTyped()"
      />
      <!-- The five faces, each button set in the face it offers: a name of a
           font tells nobody what their signature will look like. -->
      <div
        v-if="offeredFonts.length > 1"
        class="fe-signfonts"
        role="group"
        :aria-label="t('plugin.sign.font_label')"
        data-testid="surface-signature-fonts"
      >
        <button
          v-for="f in offeredFonts"
          :key="f.key"
          type="button"
          class="fe-signfonts__btn"
          :class="[f.className, { 'is-on': f.key === font }]"
          :disabled="disabled"
          :title="f.name"
          :aria-pressed="f.key === font ? 'true' : 'false'"
          :data-testid="`surface-signature-font-${f.key}`"
          @click="pickFont(f.key)"
        >
          {{ typed.trim() || f.name }}
        </button>
      </div>
      <div class="fe-ssig__preview" :style="{ maxWidth: `${size.width}px`, aspectRatio: `${size.width} / ${size.height}` }">
        <img v-if="preview || valueUrl" class="fe-ssig__img" :src="preview || valueUrl" alt="" />
      </div>
    </div>

    <div v-else class="fe-ssig__uploadwrap">
      <label class="fe-btn fe-btn--sm fe-ssig__pick">
        <input
          ref="fileInput"
          type="file"
          accept="image/png,image/jpeg"
          class="fe-ssig__file"
          :disabled="disabled"
          data-testid="surface-signature-file"
          @change="onFile"
        />
        {{ t('plugin.sig.upload_pick') }}
      </label>
      <p class="fe-ssig__hint">{{ t('plugin.sig.upload_hint', { kb: 200 }) }}</p>
      <p v-if="uploadErrorText" class="fe-surface__error" role="alert">{{ uploadErrorText }}</p>
      <div v-if="preview || valueUrl" class="fe-ssig__preview" :style="{ maxWidth: `${size.width}px` }">
        <img class="fe-ssig__img" :src="preview || valueUrl" alt="" />
      </div>
    </div>

    <div class="fe-ssig__foot">
      <button type="button" class="fe-btn fe-btn--sm" :disabled="disabled || !hasValue" data-testid="surface-signature-clear" @click="clear">
        {{ t('plugin.sig.clear') }}
      </button>
      <span v-if="hasValue" class="fe-ssig__signed">{{ t('plugin.sig.signed') }}</span>
    </div>
  </div>
</template>
