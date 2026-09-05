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
import { computed, customRef, nextTick, onBeforeUnmount, onMounted, ref, watch, watchEffect } from 'vue';
import type { ExplorerConfig, ThemeMode } from './types/ExplorerConfig';
import type {
  FileNode,
  ShareInfo,
  ViewMode,
  ClipboardState,
  Capabilities,
} from './types/FileNode';
import { isExternalUsable } from './types/FileNode';
import { useFileApi, type GlobalSearchHit } from './composables/useFileApi';
import {
  useUploadChunked,
  isStagedUnsupported,
  type UploadJob,
} from './composables/useUploadChunked';
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
import FilterBar from './components/FilterBar.vue' /* surucu:d1 */;
import ViewSwitcher from './components/ViewSwitcher.vue' /* surucu:d1 */;
import {
  EMPTY_FILTERS,
  applyFilters,
  filtersActive,
  type DriveFilters,
} from './lib/fileFilters' /* surucu:d1 */;
import GalleryView from './components/GalleryView.vue'; /* wiring:d2 */
import ContextMenu, { type ContextAction } from './components/ContextMenu.vue';
import UploadProgress from './components/UploadProgress.vue';
import PendingOpsTray from './components/PendingOpsTray.vue';
import InspectorPanel from './components/InspectorPanel.vue'; /* koru:k1 */
import SideNav from './components/SideNav.vue'; /* gezinti:g1 */
import ConnectionsPanel from './components/ConnectionsPanel.vue'; /* gezinti:g1 */
import TokensPanel from './components/TokensPanel.vue'; /* gezinti:g1 */
/* cila:c wiring */
import CommandPalette from './components/CommandPalette.vue';
import ShortcutsHelp from './components/ShortcutsHelp.vue';
/* /cila:c wiring */
/* wiring:c1 — tema galerisi */
import ThemeGallery from './components/ThemeGallery.vue';
import {
  useThemeState,
  useThemeModeState,
  applyThemeToEl,
  syncThemeStyle,
  type ThemeModePref,
} from './lib/themes';
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
/* wiring:d1 — tabs + per-tab split */
import TabBar from './components/TabBar.vue';
import SecondaryPane from './components/SecondaryPane.vue';
import { useTabs, type TabState } from './composables/useTabs';
/* /wiring:d1 */
/* wiring:e2 — end-to-end encrypted folders (docs/E2E-ENCRYPTION.md) */
import EncryptedFolderModal from './components/EncryptedFolderModal.vue';
import RecoveryKeyModal from './components/RecoveryKeyModal.vue';
import E2eRecoveryUnlockModal from './components/E2eRecoveryUnlockModal.vue';
import {
  createKeyRing,
  createEncryptedFolder,
  upgradeMarkerV1,
  addEscrowSlot,
  declineEscrowSlot,
  escrowOfferState,
  parseMarker,
  unlockWithPassword,
  unlockWithRecoveryKey,
  unlockWithEscrowKey,
  importEscrowPrivateKey,
  markerHasRecovery,
  escrowAvailability,
  bytesToB64,
  b64ToBytes,
  encryptFile,
  decryptFile,
  hasMagic,
  E2E_MARKER_NAME,
  E2E_MAX_FILE_BYTES,
  type E2eMarker,
} from './lib/e2ecrypto';
/* /wiring:e2 */

/* ui-fix — listing helpers shared with SecondaryPane (single source: the
   internal-entry filter + virtual `.trash` row must be identical in both
   panes or split view shows mismatched rows). */
import {
  filterListing,
  virtualSegmentLabel,
  makeTagSegment,
  tagOfPath,
  VIRTUAL_SEGMENTS,
  showHiddenFiles,
  setShowHiddenFiles,
  injectTrashRow,
  hydrateTrashRow as hydrateTrashRowShared,
} from './lib/listing';
import { setNodeStarred } from './lib/star';
import { fetchAllTags, fetchTaggedRows, invalidateTagCache } from './lib/tags';
import { resolveTransfer, type TransferIntent } from './lib/transfer';
import {
  activeNativeDrag,
  beginNativeDrag,
  canDownloadUrlDrag,
  dragKey,
  downloadUrlPayload,
  endNativeDrag,
  hasInternalDrag,
  internalDragItems,
  internalDragOrigin,
  type DragItem,
} from './lib/dragOut';

import NewFolderModal from './modals/NewFolderModal.vue';
import RenameModal from './modals/RenameModal.vue';
import DeleteConfirmModal from './modals/DeleteConfirmModal.vue';
import ShareModal from './modals/ShareModal.vue';
import PreviewModal from './modals/PreviewModal.vue';
import ConvertModal from './modals/ConvertModal.vue';
import PermissionsModal from './modals/PermissionsModal.vue';
import { resolveLocale } from './locales/resolve';

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
const locale = computed(() => resolveLocale(props.config.locale));
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
      flashToast(op.error_message || t('toast.failed'));
    } else if (undo) {
      undoToast(`${undo.message} (${op.progress_total})`, undo.fn);
    } else {
      const verb =
        op.op_type === 'copy'
          ? t('toast.copied')
          : op.op_type === 'move'
            ? t('toast.moved')
            : t('toast.deleted');
      flashToast(`${verb} (${op.progress_total})`);
    }
    void load();
    void splitPaneRef.value?.reload(); /* wiring:d1 — refresh the secondary pane too */
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

/* === gezinti:g1 — the navigation panel's virtual views ===================
 * Recent / Starred / Shared with me / Trash are listings with no folder behind
 * them: the rows come from a per-user endpoint and each carries its own
 * adapter-qualified path, so opening one navigates the ordinary way. The
 * pattern is trashMode's, generalised — including the part that matters most,
 * that load() clears the mode, or the view sticks and every later navigation
 * renders under the wrong heading. */
type NavView = '' | 'recent' | 'starred' | 'shared' | 'trash' | 'tag';
const navView = ref<NavView>('');
/** Where the view was entered from, so "up" goes back there. */
const navViewOrigin = ref<string>('');
/** The tag being browsed while navView === 'tag' ('' otherwise). */
const navTag = ref<string>('');
/** Sentinel parked in `dirname` so the breadcrumb can label the view. The tag
 *  view's sentinel is built per tag (`makeTagSegment`) — see lib/listing. */
const NAV_VIEW_DIRNAME: Record<Exclude<NavView, '' | 'trash' | 'tag'>, string> = {
  recent: '.recent',
  starred: '.starred',
  shared: '.shared',
};

/**
 * A path that is a virtual view rather than a folder. Used by load() so a
 * sentinel reaching it — a restored tab, a pasted `#.tag~invoices`, a reload,
 * the breadcrumb's own crumb — opens the VIEW instead of asking the backend
 * for a folder called `.starred` and landing on "not found". (That was already
 * true of the four shipped views; the tag view would have inherited it.)
 */
function virtualViewOf(path: string): { kind: Exclude<NavView, ''>; tag: string } | null {
  const clean = String(path ?? '').replace(/^\/+|\/+$/g, '');
  if (!clean) return null;
  const tag = tagOfPath(clean);
  if (tag) return { kind: 'tag', tag };
  const key = VIRTUAL_SEGMENTS[clean];
  if (!key) return null;
  const kind = clean.slice(1) as Exclude<NavView, '' | 'tag'>;
  return { kind, tag: '' };
}

// When the caller can see exactly ONE storage, the multi-storage root is a
// one-row list that carries no information — the user clicks through it every
// single time. Treat that storage as the floor instead: open it directly and
// stop offering an "up" that only leads back to the one row.
//
// Empty string means "not in that situation" (single-storage mode, or more
// than one storage visible), which leaves every existing path untouched.
const soleStorageName = computed(() => {
  if (!multiStorageRoot.value) return '';
  const list = props.config.storages ?? [];
  return list.length === 1 ? list[0].name : '';
});

// canGoUp/goUp — toolbar's "↑ Up one level" button. In single-storage
// mode "" means the storage root; in multi-storage mode "" means
// the global root (storage list). Both → no parent → button hidden.
const canGoUp = computed(() => {
  const p = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  if (rootFloor && p === rootFloor) return false; // at the confined floor — nowhere above
  if (soleStorageName.value && p === soleStorageName.value) return false;
  return p.length > 0;
});

