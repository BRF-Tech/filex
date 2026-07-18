<script setup lang="ts">
/**
 * FileExplorer — the public Vue component, panel + PWA + standalone use.
 *
 * Orchestrates:
 *   - Directory listing (useFileApi)
 *   - Chunked multipart upload (useUploadChunked) + drag & drop
 *   - Selection + keyboard shortcuts
 *   - Context menu (Teleport-based) with per-target actions
 *   - Modal flows: newFolder / rename / delete / share / preview
 *   - Eager Monaco preload so the code-edit path is snappy
 *
 * All backend endpoints arrive via the `config` prop. Auth is bearer
 * (PWA / OIDC) / CSRF (panel) / basic / none — `useFileApi` swallows
 * the difference.
 */
import { computed, customRef, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import type { ExplorerConfig } from './types/ExplorerConfig';
import type {
  FileNode,
  ShareInfo,
  ViewMode,
  ClipboardState,
  Capabilities,
} from './types/FileNode';
import { isExternalUsable } from './types/FileNode';
import { useFileApi, type GlobalSearchHit } from './composables/useFileApi';
import { useUploadChunked, type UploadJob } from './composables/useUploadChunked';
import { useSelection } from './composables/useSelection';
import { useKeyboardShortcuts } from './composables/useKeyboardShortcuts';
import { useLocale } from './composables/useLocale';
import { usePendingOps, type PendingOp } from './composables/usePendingOps';
import { useRealtime } from './composables/useRealtime';
import { useThumbs } from './composables/useThumbs';
import { preloadEditor } from './composables/useMonacoLoader';
import PresenceBar from './components/PresenceBar.vue';

import Toolbar, { type SelectionMode } from './components/Toolbar.vue';
import StarButton from './components/StarButton.vue';
import TagPicker from './components/TagPicker.vue';
import RecentlyOpened from './components/RecentlyOpened.vue';
import Breadcrumb from './components/Breadcrumb.vue';
import ListView from './components/ListView.vue';
import GridView from './components/GridView.vue';
import GalleryView from './components/GalleryView.vue'; /* wiring:d2 */
import ContextMenu, { type ContextAction } from './components/ContextMenu.vue';
import UploadProgress from './components/UploadProgress.vue';
import PendingOpsTray from './components/PendingOpsTray.vue';
import InspectorPanel from './components/InspectorPanel.vue'; /* koru:k1 */
/* cila:c wiring */
import CommandPalette from './components/CommandPalette.vue';
import ShortcutsHelp from './components/ShortcutsHelp.vue';
/* /cila:c wiring */
/* wiring:c1 — tema galerisi */
import ThemeGallery from './components/ThemeGallery.vue';
import { useThemeState, applyThemeToEl, syncThemeStyle } from './lib/themes';
/* /wiring:c1 */
/* wiring:c2 — shortcut settings modal + Space quick-look overlay */
import ShortcutSettings from './components/ShortcutSettings.vue';
import QuickLook from './components/QuickLook.vue';
/* /wiring:c2 */
/* wiring:c3 — unified operations center */
import OperationsCenter from './components/OperationsCenter.vue';
import { useOperations } from './composables/useOperations';
/* /wiring:c3 */
/* wiring:c4 */
import OnboardingTour from './components/OnboardingTour.vue';
/* /wiring:c4 */
/* wiring:d1 — sekmeler + tab başına split */
import TabBar from './components/TabBar.vue';
import SecondaryPane from './components/SecondaryPane.vue';
import { useTabs, type TabState } from './composables/useTabs';
/* /wiring:d1 */
/* wiring:e2 — uçtan uca şifreli klasörler (docs/E2E-ENCRYPTION.md) */
import EncryptedFolderModal from './components/EncryptedFolderModal.vue';
import {
  createKeyRing,
  createMarker,
  parseMarker,
  verifyPassword,
  encryptFile,
  decryptFile,
  hasMagic,
  E2E_MARKER_NAME,
  E2E_MAX_FILE_BYTES,
} from './lib/e2ecrypto';
/* /wiring:e2 */

import NewFolderModal from './modals/NewFolderModal.vue';
import RenameModal from './modals/RenameModal.vue';
import DeleteConfirmModal from './modals/DeleteConfirmModal.vue';
import ShareModal from './modals/ShareModal.vue';
import PreviewModal from './modals/PreviewModal.vue';
import ConvertModal from './modals/ConvertModal.vue';
import PermissionsModal from './modals/PermissionsModal.vue';

const props = defineProps<{
  config: ExplorerConfig;
}>();

const emit = defineEmits<{
  (e: 'share-created', payload: { path: string; url: string; pin: string | null }): void;
  (e: 'file-opened', file: { path: string; basename: string }): void;
  (e: 'error', err: { message: string; context?: unknown }): void;
  (e: 'upload-progress', p: { uploadId: string; percent: number; done: boolean }): void;
  (
    e: 'selection-change',
    items: Array<{ path: string; basename: string; type: 'file' | 'dir' }>,
  ): void;
  // Fires whenever the viewed folder changes (virtual `<storage>/<rel>` form).
  // Lets a host (e.g. the Explore page's realtime layer) track the current
  // folder without reaching into internal state.
  (e: 'navigate', p: { path: string }): void;
}>();

// --------------------------------------------------------------------
// State
// --------------------------------------------------------------------

const api = useFileApi(props.config);

// Locale up-front: the pendingOps onSettled callback below (and the undo-toast
// helpers) need `t()` at runtime, so the catalogue must be constructed before
// they are wired. Depends only on props — safe this early.
const locale = computed(() => props.config.locale || 'tr');
const { t } = useLocale(locale);

// Live collaboration (WebSocket file-change events + presence), bundled into the
// core so every consumer — the native panel AND the embedded webcomponent —
// gets it. Auth is a short-lived ticket fetched through the same API (works
// same-origin and proxied cross-origin); it degrades to polling when no live
// socket is available.
const realtime = useRealtime(api, { reload: () => load() });
const presenceUsers = realtime.presenceUsers;
// True while the live socket is unavailable and the explorer runs on the
// polling fallback — drives the small "no live connection" badge. Healthy
// connections show nothing.
const realtimeDegraded = realtime.degraded;
function realtimeRoom(vp: string): string | null {
  const p = (vp || '').replace(/^\/+|\/+$/g, '');
  if (p === '.trash' || p.startsWith('.trash/')) return null;
  // The wire form must go through the mode-aware qualify(), exactly like every
  // API call: in single-storage mode currentPath is a BARE relative path
  // ("projeler/5") — virtualToWire would mistake its first segment for an
  // adapter ("projeler://5") and subscribe a nonexistent room, so presence and
  // live changes silently missed the real folder. An empty p is the storage
  // root — a real room ("main://") — not "no room"; only the multi-storage
  // drives list (no adapter yet) has none.
  const wire = qualify(p);
  if (!wire || !wire.includes('://') || wire.startsWith('://')) return null;
  return wire;
}
onMounted(() => {
  realtime.start();
  realtime.subscribe(realtimeRoom(currentPath.value));
});
onBeforeUnmount(() => realtime.stop());

// Authenticated thumbnails — raw thumb_url is root-relative + header-less,
// which only ever worked for the native same-origin SPA (embedded hosts got
// empty/broken <img>s). See useThumbs.
const thumbs = useThumbs(props.config.apiBase, api);

const chunked = useUploadChunked(props.config, api);

// Undo registry for async pending ops: when a cleanly-invertible operation
// (move → reverse move, trash-delete → restore) is queued, its inverse is
// registered under the op id; once the op settles OK the toast grows a
// "Geri Al" action. Ops without an entry keep the plain settled toast.
const opUndo = new Map<number, { message: string; fn: () => Promise<void> }>();

const pendingOps = usePendingOps(props.config, api, {
  onSettled: (op: PendingOp) => {
    const undo = opUndo.get(op.id);
    opUndo.delete(op.id);
    if (op.status === 'error') {
      flashToast(op.error_message || 'İşlem başarısız');
    } else if (undo) {
      undoToast(`${undo.message} (${op.progress_total})`, undo.fn);
    } else {
      const verb =
        op.op_type === 'copy'
          ? 'Kopyalandı'
          : op.op_type === 'move'
            ? 'Taşındı'
            : 'Silindi';
      flashToast(`${verb} (${op.progress_total})`);
    }
    void load();
    void splitPaneRef.value?.reload(); /* wiring:d1 — ikincil panel de tazelenir */
  },
});

const loading = ref(false);
// rootPath confinement (UX): when set, the explorer treats this folder as its
// floor — it opens there, never lists the drives root, and can't navigate
// above it. Security is enforced server-side (X-Filex-Root / token root scope);
// this is purely the clean-embed presentation. `rootFloor` is the virtual form
// (`<storage>/<rel>`) used for path comparisons in multi-storage mode.
const rootPathProp = (props.config.rootPath || '').trim(); // qualified `<adapter>://<rel>`
const rootFloor = rootPathProp.replace('://', '/').replace(/^\/+|\/+$/g, '');
const initialFloorPath = rootFloor || props.config.initialPath || '';
const currentPath = ref<string>(initialFloorPath);
const adapter = ref<string>(props.config.defaultAdapter || 'brf');
const dirname = ref<string>(initialFloorPath);
const files = ref<FileNode[]>([]);
// RBAC effective level for the current directory ('' = ACL not enforced on
// this storage → no gating). Drives which write/manage actions are offered.
const dirPerm = ref<string>('');
// The dead deep-link state: set to the requested path when a listing came
// back 404 (folder doesn't exist) or 403 (RBAC-hidden — rendered identically
// on purpose so a denied folder doesn't reveal that it exists). '' = none.
const notFoundPath = ref<string>('');
// Listing failure that is NOT a dead link (network error, 5xx): remembered so
// the body can render a retryable error state instead of a misleading "this
// folder is empty". Only shown when no listing is visible — a failed
// navigation away from a healthy listing keeps the current list + toast,
// exactly as before.
const loadError = ref<string>('');
let loadErrorPath: string | undefined;
function retryLoad() {
  void load(loadErrorPath);
}

const VIEW_MODE_KEY = 'brf-file-explorer:view-mode';
const viewMode = customRef<ViewMode>((track, trigger) => {
  let value: ViewMode = (() => {
    try {
      const stored = localStorage.getItem(VIEW_MODE_KEY);
      if (stored === 'list' || stored === 'grid' || stored === 'gallery') return stored; /* wiring:d2 */
    } catch {
      /* private mode */
    }
    return props.config.viewMode ?? 'list';
  })();
  return {
    get() {
      track();
      return value;
    },
    set(next) {
      if (next === value) return;
      value = next;
      try {
        localStorage.setItem(VIEW_MODE_KEY, next);
      } catch {
        /* quota */
      }
      trigger();
    },
  };
});
/* cila:a density — Toolbar owns the persisted preference (filex.density);
   mirrored here only so the root `.fe` can carry fe--density-compact. */
const density = ref<'comfortable' | 'compact'>('comfortable');
const searchQuery = ref('');
// trashMode — true while viewing the filex trash (soft-deleted nodes from the
// backend trash endpoint), entered by opening the virtual `.trash` row and
// exited by any normal navigation (load() resets it). Replaces a brittle
// `currentPath.startsWith('fileman/.trash')` check that never matched the
// filex backend's storage layout, so trash always looked empty.
const trashMode = ref(false);
// The storage the trash view was entered from, so "up" returns there (not the
// global root). Set in loadTrash().
const trashOrigin = ref<string>('');
const trashActive = computed(() => trashMode.value);

// canGoUp/goUp — toolbar's "↑ Up one level" button. In single-storage
// mode "" means the storage root; in multi-storage mode "" means
// the global root (storage list). Both → no parent → button hidden.
const canGoUp = computed(() => {
  const p = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  if (rootFloor && p === rootFloor) return false; // at the confined floor — nowhere above
  return p.length > 0;
});

// True when the explorer is showing the synthetic storage list and
// there's no real backend folder to mutate. New Folder / Upload /
// Paste are hidden in this state.
const atVirtualRoot = computed(() => {
  if (!multiStorageRoot.value) return false;
  return !((currentPath.value ?? '').replace(/^\/+|\/+$/g, ''));
});

function goUp() {
  // Leaving the trash view returns to the storage it was opened from, not the
  // global storage-list root.
  if (trashMode.value) {
    void load(trashOrigin.value);
    return;
  }
  const cur = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  if (!cur || cur === rootFloor) return;
  const idx = cur.lastIndexOf('/');
  let parent = idx === -1 ? '' : cur.slice(0, idx);
  // Never step above the confined floor.
  if (rootFloor && !(parent === rootFloor || parent.startsWith(rootFloor + '/'))) parent = rootFloor;
  void load(parent);
}

const selection = useSelection(() => files.value);
watch(
  () => [...selection.selected.value],
  () => {
    emit(
      'selection-change',
      selection.nodes.value.map((n) => ({ path: n.path, basename: n.basename, type: n.type })),
    );
    // Presence focus: a single selected file is what the user is "on"; a
    // multi-select or folder selection clears it.
    const focusFiles = selection.nodes.value.filter((n) => n.type === 'file');
    realtime.setFocus(focusFiles.length === 1 ? focusFiles[0].basename : null);
  },
);

const clipboard = ref<ClipboardState>({ mode: null, items: [], sourcePath: null });

const capabilitiesData = ref<Capabilities | null>(null);

// Creative UI state: starred / tags / recently-opened. The component
// helpers (StarButton, TagPicker, RecentlyOpened) handle their own
// API calls — the explorer just tracks the cross-row state needed to
// render inline stars and keep the recents tray in sync.
const starredIds = ref(new Set<number>());
const showRecents = ref(false);
const showTagPicker = ref(false);
const tagPickerNode = ref<FileNode | null>(null);
const recentRefreshKey = ref(0);

async function loadStarred() {
  try {
    const headers = await buildAuthHeaders();
    const base = props.config.apiBase ?? '';
    const res = await fetch(`${base}/api/files/manager/star/list?limit=500`, {
      headers,
      credentials: 'include',
    });
    if (!res.ok) return;
    const body = await res.json();
    const rows: { id?: number }[] = Array.isArray(body)
      ? body
      : Array.isArray(body?.entries)
        ? body.entries
        : Array.isArray(body?.nodes)
          ? body.nodes
          : [];
    starredIds.value = new Set(rows.map((n) => n.id).filter((id): id is number => typeof id === 'number'));
  } catch {
    // Silent — backend may be older without the meta routes.
  }
}

function onStarChange(n: FileNode, value: boolean) {
  if (typeof n.id !== 'number') return;
  const next = new Set(starredIds.value);
  if (value) next.add(n.id);
  else next.delete(n.id);
  starredIds.value = next;
}

async function markRecent(n: FileNode) {
  if (typeof n.id !== 'number') return;
  try {
    const base = props.config.apiBase ?? '';
    await fetch(`${base}/api/files/manager/recent`, {
      method: 'POST',
      headers: await buildAuthHeaders({ 'Content-Type': 'application/json' }),
      credentials: 'include',
      body: JSON.stringify({ node_id: n.id }),
    });
    recentRefreshKey.value += 1;
  } catch {
    // Silent — the open succeeds, recent tracking is best-effort.
  }
}

function openTagPickerFor(n: FileNode) {
  if (typeof n.id !== 'number') return;
  tagPickerNode.value = n;
  showTagPicker.value = true;
}

function onRecentOpen(entry: { id: number; storage_id?: number; path: string; name: string }) {
  // RecentlyOpened emits the bare row — synthesize a FileNode shaped
  // enough for openNode to route into the editor / preview.
  const node = {
    type: 'file',
    path: entry.path,
    basename: entry.name,
    extension: (entry.name.split('.').pop() || '').toLowerCase(),
    id: entry.id,
  } as unknown as FileNode;
  showRecents.value = false;
  openNode(node);
}

// Resolution order for each external viewer: explicit config override → live
// backend probe. The probe is the source of truth: an operator can flip the
// service "on" but if last_check failed (state='error') we still hide the
// entry so users don't get 503s on click. Explicit config wins because
// embedders sometimes terminate TLS in front of filex and the backend can't
// see the public URL.
const effectiveOnlyOfficeBase = computed<string | null>(() => {
  if (props.config.onlyOfficeBase) return props.config.onlyOfficeBase;
  const ext = capabilitiesData.value?.external?.onlyoffice;
  if (ext && !isExternalUsable(ext)) return null;
  return capabilitiesData.value?.onlyoffice_url || null;
});

const effectiveOnlyOfficeConfigEndpoint = computed<string | null>(() => {
  if (!effectiveOnlyOfficeBase.value) return null;
  return api.endpoints.onlyOfficeConfig || null;
});

const effectiveDrawioUrl = computed<string | null>(() => {
  const override = props.config.drawioUrl || props.config.drawioBase;
  if (override) return override;
  const ext = capabilitiesData.value?.external?.drawio;
  if (ext && !isExternalUsable(ext)) return null;
  return capabilitiesData.value?.drawio_url || null;
});

// Universal converter (p2r3/convert fork). convert_url is only populated by
// the backend when the "convert" external service is enabled, so a simple
// presence check is enough gating.
const effectiveConvertUrl = computed<string | null>(
  () => props.config.convertBase || capabilitiesData.value?.convert_url || null,
);

// Upload
const uploadJobs = ref<UploadJob[]>([]);
const fileInputEl = ref<HTMLInputElement | null>(null);

// Modals
const showNewFolder = ref(false);
const showRename = ref(false);
const showDelete = ref(false);
const showShare = ref(false);
const showPreview = ref(false);
const renameTarget = ref<FileNode | null>(null);
/* ui-fix — açık olan rename/delete/new-folder modal'ı yan panele mi ait?
 * (menü ana panelle aynı; mutasyonlar doğru panele yönlensin diye.) */
const mutationInPane = ref(false);
const shareTarget = ref<FileNode | null>(null);
const activeShare = ref<(ShareInfo & { url: string; filename?: string }) | null>(null);
const previewTarget = ref<FileNode | null>(null);
const previewMode = ref<'edit' | 'view'>('edit');
const showConvert = ref(false);
const convertTarget = ref<FileNode | null>(null);
const showPerm = ref(false);
const permTarget = ref<FileNode | null>(null);

/* === koru:k1 — inspector (details) panel ===
 * Open/closed preference persists under `filex.inspector`; the panel itself
 * mounts with v-if so the closed state leaves zero DOM behind. */
const INSPECTOR_LS_KEY = 'filex.inspector';
const showInspector = ref<boolean>(
  (() => {
    try {
      return localStorage.getItem(INSPECTOR_LS_KEY) === '1';
    } catch {
      return false;
    }
  })(),
);
function persistInspector(v: boolean) {
  try {
    localStorage.setItem(INSPECTOR_LS_KEY, v ? '1' : '0');
  } catch {
    /* quota / private mode */
  }
}
function toggleInspector() {
  showInspector.value = !showInspector.value;
  persistInspector(showInspector.value);
}
function openInspector() {
  if (!showInspector.value) {
    showInspector.value = true;
    persistInspector(true);
  }
}
function closeInspector() {
  if (showInspector.value) {
    showInspector.value = false;
    persistInspector(false);
  }
}
// Folder summary label for the no-selection state.
const inspectorDirLabel = computed(() => {
  if (trashMode.value) return t('node.trash');
  const p = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  if (!p) return adapter.value || t('breadcrumb.root');
  return p.split('/').pop() || p;
});
function onInspectorManage(n: FileNode) {
  permTarget.value = n;
  showPerm.value = true;
}
/* === /koru:k1 === */

// RBAC helpers. '' means ACL is not enforced on this storage → full access
// (the pre-RBAC default). Otherwise 'editor'/'owner' may write; only 'owner'
// manages permissions. Enforcement is server-side; this just shapes the menu.
function permCanEdit(p: string | undefined): boolean {
  // undefined = ACL not enforced (dev / unwired) → full access. In production
  // the backend always sends a level; 'none'/'viewer' cannot write, only
  // 'editor'/'owner' can.
  return p === undefined || p === 'editor' || p === 'owner';
}
function permIsOwner(p: string | undefined): boolean {
  return p === 'owner';
}
// Effective perm for a selection: a single entry's own perm (falls back to the
// directory perm), else the directory perm for multi-select / background.
function selPerm(sel: FileNode[]): string {
  if (sel.length === 1 && typeof sel[0]?.perm === 'string') return sel[0].perm as string;
  return dirPerm.value;
}
// Can the current user write into the directory being viewed? Gates the
// toolbar New Folder / Upload / Paste + drag-drop upload.
const canWriteHere = computed(() => permCanEdit(dirPerm.value));
// Empty-state affordances: the "drop files here" hint + upload button only
// make sense in a real writable folder (not the virtual drives root, not the
// trash view).
const emptyCanUpload = computed(
  () => canWriteHere.value && !atVirtualRoot.value && !trashMode.value,
);

// Context menu
const ctxRef = ref<InstanceType<typeof ContextMenu> | null>(null);
const rootEl = ref<HTMLElement | null>(null);
const toolbarRef = ref<InstanceType<typeof Toolbar> | null>(null);

/* bag:b4 — narrow/embed mini mode.
 * isNarrow: container width < 560px (ResizeObserver on the .fe root, so it
 * tracks the EMBED container, not the viewport) → root gets `fe--narrow`,
 * the toolbar collapses and the upload FAB appears.
 * isCoarse: touch-first device → context menus render as a bottom sheet. */
const isNarrow = ref(false);
const isCoarse = ref(false);
let narrowRO: ResizeObserver | undefined;
let coarseMq: MediaQueryList | undefined;
function syncCoarsePointer(e?: MediaQueryListEvent | MediaQueryList) {
  isCoarse.value = !!(e && 'matches' in e && e.matches);
}
onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && rootEl.value) {
    narrowRO = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect?.width ?? rootEl.value?.clientWidth ?? 0;
      isNarrow.value = w > 0 && w < 560;
    });
    narrowRO.observe(rootEl.value);
  }
  if (typeof window !== 'undefined' && window.matchMedia) {
    coarseMq = window.matchMedia('(pointer: coarse)');
    syncCoarsePointer(coarseMq);
    coarseMq.addEventListener?.('change', syncCoarsePointer);
  }
});
onBeforeUnmount(() => {
  narrowRO?.disconnect();
  narrowRO = undefined;
  coarseMq?.removeEventListener?.('change', syncCoarsePointer);
  coarseMq = undefined;
});
/* /bag:b4 */

