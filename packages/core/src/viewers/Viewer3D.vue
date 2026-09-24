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
import { actionIconSvg } from '../lib/actionIcons'; /* ikon:emoji */
import { fileIconTile } from '../lib/fileIcons'; /* ikon:emoji */

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
      : 'This kind of file cannot be shown here. Download it to open it on your device.';
  }
}

/**
 * model-viewer failed to FETCH or PARSE the model.
 *
 * ⚠ `ready` only means the ~80 KB web component imported. The model itself is
 * fetched and decoded by model-viewer AFTER it mounts, and when that throws
 * (a truncated or non-glTF file) it renders an empty canvas and reports the
 * failure only on this event. Without the listener the person got a silent
 * black rectangle and nothing else — measured 2026-09-13 on an 8-byte
 * `turbine.glb`, where the console had `RangeError: Invalid DataView length 12`
 * and the pane had no text at all.
 */
function onModelError(): void {
  ready.value = false;
  error.value = props.t ? props.t('viewer.failed_to_load') : 'Failed to load file';
}

onMounted(load);

watch(() => props.url, () => {
  // Re-evaluate on URL change (different file may need different
  // fallback message).
  load();
});

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
      @error="onModelError"
    />
    <div v-else-if="error" class="filex-viewer-fallback">
      <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons + lib/actionIcons -->
      <span class="filex-viewer-fallback__icon" aria-hidden="true" v-html="typeTile"></span>
      <p>{{ error }}</p>
    </div>
    <div v-else class="filex-viewer-fallback">
      <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons + lib/actionIcons -->
      <span
        class="filex-viewer-fallback__icon filex-viewer-fallback__icon--spin"
        aria-hidden="true"
        v-html="actionIconSvg('progress')"
      ></span>
      <p>{{ t ? t('viewer.loading') : 'Loading…' }}</p>
    </div>
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
.filex-viewer-3d {
  width: 100%;
  height: 100%;
  min-height: 480px;
  background: #1a1a1a;
  display: flex;
  align-items: center;
  justify-content: center;
}
.filex-viewer-3d model-viewer {
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
