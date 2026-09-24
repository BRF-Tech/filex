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
 *   anything else                 → "Download" fallback
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
import StarButton from '../components/StarButton.vue';
import { ensureMonaco, getMonaco, ensureHighlight } from '../composables/useMonacoLoader';
import { useLocale } from '../composables/useLocale';
import { browserProbeURL } from '../lib/externalReach';
import { fileIconTile } from '../lib/fileIcons';
import { actionIconSvg } from '../lib/actionIcons';
import { OFFICE_EXTS } from '../lib/serviceGate';
import { requestFailure, sayFailure } from '../lib/errorWords';

const props = defineProps<{
  open: boolean;
  locale: LocaleCode;
  file: FileNode | null;
  previewUrl: (path: string) => string;
  downloadUrl: (path: string) => string;
  onlyOfficeBase?: string | null;
  onlyOfficeConfigEndpoint?: string | null;
  /**
   * Could this person set up a missing service (`capabilities.caller_admin`)?
   * Only changes WHICH sentence a missing document server gets: an
   * administrator is told where to set it up, everybody else that it is not
   * available here and how to get the file anyway.
   */
  canConfigure?: boolean;
  saveTextEndpoint?: string | null;
  /** Endpoint for the archive (zip/rar/7z/tar) member list. Forwarded to
   *  ArchiveViewer, which otherwise assumes a same-origin path. */
  archiveListEndpoint?: string | null;
  openMode?: 'edit' | 'view';
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  authCredentials?: RequestCredentials;
  /** New rich-viewer config (forwarded by FileExplorer from ExplorerConfig). */
  drawioUrl?: string | null;
  pdfWorkerUrl?: string | null;
  pdfSaveUrl?: string | null;
  /** Standalone full-screen viewer route — `?path=…&storage=…&type=…`. */
  viewerBaseUrl?: string | null;
  /* wiring:e2 — false hides the "Edit" / "Open in New Tab" buttons.
   * The standalone route fetches RAW server bytes, which inside an
   * E2E-encrypted folder is ciphertext (and its save path could clobber
   * the encrypted blob) — the host disables the escape hatches there. */
  newTabEnabled?: boolean;
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
  /* === gorunum:v1-viewer — the full-bleed overlay's own contract ===
   * Everything below is OPTIONAL and answered by the host, because the
   * modal cannot invent any of it: it is handed ONE file and has no idea
   * what list that file came out of, nor where the API lives. */
  /** 1-based position of `file` in the host's current file list. */
  index?: number;
  /** How many files that list holds. `index` + `total` draw "1 of 9";
   *  with either missing the counter is simply absent — a counter that
   *  guesses is worse than no counter. */
  total?: number;
  /** Draw the prev/next chevrons even when index/total are unknown.
   *  QuickLook sets it because its host already answers `nav`. */
  navEnabled?: boolean;
  /** API origin for the star toggle — same meaning as ExplorerConfig.apiBase.
   *  Omitted = same origin, which is what the admin UI wants. */
  apiBase?: string;
  /** Draw the share action. Off by default on purpose: a share button with
   *  no host listening to `@share` is a control that lies. */
  shareEnabled?: boolean;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  /** Previous / next file. −1 and +1, the SAME contract QuickLook already
   *  emits upward — the chevrons are a second trigger for it, not a second
   *  navigation mechanism. The host keeps the `file` prop in sync. */
  (e: 'nav', delta: number): void;
  /** The host opens its own share dialog. Only reachable when `shareEnabled`. */
  (e: 'share'): void;
  /** The star toggle succeeded — so the host can update its listing row. */
  (e: 'starred', value: boolean): void;
}>();

const { t, formatSize, formatDate, nodeDisplayName } = useLocale(() => props.locale);

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
/* The document server's formats — ONE list, shared with the explorer's menu
 * (lib/serviceGate), so a type previewed as "office" is exactly a type whose
 * Open needs ONLYOFFICE. */