// Toast (tiny, no lib). Evolved into a snackbar: plain messages keep the old
// 2.5s auto-hide; messages carrying an action ("Geri Al") stay 8s and can be
// dismissed by click or Esc.
interface ToastState {
  message: string;
  actionLabel?: string;
  action?: () => void | Promise<void>;
}
const toast = ref<ToastState | null>(null);
let toastTimer: ReturnType<typeof setTimeout> | undefined;
function showToast(state: ToastState, ms: number) {
  toast.value = state;
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.value = null), ms);
}
function flashToast(msg: string) {
  showToast({ message: msg }, 2500);
}
function undoToast(message: string, undo: () => Promise<void>) {
  showToast({ message, actionLabel: t('toast.undo'), action: undo }, 8000);
}
function dismissToast() {
  if (toastTimer) {
    clearTimeout(toastTimer);
    toastTimer = undefined;
  }
  toast.value = null;
}
async function runToastAction() {
  const act = toast.value?.action;
  dismissToast();
  if (!act) return;
  try {
    await act();
    flashToast(t('toast.undone'));
    await load();
  } catch {
    flashToast(t('toast.undo_failed'));
  }
}

// --------------------------------------------------------------------
// Data loading
// --------------------------------------------------------------------

// multiStorageRoot — when on, "/" is a virtual folder listing every
// configured storage as a clickable dir. Path semantics shift:
//
//   ""           → global root, list storages
//   "<storage>"  → that storage's root (api: `<storage>://`)
//   "<storage>/<rel>"  → deeper folder (api: `<storage>://<rel>`)
//
// `qualify()` is overridden inside this mode to translate the
// slash-separated user path into the wire `<adapter>://<rel>` form.
const multiStorageRoot = computed(() => props.config.multiStorageRoot === true);

function splitVirtualPath(p: string): { adapter: string; rel: string } {
  const clean = p.replace(/^\/+|\/+$/g, '');
  if (!clean) return { adapter: '', rel: '' };
  const slash = clean.indexOf('/');
  if (slash === -1) return { adapter: clean, rel: '' };
  return { adapter: clean.slice(0, slash), rel: clean.slice(slash + 1) };
}

function virtualToWire(p: string): string {
  // Convert `s3-test/example` → `s3-test://example`. Pass-through
  // when the input already carries `://` (legacy callers).
  if (p.includes('://')) return p;
  const { adapter, rel } = splitVirtualPath(p);
  if (!adapter) return ''; // global root — no wire form
  return rel ? `${adapter}://${rel}` : `${adapter}://`;
}

function wireToVirtual(p: string): string {
  // Convert `s3-test://example` → `s3-test/example`.
  const idx = p.indexOf('://');
  if (idx === -1) return p.replace(/^\/+|\/+$/g, '');
  const adapter = p.slice(0, idx);
  const rel = p.slice(idx + 3).replace(/^\/+|\/+$/g, '');
  return rel ? `${adapter}/${rel}` : adapter;
}

function virtualStorageRows(): FileNode[] {
  // Synthesize a FileNode for every configured storage. Used as the
  // "/" listing in multi-storage mode.
  const list = props.config.storages ?? [];
  return list.map((s) => ({
    type: 'dir',
    path: s.name, // virtual path (no adapter prefix)
    basename: s.label || s.name,
    extension: '',
    storage: s.name,
    visibility: 'private',
    file_size: 0,
    mime_type: 'inode/storage',
    extra_metadata: { driver: s.driver, readOnly: s.readOnly },
  } as unknown as FileNode));
}

async function load(path?: string) {
  loading.value = true;
  // Any normal navigation exits trash mode (the trash view is entered only
  // by opening the virtual `.trash` row, which calls loadTrash()).
  trashMode.value = false;
  let requested = path ?? currentPath.value ?? '';
  try {
    notFoundPath.value = '';
    loadError.value = '';
    // Clamp to the confined floor: an empty/above-floor request (incl. a stale
    // persisted path or the drives root) snaps back to rootPath. This both
    // suppresses the multi-storage drives list and blocks up-navigation.
    if (rootFloor) {
      const p = String(requested).replace(/^\/+|\/+$/g, '');
      if (!p || !(p === rootFloor || p.startsWith(rootFloor + '/'))) requested = rootFloor;
    }

    // Multi-storage virtual root — synthesize a list of storages
    // instead of calling the backend.
    if (multiStorageRoot.value && !virtualToWire(requested)) {
      currentPath.value = '';
      adapter.value = '';
      dirname.value = '';
      e2eRoot.value = ''; /* wiring:e2 — sanal kökte kilit ekranı olmaz */
      files.value = virtualStorageRows();
      return;
    }

    const target = multiStorageRoot.value
      ? virtualToWire(requested)
      : qualify(requested);

    const resp = searchQuery.value
      ? await api.search(target, searchQuery.value)
      : await api.index(target);
    adapter.value = resp.adapter;
    dirname.value = resp.dirname;
    dirPerm.value = (resp.perm as string) || '';
    /* wiring:e2 — backend tells us when this dir sits inside an encrypted
       subtree; '' resets on every plain folder. Drives the lock screen. */
    e2eRoot.value = typeof resp.e2e_root === 'string' ? resp.e2e_root : '';
    /* /wiring:e2 */
    files.value = (resp.files || []).filter((f) => {
      if (f.path.includes('.thumbs')) return false;
      if (f.path.includes('.versions') || f.basename === '.versions') return false;
      if (f.basename === '.trash') return false;
      if (f.basename === '.keepdir') return false;
      if (f.basename === E2E_MARKER_NAME) return false; /* wiring:e2 — marker gizli */
      return true;
    });
    // Inject virtual `.trash` entry at root only.
    const dirRel = stripAdapter(resp.dirname);
    const inRoot = dirRel === 'fileman' || dirRel === '';
    const isTrashListing = dirRel.startsWith('fileman/.trash');
    const trashEntryEnabled = props.config.trashVisible !== false;
    if (!isTrashListing && inRoot && trashEntryEnabled) {
      files.value.unshift({
        type: 'dir',
        path: `${resp.adapter}://fileman/.trash`,
        basename: '.trash',
        extension: '',
        storage: resp.adapter,
        visibility: 'private',
        size: 0,
        file_size: 0,
        mime_type: 'inode/directory',
        extra_metadata: {},
      } as unknown as FileNode);
      void hydrateTrashRow(resp.adapter);
    }
    // currentPath is the user-facing form: `s3-test/example` in
    // multi-storage mode, the bare relative path otherwise.
    currentPath.value = multiStorageRoot.value
      ? wireToVirtual(resp.dirname)
      : stripAdapter(resp.dirname);
  } catch (err) {
    const e = err instanceof Error ? err.message : String(err);
    const status = (err as { status?: number }).status;
    if (status === 404 || status === 403) {
      // Dead deep link (deleted folder, phantom path or RBAC-hidden dir):
      // show the dedicated not-found state instead of a toast over a stale
      // listing that reads as "this folder is empty".
      notFoundPath.value = String(requested);
      e2eRoot.value = ''; /* wiring:e2 — ölü linkte kilit ekranı kalmasın */
      files.value = [];
      emit('error', { message: e, context: { path } });
      return;
    }
    // Real failure (network, 5xx). Never swallowed: the error still emits, and
    // it surfaces either as the retryable error state (nothing else on screen)
    // or as the classic toast over the still-visible previous listing.
    loadError.value = e;
    loadErrorPath = typeof requested === 'string' ? requested : undefined;
    emit('error', { message: e, context: { path } });
    if (files.value.length > 0) flashToast(e);
  } finally {
    loading.value = false;
  }
}

function stripAdapter(p: string): string {
  const idx = p.indexOf('://');
  return idx === -1 ? p : p.slice(idx + 3);
}

// "Go to root" escape hatch on the not-found state. load('') clamps to the
// confined rootFloor on embeds, so this is safe everywhere.
function leaveNotFound() {
  notFoundPath.value = '';
  // Cold-load on a dead deep link: currentPath is still '' (the 404 never
  // committed it), so navigating to root doesn't change it and the
  // watch(currentPath) persistence never fires — the dead hash would
  // survive and a reload would land on the 404 again. Clear it explicitly;
  // if load('') clamps to a confined floor path, the watch fires with the
  // new path and rewrites the hash correctly anyway.
  writePersistedPath('');
  void load('');
}

// hydrateTrashRow — the virtual `.trash` row is synthesized with no size/date;
// fill both from the backend trash listing (total bytes + newest deletion) so
// the row reads like a real folder instead of "— / —". Best-effort and
// non-blocking: the row appears immediately and updates when the listing lands.
async function hydrateTrashRow(storage: string) {
  try {
    const { entries } = await api.listTrash(storage);
    const row = files.value.find((f) => f.basename === '.trash');
    if (!row) return;
    let total = 0;
    let newest = 0;
    for (const e of entries) {
      total += e.size || 0;
      const ts = Date.parse(e.deleted_at);
      if (!Number.isNaN(ts) && ts > newest) newest = ts;
    }
    row.size = total;
    if (newest > 0) row.last_modified = newest;
  } catch {
    /* keep the bare row */
  }
}

// loadTrash — show the backend trash (soft-deleted nodes) as a flat listing.
// Entered by opening the virtual `.trash` row. Each row keeps its node `id`
// so restore can target it. Permanent delete is admin-only / auto-purge, so
// the only mutation offered here is Restore.
async function loadTrash() {
  loading.value = true;
  trashOrigin.value = adapter.value || '';
  trashMode.value = true;
  e2eRoot.value = ''; /* wiring:e2 — çöp görünümü şifreli bağlamın dışındadır */
  selection.clear();
  try {
    const { entries } = await api.listTrash();
    files.value = entries.map(
      (e) =>
        ({
          type: 'file',
          id: e.id,
          path: e.storage_name ? `${e.storage_name}://${e.path}` : e.path,
          basename: e.name,
          extension: e.name.includes('.') ? e.name.split('.').pop() || '' : '',
          storage: e.storage_name || '',
          visibility: 'private',
          file_size: e.size,
          mime_type: e.mime || '',
          extra_metadata: { deleted_at: e.deleted_at, ttl_days: e.ttl_days ?? null },
        }) as unknown as FileNode,
    );
    dirname.value = '.trash';
    currentPath.value = '.trash';
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    emit('error', { message: msg, context: { op: 'trash-list' } });
    flashToast(msg);
  } finally {
    loading.value = false;
  }
}

/**
 * qualify — return `<adapter>://<rel>` for backend calls.
 *
 * The backend's manager handler picks a storage by parsing the
 * adapter prefix. Without one it falls back to `storages[0]`,
 * which 404s on every non-default storage (S3/SFTP/WebDAV in a
 * multi-storage install). All API callers (rename/move/delete/
 * upload/preview/download/share/copy) must use a qualified path.
 *
 * In multi-storage mode `currentPath` is `<storage>/<rel>` (no
 * `://`), so qualify forwards through `virtualToWire` which
 * splits the first segment off as the adapter. In single-storage
 * mode the legacy bare-relative path is glued onto `adapter.value`.
 *
 * `stripAdapter()` stays for cosmetic display logic only
 * (breadcrumb root check, inRoot computation, openPageBase).
 */
function qualify(p: string): string {
  if (p && p.includes('://')) return p;
  if (multiStorageRoot.value) {
    const wire = virtualToWire(p ?? '');
    if (wire) return wire;
    return adapter.value ? `${adapter.value}://` : '';
  }
  if (!p) return `${adapter.value}://`;
  return `${adapter.value}://${p.replace(/^\/+/, '')}`;
}

// ----------------------------------------------------------------
// Undo helpers — compute the inverse of cleanly-invertible operations
// (move → reverse move, rename → rename back, trash → restore). All
// paths here are wire form (`<adapter>://<rel>`).
// ----------------------------------------------------------------

function wireBasename(p: string): string {
  const idx = p.indexOf('://');
  const rel = (idx === -1 ? p : p.slice(idx + 3)).replace(/\/+$/, '');
  const slash = rel.lastIndexOf('/');
  return slash === -1 ? rel : rel.slice(slash + 1);
}

function wireParent(p: string): string {
  const idx = p.indexOf('://');
  const prefix = idx === -1 ? '' : p.slice(0, idx + 3);
  const rel = (idx === -1 ? p : p.slice(idx + 3)).replace(/\/+$/, '');
  const slash = rel.lastIndexOf('/');
  return slash === -1 ? prefix : prefix + rel.slice(0, slash);
}

/* ui-fix — iki wire yolun AYNI dizini gösterip göstermediği (trailing-slash
 * ve kök `adapter://` güvenli). Bir öğeyi ZATEN bulunduğu klasöre bırakma
 * (source parent === target) no-op olmalı; aksi halde backend "kendine
 * kopyala" 400'ü döner. */
function sameDir(a: string, b: string): boolean {
  const norm = (s: string) => {
    const i = s.indexOf('://');
    const pre = i === -1 ? '' : s.slice(0, i + 3);
    const rel = (i === -1 ? s : s.slice(i + 3)).replace(/\/+$/, '');
    return pre + rel;
  };
  return norm(a) === norm(b);
}

function wireJoin(dir: string, name: string): string {
  if (!dir) return name;
  return dir.endsWith('://') || dir.endsWith('/') ? dir + name : `${dir}/${name}`;
}

