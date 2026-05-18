<script setup lang="ts">
/**
 * TiffViewer — multi-page TIFF preview via `utif`.
 *
 * Lazy-imports `utif` (~50 KB). Decodes IFDs once, paints the active
 * page to a `<canvas>` via `UTIF.toRGBA8`. Page navigation (1/N) +
 * zoom in/out controls. Browser <img> can't render TIFF natively, so
 * the canvas pipeline is the only option short of server-side
 * conversion.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { fetchViewerArrayBuffer } from '../composables/useViewerFetch';

const props = defineProps<{
  url: string;
  mime?: string;
  ext: string;
  t?: (key: string) => string;
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
}>();

const canvasEl = ref<HTMLCanvasElement | null>(null);
const error = ref<string | null>(null);
const loading = ref(true);
const scale = ref(1);
const pageIndex = ref(0);
const pageCount = ref(0);
const pageDims = ref<{ width: number; height: number }>({ width: 0, height: 0 });

let UTIF: any = null;
let ifds: any[] | null = null;
let renderToken = 0;

async function ensureUtif(): Promise<any | null> {
  if (UTIF) return UTIF;
  try {
    const mod = await import(/* @vite-ignore */ 'utif');
    UTIF = mod.default ?? mod;
    return UTIF;
  } catch {
    return null;
  }
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  ifds = null;
  pageIndex.value = 0;
  pageCount.value = 0;
  const myToken = ++renderToken;

  const lib = await ensureUtif();
  if (myToken !== renderToken) return;
  if (!lib) {
    error.value = props.t
      ? props.t('viewer.peer_not_installed')
      : 'TIFF viewer requires `utif` — install or use download.';
    loading.value = false;
    return;
  }

  try {
    const buf = await fetchViewerArrayBuffer({
      url: props.url,
      headers: props.authHeaders?.() ?? {},
      credentials: props.authCredentials,
    });
    if (myToken !== renderToken) return;
    ifds = lib.decode(buf);
    pageCount.value = ifds?.length ?? 0;
    if (pageCount.value === 0) {
      throw new Error('No pages decoded');
    }
    paint();
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'TIFF decode failed';
  } finally {
    loading.value = false;
  }
}

function paint(): void {
  if (!UTIF || !ifds || !canvasEl.value) return;
  const idx = Math.max(0, Math.min(pageIndex.value, ifds.length - 1));
  const ifd = ifds[idx];
  try {
    UTIF.decodeImage(undefined, ifd);
  } catch {
    // some images already decoded — UTIF.decodeImage is idempotent on
    // older versions.
  }
  const rgba = UTIF.toRGBA8(ifd);
  const w = ifd.width;
  const h = ifd.height;
  pageDims.value = { width: w, height: h };
  const canvas = canvasEl.value;
  canvas.width = w;
  canvas.height = h;
  const ctx = canvas.getContext('2d');
  if (!ctx) return;
  const imageData = ctx.createImageData(w, h);
  imageData.data.set(rgba);
  ctx.putImageData(imageData, 0, 0);
}

function next(): void {
  if (pageIndex.value < pageCount.value - 1) {
    pageIndex.value++;
    paint();
  }
}
function prev(): void {
  if (pageIndex.value > 0) {
    pageIndex.value--;
    paint();
  }
}
function zoomIn(): void {
  scale.value = Math.min(8, scale.value * 1.25);
}
function zoomOut(): void {
  scale.value = Math.max(0.1, scale.value / 1.25);
}
function reset(): void {
  scale.value = 1;
}

onMounted(load);
onBeforeUnmount(() => {
  renderToken++;
  ifds = null;
});

watch(() => props.url, load);

const pageLabel = computed(() => {
  const m = (props.t && props.t('viewer.page_n_of_m')) || 'Page {n} of {m}';
  return m.replace('{n}', String(pageIndex.value + 1)).replace('{m}', String(pageCount.value));
});

function tt(key: string, fallback: string): string {
  return props.t ? props.t(key) : fallback;
}
</script>

<template>
  <div class="filex-viewer-tiff">
    <div class="filex-viewer-tiff__pane">
      <div v-if="error" class="filex-viewer-fallback">
        <span class="filex-viewer-fallback__icon">🖼️</span>
        <p>{{ error }}</p>
      </div>
      <div v-else-if="loading" class="filex-viewer-fallback">
        <span class="filex-viewer-fallback__icon">⏳</span>
        <p>{{ tt('viewer.loading', 'Loading…') }}</p>
      </div>
      <canvas
        v-show="!loading && !error"
        ref="canvasEl"
        :style="{ imageRendering: 'pixelated' }"
      />
      <div v-if="!loading && !error && pageCount > 1" class="filex-viewer-tiff__pager">
        <button
          type="button"
          class="filex-viewer-btn"
          :disabled="pageIndex === 0"
          @click="prev"
        >‹</button>
        <span class="filex-viewer-tiff__pages">{{ pageLabel }}</span>
        <button
          type="button"
          class="filex-viewer-btn"
          :disabled="pageIndex >= pageCount - 1"
          @click="next"
        >›</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.filex-viewer-tiff__pager {
  position: absolute;
  bottom: 12px;
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 8px;
  background: var(--fe-bg-elev, rgba(255, 255, 255, 0.9));
  border: 1px solid var(--fe-border, #e2e6ed);
  border-radius: 6px;
  backdrop-filter: blur(4px);
}
.filex-viewer-tiff__pages {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.filex-viewer-tiff {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 70vh;
  background: var(--fe-bg, #fff);
}
.filex-viewer-tiff__bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  font-size: 13px;
}
.filex-viewer-tiff__pages {
  min-width: 110px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  font-size: 12px;
}
.filex-viewer-tiff__zoom {
  min-width: 50px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  font-size: 12px;
}
.filex-viewer-spacer { flex: 1; }
.filex-viewer-tiff__pane {
  flex: 1;
  overflow: auto;
  padding: 16px;
  background: #2a2d33;
  display: flex;
  align-items: flex-start;
  justify-content: flex-start;
}
.filex-viewer-tiff__pane canvas {
  background: #fff;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.3);
}
.filex-viewer-fallback {
  text-align: center;
  padding: 32px;
  color: #c8cdd6;
  margin: auto;
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
.filex-viewer-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
</style>
