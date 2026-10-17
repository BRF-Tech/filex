<script setup lang="ts">
import { sanitizeHtml } from '../lib/sanitizeHtml';
import { sayFailure } from '../lib/errorWords';
/**
 * MermaidViewer — render a `.mmd` / `.mermaid` source file.
 *
 * Lazy-imports the `mermaid` library (~150 KB gzipped). Source text is
 * fetched from `props.url` (with the auth headers from PreviewModal),
 * then drawn by lib/mermaidRender - the one way a diagram is drawn, which
 * asks no other server for anything while Mermaid lays it out - and its SVG
 * goes through the diagram sanitizer, a picture from elsewhere held behind a
 * placeholder (composables/useRemoteImages: inside the preview its gate
 * asks, on its own this viewer asks itself). The SVG is inserted into a
 * wrapper that supports pan + scale via pure CSS transforms (no extra
 * panzoom dep).
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { renderMermaid, type MermaidApi } from '../lib/mermaidRender';
import { injectRemoteImages, useRemoteImages } from '../composables/useRemoteImages';
import RemoteImageDialog from '../modals/RemoteImageDialog.vue';
import { actionIconSvg } from '../lib/actionIcons'; /* ikon:emoji */
import { fileIconTile } from '../lib/fileIcons'; /* ikon:emoji */
import { fetchViewerText } from '../composables/useViewerFetch';

const props = defineProps<{
  url: string;
  mime?: string;
  ext: string;
  t?: (key: string) => string;
  /** The reader's language, for the question a held picture asks on its own. */
  locale?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  authCredentials?: RequestCredentials;
}>();

const surface = ref<HTMLDivElement | null>(null);
const parentGate = injectRemoteImages();
const remoteImages = parentGate ?? useRemoteImages(() => props.locale);
const ownGate = !parentGate;
remoteImages.watchRoot(surface);
const error = ref<string | null>(null);
const loading = ref(true);
const scale = ref(1);
const tx = ref(0);
const ty = ref(0);

let dragging = false;
let dragStart: { x: number; y: number; tx: number; ty: number } | null = null;
let renderToken = 0;

let cachedMermaid: MermaidApi | null = null;
async function ensureMermaid(): Promise<MermaidApi | null> {
  if (cachedMermaid) return cachedMermaid;
  try {
    const mod = (await import(/* @vite-ignore */ 'mermaid')) as { default?: MermaidApi } & MermaidApi;
    cachedMermaid = mod.default ?? mod;
    return cachedMermaid;
  } catch {
    return null;
  }
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  const myToken = ++renderToken;

  const mermaid = await ensureMermaid();
  if (myToken !== renderToken) return;
  if (!mermaid) {
    error.value = tt('viewer.peer_not_installed', 'This kind of file cannot be shown here. Download it to open it on your device.');
    loading.value = false;
    return;
  }

  let src: string;
  try {
    src = await fetchViewerText({
      url: props.url,
      headers: (await props.authHeaders?.()) ?? {},
      credentials: props.authCredentials,
    });
  } catch (err) {
    error.value = sayFailure(err, tt('viewer.failed_to_load', 'Failed to load file'), { t: props.t }).text;
    loading.value = false;
    return;
  }

  if (myToken !== renderToken) return;

  try {
    const id = `filex-mermaid-${Date.now()}`;
    const theme = window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'default';
    const svg = await renderMermaid(mermaid, id, src, remoteImages.policy(), theme);
    if (myToken !== renderToken) return;
    if (surface.value) {
      surface.value.innerHTML = sanitizeHtml(svg, 'diagram', { remoteImages: remoteImages.policy() });
      const svgEl = surface.value.querySelector('svg');
      if (svgEl) {
        svgEl.style.maxWidth = '100%';
        svgEl.style.height = 'auto';
      }
    }
  } catch (err) {
    error.value = sayFailure(err, tt('viewer.failed_to_load', 'Failed to load file'), { t: props.t }).text;
  } finally {
    loading.value = false;
  }
}

function zoomIn(): void {
  scale.value = Math.min(8, scale.value * 1.25);
}
function zoomOut(): void {
  scale.value = Math.max(0.2, scale.value / 1.25);
}
function reset(): void {
  scale.value = 1;
  tx.value = 0;
  ty.value = 0;
}

function onWheel(ev: WheelEvent): void {
  if (!ev.ctrlKey && !ev.metaKey) return;
  ev.preventDefault();
  const delta = -ev.deltaY;
  const factor = delta > 0 ? 1.1 : 1 / 1.1;
  scale.value = Math.max(0.2, Math.min(8, scale.value * factor));
}