// Register the inverse of a queued async move under its op id: once the op
// settles OK, the toast offers "Geri Al" which queues the reverse move. The
// inverse op deliberately gets NO undo entry of its own (no redo ping-pong).
function registerMoveUndo(
  opId: number,
  sources: string[],
  targetWire: string,
  originWire: string | undefined,
) {
  if (!originWire || !targetWire) return;
  const movedPaths = sources.map((p) => wireJoin(targetWire, wireBasename(p)));
  if (movedPaths.length === 0) return;
  opUndo.set(opId, {
    message: t('toast.moved'),
    fn: async () => {
      const { op } = await api.moveAsync(movedPaths, originWire, targetWire);
      pendingOps.register(op);
    },
  });
}

watch(
  () => searchQuery.value,
  () => void load(),
);

// ----------------------------------------------------------------
// Path persistence
// ----------------------------------------------------------------
const PATH_LS_KEY = 'brf-file-explorer:path';

function persistMode(): 'hash' | 'localStorage' | 'hash+localStorage' | 'none' {
  return props.config.pathPersist ?? 'hash';
}

function hashPersistEnabled(): boolean {
  const m = persistMode();
  return m === 'hash' || m === 'hash+localStorage';
}

// A pasted/hand-edited hash can carry a stray `%` (a folder literally named
// "100%") that decodeURIComponent rejects — fall back to the raw text.
function safeDecode(s: string): string {
  try {
    return decodeURIComponent(s);
  } catch {
    return s;
  }
}

function readLsPath(): string {
  try {
    return localStorage.getItem(PATH_LS_KEY) || '';
  } catch {
    return '';
  }
}

function readHashPath(): string {
  const h = window.location.hash || '';
  if (!h.startsWith('#')) return '';
  return safeDecode(h.slice(1)).replace(/^\/+|\/+$/g, '');
}

function readPersistedPath(): string {
  if (typeof window === 'undefined') return '';
  const mode = persistMode();
  if (mode === 'none') return '';
  if (mode === 'localStorage') return readLsPath();
  const fromHash = readHashPath();
  if (fromHash || mode === 'hash') return fromHash;
  // hash+localStorage with an empty hash: an explicit start path
  // (?storage= deep link / rootPath floor) outranks the remembered folder.
  if (initialFloorPath) return '';
  return readLsPath();
}

function writePersistedPath(path: string) {
  if (typeof window === 'undefined') return;
  const mode = persistMode();
  if (mode === 'none') return;
  if (mode === 'localStorage' || mode === 'hash+localStorage') {
    try {
      if (path) localStorage.setItem(PATH_LS_KEY, path);
      else localStorage.removeItem(PATH_LS_KEY);
    } catch {
      /* private mode / quota */
    }
    if (mode === 'localStorage') return;
  }
  // Encode per segment so folder names with `%`/`#`/`?` survive the URL
  // round-trip while `/` separators stay readable.
  const encoded = path ? path.split('/').map(encodeURIComponent).join('/') : '';
  const target = encoded ? `#${encoded}` : '';
  if ((window.location.hash || '') === target) return;
  // replaceState never fires `hashchange`, so onHashChange only ever sees
  // genuine external edits (paste, back/forward) — no self-echo to suppress.
  history.replaceState(
    null,
    '',
    target || window.location.pathname + window.location.search,
  );
}

function onHashChange() {
  if (!hashPersistEnabled()) return;
  const p = readHashPath();
  if (p && p !== currentPath.value) {
    void load(p);
  }
}

watch(currentPath, (p) => {
  writePersistedPath(p);
  emit('navigate', { path: p });
  realtime.subscribe(realtimeRoom(p));
});

// Let a host force a soft re-fetch of the current folder (reusing the existing
// list-fetch) — used by the realtime layer to refresh on live change events
// without a full component remount.
defineExpose({ reload: () => load() });

onMounted(async () => {
  // Eagerly start fetching Monaco — the user doesn't pay for it
  // perceptually; click-to-edit hits an in-memory cache.
  preloadEditor();

  const fromPersist = readPersistedPath();
  await load(fromPersist || undefined);
  await nextTick();
  rootEl.value?.focus();
  // Best-effort initial fetch — silent if the older backend doesn't
  // expose /api/files/manager/starred. Without this stars never light
  // up on first render even when the row IS starred server-side.
  void loadStarred();
  if (hashPersistEnabled()) {
    window.addEventListener('hashchange', onHashChange);
  }
  if (api.endpoints.opsList) {
    pendingOps.startPolling();
  }
  if (api.endpoints.capabilities) {
    api
      .capabilities()
      .then((cap) => {
        capabilitiesData.value = cap;
      })
      .catch(() => {
        /* swallow — `onlyoffice_url` falls back to null */
      });
  }
});

// --------------------------------------------------------------------
// Keyboard
// --------------------------------------------------------------------

/* cila:c wiring — command palette (Ctrl/Cmd+K) + shortcuts help (?) state */
const showPalette = ref(false);
const showShortcutsHelp = ref(false);
/* /cila:c wiring */

/* bul:s3 — palette "everywhere" search + open-hit navigation */

// Debounce/min-chars live in the palette; this is just the API call.
function paletteGlobalSearch(q: string): Promise<GlobalSearchHit[]> {
  return api.globalSearch(q, { limit: 8, scope: 'all' });
}

/**
 * Open a global-search hit: navigate to the file's folder, then select +
 * preview it through the existing openNode mechanics. Hits come back as raw
 * node rows (in-storage relative `path`, numeric `storage_id`), so the
 * storage segment for multi-storage mode is resolved best-effort: an
 * explicit name on the hit (future backends) > the only configured storage
 * > the storage currently open. A wrong guess lands on the existing
 * "folder not found" state, which is already a graceful dead-end.
 */
async function openSearchHit(hit: GlobalSearchHit) {
  const rel = String(hit.path ?? '').replace(/^\/+|\/+$/g, '');
  if (!rel) return;
  const isDir = hit.type === 'dir';
  const slash = rel.lastIndexOf('/');
  const targetRel = isDir ? rel : slash === -1 ? '' : rel.slice(0, slash);
  let target = targetRel;
  if (multiStorageRoot.value) {
    const configured = props.config.storages ?? [];
    const storageName =
      (typeof hit.storage === 'string' && hit.storage) ||
      (typeof hit.storage_name === 'string' && hit.storage_name) ||
      (configured.length === 1 ? configured[0].name : '') ||
      adapter.value;
    if (!storageName) return;
    target = targetRel ? `${storageName}/${targetRel}` : storageName;
  }
  await load(target);
  if (isDir) return;
  const name = String(hit.name ?? rel.slice(slash + 1));
  const node = files.value.find((f) => f.type === 'file' && f.basename === name);
  if (node) {
    selection.click(node.path);
    openNode(node);
  }
}
/* /bul:s3 */

useKeyboardShortcuts(rootEl, {
  onDelete: () => {
    /* ui-fix — kısayol da aktif panele gider (menüyle tutarlı). */
    if (paneIsActive.value) {
      const psel = splitPaneRef.value?.selectedNodes() ?? [];
      if (psel.length) {
        paneCtxTargets.value = psel;
        mutationInPane.value = true;
        showDelete.value = true;
      }
    } else if (!selection.isEmpty.value) {
      mutationInPane.value = false;
      showDelete.value = true;
    }
  },
  onRename: () => {
    if (paneIsActive.value) {
      const psel = splitPaneRef.value?.selectedNodes() ?? [];
      if (psel.length === 1) {
        renameTarget.value = psel[0];
        mutationInPane.value = true;
        showRename.value = true;
      }
    } else if (selection.nodes.value.length === 1) {
      renameTarget.value = selection.nodes.value[0];
      mutationInPane.value = false;
      showRename.value = true;
    }
  },
  onSelectAll: () => (paneIsActive.value ? splitPaneRef.value?.selectAll() : selection.selectAll()) /* wiring:d1 pane-route */,
  onOpen: () => {
    if (paneIsActive.value) return splitPaneRef.value?.openSelected(); /* wiring:d1 pane-route */
    const n = selection.nodes.value[0];
    if (n) openNode(n);
  },
  onClose: () => {
    showNewFolder.value = false;
    showRename.value = false;
    showDelete.value = false;
    showShare.value = false;
    showPreview.value = false;
    ctxRef.value?.hide();
    dismissToast();
    /* koru:k1 — Esc closes the narrow-mode inspector overlay only; the wide
       side panel is a persistent surface toggled by `i` / the toolbar. */
    if (isNarrow.value) closeInspector();
  },
  onFocusSearch: () => toolbarRef.value?.focusSearch(),
  onCut: () => (paneIsActive.value ? paneCut() : cut()) /* wiring:d1 pane-route */,
  onCopy: () => (paneIsActive.value ? paneCopy() : copyToClipboard()) /* wiring:d1 pane-route */,
  onPaste: () => (paneIsActive.value ? void panePaste() : void paste()) /* wiring:d1 pane-route */,
  onGoUp: () => (paneIsActive.value ? splitPaneRef.value?.goUp() : goUp()) /* wiring:d1 pane-route */,
  /* cila:c wiring */
  onPathJump: () => {
    showPalette.value = true;
  },
  onShowHelp: () => {
    showShortcutsHelp.value = true;
  },
  /* /cila:c wiring */
  onQuickLook: () => quickLookToggle() /* wiring:c2 */,
  onToggleInspector: () => toggleInspector() /* koru:k1 */,
  /* wiring:d1 — sekme aksiyonları (registry: tab-new/close/next/prev) */
  onTabNew: () => newTabHere(),
  onTabClose: () => closeTabById(tabsActiveId.value),
  onTabNext: () => nextTab(),
  onTabPrev: () => prevTab(),
  /* /wiring:d1 */
  hasSelection: () => !selection.isEmpty.value,
});

// --------------------------------------------------------------------
// Actions
// --------------------------------------------------------------------

const OFFICE_EXTS = new Set([
  'docx', 'xlsx', 'pptx',
  'doc', 'xls', 'ppt',
  'odt', 'ods', 'odp',
]);
const TEXT_CODE_EXTS = new Set([
  'txt', 'md', 'markdown', 'log', 'csv', 'tsv', 'conf', 'ini',
  'env', 'toml', 'cfg',
  'json', 'jsonc', 'yaml', 'yml', 'xml', 'svg',
  'js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx',
  'css', 'scss', 'sass', 'less',
  'html', 'htm', 'vue', 'svelte',
  'php', 'py', 'rb', 'rs', 'go', 'java', 'kt', 'swift',
  'cpp', 'c', 'h', 'hpp', 'cs', 'dart',
  'sh', 'bash', 'zsh', 'sql', 'lua', 'pl', 'r',
  'dockerfile', 'gradle', 'gitignore',
]);

function openNode(n: FileNode) {
  // The virtual `.trash` row opens the backend trash listing, not a real dir.
  if (n.basename === '.trash') {
    void loadTrash();
    return;
  }
  if (n.type === 'dir') {
    // Multi-storage virtual rows have a bare path (`s3-test`); pass
    // them straight to load() which will treat them as the wire form
    // for that storage's root. Real backend rows still come back as
    // `<adapter>://<rel>` and stripAdapter turns them into the user
    // path semantics load() expects.
    const target = multiStorageRoot.value
      ? wireToVirtual(n.path)
      : stripAdapter(n.path);
    void load(target);
    return;
  }
  /* wiring:e2 — şifreli klasörde dosya açma: kilit açıksa çöz + salt-okunur
     önizleme (blob URL mevcut viewer'lara gider); kilitliyken hiçbir şey
     açılmaz (kilit ekranı zaten listeyi kapatır). */
  if (e2eUnlocked.value && n.type === 'file') {
    void e2eOpenPreview(n);
    return;
  }
  if (e2eLocked.value && n.type === 'file') return;
  /* /wiring:e2 */
  // "Aç" / double-click contract: open in a new tab against the
  // standalone editor route, regardless of file type. The editor page
  // picks the right viewer (OnlyOffice for office, Monaco for code/
  // text, drawio iframe for .drawio, image/PDF/3D viewers otherwise)
  // and wires save-on-change. This is the shape brf-mono ships and
  // what users expect from a Files-style file manager.
  //
  // Capability gate: if we already know the required backend is offline
  // (OnlyOffice for office docs, drawio for diagrams), don't launch a
  // new tab that we'd just render a "service not configured" fallback
  // inside — drop into the in-page preview instead, which is the same
  // dead-end UI but without the tab-switching whiplash.
  // Double-click contract: in-page modal preview. Office docs and
  // other read-only kinds open in view mode so a quick peek doesn't
  // mount an editing surface on top of the content. Code/markdown
  // open in edit so the user gets the fast "open, tweak, Ctrl+S"
  // loop. Modal's "Yeni sekmede aç" button still launches the
  // standalone fullscreen editor route when richer editing is wanted.
  const ext = (n.extension || '').toLowerCase();
  // RBAC: viewers (no edit on this item) always get the read-only preview
  // modal — never the editable surface. This is the "view vs edit" split.
  previewMode.value = permCanEdit((n.perm as string) ?? dirPerm.value)
    ? previewModeForExt(ext)
    : 'view';
  previewTarget.value = n;
  showPreview.value = true;
  emit('file-opened', { path: n.path, basename: n.basename });
  void markRecent(n);
}

const VIEW_DEFAULT_EXTS = new Set<string>([
  ...OFFICE_EXTS,
  'drawio', 'dio',
  'pdf', 'epub', 'ipynb', 'tiff', 'tif', 'psd',
  'mmd', 'mermaid',
  'glb', 'gltf', 'obj', 'stl', 'fbx', '3ds',
  'zip', 'rar', '7z', 'tar', 'gz', 'tgz',
  'jpg', 'jpeg', 'png', 'webp', 'gif', 'bmp', 'avif', 'svg', 'heic',
  'mp4', 'webm', 'mov', 'mkv', 'm4v', 'ogv',
  'mp3', 'wav', 'ogg', 'flac', 'm4a', 'aac', 'opus',
]);

function previewModeForExt(ext: string): 'view' | 'edit' {
  if (VIEW_DEFAULT_EXTS.has(ext)) return 'view';
  return 'edit';
}

async function restoreSelection(targets?: FileNode[]) {
  const nodes = targets ?? selection.nodes.value;
  if (nodes.length === 0) return;
  try {
    // filex trash: restore by node id, then refresh the trash listing.
    if (trashMode.value) {
      const ids = nodes
        .map((n) => (n as { id?: number }).id)
        .filter((x): x is number => typeof x === 'number');
      const { restored } = await api.restoreIds(ids);
      flashToast(`${restored} öğe geri getirildi`);
      selection.clear();
      await loadTrash();
      return;
    }
    // Legacy path-based restore (brf-mono `.trash/` convention).
    if (!api.endpoints.restore) return;
    const items = nodes.map((n) => n.path); // qualified
    const { restored } = await api.restore(items);
    flashToast(`${restored} öğe geri getirildi`);
    selection.clear();
    await load();
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'restore' } });
  }
}

function previewNode(n: FileNode) {
  /* wiring:e2 — önizleme de çözülmüş blob'dan beslenir */
  if (e2eUnlocked.value && n.type === 'file') {
    void e2eOpenPreview(n);
    return;
  }
  /* /wiring:e2 */
  previewMode.value = 'view';
  previewTarget.value = n;
  showPreview.value = true;
  void markRecent(n);
}

/**
 * openNodeInNewTab — launches the standalone /files/edit route in a
 * fresh tab. Used by the context-menu "Aç" action; double-click stays
 * on the in-page modal path. Dirs still navigate inline (no editor for
 * directories). Falls back to the modal if no `openPageBase` is wired
 * by the embedder.
 */
function openNodeInNewTab(n: FileNode) {
  if (n.type === 'dir') {
    const target = multiStorageRoot.value
      ? wireToVirtual(n.path)
      : stripAdapter(n.path);
    void load(target);
    return;
  }
  /* wiring:e2 — standalone editör rotası sunucudan HAM (şifreli) baytı
     çeker; şifreli klasörde her "Aç" in-page çözülmüş önizlemeye iner. */
  if (e2eActive.value) {
    if (e2eUnlocked.value) void e2eOpenPreview(n);
    return;
  }
  /* /wiring:e2 */
  // RBAC: a viewer (no edit on this item) can't use the editable "Aç"
  // surface — drop to the read-only in-page preview instead.
  if (!permCanEdit((n.perm as string) ?? dirPerm.value)) {
    previewNode(n);
    return;
  }
  const ext = (n.extension || '').toLowerCase();
  const base = props.config.openPageBase;
  if (!base) {
    // Embedder didn't supply a standalone editor route — keep the
    // in-page modal as the only available affordance.
    openNode(n);
    return;
  }
  // Context-menu "Aç" is the intent-to-edit action — request edit
  // mode so OnlyOffice / Monaco mount with write permissions.
  // Read-only inspection lives on the "Önizle" entry + the dbl-click
  // in-page modal.
  const sep = base.includes('?') ? '&' : '?';
  const url =
    `${base}${sep}path=${encodeURIComponent(n.path)}` +
    `&type=${encodeURIComponent(ext)}` +
    `&mode=edit`;
  window.open(url, '_blank', 'noopener');
  emit('file-opened', { path: n.path, basename: n.basename });
  void markRecent(n);
}

type ContextMode = 'selection' | 'breadcrumb' | 'pane' /* ui-fix — yan panel sağ-tık */;
const ctxMode = ref<ContextMode>('selection');
const breadcrumbCtxPath = ref<string>('');
/* ui-fix — yan panel sağ-tık menüsünün hedef node'ları (pane selection). */
const paneCtxTargets = ref<FileNode[]>([]);
const breadcrumbCtxLabel = ref<string>('');

const selectionMode = computed<SelectionMode>(() => {
  const sel = selection.nodes.value;
  if (sel.length === 0) return 'none';
  if (sel.length === 1) return sel[0].type === 'dir' ? 'single-dir' : 'single-file';
  return 'multi';
});

async function onToolbarAction(key: string) {
  const sel = selection.nodes.value;
  // The toolbar's "Aç" opens the in-page preview/editor modal (quick peek);
  // everything else shares dispatchItemAction with the context menu so the two
  // identical menus also behave identically.
  if (key === 'open') {
    if (sel[0]) openNode(sel[0]);
    return;
  }
  await dispatchItemAction(key, sel);
}

