<script setup lang="ts">
/**
 * Viewer3D — 3D model preview via `@google/model-viewer`.
 *
 * Lazy-imports the `@google/model-viewer` web component (~80 KB). When
 * the module is missing we render a download fallback instead of a
 * broken surface. Format support is bounded by what model-viewer
 * understands (glb, gltf, USDZ on iOS) — the rest of the listed
 * extensions (obj, stl, fbx, 3ds) fall back gracefully when the
 * format isn't accepted.
 */
import { onMounted, ref, watch } from 'vue';

const props = defineProps<{
  url: string;
  mime?: string;
  ext: string;
  /** Locale-aware error/loading messages. */
  t?: (key: string) => string;
}>();

const error = ref<string | null>(null);
const ready = ref(false);

async function load(): Promise<void> {
  ready.value = false;
  error.value = null;
  try {
    await import(/* @vite-ignore */ '@google/model-viewer');
    ready.value = true;
  } catch {
    error.value = props.t
      ? props.t('viewer.peer_not_installed')
      : '3D viewer requires `@google/model-viewer` — install or use download.';
  }
}

onMounted(load);

watch(() => props.url, () => {
  if (ready.value) return;
  load();
});
</script>

<template>
  <div class="filex-viewer-3d">
    <model-viewer
      v-if="ready && !error"
      :src="url"
      auto-rotate
      camera-controls
      touch-action="pan-y"
      shadow-intensity="1"
      :alt="ext + ' model'"
    />
    <div v-else-if="error" class="filex-viewer-fallback">
      <span class="filex-viewer-fallback__icon">📦</span>
      <p>{{ error }}</p>
    </div>
    <div v-else class="filex-viewer-fallback">
      <span class="filex-viewer-fallback__icon">⏳</span>
      <p>{{ t ? t('viewer.loading') : 'Loading…' }}</p>
    </div>
  </div>
</template>

<style scoped>
.filex-viewer-3d {
  width: 100%;
  height: 100%;
  min-height: 480px;
  background: #1a1a1a;
  display: flex;
  align-items: center;
  justify-content: center;
}
.filex-viewer-3d :deep(model-viewer) {
  width: 100%;
  height: 100%;
  min-height: 480px;
  background: #1a1a1a;
  display: block;
}
.filex-viewer-fallback {
  text-align: center;
  color: #c8cdd6;
  padding: 32px;
  max-width: 480px;
}
.filex-viewer-fallback__icon {
  font-size: 48px;
  display: block;
  margin-bottom: 12px;
}
</style>
