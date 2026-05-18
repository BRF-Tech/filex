<script setup lang="ts">
/**
 * PreviewModal — inline file preview.
 *
 * Strategy by extension:
 *   image / video / audio / pdf  → native browser elements
 *   md / markdown                 → markdown-it (lazy) → rendered HTML
 *   code (js/ts/php/py/json/...)  → Monaco when ready, highlight.js
 *                                    fallback while it's still loading,
 *                                    plain `<pre>` when neither is
 *                                    installed
 *   office (docx/xlsx/pptx)       → OnlyOffice iframe (config.onlyOfficeBase)
 *   plain text (txt/log/conf)     → CodeMirror when saveText is set,
 *                                    `<pre>` otherwise
 *   anything else                 → "indir" fallback
 *
 * Monaco is dynamic-imported at FileExplorer onMounted; by the time the
 * user clicks an editable code file the chunk is usually already cached.
 * We probe `getMonaco()` synchronously when the modal opens — if it's
 * not yet loaded we render the highlight.js read-only view immediately,
 * then upgrade to Monaco once the import resolves.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue';
import type { Component } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import Modal from './Modal.vue';
import { ensureMonaco, getMonaco, ensureHighlight } from '../composables/useMonacoLoader';
import { useLocale } from '../composables/useLocale';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  file: FileNode | null;
  previewUrl: (path: string) => string;
  downloadUrl: (path: string) => string;
  onlyOfficeBase?: string | null;
  onlyOfficeConfigEndpoint?: string | null;
  saveTextEndpoint?: string | null;
  openMode?: 'edit' | 'view';
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
  /** New rich-viewer config (forwarded by FileExplorer from ExplorerConfig). */
  drawioUrl?: string | null;
  pdfWorkerUrl?: string | null;
  pdfSaveUrl?: string | null;
  /** Standalone full-screen viewer route — `?path=…&storage=…&type=…`. */
  viewerBaseUrl?: string | null;
  /** Drop the dialog chrome (backdrop tint, header bar, footer actions)
   *  so the viewer fills the full viewport. Used by the standalone
   *  /files/edit route where the browser tab IS the container — a
   *  modal frame on top of the editor just steals real estate. */
  chromeless?: boolean;
  /** When the dynamic-viewer chunk fails to load, fall back to the
   *  legacy native renderer (e.g. native `<object>` for PDFs). */
  /** Explicit theme. Forwarded to the underlying Modal (which tags
   *  its backdrop with `.fe--theme-{light,dark}` so the CSS variable
   *  override matches the host admin shell) and to Monaco (vs vs
   *  vs-dark). When unset Monaco falls back to `prefers-color-scheme`
   *  and the modal cascade follows whatever `.fe` parent it gets. */
  theme?: 'light' | 'dark' | 'auto';
}>();

const emit = defineEmits<{
  (e: 'close'): void;
}>();

const { t } = useLocale(() => props.locale);

function ext(f: FileNode | null): string {
  return (f?.extension || '').toLowerCase();
}

const IMAGE = ['jpg', 'jpeg', 'png', 'webp', 'gif', 'bmp', 'avif', 'svg', 'heic'];
const VIDEO = ['mp4', 'webm', 'mov', 'mkv', 'm4v', 'ogv'];
const AUDIO = ['mp3', 'wav', 'ogg', 'flac', 'm4a', 'aac', 'opus'];

const CODE_LANGS: Record<string, string> = {
  md: 'markdown', markdown: 'markdown',
  txt: 'plaintext', log: 'plaintext',
  js: 'javascript', mjs: 'javascript', cjs: 'javascript',
  ts: 'typescript', tsx: 'typescript', jsx: 'javascript',
  vue: 'xml', svelte: 'xml', html: 'xml', htm: 'xml', xml: 'xml', svg: 'xml',
  css: 'css', scss: 'scss', sass: 'scss', less: 'less',
  json: 'json', jsonc: 'json',
  yml: 'yaml', yaml: 'yaml',
  php: 'php', py: 'python', rb: 'ruby', go: 'go', rs: 'rust',
  java: 'java', kt: 'kotlin', swift: 'swift', cpp: 'cpp', c: 'c',
  h: 'cpp', hpp: 'cpp', cs: 'csharp', dart: 'dart',
  sh: 'bash', bash: 'bash', zsh: 'bash', fish: 'bash',
  sql: 'sql',
  toml: 'ini', ini: 'ini', conf: 'ini', env: 'bash', cfg: 'ini',
  dockerfile: 'dockerfile',
  graphql: 'graphql', gql: 'graphql',
  diff: 'diff', patch: 'diff',
};
// CSV/TSV land in the rich viewer (`csv` kind below) instead of the
// legacy plain-text path so the user gets a proper table preview.
const TEXT_PLAIN = ['txt', 'log'];
const OFFICE = ['docx', 'doc', 'xlsx', 'xls', 'pptx', 'ppt', 'odt', 'ods', 'odp', 'rtf'];

/**
 * Lazy viewer map — extension → component loader. Each loader is a
 * dynamic import so the viewer chunk only ships when the file type
 * is actually opened. The PreviewModal mounts the resolved component
 * via `<component :is="ViewerCmp" />` once the dynamic import settles.
 */
