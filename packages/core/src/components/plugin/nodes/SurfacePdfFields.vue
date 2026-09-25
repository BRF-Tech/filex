<script setup lang="ts">
/**
 * SurfacePdfFields — a plugin surface's `pdf-fields` node: a PDF drawn by
 * pdf.js with signature / initials / date / text / checkbox boxes on top.
 *
 * Four modes, one drawing.
 *
 * ⚠⚠ `define` and `place` are the author's two JOBS, split (v3 §3.3).
 * Naming a box and finding a place for it were one screen — a palette of
 * types, a rectangle under the pointer, and a strip of properties for
 * whichever box happened to be selected — and the owner's words for it were
 * "you are still doing both at once". `define` shows the boxes as cards and
 * no document at all; `place` shows the document and hands out the boxes
 * that still need a place, one tap each. `edit` keeps the one-screen form
 * for a plugin that wants it. All three send the WHOLE `fields[]` back under
 * the node's id; an unplaced box travels as `placed: false`.
 *
 * `fill` is a signer filling their own boxes (the pad opens inline for a signature, text is typed in place
 * under its rule, a checkbox toggles, a date takes today on a tap) and sends
 * `{fields: [{id, value, font?, rule?}]}`; the other signers' boxes are grey
 * outlines carrying that signer's name.
 *
 * ⚠⚠ A `text` field's rule is enforced WHILE the person types, not after
 * they submit: an invalid signature page that only says so on the way out is
 * a page that gets signed twice. `lib/pdfFieldRules` holds the shaping and
 * the verdict; the rule then travels back with the value so the stamping
 * plugin formats what it is handed instead of guessing from the string
 * (`01/02/2026` is a date to a rule and ambiguous text to a parser).
 *
 * ⚠ The FACE travels for the same reason, and it is one of five shipped
 * with the package (`lib/signFonts`) rather than a CSS name the machine might
 * happen to have — the same five the signature pad offers, so a document
 * signed on a phone and filled on a desktop matches.
 *
 * Geometry is fractions of the rendered page (`lib/pdfFieldsGeom`), so the
 * boxes are CSS percentages of the page element and survive every zoom
 * without being recomputed; the canvas underneath is re-rendered lazily
 * per page (IntersectionObserver) at the device's pixel ratio. The rules —
 * who may touch what, what goes on the wire — are `lib/pdfFields`.
 *
 * Bytes: `src.path` through the explorer's authenticated preview fetch
 * (like SurfacePreview), `src.ref` through the host's `fileUrl` resolver
 * (a public page's exposed copy), `src.url` as given. A document that will
 * not load is an error node, never a blank.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { LocaleCode, ThemeMode } from '../../../types/ExplorerConfig';
import type { FileApi } from '../../../composables/useFileApi';
import type { FileRefResolver, PdfFieldsSource } from '../../../types/Plugins';
import { useLocale } from '../../../composables/useLocale';
import { fetchViewerArrayBuffer } from '../../../composables/useViewerFetch';
import { labelOf } from '../../../lib/pluginLabel';
import { inkOn } from '../../../composables/usePublicBranding';
import { loadPdfjs } from '../../../lib/pdfjsLoader';
import { actionIconSvg } from '../../../lib/actionIcons';
import {
  DEFAULT_FIELD_SIZE,
  allowedTypes,
  applyFillValues,
  copyToPage,
  fillValues,
  hasValue,
  isFieldOf,
  defineField,
  isPlaced,
  normalizeFields,
  normalizeSigners,
  placeField,
  unplacedFields,
  signerColor,
  todayIso,
  withFont,
  withValue,
  type PdfField,
  type PdfFieldType,
} from '../../../lib/pdfFields';
import {
  PDF_RULE_KINDS,
  applyRule,
  ruleError,
  ruleInputMode,
  ruleMaxLength,
  type PdfFieldRule,
  type PdfRuleKind,
} from '../../../lib/pdfFieldRules';
import { SIGN_FONTS, signFont, type SignFontKey } from '../../../lib/signFonts';
import { clampFrac, fitPageWidth, pointToFrac, rectFromPoints, type PageBox } from '../../../lib/pdfFieldsGeom';
import type { SignatureValue } from '../../../lib/signaturePad';
import PdfFieldEditor from './PdfFieldEditor.vue';
import SurfaceSignaturePad from './SurfaceSignaturePad.vue';
import ChoiceButtons, { type ChoiceOption } from '../../ChoiceButtons.vue';

const props = defineProps<{
  id: string;
  src?: PdfFieldsSource;
  mode?: string;
  fields?: unknown;
  signers?: unknown;
  signer?: string;
  types?: unknown;
  /** `define` / `edit`: the date layouts a `date` box may be written in. */
  formats?: unknown;
  /** The date control's three captions, as the plugin words them (`date_labels`). */
  dateLabels?: unknown;
  /** Edit: the `fields[]` the host holds; fill: `{fields: [{id, value}]}`. */
  modelValue?: unknown;
  locale: LocaleCode;
  theme?: ThemeMode;
  disabled?: boolean;
  invalid?: boolean;
  api?: Pick<FileApi, 'fetchBlob'>;
  fileUrl?: FileRefResolver;
  pdfWorkerUrl?: string | null;
  /**
   * v3 §3.2 — `page`: this node owns the screen, so the document is fitted
   * to the BOX rather than to the text column, and the person never scrolls
   * in two directions to reach a signature box. The zoom controls stay:
   * fitting is the starting point, not a cage.
   */
  layout?: 'inline' | 'page';
  /** The height a `page` layout may fill (a CSS length). Default: the frame's. */
  pageHeight?: string;
  /**
   * The lines the plugin can print under a signature, offered per signature
   * box on the author's screens (`lib/pdfFields.PdfStampLine`).
   */
  stampLines?: unknown;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', v: unknown): void;
}>();

const { t } = useLocale(() => props.locale);

/** Every author mode; `fill` is the signer's. */
const editing = computed(() => props.mode !== 'fill');
/** The boxes as cards, with no document on screen. */
const defining = computed(() => props.mode === 'define');
/** The document, with the defined boxes waiting to be put on it. */
const placingStep = computed(() => props.mode === 'place');
const signers = computed(() => normalizeSigners(props.signers));
const palette = computed(() => allowedTypes(props.types));

/* ── the fields ───────────────────────────────────────────────────────── */

const local = ref<PdfField[]>([]);
let lastEmitted: unknown = undefined;

function seed(): void {
  if (editing.value) {
    local.value = Array.isArray(props.modelValue) ? normalizeFields(props.modelValue) : normalizeFields(props.fields);
  } else {
    local.value = applyFillValues(normalizeFields(props.fields), props.modelValue);
  }
}

