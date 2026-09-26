<script setup lang="ts">
/**
 * ConvertModal — universal file converter via a hidden, embedded iframe to
 * the p2r3/convert fork running at `convertUrl` (loaded with `?embed=1`).
 *
 * The iframe performs the actual conversion in-browser (WASM handlers); we
 * drive it headlessly over postMessage (protocol defined in the fork's
 * `setupEmbedBridge`):
 *   1. iframe → { source:'convert-embed', event:'ready' }
 *   2. we    → { target:'convert-embed', cmd:'listFormats', id }
 *      iframe→ { source, id, ok, formats:[{index,ext,format,mime,name,from,to}] }
 *   3. user searches + picks a target format
 *   4. we fetch the source bytes, → { cmd:'convert', name, bytes, fromIndex, toIndex }
 *      iframe→ { ok, name, ext, bytes:ArrayBuffer }
 *   5. we wrap the bytes in a File and upload it to the current folder.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import type { LocaleCode } from '../types/ExplorerConfig';
import { useLocale } from '../composables/useLocale';
import { actionIconSvg } from '../lib/actionIcons'; /* ikon:emoji */
import Modal from './Modal.vue';

const props = defineProps<{
  /** Converter base, e.g. https://fm.example.com/convert */
  convertUrl: string;
  /** Source file name with extension, e.g. "clip.avi" */
  fileName: string;
  /** Lazily fetch the source file bytes (api.fetchArrayBuffer bound to the path). */
  fetchBytes: () => Promise<ArrayBuffer>;
  /** Upload the produced File into the current folder (api.uploadMultipart bound to dir). */
  upload: (file: File) => Promise<void>;
  locale: LocaleCode;
  /** For an administrator: this converter is being retired, and by what. */
  adminNote?: string;
}>();

const { t } = useLocale(() => props.locale);

const emit = defineEmits<{ (e: 'close'): void; (e: 'done', name: string): void }>();

interface Fmt {
  index: number; ext: string; format: string; mime: string;
  name: string; from: boolean; to: boolean; category: string | null;
}

const iframeRef = ref<HTMLIFrameElement | null>(null);
const status = ref<'loading' | 'ready' | 'converting' | 'done' | 'error'>('loading');
const error = ref<string | null>(null);
const formats = ref<Fmt[]>([]);
const search = ref('');
const selectedTo = ref<Fmt | null>(null);

const srcExt = computed(() => (props.fileName.split('.').pop() || '').toLowerCase());

const fromFmt = computed<Fmt | null>(() => {
  const ins = formats.value.filter((f) => f.from);
  return (
    ins.find((f) => (f.ext || '').toLowerCase() === srcExt.value) ||
    ins.find((f) => (f.format || '').toLowerCase() === srcExt.value) ||
    null
  );
});

const toList = computed<Fmt[]>(() => {
  const q = search.value.trim().toLowerCase();
  const seen = new Set<string>();
  const uniq: Fmt[] = [];
  for (const f of formats.value) {
    if (!f.to) continue;
    const key = (f.ext || f.format || '').toLowerCase();
    if (!key || seen.has(key)) continue;
    seen.add(key);
    uniq.push(f);
  }
  uniq.sort((a, b) => (a.ext || a.format).localeCompare(b.ext || b.format));
  if (!q) return uniq;
  return uniq.filter(
    (f) =>
      (f.ext || '').toLowerCase().includes(q) ||
      (f.format || '').toLowerCase().includes(q) ||
      (f.name || '').toLowerCase().includes(q) ||
      (f.mime || '').toLowerCase().includes(q),
  );
});

const iframeSrc = computed(() => `${props.convertUrl.replace(/\/$/, '')}/?embed=1`);

let msgId = 0;
/** Messages waiting for the converter's answer, each with its time limit. */
const pending = new Map<
  number,
  { resolve: (v: any) => void; reject: (e: any) => void; timer: ReturnType<typeof setTimeout> }
>();

