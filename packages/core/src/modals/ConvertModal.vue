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

const props = defineProps<{
  /** Converter base, e.g. https://fm.example.com/convert */
  convertUrl: string;
  /** Source file name with extension, e.g. "clip.avi" */
  fileName: string;
  /** Lazily fetch the source file bytes (api.fetchArrayBuffer bound to the path). */
  fetchBytes: () => Promise<ArrayBuffer>;
  /** Upload the produced File into the current folder (api.uploadMultipart bound to dir). */
  upload: (file: File) => Promise<void>;
  t?: (key: string, fallback: string) => string;
}>();

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
const pending = new Map<number, { resolve: (v: any) => void; reject: (e: any) => void }>();

function send(cmd: string, extra: Record<string, unknown> = {}, transfer: Transferable[] = []): Promise<any> {
  const id = ++msgId;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    iframeRef.value?.contentWindow?.postMessage({ target: 'convert-embed', id, cmd, ...extra }, '*', transfer);
    setTimeout(() => {
      if (pending.has(id)) { pending.delete(id); reject(new Error('convert timeout')); }
    }, 180000);
  });
}

function onMessage(ev: MessageEvent) {
  const d = ev.data;
  if (!d || d.source !== 'convert-embed') return;
  if (d.event === 'ready') { void loadFormats(); return; }
  const p = pending.get(d.id);
  if (!p) return;
  pending.delete(d.id);
  if (d.ok) p.resolve(d); else p.reject(new Error(d.error || 'convert error'));
}

async function loadFormats() {
  try {
    const res = await send('listFormats');
    formats.value = res.formats || [];
    status.value = 'ready';
    if (!fromFmt.value) {
      error.value = tt('convert.unsupportedInput', 'Bu dosya tipi için kaynak format bulunamadı.');
    }
  } catch (e) {
    error.value = (e as Error).message;
    status.value = 'error';
  }
}

async function doConvert() {
  if (!selectedTo.value || !fromFmt.value) return;
  status.value = 'converting';
  error.value = null;
  try {
    const buf = await props.fetchBytes();
    const res = await send(
      'convert',
      { name: props.fileName, bytes: buf, fromIndex: fromFmt.value.index, toIndex: selectedTo.value.index },
      [buf],
    );
    const base = props.fileName.replace(/\.[^.]+$/, '');
    const ext = res.ext || selectedTo.value.ext || selectedTo.value.format;
    const outName = `${base}.${ext}`;
    const file = new File([res.bytes], outName, { type: selectedTo.value.mime || 'application/octet-stream' });
    await props.upload(file);
    status.value = 'done';
    emit('done', outName);
  } catch (e) {
    error.value = (e as Error).message;
    status.value = 'error';
  }
}

function tt(k: string, fb: string) { return props.t ? props.t(k, fb) : fb; }

onMounted(() => window.addEventListener('message', onMessage));
onBeforeUnmount(() => window.removeEventListener('message', onMessage));
</script>