function onPointerDown(ev: PointerEvent): void {
  dragging = true;
  dragged = false;
  dragStart = { x: ev.clientX, y: ev.clientY, tx: tx.value, ty: ty.value };
  (ev.target as HTMLElement).setPointerCapture?.(ev.pointerId);
}
function onPointerMove(ev: PointerEvent): void {
  if (!dragging || !dragStart) return;
  if (Math.abs(ev.clientX - dragStart.x) + Math.abs(ev.clientY - dragStart.y) > 4) dragged = true;
  tx.value = dragStart.tx + (ev.clientX - dragStart.x);
  ty.value = dragStart.ty + (ev.clientY - dragStart.y);
}
/** A click on a held picture asks - unless it ended a pan of the diagram. */
let dragged = false;
function onSurfaceClick(ev: MouseEvent): void {
  if (dragged) {
    dragged = false;
    return;
  }
  remoteImages.onClick(ev);
}
function onPointerUp(): void {
  dragging = false;
  dragStart = null;
}

onMounted(load);
onBeforeUnmount(() => {
  renderToken++;
});

function tt(key: string, fallback: string): string {
  return props.t ? props.t(key) : fallback;
}

/* === ikon:emoji — the fallback screen's mark ==========================
 * Every viewer opened its "cannot show this" / "still loading" screen with a
 * 48px colour emoji, one per format, each from whatever emoji font the OS
 * shipped. The format mark is `lib/fileIcons`'s tile — the SAME tile the row
 * the person just clicked is wearing, so the fallback is recognisably about
 * that file — and "loading" is the stroked ring, spun by CSS, because no
 * still picture can say "still going". */
const typeTile = computed(() => fileIconTile({ type: 'file', extension: props.ext }));
</script>

<template>
  <div class="filex-viewer-mermaid">
    <div
      class="filex-viewer-mermaid__pane"
      @wheel="onWheel"
      @pointerdown="onPointerDown"
      @pointermove="onPointerMove"
      @pointerup="onPointerUp"
      @pointercancel="onPointerUp"
    >
      <div
        v-if="error"
        class="filex-viewer-fallback"
      >
        <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons + lib/actionIcons -->
      <span class="filex-viewer-fallback__icon" aria-hidden="true" v-html="typeTile"></span>
        <p>{{ error }}</p>
      </div>
      <div
        v-else-if="loading"
        class="filex-viewer-fallback"
      >
        <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons + lib/actionIcons -->
      <span
        class="filex-viewer-fallback__icon filex-viewer-fallback__icon--spin"
        aria-hidden="true"
        v-html="actionIconSvg('progress')"
      ></span>
        <p>{{ tt('viewer.loading', 'Loading…') }}</p>
      </div>
      <div
        v-show="!loading && !error"
        ref="surface"
        class="filex-viewer-mermaid__surface"
        :style="{ transform: `translate(${tx}px, ${ty}px) scale(${scale})` }"
        @click="onSurfaceClick"
        @keydown="remoteImages.onKeydown"
      />
    </div>
    <RemoteImageDialog v-if="ownGate" :gate="remoteImages" :locale="locale" />
  </div>
</template>

<style>
/* ⚠ NOT `scoped`, deliberately. Vue's scoped styles compile to
   `.cls[data-v-HASH]`, and in the web-component build the hash baked into
   this CSS does not match the one Vue stamps onto the DOM — so every rule
   here silently stopped applying. Measured in the desktop app: the share
   dialog had `position: static`, no background and no radius, i.e. raw
   unstyled HTML, in EVERY embedded surface.
   Safe to drop: every selector below is prefixed (fx-/fe-/filex-), so
   there is nothing here that can leak into a host page. */
.filex-viewer-mermaid {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 70vh;
  background: var(--fe-bg, #fff);
  color: var(--fe-text, #1a1e27);
}
.filex-viewer-mermaid__bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  font-size: 13px;
}
.filex-viewer-mermaid__zoom {
  min-width: 50px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  font-size: 12px;
}
.filex-viewer-mermaid__pane {
  flex: 1;
  overflow: hidden;
  position: relative;
  cursor: grab;
  background: var(--fe-bg-elev, #f7f8fa);
  touch-action: none;
}
.filex-viewer-mermaid__pane:active {
  cursor: grabbing;
}
.filex-viewer-mermaid__surface {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  transform-origin: center center;
  transition: transform 0.05s linear;
  user-select: none;
}
.filex-viewer-mermaid__surface svg {
  max-width: 90%;
  max-height: 90%;
}
.filex-viewer-fallback {
  text-align: center;
  padding: 32px;
  color: var(--fe-text-muted, #5a6475);
  width: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}
.filex-viewer-fallback__icon {
  font-size: 48px;
  display: block;
  margin-bottom: 12px;
}
.filex-viewer-btn {
  border: 1px solid var(--fe-border, #e2e6ed);
  background: var(--fe-bg, #fff);
  color: var(--fe-text, #1a1e27);
  padding: 4px 10px;
  border-radius: 4px;
  cursor: pointer;
  font: inherit;
  font-size: 12px;
}
.filex-viewer-btn:hover:not(:disabled) {
  background: var(--fe-bg-hover, #edf0f5);
}
</style>
