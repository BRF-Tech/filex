<script setup lang="ts">
/**
 * Editor.vue — standalone fullscreen viewer/editor route.
 *
 * The FileExplorer SFC's "Aç" / double-click contract opens
 *
 *   /files/edit?path=<adapter>://<rel>&type=<ext>&mode=edit
 *
 * in a new tab. We mount the SFC's PreviewModal fullscreen against the
 * supplied target so OnlyOffice / Monaco / drawio / image / pdf viewers
 * each pick the right backend (capabilities probe + onlyOfficeBase +
 * drawioUrl all flow from ExplorerConfig). Save-on-change is wired via
 * `saveText: '/api/files/save-text'`.
 */

import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';

import { PreviewModal, isExternalUsable, type FileNode, type ExternalServiceStatus } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';

const { locale } = useI18n();
const route = useRoute();

function readBearerToken(): string | null {
  return sessionStorage.getItem('filex.bearer');
}

const previewUrl = (p: string) =>
  `/api/files/manager?action=preview&path=${encodeURIComponent(p)}`;
const downloadUrl = (p: string) =>
  `/api/files/manager?action=download&path=${encodeURIComponent(p)}`;

function authHeaders(): Record<string, string> {
  const token = readBearerToken();
  if (token) return { Authorization: `Bearer ${token}` };
  return {};
}

// Capability probe — drives drawio + onlyoffice prop wiring. Without
// this fetch the standalone editor route would mount PreviewModal with
// drawioUrl=undefined and the "viewer.drawio.disabled" fallback would
// fire on every diagram open even when the operator has configured
// FILEX_DRAWIO_URL. The FileExplorer SFC already does this dance; the
// standalone editor route needs its own copy because it doesn't host
// the explorer's capability store.
const onlyOfficeBase = ref<string | null>(null);
const drawioUrl = ref<string | null>(null);
async function loadCapabilities(): Promise<void> {
  try {
    const res = await fetch('/api/files/capabilities', {
      credentials: 'same-origin',
      headers: authHeaders(),
    });
    if (!res.ok) return;
    const caps = (await res.json()) as {
      onlyoffice_url?: string;
      drawio_url?: string;
      external?: {
        onlyoffice?: ExternalServiceStatus;
        drawio?: ExternalServiceStatus;
      };
    };
    if (caps.external?.onlyoffice && isExternalUsable(caps.external.onlyoffice)) {
      onlyOfficeBase.value = caps.onlyoffice_url || null;
    }
    if (caps.external?.drawio && isExternalUsable(caps.external.drawio)) {
      drawioUrl.value = caps.drawio_url || null;
    }
  } catch {
    /* keep both null — viewers will surface a "not configured" fallback */
  }
}

const node = computed<FileNode | null>(() => {
  const rawPath = route.query.path;
  if (typeof rawPath !== 'string' || !rawPath) return null;
  const idx = rawPath.indexOf('://');
  const adapter = idx >= 0 ? rawPath.slice(0, idx) : '';
  const rel = idx >= 0 ? rawPath.slice(idx + 3) : rawPath;
  const basename = rel.split('/').filter(Boolean).pop() || rel;
  const dot = basename.lastIndexOf('.');
  const ext = dot > 0 ? basename.slice(dot + 1).toLowerCase() : '';
  return {
    type: 'file',
    path: rawPath,
    basename,
    extension: ext,
    storage: adapter,
    visibility: 'private',
    file_size: 0,
    mime_type: '',
    extra_metadata: {},
  } as unknown as FileNode;
});

const mode = computed<'edit' | 'view'>(() =>
  route.query.mode === 'view' ? 'view' : 'edit',
);

const open = ref(true);

function closeWindow() {
  try {
    window.close();
  } catch {
    open.value = false;
  }
}

onMounted(() => {
  const n = node.value;
  if (n) document.title = `${n.basename} — filex`;
  void loadCapabilities();
});
</script>

<template>
  <div class="editor-host">
    <PreviewModal
      v-if="node"
      :open="open"
      :file="node"
      :open-mode="mode"
      :preview-url="previewUrl"
      :download-url="downloadUrl"
      :only-office-base="onlyOfficeBase"
      :only-office-config-endpoint="'/api/files/onlyoffice/config'"
      :drawio-url="drawioUrl"
      :save-text-endpoint="'/api/files/save-text'"
      :auth-headers="authHeaders"
      :auth-credentials="'same-origin'"
      :locale="locale === 'en' ? 'en' : 'tr'"
      chromeless
      @close="closeWindow"
    />
    <div v-else class="empty">
      <p>Missing <code>?path=</code> query parameter.</p>
    </div>
  </div>
</template>

<style scoped>
.editor-host {
  position: fixed;
  inset: 0;
  background: var(--fe-bg, #0b0d12);
  color: var(--fe-fg, #e6eaf0);
}
.empty {
  display: grid;
  place-items: center;
  height: 100%;
  font-family: system-ui;
  font-size: 0.9rem;
}
</style>
