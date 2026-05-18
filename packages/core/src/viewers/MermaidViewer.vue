<script setup lang="ts">
/**
 * MermaidViewer — render a `.mmd` / `.mermaid` source file.
 *
 * Lazy-imports the `mermaid` library (~150 KB gzipped). Source text is
 * fetched from `props.url` (with the auth headers from PreviewModal),
 * then rendered to SVG via `mermaid.render('id', src)`. The resulting
 * SVG node is inserted into a wrapper that supports pan + scale via
 * pure CSS transforms (no extra panzoom dep).
 */
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { fetchViewerText } from '../composables/useViewerFetch';

const props = defineProps<{
  url: string;
  mime?: string;
  ext: string;
  t?: (key: string) => string;
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
}>();

const surface = ref<HTMLDivElement | null>(null);
const error = ref<string | null>(null);
const loading = ref(true);
const scale = ref(1);
const tx = ref(0);
const ty = ref(0);

let dragging = false;
let dragStart: { x: number; y: number; tx: number; ty: number } | null = null;
let renderToken = 0;

let cachedMermaid: any = null;
async function ensureMermaid(): Promise<any | null> {
  if (cachedMermaid) return cachedMermaid;
  try {
    const mod = await import(/* @vite-ignore */ 'mermaid');
    cachedMermaid = mod.default ?? mod;
    cachedMermaid.initialize?.({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: window.matchMedia?.('(prefers-color-scheme: dark)').matches
        ? 'dark'
        : 'default',
    });
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
    error.value = props.t
      ? props.t('viewer.peer_not_installed')
      : 'Mermaid viewer requires `mermaid` — install or use download.';
    loading.value = false;
    return;
  }

  let src: string;
  try {
    src = await fetchViewerText({
      url: props.url,
      headers: props.authHeaders?.() ?? {},
      credentials: props.authCredentials,
    });
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'fetch failed';
    loading.value = false;
    return;
  }

  if (myToken !== renderToken) return;

  try {
    const id = `filex-mermaid-${Date.now()}`;
    const { svg } = await mermaid.render(id, src);
    if (myToken !== renderToken) return;
    if (surface.value) {
      surface.value.innerHTML = svg;
      const svgEl = surface.value.querySelector('svg');
      if (svgEl) {
        svgEl.style.maxWidth = '100%';
        svgEl.style.height = 'auto';
      }
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'render failed';
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
  dragStart = { x: ev.clientX, y: ev.clientY, tx: tx.value, ty: ty.value };
  (ev.target as HTMLElement).setPointerCapture?.(ev.pointerId);
}
function onPointerMove(ev: PointerEvent): void {
  if (!dragging || !dragStart) return;
  tx.value = dragStart.tx + (ev.clientX - dragStart.x);
  ty.value = dragStart.ty + (ev.clientY - dragStart.y);
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
        <span class="filex-viewer-fallback__icon">📊</span>
        <p>{{ error }}</p>
      </div>
      <div
        v-else-if="loading"
        class="filex-viewer-fallback"
      >
        <span class="filex-viewer-fallback__icon">⏳</span>
        <p>{{ tt('viewer.loading', 'Loading…') }}</p>
      </div>
      <div
        v-show="!loading && !error"
        ref="surface"
        class="filex-viewer-mermaid__surface"
        :style="{ transform: `translate(${tx}px, ${ty}px) scale(${scale})` }"
      />
    </div>
  </div>
</template>

<style scoped>
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
.filex-viewer-mermaid__surface :deep(svg) {
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
