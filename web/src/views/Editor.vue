<script setup lang="ts">
/**
 * Editor.vue — standalone fullscreen viewer/editor route.
 *
 * The FileExplorer SFC's "Open" / double-click contract opens
 *
 *   /files/edit?path=<adapter>://<rel>&type=<ext>&mode=edit
 *
 * in a new tab. We mount the SFC's PreviewModal fullscreen against the
 * supplied target so OnlyOffice / Monaco / drawio / image / pdf viewers
 * each pick the right backend (capabilities probe + onlyOfficeBase +
 * drawioUrl all flow from ExplorerConfig). Save-on-change is wired via
 * `saveText: '/api/files/save-text'`.
 */

import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';

import {
  PreviewModal,
  isExternalUsable,
  pickAppViewer,
  useFileApi,
  type ExplorerConfig,
  type FileNode,
  type ExternalServiceStatus,
  type PluginViewRow,
} from '@brftech/filex-core';
import '@brftech/filex-core/style.css';
import { effectiveTheme } from '@/lib/theme';
import { getServerRoot } from '@/api/runtimeConfig';
import { explorerAuth } from '@/lib/explorerConfig';

const { locale } = useI18n();
const route = useRoute();

function readBearerToken(): string | null {
  return sessionStorage.getItem('filex.bearer');
}

// Every address below is under the server root — '' at the root, `/filex`
// under a sub-path (FILEX_BASE_PATH; runtimeConfig.getServerRoot).
const api = (path: string) => `${getServerRoot()}${path}`;
const previewUrl = (p: string) =>
  api(`/api/files/manager?action=preview&path=${encodeURIComponent(p)}`);
const downloadUrl = (p: string) =>
  api(`/api/files/manager?action=download&path=${encodeURIComponent(p)}`);

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
/* Could this person set up a missing service? Only picks which sentence a
 * missing document server gets (PreviewModal `canConfigure`). */
const callerAdmin = ref(false);
/**
 * ⚠ The viewer waits for the probe. PreviewModal decides "no document server"
 * from `onlyOfficeBase`, and it decides at mount — so mounting it before the
 * capabilities answer arrived would tell every person opening a .docx that
 * ONLYOFFICE is missing, on the install where it is not. (It used to decide
 * from the config ENDPOINT alone, which this route always passes, and that is
 * how the raw 503 reached the screen on an install where it really was.)
 */
const capsLoaded = ref(false);
async function loadCapabilities(): Promise<void> {
  try {
    const res = await fetch(api('/api/files/capabilities'), {
      credentials: 'same-origin',
      headers: authHeaders(),
    });
    if (!res.ok) return;
    const caps = (await res.json()) as {
      onlyoffice_url?: string;
      drawio_url?: string;
      caller_admin?: boolean;
      external?: {
        onlyoffice?: ExternalServiceStatus;
        drawio?: ExternalServiceStatus;
      };
    };
    callerAdmin.value = caps.caller_admin === true;
    if (caps.external?.onlyoffice && isExternalUsable(caps.external.onlyoffice)) {
      onlyOfficeBase.value = caps.onlyoffice_url || null;
    }
    if (caps.external?.drawio && isExternalUsable(caps.external.drawio)) {
      drawioUrl.value = caps.drawio_url || null;
    }
  } catch {
    /* keep both null — viewers will surface a "not configured" fallback */
  } finally {
    capsLoaded.value = true;
  }
}

/**
 * An app's own interface for this file (Burak, 2026-09-27 — one surface):
 * the explorer's rule (lib/appViewer `pickAppViewer`) over the server's list
 * of views, and "Open with"'s choice when the tab was opened from one
 * (`app=plugin/view`, or `builtin`). The same default plugin endpoints the
 * explorer derives its own from.
 */
const fileApi = useFileApi({ apiBase: getServerRoot(), auth: explorerAuth() } as ExplorerConfig);
const appViews = ref<PluginViewRow[]>([]);
/** The viewer waits for the list too: an app's file must not flash in
 *  filex's own viewer first. */
const appsLoaded = ref(false);
async function loadAppViews(): Promise<void> {
  try {
    const list = await fileApi.pluginActions();
    appViews.value = list.views ?? [];
  } catch {
    /* apps off, or not reachable: filex's own viewer */
  } finally {
    appsLoaded.value = true;
  }
}
const appChoice = computed(() => (typeof route.query.app === 'string' && route.query.app ? route.query.app : null));
const appViewer = computed(() => pickAppViewer(appViews.value, node.value, appChoice.value));

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

