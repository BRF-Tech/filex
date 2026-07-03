<script setup lang="ts">
/**
 * EpubViewer — flowable EPUB reader via `epubjs`.
 *
 * Lazy-imports `epubjs` (~80 KB gzipped). epub.js renders the publication
 * inside an iframe (default `flow: 'paginated'`), but we use it in
 * scrolled-doc mode only when the user toggles the "fit-width" flag — by
 * default we keep paginated since that matches reader-app expectations.
 *
 * UX:
 *   - prev/next page buttons (also keyboard arrow keys via composable)
 *   - collapsible TOC sidebar (built from `book.loaded.navigation`)
 *   - font size +/- (Themes API)
 *   - graceful fallback if peer not installed
 */
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';

const props = defineProps<{
  url: string;
  mime?: string;
  ext: string;
  t?: (key: string) => string;
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
}>();

interface TocNode {
  href: string;
  label: string;
  subitems?: TocNode[];
}

const root = ref<HTMLDivElement | null>(null);
const containerRef = ref<HTMLDivElement | null>(null);
const error = ref<string | null>(null);
const toc = ref<TocNode[]>([]);
const tocOpen = ref(false);
const fontSize = ref(100);
const ready = ref(false);
const loading = ref(true);

let book: any = null;
let rendition: any = null;

async function load(): Promise<void> {
  loading.value = true;
  ready.value = false;
  error.value = null;
  let mod: any = null;
  try {
    mod = await import(/* @vite-ignore */ 'epubjs');
  } catch {
    error.value = props.t
      ? props.t('viewer.peer_not_installed')
      : 'EPUB viewer requires `epubjs` — install or use download.';
    loading.value = false;
    return;
  }
  try {
    const Epub = mod.default ?? mod;
    // epub.js wants a URL (it then fetches with XHR). When the host
    // site requires auth headers we have to load the file ourselves
    // and pass an ArrayBuffer instead.
    let source: string | ArrayBuffer = props.url;
    if (props.authHeaders) {
      const headers = props.authHeaders();
      const res = await fetch(props.url, {
        headers,
        credentials: props.authCredentials || 'same-origin',
      });
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
      source = await res.arrayBuffer();
    }
    book = Epub(source);
    if (!containerRef.value) {
      throw new Error('EPUB mount target missing');
    }
    rendition = book.renderTo(containerRef.value, {
      width: '100%',
      height: '100%',
      flow: 'paginated',
      manager: 'default',
    });
    await rendition.display();
    rendition.themes.fontSize(fontSize.value + '%');
    const nav = await book.loaded.navigation;
    toc.value = (nav?.toc ?? []) as TocNode[];
    ready.value = true;
  } catch (err) {
    error.value =
      err instanceof Error ? err.message : 'EPUB load failed';
  } finally {
    loading.value = false;
  }
}

function next(): void {
  rendition?.next?.();
}
function prev(): void {
  rendition?.prev?.();
}
function gotoHref(href: string): void {
  rendition?.display?.(href);
  tocOpen.value = false;
}
function bigger(): void {
  fontSize.value = Math.min(200, fontSize.value + 10);
  rendition?.themes?.fontSize(fontSize.value + '%');
}
function smaller(): void {
  fontSize.value = Math.max(60, fontSize.value - 10);
  rendition?.themes?.fontSize(fontSize.value + '%');
}

function onKey(ev: KeyboardEvent): void {
  if (ev.key === 'ArrowRight' || ev.key === 'PageDown') next();
  else if (ev.key === 'ArrowLeft' || ev.key === 'PageUp') prev();
}

onMounted(() => {
  load();
  window.addEventListener('keydown', onKey);
});

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey);
  try {
    rendition?.destroy?.();
  } catch {
    /* ignore */
  }
  try {
    book?.destroy?.();
  } catch {
    /* ignore */
  }
  rendition = null;
  book = null;
});

watch(
  () => props.url,
  () => {
    if (rendition) {
      try {
        rendition.destroy();
      } catch {
        /* ignore */
      }
    }
    if (book) {
      try {
        book.destroy();
      } catch {
        /* ignore */
      }
    }
    book = null;
    rendition = null;
    load();
  },
);

function tt(key: string, fallback: string): string {
  return props.t ? props.t(key) : fallback;
}
</script>

<template>
  <div ref="root" class="filex-viewer-epub">
    <div v-if="error" class="filex-viewer-fallback">
      <span class="filex-viewer-fallback__icon">📖</span>
      <p>{{ error }}</p>
    </div>
    <template v-else>
      <div ref="containerRef" class="filex-viewer-epub__rendition" />
      <div v-if="ready" class="filex-viewer-epub__nav">
        <button type="button" class="filex-viewer-btn" @click="prev" :disabled="!ready">‹</button>
        <button type="button" class="filex-viewer-btn" @click="next" :disabled="!ready">›</button>
      </div>
      <div v-if="loading" class="filex-viewer-epub__loading">
        {{ tt('viewer.loading', 'Loading…') }}
      </div>
    </template>
  </div>
</template>

<style scoped>
.filex-viewer-epub__nav {
  position: absolute;
  bottom: 12px;
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  gap: 8px;
  padding: 4px 8px;
  background: var(--fe-bg-elev, rgba(255, 255, 255, 0.9));
  border: 1px solid var(--fe-border, #e2e6ed);
  border-radius: 6px;
  backdrop-filter: blur(4px);
}
.filex-viewer-epub {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 70vh;
  background: var(--fe-bg, #fff);
  color: var(--fe-text, #1a1e27);
  position: relative;
}
.filex-viewer-epub__bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  font-size: 13px;
}
.filex-viewer-spacer { flex: 1; }
.filex-viewer-epub__fs {
  display: inline-block;
  min-width: 40px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-epub__main {
  flex: 1;
  min-height: 0;
  display: flex;
}
.filex-viewer-epub__rendition {
  flex: 1;
  min-height: 0;
}
.filex-viewer-epub__toc {
  width: 260px;
  border-right: 1px solid var(--fe-border, #e2e6ed);
  overflow-y: auto;
  padding: 12px 0;
  background: var(--fe-bg-elev, #f7f8fa);
}
.filex-viewer-epub__toc ul {
  list-style: none;
  margin: 0;
  padding: 0;
}
.filex-viewer-epub__toc-link {
  display: block;
  width: 100%;
  text-align: left;
  background: transparent;
  border: 0;
  padding: 6px 14px;
  font: inherit;
  color: inherit;
  cursor: pointer;
}
.filex-viewer-epub__toc-link:hover {
  background: var(--fe-bg-hover, #edf0f5);
}
.filex-viewer-epub__toc-link.is-child {
  padding-left: 28px;
  color: var(--fe-text-muted, #5a6475);
  font-size: 12px;
}
.filex-viewer-epub__loading {
  position: absolute;
  inset: auto 0 0 0;
  text-align: center;
  padding: 6px;
  background: rgba(0, 0, 0, 0.05);
  font-size: 12px;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-fallback {
  text-align: center;
  padding: 32px;
  color: var(--fe-text-muted, #5a6475);
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
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
