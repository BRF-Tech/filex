<script setup lang="ts">
/**
 * PdfViewer — rich PDF preview/edit via `pdfjs-dist`.
 *
 * Lazy-imports `pdfjs-dist/legacy/build/pdf` (~600 KB; the worker is
 * loaded as a separate script). Renders pages on-demand to canvases:
 * an IntersectionObserver attached to the page placeholders kicks off
 * `page.render()` only for visible pages — opening a 500-page PDF
 * doesn't paint 500 canvases up front.
 *
 * Features:
 *   - prev/next page jump (also typing the page number)
 *   - zoom in/out + fit-width
 *   - search box (case-insensitive, full document text scan)
 *   - download button
 *   - annotation toggle: enables form field editing via the AnnotationLayer
 *   - "Save annotations" — `pdf.saveDocument()` → POST to `pdfSaveUrl`
 *
 * Falls back to the host's native `<object>` mounting when pdfjs isn't
 * installed (the parent PreviewModal handles that branch).
 */
import { computed, onBeforeUnmount, onMounted, ref, watch, nextTick } from 'vue';
import { fetchViewerArrayBuffer } from '../composables/useViewerFetch';

const props = defineProps<{
  url: string;
  filePath: string;
  mime?: string;
  ext: string;
  pdfWorkerUrl?: string;
  pdfSaveUrl?: string;
  t?: (key: string) => string;
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
}>();

const emit = defineEmits<{
  (e: 'fallback'): void;
}>();

const containerEl = ref<HTMLDivElement | null>(null);
const error = ref<string | null>(null);
const loading = ref(true);
const totalPages = ref(0);
const currentPage = ref(1);
const scale = ref(1);
const annotationsOn = ref(false);
const saveStatus = ref<'idle' | 'saving' | 'saved' | 'error'>('idle');
const searchQuery = ref('');
const searchResults = ref(0);

let pdfjs: any = null;
let pdfDoc: any = null;
const pageRenderState: Map<number, 'pending' | 'rendered'> = new Map();
const pageEls: Map<number, HTMLDivElement> = new Map();
let observer: IntersectionObserver | null = null;
let renderToken = 0;

async function ensurePdfjs(): Promise<any | null> {
  if (pdfjs) return pdfjs;
  try {
    // Use the legacy build for broader browser compatibility and
    // simpler worker bootstrap (no module worker requirement).
    const mod = await import(/* @vite-ignore */ 'pdfjs-dist/legacy/build/pdf');
    pdfjs = mod.default ?? mod;
    if (pdfjs.GlobalWorkerOptions) {
      const version = pdfjs.version || '4.0.379';
      pdfjs.GlobalWorkerOptions.workerSrc =
        props.pdfWorkerUrl ||
        `https://cdn.jsdelivr.net/npm/pdfjs-dist@${version}/legacy/build/pdf.worker.min.js`;
    }
    return pdfjs;
  } catch {
    return null;
  }
}

// useNativeViewer flips the renderer to <embed type="application/pdf">
// when pdfjs.getDocument throws (worker fetch blocked, CDN
// unreachable, or pdfjs not installed). All evergreen browsers ship a
// built-in PDF viewer at the embed level, so this is a graceful
// fallback rather than the "open in new tab" lifeboat the toolbar
// already exposes.
const useNativeViewer = ref(false);

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  searchResults.value = 0;
  pageRenderState.clear();
  pageEls.clear();
  if (containerEl.value) containerEl.value.innerHTML = '';
  if (observer) {
    observer.disconnect();
    observer = null;
  }
  if (pdfDoc) {
    try { pdfDoc.destroy?.(); } catch { /* ignore */ }
    pdfDoc = null;
  }

  const myToken = ++renderToken;

  const lib = await ensurePdfjs();
  if (myToken !== renderToken) return;
  if (!lib) {
    // pdfjs-dist absent → use the browser's built-in PDF viewer via
    // <embed>. error.value stays null so the template renders the
    // native preview without an angry banner.
    useNativeViewer.value = true;
    loading.value = false;
    emit('fallback');
    return;
  }

  try {
    const buf = await fetchViewerArrayBuffer({
      url: props.url,
      headers: props.authHeaders?.() ?? {},
      credentials: props.authCredentials,
    });
    if (myToken !== renderToken) return;

    pdfDoc = await lib.getDocument({
      data: buf,
      enableXfa: true,
    }).promise;

    if (myToken !== renderToken) return;
    totalPages.value = pdfDoc.numPages;

    await nextTick();
    buildPagePlaceholders();
    setupObserver();
  } catch (err) {
    // Worker fetch blocked (CDN/CSP), unsupported runtime, etc. —
    // hand off to the browser's PDF viewer instead of a dead error
    // panel. Captures the original message in case operators want
    // it via DevTools.
    if (typeof console !== 'undefined') {
      console.warn('[filex/PdfViewer] pdfjs failed, using native embed:', err);
    }
    useNativeViewer.value = true;
  } finally {
    loading.value = false;
  }
}