async function onContextTarget(node: FileNode, ev: MouseEvent) {
  ctxMode.value = 'selection';
  if (!selection.has(node.path)) {
    selection.click(node.path);
    await nextTick();
  }
  ctxRef.value?.show({ clientX: ev.clientX, clientY: ev.clientY }, selection.nodes.value);
}

function onContextCanvas(ev: MouseEvent) {
  ev.preventDefault();
  ctxMode.value = 'selection';
  selection.clear();
  ctxRef.value?.show({ clientX: ev.clientX, clientY: ev.clientY }, []);
}

function onCrumbContext(payload: { x: number; y: number; adapterPath: string; label: string }) {
  ctxMode.value = 'breadcrumb';
  breadcrumbCtxPath.value = payload.adapterPath;
  breadcrumbCtxLabel.value = payload.label;
  ctxRef.value?.show({ clientX: payload.x, clientY: payload.y }, []);
}

/* ui-fix — yan (ikincil) panelde sağ-tık: pane'i aktifleştir + menüyü aç.
 * Menü ana panelle BİREBİR aynı (selectionActionList tek kaynak); aksiyonlar
 * dispatchItemAction'a gider ve ctxMode==='pane' iken pane-route'lanır. */
function onPaneContext(node: FileNode | null, ev: MouseEvent) {
  activePane.value = 'split';
  const sel = splitPaneRef.value?.selectedNodes() ?? [];
  // node=null (boş alana sağ-tık): seçimsiz menü (Yeni Klasör + Yapıştır).
  // Aksi halde pane seçimi (yoksa tıklanan node) hedeftir.
  paneCtxTargets.value = node ? (sel.length > 0 ? sel : [node]) : [];
  ctxMode.value = 'pane';
  ctxRef.value?.show({ clientX: ev.clientX, clientY: ev.clientY }, paneCtxTargets.value);
}

const contextActions = computed<ContextAction[]>(() => {
  if (ctxMode.value === 'breadcrumb') {
    return [
      { key: 'open', label: t('ctx.open'), icon: '↗' },
      { key: 'copy-path', label: t('breadcrumb.copy_path'), icon: '📋' },
    ];
  }
  if (ctxMode.value === 'pane' /* ui-fix — yan panel menüsü ana panelle BİREBİR aynı */) {
    const psel = paneCtxTargets.value;
    if (psel.length === 0) {
      // Boş alana sağ-tık: ana paneldeki canvas menüsüyle aynı.
      return [
        { key: 'new-folder', label: t('toolbar.new_folder'), icon: '📁' },
        { key: 'paste', label: t('ctx.paste'), icon: '📋', disabled: !clipboard.value.mode },
      ];
    }
    return selectionActionList(psel);
  }

  const sel = selection.nodes.value;
  const any = sel.length > 0;
  const single = sel.length === 1;

  if (trashActive.value) {
    if (!any) return [];
    return [
      { key: 'restore', label: t('ctx.restore'), icon: '↩' },
      { divider: true, key: 'sep1', label: '' },
      { key: 'delete', label: t('ctx.delete_perm'), icon: '🗑', danger: true },
    ];
  }

  // Storage roots (the virtual rows shown at the multi-storage "/"
  // overview) aren't real filesystem entries — they're mount points.
  // Hide every mutation entry (rename/delete/share/cut/copy/new-folder/
  // paste) and only offer "Aç" so the menu doesn't surface actions
  // that would 4xx on the backend.
  //
  // PRIOR BUG: this used `currentPath === '/'` but the load() branch
  // for the virtual root sets currentPath to EMPTY string, not '/'.
  // So the guard never fired and every mutation action leaked into
  // the menu at the depo listing — including new-folder + paste,
  // which Burak called out in the most direct possible terms. Use
  // the same empty-after-trim test as `atVirtualRoot` above.
  const trimmedPath = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  const inStorageRoot = multiStorageRoot.value && trimmedPath === '';
  if (inStorageRoot) {
    if (!any) return [];
    if (!single) return [];
    return [
      { key: 'open', label: t('ctx.open'), icon: '↗' },
      { key: 'open-tab', label: t('ctx.open_new_tab'), icon: '⧉' } /* wiring:d1 */,
    ];
  }

  // Empty background right-click: folder-level actions only. Viewers (no edit
  // on this dir) get nothing here.
  if (!any) {
    if (!permCanEdit(dirPerm.value)) return [];
    return [
      { key: 'new-folder', label: t('toolbar.new_folder'), icon: '📁' },
      { key: 'paste', label: t('ctx.paste'), icon: '📋', disabled: !clipboard.value.mode },
    ];
  }

  return selectionActionList(sel);
});

// selectionActionList — the SINGLE source of truth for the actions offered on a
// selection. BOTH the right-click context menu AND the top toolbar render this
// exact list so they can never drift apart (Burak: "sağ klik menüyle üst menü
// tutmuyor"). The toolbar filters out dividers/hidden; the context menu shows
// them. Action handling is unified in dispatchItemAction().
function selectionActionList(sel: FileNode[]): ContextAction[] {
  const any = sel.length > 0;
  const single = sel.length === 1;
  const isFile = single && sel[0]?.type === 'file';
  const tagsLabel = locale.value === 'en' ? 'Tags…' : 'Etiketler…';
  const singleHasId = single && typeof sel[0]?.id === 'number';
  const copyIdLabel = locale.value === 'en' ? 'Copy node id' : "Node id'yi kopyala";
  // RBAC: gate mutating actions when the caller lacks edit on the target. The
  // "İzinler" (permissions) action shows only for owners on RBAC-on storages.
  const p = selPerm(sel);
  const w = permCanEdit(p); // may write here
  // Unified "Paylaş / İzinler" popup: public share link (editor+) + per-user
  // permissions (owner-only, decided inside the modal).
  // Unified "Paylaş / İzinler" popup carries the public share link, per-user
  // permissions AND the folder-only "Dosya İste" (file-drop) tab — the user
  // picks the action from inside the modal, so there's no separate button.
  const accessLabel = locale.value === 'en' ? 'Share / Permissions' : 'Paylaş / İzinler';
  return [
    { key: 'open', label: t('ctx.open'), icon: '↗', hidden: !single },
    { key: 'open-tab', label: t('ctx.open_new_tab'), icon: '⧉', hidden: !single || sel[0]?.type !== 'dir' } /* wiring:d1 — klasörü yeni sekmede aç */,
    { key: 'preview', label: t('ctx.preview'), icon: '👁', hidden: !single, disabled: !isFile },
    { key: 'download', label: t('ctx.download'), icon: '⬇', hidden: !single, disabled: !isFile },
    { key: 'convert', label: t('ctx.convert'), icon: '🔄', hidden: !single || !effectiveConvertUrl.value || !w || e2eActive.value /* wiring:e2 — convert ciphertext'e anlamsız */, disabled: !isFile },
    { key: 'access', label: accessLabel, icon: '🔗', hidden: !single || !w || e2eActive.value /* wiring:e2 — paylaşım MVP'de kapalı (link ciphertext verir) */ },
    { key: 'details', label: t('ctx.details'), icon: 'ℹ', hidden: !any } /* koru:k1 */,
    { key: 'copy-id', label: copyIdLabel, icon: '🆔', hidden: !singleHasId, disabled: !singleHasId },
    { divider: true, key: 'sep1', label: '', hidden: !w },
    { key: 'rename', label: t('ctx.rename'), icon: '✎', hidden: !single || !w, disabled: !single },
    { key: 'cut', label: t('ctx.cut'), icon: '✂', hidden: !any || !w, disabled: !any },
    { key: 'copy', label: t('ctx.copy'), icon: '❐', hidden: !any, disabled: !any },
    { key: 'paste', label: t('ctx.paste'), icon: '📋', hidden: !w, disabled: !clipboard.value.mode },
    { divider: true, key: 'sep-meta', label: '', hidden: !singleHasId },
    { key: 'tags', label: tagsLabel, icon: '🏷', hidden: !singleHasId, disabled: !singleHasId },
    { divider: true, key: 'sep2', label: '', hidden: !w },
    { key: 'delete', label: t('ctx.delete'), icon: '🗑', danger: true, hidden: !any || !w, disabled: !any },
  ];
}

// toolbarActions — what the top toolbar shows. Mirrors the context menu so the
// two stay identical for a selection; the empty/trash/virtual-root cases match
// the context menu's special branches.
const toolbarActions = computed<ContextAction[]>(() => {
  const sel = selection.nodes.value;
  if (trashActive.value) {
    if (sel.length === 0) return [];
    return [
      { key: 'restore', label: t('ctx.restore'), icon: '↩' },
      { key: 'delete', label: t('ctx.delete_perm'), icon: '🗑', danger: true },
    ];
  }
  const trimmedPath = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  if (multiStorageRoot.value && trimmedPath === '') {
    return sel.length === 1 ? [{ key: 'open', label: t('ctx.open'), icon: '↗' }] : [];
  }
  if (sel.length === 0) return [];
  return selectionActionList(sel);
});

async function onContextAction(action: ContextAction, targets: FileNode[]) {
  if (ctxMode.value === 'breadcrumb') {
    if (action.key === 'open') {
      void load(stripAdapter(breadcrumbCtxPath.value));
    } else if (action.key === 'copy-path') {
      await onCopyPath(breadcrumbCtxPath.value);
    }
    return;
  }
  if (ctxMode.value === 'pane' /* ui-fix — yan panel: aynı dispatch, pane-route */) {
    await dispatchItemAction(action.key, paneCtxTargets.value);
    return;
  }
  await dispatchItemAction(action.key, targets);
}

// dispatchItemAction — unified handler for an action key on a target set. Both
// the right-click menu (onContextAction) and the toolbar (onToolbarAction)
// route here, so the two menus that now render the SAME list also behave the
// same. (Toolbar "Aç" is the one deliberate exception — see onToolbarAction.)
async function dispatchItemAction(key: string, targets: FileNode[]) {
  switch (key) {
    case 'open':
      // Context-menu "Aç" launches the standalone fullscreen route
      // in a new tab. Double-click (openNode) opens the in-page
      // modal — two distinct affordances on purpose: quick peek vs
      // dedicated editing surface.
      if (targets[0]) openNodeInNewTab(targets[0]);
      break;
    case 'preview':
      if (targets[0]) previewNode(targets[0]);
      break;
    case 'download':
      if (targets[0]) downloadFile(targets[0]);
      break;
    case 'convert':
      if (targets[0]) openConvert(targets[0]);
      break;
    case 'share':
      if (targets[0]) openShare(targets[0]);
      break;
    case 'access':
      if (targets[0]) {
        permTarget.value = targets[0];
        showPerm.value = true;
      }
      break;
    case 'details' /* koru:k1 */:
      openInspector();
      break;
    case 'copy-id':
      if (targets[0] && typeof targets[0].id === 'number') {
        const id = targets[0].id;
        navigator.clipboard?.writeText(String(id)).then(
          () => flashToast(locale.value === 'en' ? `Node id ${id} copied` : `Node id ${id} kopyalandı`),
          () => flashToast(`#${id}`),
        );
      }
      break;
    case 'tags':
      if (targets[0]) openTagPickerFor(targets[0]);
      break;
    case 'rename':
      if (targets[0]) {
        renameTarget.value = targets[0];
        mutationInPane.value = ctxMode.value === 'pane'; /* ui-fix */
        showRename.value = true;
      }
      break;
    case 'cut':
      /* ui-fix — pane bağlamında pano kaynağı pane'in dizini olmalı. */
      if (ctxMode.value === 'pane') paneCut();
      else {
        clipboard.value = { mode: 'cut', items: targets, sourcePath: currentPath.value };
        flashToast('Kes → Yapıştır hazır');
      }
      break;
    case 'copy':
      if (ctxMode.value === 'pane') paneCopy();
      else {
        clipboard.value = { mode: 'copy', items: targets, sourcePath: currentPath.value };
        flashToast('Kopyala → Yapıştır hazır');
      }
      break;
    case 'paste':
      /* ui-fix — sağ-klik menüsünden yapıştırma da aktif panele gider
       * (klavye kısayolu zaten pane-route'luydu; menü değildi). */
      if (ctxMode.value === 'pane' || paneIsActive.value) await panePaste();
      else await paste();
      break;
    case 'delete':
      mutationInPane.value = ctxMode.value === 'pane'; /* ui-fix */
      showDelete.value = true;
      break;
    case 'restore':
      if (targets.length > 0) await restoreSelection(targets);
      break;
    case 'new-folder':
      mutationInPane.value = ctxMode.value === 'pane'; /* ui-fix */
      showNewFolder.value = true;
      break;
    case 'duplicate':
      if (targets[0]) await duplicate(targets[0]);
      break;
    /* wiring:d1 — sağ-tık "Yeni sekmede aç" */
    case 'open-tab':
      if (targets[0]) openNodeInTab(targets[0]);
      break;
    /* /wiring:d1 */
  }
}

function cut() {
  if (selection.isEmpty.value) return;
  clipboard.value = { mode: 'cut', items: selection.nodes.value, sourcePath: currentPath.value };
  flashToast('Kesildi');
}

function copyToClipboard() {
  if (selection.isEmpty.value) return;
  clipboard.value = { mode: 'copy', items: selection.nodes.value, sourcePath: currentPath.value };
  flashToast('Kopyalandı');
}

async function paste() {
  const cb = clipboard.value;
  if (!cb.mode || cb.items.length === 0) return;
  try {
    const items = cb.items.map((n) => n.path); // already qualified (adapter://rel)
    const sourceDir = cb.sourcePath || '';
    const sameDir = cb.mode === 'cut' && sourceDir === currentPath.value;
    if (sameDir) {
      flashToast('Aynı klasöre kesilemez');
      return;
    }

    if (cb.mode === 'cut') {
      const targetWire = qualify(currentPath.value);
      const originWire = qualify(sourceDir) || undefined;
      const { op } = await api.moveAsync(items, targetWire, originWire);
      registerMoveUndo(op.id, items, targetWire, originWire);
      pendingOps.register(op);
      flashToast('Taşıma kuyruğa alındı');
    } else {
      const { op } = await api.copy(items, qualify(currentPath.value));
      pendingOps.register(op);
      flashToast('Kopyalama kuyruğa alındı');
    }
    clipboard.value = { mode: null, items: [], sourcePath: null };
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'paste' } });
  }
}

async function duplicate(n: FileNode) {
  try {
    const { op } = await api.copy([n.path], qualify(currentPath.value));
    pendingOps.register(op);
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'duplicate' } });
  }
}

function downloadFile(n: FileNode) {
  /* wiring:e2 — şifreli klasörde indirme: baytları çek, çöz, orijinal adla
     kaydet (ham ciphertext'i kullanıcıya vermek anlamsız). */
  if (e2eUnlocked.value) {
    void e2eDownload(n);
    return;
  }
  /* /wiring:e2 */
  // Keep `<adapter>://<rel>` so backend resolves the right storage
  // (stripping it would default to the first storage, which 404s for
  // any non-default storage like S3/SFTP/WebDAV).
  const url = api.downloadUrl(n.path);
  window.open(url, '_blank');
}

// ------- Modals -------

async function submitNewFolder(name: string) {
  const inPane = mutationInPane.value; /* ui-fix — yan panelde yeni klasör */
  try {
    const dirWire = inPane ? qualify(splitPaneRef.value?.getPath() ?? '') : qualify(currentPath.value);
    await api.newFolder(dirWire, name);
    showNewFolder.value = false;
    if (inPane) await splitPaneRef.value?.reload();
    else await load();
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'newfolder' } });
  }
}

async function submitRename(name: string) {
  const target = renameTarget.value;
  if (!target) return;
  const inPane = mutationInPane.value; /* ui-fix — yan panelden yeniden adlandırma */
  try {
    const dirWire = inPane ? qualify(splitPaneRef.value?.getPath() ?? '') : qualify(currentPath.value);
    const oldPath = target.path; // qualified
    const oldName = target.basename;
    await api.rename(dirWire, oldPath, name);
    showRename.value = false;
    renameTarget.value = null;
    if (inPane) await splitPaneRef.value?.reload();
    else await load();
    // Clean inverse: rename the new path back to the old basename.
    if (name && name !== oldName) {
      const newPath = wireJoin(wireParent(oldPath), name);
      undoToast(t('toast.renamed'), async () => {
        await api.rename(dirWire, newPath, oldName);
      });
    }
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'rename' } });
  }
}

async function confirmDelete() {
  // In the trash view, items are already soft-deleted. Permanent removal is
  // admin-only (and the backend auto-purges after the retention window), so
  // offer Restore here rather than a delete that would just re-trash a path.
  if (trashMode.value) {
    showDelete.value = false;
    flashToast('Çöpteki öğeler saklama süresi sonunda otomatik silinir. Kalıcı silme yönetici panelinden yapılır.');
    return;
  }
  /* ui-fix — yan panelden silme: hedefler + dizin + tazeleme pane'e ait. */
  const inPane = mutationInPane.value;
  const targets = inPane ? paneCtxTargets.value : selection.nodes.value;
  const dirWire = inPane ? qualify(splitPaneRef.value?.getPath() ?? '') : qualify(currentPath.value);
  const items = targets.map((n) => n.path);
  if (items.length === 0) {
    showDelete.value = false;
    return;
  }
  // Trash-delete is invertible via node-id restore — but only when EVERY
  // selected node carries a backend id and the restore endpoint exists.
  // A partial-undo offer would be a lie, so all-or-nothing.
  const nodeIds = targets
    .map((n) => (n as { id?: number }).id)
    .filter((x): x is number => typeof x === 'number');
  const restoreUndo =
    api.endpoints.trashRestore && nodeIds.length === targets.length
      ? async () => {
          const { restored } = await api.restoreIds(nodeIds);
          if (restored === 0) throw new Error('restore failed');
        }
      : null;
  try {
    if (api.endpoints.deleteAsync) {
      const { op } = await api.deleteAsync(items, dirWire);
      if (restoreUndo) {
        opUndo.set(op.id, { message: t('toast.trashed'), fn: restoreUndo });
      }
      pendingOps.register(op);
      flashToast('Silme kuyruğa alındı');
    } else {
      await api.deleteItems(dirWire, items);
      if (inPane) await splitPaneRef.value?.reload();
      else await load();
      if (restoreUndo) undoToast(t('toast.trashed'), restoreUndo);
    }
    showDelete.value = false;
    if (inPane) void splitPaneRef.value?.reload();
    else selection.clear();
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'delete' } });
  }
}