// True when the explorer is showing the synthetic storage list and
// there's no real backend folder to mutate. New Folder / Upload /
// Paste are hidden in this state.
const atVirtualRoot = computed(() => {
  // gezinti:g1 — a virtual view (Recent / Starred / Shared with me) has no
  // backend folder behind it either. "New folder" there would have to invent a
  // destination, and "upload" would have to guess one.
  if (navView.value && navView.value !== 'trash') return true;
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
  /* gezinti:g1 — the other virtual views behave the same way. */
  if (navView.value) {
    void load(navViewOrigin.value);
    return;
  }
  const cur = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  if (!cur || cur === rootFloor) return;
  // The button is hidden here, but Alt+↑ / Backspace still route through.
  if (soleStorageName.value && cur === soleStorageName.value) return;
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
// Longest life a new share link may be given (server setting, days; 0 = no
// ceiling). Both share dialogs derive their expiry choices from it.
const shareMaxTtlDays = computed(() => capabilitiesData.value?.share_max_ttl_days ?? 0);

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
      // ⚠ NOT 'include'. With a bearer token the request is cross-origin for
      // every embedder that serves the UI from a different origin to the API
      // (the desktop app is one), and a credentialed request may not be
      // answered with `Access-Control-Allow-Origin: *` — which is what filex
      // sends. This one line made starred files fail silently in every such
      // install while the rest of the explorer worked.
      credentials: api.credentialsMode(),
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

/* === yildiz:s1 — starring as an ACTION ================================
 * The star shipped as an indicator in ONE view: `StarButton` was rendered by
 * ListView and nowhere else, so a user in grid view (the mode the navigation
 * panel's own screenshots show) had a Starred view with no way to fill it.
 * It is a verb, like tagging — so it is a menu entry beside Tags, a chip on
 * every card, and a key.
 *
 * ⚠ ONE implementation of the request: `lib/star.ts`. StarButton calls it,
 * this calls it. A menu cannot render a component, but it must not grow its
 * own fetch either — that is the second path that drifts.
 */
/** Which of `targets` can carry a star: files the server knows by id.
 *
 * Empty when the identity surfaces are suppressed, which is the one
 * chokepoint the context menu, the toolbar and the keyboard action all go
 * through — so the affordance disappears everywhere at once rather than in
 * the two places somebody remembered. Offering to star a file while the
 * Starred view is hidden would write into a list the person cannot open,
 * and under a shared app token that list belongs to everyone at once. */
function starableNodes(targets: FileNode[]): FileNode[] {
  if (!identitySurfaces.value) return [];
  return targets.filter((n) => typeof n.id === 'number' && n.type === 'file');
}

/** True when EVERY starable target is starred — i.e. the action reads
 *  "Unstar". A mixed selection reads "Star" and stars the rest, which is the
 *  behaviour that needs no explanation. */
function selectionAllStarred(targets: FileNode[]): boolean {
  const list = starableNodes(targets);
  return list.length > 0 && list.every((n) => starredIds.value.has(n.id as number));
}

/**
 * Toggle the star on a selection. Optimistic like the button, and rolled back
 * per node on failure — a partial failure must not leave the set lying about
 * what the server holds.
 */
async function toggleStar(targets: FileNode[]) {
  const list = starableNodes(targets);
  if (list.length === 0) return;
  const next = !selectionAllStarred(list);
  const opts = {
    apiBase: props.config.apiBase ?? '',
    authHeaders: () => buildAuthHeaders(),
    authCredentials: api.credentialsMode(),
  };
  const set = new Set(starredIds.value);
  for (const n of list) {
    if (next) set.add(n.id as number);
    else set.delete(n.id as number);
  }
  starredIds.value = set;
  let failed = 0;
  await Promise.all(
    list.map(async (n) => {
      try {
        await setNodeStarred(n.id as number, next, opts);
      } catch {
        failed += 1;
        const rollback = new Set(starredIds.value);
        if (next) rollback.delete(n.id as number);
        else rollback.add(n.id as number);
        starredIds.value = rollback;
      }
    }),
  );
  if (failed > 0) flashToast(t('star.failed'));
  // Starring is what fills the Starred view; if that IS the view on screen,
  // an unstar has to remove the row instead of leaving a listing that
  // disagrees with its own heading.
  if (navView.value === 'starred') await loadNavView('starred');
}
/* === /yildiz:s1 === */

async function markRecent(n: FileNode) {
  if (typeof n.id !== 'number') return;
  try {
    const base = props.config.apiBase ?? '';
    await fetch(`${base}/api/files/manager/recent`, {
      method: 'POST',
      headers: await buildAuthHeaders({ 'Content-Type': 'application/json' }),
      credentials: api.credentialsMode(),
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

/* etiket:t1 — the user just changed a node's tags, so the cached "every tag
 * that exists" list is wrong RIGHT NOW, which is the only staleness anybody
 * notices. Drop it and re-ask; if a tag view is on screen, refresh it too —
 * removing a file's tag has to remove it from the listing that is named after
 * that tag. */
function onNodeTagsChanged() {
  invalidateTagCache();
  void loadNavTags(true);
  if (navView.value === 'tag' && navTag.value) void loadTagView(navTag.value);
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
/* ui-fix — does the open rename/delete/new-folder modal belong to the side
 * pane? (the menu is identical to the main pane's; this routes the mutation
 * to the right one.) */
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

/* === gezinti:g1 — navigation panel (SideNav) =============================
 * One explorer with the navigation everybody already knows, collapsible so the
 * existing UI keeps its width when somebody does not want it (GitHub #14).
 *
 * The panel is NOT gated on role or profile: administrators get it too, and
 * `uiProfile` only changes the rest of the chrome. Gating it would be exactly
 * the "one behaviour on one surface" split this shared package exists to
 * prevent. */
const uiProfile = computed(() => props.config.uiProfile ?? 'standard');
/**
 * ⚠ `drive` answers TRUE here. It is a superset of `simple`, so every question
 * `simple` already answers ("one pane?", "no tab strip?", "list and grid
 * only?") must keep the same answer under it — asking `=== 'simple'` in those
 * places is how the drive profile would silently grow a split pane the day
 * somebody adds a fourth condition. `driveShell` is only for what `drive` adds
 * on TOP.
 */
const simpleUi = computed(() => uiProfile.value === 'simple' || uiProfile.value === 'drive');
/* === surucu:d1 — the Drive shell (GitHub #14, the reporter's mockups) ===== */
const driveShell = computed(() => uiProfile.value === 'drive');

const SIDENAV_LS_KEY = 'filex.sidenav';
const sideNavExpanded = ref<boolean>(
  (() => {
    try {
      const v = localStorage.getItem(SIDENAV_LS_KEY);
      if (v === '1') return true;
      if (v === '0') return false;
    } catch {
      /* private mode / embed with site data blocked */
    }
    // No stored choice: expanded. A collapsed default would ship a navigation
    // panel most people never discover, which is the problem it was built for.
    return true;
  })(),
);
function persistSideNav(v: boolean) {
  try {
    localStorage.setItem(SIDENAV_LS_KEY, v ? '1' : '0');
  } catch {
    /* quota / private mode — the choice just will not survive the session */
  }
}
/**
 * Narrow mode: the panel is a drawer over the listing, and this is its open
 * state. Deliberately NOT persisted and NOT the same ref as the desktop
 * collapse: at 390px a remembered "expanded" would reopen the drawer on top of
 * the files every single time the explorer mounts.
 */
const navDrawerOpen = ref(false);
/**
 * Is the panel part of this deployment at all? On by default everywhere —
 * except under `rootPath`, where there is no storage list to show and the views
 * would list files from outside the folder the embed was confined to.
 */
const sideNavEnabled = computed(() => props.config.sideNav ?? !rootPathProp);
const navVisible = computed(
  () => sideNavEnabled.value && (isNarrow.value ? navDrawerOpen.value : true),
);
/**
 * Is the navigation panel already offering Trash as a destination?
 *
 * ⚠ Keyed to `sideNavEnabled`, NOT to `navVisible`. Three cases decided here,
 * and each one is a judgement about whether the person has a way to the bin
 * that is not this row:
 *
 *   - collapsed to the icon rail → OFFERING. The entry is still rendered, still
 *     one click, still labelled for a screen reader; only its text is gone.
 *   - the drawer under 560px, closed → OFFERING. The toolbar's panel toggle is
 *     on screen at every width, so the destination is one tap away. Keying this
 *     to `navVisible` instead would add and remove a row from the LISTING every
 *     time somebody opened or closed the drawer — the folder changing under
 *     them for a reason that has nothing to do with the folder.
 *   - no panel at all (`sideNav: false`, or the default under `rootPath`) → NOT
 *     offering, and the row stays. That is the case it was invented for: a
 *     listing with no other door to the bin.
 *
 * `trashVisible: false` is handled inside the helper and outranks all of it —
 * it means no Trash anywhere, panel entry included.
 */
const navOffersTrash = computed(
  () => sideNavEnabled.value && props.config.trashVisible !== false,
);

/** What the toolbar toggle reports as pressed. */
const navToggleOn = computed(() =>
  isNarrow.value ? navDrawerOpen.value : sideNavExpanded.value,
);
function toggleSideNav() {
  if (!sideNavEnabled.value) return;
  if (isNarrow.value) {
    navDrawerOpen.value = !navDrawerOpen.value;
    return;
  }
  sideNavExpanded.value = !sideNavExpanded.value;
  persistSideNav(sideNavExpanded.value);
}
function closeNavDrawer() {
  navDrawerOpen.value = false;
}

/** Storages the caller reaches only through a grant — marked in the panel. */
const sharedStorageNames = ref<string[]>([]);

/**
 * The Connections entries — "How to connect" and "API keys" — and the two
 * overlays they open. Both surfaces already lived in this package
 * (ConnectionsPanel, TokensPanel) and neither was reachable from inside the
 * explorer: our own web app wired the buttons in its page shell, so an
 * embedder's users had no path to a protocol guide or to the API token those
 * guides tell them to use.
 *
 * ⚠ Not gated on role. The backend decides — ConnectionsPanel renders what the
 * API returns (a non-admin gets the guides and a "why not" card instead of the
 * storage form), and /api/tokens caps every scope against the caller's own role.
 */
const connectionsEnabled = computed(() => props.config.connections ?? !simpleUi.value);

/**
 * Is this caller an integration rather than a person (backend migration 00030)?
 *
 * A filex API token authenticates AS its owner, so from here a shared embed
 * token and somebody's own token are indistinguishable — only the server knows
 * which kind it is, and it says so in `capabilities.caller_kind`. A host that
 * already knows can say so with `config.callerKind`, which wins — purely to
 * spare the flash of a Starred row that appears and then disappears when
 * capabilities land.
 *
 * ⚠ Defaults to "person" in every unknown state — no config, capabilities not
 * back yet, or a server too old to answer. The cost of guessing wrong that way
 * is one row too many for a moment; guessing the other way would hide Recent
 * and API keys from every ordinary user of every older server.
 */
const callerIsApp = computed(
  () => (props.config.callerKind ?? capabilitiesData.value?.caller_kind) === 'app',
);
/**
 * The identity-bearing surfaces: API keys, Recent, Starred, Shared with me.
 * ⚠ Suppression is per token KIND, never per role — a viewer is still a person
 * with their own recents. And it is not the whole panel: Upload, the storages,
 * Trash and "How to connect" stay useful inside an embed.
 */
const identitySurfaces = computed(() => !callerIsApp.value);
const showConnections = ref(false);
const showTokens = ref(false);
function openConnections() {
  showTokens.value = false;
  showConnections.value = true;
}
function openTokens() {
  // Belt and braces: the panel entry is gone for an app token, but a host may
  // also open this overlay from its own chrome, and /api/tokens would answer
  // that caller with a 403 it has nowhere to show.
  if (callerIsApp.value) return;
  showConnections.value = false;
  showTokens.value = true;
}
function closeOverlays() {
  showConnections.value = false;
  showTokens.value = false;
}
const anyOverlayOpen = computed(() => showConnections.value || showTokens.value);
/** ConnectionsPanel reports its own failures; surface them the way the
 *  explorer surfaces everything else rather than swallowing them. */
function onConnectionsError(err: unknown) {
  const msg = err instanceof Error ? err.message : String(err);
  emit('error', { message: msg, context: { op: 'connections' } });
  flashToast(msg);
}

/**
 * View modes the toolbar offers. `simple` drops gallery: a four-way switcher
 * is one of the things #14 named as power-user chrome, and gallery is the one
 * nobody outside a photo folder reaches for.
 */
const allowedViewModes = computed<ViewMode[] | undefined>(() =>
  simpleUi.value ? ['list', 'grid'] : undefined,
);
// A stored 'gallery' outlives a switch to the simple profile, and the button
// that would take the user back out of it is the one the profile hides.
watchEffect(() => {
  if (simpleUi.value && viewMode.value === 'gallery') viewMode.value = 'grid';
});

/* === surucu:d1 — the filter row =========================================
 * Three chips over the listing in hand: Type · Modified · Size.
 *
 * ⚠ CLIENT-SIDE, and that is the honest place for them, not a shortcut. The
 * listing endpoint reads exactly six parameters (`action`, `path`, `filter`,
 * `storage`, `parent`, `cache`) and has no `limit`/`offset` either — so the
 * rows in hand ARE the folder, and filtering them here answers the whole
 * question rather than "the first page of it". Wiring a chip to a `min_size`
 * the server never reads would look identical and change nothing.
 *
 * ⚠ Reset on navigation. A filter that survives a folder change makes the next
 * folder look empty, and the reason is off-screen the moment you scroll.
 */
const driveFilters = ref<DriveFilters>({ ...EMPTY_FILTERS });
const filtersOn = computed(() => driveShell.value && filtersActive(driveFilters.value));
/** What the views render. Identical reference to `files` when nothing is set. */
const displayFiles = computed<FileNode[]>(() =>
  driveShell.value ? applyFilters(files.value, driveFilters.value) : files.value,
);
function clearDriveFilters() {
  setDriveFilters({ ...EMPTY_FILTERS });
}
function setDriveFilters(v: DriveFilters) {
  driveFilters.value = v;
  // A selection the filter just hid would still be what Delete acts on, with
  // nothing on screen to say so.
  selection.clear();
}
watch(
  () => `${currentPath.value}|${navView.value}`,
  () => {
    if (filtersActive(driveFilters.value)) driveFilters.value = { ...EMPTY_FILTERS };
  },
);

/**
 * surucu:d1 — what the header field says it will search: the folder you are
 * standing in, by name. At a storage root that is the storage; in a panel view
 * ("Recent", a tag) it is that view's own name, because searching from there
 * searches what is on screen.
 */
const driveScopeLabel = computed(() => {
  if (navView.value === 'tag') return navTag.value;
  if (navView.value) return t(`sidenav.${navView.value}`);
  const rel = currentPath.value.replace(/\/+$/, '');
  const last = rel.split('/').filter(Boolean).pop();
  return last || adapter.value || '';
});

/**
 * surucu:d1 — the ⌘K escalation. The header field searches THIS folder (it
 * sets `searchQuery`, which the loader answers with `action=search`); this
 * hands the same words to the command palette, which is where "everywhere",
 * the saved searches and the commands live. One box, one shortcut, and the
 * hint printed on the box does what it says.
 */
const paletteSeed = ref('');
function openPaletteWith(q: string) {
  paletteSeed.value = q;
  showPalette.value = true;
}

/**
 * surucu:d1 — "Request files" from the New menu: the access modal on the
 * CURRENT folder, opened on its file-drop tab. The modal already owns that
 * surface; this only gives it a target, since the folder you are standing in
 * is not a selected row and has no FileNode of its own.
 */
const permInitialTab = ref<'perms' | 'share' | 'drop' | undefined>(undefined);
function openFileRequest() {
  const path = qualify(currentPath.value);
  if (!path) return;
  const segs = currentPath.value.split('/').filter(Boolean);
  permTarget.value = {
    path,
    basename: segs.length ? segs[segs.length - 1] : adapter.value,
    type: 'dir',
  };
  permInitialTab.value = 'drop';
  showPerm.value = true;
}

/** surucu:d1 — a link minted from the details panel; same event the share
 *  dialog emits, so a host listening for `share-created` hears both. */
function onInspectorShareCreated(payload: { path: string; url: string }) {
  emit('share-created', { path: payload.path, url: payload.url, pin: null });
  flashToast(t('inspector.copied'));
}

/* === surucu:d1 — the storage line under the navigation ==================
 * Fetched once per mount, and ONLY in the drive shell: no other profile draws
 * it, and an explorer that has drawn this panel for a year should not start
 * making a request it has no use for. `quotaMe()` answers null for a server
 * without the route or a caller without a person behind it, and null renders
 * nothing at all.
 */
const quotaSnapshot = ref<{ used: number; total: number; unlimited: boolean } | null>(null);
async function loadQuota() {
  if (!driveShell.value || !identitySurfaces.value) {
    quotaSnapshot.value = null;
    return;
  }
  const q = await api.quotaMe();
  if (!q) {
    quotaSnapshot.value = null;
    return;
  }
  const unlimited = !!q.unlimited || q.quota_bytes <= 0;
  // ⚠ No ceiling AND nothing counted = nothing to say, so say nothing.
  //
  // Measured, not guessed: `used_bytes` is `SUM(nodes.size) WHERE owner_id = me`
  // and `nodes.owner_id` is set by the UPLOAD path only — it is nil for every
  // file a storage sync discovered (handlers/shared.go). On an install whose
  // drives were mounted rather than uploaded into, the honest figure is
  // therefore "0 B", and a line reading "0 B used" under a drive visibly full
  // of files reads as a broken widget, not as a fact about quota. With a quota
  // set the line still earns its place — the ceiling is real and uploads count
  // against it — so only the no-quota-no-usage case is dropped.
  quotaSnapshot.value =
    unlimited && q.used_bytes <= 0
      ? null
      : { used: q.used_bytes, total: q.quota_bytes, unlimited };
}

/**
 * nodeRowToFileNode — the starred / recently-opened endpoints answer with raw
 * node rows (relative `path`, numeric `storage_id`), not the listing shape.
 *
 * The storage NAME is what a qualified path needs, and a node row does not
 * carry the id-to-name mapping. The backend fills `storage` for exactly this
 * (handlers/shared.go, attachStorageNames); against an older server the only
 * safe fallback is the single-storage case — guessing in a multi-storage
 * install sends the user to a path in somebody else's drive.
 */
function nodeRowToFileNode(row: Record<string, unknown>): FileNode | null {
  const rel = String(row?.path ?? '').replace(/^\/+/, '');
  if (!rel) return null;
  const configured = props.config.storages ?? [];
  const storageName =
    typeof row.storage === 'string' && row.storage
      ? row.storage
      : configured.length === 1
        ? configured[0].name
        : '';
  if (multiStorageRoot.value && !storageName) return null;
  const name = String(row.name ?? rel.split('/').pop() ?? '');
  const isDir = row.type === 'dir';
  const size = typeof row.size === 'number' ? row.size : 0;
  const id = typeof row.id === 'number' ? row.id : undefined;
  return {
    type: isDir ? 'dir' : 'file',
    id,
    path: storageName ? `${storageName}://${rel}` : rel,
    basename: name,
    extension: isDir
      ? ''
      : name.includes('.')
        ? (name.split('.').pop() || '').toLowerCase()
        : '',
    storage: storageName,
    visibility: 'private',
    size,
    file_size: size,
    mime_type: typeof row.mime === 'string' ? row.mime : '',
    // Keyed by node id. A file with no rendered thumbnail 404s here and the
    // view falls back to its icon — the contract the ordinary listing has too.
    thumb_url: !isDir && id !== undefined ? `/api/files/thumb/${id}` : undefined,
    extra_metadata: {},
  } as unknown as FileNode;
}

/** GET one of the view endpoints. Returns rows already in listing shape. */
async function fetchNavRows(kind: 'recent' | 'starred' | 'shared'): Promise<FileNode[]> {
  const base = props.config.apiBase ?? '';
  const url =
    kind === 'shared'
      ? `${base}/api/files/manager/shared-with-me?limit=200`
      : kind === 'starred'
        ? `${base}/api/files/manager/star/list?limit=200`
        : `${base}/api/files/manager/recent?limit=50`;
  // ⚠ await. `buildAuthHeaders` is async because a token may be a function the
  // desktop shell resolves per call; spreading the un-awaited promise sends the
  // request with no Authorization header and it fails silently with a 401.
  const res = await fetch(url, {
    headers: await buildAuthHeaders(),
    // ⚠ NOT 'include' — same reason as loadStarred: a credentialed
    // cross-origin request cannot be answered with `ACAO: *`.
    credentials: api.credentialsMode(),
  });
  if (!res.ok) throw new Error(String(res.status));
  const body = await res.json();
  if (kind === 'shared') {
    // The shared endpoint already answers in the listing shape, and reports
    // which storages are grant-only in the same call.
    sharedStorageNames.value = Array.isArray(body?.storages) ? body.storages : [];
    return (Array.isArray(body?.files) ? body.files : []) as FileNode[];
  }
  const rows: Record<string, unknown>[] = Array.isArray(body?.nodes) ? body.nodes : [];
  return rows.map(nodeRowToFileNode).filter((n): n is FileNode => n !== null);
}

/** Open one of the panel views in the main pane. */
async function loadNavView(kind: Exclude<NavView, ''>) {
  closeNavDrawer();
  if (kind === 'tag') {
    // The tag view needs a name; the panel calls loadTagView directly.
    if (navTag.value) await loadTagView(navTag.value);
    return;
  }
  if (kind === 'trash') {
    await loadTrash();
    // ⚠ After loadTrash, not before: loadTrash goes through load()-adjacent
    // state and the mode has to be the last word, or the panel row for Trash
    // never lights up.
    navView.value = 'trash';
    navTag.value = '';
    return;
  }
  loading.value = true;
  // ⚠ Only when coming from a real folder. Stepping Starred → Recent used to
  // record `.starred` as the origin, so "up" out of Recent landed in Starred
  // and the user had to press it twice to get back to their files.
  if (!navView.value) navViewOrigin.value = currentPath.value ?? '';
  navView.value = kind;
  navTag.value = '';
  trashMode.value = false;
  e2eRoot.value = '';
  selection.clear();
  try {
    files.value = await fetchNavRows(kind);
    dirname.value = NAV_VIEW_DIRNAME[kind];
    currentPath.value = NAV_VIEW_DIRNAME[kind];
    // These three span every storage, so the crumb reads "/ > Starred", not
    // "/ > My files > Starred", which would name a storage half the rows are
    // not in. Trash keeps its storage crumb: trash IS per-storage.
    adapter.value = '';
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    files.value = [];
    emit('error', { message: msg, context: { op: `nav-view:${kind}` } });
    flashToast(msg);
  } finally {
    loading.value = false;
  }
}

/* === etiket:t1 — the tag view ==========================================
 * "Tagged files should show up inside the tag." A tag is not a folder: its
 * files live all over the tree and in every storage, so this is the same
 * shape as Starred — a per-user endpoint answering with node rows, each
 * carrying its own qualified path, so opening one navigates normally.
 *
 * The sentinel is `.tag~<name>` (lib/listing). Every surface that renders a
 * path segment — tab strip, breadcrumb, inspector heading, the address-bar
 * hash — goes through `virtualSegmentLabel`, so none of them can print the
 * sentinel the way the strip once printed `.shared`.
 */
async function loadTagView(tag: string) {
  closeNavDrawer();
  const name = String(tag ?? '').trim();
  if (!name) return;
  loading.value = true;
  if (!navView.value) navViewOrigin.value = currentPath.value ?? '';
  navView.value = 'tag';
  navTag.value = name;
  trashMode.value = false;
  e2eRoot.value = '';
  selection.clear();
  try {
    const rows = await fetchTaggedRows(name, {
      apiBase: props.config.apiBase ?? '',
      authHeaders: () => buildAuthHeaders(),
      authCredentials: api.credentialsMode(),
    });
    files.value = rows.map(nodeRowToFileNode).filter((n): n is FileNode => n !== null);
    const seg = makeTagSegment(name);
    dirname.value = seg;
    currentPath.value = seg;
    // Spans every storage, like Starred/Recent/Shared — so no storage crumb.
    adapter.value = '';
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    files.value = [];
    emit('error', { message: msg, context: { op: `nav-view:tag:${name}` } });
    flashToast(msg);
  } finally {
    loading.value = false;
  }
}

/**
 * The tags that exist, for the panel's Tags section.
 *
 * ⚠ WHEN this loads was a deliberate decision, not a default: `tags/all` is a
 * distinct-scan and the panel renders in every mounted explorer (a page can
 * hold several). It is therefore NOT fetched during mount — it is asked for
 * once the first listing is on screen, through a module-level cache that
 * dedupes concurrent callers and reuses the answer for a minute
 * (lib/tags.ts). N explorers on a page cost ONE query; a navigation costs
 * none. The cache is dropped the instant the user edits tags, which is the
 * only staleness anybody can notice.
 */
const navTags = ref<string[]>([]);
const navTagsLoaded = ref(false);

async function loadNavTags(force = false) {
  if (!navVisible.value) return; // no panel → nobody can see the list
  navTags.value = await fetchAllTags({
    apiBase: props.config.apiBase ?? '',
    authHeaders: () => buildAuthHeaders(),
    authCredentials: api.credentialsMode(),
    force,
  });
  navTagsLoaded.value = true;
}

/* === /etiket:t1 === */

/** Panel to a storage root. */
function openNavStorage(name: string) {
  closeNavDrawer();
  void load(multiStorageRoot.value ? name : '');
}

/**
 * Which storages are grant-only, asked once at mount so the panel can mark them
 * before anybody opens the shared view. `limit=1` on purpose: the storage list
 * is built from every grant, the page size only bounds the item rows.
 */
async function loadSharedStorages() {
  try {
    const base = props.config.apiBase ?? '';
    const res = await fetch(`${base}/api/files/manager/shared-with-me?limit=1`, {
      headers: await buildAuthHeaders(),
      credentials: api.credentialsMode(),
    });
    if (!res.ok) return;
    const body = await res.json();
    sharedStorageNames.value = Array.isArray(body?.storages) ? body.storages : [];
  } catch {
    // Silent — an older backend has no such endpoint, and the panel is still
    // useful without the shared markers.
  }
}
/* === /gezinti:g1 === */
// Folder summary label for the no-selection state.
const inspectorDirLabel = computed(() => {
  if (trashMode.value) return t('node.trash');
  const p = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  if (!p) return adapter.value || t('breadcrumb.root');
  const seg = p.split('/').pop() || p;
  /* etiket:t1 — a THIRD surface that renders a path segment, and it had the
     same hole the tab strip did: in a virtual view the details panel headed
     itself ".starred". Same shared resolver, so it cannot drift again. */
  return virtualSegmentLabel(seg, t) || seg;
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

// Flip dot-file visibility. Both panes filter at load time rather than in a
// computed, so the listings are re-fetched instead of re-filtered — that keeps
// selection and the operations that read `files` working on exactly what is
// on screen.
function toggleHiddenFiles() {
  setShowHiddenFiles(!showHiddenFiles.value);
  void load();
  void splitPaneRef.value?.reload();
}

async function load(path?: string) {
  /* === etiket:t1 — a sentinel is a VIEW, not a folder ===================
   * A restored tab, a reload on `#.trash` / `#.starred` / `#.tag~invoices`,
   * or the breadcrumb crumb for the view you are standing in all arrive here
   * as a plain path. Without this they went to the backend as a FOLDER NAME
   * and came back 404, so a view that exists and is merely empty greeted the
   * user with "Folder not found — this folder does not exist, was moved, or
   * you do not have access to it" (measured on `#.trash` and `#.starred`,
   * v0.30.1). The trash is not missing; it is empty, and it has a state that
   * says so.
   *
   * ⚠ Through `virtualViewOf` → the ONE map in lib/listing.ts, never a second
   * list of names here: two copies of that mapping are what printed `.shared`
   * in the tab strip two days ago, and the tag view adds a dynamic third kind.
   *
   * ⚠ Only a sentinel this build KNOWS is intercepted. Anything else keeps
   * going — a user may genuinely own a folder called `.config`, and with
   * hidden files shown they can open it.
   *
   * ⚠ And only when the view is actually REACHABLE here. Under `rootPath` the
   * panel is off on purpose (the views span storages and would list files
   * outside the folder the embed was confined to), so a stale hash from
   * another deployment must not smuggle them in: it falls back to the root —
   * which the floor clamp below then turns into the confined folder.
   *
   * ⚠ No recursion: neither loader calls load(), and the fallback passes '',
   * which is not a sentinel.
   */
  const asView = virtualViewOf(path ?? currentPath.value ?? '');
  if (asView) {
    const reachable =
      asView.kind === 'trash' ? props.config.trashVisible !== false : sideNavEnabled.value;
    if (!reachable) {
      // Clear it explicitly: if the fallback lands on the path we are already
      // on, watch(currentPath) never fires and the dead hash would survive to
      // the next reload (the same trap leaveNotFound documents).
      writePersistedPath('');
      return await load('');
    }
    if (asView.kind === 'tag') await loadTagView(asView.tag);
    else await loadNavView(asView.kind);
    return;
  }
  loading.value = true;
  // Any normal navigation exits trash mode (the trash view is entered only
  // by opening the virtual `.trash` row, which calls loadTrash()).
  trashMode.value = false;
  /* gezinti:g1 — and every other virtual view, for the same reason: without
     this the mode sticks and the breadcrumb keeps saying "Starred" over a
     folder listing. */
  navView.value = '';
  navTag.value = '';
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
      // One visible storage → skip the one-row list and open it. Recursion is
      // bounded: the recursive call carries a non-empty path, so
      // virtualToWire() resolves and this branch is not re-entered.
      if (soleStorageName.value) return await load(soleStorageName.value);
      currentPath.value = '';
      adapter.value = '';
      dirname.value = '';
      e2eRoot.value = ''; /* wiring:e2 — no lock screen at the virtual root */
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
    files.value = filterListing(resp.files);
    // Inject virtual `.trash` entry at root only — shared helper so the
    // split-view secondary pane shows the exact same row (no row-offset).
    // ⚠ …and NOT into a search result. The search response's `dirname` is the
    // scope it searched, so it is the storage root exactly as an `index` of
    // that folder would be; only this call site knows which of the two it just
    // asked for.
    if (
      injectTrashRow(files.value, resp.adapter, resp.dirname, props.config.trashVisible !== false, {
        isSearchResult: !!searchQuery.value,
        navOffersTrash: navOffersTrash.value,
      })
    ) {
      void hydrateTrashRowShared(files.value, resp.adapter, api);
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
      e2eRoot.value = ''; /* wiring:e2 — no lock screen left over on a dead link */
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

// (hydrateTrashRow moved to lib/listing.ts — shared with SecondaryPane.)

// loadTrash — show the backend trash (soft-deleted nodes) as a flat listing.
// Entered by opening the virtual `.trash` row. Each row keeps its node `id`
// so restore can target it. Permanent delete is admin-only / auto-purge, so
// the only mutation offered here is Restore.
async function loadTrash() {
  loading.value = true;
  trashOrigin.value = adapter.value || '';
  trashMode.value = true;
  e2eRoot.value = ''; /* wiring:e2 — the trash view is outside the encrypted context */
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

/* ui-fix — do two wire paths point at the SAME directory (safe against a
 * trailing slash and against the bare `adapter://` root)? Dropping an item
 * into the folder it is ALREADY in (source parent === target) must be a
 * no-op; otherwise the backend answers with a "copy onto itself" 400. */
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
  /* gezinti:g1 — which storages are grant-only, so the panel can mark them
     before anybody opens the shared view. */
  void loadSharedStorages();
  /* etiket:t1 — the panel's tag list. AFTER the first listing has been
     awaited above, never racing it: `tags/all` is a distinct-scan and the
     folder the user asked for is the only thing on the critical path. The
     module-level cache in lib/tags.ts means several explorers on one page
     still cost a single query. */
  void loadNavTags();
  /* surucu:d1 — the storage line. After the listing, like the tag list: it is
     a status, and the folder somebody asked for is the critical path. */
  void loadQuota();
  /* ⚠ The panel is not always on screen at mount. Below 560px it is a DRAWER
     that starts closed, so `navVisible` is false and the call above returns
     without asking for anything — measured at 390px: the drawer opened with
     no Tags section at all. Ask again the first time the panel appears.
     ⚠ Registered HERE and not beside loadNavTags: `watch` evaluates its
     source immediately, `navVisible` reads `isNarrow`, and `isNarrow` is
     declared further down the file — so a watcher created at setup time threw
     "Cannot access 'isNarrow' before initialization" and took the whole
     explorer down with it (measured: blank pane, two TDZ errors in the
     console). In onMounted every ref exists. */
  watch(navVisible, (visible) => {
    if (visible && !navTagsLoaded.value) void loadNavTags();
  });
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
    /* ui-fix — the shortcut goes to the active pane too (consistent with the menu). */
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
    /* gezinti:g1 — an open Connections / API-keys overlay is the topmost thing
       on screen, so Esc dismisses that before anything under it. */
    if (anyOverlayOpen.value) {
      closeOverlays();
      return;
    }
    /* gezinti:g1 — Esc closes the navigation drawer first. It is the topmost
       thing on a narrow screen, so dismissing something underneath it while it
       covers the listing reads as Esc doing nothing. */
    if (navDrawerOpen.value) {
      closeNavDrawer();
      return;
    }
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
    /* surucu:d1 — the shortcut from anywhere else opens a BLANK palette. The
       seed belongs to the drive header's field; leaving the last one in place
       would reopen the palette pre-filled with a query the user has since
       moved on from. */
    paletteSeed.value = '';
    showPalette.value = true;
  },
  onShowHelp: () => {
    showShortcutsHelp.value = true;
  },
  /* /cila:c wiring */
  onQuickLook: () => quickLookToggle() /* wiring:c2 */,
  onToggleHidden: () => toggleHiddenFiles(),
  onStar: () => void toggleStar(selection.nodes.value) /* yildiz:s1 */,
  onToggleInspector: () => toggleInspector() /* koru:k1 */,
  /* wiring:d1 — tab actions (registry: tab-new/close/next/prev) */
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
  /* wiring:e2 — opening a file inside an encrypted folder: while unlocked,
     decrypt + show a read-only preview (the blob URL feeds the existing
     viewers); while locked nothing opens at all (the lock screen already
     hides the listing). */
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
      flashToast(t('toast.restored', { n: restored }));
      selection.clear();
      await loadTrash();
      return;
    }
    // Legacy path-based restore (brf-mono `.trash/` convention).
    if (!api.endpoints.restore) return;
    const items = nodes.map((n) => n.path); // qualified
    const { restored } = await api.restore(items);
    flashToast(t('toast.restored', { n: restored }));
    selection.clear();
    await load();
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'restore' } });
  }
}

function previewNode(n: FileNode) {
  /* wiring:e2 — the preview is fed from the decrypted blob as well */
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
  /* wiring:e2 — the standalone editor route pulls the RAW (encrypted) bytes
     from the server, so inside an encrypted folder every "Aç" falls back to
     the in-page decrypted preview. */
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
  const abs = absolutePageUrl(base);
  const sep = abs.includes('?') ? '&' : '?';
  const url =
    `${abs}${sep}path=${encodeURIComponent(n.path)}` +
    `&type=${encodeURIComponent(ext)}` +
    `&mode=edit`;
  window.open(url, '_blank', 'noopener');
  emit('file-opened', { path: n.path, basename: n.basename });
  void markRecent(n);
}

type ContextMode = 'selection' | 'breadcrumb' | 'pane' /* ui-fix — side-pane right-click */;
const ctxMode = ref<ContextMode>('selection');
const breadcrumbCtxPath = ref<string>('');
/* ui-fix — target nodes of the side pane's right-click menu (pane selection). */
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

// ─── desktop selective sync — "keep on this computer" ──────────────────
// Present only when the desktop shell passes config.desktopSync; the web
// admin and the embeds never see these entries. State is PULLED, not pushed:
// the kept list is re-read as a menu opens, so the component needs no event
// channel back to the shell and cannot go stale in a way that outlives one
// right-click.
const desktopSync = computed(() => props.config.desktopSync ?? null);
const keptPairs = ref<Array<{ remote: string; local: string }>>([]);

async function refreshKept(): Promise<void> {
  if (!desktopSync.value) return;
  try {
    keptPairs.value = await desktopSync.value.kept();
  } catch {
    // Shell went away mid-call; a stale entry only mislabels a menu item.
  }
}

/** The folder the engine is working on RIGHT NOW (null between runs). Drives
 *  the ⟳ row badges and the bottom progress strip. */
const keepActive = ref<{
  remote: string;
  phase: 'inventory' | 'plan' | 'transfer' | 'settling';
  done: number;
  total: number;
} | null>(null);

async function refreshKeepStatus(): Promise<void> {
  const ds = desktopSync.value;
  if (!ds?.status) return;
  try {
    keepActive.value = (await ds.status()).active ?? null;
  } catch {
    keepActive.value = null;
  }
}

// The shell pokes on every engine output line — during a transfer that can be
// several a second, and each refresh is an IPC round-trip. Trailing-edge
// throttle: at most one refresh per 300ms, and the FINAL poke always lands,
// so the strip cannot get stuck showing a finished transfer.
let keepPokeTimer: ReturnType<typeof setTimeout> | null = null;
// The shell holds the callback and only drops it on the NEXT mount, so a poke
// can arrive after this instance is gone — pokes then set refs nothing reads.
// Cheap to make explicit rather than rely on that being harmless.
let keepAlive = true;
function onKeepPoke(): void {
  if (keepPokeTimer || !keepAlive) return;
  keepPokeTimer = setTimeout(() => {
    keepPokeTimer = null;
    if (!keepAlive) return;
    void refreshKeepStatus();
    void refreshKept();
  }, 300);
}

onMounted(() => {
  void refreshKept();
  void refreshKeepStatus();
  desktopSync.value?.onChange?.(onKeepPoke);
});
onBeforeUnmount(() => {
  keepAlive = false;
  if (keepPokeTimer) {
    clearTimeout(keepPokeTimer);
    keepPokeTimer = null;
  }
});

/** Adapter-qualified remote for a row. Virtual storage rows carry a bare
 *  name (`docs`), real rows a wire path (`docs://reports`). */
function keepRemoteOf(node: FileNode): string {
  const p = String(node.path ?? '');
  return p.includes('://') ? p.replace(/\/+$/, '') : `${p}://`;
}

type KeepState = 'none' | 'kept' | 'inherited' | 'partial';

/** True when `child` lives strictly inside `parent` (both wire-form). */
function remoteInside(child: string, parent: string): boolean {
  if (parent.endsWith('://')) return child.startsWith(parent) && child !== parent;
  return child === parent ? false : child.startsWith(parent + '/');
}

/** How `remote` relates to the kept set: exactly a pair, inside one
 *  (inherited), an ancestor of some (partial), or unrelated. */
function keepStateOf(remote: string): KeepState {
  if (keptPairs.value.some((p) => p.remote === remote)) return 'kept';
  if (keptPairs.value.some((p) => remoteInside(remote, p.remote))) return 'inherited';
  if (keptPairs.value.some((p) => remoteInside(p.remote, remote))) return 'partial';
  return 'none';
}

type KeepBadge = 'kept' | 'syncing' | 'cloud' | 'partial';

/**
 * The availability badge for one row: on this computer, being synced right
 * now, holding kept items somewhere below (partial), or online-only. Every
 * row gets one — that is the OneDrive/Drive grammar people already read —
 * except the rows where it would be a lie or noise: trash, and the `.trash`
 * row itself. `partial` is what saves the user from drilling into every
 * folder to find out whether anything inside is on this computer.
 */
function keepBadgeFor(n: FileNode): KeepBadge | null {
  if (!desktopSync.value || trashActive.value) return null;
  if (n.basename === '.trash') return null;
  const r = keepRemoteOf(n);
  const act = keepActive.value;
  if (act && (r === act.remote || remoteInside(r, act.remote) || remoteInside(act.remote, r))) {
    return 'syncing';
  }
  const st = keepStateOf(r);
  if (st === 'kept' || st === 'inherited') return 'kept';
  if (st === 'partial') return 'partial';
  return 'cloud';
}

/** Bottom strip: what to say while the engine works. */
const keepStripLabel = computed<string>(() => {
  const act = keepActive.value;
  if (!act) return '';
  const name = act.remote.endsWith('://')
    ? act.remote.slice(0, -'://'.length)
    : act.remote.slice(act.remote.lastIndexOf('/') + 1);
  if (act.phase === 'transfer' && act.total > 0) {
    return t('keep.strip_transfer', {
      name,
      done: String(act.done),
      total: String(act.total),
      pct: String(Math.min(100, Math.round((act.done * 100) / act.total))),
    });
  }
  if (act.phase === 'settling') return t('keep.strip_settling', { name });
  return t('keep.strip_inventory', { name });
});

const keepStripPercent = computed<number | null>(() => {
  const act = keepActive.value;
  if (!act || act.phase !== 'transfer' || act.total <= 0) return null;
  return Math.min(100, Math.round((act.done * 100) / act.total));
});

/** Menu entries for one selected folder OR file, by its keep state. Empty
 *  for multi-selections, trash, encrypted folders, or a web mount. */
function keepActionsFor(sel: FileNode[]): ContextAction[] {
  const ds = desktopSync.value;
  const single = sel.length === 1 && (sel[0]?.type === 'dir' || sel[0]?.type === 'file');
  if (!ds || !single || trashActive.value || e2eActive.value || sel[0]?.e2e === true) return [];
  const st = keepStateOf(keepRemoteOf(sel[0]!));
  return [
    { divider: true, key: 'sep-keep', label: '' },
    { key: 'keep-local', label: t('ctx.keep_local'), icon: '📌', hidden: st === 'kept' || st === 'inherited' },
    { key: 'keep-online', label: t('ctx.keep_online'), icon: '☁', hidden: st !== 'kept' },
    { key: 'keep-inherited', label: t('ctx.keep_inherited'), icon: '📌', disabled: true, hidden: st !== 'inherited' },
    { key: 'keep-reveal', label: t('ctx.keep_reveal'), icon: '📂', hidden: st !== 'kept' && st !== 'inherited' },
  ];
}

async function onContextTarget(node: FileNode, ev: MouseEvent) {
  ctxMode.value = 'selection';
  void refreshKept(); // menu labels react if the kept set changed since last look
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

/* ui-fix — right-click in the side (secondary) pane: activate the pane, then
 * open the menu. The menu is EXACTLY the main pane's (selectionActionList is
 * the single source); actions go to dispatchItemAction and are pane-routed
 * while ctxMode==='pane'. */
function onPaneContext(node: FileNode | null, ev: MouseEvent) {
  activePane.value = 'split';
  void refreshKept();
  const sel = splitPaneRef.value?.selectedNodes() ?? [];
  // node=null (right-click on empty space): the selection-less menu
  // ("Yeni Klasör" + "Yapıştır"). Otherwise the pane selection is the target
  // (falling back to the clicked node).
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
  if (ctxMode.value === 'pane' /* ui-fix — side-pane menu is EXACTLY the main pane's */) {
    const psel = paneCtxTargets.value;
    if (psel.length === 0) {
      // Right-click on empty space: same as the main pane's canvas menu.
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
  // the menu at the storage listing — including new-folder + paste,
  // which Ada called out in the most direct possible terms. Use
  // the same empty-after-trim test as `atVirtualRoot` above.
  const trimmedPath = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  const inStorageRoot = multiStorageRoot.value && trimmedPath === '';
  if (inStorageRoot) {
    if (!any) return [];
    if (!single) return [];
    return [
      { key: 'open', label: t('ctx.open'), icon: '↗' },
      { key: 'open-tab', label: t('ctx.open_new_tab'), icon: '⧉' } /* wiring:d1 */,
      // A whole storage can be kept too — that IS the "sync everything"
      // shape, and it is one pair, not one per subfolder.
      ...keepActionsFor(sel),
    ];
  }

  // Empty background right-click: folder-level actions only. Viewers (no edit
  // on this dir) get nothing here.
  if (!any) {
    // Showing hidden files is a VIEW preference, so it stays available to
    // viewers too — the edit gate below only covers the mutating actions.
    const view: ContextAction[] = [
      {
        key: 'toggle-hidden',
        label: showHiddenFiles.value ? t('ctx.hide_hidden') : t('ctx.show_hidden'),
        icon: showHiddenFiles.value ? '🙈' : '👁',
      },
    ];
    if (!permCanEdit(dirPerm.value)) return view;
    return [
      { key: 'new-folder', label: t('toolbar.new_folder'), icon: '📁' },
      { key: 'paste', label: t('ctx.paste'), icon: '📋', disabled: !clipboard.value.mode },
      ...view,
    ];
  }

  return selectionActionList(sel);
});

// selectionActionList — the SINGLE source of truth for the actions offered on a
// selection. BOTH the right-click context menu AND the top toolbar render this
// exact list so they can never drift apart (Ada, translated from Turkish: "the
// right-click menu and the top menu don't match"). The toolbar filters out
// dividers/hidden; the context menu shows them. Action handling is unified in
// dispatchItemAction().
function selectionActionList(sel: FileNode[]): ContextAction[] {
  const any = sel.length > 0;
  const single = sel.length === 1;
  const isFile = single && sel[0]?.type === 'file';
  const tagsLabel = locale.value === 'en' ? 'Tags…' : 'Etiketler…';
  const singleHasId = single && typeof sel[0]?.id === 'number';
  /* yildiz:s1 */
  const canStar = starableNodes(sel).length > 0;
  const allStarred = selectionAllStarred(sel);
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
    { key: 'open-tab', label: t('ctx.open_new_tab'), icon: '⧉', hidden: !single || sel[0]?.type !== 'dir' } /* wiring:d1 — open the folder in a new tab */,
    { key: 'preview', label: t('ctx.preview'), icon: '👁', hidden: !single, disabled: !isFile },
    { key: 'download', label: t('ctx.download'), icon: '⬇', hidden: !single, disabled: !isFile },
    { key: 'convert', label: t('ctx.convert'), icon: '🔄', hidden: !single || !effectiveConvertUrl.value || !w || e2eActive.value /* wiring:e2 — convert is meaningless on ciphertext */, disabled: !isFile },
    { key: 'access', label: accessLabel, icon: '🔗', hidden: !single || !w || e2eActive.value /* wiring:e2 — sharing is off in the MVP (the link would serve ciphertext) */ },
    { key: 'details', label: t('ctx.details'), icon: 'ℹ', hidden: !any } /* koru:k1 */,
    { key: 'copy-id', label: copyIdLabel, icon: '🆔', hidden: !singleHasId, disabled: !singleHasId },
    { divider: true, key: 'sep1', label: '', hidden: !w },
    { key: 'rename', label: t('ctx.rename'), icon: '✎', hidden: !single || !w, disabled: !single },
    { key: 'cut', label: t('ctx.cut'), icon: '✂', hidden: !any || !w, disabled: !any },
    { key: 'copy', label: t('ctx.copy'), icon: '❐', hidden: !any, disabled: !any },
    { key: 'paste', label: t('ctx.paste'), icon: '📋', hidden: !w, disabled: !clipboard.value.mode },
    { divider: true, key: 'sep-meta', label: '', hidden: !singleHasId && !canStar },
    /* yildiz:s1 — "star must be an action, like a tag" (owner, v0.30.0).
       Beside Tags on purpose: they are the same kind of verb, and this is the
       ONLY star a grid/gallery user reaches with the keyboard. Works on a
       multi-selection; the label follows the selection's state. */
    {
      key: 'star',
      label: allStarred ? t('ctx.unstar') : t('ctx.star'),
      icon: allStarred ? '★' : '☆',
      hidden: !canStar,
    },
    { key: 'tags', label: tagsLabel, icon: '🏷', hidden: !singleHasId, disabled: !singleHasId },
    ...keepActionsFor(sel),
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
  if (ctxMode.value === 'pane' /* ui-fix — side pane: same dispatch, pane-routed */) {
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
    case 'keep-local': {
      const ds = desktopSync.value;
      if (!ds || !targets[0]) break;
      const remote = keepRemoteOf(targets[0]);
      try {
        await ds.keep(remote, targets[0].type === 'file' ? 'file' : 'dir');
        await refreshKept();
        // The shell may have shown its root-folder prompt and been cancelled —
        // only claim success when the pair is really there now.
        if (keepStateOf(remote) !== 'none') flashToast(t('keep.started'));
      } catch (e) {
        await refreshKept();
        flashToast(`${t('keep.failed')}: ${String((e as Error)?.message ?? e)}`);
      }
      break;
    }
    case 'keep-online': {
      const ds = desktopSync.value;
      if (!ds || !targets[0]) break;
      try {
        await ds.unkeep(keepRemoteOf(targets[0]));
      } catch {
        // The shell owns the confirm dialog and reports its own failures.
      }
      await refreshKept();
      break;
    }
    case 'keep-reveal': {
      const ds = desktopSync.value;
      if (ds && targets[0]) void ds.reveal(keepRemoteOf(targets[0]));
      break;
    }
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
    case 'star':
      await toggleStar(targets);
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
      /* ui-fix — in a pane context the clipboard source must be the pane's dir. */
      if (ctxMode.value === 'pane') paneCut();
      else {
        clipboard.value = { mode: 'cut', items: targets, sourcePath: currentPath.value };
        flashToast(t('toast.cut_ready'));
      }
      break;
    case 'copy':
      if (ctxMode.value === 'pane') paneCopy();
      else {
        clipboard.value = { mode: 'copy', items: targets, sourcePath: currentPath.value };
        flashToast(t('toast.copy_ready'));
      }
      break;
    case 'paste':
      /* ui-fix — pasting from the right-click menu goes to the active pane too
       * (the keyboard shortcut was already pane-routed; the menu was not). */
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
    case 'toggle-hidden':
      toggleHiddenFiles();
      break;
    case 'new-folder':
      mutationInPane.value = ctxMode.value === 'pane'; /* ui-fix */
      showNewFolder.value = true;
      break;
    case 'duplicate':
      if (targets[0]) await duplicate(targets[0]);
      break;
    /* wiring:d1 — right-click "Yeni sekmede aç" (open in new tab) */
    case 'open-tab':
      if (targets[0]) openNodeInTab(targets[0]);
      break;
    /* /wiring:d1 */
  }
}

function cut() {
  if (selection.isEmpty.value) return;
  clipboard.value = { mode: 'cut', items: selection.nodes.value, sourcePath: currentPath.value };
  flashToast(t('toast.cut'));
}

function copyToClipboard() {
  if (selection.isEmpty.value) return;
  clipboard.value = { mode: 'copy', items: selection.nodes.value, sourcePath: currentPath.value };
  flashToast(t('toast.copied'));
}

async function paste() {
  const cb = clipboard.value;
  if (!cb.mode || cb.items.length === 0) return;
  try {
    const items = cb.items.map((n) => n.path); // already qualified (adapter://rel)
    const sourceDir = cb.sourcePath || '';
    const sameDir = cb.mode === 'cut' && sourceDir === currentPath.value;
    if (sameDir) {
      flashToast(t('toast.same_folder_cut'));
      return;
    }

    const targetWire = qualify(currentPath.value);
    // A different storage only changes the message: cut still CUTS
    // and copy still COPIES, even when the target lives on another storage.
    // The server does the transfer (the ops queue carries both the source
    // and the target storage).
    const plan = resolveTransfer(items, targetWire, cb.mode === 'cut' ? 'move' : 'copy');
    if (cb.mode === 'cut') {
      const originWire = qualify(sourceDir) || undefined;
      const { op } = await api.moveAsync(items, targetWire, originWire);
      registerMoveUndo(op.id, items, targetWire, originWire);
      pendingOps.register(op);
      flashToast(plan.cross ? t('split.cross_move') : t('split.move_queued'));
    } else {
      const { op } = await api.copy(items, targetWire);
      pendingOps.register(op);
      flashToast(plan.cross ? t('split.cross_copy') : t('split.copy_queued'));
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
  /* wiring:e2 — downloading inside an encrypted folder: fetch the bytes,
     decrypt them, save under the original name (handing the user raw
     ciphertext would be pointless). */
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
  const inPane = mutationInPane.value; /* ui-fix — new folder in the side pane */
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
  const inPane = mutationInPane.value; /* ui-fix — rename from the side pane */
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
    flashToast(t('toast.trash_retention'));
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
      flashToast(t('toast.delete_queued'));
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
  /* wiring:e2 — uploads into an encrypted folder are encrypted transparently.
     No upload while locked (that would be a plaintext-leak door); anything
     over 200MB hits the MVP single-shot limit and is skipped with a warning. */
  if (e2eLocked.value) {
    flashToast(t('e2e.upload.locked'));
    return;
  }
  if (e2eUnlocked.value) {
    list = await e2eEncryptUploads(list);
    if (list.length === 0) return;
  }
  /* /wiring:e2 */
  for (const f of list) {
    // Anything above the chunk size goes on the STAGED path: chunked into
    // filex's own staging area, resumable across a dropped connection and — via
    // the bookmark in lib/uploadResume — across a reloaded tab. It works on
    // every driver, unlike the S3-presigned path it replaced. Small files keep
    // the single-POST fast path, and a server that has no staged path at all
    // falls back to it too.
    if (chunked.shouldChunk(f)) {
      const pending = chunked.resumableFor(qualify(currentPath.value), f);
      if (pending) {
        // Say so. An upload that silently starts over looks identical to one
        // that never happened, which is precisely the complaint.
        flashToast(
          t('upload.resuming', {
            name: f.name,
            percent: f.size > 0 ? Math.round((pending.offset / f.size) * 100) : 0,
          }),
        );
      }
      if (await chunkedUpload(f)) continue;
    }
    await legacyUpload(f);
  }
  await load();
}

async function legacyUpload(file: File, dest?: string) {
  // Register a progress row so the corner badge tracks the upload — large files
  // fall back here from the chunked path, and previously showed no progress at
  // all (the chunked placeholder was removed on init failure and the legacy
  // POST tracked nothing, so the badge vanished mid-upload).
  const id = crypto.randomUUID();
  const target = dest ?? qualify(currentPath.value);
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
    // Tell the USER, not just the embedding app. `emit('error')` alone left a
    // standalone deployment silent: the progress bar ran to 100% (the bytes
    // do go out — the server rejects them afterwards), the row flipped to an
    // error state carrying no message, and nothing else appeared. olivov lost
    // ten days of uploads to that silence (H2, 2026-08-05).
    const message = (err as Error).message;
    patch({ status: 'error', error: message });
    flashToast(t('upload.failed', { name: file.name }));
    emit('error', {
      message,
      context: { op: 'upload', file: file.name },
    });
  }
}

/**
 * Attempt a staged (chunked, resumable) upload. Returns `true` when it was
 * handled — including when it failed — and `false` ONLY when this server has no
 * staged path at all, so the caller may fall back to the single-POST upload.
 *
 * ⚠ The old version fell back on ANY error, which was harmless while the
 * chunked path was S3-only and failed at init. It is not harmless now: a staged
 * upload that dies at 90 % has bytes on the server and a bookmark to resume
 * from, and quietly re-POSTing the whole file would throw both away — the
 * "starts from zero" behaviour this change exists to remove. A real failure is
 * shown to the user instead, and picking the same file again continues it.
 */
async function chunkedUpload(file: File, dest?: string): Promise<boolean> {
  // Register the progress row LAZILY — only once `begin` succeeded and bytes are
  // actually moving. A server with no staged path then shows no badge at all,
  // so the fallback's own badge is the only one the user sees (no
  // appear-then-vanish flicker).
  const id = crypto.randomUUID();
  let registered = false;
  const patch = (job: UploadJob) => {
    if (!registered) {
      uploadJobs.value = [...uploadJobs.value, { ...job, id } as UploadJob];
      registered = true;
      return;
    }
    const idx = uploadJobs.value.findIndex((j) => j.id === id);
    if (idx !== -1) {
      const next = [...uploadJobs.value];
      next[idx] = { ...job, id } as UploadJob;
      uploadJobs.value = next;
    }
  };
  const target = dest ?? qualify(currentPath.value);
  try {
    await chunked.uploadFile({
      path: target,
      file,
      onProgress: (job) => {
        if (!registered && job.status !== 'uploading' && job.uploadedBytes <= 0) return;
        patch(job);
        emit('upload-progress', {
          uploadId: job.uploadId ?? id,
          percent: job.percent,
          done: job.status === 'done',
        });
      },
    });
    return true;
  } catch (err) {
    if (isStagedUnsupported(err)) {
      if (registered) uploadJobs.value = uploadJobs.value.filter((j) => j.id !== id);
      return false;
    }
    const message = (err as Error).message;
    if (!registered) {
      uploadJobs.value = [
        ...uploadJobs.value,
        {
          id,
          file,
          path: target,
          totalBytes: file.size,
          uploadedBytes: 0,
          percent: 0,
          status: 'error',
          error: message,
          cancel() {},
        } as UploadJob,
      ];
      registered = true;
    }
    flashToast(t('upload.failed', { name: file.name }));
    emit('error', { message, context: { op: 'upload', file: file.name } });
    return true;
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
  /* wiring:f1 — an OS drag we started ourselves also carries 'Files', but it
     is NOT an upload: it would mean downloading the same bytes from the
     server and posting them straight back (a copy instead of a move, at
     twice the traffic). */
  if (activeNativeDrag()) return false;
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
  /* wiring:d1 — internal drags must be droppable on the root body too, so that
     dropping from the split pane onto the main pane's EMPTY SPACE works (the
     origin can't be read during dragover — the decision is made at drop time;
     a same-folder drop stays a no-op). */
  if (hasInternalDrag(ev)) {
    ev.preventDefault();
    return;
  }
  /* /wiring:d1 */
  if (isExternalFileDrag(ev)) {
    ev.preventDefault();
  }
}
function onDropUpload(ev: DragEvent) {
  /* wiring:d1 — dropping from the split pane onto the main pane's empty space
     = transfer into the current folder (items from the same folder stay a
     no-op, so the old behaviour is preserved). */
  if (hasInternalDrag(ev)) {
    const d1Origin = internalDragOrigin(ev) || '';
    const d1Here = qualify(currentPath.value);
    const d1Items = internalDragItems(ev);
    if (d1Items && d1Origin && d1Here && d1Origin !== d1Here && !trashMode.value && canWriteHere.value) {
      ev.preventDefault();
      dragCounter.value = 0;
      dragOver.value = false;
      endNativeDrag();
      cancelShellDrag();
      void transferItems(d1Items.map((i) => i.path), d1Here, d1Origin);
      return;
    }
  }
  /* /wiring:d1 */
  // Internal row drag — nothing to do here, the row drop handler
  // in GridView/ListView already resolved the move.
  if (hasInternalDrag(ev)) {
    dragCounter.value = 0;
    dragOver.value = false;
    endNativeDrag();
    cancelShellDrag();
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
  window.addEventListener('pointerup', onGlobalPointerUp);
  window.addEventListener('blur', onGlobalPointerUp);
  /* wiring:f1 — while the shell prepares, say "preparing" exactly once; a
     toast per file would show noise rather than progress. The finish is
     announced by the 'ready' toast (prepareDragOut). */
  dragOut.value?.onProgress?.((p) => {
    // Work that happens AFTER the drop (the placeholder path) is always
    // announced — the user dropped a file into a folder and has a right to
    // know what happened there. Silence only applies to the pre-preparation
    // nobody asked for.
    const afterDrop = !!p?.dropped;
    if (p?.error === 'drop_not_found') {
      flashToast(t('dragout.not_found'));
      return;
    }
    if (p?.error) {
      if (afterDrop || !dragOutQuiet) flashToast(p.error);
      return;
    }
    if (afterDrop) {
      flashToast(p?.finished ? t('dragout.done') : t('dragout.downloading'));
      return;
    }
    if (dragOutQuiet) return;
    if (!p?.finished && p?.done === 0) flashToast(t('dragout.preparing'));
  });
});
/* wiring:f1 — an OS drag never fires 'dragend' for us (the HTML5 drag never
   started). We drop the record when the mouse is released; otherwise the next
   ordinary drag would think it was carrying the previous selection. */
function onGlobalPointerUp() {
  if (activeNativeDrag()) {
    endNativeDrag();
    // The drop MAY NOT have landed in our own window; only our own drop paths
    // cancel the shell's watch (onDropUpload / onItemDropInto).
  }
}

/** The drag ended inside the app: the shell should stop waiting for a drop. */
function cancelShellDrag() {
  if (dragOut.value?.cancel) void Promise.resolve(dragOut.value.cancel()).catch(() => undefined);
}

onBeforeUnmount(() => {
  window.removeEventListener('dragover', onWindowDragOver);
  window.removeEventListener('drop', onWindowDrop);
  window.removeEventListener('hashchange', onHashChange);
  window.removeEventListener('pointerup', onGlobalPointerUp);
  window.removeEventListener('blur', onGlobalPointerUp);
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

  /* wiring:f1 — drag-out (to the desktop / another application).
     When a shell is present the drag is ALWAYS an OS drag: folders and
     multi-selections land as REAL files, one by one, with NO SIZE LIMIT.
     If the bytes are ready, real files are handed over; if not, the shell
     hands over an empty "placeholder", finds out where it was dropped and
     downloads THERE (see desktop/src/dropwatch.ts). If the drop lands INSIDE
     the app the drag is still a server-side move — the payload stays with us —
     and the shell is told to "give up" so it doesn't watch the drives for
     nothing. */
  if (dragOut.value && items.length > 0) {
    ev.preventDefault();
    beginNativeDrag(items, qualify(currentPath.value));
    void Promise.resolve(dragOut.value.start(items)).catch((err) => {
      endNativeDrag();
      cancelShellDrag();
      emit('error', { message: (err as Error).message, context: { op: 'drag-out' } });
    });
    return;
  }

  ev.dataTransfer.setData(FE_DND_MIME, JSON.stringify(items));
  ev.dataTransfer.setData(FE_DND_SRC_MIME, qualify(currentPath.value)); /* wiring:d1 — cross-pane origin stamp */
  ev.dataTransfer.setData('text/plain', items.map((i) => i.path).join('\n'));
  ev.dataTransfer.effectAllowed = 'move';

  /* Single file + a cookie session: the browser's own download path
     (DownloadURL) fetches the file onto the desktop at drop time; no
     preparation is needed at all. In a bearer-token setup (the desktop app)
     that path goes out unauthenticated, which is why the local path above
     takes over there — see lib/dragOut.ts. */
  if (items.length === 1 && items[0] && canDownloadUrlDrag(props.config.auth)) {
    const payload = downloadUrlPayload(items[0], api.downloadUrl(items[0].path), node.mime_type);
    if (payload) ev.dataTransfer.setData('DownloadURL', payload);
  }

  if (dragOut.value && items.length > 0) void prepareDragOut(items);
}

/* === wiring:f1 — drag-out preparation ===
 *
 * The bytes have to be on disk BEFORE the drag starts (the OS copies from the
 * path at drop time), so preparation is a separate step. When it finishes the
 * user is told "ready"; the second drag is now an OS drag and starts
 * instantly. For files kept on this computer (synced) preparation finishes
 * instantly on the first go too — the copy is already local.
 */
const dragOut = computed(() => props.config.dragOut ?? null);
const dragOutReadyKey = ref('');
const dragOutBusy = ref(false);
/** True while a preparation nobody asked for is running. */
let dragOutQuiet = false;

/* Small selections are prepared the moment they are SELECTED, because once
   preparation is done the next drag is an OS drag: picking a document and
   dragging it to the desktop therefore works on the FIRST try. The ceiling is
   deliberately low — someone click-browsing shouldn't download a movie on
   every row; anything above the limit is prepared on the first drag and the
   second drag starts instantly. */
const DRAGOUT_PREFETCH_MAX_BYTES = 8 * 1024 * 1024;
const DRAGOUT_PREFETCH_MAX_ITEMS = 10;
let dragOutPrefetchTimer: ReturnType<typeof setTimeout> | undefined;

watch(
  () => selection.selected.value,
  () => {
    if (!dragOut.value) return;
    clearTimeout(dragOutPrefetchTimer);
    const nodes = selection.nodes.value;
    if (nodes.length === 0 || nodes.length > DRAGOUT_PREFETCH_MAX_ITEMS) return;
    // A folder's size isn't known from the listing; rather than guess it, we
    // leave it to the first drag.
    if (nodes.some((n) => n.type !== 'file')) return;
    const total = nodes.reduce((sum, n) => sum + (n.size ?? 0), 0);
    if (total > DRAGOUT_PREFETCH_MAX_BYTES) return;
    const items = nodes.map((n) => ({ path: n.path, basename: n.basename, type: n.type }));
    dragOutPrefetchTimer = setTimeout(() => void prepareDragOut(items, true), 400);
  },
  { deep: true },
);

async function prepareDragOut(items: DragItem[], quiet = false): Promise<void> {
  const hook = dragOut.value;
  if (!hook || dragOutBusy.value) return;
  const key = dragKey(items);
  if (key === dragOutReadyKey.value) return;
  dragOutBusy.value = true;
  dragOutQuiet = quiet;
  try {
    const res = await hook.prepare(items);
    if (res?.ready) {
      dragOutReadyKey.value = key;
      // The quiet round is triggered by a selection; the user ASKED for
      // nothing, so there's nothing to tell them either. A round that starts
      // from an actual drag does speak up.
      if (!quiet) flashToast(t('dragout.ready'));
    } else if (res?.error && !quiet) {
      flashToast(res.error);
    }
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'drag-out-prepare' } });
  } finally {
    dragOutBusy.value = false;
    dragOutQuiet = false;
  }
}

async function moveSourcesAsync(sources: string[], targetDir: string, opLabel: string, originOverride?: string): Promise<void> {
  try {
    const originWire = originOverride ?? qualify(currentPath.value); /* wiring:d1 — the real source folder for a drag coming from the split pane */
    if (api.endpoints.moveAsync) {
      const { op } = await api.moveAsync(sources, targetDir, originWire);
      registerMoveUndo(op.id, sources, targetDir, originWire);
      pendingOps.register(op);
      flashToast(t('split.move_queued'));
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
  const items = internalDragItems(ev);
  if (!items || items.length === 0) return;
  endNativeDrag();
  cancelShellDrag();

  const targetDir = target.path; // qualified
  const sources = items
    .map((i) => i.path)
    // ui-fix — skip items already inside targetDir (parent===target): an
    // in-place drop is a no-op and must not trigger the backend's "copy onto
    // itself" 400.
    .filter((p) => p && p !== targetDir && !targetDir.startsWith(p + '/') && !sameDir(wireParent(p), targetDir));
  if (sources.length === 0) return; // silent no-op (in-place drop)
  await transferItems(sources, targetDir, dndOrigin(ev)); /* wiring:d1 — storage-aware transfer */
}

async function onCrumbDropInto(adapterPath: string, ev: DragEvent) {
  const items = internalDragItems(ev);
  if (!items || items.length === 0) return;
  endNativeDrag();
  cancelShellDrag();

  const targetDir = adapterPath; // already qualified by breadcrumb
  const sources = items
    .map((i) => i.path)
    // ui-fix — an in-place drop onto the breadcrumb (the same folder) is a no-op.
    .filter((p) => p && p !== targetDir && !targetDir.startsWith(p + '/') && !sameDir(wireParent(p), targetDir));
  if (sources.length === 0) return;
  await transferItems(sources, targetDir, dndOrigin(ev)); /* wiring:d1 — storage-aware transfer */
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

// Auth-headers builder handed to PreviewModal, QuickLook and every viewer.
//
// ⚠⚠ ASYNC on purpose. This used to call `authHeadersSync`, which drops the
// bearer entirely when the embedder supplies a token FUNCTION rather than a
// string — the shape the desktop app uses so the credential is fetched per
// call instead of sitting in the page. The result was an Authorization-less
// request and a 401 on the OnlyOffice config endpoint, the starred list, the
// recently-opened POST and every viewer that fetches its own bytes. Consumers
// must `await` this; the guard test in web/tests/api/authHeaders.test.ts fails
// the build if one forgets.
function buildAuthHeaders(extra: Record<string, string> = {}) {
  return api.authHeaders({ ...extra });
}

/** The standalone viewer/editor route the backend serves next to the API. */
const STANDALONE_ROUTE = '/files/edit';

/**
 * Makes a page route absolute against the API host.
 *
 * ⚠ A root-relative route like `/files/edit` is resolved by the browser
 * against the PAGE, not the API — which is right when the explorer is embedded
 * in the same app that serves the route, and wrong for every embed served from
 * a different origin. In the desktop app the page origin is `app://filex`, so
 * "Open in new tab" asked the OS to open `app://filex/files/edit?…`: no handler
 * for that scheme exists, so the click did nothing at all, silently. Measured
 * 2026-08-10.
 */
function absolutePageUrl(base: string): string {
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(base)) return base; // already absolute
  const api = props.config.apiBase;
  if (!api) return base; // same-origin embed — the browser resolves it correctly
  try {
    return new URL(base, api.endsWith('/') ? api : `${api}/`).toString();
  } catch {
    return base;
  }
}

/** Where the modal's "Open in new tab" / "Edit" buttons should land. */
const effectiveViewerBaseUrl = computed(() =>
  absolutePageUrl(props.config.viewerBaseUrl || STANDALONE_ROUTE),
);

/* === wiring:c1 — tema galerisi ===
 * Selected theme (shared module state, localStorage `filex.theme`) is applied
 * as inline `--fe-*` variables on the explorer root, resolved to the active
 * light/dark variant — plus a mirrored injected stylesheet for the surfaces
 * that re-declare tokens (teleported context menu, modal backdrops). Theme
 * selection is independent from the light/dark mode: the mode only picks
 * WHICH variant of the theme paints. */
const showThemeGallery = ref(false);
const { themeId: activeThemeId, setTheme: setActiveTheme } = useThemeState();
// The light/dark MODE the person looking at it picked in the gallery. 'host'
// (the default) defers to whatever the embedder passed, so no existing embed
// changes appearance until someone actually chooses.
//
// ⚠ Everything that used to read `config.theme` reads `themeMode` instead — a
// choice that only reached the token resolution would leave the root class, the
// modals and the teleported menus painting the OLD mode, which is exactly the
// half-dark UI that bug looks like.
const { themeMode: themeModePref, setThemeMode: setThemeModePref } = useThemeModeState();
const themeMode = computed<ThemeMode>(() =>
  themeModePref.value === 'host' ? props.config.theme || 'auto' : themeModePref.value,
);
// Resolved mode: an explicit choice wins, otherwise the OS preference — the
// same logic variables.css encodes in CSS. The inline root variables beat every
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
  () => themeMode.value === 'dark' || (themeMode.value !== 'light' && themeOsDark.value),
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
  /* wiring:e2 — in an encrypted folder the Space peek decrypts first, then opens */
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
    /* wiring:e2 — when arrowing through files, decrypt BEFORE assigning the
       target as well, otherwise the viewer briefly gets the raw ciphertext URL. */
    if (n && n.path !== quickLookTarget.value?.path && e2eUnlocked.value) {
      void (async () => {
        try {
          await e2eFetchDecrypted(n);
        } catch {
          /* decryption failed — switch the target anyway, the viewer shows the error */
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
 * Retry a failed upload from the operations center. The failed row is already
 * retired by the store; re-run the upload against the job's ORIGINAL target
 * folder (the user may have navigated away since).
 *
 * ⚠ It goes back through the SAME decision a fresh upload makes, rather than
 * straight to the single-POST path as it used to. A retry is the moment resume
 * matters most: the staged session and its bookmark are still there, so this
 * continues from filex's offset instead of pushing the whole file again.
 */
function retryUploadJob(job: UploadJob) {
  uploadJobs.value = uploadJobs.value.filter((j) => j.id !== job.id);
  const file = job.file;
  const target = job.path || qualify(currentPath.value);
  void (async () => {
    if (chunked.shouldChunk(file)) {
      if (await chunkedUpload(file, target)) {
        await load();
        return;
      }
    }
    await legacyUpload(file, target);
    await load();
  })();
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

/* === wiring:d1 — tabs (tab strip) + per-tab split ===
 *
 * useTabs is a layer ON TOP of the existing location state
 * (currentPath/viewMode): the active tab watches navigations and updates its
 * snapshot; switching tabs calls the existing load(path) path — no new fetch
 * logic. The strip is not rendered at all on a single tab (embeds stay
 * pixel-identical).
 *
 * Persist: `filex.tabs` — it follows the pathPersist scope logic: with mode
 * 'none' persistence is off; the rootPath confine is added to the key so that
 * embeds with different confines don't overwrite each other's tabs.
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

const activeSplit = computed(() => tabsApi.activeTab.value?.split ?? null);

// Tab name is AUTOMATIC = the current folder name (root = storage name / root label).
function tabLabel(path: string): string {
  const p = (path || '').replace(/^\/+|\/+$/g, '');
  // gezinti:g1 — the virtual views park a sentinel in the path. Translate via
  // the SHARED map: this special-cased only '.trash' when recent/starred/shared
  // arrived, so the strip read ".shared" at users (reported 2026-09-04).
  const virtualLabel = virtualSegmentLabel(p.split('/').pop() || p, t);
  if (virtualLabel) return virtualLabel;
  if (!p) return multiStorageRoot.value ? t('breadcrumb.root') : adapter.value || t('breadcrumb.root');
  return p.split('/').pop() || p;
}
const tabItems = computed(() =>
  tabsApi.tabs.value.map((tb) => ({ id: tb.id, label: tabLabel(tb.path), split: !!tb.split })),
);

// The strip is visible by DEFAULT, on every surface (see ExplorerConfig — the
// default used to differ between the desktop app and the web, which is the one
// thing a shared package must never do). `tabStrip: 'auto'` is the opt-out.
// ⚠ Gated on there being a tab at all, not on the flag alone: tabs are seeded
// in onMounted, so the strip would otherwise paint one frame empty — a lone
// `+` floating above the toolbar.
const tabsVisible = computed(
  () =>
    /* gezinti:g1 — the simple profile has no tab strip at all, not even once a
       second tab exists: tabs were the first thing #14 named as power-user
       chrome. The tab STATE is untouched, so switching the profile back brings
       the strip and its tabs straight back. */
    !simpleUi.value &&
    (tabsApi.hasMultiple.value ||
      (props.config.tabStrip !== 'auto' && tabItems.value.length > 0)),
);

// The active tab follows the user: navigation + view changes go into the snapshot.
watch(currentPath, (p) => tabsApi.syncActive({ path: p }));
watch(viewMode, (v) => tabsApi.syncActive({ viewMode: v }));

// The first tab is seeded as soon as the first location is known (don't touch
// it if a restore happened — the active snapshot is already synced by the
// currentPath watcher after the first load).
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
  // Clones the current location; the view is already there, so no load needed.
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

/** Open a folder in a new tab IN THE BACKGROUND (middle-click / right-click / palette). */
function openNodeInTab(n: FileNode) {
  if (n.type !== 'dir' || n.basename === '.trash') return;
  const target = multiStorageRoot.value ? wireToVirtual(n.path) : stripAdapter(n.path);
  tabsApi.openTab(target, { viewMode: viewMode.value, background: true });
}

// Middle-click delegation: ListView/GridView rows carry data-fe-path, so a
// single auxclick listener at the root is enough instead of adding a
// keydown/emit chain of our own. (SecondaryPane handles its own rows via
// stopPropagation.)
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
// Cancel the middle-button mousedown over rows: in a scrollable body Chromium's
// autoscroll kicks in and auxclick is NEVER produced (diagnosed live) —
// preventDefault suppresses autoscroll and auxclick flows again.
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

// ---- split (per-tab secondary pane) --------------------------------

const splitPaneRef = ref<InstanceType<typeof SecondaryPane> | null>(null);
// Split is disabled in narrow mode (the state is kept and comes back on widen).
const splitVisible = computed(
  () => !!activeSplit.value && !isNarrow.value && !simpleUi.value /* gezinti:g1 */,
);

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
/* ui-fix — when the trash row is opened from the side pane: the trash view
 * (with its restore actions) belongs to the main pane → activate the main
 * pane and open it there. */
function onPaneOpenTrash() {
  activePane.value = 'main';
  void loadTrash();
}
/* ui-fix — the pane's OWN view mode: it inherits the main pane's when the
 * split opens, and is independent afterwards. The toolbar's view switcher and
 * the palette toggle write to the ACTIVE pane (Ada, translated from Turkish:
 * "if I say change the icon while B is focused, B is the one that has to
 * change"). */
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

// Active pane: shortcuts go to the active pane; a pane is activated by clicking it.
const activePane = ref<'main' | 'split'>('main');
function setPaneMain() {
  activePane.value = 'main';
}
watch(splitVisible, (v) => {
  if (!v) activePane.value = 'main';
});
const paneIsActive = computed(() => activePane.value === 'split' && splitVisible.value);
const mainPaneFocus = computed(() => splitVisible.value && activePane.value === 'main');

// Pane helpers — always wrap the main pane's existing converters.
function paneToUser(wire: string): string {
  return multiStorageRoot.value ? wireToVirtual(wire) : stripAdapter(wire);
}
function paneClamp(p: string): string {
  const clean = String(p ?? '').replace(/^\/+|\/+$/g, '');
  if (!rootFloor) return clean;
  if (!clean || !(clean === rootFloor || clean.startsWith(rootFloor + '/'))) return rootFloor;
  return clean;
}

// The clipboard follows the active pane: cut/copy is fed from the pane
// selection and paste lands in the pane's folder. The state is SHARED with the
// main pane's — so cut-and-paste between panes works for free.
function paneCut() {
  const nodes = splitPaneRef.value?.selectedNodes() ?? [];
  if (nodes.length === 0) return;
  clipboard.value = { mode: 'cut', items: nodes, sourcePath: splitPaneRef.value?.getPath() ?? '' };
  flashToast(t('toast.cut'));
}
function paneCopy() {
  const nodes = splitPaneRef.value?.selectedNodes() ?? [];
  if (nodes.length === 0) return;
  clipboard.value = { mode: 'copy', items: nodes, sourcePath: splitPaneRef.value?.getPath() ?? '' };
  flashToast(t('toast.copied'));
}
async function panePaste() {
  const cb = clipboard.value;
  const pane = splitPaneRef.value;
  if (!cb.mode || cb.items.length === 0 || !pane) return;
  const targetWire = qualify(pane.getPath() ?? '');
  const originWire = qualify(cb.sourcePath || '') || undefined;
  if (cb.mode === 'cut' && originWire === targetWire) {
    flashToast(t('toast.same_folder_cut'));
    return;
  }
  await transferItems(cb.items.map((n) => n.path), targetWire, originWire, cb.mode === 'copy' ? 'copy' : 'move');
  clipboard.value = { mode: null, items: [], sourcePath: null };
}

// ---- cross-pane transfer -------------------------------------------
function dndOrigin(ev: DragEvent): string | undefined {
  return internalDragOrigin(ev);
}

/**
 * transferItems — the single gate for cross-pane / clipboard transfers.
 *
 * `resolveTransfer` decides what to do (lib/transfer.ts): a drag MOVES within
 * the same storage and COPIES across storages; a cut/copy coming from
 * the clipboard does exactly what it says — cut MOVES even when the target is
 * another storage (the server transfers the bytes and deletes the source).
 * ⚠ Cross-storage transfers used to silently fall back to a copy: the user
 * said "cut" and found the file in two places. When it's done the secondary
 * pane is refreshed too (the main pane is already refreshed via
 * moveSourcesAsync / pendingOps onSettled).
 */
async function transferItems(
  sources: string[],
  targetWire: string,
  originWire?: string,
  intent: TransferIntent = 'auto',
): Promise<void> {
  // ui-fix — an in-place drop (source parent === target) is a no-op: this
  // avoids the backend's "copy onto itself" 400 (cross-pane + clipboard paths).
  const list = sources.filter(
    (p) => p && p !== targetWire && !targetWire.startsWith(p + '/') && !sameDir(wireParent(p), targetWire),
  );
  if (list.length === 0 || !targetWire) return;
  const plan = resolveTransfer(list, targetWire, intent);
  if (plan.kind === 'copy') {
    try {
      const { op } = await api.copy(list, targetWire);
      pendingOps.register(op);
      flashToast(plan.cross ? t('split.cross_copy') : t('split.copy_queued'));
    } catch (err) {
      // ⚠ Show the server's own message. There used to be a hard-coded
      // "cross-storage is not supported" text here; it IS supported now, so
      // that text would mask the real cause (permissions, a read-only storage,
      // a full quota).
      emit('error', { message: (err as Error).message, context: { op: 'transfer', targetWire } });
      flashToast((err as Error).message);
      return;
    }
  } else {
    if (plan.cross) flashToast(t('split.cross_move'));
    await moveSourcesAsync(list, targetWire, 'move-transfer', originWire);
  }
  void splitPaneRef.value?.reload();
}

function onPaneTransfer(p: { sources: string[]; targetWire: string; originWire?: string }) {
  void transferItems(p.sources, p.targetWire, p.originWire);
}
/* === /wiring:d1 === */

/* === wiring:e2 — end-to-end encrypted folders ===
 *
 * Crypto scheme + threat model: docs/E2E-ENCRYPTION.md and lib/e2ecrypto.ts.
 * Only the orchestration lives here: `e2e_root` in the backend listing
 * response drives the lock screen; the password is verified against the
 * marker IN THE BROWSER (nothing goes to the server); the derived folder key
 * (KEK) lives ONLY in memory (`e2eRing`) — it is never written to
 * localStorage/sessionStorage. Uploads are encrypted transparently and
 * previews/downloads decrypted transparently (a blob URL to the existing
 * viewers).
 */
const e2eRing = createKeyRing();
// Maps aren't reactive — a version counter drives the computeds.
const e2eRingVer = ref(0);
// Wire path of the encrypted root we are inside ('' = no encrypted context).
const e2eRoot = ref('');
// path → decrypted blob objectURL (preview). Revoked on lock/unmount.
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

// Lock screen form.
const e2ePw = ref('');
const e2eUnlockBusy = ref(false);
const e2eUnlockErr = ref('');
// Encrypted-folder creation modal.
const showEncFolder = ref(false);
const e2eCreateBusy = ref(false);

/* wiring:e2 recovery — recovery key + escrow.
 *
 * The marker of the folder we are looking at is cached here while the lock
 * screen is up: the recovery dialog needs to know which doors this folder
 * actually has (a pre-0.31 folder has none) before offering them. */
const e2eMarker = ref<E2eMarker | null>(null);
const showRecoveryUnlock = ref(false);
const e2eRecoverBusy = ref(false);
const e2eRecoverErr = ref<string | null>(null);
// The shown-once key. Held only while its dialog is open.
const showRecoveryKey = ref(false);
const recoveryKeyValue = ref('');
const recoveryKeyVariant = ref<'created' | 'upgraded'>('created');
const recoveryKeyFolder = ref('');
// A v1 folder that just opened by password: offer to give it recovery now,
// because this is the only moment filex holds the password.
const e2eUpgradeOffer = ref(false);
const e2eUpgradePw = ref('');
const e2eUpgradeBusy = ref(false);

/* wiring:e2 escrow-offer — offering an EXISTING folder an escrow slot.
 *
 * Adoption covers folders created after it, which on an installation that
 * has been running for a while is nobody's folders: the ones that matter
 * already exist. The server cannot reach them — adding a slot needs the
 * folder master key. Its owner can, from inside, with the password, and the
 * one moment that password exists in the browser is an unlock. So this
 * lives exactly where the v1 recovery offer lives, in the same strip, with
 * the same shape.
 *
 * mode distinguishes the two ways in:
 *   'unlock'  right after a successful password unlock — we still hold the
 *             password, so Accept needs no typing, and Not now RECORDS a
 *             refusal so the question is not asked again.
 *   'manual'  the way back, from the unlocked strip, for somebody who said
 *             no earlier. The password is gone by then, so it is typed, and
 *             Cancel records nothing — they already answered once. */
const e2eEscrowOffer = ref(false);
const e2eEscrowOfferMode = ref<'unlock' | 'manual'>('unlock');
const e2eEscrowPw = ref('');
const e2eEscrowBusy = ref(false);
const e2eEscrowErr = ref('');

/** The installation's escrow public key, or null when escrow is off.
 *  Published in /api/capabilities on purpose — see docs/E2E-ENCRYPTION.md. */
const e2eEscrowPub = computed<string | null>(
  () => capabilitiesData.value?.e2e_escrow?.public_key || null,
);
/** Where the honest version of "what escrow can and cannot do" lives.
 *  ⚠ Linked from the offer on purpose: the notice says what accepting means,
 *  and the page says what the use-notification can and cannot promise — that
 *  an operator holding the private key can decrypt offline with no request,
 *  no notification and no audit row. Somebody being asked to hand over a key
 *  is entitled to read that before answering, not after. */
const e2eEscrowDocsUrl = 'https://docs.filex.sh/E2E-ENCRYPTION#what-escrow-can-and-cannot-do';

const e2eEscrowKid = computed<string | null>(
  () => capabilitiesData.value?.e2e_escrow?.kid || null,
);

function e2eKek(): CryptoKey | null {
  return e2eRing.get(e2eRoot.value) ?? null;
}

function e2eRevokeAll() {
  for (const url of e2eUrls.values()) URL.revokeObjectURL(url);
  e2eUrls.clear();
}
onBeforeUnmount(e2eRevokeAll);

/** Unlock: fetch the marker from the root, verify the password LOCALLY, put the KEK in memory. */
async function e2eUnlock() {
  if (!e2ePw.value || e2eUnlockBusy.value || !e2eRoot.value) return;
  e2eUnlockBusy.value = true;
  e2eUnlockErr.value = '';
  try {
    let markerText = '';
    try {
      const { blob, url } = await api.fetchBlob(wireJoin(e2eRoot.value, E2E_MARKER_NAME), { fresh: true });
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
    e2eMarker.value = marker;
    const fmk = await unlockWithPassword(marker, e2ePw.value);
    if (!fmk) {
      e2eUnlockErr.value = t('e2e.unlock.wrong');
      return;
    }
    e2eRing.set(e2eRoot.value, fmk);
    e2eRingVer.value++;
    /* wiring:e2 recovery — a folder from before recovery existed has no way
     * back in but its password. This is the ONE moment we hold that password,
     * so ask now. Asking is all we do: the folder keeps working untouched if
     * the user says no, and saying yes is the only path that also hands the
     * operator an escrow key (when the install has one), which is why the
     * prompt says so rather than doing it quietly. */
    if (marker.v === 1) {
      e2eUpgradePw.value = e2ePw.value;
      e2eUpgradeOffer.value = true;
    } else if (escrowOfferState(marker, e2eEscrowKid.value) === 'offer') {
      /* wiring:e2 escrow-offer — a v2 folder that predates escrow here.
       * ⚠ Only after the unlock SUCCEEDED, and only by password: accepting
       * gives the operator a key to this folder, so the person asked has to
       * be the person who can already open it. A v1 folder is handled by the
       * branch above, which seals an escrow slot as part of the upgrade and
       * already discloses that — two offers on one unlock would be two
       * chances to get the disclosure wrong. */
      e2eEscrowPw.value = e2ePw.value;
      e2eEscrowOfferMode.value = 'unlock';
      e2eEscrowErr.value = '';
      e2eEscrowOffer.value = true;
    }
    e2ePw.value = '';
  } finally {
    e2eUnlockBusy.value = false;
  }
}

/** "Kilitle" (Lock): drop the in-memory key and the decrypted blobs. */
function e2eLock() {
  if (!e2eRoot.value) return;
  e2eRing.lock(e2eRoot.value);
  e2eRingVer.value++;
  e2eRevokeAll();
  flashToast(t('e2e.locked_toast'));
}

// Extension → preview MIME: so the decrypted blob renders correctly in
// <img>/<video>/<object> tags (the server knows the encrypted file as
// octet-stream, so the type coming from there is useless).
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
 * Fetch the file + decrypt it + cache its objectURL. Returns null for a file
 * with no magic (e.g. written in the clear over DAV) — the caller then falls
 * back to the normal raw flow. A wrong key / corrupt data throws
 * E2eDecryptError (the caller toasts it).
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

/** URL provider handed to PreviewModal/QuickLook: decrypted blob > raw URL. */
function e2ePreviewSrc(p: string): string {
  return e2eUrls.get(p) ?? api.previewUrl(p);
}

/** Decrypt + read-only in-page preview (openNode/previewNode land here). */
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

/** Decrypt + download under the original name. A file with no magic comes down as-is. */
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

/** Upload list → encrypted File list (anything over 200MB + the marker name is skipped). */
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

/** EncryptedFolderModal submit: create the folder + upload the marker + leave it unlocked. */
async function submitEncryptedFolder(payload: { name: string; password: string }) {
  if (e2eActive.value) {
    // Nested encrypted folders blur root detection — not in the MVP.
    flashToast(t('e2e.create.nested'));
    return;
  }
  e2eCreateBusy.value = true;
  try {
    const dirWire = qualify(currentPath.value);
    await api.newFolder(dirWire, payload.name);
    /* wiring:e2 recovery — the folder gets a recovery key at birth, and an
     * escrow slot when the installation has one. Both are decided HERE and
     * never again: the wrapped copies are written into the marker now, so a
     * folder created without escrow can never be opened by an escrow key. */
    const { marker, fmk, recoveryKey } = await createEncryptedFolder(payload.password, {
      escrowPublicKey: e2eEscrowPub.value,
    });
    const markerFile = new File([JSON.stringify(marker)], E2E_MARKER_NAME, {
      type: 'application/json',
    });
    const newDirWire = wireJoin(dirWire, payload.name);
    await api.uploadMultipart(newDirWire, [markerFile]);
    // It starts unlocked in the creating session (they just typed the password).
    e2eRing.set(newDirWire, fmk);
    e2eRingVer.value++;
    showEncFolder.value = false;
    // ⚠ Show the key only after the marker is safely uploaded. Showing it
    // first would promise recovery for a folder that failed to be created.
    recoveryKeyValue.value = recoveryKey;
    recoveryKeyFolder.value = payload.name;
    recoveryKeyVariant.value = 'created';
    showRecoveryKey.value = true;
    await load();
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'e2e-create' } });
    flashToast(t('e2e.create.failed'));
  } finally {
    e2eCreateBusy.value = false;
  }
}

/* --- wiring:e2 recovery ------------------------------------------------
 *
 * Two more ways into a locked folder, and one way to give an old folder
 * those ways. The password path above is untouched, and nothing here runs
 * without an explicit user action.
 */

/** Unlock without the password: user recovery key, or the operator's escrow
 *  key. The escrow branch announces itself to the server first. */
async function e2eRecoverUnlock(payload: { mode: 'recovery' | 'escrow'; value: string }) {
  if (!e2eMarker.value || !e2eRoot.value) return;
  e2eRecoverBusy.value = true;
  e2eRecoverErr.value = null;
  try {
    let fmk: CryptoKey | null = null;
    if (payload.mode === 'recovery') {
      fmk = await unlockWithRecoveryKey(e2eMarker.value, payload.value);
      if (!fmk) {
        e2eRecoverErr.value = t('e2e.recover.wrong_recovery');
        return;
      }
    } else {
      let priv: CryptoKey;
      try {
        priv = await importEscrowPrivateKey(payload.value);
      } catch {
        e2eRecoverErr.value = t('e2e.recover.bad_escrow_key');
        return;
      }
      fmk = await unlockWithEscrowKey(e2eMarker.value, priv);
      if (!fmk) {
        e2eRecoverErr.value = t('e2e.recover.wrong_escrow');
        return;
      }
      /* ⚠ Announce BEFORE unlocking, and treat a failure to announce as a
       * failure to unlock. The server hands out a nonce sealed to the escrow
       * public key; returning it proves the key was really here, and that is
       * what earns the owner their notification.
       *
       * ⚠⚠ This is not enforcement and must never be described as such. An
       * operator holding the escrow private key can decrypt the same folder
       * offline, with a script, and this code will never run. Refusing to
       * unlock on a failed announcement only keeps the honest path honest. */
      try {
        const ch = await api.e2eEscrowChallenge(e2eRoot.value);
        const nonce = new Uint8Array(
          await crypto.subtle.decrypt(
            { name: 'RSA-OAEP' },
            priv,
            b64ToBytes(ch.challenge).buffer as ArrayBuffer,
          ),
        );
        await api.e2eEscrowUsed({
          path: e2eRoot.value,
          id: ch.id,
          nonce: bytesToB64(nonce),
        });
      } catch (err) {
        e2eRecoverErr.value = t('e2e.recover.notify_failed');
        emit('error', {
          message: (err as Error).message,
          context: { op: 'e2e-escrow-notify' },
        });
        return;
      }
    }
    e2eRing.set(e2eRoot.value, fmk);
    e2eRingVer.value++;
    showRecoveryUnlock.value = false;
    flashToast(
      payload.mode === 'escrow' ? t('e2e.recover.escrow_done') : t('e2e.recover.recovery_done'),
    );
  } catch (err) {
    e2eRecoverErr.value = (err as Error).message;
  } finally {
    e2eRecoverBusy.value = false;
  }
}

/** Open the recovery dialog from the lock screen. The marker was cached by
 *  the last unlock attempt; fetch it if the user came straight here. */
async function openRecoveryUnlock() {
  if (!e2eMarker.value && e2eRoot.value) {
    try {
      const { blob, url } = await api.fetchBlob(wireJoin(e2eRoot.value, E2E_MARKER_NAME), { fresh: true });
      URL.revokeObjectURL(url);
      e2eMarker.value = parseMarker(await blob.text());
    } catch {
      e2eMarker.value = null;
    }
  }
  e2eRecoverErr.value = null;
  showRecoveryUnlock.value = true;
}

/** Give a pre-0.31 folder a recovery key, in place, using the password the
 *  user just typed. The files are NOT rewritten — only the marker is. */
async function e2eDoUpgrade() {
  if (!e2eMarker.value || !e2eRoot.value || !e2eUpgradePw.value) return;
  e2eUpgradeBusy.value = true;
  try {
    const up = await upgradeMarkerV1(e2eMarker.value, e2eUpgradePw.value, {
      escrowPublicKey: e2eEscrowPub.value,
    });
    const markerFile = new File([JSON.stringify(up.marker)], E2E_MARKER_NAME, {
      type: 'application/json',
    });
    await api.uploadMultipart(e2eRoot.value, [markerFile]);
    e2eMarker.value = up.marker;
    e2eUpgradeOffer.value = false;
    e2eUpgradePw.value = '';
    recoveryKeyValue.value = up.recoveryKey;
    recoveryKeyFolder.value = wireBasename(e2eRoot.value);
    recoveryKeyVariant.value = 'upgraded';
    showRecoveryKey.value = true;
  } catch (err) {
    emit('error', { message: (err as Error).message, context: { op: 'e2e-upgrade' } });
    flashToast(t('e2e.upgrade.failed'));
  } finally {
    e2eUpgradeBusy.value = false;
  }
}

/* wiring:e2 escrow-offer ------------------------------------------- */

/** Whether this folder could be given an escrow slot, and whether its owner
 *  has already answered. Drives the way-back button in the unlocked strip. */
const e2eEscrowOfferState = computed(() => escrowOfferState(e2eMarker.value, e2eEscrowKid.value));

/** The way back. Somebody who said no last month can say yes today without
 *  deleting and re-creating the folder — but the password is long gone from
 *  memory, so they type it. That is not friction to be smoothed away: it is
 *  the same proof of ownership the offer at unlock had, and handing the
 *  operator a key deserves it. */
function e2eOpenEscrowOffer() {
  e2eEscrowOfferMode.value = 'manual';
  e2eEscrowPw.value = '';
  e2eEscrowErr.value = '';
  e2eEscrowOffer.value = true;
}

/** Write a marker back to the folder and adopt it as the one we are holding.
 *  Both escrow-offer answers change only `.filex-e2e.json`. */
async function e2eWriteMarker(next: E2eMarker) {
  const file = new File([JSON.stringify(next)], E2E_MARKER_NAME, { type: 'application/json' });
  await api.uploadMultipart(e2eRoot.value, [file]);
  e2eMarker.value = next;
}

/** Accept: seal this folder's master key to the installation's escrow key.
 *  No file is re-encrypted or moved — only the marker gains a slot. */
async function e2eAcceptEscrow() {
  const marker = e2eMarker.value;
  const pub = e2eEscrowPub.value;
  if (!marker || !pub || !e2eRoot.value || e2eEscrowBusy.value) return;
  if (!e2eEscrowPw.value) {
    e2eEscrowErr.value = t('e2e.escrowoffer.password_required');
    return;
  }
  e2eEscrowBusy.value = true;
  e2eEscrowErr.value = '';
  try {
    // ⚠ The kid comes from the installation's CURRENT key, via the marker
    // addEscrowSlot writes — never from anything the UI is displaying. The
    // sibling bug went the other way (the dialog labelled a folder's slot
    // with the installation's kid); writing the installation's kid into a
    // slot sealed to some other key would be the same lie in reverse.
    const next = await addEscrowSlot(marker, e2eEscrowPw.value, pub);
    await e2eWriteMarker(next);
    e2eEscrowOffer.value = false;
    e2eEscrowPw.value = '';
    flashToast(t('e2e.escrowoffer.done'));
  } catch (err) {
    // A wrong password is the ordinary case here and reads as a typo, not
    // as a broken folder; anything else is worth reporting.
    if ((err as Error)?.name === 'E2eDecryptError') {
      e2eEscrowErr.value = t('e2e.escrowoffer.wrong_password');
    } else {
      e2eEscrowErr.value = t('e2e.escrowoffer.failed');
      emit('error', { message: (err as Error).message, context: { op: 'e2e-escrow-offer' } });
    }
  } finally {
    e2eEscrowBusy.value = false;
  }
}

/** Not now. Recorded IN THE FOLDER, so the same person on another device is
 *  not asked again and the answer survives a cleared browser. A question
 *  that returns every single unlock is how people learn to click past
 *  security dialogs without reading them. */
async function e2eDeclineEscrow() {
  const marker = e2eMarker.value;
  if (!marker || !e2eRoot.value || e2eEscrowBusy.value) return;
  e2eEscrowBusy.value = true;
  try {
    await e2eWriteMarker(declineEscrowSlot(marker, new Date().toISOString()));
    e2eEscrowOffer.value = false;
    e2eEscrowPw.value = '';
    flashToast(t('e2e.escrowoffer.declined_toast'));
  } catch (err) {
    // ⚠ Fail towards asking again rather than towards silence: if the
    // refusal could not be written, the honest state is "not answered yet".
    e2eEscrowErr.value = t('e2e.escrowoffer.decline_failed');
    emit('error', { message: (err as Error).message, context: { op: 'e2e-escrow-decline' } });
  } finally {
    e2eEscrowBusy.value = false;
  }
}

/** Close the way-back panel without answering anything. Distinct from
 *  declining: they answered once already, and re-recording the same refusal
 *  would overwrite the date it was actually made. */
function e2eCloseEscrowOffer() {
  e2eEscrowOffer.value = false;
  e2eEscrowPw.value = '';
  e2eEscrowErr.value = '';
}

/** Decline the offer. The folder keeps working exactly as it did, and the
 *  prompt returns on the next unlock because the risk has not changed. */
function e2eDeclineUpgrade() {
  e2eUpgradeOffer.value = false;
  e2eUpgradePw.value = '';
}

/** Drop the shown-once key from memory the moment its dialog closes. */
function closeRecoveryKey() {
  showRecoveryKey.value = false;
  recoveryKeyValue.value = '';
  recoveryKeyFolder.value = '';
}
/* === /wiring:e2 === */
</script>

<template>
  <div
    ref="rootEl"
    class="fe"
    :class="{
      'fe--theme-light': themeMode === 'light',
      'fe--theme-dark': themeMode === 'dark',
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
    <!-- wiring:d1 — tab strip: not rendered at all on a SINGLE tab (embeds stay pixel-identical) -->
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
      :view-mode="displayedViewMode /* ui-fix — the active pane's mode */"
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
      :theme="themeMode /* bag:b4 */"
      :inspector-open="showInspector /* koru:k1 */"
      :nav-open="navToggleOn /* gezinti:g1 */"
      :nav-enabled="sideNavEnabled /* gezinti:g1 */"
      :view-modes="allowedViewModes /* gezinti:g1 */"
      :shell="driveShell ? 'drive' : 'classic' /* surucu:d1 */"
      :scope-label="driveScopeLabel /* surucu:d1 */"
      @open-palette="openPaletteWith /* surucu:d1 */"
      @toggle-inspector="toggleInspector /* koru:k1 */"
      @toggle-nav="toggleSideNav /* gezinti:g1 */"
      @open-theme="showThemeGallery = true /* wiring:c1 */"
      @update:view-mode="setDisplayedViewMode($event) /* ui-fix — to the active pane */"
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
    <!-- gezinti:g1 — navigation panel. First child of fe__main, the mirror of
         InspectorPanel on the right; .fe__primary already carries
         `flex: 1 1 auto; min-width: 0` so it absorbs the width with no rule of
         its own. Wide: a docked column (or a 56px icon rail when collapsed).
         Narrow: a drawer over the listing, because a column at 390px leaves
         the files 158px. -->
    <SideNav
      v-if="navVisible"
      :expanded="sideNavExpanded"
      :narrow="isNarrow"
      :active-view="navView"
      :active-tag="navTag"
      :tags="navTags"
      :tags-loaded="navTagsLoaded"
      :active-storage="adapter"
      :storages="config.storages ?? []"
      :shared-storages="sharedStorageNames"
      :trash-visible="config.trashVisible !== false"
      :show-connections="connectionsEnabled"
      :show-identity-surfaces="identitySurfaces"
      :can-write="canWriteHere && !atVirtualRoot && !trashActive"
      :locale="locale"
      :new-menu="driveShell /* surucu:d1 */"
      :can-request-files="canWriteHere && !atVirtualRoot && !trashActive && !navView /* surucu:d1 */"
      :quota="quotaSnapshot /* surucu:d1 */"
      :theme="themeMode /* surucu:d1 — the teleported New menu leaves .fe */"
      @request-files="openFileRequest /* surucu:d1 */"
      @toggle="toggleSideNav"
      @close="closeNavDrawer"
      @open-view="loadNavView"
      @open-tag="loadTagView"
      @open-storage="openNavStorage"
      @upload="triggerUpload"
      @new-folder="showNewFolder = true"
      @open-connections="openConnections"
      @open-tokens="openTokens"
    />
    <!-- The drawer's scrim. A button, not a div: dismissing an overlay by
         clicking beside it has to be reachable from the keyboard too. -->
    <button
      v-if="isNarrow && navDrawerOpen"
      type="button"
      class="fe-sidenav__scrim"
      :title="t('sidenav.close')"
      :aria-label="t('sidenav.close')"
      @click="closeNavDrawer"
    ></button>
    <!-- ui-fix — the left pane's header (breadcrumb + status strips + body)
         in one wrapper: in split mode this wrapper fits the left half, so the
         breadcrumb spans its own pane rather than the whole page (symmetric
         with SecondaryPane's own crumbs). The active-pane accent lives on
         this wrapper too. -->
    <div
      class="fe__primary"
      :class="{ 'fe-pane--focus': mainPaneFocus } /* wiring:d1 — active-pane accent */"
    >
    <!-- surucu:d1 — the breadcrumb row. In the drive shell it also carries the
         two controls that belong to a LISTING rather than to the app (the view
         switcher and the details toggle), which is where the mockups put them
         and where the header then has room for one wide search field.
         Everywhere else the wrapper is `display: contents`, so the breadcrumb
         is the same flex child of `.fe__primary` it has always been — one
         Breadcrumb, not a second copy that can drift. -->
    <div :class="driveShell ? 'fe-subhead' : 'fe-subhead--plain'">
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
    <div v-if="driveShell" class="fe-subhead__actions">
      <ViewSwitcher
        :view-mode="displayedViewMode"
        :locale="locale"
        :modes="allowedViewModes"
        @update:view-mode="setDisplayedViewMode($event)"
      />
      <button
        type="button"
        class="fe-btn fe-btn--icon-only fe-toolbar__inspector"
        :class="{ 'is-active': showInspector }"
        :aria-pressed="showInspector"
        :title="t('toolbar.inspector')"
        :aria-label="t('toolbar.inspector')"
        data-testid="subhead-inspector"
        @click="toggleInspector"
      >
        <svg
          class="fe-ficon"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          aria-hidden="true"
          focusable="false"
        >
          <circle cx="12" cy="12" r="9" />
          <path d="M12 11v5" />
          <circle cx="12" cy="7.6" r="1" fill="currentColor" stroke="none" />
        </svg>
      </button>
    </div>
    </div>
    <FilterBar
      v-if="driveShell && !atVirtualRoot"
      :value="driveFilters"
      :locale="locale"
      :theme="themeMode"
      :shown="displayFiles.length"
      :total="files.length"
      @update:value="setDriveFilters"
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

    <!-- wiring:e2 — unlocked strip: visible in an encrypted folder while the
         key is in memory; "Kilitle" (Lock) drops the key and the decrypted
         blobs. -->
    <!-- wiring:e2 recovery — a v1 folder just opened by password. Offer it
         recovery HERE, visibly, rather than doing anything silently: this is
         the only moment filex holds the password, and (when the install has
         escrow) accepting also gives the operator a key. -->
    <div v-if="e2eUpgradeOffer" class="fe-e2e-upgrade" role="alert">
      <div class="fe-e2e-upgrade__text">
        <strong>{{ t('e2e.upgrade.title') }}</strong>
        <p>{{ t('e2e.upgrade.body') }}</p>
        <p v-if="e2eEscrowKid" class="fe-e2e-upgrade__escrow">
          {{ t('e2e.upgrade.escrow_note') }}
        </p>
      </div>
      <div class="fe-e2e-upgrade__actions">
        <button type="button" class="fe-btn" :disabled="e2eUpgradeBusy" @click="e2eDeclineUpgrade">
          {{ t('e2e.upgrade.decline') }}
        </button>
        <button
          type="button"
          class="fe-btn fe-btn--primary"
          :disabled="e2eUpgradeBusy"
          @click="e2eDoUpgrade"
        >
          {{ e2eUpgradeBusy ? t('e2e.upgrade.busy') : t('e2e.upgrade.accept') }}
        </button>
      </div>
    </div>
<!-- wiring:e2 escrow-offer — an existing folder that predates escrow on
         this installation. Shown only after an unlock SUCCEEDED, because
         accepting hands the operator a permanent second key to this folder
         and the only person entitled to do that is the one who can already
         open it. Doing nothing leaves the folder exactly as it is. -->
    <div v-if="e2eEscrowOffer" class="fe-e2e-upgrade fe-e2e-upgrade--escrow" role="alert">
      <div class="fe-e2e-upgrade__text">
        <strong>{{ t('e2e.escrowoffer.title') }}</strong>
        <p>{{ t('e2e.escrowoffer.body') }}</p>
        <p class="fe-e2e-upgrade__escrow">
          {{ t('e2e.escrowoffer.consequence') }}
          <a :href="e2eEscrowDocsUrl" target="_blank" rel="noopener noreferrer">
            {{ t('e2e.escrowoffer.learn_more') }}
          </a>
        </p>
        <p v-if="e2eEscrowKid" class="fe-e2e-upgrade__escrow">
          {{ t('e2e.recover.escrow_kid') }}: <code>{{ e2eEscrowKid }}</code>
        </p>
        <!-- The way-back path has no password in memory any more, so it is
             typed. The same proof of ownership the unlock path already had. -->
        <label v-if="e2eEscrowOfferMode === 'manual'" class="fe-e2e-upgrade__pw">
          <span>{{ t('e2e.escrowoffer.password_label') }}</span>
          <input
            v-model="e2eEscrowPw"
            type="password"
            class="fe-input"
            autocomplete="current-password"
            :disabled="e2eEscrowBusy"
            @keyup.enter="e2eAcceptEscrow"
          />
        </label>
        <p v-if="e2eEscrowErr" class="fe-form__error" role="alert">{{ e2eEscrowErr }}</p>
      </div>
      <div class="fe-e2e-upgrade__actions">
        <button
          type="button"
          class="fe-btn"
          :disabled="e2eEscrowBusy"
          @click="e2eEscrowOfferMode === 'manual' ? e2eCloseEscrowOffer() : e2eDeclineEscrow()"
        >
          {{
            e2eEscrowOfferMode === 'manual'
              ? t('e2e.escrowoffer.cancel')
              : t('e2e.escrowoffer.decline')
          }}
        </button>
        <button
          type="button"
          class="fe-btn fe-btn--primary"
          :disabled="e2eEscrowBusy"
          @click="e2eAcceptEscrow"
        >
          {{ e2eEscrowBusy ? t('e2e.escrowoffer.busy') : t('e2e.escrowoffer.accept') }}
        </button>
      </div>
    </div>
    <div v-if="e2eUnlocked" class="fe-e2e-strip" role="status">
      <span class="fe-e2e-strip__icon" aria-hidden="true">🔒</span>
      <span class="fe-e2e-strip__label">{{ t('e2e.strip.label') }}</span>
      <!-- The way back for somebody who declined. Quiet, but present: a
           refusal that could not be reversed without deleting the folder
           would not be a decision, it would be a trap. -->
      <button
        v-if="e2eEscrowOfferState !== 'n/a' && !e2eEscrowOffer"
        type="button"
        class="fe-btn fe-e2e-strip__btn"
        @click="e2eOpenEscrowOffer"
      >
        {{ t('e2e.escrowoffer.strip_action') }}
      </button>
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
        <div v-if="viewMode !== 'list' /* wiring:d2 — the gallery uses the grid skeleton too */" class="fe-skel-grid" aria-hidden="true">
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
      <!-- wiring:e2 — encrypted-folder lock screen: the listing is not
           rendered until the correct password is entered. The password is
           verified against the marker in the browser; it never reaches the
           server. -->
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
        <!-- wiring:e2 recovery — the second door. Always offered: whether
             this folder actually has one is answered inside the dialog,
             which can say "this folder predates recovery keys" instead of
             leaving the user guessing why there is no link. -->
        <button type="button" class="fe-e2e-optlink" @click="openRecoveryUnlock">
          {{ t('e2e.locked.use_recovery') }}
        </button>
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
      <!-- gezinti:g1 — empty panel views. Each says which list is empty and
           how it fills up; "This folder is empty" would be wrong twice over,
           because there is no folder and nothing to drop into it. -->
      <div
        v-else-if="!loading && files.length === 0 && navView && navView !== 'trash'"
        class="fe-state"
        :data-testid="`empty-${navView}`"
      >
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
          <template v-if="navView === 'recent'">
            <circle cx="60" cy="50" r="28" />
            <path d="M60 32v18l12 8" />
          </template>
          <template v-else-if="navView === 'starred'">
            <path d="M60 26l9 18.6 20.4 3-14.8 14.4 3.5 20.4L60 72.8 41.9 82.4l3.5-20.4L30.6 47.6l20.4-3z" />
          </template>
          <template v-else-if="navView === 'tag'">
            <path d="M30 30h24l32 32-24 24-32-32z" />
            <circle cx="43" cy="43" r="4.5" />
          </template>
          <template v-else>
            <circle cx="84" cy="34" r="9" />
            <circle cx="36" cy="52" r="9" />
            <circle cx="84" cy="70" r="9" />
            <path d="M44.5 47.5l31-9M44.5 56.5l31 9" />
          </template>
        </svg>
        <!-- etiket:t1 — the tag view's empty state names the TAG. "Nothing
             here" would be the fourth identical sentence and would not say
             which of the user's tags is the empty one. -->
        <p class="fe-state__title">
          {{ navView === 'tag' ? t('empty.tag.title', { tag: navTag }) : t(`empty.${navView}.title`) }}
        </p>
        <p class="fe-state__hint">
          {{ navView === 'tag' ? t('empty.tag.hint') : t(`empty.${navView}.hint`) }}
        </p>
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
      <!-- surucu:d1 — the folder HAS rows and the filters hid all of them.
           "This folder is empty" would be false, and the way back is the chip
           row just above, so the message names it and offers the button. -->
      <div
        v-else-if="!loading && filtersOn && displayFiles.length === 0 && files.length > 0"
        class="fe-state"
        data-testid="empty-filtered"
      >
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
          <path d="M26 28h68L68 58v24l-16 8V58z" />
        </svg>
        <p class="fe-state__title">{{ t('filter.empty.title') }}</p>
        <p class="fe-state__hint">{{ t('filter.empty.hint') }}</p>
        <div class="fe-state__actions">
          <button type="button" class="fe-btn" @click="clearDriveFilters">
            {{ t('filter.clear') }}
          </button>
        </div>
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
        :files="displayFiles /* surucu:d1 */"
        :selected="selection.selected.value"
        :clipped="clippedPaths"
        :show-parent-path="!!searchQuery"
        :locale="locale"
        :loading="loading"
        :keep-badge-for="desktopSync ? keepBadgeFor : undefined"
        :starred-ids="starredIds"
        :star-enabled="identitySurfaces"
        :api-base="props.config.apiBase ?? ''"
        :auth-headers="() => buildAuthHeaders()"
        :auth-credentials="api.credentialsMode()"
        @click-row="(n, m) => selection.click(n.path, m)"
        @dbl-row="openNode"
        @context-row="onContextTarget"
        @item-drag-start="onItemDragStart"
        @item-drop-into="onItemDropInto"
        @star-change="onStarChange"
      />
      <GridView
        v-else-if="viewMode === 'grid' /* wiring:d2 — v-else → v-else-if (3. mod eklendi) */"
        :files="displayFiles /* surucu:d1 */"
        :sections="driveShell /* surucu:d1 */"
        :selected="selection.selected.value"
        :clipped="clippedPaths"
        :show-parent-path="!!searchQuery"
        :locale="locale"
        :loading="loading"
        :keep-badge-for="desktopSync ? keepBadgeFor : undefined"
        :thumb-src="thumbs.src"
        :starred-ids="starredIds"
        :star-enabled="identitySurfaces"
        :api-base="props.config.apiBase ?? ''"
        :auth-headers="() => buildAuthHeaders()"
        :auth-credentials="api.credentialsMode()"
        @click-card="(n, m) => selection.click(n.path, m)"
        @dbl-card="openNode"
        @context-card="onContextTarget"
        @item-drag-start="onItemDragStart"
        @item-drop-into="onItemDropInto"
        @star-change="onStarChange"
      />
      <!-- wiring:d2 — gallery view (same event contract as GridView) -->
      <GalleryView
        v-else
        :files="displayFiles /* surucu:d1 */"
        :selected="selection.selected.value"
        :clipped="clippedPaths"
        :show-parent-path="!!searchQuery"
        :locale="locale"
        :loading="loading"
        :thumb-src="thumbs.src"
        :starred-ids="starredIds"
        :star-enabled="identitySurfaces"
        :api-base="props.config.apiBase ?? ''"
        :auth-headers="() => buildAuthHeaders()"
        :auth-credentials="api.credentialsMode()"
        @click-card="(n, m) => selection.click(n.path, m)"
        @dbl-card="openNode"
        @context-card="onContextTarget"
        @item-drag-start="onItemDragStart"
        @item-drop-into="onItemDropInto"
        @star-change="onStarChange"
      />
      <!-- /wiring:d2 -->
    </div>
    </div><!-- /fe__primary ui-fix -->

    <!-- wiring:d1 — per-tab split: the secondary pane on the right (off in
         narrow mode). :key is bound to the tab id — on a tab switch the pane
         remounts cleanly with its own location. -->
    <SecondaryPane
      :keep-badge-for="desktopSync ? keepBadgeFor : undefined"
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
      :trash-visible="config.trashVisible !== false /* ui-fix — trash row symmetry */"
      :nav-offers-trash="navOffersTrash /* surucu:d1 — the same door is not opened twice */"
      @navigate="onPaneNavigate"
      @activate="activePane = 'split'"
      @close="closeSplit"
      @open-tab="(p: string) => tabsApi.openTab(p, { viewMode: viewMode, background: true })"
      @transfer="onPaneTransfer"
      @context="onPaneContext /* ui-fix — side-pane right-click menu */"
      @open-trash="onPaneOpenTrash /* ui-fix — the trash opens in the main pane */"
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
      :tabs="driveShell /* surucu:d1 */"
      @close="closeInspector"
      @share-created="onInspectorShareCreated /* surucu:d1 */"
      @manage-permissions="onInspectorManage"
      @toast="flashToast"
      @changed="() => load()"
    />
    </div>
    <!-- /koru:k1 fe__main -->

    <!-- gezinti:g1 — the Connections / API-keys overlays, opened from the
         navigation panel. ⚠ z-index 130, measured, not guessed: the explorer's
         onboarding tour and its context menus are appended to <body> at 96 and
         90 and are `fixed`, so anything in the normal stacking order is painted
         over by them — the tour card landed on top of the same panel in the web
         app and swallowed its clicks, which is why that page uses z-[120]. This
         one has to clear the host's wrapper too, so it goes above it. -->
    <div
      v-if="showConnections || showTokens"
      class="fe-overlay"
      data-testid="explorer-overlay"
      @click.self="closeOverlays"
    >
      <div class="fe-overlay__card" @click.stop>
        <ConnectionsPanel
          v-if="showConnections"
          :config="config"
          initial-tab="connect"
          closable
          @close="closeOverlays"
          @changed="() => load()"
          @error="onConnectionsError"
        />
        <template v-else>
          <header class="fe-overlay__head">
            <h2 class="fe-overlay__title">{{ t('sidenav.apikeys') }}</h2>
            <button
              type="button"
              class="fe-overlay__close"
              :title="t('overlay.close')"
              :aria-label="t('overlay.close')"
              @click="closeOverlays"
            >
              ×
            </button>
          </header>
          <TokensPanel :config="config" full />
        </template>
      </div>
    </div>


    <div v-if="dragOver" class="fe__dragover">
      <div class="fe__dragover-card">
        <span class="fe-icon">⬆</span>
        <p>{{ t('dropzone.hint') }}</p>
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

    <!-- selective-sync progress: the folder the engine is moving RIGHT NOW.
         Overlay, pointer-events none — status is never in the way of work. -->
    <div v-if="keepActive" class="fe-keep-strip" role="status" aria-live="polite">
      <span class="fe-keep-strip__icon" aria-hidden="true">⟳</span>
      <span class="fe-keep-strip__label">{{ keepStripLabel }}</span>
      <div v-if="keepStripPercent !== null" class="fe-keep-strip__bar" aria-hidden="true">
        <div class="fe-keep-strip__fill" :style="{ width: keepStripPercent + '%' }"></div>
      </div>
    </div>

    <ContextMenu
      ref="ctxRef"
      :locale="locale"
      :theme="themeMode"
      :sheet="isCoarse /* bag:b4 */"
      :actions="contextActions"
      @select="onContextAction"
    />

    <NewFolderModal
      :open="showNewFolder"
      :locale="locale"
      :encrypted-option="!e2eActive /* wiring:e2 — no nested encrypted folders */"
      @close="showNewFolder = false"
      @submit="submitNewFolder"
      @encrypted="showNewFolder = false; showEncFolder = true /* wiring:e2 */"
    />
    <!-- wiring:e2 — encrypted-folder creation modal -->
    <EncryptedFolderModal
      :open="showEncFolder"
      :locale="locale"
      :busy="e2eCreateBusy"
      :escrow-kid="e2eEscrowKid"
      @close="showEncFolder = false"
      @submit="submitEncryptedFolder"
    />
    <!-- wiring:e2 recovery — the key, shown exactly once. -->
    <RecoveryKeyModal
      :open="showRecoveryKey"
      :locale="locale"
      :recovery-key="recoveryKeyValue"
      :folder-name="recoveryKeyFolder"
      :escrow-kid="e2eEscrowKid"
      :variant="recoveryKeyVariant"
      @close="closeRecoveryKey"
    />
    <!-- wiring:e2 recovery — the way back in without the password. -->
    <E2eRecoveryUnlockModal
      :open="showRecoveryUnlock"
      :locale="locale"
      :has-recovery="markerHasRecovery(e2eMarker)"
      :escrow-state="escrowAvailability(e2eMarker, e2eEscrowKid)"
      :escrow-kid="e2eMarker?.esc?.kid || e2eEscrowKid"
      :busy="e2eRecoverBusy"
      :error="e2eRecoverErr"
      @close="showRecoveryUnlock = false"
      @submit="e2eRecoverUnlock"
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
      :share-max-ttl-days="shareMaxTtlDays"
      @close="closeShare"
      @submit="submitShare"
      @toast="flashToast"
    />
    <PreviewModal
      :open="showPreview"
      :locale="locale"
      :file="previewTarget"
      :theme="themeMode"
      :preview-url="(p) => e2ePreviewSrc(p) /* wiring:e2 — decrypted blob > raw URL */"
      :download-url="(p) => (e2eUnlocked ? e2ePreviewSrc(p) : api.downloadUrl(p)) /* wiring:e2 */"
      :only-office-base="e2eActive ? null : effectiveOnlyOfficeBase /* wiring:e2 — OO cannot open ciphertext */"
      :only-office-config-endpoint="effectiveOnlyOfficeConfigEndpoint"
      :new-tab-enabled="!e2eActive /* wiring:e2 — the standalone route pulls raw bytes */"
      :save-text-endpoint="e2eActive ? null : api.endpoints.saveText || null /* wiring:e2 — a plaintext save would be a leak */"
      :open-mode="previewMode"
      :auth-headers="() => buildAuthHeaders({ 'Content-Type': 'application/json' })"
      :auth-credentials="api.credentialsMode()"
      :drawio-url="effectiveDrawioUrl"
      :pdf-worker-url="props.config.pdfWorkerUrl || null"
      :pdf-save-url="props.config.pdfSaveUrl || null"
      :viewer-base-url="effectiveViewerBaseUrl"
      @close="showPreview = false"
    />
    <ConvertModal
      v-if="showConvert && convertTarget && effectiveConvertUrl"
      :convert-url="effectiveConvertUrl"
      :file-name="convertTarget?.basename || convertTarget?.path || ''"
      :fetch-bytes="() => api.fetchArrayBuffer(convertTarget?.path ?? '')"
      :upload="(f) => api.uploadMultipart(qualify(currentPath), [f]).then(() => {})"
      :locale="locale"
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
      :share-max-ttl-days="shareMaxTtlDays"
      :initial-tab="permInitialTab /* surucu:d1 */"
      @close="showPerm = false; permInitialTab = undefined"
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
          'fe--theme-light': themeMode === 'light',
          'fe--theme-dark': themeMode === 'dark',
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
            :auth-credentials="api.credentialsMode()"
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
              :auth-credentials="api.credentialsMode()"
              @change="onNodeTagsChanged"
              @error="(msg: string) => emit('error', { message: msg, context: { op: 'tags' } })"
            />
          </div>
        </div>
      </div>
    </transition>

    <!-- cila:c wiring — command palette (Ctrl/Cmd+K) + shortcuts help (?) -->
    <CommandPalette
      :initial-query="paletteSeed /* surucu:d1 */"
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
      @toggle-view="setDisplayedViewMode(displayedViewMode === 'list' ? 'grid' : displayedViewMode === 'grid' ? 'gallery' : 'list') /* wiring:d2 + ui-fix — 3-mode cycle, to the active pane */"
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
      :theme="themeMode"
      :dark="themeResolvedDark"
      :current="activeThemeId"
      :mode="themeModePref"
      :host-mode="config.theme || 'auto'"
      @close="showThemeGallery = false"
      @select="setActiveTheme"
      @mode="(m: ThemeModePref) => setThemeModePref(m)"
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
      :theme="themeMode"
      @close="showShortcutSettings = false"
    />
    <QuickLook
      :open="quickLookOpen"
      :locale="locale"
      :file="quickLookTarget"
      :theme="themeMode"
      :preview-url="(p: string) => e2ePreviewSrc(p) /* wiring:e2 */"
      :download-url="(p: string) => (e2eUnlocked ? e2ePreviewSrc(p) : api.downloadUrl(p)) /* wiring:e2 */"
      :only-office-base="e2eActive ? null : effectiveOnlyOfficeBase /* wiring:e2 */"
      :only-office-config-endpoint="effectiveOnlyOfficeConfigEndpoint"
      :auth-headers="() => buildAuthHeaders({ 'Content-Type': 'application/json' })"
      :auth-credentials="api.credentialsMode()"
      :drawio-url="effectiveDrawioUrl"
      :pdf-worker-url="props.config.pdfWorkerUrl || null"
      :viewer-base-url="effectiveViewerBaseUrl"
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
      :theme="themeMode"
      @close="onTourClose"
    />
    <!-- /wiring:c4 -->
  </div>
</template>

<style src="./styles/variables.css"></style>
<style src="./styles/base.css"></style>