function buildPagePlaceholders(): void {
  if (!containerEl.value) return;
  for (let i = 1; i <= totalPages.value; i++) {
    const wrap = document.createElement('div');
    wrap.className = 'filex-viewer-pdf__page';
    wrap.dataset.page = String(i);
    wrap.style.position = 'relative';
    wrap.style.margin = '12px auto';
    wrap.style.background = '#fff';
    wrap.style.boxShadow = '0 4px 16px rgba(0, 0, 0, 0.3)';
    wrap.style.minHeight = '600px';
    wrap.style.width = '816px';
    containerEl.value.appendChild(wrap);
    pageEls.set(i, wrap);
    pageRenderState.set(i, 'pending');
  }
}

function setupObserver(): void {
  observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        const el = entry.target as HTMLDivElement;
        const num = Number(el.dataset.page);
        if (entry.isIntersecting) {
          if (pageRenderState.get(num) === 'pending') {
            pageRenderState.set(num, 'rendered');
            renderPage(num).catch(() => {
              pageRenderState.set(num, 'pending');
            });
          }
          if (entry.intersectionRatio > 0.5) {
            currentPage.value = num;
          }
        }
      }
    },
    { root: containerEl.value, threshold: [0, 0.5, 1] },
  );
  for (const el of pageEls.values()) {
    observer.observe(el);
  }
}

async function renderPage(pageNum: number): Promise<void> {
  if (!pdfDoc) return;
  const wrap = pageEls.get(pageNum);
  if (!wrap) return;
  const page = await pdfDoc.getPage(pageNum);
  const viewport = page.getViewport({ scale: scale.value * 1.5 });
  wrap.style.width = `${viewport.width / 1.5}px`;
  wrap.style.minHeight = `${viewport.height / 1.5}px`;

  const canvas = document.createElement('canvas');
  canvas.width = viewport.width;
  canvas.height = viewport.height;
  canvas.style.width = '100%';
  canvas.style.height = 'auto';
  canvas.style.display = 'block';
  wrap.innerHTML = '';
  wrap.appendChild(canvas);

  const ctx = canvas.getContext('2d');
  if (!ctx) return;
  await page.render({ canvasContext: ctx, viewport }).promise;

  if (annotationsOn.value && pdfjs?.AnnotationLayer) {
    const annoDiv = document.createElement('div');
    annoDiv.className = 'filex-viewer-pdf__anno';
    annoDiv.style.position = 'absolute';
    annoDiv.style.inset = '0';
    annoDiv.style.transformOrigin = '0 0';
    wrap.appendChild(annoDiv);
    try {
      const annotations = await page.getAnnotations({ intent: 'display' });
      pdfjs.AnnotationLayer.render({
        viewport: viewport.clone({ dontFlip: true }),
        div: annoDiv,
        annotations,
        page,
        renderForms: true,
      });
    } catch {
      /* ignore — best-effort annotation render */
    }
  }
}

async function rerenderAll(): Promise<void> {
  for (const [num, state] of pageRenderState.entries()) {
    if (state === 'rendered') {
      pageRenderState.set(num, 'pending');
      const el = pageEls.get(num);
      if (el && observer) {
        const rect = el.getBoundingClientRect();
        const root = containerEl.value?.getBoundingClientRect();
        if (root && rect.bottom > root.top && rect.top < root.bottom) {
          pageRenderState.set(num, 'rendered');
          renderPage(num).catch(() => {
            pageRenderState.set(num, 'pending');
          });
        }
      }
    }
  }
}