function publish(): void {
  const v = editing.value ? local.value.map((f) => ({ ...f })) : fillValues(local.value, props.signer);
  lastEmitted = v;
  emit('update:modelValue', v);
}

/**
 * A seed that arrives WHILE a box is being dragged waits for the drag to end.
 *
 * ⚠⚠ Every `change` answer re-sends the surface, so `fields` arrives as a new
 * array (and, for an echo the person has already overtaken, with the box
 * where it was BEFORE the gesture in progress). Seeding then replaced the
 * list under the pointer: the dragged box snapped back for a frame and was
 * pulled forward again on the next move — part of the flicker the owner saw
 * while placing and moving boxes (2026-09-21, item 6). The drag's own
 * `publish()` at the end carries the newer list anyway, so the deferred seed
 * is usually dropped rather than run.
 */
let seedLater = false;
function reseed(): void {
  if (drag) {
    seedLater = true;
    return;
  }
  seed();
}

seed();
watch([() => props.fields, () => props.mode, () => props.signer], () => reseed());
watch(
  () => props.modelValue,
  (v) => {
    if (v !== lastEmitted) reseed();
  },
);

const byPage = computed(() => {
  const m = new Map<number, PdfField[]>();
  for (const f of local.value) {
    // A defined box that nobody has placed is not anywhere yet.
    if (!isPlaced(f)) continue;
    const arr = m.get(f.page) ?? [];
    arr.push(f);
    m.set(f.page, arr);
  }
  return m;
});

function fieldColor(f: PdfField): string {
  return signerColor(signers.value, f.assignee);
}

/**
 * A box card's colours: its signer's, and the ink that reads on THAT colour.
 *
 * ⚠ Not `--fe-text-on-primary`. A signer's colour is an identity colour, the
 * same in every theme, so the ink on it must be the colour's own — the theme
 * token is dark in dark mode and measured 3.5:1 on the first signer's blue
 * (#57). A colour that is not hex (a plugin may name its own) keeps the token.
 */
function cardStyle(f: PdfField): Record<string, string> {
  const color = fieldColor(f);
  const ink = inkOn(color);
  return ink ? { '--spdf-color': color, '--spdf-ink': ink } : { '--spdf-color': color };
}

function signerLabel(assignee: string | undefined): string {
  const s = signers.value.find((x) => x.id === assignee);
  return s ? labelOf(s.label, props.locale) || s.id : assignee || '';
}

function mine(f: PdfField): boolean {
  return !editing.value && isFieldOf(f, props.signer);
}

function typeLabel(type: PdfFieldType): string {
  return t(`plugin.pdf.type_${type}`);
}

/**
 * What this field is CALLED — its own name, or the type's as a stand-in.
 *
 * ⚠ v3 §3.1. Three signature boxes on one page were three identical grey
 * rectangles saying "Signature"; a signer could only tell them apart by
 * where they were. The name is shown in the box, in the pad's heading and
 * on the text box's own label, so the same word identifies the field
 * wherever the person meets it.
 */
function fieldLabel(f: PdfField): string {
  const own = (f.label ?? '').trim();
  if (own) return own;
  // An unnamed box is its kind's name, NUMBERED among the unnamed boxes of
  // that kind when there are several — the same rule the signing app uses
  // on its own screens (views.NameIn), so "Signature 2" on the document is
  // "Signature 2" in the form that asks for it.
  const same = local.value.filter((g) => g.type === f.type && !(g.label ?? '').trim());
  if (same.length < 2) return typeLabel(f.type);
  return `${typeLabel(f.type)} ${same.findIndex((g) => g.id === f.id) + 1}`;
}

/* ── the document ─────────────────────────────────────────────────────── */

const status = ref<'loading' | 'ok' | 'error'>('loading');
const pages = ref<Array<PageBox & { num: number }>>([]);
const zoom = ref(1);
const containerEl = ref<HTMLDivElement | null>(null);
const containerW = ref(640);
const containerH = ref(0);

/** This node has the screen to itself. */
const pageLayout = computed(() => props.layout === 'page');

// eslint-disable-next-line @typescript-eslint/no-explicit-any
let pdfDoc: any = null;
let seq = 0;
let observer: IntersectionObserver | null = null;
let resizeObs: ResizeObserver | null = null;
const pageEls = new Map<number, HTMLDivElement>();
const rendered = new Map<number, number>();
const canvases = new Map<number, HTMLCanvasElement>();

/**
 * The width a page is drawn at before zoom.
 *
 * ⚠⚠ In `page` layout it is whichever of the two constraints binds: the
 * column's width, or the width at which the WHOLE page still fits the
 * height. Fitting to width alone is what produced the complaint that
 * started this — a portrait page in a landscape window is taller than the
 * screen, so signing meant scrolling down to find the box and sideways to
 * read the line it sits on. A margin is taken off the height so the page
 * does not touch the toolbar above it.
 */
const fitWidth = computed(() => {
  if (!pageLayout.value) return containerW.value;
  const first = pages.value[0];
  if (!first) return containerW.value;
  return fitPageWidth({ width: containerW.value, height: containerH.value }, first);
});

/** The page's drawn width in CSS px: the fitted width, times the zoom. */
const pageWidth = computed(() => Math.max(120, Math.round(fitWidth.value * zoom.value)));

async function bytes(): Promise<ArrayBuffer> {
  const s = props.src ?? {};
  if (s.path) {
    if (!props.api) throw new Error('no api');
    const r = await props.api.fetchBlob(s.path);
    URL.revokeObjectURL(r.url);
    return r.blob.arrayBuffer();
  }
  const url = s.url || (s.ref ? props.fileUrl?.(s.ref)?.url : '');
  if (!url) throw new Error('no source');
  return fetchViewerArrayBuffer({ url, headers: {}, credentials: 'same-origin' });
}

async function load(): Promise<void> {
  const run = ++seq;
  status.value = 'loading';
  pages.value = [];
  disconnect();
  try {
    pdfDoc?.destroy?.();
  } catch {
    /* ignore */
  }
  pdfDoc = null;
  try {
    const lib = await loadPdfjs(props.pdfWorkerUrl ?? undefined);
    if (!lib) throw new Error('pdfjs unavailable');
    const data = await bytes();
    if (run !== seq) return;
    const doc = await lib.getDocument({ data }).promise;
    if (run !== seq) {
      doc.destroy?.();
      return;
    }
    pdfDoc = doc;
    const list: Array<PageBox & { num: number }> = [];
    for (let n = 1; n <= doc.numPages; n++) {
      const page = await doc.getPage(n);
      const vp = page.getViewport({ scale: 1 });
      list.push({ num: n, width: vp.width, height: vp.height, rotation: vp.rotation ?? 0 });
    }
    if (run !== seq) return;
    pages.value = list;
    status.value = 'ok';
    await nextTick();
    observe();
  } catch {
    if (run !== seq) return;
    status.value = 'error';
  }
}

