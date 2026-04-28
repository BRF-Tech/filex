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
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import type { FileNode } from '../types/FileNode';
import type { LocaleCode } from '../types/ExplorerConfig';
import Modal from './Modal.vue';
import { ensureMonaco, getMonaco, ensureHighlight } from '../composables/useMonacoLoader';

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
}>();

const emit = defineEmits<{
  (e: 'close'): void;
}>();

void props.locale; // locale ref reserved for future labels in the preview chrome

function ext(f: FileNode | null): string {
  return (f?.extension || '').toLowerCase();
}

const IMAGE = ['jpg', 'jpeg', 'png', 'webp', 'gif', 'bmp', 'avif', 'svg', 'heic'];
const VIDEO = ['mp4', 'webm', 'mov', 'mkv', 'm4v', 'ogv'];
const AUDIO = ['mp3', 'wav', 'ogg', 'flac', 'm4a', 'aac', 'opus'];

const CODE_LANGS: Record<string, string> = {
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
const TEXT_PLAIN = ['txt', 'log', 'csv', 'tsv'];
const OFFICE = ['docx', 'doc', 'xlsx', 'xls', 'pptx', 'ppt', 'odt', 'ods', 'odp', 'rtf'];

type PreviewKind = 'image' | 'video' | 'audio' | 'pdf' | 'markdown' | 'code' | 'office' | 'text' | 'other';

const kind = computed<PreviewKind>(() => {
  const e = ext(props.file);
  if (!e) return 'other';
  if (IMAGE.includes(e)) return 'image';
  if (VIDEO.includes(e)) return 'video';
  if (AUDIO.includes(e)) return 'audio';
  if (e === 'pdf') return 'pdf';
  if (e === 'md' || e === 'markdown') return 'markdown';
  if (e in CODE_LANGS) return 'code';
  if (OFFICE.includes(e)) return 'office';
  if (TEXT_PLAIN.includes(e)) return 'text';
  return 'other';
});

const src = computed(() => (props.file ? props.previewUrl(stripAdapter(props.file.path)) : ''));
const download = computed(() => (props.file ? props.downloadUrl(stripAdapter(props.file.path)) : ''));

function stripAdapter(p: string): string {
  const idx = p.indexOf('://');
  return idx === -1 ? p : p.slice(idx + 3);
}

const loading = ref(false);
const fetchError = ref<string | null>(null);
const rawText = ref<string>('');
const MAX_TEXT_BYTES = 1_000_000;
const tooLarge = ref(false);

async function fetchText(url: string): Promise<void> {
  loading.value = true;
  fetchError.value = null;
  rawText.value = '';
  tooLarge.value = false;
  try {
    const res = await fetch(url, { credentials: 'include' });
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

const markdownHtml = ref<string>('');

async function renderMarkdown(text: string): Promise<void> {
  try {
    const mod = (await import(/* @vite-ignore */ 'markdown-it').catch(() => null)) as any;
    if (!mod) {
      markdownHtml.value = '';
      return;
    }
    const Md = (mod as { default: any }).default ?? (mod as any);
    const md = new Md({
      html: false,
      linkify: true,
      breaks: true,
      typographer: true,
    });
    markdownHtml.value = md.render(text);
  } catch (err) {
    markdownHtml.value = '';
    fetchError.value = err instanceof Error ? err.message : String(err);
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
    const dark = window.matchMedia?.('(prefers-color-scheme: dark)').matches;
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

watch(
  () => [props.open, src.value, kind.value, props.openMode] as const,
  async ([open, url, k]) => {
    rawText.value = '';
    markdownHtml.value = '';
    codeHtml.value = '';
    fetchError.value = null;
    officeError.value = null;
    disposeOnlyOfficeEditor();
    disposeMonaco();
    if (!open || !url) return;
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
  },
);

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
        path: stripAdapter(props.file.path),
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
        officeError.value = String(err?.data ?? err?.message ?? err);
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
  <Modal :open="open" size="xl" :title="file?.basename || ''" @close="emit('close')">
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
        <div v-if="loading" class="fe-preview__fallback">Yükleniyor…</div>
        <div v-else-if="tooLarge" class="fe-preview__fallback">
          Dosya çok büyük (>1 MB). <a :href="download" class="fe-btn">İndir</a>
        </div>
        <div v-else-if="fetchError" class="fe-preview__fallback">
          <p>{{ fetchError }}</p>
          <a :href="download" class="fe-btn fe-btn--primary">İndir</a>
        </div>
        <div v-else-if="markdownHtml" class="fe-preview__md" v-html="markdownHtml"></div>
        <pre v-else class="fe-preview__pre">{{ rawText }}</pre>
      </template>

      <template v-else-if="kind === 'code' || kind === 'text'">
        <div v-if="loading" class="fe-preview__fallback">Yükleniyor…</div>
        <div v-else-if="tooLarge" class="fe-preview__fallback">
          Dosya çok büyük (>1 MB). <a :href="download" class="fe-btn">İndir</a>
        </div>
        <div v-else-if="fetchError" class="fe-preview__fallback">
          <p>{{ fetchError }}</p>
          <a :href="download" class="fe-btn fe-btn--primary">İndir</a>
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
      <a v-if="file" :href="download" class="fe-btn">İndir</a>
      <button type="button" class="fe-btn" @click="emit('close')">Kapat</button>
    </template>
  </Modal>
</template>

<style>
.fe-preview__code-editor.is-hidden {
  display: none;
}
</style>