function openShare(n: FileNode) {
  shareTarget.value = n;
  activeShare.value = null;
  showShare.value = true;
}

function openConvert(n: FileNode) {
  convertTarget.value = n;
  showConvert.value = true;
}

function onConvertDone(name: string) {
  flashToast(locale.value === 'en' ? `Converted → ${name}` : `Dönüştürüldü → ${name}`);
  void load();
}

async function submitShare(payload: {
  password: boolean;
  expires_at: string | null;
  max_downloads: number | null;
}) {
  const target = shareTarget.value;
  if (!target) return;
  try {
    const { share } = await api.createShare({
      path: target.path, // qualified `<adapter>://<rel>`
      password: payload.password,
      expires_at: payload.expires_at,
      max_downloads: payload.max_downloads,
    });
    activeShare.value = share;
    emit('share-created', { path: target.path, url: share.url, pin: share.password_pin ?? null });
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'share' } });
  }
}

function closeShare() {
  showShare.value = false;
  shareTarget.value = null;
  activeShare.value = null;
}

// ------- Upload -------

function triggerUpload() {
  if (!canWriteHere.value) {
    flashToast(locale.value === 'en' ? 'Read-only here' : 'Burada yazma yetkiniz yok');
    return;
  }
  fileInputEl.value?.click();
}

function onFilePicked(ev: Event) {
  const input = ev.target as HTMLInputElement;
  const list = input.files ? Array.from(input.files) : [];
  input.value = '';
  void uploadFiles(list);
}

async function uploadFiles(list: File[]) {
  if (list.length === 0) return;
  /* wiring:e2 — şifreli klasörde upload şeffaf şifrelenir. Kilitliyken
     yükleme yok (düz metin sızdırma kapısı olurdu); 200MB üstü MVP
     tek-shot sınırına takılır ve uyarıyla atlanır. */
  if (e2eLocked.value) {
    flashToast(t('e2e.upload.locked'));
    return;
  }
  if (e2eUnlocked.value) {
    list = await e2eEncryptUploads(list);
    if (list.length === 0) return;
  }
  /* /wiring:e2 */
  const canChunk = !!(api.endpoints.uploadInit && api.endpoints.uploadFinalize);
  for (const f of list) {
    // Chunked (S3 multipart) only when the endpoints exist AND the file is
    // large. If chunked isn't viable (storage has no multipart support —
    // e.g. the local driver — or init errors out) fall back to the legacy
    // single-POST upload, which works for any storage / file size.
    if (canChunk && f.size >= 10 * 1024 * 1024) {
      if (await chunkedUpload(f)) continue;
    }
    await legacyUpload(f);
  }
  await load();
}

async function legacyUpload(file: File) {
  // Register a progress row so the corner badge tracks the upload — large files
  // fall back here from the chunked path, and previously showed no progress at
  // all (the chunked placeholder was removed on init failure and the legacy
  // POST tracked nothing, so the badge vanished mid-upload).
  const id = crypto.randomUUID();
  const target = qualify(currentPath.value);
  uploadJobs.value = [
    ...uploadJobs.value,
    { id, file, path: target, totalBytes: file.size, uploadedBytes: 0, percent: 0, status: 'uploading', cancel() {} },
  ];
  const patch = (p: Partial<UploadJob>) => {
    const idx = uploadJobs.value.findIndex((j) => j.id === id);
    if (idx === -1) return;
    const next = [...uploadJobs.value];
    next[idx] = { ...next[idx], ...p };
    uploadJobs.value = next;
  };
  try {
    await api.uploadMultipart(target, [file], (percent) => {
      patch({ percent, uploadedBytes: Math.round((percent / 100) * file.size) });
      emit('upload-progress', { uploadId: id, percent, done: percent >= 100 });
    });
    patch({ percent: 100, uploadedBytes: file.size, status: 'done' });
    emit('upload-progress', { uploadId: id, percent: 100, done: true });
  } catch (err) {
    patch({ status: 'error' });
    emit('error', {
      message: (err as Error).message,
      context: { op: 'upload', file: file.name },
    });
  }
}

/**
 * Attempt an S3 multipart (chunked) upload. Returns `true` on success,
 * `false` when the storage can't do multipart (local driver, init 4xx/5xx)
 * so the caller can transparently fall back to the legacy single-POST
 * upload. On failure the progress placeholder is removed — no stuck error
 * row, no error toast, because the fallback path will report any real error.
 */
async function chunkedUpload(file: File): Promise<boolean> {
  // Register the progress row LAZILY — only once init succeeded and bytes are
  // actually moving. A doomed init (local driver / 4xx) then shows no badge at
  // all, so the legacy fallback's own badge is the only one the user sees (no
  // appear-then-vanish flicker).
  const id = crypto.randomUUID();
  let registered = false;
  try {
    await chunked.uploadFile({
      path: qualify(currentPath.value),
      file,
      onProgress: (job) => {
        if (!registered) {
          if (job.status !== 'uploading' && job.uploadedBytes <= 0) return;
          uploadJobs.value = [...uploadJobs.value, { ...job, id } as UploadJob];
          registered = true;
        } else {
          const idx = uploadJobs.value.findIndex((j) => j.id === id);
          if (idx !== -1) {
            const next = [...uploadJobs.value];
            next[idx] = { ...job, id } as UploadJob;
            uploadJobs.value = next;
          }
        }
        emit('upload-progress', {
          uploadId: job.uploadId ?? id,
          percent: job.percent,
          done: job.status === 'done',
        });
      },
    });
    return true;
  } catch {
    if (registered) uploadJobs.value = uploadJobs.value.filter((j) => j.id !== id);
    return false;
  }
}

const dragCounter = ref(0);
const dragOver = ref(false);

/**
 * isExternalFileDrag — `true` only when the user is dragging files
 * INTO the page from the OS (file picker, finder, etc.). Filters out:
 *   - internal row drags (FE_DND_MIME present)
 *   - browser image drags (`<img draggable=true>` on this page or
 *     across pages). HTML5 `Files` type is leaky — it appears when
 *     dragging any image element even though no real file is moving;
 *     `dataTransfer.items[*].kind === 'file'` is the canonical signal
 *     for an actual OS file.
 */
function isExternalFileDrag(ev: DragEvent): boolean {
  const dt = ev.dataTransfer;
  if (!dt) return false;
  if (dt.types && dt.types.includes(FE_DND_MIME)) return false;
  // Some browsers expose `items` early in the drag, others only on
  // drop. When `items` is available we use it as the authoritative
  // signal — `kind === 'file'` means a real OS file. When unavailable
  // (Firefox during dragover sometimes returns 0 items), fall back to
  // the legacy `Files` type check.
  if (dt.items && dt.items.length > 0) {
    let hasFile = false;
    for (const it of Array.from(dt.items)) {
      if (it.kind === 'file') {
        hasFile = true;
        break;
      }
    }
    return hasFile;
  }
  return dt.types ? dt.types.includes('Files') : false;
}

function onDragEnter(ev: DragEvent) {
  if (!isExternalFileDrag(ev)) return;
  ev.preventDefault();
  dragCounter.value++;
  dragOver.value = true;
}
function onDragLeave() {
  dragCounter.value = Math.max(0, dragCounter.value - 1);
  if (dragCounter.value === 0) dragOver.value = false;
}
function onDragOver(ev: DragEvent) {
  /* wiring:d1 — iç sürüklemeler kök gövdeye de bırakılabilir olmalı ki split
     panelinden ana panelin BOŞLUĞUNA bırakmak çalışsın (origin dragover'da
     okunamaz — karar drop anında verilir; aynı-klasör drop'u no-op kalır). */
  if (ev.dataTransfer?.types.includes(FE_DND_MIME)) {
    ev.preventDefault();
    return;
  }
  /* /wiring:d1 */
  if (isExternalFileDrag(ev)) {
    ev.preventDefault();
  }
}
function onDropUpload(ev: DragEvent) {
  /* wiring:d1 — split panelinden ana panelin boşluğuna bırakma = geçerli
     klasöre aktar (aynı klasörden gelenler no-op, eski davranış korunur). */
  if (ev.dataTransfer?.types.includes(FE_DND_MIME)) {
    const d1Origin = ev.dataTransfer.getData(FE_DND_SRC_MIME) || '';
    const d1Here = qualify(currentPath.value);
    if (d1Origin && d1Here && d1Origin !== d1Here && !trashMode.value && canWriteHere.value) {
      ev.preventDefault();
      dragCounter.value = 0;
      dragOver.value = false;
      try {
        const d1Items = JSON.parse(ev.dataTransfer.getData(FE_DND_MIME)) as Array<{ path: string }>;
        void transferItems(d1Items.map((i) => i.path), d1Here, d1Origin);
      } catch {
        /* bozuk payload — yok say */
      }
      return;
    }
  }
  /* /wiring:d1 */
  // Internal row drag — nothing to do here, the row drop handler
  // in GridView/ListView already resolved the move.
  if (ev.dataTransfer?.types.includes(FE_DND_MIME)) {
    dragCounter.value = 0;
    dragOver.value = false;
    return;
  }
  // Browser-internal image drag without real files — bail before
  // we accidentally synthesise an upload from a 0-length file list
  // (some browsers populate `files` with zero-byte placeholders).
  if (!isExternalFileDrag(ev)) {
    dragCounter.value = 0;
    dragOver.value = false;
    return;
  }
  ev.preventDefault();
  dragCounter.value = 0;
  dragOver.value = false;
  // RBAC: block drag-drop upload where the user can't write.
  if (!canWriteHere.value) {
    flashToast(locale.value === 'en' ? 'Read-only here' : 'Burada yazma yetkiniz yok');
    return;
  }
  const list = ev.dataTransfer?.files ? Array.from(ev.dataTransfer.files) : [];
  if (list.length === 0) return;
  void uploadFiles(list);
}

function onWindowDragOver(ev: DragEvent) {
  if (ev.dataTransfer?.types.includes('Files')) ev.preventDefault();
}
function onWindowDrop(ev: DragEvent) {
  const root = rootEl.value;
  const target = ev.target as Node | null;
  if (root && target && !root.contains(target)) {
    ev.preventDefault();
  }
}
onMounted(() => {
  window.addEventListener('dragover', onWindowDragOver);
  window.addEventListener('drop', onWindowDrop);
});
onBeforeUnmount(() => {
  window.removeEventListener('dragover', onWindowDragOver);
  window.removeEventListener('drop', onWindowDrop);
  window.removeEventListener('hashchange', onHashChange);
});

const clippedPaths = computed<Set<string>>(() => {
  if (clipboard.value.mode !== 'cut') return new Set();
  return new Set(clipboard.value.items.map((n) => n.path));
});

// --------------------------------------------------------------------
// Item drag&drop move
// --------------------------------------------------------------------

const FE_DND_MIME = 'application/x-brf-files';

function onItemDragStart(node: FileNode, ev: DragEvent) {
  if (!ev.dataTransfer) return;
  if (node.basename === '.trash') {
    ev.preventDefault();
    return;
  }
  if (!selection.has(node.path)) {
    selection.click(node.path);
  }
  const items = selection.nodes.value
    .filter((n) => !clippedPaths.value.has(n.path))
    .filter((n) => n.basename !== '.trash')
    .map((n) => ({ path: n.path, basename: n.basename, type: n.type })); // qualified
  ev.dataTransfer.setData(FE_DND_MIME, JSON.stringify(items));
  ev.dataTransfer.setData(FE_DND_SRC_MIME, qualify(currentPath.value)); /* wiring:d1 — paneller arası origin damgası */
  ev.dataTransfer.setData('text/plain', items.map((i) => i.path).join('\n'));
  ev.dataTransfer.effectAllowed = 'move';
}

async function moveSourcesAsync(sources: string[], targetDir: string, opLabel: string, originOverride?: string): Promise<void> {
  try {
    const originWire = originOverride ?? qualify(currentPath.value); /* wiring:d1 — split panelinden gelen sürüklemede gerçek kaynak klasör */
    if (api.endpoints.moveAsync) {
      const { op } = await api.moveAsync(sources, targetDir, originWire);
      registerMoveUndo(op.id, sources, targetDir, originWire);
      pendingOps.register(op);
      flashToast('Taşıma kuyruğa alındı');
    } else {
      await api.move(originWire, sources, targetDir);
      await load();
      // Sync move (no async endpoint): offer the reverse move right away.
      const movedPaths = sources.map((p) => wireJoin(targetDir, wireBasename(p)));
      undoToast(t('toast.moved'), async () => {
        await api.move(targetDir, movedPaths, originWire);
      });
    }
    selection.clear();
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: opLabel, targetDir } });
  }
}

async function onItemDropInto(target: FileNode, ev: DragEvent) {
  if (target.type !== 'dir') return;
  const raw = ev.dataTransfer?.getData(FE_DND_MIME);
  if (!raw) return;
  let items: Array<{ path: string }> = [];
  try {
    items = JSON.parse(raw);
  } catch {
    return;
  }
  if (items.length === 0) return;

  const targetDir = target.path; // qualified
  const sources = items
    .map((i) => i.path)
    // ui-fix — öğe zaten targetDir'in içindeyse (parent===target) atla: yerinde
    // bırakma no-op, backend "kendine kopyala" 400'ü tetiklemez.
    .filter((p) => p && p !== targetDir && !targetDir.startsWith(p + '/') && !sameDir(wireParent(p), targetDir));
  if (sources.length === 0) return; // sessiz no-op (yerinde bırakma)
  await transferItems(sources, targetDir, dndOrigin(ev)); /* wiring:d1 — depo-farkında aktarım */
}

async function onCrumbDropInto(adapterPath: string, ev: DragEvent) {
  const raw = ev.dataTransfer?.getData(FE_DND_MIME);
  if (!raw) return;
  let items: Array<{ path: string }> = [];
  try {
    items = JSON.parse(raw);
  } catch {
    return;
  }
  if (items.length === 0) return;

  const targetDir = adapterPath; // already qualified by breadcrumb
  const sources = items
    .map((i) => i.path)
    // ui-fix — kırıntıya (aynı klasöre) yerinde bırakma no-op.
    .filter((p) => p && p !== targetDir && !targetDir.startsWith(p + '/') && !sameDir(wireParent(p), targetDir));
  if (sources.length === 0) return;
  await transferItems(sources, targetDir, dndOrigin(ev)); /* wiring:d1 — depo-farkında aktarım */
}

function onCancelUpload(job: UploadJob) {
  job.cancel();
}

function onDismissUpload(job: UploadJob) {
  uploadJobs.value = uploadJobs.value.filter((j) => j.id !== job.id);
}

// ------- Breadcrumb -------

function onNavigate(adapterPath: string) {
  // Multi-storage emits empty string for the global "/" crumb. The
  // load() function recognises that as the storage-list virtual root.
  if (multiStorageRoot.value && !adapterPath) {
    void load('');
    return;
  }
  if (multiStorageRoot.value) {
    void load(wireToVirtual(adapterPath));
    return;
  }
  void load(stripAdapter(adapterPath));
}

async function onCopyPath(adapterPath: string) {
  try {
    await navigator.clipboard.writeText(adapterPath);
    flashToast(t('breadcrumb.copy_path'));
  } catch {
    /* no-op */
  }
}

// Sync auth-headers builder for PreviewModal — fetches against the
// OnlyOffice config endpoint and the saveText endpoint need real
// headers, not promises. Function-token bearers will use the cached
// token; first-call resolution happens via the async path elsewhere.
function buildAuthHeaders(extra: Record<string, string> = {}) {
  return api.authHeadersSync({ ...extra });
}

/* === wiring:c1 — tema galerisi ===
 * Selected theme (shared module state, localStorage `filex.theme`) is applied
 * as inline `--fe-*` variables on the explorer root, resolved to the active
 * light/dark variant — plus a mirrored injected stylesheet for the surfaces
 * that re-declare tokens (teleported context menu, modal backdrops). Theme
 * selection is independent from the light/dark mode: the mode only picks
 * WHICH variant of the theme paints. */
const showThemeGallery = ref(false);
const { themeId: activeThemeId, setTheme: setActiveTheme } = useThemeState();
// Resolved mode: explicit config wins, otherwise the OS preference — the same
// logic variables.css encodes in CSS. The inline root variables beat every
// stylesheet rule, so they must track this resolution at runtime.
const themeMq =
  typeof window !== 'undefined' && window.matchMedia
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : undefined;
const themeOsDark = ref(!!themeMq?.matches);
function onThemeMqChange(e: MediaQueryListEvent) {
  themeOsDark.value = e.matches;
}
onMounted(() => themeMq?.addEventListener?.('change', onThemeMqChange));
onBeforeUnmount(() => themeMq?.removeEventListener?.('change', onThemeMqChange));
const themeResolvedDark = computed(
  () => props.config.theme === 'dark' || (props.config.theme !== 'light' && themeOsDark.value),
);
watch(
  [activeThemeId, themeResolvedDark, rootEl],
  () => {
    syncThemeStyle(activeThemeId.value);
    if (rootEl.value) applyThemeToEl(rootEl.value, activeThemeId.value, themeResolvedDark.value);
  },
  { immediate: true },
);
/* === /wiring:c1 === */
/* === wiring:c2 — shortcut settings modal + Space quick-look ===
 * quickLookTarget follows the selection while the peek is open: arrow
 * keys emitted by QuickLook move the selection here, and the watcher
 * below syncs the previewed file. Space toggles (registry action
 * `quicklook`); Enter promotes the peek into the normal open flow. */
