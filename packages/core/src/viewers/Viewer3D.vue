<script setup lang="ts">
/**
 * Viewer3D — 3D model preview via `@google/model-viewer`.
 *
 * Lazy-imports the `@google/model-viewer` web component (~80 KB).
 *
 * Format support: model-viewer **only** understands glTF (`.gltf`,
 * `.glb`) and USDZ (iOS). For unsupported formats (`.stl`, `.obj`,
 * `.fbx`, `.3ds`) we deliberately render a download fallback instead
 * of feeding them to model-viewer — earlier code did the latter,
 * which surfaced as `JSON.parse(<ascii STL>)` SyntaxError because
 * model-viewer parses the response body as glTF JSON. (sweep-2026-05-09
 * bugs 19-20.)
 *
 * GLB rendering size: model-viewer mounts its canvas inside a
 * shadow root that doesn't always inherit `height: 100%` from
 * flexbox parents — we pin explicit sizing on the host element
 * to avoid the "Framebuffer is incomplete: zero size" warning.
 * (sweep-2026-05-09 bug 21.)
 */
import { computed, onMounted, ref, watch } from 'vue';

const props = defineProps<{
  url: string;
  mime?: string;
  ext: string;
  /** Locale-aware error/loading messages. */
  t?: (key: string) => string;
}>();

const SUPPORTED_EXTS = new Set(['glb', 'gltf', 'usdz']);

const isSupported = computed(() => SUPPORTED_EXTS.has((props.ext || '').toLowerCase()));

const error = ref<string | null>(null);
const ready = ref(false);

async function load(): Promise<void> {
  ready.value = false;
  error.value = null;
  // Bail out early for formats model-viewer can't parse — feeding
  // them to <model-viewer> triggers JSON.parse SyntaxError because
  // it expects glTF JSON.
  if (!isSupported.value) {
    error.value = props.t
      ? props.t('viewer.format_unsupported_3d')
      : `3D format ".${props.ext}" not supported in browser preview — please download.`;
    return;
  }
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
  // Re-evaluate on URL change (different file may need different
  // fallback message).
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
      style="width: 100%; height: 100%; min-height: 480px; display: block"
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