function gotoPage(n: number): void {
  if (n < 1 || n > totalPages.value) return;
  const el = pageEls.get(n);
  el?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  currentPage.value = n;
}

function next(): void {
  gotoPage(currentPage.value + 1);
}
function prev(): void {
  gotoPage(currentPage.value - 1);
}

function zoomIn(): void {
  scale.value = Math.min(4, scale.value * 1.25);
  rerenderAll();
}
function zoomOut(): void {
  scale.value = Math.max(0.25, scale.value / 1.25);
  rerenderAll();
}
function fitWidth(): void {
  if (!containerEl.value || !pdfDoc) return;
  pdfDoc
    .getPage(1)
    .then((p: any) => {
      const v = p.getViewport({ scale: 1 });
      const containerWidth = (containerEl.value?.clientWidth ?? 800) - 48;
      scale.value = Math.max(0.25, Math.min(4, containerWidth / v.width));
      rerenderAll();
    })
    .catch(() => {});
}

function toggleAnnotations(): void {
  annotationsOn.value = !annotationsOn.value;
  rerenderAll();
}

async function saveAnnotations(): Promise<void> {
  if (!props.pdfSaveUrl || !pdfDoc) return;
  saveStatus.value = 'saving';
  try {
    const data: Uint8Array = await pdfDoc.saveDocument();
    const base64 = btoa(String.fromCharCode(...data));
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(props.authHeaders?.() ?? {}),
    };
    const res = await fetch(props.pdfSaveUrl, {
      method: 'POST',
      headers,
      credentials: props.authCredentials || 'same-origin',
      body: JSON.stringify({ path: props.filePath, base64 }),
    });
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
    saveStatus.value = 'saved';
    setTimeout(() => {
      if (saveStatus.value === 'saved') saveStatus.value = 'idle';
    }, 2500);
  } catch (err) {
    saveStatus.value = 'error';
    error.value = err instanceof Error ? err.message : 'save failed';
  }
}

async function search(): Promise<void> {
  if (!pdfDoc) return;
  const q = searchQuery.value.trim().toLowerCase();
  if (!q) {
    searchResults.value = 0;
    return;
  }
  let count = 0;
  for (let i = 1; i <= totalPages.value; i++) {
    const page = await pdfDoc.getPage(i);
    const content = await page.getTextContent();
    const text = content.items.map((it: any) => it.str).join(' ').toLowerCase();
    let from = 0;
    while ((from = text.indexOf(q, from)) !== -1) {
      count++;
      from += q.length;
    }
  }
  searchResults.value = count;
}

const pageLabel = computed(() => {
  const m = (props.t && props.t('viewer.page_n_of_m')) || 'Page {n} of {m}';
  return m
    .replace('{n}', String(currentPage.value))
    .replace('{m}', String(totalPages.value));
});

onMounted(load);
onBeforeUnmount(() => {
  renderToken++;
  if (observer) {
    observer.disconnect();
    observer = null;
  }
  if (pdfDoc) {
    try { pdfDoc.destroy?.(); } catch { /* ignore */ }
    pdfDoc = null;
  }
});

watch(() => props.url, load);

function tt(key: string, fallback: string): string {
  return props.t ? props.t(key) : fallback;
}
</script>