const showShortcutSettings = ref(false);
const quickLookOpen = ref(false);
const quickLookTarget = ref<FileNode | null>(null);

function quickLookToggle() {
  if (quickLookOpen.value) {
    quickLookOpen.value = false;
    return;
  }
  const sel = selection.nodes.value;
  const n = sel.length === 1 ? sel[0] : null;
  if (!n || n.type !== 'file' || n.basename === '.trash') return;
  /* wiring:e2 — Space peek şifreli klasörde önce çözer, sonra açar */
  if (e2eActive.value) {
    if (!e2eUnlocked.value) return;
    void (async () => {
      try {
        await e2eFetchDecrypted(n);
      } catch {
        flashToast(t('e2e.decrypt_failed'));
        return;
      }
      quickLookTarget.value = n;
      quickLookOpen.value = true;
      void markRecent(n);
    })();
    return;
  }
  /* /wiring:e2 */
  quickLookTarget.value = n;
  quickLookOpen.value = true;
  void markRecent(n);
}

function quickLookNav(delta: number) {
  const onlyFiles = files.value.filter((f) => f.type === 'file');
  if (onlyFiles.length === 0) return;
  const cur = quickLookTarget.value;
  let idx = cur ? onlyFiles.findIndex((f) => f.path === cur.path) : -1;
  idx = idx === -1 ? (delta > 0 ? 0 : onlyFiles.length - 1) : (idx + delta + onlyFiles.length) % onlyFiles.length;
  const next = onlyFiles[idx];
  if (!next || next.path === cur?.path) return;
  selection.click(next.path);
}

function quickLookOpenFull() {
  const n = quickLookTarget.value;
  quickLookOpen.value = false;
  if (n) openNode(n);
}

watch(
  () => selection.nodes.value,
  (nodes) => {
    if (!quickLookOpen.value) return;
    const n = nodes.length === 1 && nodes[0].type === 'file' ? nodes[0] : null;
    /* wiring:e2 — ok tuşlarıyla gezerken de hedef atanmadan ÖNCE çöz,
       yoksa viewer bir anlığına ham ciphertext URL'i alır. */
    if (n && n.path !== quickLookTarget.value?.path && e2eUnlocked.value) {
      void (async () => {
        try {
          await e2eFetchDecrypted(n);
        } catch {
          /* çözülemedi — hedefi yine de değiştir, viewer hata gösterir */
        }
        quickLookTarget.value = n;
        void markRecent(n);
      })();
      return;
    }
    /* /wiring:e2 */
    if (n && n.path !== quickLookTarget.value?.path) {
      quickLookTarget.value = n;
      void markRecent(n);
    }
  },
);
/* === /wiring:c2 === */
/* wiring:c3 — operations center store + failed-upload retry */
const opsCenter = useOperations();

/**
 * Retry a failed upload from the operations center. The failed row is
 * already retired by the store; re-run the upload against the job's ORIGINAL
 * target folder (the user may have navigated away since) via the legacy
 * single-POST path — works for any storage / size, no chunked precondition.
 */
function retryUploadJob(job: UploadJob) {
  uploadJobs.value = uploadJobs.value.filter((j) => j.id !== job.id);
  const file = job.file;
  const target = job.path || qualify(currentPath.value);
  const id = crypto.randomUUID();
  uploadJobs.value = [
    ...uploadJobs.value,
    { id, file, path: target, totalBytes: file.size, uploadedBytes: 0, percent: 0, status: 'uploading', cancel() {} },
  ];
  const patchRetry = (p: Partial<UploadJob>) => {
    const idx = uploadJobs.value.findIndex((j) => j.id === id);
    if (idx === -1) return;
    const next = [...uploadJobs.value];
    next[idx] = { ...next[idx], ...p };
    uploadJobs.value = next;
  };
  api
    .uploadMultipart(target, [file], (percent) => {
      patchRetry({ percent, uploadedBytes: Math.round((percent / 100) * file.size) });
      emit('upload-progress', { uploadId: id, percent, done: percent >= 100 });
    })
    .then(() => {
      patchRetry({ percent: 100, uploadedBytes: file.size, status: 'done' });
      emit('upload-progress', { uploadId: id, percent: 100, done: true });
      void load();
    })
    .catch((err: Error) => {
      patchRetry({ status: 'error', error: err.message });
      emit('error', { message: err.message, context: { op: 'upload-retry', file: file.name } });
    });
}
/* /wiring:c3 */
/* === wiring:c4 — onboarding coach-mark tour ===
 * First mount with no `filex.tourDone` flag auto-starts the tour (short
 * delay so the listing/toolbar are laid out). Closing it — finished OR
 * skipped — stamps the flag; "Turu tekrar başlat" re-opens it any time.
 * Restart arrives as a bubbled `fe:tour-restart` CustomEvent from the
 * Toolbar overflow menu, so no extra prop/emit threading through the
 * shared component tags is needed. */
const TOUR_LS_KEY = 'filex.tourDone';
const showTour = ref(false);
let tourTimer: ReturnType<typeof setTimeout> | undefined;

function tourAlreadyDone(): boolean {
  try {
    return localStorage.getItem(TOUR_LS_KEY) === '1';
  } catch {
    return true; // no storage → never auto-nag
  }
}

function startTour() {
  showTour.value = true;
}

function onTourClose() {
  showTour.value = false;
  try {
    localStorage.setItem(TOUR_LS_KEY, '1');
  } catch {
    /* private mode / quota */
  }
  rootEl.value?.focus();
}

function onTourRestartEvent() {
  startTour();
}

onMounted(() => {
  rootEl.value?.addEventListener('fe:tour-restart', onTourRestartEvent);
  if (!tourAlreadyDone()) {
    tourTimer = setTimeout(() => {
      if (!showTour.value) startTour();
    }, 900);
  }
});
onBeforeUnmount(() => {
  if (tourTimer) clearTimeout(tourTimer);
  rootEl.value?.removeEventListener('fe:tour-restart', onTourRestartEvent);
});
/* === /wiring:c4 === */

/* === wiring:d1 — sekmeler (tab şeridi) + tab başına split ===
 *
 * useTabs, mevcut konum state'inin (currentPath/viewMode) ÜSTÜNDE bir
 * katmandır: aktif tab gezinmeleri watch ile dinleyip snapshot'ını
 * günceller; tab geçişi mevcut load(path) yolunu çağırır — yeni fetch
 * mantığı yok. Şerit tek sekmede hiç render edilmez (embed pixel-aynı).
 *
 * Persist: `filex.tabs` — pathPersist scope mantığı izlenir: mode 'none'
 * ise persist kapalı; rootPath confine'ı anahtara eklenir ki farklı
 * confine'lı embed'ler birbirinin sekmelerini ezmesin.
 */
const FE_DND_SRC_MIME = 'application/x-brf-files-src';

const TABS_LS_BASE = 'filex.tabs';
function tabsStorageKey(): string | null {
  if (persistMode() === 'none') return null;
  return rootPathProp ? `${TABS_LS_BASE}:${rootPathProp}` : TABS_LS_BASE;
}
const tabsApi = useTabs({ storageKey: tabsStorageKey() });
const tabsRestored = tabsApi.restore();
const tabsActiveId = tabsApi.activeId;

const tabsVisible = computed(() => tabsApi.hasMultiple.value);
const activeSplit = computed(() => tabsApi.activeTab.value?.split ?? null);

// Sekme adı OTOMATİK = güncel klasör adı (kök = depo adı / kök etiketi).
function tabLabel(path: string): string {
  const p = (path || '').replace(/^\/+|\/+$/g, '');
  if (p === '.trash') return t('node.trash');
  if (!p) return multiStorageRoot.value ? t('breadcrumb.root') : adapter.value || t('breadcrumb.root');
  return p.split('/').pop() || p;
}
const tabItems = computed(() =>
  tabsApi.tabs.value.map((tb) => ({ id: tb.id, label: tabLabel(tb.path), split: !!tb.split })),
);

// Aktif tab kullanıcıyı izler: gezinme + görünüm değişimi snapshot'a yazılır.
watch(currentPath, (p) => tabsApi.syncActive({ path: p }));
watch(viewMode, (v) => tabsApi.syncActive({ viewMode: v }));

// İlk sekme, ilk konum belli olur olmaz tohumlanır (restore varsa dokunma —
// aktif snapshot ilk load sonrası currentPath watcher'ıyla zaten senkronlanır).
onMounted(() => {
  if (!tabsRestored) tabsApi.seed(currentPath.value ?? '', viewMode.value);
});

function applyTabLocation(tb: TabState) {
  if (tb.viewMode && tb.viewMode !== viewMode.value) viewMode.value = tb.viewMode;
  if (tb.path === '.trash') {
    void loadTrash();
    return;
  }
  void load(tb.path);
}
function activateTab(id: string) {
  const tb = tabsApi.activate(id);
  if (tb) applyTabLocation(tb);
}
function newTabHere() {
  // Mevcut konumu klonlar; görünüm zaten oradadır, load gerekmez.
  tabsApi.openTab(currentPath.value ?? '', { viewMode: viewMode.value, background: false });
}
function closeTabById(id: string) {
  const next = tabsApi.closeTab(id);
  if (next) applyTabLocation(next);
}
function nextTab() {
  const tb = tabsApi.step(1);
  if (tb) applyTabLocation(tb);
}
function prevTab() {
  const tb = tabsApi.step(-1);
  if (tb) applyTabLocation(tb);
}

/** Bir klasörü ARKA PLANDA yeni sekmede aç (orta-tık / sağ-tık / palet). */
function openNodeInTab(n: FileNode) {
  if (n.type !== 'dir' || n.basename === '.trash') return;
  const target = multiStorageRoot.value ? wireToVirtual(n.path) : stripAdapter(n.path);
  tabsApi.openTab(target, { viewMode: viewMode.value, background: true });
}

// Orta-tık delegasyonu: ListView/GridView satırları data-fe-path taşır; kendi
// keydown/emit zinciri eklemek yerine kökte tek auxclick dinleyicisi yeter.
// (SecondaryPane kendi satırlarında stopPropagation ile kendisi halleder.)
function onListAuxClick(ev: MouseEvent) {
  if (ev.button !== 1) return;
  const host = ev.target as HTMLElement | null;
  const el = host && typeof host.closest === 'function' ? host.closest('[data-fe-path]') : null;
  const p = el?.getAttribute('data-fe-path');
  if (!p) return;
  const node = files.value.find((f) => f.path === p);
  if (!node || node.type !== 'dir' || node.basename === '.trash') return;
  ev.preventDefault();
  openNodeInTab(node);
}
// Orta-tuş mousedown'ı satırlar üzerinde iptal: scroll'lu gövdede Chromium'un
// autoscroll'u devreye girer ve auxclick HİÇ üretilmez (canlı teşhis) —
// preventDefault autoscroll'u bastırır, auxclick yeniden akar.
function onListMiddleDown(ev: MouseEvent) {
  if (ev.button !== 1) return;
  const host = ev.target as HTMLElement | null;
  if (host && typeof host.closest === 'function' && host.closest('[data-fe-path]')) {
    ev.preventDefault();
  }
}
onMounted(() => {
  rootEl.value?.addEventListener('auxclick', onListAuxClick);
  rootEl.value?.addEventListener('mousedown', onListMiddleDown);
});
onBeforeUnmount(() => {
  rootEl.value?.removeEventListener('auxclick', onListAuxClick);
  rootEl.value?.removeEventListener('mousedown', onListMiddleDown);
});

// ---- split (tab başına ikincil panel) ------------------------------

const splitPaneRef = ref<InstanceType<typeof SecondaryPane> | null>(null);
// Dar modda split devre dışı (state korunur, genişleyince geri gelir).
const splitVisible = computed(() => !!activeSplit.value && !isNarrow.value);

function toggleSplit() {
  if (activeSplit.value) {
    tabsApi.setSplit(null);
    activePane.value = 'main';
    return;
  }
  if (isNarrow.value) return;
  tabsApi.setSplit({ path: currentPath.value ?? '', viewMode: viewMode.value });
}
function closeSplit() {
  tabsApi.setSplit(null);
  activePane.value = 'main';
}
function onPaneNavigate(p: string) {
  tabsApi.setSplit({ ...(activeSplit.value ?? {}), path: p });
}
/* ui-fix — pane'in KENDİ görünüm modu: split açılırken ana panelinkini
 * devralır, sonrasında bağımsız. Toolbar'ın görünüm değiştiricisi ve palet
 * toggle'ı AKTİF panele yazar (Burak: "B tıklıyken ikon değiştir dersem
 * B'nin değişmesi lazım"). */
const paneViewMode = computed<ViewMode>(() => activeSplit.value?.viewMode ?? viewMode.value);
function setPaneViewMode(v: ViewMode) {
  if (!activeSplit.value) return;
  tabsApi.setSplit({ ...activeSplit.value, viewMode: v });
}
const displayedViewMode = computed<ViewMode>(() =>
  paneIsActive.value ? paneViewMode.value : viewMode.value,
);
function setDisplayedViewMode(v: ViewMode) {
  if (paneIsActive.value) setPaneViewMode(v);
  else viewMode.value = v;
}

// Aktif panel: kısayollar aktif panele gider; panel tıklamayla aktifleşir.
const activePane = ref<'main' | 'split'>('main');
function setPaneMain() {
  activePane.value = 'main';
}
watch(splitVisible, (v) => {
  if (!v) activePane.value = 'main';
});
const paneIsActive = computed(() => activePane.value === 'split' && splitVisible.value);
const mainPaneFocus = computed(() => splitVisible.value && activePane.value === 'main');

// Pane yardımcıları — hep ana panelin mevcut dönüştürücülerini sarar.
function paneToUser(wire: string): string {
  return multiStorageRoot.value ? wireToVirtual(wire) : stripAdapter(wire);
}
function paneClamp(p: string): string {
  const clean = String(p ?? '').replace(/^\/+|\/+$/g, '');
  if (!rootFloor) return clean;
  if (!clean || !(clean === rootFloor || clean.startsWith(rootFloor + '/'))) return rootFloor;
  return clean;
}

// Pano (clipboard) aktif panele göre: kes/kopyala pane seçiminden beslenir,
// yapıştır pane klasörüne iner. State ana panelinkiyle ORTAK — panolar arası
// kes-yapıştır bedavaya çalışır.
function paneCut() {
  const nodes = splitPaneRef.value?.selectedNodes() ?? [];
  if (nodes.length === 0) return;
  clipboard.value = { mode: 'cut', items: nodes, sourcePath: splitPaneRef.value?.getPath() ?? '' };
  flashToast('Kesildi');
}
function paneCopy() {
  const nodes = splitPaneRef.value?.selectedNodes() ?? [];
  if (nodes.length === 0) return;
  clipboard.value = { mode: 'copy', items: nodes, sourcePath: splitPaneRef.value?.getPath() ?? '' };
  flashToast('Kopyalandı');
}
async function panePaste() {
  const cb = clipboard.value;
  const pane = splitPaneRef.value;
  if (!cb.mode || cb.items.length === 0 || !pane) return;
  const targetWire = qualify(pane.getPath() ?? '');
  const originWire = qualify(cb.sourcePath || '') || undefined;
  if (cb.mode === 'cut' && originWire === targetWire) {
    flashToast('Aynı klasöre kesilemez');
    return;
  }
  await transferItems(cb.items.map((n) => n.path), targetWire, originWire, cb.mode === 'copy');
  clipboard.value = { mode: null, items: [], sourcePath: null };
}

// ---- paneller arası aktarım ----------------------------------------

function wireAdapterOf(p: string): string {
  const i = p.indexOf('://');
  return i === -1 ? '' : p.slice(0, i);
}
function dndOrigin(ev: DragEvent): string | undefined {
  const v = ev.dataTransfer?.getData(FE_DND_SRC_MIME);
  return v || undefined;
}

/**
 * transferItems — panel-arası / pano aktarımının tek kapısı.
 * Aynı depo → TAŞI (mevcut moveSourcesAsync yolu: kuyruk + geri al).
 * Farklı depo → KOPYALA dene; backend cross-storage desteklemiyorsa
 * i18n'li hata toast'ı. Bitince ikincil panel de tazelenir (ana panel
 * moveSourcesAsync / pendingOps onSettled üzerinden zaten tazelenir).
 */
async function transferItems(
  sources: string[],
  targetWire: string,
  originWire?: string,
  forceCopy = false,
): Promise<void> {
  // ui-fix — yerinde bırakma (source parent === target) no-op: backend
  // "kendine kopyala" 400'ü engellenir (paneller arası + pano yolu).
  const list = sources.filter(
    (p) => p && p !== targetWire && !targetWire.startsWith(p + '/') && !sameDir(wireParent(p), targetWire),
  );
  if (list.length === 0 || !targetWire) return;
  const targetAdapter = wireAdapterOf(targetWire);
  const cross = list.some((p) => wireAdapterOf(p) !== targetAdapter);
  if (cross || forceCopy) {
    try {
      const { op } = await api.copy(list, targetWire);
      pendingOps.register(op);
      flashToast(cross ? t('split.cross_copy') : t('split.copy_queued'));
    } catch (err) {
      emit('error', { message: (err as Error).message, context: { op: 'transfer', targetWire } });
      flashToast(cross ? t('split.cross_failed') : (err as Error).message);
      return;
    }
  } else {
    await moveSourcesAsync(list, targetWire, 'move-transfer', originWire);
  }
  void splitPaneRef.value?.reload();
}

function onPaneTransfer(p: { sources: string[]; targetWire: string; originWire?: string }) {
  void transferItems(p.sources, p.targetWire, p.originWire);
}
/* === /wiring:d1 === */

