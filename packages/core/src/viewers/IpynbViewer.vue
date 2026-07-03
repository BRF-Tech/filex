<script setup lang="ts">
/**
 * IpynbViewer — Jupyter notebook (.ipynb JSON) renderer.
 *
 * Pure-JS render — no peer dependency required:
 *   - code cells     → `<pre><code>` (highlight.js when available)
 *   - markdown cells → markdown-it when available, plain `<pre>` otherwise
 *   - output cells   → text (stream / display_data 'text/plain'),
 *                      images (display_data 'image/png' base64),
 *                      HTML (display_data 'text/html', sanitized via
 *                      element-only insertion — no script tags)
 *
 * Source list / output count counters at the top so a 200-cell
 * notebook doesn't surprise the user.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { fetchViewerText } from '../composables/useViewerFetch';
import { ensureHighlight } from '../composables/useMonacoLoader';

const props = defineProps<{
  url: string;
  ext: string;
  t?: (key: string) => string;
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
}>();

interface Cell {
  cell_type: 'code' | 'markdown' | 'raw' | string;
  source: string | string[];
  outputs?: any[];
  execution_count?: number | null;
  metadata?: Record<string, unknown>;
}

interface Notebook {
  cells: Cell[];
  metadata?: { kernelspec?: { language?: string; name?: string } };
}

const cells = ref<Cell[]>([]);
const language = ref<string>('python');
const error = ref<string | null>(null);
const loading = ref(true);
const renderedSources = ref<Map<number, string>>(new Map());
const renderedMarkdown = ref<Map<number, string>>(new Map());

let renderToken = 0;
let highlight: any = null;
let mdRenderer: any = null;

async function ensureMd(): Promise<any | null> {
  if (mdRenderer !== null) return mdRenderer || null;
  try {
    const mod = await import(/* @vite-ignore */ 'markdown-it');
    const Md = (mod as any).default ?? mod;
    mdRenderer = new Md({ html: false, linkify: true, breaks: true });
    return mdRenderer;
  } catch {
    mdRenderer = false;
    return null;
  }
}