const VIEWER_MAP: Record<string, () => Promise<Component>> = {
  glb: () => import('../viewers/Viewer3D.vue'),
  gltf: () => import('../viewers/Viewer3D.vue'),
  obj: () => import('../viewers/Viewer3D.vue'),
  stl: () => import('../viewers/Viewer3D.vue'),
  fbx: () => import('../viewers/Viewer3D.vue'),
  '3ds': () => import('../viewers/Viewer3D.vue'),

  epub: () => import('../viewers/EpubViewer.vue'),

  mmd: () => import('../viewers/MermaidViewer.vue'),
  mermaid: () => import('../viewers/MermaidViewer.vue'),

  drawio: () => import('../viewers/DrawioViewer.vue'),
  dio: () => import('../viewers/DrawioViewer.vue'),

  tif: () => import('../viewers/TiffViewer.vue'),
  tiff: () => import('../viewers/TiffViewer.vue'),

  psd: () => import('../viewers/PsdViewer.vue'),

  // PDF deliberately uses the native browser viewer (`kind === 'pdf'`
  // branch in the template). PdfViewer.vue still exists for callers
  // that want the rich custom UI but the SFC default keeps things
  // minimal — the browser already paints search/zoom/page UI for free.

  ipynb: () => import('../viewers/IpynbViewer.vue'),

  csv: () => import('../viewers/CsvViewer.vue'),
  tsv: () => import('../viewers/CsvViewer.vue'),

  zip: () => import('../viewers/ArchiveViewer.vue'),
};

type PreviewKind =
  | 'image' | 'video' | 'audio' | 'pdf' | 'markdown' | 'code'
  | 'office' | 'text' | 'viewer' | 'other';

const kind = computed<PreviewKind>(() => {
  const e = ext(props.file);
  if (!e) return 'other';
  if (IMAGE.includes(e)) return 'image';
  if (VIDEO.includes(e)) return 'video';
  if (AUDIO.includes(e)) return 'audio';
  // PDF always uses the native browser <object> renderer. The
  // PdfViewer SFC is no longer wired into the default map — the
  // browser-bundled toolbar is enough and removes a 600 KB pdfjs
  // worker chunk + a custom toolbar layer for parity with the rest
  // of the read-only viewers.
  if (e === 'pdf') return 'pdf';
  // Markdown stays in its own branch (gets the split view in edit mode);
  // plain text → code editor in edit mode, raw <pre> in view mode.
  const wantEdit = props.openMode !== 'view';
  if (e === 'md' || e === 'markdown') return 'markdown';
  if (e in CODE_LANGS) return 'code';
  if (e in VIEWER_MAP) return 'viewer';
  if (OFFICE.includes(e)) return 'office';
  if (TEXT_PLAIN.includes(e)) return wantEdit ? 'code' : 'text';
  return 'other';
});

// --- Rich viewer plumbing ---
//
// `viewerCmp` holds the dynamically-imported component once the load
// resolves. Stored in a `shallowRef` because the component itself
// shouldn't be deep-watched.
const viewerCmp = shallowRef<Component | null>(null);
const viewerLoadError = ref<string | null>(null);
const pdfFallbackToNative = ref(false);

async function loadViewerFor(extension: string): Promise<void> {
  viewerCmp.value = null;
  viewerLoadError.value = null;
  const loader = VIEWER_MAP[extension];
  if (!loader) return;
  try {
    const mod = (await loader()) as { default?: Component };
    viewerCmp.value = mod.default ?? (mod as unknown as Component);
  } catch (err) {
    viewerLoadError.value = err instanceof Error ? err.message : 'viewer load failed';
    if (extension === 'pdf') pdfFallbackToNative.value = true;
  }
}

function onPdfFallback(): void {
  pdfFallbackToNative.value = true;
}

/** Build the props bundle pushed into the active viewer component. */
const viewerProps = computed(() => {
  const e = ext(props.file);
  const base: Record<string, unknown> = {
    url: src.value,
    ext: e,
    mime: props.file?.mime_type,
    t,
    authHeaders: props.authHeaders,
    authCredentials: props.authCredentials,
  };
  if (props.file) {
    base.filePath = stripAdapter(props.file.path);
    // Archive viewer needs the adapter-qualified path because the
    // /api/files/archive/list handler falls back to storages[0] when
    // no adapter prefix is present — on multi-storage instances that
    // 500s for every non-default storage (sample.zip on fm s3-test).
    if (e === 'zip' || e === 'rar' || e === '7z' || e === 'tar' || e === 'gz' || e === 'tgz') {
      base.filePath = props.file.path;
    }
  }
  if (e === 'drawio' || e === 'dio') {
    base.drawioUrl = props.drawioUrl ?? undefined;
    base.saveUrl = props.saveTextEndpoint ?? undefined;
    base.readOnly = props.openMode === 'view';
  }
  if (e === 'pdf') {
    base.pdfWorkerUrl = props.pdfWorkerUrl ?? undefined;
    base.pdfSaveUrl = props.pdfSaveUrl ?? undefined;
  }
  return base;
});

function openInNewTab(): void {
  buildAndOpenStandalone(props.openMode || 'edit');
}

function openEditInNewTab(): void {
  buildAndOpenStandalone('edit');
}