function disconnect(): void {
  observer?.disconnect();
  observer = null;
  pageEls.clear();
  rendered.clear();
  canvases.clear();
}

/**
 * A page element arrived (or was replaced): remember it, and have it drawn.
 *
 * ⚠⚠ Observed HERE, not only in `observe()` after a load. The same node
 * lives through the wizard's `define` step, where no page is drawn at all,
 * and its document finishes loading there — `observe()` then finds no page
 * to watch, and when `place` draws the pages a moment later nothing ever
 * renders them. Measured 2026-09-21: the observer was made at t=2.7 s, the
 * page element arrived at t=3.7 s, and the place step showed a blank page.
 * (It had been hidden by the reload-on-every-surface bug this round fixed:
 * the reload happened to observe the pages the second time.)
 */
function setPageEl(num: number, el: unknown): void {
  if (!(el instanceof HTMLDivElement)) return;
  const before = pageEls.get(num);
  if (before === el) return;
  if (before) observer?.unobserve(before);
  pageEls.set(num, el);
  if (!pdfDoc) return;
  if (observer) observer.observe(el);
  else if (typeof IntersectionObserver === 'undefined') void renderPage(num);
  // A replaced element holds no canvas: draw it again at the current width.
  if (before) {
    rendered.delete(num);
    canvases.delete(num);
  }
}

function observe(): void {
  if (typeof IntersectionObserver === 'undefined') {
    for (const n of pageEls.keys()) void renderPage(n);
    return;
  }
  observer = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        const n = Number((e.target as HTMLElement).dataset.page);
        if (e.isIntersecting) void renderPage(n);
      }
    },
    { rootMargin: '200px 0px' },
  );
  for (const el of pageEls.values()) observer.observe(el);
}

/**
 * Draws page `num` at the current width × DPR, once per width.
 *
 * ⚠⚠ Into a FRESH canvas, swapped in only once pdf.js has finished. Writing
 * `canvas.width` on the canvas already on screen clears it on the spot, and
 * the page stayed white until the asynchronous render came back — every
 * zoom, every re-fit (a strip appearing above the document is enough to
 * re-fit a `page` layout) blinked the whole document. The old bitmap now
 * stays up, stretched by CSS to the new size, until its replacement is
 * ready.
 */
async function renderPage(num: number): Promise<void> {
  if (!pdfDoc) return;
  const el = pageEls.get(num);
  if (!el) return;
  const wantW = pageWidth.value;
  if (rendered.get(num) === wantW) return;
  rendered.set(num, wantW);
  const meta = pages.value.find((p) => p.num === num);
  if (!meta) return;
  const k = typeof window !== 'undefined' && window.devicePixelRatio > 0 ? Math.min(3, window.devicePixelRatio) : 1;
  const scale = (wantW / meta.width) * k;
  const doc = pdfDoc;
  try {
    const page = await doc.getPage(num);
    const viewport = page.getViewport({ scale });
    const fresh = document.createElement('canvas');
    fresh.className = 'fe-spdf__canvas';
    fresh.width = Math.round(viewport.width);
    fresh.height = Math.round(viewport.height);
    const ctx = fresh.getContext('2d');
    if (!ctx) return;
    await page.render({ canvasContext: ctx, viewport }).promise;
    // A newer width (or another document) overtook this render: its own
    // render will swap in, and this bitmap is already stale.
    if (doc !== pdfDoc || rendered.get(num) !== wantW) return;
    const live = pageEls.get(num);
    if (!live) return;
    const old = canvases.get(num);
    if (old && old.parentNode === live) live.replaceChild(fresh, old);
    else live.prepend(fresh);
    canvases.set(num, fresh);
  } catch {
    if (rendered.get(num) === wantW) rendered.delete(num);
  }
}

function rerenderVisible(): void {
  for (const n of pageEls.keys()) {
    if (rendered.has(n)) {
      rendered.delete(n);
      void renderPage(n);
    }
  }
}

watch(pageWidth, () => rerenderVisible());

function zoomIn(): void {
  zoom.value = Math.min(3, Math.round(zoom.value * 1.25 * 100) / 100);
}
function zoomOut(): void {
  zoom.value = Math.max(0.5, Math.round((zoom.value / 1.25) * 100) / 100);
}
function zoomFit(): void {
  zoom.value = 1;
}

/**
 * Measure the pane the document is drawn into, and keep measuring it.
 *
 * ⚠⚠ Called on mount AND whenever the pane appears, because it does not
 * always exist when this component mounts: the wizard draws the same node in
 * `define` first (cards, no document) and the pane arrives only on the next
 * step. Measured once at mount, `containerEl` was null, no observer was
 * attached, and the width kept its 640px default — so the "fit the page to
 * the window" rule was computed against a box that had nothing to do with
 * the window, and an A4 page ran off the bottom of the screen.
 */
function measurePane(): void {
  const el = containerEl.value;
  if (!el) return;
  containerW.value = el.clientWidth || containerW.value;
  containerH.value = el.clientHeight || containerH.value;
  if (typeof ResizeObserver === 'undefined') return;
  resizeObs?.disconnect();
  resizeObs = new ResizeObserver(() => applyPaneSize());
  resizeObs.observe(el);
}

/**
 * Take the pane's current size — unless a box is being dragged.
 *
 * ⚠⚠ A re-fit DURING a drag changes the page's size under the pointer: the
 * fractions the drag is computing are then fractions of a different page,
 * and the box slides away from the finger. Measured 2026-09-21: selecting
 * the box on pointer-down brought a strip in above the document, the pane
 * lost 34px of height, and the fitted page shrank from 383px to 349px wide
 * mid-gesture. The size is taken again the moment the drag ends.
 */
function applyPaneSize(): void {
  if (drag) {
    sizeLater = true;
    return;
  }
  const w = containerEl.value?.clientWidth ?? 0;
  const h = containerEl.value?.clientHeight ?? 0;
  if (w > 0 && Math.abs(w - containerW.value) > 2) containerW.value = w;
  // ⚠ The height matters: in `page` layout it is half of what decides the
  // drawn width, so a window resized shorter must re-fit rather than keep
  // a page that no longer fits it.
  if (h > 0 && Math.abs(h - containerH.value) > 2) containerH.value = h;
}
let sizeLater = false;