/* === wiring:e2 — uçtan uca şifreli klasörler ===
 *
 * Kripto şeması + tehdit modeli: docs/E2E-ENCRYPTION.md ve lib/e2ecrypto.ts.
 * Burada yalnız orkestrasyon var: backend listing yanıtındaki `e2e_root`
 * kilit ekranını sürer; parola marker'a karşı TARAYICIDA doğrulanır (sunucuya
 * hiçbir şey gitmez); türetilen klasör anahtarı (KEK) YALNIZ bellekte yaşar
 * (`e2eRing`) — localStorage/sessionStorage'a asla yazılmaz. Upload şeffaf
 * şifrelenir, önizleme/indirme şeffaf çözülür (blob URL mevcut viewer'lara).
 */
const e2eRing = createKeyRing();
// Map'ler reaktif değil — sürüm sayacı computed'ları tetikler.
const e2eRingVer = ref(0);
// İçinde bulunulan şifreli kökün wire yolu ('' = şifreli bağlam yok).
const e2eRoot = ref('');
// path → çözülmüş blob objectURL (önizleme). Kilitleme/unmount'ta revoke.
const e2eUrls = new Map<string, string>();

const e2eActive = computed(() => !!e2eRoot.value && !trashMode.value);
const e2eUnlocked = computed(() => {
  void e2eRingVer.value;
  return e2eActive.value && e2eRing.has(e2eRoot.value);
});
const e2eLocked = computed(() => {
  void e2eRingVer.value;
  return e2eActive.value && !e2eRing.has(e2eRoot.value);
});

// Kilit ekranı formu.
const e2ePw = ref('');
const e2eUnlockBusy = ref(false);
const e2eUnlockErr = ref('');
// Şifreli klasör oluşturma modalı.
const showEncFolder = ref(false);
const e2eCreateBusy = ref(false);

function e2eKek(): CryptoKey | null {
  return e2eRing.get(e2eRoot.value) ?? null;
}

function e2eRevokeAll() {
  for (const url of e2eUrls.values()) URL.revokeObjectURL(url);
  e2eUrls.clear();
}
onBeforeUnmount(e2eRevokeAll);

/** Kilidi aç: marker'ı kökten çek, parolayı YERELDE doğrula, KEK'i belleğe koy. */
async function e2eUnlock() {
  if (!e2ePw.value || e2eUnlockBusy.value || !e2eRoot.value) return;
  e2eUnlockBusy.value = true;
  e2eUnlockErr.value = '';
  try {
    let markerText = '';
    try {
      const { blob, url } = await api.fetchBlob(wireJoin(e2eRoot.value, E2E_MARKER_NAME));
      URL.revokeObjectURL(url);
      markerText = await blob.text();
    } catch {
      e2eUnlockErr.value = t('e2e.unlock.marker_missing');
      return;
    }
    const marker = parseMarker(markerText);
    if (!marker) {
      e2eUnlockErr.value = t('e2e.unlock.marker_missing');
      return;
    }
    const kek = await verifyPassword(marker, e2ePw.value);
    if (!kek) {
      e2eUnlockErr.value = t('e2e.unlock.wrong');
      return;
    }
    e2eRing.set(e2eRoot.value, kek);
    e2eRingVer.value++;
    e2ePw.value = '';
  } finally {
    e2eUnlockBusy.value = false;
  }
}

/** "Kilitle": bellekteki anahtarı ve çözülmüş blob'ları at. */
function e2eLock() {
  if (!e2eRoot.value) return;
  e2eRing.lock(e2eRoot.value);
  e2eRingVer.value++;
  e2eRevokeAll();
  flashToast(t('e2e.locked_toast'));
}

// Uzantı → önizleme MIME'ı: çözülmüş blob'un <img>/<video>/<object>
// etiketlerinde doğru render'ı için (sunucu şifreli dosyayı octet-stream
// bilir, oradan gelen tip işe yaramaz).
const E2E_MIME: Record<string, string> = {
  txt: 'text/plain', md: 'text/markdown', log: 'text/plain', csv: 'text/csv',
  json: 'application/json', xml: 'application/xml', html: 'text/html',
  jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', gif: 'image/gif',
  webp: 'image/webp', bmp: 'image/bmp', avif: 'image/avif', svg: 'image/svg+xml',
  pdf: 'application/pdf',
  mp4: 'video/mp4', webm: 'video/webm', mov: 'video/quicktime', m4v: 'video/mp4',
  mp3: 'audio/mpeg', wav: 'audio/wav', ogg: 'audio/ogg', flac: 'audio/flac',
  m4a: 'audio/mp4', aac: 'audio/aac', opus: 'audio/opus',
};
function e2eMimeFor(n: FileNode): string {
  const ext = (n.extension || '').toLowerCase();
  return E2E_MIME[ext] || 'application/octet-stream';
}

/**
 * Dosyayı çek + çöz + objectURL'ini cache'le. Magic'siz (ör. DAV'la düz
 * yazılmış) dosyada null döner — çağıran normal ham akışa düşer. Yanlış
 * anahtar/bozuk veri E2eDecryptError fırlatır (çağıran toast'lar).
 */
async function e2eFetchDecrypted(n: FileNode): Promise<string | null> {
  const cached = e2eUrls.get(n.path);
  if (cached) return cached;
  const kek = e2eKek();
  if (!kek) return null;
  const buf = await api.fetchArrayBuffer(n.path);
  if (!hasMagic(buf)) return null;
  const plain = await decryptFile(kek, buf);
  const url = URL.createObjectURL(new Blob([plain], { type: e2eMimeFor(n) }));
  e2eUrls.set(n.path, url);
  return url;
}

/** PreviewModal/QuickLook'a giden URL sağlayıcı: çözülmüş blob > ham URL. */
function e2ePreviewSrc(p: string): string {
  return e2eUrls.get(p) ?? api.previewUrl(p);
}

/** Çöz + salt-okunur in-page önizleme (openNode/previewNode buraya iner). */
async function e2eOpenPreview(n: FileNode) {
  try {
    await e2eFetchDecrypted(n);
  } catch {
    flashToast(t('e2e.decrypt_failed'));
    return;
  }
  previewMode.value = 'view';
  previewTarget.value = n;
  showPreview.value = true;
  emit('file-opened', { path: n.path, basename: n.basename });
  void markRecent(n);
}

/** Çöz + orijinal adla indir. Magic'siz dosya olduğu gibi iner. */
async function e2eDownload(n: FileNode) {
  try {
    const buf = await api.fetchArrayBuffer(n.path);
    const kek = e2eKek();
    let out = buf;
    if (hasMagic(buf)) {
      if (!kek) throw new Error('locked');
      out = await decryptFile(kek, buf);
    }
    const url = URL.createObjectURL(new Blob([out], { type: e2eMimeFor(n) }));
    const a = document.createElement('a');
    a.href = url;
    a.download = n.basename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 30_000);
  } catch {
    flashToast(t('e2e.download.failed'));
  }
}

/** Upload listesi → şifreli File listesi (200MB üstü + marker adı atlanır). */
async function e2eEncryptUploads(list: File[]): Promise<File[]> {
  const kek = e2eKek();
  if (!kek) return [];
  const out: File[] = [];
  for (const f of list) {
    if (f.name === E2E_MARKER_NAME) continue;
    if (f.size > E2E_MAX_FILE_BYTES) {
      flashToast(t('e2e.upload.too_big'));
      continue;
    }
    try {
      const ct = await encryptFile(kek, await f.arrayBuffer());
      out.push(new File([ct], f.name, { type: 'application/octet-stream' }));
    } catch (err) {
      emit('error', { message: (err as Error).message, context: { op: 'e2e-encrypt', file: f.name } });
    }
  }
  return out;
}

/** EncryptedFolderModal submit'i: klasörü aç + marker'ı yükle + kilidi açık bırak. */
async function submitEncryptedFolder(payload: { name: string; password: string }) {
  if (e2eActive.value) {
    // İç içe şifreli klasör kök tespitini bulanıklaştırır — MVP'de yok.
    flashToast(t('e2e.create.nested'));
    return;
  }
  e2eCreateBusy.value = true;
  try {
    const dirWire = qualify(currentPath.value);
    await api.newFolder(dirWire, payload.name);
    const { marker, kek } = await createMarker(payload.password);
    const markerFile = new File([JSON.stringify(marker)], E2E_MARKER_NAME, {
      type: 'application/json',
    });
    const newDirWire = wireJoin(dirWire, payload.name);
    await api.uploadMultipart(newDirWire, [markerFile]);
    // Oluşturan oturumda kilit açık başlar (parolayı az önce kendisi girdi).
    e2eRing.set(newDirWire, kek);
    e2eRingVer.value++;
    showEncFolder.value = false;
    flashToast(t('e2e.create.done'));
    await load();
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'e2e-create' } });
    flashToast(t('e2e.create.failed'));
  } finally {
    e2eCreateBusy.value = false;
  }
}
/* === /wiring:e2 === */
</script>