<template>
  <div class="filex-cv__bg" @click.self="emit('close')">
    <div class="filex-cv">
      <header class="filex-cv__head">
        <h3>{{ tt('convert.title', 'Dönüştür') }} — {{ fileName }}</h3>
        <button class="filex-cv__x" @click="emit('close')">✕</button>
      </header>

      <div v-if="status === 'loading'" class="filex-cv__msg">
        {{ tt('convert.loading', 'Dönüştürücü yükleniyor…') }}
      </div>

      <div v-else-if="status === 'done'" class="filex-cv__msg filex-cv__ok">
        <p>✓ {{ tt('convert.done', 'Dönüştürüldü ve klasöre yüklendi.') }}</p>
        <button class="filex-cv__convert" @click="emit('close')">{{ tt('convert.close', 'Kapat') }}</button>
      </div>

      <template v-else>
        <p class="filex-cv__src">
          {{ tt('convert.from', 'Kaynak') }}: <b>{{ srcExt || '?' }}</b>
        </p>
        <input
          v-model="search"
          class="filex-cv__search"
          :placeholder="tt('convert.searchFmt', 'Hedef format ara (pdf, mp4, png…)')"
        />
        <div class="filex-cv__list">
          <button
            v-for="f in toList"
            :key="f.index"
            :class="['filex-cv__fmt', { 'is-sel': selectedTo?.index === f.index }]"
            @click="selectedTo = f"
          >
            <b>{{ (f.ext || f.format).toUpperCase() }}</b>
            <small>{{ f.name }}</small>
          </button>
          <div v-if="toList.length === 0" class="filex-cv__empty">
            {{ tt('convert.noFmt', 'Eşleşen format yok.') }}
          </div>
        </div>
        <div v-if="error" class="filex-cv__err">{{ error }}</div>
        <footer class="filex-cv__foot">
          <button
            class="filex-cv__convert"
            :disabled="!selectedTo || !fromFmt || status === 'converting'"
            @click="doConvert"
          >
            {{ status === 'converting' ? tt('convert.converting', 'Dönüştürülüyor…') : tt('convert.convert', 'Dönüştür') }}
          </button>
        </footer>
      </template>

      <!-- hidden headless converter engine -->
      <iframe ref="iframeRef" :src="iframeSrc" class="filex-cv__frame" title="converter" />
    </div>
  </div>
</template>

<style scoped>
.filex-cv__bg {
  position: fixed; inset: 0; z-index: 1000;
  background: rgba(0, 0, 0, 0.55);
  display: flex; align-items: center; justify-content: center;
}
.filex-cv {
  width: min(560px, 92vw); max-height: 80vh; overflow: hidden;
  display: flex; flex-direction: column;
  background: var(--fe-bg, #fff); color: var(--fe-text, #1a1f29);
  border: 1px solid var(--fe-border, #e2e6ed); border-radius: 12px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.35);
}
.filex-cv__head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 12px 16px; border-bottom: 1px solid var(--fe-border, #e2e6ed);
}
.filex-cv__head h3 { margin: 0; font-size: 14px; font-weight: 600; }
.filex-cv__x { border: 0; background: none; cursor: pointer; font-size: 16px; color: var(--fe-text-muted, #5a6475); }
.filex-cv__msg { padding: 28px; text-align: center; color: var(--fe-text-muted, #5a6475); }
.filex-cv__ok { color: #059669; }
.filex-cv__src { padding: 12px 16px 0; font-size: 13px; color: var(--fe-text-muted, #5a6475); }
.filex-cv__search {
  margin: 8px 16px; padding: 8px 10px;
  border: 1px solid var(--fe-border, #e2e6ed); border-radius: 8px;
  background: var(--fe-bg-elev, #f7f8fa); color: inherit; font-size: 13px;
}
.filex-cv__list {
  flex: 1; overflow-y: auto; padding: 4px 12px 12px;
  display: grid; grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); gap: 8px;
}
.filex-cv__fmt {
  display: flex; flex-direction: column; gap: 2px; align-items: flex-start;
  padding: 8px 10px; cursor: pointer; text-align: left;
  border: 1px solid var(--fe-border, #e2e6ed); border-radius: 8px;
  background: var(--fe-bg-elev, #f7f8fa); color: inherit;
}
.filex-cv__fmt small { color: var(--fe-text-muted, #5a6475); font-size: 11px; }
.filex-cv__fmt.is-sel { border-color: #44c878; background: rgba(68, 200, 120, 0.12); }
.filex-cv__empty { grid-column: 1 / -1; text-align: center; color: var(--fe-text-muted, #5a6475); padding: 16px; }
.filex-cv__err { padding: 0 16px; color: var(--fe-danger, #dc2626); font-size: 13px; }
.filex-cv__foot { padding: 12px 16px; border-top: 1px solid var(--fe-border, #e2e6ed); text-align: right; }
.filex-cv__convert {
  padding: 8px 18px; border: 0; border-radius: 8px; cursor: pointer;
  background: #44c878; color: #03200f; font-weight: 600; font-size: 13px;
}
.filex-cv__convert:disabled { opacity: 0.5; cursor: default; }
.filex-cv__frame { position: absolute; width: 0; height: 0; border: 0; left: -9999px; }
</style>