watch(containerEl, () => {
  void nextTick(measurePane);
});

onMounted(() => {
  measurePane();
  void load();
});

/**
 * Which document this is, as a VALUE.
 *
 * ⚠⚠ Not the `src` object. Every `change` answer re-sends the surface, so
 * this node is handed a NEW `src` object carrying the same three strings,
 * and the deep watcher that stood here fired on every such arrival and
 * reloaded the document from scratch. Measured 2026-09-21 (the owner's
 * "the screen flickers while I place and drag boxes"): each placement's
 * debounced `change` answer tore the page down — "Loading…", every canvas
 * thrown away, redrawn — and a drag that was in progress when it landed kept
 * a DETACHED page element, whose zero-size rectangle made every later
 * pointer position read as the bottom-right corner: "sometimes the box goes
 * to the edge of the document". Only a different document reloads now.
 */
const srcKey = computed(() => {
  const s = props.src ?? {};
  return [s.ref ?? '', s.path ?? '', s.url ?? ''].join('\u0000');
});
watch(srcKey, () => void load());

onBeforeUnmount(() => {
  seq++;
  disconnect();
  resizeObs?.disconnect();
  try {
    pdfDoc?.destroy?.();
  } catch {
    /* ignore */
  }
  pdfDoc = null;
});

/* ── edit mode: place, move, resize ───────────────────────────────────── */

const selected = ref('');
const placing = ref<PdfFieldType | ''>('');

interface Drag {
  kind: 'place' | 'move' | 'resize';
  page: number;
  el: HTMLElement;
  start: { x: number; y: number };
  id?: string;
  orig?: PdfField;
  /** The box being drawn, while placing. */
  ghost?: { x: number; y: number; w: number; h: number };
}
let drag: Drag | null = null;
const ghost = ref<{ page: number; x: number; y: number; w: number; h: number } | null>(null);

function fracAt(ev: PointerEvent, el: HTMLElement): { x: number; y: number } {
  const r = el.getBoundingClientRect();
  return pointToFrac({ x: ev.clientX - r.left, y: ev.clientY - r.top }, { width: r.width, height: r.height });
}

/**
 * The rectangle of the page a drag is on, read FRESH, or null when there is
 * no page to read it from.
 *
 * ⚠⚠ The element captured at pointer-down is only a fallback. If the page
 * list is re-rendered mid-gesture the captured element is detached, and a
 * detached element measures 0×0 — `pointToFrac` then turns ANY pointer into
 * the corner (it clamps `x / 1` to 1), which is exactly how a dragged box
 * ended up pinned to the document's edge. A rectangle that is not a real
 * page is refused, and the box simply stays where it was for that frame.
 */
function dragRect(d: { page: number; el: HTMLElement }): DOMRect | null {
  const live = pageEls.get(d.page);
  const el = live && live.isConnected ? live : d.el;
  if (!el || !el.isConnected) return null;
  const r = el.getBoundingClientRect();
  return r.width >= 1 && r.height >= 1 ? r : null;
}

function fracIn(ev: PointerEvent, r: DOMRect): { x: number; y: number } {
  return pointToFrac({ x: ev.clientX - r.left, y: ev.clientY - r.top }, { width: r.width, height: r.height });
}

function pickType(type: PdfFieldType): void {
  placing.value = placing.value === type ? '' : type;
  selected.value = '';
}

/* ── define: the boxes, as cards ──────────────────────────────────────── */

/**
 * Add a box of this type. It has an owner; it has no place — and no name
 * until somebody types one.
 *
 * ⚠ Not pre-filled with the type's name any more. That default was written
 * into the request in the REQUESTER's language, so a Turkish signer of an
 * English requester's document was asked for "Signature", "Date" and "Text"
 * (2026-09-21). An unnamed box is shown under its kind's name — here as the
 * input's placeholder, and on every other screen in that reader's language.
 */
function addField(type: PdfFieldType): void {
  const f = defineField(local.value, type, {
    assignee: defaultAssignee(),
    required: true,
  });
  local.value = [...local.value, f];
  publish();
}

/** One card changed. */
function patchField(next: PdfField): void {
  local.value = local.value.map((f) => (f.id === next.id ? next : f));
  publish();
}

function removeField(id: string): void {
  local.value = local.value.filter((f) => f.id !== id);
  if (selected.value === id) selected.value = '';
  publish();
}

/* ── place: hand the defined boxes out, one at a time ─────────────────── */

/** The boxes still waiting for a place, in the order they were defined. */
const pending = computed(() => unplacedFields(local.value));
/** Which of them the next tap on the page will put down. */
const placingId = ref('');

function pickPending(id: string): void {
  placingId.value = placingId.value === id ? '' : id;
  selected.value = '';
}

/** Put the chosen box on this page, at this rectangle. */
function placePending(page: number, rect: { x: number; y: number; w: number; h: number }): void {
  const id = placingId.value;
  if (!id) return;
  local.value = local.value.map((f) => {
    if (f.id !== id) return f;
    const next: PdfField = { ...f, page, ...clampFrac(rect) };
    delete next.placed;
    return next;
  });
  placingId.value = '';
  selected.value = id;
  publish();
}

/** Take a box off the page again: it goes back to the waiting list. */
function unplace(): void {
  const id = selected.value;
  if (!id) return;
  local.value = local.value.map((f) => (f.id === id ? { ...f, placed: false } : f));
  selected.value = '';
  publish();
}

/** The name a waiting box is offered under, with whose it is. */
function pendingLabel(f: PdfField): string {
  const who = signerLabel(f.assignee);
  const name = fieldLabel(f);
  return who ? `${name} · ${who}` : name;
}

function onPageDown(ev: PointerEvent, num: number): void {
  if (!editing.value || props.disabled) return;
  const el = ev.currentTarget as HTMLElement;
  if (placingStep.value && placingId.value) {
    drag = { kind: 'place', page: num, el, start: fracAt(ev, el) };
    ghost.value = null;
    bindWindow();
    ev.preventDefault?.();
    return;
  }
  if (placing.value) {
    drag = { kind: 'place', page: num, el, start: fracAt(ev, el) };
    ghost.value = null;
    bindWindow();
    ev.preventDefault?.();
    return;
  }
  selected.value = '';
}