<template>
  <div class="filex-viewer-pdf">
    <div class="filex-viewer-pdf__bar">
      <button type="button" class="filex-viewer-btn" :disabled="currentPage <= 1" @click="prev">‹</button>
      <input
        type="number"
        class="filex-viewer-pdf__page-input"
        :min="1"
        :max="totalPages || 1"
        :value="currentPage"
        @change="(ev) => gotoPage(Number((ev.target as HTMLInputElement).value))"
      />
      <span class="filex-viewer-pdf__total">/ {{ totalPages }}</span>
      <button type="button" class="filex-viewer-btn" :disabled="currentPage >= totalPages" @click="next">›</button>
      <span class="filex-viewer-spacer" />
      <button type="button" class="filex-viewer-btn" @click="zoomOut" :title="tt('viewer.zoom_out', 'Zoom out')">−</button>
      <span class="filex-viewer-pdf__zoom">{{ Math.round(scale * 100) }}%</span>
      <button type="button" class="filex-viewer-btn" @click="zoomIn" :title="tt('viewer.zoom_in', 'Zoom in')">+</button>
      <button type="button" class="filex-viewer-btn" @click="fitWidth" :title="tt('viewer.fit_width', 'Fit width')">↔</button>
      <input
        v-model="searchQuery"
        type="search"
        class="filex-viewer-pdf__search"
        :placeholder="tt('viewer.search', 'Search')"
        @keydown.enter="search"
      />
      <span v-if="searchResults > 0" class="filex-viewer-pdf__hits">{{ searchResults }}</span>
      <button
        type="button"
        class="filex-viewer-btn"
        :class="{ 'is-active': annotationsOn }"
        @click="toggleAnnotations"
      >
        {{ annotationsOn ? tt('viewer.annotations_on', 'Annot: on') : tt('viewer.annotations_off', 'Annot: off') }}
      </button>
      <button
        v-if="pdfSaveUrl"
        type="button"
        class="filex-viewer-btn"
        :disabled="saveStatus === 'saving'"
        @click="saveAnnotations"
      >
        {{
          saveStatus === 'saving'
            ? '⏳'
            : saveStatus === 'saved'
              ? '✓'
              : tt('viewer.save_annotations', 'Save')
        }}
      </button>
    </div>
    <div class="filex-viewer-pdf__pane">
      <!-- Browser-native PDF viewer fallback. Triggered when pdfjs
           is missing or its worker can't load (CDN blocked / CSP). -->
      <embed
        v-if="useNativeViewer"
        :src="props.url"
        type="application/pdf"
        class="filex-viewer-pdf__embed"
      />
      <div v-else-if="error" class="filex-viewer-fallback">
        <span class="filex-viewer-fallback__icon">📕</span>
        <p>{{ error }}</p>
      </div>
      <div v-else-if="loading" class="filex-viewer-fallback">
        <span class="filex-viewer-fallback__icon">⏳</span>
        <p>{{ tt('viewer.loading', 'Loading…') }}</p>
      </div>
      <div ref="containerEl" v-show="!loading && !error" class="filex-viewer-pdf__pages" />
    </div>
    <div v-if="!loading && !error && totalPages > 0" class="filex-viewer-pdf__footer">
      {{ pageLabel }}
    </div>
  </div>
</template>

<style scoped>
.filex-viewer-pdf {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 70vh;
  background: var(--fe-bg, #fff);
}
.filex-viewer-pdf__embed {
  width: 100%;
  height: 100%;
  min-height: 70vh;
  border: 0;
}
.filex-viewer-pdf__bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  font-size: 13px;
  flex-wrap: wrap;
}
.filex-viewer-pdf__page-input {
  width: 60px;
  padding: 4px 6px;
  border: 1px solid var(--fe-border, #e2e6ed);
  border-radius: 4px;
  background: var(--fe-bg, #fff);
  color: inherit;
  text-align: center;
  font: inherit;
  font-size: 12px;
}
.filex-viewer-pdf__total {
  font-size: 12px;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-pdf__zoom {
  min-width: 50px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  font-size: 12px;
}
.filex-viewer-pdf__search {
  width: 160px;
  padding: 4px 8px;
  border: 1px solid var(--fe-border, #e2e6ed);
  border-radius: 4px;
  background: var(--fe-bg, #fff);
  color: inherit;
  font: inherit;
  font-size: 12px;
}
.filex-viewer-pdf__hits {
  font-size: 11px;
  background: var(--fe-bg-selected, #dfe8ff);
  color: var(--fe-primary, #3b82f6);
  padding: 2px 6px;
  border-radius: 8px;
}
.filex-viewer-spacer { flex: 1; }
.filex-viewer-pdf__pane {
  flex: 1;
  overflow: auto;
  padding: 16px;
  background: #2a2d33;
}
.filex-viewer-pdf__pages {
  display: flex;
  flex-direction: column;
  align-items: center;
}
.filex-viewer-pdf__footer {
  padding: 6px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-top: 1px solid var(--fe-border, #e2e6ed);
  font-size: 11px;
  color: var(--fe-text-muted, #5a6475);
  text-align: center;
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
.filex-viewer-btn.is-active {
  background: var(--fe-bg-selected, #dfe8ff);
  border-color: var(--fe-primary, #3b82f6);
}
</style>