const OFFICE = OFFICE_EXTS;

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
    viewerLoadError.value = said(err, 'err.viewer_failed').text;
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
    /* The viewer's language as a code, for the one thing `t` cannot give it:
       how a NUMBER is written (a size in the archive list). */
    locale: props.locale,
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
      if (props.archiveListEndpoint) base.archiveListUrl = props.archiveListEndpoint;
    }
  }
  if (e === 'drawio' || e === 'dio') {
    base.drawioUrl = props.drawioUrl ?? undefined;
    base.canConfigure = props.canConfigure === true;
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
 * (image/video/audio/3D/archive) don't surface an "Edit" button.
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

/**
 * The Edit button of a document whose editor is an optional service that is
 * not there — ONLYOFFICE for office files, draw.io for diagrams. It led to a
 * tab that could only say so. The owner's rule (lib/serviceGate): greyed with
 * where to set it up for someone who can, not offered to anybody else.
 */
const editNeeds = computed<'' | 'onlyoffice' | 'drawio'>(() => {
  const e = ext(props.file);
  if (OFFICE.includes(e) && !props.onlyOfficeBase) return 'onlyoffice';
  if ((e === 'drawio' || e === 'dio') && !props.drawioUrl) return 'drawio';
  return '';
});
const editTitle = computed(() =>
  editNeeds.value === 'onlyoffice'
    ? t('ctx.needs_onlyoffice')
    : editNeeds.value === 'drawio'
      ? t('ctx.needs_drawio')
      : t('viewer.edit'),
);

// Keep adapter prefix so backend resolves the right storage — stripping
// it defaults to storages[0] and 404s on any non-default adapter.
const src = computed(() => (props.file ? props.previewUrl(props.file.path) : ''));
const download = computed(() => (props.file ? props.downloadUrl(props.file.path) : ''));

function stripAdapter(p: string): string {
  const idx = p.indexOf('://');
  return idx === -1 ? p : p.slice(idx + 3);
}

/* === gorunum:v1-viewer — full-bleed overlay chrome ===
 *
 * The card modal (title bar + Download/Close footer) is gone; what replaces
 * it is one bar across the top, a chevron on each screen edge and a floating
 * zoom pill. `chromeless` is untouched: the standalone /files/edit route still
 * gets the bare viewer with no chrome at all, which is the whole point of that
 * flag — the browser tab is its container.
 */

/** Chrome is drawn for the in-page overlay only. */
const showChrome = computed(() => !props.chromeless);

const viewerEl = ref<HTMLElement | null>(null);

const tileHtml = computed(() => (props.file ? fileIconTile(props.file) : ''));
const displayName = computed(() => (props.file ? nodeDisplayName(props.file) : ''));

/** "1 of 9" — only when the host answered BOTH halves. */
const counterText = computed(() => {
  const i = props.index;
  const n = props.total;
  if (!i || !n || n < 1 || i < 1) return '';
  return t('viewer.counter', { i, n });
});

/** `246.3 KB • Sep 9, 2026 • 1 of 9`, with any unknown part left out
 *  rather than printed as a placeholder. */
const metaLine = computed(() => {
  const f = props.file;
  if (!f) return '';
  const parts: string[] = [];
  if (typeof f.size === 'number' && f.type === 'file') parts.push(formatSize(f.size));
  const when = formatDate(f.last_modified);
  if (when) parts.push(when);
  if (counterText.value) parts.push(counterText.value);
  return parts.join(' • ');
});

/** The chevrons appear only where prev/next actually goes somewhere. */
const canNav = computed(
  () => showChrome.value && (props.navEnabled === true || (props.total ?? 0) > 1),
);

/** Starring is the real thing, not a second implementation: StarButton +
 *  lib/star.ts, the same pair the listing rows use. It needs the DB node id,
 *  which client-synthesized rows do not carry — no id, no button. */
const starNodeId = computed(() =>
  typeof props.file?.id === 'number' ? (props.file.id as number) : null,
);

/**
 * Which kinds get the stage stretched instead of centred. Editors and
 * document surfaces want every pixel; a photo wants to sit in the middle
 * of the ground with room around it.
 */
const FILL_KINDS = new Set(['pdf', 'markdown', 'code', 'text', 'office', 'viewer']);
const stageModifier = computed(() =>
  FILL_KINDS.has(kind.value) ? 'fe-preview--fill' : 'fe-preview--center',
);

/* --- Zoom (image only) ----------------------------------------------
 *
 * ⚠ The pill is hidden for pdf / office / drawio. Those three paint their
 * OWN zoom control inside their surface, and a second one wired to nothing
 * is a control that lies — worse than no control, because the user reads a
 * percentage that the thing on screen does not obey.
 */
const ZOOM_STEPS = [0.25, 0.5, 0.75, 1, 1.25, 1.5, 2, 3, 4];
const zoom = ref(1);
const imgEl = ref<HTMLImageElement | null>(null);
/** The width the image settles at when it is fit to the stage (zoom = 1).
 *  Everything else is a multiple of it, which is what makes "100%" mean
 *  "the size you first saw" rather than "the pixel size of the file". */
const fitWidth = ref(0);

const canZoom = computed(() => showChrome.value && kind.value === 'image');

function measureFit(): void {
  if (zoom.value !== 1 || !imgEl.value) return;
  const w = imgEl.value.getBoundingClientRect().width;
  if (w > 0) fitWidth.value = w;
}

const imageStyle = computed(() => {
  if (zoom.value === 1 || !fitWidth.value) return undefined;
  return {
    width: `${Math.round(fitWidth.value * zoom.value)}px`,
    height: 'auto',
    maxWidth: 'none',
    maxHeight: 'none',
  } as Record<string, string>;
});

function stepZoom(dir: 1 | -1): void {
  const cur = zoom.value;
  if (dir > 0) {
    zoom.value = ZOOM_STEPS.find((s) => s > cur + 1e-6) ?? ZOOM_STEPS[ZOOM_STEPS.length - 1];
  } else {
    const lower = ZOOM_STEPS.filter((s) => s < cur - 1e-6);
    zoom.value = lower.length ? lower[lower.length - 1] : ZOOM_STEPS[0];
  }
}

function resetZoom(): void {
  zoom.value = 1;
  void nextTick(measureFit);
}

const zoomLabel = computed(() => `${Math.round(zoom.value * 100)}%`);
const canZoomIn = computed(() => zoom.value < ZOOM_STEPS[ZOOM_STEPS.length - 1] - 1e-6);
const canZoomOut = computed(() => zoom.value > ZOOM_STEPS[0] + 1e-6);

/* --- Fullscreen ------------------------------------------------------ */
const isFullscreen = ref(false);

function onFullscreenChange(): void {
  isFullscreen.value = !!document.fullscreenElement;
}

function toggleFullscreen(): void {
  try {
    if (document.fullscreenElement) {
      void document.exitFullscreen?.();
    } else {
      void viewerEl.value?.requestFullscreen?.();
    }
  } catch {
    /* a browser that refuses fullscreen just leaves the overlay as it is */
  }
}

/**
 * The overlay has no backdrop ring to click — the card IS the viewport — so
 * the ground around the content takes that job. `.self` is what keeps it
 * honest: it only fires when the click landed on the ground itself, never on
 * an editor, an iframe or the image.
 */
function onGroundClick(): void {
  if (!showChrome.value) return;
  emit('close');
}

const loading = ref(false);
const fetchError = ref<string | null>(null);
/** An administrator's second line under `fetchError` (lib/errorWords). */
const fetchErrorDetail = ref<string | null>(null);

/**
 * A failure, said (lib/errorWords): the sentence for everybody, the raw words
 * as a second line only for a caller who can administer the instance.
 *
 * ⚠⚠ This modal used to print what it caught: "save failed: 500 {…}",
 * "404 Not Found", "Monaco mount fail: …" — a status code, a JSON body and a
 * library's message, to whoever was editing (QA, 2026-09-21).
 */
function said(err: unknown, fallbackKey: string) {
  return sayFailure(err, t(fallbackKey), { callerAdmin: props.canConfigure === true });
}
function showFetchError(f: { text: string; detail?: string }): void {
  fetchError.value = f.text;
  fetchErrorDetail.value = f.detail ?? null;
}
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
  // Autosave 1.5s after last keystroke — the manual Save button + Ctrl+S
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
  fetchErrorDetail.value = null;
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
      throw requestFailure(res.status, await res.text().catch(() => ''), props.locale);
    }
    mdDirty.value = false;
  } catch (err) {
    showFetchError(said(err, 'err.save_failed'));
  } finally {
    mdSaving.value = false;
  }
}