/** How long the converter may take to answer an ordinary command. */
const ANSWER_TIMEOUT_MS = 180000;
/** ⚠ How long a CONVERSION may take. It shared the 180 s above, and a large
 *  video or document simply takes longer: the window called it failed while
 *  the converter was still at it, and dropped the result when it came. */
const CONVERT_TIMEOUT_MS = 30 * 60 * 1000;

function send(
  cmd: string,
  extra: Record<string, unknown> = {},
  transfer: Transferable[] = [],
  timeoutMs = ANSWER_TIMEOUT_MS,
): Promise<any> {
  const id = ++msgId;
  return new Promise((resolve, reject) => {
    // ⚠ The time limit is cleared with its answer, and every one that is
    // left goes with the window (onBeforeUnmount): a conversion's is half an
    // hour, and it used to outlive the window that asked.
    const timer = setTimeout(() => {
      if (pending.has(id)) { pending.delete(id); reject(new Error('convert timeout')); }
    }, timeoutMs);
    pending.set(id, { resolve, reject, timer });
    iframeRef.value?.contentWindow?.postMessage({ target: 'convert-embed', id, cmd, ...extra }, '*', transfer);
  });
}

/**
 * ⚠ The converter has to SAY it is there. The iframe posts `ready` when it
 * has loaded; a converter that is down never does, and this modal used to sit
 * on "Loading the converter…" for ever. 20 s is far longer than a healthy
 * converter needs to boot and short enough that a person is not left
 * wondering.
 */
const READY_TIMEOUT_MS = 20000;
let readyTimer: ReturnType<typeof setTimeout> | undefined;

/** Every failure a person sees here is a sentence (owner, 2026-09-21: never
 *  the raw status or body — which is what `fetchBytes`/`upload` errors carry,
 *  `${status} ${statusText} — <json>`). The detail goes to the console. */
function failWith(key: string, detail: unknown): void {
  console.warn('[filex] convert:', key, detail);
  error.value = t(key);
  status.value = 'error';
}

function onMessage(ev: MessageEvent) {
  const d = ev.data;
  if (!d || d.source !== 'convert-embed') return;
  if (d.event === 'ready') {
    if (readyTimer) clearTimeout(readyTimer);
    readyTimer = undefined;
    void loadFormats();
    return;
  }
  const p = pending.get(d.id);
  if (!p) return;
  pending.delete(d.id);
  clearTimeout(p.timer);
  if (d.ok) p.resolve(d); else p.reject(new Error(d.error || 'convert error'));
}

async function loadFormats() {
  try {
    const res = await send('listFormats');
    formats.value = res.formats || [];
    status.value = 'ready';
    if (!fromFmt.value) {
      error.value = t('convert.unsupported_input');
    }
  } catch (e) {
    failWith('convert.unreachable', e);
  }
}

/** The step a conversion is on — said on its button: reading the file can be
 *  as long as converting it, and saving the result as well. */
const stage = ref<'read' | 'convert' | 'save'>('read');
const STAGE_WORDS = { read: 'convert.reading', convert: 'convert.converting', save: 'convert.saving' } as const;

async function doConvert() {
  if (!selectedTo.value || !fromFmt.value || status.value === 'converting') return;
  status.value = 'converting';
  error.value = null;
  stage.value = 'read';
  try {
    const buf = await props.fetchBytes();
    stage.value = 'convert';
    const res = await send(
      'convert',
      { name: props.fileName, bytes: buf, fromIndex: fromFmt.value.index, toIndex: selectedTo.value.index },
      [buf],
      CONVERT_TIMEOUT_MS,
    );
    const base = props.fileName.replace(/\.[^.]+$/, '');
    const ext = res.ext || selectedTo.value.ext || selectedTo.value.format;
    const outName = `${base}.${ext}`;
    const file = new File([res.bytes], outName, { type: selectedTo.value.mime || 'application/octet-stream' });
    stage.value = 'save';
    await props.upload(file);
    status.value = 'done';
    emit('done', outName);
  } catch (e) {
    failWith(
      stage.value === 'read' ? 'convert.read_failed' : stage.value === 'save' ? 'convert.save_failed' : 'convert.failed',
      e,
    );
  }
}