/**
 * `type=` when it says something the name does not (#56). The viewer builds
 * this link with the type it OPENED the file as, so `LICENSE` created as Plain
 * text arrives as `type=txt` and opens in the text editor here too; a link
 * whose type is just the name's own extension (every other caller) changes
 * nothing.
 */
const openAs = computed<string | null>(() => {
  const ty = route.query.type;
  if (typeof ty !== 'string' || !ty || !node.value) return null;
  const want = ty.toLowerCase();
  return want === node.value.extension ? null : want;
});

const open = ref(true);

/**
 * Drafts (issue #71): the draft this tab was editing was saved — it is a file
 * where it belongs now. The address and the tab's name follow it, so a reload
 * opens the saved document rather than a draft that is gone. `replaceState`,
 * not the router: a route change would remount the viewer and close the
 * editor that is still open on the document.
 */
function onDraftSaved(saved: { path: string; name: string }) {
  try {
    const url = new URL(window.location.href);
    url.searchParams.set('path', saved.path);
    window.history.replaceState(window.history.state, '', url.toString());
  } catch {
    /* an address the browser would not take — the reload is the only loss */
  }
  document.title = saved.name;
}

function closeWindow() {
  try {
    window.close();
  } catch {
    open.value = false;
  }
}

// Reactive theme passthrough — feeds the PreviewModal so the
// embedded viewer follows the host admin's dark/light state. Without
// this the SFC's `prefers-color-scheme` media-query fallback locks
// the standalone editor to OS-dark when the admin shell is light.
const currentTheme = ref<'light' | 'dark'>(effectiveTheme());
let htmlObserver: MutationObserver | null = null;
const onStorage = (e: StorageEvent) => {
  if (e.key === 'filex.theme') currentTheme.value = effectiveTheme();
};

onMounted(() => {
  const n = node.value;
  // The tab is named after the document (same as the router's afterEach in
  // lib/documentTitle and the desktop's document windows) — just the file name,
  // no " — filex" suffix, so all three surfaces read the same.
  if (n) document.title = n.basename;
  void loadCapabilities();
  void loadAppViews();
  htmlObserver = new MutationObserver(() => {
    currentTheme.value = document.documentElement.classList.contains('dark') ? 'dark' : 'light';
  });
  htmlObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
  window.addEventListener('storage', onStorage);
});
onBeforeUnmount(() => {
  htmlObserver?.disconnect();
  window.removeEventListener('storage', onStorage);
});
</script>

<template>
  <div class="editor-host">
    <PreviewModal
      v-if="node && capsLoaded && appsLoaded"
      :open="open"
      :file="node"
      :open-mode="mode"
      :open-as="openAs"
      :theme="currentTheme"
      :preview-url="previewUrl"
      :download-url="downloadUrl"
      :only-office-base="onlyOfficeBase"
      :only-office-config-endpoint="api('/api/files/onlyoffice/config')"
      :can-configure="callerAdmin"
      :drawio-url="drawioUrl"
      :save-text-endpoint="api('/api/files/save-text')"
      :drafts-endpoint="api('/api/files/drafts')"
      :app-viewer="appViewer"
      :api="fileApi"
      :auth-headers="authHeaders"
      :auth-credentials="'same-origin'"
      :locale="locale"
      chromeless
      @draft-saved="onDraftSaved"
      @close="closeWindow"
    />
    <div v-else-if="!node" class="empty">
      <i18n-t keypath="editor.missingPath" tag="p"><template #param><code>?path=</code></template></i18n-t>
    </div>
  </div>
</template>

<style scoped>
.editor-host {
  position: fixed;
  inset: 0;
  /* Use filex-core's CSS variables (`--fe-bg` + `--fe-text`) so the
   * surface follows the host shell's dark/light state. The previous
   * fallback referenced `--fe-fg` (which doesn't exist) and hard-
   * coded a dark colour — leaving every standalone editor tab dark
   * for a brief flash before the SFC stylesheet finished loading,
   * and permanently dark on the light theme because of a typo. */
  background: var(--fe-bg, #ffffff);
  color: var(--fe-text, #1a1e27);
}
:global(html.dark) .editor-host {
  background: var(--fe-bg, #0f1419);
  color: var(--fe-text, #e5e9f0);
}
.empty {
  display: grid;
  place-items: center;
  height: 100%;
  font-family: system-ui;
  font-size: 0.9rem;
}
</style>
