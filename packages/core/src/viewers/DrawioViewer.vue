<script setup lang="ts">
/**
 * DrawioViewer — embed diagrams.net via iframe + postMessage handshake.
 *
 * The diagrams.net embed mode (`?embed=1&proto=json`) handshakes via
 * window.postMessage:
 *
 *   1. iframe sends {event:'init'}                   ← bootstrap
 *   2. parent sends {action:'load', xml}             ← our payload
 *   3. iframe sends {event:'save', xml} on save      ← we POST back
 *
 * No external library required — diagrams.net hosts the entire editor
 * inside the iframe. We just fetch the source XML on mount and route
 * `save` events back to the configured `pdfSaveUrl`-style endpoint
 * (drawio uses the same shape: POST `{path, content}` body).
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { fetchViewerText } from '../composables/useViewerFetch';

const props = defineProps<{
  url: string;
  filePath: string;
  ext: string;
  drawioUrl?: string;
  /** Optional save endpoint. When unset the iframe stays read-only. */
  saveUrl?: string;
  readOnly?: boolean;
  t?: (key: string) => string;
  authHeaders?: () => Record<string, string>;
  authCredentials?: RequestCredentials;
}>();

const iframeRef = ref<HTMLIFrameElement | null>(null);
const error = ref<string | null>(null);
const status = ref<'loading' | 'ready' | 'saving' | 'saved' | 'error'>('loading');
const readOnly = ref(!!props.readOnly || !props.saveUrl);

// drawioUrl=null is the operator's "off" signal — FileExplorer wipes it
// when the capabilities probe reports drawio offline. Don't silently fall
// back to the public embed.diagrams.net iframe, that defeats the point of
// gating: render a "not configured" pane instead so the user understands
// why the editor isn't loading.
//
// `drawioBase` MUST be a computed (not a const), otherwise a stale null
// at mount time (capabilities probe still inflight) gets cached for the
// component's lifetime — leading to the spurious
// "viewer.drawio.disabled" fallback after the capability flips to "ok".
const drawioBase = computed<string | null>(() =>
  props.drawioUrl ? props.drawioUrl.replace(/\/$/, '') : null,
);
const iframeSrc = computed<string>(() => {
  if (!drawioBase.value) return '';
  const params = new URLSearchParams({
    embed: '1',
    proto: 'json',
    spin: '1',
    saveAndExit: '0',
    noSaveBtn: readOnly.value ? '1' : '0',
    noExitBtn: '1',
    ui: 'kennedy',
    modified: 'unsavedChanges',
  });
  return `${drawioBase.value}/?${params.toString()}`;
});

let pendingXml: string = '';

async function loadXml(): Promise<void> {
  status.value = 'loading';
  error.value = null;
  try {
    pendingXml = await fetchViewerText({
      url: props.url,
      headers: props.authHeaders?.() ?? {},
      credentials: props.authCredentials,
    });
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'fetch failed';
    status.value = 'error';
  }
}

function send(msg: unknown): void {
  iframeRef.value?.contentWindow?.postMessage(JSON.stringify(msg), '*');
}

async function persist(xml: string): Promise<void> {
  if (!props.saveUrl) return;
  status.value = 'saving';
  try {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(props.authHeaders?.() ?? {}),
    };
    const res = await fetch(props.saveUrl, {
      method: 'POST',
      headers,
      credentials: props.authCredentials || 'same-origin',
      body: JSON.stringify({ path: props.filePath, content: xml }),
    });
    if (!res.ok) {
      throw new Error(`${res.status} ${res.statusText}`);
    }
    status.value = 'saved';
    setTimeout(() => {
      if (status.value === 'saved') status.value = 'ready';
    }, 2500);
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'save failed';
    status.value = 'error';
  }
}

function onMessage(ev: MessageEvent): void {
  if (typeof ev.data !== 'string' || ev.data.length === 0) return;
  // Drawio messages always start with '{' or '<'. Ignore anything else
  // (devtools / unrelated postMessage senders inside the same window).
  const first = ev.data[0];
  if (first !== '{' && first !== '<') return;
  let payload: any;
  try {
    payload = JSON.parse(ev.data);
  } catch {
    return;
  }
  if (!payload || typeof payload !== 'object') return;
  switch (payload.event) {
    case 'init':
      send({
        action: 'load',
        xml: pendingXml,
        autosave: 0,
      });
      status.value = 'ready';
      break;
    case 'save':
      if (typeof payload.xml === 'string') {
        persist(payload.xml);
      }
      break;
    case 'export':
      // Could persist a PNG/SVG render; out of V1 scope.
      break;
    case 'autosave':
      if (typeof payload.xml === 'string' && !readOnly.value) {
        persist(payload.xml);
      }
      break;
    default:
      break;
  }
}

function bootIfReady(): void {
  if (!drawioBase.value) {
    error.value = tt('viewer.drawio.disabled', 'Drawio (diagrams.net) yapılandırılmamış.');
    status.value = 'error';
    return;
  }
  // Clear a previous "disabled" error in case the prop just became
  // available (capability probe finished after mount).
  if (error.value === tt('viewer.drawio.disabled', 'Drawio (diagrams.net) yapılandırılmamış.')) {
    error.value = null;
    status.value = 'loading';
  }
  loadXml();
}

onMounted(() => {
  window.addEventListener('message', onMessage);
  bootIfReady();
});

watch(() => props.drawioUrl, bootIfReady);

onBeforeUnmount(() => {
  window.removeEventListener('message', onMessage);
});

function tt(key: string, fallback: string): string {
  return props.t ? props.t(key) : fallback;
}
</script>

<template>
  <div class="filex-viewer-drawio">
    <div v-if="error" class="filex-viewer-fallback">
      <span class="filex-viewer-fallback__icon">📐</span>
      <p>{{ error }}</p>
    </div>
    <iframe
      v-else
      ref="iframeRef"
      :src="iframeSrc"
      class="filex-viewer-drawio__frame"
      title="diagrams.net editor"
    />
  </div>
</template>

<style scoped>
.filex-viewer-drawio {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 70vh;
  background: var(--fe-bg, #fff);
}
.filex-viewer-drawio__bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 6px 12px;
  background: var(--fe-bg-elev, #f7f8fa);
  border-bottom: 1px solid var(--fe-border, #e2e6ed);
  font-size: 12px;
  color: var(--fe-text-muted, #5a6475);
}
.filex-viewer-drawio__status[data-state="error"] { color: var(--fe-danger, #dc2626); }
.filex-viewer-drawio__status[data-state="saved"] { color: #059669; }
.filex-viewer-drawio__readonly {
  font-style: italic;
  margin-left: auto;
}
.filex-viewer-drawio__frame {
  flex: 1;
  width: 100%;
  border: 0;
  background: #fafafa;
  min-height: 0;
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