function onFieldDown(ev: PointerEvent, f: PdfField, num: number): void {
  if (!editing.value || props.disabled) return;
  const el = (ev.currentTarget as HTMLElement).closest('.fe-spdf__page') as HTMLElement | null;
  if (!el) return;
  selected.value = f.id;
  placing.value = '';
  drag = { kind: 'move', page: num, el, start: fracAt(ev, el), id: f.id, orig: { ...f } };
  bindWindow();
  ev.stopPropagation();
  ev.preventDefault?.();
}

function onHandleDown(ev: PointerEvent, f: PdfField, num: number): void {
  if (!editing.value || props.disabled) return;
  const el = (ev.currentTarget as HTMLElement).closest('.fe-spdf__page') as HTMLElement | null;
  if (!el) return;
  selected.value = f.id;
  drag = { kind: 'resize', page: num, el, start: fracAt(ev, el), id: f.id, orig: { ...f } };
  bindWindow();
  ev.stopPropagation();
  ev.preventDefault?.();
}

function onWindowMove(ev: PointerEvent): void {
  if (!drag) return;
  const r = dragRect(drag);
  if (!r) return;
  const p = fracIn(ev, r);
  if (drag.kind === 'place') {
    const r = rectFromPoints(drag.start, p);
    ghost.value = { page: drag.page, ...r };
    return;
  }
  if (!drag.id || !drag.orig) return;
  const dx = p.x - drag.start.x;
  const dy = p.y - drag.start.y;
  const o = drag.orig;
  const next = drag.kind === 'move' ? clampFrac({ x: o.x + dx, y: o.y + dy, w: o.w, h: o.h }) : clampFrac({ x: o.x, y: o.y, w: o.w + dx, h: o.h + dy });
  const id = drag.id;
  local.value = local.value.map((f) => (f.id === id ? { ...f, ...next } : f));
}

function onWindowUp(ev: PointerEvent): void {
  if (!drag) return;
  const d = drag;
  drag = null;
  unbindWindow();
  const r = dragRect(d);
  // The pane may have changed size while the gesture held it still.
  if (sizeLater) {
    sizeLater = false;
    applyPaneSize();
  }
  // A seed that waited for the gesture is overtaken by what the gesture
  // publishes below; one that nothing will overtake runs now.
  const publishes = d.kind === 'place' || d.kind === 'move' || d.kind === 'resize';
  if (seedLater) {
    seedLater = false;
    if (!publishes) seed();
  }
  if (!r) {
    // No page to measure against (it was re-drawn under the gesture): keep
    // what the last good frame put down rather than inventing a place.
    ghost.value = null;
    if (d.kind === 'move' || d.kind === 'resize') publish();
    return;
  }
  if (d.kind === 'place' && placingStep.value && placingId.value) {
    const p = fracIn(ev, r);
    const box = local.value.find((f) => f.id === placingId.value);
    const dragged = Math.abs(p.x - d.start.x) > 0.01 || Math.abs(p.y - d.start.y) > 0.01;
    // ⚠ A tap is enough. The box already knows how big it wants to be (it
    // was defined with a size), so placing is one gesture, not a drawing
    // exercise; a drag is still honoured for somebody who wants a different
    // size straight away.
    const rect = dragged
      ? rectFromPoints(d.start, p)
      : { x: p.x - (box?.w ?? 0.2) / 2, y: p.y - (box?.h ?? 0.05) / 2, w: box?.w ?? 0.2, h: box?.h ?? 0.05 };
    placePending(d.page, rect);
    ghost.value = null;
    return;
  }
  if (d.kind === 'place' && placing.value) {
    const p = fracIn(ev, r);
    const dragged = Math.abs(p.x - d.start.x) > 0.01 || Math.abs(p.y - d.start.y) > 0.01;
    const type = placing.value;
    const f = dragged
      ? placeField(local.value, type, d.page, rectFromPoints(d.start, p), rectFromPoints(d.start, p), defaultAssignee())
      : placeField(local.value, type, d.page, { x: p.x - DEFAULT_FIELD_SIZE[type].w / 2, y: p.y - DEFAULT_FIELD_SIZE[type].h / 2 }, undefined, defaultAssignee());
    // Born with a name (the type's) rather than nameless: the placer
    // renames it if they want to, and a field nobody renamed still reads as
    // something in the signer's form.
    local.value = [...local.value, { ...f, label: typeLabel(type) }];
    selected.value = f.id;
    ghost.value = null;
    publish();
    return;
  }
  ghost.value = null;
  if (d.kind === 'move' || d.kind === 'resize') {
    // A tap on a placed box selects it and moves nothing: no round trip for
    // a list that did not change.
    const now = local.value.find((f) => f.id === d.id);
    const o = d.orig;
    if (!now || !o || now.x !== o.x || now.y !== o.y || now.w !== o.w || now.h !== o.h) publish();
  }
}

function defaultAssignee(): string | undefined {
  return signers.value.length === 1 ? signers.value[0].id : undefined;
}

/**
 * Reactive twin of `drag` for the template: the one-screen editor waits for
 * the gesture to end before it appears, so selecting a box by dragging it
 * does not push the document down under the pointer mid-drag.
 */
const dragging = ref(false);

function bindWindow(): void {
  dragging.value = true;
  window.addEventListener('pointermove', onWindowMove);
  window.addEventListener('pointerup', onWindowUp);
  window.addEventListener('pointercancel', onWindowUp);
}
function unbindWindow(): void {
  dragging.value = false;
  window.removeEventListener('pointermove', onWindowMove);
  window.removeEventListener('pointerup', onWindowUp);
  window.removeEventListener('pointercancel', onWindowUp);
}

const selectedField = computed(() => local.value.find((f) => f.id === selected.value) ?? null);

const copyTarget = ref(1);
function copy(): void {
  if (!selectedField.value) return;
  const before = local.value.length;
  local.value = copyToPage(local.value, selected.value, copyTarget.value);
  if (local.value.length === before) return;
  // The copy is the box you are now working on, and it is on another page —
  // which in a tall document is usually off the screen. Select it and go
  // there, or the button answers by appearing to do nothing.
  selected.value = local.value[local.value.length - 1].id;
  publish();
}

/**
 * Bring the selected box into view inside the document pane.
 *
 * ⚠ The pane scrolls, the page does not: `.fe-spdf__scroll` is the window
 * on to a column of pages, so "scroll to the box" is arithmetic on ITS
 * scrollTop, not `el.scrollIntoView()` (which would also scroll the app
 * frame, whose whole point in this layout is that it does not move).
 * Nothing happens mid-gesture: a drag selects the box it picks up, and a
 * pane that scrolled under the pointer would take the box with it.
 */
