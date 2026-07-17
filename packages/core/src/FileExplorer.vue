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
import { useFileApi } from './composables/useFileApi';
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
import ContextMenu, { type ContextAction } from './components/ContextMenu.vue';
import UploadProgress from './components/UploadProgress.vue';
import PendingOpsTray from './components/PendingOpsTray.vue';
/* cila:c wiring */
import CommandPalette from './components/CommandPalette.vue';
import ShortcutsHelp from './components/ShortcutsHelp.vue';
/* /cila:c wiring */

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
      if (stored === 'list' || stored === 'grid') return stored;
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
const shareTarget = ref<FileNode | null>(null);
const activeShare = ref<(ShareInfo & { url: string; filename?: string }) | null>(null);
const previewTarget = ref<FileNode | null>(null);
const previewMode = ref<'edit' | 'view'>('edit');
const showConvert = ref(false);
const convertTarget = ref<FileNode | null>(null);
const showPerm = ref(false);
const permTarget = ref<FileNode | null>(null);

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
    files.value = (resp.files || []).filter((f) => {
      if (f.path.includes('.thumbs')) return false;
      if (f.path.includes('.versions') || f.basename === '.versions') return false;
      if (f.basename === '.trash') return false;
      if (f.basename === '.keepdir') return false;
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

useKeyboardShortcuts(rootEl, {
  onDelete: () => {
    if (!selection.isEmpty.value) showDelete.value = true;
  },
  onRename: () => {
    if (selection.nodes.value.length === 1) {
      renameTarget.value = selection.nodes.value[0];
      showRename.value = true;
    }
  },
  onSelectAll: () => selection.selectAll(),
  onOpen: () => {
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
  },
  onFocusSearch: () => toolbarRef.value?.focusSearch(),
  onCut: () => cut(),
  onCopy: () => copyToClipboard(),
  onPaste: () => paste(),
  onGoUp: () => goUp(),
  /* cila:c wiring */
  onPathJump: () => {
    showPalette.value = true;
  },
  onShowHelp: () => {
    showShortcutsHelp.value = true;
  },
  /* /cila:c wiring */
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

type ContextMode = 'selection' | 'breadcrumb';
const ctxMode = ref<ContextMode>('selection');
const breadcrumbCtxPath = ref<string>('');
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

const contextActions = computed<ContextAction[]>(() => {
  if (ctxMode.value === 'breadcrumb') {
    return [
      { key: 'open', label: t('ctx.open'), icon: '↗' },
      { key: 'copy-path', label: t('breadcrumb.copy_path'), icon: '📋' },
    ];
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
    { key: 'preview', label: t('ctx.preview'), icon: '👁', hidden: !single, disabled: !isFile },
    { key: 'download', label: t('ctx.download'), icon: '⬇', hidden: !single, disabled: !isFile },
    { key: 'convert', label: t('ctx.convert'), icon: '🔄', hidden: !single || !effectiveConvertUrl.value || !w, disabled: !isFile },
    { key: 'access', label: accessLabel, icon: '🔗', hidden: !single || !w },
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
        showRename.value = true;
      }
      break;
    case 'cut':
      clipboard.value = { mode: 'cut', items: targets, sourcePath: currentPath.value };
      flashToast('Kes → Yapıştır hazır');
      break;
    case 'copy':
      clipboard.value = { mode: 'copy', items: targets, sourcePath: currentPath.value };
      flashToast('Kopyala → Yapıştır hazır');
      break;
    case 'paste':
      await paste();
      break;
    case 'delete':
      showDelete.value = true;
      break;
    case 'restore':
      if (targets.length > 0) await restoreSelection(targets);
      break;
    case 'new-folder':
      showNewFolder.value = true;
      break;
    case 'duplicate':
      if (targets[0]) await duplicate(targets[0]);
      break;
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
  // Keep `<adapter>://<rel>` so backend resolves the right storage
  // (stripping it would default to the first storage, which 404s for
  // any non-default storage like S3/SFTP/WebDAV).
  const url = api.downloadUrl(n.path);
  window.open(url, '_blank');
}

// ------- Modals -------

async function submitNewFolder(name: string) {
  try {
    await api.newFolder(qualify(currentPath.value), name);
    showNewFolder.value = false;
    await load();
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'newfolder' } });
  }
}

async function submitRename(name: string) {
  const target = renameTarget.value;
  if (!target) return;
  try {
    const dirWire = qualify(currentPath.value);
    const oldPath = target.path; // qualified
    const oldName = target.basename;
    await api.rename(dirWire, oldPath, name);
    showRename.value = false;
    renameTarget.value = null;
    await load();
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
  const targets = selection.nodes.value;
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
      const { op } = await api.deleteAsync(items, qualify(currentPath.value));
      if (restoreUndo) {
        opUndo.set(op.id, { message: t('toast.trashed'), fn: restoreUndo });
      }
      pendingOps.register(op);
      flashToast('Silme kuyruğa alındı');
    } else {
      await api.deleteItems(qualify(currentPath.value), items);
      await load();
      if (restoreUndo) undoToast(t('toast.trashed'), restoreUndo);
    }
    showDelete.value = false;
    selection.clear();
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
  if (isExternalFileDrag(ev)) {
    ev.preventDefault();
  }
}
function onDropUpload(ev: DragEvent) {
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
  ev.dataTransfer.setData('text/plain', items.map((i) => i.path).join('\n'));
  ev.dataTransfer.effectAllowed = 'move';
}

async function moveSourcesAsync(sources: string[], targetDir: string, opLabel: string): Promise<void> {
  try {
    const originWire = qualify(currentPath.value);
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
    .filter((p) => p && p !== targetDir && !targetDir.startsWith(p + '/'));
  if (sources.length === 0) {
    flashToast('Aynı klasöre taşınamaz');
    return;
  }
  await moveSourcesAsync(sources, targetDir, 'move-dnd');
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
    .filter((p) => p && p !== targetDir && !targetDir.startsWith(p + '/'));
  if (sources.length === 0) return;
  await moveSourcesAsync(sources, targetDir, 'move-dnd-crumb');
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
    }"
    tabindex="-1"
    @dragenter="onDragEnter"
    @dragover="onDragOver"
    @dragleave="onDragLeave"
    @drop="onDropUpload"
    @contextmenu="onContextCanvas"
  >
    <Toolbar
      ref="toolbarRef"
      :view-mode="viewMode"
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
      @update:view-mode="viewMode = $event"
      @update:search-query="searchQuery = $event"
      @update:density="density = $event"
      @new-folder="showNewFolder = true"
      @upload="triggerUpload"
      @refresh="() => load()"
      @go-up="goUp"
      @action="onToolbarAction"
      @open-recents="showRecents = true"
    />

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

    <div class="fe__body" @click.self="selection.clear()">
      <!-- Initial load: skeleton ghosts (view-mode aware) instead of an
           empty/"no files" flash. Only when there's nothing yet — navigation
           keeps the current list, exactly as before. -->
      <div v-if="loading && files.length === 0" class="fe__skeleton" role="status">
        <span class="fe-sr-only">{{ t('loading') }}</span>
        <div v-if="viewMode === 'grid'" class="fe-skel-grid" aria-hidden="true">
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
        <p class="fe-state__hint">{{ loadError }}</p>
        <div class="fe-state__actions">
          <button type="button" class="fe-btn fe-btn--primary" @click="retryLoad">
            {{ t('error.retry') }}
          </button>
        </div>
      </div>
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
    </div>

    <div v-if="dragOver" class="fe__dragover">
      <div class="fe__dragover-card">
        <span class="fe-icon">⬆</span>
        <p>Dosyaları buraya bırak</p>
      </div>
    </div>

    <UploadProgress
      :jobs="uploadJobs"
      :locale="locale"
      @cancel="onCancelUpload"
      @dismiss="onDismissUpload"
    />

    <PendingOpsTray
      :ops="pendingOps.ops.value"
      :locale="locale"
      @dismiss="(id) => pendingOps.dismiss(id)"
    />

    <ContextMenu
      ref="ctxRef"
      :locale="locale"
      :theme="config.theme || 'auto'"
      :actions="contextActions"
      @select="onContextAction"
    />

    <NewFolderModal
      :open="showNewFolder"
      :locale="locale"
      @close="showNewFolder = false"
      @submit="submitNewFolder"
    />
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
      :preview-url="(p) => api.previewUrl(p)"
      :download-url="(p) => api.downloadUrl(p)"
      :only-office-base="effectiveOnlyOfficeBase"
      :only-office-config-endpoint="effectiveOnlyOfficeConfigEndpoint"
      :save-text-endpoint="api.endpoints.saveText || null"
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
      @close="showPalette = false"
      @open-node="openNode"
      @navigate="(p: string) => load(p)"
      @new-folder="showNewFolder = true"
      @upload="triggerUpload"
      @toggle-view="viewMode = viewMode === 'list' ? 'grid' : 'list'"
      @open-trash="loadTrash"
      @refresh="() => load()"
      @go-up="goUp"
    />
    <ShortcutsHelp
      :open="showShortcutsHelp"
      :locale="locale"
      @close="showShortcutsHelp = false"
    />
    <!-- /cila:c wiring -->

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
  </div>
</template>

<style src="./styles/variables.css"></style>
<style src="./styles/base.css"></style>