<template>
  <div
    ref="rootEl"
    class="fe"
    :class="{
      'fe--theme-light': config.theme === 'light',
      'fe--theme-dark': config.theme === 'dark',
      'fe--is-dragover': dragOver,
      'fe--density-compact': density === 'compact' /* cila:a density */,
      'fe--narrow': isNarrow /* bag:b4 */,
    }"
    tabindex="-1"
    @dragenter="onDragEnter"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDropUpload"
    @contextmenu="onContextCanvas"
  >
    <!-- wiring:d1 — sekme şeridi: TEK sekmede hiç render edilmez (embed pixel-aynı) -->
    <TabBar
      v-if="tabsVisible"
      :tabs="tabItems"
      :active-id="tabsActiveId"
      :locale="locale"
      :split-enabled="!isNarrow"
      :split-active="!!activeSplit"
      @select="activateTab"
      @close="closeTabById"
      @new="newTabHere"
      @reorder="(from: number, to: number) => tabsApi.move(from, to)"
      @toggle-split="toggleSplit"
    />
    <!-- /wiring:d1 -->
    <Toolbar
      ref="toolbarRef"
      :view-mode="displayedViewMode /* ui-fix — aktif panelin modu */"
      :search-query="searchQuery"
      :trash-active="trashActive"
      :actions="toolbarActions"
      :selection-mode="selectionMode"
      :paste-enabled="!!clipboard.mode"
      :convert-enabled="!!effectiveConvertUrl"
      :can-go-up="canGoUp"
      :at-virtual-root="atVirtualRoot"
      :can-write="canWriteHere"
      :locale="locale"
      :narrow="isNarrow /* bag:b4 */"
      :theme="config.theme || 'auto' /* bag:b4 */"
      :inspector-open="showInspector /* koru:k1 */"
      @toggle-inspector="toggleInspector /* koru:k1 */"
      @open-theme="showThemeGallery = true /* wiring:c1 */"
      @update:view-mode="setDisplayedViewMode($event) /* ui-fix — aktif panele */"
      @update:search-query="searchQuery = $event"
      @update:density="density = $event"
      @open-shortcut-settings="showShortcutSettings = true /* wiring:c2 */"
      @new-folder="showNewFolder = true"
      @upload="triggerUpload"
      @refresh="() => load()"
      @go-up="goUp"
      @action="onToolbarAction"
      @open-recents="showRecents = true"
    />

    <!-- koru:k1 — fe__main lays the listing body and the inspector panel out
         as flex siblings (row). Without the inspector open it is visually
         identical to the previous direct-child fe__body. -->
    <div class="fe__main" :class="{ 'fe__main--split': splitVisible } /* wiring:d1 */">
    <!-- ui-fix — sol panelin başlığı (breadcrumb + durum şeritleri + body)
         tek bir sarmalda: split modunda bu sarmal sol yarıya sığar, böylece
         breadcrumb tüm sayfayı değil kendi panelini kaplar (SecondaryPane'in
         kendi kırıntısıyla simetrik). Aktif-panel vurgusu da bu sarmalda. -->
    <div
      class="fe__primary"
      :class="{ 'fe-pane--focus': mainPaneFocus } /* wiring:d1 — aktif panel vurgusu */"
    >
    <Breadcrumb
      :dirname="dirname"
      :adapter="adapter"
      :root-label="adapter"
      :locale="locale"
      :multi-storage-root="multiStorageRoot"
      :root-path="rootPathProp"
      @navigate="onNavigate"
      @copy-path="onCopyPath"
      @crumb-context="onCrumbContext"
      @crumb-drop="onCrumbDropInto"
    />

    <!-- Live presence: who else is viewing this folder (empty → nothing shown).
         When the live socket is unavailable the same strip carries a small
         degraded-connection badge instead (presence is empty in fallback);
         a healthy connection shows nothing extra. -->
    <div v-if="presenceUsers.length || realtimeDegraded" class="fe__presence">
      <PresenceBar v-if="presenceUsers.length" :users="presenceUsers" :locale="locale" />
      <span
        v-if="realtimeDegraded"
        class="fe-connbadge"
        role="status"
        :title="t('conn.tooltip')"
      >
        <span class="fe-connbadge__dot" aria-hidden="true"></span>
        {{ t('conn.offline') }}
      </span>
    </div>

    <!-- wiring:e2 — kilit açık şeridi: şifreli klasörde anahtar bellekteyken
         görünür; "Kilitle" anahtarı ve çözülmüş blob'ları atar. -->
    <div v-if="e2eUnlocked" class="fe-e2e-strip" role="status">
      <span class="fe-e2e-strip__icon" aria-hidden="true">🔒</span>
      <span class="fe-e2e-strip__label">{{ t('e2e.strip.label') }}</span>
      <button type="button" class="fe-btn fe-e2e-strip__btn" @click="e2eLock">
        {{ t('e2e.strip.lock') }}
      </button>
    </div>
    <!-- /wiring:e2 -->

    <div
      class="fe__body"
      @pointerdown.capture="setPaneMain() /* wiring:d1 */"
      @click.self="selection.clear()"
    >
      <!-- Initial load: skeleton ghosts (view-mode aware) instead of an
           empty/"no files" flash. Only when there's nothing yet — navigation
           keeps the current list, exactly as before. -->
      <div v-if="loading && files.length === 0" class="fe__skeleton" role="status">
        <span class="fe-sr-only">{{ t('loading') }}</span>
        <div v-if="viewMode !== 'list' /* wiring:d2 — galeri de grid iskeletini kullanır */" class="fe-skel-grid" aria-hidden="true">
          <div v-for="i in 8" :key="i" class="fe-skel-card">
            <div class="fe-skel fe-skel--thumb"></div>
            <div class="fe-skel fe-skel--label"></div>
          </div>
        </div>
        <div v-else class="fe-skel-list" aria-hidden="true">
          <div v-for="i in 8" :key="i" class="fe-skel-row">
            <div class="fe-skel fe-skel--icon"></div>
            <div class="fe-skel fe-skel--name"></div>
            <div class="fe-skel fe-skel--size"></div>
            <div class="fe-skel fe-skel--date"></div>
          </div>
        </div>
      </div>
      <!-- Dead deep link (404) or RBAC-hidden dir (403, shown identically):
           a dedicated state instead of a misleading "this folder is empty". -->
      <div v-else-if="notFoundPath" class="fe-state">
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="110"
          height="92"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M18 36v42a6 6 0 0 0 6 6h72a6 6 0 0 0 6-6V44a6 6 0 0 0-6-6H62l-9-10H24a6 6 0 0 0-6 6z" />
          <path d="M52 55c0-4.6 3.6-8 8-8s8 3.4 8 8c0 5.5-8 4.8-8 11" />
          <circle cx="60" cy="73" r="1.6" fill="currentColor" stroke="none" />
        </svg>
        <p class="fe-state__title">{{ t('notFound.title') }}</p>
        <p class="fe-state__path">{{ notFoundPath }}</p>
        <p class="fe-state__hint">{{ t('notFound.desc') }}</p>
        <div class="fe-state__actions">
          <button type="button" class="fe-btn" @click="leaveNotFound">
            {{ t('notFound.goRoot') }}
          </button>
        </div>
      </div>
      <!-- Listing failed (network / 5xx) with nothing else to show: retryable
           error state in the same visual language. -->
      <div v-else-if="loadError && files.length === 0" class="fe-state">
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="110"
          height="92"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="60" cy="50" r="28" />
          <path d="M60 36v18" />
          <circle cx="60" cy="63" r="1.8" fill="currentColor" stroke="none" />
          <path d="M24 88h72" stroke-dasharray="3 5" />
        </svg>
        <p class="fe-state__title">{{ t('error.title') }}</p>
        <!-- wiring:c4 — friendly hint + collapsible technical detail; the raw
             error message used to sit in the hint slot and read like UI copy. -->
        <p class="fe-state__hint">{{ t('error.hint') }}</p>
        <div class="fe-state__actions">
          <button type="button" class="fe-btn fe-btn--primary" @click="retryLoad">
            {{ t('error.retry') }}
          </button>
        </div>
        <details class="fe-state__details">
          <summary class="fe-state__details-summary">{{ t('error.details') }}</summary>
          <pre class="fe-state__details-pre">{{ loadError }}</pre>
        </details>
        <!-- /wiring:c4 -->
      </div>
      <!-- wiring:e2 — şifreli klasör kilit ekranı: parola doğru girilene dek
           listeleme render edilmez. Parola tarayıcıda marker'a karşı
           doğrulanır; sunucuya gitmez. -->
      <div v-else-if="e2eLocked" class="fe-state fe-e2e-lock">
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="110"
          height="92"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <rect x="38" y="44" width="44" height="34" rx="6" />
          <path d="M46 44v-8a14 14 0 0 1 28 0v8" />
          <circle cx="60" cy="59" r="3" fill="currentColor" stroke="none" />
          <path d="M60 62v7" />
        </svg>
        <p class="fe-state__title">{{ t('e2e.locked.title') }}</p>
        <p class="fe-state__hint">{{ t('e2e.locked.hint') }}</p>
        <form class="fe-e2e-lock__form" @submit.prevent="e2eUnlock">
          <input
            v-model="e2ePw"
            type="password"
            class="fe-input fe-e2e-lock__input"
            :placeholder="t('e2e.locked.pw_placeholder')"
            autocomplete="current-password"
            :disabled="e2eUnlockBusy"
          />
          <button
            type="submit"
            class="fe-btn fe-btn--primary"
            :disabled="e2eUnlockBusy || !e2ePw"
          >
            {{ e2eUnlockBusy ? t('e2e.locked.busy') : t('e2e.locked.unlock') }}
          </button>
        </form>
        <p v-if="e2eUnlockErr" class="fe-form__error" role="alert">{{ e2eUnlockErr }}</p>
      </div>
      <!-- /wiring:e2 -->
      <!-- Search with zero hits — its own message, not "folder is empty". -->
      <div v-else-if="!loading && files.length === 0 && searchQuery" class="fe-state">
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="110"
          height="92"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="52" cy="44" r="22" />
          <path d="M68 61l20 20" />
          <path d="M46 38l12 12M58 38l-12 12" />
        </svg>
        <p class="fe-state__title">{{ t('empty.search.title') }}</p>
        <p class="fe-state__hint">{{ t('empty.search.hint') }}</p>
      </div>
      <!-- Empty trash view. -->
      <div v-else-if="!loading && files.length === 0 && trashMode" class="fe-state">
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="110"
          height="92"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M38 34l4 48a6 6 0 0 0 6 5.6h24a6 6 0 0 0 6-5.6l4-48" />
          <path d="M32 34h56" />
          <path d="M50 34v-6a6 6 0 0 1 6-6h8a6 6 0 0 1 6 6v6" />
          <path d="M52 44v32M60 44v32M68 44v32" opacity="0.5" />
        </svg>
        <p class="fe-state__title">{{ t('empty.trash.title') }}</p>
      </div>
      <!-- Loaded, zero files, no search: the real empty-folder state. The
           upload affordances follow write permission (RBAC viewers only get
           the title). -->
      <div v-else-if="!loading && files.length === 0" class="fe-state">
        <svg
          class="fe-state__art"
          viewBox="0 0 120 100"
          width="110"
          height="92"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M18 36v42a6 6 0 0 0 6 6h72a6 6 0 0 0 6-6V44a6 6 0 0 0-6-6H62l-9-10H24a6 6 0 0 0-6 6z" />
          <g v-if="emptyCanUpload">
            <path d="M60 50v14" stroke-dasharray="3 4" />
            <path d="M53 59l7 8 7-8" />
          </g>
        </svg>
        <p class="fe-state__title">{{ t('empty.folder') }}</p>
        <p v-if="emptyCanUpload" class="fe-state__hint">{{ t('empty.hint') }}</p>
        <div v-if="emptyCanUpload" class="fe-state__actions">
          <button type="button" class="fe-btn fe-btn--primary" @click="triggerUpload">
            {{ t('empty.upload') }}
          </button>
        </div>
      </div>
      <ListView
        v-else-if="viewMode === 'list'"
        :files="files"
        :selected="selection.selected.value"
        :clipped="clippedPaths"
        :show-parent-path="!!searchQuery"
        :locale="locale"
        :loading="loading"
        :starred-ids="starredIds"
        :api-base="props.config.apiBase ?? ''"
        :auth-headers="() => buildAuthHeaders()"
        @click-row="(n, m) => selection.click(n.path, m)"
        @dbl-row="openNode"
        @context-row="onContextTarget"
        @item-drag-start="onItemDragStart"
        @item-drop-into="onItemDropInto"
        @star-change="onStarChange"
      />
      <GridView
        v-else-if="viewMode === 'grid' /* wiring:d2 — v-else → v-else-if (3. mod eklendi) */"
        :files="files"
        :selected="selection.selected.value"
        :clipped="clippedPaths"
        :show-parent-path="!!searchQuery"
        :locale="locale"
        :loading="loading"
        :thumb-src="thumbs.src"
        @click-card="(n, m) => selection.click(n.path, m)"
        @dbl-card="openNode"
        @context-card="onContextTarget"
        @item-drag-start="onItemDragStart"
        @item-drop-into="onItemDropInto"
      />
      <!-- wiring:d2 — galeri görünümü (GridView ile aynı event sözleşmesi) -->
      <GalleryView
        v-else
        :files="files"
        :selected="selection.selected.value"
        :clipped="clippedPaths"
        :show-parent-path="!!searchQuery"
        :locale="locale"
        :loading="loading"
        :thumb-src="thumbs.src"
        @click-card="(n, m) => selection.click(n.path, m)"
        @dbl-card="openNode"
        @context-card="onContextTarget"
        @item-drag-start="onItemDragStart"
        @item-drop-into="onItemDropInto"
      />
      <!-- /wiring:d2 -->
    </div>
    </div><!-- /fe__primary ui-fix -->

    <!-- wiring:d1 — tab başına split: sağ ikincil panel (dar modda kapalı).
         :key tab kimliğine bağlı — tab geçişinde pane kendi konumuyla temiz
         remount olur. -->
    <SecondaryPane
      v-if="splitVisible && activeSplit"
      ref="splitPaneRef"
      :key="'split-' + tabsActiveId"
      :api="api"
      :initial-path="activeSplit.path"
      :locale="locale"
      :qualify="qualify"
      :to-user="paneToUser"
      :clamp="paneClamp"
      :root-label="multiStorageRoot ? '/' : adapter || t('breadcrumb.root')"
      :floor="rootFloor"
      :multi-root="multiStorageRoot"
      :virtual-rows="virtualStorageRows"
      :active="paneIsActive"
      :view-mode="paneViewMode /* ui-fix */"
      :thumb-src="thumbs.src /* ui-fix */"
      @navigate="onPaneNavigate"
      @activate="activePane = 'split'"
      @close="closeSplit"
      @open-tab="(p: string) => tabsApi.openTab(p, { viewMode: viewMode, background: true })"
      @transfer="onPaneTransfer"
      @context="onPaneContext /* ui-fix — yan panel sağ-tık menüsü */"
    />
    <!-- /wiring:d1 -->

    <!-- koru:k1 — inspector (details) panel; v-if keeps the closed state
         free of any DOM. Narrow mode renders it as a full-size overlay. -->
    <InspectorPanel
      v-if="showInspector"
      :api="api"
      :nodes="selection.nodes.value"
      :dir-label="inspectorDirLabel"
      :dir-count="files.length"
      :dir-perm="dirPerm"
      :locale="locale"
      :narrow="isNarrow"
      :thumb-src="thumbs.src"
      @close="closeInspector"
      @manage-permissions="onInspectorManage"
      @toast="flashToast"
      @changed="() => load()"
    />
    </div>
    <!-- /koru:k1 fe__main -->

    <div v-if="dragOver" class="fe__dragover">
      <div class="fe__dragover-card">
        <span class="fe-icon">⬆</span>
        <p>Dosyaları buraya bırak</p>
      </div>
    </div>

    <!-- wiring:c3 — unified operations center. UploadProgress + PendingOpsTray
         no longer draw their own corner UIs: they are renderless publishers
         feeding the opsCenter store; the single visible surface is the
         OperationsCenter badge + panel below. -->
    <UploadProgress
      :jobs="uploadJobs"
      :locale="locale"
      :center="opsCenter"
      @cancel="onCancelUpload"
      @dismiss="onDismissUpload"
      @retry="retryUploadJob"
    />

    <PendingOpsTray
      :ops="pendingOps.ops.value"
      :locale="locale"
      :center="opsCenter"
      @dismiss="(id) => pendingOps.dismiss(id)"
    />

    <OperationsCenter
      :center="opsCenter"
      :locale="locale"
      :narrow="isNarrow"
    />
    <!-- /wiring:c3 -->

    <!-- bag:b4 — narrow-mode upload FAB (hidden in trash / read-only /
         virtual root; PendingOpsTray+UploadProgress shift up via CSS). -->
    <button
      v-if="isNarrow && emptyCanUpload"
      type="button"
      class="fe-fab"
      :title="t('toolbar.upload')"
      :aria-label="t('toolbar.upload')"
      @click="triggerUpload"
    >
      <svg
        class="fe-ficon"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2.2"
        stroke-linecap="round"
        aria-hidden="true"
        focusable="false"
      >
        <path d="M12 5v14M5 12h14" />
      </svg>
    </button>

    <ContextMenu
      ref="ctxRef"
      :locale="locale"
      :theme="config.theme || 'auto'"
      :sheet="isCoarse /* bag:b4 */"
      :actions="contextActions"
      @select="onContextAction"
    />

    <NewFolderModal
      :open="showNewFolder"
      :locale="locale"
      :encrypted-option="!e2eActive /* wiring:e2 — iç içe şifreli klasör yok */"
      @close="showNewFolder = false"
      @submit="submitNewFolder"
      @encrypted="showNewFolder = false; showEncFolder = true /* wiring:e2 */"
    />
    <!-- wiring:e2 — şifreli klasör oluşturma modalı -->
    <EncryptedFolderModal
      :open="showEncFolder"
      :locale="locale"
      :busy="e2eCreateBusy"
      @close="showEncFolder = false"
      @submit="submitEncryptedFolder"
    />
    <!-- /wiring:e2 -->
    <RenameModal
      :open="showRename"
      :locale="locale"
      :current-name="renameTarget?.basename || ''"
      @close="showRename = false"
      @submit="submitRename"
    />
    <DeleteConfirmModal
      :open="showDelete"
      :locale="locale"
      :count="selection.size.value"
      @close="showDelete = false"
      @confirm="confirmDelete"
    />
    <ShareModal
      :open="showShare"
      :locale="locale"
      :share="activeShare"
      @close="closeShare"
      @submit="submitShare"
      @toast="flashToast"
    />
    <PreviewModal
      :open="showPreview"
      :locale="locale"
      :file="previewTarget"
      :theme="config.theme || 'auto'"
      :preview-url="(p) => e2ePreviewSrc(p) /* wiring:e2 — çözülmüş blob > ham URL */"
      :download-url="(p) => (e2eUnlocked ? e2ePreviewSrc(p) : api.downloadUrl(p)) /* wiring:e2 */"
      :only-office-base="e2eActive ? null : effectiveOnlyOfficeBase /* wiring:e2 — OO ciphertext açamaz */"
      :only-office-config-endpoint="effectiveOnlyOfficeConfigEndpoint"
      :new-tab-enabled="!e2eActive /* wiring:e2 — standalone rota ham baytı çeker */"
      :save-text-endpoint="e2eActive ? null : api.endpoints.saveText || null /* wiring:e2 — düz metin kaydı sızıntı olur */"
      :open-mode="previewMode"
      :auth-headers="() => buildAuthHeaders({ 'Content-Type': 'application/json' })"
      :auth-credentials="api.credentialsMode()"
      :drawio-url="effectiveDrawioUrl"
      :pdf-worker-url="props.config.pdfWorkerUrl || null"
      :pdf-save-url="props.config.pdfSaveUrl || null"
      :viewer-base-url="props.config.viewerBaseUrl || null"
      @close="showPreview = false"
    />
    <ConvertModal
      v-if="showConvert && convertTarget && effectiveConvertUrl"
      :convert-url="effectiveConvertUrl"
      :file-name="convertTarget?.basename || convertTarget?.path || ''"
      :fetch-bytes="() => api.fetchArrayBuffer(convertTarget?.path ?? '')"
      :upload="(f) => api.uploadMultipart(qualify(currentPath), [f]).then(() => {})"
      @close="showConvert = false"
      @done="onConvertDone"
    />
    <PermissionsModal
      v-if="showPerm && permTarget"
      :api="api"
      :path="permTarget.path"
      :is-dir="permTarget.type === 'dir'"
      :size="typeof permTarget.size === 'number' ? permTarget.size : undefined"
      :locale="locale === 'en' ? 'en' : 'tr'"
      @close="showPerm = false"
    />

    <!-- Recently-opened tray. Anchored to the toolbar trigger via fixed
         position; click the backdrop or any entry to dismiss.
         `.fe` + theme class keeps the dark/light cascade matching the
         host shell — without them the popup floats outside the
         FileExplorer root and falls back to :root light defaults. -->
    <transition name="fe-modal">
      <div
        v-if="showRecents"
        class="fe fe-modal__backdrop fe-recents__backdrop"
        :class="{
          'fe--theme-light': config.theme === 'light',
          'fe--theme-dark': config.theme === 'dark',
        }"
        @click="showRecents = false"
      >
        <div class="fe-recents__panel" @click.stop>
          <div class="fe-recents__header">
            <strong>{{ locale === 'en' ? 'Recently opened' : 'Son açılanlar' }}</strong>
            <button class="fe-recents__close" aria-label="Close" @click="showRecents = false">×</button>
          </div>
          <RecentlyOpened
            :api-base="props.config.apiBase ?? ''"
            :auth-headers="() => buildAuthHeaders()"
            :limit="20"
            :refresh-key="recentRefreshKey"
            @open="onRecentOpen"
            @error="(msg: string) => emit('error', { message: msg, context: { op: 'recents' } })"
          />
        </div>
      </div>
    </transition>

    <!-- Tag editor — opened from the context menu via Etiketler. -->
    <transition name="fe-modal">
      <div
        v-if="showTagPicker && tagPickerNode && typeof tagPickerNode.id === 'number'"
        class="fe-modal__backdrop"
        @click="showTagPicker = false"
      >
        <div class="fe-modal__card fe-modal__card--md" @click.stop>
          <header class="fe-modal__head">
            <h2 class="fe-modal__title">
              {{ locale === 'en' ? 'Tags' : 'Etiketler' }} — {{ tagPickerNode.basename }}
            </h2>
            <button class="fe-modal__close" aria-label="Close" @click="showTagPicker = false">×</button>
          </header>
          <div class="fe-modal__body">
            <TagPicker
              :node-id="tagPickerNode.id"
              :api-base="props.config.apiBase ?? ''"
              :auth-headers="() => buildAuthHeaders()"
              @error="(msg: string) => emit('error', { message: msg, context: { op: 'tags' } })"
            />
          </div>
        </div>
      </div>
    </transition>

    <!-- cila:c wiring — command palette (Ctrl/Cmd+K) + shortcuts help (?) -->
    <CommandPalette
      :open="showPalette"
      :locale="locale"
      :files="files"
      :view-mode="viewMode"
      :can-write="canWriteHere && !atVirtualRoot && !trashActive"
      :can-go-up="canGoUp"
      :global-search="paletteGlobalSearch"
      @close="showPalette = false"
      @open-hit="openSearchHit"
      @open-node="openNode"
      @navigate="(p: string) => load(p)"
      @new-folder="showNewFolder = true"
      @upload="triggerUpload"
      @toggle-view="setDisplayedViewMode(displayedViewMode === 'list' ? 'grid' : displayedViewMode === 'grid' ? 'gallery' : 'list') /* wiring:d2 + ui-fix — 3 mod döngüsü, aktif panele */"
      @open-trash="loadTrash"
      @refresh="() => load()"
      @go-up="goUp"
      @open-theme="showThemeGallery = true /* wiring:int */"
      @open-shortcut-settings="showShortcutSettings = true /* wiring:int */"
      @start-tour="startTour() /* wiring:int */"
      :split-enabled="!isNarrow /* wiring:d1 */"
      @tab-new="newTabHere() /* wiring:d1 */"
      @split-toggle="toggleSplit() /* wiring:d1 */"
    />
    <ShortcutsHelp
      :open="showShortcutsHelp"
      :locale="locale"
      @close="showShortcutsHelp = false"
      @customize="showShortcutsHelp = false; showShortcutSettings = true /* wiring:c2 */"
    />
    <!-- /cila:c wiring -->

    <!-- wiring:c1 — tema galerisi -->
    <ThemeGallery
      :open="showThemeGallery"
      :locale="locale"
      :theme="config.theme || 'auto'"
      :dark="themeResolvedDark"
      :current="activeThemeId"
      @close="showThemeGallery = false"
      @select="setActiveTheme"
    />
    <!-- /wiring:c1 -->

    <input
      ref="fileInputEl"
      type="file"
      multiple
      class="fe__file-input"
      @change="onFilePicked"
    />

    <transition name="fe-toast">
      <div
        v-if="toast"
        class="fe-toast"
        :class="{ 'fe-toast--action': !!toast.actionLabel }"
        role="status"
        @click="dismissToast"
      >
        <span class="fe-toast__msg">{{ toast.message }}</span>
        <button
          v-if="toast.actionLabel && toast.action"
          type="button"
          class="fe-toast__action"
          @click.stop="runToastAction"
        >{{ toast.actionLabel }}</button>
      </div>
    </transition>

    <!-- wiring:c2 — shortcut settings modal + Space quick-look overlay -->
    <ShortcutSettings
      :open="showShortcutSettings"
      :locale="locale"
      :theme="config.theme || 'auto'"
      @close="showShortcutSettings = false"
    />
    <QuickLook
      :open="quickLookOpen"
      :locale="locale"
      :file="quickLookTarget"
      :theme="config.theme || 'auto'"
      :preview-url="(p: string) => e2ePreviewSrc(p) /* wiring:e2 */"
      :download-url="(p: string) => (e2eUnlocked ? e2ePreviewSrc(p) : api.downloadUrl(p)) /* wiring:e2 */"
      :only-office-base="e2eActive ? null : effectiveOnlyOfficeBase /* wiring:e2 */"
      :only-office-config-endpoint="effectiveOnlyOfficeConfigEndpoint"
      :auth-headers="() => buildAuthHeaders({ 'Content-Type': 'application/json' })"
      :auth-credentials="api.credentialsMode()"
      :drawio-url="effectiveDrawioUrl"
      :pdf-worker-url="props.config.pdfWorkerUrl || null"
      :viewer-base-url="props.config.viewerBaseUrl || null"
      @close="quickLookOpen = false"
      @nav="quickLookNav"
      @open-full="quickLookOpenFull"
    />
    <!-- /wiring:c2 -->
    <!-- wiring:c4 — onboarding coach-mark tour (teleports itself to body) -->
    <OnboardingTour
      :open="showTour"
      :locale="locale"
      :root="rootEl"
      :theme="config.theme || 'auto'"
      @close="onTourClose"
    />
    <!-- /wiring:c4 -->
  </div>
</template>

<style src="./styles/variables.css"></style>
<style src="./styles/base.css"></style>