function revealSelected(): void {
  const pane = containerEl.value;
  const f = selectedField.value;
  if (!pane || !f || drag || !isPlaced(f)) return;
  const pageEl = pageEls.get(f.page);
  if (!pageEl) return;
  const page = pageEl.getBoundingClientRect();
  const view = pane.getBoundingClientRect();
  if (!page.height || !view.height) return;
  const top = page.top - view.top + pane.scrollTop + f.y * page.height;
  const bottom = top + f.h * page.height;
  const pad = 24;
  if (top < pane.scrollTop + pad) pane.scrollTop = Math.max(0, top - pad);
  else if (bottom > pane.scrollTop + pane.clientHeight - pad) pane.scrollTop = bottom - pane.clientHeight + pad;
}
watch(selected, () => void nextTick(revealSelected));

/* ── fill mode ────────────────────────────────────────────────────────── */

/** The field whose signature pad is open. */
const padFor = ref('');

function setValue(id: string, value: unknown): void {
  local.value = withValue(local.value, id, value);
  publish();
}

function onFieldTap(f: PdfField): void {
  if (editing.value || props.disabled || !mine(f)) return;
  switch (f.type) {
    case 'signature':
    case 'initials':
      padFor.value = padFor.value === f.id ? '' : f.id;
      break;
    case 'checkbox':
      setValue(f.id, f.value === true ? false : true);
      break;
    case 'date':
      setValue(f.id, hasValue(f) ? '' : todayIso());
      break;
    default:
      break;
  }
}

function onTextInput(f: PdfField, ev: Event): void {
  const el = ev.target as HTMLInputElement;
  // ⚠ Shaped as it is typed (digits only, the GG/AA/YYYY mask, …) and the
  // INPUT is written back, because a value the box refused has to leave the
  // box too — otherwise the person sees what they typed and the document
  // gets something else.
  const shaped = applyRule(el.value, ruleOf(f));
  if (shaped !== el.value) el.value = shaped;
  setValue(f.id, shaped);
}

const padField = computed(() => local.value.find((f) => f.id === padFor.value) ?? null);

/**
 * What the pad offers for this box: the requester chose drawn or typed, so
 * the signer gets exactly that — a drawn box draws (or takes a picture of a
 * signature), a typed one types, in the face that was chosen for it.
 */
const padModes = computed(() => (padField.value?.style === 'typed' ? ['type'] : ['draw', 'upload']));
const padFonts = computed(() =>
  padField.value?.style === 'typed' && padField.value.font ? [padField.value.font] : undefined,
);
const padValue = computed<SignatureValue | null>(() => {
  const v = padField.value?.value;
  return typeof v === 'string' && v ? { png_b64: v, mode: 'draw' } : null;
});

function onPadValue(v: SignatureValue | null): void {
  if (!padField.value) return;
  setValue(padField.value.id, v?.png_b64 ?? '');
}

function sigSrc(f: PdfField): string {
  return typeof f.value === 'string' && f.value ? `data:image/png;base64,${f.value}` : '';
}

/* ── rules and faces ─────────────────────────────────────── */

/** A field's rule, or `null` — only `text` carries one. */
function ruleOf(f: PdfField): PdfFieldRule | null {
  return f.type === 'text' ? (f.rule ?? null) : null;
}

/** The words under a text box: what it will accept. `''` when anything goes. */
function ruleHint(f: PdfField): string {
  const r = ruleOf(f);
  if (!r) return '';
  const parts: string[] = [];
  if (r.kind === 'number') parts.push(t('plugin.pdf.rule_number'));
  else if (r.kind === 'email') parts.push(t('plugin.pdf.rule_email'));
  if (r.min && r.max) parts.push(t('plugin.pdf.rule_length', { min: r.min, max: r.max }));
  else if (r.min) parts.push(t('plugin.pdf.rule_min', { min: r.min }));
  else if (r.max) parts.push(t('plugin.pdf.rule_max', { max: r.max }));
  return parts.join(' · ');
}

/** What is wrong with what is in the box right now. `''` when nothing is. */
function ruleMessage(f: PdfField): string {
  const r = ruleOf(f);
  const code = ruleError(f.value, r);
  if (!code || !r) return '';
  if (code === 'min') return t('plugin.pdf.err_min', { min: r.min ?? 0 });
  if (code === 'max') return t('plugin.pdf.err_max', { max: r.max ?? 0 });
  return t(`plugin.pdf.err_${code}`);
}

/** The face a field's words are drawn in — the field's, else the default. */
function fontClassOf(f: PdfField): string {
  return signFont(f.font).className;
}

const checkIcon = actionIconSvg('check');
</script>