async function fetchText(url: string): Promise<void> {
  loading.value = true;
  fetchError.value = null;
  fetchErrorDetail.value = null;
  rawText.value = '';
  tooLarge.value = false;
  try {
    const headers: Record<string, string> = {};
    if (props.authHeaders) Object.assign(headers, await props.authHeaders());
    const res = await fetch(url, {
      credentials: props.authCredentials || 'same-origin',
      headers,
    });
    if (!res.ok) {
      throw requestFailure(res.status, await res.text().catch(() => ''), props.locale);
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
    showFetchError(said(err, 'err.load_failed'));
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
/**
 * The renderer could not be loaded at all.
 *
 * ⚠ Tracked separately from "nothing to render". In view mode a missing
 * renderer falls through to the raw source, which is honest; in the split
 * editor the preview pane simply stayed BLANK — a white half-window with no
 * explanation, measured in the desktop app on 2026-08-10.
 */
const markdownUnavailable = ref(false);

async function renderMarkdown(text: string): Promise<void> {
  try {
    const mod = (await import(/* @vite-ignore */ 'markdown-it').catch(() => null)) as any;
    if (!mod) {
      markdownHtml.value = '';
      markdownUnavailable.value = true;
      return;
    }
    markdownUnavailable.value = false;
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
    showFetchError(said(err, 'err.load_failed'));
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
    showFetchError(said(err, 'err.load_failed'));
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
/** Monaco resolved to nothing — the peer is not in this build at all. */
const monacoUnavailable = ref(false);
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
  // ⚠ "Not installed" is not "still loading". Monaco is an optional peer that
  // most embeds do not ship, and the toolbar label was gated only on "not ready
  // yet" — so a bundle without it showed "Editor loading…" forever, over a
  // perfectly good read-only view. Measured in the desktop app once syntax
  // highlighting started working and that label finally had something to sit on.
  if (!monaco) monacoUnavailable.value = true;
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
    showFetchError(said(err, 'err.viewer_failed'));
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
    if (props.authHeaders) Object.assign(headers, await props.authHeaders());
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
      throw requestFailure(res.status, await res.text().catch(() => ''), props.locale);
    }
    saveOk.value = true;
    setTimeout(() => {
      saveOk.value = false;
    }, 2500);
  } catch (err) {
    const f = said(err, 'err.save_failed');
    saveError.value = f.detail ? `${f.text}\n${f.detail}` : f.text;
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
  fetchErrorDetail.value = null;
  officeError.value = null;
  viewerCmp.value = null;
  viewerLoadError.value = null;
  pdfFallbackToNative.value = false;
  /* gorunum:v1-viewer — a new file starts at 100%. Carrying 400% over from
   * the previous photo means the next one opens mid-crop with no hint why. */
  zoom.value = 1;
  fitWidth.value = 0;
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
        // ⚠ Record the "never coming" case here too. This branch returns early
        // without ever reaching tryMountMonaco, so a build with no Monaco left
        // the toolbar saying "Editor loading…" indefinitely.
        if (!m) { monacoUnavailable.value = true; return; }
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
  document.addEventListener('fullscreenchange', onFullscreenChange);
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

/** "No document server here", in the sentence this person can act on. */
function officeUnconfigured(): string {
  return t(props.canConfigure ? 'viewer.office_unconfigured_admin' : 'viewer.office_unconfigured');
}

/**
 * ⚠⚠ Every failure below becomes a SENTENCE, never the HTTP layer. This
 * function used to throw `Config fetch ${status}: ${body}` and put it on
 * screen, which is how a person clicking "Open" on a .docx read
 * `Config fetch 503: {"error":"onlyoffice not configured"}` (owner,
 * 2026-09-21: "böyle hata vermek yerine … düzgün hata mesajı vermemiz
 * lazım"). The status still reaches the console for whoever is debugging it.
 */
async function officeConfigError(res: Response): Promise<string> {
  let body = '';
  try {
    body = (await res.text()).slice(0, 200);
  } catch {
    /* nothing to read */
  }
  console.warn('[filex] ONLYOFFICE config request failed', res.status, body);
  if (res.status === 503 && /not configured/i.test(body)) return officeUnconfigured();
  if (res.status === 403) return t('viewer.office_forbidden');
  return t('viewer.office_failed');
}

async function mountOnlyOfficeEditor(): Promise<void> {
  officeError.value = null;
  if (!props.file || kind.value !== 'office') return;
  /* ⚠ BOTH halves. The standalone /files/edit route always handed over the
   * config ENDPOINT and left the base null when the capabilities probe said
   * the service was off — so this check, reading the endpoint alone, let the
   * request go out and put the server's 503 on screen. No base means no
   * document server, whatever else is configured. */
  if (!props.onlyOfficeConfigEndpoint || !props.onlyOfficeBase) {
    officeError.value = officeUnconfigured();
    return;
  }
  if (!officeEl.value) return;

  try {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (props.authHeaders) Object.assign(headers, await props.authHeaders());
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
      officeError.value = await officeConfigError(res);
      return;
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
    /* The document server's script did not load, or did not define its API:
       the server is configured but not answering. The detail is for the
       console, the sentence for the person. */
    console.warn('[filex] ONLYOFFICE did not start', err);
    officeError.value = t(props.canConfigure ? 'viewer.office_unreachable_admin' : 'viewer.office_unreachable');
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
  document.removeEventListener('fullscreenchange', onFullscreenChange);
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
    // Same URL the admin page's browser probe attempts, from the same
    // helper — a probe that tested a different address than the editor loads
    // would be the old lie wearing a new badge.
    script.src = browserProbeURL('onlyoffice', base);
    script.async = true;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error('OnlyOffice api.js load failed'));
    document.head.appendChild(script);
  });
}
</script>

<template>
  <Modal
    :open="open"
    size="xl"
    :title="file?.basename || ''"
    :chromeless="chromeless"
    :fullbleed="!chromeless"
    :theme="theme"
    @close="emit('close')"
  >
    <!-- === gorunum:v1-viewer ===
         A full-bleed overlay, not a card: one bar across the top, a chevron on
         each screen edge, a floating zoom pill at the bottom. `chromeless`
         renders the viewer BARE (no bar, no chevrons, no pill) because the
         standalone /files/edit route's container is the browser tab itself. -->
    <div ref="viewerEl" class="fe-viewer" :class="{ 'fe-viewer--bare': chromeless }">
      <header v-if="showChrome && file" class="fe-viewer__bar">
        <div class="fe-viewer__ident">
          <span class="fe-viewer__tile" aria-hidden="true" v-html="tileHtml"></span>
          <span class="fe-viewer__idcol">
            <span class="fe-viewer__name" :title="file.path">{{ displayName }}</span>
            <span v-if="metaLine" class="fe-viewer__meta">{{ metaLine }}</span>
          </span>
        </div>

        <div class="fe-viewer__acts">
          <a
            :href="download"
            class="fe-viewer__act"
            :title="t('viewer.download')"
            :aria-label="t('viewer.download')"
          ><span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('download')"></span></a>
          <button
            v-if="shareEnabled"
            type="button"
            class="fe-viewer__act"
            :title="t('viewer.share')"
            :aria-label="t('viewer.share')"
            @click="emit('share')"
          ><span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('access')"></span></button>
          <!-- The listing's StarButton, not a copy of it: same component, same
               lib/star.ts request, same optimistic rollback. -->
          <StarButton
            v-if="starNodeId !== null"
            class="fe-viewer__act fe-viewer__act--star"
            :starred="file.starred === true"
            :node-id="starNodeId"
            :api-base="apiBase"
            :auth-headers="authHeaders"
            :auth-credentials="authCredentials"
            :locale="locale"
            compact
            @change="(v: boolean) => emit('starred', v)"
          />
          <button
            v-if="openMode === 'view' && canEditKind && newTabEnabled !== false /* wiring:e2 */ && (!editNeeds || canConfigure)"
            type="button"
            class="fe-viewer__act"
            :disabled="!!editNeeds"
            :title="editTitle"
            :aria-label="editTitle"
            @click="openEditInNewTab"
          ><span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('rename')"></span></button>
          <button
            v-if="newTabEnabled !== false /* wiring:e2 */"
            type="button"
            class="fe-viewer__act"
            :title="t('viewer.open_in_new_tab')"
            :aria-label="t('viewer.open_in_new_tab')"
            @click="openInNewTab"
          ><span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('open-tab')"></span></button>
        </div>

        <div class="fe-viewer__tail">
          <button
            type="button"
            class="fe-viewer__act fe-viewer__close"
            :title="t('viewer.close')"
            :aria-label="t('viewer.close')"
            @click="emit('close')"
          ><span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('close')"></span></button>
        </div>
      </header>

      <div class="fe-viewer__stage" @click.self="onGroundClick">
        <div v-if="file" class="fe-preview" :class="stageModifier" @click.self="onGroundClick">
          <template v-if="kind === 'image'">
            <img
              ref="imgEl"
              :src="src"
              :alt="file.basename"
              class="fe-preview__image"
              :style="imageStyle"
              @load="measureFit"
            />
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
                <!-- ikon:emoji — the file's own tile, the one the row behind
                     this overlay is wearing. -->
                <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons -->
                <span class="fe-preview__fallback-icon" aria-hidden="true" v-html="tileHtml"></span>
                <p>{{ t('viewer.pdf_inline_failed') }}</p>
                <a :href="download" class="fe-btn fe-btn--primary" target="_blank" rel="noopener">{{ t('viewer.open_in_new_tab') }}</a>
              </div>
            </object>
          </template>

          <template v-else-if="kind === 'markdown'">
            <div v-if="loading" class="fe-preview__fallback">{{ t('viewer.loading') }}</div>
            <div v-else-if="tooLarge" class="fe-preview__fallback">
              {{ t('viewer.too_large') }} <a :href="download" class="fe-btn">{{ t('viewer.download') }}</a>
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
                  {{ mdSaving ? t('viewer.saving') : (mdDirty ? t('viewer.save') : t('viewer.saved')) }}
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
                  :placeholder="t('viewer.md_placeholder')"
                />
                <div
                  v-if="markdownUnavailable"
                  class="fe-preview__md-split-output fe-preview__md"
                >{{ t('viewer.peer_not_installed') }}</div>
                <div
                  v-else
                  ref="markdownEl"
                  class="fe-preview__md-split-output fe-preview__md"
                  dir="auto"
                  v-html="markdownHtml"
                ></div>
              </div>
            </div>
            <div v-else-if="fetchError" class="fe-preview__fallback">
              <p>{{ fetchError }}</p>
              <p v-if="fetchErrorDetail" class="fe-preview__error-detail" data-testid="preview-error-detail">{{ fetchErrorDetail }}</p>
              <a :href="download" class="fe-btn fe-btn--primary">{{ t('viewer.download') }}</a>
            </div>
            <!-- ⚠ RTL: a document reads in ITS direction, not the interface's —
                 `auto` lets an English README stay left to right in Arabic. -->
            <div v-else-if="markdownHtml" ref="markdownEl" class="fe-preview__md" dir="auto" v-html="markdownHtml"></div>
            <pre v-else class="fe-preview__pre">{{ rawText }}</pre>
          </template>

          <template v-else-if="kind === 'viewer'">
            <div v-if="viewerLoadError && !pdfFallbackToNative" class="fe-preview__fallback">
              <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
              <span
                class="fe-preview__fallback-icon fe-preview__fallback-icon--alert"
                aria-hidden="true"
                v-html="actionIconSvg('alert')"
              ></span>
              <p>{{ viewerLoadError }}</p>
              <a :href="download" class="fe-btn fe-btn--primary">{{ t('viewer.download') }}</a>
            </div>
            <div v-else-if="!viewerCmp" class="fe-preview__fallback">
              <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
              <span
                class="fe-preview__fallback-icon fe-preview__fallback-icon--spin"
                aria-hidden="true"
                v-html="actionIconSvg('progress')"
              ></span>
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
              {{ t('viewer.too_large') }} <a :href="download" class="fe-btn">{{ t('viewer.download') }}</a>
            </div>
            <div v-else-if="fetchError" class="fe-preview__fallback">
              <p>{{ fetchError }}</p>
              <p v-if="fetchErrorDetail" class="fe-preview__error-detail" data-testid="preview-error-detail">{{ fetchErrorDetail }}</p>
              <a :href="download" class="fe-btn fe-btn--primary">{{ t('viewer.download') }}</a>
            </div>
            <div v-else class="fe-preview__code-wrap">
              <div class="fe-preview__code-toolbar">
                <!-- ⚠ lang="en". This badge holds a highlight.js language ID
                     ("typescript", "ini", "nginx") — a technical token, never
                     translated prose — and the CSS uppercases it. CSS
                     text-transform is LOCALE-AWARE: under the Turkish UI the
                     document is lang="tr", so "typescript" uppercased to
                     "TYPESCRİPT" with a dotted İ (measured 2026-09-13). The
                     sibling uppercase labels (.fe-sidenav__heading and friends)
                     hold real Turkish words where "ETİKETLER" is exactly right,
                     so the transform stays and only this one token opts out. -->
                <span class="fe-preview__code-lang" lang="en">{{ codeLanguage }}</span>
                <span v-if="!monacoReady && codeHtml" class="fe-preview__code-status">
                  {{ saveTextEndpoint && !monacoUnavailable ? t('viewer.editor_loading') : t('viewer.read_only') }}
                </span>
                <span v-if="saveOk" class="fe-preview__code-status fe-preview__code-status--ok">
                  <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
                  <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('check')"></span>
                  {{ t('viewer.saved') }}
                </span>
                <span v-if="saveError" class="fe-preview__code-status fe-preview__code-status--err" :title="saveError">
                  <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/actionIcons -->
                  <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('close')"></span>
                  {{ t('viewer.save_error') }}
                </span>
                <span v-if="openMode === 'view'" class="fe-preview__code-status">{{ t('viewer.read_only') }}</span>
                <button
                  v-else-if="saveTextEndpoint && monacoReady"
                  type="button"
                  class="fe-btn fe-btn--primary"
                  :disabled="saving"
                  @click="saveCode"
                >{{ saving ? t('viewer.saving') : t('viewer.save') }}</button>
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
            <div v-if="officeError" class="fe-preview__fallback" data-testid="office-fallback">
              <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons -->
              <span class="fe-preview__fallback-icon" aria-hidden="true" v-html="tileHtml"></span>
              <p>{{ officeError }}</p>
              <a :href="download" class="fe-btn fe-btn--primary">{{ t('viewer.download') }}</a>
            </div>
            <div v-else ref="officeEl" class="fe-preview__office" />
          </template>

          <template v-else>
            <div class="fe-preview__fallback">
              <!-- eslint-disable-next-line vue/no-v-html -- static markup from lib/fileIcons -->
              <span class="fe-preview__fallback-icon" aria-hidden="true" v-html="tileHtml"></span>
              <p>{{ t('viewer.no_preview') }}</p>
              <a :href="download" class="fe-btn fe-btn--primary">{{ t('viewer.download') }}</a>
            </div>
          </template>
        </div>
      </div>

      <button
        v-if="canNav"
        type="button"
        class="fe-viewer__chev fe-viewer__chev--prev"
        :title="t('viewer.nav_prev')"
        :aria-label="t('viewer.nav_prev')"
        @click="emit('nav', -1)"
      >
        <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor"
             stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"
             aria-hidden="true" focusable="false"><path d="M15 4.5L7.5 12 15 19.5" /></svg>
      </button>
      <button
        v-if="canNav"
        type="button"
        class="fe-viewer__chev fe-viewer__chev--next"
        :title="t('viewer.nav_next')"
        :aria-label="t('viewer.nav_next')"
        @click="emit('nav', 1)"
      >
        <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor"
             stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"
             aria-hidden="true" focusable="false"><path d="M9 4.5L16.5 12 9 19.5" /></svg>
      </button>

      <!-- Zoom pill. Shown for the image viewer only - pdf, office and drawio
           paint their own zoom inside their surface, and a second percentage
           that the content does not obey is a control that lies. -->
      <div v-if="canZoom" class="fe-viewer__zoom">
        <button
          type="button"
          class="fe-viewer__zoom-btn"
          :disabled="!canZoomOut"
          :title="t('viewer.zoom_out')"
          :aria-label="t('viewer.zoom_out')"
          @click="stepZoom(-1)"
        >&#8722;</button>
        <button
          type="button"
          class="fe-viewer__zoom-level"
          :title="t('viewer.zoom_reset')"
          :aria-label="t('viewer.zoom_reset')"
          @click="resetZoom"
        >{{ zoomLabel }}</button>
        <button
          type="button"
          class="fe-viewer__zoom-btn"
          :disabled="!canZoomIn"
          :title="t('viewer.zoom_in')"
          :aria-label="t('viewer.zoom_in')"
          @click="stepZoom(1)"
        >+</button>
        <span class="fe-viewer__zoom-sep" aria-hidden="true"></span>
        <button
          type="button"
          class="fe-viewer__zoom-btn"
          :title="isFullscreen ? t('viewer.exit_fullscreen') : t('viewer.fullscreen')"
          :aria-label="isFullscreen ? t('viewer.exit_fullscreen') : t('viewer.fullscreen')"
          @click="toggleFullscreen"
        >
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor"
               stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"
               aria-hidden="true" focusable="false">
            <template v-if="isFullscreen">
              <path d="M9.5 4.5v5h-5" /><path d="M14.5 4.5v5h5" />
              <path d="M9.5 19.5v-5h-5" /><path d="M14.5 19.5v-5h5" />
            </template>
            <template v-else>
              <path d="M4.5 9.5v-5h5" /><path d="M19.5 9.5v-5h-5" />
              <path d="M4.5 14.5v5h5" /><path d="M19.5 14.5v5h-5" />
            </template>
          </svg>
        </button>
      </div>
    </div>
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