function buildAndOpenStandalone(mode: 'view' | 'edit'): void {
  if (!props.file) return;
  const e = ext(props.file);
  // Keep the adapter-qualified path intact so the editor route
  // resolves the storage from the URL (stripping it falls back to
  // storages[0] and 404s for any non-default adapter). Default base
  // is the SFC's standalone /files/edit route; embedders override via
  // viewerBaseUrl when they mount us elsewhere.
  const base = props.viewerBaseUrl || '/files/edit';
  const sep = base.includes('?') ? '&' : '?';
  const url =
    `${base}${sep}path=${encodeURIComponent(props.file.path)}` +
    `&type=${encodeURIComponent(e)}` +
    `&mode=${encodeURIComponent(mode)}`;
  window.open(url, '_blank', 'noopener');
}

/**
 * Extensions that have a meaningful "edit" surface. Read-only kinds
 * (image/video/audio/3D/archive) don't surface a "Düzenle" button.
 */
const EDITABLE_EXTS = new Set([
  // OnlyOffice — open the in-page office editor (or new tab) with
  // edit permissions.
  'docx', 'doc', 'xlsx', 'xls', 'pptx', 'ppt',
  'odt', 'ods', 'odp', 'rtf',
  // Drawio / mermaid round-trip via the new-tab route.
  'drawio', 'dio', 'mmd', 'mermaid',
  // Code / text / markdown — Monaco / split editor.
  'md', 'markdown', 'txt', 'log',
  'json', 'jsonc', 'yaml', 'yml', 'xml', 'svg', 'html', 'htm',
  'js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx', 'vue', 'svelte',
  'css', 'scss', 'sass', 'less',
  'php', 'py', 'rb', 'go', 'rs', 'java', 'kt', 'swift',
  'cpp', 'c', 'h', 'hpp', 'cs', 'dart',
  'sh', 'bash', 'sql',
  'toml', 'ini', 'conf', 'cfg', 'env',
  'dockerfile', 'graphql', 'gql',
]);

const canEditKind = computed<boolean>(() =>
  !!props.file && EDITABLE_EXTS.has(ext(props.file)),
);

// Keep adapter prefix so backend resolves the right storage — stripping
// it defaults to storages[0] and 404s on any non-default adapter.
const src = computed(() => (props.file ? props.previewUrl(props.file.path) : ''));
const download = computed(() => (props.file ? props.downloadUrl(props.file.path) : ''));

function stripAdapter(p: string): string {
  const idx = p.indexOf('://');
  return idx === -1 ? p : p.slice(idx + 3);
}

const loading = ref(false);
const fetchError = ref<string | null>(null);
const rawText = ref<string>('');
const MAX_TEXT_BYTES = 1_000_000;
const tooLarge = ref(false);

// Markdown split-edit state — drives the side-by-side textarea+preview
// layout inside the `kind === 'markdown'` template when openMode='edit'.
const mdDirty = ref(false);
const mdSaving = ref(false);
let mdReRenderTimer: ReturnType<typeof setTimeout> | undefined;
let mdAutosaveTimer: ReturnType<typeof setTimeout> | undefined;
function onMdInput() {
  mdDirty.value = true;
  if (mdReRenderTimer) clearTimeout(mdReRenderTimer);
  mdReRenderTimer = setTimeout(() => {
    renderMarkdown(rawText.value);
  }, 250);
  // Autosave 1.5s after last keystroke — manual Kaydet button + Ctrl+S
  // still work; saveMarkdown() guards against overlap.
  if (mdAutosaveTimer) clearTimeout(mdAutosaveTimer);
  mdAutosaveTimer = setTimeout(() => {
    void saveMarkdown();
  }, 1500);
}
async function saveMarkdown() {
  if (!props.saveTextEndpoint || !props.file || mdSaving.value) return;
  mdSaving.value = true;
  fetchError.value = null;
  try {
    const headers = {
      'Content-Type': 'application/json',
      ...(await (props.authHeaders ?? (() => ({})))()),
    };
    const res = await fetch(props.saveTextEndpoint, {
      method: 'POST',
      headers,
      credentials: props.authCredentials || 'same-origin',
      body: JSON.stringify({ path: stripAdapter(props.file.path), content: rawText.value }),
    });
    if (!res.ok) {
      throw new Error(`save failed: ${res.status} ${await res.text()}`);
    }
    mdDirty.value = false;
  } catch (err) {
    fetchError.value = err instanceof Error ? err.message : String(err);
  } finally {
    mdSaving.value = false;
  }
}