<template>
  <div
    class="fe-spdf"
    data-testid="surface-pdf-fields"
    :class="{
      'is-edit': editing,
      'is-fill': !editing,
      'is-invalid': invalid,
      'is-disabled': disabled,
      'is-placing': !!placing || !!placingId,
      'is-define': defining,
      'fe-spdf--page': pageLayout && !defining,
    }"
    :style="pageLayout ? { '--spdf-h': pageHeight || '100%' } : undefined"
    :data-state="status"
    :data-layout="pageLayout ? 'page' : 'inline'"
  >
    <div v-if="!defining" class="fe-spdf__bar">
      <!-- ⚠ In `place` the toolbar is not a palette of TYPES but the boxes
           that were defined a step earlier and still have nowhere to go.
           Choosing one and tapping the page is the whole gesture. -->
      <div v-if="placingStep" class="fe-spdf__palette" role="toolbar" :aria-label="t('plugin.pdf.pending')">
        <button
          v-for="f in pending"
          :key="f.id"
          type="button"
          class="fe-btn fe-btn--sm"
          :class="{ 'fe-btn--primary': placingId === f.id }"
          :style="{ '--spdf-color': fieldColor(f) }"
          :disabled="disabled || status !== 'ok'"
          :aria-pressed="placingId === f.id ? 'true' : 'false'"
          :data-testid="`surface-pdf-pending-${f.id}`"
          @click="pickPending(f.id)"
        >
          {{ pendingLabel(f) }}
        </button>
        <span v-if="!pending.length" class="fe-spdf__done" data-testid="surface-pdf-all-placed">
          {{ t('plugin.pdf.all_placed') }}
        </span>
      </div>
      <div v-else-if="editing" class="fe-spdf__palette" role="toolbar" :aria-label="t('plugin.pdf.palette')">
        <button
          v-for="ty in palette"
          :key="ty"
          type="button"
          class="fe-btn fe-btn--sm"
          :class="{ 'fe-btn--primary': placing === ty }"
          :disabled="disabled || status !== 'ok'"
          :aria-pressed="placing === ty ? 'true' : 'false'"
          :data-testid="`surface-pdf-type-${ty}`"
          @click="pickType(ty)"
        >
          {{ typeLabel(ty) }}
        </button>
      </div>
      <div class="fe-spdf__zoom">
        <button type="button" class="fe-btn fe-btn--sm" :aria-label="t('plugin.pdf.zoom_out')" :disabled="status !== 'ok'" @click="zoomOut">−</button>
        <button type="button" class="fe-btn fe-btn--sm fe-spdf__zoomval" :aria-label="t(pageLayout ? 'plugin.pdf.zoom_fit_page' : 'plugin.pdf.zoom_fit')" :disabled="status !== 'ok'" @click="zoomFit">
          {{ Math.round(zoom * 100) }}%
        </button>
        <button type="button" class="fe-btn fe-btn--sm" :aria-label="t('plugin.pdf.zoom_in')" :disabled="status !== 'ok'" @click="zoomIn">+</button>
      </div>
    </div>
    <p v-if="editing && !defining && !placingStep && placing" class="fe-surface__text fe-surface__text--info fe-spdf__hint">{{ t('plugin.pdf.place_hint', { type: typeLabel(placing) }) }}</p>

    <!-- ⚠⚠ DEFINE: the boxes as cards, and no document anywhere on the
         screen. This step answers "what is being asked of whom"; where each
         box goes is the next one's question. -->
    <div v-if="defining" class="fe-spdf__define" data-testid="surface-pdf-define">
      <div v-if="!local.length" class="fe-spdf__empty" data-testid="surface-pdf-no-boxes">
        {{ t('plugin.pdf.no_boxes') }}
      </div>
      <div
        v-for="(f, i) in local"
        :key="f.id"
        class="fe-spdf__card"
        :style="cardStyle(f)"
        :data-testid="`surface-pdf-card-${f.id}`"
      >
        <span class="fe-spdf__cardno" aria-hidden="true">{{ i + 1 }}</span>
        <PdfFieldEditor
          :field="f"
          :signers="signers"
          :formats="formats"
          :date-labels="dateLabels"
          :stamp-lines="stampLines"
          :locale="locale"
          :disabled="disabled"
          :testid-prefix="`surface-pdf-card-${f.id}`"
          @patch="patchField"
          @remove="removeField(f.id)"
        />
      </div>
      <div class="fe-spdf__add" role="toolbar" :aria-label="t('plugin.pdf.add_box')">
        <span class="fe-spdf__addlbl">{{ t('plugin.pdf.add_box') }}</span>
        <button
          v-for="ty in palette"
          :key="ty"
          type="button"
          class="fe-btn fe-btn--sm"
          :disabled="disabled"
          :data-testid="`surface-pdf-add-${ty}`"
          @click="addField(ty)"
        >
          + {{ typeLabel(ty) }}
        </button>
      </div>
    </div>

    <!-- ⚠⚠ PLACE asks WHERE, and only where. The box was named a step ago;
         putting the naming controls back on this screen would be "both at
         once" again, which is the thing this split exists to end. What a
         selected box offers here is about its PLACE: take it off, put a
         copy on another page, or drop it. -->
    <!-- ⚠⚠ ONE strip of constant height in `place`, whatever it is saying.
         It used to be three things that came and went — a hint while a box
         was chosen, a property strip once one was selected, nothing in
         between — and each arrival took height from the document pane. In
         a `page` layout the page is fitted to that pane, so every tap
         re-fitted (and redrew) the whole document: measured 2026-09-21, the
         page shrank from 383px to 349px wide the moment the first box was
         selected, in the middle of the gesture that selected it. -->
    <div v-else-if="placingStep" class="fe-spdf__strip" data-testid="surface-pdf-strip">
      <div v-if="selectedField" class="fe-spdf__props" data-testid="surface-pdf-selected">
        <span class="fe-spdf__propname" :style="{ '--spdf-color': fieldColor(selectedField) }">
          {{ fieldLabel(selectedField) }}
        </span>
        <span v-if="signerLabel(selectedField.assignee)" class="fe-spdf__propwho">
          {{ signerLabel(selectedField.assignee) }}
        </span>
        <button type="button" class="fe-btn fe-btn--sm" data-testid="surface-pdf-unplace" @click="unplace">
          {{ t('plugin.pdf.unplace') }}
        </button>
        <!-- ⚠ A number, not a list. The rule is "no dropdown in a surface",
             and a 200-page contract would answer it with 200 buttons — the
             same complaint (a control you have to hunt through) wearing the
             fix's clothes. -->
        <label v-if="pages.length > 1" class="fe-spdf__prop">
          <span>{{ t('plugin.pdf.copy_to_page') }}</span>
          <input
            v-model.number="copyTarget"
            class="fe-cfield__input fe-spdf__num"
            type="number"
            min="1"
            :max="pages.length"
            data-testid="surface-pdf-copy-page"
          />
          <button type="button" class="fe-btn fe-btn--sm" data-testid="surface-pdf-copy" @click="copy">{{ t('plugin.pdf.copy') }}</button>
        </label>
        <button
          type="button"
          class="fe-btn fe-btn--sm fe-btn--danger"
          data-testid="surface-pdf-delete"
          @click="removeField(selectedField.id)"
        >
          {{ t('plugin.pdf.delete') }}
        </button>
      </div>
      <p
        v-else-if="placingId"
        class="fe-surface__text fe-surface__text--info fe-spdf__hint"
        data-testid="surface-pdf-place-hint"
      >
        {{ t('plugin.pdf.place_box_hint', { name: pendingLabel(local.find((f) => f.id === placingId) ?? local[0]) }) }}
      </p>
      <p v-else class="fe-surface__text fe-surface__text--muted fe-spdf__hint" data-testid="surface-pdf-idle-hint">
        {{ t(pending.length ? 'plugin.pdf.place_idle' : 'plugin.pdf.place_idle_done') }}
      </p>
    </div>

    <!-- The one-screen `edit` mode keeps the whole editor for whichever box
         is selected: a plugin that asks for both jobs at once gets both.
         ⚠ Not while a box is being dragged: the editor arriving above the
         document on pointer-down pushed the page down under the pointer. -->
    <div v-else-if="editing && selectedField && !dragging" data-testid="surface-pdf-selected">
      <PdfFieldEditor
        :field="selectedField"
        :signers="signers"
        :formats="formats"
        :date-labels="dateLabels"
        :stamp-lines="stampLines"
        :locale="locale"
        :disabled="disabled"
        testid-prefix="surface-pdf"
        @patch="patchField"
        @remove="removeField(selectedField.id)"
      >
        <template #extra>
          <label v-if="pages.length > 1" class="fe-spdf__prop">
            <span>{{ t('plugin.pdf.copy_to_page') }}</span>
            <input
              v-model.number="copyTarget"
              class="fe-cfield__input fe-spdf__num"
              type="number"
              min="1"
              :max="pages.length"
              data-testid="surface-pdf-copy-page"
            />
            <button type="button" class="fe-btn fe-btn--sm" data-testid="surface-pdf-copy" @click="copy">{{ t('plugin.pdf.copy') }}</button>
          </label>
        </template>
      </PdfFieldEditor>
    </div>

    <div v-if="!defining" ref="containerEl" class="fe-spdf__scroll">
      <p v-if="status === 'loading'" class="fe-surface__text fe-surface__text--muted">{{ t('plugin.view.loading') }}</p>
      <p v-else-if="status === 'error'" class="fe-surface__unknown" role="alert" data-testid="surface-pdf-error">{{ t('plugin.pdf.load_failed') }}</p>
      <div v-else class="fe-spdf__pages">
        <div
          v-for="p in pages"
          :key="p.num"
          :ref="(el) => setPageEl(p.num, el)"
          class="fe-spdf__page"
          :data-page="p.num"
          :style="{ width: `${pageWidth}px`, aspectRatio: `${p.width} / ${p.height}` }"
          @pointerdown="onPageDown($event, p.num)"
        >
          <div
            v-for="f in byPage.get(p.num) ?? []"
            :key="f.id"
            class="fe-spdf__field"
            :class="[
              `fe-spdf__field--${f.type}`,
              {
                'is-selected': editing && selected === f.id,
                'is-mine': mine(f),
                'is-foreign': !editing && !mine(f),
                'is-filled': hasValue(f),
                'is-required': f.required === true,
              },
            ]"
            :style="{ left: `${f.x * 100}%`, top: `${f.y * 100}%`, width: `${f.w * 100}%`, height: `${f.h * 100}%`, '--spdf-color': fieldColor(f) }"
            :data-testid="`surface-pdf-field-${f.id}`"
            :data-field-type="f.type"
            :role="mine(f) ? 'button' : undefined"
            :tabindex="mine(f) ? 0 : undefined"
            :title="editing || !mine(f) ? signerLabel(f.assignee) : undefined"
            @pointerdown="onFieldDown($event, f, p.num)"
            @click="onFieldTap(f)"
            @keydown.enter.prevent="onFieldTap(f)"
          >
            <img v-if="(f.type === 'signature' || f.type === 'initials') && sigSrc(f)" class="fe-spdf__sig" :src="sigSrc(f)" alt="" />
            <input
              v-else-if="f.type === 'text' && mine(f)"
              class="fe-spdf__text"
              :class="fontClassOf(f)"
              type="text"
              :value="typeof f.value === 'string' ? f.value : ''"
              :placeholder="fieldLabel(f)"
              :aria-label="fieldLabel(f)"
              :inputmode="ruleInputMode(ruleOf(f))"
              :maxlength="ruleMaxLength(ruleOf(f))"
              :aria-invalid="ruleMessage(f) ? 'true' : undefined"
              :aria-describedby="ruleHint(f) ? `${id}-${f.id}-rule` : undefined"
              :title="ruleHint(f) || undefined"
              :disabled="disabled"
              :data-testid="`surface-pdf-text-${f.id}`"
              @pointerdown.stop
              @click.stop
              @input="onTextInput(f, $event)"
            />
            <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
            <span v-else-if="f.type === 'checkbox' && f.value === true" class="fe-spdf__check" aria-hidden="true" v-html="checkIcon"></span>
            <span
              v-else-if="f.type === 'date' && typeof f.value === 'string' && f.value"
              class="fe-spdf__val"
              :class="fontClassOf(f)"
            >{{ f.value }}</span>
