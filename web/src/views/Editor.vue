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
 *
 * The page is intentionally chrome-free — no sidebar, no breadcrumb.
 * Closing the tab returns the user to the explorer they came from.
 */

import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';

import { PreviewModal, type ExplorerConfig, type FileNode } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';

const { locale } = useI18n();
const route = useRoute();

function readBearerToken(): string | null {
  return sessionStorage.getItem('filex.bearer');
}
function readCsrfCookie(): string | null {
  const prefix = 'filex_csrf=';
  for (const part of document.cookie.split(';')) {
    const trimmed = part.trim();
    if (trimmed.startsWith(prefix)) return decodeURIComponent(trimmed.slice(prefix.length));
  }
  return null;
}

const config = computed<ExplorerConfig>(() => {
  const bearer = readBearerToken();
  const csrf = readCsrfCookie();
  const auth: ExplorerConfig['auth'] = bearer
    ? { kind: 'bearer', token: bearer }
    : csrf
      ? { kind: 'csrf', csrf }
      : { kind: 'none' };
  return {
    apiBase: '',
    endpoint: '/api/files/manager',
    capabilities: '/api/files/capabilities',
    saveText: '/api/files/save-text',
    onlyOfficeConfig: '/api/files/onlyoffice/config',
    auth,
    locale: locale.value === 'en' ? 'en' : 'tr',
  };
});

// Synthesise a minimal FileNode from the URL params. The PreviewModal
// only needs `path` + `basename` + `extension` + `type` to pick a viewer;
// the rest comes from the capabilities/preview API calls.
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

const mode = computed<'edit' | 'view'>(() => (route.query.mode === 'view' ? 'view' : 'edit'));

const open = ref(true);

onMounted(() => {
  // Match the file's basename to the page title so the user can find
  // the tab in their session ribbon.
  const n = node.value;
  if (n) document.title = `${n.basename} — filex`;
});
</script>

<template>
  <div class="editor-host">
    <PreviewModal
      v-if="node"
      :open="open"
      :file="node"
      :open-mode="mode"
      :preview-url="(p) => `/api/files/manager?action=preview&path=${encodeURIComponent(p)}`"
      :download-url="(p) => `/api/files/manager?action=download&path=${encodeURIComponent(p)}`"
      :only-office-config-endpoint="config.onlyOfficeConfig ?? null"
      :save-text-endpoint="config.saveText ?? null"
      :auth="config.auth"
      :locale="config.locale ?? 'tr'"
      :show-edit-button="true"
      :standalone="true"
      @close="window.close()"
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