async function fetchText(url: string): Promise<void> {
  loading.value = true;
  fetchError.value = null;
  rawText.value = '';
  tooLarge.value = false;
  try {
    const headers: Record<string, string> = {};
    if (props.authHeaders) Object.assign(headers, props.authHeaders());
    const res = await fetch(url, {
      credentials: props.authCredentials || 'include',
      headers,
    });
    if (!res.ok) {
      throw new Error(`${res.status} ${res.statusText}`);
    }
    const len = Number(res.headers.get('content-length') || '0');
    if (len > MAX_TEXT_BYTES) {
      tooLarge.value = true;
      return;
    }
    const text = await res.text();
    if (text.length > MAX_TEXT_BYTES) {
      tooLarge.value = true;
      rawText.value = text.slice(0, MAX_TEXT_BYTES);
    } else {
      rawText.value = text;
    }
  } catch (err) {
    fetchError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

// --- Markdown rendering (lazy markdown-it) ---
//
// Beyond the base markdown render we walk the resulting DOM for two
// enrichments:
//   - ```mermaid``` fences → swap the `<pre>` for a rendered SVG via
//     the `mermaid` peer (when installed).
//   - ```math``` / `$$ … $$` blocks → render with `katex` (peer).
// Both enrichments degrade gracefully when the peer isn't available
// (the original `<pre>` stays in place).

const markdownHtml = ref<string>('');
const markdownEl = ref<HTMLDivElement | null>(null);

async function renderMarkdown(text: string): Promise<void> {
  try {
    const mod = (await import(/* @vite-ignore */ 'markdown-it').catch(() => null)) as any;
    if (!mod) {
      markdownHtml.value = '';
      return;
    }
    const Md = (mod as { default: any }).default ?? (mod as any);
    // html: true so inline HTML in README.md / docs renders (the
    // GitHub / GitLab contract — operators expect `<img>`, tables,
    // `<details>`, etc. to work). We sanitize the output below to
    // strip scripts / event handlers before injecting via v-html.
    const md = new Md({
      html: true,
      linkify: true,
      breaks: true,
      typographer: true,
    });
    const raw = md.render(text);
    markdownHtml.value = sanitizeHtml(raw);
    // Wait for Vue to flush the v-html into the DOM before walking it.
    await new Promise<void>((r) => setTimeout(r, 0));
    await enrichMarkdown();
  } catch (err) {
    markdownHtml.value = '';
    fetchError.value = err instanceof Error ? err.message : String(err);
  }
}

/**
 * Sanitize the markdown-it output before v-html injection.
 *
 * GitHub / GitLab let README authors embed inline HTML (img, table,
 * details, kbd …) so the viewer ships with `html: true`. This filter
 * strips the executable surface: `<script>`, `<iframe>`, `<object>`,
 * `<embed>`, any `on*` event-handler attribute, and `javascript:`
 * URLs. Conservative — README content with inline handlers is
 * exceptional and silent over-removal beats silent XSS.
 *
 * No external dep — keeping the package install-free is the trade
 * we want. Integrators who need a heavier sanitizer can run their
 * own DOMPurify pass on the rendered output before showing it.
 */
function sanitizeHtml(html: string): string {
  return html
    .replace(/<\s*script\b[^<]*(?:(?!<\s*\/\s*script\s*>)<[^<]*)*<\s*\/\s*script\s*>/gi, '')
    .replace(/<\s*iframe\b[\s\S]*?<\s*\/\s*iframe\s*>/gi, '')
    .replace(/<\s*object\b[\s\S]*?<\s*\/\s*object\s*>/gi, '')
    .replace(/<\s*embed\b[^>]*>/gi, '')
    .replace(/\son\w+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)/gi, '')
    .replace(/(href|src|action|formaction|xlink:href)\s*=\s*(?:"\s*javascript:[^"]*"|'\s*javascript:[^']*')/gi, '$1="#"');
}

async function enrichMarkdown(): Promise<void> {
  if (!markdownEl.value) return;
  const root = markdownEl.value;
  const blocks = Array.from(root.querySelectorAll('pre > code')) as HTMLElement[];
  if (blocks.length === 0) return;

  let mermaid: any = undefined;
  let katex: any = undefined;

  for (const block of blocks) {
    const cls = block.className || '';
    const isMermaid =
      /\blanguage-mermaid\b/.test(cls) || /\blanguage-mmd\b/.test(cls);
    const isMath =
      /\blanguage-math\b/.test(cls) ||
      /\blanguage-latex\b/.test(cls) ||
      /\blanguage-tex\b/.test(cls);

    if (isMermaid) {
      if (mermaid === undefined) {
        try {
          const m = await import(/* @vite-ignore */ 'mermaid');
          mermaid = m.default ?? m;
          mermaid.initialize?.({
            startOnLoad: false,
            securityLevel: 'strict',
          });
        } catch {
          mermaid = null;
        }
      }
      if (!mermaid) continue;
      try {
        const id = `filex-md-mermaid-${Math.random().toString(36).slice(2)}`;
        const { svg } = await mermaid.render(id, block.textContent || '');
        const wrap = document.createElement('div');
        wrap.className = 'fe-preview__md-mermaid';
        wrap.innerHTML = svg;
        block.parentElement?.replaceWith(wrap);
      } catch {
        /* leave fenced block */
      }
    } else if (isMath) {
      if (katex === undefined) {
        try {
          const m = await import(/* @vite-ignore */ 'katex');
          katex = m.default ?? m;
        } catch {
          katex = null;
        }
      }
      if (!katex) continue;
      try {
        const html = katex.renderToString(block.textContent || '', {
          displayMode: true,
          throwOnError: false,
        });
        const wrap = document.createElement('div');
        wrap.className = 'fe-preview__md-math';
        wrap.innerHTML = html;
        block.parentElement?.replaceWith(wrap);
      } catch {
        /* leave fenced block */
      }
    }
  }
}

// --- Code highlight (highlight.js fallback) ---
//
// Used as the placeholder render until Monaco resolves, AND as the
// permanent renderer when Monaco isn't installed at all (peer missing).

const codeHtml = ref<string>('');

async function highlightCode(text: string, language: string): Promise<void> {
  try {
    const mod = (await ensureHighlight()) as any;
    if (!mod) {
      codeHtml.value = '';
      return;
    }
    const hljs = mod.default ?? mod;
    if (hljs.getLanguage(language)) {
      const result = hljs.highlight(text, { language, ignoreIllegals: true });
      codeHtml.value = result.value;
    } else {
      const result = hljs.highlightAuto(text);
      codeHtml.value = result.value;
    }
  } catch (err) {
    codeHtml.value = '';
    fetchError.value = err instanceof Error ? err.message : String(err);
  }
}

// --- Monaco editor (lazy) ---
//
// We attempt to instantiate Monaco when the modal opens for a code/text
// file. If the module isn't loaded yet, the highlight.js placeholder
// renders first, and the editor mounts in-place once Monaco resolves.
// Save endpoint required for editing — read-only Monaco still loads if
// the user just wants the IDE-grade syntax/colour view.

const monacoEl = ref<HTMLDivElement | null>(null);
let monacoEditor: any = null;
let codeAutosaveTimer: ReturnType<typeof setTimeout> | undefined;
const monacoReady = ref(false);
const saving = ref(false);
const saveOk = ref(false);
const saveError = ref<string | null>(null);

function disposeMonaco(): void {
  if (monacoEditor) {
    try {
      monacoEditor.dispose();
    } catch {
      /* ignore */
    }
    monacoEditor = null;
  }
  monacoReady.value = false;
  saveOk.value = false;
  saveError.value = null;
}

/**
 * Map an extension to a Monaco language id. Anything not in the map
 * just becomes 'plaintext' — Monaco still gives line numbers + Ctrl+S.
 */
function monacoLanguageFor(extension: string): string {
  const map: Record<string, string> = {
    js: 'javascript', mjs: 'javascript', cjs: 'javascript',
    ts: 'typescript', tsx: 'typescript', jsx: 'javascript',
    json: 'json', jsonc: 'json',
    css: 'css', scss: 'scss', sass: 'scss', less: 'less',
    html: 'html', htm: 'html', vue: 'html', svelte: 'html',
    xml: 'xml', svg: 'xml',
    md: 'markdown', markdown: 'markdown',
    php: 'php', py: 'python',
    yml: 'yaml', yaml: 'yaml',
    go: 'go', rs: 'rust', sql: 'sql',
    cpp: 'cpp', c: 'c', h: 'cpp', hpp: 'cpp',
    sh: 'shell', bash: 'shell',
    dockerfile: 'dockerfile',
    toml: 'ini', ini: 'ini', conf: 'ini', cfg: 'ini',
  };
  return map[extension] ?? 'plaintext';
}

async function tryMountMonaco(text: string, extension: string): Promise<boolean> {
  const monaco = (await ensureMonaco()) as any;
  if (!monaco || !monacoEl.value) return false;
  try {
    disposeMonaco();
    const editable = !!props.saveTextEndpoint && props.openMode !== 'view';
    // Honour the explicit theme prop when set; fall back to the OS
    // preference otherwise. Without this the Monaco editor stays on
    // vs-dark on OS-dark systems even when the host admin shell is
    // light, producing a jarring light-card + dark-code combo inside
    // the in-page preview modal.
    const dark = props.theme === 'dark'
      ? true
      : props.theme === 'light'
        ? false
        : window.matchMedia?.('(prefers-color-scheme: dark)').matches;
    monacoEditor = monaco.editor.create(monacoEl.value, {
      value: text,
      language: monacoLanguageFor(extension),
      readOnly: !editable,
      theme: dark ? 'vs-dark' : 'vs',
      automaticLayout: true,
      minimap: { enabled: false },
      fontSize: 13,
    });
    if (editable) {
      monacoEditor.addCommand(
        monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS,
        () => void saveCode(),
      );
      // Autosave: 1.5s after the last keystroke we POST the buffer.
      // Ctrl+S still works for the impatient — saveCode() guards against
      // overlap, so a manual save during the debounce window just wins
      // and the queued autosave becomes a no-op.
      monacoEditor.onDidChangeModelContent(() => {
        if (codeAutosaveTimer) clearTimeout(codeAutosaveTimer);
        codeAutosaveTimer = setTimeout(() => {
          void saveCode();
        }, 1500);
      });
    }
    monacoReady.value = true;
    return true;
  } catch (err) {
    fetchError.value = `Monaco mount fail: ${(err as Error).message}`;
    return false;
  }
}

async function saveCode(): Promise<void> {
  if (!props.saveTextEndpoint || !props.file) return;
  if (props.openMode === 'view') return;
  if (saving.value) return;
  saving.value = true;
  saveOk.value = false;
  saveError.value = null;
  try {
    const text = monacoEditor ? monacoEditor.getValue() : '';
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (props.authHeaders) Object.assign(headers, props.authHeaders());
    const res = await fetch(props.saveTextEndpoint, {
      method: 'POST',
      headers,
      credentials: props.authCredentials || 'same-origin',
      body: JSON.stringify({
        path: stripAdapter(props.file.path),
        content: text,
      }),
    });
    if (!res.ok) {
      const txt = await res.text().catch(() => '');
      throw new Error(`${res.status} ${res.statusText}${txt ? ' — ' + txt.slice(0, 150) : ''}`);
    }
    saveOk.value = true;
    setTimeout(() => {
      saveOk.value = false;
    }, 2500);
  } catch (err) {
    saveError.value = (err as Error).message;
  } finally {
    saving.value = false;
  }
}

// --- Orchestration: fetch + render when modal opens ---

async function runOrchestration(open: boolean, url: string, k: string): Promise<void> {
  rawText.value = '';
  markdownHtml.value = '';
  codeHtml.value = '';
  fetchError.value = null;
  officeError.value = null;
  viewerCmp.value = null;
  viewerLoadError.value = null;
  pdfFallbackToNative.value = false;
  disposeOnlyOfficeEditor();
  disposeMonaco();
  if (!open || !url) return;
  if (k === 'viewer') {
    await loadViewerFor(ext(props.file));
    return;
  }
  if (k === 'markdown' || k === 'code' || k === 'text') {
    await fetchText(url);
    if (tooLarge.value) return;
    if (k === 'markdown') {
      await renderMarkdown(rawText.value);
      return;
    }
    // For code/text — try Monaco first when the cached module is
    // already in memory. If not, render the highlight.js placeholder
    // immediately, then attempt to upgrade to Monaco when the import
    // settles.
    const lang = CODE_LANGS[ext(props.file)] || '';
    const monacoCached = getMonaco();
    if (monacoCached) {
      await new Promise<void>((r) => setTimeout(r, 0));
      const ok = await tryMountMonaco(rawText.value, ext(props.file));
      if (!ok) await highlightCode(rawText.value, lang);
    } else {
      // Render highlight.js immediately for read-only colour. Then
      // kick off the Monaco load and swap it in once ready.
      if (k === 'code') await highlightCode(rawText.value, lang);
      ensureMonaco().then(async (m) => {
        if (!m) return;
        if (!props.open) return;
        // Wait one tick so the placeholder DOM exists before swapping.
        await new Promise<void>((r) => setTimeout(r, 0));
        await tryMountMonaco(rawText.value, ext(props.file));
      });
    }
  } else if (k === 'office') {
    await new Promise<void>((r) => setTimeout(r, 0));
    await mountOnlyOfficeEditor();
  }
}

watch(
  () => [props.open, src.value, kind.value, props.openMode] as const,
  ([open, url, k]) => {
    void runOrchestration(open, url, k);
  },
);

// Hand-fire on mount so the standalone Editor.vue route (which mounts
// us with `open` already true) actually runs the orchestration. The
// watcher itself is non-immediate because `immediate: true` would fire
// before the rest of <script setup> finishes, hitting the TDZ on
// `officeEditor`/Monaco state below. Doing this in onMounted+nextTick
// guarantees every `let`/`function` in the file has been hoisted.
onMounted(() => {
  void nextTick(() => {
    if (!props.open) return;
    void runOrchestration(props.open, src.value, kind.value);
  });
});

const codeLanguage = computed(() => CODE_LANGS[ext(props.file)] || 'plaintext');

// --- OnlyOffice DocEditor (real, JWT-signed) ---

const officeEl = ref<HTMLDivElement | null>(null);
const officeError = ref<string | null>(null);
let officeEditor: any = null;

async function mountOnlyOfficeEditor(): Promise<void> {
  officeError.value = null;
  if (!props.file || kind.value !== 'office') return;
  if (!props.onlyOfficeConfigEndpoint) {
    officeError.value = 'OnlyOffice yapılandırması yok';
    return;
  }
  if (!officeEl.value) return;

  try {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (props.authHeaders) Object.assign(headers, props.authHeaders());
    const res = await fetch(props.onlyOfficeConfigEndpoint, {
      method: 'POST',
      headers,
      credentials: props.authCredentials || 'same-origin',
      body: JSON.stringify({
        // Send the FULL adapter-qualified path. The backend resolves
        // `<adapter>://<rel>` against ListEnabledStorages; passing the
        // bare relative path falls back to storages[0] which 404s for
        // anything sitting on a non-primary storage (e.g. s3-test).
        path: props.file.path,
        mode: props.openMode || 'edit',
      }),
    });
    if (!res.ok) {
      throw new Error(`Config fetch ${res.status}: ${(await res.text()).slice(0, 200)}`);
    }
    const { config, documentServerUrl } = (await res.json()) as {
      config: any;
      documentServerUrl: string;
    };

    await loadOnlyOfficeScript(documentServerUrl);
    disposeOnlyOfficeEditor();

    const mountId = 'fe-onlyoffice-mount';
    officeEl.value.id = mountId;

    config.events = {
      onError: (err: any) => {
        officeError.value = formatOnlyOfficeError(err);
      },
    };

    const W = window as any;
    if (!W.DocsAPI || !W.DocsAPI.DocEditor) {
      throw new Error('DocsAPI not available after script load');
    }
    officeEditor = new W.DocsAPI.DocEditor(mountId, config);
  } catch (err) {
    officeError.value = err instanceof Error ? err.message : String(err);
  }
}

function disposeOnlyOfficeEditor(): void {
  try {
    officeEditor?.destroyEditor?.();
  } catch {
    /* ignore */
  }
  officeEditor = null;
}

onBeforeUnmount(() => {
  disposeOnlyOfficeEditor();
  disposeMonaco();
});

/**
 * OnlyOffice fires onError with an event-like object:
 *   { type:'error', data:{ errorCode, errorDescription, ... } }
 * The legacy stringification produced "[object Object]" for objects
 * whose `data` was itself an object. Walk one level deeper so the
 * user sees the actual error description instead of a useless cast.
 */
function formatOnlyOfficeError(err: unknown): string {
  if (typeof err === 'string') return err;
  const e = err as { data?: unknown; message?: unknown; errorDescription?: unknown };
  if (e?.data && typeof e.data === 'object') {
    const d = e.data as { errorDescription?: unknown; errorCode?: unknown; message?: unknown };
    if (typeof d.errorDescription === 'string') return d.errorDescription;
    if (typeof d.message === 'string') return d.message;
    if (d.errorCode !== undefined) return `OnlyOffice error ${d.errorCode}`;
  }
  if (typeof e?.data === 'string') return e.data;
  if (typeof e?.errorDescription === 'string') return e.errorDescription;
  if (typeof e?.message === 'string') return e.message;
  try {
    return JSON.stringify(err);
  } catch {
    return 'OnlyOffice error';
  }
}

const ONLYOFFICE_SCRIPT_ID = 'fe-onlyoffice-api-js';
function loadOnlyOfficeScript(base: string): Promise<void> {
  return new Promise((resolve, reject) => {
    if ((window as any).DocsAPI?.DocEditor) {
      resolve();
      return;
    }
    const existing = document.getElementById(ONLYOFFICE_SCRIPT_ID) as HTMLScriptElement | null;
    if (existing) {
      existing.addEventListener('load', () => resolve());
      existing.addEventListener('error', () => reject(new Error('OnlyOffice api.js load failed')));
      return;
    }
    const script = document.createElement('script');
    script.id = ONLYOFFICE_SCRIPT_ID;
    script.src = `${base.replace(/\/$/, '')}/web-apps/apps/api/documents/api.js`;
    script.async = true;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error('OnlyOffice api.js load failed'));
    document.head.appendChild(script);
  });
}
</script>

<template>
  <Modal :open="open" size="xl" :title="file?.basename || ''" :chromeless="chromeless" :theme="theme" @close="emit('close')">
    <div v-if="file" class="fe-preview">
      <template v-if="kind === 'image'">
        <img :src="src" :alt="file.basename" class="fe-preview__image" />
      </template>
      <template v-else-if="kind === 'video'">
        <video :src="src" controls preload="metadata" class="fe-preview__video" />
      </template>
      <template v-else-if="kind === 'audio'">
        <audio :src="src" controls class="fe-preview__audio" />
      </template>
      <template v-else-if="kind === 'pdf'">
        <object :data="src" type="application/pdf" class="fe-preview__iframe">
          <div class="fe-preview__fallback">
            <span class="fe-preview__fallback-icon">📕</span>
            <p>Tarayıcı PDF'i inline açamadı.</p>
            <a :href="download" class="fe-btn fe-btn--primary" target="_blank" rel="noopener">PDF'i Yeni Sekmede Aç</a>
          </div>
        </object>
      </template>

      <template v-else-if="kind === 'markdown'">
        <div v-if="loading" class="fe-preview__fallback">{{ t('viewer.loading') }}</div>
        <div v-else-if="tooLarge" class="fe-preview__fallback">
          Dosya çok büyük (>1 MB). <a :href="download" class="fe-btn">{{ t('viewer.download') }}</a>
        </div>
        <div
          v-else-if="openMode === 'edit' && saveTextEndpoint"
          class="fe-preview__md-split"
        >
          <div class="fe-preview__md-split-bar">
            <span class="fe-preview__md-split-label">MARKDOWN</span>
            <span v-if="fetchError" class="fe-preview__md-split-error">{{ fetchError }}</span>
            <button
              type="button"
              class="fe-btn fe-btn--primary"
              :disabled="!mdDirty || mdSaving"
              @click="saveMarkdown"
            >
              {{ mdSaving ? 'Kaydediliyor…' : (mdDirty ? 'Kaydet (Ctrl+S)' : 'Kaydedildi') }}
            </button>
          </div>
          <div class="fe-preview__md-split-body">
            <textarea
              class="fe-preview__md-split-input"
              v-model="rawText"
              @input="onMdInput"
              @keydown.ctrl.s.prevent="saveMarkdown"
              @keydown.meta.s.prevent="saveMarkdown"
              spellcheck="false"
              placeholder="# Markdown buraya…"
            />
            <div
              ref="markdownEl"
              class="fe-preview__md-split-output fe-preview__md"
              v-html="markdownHtml"
            ></div>
          </div>
        </div>
        <div v-else-if="fetchError" class="fe-preview__fallback">
          <p>{{ fetchError }}</p>
          <a :href="download" class="fe-btn fe-btn--primary">{{ t('viewer.download') }}</a>
        </div>
        <div v-else-if="markdownHtml" ref="markdownEl" class="fe-preview__md" v-html="markdownHtml"></div>
        <pre v-else class="fe-preview__pre">{{ rawText }}</pre>
      </template>

      <template v-else-if="kind === 'viewer'">
        <div v-if="viewerLoadError && !pdfFallbackToNative" class="fe-preview__fallback">
          <span class="fe-preview__fallback-icon">⚠️</span>
          <p>{{ viewerLoadError }}</p>
          <a :href="download" class="fe-btn fe-btn--primary">{{ t('viewer.download') }}</a>
        </div>
        <div v-else-if="!viewerCmp" class="fe-preview__fallback">
          <span class="fe-preview__fallback-icon">⏳</span>
          <p>{{ t('viewer.loading') }}</p>
        </div>
        <component
          v-else
          :is="viewerCmp"
          v-bind="viewerProps"
          class="fe-preview__viewer"
          @fallback="onPdfFallback"
        />
      </template>

      <template v-else-if="kind === 'code' || kind === 'text'">
        <div v-if="loading" class="fe-preview__fallback">{{ t('viewer.loading') }}</div>
        <div v-else-if="tooLarge" class="fe-preview__fallback">
          Dosya çok büyük (>1 MB). <a :href="download" class="fe-btn">{{ t('viewer.download') }}</a>
        </div>
        <div v-else-if="fetchError" class="fe-preview__fallback">
          <p>{{ fetchError }}</p>
          <a :href="download" class="fe-btn fe-btn--primary">{{ t('viewer.download') }}</a>
        </div>
        <div v-else class="fe-preview__code-wrap">
          <div class="fe-preview__code-toolbar">
            <span class="fe-preview__code-lang">{{ codeLanguage }}</span>
            <span v-if="!monacoReady && codeHtml" class="fe-preview__code-status">
              {{ saveTextEndpoint ? 'Editör yükleniyor…' : 'Salt okunur' }}
            </span>
            <span v-if="saveOk" class="fe-preview__code-status fe-preview__code-status--ok">✓ Kaydedildi</span>
            <span v-if="saveError" class="fe-preview__code-status fe-preview__code-status--err" :title="saveError">✗ Hata</span>
            <span v-if="openMode === 'view'" class="fe-preview__code-status">Salt okunur</span>
            <button
              v-else-if="saveTextEndpoint && monacoReady"
              type="button"
              class="fe-btn fe-btn--primary"
              :disabled="saving"
              @click="saveCode"
            >{{ saving ? 'Kaydediliyor…' : 'Kaydet (Ctrl+S)' }}</button>
          </div>
          <!-- Monaco target — hidden until ready, then occupies the slot. -->
          <div ref="monacoEl" class="fe-preview__code-editor" :class="{ 'is-hidden': !monacoReady }" />
          <!-- Highlight.js read-only fallback — visible until Monaco mounts. -->
          <pre
            v-if="!monacoReady && codeHtml"
            class="fe-preview__pre fe-preview__code hljs"
          ><code :class="`language-${codeLanguage}`" v-html="codeHtml"></code></pre>
          <pre
            v-else-if="!monacoReady && !codeHtml"
            class="fe-preview__pre"
            :data-lang="codeLanguage"
          >{{ rawText }}</pre>
        </div>
      </template>

      <template v-else-if="kind === 'office'">
        <div v-if="officeError" class="fe-preview__fallback">
          <span class="fe-preview__fallback-icon">📄</span>
          <p>{{ officeError }}</p>
          <a :href="download" class="fe-btn fe-btn--primary">İndir</a>
        </div>
        <div v-else ref="officeEl" class="fe-preview__office" />
      </template>

      <template v-else>
        <div class="fe-preview__fallback">
          <span class="fe-preview__fallback-icon">📎</span>
          <p>Bu dosya tipi için önizleme henüz yok.</p>
          <a :href="download" class="fe-btn fe-btn--primary">İndir</a>
        </div>
      </template>
    </div>
    <template #actions>
      <button
        v-if="file && openMode === 'view' && canEditKind"
        type="button"
        class="fe-btn fe-btn--primary"
        @click="openEditInNewTab"
      >✏ Düzenle</button>
      <button
        v-if="file"
        type="button"
        class="fe-btn"
        @click="openInNewTab"
      >↗ {{ t('viewer.open_in_new_tab') }}</button>
      <a v-if="file" :href="download" class="fe-btn">{{ t('viewer.download') }}</a>
      <button type="button" class="fe-btn" @click="emit('close')">Kapat</button>
    </template>
  </Modal>
</template>

<style>
.fe-preview__code-editor.is-hidden {
  display: none;
}
/* Wrapper for the dynamic viewers — they render their own toolbars
 * inside the wrapper, so we just give them the full pane height. */
.fe-preview__viewer {
  width: 100%;
  height: 100%;
  min-height: 70vh;
  display: flex;
  flex-direction: column;
}
.fe-preview .fe-preview__viewer {
  align-self: stretch;
}
.fe-preview__md-mermaid {
  display: flex;
  justify-content: center;
  margin: 16px 0;
}
.fe-preview__md-mermaid svg {
  max-width: 100%;
  height: auto;
}
.fe-preview__md-math {
  margin: 16px 0;
  text-align: center;
  overflow-x: auto;
}
</style>