<!-- ⚠ The NAME, then what to do about it. A box called "Yetkili
                 imzası" says more than a third grey rectangle reading
                 "Signature", and the person filling it is looking for the
                 name they were told, not for a type. -->
            <span v-else class="fe-spdf__label">
              {{ fieldLabel(f) }}
              <small v-if="mine(f)" class="fe-spdf__who">{{ t(`plugin.pdf.tap_${f.type}`) }}</small>
              <small v-else-if="f.assignee && (editing || !mine(f))" class="fe-spdf__who">{{ signerLabel(f.assignee) }}</small>
            </span>
            <!-- ⚠ The rule's words sit OUTSIDE the box, not inside it: a
                 field is a few millimetres of a page, and a hint drawn in it
                 would cover the value it is about. -->
            <span
              v-if="!editing && mine(f) && f.type === 'text' && (ruleMessage(f) || ruleHint(f))"
              :id="`${id}-${f.id}-rule`"
              class="fe-spdf__rule"
              :class="{ 'is-bad': !!ruleMessage(f) }"
              :role="ruleMessage(f) ? 'alert' : undefined"
              :data-testid="`surface-pdf-rule-${f.id}`"
            >{{ ruleMessage(f) || ruleHint(f) }}</span>
            <span v-if="editing" class="fe-spdf__handle" data-testid="surface-pdf-handle" @pointerdown="onHandleDown($event, f, p.num)"></span>
          </div>
          <div
            v-if="ghost && ghost.page === p.num"
            class="fe-spdf__field fe-spdf__field--ghost"
            :style="{ left: `${ghost.x * 100}%`, top: `${ghost.y * 100}%`, width: `${ghost.w * 100}%`, height: `${ghost.h * 100}%` }"
          ></div>
        </div>
      </div>
    </div>

    <div v-if="!editing && padField" class="fe-spdf__pad" data-testid="surface-pdf-pad">
      <p class="fe-surface__text fe-surface__text--muted">{{ t('plugin.pdf.pad_for', { type: fieldLabel(padField) }) }}</p>
      <SurfaceSignaturePad
        :id="`${id}-${padField.id}`"
        :model-value="padValue"
        :modes="padModes"
        :font="padField.font"
        :fonts="padFonts"
        :locale="locale"
        :disabled="disabled"
        :width="padField.type === 'initials' ? 240 : undefined"
        :height="padField.type === 'initials' ? 120 : undefined"
        @update:model-value="onPadValue"
      />
      <button type="button" class="fe-btn fe-btn--sm" data-testid="surface-pdf-pad-done" @click="padFor = ''">{{ t('plugin.pdf.pad_done') }}</button>
    </div>
  </div>
</template>
