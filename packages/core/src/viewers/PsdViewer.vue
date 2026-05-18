<script setup lang="ts">
/**
 * PsdViewer — Photoshop document preview via `ag-psd`.
 *
 * Lazy-imports `ag-psd` (~150 KB). Renders the flattened composite to
 * a canvas via `psd.canvas`. A simple layer panel toggles the rendered
 * preview (V1: just lists names + visibility; full composite swap on
 * toggle requires re-rendering with `applyOpacity`/`composite` logic
 * — left as TODO for V2 since `ag-psd` doesn't ship a composite
 * pipeline of its own).
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { fetchViewerArrayBuffer } from '../composables/useViewerFetch';

const props = defineProps<{
  url: string;
  mime?: string;
  ext: string;
  t?: (key: string) => string;
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
}>();

interface FlatLayer {
  index: number;
  name: string;
  hidden: boolean;
}

const canvasEl = ref<HTMLCanvasElement | null>(null);
const layers = ref<FlatLayer[]>([]);
const error = ref<string | null>(null);
const loading = ref(true);
const showLayerPanel = ref(false);
const dims = ref<{ width: number; height: number }>({ width: 0, height: 0 });
const scale = ref(1);

let renderToken = 0;

let agPsd: any = null;
async function ensureAgPsd(): Promise<any | null> {
  if (agPsd) return agPsd;
  try {
    const mod = await import(/* @vite-ignore */ 'ag-psd');
    agPsd = mod;
    return agPsd;
  } catch {
    return null;
  }
}

function flattenLayers(node: any, list: FlatLayer[]): void {
  if (!node) return;
  if (Array.isArray(node.children)) {
    for (const child of node.children) {
      list.push({
        index: list.length,
        name: child.name ?? '(unnamed)',
        hidden: !!child.hidden,
      });
      if (child.children) flattenLayers(child, list);
    }
  }
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  layers.value = [];
  const myToken = ++renderToken;

  const lib = await ensureAgPsd();
  if (myToken !== renderToken) return;
  if (!lib) {
    error.value = props.t
      ? props.t('viewer.peer_not_installed')
      : 'PSD viewer requires `ag-psd` — install or use download.';
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
    const psd = lib.readPsd(buf, {
      skipLayerImageData: false,
      skipCompositeImageData: false,
      skipThumbnail: true,
    });
    dims.value = { width: psd.width, height: psd.height };

    const composite = psd.canvas;
    if (composite && canvasEl.value) {
      const target = canvasEl.value;
      target.width = psd.width;
      target.height = psd.height;
      const ctx = target.getContext('2d');
      ctx?.drawImage(composite, 0, 0);
    } else {
      throw new Error('PSD has no composite image (skipCompositeImageData?)');
    }

    const flat: FlatLayer[] = [];
    flattenLayers(psd, flat);
    layers.value = flat;
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'PSD decode failed';
  } finally {
    loading.value = false;
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
});
watch(() => props.url, load);

function tt(key: string, fallback: string): string {
  return props.t ? props.t(key) : fallback;
}
</script>

<template>
  <div class="filex-viewer-psd">
    <div class="filex-viewer-psd__pane">
      <div v-if="error" class="filex-viewer-fallback">
        <span class="filex-viewer-fallback__icon">🎨</span>
        <p>{{ error }}</p>
      </div>
      <div v-else-if="loading" class="filex-viewer-fallback">
        <span class="filex-viewer-fallback__icon">⏳</span>
        <p>{{ tt('viewer.loading', 'Loading…') }}</p>
      </div>
      <canvas
        v-show="!loading && !error"
        ref="canvasEl"
      />
    </div>
  </div>
</template>

<style scoped>
.filex-viewer-psd {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 70vh;
  background: var(--fe-bg, #fff);
}
.filex-viewer-psd__bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  font-size: 13px;
}
.filex-viewer-spacer { flex: 1; }
.filex-viewer-psd__dim {
  font-size: 12px;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-psd__zoom {
  min-width: 50px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  font-size: 12px;
}
.filex-viewer-psd__main {
  flex: 1;
  display: flex;
  min-height: 0;
}
.filex-viewer-psd__layers {
  width: 240px;
  border-right: 1px solid var(--fe-border, #e2e6ed);
  overflow-y: auto;
  padding: 8px 0;
  background: var(--fe-bg-elev, #f7f8fa);
}
.filex-viewer-psd__layers ul {
  list-style: none;
  margin: 0;
  padding: 0;
}
.filex-viewer-psd__layers li {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 12px;
  font-size: 12px;
}
.filex-viewer-psd__layer-vis {
  width: 14px;
  text-align: center;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-psd__layer-vis[data-hidden="1"] { color: var(--fe-border-strong, #c7ced9); }
.filex-viewer-psd__layer-name {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.filex-viewer-psd__pane {
  flex: 1;
  overflow: auto;
  padding: 16px;
  background: #2a2d33;
  display: flex;
  align-items: flex-start;
  justify-content: flex-start;
}
.filex-viewer-psd__pane canvas {
  background: #fff
    repeating-conic-gradient(#e0e0e0 0% 25%, transparent 0% 50%) 50% / 16px 16px;
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
.filex-viewer-btn.is-active {
  background: var(--fe-bg-selected, #dfe8ff);
  border-color: var(--fe-primary, #3b82f6);
}
</style>