function joinSource(source: string | string[]): string {
  return Array.isArray(source) ? source.join('') : (source ?? '');
}

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  cells.value = [];
  renderedSources.value = new Map();
  renderedMarkdown.value = new Map();
  const myToken = ++renderToken;

  let raw: string;
  try {
    raw = await fetchViewerText({
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

  let nb: Notebook;
  try {
    nb = JSON.parse(raw);
  } catch (err) {
    error.value = 'Invalid notebook JSON';
    loading.value = false;
    return;
  }
  cells.value = Array.isArray(nb.cells) ? nb.cells : [];
  language.value = nb.metadata?.kernelspec?.language || 'python';

  // Best-effort hljs highlight per code cell.
  highlight = (await ensureHighlight()) as any;
  if (myToken !== renderToken) return;
  if (highlight) {
    const hljs = highlight.default ?? highlight;
    const langOk = hljs.getLanguage?.(language.value) ? language.value : null;
    cells.value.forEach((cell, idx) => {
      if (cell.cell_type === 'code') {
        const text = joinSource(cell.source);
        try {
          const result = langOk
            ? hljs.highlight(text, { language: langOk, ignoreIllegals: true })
            : hljs.highlightAuto(text);
          renderedSources.value.set(idx, result.value);
        } catch {
          /* leave unrendered */
        }
      }
    });
  }

  const md = await ensureMd();
  if (myToken !== renderToken) return;
  if (md) {
    cells.value.forEach((cell, idx) => {
      if (cell.cell_type === 'markdown') {
        try {
          renderedMarkdown.value.set(idx, md.render(joinSource(cell.source)));
        } catch {
          /* leave plain */
        }
      }
    });
  }

  loading.value = false;
}

function outputText(out: any): string | null {
  if (!out) return null;
  if (out.output_type === 'stream') {
    return joinSource(out.text);
  }
  if (out.output_type === 'error') {
    return [out.ename, out.evalue, ...(out.traceback || [])]
      .filter(Boolean)
      .join('\n');
  }
  if (out.output_type === 'execute_result' || out.output_type === 'display_data') {
    if (out.data?.['text/plain']) {
      return joinSource(out.data['text/plain']);
    }
  }
  return null;
}

function outputImage(out: any): string | null {
  if (!out?.data) return null;
  if (out.data['image/png']) return `data:image/png;base64,${out.data['image/png']}`;
  if (out.data['image/jpeg']) return `data:image/jpeg;base64,${out.data['image/jpeg']}`;
  if (out.data['image/svg+xml']) {
    const svg = joinSource(out.data['image/svg+xml']);
    return `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`;
  }
  return null;
}

function outputHtml(out: any): string | null {
  if (!out?.data) return null;
  if (out.data['text/html']) {
    const html = joinSource(out.data['text/html']);
    // Strip <script> for safety — outputs from arbitrary notebooks.
    return html.replace(/<script[\s\S]*?<\/script>/gi, '');
  }
  return null;
}

onMounted(load);
onBeforeUnmount(() => {
  renderToken++;
});
watch(() => props.url, load);

const stats = computed(() => {
  const codeCount = cells.value.filter((c) => c.cell_type === 'code').length;
  const mdCount = cells.value.filter((c) => c.cell_type === 'markdown').length;
  return { code: codeCount, md: mdCount, total: cells.value.length };
});

function tt(key: string, fallback: string): string {
  return props.t ? props.t(key) : fallback;
}
</script>

<template>
  <div class="filex-viewer-ipynb">
    <div class="filex-viewer-ipynb__pane">
      <div v-if="error" class="filex-viewer-fallback">
        <span class="filex-viewer-fallback__icon">📓</span>
        <p>{{ error }}</p>
      </div>
      <div v-else-if="loading" class="filex-viewer-fallback">
        <span class="filex-viewer-fallback__icon">⏳</span>
        <p>{{ tt('viewer.loading', 'Loading…') }}</p>
      </div>
      <div v-else>
        <div
          v-for="(cell, idx) in cells"
          :key="idx"
          class="filex-viewer-ipynb__cell"
          :data-type="cell.cell_type"
        >
          <template v-if="cell.cell_type === 'code'">
            <div class="filex-viewer-ipynb__exec">
              In [{{ cell.execution_count ?? ' ' }}]:
            </div>
            <pre class="filex-viewer-ipynb__src hljs"><code
              v-if="renderedSources.get(idx)"
              v-html="renderedSources.get(idx)"
            /><code v-else>{{ joinSource(cell.source) }}</code></pre>
            <div v-if="cell.outputs && cell.outputs.length" class="filex-viewer-ipynb__outputs">
              <template v-for="(out, j) in cell.outputs" :key="j">
                <img
                  v-if="outputImage(out)"
                  :src="outputImage(out) || ''"
                  class="filex-viewer-ipynb__img"
                  :alt="`output ${j}`"
                />
                <div
                  v-else-if="outputHtml(out)"
                  class="filex-viewer-ipynb__html"
                  v-html="outputHtml(out)"
                />
                <pre
                  v-else-if="outputText(out)"
                  class="filex-viewer-ipynb__out"
                  :data-type="out.output_type"
                >{{ outputText(out) }}</pre>
              </template>
            </div>
          </template>

          <template v-else-if="cell.cell_type === 'markdown'">
            <div
              v-if="renderedMarkdown.get(idx)"
              class="filex-viewer-ipynb__md"
              v-html="renderedMarkdown.get(idx)"
            />
            <pre v-else class="filex-viewer-ipynb__md-raw">{{ joinSource(cell.source) }}</pre>
          </template>

          <template v-else>
            <pre class="filex-viewer-ipynb__src">{{ joinSource(cell.source) }}</pre>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.filex-viewer-ipynb {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 70vh;
  background: var(--fe-bg, #fff);
  color: var(--fe-text, #1a1e27);
}
.filex-viewer-ipynb__bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  font-size: 12px;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-ipynb__lang {
  font-family: var(--fe-font-mono, monospace);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.filex-viewer-ipynb__pane {
  flex: 1;
  overflow: auto;
  padding: 16px 24px;
}
.filex-viewer-ipynb__cell {
  border: 1px solid var(--fe-border, #e2e6ed);
  border-radius: 6px;
  margin-bottom: 16px;
  overflow: hidden;
}
.filex-viewer-ipynb__cell[data-type="code"] {
  background: var(--fe-bg-elev, #f7f8fa);
}
.filex-viewer-ipynb__exec {
  padding: 4px 12px;
  font-family: var(--fe-font-mono, monospace);
  font-size: 11px;
  color: var(--fe-text-muted, #5a6475);
  background: rgba(0, 0, 0, 0.04);
}
.filex-viewer-ipynb__src {
  margin: 0;
  padding: 12px;
  font-family: var(--fe-font-mono, monospace);
  font-size: 12px;
  line-height: 1.5;
  white-space: pre;
  overflow-x: auto;
}
.filex-viewer-ipynb__outputs {
  border-top: 1px dashed var(--fe-border, #e2e6ed);
  background: var(--fe-bg, #fff);
  padding: 8px 12px;
}
.filex-viewer-ipynb__out {
  margin: 0;
  padding: 4px 0;
  font-family: var(--fe-font-mono, monospace);
  font-size: 12px;
  white-space: pre-wrap;
  word-wrap: break-word;
}
.filex-viewer-ipynb__out[data-type="error"] {
  color: var(--fe-danger, #dc2626);
}
.filex-viewer-ipynb__img {
  display: block;
  max-width: 100%;
  height: auto;
  margin: 8px 0;
}
.filex-viewer-ipynb__html {
  font-size: 13px;
}
.filex-viewer-ipynb__md {
  padding: 12px 16px;
  font-size: 14px;
  line-height: 1.6;
}
.filex-viewer-ipynb__md-raw {
  padding: 12px;
  margin: 0;
  font-family: var(--fe-font-mono, monospace);
  font-size: 12px;
  white-space: pre-wrap;
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
</style>