/**
 * ⚠ The conversion runs in this window's frame: closing it mid-way throws the
 * work away. While it runs the window is Modal's `busy` — Escape and a click
 * outside do nothing — and × asks, in the window, whether to stop it (never
 * the browser's confirm(): unthemed, not right-to-left, and blocked in an
 * iframe sandboxed without allow-modals).
 *
 * ⚠ Saving the result is not stopped by closing: the upload is the
 * explorer's and goes on, and the explorer says when the file has arrived
 * (FileExplorer `saveConverted`). The question says that, not "stop it".
 */
const busyClose = computed(() =>
  stage.value === 'save'
    ? { question: t('convert.close_while_saving'), confirm: t('convert.close') }
    : { question: t('convert.close_while_converting') },
);

onMounted(() => {
  window.addEventListener('message', onMessage);
  readyTimer = setTimeout(() => {
    if (status.value === 'loading') failWith('convert.unreachable', 'no ready message');
  }, READY_TIMEOUT_MS);
});
onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage);
  if (readyTimer) clearTimeout(readyTimer);
  for (const p of pending.values()) clearTimeout(p.timer);
  pending.clear();
});
</script>

<template>
  <Modal
    :open="true"
    :title="`${t('convert.title')} — ${fileName}`"
    size="md"
    :locale="locale"
    :busy="status === 'converting'"
    :busy-close="busyClose"
    @close="emit('close')"
  >
    <div class="filex-cv" data-testid="convert-modal">
      <p v-if="adminNote" class="filex-cv__note" data-testid="convert-legacy-note">{{ adminNote }}</p>

      <div v-if="status === 'loading'" class="filex-cv__msg" role="status">
        {{ t('convert.loading') }}
      </div>

      <!-- The converter never answered: there is nothing to pick from, so the
           sentence stands alone rather than above an empty format list. -->
      <div v-else-if="status === 'error' && formats.length === 0" class="filex-cv__msg filex-cv__err" role="alert">
        {{ error }}
      </div>

      <div v-else-if="status === 'done'" class="filex-cv__msg filex-cv__ok" role="status">
        <p>
          <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
          <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('check')"></span>
          {{ t('convert.done') }}
        </p>
      </div>

      <template v-else>
        <p class="filex-cv__src">
          {{ t('convert.from') }}: <b>{{ srcExt || '?' }}</b>
        </p>
        <input
          v-model="search"
          class="filex-cv__search"
          :placeholder="t('convert.search_format')"
          :disabled="status === 'converting'"
        />
        <div class="filex-cv__list">
          <button
            v-for="f in toList"
            :key="f.index"
            type="button"
            :class="['filex-cv__fmt', { 'is-sel': selectedTo?.index === f.index }]"
            :disabled="status === 'converting'"
            @click="selectedTo = f"
          >
            <b>{{ (f.ext || f.format).toUpperCase() }}</b>
            <small><bdi>{{ f.name }}</bdi></small>
          </button>
          <div v-if="toList.length === 0" class="filex-cv__empty">
            {{ t('convert.no_format') }}
          </div>
        </div>
        <div v-if="error" class="filex-cv__err" role="alert">{{ error }}</div>
      </template>

      <!-- hidden headless converter engine -->
      <iframe ref="iframeRef" :src="iframeSrc" class="filex-cv__frame" title="converter" />
    </div>
    <template v-if="status === 'done'" #actions>
      <button type="button" class="fe-btn fe-btn--primary filex-cv__convert" @click="emit('close')">
        {{ t('convert.close') }}
      </button>
    </template>
    <template v-else-if="status !== 'loading' && !(status === 'error' && formats.length === 0)" #actions>
      <button
        type="button"
        class="fe-btn fe-btn--primary filex-cv__convert"
        :disabled="!selectedTo || !fromFmt || status === 'converting'"
        :aria-busy="status === 'converting' ? 'true' : undefined"
        @click="doConvert"
      >
        {{ status === 'converting' ? t(STAGE_WORDS[stage]) : t('convert.convert') }}
      </button>
    </template>
  </Modal>
</template>
