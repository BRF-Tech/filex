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
import { preloadEditor } from './composables/useMonacoLoader';

import Toolbar, { type SelectionMode, type ToolbarAction } from './components/Toolbar.vue';
import StarButton from './components/StarButton.vue';
import TagPicker from './components/TagPicker.vue';
import RecentlyOpened from './components/RecentlyOpened.vue';
import Breadcrumb from './components/Breadcrumb.vue';
import ListView from './components/ListView.vue';
import GridView from './components/GridView.vue';
import ContextMenu, { type ContextAction } from './components/ContextMenu.vue';
import UploadProgress from './components/UploadProgress.vue';
import PendingOpsTray from './components/PendingOpsTray.vue';

import NewFolderModal from './modals/NewFolderModal.vue';
import RenameModal from './modals/RenameModal.vue';
import DeleteConfirmModal from './modals/DeleteConfirmModal.vue';
import ShareModal from './modals/ShareModal.vue';
import PreviewModal from './modals/PreviewModal.vue';

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
}>();

// --------------------------------------------------------------------
// State
// --------------------------------------------------------------------

const api = useFileApi(props.config);
const chunked = useUploadChunked(props.config, api);
const pendingOps = usePendingOps(props.config, api, {
  onSettled: (op: PendingOp) => {
    if (op.status === 'error') {
      flashToast(op.error_message || 'İşlem başarısız');
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
const currentPath = ref<string>(props.config.initialPath || '');
const adapter = ref<string>(props.config.defaultAdapter || 'brf');
const dirname = ref<string>(props.config.initialPath || '');
const files = ref<FileNode[]>([]);

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
const searchQuery = ref('');
const trashActive = computed(() => currentPath.value.startsWith('fileman/.trash'));
const locale = computed(() => props.config.locale || 'tr');

// canGoUp/goUp — toolbar's "↑ Up one level" button. In single-storage
// mode "" means the storage root; in multi-storage mode "" means
// the global root (storage list). Both → no parent → button hidden.
const canGoUp = computed(() => {
  const p = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
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
  const cur = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  if (!cur) return;
  const idx = cur.lastIndexOf('/');
  const parent = idx === -1 ? '' : cur.slice(0, idx);
  void load(parent);
}

const { t } = useLocale(locale);

const selection = useSelection(() => files.value);
watch(
  () => [...selection.selected.value],
  () => {
    emit(
      'selection-change',
      selection.nodes.value.map((n) => ({ path: n.path, basename: n.basename, type: n.type })),
    );
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
    const res = await fetch(`${base}/api/files/manager/starred?limit=500`, {
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

// Context menu
const ctxRef = ref<InstanceType<typeof ContextMenu> | null>(null);
const rootEl = ref<HTMLElement | null>(null);
const toolbarRef = ref<InstanceType<typeof Toolbar> | null>(null);

// Toast (tiny, no lib)
const toast = ref<string | null>(null);
let toastTimer: ReturnType<typeof setTimeout> | undefined;
function flashToast(msg: string) {
  toast.value = msg;
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.value = null), 2500);
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
  try {
    const requested = path ?? currentPath.value ?? '';

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
    files.value = (resp.files || []).filter((f) => {
      if (f.path.includes('.thumbs')) return false;
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
        file_size: 0,
        mime_type: 'inode/directory',
        extra_metadata: {},
      } as unknown as FileNode);
    }
    // currentPath is the user-facing form: `s3-test/example` in
    // multi-storage mode, the bare relative path otherwise.
    currentPath.value = multiStorageRoot.value
      ? wireToVirtual(resp.dirname)
      : stripAdapter(resp.dirname);
  } catch (err) {
    const e = err instanceof Error ? err.message : String(err);
    emit('error', { message: e, context: { path } });
    flashToast(e);
  } finally {
    loading.value = false;
  }
}

function stripAdapter(p: string): string {
  const idx = p.indexOf('://');
  return idx === -1 ? p : p.slice(idx + 3);
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

watch(
  () => searchQuery.value,
  () => void load(),
);

// ----------------------------------------------------------------
// Path persistence
// ----------------------------------------------------------------
const PATH_LS_KEY = 'brf-file-explorer:path';

function persistMode(): 'hash' | 'localStorage' | 'none' {
  return props.config.pathPersist ?? 'hash';
}

function readPersistedPath(): string {
  if (typeof window === 'undefined') return '';
  const mode = persistMode();
  if (mode === 'none') return '';
  if (mode === 'localStorage') {
    try {
      return localStorage.getItem(PATH_LS_KEY) || '';
    } catch {
      return '';
    }
  }
  const h = window.location.hash || '';
  if (!h.startsWith('#')) return '';
  return decodeURIComponent(h.slice(1)).replace(/^\/+|\/+$/g, '');
}

let hashSyncSuppressed = false;

function writePersistedPath(path: string) {
  if (typeof window === 'undefined') return;
  const mode = persistMode();
  if (mode === 'none') return;
  if (mode === 'localStorage') {
    try {
      if (path) localStorage.setItem(PATH_LS_KEY, path);
      else localStorage.removeItem(PATH_LS_KEY);
    } catch {
      /* private mode / quota */
    }
    return;
  }
  const target = path ? `#${path}` : '';
  if (window.location.hash === target) return;
  hashSyncSuppressed = true;
  history.replaceState(
    null,
    '',
    target || window.location.pathname + window.location.search,
  );
}

function onHashChange() {
  if (persistMode() !== 'hash') return;
  if (hashSyncSuppressed) {
    hashSyncSuppressed = false;
    return;
  }
  const p = readPersistedPath();
  if (p && p !== currentPath.value) {
    void load(p);
  }
}

watch(currentPath, (p) => writePersistedPath(p));

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
  if (persistMode() === 'hash') {
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
  },
  onFocusSearch: () => toolbarRef.value?.focusSearch(),
  onCut: () => cut(),
  onCopy: () => copyToClipboard(),
  onPaste: () => paste(),
  onGoUp: () => goUp(),
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
  const ext = (n.extension || '').toLowerCase();
  const officeBlocked = OFFICE_EXTS.has(ext) && !effectiveOnlyOfficeBase.value;
  const drawioBlocked = (ext === 'drawio' || ext === 'dio') && !effectiveDrawioUrl.value;
  if (props.config.openPageBase && !officeBlocked && !drawioBlocked) {
    const url = `${props.config.openPageBase}?path=${encodeURIComponent(n.path)}&mode=edit&type=${encodeURIComponent(ext)}`;
    window.open(url, '_blank', 'noopener');
    emit('file-opened', { path: n.path, basename: n.basename });
    void markRecent(n);
    return;
  }
  // Fallback when no editor page is configured, or the editor would
  // hit an offline backend — in-page modal.
  previewMode.value = 'edit';
  previewTarget.value = n;
  showPreview.value = true;
  emit('file-opened', { path: n.path, basename: n.basename });
  void markRecent(n);
}

async function restoreSelection(targets?: FileNode[]) {
  if (!api.endpoints.restore) return;
  const nodes = targets ?? selection.nodes.value;
  const items = nodes.map((n) => n.path); // qualified
  if (items.length === 0) return;
  try {
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

async function onToolbarAction(key: ToolbarAction) {
  const sel = selection.nodes.value;
  switch (key) {
    case 'open':
      if (sel[0]) openNode(sel[0]);
      break;
    case 'preview':
      if (sel[0] && sel[0].type === 'file') previewNode(sel[0]);
      break;
    case 'download':
      if (sel[0] && sel[0].type === 'file') downloadFile(sel[0]);
      break;
    case 'share':
      if (sel[0]) openShare(sel[0]);
      break;
    case 'rename':
      if (sel.length === 1) {
        renameTarget.value = sel[0];
        showRename.value = true;
      }
      break;
    case 'delete':
      if (sel.length > 0) showDelete.value = true;
      break;
    case 'restore':
      if (sel.length > 0) await restoreSelection();
      break;
    case 'cut':
      cut();
      break;
    case 'copy':
      copyToClipboard();
      break;
    case 'paste':
      await paste();
      break;
  }
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
  const isFile = single && sel[0]?.type === 'file';

  if (trashActive.value) {
    if (!any) return [];
    return [
      { key: 'restore', label: t('ctx.restore'), icon: '↩' },
      { divider: true, key: 'sep1', label: '' },
      { key: 'delete', label: t('ctx.delete_perm'), icon: '🗑', danger: true },
    ];
  }

  const tagsLabel = locale.value === 'en' ? 'Tags…' : 'Etiketler…';
  const singleHasId = single && typeof sel[0]?.id === 'number';
  return [
    { key: 'open', label: t('ctx.open'), icon: '↗', hidden: !single },
    { key: 'preview', label: t('ctx.preview'), icon: '👁', hidden: !single, disabled: !isFile },
    { key: 'download', label: t('ctx.download'), icon: '⬇', hidden: !single, disabled: !isFile },
    { key: 'share', label: t('ctx.share'), icon: '🔗', hidden: !single, disabled: !single },
    { divider: true, key: 'sep1', label: '' },
    { key: 'rename', label: t('ctx.rename'), icon: '✎', hidden: !single, disabled: !single },
    { key: 'duplicate', label: t('ctx.duplicate'), icon: '⎘', hidden: !any, disabled: !any },
    { key: 'cut', label: t('ctx.cut'), icon: '✂', hidden: !any, disabled: !any },
    { key: 'copy', label: t('ctx.copy'), icon: '❐', hidden: !any, disabled: !any },
    { key: 'paste', label: t('ctx.paste'), icon: '📋', disabled: !clipboard.value.mode },
    { divider: true, key: 'sep-meta', label: '', hidden: !singleHasId },
    { key: 'tags', label: tagsLabel, icon: '🏷', hidden: !singleHasId, disabled: !singleHasId },
    { divider: true, key: 'sep2', label: '' },
    { key: 'delete', label: t('ctx.delete'), icon: '🗑', danger: true, hidden: !any, disabled: !any },
    { divider: true, key: 'sep3', label: '', hidden: any },
    { key: 'new-folder', label: t('toolbar.new_folder'), icon: '📁', hidden: any },
  ];
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

  switch (action.key) {
    case 'open':
      if (targets[0]) openNode(targets[0]);
      break;
    case 'preview':
      if (targets[0]) previewNode(targets[0]);
      break;
    case 'download':
      if (targets[0]) downloadFile(targets[0]);
      break;
    case 'share':
      if (targets[0]) openShare(targets[0]);
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
      const { op } = await api.moveAsync(items, qualify(currentPath.value), qualify(sourceDir) || undefined);
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
    await api.rename(qualify(currentPath.value), target.path, name);
    showRename.value = false;
    renameTarget.value = null;
    await load();
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'rename' } });
  }
}

async function confirmDelete() {
  const items = selection.nodes.value.map((n) => n.path);
  if (items.length === 0) {
    showDelete.value = false;
    return;
  }
  try {
    if (api.endpoints.deleteAsync) {
      const { op } = await api.deleteAsync(items, qualify(currentPath.value));
      pendingOps.register(op);
      flashToast('Silme kuyruğa alındı');
    } else {
      await api.deleteItems(qualify(currentPath.value), items);
      await load();
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
    if (!canChunk || f.size < 10 * 1024 * 1024) {
      await legacyUpload(f);
      continue;
    }
    await chunkedUpload(f);
  }
  await load();
}

async function legacyUpload(file: File) {
  try {
    await api.uploadMultipart(qualify(currentPath.value), [file]);
  } catch (err) {
    emit('error', {
      message: (err as Error).message,
      context: { op: 'upload', file: file.name },
    });
  }
}

async function chunkedUpload(file: File) {
  const placeholder: UploadJob = {
    id: crypto.randomUUID(),
    file,
    path: qualify(currentPath.value),
    totalBytes: file.size,
    uploadedBytes: 0,
    percent: 0,
    status: 'pending',
    cancel() {},
  };
  uploadJobs.value = [...uploadJobs.value, placeholder];

  await chunked
    .uploadFile({
      path: qualify(currentPath.value),
      file,
      onProgress: (job) => {
        const idx = uploadJobs.value.findIndex((j) => j.id === placeholder.id);
        if (idx !== -1) {
          const next = [...uploadJobs.value];
          next[idx] = { ...job, id: placeholder.id } as UploadJob;
          uploadJobs.value = next;
          emit('upload-progress', {
            uploadId: job.uploadId ?? placeholder.id,
            percent: job.percent,
            done: job.status === 'done',
          });
        }
      },
      onError: (job, err) => {
        void job;
        emit('error', {
          message: err.message,
          context: { op: 'upload', file: file.name },
        });
      },
    })
    .catch(() => {});
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
    if (api.endpoints.moveAsync) {
      const { op } = await api.moveAsync(sources, targetDir, qualify(currentPath.value));
      pendingOps.register(op);
      flashToast('Taşıma kuyruğa alındı');
    } else {
      await api.move(qualify(currentPath.value), sources, targetDir);
      await load();
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
      :selection-mode="selectionMode"
      :paste-enabled="!!clipboard.mode"
      :can-go-up="canGoUp"
      :at-virtual-root="atVirtualRoot"
      :locale="locale"
      @update:view-mode="viewMode = $event"
      @update:search-query="searchQuery = $event"
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
      @navigate="onNavigate"
      @copy-path="onCopyPath"
      @crumb-context="onCrumbContext"
      @crumb-drop="onCrumbDropInto"
    />

    <div class="fe__body" @click.self="selection.clear()">
      <ListView
        v-if="viewMode === 'list'"
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

    <!-- Recently-opened tray. Anchored to the toolbar trigger via fixed
         position; click the backdrop or any entry to dismiss. -->
    <transition name="fe-modal">
      <div
        v-if="showRecents"
        class="fe-modal__backdrop fe-recents__backdrop"
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

    <input
      ref="fileInputEl"
      type="file"
      multiple
      class="fe__file-input"
      @change="onFilePicked"
    />

    <transition name="fe-toast">
      <div v-if="toast" class="fe-toast">{{ toast }}</div>
    </transition>
  </div>
</template>

<style src="./styles/variables.css"></style>
<style src="./styles/base.css"></style>
