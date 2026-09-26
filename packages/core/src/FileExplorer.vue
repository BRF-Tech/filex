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
import { computed, nextTick, onBeforeUnmount, onMounted, onScopeDispose, provide, ref, watch, watchEffect } from 'vue';
import type { ExplorerConfig, SearchAccount, ThemeMode } from './types/ExplorerConfig';
import type {
  FileNode,
  ShareInfo,
  ViewMode,
  ClipboardState,
  Capabilities,
  ArchiveCreateFormat,
} from './types/FileNode';
import { isExternalUsable } from './types/FileNode';
import { useFileApi, type GlobalSearchHit, type ManagerResponse, type PendingOpDto, type QuotaSnapshot } from './composables/useFileApi';
import {
  useUploadChunked,
  isStagedUnsupported,
  type UploadJob,
} from './composables/useUploadChunked';
import { useSelection } from './composables/useSelection';
import { useKeyboardShortcuts } from './composables/useKeyboardShortcuts';
import { EXPLORER_LOCALE, useLocale, localeTag } from './composables/useLocale';
import { useSystemDark } from './composables/useSystemDark';
import { usePendingOps, type PendingOp } from './composables/usePendingOps';
import { usePluginActions } from './composables/usePluginActions'; /* App plugins — docs/APP-PLUGINS-API.md */
import { useRealtime } from './composables/useRealtime';
import { useThumbs } from './composables/useThumbs';
import { preloadEditor } from './composables/useMonacoLoader';
import PresenceBar from './components/PresenceBar.vue';

import Toolbar, { type SelectionMode } from './components/Toolbar.vue';
import StarButton from './components/StarButton.vue';
import TagPicker from './components/TagPicker.vue';
import {
  activeSortDir,
  activeSortKey,
  applySort,
  defaultSort,
  defaultSortDir,
  setSortLocale,
  type ListingOrder,
} from './lib/sortOrder'; /* surucu:d1-sort */
import {
  ROOT_FOLDER_KEY,
  defaultFolderView,
  folderIsRemembered,
  folderKey as makeFolderKey,
  onViewPrefsApplied,
  rememberFolder,
  resolveFolderView,
  setPersonFolderDefault,
  touchFolder,
  viewPrefsReady,
} from './lib/viewPrefs'; /* tablo:t1 */
import { attachViewPrefsHttp } from './lib/viewPrefsHttp';
import { provideTableEnv } from './lib/tableEnv';
import { gateOnService, isOfficeExt, legacyConvertGate } from './lib/serviceGate';
import { opFailure, sayFailure } from './lib/errorWords';
import { resolveUiProfile } from './lib/uiProfile';
import RecentlyOpened from './components/RecentlyOpened.vue';
import {
  EMPTY_FILTERS,
  applyFilters,
  filtersActive,
  type DriveFilters,
} from './lib/fileFilters' /* surucu:d1 */;
import ContextMenu, { type ContextAction } from './components/ContextMenu.vue';
import UploadProgress from './components/UploadProgress.vue';
import PendingOpsTray from './components/PendingOpsTray.vue';
import InspectorPanel from './components/InspectorPanel.vue'; /* koru:k1 */
import SideNav, { type NavDest } from './components/SideNav.vue'; /* gezinti:g1 */
import HomeView from './components/HomeView.vue'; /* gorunum:v3-shell */
import ConnectionsPanel from './components/ConnectionsPanel.vue'; /* gezinti:g1 */
import TokensPanel from './components/TokensPanel.vue'; /* gezinti:g1 */
/* cila:c wiring */
import CommandPalette from './components/CommandPalette.vue';
import AdvancedSearch from './components/AdvancedSearch.vue' /* gorunum:v1-advsearch */;
import {
  advQueryString,
  advSearchTruncated,
  type AdvCountResult,
  type AdvScope,
  type AdvSearchRequest,
} from './lib/advSearch' /* gorunum:v1-advsearch */;
import ShortcutsHelp from './components/ShortcutsHelp.vue';
/* /cila:c wiring */
import { coverageByStorage, coverageNotice, type CatalogCoverage } from './lib/catalogCoverage';
import { needsMeasuredDrives, storageLine, type MeasuredDrive } from './lib/storageLine'; /* surucu:d1 — which number the storage line prints */
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
/* zaman:z3 — the embed's own time-zone setting + this instance's tiers */
import TimeZoneDialog from './components/TimeZoneDialog.vue';
import { useExplorerTimeZone } from './composables/useExplorerTimeZone';
import { connectionsBase } from './composables/useConnections';
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
import { markTourSeen, offerTourOnce } from './lib/tour'; /* the tour is offered to a person once */
import { orderStorages, saveStorageOrder, useStorageOrder } from './lib/storageOrder'; /* #57 — the person's storage order */
/* /wiring:c4 */
/* wiring:d1 — tabs + per-tab split */
import TabBar from './components/TabBar.vue';
import FilePane from './components/FilePane.vue';
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
  isVirtualViewPath,
  makeTagSegment,
  tagOfPath,
  tagKindOfPath,
  showHiddenFiles,
  setShowHiddenFiles,
  injectTrashRow,
  hydrateTrashRow as hydrateTrashRowShared,
} from './lib/listing';
import { nodeRowToFileNode as nodeRowToFileNodePure } from './lib/nodeRow'; /* Recent / Starred / tag / Home rows — one shape, with `perm` + `read_only` */
import { iconFamilyFor, isStorageRow } from './lib/fileIcons'; /* pane:p1 — the storage-row predicate's one home */
import { openSurface } from './lib/openSurface';
import { actionIconSvg } from './lib/actionIcons'; /* inceleme:r1 — the drop overlay's mark, off the emoji font */
import { convertAppOffered, isPluginActionKey, pluginActionKey, pluginMenuRows } from './lib/pluginMenu'; /* App plugins — the menu block, pure */
import { isPagePlacement, pluginPageUrl } from './lib/pluginPage'; /* App plugins — a `page` view opens in a new tab */
import { lockOf, lockWords, lockedRefusal } from './lib/appLock'; /* App plugins — an app's hold on a file */
import { linkWordsFor } from './lib/symlink'; /* issue #34 — a link the server will not follow */
import { labelOf as pluginLabelOf } from './lib/pluginLabel';
import type { PluginActionRow, PluginSurface, PluginViewRow } from './types/Plugins';
import type { NavApp } from './components/SideNav.vue';
import { setNodeStarred } from './lib/star';
import { emptyTrashAndFollow, TrashEmptyBusy, type TrashEmptyStatus } from './lib/trashEmpty';
import { fetchAllTags, fetchTaggedRows, onTagsChanged, type TagItem, type TagKind } from './lib/tags';
import { resolveTransfer, type TransferIntent } from './lib/transfer';
import { downloadArchive, requestFileLink } from './lib/downloadSelection'; /* tasi:m1, #71 */
import { hitItem, hitRelPath, hitStorageName, hitToNode, type HitDriveContext } from './lib/searchHit'; /* #47 */
import { labelOfWire } from './lib/destinationTree'; /* tasi:m1 */
import {
  activeNativeDrag,
  beginNativeDrag,
  canDownloadUrlDrag,
  createDragLinks,
  dragKey,
  downloadUrlPayload,
  endNativeDrag,
  hasInternalDrag,
  internalDragItems,
  internalDragOrigin,
  type DragItem,
} from './lib/dragOut';

import NewFolderModal from './modals/NewFolderModal.vue';
import ArchiveCreateModal from './modals/ArchiveCreateModal.vue';
import ArchiveExtractModal from './modals/ArchiveExtractModal.vue';
import ArchivePasswordModal from './modals/ArchivePasswordModal.vue';
import NewDocumentModal from './modals/NewDocumentModal.vue'; /* belge:n1 */
import RenameModal from './modals/RenameModal.vue';
import DeleteConfirmModal from './modals/DeleteConfirmModal.vue';
import Modal from './modals/Modal.vue'; /* tablo:t1 — the empty-trash confirmation */
import PreviewModal from './modals/PreviewModal.vue';
import ConvertModal from './modals/ConvertModal.vue';
import PluginViewModal from './components/plugin/PluginViewModal.vue'; /* App plugins */
import PluginConfirmModal from './components/plugin/PluginConfirmModal.vue';
import PermissionsModal from './modals/PermissionsModal.vue';
import DestinationPickerModal from './modals/DestinationPickerModal.vue'; /* tasi:m1 */
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
  /**
   * gorunum:v2-topbar — the user asked for a refresh (the header's button or
   * the palette's command; both reach `refreshAll`).
   *
   * The explorer reloads the LISTING itself; this is for the half it cannot
   * know about. `config.storages` is the host's answer to "which drives may I
   * show you", computed before the explorer was mounted, and nothing inside
   * here can recompute it — so a drive added from somewhere else stayed
   * invisible until the whole page was reloaded. The Explore page used to
   * paper over that with a Refresh button of its own in the page bar; the bar
   * is gone, so the one Refresh has to mean both halves.
   *
   * ⚠ A notification, not a request: the explorer does not wait for the host
   * and does not care whether it does anything. An embedder with a fixed
   * storage list simply ignores it.
   */
  (e: 'refresh'): void;
  /**
   * paylas:m1 — the navigation panel's "My shares" row was pressed.
   *
   * ⚠ Passed through, not acted on. "How to connect" and "API keys" are
   * surfaces this component OWNS, so it opens them itself; the list of links a
   * person minted is a page of the host application (the SPA's `my-shares`
   * route), and the explorer has no business deciding what a host's URLs look
   * like.
   *
   * ⚠ Which is why the row is behind `config.mySharesVisible` and that flag is
   * OFF by default: an embedder who does not listen would otherwise be given
   * the one row in the panel that leads nowhere. Listening and setting the
   * flag are the same decision, made once — see the note on the row itself in
   * SideNav.vue.
   */
  (e: 'open-my-shares'): void;
  /**
   * An "Apps" row was pressed and the host draws app home pages itself
   * (`config.appHomePage`): the plugin and its `home` view. ⚠ Passed
   * through, like `open-my-shares` — the page and its address are the
   * host's.
   */
  (e: 'open-app-home', p: { plugin: string; view: string }): void;
}>();

// --------------------------------------------------------------------
// State
// --------------------------------------------------------------------

const api = useFileApi(props.config);

// Locale up-front: the pendingOps onSettled callback below (and the undo-toast
// helpers) need `t()` at runtime, so the catalogue must be constructed before
// they are wired. Depends only on props — safe this early.
const locale = computed(() => resolveLocale(props.config.locale));
// Every dialog under this explorer speaks its language (EXPLORER_LOCALE).
provide(EXPLORER_LOCALE, () => locale.value);
/* surucu:d1-sort — the alphabet the `type` key sorts in (lib/sortOrder sorts
 * by the word the Type column PRINTS, so "Image" and "Görsel" each fall in
 * their own order).
 * ⚠ Pushed from HERE as well as from FilterBar and ListView, and that is not a
 * third copy of a decision — it is one value, pushed by the component that
 * always exists. This file is now the one that sorts; the filter row is absent
 * at a virtual root and the list is absent in grid and gallery, so relying on
 * either would leave the comparator on a stale alphabet exactly when they are
 * not mounted. */
watch(locale, (l) => setSortLocale(l), { immediate: true });
const { t, formatSize, formatDate, dir } = useLocale(locale); /* tablo:t1 — the empty-trash confirmation names the space; `formatDate` reads an app lock's end on the user's clock */
// ⚠ RTL — `dir` goes on the root: the explorer's direction is its OWN locale's,
// never the host page's (lib/direction has the rule and why).

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
    /* "Empty the trash" says how it ended itself (emptyTrash below), and a
     * row somebody cancelled is not announced as done. */
    if (op.op_type === 'trash-empty') {
      void load();
      return;
    }
    if (op.status === 'cancelled') {
      const message = op.op_type === 'archive-extract'
        ? t('archive.extraction_cancelled', { count: op.progress_done })
        : t('opc.status.aborted');
      flashToast(message);
    } else if (op.status === 'error' && op.op_type === 'restore' && op.progress_done > 0) {
      // A restore that brought some entries back says how many, and why the
      // rest did not come.
      flashToast(
        t('toast.restore_partial', {
          n: op.progress_done,
          failed: op.progress_total - op.progress_done,
          reason: opFailure(op, t).text,
        }),
      );
    } else if (op.status === 'error') {
      // Said, not printed: the server's error text is English and sometimes
      // plumbing ("engine libreoffice is not installed on this host").
      flashToast(opFailure(op, t).text);
    } else if (undo) {
      // A rename is one item: no count after it.
      undoToast(op.op_type === 'rename' ? undo.message : `${undo.message} (${op.progress_total})`, undo.fn);
    } else if (op.op_type === 'plugin') {
      /* App plugins — the job's own last words, else "<label> finished". */
      flashToast(op.message || t('plugin.done', { label: pluginOpLabel(op) }));
    } else if (op.op_type === 'rename') {
      flashToast(t('toast.renamed'));
    } else if (op.op_type === 'restore') {
      flashToast(t('toast.restored', { n: op.progress_done }));
    } else {
      const verb =
        op.op_type === 'archive-create'
          ? t('archive.created')
          : op.op_type === 'archive-extract'
            ? t('archive.extraction_completed')
            : op.op_type === 'copy'
              ? t('toast.copied')
              : op.op_type === 'move'
                ? t('toast.moved')
                : t('toast.deleted');
      flashToast(op.op_type.startsWith('archive-') ? verb : `${verb} (${op.progress_total})`);
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
/** The listed folder sits on a read-only storage (`read_only` on the index
 *  response). Folded into permCanEdit so every write affordance — toolbar,
 *  sidebar menu, context menu, drop zone, editor mode — reads the same fact. */
const dirReadOnly = ref(false);
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

/**
 * The first-paint cache of the DEFAULT view mode (`lib/viewPrefs` writes it
 * from the person's or the instance's default). ⚠ Read here, never written:
 * see below.
 */
const VIEW_MODE_KEY = 'brf-file-explorer:view-mode';
/**
 * The view mode on screen.
 *
 * ⚠⚠ Setting it writes NOTHING global, and that is the fix for the owner's
 * report of 2026-09-21 — "Explore içindeki değişikliklerimiz o klasör özelinde
 * olmalı; tüm klasörlerde görünüm değişikliği geçerli oluyor." It used to
 * write `brf-file-explorer:view-mode` AND the account's global default on
 * every change, under the rule "your last choice becomes the default for every
 * folder you have never set up": switch folder A to grid and folder B, never
 * touched, came up as grid. A change is now recorded against the folder it was
 * made in (the recorder below) and nowhere else; the default is set on purpose
 * in the person's settings or by the operator.
 */
const viewMode = ref<ViewMode>(
  (() => {
    try {
      const stored = localStorage.getItem(VIEW_MODE_KEY);
      if (stored === 'list' || stored === 'grid' || stored === 'gallery') return stored; /* wiring:d2 */
    } catch {
      /* private mode */
    }
    return props.config.viewMode ?? 'list';
  })(),
);
/* cila:a density — Toolbar owns the persisted preference (filex.density);
   mirrored here only so the root `.fe` can carry fe--density-compact. */
const density = ref<'comfortable' | 'compact'>('comfortable');
const searchQuery = ref('');
/** The search answer on screen was cut: more rows matched than came back
 *  (`truncated` on the response; lib/advSearch `advSearchTruncated`). Reset by
 *  every load(), so it can only ever describe the listing that is showing. */
const searchTruncated = ref(false);
/** The manager's search action asks the index for this many hits
 *  (handlers.Manager, managerSearchPage) — the page an older server's full
 *  answer is recognised by. */
const MANAGER_SEARCH_PAGE = 250;
/**
 * How much of each storage its catalog covers, from the last response that
 * said (`storage_info` on every listing and search — lib/catalogCoverage).
 * Kept across loads: the content search (`/api/files/search`) does not carry
 * it, and its banner still has to name a storage the catalog does not cover.
 */
const coverageMap = ref<Record<string, CatalogCoverage | null>>({});
const catalogAllBusy = ref(false);
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
type NavView = '' | 'home' | 'recent' | 'starred' | 'shared' | 'trash' | 'tag';
const navView = ref<NavView>('');
/** Where the view was entered from, so "up" goes back there. */
const navViewOrigin = ref<string>('');

/**
 * gorunum:v1 — when a row's NAME is not enough to know where it is.
 *
 * Recent, Starred, Shared with me and a tag view each draw rows gathered from
 * every folder in the storage, so two files called `report.pdf` are two
 * identical lines. The listing already knows how to print a row's folder — it
 * did it for search hits only. Trash is left out on purpose: a trashed row's
 * stored path is its trash key, not the folder it came from, so the column
 * would print an internal name.
 */
const crossFolderView = computed(
  () => navView.value !== '' && navView.value !== 'trash' && navView.value !== 'home',
);

/* === tablo:t1 — per-folder view memory ==================================
 * "x folder'ında son görünüm nasıl kaldı ise öyle görünümde göstermemiz
 * lazım." A folder opens the way you left it. The rule, the cap and the split
 * between what is per-folder and what is a global preference are all argued in
 * `lib/viewPrefs`; this is only the wiring — the two moments the explorer is
 * the one that knows something: a navigation ended, and a person changed a
 * view.
 */

/** An embed's opt-out. A product mounting filex in a two-inch panel does not
 *  want a remembered gallery view arriving from somebody's main window. */
const folderMemoryOn = computed(() => props.config.rememberFolderView !== false);

/**
 * The key the folder on screen is remembered under.
 *
 * `currentPath` is already the qualified `<storage>/<rel>` form, so the first
 * segment is the storage ref — and the ref is swapped for the storage's
 * immutable `uid` when the host supplies one (`config.storages[].uid`), which
 * is the difference between a memory that survives a rename and one that does
 * not. See the note on `folderKey` for what happens until it does.
 *
 * The virtual views (`.recent`, `.starred`, `.tag~x`) have no storage and so
 * key on their own sentinel — which is how Recent gets a remembered sort of
 * its own without a special case anywhere.
 */
const currentFolderKey = computed(() => {
  if (!folderMemoryOn.value) return '';
  const path = String(currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  /* The listing of every storage is a place too: a person who sorts their
     drives expects them to stay sorted, and `''` would mean "no folder", whose
     changes land on the person's DEFAULT. */
  if (!path) return ROOT_FOLDER_KEY;
  const [first, ...rest] = path.split('/');
  const st = (props.config.storages ?? []).find((s) => s.name === first);
  return makeFolderKey(st?.uid || first, rest.join('/'));
});

/**
 * The DEFAULT view mode — what a folder nobody has configured opens as: the
 * person's default, else the instance's (`lib/viewPrefs.defaultFolderView`),
 * else this host's `config.viewMode`, else list. Until the account's document
 * has landed, this browser's cached copy of that answer stands in for it.
 */
function defaultViewMode(): ViewMode {
  const d = defaultFolderView().v;
  if (d) return d;
  if (!viewPrefsReady()) {
    try {
      const stored = localStorage.getItem(VIEW_MODE_KEY);
      if (stored === 'list' || stored === 'grid' || stored === 'gallery') return stored;
    } catch {
      /* private mode */
    }
  }
  return props.config.viewMode ?? 'list';
}

/**
 * The stored document landed — or another browser's newer one did when this
 * tab came back to the front, or the operator's default arrived with it.
 * Re-apply the folder on screen: its own memory wins, and a folder with none
 * follows whatever the default now is.
 *
 * ⚠ Unsubscribed with the component: the registry is module-level and two
 * explorers can be mounted at once.
 */
onScopeDispose(
  onViewPrefsApplied(() => {
    if (!viewPrefsReady()) return;
    applyFolderView(currentFolderKey.value);
  }),
);

/**
 * What the folder on screen was last put into — by the applier, or by the
 * recorder after it wrote a change down. The recorder compares against it to
 * tell a person's change from a restore's echo, and to know WHICH field
 * changed.
 *
 * ⚠⚠ A record and not a `restoring = true … false` fence: Vue's watchers are
 * asynchronous, so a fence around an assignment is already back down by the
 * time the recorder runs, and every navigation would record itself as a
 * deliberate choice — walking through a folder would configure it.
 */
let applied: { key: string; v: ViewMode; k: string; d: string } | null = null;

/** A navigation ended (or the defaults changed): put this folder back the way
 *  it was left — its own memory, else the default, field by field. */
function applyFolderView(key: string) {
  if (!key && folderMemoryOn.value) return;
  touchFolder(key); // LRU clock — only bumps folders already remembered
  const p = key ? resolveFolderView(key) : {};
  /* ⚠ The `else` halves matter as much as the `if`s. Without them a folder
   * with no memory of its own would inherit whatever the PREVIOUS folder was
   * put into — walk from a remembered gallery into a plain folder and it
   * comes up as a gallery, which is the leak wearing a different hat. */
  const wantView = p.v ?? defaultViewMode();
  if (wantView !== viewMode.value) viewMode.value = wantView;
  if (p.k) applySort(p.k, p.d ?? defaultSortDir(p.k));
  else {
    const g = defaultSort();
    applySort(g.key, g.dir);
  }
  applied = { key, v: viewMode.value, k: activeSortKey(), d: activeSortDir() };
}

/**
 * tablo:t1 — hand `lib/viewPrefs` its transport.
 *
 * The document lives on the user row (migration 00039), so this is the one
 * place that knows the base URL, the auth headers and the credentials mode.
 * Started in `onMounted`, i.e. in the same turn as the first listing — the
 * prefs are a single row and the listing has to walk a storage, so the prefs
 * land first in practice, and nothing is applied to a folder until they do.
 */
onMounted(() => {
  /* The one transport (`lib/viewPrefsHttp`), shared with the admin app. A
   * no-op when the host already attached it — which the admin app does on
   * sign-in, because its tables keep their columns in the same document. */
  attachViewPrefsHttp({
    apiBase: props.config.apiBase ?? '',
    headers: buildAuthHeaders,
    credentials: api.credentialsMode(),
  });
});

/* ⚠ `flush: 'post'` so this is the LAST word in the tick. A tab switch sets
 * the path and the tab's own remembered view mode in the same turn; running
 * before it would apply the folder's memory and then have the tab overwrite
 * it, which is the one arrangement in which the feature silently does nothing
 * on exactly the gesture people use most.
 *
 * ⚠⚠ `viewPrefsReady()` is a DEPENDENCY, not a guard, and that is what stops
 * the flash the other way round: the first folder is usually open before the
 * document lands, so this has to re-run when it does. Reading it here means
 * the applier fires once more the moment the answer exists, and the folder
 * settles into its remembered view without anybody having navigated again. */
watch(
  [currentFolderKey, () => viewPrefsReady()],
  ([key, rdy]) => {
    if (!rdy) return;
    applyFolderView(key);
  },
  { immediate: true, flush: 'post' },
);

/**
 * A person changed a view. Record it against THE FOLDER — and only the field
 * that changed: switching to grid records the view mode and leaves the
 * folder's sort following the default, so a default sort chosen later still
 * reaches it.
 *
 * ⚠⚠ Nothing global is written here or anywhere else on a click. This is the
 * fix for "tüm klasörlerde görünüm değişikliği geçerli oluyor".
 *
 * ⚠ A host that switched the per-folder memory OFF (`rememberFolderView:
 * false`) has no folder to record against (`currentFolderKey` is ''), and asked
 * for one arrangement everywhere — so for it, and only for it, a change is the
 * person's default.
 */
watch(
  () => [currentFolderKey.value, viewMode.value, activeSortKey(), activeSortDir()] as const,
  ([key, v, k, d]) => {
    /* ⚠ Nothing is recorded before the document has landed: until then the
     * state on screen is this session's defaults, not the person's choices. */
    if (!viewPrefsReady()) return;
    if (!applied || applied.key !== key) return; // a navigation — the applier's turn
    const patch: { v?: ViewMode; k?: typeof k; d?: typeof d } = {};
    if (v !== applied.v) patch.v = v;
    if (k !== applied.k || d !== applied.d) {
      patch.k = k;
      patch.d = d;
    }
    applied = { key, v, k, d };
    if (!Object.keys(patch).length) return;
    if (key) rememberFolder(key, patch);
    else if (!folderMemoryOn.value) setPersonFolderDefault(patch);
  },
  /* ⚠⚠ `post`, and registered AFTER the applier: a navigation changes
   * `currentFolderKey`, which both watchers depend on. Pre-flush this would
   * run first, while the view state is still the folder you LEFT, and record
   * that folder's setup against the one you arrived in. */
  { flush: 'post' },
);

/* "Forget this folder's view" (the column menu): put the folder on screen
 * back onto the default straight away, rather than at the next navigation. */
watch(
  () => folderIsRemembered(currentFolderKey.value),
  (now, was) => {
    if (was && !now && viewPrefsReady()) applyFolderView(currentFolderKey.value);
  },
);
/** The tag being browsed while navView === 'tag' ('' otherwise). */
const navTag = ref<string>('');
/** …and which KIND of it (etiket:k2, v0.43): 'personal', 'team', or '' for
 *  both — what a `#.tag~x` link from before kinds existed still opens. */
const navTagKind = ref<TagKind | ''>('');
/** Sentinel parked in `dirname` so the breadcrumb can label the view. The tag
 *  view's sentinel is built per tag (`makeTagSegment`) — see lib/listing. */
const NAV_VIEW_DIRNAME: Record<Exclude<NavView, '' | 'trash' | 'tag'>, string> = {
  home: '.home',
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
function virtualViewOf(
  path: string,
): { kind: Exclude<NavView, ''>; tag: string; tagKind: TagKind | '' } | null {
  /* ⚠ The "is this a sentinel at all?" half is `lib/listing`'s
     `isVirtualViewPath`, not a second reading of the map here: FilePane has to
     answer the same question before it qualifies a path (a qualified sentinel
     becomes an adapter and stops being translatable), and two answers to it is
     how the breadcrumb ended up printing `.starred`. This function adds only
     what the panel needs on top: WHICH view. */
  if (!isVirtualViewPath(path)) return null;
  const clean = String(path ?? '').replace(/^\/+|\/+$/g, '');
  const tag = tagOfPath(clean);
  if (tag) return { kind: 'tag', tag, tagKind: tagKindOfPath(clean) };
  const kind = clean.slice(1) as Exclude<NavView, '' | 'tag'>;
  return { kind, tag: '', tagKind: '' };
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
  //
  // ⚠ gorunum:v3-shell — Home is in that set, and it is now the LANDING view,
  // so the "+ New" menu opens with its three rows disabled on the first screen
  // a person sees. That is deliberate and it is not a bug to "fix" by picking
  // a drive: with several storages there is no honest answer to "upload where",
  // and an entry that silently chose one would put somebody's file in a place
  // they did not name. (With exactly one visible storage the question does not
  // arise — `soleStorageName` opens that storage as the root, so "My files" is
  // a real folder.) The reference stand enables it because it has one drive.
  if (navView.value && navView.value !== 'trash') return true;
  if (!multiStorageRoot.value) return false;
  return !((currentPath.value ?? '').replace(/^\/+|\/+$/g, ''));
});

/* surucu:d1-scope — WHERE THE FILTER ROW IS DRAWN, 2026-09-13.
 *
 * ⚠⚠ There is no longer a LIST of places. The row is drawn everywhere, and the
 * only question left is which SHAPE it takes — and that is answered by what the
 * rows are, not by which view you are in:
 *
 *     rows are files  → the whole row (Type · People · Modified · Size · find ·
 *                       sort · ⋮). A folder, the trash, Starred, Shared,
 *                       Recent, a tag — "it is a listing like any other"
 *                       (owner, on the tag view), and the four chips answer
 *                       from fields those rows carry.
 *     rows are not    → the name box alone. The drive list (each row is a
 *                       storage) and Home (three blocks of cards).
 *
 * ⚠ It used to be `!atVirtualRoot`, which is a different question altogether:
 * that flag answers "is there a backend folder here to create in / upload to".
 * Borrowing it cost the row its place in six views at once, including the two
 * the owner asked for it back in ("root folder'da filtre barı kalsın … orada
 * adam isterse storage ismi aratabilir" and "Home sayfasında da filtreleme
 * barını getirelim").
 */
/** Home's body is three blocks of cards, not a listing — name box only. The
 *  drive root reaches the same shape through the pane's own `atVirtualRoot`,
 *  because a pane knows when it is showing drives and both panes can be. */
const filterRowMode = computed<'full' | 'find'>(() =>
  navView.value === 'home' ? 'find' : 'full',
);

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

/**
 * gorunum:v1 — what the ACTIVE view is showing, in the order it shows it.
 *
 * A shift-range is arithmetic over a list, and this used to run it over
 * `files` — the backend's answer — while the user was looking at a sorted or
 * folder-hoisted one. Measured on a seeded storage: shift-clicking the first
 * and fourth visible rows selected eight, because the folder the view had
 * lifted to the top still sat last in `files`. The view now says what it drew
 * (`display-order`) and the range is computed over that; `files` remains the
 * fallback for the moment before the first paint and for surfaces that publish
 * nothing.
 */
const displayOrder = ref<FileNode[]>([]);

/**
 * gorunum:v1 — where the previewed file sits in what the user is looking at.
 *
 * Counted over the DISPLAYED order, not over `files`: the viewer's "3 of 9"
 * and its chevrons have to agree with the listing behind them, and that order
 * is the view's, not the backend's. Directories are skipped — the viewer
 * cannot open one, so counting them would promise a step that does nothing.
 */
const previewables = computed<FileNode[]>(() =>
  (displayOrder.value.length ? displayOrder.value : files.value).filter((n) => n.type !== 'dir'),
);
const previewPosition = computed(() => {
  const list = previewables.value;
  const path = previewTarget.value?.path;
  const i = path ? list.findIndex((n) => n.path === path) : -1;
  return { index: i === -1 ? 0 : i + 1, total: i === -1 ? 0 : list.length };
});
function onPreviewNav(delta: number) {
  const list = previewables.value;
  const i = list.findIndex((n) => n.path === previewTarget.value?.path);
  if (i === -1) return;
  const next = list[i + delta];
  if (next) previewTarget.value = next;
}
const selection = useSelection(() => (displayOrder.value.length ? displayOrder.value : files.value));
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

/**
 * `GET /api/capabilities`, asked once — RETRIED, and AWAITABLE.
 *
 * ⚠⚠ This used to be a fire-and-forget `.then(…).catch(() => {})` in
 * `onMounted`, and that one swallowed catch was a silent, page-wide feature
 * kill. Everything gated on capabilities reads a ref that a failed or slow
 * call simply leaves null: app plugins (`pluginsEnabled` below), OnlyOffice,
 * draw.io, the convert service, new-document types. Nothing retried, so ONE
 * unlucky request — a reload during a backend restart, a proxy hiccup — turned
 * every plugin deep link on that page load into a no-op, for the whole life of
 * the page, with nothing in the console to say why.
 *
 * ⚠⚠ And it was a RACE as well as a failure mode. A notification's deep link
 * arrives from the host (Explore.vue → `openAppTarget`) as soon as the row is
 * revealed, which can be before this answer lands; `openAppTarget` then read
 * `pluginsEnabled` as false and returned. Awaiting THIS PROMISE is the fix —
 * not a delay before trying, which would only move the coin toss.
 *
 * ⚠ `settled`, not `loaded`: a caller waits for the question to be ANSWERED,
 * including "the server would not say". Resolving only on success would hang
 * every awaiting caller on an instance that has no such endpoint.
 */
const CAPABILITIES_TRIES = 3;
let capabilitiesSettled: Promise<void> | null = null;

function loadCapabilities(): Promise<void> {
  if (capabilitiesSettled) return capabilitiesSettled;
  if (!api.endpoints.capabilities) {
    capabilitiesSettled = Promise.resolve();
    return capabilitiesSettled;
  }
  capabilitiesSettled = (async () => {
    for (let attempt = 1; attempt <= CAPABILITIES_TRIES; attempt++) {
      try {
        capabilitiesData.value = await api.capabilities();
        return;
      } catch (e) {
        if (attempt === CAPABILITIES_TRIES) {
          // ⚠ SAID, not swallowed. The host logs this (Explore.vue →
          // onExplorerError) so "my apps disappeared" has an answer in the
          // console instead of being indistinguishable from "this instance
          // has no apps".
          emit('error', {
            message: `capabilities: ${(e as Error)?.message ?? String(e)}`,
            context: { what: 'capabilities', attempts: attempt },
          });
          return;
        }
        await new Promise((r) => setTimeout(r, 200 * attempt));
      }
    }
  })();
  return capabilitiesSettled;
}

/* zaman:z3 — the two clock tiers only an explorer instance can know: the zone
 * its host configured, and the account behind its credential when that
 * credential is a person's. Ranked in lib/timezone, never here. */
const showTimeZone = ref(false);
useExplorerTimeZone({
  config: () => props.config,
  capabilities: capabilitiesData,
  fetchMe: () => api.jsonFetch(`${connectionsBase(props.config)}/api/auth/me`),
});
// Longest life a new share link may be given (server setting, days; 0 = no
// ceiling). Both share dialogs derive their expiry choices from it.
const shareMaxTtlDays = computed(() => capabilitiesData.value?.share_max_ttl_days ?? 0);

/* === App plugins (docs/APP-PLUGINS-API.md) ==============================
 * Rows a WebAssembly plugin adds to the file menu, run as ops jobs. The
 * feature follows `capabilities.app_plugins.enabled` unless the host decides
 * (`config.plugins`); while it is off the explorer makes no plugin request.
 * The actions list is fetched once and shared across instances
 * (usePluginActions); the menu block is pure (lib/pluginMenu). */
const pluginsEnabled = computed<boolean>(() => {
  if (typeof props.config.plugins === 'boolean') return props.config.plugins;
  return capabilitiesData.value?.app_plugins?.enabled === true;
});
const pluginActions = usePluginActions(api, () => pluginsEnabled.value);

/** The open `modal` view: which plugin/view, its first surface, the row it was opened on. */
const pluginView = ref<{
  plugin: string;
  view: string;
  surface: PluginSurface;
  path?: string;
  /** Every row the view was opened on — a menu action on a selection (#64). */
  paths?: string[];
  /** A `home` view is drawn full-size whatever the surface says. */
  size?: 'xl';
} | null>(null);

/** The `inspector` views, for the details panel; `[]` while the feature is off. */
const pluginInspectorViews = computed<PluginViewRow[]>(() =>
  pluginsEnabled.value ? pluginActions.views.value.filter((v) => v.placement === 'inspector') : [],
);

/** The `home` views, as the navigation panel's "Apps" rows. */
const pluginHomeApps = computed<NavApp[]>(() =>
  pluginsEnabled.value
    ? pluginActions.views.value
        .filter((v) => v.placement === 'home')
        .map((v) => ({ key: `${v.plugin}/${v.id}`, label: pluginLabelOf(v.label, locale.value) || v.id, icon: v.icon }))
    : [],
);

/**
 * An "Apps" row: the host's own page for it when it has one (same tab, its
 * own address — `config.appHomePage`), else `GET …/views/{p}/{v}` with no
 * path, drawn full-size in a dialog.
 */
async function openPluginHome(key: string) {
  const view = pluginActions.views.value.find((v) => `${v.plugin}/${v.id}` === key);
  if (!view) return;
  closeNavDrawer();
  if (props.config.appHomePage === true) {
    emit('open-app-home', { plugin: view.plugin, view: view.id });
    return;
  }
  try {
    const res = await api.pluginView(view.plugin, view.id);
    if (!res?.surface) {
      flashToast(t('plugin.view.error'));
      return;
    }
    pluginView.value = { plugin: view.plugin, view: view.id, surface: res.surface, size: 'xl' };
  } catch (e) {
    flashToast((e as Error)?.message || t('plugin.view.error'));
  }
}
/** An action whose manifest asks for confirmation, waiting for the answer. */
const pluginConfirm = ref<{ action: PluginActionRow; targets: FileNode[] } | null>(null);

/** Menu rows for a selection — hidden in the trash, inside an encrypted
 *  folder and (through `selectionActionList`'s early return) on storage rows.
 *  On a read-only storage the actions that write their result are left out
 *  (`pluginActionWrites`): the folder's own flag for rows listed in it, each
 *  row's storage for Recent / Starred / a tag / Home, where no folder is. */
function pluginActionRows(sel: FileNode[]): ContextAction[] {
  if (!pluginsEnabled.value) return [];
  return pluginMenuRows(pluginActions.actions.value, sel, {
    locale: locale.value,
    trash: trashActive.value,
    e2e: e2eActive.value,
    readOnly: dirReadOnly.value || selReadOnly(sel),
    // The level each row answers with — the same one the built-in verbs
    // read — so an app's row a viewer could only be refused is not offered.
    permOf: (n) => rowPerm(n as FileNode),
    hasIcon: (name) => actionIconSvg(name) !== '',
    // Greyed rows with the reason are an ADMINISTRATOR's (the server sends
    // `gated` to them only; asked here too, so no other caller draws one).
    needWords: callerAdmin.value
      ? (need) => (need.kind === 'engine' ? t('plugin.needs_engine', { name: need.name }) : t('plugin.needs_other', { name: need.name }))
      : undefined,
  });
}

/** What a queued plugin job is called in a toast: its label, or the action id. */
function pluginOpLabel(op: PendingOp): string {
  return op.label || op.action || t('opc.kind.plugin');
}

/**
 * Where an app job's result lands — the tray's little "new file" / "new
 * version" chip.
 *
 * ⚠ Read from the CACHED ACTIONS LIST, because the ops row does not carry it:
 * the queue knows the plugin and the action, and the action's manifest output
 * is what the list already holds. Two consequences worth knowing rather than
 * discovering: a hidden action (never listed) and a surface that overrode the
 * output for that one job (`job.output`, which the browser never sees) both
 * answer `undefined`, and the chip is simply not drawn. A wrong chip would be
 * worse than none — it is the difference between "your file was replaced" and
 * "a copy appeared".
 */
function pluginOutputModeOf(op: PendingOp): string | undefined {
  if (!op.plugin || !op.action) return undefined;
  return pluginActions.actions.value.find((a) => a.plugin === op.plugin && a.id === op.action)?.output_mode;
}

/** The menu row was chosen: confirm if the manifest asks, then run. */
async function onPluginAction(key: string, targets: FileNode[]) {
  const action = pluginActions.byKey(key);
  if (!action || targets.length === 0) return;
  const confirm = pluginLabelOf(action.confirm, locale.value);
  if (confirm) {
    pluginConfirm.value = { action, targets };
    return;
  }
  await runPluginAction(action, targets);
}

async function onPluginConfirmed() {
  const pending = pluginConfirm.value;
  pluginConfirm.value = null;
  if (pending) await runPluginAction(pending.action, pending.targets);
}

/**
 * A `page` action: open the view as a whole page in a new tab and DO NOT run
 * anything here. The tab asks for the surface itself (`GET …/views/…?path=`)
 * and the job is born from its own submit — the same `{surface}` conversation
 * a modal has, in a frame that has room for a document.
 *
 * ⚠ Returns false when the host has not said where its pages live
 * (`config.pluginPageBase`) or when the browser refused the tab; the caller
 * then falls back to the dialog. A `page` action that silently does nothing
 * because a pop-up blocker spoke is indistinguishable from a broken app.
 */
function openPluginPage(action: PluginActionRow, targets: FileNode[]): boolean {
  const view = action.view;
  const base = props.config.pluginPageBase;
  if (!view || base === undefined) return false;
  const target = { plugin: action.plugin, view, path: targets[0]?.path };
  const url = pluginPageUrl(base, target);
  if (props.config.openPluginPage?.({ ...target, url }) === true) return true;
  // ⚠⚠ `noopener` is NOT in the feature string, and that is not an oversight.
  // Per the HTML spec a `window.open` that sets `noopener` returns **null**
  // even when the tab opened perfectly — so a caller that reads the return as
  // "was it blocked?" gets a false every single time. Measured: the wizard
  // opened in its own tab AND a dialog appeared behind it, because this
  // function answered false and the caller fell back to `…/run`. The opener
  // is severed on the handle instead, which keeps the guarantee and leaves
  // `null` meaning the one thing worth knowing: a pop-up blocker spoke.
  const win = window.open(url, '_blank');
  if (!win) {
    flashToast(t('plugin.page_view.blocked'));
    return false;
  }
  try {
    win.opener = null;
  } catch {
    /* a browser that will not let us sever it still opened the page */
  }
  return true;
}

/**
 * POST run. `{op}` → the ops tray (same path a copy takes); `{surface}` → the
 * view dialog, whose submit will bring the `{op}` later.
 */
/**
 * A mutation was refused. ⚠⚠ `423` is an APP LOCK, not a permission problem:
 * a named app is holding the file until a named date, and the person is
 * otherwise perfectly allowed to do what they just tried. Dropping it into
 * the generic path told them "Error (423)" and, once a caller mapped it onto
 * the permission text, "you are not allowed to do this" — a sentence they
 * can do nothing with. Every rename / move / delete catch goes through here
 * so there is exactly one place that decides.
 *
 * ⚠⚠ And every other refusal is SAID, on screen. It used to go out as
 * `emit('error')` only, and both first-party hosts write that event to the
 * console (web Explore.vue, desktop app.html): a paste, a drag-move, a
 * duplicate or a copy the server refused looked exactly like one that worked.
 * A dialog that shows the failure itself (rename, delete) passes `inDialog`,
 * so the sentence is not said twice.
 */
function reportMutationError(
  err: unknown,
  context: Record<string, unknown>,
  opts: { inDialog?: boolean } = {},
): void {
  const held = lockedRefusal(err);
  if (held) flashToast(lockWords(held, { t, formatDate, locale: locale.value }));
  else if (!opts.inDialog) showToast({ message: failureText(err) }, ERROR_TOAST_MS);
  emit('error', { message: (err as Error)?.message ?? String(err), context });
}

/** A caught failure in the reader's words (lib/errorWords). */
function failureText(err: unknown): string {
  return sayFailure(err, t('toast.failed'), { t, callerAdmin: callerAdmin.value }).text;
}

async function runPluginAction(action: PluginActionRow, targets: FileNode[]) {
  const label = pluginLabelOf(action.label, locale.value);
  if (isPagePlacement(action.view_placement) && openPluginPage(action, targets)) return;
  try {
    const res = await pluginActions.run(action, targets);
    if (res.surface) {
      // ⚠ `paths` too: the screen's later events must name the same files the
      // run did, or its second screen talks about targets[0] alone (#64).
      pluginView.value = {
        plugin: action.plugin,
        view: action.view || action.id,
        surface: res.surface,
        path: targets[0]?.path,
        paths: targets.map((n) => n.path),
      };
      return;
    }
    if (res.op) onPluginOpQueued(res.op, label);
  } catch (e) {
    const err = e as Error & { status?: number; detail?: string };
    const detail = String(err?.detail ?? '');
    const held = lockedRefusal(err);
    if (held) flashToast(lockWords(held, { t, formatDate, locale: locale.value }));
    else if (err?.status === 422 && detail.includes('not_applicable')) flashToast(t('plugin.not_applicable'));
    else if (err?.status === 409 && detail.includes('read_only')) flashToast(t('plugin.read_only'));
    else flashToast(err?.message || t('plugin.failed', { label }));
  }
}

/** A job was enqueued (by `run` or by a view's submit): track it, say so. */
function onPluginOpQueued(op: Record<string, unknown>, label?: string) {
  pendingOps.register(op);
  const name = label || (typeof op.label === 'string' ? op.label : '') || t('opc.kind.plugin');
  flashToast(t('plugin.queued', { label: name }));
}

/**
 * A notification's deep link (`target.open`): show the file and open what the
 * app asked for on it — its action, or its view.
 *
 * ⚠⚠ Why this is exposed rather than a config prop. A bell click does not
 * remount anything: the person is already standing in the explorer, the hash
 * changes, the listing reloads. An `initialPluginAction` prop would fire once,
 * at mount, and every click after the first would land on the file and stop
 * there — which is the bug this whole field exists to fix ("please sign" that
 * drops you in a folder instead of in the signing screen).
 *
 * ⚠ The row may not be in the listing yet (a fresh tab, a folder still
 * loading), so a path that is not on screen is carried as a bare node. The
 * server re-checks `applies` and the ACL on every run, so the worst case is
 * an honest 422 rather than a wrong screen.
 *
 * ⚠⚠ It AWAITS the capability answer before reading `pluginsEnabled`. That
 * flag comes from `GET /api/capabilities`, which the explorer asks for in its
 * own `onMounted` — so whether this function found it filled was a matter of
 * which of two unrelated requests came back first. Measured as "roughly one
 * opening in eight the signing screen does not come up"; it was never the
 * plugin LIST (that was already awaited two lines down), it was the flag
 * gating it. Awaiting the promise removes the timing question instead of
 * betting on it.
 *
 * ⚠ Every refusal below now SAYS something. Four `return false`s in this path
 * were silent, and a deep link that silently does nothing is the hardest
 * possible bug to report: the person clicked a notification, the folder
 * opened, and the thing they were asked to do never appeared.
 */
async function openAppTarget(p: {
  plugin: string;
  action?: string;
  view?: string;
  path: string;
}): Promise<boolean> {
  if (!p.plugin || !p.path) return false;
  await loadCapabilities();
  if (!pluginsEnabled.value) {
    emit('error', {
      message: 'app plugins are off on this instance — deep link ignored',
      context: { what: 'openAppTarget', plugin: p.plugin, path: p.path },
    });
    return false;
  }
  await pluginActions.refresh();
  const node: FileNode =
    files.value.find((f) => f.path === p.path) ??
    ({ path: p.path, basename: p.path.split('/').pop() ?? p.path, type: 'file' } as FileNode);
  if (p.action) {
    const action = pluginActions.byKey(`plugin:${p.plugin}/${p.action}`);
    if (!action) {
      // The server validated this pair when it stored the target, so getting
      // here means the app was removed, disabled, or is not visible to this
      // account — all of which are worth a sentence.
      flashToast(t('plugin.not_applicable'));
      emit('error', {
        message: `no such plugin action: ${p.plugin}/${p.action}`,
        context: { what: 'openAppTarget', path: p.path },
      });
      return false;
    }
    await onPluginAction(pluginActionKey(action), [node]);
    return true;
  }
  if (!p.view) return false;
  // A `page` view is never listed in `views[]` (only inspector/home are), so
  // its placement is learned from an action that opens it. Nothing else
  // knows, and guessing "page" for everything would take a dialog-sized
  // screen into a tab of its own.
  const owner = pluginActions.actions.value.find((a) => a.plugin === p.plugin && a.view === p.view);
  if (owner && isPagePlacement(owner.view_placement)) {
    return openPluginPage(owner, [node]);
  }
  try {
    const res = await api.pluginView(p.plugin, p.view, p.path);
    if (!res?.surface) {
      flashToast(t('plugin.view.error'));
      return false;
    }
    pluginView.value = { plugin: p.plugin, view: p.view, surface: res.surface, path: p.path };
    return true;
  } catch (e) {
    flashToast((e as Error)?.message || t('plugin.view.error'));
    return false;
  }
}

/**
 * v3 §3.0 — a surface answered "go to this file, and start that screen".
 *
 * ⚠⚠ In-app, through `openAppTarget`, rather than by navigating the window
 * to the deep link: the person is already standing in the explorer, and a
 * reload would throw away the folder, the selection and the tab strip to
 * arrive at a screen the explorer can simply open. The deep-link ADDRESS is
 * the right answer for a frame with no explorer under it (a plugin `page`
 * view navigates); it is the wrong one here.
 *
 * ⚠ The screen that asked is closed first — an answer carrying `done` has
 * already closed it, and one that does not would otherwise sit over the
 * screen the person was sent to.
 *
 * ⚠ No permission check here, and that is deliberate: the host verified the
 * screen belongs to the plugin that answered, and the server re-checked the
 * path against THIS person's rights before the answer left, dropping the
 * request rather than refusing the screen. A second, weaker copy of that
 * rule in the browser would be the kind of check that drifts.
 */
async function onSurfaceOpen(plugin: string, req: { path: string; action?: string; view?: string }): Promise<void> {
  if (!plugin || !req?.path) return;
  pluginView.value = null;
  // The folder first, so the person can SEE where they were taken — the same
  // walk `onOpenOpOutput` makes for a job's output.
  const sep = req.path.indexOf('://');
  const storageName = sep === -1 ? '' : req.path.slice(0, sep);
  const rel = stripAdapter(req.path).replace(/^\/+|\/+$/g, '');
  const slash = rel.lastIndexOf('/');
  const dirRel = slash === -1 ? '' : rel.slice(0, slash);
  const target = multiStorageRoot.value && storageName ? (dirRel ? `${storageName}/${dirRel}` : storageName) : dirRel;
  try {
    await load(target);
  } catch {
    /* the folder may be gone; the screen below still tries the file */
  }
  await openAppTarget({ plugin, action: req.action, view: req.view, path: req.path });
}

/**
 * "Open" on a finished job: go to the output's folder and select it. The
 * output is an adapter-qualified path (`docs://reports/nda-signed.pdf`); the
 * folder is loaded the way a search hit's is, then the row is picked out of
 * the fresh listing by its exact path.
 */
async function onOpenOpOutput(_id: number, path: string) {
  const sep = path.indexOf('://');
  const storageName = sep === -1 ? '' : path.slice(0, sep);
  const rel = stripAdapter(path).replace(/^\/+|\/+$/g, '');
  const slash = rel.lastIndexOf('/');
  const dirRel = slash === -1 ? '' : rel.slice(0, slash);
  const target = multiStorageRoot.value && storageName ? (dirRel ? `${storageName}/${dirRel}` : storageName) : dirRel;
  await load(target);
  const node = files.value.find((f) => f.path === path);
  if (node) {
    selection.click(node.path);
    return;
  }
  flashToast(t('plugin.output_missing'));
}
/* === /App plugins === */

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
/**
 * etiket:t1 — "show me everything tagged this", from a FILE.
 *
 * ⚠⚠ The missing half of the tag feature, reported 2026-09-13 ("taglediğim
 * dosya klasör tag'ine gitmiyor"): the view existed, the panel's Tags section
 * listed every tag, and from a file's own chip there was no way in — you had to
 * read the word off the chip and go find it again in the panel.
 *
 * ⚠ It is `loadTagView`, not a variant of it. The panel's Tags section, a
 * restored tab, a pasted `#.tag~invoices` and now a chip all land in the one
 * loader, so there is one definition of what a tag view IS.
 *
 * ⚠ The modal is closed on the way. It is opened over a listing to EDIT tags;
 * once the chip has navigated, leaving it up means a dialog about one file
 * covering the view of all the others that share its tag.
 */
function openTagView(tag: string, kind: TagKind | '' = '') {
  showTagPicker.value = false;
  void loadTagView(tag, kind);
}

function onNodeTagsChanged() {
  // The cache is already dropped (lib/tags `announceTagsChanged`), so this
  // re-ask is real — and unforced, so every explorer on the page shares one.
  void loadNavTags();
  if (navView.value === 'tag' && navTag.value) void loadTagView(navTag.value, navTagKind.value);
}
/* etiket:k2 — the ONE path a tag write reaches the panel by: the module-level
 * announcement, which survives the picker being closed before the save
 * answered (a component emit does not) and reaches every explorer on the page.
 * The picker's own `change` / the details panel's `tags-changed` are no longer
 * wired here, or each save would reload the list twice. */
const offTagsChanged = onTagsChanged(onNodeTagsChanged);
onBeforeUnmount(offTagsChanged);

function onRecentOpen(entry: { id: number; storage_id?: number; path: string; name: string }) {
  // RecentlyOpened emits the bare row. The same converter Recent / Starred /
  // a tag / Home use gives openNode a row with its storage-qualified path AND
  // its own `perm` + `read_only`, so the editor-vs-preview split reads the
  // row's level, not the folder's. The bare synthesis stays as the fallback
  // for a row the converter cannot address (no storage name at a
  // multi-storage root) — it was the only shape until 2026-09-19.
  const node =
    nodeRowToFileNode(entry as unknown as Record<string, unknown>) ??
    ({
      type: 'file',
      path: entry.path,
      basename: entry.name,
      extension: (entry.name.split('.').pop() || '').toLowerCase(),
      id: entry.id,
    } as unknown as FileNode);
  showRecents.value = false;
  openNode(node);
}

/**
 * Right-click on a row of the Recently-opened tray: the SAME menu a listing
 * row gets (owner: "CONTEXT MENÜ HER YERDE AYNI OLMALI"). The tray's row is
 * the raw node row the endpoint answers with, so it goes through
 * `nodeRowToFileNode` like Recent / Starred / a tag / Home do and arrives
 * with its own `perm` + `read_only`; `onContextTarget` then treats it like a
 * Home card — a target outside this pane's listing. The tray closes first,
 * as it does on open: it is a modal over the listing, and the verb the menu
 * runs (Rename, Move to…) opens a dialog of its own.
 */
function onRecentContext(row: Record<string, unknown>, ev: MouseEvent) {
  const node = nodeRowToFileNode(row);
  if (!node) return;
  showRecents.value = false;
  void onContextTarget(node, ev);
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

/**
 * Could this caller SET UP a missing optional service? The server's answer
 * (`caller_admin` — an administrator who can reach the instance settings;
 * a tenant admin and an API token cannot), never a role guessed here. It picks
 * between the owner's two answers for an action that needs a service that is
 * not there: "disabled with a reason for administrators, hidden for everybody
 * else" (lib/serviceGate).
 */
const callerAdmin = computed(() => capabilitiesData.value?.caller_admin === true);

/** Does the server run this change as a job of its operations queue when asked
 *  (`queued=1`)? A folder rename and a restore from the trash are one request
 *  per object on an object store; inside the request they outlasted the proxy
 *  with nothing on screen. An older server does not say, and is asked the old
 *  way. */
function serverQueues(kind: 'rename' | 'restore'): boolean {
  return capabilitiesData.value?.queued?.includes(kind) === true;
}

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
const effectiveConvertUrl = computed<string | null>(() => {
  if (props.config.convertBase) return props.config.convertBase;
  /* ⚠ Health too, like the two above: the server fills `convert_url` whenever
   * the service is ENABLED, so a converter that is enabled but unreachable
   * used to be offered and then sat on "Loading the converter…" for ever. */
  const ext = capabilitiesData.value?.external?.convert;
  if (ext && !isExternalUsable(ext)) return null;
  return capabilitiesData.value?.convert_url || null;
});

/* The legacy converter against the Convert app: one of them, never both
 * (lib/serviceGate `legacyConvertGate`). `legacyConvertUrl` is what every
 * door to the iframe dialog reads — the menu, the toolbar, the dialog's own
 * v-if — so none of them can offer what the rule withheld. */
const legacyConvert = computed(() =>
  legacyConvertGate({
    appOffered: pluginsEnabled.value && convertAppOffered(pluginActions.actions.value),
    configured:
      !!props.config.convertBase ||
      capabilitiesData.value?.external?.convert?.enabled === true ||
      !!capabilitiesData.value?.convert_url,
    healthy: !!effectiveConvertUrl.value,
    callerAdmin: callerAdmin.value,
    unhealthyReason: t('ctx.needs_convert'),
    adminNote: t('convert.legacy_admin'),
  }),
);
const legacyConvertUrl = computed<string | null>(() =>
  legacyConvert.value.hidden || legacyConvert.value.disabled ? null : effectiveConvertUrl.value,
);

/* belge:n1 — what the SERVER can create, crossed with what WE could open.
 * `null` (a backend older than the feature) hides the entry entirely. */
const newDocTypes = computed(() => capabilitiesData.value?.newdoc_types ?? null);
const canNewDocument = computed(() => {
  const list = newDocTypes.value;
  if (!list || list.length === 0) return false;
  return list.some((ty) =>
    ty.requires === 'onlyoffice'
      ? !!effectiveOnlyOfficeBase.value
      : ty.requires === 'drawio'
        ? !!effectiveDrawioUrl.value
        : true,
  );
});

// Upload
const uploadJobs = ref<UploadJob[]>([]);
const fileInputEl = ref<HTMLInputElement | null>(null);

// Modals
const showNewFolder = ref(false);
const showNewDocument = ref(false); /* belge:n1 */
const showRename = ref(false);
const showDelete = ref(false);
const showPreview = ref(false);
const showArchiveCreate = ref(false);
const showArchiveExtract = ref(false);
const showArchivePassword = ref(false);
const archiveTargets = ref<FileNode[]>([]);
const archiveTarget = ref<FileNode | null>(null);
const archiveDestinationDir = ref('');
const archiveBusy = ref(false);
const archiveError = ref('');
const archiveInPane = ref(false);
const archivePasswordError = ref('');
const archivePasswordAction = ref<{
  target: FileNode;
  dest: string;
  inPane: boolean;
} | null>(null);

const archiveSuggestedName = computed(() => {
  if (archiveTargets.value.length !== 1) return 'archive';
  return archiveTargets.value[0]?.basename.replace(/\.[^.]+$/, '') || 'archive';
});
const ARCHIVE_CREATE_FORMATS: ArchiveCreateFormat[] = ['zip', '7z', 'tar', 'tar.gz', 'tar.bz2', 'tar.xz'];
/* The formats THIS server can make (capabilities.archive): every one but a
 * plain ZIP needs 7-Zip there, so a server without it offers ZIP alone. A
 * server with no `archive` block predates archive creation: nothing is
 * offered, and "Create archive…" is not in the menu. */
const archiveAllowedFormats = computed<ArchiveCreateFormat[]>(() => {
  const configured = capabilitiesData.value?.archive?.allowed_formats ?? [];
  return configured.filter((format): format is ArchiveCreateFormat => ARCHIVE_CREATE_FORMATS.includes(format));
});
const archiveDefaultFormat = computed<ArchiveCreateFormat>(() => {
  const configured = capabilitiesData.value?.archive?.default_format;
  if (configured && archiveAllowedFormats.value.includes(configured)) return configured;
  return archiveAllowedFormats.value.includes('7z') ? '7z' : archiveAllowedFormats.value[0] ?? 'zip';
});
const archiveEncryption = computed(() => capabilitiesData.value?.archive?.encryption !== false);
const archiveSuggestedFolder = computed(() =>
  (archiveTarget.value?.basename || 'archive')
    .replace(/\.(tar\.(gz|bz2|xz)|zip|7z|rar|tgz|gz|bz2|xz)$/i, '') || 'archive',
);
const renameTarget = ref<FileNode | null>(null);
/* Why the last rename did not happen, shown in the dialog. A failure used to be
 * only EMITTED, which the stock web app logs to the console: the dialog stayed
 * open and silent, the one answer a person can act on — the name is taken —
 * unseen. */
const renameError = ref<string | null>(null);
watch(showRename, (open) => {
  if (open) renameError.value = null;
});
/* The same for New folder and Delete, whose dialogs had nothing to show a
 * failure in at all — and a busy flag for each of the three: a request that
 * is still on its way keeps its dialog's button shut, so a second press does
 * not send the same order twice (a folder rename on an object store copies
 * every object inside the request; a second Save met the half-copied folder
 * and was refused as "already here"). */
const renameBusy = ref(false);
const newFolderBusy = ref(false);
const newFolderError = ref<string | null>(null);
watch(showNewFolder, (open) => {
  if (open) newFolderError.value = null;
});
const deleteBusy = ref(false);
const deleteError = ref<string | null>(null);
watch(showDelete, (open) => {
  if (open) deleteError.value = null;
});
/* ui-fix — does the open rename/delete/new-folder modal belong to the side
 * pane? (the menu is identical to the main pane's; this routes the mutation
 * to the right one.) */
const mutationInPane = ref(false);
const previewTarget = ref<FileNode | null>(null);
const previewMode = ref<'edit' | 'view'>('edit');
/* #56 — the type a document was just CREATED as, for the viewer's `openAs`:
 * `LICENSE` made as Plain text opens in the text editor although its name
 * picks no viewer. Tied to that one path (prev/next to another file drops it)
 * and forgotten when the viewer closes — a later open goes by the file itself,
 * like every other open. */
const previewOpenAs = ref<{ path: string; ext: string } | null>(null);
const previewOpenAsExt = computed(() =>
  previewOpenAs.value && previewTarget.value?.path === previewOpenAs.value.path
    ? previewOpenAs.value.ext
    : null,
);
watch(showPreview, (open) => {
  if (!open) previewOpenAs.value = null;
});
const showConvert = ref(false);
const convertTarget = ref<FileNode | null>(null);
const showPerm = ref(false);
const permTarget = ref<FileNode | null>(null);
/* tasi:m1 — "Move to…" / "Copy to…" ask the SAME dialog where; only the mode
 * differs, so there is one piece of state and not two dialogs. */
const showDestPicker = ref(false);
const destPickerMode = ref<'move' | 'copy'>('move');
const destPickerTargets = ref<FileNode[]>([]);
const destPickerBusy = ref(false);

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
 * so is everything else the header and the panel draw. Gating any of it would
 * be exactly the "one behaviour on one surface" split this shared package
 * exists to prevent — which is precisely what a per-role profile in the web
 * app turned out to be. */
const uiProfile = computed(() => resolveUiProfile(props.config.uiProfile));
/**
 * Is this the REDUCED explorer — one pane, one folder, list/grid only?
 *
 * ⚠⚠ This is the only question `uiProfile` still answers, and it is a question
 * about REDUCTION, never about LOOK. The shell (the header with its one search
 * field, the filter row, "+ New", the Folders/Files sections, the info panel's
 * tabs, the storage line) is what filex IS — it is not a profile anything can
 * be put into, so nothing below reads a profile to decide whether to draw it.
 * The day that distinction blurs again, the profiles become two products with
 * one name, which is exactly what this pass undid.
 *
 * ⚠ There are TWO values, and the rule for everything else — a typo, or the
 * profile removed after v0.40.0 — lives in `lib/uiProfile` together with the
 * argument for it. Nothing here branches on a retired name; this file asks one
 * question of a value that has already been resolved.
 */
const simpleUi = computed(() => uiProfile.value === 'simple');

/**
 * ⚠ `showInfoPanel` is documented public API ("whether the info panel toggle
 * is visible") and until now nothing read it: an embedder who set it to false
 * got the toggle anyway. Default stays TRUE, so every existing embed —
 * including the admin SPA, which passes `showInfoPanel: true` — is unchanged.
 * It hides the TOGGLE, exactly as documented; the inspector itself stays
 * reachable from the context menu, which is what the option says.
 */
const infoPanelToggle = computed(() => props.config.showInfoPanel !== false);
/* === surucu:d1 — the shell (GitHub #14, the reporter's mockups) ===========
 * There is no `driveShell` computed any more, and its absence is the point:
 * the filter row, "+ New", the info-panel tabs and the storage line are drawn
 * because this is filex, not because a caller passed a string. Grep for
 * `surucu:d1` to find them; every one of them is now unconditional or gated on
 * something real (a folder to filter, a person to have a quota).
 */

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
 * ⚠ Not gated on role. The backend decides — ConnectionsPanel is the protocol
 * guides and the credentials they need, which every caller may read, and
 * /api/tokens caps every scope against the caller's own role.
 */
const connectionsEnabled = computed(() => props.config.connections ?? !simpleUi.value);

/**
 * paylas:m1 — may the panel draw its "My shares" row?
 *
 * ⚠ `=== true`, not `?? <default>`: the row's screen is the HOST's, this
 * component only announces the intent, and an embedder who has not said yes
 * has not built that screen. Silence therefore has to mean no — the inverse of
 * `connectionsEnabled` above, whose panel this component draws itself and can
 * always deliver on.
 */
const mySharesEnabled = computed(() => props.config.mySharesVisible === true);

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
/* gorunum:v1-advsearch — declared HERE, beside the row's own state, and not
   down with the rest of the dialog's wiring: `filtersOn` and `displayFiles`
   read them, and a ref declared after a computed that touches it is the exact
   "Cannot access X before initialization" this file was taken down by once
   before (see the navVisible watcher note in onMounted). */
const advFilters = ref<DriveFilters | null>(null);
const advScope = ref<AdvScope>('name');
/**
 * surucu:d1-sort — WHERE THE ROWS IN HAND GOT THEIR ORDER, and the only place
 * in the bundle that knows. `files` is a search answer exactly when
 * `searchQuery` is set (`load()` picks `action=search` / `/api/files/search`
 * off that same ref), and a search answer is RANKED: the backend scores every
 * candidate (`internal/search/scorer.go`, ported from VS Code's Quick Open)
 * and returns best-first.
 *
 * ⚠⚠ Owner's ruling, 2026-09-12, verbatim (translated from Turkish): "the
 * filter in advanced search should belong to it alone. The other, ordinary
 * search and the ⌘K side must stay in relevance order." Measured the next day
 * on qldemo with the query `s`: the server ranked `Documents/server.ts` first
 * and the list drew it FIFTEENTH of seventeen, because the active sort key was
 * applied to everything the listing shows. Nothing looked broken — the grid
 * and the list agreed with each other and every test was green — because
 * re-alphabetising a ranked list is indistinguishable from sorting a folder.
 *
 * ⚠ Both scopes, one answer: the advanced dialog's content search lands in
 * `files` through this same ref, so it is covered without a second rule. The
 * ⌘K palette never needed one — it renders its own hits straight from
 * `paletteGlobalSearch` and reaches for no comparator (verified, not assumed).
 *
 * ⚠ Declared HERE, above `displayFiles`, for the reason the `advFilters` note
 * above gives: a ref/computed declared after the computed that reads it is the
 * "Cannot access X before initialization" this file was taken down by once.
 *
 * ⚠ And NOT a module-level flag in `lib/sortOrder`: the split view's secondary
 * pane only ever lists (`SecondaryPane.loadPane` calls `index`), so a global
 * "we are searching" would silently unsort the pane that is not.
 */
const listingOrder = computed<ListingOrder>(() => (searchQuery.value ? 'relevance' : 'sort'));
/* pane:p1 — `filtersOn` and `displayFiles` USED TO LIVE HERE, and they are the
 * clearest example of what this refactor is for: they compose the filter row's
 * narrowing, the advanced dialog's narrowing and the sort into the rows a
 * listing draws — a PANE's job, done once in the host, which is why the split
 * view's right-hand half had no filter row for two months and would have had
 * to grow a second copy of this to get one. They are `FilePane`'s
 * `displayFiles` / `filtersOn` now, and every pane has them.
 *
 * ⚠ `advFilters` stays here, and it is not an exception: it belongs to the
 * advanced SEARCH, which is the window's (one search field, one dialog, one
 * set of results). It is handed to the main pane as `extra-filters`, which is
 * a narrowing composed AFTER the pane's own chips rather than instead of them.
 * ⚠ `listingOrder` stays here for the same reason and is passed as `order`:
 * whether the rows in hand were RANKED by the server is a fact about the
 * answer the host fetched, not a preference the pane holds. The split pane
 * only ever lists a folder, so it is never in relevance mode — which is
 * exactly why a global "we are searching" flag would have been wrong.
 */

/* gorunum:v2-topbar / pane:p1 — the breadcrumb's "Subfolders" chevron used to
 * be fed from here, which is why only the left-hand pane had one: the host
 * knows ONE folder's listing and there are two panes. It is derived inside
 * `FilePane` now, from the rows that pane is holding. */

function clearDriveFilters() {
  advFilters.value = null /* gorunum:v1-advsearch — the escape hatch clears BOTH */;
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
    /* gorunum:v1-advsearch — the advanced filters belong to the search that
       set them, so they survive the rebase a search causes (a search moves
       `currentPath` to the storage root, which is what fires this watcher) and
       are dropped the moment there is no search left to belong to. */
    if (!searchQuery.value) advFilters.value = null;
  },
);

/* === gorunum:v1-advsearch — the Advanced search dialog ===================
 *
 * One results surface: whatever the dialog asks for lands in `files` through
 * the same `load()` a toolbar search lands in. There is no second list, no
 * "search results" page and no separate empty state — the dialog composes a
 * query, the explorer runs it, and the rows arrive where rows always arrive.
 *
 * Two things the dialog cannot do itself, and they live here because this is
 * where the API client is:
 *
 *  1. **Scope routing.** `name` is served by the manager's
 *     `?action=search&filter=…`, which is what the toolbar already uses and
 *     what returns adapter-qualified rows. `content`/`all` exist ONLY on
 *     `/api/files/search` (the manager's search action hardcodes
 *     `search.ScopeName`), whose rows are raw node rows — so they are mapped
 *     onto the listing shape below.
 *  2. **The live count**, which is a real query. See `advSearchCount`.
 */
const showAdvSearch = ref(false);
/* etiket:k2 — the dialog offers the person's tags, each with its kind, as
   picks under the Tags box. Asked for when it opens, through the same
   module-level cache as the panel's list, so it costs nothing when the panel
   already has them — and works in an embed that shows no panel at all. */
const advKnownTags = ref<TagItem[]>([]);
watch(showAdvSearch, async (open) => {
  if (!open) return;
  advKnownTags.value = await fetchAllTags({
    apiBase: props.config.apiBase ?? '',
    authHeaders: () => buildAuthHeaders(),
    authCredentials: api.credentialsMode(),
  });
});
/** The folder the dialog was opened from, frozen for its lifetime. */
const advPathBase = ref('');

/**
 * Whether the content scopes may be offered at all.
 *
 * ⚠ This USED to be a capability check: `/api/files/search` answered with a
 * `storage_id` and no storage name, the explorer has no id→name map, and a row
 * wearing the wrong drive's name is worse than a scope we did not offer — so
 * the tabs were gated to single-storage installs, where the guess could not be
 * wrong. `handlers/search.go` now labels every hit with its drive's name (and
 * its owner) in `describeHits`, so there is nothing left to guess and the
 * scopes are offered everywhere. Kept as a computed rather than deleted: the
 * prop is public API, and an embedder pointed at an older backend still gets
 * hits with no `storage` — which `hitToNode` (lib/searchHit) falls back for,
 * row by row.
 */
const advContentAvailable = computed(() => true);

/** How many hits we ask the content endpoint for. The manager's search action
 *  uses 250 internally; matching it keeps the two scopes comparable. */
const ADV_CONTENT_LIMIT = 250;

/** Run one advanced search and hand back the rows, unfiltered — and whether
 *  the answer was cut. The name scope has the server's own word for that
 *  (`truncated`); /api/files/search's flag does not survive `globalSearch`,
 *  which returns hits only, so the content scope keeps the full-page guess. */
async function advFetchRows(
  scope: AdvScope,
  query: string,
  target: string,
): Promise<{ rows: FileNode[]; truncated: boolean }> {
  if (scope === 'name') {
    const resp = await api.search(target, query);
    return {
      rows: filterListing(resp.files),
      truncated: advSearchTruncated(resp.files.length, MANAGER_SEARCH_PAGE, resp.truncated),
    };
  }
  const hits = await api.globalSearch(query, { limit: ADV_CONTENT_LIMIT, scope });
  const storageName = adapter.value || (props.config.storages ?? [])[0]?.name || '';
  return {
    rows: filterListing(hits.map((h) => hitToNode(h, storageName))),
    truncated: advSearchTruncated(hits.length, ADV_CONTENT_LIMIT),
  };
}

/** The target `load()` would use for the current position. */
function advTarget(): string {
  const requested = currentPath.value ?? '';
  return multiStorageRoot.value ? virtualToWire(requested) : qualify(requested);
}

/**
 * The dialog's live count — a REAL query, not an estimate.
 *
 * ⚠ It costs exactly what pressing Search costs: the same request, the same
 * rows, the same client-side narrowing. There is no cheaper way to answer it —
 * neither endpoint has a count mode — so the dialog prints that the number is
 * produced by running the search rather than letting it look free.
 *
 * `capped` is the other half of the honesty: when the server returned as many
 * hits as it was allowed to, the client-side filters narrowed a window and the
 * number describes the rows that came back, not the storage.
 */
async function advSearchCount(req: AdvSearchRequest): Promise<AdvCountResult> {
  const query = advQueryString(req);
  const { rows, truncated } = await advFetchRows(req.scope, query, advTarget());
  return {
    count: applyFilters(rows, req.filters).length,
    capped: truncated,
  };
}

function openAdvancedSearch(seed: string) {
  advPathBase.value = qualify(currentPath.value ?? '') || '';
  advSearchSeed.value = seed;
  showAdvSearch.value = true;
}
const advSearchSeed = ref('');

function applyAdvancedSearch(req: AdvSearchRequest) {
  showAdvSearch.value = false;
  advScope.value = req.scope;
  advFilters.value = filtersActive(req.filters) ? { ...req.filters } : null;
  const q = advQueryString(req);
  // Same text as the box already holds → the `searchQuery` watcher will not
  // fire, so the reload (which the new scope/filters need) is issued here.
  if (q === searchQuery.value) void load();
  else searchQuery.value = q;
}

/** Typing in the toolbar field is a plain search again — it replaces whatever
 *  the dialog set rather than silently inheriting filters the user cannot see
 *  from a box that shows only words. */
function onToolbarSearch(v: string) {
  advFilters.value = null;
  advScope.value = 'name';
  /* gorunum:v3-shell — Home has no listing behind it, and `searchQuery` is
     watched by `load()`. Setting it here re-entered loadNavView('home') on
     every keystroke — two fetches per pause, for a narrowing that could not
     appear anywhere. The words are not lost: Enter hands them to the command
     palette (Toolbar `searchEscalates`), which is the "everywhere" search the
     field's own ⌘K chip advertises. */
  if (navView.value === 'home') return;
  searchQuery.value = v;
}
/* === /gorunum:v1-advsearch === */

/**
 * surucu:d1 — what the header field says it will search: the folder you are
 * standing in, by name. At a storage root that is the storage; in a panel view
 * ("Recent", a tag) it is that view's own name, because searching from there
 * searches what is on screen.
 */
const driveScopeLabel = computed(() => {
  /* ⚠ The field searches the WHOLE storage (`?action=search` filters by
     storage id only — see handlers/manager.go vfSearch), and at the virtual
     root every storage. Naming the open FOLDER here ("Search in Photos") was
     a lie the user found: results came from the whole storage. The scope is
     the storage; the folder-scoped box is FilterBar's "Filter in this
     folder…". Home, the virtual root and the cross-storage views (Recent,
     Starred, Shared, tags) have no single storage → the everywhere wording.
     ⚠ `multiStorageRoot` is the MODE (the admin explorer is always in it), not
     the place: inside a storage the scope is that storage. Gating on the mode
     printed "Search all storages" on every admin folder (caught by
     e2e/tests/107 on the v0.42.2 chain). */
  if (navView.value || atVirtualRoot.value) return '';
  const name = adapter.value;
  if (!name) return '';
  const st = (props.config.storages ?? []).find((s) => s.name === name);
  return st?.label || name;
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
 * Fetched once per mount. The gate is `identitySurfaces` and nothing else: a
 * quota is one PERSON's ceiling, so an app token has nobody to have one, but
 * every surface that has a person behind it draws the same line — this is the
 * shell, not a profile. `quotaMe()` answers null for a server without the
 * route, and null renders nothing at all.
 *
 * ⚠ WHICH number the line prints is `lib/storageLine`'s decision, not this
 * function's. It used to print `used_bytes` for everybody — the person's
 * upload counter (`SUM(nodes.size) WHERE owner_id = me`, and the upload path
 * is the only one that sets an owner) — under "Storage", in the sentence
 * Home's card uses for a drive's size. Measured on a production install
 * (2026-09-25): card 245.3 GB, line 523.5 MB. Now a person with a quota sees
 * their share of it; everybody else sees the drives the panel lists, from the
 * same `homeStorages` the Home cards are drawn from, and the server is asked
 * only for the drives the host sent no size for (the desktop app sends names
 * only).
 */
const quotaMine = ref<QuotaSnapshot | null>(null);
const quotaDrives = ref<MeasuredDrive[] | null>(null);
/* ⚠ A lazy `computed`, and it has to stay one: `homeStorages` is declared
   further down, so anything that reads it at setup time — a `watch` on this
   line, say — throws "Cannot access before initialization" and takes the
   explorer down (the `isNarrow` note in onMounted). */
const quotaSnapshot = computed(() => storageLine(quotaMine.value, homeStorages.value, quotaDrives.value));
async function loadQuota() {
  if (!identitySurfaces.value) {
    quotaMine.value = null;
    return;
  }
  const q = await api.quotaMe();
  quotaMine.value = q;
  quotaDrives.value = needsMeasuredDrives(q, homeStorages.value) ? await api.storageUsage() : null;
}

/**
 * nodeRowToFileNode — the starred / recently-opened / tag endpoints answer
 * with raw node rows (relative `path`, numeric `storage_id`), not the listing
 * shape. The conversion itself lives in `lib/nodeRow` (pure, unit-tested);
 * this binds it to the explorer's config. Every view that lists such rows —
 * Recent, Starred, a tag, the Home cards, the Recently-opened tray — goes
 * through here, so they all carry the same `perm` + `read_only` the context
 * menu gates its write verbs on.
 */
function nodeRowToFileNode(row: Record<string, unknown>): FileNode | null {
  return nodeRowToFileNodePure(row, {
    storages: props.config.storages ?? [],
    multiStorageRoot: multiStorageRoot.value,
  });
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

/* === gorunum:v3-shell — the Home view's own state ========================
 * Two lists and a flag, and nothing else: the storages are already
 * `config.storages` (the host's list, kept current by its own Refresh) and the
 * cards come from GridView, so Home adds no third source of truth about what
 * exists — it only asks the two per-user endpoints the panel's Recent and
 * Starred rows already ask.
 *
 * ⚠ `files` stays EMPTY while Home is open. Home is not a listing: it renders
 * its own sections, and putting its rows in `files` would hand the selection,
 * the inspector, the keyboard range and every `files.length` in this file a
 * list nobody is standing in.
 */
const homeRecent = ref<FileNode[]>([]);
const homeStarred = ref<FileNode[]>([]);
const homeLoading = ref(false);

/* === #57 — the order the person put their storages in ====================
 * `config.storages` is the HOST's order; the panel and Home draw the person's
 * (`lib/storageOrder`: none saved = the host's order, unchanged). The panel
 * announces a new order and it is stored here — on the account beside the
 * palette, with this browser's mirror for the first paint, or in the mirror
 * alone for an embed that wires no account document. */
const storageOrder = useStorageOrder();
const navStorages = computed(() => orderStorages(props.config.storages ?? [], storageOrder.saved.value));
function onReorderStorages(keys: string[]) {
  saveStorageOrder(keys, navStorages.value);
}

/**
 * The storages Home draws.
 *
 * ⚠ Straight from `config.storages`, NOT a second fetch. The host already
 * decided which drives this caller may see (RBAC on the server, then
 * `fetchVisibleStorages` in our own app) and the navigation panel two hundred
 * pixels to the left is rendering that same array — a Home that asked for its
 * own copy could show a drive the panel beside it hides.
 *
 * #57 — and in the SAME ORDER as that panel: the person's own
 * (`navStorages`), so a drive they moved to the top of the panel is not the
 * third card on Home.
 */
/* A drive's figure is a lower bound while its catalog does not cover all of
   it: the host says so (`usedPartial`, from the usage endpoint's `coverage`),
   or else the last listing did (coverageMap). */
const homeStorages = computed(() =>
  navStorages.value.map((s) =>
    s.usedPartial !== undefined || !(s.name in coverageMap.value)
      ? s
      : { ...s, usedPartial: coverageMap.value[s.name] !== null },
  ),
);

async function loadHome() {
  homeLoading.value = true;
  try {
    // ⚠ Both at once and neither fatal on its own: a server without the
    // starred endpoint must still be able to show somebody their recents.
    const [r, st] = await Promise.all([
      fetchNavRows('recent').catch(() => [] as FileNode[]),
      fetchNavRows('starred').catch(() => [] as FileNode[]),
    ]);
    homeRecent.value = r;
    homeStarred.value = st;
  } finally {
    homeLoading.value = false;
  }
}

/** Open one of the panel views in the main pane. */
async function loadNavView(kind: Exclude<NavView, ''>) {
  closeNavDrawer();
  if (kind === 'home') {
    // ⚠ The mode is set BEFORE the fetch, unlike the listing views below: Home
    // renders its own sections with their own loading line, so there is
    // nothing to hold back — and setting it afterwards would leave the
    // previous folder's files on screen under the panel row that already reads
    // as selected.
    if (!navView.value) navViewOrigin.value = currentPath.value ?? '';
    navView.value = 'home';
    navTag.value = '';
    navTagKind.value = '';
    trashMode.value = false;
    e2eRoot.value = '';
    forgetFolderPerm();
    selection.clear();
    files.value = [];
    dirname.value = NAV_VIEW_DIRNAME.home;
    currentPath.value = NAV_VIEW_DIRNAME.home;
    adapter.value = '';
    await loadHome();
    return;
  }
  if (kind === 'tag') {
    // The tag view needs a name; the panel calls loadTagView directly.
    if (navTag.value) await loadTagView(navTag.value, navTagKind.value);
    return;
  }
  if (kind === 'trash') {
    void probeTrashPolicy(); /* tablo:t1 — in parallel: the banner is above the
                                listing and must not wait behind it */
    await loadTrash();
    // ⚠ After loadTrash, not before: loadTrash goes through load()-adjacent
    // state and the mode has to be the last word, or the panel row for Trash
    // never lights up.
    navView.value = 'trash';
    navTag.value = '';
    navTagKind.value = '';
    return;
  }
  loading.value = true;
  // ⚠ Only when coming from a real folder. Stepping Starred → Recent used to
  // record `.starred` as the origin, so "up" out of Recent landed in Starred
  // and the user had to press it twice to get back to their files.
  if (!navView.value) navViewOrigin.value = currentPath.value ?? '';
  navView.value = kind;
  navTag.value = '';
  navTagKind.value = '';
  trashMode.value = false;
  e2eRoot.value = '';
  forgetFolderPerm();
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
async function loadTagView(tag: string, kind: TagKind | '' = '') {
  closeNavDrawer();
  const name = String(tag ?? '').trim();
  if (!name) return;
  loading.value = true;
  if (!navView.value) navViewOrigin.value = currentPath.value ?? '';
  navView.value = 'tag';
  navTag.value = name;
  navTagKind.value = kind;
  trashMode.value = false;
  e2eRoot.value = '';
  forgetFolderPerm();
  selection.clear();
  try {
    const rows = await fetchTaggedRows(
      name,
      {
        apiBase: props.config.apiBase ?? '',
        authHeaders: () => buildAuthHeaders(),
        authCredentials: api.credentialsMode(),
      },
      200,
      kind,
    );
    files.value = rows.map(nodeRowToFileNode).filter((n): n is FileNode => n !== null);
    // etiket:k2 — the kind is part of the address (`.mytag~x` / `.teamtag~x`),
    // so a reload or a copied link opens the same one of two same-named tags.
    const seg = makeTagSegment(name, kind);
    dirname.value = seg;
    currentPath.value = seg;
    // Spans every storage, like Starred/Recent/Shared — so no storage crumb.
    adapter.value = '';
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    files.value = [];
    emit('error', { message: msg, context: { op: `nav-view:tag:${kind ? `${kind}:` : ''}${name}` } });
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
const navTags = ref<TagItem[]>([]);
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

/**
 * gorunum:v3-shell — what the panel's first group does.
 *
 * ⚠ "My files" is answered with an ordinary navigation, not with a view. It
 * opens the ROOT — the storage list in a multi-storage install, the storage
 * root in a single-storage one, and the confined floor inside a `rootPath`
 * embed (load() clamps it, so the row cannot be used to climb out of a
 * confined explorer). `load('')` also clears `navView`, which is what takes
 * the panel's highlight off whichever view you were in.
 */
function openNavDest(dest: NavDest) {
  if (dest === 'myfiles') {
    closeNavDrawer();
    void load('');
    return;
  }
  void loadNavView(dest);
}

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
/**
 * What a user path READS AS — the folder's own name, or the view's.
 *
 * ⚠ Takes the path as an argument rather than reading `currentPath`, because
 * there are two panes and the details panel follows whichever one has the
 * keyboard (see `inspectorDirLabel`).
 *
 * ⚠ The `trashMode` special case it used to open with is gone, and that is a
 * removal, not an omission: the trash view parks `.trash` in `currentPath`, and
 * `virtualSegmentLabel('.trash')` is `t('node.trash')` — the same string, by
 * the same route as every other view. One of the two was going to be forgotten
 * the next time a view was added; it is the one that could be.
 */
function folderLabelOf(path: string): string {
  const p = (path ?? '').replace(/^\/+|\/+$/g, '');
  /* No path = the top of the tree. In multi-storage that is the DRIVE LIST, so
     naming it after `adapter` — whichever storage was loaded last — would head
     the panel with a drive the person is not looking at. */
  if (!p) return multiStorageRoot.value ? t('breadcrumb.root') : (adapter.value || t('breadcrumb.root'));
  const seg = p.split('/').pop() || p;
  /* etiket:t1 — a THIRD surface that renders a path segment, and it had the
     same hole the tab strip did: in a virtual view the details panel headed
     itself ".starred". Same shared resolver, so it cannot drift again. */
  return virtualSegmentLabel(seg, t) || seg;
}
function onInspectorManage(n: FileNode) {
  permTarget.value = n;
  showPerm.value = true;
}
/* === /koru:k1 === */

// RBAC helpers. '' means ACL is not enforced on this storage → full access
// (the pre-RBAC default). Otherwise 'editor'/'owner' may write; only 'owner'
// manages permissions. Enforcement is server-side; this just shapes the menu.
function permCanEdit(p: string | undefined): boolean {
  // A read-only storage refuses every write on the server (403 "storage is
  // read-only") whatever level the ACL grants — an owner of a read-only mount
  // is an owner who cannot write. Say so here rather than after the click:
  // until this line the toolbar offered New folder / Upload and the sidebar's
  // "+ New" menu on a read-only mount, and the user found out from the error
  // (issue #30). The level itself is left alone: 'owner' still opens the
  // permissions panel, sharing a read-only file is still allowed.
  if (dirReadOnly.value) return false;
  // undefined = ACL not enforced (dev / unwired) → full access. In production
  // the backend always sends a level; 'none'/'viewer' cannot write, only
  // 'editor'/'owner' can.
  return p === undefined || p === 'editor' || p === 'owner';
}
function permIsOwner(p: string | undefined): boolean {
  return p === 'owner';
}
/* ⚠⚠ THE CONTEXT MENU MUST BE THE SAME EVERYWHERE (owner, 2026-09-19: "son
 * kullanılanlar, ana sayfa gibi sayfalarda context menu eksik kalıyor.
 * CONTEXT MENÜ HER YERDE AYNI OLMALI").
 *
 * `dirPerm` / `dirReadOnly` describe the FOLDER being listed, and only
 * `load()` sets them. Recent, Starred, Shared, a tag view and Home list rows
 * from every storage and have no folder — so whatever folder was open LAST
 * answered for them. On the landing page that is no folder at all
 * (`dirPerm === ''`), and `permCanEdit('')` is false, so the menu on a Home
 * card or a Recent row came up without Rename / Delete / Move to / Share.
 * After a writable folder had been visited the stale level said yes to
 * everything, including rows on a read-only mount. Intermittent, therefore
 * reported as "eksik kalıyor" rather than "yok".
 *
 * Three rules restore one menu:
 *   1. entering a virtual view forgets the folder (`forgetFolderPerm`), so
 *      nothing stale can leak into it;
 *   2. a row answers for itself — its own `perm` (every listing carries it
 *      now, `handlers/meta.go`) and its own storage's `read_only`;
 *   3. a multi-selection is as weak as its weakest row, because the server
 *      refuses the whole batch on the one row the caller may not touch.
 * What stays hidden in a virtual view is only what is structurally
 * impossible there — New folder, Upload, Paste need a destination folder
 * (`atVirtualRoot`).
 */
function forgetFolderPerm() {
  dirPerm.value = '';
  dirReadOnly.value = false;
}
/** A view whose rows span every storage: no folder is behind it. */
const inVirtualView = computed(() => !!navView.value && navView.value !== 'trash');
/** The row's own level; the folder's when the row has none. In a virtual view
 *  a row without a level (a server older than `handlers/meta.go`'s `perm`)
 *  is ungated — the server enforces, this only shapes the menu — rather than
 *  gated by an empty folder level that would hide every write verb. */
function rowPerm(n: FileNode): string | undefined {
  if (typeof n.perm === 'string') return n.perm;
  return inVirtualView.value ? undefined : dirPerm.value;
}
/** The row sits on a read-only storage: its own `read_only` (nav/tag rows),
 *  else the host's storage list — a folder listing's rows carry no flag of
 *  their own because `dirReadOnly` already answers for the whole folder. */
function nodeReadOnly(n: FileNode): boolean {
  if (n.read_only === true) return true;
  const storage = typeof n.storage === 'string' ? n.storage : '';
  if (!storage) return false;
  return (props.config.storages ?? []).some((s) => s.name === storage && s.readOnly === true);
}
/** May the caller write THIS row — level and read-only mount folded together.
 *  The one predicate behind the editor/preview split and the menu's write
 *  verbs, so a Home card and a listing row cannot disagree. */
function nodeCanEdit(n: FileNode): boolean {
  return permCanEdit(rowPerm(n)) && !nodeReadOnly(n);
}
const PERM_RANK: Record<string, number> = { none: 0, viewer: 1, editor: 2, owner: 3 };
// Effective perm for a selection: a single entry's own perm, the WEAKEST of a
// multi-selection's own perms, else the directory perm (background / rows
// that carry no level).
function selPerm(sel: FileNode[]): string | undefined {
  if (sel.length === 0) return dirPerm.value;
  let weakest: string | undefined;
  let ungated = false;
  for (const n of sel) {
    const p = rowPerm(n);
    if (p === undefined) {
      ungated = true;
      continue;
    }
    if (weakest === undefined || (PERM_RANK[p] ?? 0) < (PERM_RANK[weakest] ?? 0)) weakest = p;
  }
  // An ungated row (no level, unwired ACL) never strengthens a selection
  // that also holds a gated one; alone, it stays ungated.
  return weakest ?? (ungated ? undefined : dirPerm.value);
}
/** Any row of the selection on a read-only storage → the selection is. */
function selReadOnly(sel: FileNode[]): boolean {
  return sel.some(nodeReadOnly);
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
/** How long a failure stays up: long enough to read a sentence. */
const ERROR_TOAST_MS = 6000;
/** A "still working on it" toast: up until the answer replaces it. */
const STICKY_TOAST_MS = 10 * 60 * 1000;
function showToast(state: ToastState, ms: number) {
  toast.value = state;
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.value = null), ms);
}
function flashToast(msg: string) {
  showToast({ message: msg }, 2500);
}

/**
 * The catalog-coverage strip over a listing and over search results
 * (lib/catalogCoverage): what search, folder sizes and usage leave out, and
 * why. Not over a view that is not a storage's (Home, Recent, the trash) or a
 * dead link. A search typed at a storage's root spans every storage — the
 * server's own rule (handlers.Manager vfSearch) — so it names each one that is
 * not fully cataloged.
 */
const coverageShown = computed(() => {
  if (navView.value || trashActive.value || notFoundPath.value) return null;
  const searching = !!searchQuery.value;
  const atStorageRoot = stripAdapter(dirname.value).replace(/^\/+|\/+$/g, '') === '';
  return coverageNotice({
    map: coverageMap.value,
    adapter: adapter.value,
    searching,
    crossStorage: searching && atStorageRoot && Object.keys(coverageMap.value).length > 1,
    admin: callerAdmin.value,
  });
});

/**
 * "Catalog everything" (an administrator, a storage cataloged only on open):
 * the ordinary full sync of that storage, the one the admin panel's
 * "Sync now" starts. The storage's id is asked for at click time — the
 * listing carries names only, and nobody but an administrator needs the id.
 */
async function catalogAll(storage: string | undefined) {
  if (!storage || catalogAllBusy.value) return;
  catalogAllBusy.value = true;
  try {
    const base = connectionsBase(props.config);
    const rows = await api.jsonFetch<Array<{ id: number; name: string }>>(`${base}/api/admin/storages`);
    const row = Array.isArray(rows) ? rows.find((r) => r.name === storage) : undefined;
    if (!row) throw new Error('no such storage');
    await api.jsonFetch(`${base}/api/admin/storages/${row.id}/sync`, { method: 'POST' });
    flashToast(t('coverage.catalog_started'));
  } catch (err) {
    flashToast(sayFailure(err, t('toast.failed'), { t, callerAdmin: callerAdmin.value }).text);
  } finally {
    catalogAllBusy.value = false;
  }
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

/**
 * gorunum:v2-topbar — what the Refresh control actually means.
 *
 * ⚠ ONE function behind BOTH doors (the header's button and the palette's
 * `refresh` command). They used to be two separate `() => load()` arrow
 * functions in the template, which is how one of them would have quietly kept
 * reloading only half of what the other does.
 *
 * The listing is ours; the storage list is the host's (`config.storages`), so
 * the host is told and re-answers it in its own time. Nothing here waits on
 * that: the folder is on screen again either way.
 *
 * surucu:d1 — the storage line is read again too: the person's usage and the
 * drives' sizes both move, and a host that sends no sizes (the desktop app)
 * would otherwise keep the figure the panel mounted with.
 */
function refreshAll() {
  void load();
  void loadQuota();
  emit('refresh');
}

async function load(path?: string) {
  // Whatever an earlier search said about ITS answer, this listing has not
  // answered yet (the "more results than shown" strip reads this).
  searchTruncated.value = false;
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
    if (asView.kind === 'tag') await loadTagView(asView.tag, asView.tagKind);
    else await loadNavView(asView.kind);
    return;
  }
  /* A different folder is a different listing, and a selection belongs to the
   * listing it was made in.
   *
   * ⚠⚠ Measured 2026-09-14 (v0.41.0 screenshot pass): double-clicking into an
   * encrypted folder left "1 selected" — cut, copy, DELETE — hanging over its
   * lock screen, and those actions were aimed at the folder the person was now
   * standing INSIDE. Two stale things carried it: the selection itself, and
   * `displayOrder`, the rows the view last drew. The lock screen (like the
   * not-found state) draws no view, so nothing replaced the parent's rows, and
   * `selection.nodes` kept resolving the double-clicked folder against them.
   * A plain folder hid the bug only because its own view re-published its rows
   * and the stale path stopped resolving — the selection was still there, and
   * stepping back up brought it back to life.
   *
   * ⚠ Only when the FOLDER changes. A reload of the same folder (a realtime
   * refresh, a mutation's re-render, a search rebased onto it) keeps what the
   * person picked. Same rule the split pane applies in `onPaneNavigate`. */
  const leaving = String(currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  const arriveAt = (to: string) => {
    if (String(to ?? '').replace(/^\/+|\/+$/g, '') === leaving) return;
    selection.clear();
    displayOrder.value = [];
  };
  loading.value = true;
  // Any normal navigation exits trash mode (the trash view is entered only
  // by opening the virtual `.trash` row, which calls loadTrash()).
  trashMode.value = false;
  /* gezinti:g1 — and every other virtual view, for the same reason: without
     this the mode sticks and the breadcrumb keeps saying "Starred" over a
     folder listing. */
  navView.value = '';
  navTag.value = '';
  navTagKind.value = '';
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
      arriveAt('');
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

    /* gorunum:v1-advsearch — a content-scoped search cannot come from the
       manager's search action (it hardcodes `search.ScopeName`), so it is
       fetched from /api/files/search and projected onto the listing shape
       here. Everything downstream — the views, the selection, the inspector —
       sees ordinary rows, which is the point: one results surface. */
    const advContent = !!searchQuery.value && advScope.value !== 'name';
    const adv = advContent ? await advFetchRows(advScope.value, searchQuery.value, target) : null;
    const resp: ManagerResponse = adv
      ? {
          adapter: adapter.value,
          storages: (props.config.storages ?? []).map((s) => s.name),
          dirname: dirname.value,
          read_only: false,
          files: adv.rows,
          truncated: adv.truncated,
        }
      : searchQuery.value
        ? await api.search(target, searchQuery.value)
        : await api.index(target);
    // A search that matched more than it returned says so (banner strip). The
    // server's `truncated` is the answer; an older server that does not send
    // it leaves the full-page guess the advanced search count always made.
    searchTruncated.value =
      !!searchQuery.value && advSearchTruncated(resp.files.length, MANAGER_SEARCH_PAGE, resp.truncated);
    if (Array.isArray(resp.storage_info)) coverageMap.value = coverageByStorage(resp.storage_info);
    adapter.value = resp.adapter;
    dirname.value = resp.dirname;
    dirPerm.value = (resp.perm as string) || '';
    dirReadOnly.value = resp.read_only === true;
    /* wiring:e2 — backend tells us when this dir sits inside an encrypted
       subtree; '' resets on every plain folder. Drives the lock screen. */
    e2eRoot.value = typeof resp.e2e_root === 'string' ? resp.e2e_root : '';
    /* /wiring:e2 */
    // currentPath is the user-facing form: `s3-test/example` in
    // multi-storage mode, the bare relative path otherwise.
    const arrived = multiStorageRoot.value ? wireToVirtual(resp.dirname) : stripAdapter(resp.dirname);
    arriveAt(arrived);
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
    currentPath.value = arrived;
  } catch (err) {
    const e = err instanceof Error ? err.message : String(err);
    const status = (err as { status?: number }).status;
    if (status === 404 || status === 403) {
      // Dead deep link (deleted folder, phantom path or RBAC-hidden dir):
      // show the dedicated not-found state instead of a toast over a stale
      // listing that reads as "this folder is empty".
      notFoundPath.value = String(requested);
      arriveAt(String(requested));
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
/* === tablo:t1 — the trash banner ======================================
 *
 * The reference build draws, above the listing: what the trash IS, and the one
 * irreversible action. We had neither — and the second half is a real
 * regression rather than a missing decoration, because the only "Empty trash"
 * in the whole repo is on the admin page `web/src/views/Trash.vue`, reached
 * from the admin panel's own sidebar. An end user, who never sees the admin
 * panel, had no way to empty their own trash at all.
 *
 * ⚠⚠ RETENTION IS A CLAIM, NOT A DECORATION. The reference says "30 days";
 * ours must say what THIS deployment actually does, and `trash.retention_days`
 * is a setting an operator changes. A banner stating the wrong number is worse
 * than no banner, because people act on it — they leave something in the
 * trash believing they have a month. So the number is asked for, and when the
 * answer does not come the wording drops the period instead of guessing one.
 *
 * ⚠⚠ ONE PROBE ANSWERS BOTH QUESTIONS. `GET /api/admin/protection` carries
 * the real retention and is refused to anyone who is not an operator — and
 * `POST /api/admin/trash/empty` is gated on exactly the same thing. So its
 * status code tells us the number AND whether this caller may empty anything,
 * without a UI-side role check. That matters here: this file's own rule is
 * that the BACKEND decides what a caller may see (see the note on
 * `connections` in ExplorerConfig), and a client-side `role === "admin"` would
 * be us guessing at an answer the server is willing to give.
 */
const trashRetentionDays = ref<number | null>(null);
/** True only when the server has confirmed this caller may purge. */
const trashCanEmpty = ref(false);
const trashEmptying = ref(false);
const showTrashConfirm = ref(false);
/** The purge being followed, while the server reports it as running. */
const trashEmptyRun = ref<TrashEmptyStatus | null>(null);
/* Set when the explorer is taken down: it stops the following — the purge
 * is the server's and carries on. */
let trashEmptyUnwatched = false;
onBeforeUnmount(() => {
  trashEmptyUnwatched = true;
});

async function probeTrashPolicy() {
  trashRetentionDays.value = null;
  trashCanEmpty.value = false;
  try {
    const res = await fetch(`${props.config.apiBase ?? ''}/api/admin/protection`, {
      headers: await buildAuthHeaders(),
      credentials: api.credentialsMode(),
    });
    /* ⚠ A 403 is the ANSWER "you are not an operator", not a failure: it is
     * the ordinary case for every end user, and it must not reach the error
     * emitter or the toast. */
    if (!res.ok) return;
    const body = (await res.json()) as { trash_retention_days?: unknown };
    const d = body?.trash_retention_days;
    if (typeof d === 'number' && d > 0) trashRetentionDays.value = d;
    trashCanEmpty.value = true;
  } catch {
    /* offline / CORS — same as "not allowed": say nothing we cannot verify */
  }
}

/** What the banner promises. Named the number, or explicitly not. */
const trashBannerText = computed(() =>
  trashRetentionDays.value !== null
    ? t('trash.retention', { days: trashRetentionDays.value })
    : t('trash.retention_unknown'),
);

/** How much is about to go. The confirmation names both, because "empty the
 *  trash?" with no quantity is a question nobody can answer. */
const trashTotalBytes = computed(() =>
  files.value.reduce((sum, n) => {
    const v = typeof n.size === 'number' ? n.size : (n as Record<string, unknown>).file_size;
    return sum + (typeof v === 'number' ? v : 0);
  }, 0),
);
/** ⚠ A total of zero is reported as "we do not know", not as "0 B". A server
 *  that does not send sizes would otherwise have us telling somebody that
 *  deleting their files frees nothing, on the last screen before it happens.
 *  A genuinely empty set never reaches here — the button is disabled. */
const trashSizeKnown = computed(() => trashTotalBytes.value > 0);
/** Which sentence the confirmation uses: with or without a size. Singular or
 *  plural is `t()`'s business (composables/useLocale → countedKey). */
const trashConfirmKey = computed(() =>
  trashSizeKnown.value ? 'trash.empty_confirm_body' : 'trash.empty_confirm_body_nosize',
);

/** "Emptying the trash… 120 of 61,844" while a purge is followed. */
const trashEmptyProgress = computed(() => {
  const run = trashEmptyRun.value;
  if (!run?.running) return '';
  if (run.queued) return t('trash.emptying_queued');
  const nf = new Intl.NumberFormat(localeTag(locale.value));
  return t('trash.emptying', { done: nf.format(run.scanned ?? 0), total: nf.format(run.total ?? 0) });
});

/* ⚠⚠ A 2xx is not "emptied" any more. The endpoint used to purge inside the
 * request, so a large trash never answered — nginx's 504 at sixty seconds
 * became a toast reading "504". It now answers within seconds: the final
 * counts, or 202 while the purge goes on in the background, which
 * lib/trashEmpty follows on GET until the run ends. */
async function emptyTrash() {
  showTrashConfirm.value = false;
  if (!trashCanEmpty.value || trashEmptying.value) return;
  trashEmptying.value = true;
  const url = `${props.config.apiBase ?? ''}/api/admin/trash/empty`;
  try {
    const end = await emptyTrashAndFollow({
      start: async () =>
        fetch(url, { method: 'POST', headers: await buildAuthHeaders(), credentials: api.credentialsMode() }),
      status: async () => fetch(url, { headers: await buildAuthHeaders(), credentials: api.credentialsMode() }),
      /* The run is an ops row: it goes into the operations centre (progress,
       * Cancel) the moment the server names it — a run done within the wait
       * as well, like any other operation the person started. */
      onStart: (run) => {
        if (!run.op_id) return;
        pendingOps.register({
          id: run.op_id,
          kind: 'trash-empty',
          status: run.running ? (run.queued ? 'pending' : 'running') : run.cancelled ? 'cancelled' : 'ok',
          total: run.total ?? 0,
          done: run.scanned ?? 0,
        });
      },
      onProgress: (run) => {
        trashEmptyRun.value = run;
      },
      onBusy: () => flashToast(t('trash.empty_busy')),
      stopped: () => trashEmptyUnwatched,
    });
    if (end === null) return;
    if (end.error) throw new Error(t('trash.empty_stopped', { error: end.error }));
    /* Only the view that is still showing the trash is redrawn: somebody who
     * went on to a folder while it ran is not pulled back into the trash. */
    if (trashMode.value) await loadTrash();
    /* `{running: false}` with no start is a run the server no longer knows —
     * it restarted under it. The listing just reloaded says what is left, and
     * nothing is claimed about the rest. */
    if (!end.started_at) return;
    if (end.cancelled) {
      flashToast(t('trash.empty_cancelled', { count: end.purged ?? 0 }));
      return;
    }
    flashToast(end.failed ? t('trash.emptied_partly', { count: end.failed }) : t('trash.emptied'));
  } catch (err) {
    if (err instanceof TrashEmptyBusy) {
      flashToast(t('trash.empty_busy'));
      return;
    }
    const msg = err instanceof Error ? err.message : String(err);
    emit('error', { message: msg, context: { op: 'trash:empty' } });
    flashToast(msg);
  } finally {
    trashEmptyRun.value = null;
    trashEmptying.value = false;
  }
}

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
          /* ⚠ The ORIGINAL path, which the server sends with its leading
             slash: `depo:///x.txt` put an empty segment in the Location
             column ("depo/"), so it is dropped here. */
          path: e.storage_name ? `${e.storage_name}://${e.path.replace(/^\/+/, '')}` : e.path,
          basename: e.name,
          extension: e.name.includes('.') ? e.name.split('.').pop() || '' : '',
          storage: e.storage_name || '',
          visibility: 'private',
          /* tablo:t1 — ⚠ BOTH. Every view reads `size` (the Size column, the
             info panel, the empty-trash confirmation); only the upload code
             reads `file_size`. Setting one of the two left every trashed row
             with a blank Size cell and made "this permanently deletes 2 items
             (0 B)" a false statement about two real files. */
          size: e.size,
          file_size: e.size,
          mime_type: e.mime || '',
          /* The date the Trash's date column prints and sorts by — WHEN it was
             deleted. A trashed row has no modification date to show, and the
             column read "—" for every row. */
          last_modified: Date.parse(e.deleted_at) || undefined,
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

/**
 * Does anything in `targetWire` already carry the name of one of `sources`?
 * Asked BEFORE a move is queued, because the answer decides whether the move
 * can be undone.
 *
 * ⚠ The server never moves onto a taken name — it keeps both, and the moved
 * item lands as `name-copy` (ops.MoveDest). The undo moves `target/<name>`
 * back, so after a collision it would move the item that WAS ALREADY THERE.
 * A colliding move is therefore offered no undo, and says so.
 *
 * Case-insensitive, and "could not list" counts as a collision: both err on
 * the side of withholding an undo rather than offering one that moves the
 * wrong file.
 */
async function movedNamesCollide(sources: string[], targetWire: string): Promise<boolean> {
  try {
    const res = await api.index(targetWire);
    const taken = new Set((res.files ?? []).map((f) => String(f.basename ?? '').toLowerCase()));
    return sources.some((s) => taken.has(wireBasename(s).toLowerCase()));
  } catch {
    return true;
  }
}

// Register the inverse of a queued async move under its op id: once the op
// settles OK, the toast offers "Geri Al" which queues the reverse move. The
// inverse op deliberately gets NO undo entry of its own (no redo ping-pong).
function registerMoveUndo(
  opId: number,
  sources: string[],
  targetWire: string,
  originWire: string | undefined,
  collides: boolean,
) {
  if (!originWire || !targetWire || collides) return;
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
  //
  // ⚠⚠ `history.state`, NEVER `null`. This mirrors the current folder into the
  // hash, and it runs inside a host that may be a router-driven SPA: the admin
  // app is vue-router, which keeps its own bookkeeping (scroll position, the
  // position counter, `back`/`forward` links) in `history.state`. Passing
  // `null` here erased it, vue-router warned
  //   "history.state seems to have been manually replaced without preserving
  //    the necessary values"
  // and the NEXT navigation — into Home, or out to the admin panel — rendered
  // a blank page. Measured 2026-09-13. Preserving the object costs nothing:
  // the explorer has no state of its own to put there, only a URL to change.
  history.replaceState(
    history.state,
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
defineExpose({
  reload: () => load(),
  /* App plugins — a notification's `target.open` lands here (see the
     function's own note for why this is a method and not a prop). */
  openAppTarget,
});

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
  // ⚠ Not awaited here — the listing must not wait on it — but tracked, so a
  // caller that genuinely needs the answer (openAppTarget) can await the same
  // promise instead of reading a ref that may not be filled yet.
  void loadCapabilities();
});

// --------------------------------------------------------------------
// Keyboard
// --------------------------------------------------------------------

/* cila:c wiring — command palette (Ctrl/Cmd+K) + shortcuts help (?) state */
const showPalette = ref(false);
const showShortcutsHelp = ref(false);
/* /cila:c wiring */

/* bul:s3 — palette "everywhere" search + open-hit navigation */

/** What this mount knows about drives, for a hit that does not name its own. */
function hitDrives(): HitDriveContext {
  return { configured: (props.config.storages ?? []).map((s) => s.name), current: adapter.value };
}

/**
 * #47 — the account a hit belongs to when it is NOT this mount's own, else
 * null. Only a host that searches several accounts stamps hits at all.
 */
function foreignAccountOf(hit: GlobalSearchHit): SearchAccount | null {
  const hook = props.config.accountSearch;
  const acc = hit.account;
  if (!hook || !acc || acc.id === hook.self.id) return null;
  return acc;
}

/** #47 — the hit as an item. Another account's hit is addressed by what the
 *  HIT says and nothing else: this mount's drives are not that server's. */
function paletteHitItem(hit: GlobalSearchHit) {
  return hitItem(hit, foreignAccountOf(hit) ? { configured: [], current: '' } : hitDrives());
}

// Debounce/min-chars live in the palette; this is the API call — and, when
// the host holds several accounts (#47), one call per account through the
// host's hook, each answer stamped with its account. One account failing
// costs its own group, not the others'.
async function paletteGlobalSearch(q: string): Promise<GlobalSearchHit[]> {
  const own = api.globalSearch(q, { limit: 8, scope: 'all' });
  const hook = props.config.accountSearch;
  if (!hook) return own;
  let others: SearchAccount[] = [];
  try {
    others = (await hook.others()) ?? [];
  } catch {
    others = [];
  }
  if (others.length === 0) return own;
  const accounts = [hook.self, ...others];
  const answers = await Promise.allSettled([
    own,
    ...others.map((a) => hook.search(a.id, q, { limit: 8, scope: 'all' })),
  ]);
  const out: GlobalSearchHit[] = [];
  answers.forEach((r, i) => {
    if (r.status !== 'fulfilled' || !Array.isArray(r.value)) return;
    for (const h of r.value) out.push({ ...h, account: accounts[i] });
  });
  return out;
}

/**
 * Open a global-search hit: navigate to the file's folder, then select +
 * preview it through the existing openNode mechanics. Hits come back as raw
 * node rows (in-storage relative `path`, numeric `storage_id`), so the
 * storage segment for multi-storage mode is resolved by `hitStorageName`
 * (lib/searchHit): the name on the hit > the only configured storage > the
 * storage currently open. A wrong guess lands on the existing "folder not
 * found" state, which is already a graceful dead-end.
 */
async function openSearchHit(hit: GlobalSearchHit) {
  const rel = hitRelPath(hit);
  if (!rel) return;
  const isDir = hit.type === 'dir';
  const slash = rel.lastIndexOf('/');
  const targetRel = isDir ? rel : slash === -1 ? '' : rel.slice(0, slash);
  let target = targetRel;
  if (multiStorageRoot.value) {
    const storageName = hitStorageName(hit, hitDrives());
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

/* === #47 — the palette's hit verbs ====================================
 *
 * Open, download and drag out, each answered by what a LISTING row of the
 * same file already uses: `openSearchHit` → openNode, `downloadSelection`,
 * `handDragOut`. Another account's hit goes to the host's hook instead — the
 * explorer holds no credential for that server.
 */

function reportHookFailure(op: string) {
  return (err: unknown) => emit('error', { message: (err as Error)?.message ?? String(err), context: { op } });
}

function onPaletteOpenHit(hit: GlobalSearchHit) {
  const acc = foreignAccountOf(hit);
  const hook = props.config.accountSearch;
  if (acc && hook) {
    const item = paletteHitItem(hit);
    if (item) void Promise.resolve(hook.open(acc.id, item)).catch(reportHookFailure('search-open'));
    return;
  }
  void openSearchHit(hit);
}

/** Which verbs a hit row offers. Mirrors what a listing row of it would do. */
function paletteHitCan(hit: GlobalSearchHit, action: 'download' | 'drag'): boolean {
  const item = paletteHitItem(hit);
  if (!item) return false;
  if (foreignAccountOf(hit)) {
    const hook = props.config.accountSearch;
    return action === 'download' ? !!hook?.download : !!hook?.dragStart;
  }
  if (action === 'download') return true;
  // The shell carries anything; the browser's own path carries ONE file — on
  // the plain download URL where the session is a cookie, on a minted link
  // where it is a bearer (#71, lib/dragOut createDragLinks), and not at all
  // against a server too old to mint one.
  return (
    !!dragOut.value ||
    (item.type === 'file' && (canDownloadUrlDrag(props.config.auth) || dragLinksSupported.value))
  );
}

/** #71 — the pointer rests on / presses a hit: get its drag-out link ready. */
function onPaletteWarmHit(hit: GlobalSearchHit) {
  if (foreignAccountOf(hit)) return;
  const item = paletteHitItem(hit);
  if (item && item.type === 'file') warmDragLink(item.path);
}

function downloadSearchHit(hit: GlobalSearchHit) {
  const item = paletteHitItem(hit);
  if (!item) return;
  const acc = foreignAccountOf(hit);
  if (acc) {
    const hook = props.config.accountSearch;
    if (hook?.download) void Promise.resolve(hook.download(acc.id, item)).catch(reportHookFailure('search-download'));
    return;
  }
  // The same row the advanced search draws for this hit, through the same
  // download a selected listing row takes: a file is its own body, a folder
  // one streaming archive.
  void downloadSelection([hitToNode(hit, hitStorageName(hit, hitDrives()))]);
}

function onPaletteDragHit(hit: GlobalSearchHit, ev: DragEvent) {
  const item = paletteHitItem(hit);
  if (!item || !ev.dataTransfer) {
    ev.preventDefault();
    return;
  }
  ev.dataTransfer.effectAllowed = 'copy';
  const acc = foreignAccountOf(hit);
  if (acc) {
    const hook = props.config.accountSearch;
    // Always the OS drag: the browser's own path would fetch another
    // server's file with THIS page's session.
    ev.preventDefault();
    if (hook?.dragStart) void Promise.resolve(hook.dragStart(acc.id, [item])).catch(reportHookFailure('drag-out'));
    return;
  }
  // `null` origin: a hit is dragged OUT, never moved — the palette covers the
  // listing, so there is no folder in this window for it to land on.
  handDragOut(ev, [item], null, typeof hit.mime === 'string' ? hit.mime : undefined);
}

/** A hit drag let go inside the palette: the shell stops waiting for a drop. */
function onPaletteDropInside() {
  endNativeDrag();
  cancelShellDrag();
}
/* /bul:s3 */

useKeyboardShortcuts(rootEl, {
  onDelete: () => {
    /* ui-fix — the shortcut goes to the active pane too (consistent with the menu). */
    if (paneIsActive.value) {
      const psel = splitSelection.nodes.value;
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
      const psel = splitSelection.nodes.value;
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
  onSelectAll: () => (paneIsActive.value ? splitSelection.selectAll() : selection.selectAll()) /* wiring:d1 pane-route */,
  onOpen: () => {
    /* wiring:d1 pane-route — one `openNode`, whichever pane asked. A folder
       opens IN the pane that had the keyboard; a file opens in the preview,
       which is the window's, so there is nothing to route. */
    if (paneIsActive.value) {
      const pn = splitSelection.nodes.value[0];
      if (pn) onPaneOpen('split', pn);
      return;
    }
    const n = selection.nodes.value[0];
    if (n) openNode(n);
  },
  onClose: () => {
    showNewFolder.value = false;
    showRename.value = false;
    showDelete.value = false;
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
  /* tus:t1 — the menu verbs, routed through the SAME dispatcher the right-click
   * menu and the toolbar use (dispatchItemAction), so a key and a click cannot
   * drift apart. The ones that act on a selection do nothing without one, which
   * is what the menu does too. */
  onNewFolder: () => {
    showNewFolder.value = true;
  },
  onUpload: () => triggerUpload(),
  onRefresh: () => void load(),
  onDownload: () => void dispatchItemAction('download', activeTargets()),
  onPreview: () => void dispatchItemAction('preview', activeTargets()),
  onShare: () => void dispatchItemAction('access', activeTargets()),
  onTags: () => void dispatchItemAction('tags', activeTargets()),
  onConvert: () => void dispatchItemAction('convert', activeTargets()),
  onOpenTab: () => void dispatchItemAction('open-tab', activeTargets()),
  onCopyPath: () => {
    const n = activeTargets()[0];
    if (n) void onCopyPath(n.path);
  },
  onCopyId: () => void dispatchItemAction('copy-id', activeTargets()),
  onRestore: () => void dispatchItemAction('restore', activeTargets()),
  /* /tus:t1 */
  hasSelection: () => !selection.isEmpty.value,
});

/**
 * tus:t1 / pane:p1 — THE rows a verb acts on, whoever asked for it.
 *
 * The active pane's selection when the split pane has focus, otherwise the
 * main listing's. It began as the keyboard's rule (hence tus:t1) and it is now
 * everybody's: the keyboard shortcuts, the selection bar's count and mode, the
 * action list the bar renders and the handler it dispatches through all read
 * this one function.
 *
 * ⚠ Measured 2026-09-13, before that was true: the toolbar read
 * `selection.nodes` directly, so ticking rows in the RIGHT pane raised no bar
 * at all while the LEFT pane's bar went on describing a selection nobody was
 * touching. Two answers to "what is selected" is how that happens; there is
 * one now.
 */
function activeTargets(): FileNode[] {
  if (paneIsActive.value) return splitSelection.nodes.value;
  return selection.nodes.value;
}

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
  /* ⚠⚠ issue #34 — A SYMLINK THE SERVER WILL NOT FOLLOW IS REFUSED HERE,
     OUT LOUD, and this is the ONE funnel every way of opening a row passes
     through (double-click, Enter, the menu's Open, the command palette, a
     deep link's auto-open), so the answer is the same everywhere.

     The reported bug was the silence: an out-of-root link arrived looking like
     an ordinary 0-byte file, and clicking it did nothing at all. Letting the
     request go instead is barely an improvement — the driver's containment
     refusal surfaces as a generic failure, which reads as "filex is broken"
     rather than "this boundary is a setting somebody chose". So: no request,
     and a toast carrying the sentence from lib/symlink, which is the same
     sentence the row's badge and the details panel already show. Nothing is
     lost by not asking; the server would refuse it too, and only this side
     knows the wording that names the setting. */
  const link = linkWordsFor(n, { t });
  if (link) {
    /* ⚠ 8 seconds, not flashToast's 2.5: this is a two-clause explanation
       that names a setting, and a sentence nobody finishes reading is the
       silence again with extra steps. Same budget undoToast uses for the
       other toast people are expected to act on. */
    showToast({ message: link.why }, 8000);
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
  // Host-owned open (desktop app): a file opens in the host's OWN window per
  // document, not in the in-page overlay. Emit and stop — the host listens on
  // `file-opened` and spawns the window. Directories still navigate inline
  // (handled above); Space quick-look still peeks in-page. E2E files fell into
  // the decrypted in-page branch above, so a host window never gets ciphertext.
  if (openSurface(props.config, n) === 'host') {
    emit('file-opened', { path: n.path, basename: n.basename });
    void markRecent(n);
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
  previewMode.value = nodeCanEdit(n)
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

/** A restore on its way. It is one request per item, and a folder is moved
 *  back object by object on an object store: minutes, with nothing on screen,
 *  and a second press met "something already has that name". */
const restoreBusy = ref(false);

async function restoreSelection(targets?: FileNode[]) {
  if (restoreBusy.value) return;
  const nodes = targets ?? selection.nodes.value;
  if (nodes.length === 0) return;
  restoreBusy.value = true;
  try {
    // filex trash: restore by node id, then refresh the trash listing.
    if (trashMode.value) {
      const ids = nodes
        .map((n) => (n as { id?: number }).id)
        .filter((x): x is number => typeof x === 'number');
      if (serverQueues('restore')) {
        // One request for the selection, answered at once with its jobs: the
        // operations centre follows them, and the trash listing, with what did
        // not come back and why, comes when each ends (onSettled).
        const { ops } = await api.restoreQueued(ids);
        for (const op of ops) pendingOps.register(op);
        flashToast(t('toast.restoring', { n: ids.length }));
        selection.clear();
        return;
      }
      showToast({ message: t('toast.restoring', { n: ids.length }) }, STICKY_TOAST_MS);
      const { restored, taken, failed, failure } = await api.restoreIds(ids);
      if (taken.length) {
        showToast({ message: t('toast.restore_taken', { n: taken.length, name: taken[0] }) }, ERROR_TOAST_MS);
      } else if (failed > 0) {
        // ⚠ Said, not skipped: what did not come back used to vanish from the
        // count, and "2 items restored" stood over a selection of three.
        showToast(
          { message: t('toast.restore_partial', { n: restored, failed, reason: failureText(failure) }) },
          ERROR_TOAST_MS,
        );
      } else {
        flashToast(t('toast.restored', { n: restored }));
      }
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
    showToast({ message: failureText(err) }, ERROR_TOAST_MS);
    emit('error', { message: (err as Error).message, context: { op: 'restore' } });
  } finally {
    restoreBusy.value = false;
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
  // Host-owned open (desktop): the context-menu "Aç" opens the host window
  // too, so Enter/double-click/"Aç" are ONE open path — not a window here and
  // a system-browser tab there. openNode carries the `file-opened` emit.
  if (props.config.openInHost) {
    openNode(n);
    return;
  }
  // RBAC: a viewer (no edit on this item) can't use the editable "Aç"
  // surface — drop to the read-only in-page preview instead.
  if (!nodeCanEdit(n)) {
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

/* pane:p1 — `activeTargets()`, not `selection`: the bar describes the pane the
 * keyboard is in. See the function's own note for what reading `selection`
 * directly here used to cost. */
const selectionMode = computed<SelectionMode>(() => {
  const sel = activeTargets();
  if (sel.length === 0) return 'none';
  if (sel.length === 1) return sel[0].type === 'dir' ? 'single-dir' : 'single-file';
  return 'multi';
});

async function onToolbarAction(key: string) {
  const sel = activeTargets();
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
    { key: 'keep-local', label: t('ctx.keep_local'), hidden: st === 'kept' || st === 'inherited' },
    { key: 'keep-online', label: t('ctx.keep_online'), hidden: st !== 'kept' },
    { key: 'keep-inherited', label: t('ctx.keep_inherited'), disabled: true, hidden: st !== 'inherited' },
    { key: 'keep-reveal', label: t('ctx.keep_reveal'), hidden: st !== 'kept' && st !== 'inherited' },
  ];
}

/**
 * The context target when it is NOT part of this pane's listing.
 *
 * Home's Recent and Starred cards are the only rows in the product that a
 * person can right-click without them being in `files` — they come from their
 * own endpoints. `selection.nodes` can never resolve them (it filters the
 * listing by selected path), so without this the menu is built from an empty
 * selection and renders the blank-canvas menu. Empty in every other case, so
 * the ordinary selection path is unaffected.
 */
const ctxUnlistedTargets = ref<FileNode[]>([]);

async function onContextTarget(node: FileNode, ev: MouseEvent) {
  ctxMode.value = 'selection';
  void refreshKept(); // menu labels react if the kept set changed since last look
  if (!selection.has(node.path)) {
    selection.click(node.path);
    await nextTick();
  }
  // ⚠⚠ Fall back to the node that was actually clicked.
  //
  // `selection.nodes` is the CURRENT LISTING filtered by the selected paths
  // (`useSelection(() => displayOrder ?? files)`), so it can only ever resolve
  // a row that is in this pane's listing. Home is not a listing: its Recent
  // and Starred cards come from their own endpoints and are absent from
  // `files`, so selecting one left `selection.nodes` EMPTY and the menu opened
  // with zero targets — which is the signature of a right-click on blank
  // canvas. Measured 2026-09-13: the ⋮ and the right-click on every Home card
  // opened a one-line "Show hidden files" menu instead of the fifteen-line
  // file menu, in both Recent and Starred. HomeView's own header says these
  // cards carry "the same right-click menu as the listing", so this is the
  // contract being restored, not a new behaviour.
  //
  // In a real listing `selection.nodes` is non-empty by the line above, so
  // multi-selection is untouched — this only rescues the case where the path
  // cannot be resolved against the current pane.
  //
  // ⚠ BOTH halves are needed. `show()` decides what the chosen action RUNS on;
  // `contextActions` decides what the menu LISTS, and it reads
  // `selection.nodes` on its own. Setting only the first left the actions
  // correct and the menu still empty, which looks identical to the bug.
  ctxUnlistedTargets.value = selection.nodes.value.length ? [] : [node];
  const targets = selection.nodes.value.length ? selection.nodes.value : [node];
  ctxRef.value?.show({ clientX: ev.clientX, clientY: ev.clientY }, targets);
}

function onContextCanvas(ev: MouseEvent) {
  ev.preventDefault();
  ctxMode.value = 'selection';
  selection.clear();
  // Blank canvas has no target — drop any node left over from a card menu, or
  // the next right-click on empty space would offer that file's actions.
  ctxUnlistedTargets.value = [];
  ctxRef.value?.show({ clientX: ev.clientX, clientY: ev.clientY }, []);
}

function onCrumbContext(payload: { x: number; y: number; adapterPath: string; label: string }) {
  ctxMode.value = 'breadcrumb';
  breadcrumbCtxPath.value = payload.adapterPath;
  breadcrumbCtxLabel.value = payload.label;
  ctxRef.value?.show({ clientX: payload.x, clientY: payload.y }, []);
}

/**
 * pane:p1 — right-click in EITHER pane, through one door.
 *
 * The menu itself was already single-source (`selectionActionList`); this is
 * the other half — the two panes no longer reach it through two different
 * handlers, so a change to how a right-click picks its targets cannot land in
 * one pane and miss the other. `node === null` is a right-click on empty
 * space: the selection-less menu ("New folder" + "Paste").
 */
async function onPaneMenu(pane: 'main' | 'split', node: FileNode | null, ev: MouseEvent) {
  void refreshKept(); // menu labels react if the kept set changed since last look
  if (pane === 'split') {
    activePane.value = 'split';
    const sel = splitSelection.nodes.value;
    if (node && !splitSelection.has(node.path)) {
      splitSelection.click(node.path);
      await nextTick();
    }
    const after = splitSelection.nodes.value;
    paneCtxTargets.value = node ? (after.length > 0 ? after : (sel.length > 0 ? sel : [node])) : [];
    ctxMode.value = 'pane';
    ctxRef.value?.show({ clientX: ev.clientX, clientY: ev.clientY }, paneCtxTargets.value);
    return;
  }
  activePane.value = 'main';
  ctxMode.value = 'selection';
  if (!node) {
    selection.clear();
    ctxRef.value?.show({ clientX: ev.clientX, clientY: ev.clientY }, []);
    return;
  }
  await onContextTarget(node, ev);
}

const contextActions = computed<ContextAction[]>(() => {
  if (ctxMode.value === 'breadcrumb') {
    return [
      { key: 'open', label: t('ctx.open') },
      { key: 'copy-path', label: t('breadcrumb.copy_path') },
    ];
  }
  if (ctxMode.value === 'pane' /* ui-fix — side-pane menu is EXACTLY the main pane's */) {
    const psel = paneCtxTargets.value;
    if (psel.length === 0) {
      // Right-click on empty space: same as the main pane's canvas menu.
      return [
        { key: 'new-folder', label: t('toolbar.new_folder') },
        { key: 'paste', label: t('ctx.paste'), disabled: !clipboard.value.mode },
      ];
    }
    return selectionActionList(psel);
  }

  // `ctxUnlistedTargets` is non-empty only for a row that exists outside this
  // pane's listing (Home's Recent / Starred cards) — see `onContextTarget`.
  const sel = selection.nodes.value.length ? selection.nodes.value : ctxUnlistedTargets.value;
  const any = sel.length > 0;
  const single = sel.length === 1;

  if (trashActive.value) {
    if (!any) return [];
    return [
      { key: 'restore', label: t('ctx.restore') },
      { divider: true, key: 'sep1', label: '' },
      { key: 'delete', label: t('ctx.delete_perm'), danger: true },
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
      { key: 'open', label: t('ctx.open') },
      { key: 'open-tab', label: t('ctx.open_new_tab') } /* wiring:d1 */,
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
      },
    ];
    if (!permCanEdit(dirPerm.value)) return view;
    return [
      { key: 'new-folder', label: t('toolbar.new_folder') },
      { key: 'paste', label: t('ctx.paste'), disabled: !clipboard.value.mode },
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
  /* pane:p1 — a STORAGE row is a mount point, not a file: rename, delete, cut,
   * copy and share all 4xx on it. The main listing answers this before it ever
   * gets here (the `inStorageRoot` branch in `contextActions`), but the split
   * pane lists the same virtual rows through no such branch — so its
   * right-click menu has been offering "Sil" on a whole storage, and with the
   * selection bar now reaching both panes it would be one click. Measured
   * 2026-09-13: download/share/cut/copy/delete, all of them, on `depoB`.
   * Answered HERE because this is the one list both surfaces render. */
  if (sel.some(isStorageRow)) {
    if (!single) return [];
    return [
      { key: 'open', label: t('ctx.open') },
      { key: 'open-tab', label: t('ctx.open_new_tab') },
      ...keepActionsFor(sel),
    ];
  }
  const isFile = single && sel[0]?.type === 'file';
  const tagsLabel = t('ctx.tags_menu');
  const isArchive = single && isArchiveFile(sel[0]);
  const singleHasId = single && typeof sel[0]?.id === 'number';
  /* yildiz:s1 */
  const canStar = starableNodes(sel).length > 0;
  const allStarred = selectionAllStarred(sel);
  // RBAC: gate mutating actions when the caller lacks edit on the target. The
  // "İzinler" (permissions) action shows only for owners on RBAC-on storages.
  const p = selPerm(sel);
  // May write to the selection: the weakest row's level, and no row on a
  // read-only mount — `selReadOnly` is what `dirReadOnly` cannot answer in a
  // view whose rows come from several storages (Recent, Starred, a tag, Home).
  const w = permCanEdit(p) && !selReadOnly(sel);
  // Unified "Paylaş / İzinler" popup: public share link (editor+) + per-user
  // permissions (owner-only, decided inside the modal).
  // Unified "Paylaş / İzinler" popup carries the public share link, per-user
  // permissions AND the folder-only "Dosya İste" (file-drop) tab — the user
  // picks the action from inside the modal, so there's no separate button.
  const accessLabel = t('shortcuts.share'); // the same words the shortcut list uses
  /* ⚠ "Open" on an office document IS "open in ONLYOFFICE": every path it
   * takes (the standalone editor tab, the desktop's host window, the in-page
   * modal) ends at the document server. With none configured it used to open
   * a tab that printed `Config fetch 503: {"error":"onlyoffice not
   * configured"}`. The owner's rule (2026-09-21): an administrator sees it
   * greyed with where to set it up; everybody else is not offered it. Preview
   * stays — it says the same thing in words and offers the download. */
  const openExt = String(sel[0]?.extension ?? '').toLowerCase();
  const openGate = !isFile
    ? {}
    : isOfficeExt(openExt)
      ? gateOnService(!!effectiveOnlyOfficeBase.value, callerAdmin.value, t('ctx.needs_onlyoffice'))
      : openExt === 'drawio' || openExt === 'dio'
        ? gateOnService(!!effectiveDrawioUrl.value, callerAdmin.value, t('ctx.needs_drawio'))
        : {};
  /* The legacy converter: configured-and-healthy is the only state in which
     it is offered as working, and never beside the Convert app. */
  const convertGate = legacyConvert.value;
  return [
    { key: 'open', label: t('ctx.open'), ...openGate, hidden: !single || openGate.hidden === true },
    { key: 'open-tab', label: t('ctx.open_new_tab'), hidden: !single || sel[0]?.type !== 'dir' } /* wiring:d1 — open the folder in a new tab */,
    { key: 'preview', label: t('ctx.preview'), hidden: !single, disabled: !isFile },
    /* tasi:m1 — Download works on ANY selection now. It used to disappear the
       moment a second row was ticked, because the only implementation was one
       `window.open` per node and the browser blocks the second popup; there is
       one streaming archive behind it now (lib/downloadSelection), and the
       server expands a selected folder itself, so a lone folder is a zip too.
       ⚠ Still single-only inside an encrypted folder: those bytes are
       decrypted IN THE BROWSER, one file at a time, and the server has no
       plaintext to zip. */
    { key: 'download', label: t('ctx.download'), hidden: !any || (e2eActive.value && !single), disabled: !any },
    {
      key: 'convert',
      label: t('ctx.convert'),
      title: convertGate.title,
      hidden: !single || convertGate.hidden === true || !w || e2eActive.value /* wiring:e2 — convert is meaningless on ciphertext */,
      disabled: !isFile || convertGate.disabled === true,
    },
    { key: 'archive-create', label: t('ctx.archive_create'), hidden: !any || (single && isArchive) || !w || e2eActive.value || archiveAllowedFormats.value.length === 0, disabled: !any },
    { key: 'archive-extract', label: t('ctx.archive_extract'), hidden: !isArchive || !w || e2eActive.value, disabled: !isArchive },
    { key: 'archive-extract-here', icon: 'archive-extract', label: t('archive.extract_here'), hidden: !isArchive || !w || e2eActive.value, disabled: !isArchive },
    /* tasi:m1 — VISIBLE and grey above a multi-selection, not gone. Sharing
       really is one item at a time (a share link addresses one node), and the
       row now says so in its tooltip; vanishing taught the reader that filex
       cannot share the thing they are looking at. */
    {
      key: 'access',
      label: accessLabel,
      hidden: !any || !w || e2eActive.value /* wiring:e2 — sharing is off in the MVP (the link would serve ciphertext) */,
      disabled: !single,
      title: single ? undefined : t('ctx.access.one_only'),
    },
    { key: 'details', label: t('ctx.details'), hidden: !any } /* koru:k1 */,
    /* ⚠ "Copy node id" is NOT here any more (owner's call, 2026-09-13): it is a
       developer's handle on a support ticket, not an everyday verb, and this
       list is rendered by BOTH the right-click menu and the selection bar — so
       one row put it in front of everyone, twice. It lives in the details
       panel now, beside Path and ETag, which is where the other technical
       facts about a file already are. The `copy-id` case below stays: the
       panel dispatches it. */
    { divider: true, key: 'sep1', label: '', hidden: !w },
    { key: 'rename', label: t('ctx.rename'), hidden: !single || !w, disabled: !single },
    { key: 'cut', label: t('ctx.cut'), hidden: !any || !w, disabled: !any },
    { key: 'copy', label: t('ctx.copy'), hidden: !any, disabled: !any },
    /* tasi:m1 — "somewhere else", without the clipboard. Cut+paste has always
       been able to do this, but only by navigating away from the rows you had
       just picked; these two ask WHERE in a dialog and leave the listing where
       it is.
       ⚠ `icon:` is not decoration here — `actionIconSvg` answers '' for a key
       it does not know, and a bar button with no glyph is an empty 28px box.
       They borrow the clipboard verbs' marks, which is what they are. */
    { key: 'move-to', label: t('ctx.move_to'), icon: 'cut', hidden: !any || !w, disabled: !any },
    { key: 'copy-to', label: t('ctx.copy_to'), icon: 'copy', hidden: !any, disabled: !any },
    /* Paste needs a destination folder; a virtual view (Recent / Starred /
       Shared / a tag / Home) has none, so it is the one write verb the row
       menu does not carry there — every per-row verb above and below does. */
    { key: 'paste', label: t('ctx.paste'), hidden: !w || atVirtualRoot.value, disabled: !clipboard.value.mode },
    { divider: true, key: 'sep-meta', label: '', hidden: !singleHasId && !canStar },
    /* yildiz:s1 — "star must be an action, like a tag" (owner, v0.30.0).
       Beside Tags on purpose: they are the same kind of verb, and this is the
       ONLY star a grid/gallery user reaches with the keyboard. Works on a
       multi-selection; the label follows the selection's state. */
    {
      key: 'star',
      label: allStarred ? t('ctx.unstar') : t('ctx.star'),
      icon: allStarred ? 'unstar' : 'star' /* gorunum:v1-icons — names the glyph, not an emoji */,
      hidden: !canStar,
    },
    { key: 'tags', label: tagsLabel, hidden: !singleHasId, disabled: !singleHasId },
    /* App plugins — one row per action whose `applies` rule accepts the
       selection, under its own `sep-plugins` divider; `[]` when the feature
       is off, in the trash, or inside an encrypted folder (lib/pluginMenu). */
    ...pluginActionRows(sel),
    ...keepActionsFor(sel),
    { divider: true, key: 'sep2', label: '', hidden: !w },
    { key: 'delete', label: t('ctx.delete'), danger: true, hidden: !any || !w, disabled: !any },
  ];
}

const ARCHIVE_EXTENSIONS = new Set(['zip', '7z', 'rar', 'tar', 'gz', 'tgz', 'bz2', 'tbz2', 'xz', 'txz']);

function isArchiveFile(node: FileNode | undefined): boolean {
  return !!node && node.type === 'file' && ARCHIVE_EXTENSIONS.has((node.extension || '').toLowerCase());
}

// toolbarActions — what the top toolbar shows. Mirrors the context menu so the
// two stay identical for a selection; the empty/trash/virtual-root cases match
// the context menu's special branches.
const toolbarActions = computed<ContextAction[]>(() => {
  const sel = activeTargets();
  /* pane:p1 — the split pane's bar offers EXACTLY what its own right-click
   * menu offers (`contextActions`, ctxMode 'pane'). The three special branches
   * below describe the MAIN listing's state — the trash, the multi-storage
   * root — and the split pane reaches neither through this computed. */
  if (paneIsActive.value) return sel.length ? selectionActionList(sel).filter((a) => a.key !== 'archive-extract-here') : [];
  if (trashActive.value) {
    if (sel.length === 0) return [];
    return [
      { key: 'restore', label: t('ctx.restore') },
      { key: 'delete', label: t('ctx.delete_perm'), danger: true },
    ];
  }
  const trimmedPath = (currentPath.value ?? '').replace(/^\/+|\/+$/g, '');
  if (multiStorageRoot.value && trimmedPath === '') {
    return sel.length === 1 ? [{ key: 'open', label: t('ctx.open') }] : [];
  }
  if (sel.length === 0) return [];
  return selectionActionList(sel).filter((a) => a.key !== 'archive-extract-here');
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
/**
 * pane:p1 — is this verb mutating the SPLIT pane?
 *
 * Two signals, and they agree in every case a menu produced: the pane menu
 * names itself (`ctxMode === 'pane'`), and every other door — the selection
 * bar, the keyboard — means "the pane that has the keyboard".
 *
 * ⚠ Reading only `ctxMode` was safe only while the toolbar acted on
 * `selection` no matter which pane had focus. It does not any more (see
 * `activeTargets`), so a Cut fired from the bar over the right-hand pane would
 * have taken the right pane's ROWS with the left pane's FOLDER as their
 * origin, and the paste would then have moved them out of a directory they
 * were never in.
 */
function actingInPane(): boolean {
  return ctxMode.value === 'pane' || paneIsActive.value;
}

async function dispatchItemAction(key: string, targets: FileNode[]) {
  /* App plugins — `plugin:<plugin>/<action>` rows are not in the switch: the
     set is whatever the server listed, so they are resolved by prefix. */
  if (isPluginActionKey(key)) {
    await onPluginAction(key, targets);
    return;
  }
  switch (key) {
    /* gorunum:v1 — the selection bar's × . It used to be delivered by
     * synthesising a click on the listing's background, because nothing here
     * answered the key; a gesture that works by imitating another gesture
     * breaks the first time that other one changes. */
    case 'clear-selection':
      if (paneIsActive.value) splitSelection.clear();
      else selection.clear();
      return;
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
      await downloadSelection(targets);
      break;
    case 'archive-create':
      if (targets.length === 0) break;
      archiveTargets.value = targets;
      archiveInPane.value = actingInPane();
      archiveDestinationDir.value = archiveInPane.value
        ? qualify(splitPaneRef.value?.getPath() ?? '')
        : qualify(currentPath.value);
      archiveError.value = '';
      showArchiveCreate.value = true;
      break;
    case 'archive-extract':
      if (!targets[0] || !isArchiveFile(targets[0])) break;
      archiveTarget.value = targets[0];
      archiveInPane.value = actingInPane();
      archiveDestinationDir.value = wireParent(targets[0].path);
      archiveError.value = '';
      showArchiveExtract.value = true;
      break;
    case 'archive-extract-here':
      if (!targets[0] || !isArchiveFile(targets[0])) break;
      archiveTarget.value = targets[0];
      archiveInPane.value = actingInPane();
      archiveDestinationDir.value = wireParent(targets[0].path);
      archiveError.value = '';
      await startArchiveExtraction(targets[0], archiveDestinationDir.value, archiveInPane.value);
      break;
    /* tasi:m1 — the same dialog, twice; only `mode` differs. */
    case 'move-to':
    case 'copy-to':
      if (targets.length === 0) break;
      destPickerMode.value = key === 'move-to' ? 'move' : 'copy';
      destPickerTargets.value = targets;
      destPickerBusy.value = false;
      showDestPicker.value = true;
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
          () => flashToast(t('toast.node_id_copied', { id })),
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
        mutationInPane.value = actingInPane(); /* ui-fix */
        showRename.value = true;
      }
      break;
    case 'cut':
      /* ui-fix — in a pane context the clipboard source must be the pane's dir. */
      if (actingInPane()) paneCut();
      else {
        clipboard.value = { mode: 'cut', items: targets, sourcePath: currentPath.value };
        flashToast(t('toast.cut_ready'));
      }
      break;
    case 'copy':
      if (actingInPane()) paneCopy();
      else {
        clipboard.value = { mode: 'copy', items: targets, sourcePath: currentPath.value };
        flashToast(t('toast.copy_ready'));
      }
      break;
    case 'paste':
      /* ui-fix — pasting from the right-click menu goes to the active pane too
       * (the keyboard shortcut was already pane-routed; the menu was not). */
      if (actingInPane()) await panePaste();
      else await paste();
      break;
    case 'delete': {
      /* ⚠ The confirm dialog reads `paneCtxTargets` in pane mode, and only the
       * pane MENU used to fill it. Coming from the selection bar there is no
       * menu, so the rows are handed over here or the dialog would delete
       * whatever the last right-click left behind. */
      const inPane = actingInPane(); /* ui-fix */
      if (inPane) paneCtxTargets.value = targets;
      mutationInPane.value = inPane;
      showDelete.value = true;
      break;
    }
    case 'restore':
      if (targets.length > 0) await restoreSelection(targets);
      break;
    case 'toggle-hidden':
      toggleHiddenFiles();
      break;
    case 'new-folder':
      mutationInPane.value = actingInPane(); /* ui-fix */
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

/** A paste on its way. A cut paste lists the target folder before it queues
 *  (movedNamesCollide), and the clipboard is only emptied once it has: a second
 *  Ctrl+V meanwhile queued the same move twice. */
let pasting = false;

async function paste() {
  const cb = clipboard.value;
  if (!cb.mode || cb.items.length === 0 || pasting) return;
  pasting = true;
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
      const collides = await movedNamesCollide(items, targetWire);
      const { op } = await api.moveAsync(items, targetWire, originWire);
      registerMoveUndo(op.id, items, targetWire, originWire, collides);
      pendingOps.register(op);
      flashToast(
        collides
          ? t('toast.move_kept_both')
          : plan.cross
            ? t('split.cross_move')
            : t('split.move_queued'),
      );
    } else {
      const { op } = await api.copy(items, targetWire);
      pendingOps.register(op);
      flashToast(plan.cross ? t('split.cross_copy') : t('split.copy_queued'));
    }
    clipboard.value = { mode: null, items: [], sourcePath: null };
  } catch (err) {
    reportMutationError(err, { op: 'paste' });
  } finally {
    pasting = false;
  }
}

async function duplicate(n: FileNode) {
  try {
    const { op } = await api.copy([n.path], qualify(currentPath.value));
    pendingOps.register(op);
  } catch (err) {
    reportMutationError(err, { op: 'duplicate' });
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

/**
 * tasi:m1 — download WHATEVER is selected, as one thing.
 *
 * One file is still one navigation to its own body: it keeps the range
 * requests, the browser's own resume, and the encrypted-folder path that has
 * to decrypt in this tab. Anything else — several files, or a folder, whose
 * members the server expands itself — is one streaming archive behind a
 * single-use ticket (lib/downloadSelection explains why a POST then a
 * navigation, and why an iframe rather than `window.open`).
 *
 * ⚠ The old behaviour was not "download each one": the row HID itself above a
 * single selection, because the only implementation was one `window.open` per
 * node and the browser blocks the second as a popup.
 */
/** An archive download being prepared: a second press meanwhile asked the
 *  server to walk the same folders again, for a second archive. */
const archivePreparing = ref(false);

async function downloadSelection(targets: FileNode[]): Promise<void> {
  if (targets.length === 0) return;
  if (targets.length === 1 && targets[0].type === 'file') {
    downloadFile(targets[0]);
    return;
  }
  if (archivePreparing.value) return;
  archivePreparing.value = true;
  /* ⚠ Not a courtesy: minting asks the server to resolve and authorize every
   * member, which on a deep folder is not instant, and nothing else on screen
   * moves until the browser is handed the response. It stays up until the
   * answer replaces it — it used to be the 2.5 s toast, gone long before a
   * walk of a folder on an object store is. */
  showToast({ message: t('toast.archive.preparing') }, STICKY_TOAST_MS);
  try {
    const ticket = await downloadArchive(api, targets.map((n) => n.path));
    flashToast(t('toast.archive.started', { name: ticket.name, count: String(ticket.files) }));
  } catch (err) {
    const e = err as Error & { status?: number };
    /* 409 is the server saying the selection held nothing this account may
     * read — a different sentence from "it failed", and the only one that
     * tells the reader what to do next. */
    showToast({ message: e.status === 409 ? t('toast.archive.empty') : failureText(err) }, ERROR_TOAST_MS);
    emit('error', { message: e.message, context: { op: 'archive-download' } });
  } finally {
    archivePreparing.value = false;
  }
}

/**
 * tasi:m1 — the destination dialog came back with a folder.
 *
 * Everything the verb needs already exists: `transferItems` is the one gate
 * that moves and copies (cross-storage included) and owns the undo
 * registration, so this only says WHICH rows, WHERE, and WHICH intent.
 *
 * ⚠ `originWire` is the folder the rows came FROM, and it has to follow the
 * pane the selection belongs to — the same rule `actingInPane` states for
 * every other mutation.
 */
async function onDestinationPicked(dest: string): Promise<void> {
  const targets = destPickerTargets.value;
  if (!dest || targets.length === 0) {
    showDestPicker.value = false;
    return;
  }
  const move = destPickerMode.value === 'move';
  const originWire = actingInPane()
    ? qualify(splitPaneRef.value?.getPath() ?? '')
    : qualify(currentPath.value);
  destPickerBusy.value = true;
  try {
    const queued = await transferItems(targets.map((n) => n.path), dest, originWire || undefined, move ? 'move' : 'copy');
    /* ⚠ `transferItems` has already flashed "queued". This REPLACES it rather
     * than stacking on it — there is one toast slot (`showToast`), and the
     * useful half of the sentence is the destination, which "queued" does not
     * carry. Do not add a second call expecting two messages; you would only
     * be choosing which one nobody reads.
     * ⚠⚠ Only when it WAS queued. This used to run whatever happened: a refused
     * move said "Moved to X" over the refusal, and past tense besides — the
     * job is only queued here; its own toast says when it is done. */
    if (queued) {
      const name = labelOfWire(dest, dest);
      flashToast(move ? t('toast.moved_to', { name }) : t('toast.copied_to', { name }));
      if (move) {
        if (actingInPane()) splitSelection.clear();
        else selection.clear();
      }
    }
  } finally {
    destPickerBusy.value = false;
    showDestPicker.value = false;
  }
}

// ------- Modals -------

/* belge:n1 — after creating it, OPEN it. Creating a file and leaving the
 * person looking at a listing is half the feature. */
async function onDocumentCreated(file: { path: string; name: string; ext: string }) {
  showNewDocument.value = false;
  const dir = file.path.slice(0, file.path.lastIndexOf('/'));
  if (dir && dir !== qualify(currentPath.value)) await load(dir);
  else await load();
  /* `file.ext` is the TYPE the bytes were made from, not the name's extension
   * (#56: a Plain text document may be `LICENSE`), so the stand-in row takes
   * its extension from the name, and the type goes to the viewer as openAs. */
  const dot = file.name.lastIndexOf('.');
  const node =
    files.value.find((n) => n.path === file.path) ??
    ({
      type: 'file',
      path: file.path,
      basename: file.name,
      extension: dot > 0 ? file.name.slice(dot + 1).toLowerCase() : '',
    } as unknown as FileNode);
  // Host-owned open (desktop): the new document goes to its own window like
  // any other open (lib/openSurface.ts) — not ALSO over the explorer.
  if (openSurface(props.config, node) === 'host') {
    emit('file-opened', { path: node.path, basename: node.basename });
    void markRecent(node);
    return;
  }
  previewOpenAs.value = file.ext ? { path: node.path, ext: file.ext } : null;
  previewTarget.value = node;
  // ⚠ NOT previewModeForExt: that sends office types to 'view', which is right
  // for a peek at somebody else's file and wrong for the one you just made.
  previewMode.value = nodeCanEdit(node) ? 'edit' : 'view';
  showPreview.value = true;
  emit('file-opened', { path: node.path, basename: node.basename });
  void markRecent(node);
}

function archiveRequestCode(err: unknown): string | undefined {
  const detail = (err as { detail?: string })?.detail;
  if (!detail) return undefined;
  try {
    return (JSON.parse(detail) as { code?: string }).code;
  } catch {
    return undefined;
  }
}

function archiveRequestError(err: unknown): string {
  const detail = (err as { detail?: string })?.detail;
  if (detail) {
    try {
      const body = JSON.parse(detail) as { code?: string; error?: string };
      const byCode: Record<string, string> = {
        PASSWORD_REQUIRED: 'archive.password_required',
        BAD_PASSWORD: 'archive.bad_password',
        TARGET_EXISTS: 'archive.target_exists',
        PASSWORD_CHARSET: 'archive.password_charset',
        UNSUPPORTED_FORMAT: 'archive.error_unsupported',
        ARCHIVE_LIMIT_EXCEEDED: 'archive.error_limit',
        PROVIDER_UNAVAILABLE: 'archive.error_unavailable',
      };
      if (body.code && byCode[body.code]) return t(byCode[body.code]);
      if (body.error) return body.error;
    } catch {
      // The common API wrapper still supplies a friendly status message.
    }
  }
  return (err as Error)?.message || t('toast.failed');
}

async function submitArchiveCreate(value: {
  name: string;
  format: ArchiveCreateFormat;
  password?: string;
  encrypt_filenames: boolean;
  compression: number;
  solid?: boolean;
  dictionary_size_mb?: number;
}) {
  archiveBusy.value = true;
  archiveError.value = '';
  try {
    const { op } = await api.archiveCreate({
      dest: wireJoin(archiveDestinationDir.value, value.name),
      sources: archiveTargets.value.map((node) => node.path),
      format: value.format,
      password: value.password,
      encrypt_filenames: value.encrypt_filenames,
      compression: value.compression,
      solid: value.solid,
      dictionary_size_mb: value.dictionary_size_mb,
    });
    pendingOps.register(op);
    showArchiveCreate.value = false;
    /* The dialog has closed and the job may run for minutes: its row, not the
     * corner badge alone, is what says it is under way (useOperations `reveal`). */
    opsCenter.reveal();
    flashToast(t('archive.queued'));
  } catch (err) {
    archiveError.value = archiveRequestError(err);
    emit('error', { message: archiveError.value, context: { op: 'archive-create' } });
  } finally {
    archiveBusy.value = false;
  }
}

async function startArchiveExtraction(
  target: FileNode,
  dest: string,
  inPane: boolean,
  password?: string,
) {
  archiveBusy.value = true;
  archiveError.value = '';
  try {
    const result = await api.archiveExtract(target.path, {
      dest,
      password,
    });
    showArchiveExtract.value = false;
    showArchivePassword.value = false;
    archivePasswordAction.value = null;
    archivePasswordError.value = '';
    if (result.op) {
      pendingOps.register(result.op);
      opsCenter.reveal(); /* as for a new archive (submitArchiveCreate) */
      flashToast(t('archive.extraction_queued'));
      return;
    }
    if (inPane) await splitPaneRef.value?.reload();
    else await load();
    flashToast(t('archive.extracted', { count: result.count ?? 0 }));
  } catch (err) {
    const code = archiveRequestCode(err);
    if (code === 'PASSWORD_REQUIRED' || code === 'BAD_PASSWORD') {
      archivePasswordAction.value = { target, dest, inPane };
      archivePasswordError.value = code === 'BAD_PASSWORD' ? t('archive.bad_password') : '';
      showArchiveExtract.value = false;
      showArchivePassword.value = true;
      return;
    }
    const message = archiveRequestError(err);
    if (showArchivePassword.value) archivePasswordError.value = message;
    else if (showArchiveExtract.value) archiveError.value = message;
    /* "Extract here" opens no dialog, so an error written into one was never
     * seen: a refused archive (a link inside, too large, a format this server
     * cannot read) looked like a click that did nothing. */
    else showToast({ message }, 6000);
  } finally {
    archiveBusy.value = false;
  }
}

async function submitArchiveExtract(value: { folder: string }) {
  const target = archiveTarget.value;
  if (!target) return;
  await startArchiveExtraction(
    target,
    wireJoin(archiveDestinationDir.value, value.folder),
    archiveInPane.value,
  );
}

async function submitArchivePassword(password: string) {
  const action = archivePasswordAction.value;
  if (!action) return;
  archivePasswordError.value = '';
  await startArchiveExtraction(action.target, action.dest, action.inPane, password);
}

function closeArchivePassword() {
  showArchivePassword.value = false;
  archivePasswordAction.value = null;
  archivePasswordError.value = '';
}

async function cancelPendingOp(id: number) {
  try {
    await api.opsCancel(id);
    await pendingOps.poll();
  } catch (err) {
    const op = pendingOps.ops.value.find((candidate) => candidate.id === id);
    const archive = op?.op_type === 'archive-create' || op?.op_type === 'archive-extract';
    flashToast(archive ? archiveRequestError(err) : t('plugin.cancel_failed'));
  }
}

async function submitNewFolder(name: string) {
  if (newFolderBusy.value) return;
  const inPane = mutationInPane.value; /* ui-fix — new folder in the side pane */
  newFolderBusy.value = true;
  // Cleared first: the dialog shows it again when it CHANGES, and a retry
  // refused with the same sentence would otherwise leave it untouched.
  newFolderError.value = null;
  try {
    const dirWire = inPane ? qualify(splitPaneRef.value?.getPath() ?? '') : qualify(currentPath.value);
    await api.newFolder(dirWire, name);
    showNewFolder.value = false;
    if (inPane) await splitPaneRef.value?.reload();
    else await load();
  } catch (err) {
    newFolderError.value = failureText(err);
    emit('error', { message: (err as Error).message, context: { op: 'newfolder' } });
  } finally {
    newFolderBusy.value = false;
  }
}

async function submitRename(name: string) {
  if (renameBusy.value) return;
  const target = renameTarget.value;
  if (!target) return;
  /* ⚠ Cleared BEFORE the attempt, not only when the dialog opens. The dialog
   * drops the line as soon as the name is edited and re-shows it when this ref
   * CHANGES — so retrying the same taken name, which fails with the identical
   * sentence, left the ref untouched, nothing re-rendered, and Save looked like
   * it did nothing at all. */
  renameError.value = null;
  const inPane = mutationInPane.value; /* ui-fix — rename from the side pane */
  renameBusy.value = true;
  try {
    const dirWire = inPane ? qualify(splitPaneRef.value?.getPath() ?? '') : qualify(currentPath.value);
    const oldPath = target.path; // qualified
    const oldName = target.basename;
    // A folder is a job of the queue when the server runs it there: on an
    // object store every object inside it is a request of its own, and inside
    // this one the dialog outlasted the proxy with nothing on screen.
    const queued = target.type === 'dir' && serverQueues('rename');
    let job: PendingOpDto | undefined;
    if (queued) job = (await api.renameQueued(dirWire, oldPath, name)).op;
    else await api.rename(dirWire, oldPath, name);
    showRename.value = false;
    renameTarget.value = null;
    // Clean inverse: rename the new path back to the old basename — as a job
    // too, when this one was.
    const newPath = wireJoin(wireParent(oldPath), name);
    const undo =
      name && name !== oldName
        ? async () => {
            if (!queued) {
              await api.rename(dirWire, newPath, oldName);
              return;
            }
            const back = (await api.renameQueued(dirWire, newPath, oldName)).op;
            if (back) pendingOps.register(back);
          }
        : null;
    if (job) {
      // The operations centre follows the job; the listing, and the undo,
      // come when it ends (onSettled).
      if (undo) opUndo.set(job.id, { message: t('toast.renamed'), fn: undo });
      pendingOps.register(job);
      flashToast(t('toast.rename_queued', { name }));
      return;
    }
    if (inPane) await splitPaneRef.value?.reload();
    else await load();
    if (undo) undoToast(t('toast.renamed'), undo);
  } catch (err) {
    const e = err as Error & { status?: number };
    // 409 is the server refusing to replace what already has the name
    // (NAME_TAKEN). Everything else still says what went wrong, in the dialog.
    renameError.value = e.status === 409 ? t('newdoc.err.exists', { name }) : failureText(err);
    reportMutationError(err, { op: 'rename' }, { inDialog: true });
  } finally {
    renameBusy.value = false;
  }
}

async function confirmDelete() {
  if (deleteBusy.value) return;
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
  deleteBusy.value = true;
  deleteError.value = null;
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
    // Said in the dialog, which stays open over what was not deleted.
    deleteError.value = failureText(err);
    reportMutationError(err, { op: 'delete' }, { inDialog: true });
  } finally {
    deleteBusy.value = false;
  }
}

function openConvert(n: FileNode) {
  // A shortcut or a stale menu must not open what the rule withholds.
  if (!legacyConvertUrl.value) return;
  convertTarget.value = n;
  showConvert.value = true;
}

function onConvertDone(name: string) {
  flashToast(t('toast.converted_to', { name }));
  void load();
}


// ------- Upload -------

function triggerUpload() {
  if (!canWriteHere.value) {
    flashToast(t('toast.read_only_here'));
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
  // ⚠ The folder the files were dropped into or picked for, read ONCE. The
  // files go one after another, and each used to read the open folder again
  // when its turn came — so browsing while the first file was on its way sent
  // the rest of the batch into whatever folder was open by then.
  const target = qualify(currentPath.value);
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
      const pending = chunked.resumableFor(target, f);
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
      if (await chunkedUpload(f, target)) continue;
    }
    await legacyUpload(f, target);
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
    flashToast(t('toast.read_only_here'));
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

/**
 * pane:p1 — a drag started in one of the panes.
 *
 * `FilePane` has already written the payload both panes must agree on: the
 * internal MIME, the ORIGIN directory (which is what lets a cross-pane move be
 * undone) and the plain-text fallback. This is the part only the host knows —
 * the desktop shell's OS drag and the browser's own `DownloadURL` hand-off —
 * and wiring it for BOTH panes rather than only the left one is a capability
 * the right-hand pane gains simply by being the same pane.
 *
 * ⚠ `pane` decides two things and nothing else: whose selection is being
 * dragged, and which directory the drag says it came from. Everything below
 * that point is identical, which is the reason there is one function.
 */
/**
 * pane:p1 — a row was clicked in one of the panes. Both panes reach the SAME
 * Ctrl/Shift semantics (`useSelection.click`), which is the point: the
 * right-hand pane used to keep a plain Set in which Shift behaved as Ctrl, so
 * a range-select worked on one side of the split and not the other.
 */
function onPaneClickRow(
  pane: 'main' | 'split',
  n: FileNode,
  mod: { ctrl: boolean; shift: boolean },
) {
  (pane === 'split' ? splitSelection : selection).click(n.path, mod);
}

function onPaneItemDragStart(pane: 'main' | 'split', node: FileNode, ev: DragEvent) {
  if (!ev.dataTransfer) return;
  if (node.basename === '.trash') {
    ev.preventDefault();
    return;
  }
  const sel = pane === 'split' ? splitSelection.nodes.value : selection.nodes.value;
  const dirWire =
    pane === 'split' ? qualify(splitPaneRef.value?.getPath() ?? '') : qualify(currentPath.value);
  const chosen = sel.some((n) => n.path === node.path) ? sel : [node];
  const items = chosen
    .filter((n) => !clippedPaths.value.has(n.path))
    .filter((n) => n.basename !== '.trash')
    .map((n) => ({ path: n.path, basename: n.basename, type: n.type })); // qualified
  if (items.length === 0) return;

  handDragOut(ev, items, dirWire, node.mime_type);
}

/**
 * Hand a drag to whatever can carry it out of this window.
 *
 * #47 — ONE function for a listing row and a ⌘K search hit, so a hit drags
 * out exactly the way the same file's row does. `origin` is the folder the
 * rows came from: set, the drag is ALSO an internal one (dropped on a folder
 * inside filex it is a move); null, it only goes out (a palette hit — the
 * palette covers the listing, so there is nowhere inside to drop it).
 */
function handDragOut(ev: DragEvent, items: DragItem[], origin: string | null, mime?: string): void {
  /* wiring:f1 — drag-out (to the desktop / another application).
     When a shell is present the drag is ALWAYS an OS drag: folders and
     multi-selections land as REAL files, one by one, with NO SIZE LIMIT.
     If the bytes are ready, real files are handed over; if not, the shell
     hands over an empty "placeholder", finds out where it was dropped and
     downloads THERE (see desktop/src/dropwatch.ts). If the drop lands INSIDE
     the app the drag is still a server-side move — the payload stays with us —
     and the shell is told to "give up" so it doesn't watch the drives for
     nothing. */
  if (dragOut.value) {
    ev.preventDefault();
    if (origin !== null) beginNativeDrag(items, origin);
    void Promise.resolve(dragOut.value.start(items)).catch((err) => {
      endNativeDrag();
      cancelShellDrag();
      emit('error', { message: (err as Error).message, context: { op: 'drag-out' } });
    });
    void prepareDragOut(items);
    return;
  }

  /* Single file, no shell: the browser's own download path (DownloadURL)
     fetches the file onto the desktop at drop time. Which URL depends on the
     credential. A cookie session hands over the plain download URL — the
     cookie travels with it, and it rides whatever route the embed already
     proxies. A bearer session (the admin SPA) cannot: the download stack
     sends no Authorization header, so it hands over the short-lived link
     minted while the pointer rested on the row (#71). None ready yet — the
     drag simply carries no download; it never waits (see lib/dragOut). */
  if (items.length === 1 && items[0] && ev.dataTransfer) {
    const one = items[0];
    const url = canDownloadUrlDrag(props.config.auth) ? api.downloadUrl(one.path) : dragLinks.take(one.path);
    const payload = url ? downloadUrlPayload(one, url, mime) : null;
    if (payload) ev.dataTransfer.setData('DownloadURL', payload);
  }
}

/* === #71 — a drag-out link for a session the browser cannot sign ========
 *
 * `dragstart` fills the dataTransfer synchronously, and the link is a server
 * round trip, so it is asked for BEFORE the drag: when the pointer comes to
 * rest on a file row and again on the press (lib/dragOut createDragLinks has
 * the rules). One delegated listener pair on the root, the way the middle-
 * click delegation below reads `[data-fe-path]` — every view (list, grid,
 * gallery) and both panes, with nothing added to any of them; the palette's
 * hits say so themselves (`warm-hit`), because they are not listing rows.
 *
 * Only where it is needed: no shell (the desktop app drags through the OS)
 * and a credential the browser cannot carry (a cookie session has the plain
 * URL). Nowhere else does a hover cost a request.
 */
const dragLinksSupported = ref(true);
const dragLinks = createDragLinks((p) => requestFileLink(api, p), {
  onUnsupported: () => {
    dragLinksSupported.value = false;
  },
});
const dragNeedsLink = computed(() => !dragOut.value && !canDownloadUrlDrag(props.config.auth));

function warmDragLink(path: string | null | undefined) {
  if (!path || !dragNeedsLink.value || trashActive.value) return;
  dragLinks.warm(path);
}

function onRowPointerWarm(ev: PointerEvent) {
  if (ev.pointerType === 'touch' || !dragNeedsLink.value) return;
  const host = ev.target as Element | null;
  const el = host && typeof host.closest === 'function' ? host.closest('[data-fe-path]') : null;
  const p = el?.getAttribute('data-fe-path');
  if (!p) return;
  const node = files.value.find((f) => f.path === p) ?? splitDisplayOrder.value.find((f) => f.path === p);
  if (node && node.type === 'file' && node.basename !== '.trash') warmDragLink(node.path);
}
onMounted(() => {
  rootEl.value?.addEventListener('pointerover', onRowPointerWarm, { passive: true });
  rootEl.value?.addEventListener('pointerdown', onRowPointerWarm, { capture: true, passive: true });
});
onBeforeUnmount(() => {
  rootEl.value?.removeEventListener('pointerover', onRowPointerWarm);
  rootEl.value?.removeEventListener('pointerdown', onRowPointerWarm, { capture: true });
});

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

/** Moves `sources` into `targetDir`; true once the move is queued (or done,
 *  without the async endpoint), false when it was refused — the refusal is
 *  already said (reportMutationError). */
async function moveSourcesAsync(sources: string[], targetDir: string, opLabel: string, originOverride?: string): Promise<boolean> {
  try {
    const originWire = originOverride ?? qualify(currentPath.value); /* wiring:d1 — the real source folder for a drag coming from the split pane */
    const collides = await movedNamesCollide(sources, targetDir);
    if (api.endpoints.moveAsync) {
      const { op } = await api.moveAsync(sources, targetDir, originWire);
      registerMoveUndo(op.id, sources, targetDir, originWire, collides);
      pendingOps.register(op);
      flashToast(collides ? t('toast.move_kept_both') : t('split.move_queued'));
    } else {
      await api.move(originWire, sources, targetDir);
      await load();
      if (collides) {
        flashToast(t('toast.move_kept_both'));
      } else {
        // Sync move (no async endpoint): offer the reverse move right away.
        const movedPaths = sources.map((p) => wireJoin(targetDir, wireBasename(p)));
        undoToast(t('toast.moved'), async () => {
          await api.move(targetDir, movedPaths, originWire);
        });
      }
    }
    selection.clear();
    return true;
  } catch (err) {
    reportMutationError(err, { op: opLabel, targetDir });
    return false;
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
/* tablo:t3 — every table under the explorer (its own listing, the connection
 * panels, an archive's or a spreadsheet's preview, an app's list) is the one
 * DataTable, and its column menu and Actions menu teleport to <body>: they
 * learn the language and the mode here, once, instead of from a prop each
 * surface would have to remember to pass (lib/tableEnv). */
provideTableEnv({ locale, theme: themeMode });
// Resolved mode: an explicit choice wins, otherwise the OS preference — the
// same logic variables.css encodes in CSS. The inline root variables beat every
// stylesheet rule, so they must track this resolution at runtime.
const themeOsDark = useSystemDark();
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
 * Offered to a PERSON once, never once per mount (lib/tour): the first mount
 * that finds neither this browser's flag nor the account's opens it a moment
 * later and records it right away, so a second tab, a remount or another
 * device does not open it again. "Turu tekrar başlat" re-opens it any time.
 * Restart arrives as a bubbled `fe:tour-restart` CustomEvent from the
 * Toolbar overflow menu, so no extra prop/emit threading through the
 * shared component tags is needed. */
const showTour = ref(false);
let cancelTourOffer: (() => void) | undefined;

function startTour() {
  showTour.value = true;
}

function onTourClose() {
  showTour.value = false;
  // Already recorded when it was offered; recorded again for a restart that
  // somebody opened on a browser where it had never been offered.
  markTourSeen();
  rootEl.value?.focus();
}

function onTourRestartEvent() {
  startTour();
}

onMounted(() => {
  rootEl.value?.addEventListener('fe:tour-restart', onTourRestartEvent);
  cancelTourOffer = offerTourOnce(() => {
    if (!showTour.value) startTour();
  });
});
onBeforeUnmount(() => {
  cancelTourOffer?.();
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
  /* tablo:t1 — the tab's own remembered view mode is applied only while
   * per-folder memory is OFF.
   *
   * ⚠⚠ Two things remembering the same fact is two things that disagree, and
   * this pair disagreed in a way nothing would have caught: open one folder in
   * two tabs, set it to grid in the first, switch to the second, and the second
   * restores the LIST it happened to be showing — for the same folder, whose
   * remembered view is grid. Measured, 2026-09-13. The applier below could not
   * correct it either, because it fires on a change of FOLDER and the folder
   * did not change; only the tab did.
   *
   * With the memory on, the folder is the authority and a tab is just a window
   * onto one, so the tab's copy is redundant and is ignored. With it off,
   * nothing here changes at all and tabs keep exactly the behaviour they had.
   */
  if (!folderMemoryOn.value && tb.viewMode && tb.viewMode !== viewMode.value) {
    viewMode.value = tb.viewMode;
  }
  /* ⚠ AFTER the load, not before. `load()` is async, so `currentPath` — and
   * with it `currentFolderKey` — only moves once it settles; re-asserting here
   * is also what covers the case above, where the key never changes and the
   * watcher therefore never runs. */
  const settle = () => applyFolderView(currentFolderKey.value);
  if (tb.path === '.trash') {
    void loadTrash().then(settle, settle);
    return;
  }
  void load(tb.path).then(settle, settle);
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

const mainPaneRef = ref<InstanceType<typeof FilePane> | null>(null);
const splitPaneRef = ref<InstanceType<typeof FilePane> | null>(null);

/* pane:p1 — the split pane's half of the state the HOST has to hold.
 *
 * ⚠⚠ Two instances of ONE composable, not two implementations. The explorer
 * routes cut/copy/paste, the context menu, the inspector, the toolbar's count
 * and every keyboard shortcut through "the active pane's selection", so it has
 * to hold both — but `useSelection` is written once and this is the second
 * call to it, exactly as `lib/fileFilters` is written once and this is the
 * second filter object. What used to be here instead was a SECOND selection
 * model living inside `SecondaryPane`, in which Shift behaved as Ctrl because
 * it kept a plain Set with no range anchor. */
const splitDisplayOrder = ref<FileNode[]>([]);
const splitSelection = useSelection(() =>
  splitDisplayOrder.value.length ? splitDisplayOrder.value : (splitPaneRef.value?.visibleNodes() ?? []),
);
const splitFilters = ref<DriveFilters>({ ...EMPTY_FILTERS });

/** The split pane's folder, in the key `lib/viewPrefs` remembers folders under
 *  — so the column menu's two per-folder rows tell the truth on the right as
 *  well as on the left.
 *
 * ⚠⚠ READ-ONLY on this side, and that is a decision. The memory answers "how
 * did I leave this folder", and a folder is left in ONE state; two panes both
 * writing it would be the stale-snapshot race that already bit the tab strip
 * once today, with a second racer. So: BOTH panes read a folder's arrangement
 * when they arrive in it (the split pane's sort store is seeded from the
 * global default and this key is what its column menu operates on), and only
 * the main pane — the one the window's tab is about, the one whose view mode
 * the tab records — writes it back. Nothing re-applies memory to a pane that
 * is sitting still, so a choice made in one pane can never yank the other
 * pane's view out from under it. */
const splitFolderKey = computed(() => {
  if (!folderMemoryOn.value) return '';
  const path = String(splitPaneRef.value?.getPath() ?? '').replace(/^\/+|\/+$/g, '');
  if (!path) return '';
  const [first, ...rest] = path.split('/');
  const st = (props.config.storages ?? []).find((sto) => sto.name === first);
  return makeFolderKey(st?.uid || first, rest.join('/'));
});

/* pane:p1 — the row above BOTH panes. It survives the tabs being switched off
 * (the `simple` profile) because the split and details toggles live in it too;
 * `hideTabs` empties it without removing it. */
const paneRowVisible = computed(
  () => tabsVisible.value || infoPanelToggle.value || splitOffered.value,
);

/**
 * pane:p1 — the states the WINDOW draws INSTEAD of a listing.
 *
 * Home, a dead deep link and the encrypted lock screen are not things that can
 * happen to a pane — they are things that have happened to the explorer — so
 * they arrive in `FilePane`'s `body` slot and this says which. Everything else
 * in the old chain (the skeleton, the failed listing, the filtered-empty
 * state, the listing itself) IS pane state and now lives in the pane, once,
 * for both halves.
 *
 * ⚠ The two `return ''`s are load-bearing: they hand the turn back to the
 * pane's own skeleton and error states in exactly the order the chain had, so
 * a folder that is still loading does not flash its lock screen and a failed
 * listing still gets the retry state rather than "not found".
 */
const hostBodyState = computed<'' | 'home' | 'notfound' | 'locked'>(() => {
  if (navView.value === 'home') return 'home';
  if (loading.value && files.value.length === 0) return '';
  if (notFoundPath.value) return 'notfound';
  if (loadError.value && files.value.length === 0) return '';
  if (e2eLocked.value) return 'locked';
  return '';
});
/**
 * Can a second pane be OPENED here at all?
 *
 * ⚠⚠ One answer for every door to the split, and it is not `!isNarrow`. The
 * tab strip's toggle and the command palette's row were both handed
 * `!isNarrow` and nothing else, while the pane below was gated on the
 * `simple` profile as well — so under `ui-profile="simple"` the button was
 * drawn, a click recorded a split on the tab and turned the button PRESSED,
 * and no pane ever appeared (measured 2026-09-14 at 1440: one pane before the
 * click, one after, `aria-pressed="true"`). A control that lies about its own
 * state is worse than a missing one. The state it wrote is kept, so switching
 * the profile back would suddenly split a tab nobody remembers splitting —
 * which is why `toggleSplit` refuses to open one here too, not only the
 * button.
 *
 * Narrow mode keeps the state and brings it back on widen; the simple profile
 * offers no split at all (`lib/uiProfile`).
 */
const splitOffered = computed(() => !isNarrow.value && !simpleUi.value);
const splitVisible = computed(() => !!activeSplit.value && splitOffered.value /* gezinti:g1 */);

function toggleSplit() {
  if (activeSplit.value) {
    tabsApi.setSplit(null);
    activePane.value = 'main';
    return;
  }
  if (!splitOffered.value) return;
  tabsApi.setSplit({ path: currentPath.value ?? '', viewMode: viewMode.value });
}
function closeSplit() {
  tabsApi.setSplit(null);
  activePane.value = 'main';
}
function onPaneNavigate(p: string) {
  tabsApi.setSplit({ ...(activeSplit.value ?? {}), path: p });
  /* A navigation is a new folder: the chips the person set for the folder they
     just left must not silently keep narrowing the one they arrived in. Same
     rule the main pane's own watcher applies. */
  if (filtersActive(splitFilters.value)) splitFilters.value = { ...EMPTY_FILTERS };
  splitSelection.clear();
  splitDisplayOrder.value = [];
}

/**
 * pane:p1 — a row was opened (double-click, Enter) in one of the panes.
 *
 * A FOLDER opens in the pane it was opened from — that is the whole point of a
 * second pane. Everything else is the window's business (the preview modal,
 * the encrypted-blob path, `markRecent`) and goes to the one `openNode` both
 * panes have always been entitled to. The right-hand pane used to have no path
 * to it at all, so double-clicking a file there did nothing.
 */
function onPaneOpen(pane: 'main' | 'split', n: FileNode) {
  if (pane === 'split' && n.type === 'dir' && n.basename !== '.trash') {
    void splitPaneRef.value?.loadFolder(
      /* A multi-storage drive row's path is already the wire form for that
         storage's root; a real row needs converting. The predicate comes from
         `lib/fileIcons`, which is that concept's one home. */
      iconFamilyFor(n) === 'storage' ? n.path : paneToUser(n.path),
    );
    return;
  }
  openNode(n);
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
/** The `data-pane` of the half that owns `activeTargets()`. The selection bar
 *  is teleported into THAT pane, so this is what tells the toolbar where. */
const activePaneId = computed(() => (paneIsActive.value ? 'split' : 'main'));

/* === koru:k1 + pane:p1 — THE DETAILS PANEL FOLLOWS THE FOCUSED PANE =======
 *
 * ⚠⚠ It did not, and the owner reported it, 2026-09-13: "Yanda açılan info
 * panele sadece ana pane üzerinde tıklanmış ya da bulunduğumuz yerin infosu
 * geliyor. Onun dışında öteki pane'de tıkladığımız yerlerin infosu hiç
 * gelmiyor." The panel read `selection.nodes` — the MAIN pane's selection —
 * so clicking a row in the right-hand half changed the highlight, changed the
 * selection bar (which had just been taught this rule) and left the panel
 * describing something in the other half entirely.
 *
 * ⚠ The same source as the selection bar, deliberately: `activeTargets()`.
 * That function is already the one answer to "what is selected right now", and
 * the whole reason it exists is that the toolbar used to have a second one.
 * A third, here, would be the same bug a month later.
 *
 * ⚠ WITH NO SELECTION the panel describes the focused pane's FOLDER, which is
 * the behaviour it already had for the main pane and the only honest one: the
 * head then reads the place you are standing in. So clicking into the split
 * pane's empty background moves the panel to that pane's folder rather than
 * leaving it on the other half's — which is the complaint, one level up.
 */
/**
 * ⚠⚠ IT HOLDS THE LAST SELECTED THING. Owner's ruling, 2026-09-13, asked and
 * answered: "son seçilen şeyi tutsun."
 *
 * So moving the keyboard to a pane with nothing ticked does NOT empty the panel
 * and does NOT fall back to that pane's folder — it goes on describing whatever
 * was selected last, until something else is selected. That is the difference
 * between a panel you can read while you work in the other half and a panel
 * that blanks the moment you click away from what you were reading about.
 *
 * ⚠ The two consequences of holding, handled below rather than left implicit:
 *   1. the held item can live in the pane you are NOT looking at, so the panel
 *      is TOLD it is holding and where the item is (`heldIn`), and says so;
 *   2. a held item can stop existing — deleted, moved, or its pane closed — and
 *      a panel describing a file that is gone is worse than an empty one, so
 *      `heldValid` drops it the moment its own pane's listing no longer has it.
 */
const heldSelection = ref<{ nodes: FileNode[]; pane: 'main' | 'split'; path: string } | null>(null);

/** The focused pane's location, user-path form. */
const activePanePath = computed(() =>
  paneIsActive.value ? (splitPaneRef.value?.getPath() ?? '') : (currentPath.value ?? ''),
);
function panePathOf(pane: 'main' | 'split'): string {
  return pane === 'split' ? (splitPaneRef.value?.getPath() ?? '') : (currentPath.value ?? '');
}
/** The paths a pane's folder HOLDS — before the filter row narrows them. ⚠ The
 *  unfiltered set on both sides: "is this row still there" and "is this row
 *  currently drawn" are different questions, and answering the first with the
 *  second would make typing in the filter box drop the held item as if the file
 *  had been deleted. */
function panePathsOf(pane: 'main' | 'split'): string[] {
  return pane === 'split'
    ? (splitPaneRef.value?.rowPaths() ?? [])
    : files.value.map((n) => n.path);
}

/* Capture: any non-empty selection, in either pane, becomes the held one. */
watch(
  () => activeTargets(),
  (nodes) => {
    if (nodes.length) {
      heldSelection.value = {
        nodes,
        pane: activePaneId.value as 'main' | 'split',
        path: activePanePath.value,
      };
    }
  },
  { deep: false },
);

/**
 * Is the held item still a real thing?
 *
 * ⚠ Only checked while its own pane is still showing the folder it was held
 * from. Walking away from a folder is not a deletion — "last selected" has to
 * survive a navigation or it is not a hold at all — but standing in the same
 * folder with the row gone IS one, and that is the case that must not be
 * described. A closed split pane takes its held item with it.
 */
const heldValid = computed<boolean>(() => {
  const h = heldSelection.value;
  if (!h || !h.nodes.length) return false;
  if (h.pane === 'split' && !splitVisible.value) return false;
  if (panePathOf(h.pane) !== h.path) return true; // navigated away — still held
  const here = new Set(panePathsOf(h.pane));
  return h.nodes.some((n) => here.has(n.path));
});
watch(heldValid, (ok) => {
  if (!ok) heldSelection.value = null;
});

/** True while the panel is describing the HELD item rather than a live
 *  selection — i.e. the focused pane has nothing ticked. */
const inspectorHeld = computed(() => activeTargets().length === 0 && heldValid.value);
const inspectorNodes = computed<FileNode[]>(() => {
  const live = activeTargets();
  if (live.length) return live;
  return heldValid.value ? (heldSelection.value?.nodes ?? []) : [];
});
/** Where the held item lives — named, so "this is not what is selected in front
 *  of you" is never a guess. Empty while a live selection is shown. */
const inspectorHeldIn = computed(() =>
  inspectorHeld.value ? folderLabelOf(heldSelection.value?.path ?? '') : '',
);

/** Folder summary label for the truly-empty state — of the FOCUSED pane. */
const inspectorDirLabel = computed(() => folderLabelOf(activePanePath.value));
/** How many rows that folder holds. `files` IS the main pane's row array, so
 *  the two branches are the same quantity measured on the two panes. */
const inspectorDirCount = computed(() =>
  paneIsActive.value ? (splitPaneRef.value?.rowCount() ?? 0) : files.value.length,
);
/** RBAC level of that folder. The main pane's comes off the host's own load;
 *  the split pane reads it from the response to its own `index`. */
const inspectorDirPerm = computed(() =>
  paneIsActive.value ? (splitPaneRef.value?.dirPerm() ?? '') : dirPerm.value,
);

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
  const nodes = splitSelection.nodes.value;
  if (nodes.length === 0) return;
  clipboard.value = { mode: 'cut', items: nodes, sourcePath: splitPaneRef.value?.getPath() ?? '' };
  flashToast(t('toast.cut'));
}
function paneCopy() {
  const nodes = splitSelection.nodes.value;
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
): Promise<boolean> {
  // ui-fix — an in-place drop (source parent === target) is a no-op: this
  // avoids the backend's "copy onto itself" 400 (cross-pane + clipboard paths).
  const list = sources.filter(
    (p) => p && p !== targetWire && !targetWire.startsWith(p + '/') && !sameDir(wireParent(p), targetWire),
  );
  // Nothing to send is not a success: "Move to…" must not report one.
  if (list.length === 0 || !targetWire) return false;
  const plan = resolveTransfer(list, targetWire, intent);
  let queued = false;
  if (plan.kind === 'copy') {
    try {
      const { op } = await api.copy(list, targetWire);
      pendingOps.register(op);
      flashToast(plan.cross ? t('split.cross_copy') : t('split.copy_queued'));
      queued = true;
    } catch (err) {
      // ⚠ Said in the reader's words by reportMutationError (the server's own
      // sentence when it wrote one for a person — permissions, a read-only
      // storage, a full quota — and the lock sentence for a 423). There used
      // to be a hard-coded "cross-storage is not supported" here, and then the
      // raw message.
      reportMutationError(err, { op: 'transfer', targetWire });
      return false;
    }
  } else {
    if (plan.cross) flashToast(t('split.cross_move'));
    queued = await moveSourcesAsync(list, targetWire, 'move-transfer', originWire);
  }
  void splitPaneRef.value?.reload();
  return queued;
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
    :dir="dir"
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
    <!-- wiring:d1 — the tab strip used to be the explorer's first row, above
         the header and spanning the sidebar too.
         gorunum:v2-topbar — it then moved under the breadcrumb, INSIDE the
         left pane.
         gorunum:v5-panestack — and it is out again, one level up: a tab is a
         location the WINDOW is showing and the split happens within it, so the
         strip spans both panes and each pane carries only its own address.
         See `.fe__stack` below. -->
    <Toolbar
      ref="toolbarRef"
      :view-mode="displayedViewMode /* ui-fix — the active pane's mode */"
      :search-query="searchQuery"
      :trash-active="trashActive"
      :actions="toolbarActions"
      :selection-mode="selectionMode"
      :selection-count="activeTargets().length /* gorunum:v1 — the count the
           selection bar prints. Without it the toolbar counted the ticked rows by
           watching the DOM, which is a second source of truth for something the
           parent already knows.
           pane:p1 — and it is the ACTIVE pane's count, so the number and the
           bar's position can never describe two different panes. */"
      :selection-pane="activePaneId /* pane:p1 — which half the bar mounts into */"
      :paste-enabled="!!clipboard.mode"
      :convert-enabled="!!legacyConvertUrl"
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
      :scope-label="driveScopeLabel /* surucu:d1 */"
      :brand-name="config.brand?.name /* gorunum:v3-shell */"
      :brand-mark-url="config.brand?.markUrl /* gorunum:v3-shell */"
      :search-escalates="navView === 'home' /* gorunum:v3-shell */"
      @open-palette="openPaletteWith /* surucu:d1 */"
      @toggle-inspector="toggleInspector /* koru:k1 */"
      @toggle-nav="toggleSideNav /* gezinti:g1 */"
      @open-theme="showThemeGallery = true /* wiring:c1 */"
      @update:view-mode="setDisplayedViewMode($event) /* ui-fix — to the active pane */"
      @update:search-query="onToolbarSearch /* gorunum:v1-advsearch */"
      @open-advanced-search="openAdvancedSearch /* gorunum:v1-advsearch */"
      @update:density="density = $event"
      @open-shortcut-settings="showShortcutSettings = true /* wiring:c2 */"
      @open-timezone="showTimeZone = true /* zaman:z3 */"
      @new-folder="showNewFolder = true"
      @upload="triggerUpload"
      @refresh="refreshAll /* gorunum:v2-topbar */"
      @go-up="goUp"
      @action="onToolbarAction"
      @open-recents="showRecents = true"
    >
      <!-- gorunum:v3-shell — the host's product mark, at the far left of the
           top bar beside the panel's collapse control. Passed straight
           through: this package has no branding of its own and must not grow
           any.
           ⚠⚠ `v-if="$slots.brand"` is load-bearing, not tidiness. Declaring
           this template unconditionally would hand Toolbar a `brand` slot on
           every mount — an EMPTY one for a host that filled nothing — and a
           slot that exists always is a slot whose fallback content never
           renders. That fallback is `config.brand`, and it is the only door a
           `<filex-explorer>` host has (slots do not reach a Vue custom
           element at all; see ExplorerConfig.brand for the measurement). So:
           slot when there is one, config otherwise. -->
      <template v-if="$slots.brand" #brand><slot name="brand"></slot></template>
      <!-- gorunum:v2-topbar — the host's account-level doors (admin panel,
           settings, sign out), passed straight through. The explorer knows
           nothing about them and must not: they are the EMBEDDER's chrome,
           and an embed with none renders an empty cluster. -->
      <template #header-actions><slot name="header-actions"></slot></template>
    </Toolbar>

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
      :active-tag-kind="navTagKind"
      :tags="navTags"
      :tags-loaded="navTagsLoaded"
      :active-storage="adapter"
      :storages="navStorages /* #57 — in the person's order */"
      :storage-order-custom="storageOrder.custom.value"
      @reorder-storages="onReorderStorages"
      :shared-storages="sharedStorageNames"
      :trash-visible="config.trashVisible !== false"
      :show-connections="connectionsEnabled"
      :show-my-shares="mySharesEnabled /* paylas:m1 — off unless the host has the page */"
      :show-identity-surfaces="identitySurfaces"
      :can-write="canWriteHere && !atVirtualRoot && !trashActive"
      :locale="locale"
      :can-request-files="canWriteHere && !atVirtualRoot && !trashActive && !navView /* surucu:d1 */"
      :can-new-document="canNewDocument && !trashActive && !navView /* belge:n1 */"
      @new-document="showNewDocument = true"
      :quota="quotaSnapshot /* surucu:d1 */"
      :theme="themeMode /* surucu:d1 — the teleported New menu leaves .fe */"
      @request-files="openFileRequest /* surucu:d1 */"
      @toggle="toggleSideNav"
      @close="closeNavDrawer"
      @open-view="openNavDest /* gorunum:v3-shell */"
      @open-tag="loadTagView"
      @open-storage="openNavStorage"
      @upload="triggerUpload"
      @new-folder="showNewFolder = true"
      @open-connections="openConnections"
      @open-tokens="openTokens"
      @open-my-shares="emit('open-my-shares') /* paylas:m1 — the host owns the page */"
      :apps="pluginHomeApps /* App plugins — the home views */"
      @open-app="openPluginHome"
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
    <!-- gorunum:v5-panestack / pane:p1 — THE TAB STRIP BELONGS TO THE WINDOW,
         and BOTH halves below it are the SAME component.

         Owner's decision, 2026-09-13: *"tab içinde split yapman lazım, dışında
         yapıyorsun"* — a tab is a location the window is showing, and the split
         happens WITHIN that location. So: top bar, then the strip full width,
         then the panes. The strip used to render inside the left half, which
         made the left pane carry the window's chrome and the right one read as
         an afterthought.

         And the second half of the same ruling: *"split pane ile gelen yeni
         pane aslında yandaki pane ile birebir olması lazım"*. There is now ONE
         `FilePane`, rendered twice. Everything a listing has — the crumbs, the
         filter row, the sort control, the view switcher, the selection-bar
         slot, the states, the column menu — is defined once, in that file, so
         a feature added to a pane cannot miss the other pane. -->
    <div class="fe__stack">
    <TabBar
      v-if="paneRowVisible"
      :tabs="tabItems"
      :active-id="tabsActiveId"
      :locale="locale"
      :hide-tabs="!tabsVisible /* gorunum:v5-panerow — the row stays for the
             split and details toggles even where tabs are not offered */"
      :split-enabled="splitOffered /* the pane's own gate — see splitOffered */"
      :split-active="!!activeSplit"
      :inspector-enabled="infoPanelToggle"
      :inspector-open="showInspector"
      @select="activateTab"
      @close="closeTabById"
      @new="newTabHere"
      @reorder="(from: number, to: number) => tabsApi.move(from, to)"
      @toggle-split="toggleSplit"
      @toggle-inspector="toggleInspector"
    />
    <div class="fe__panes">

    <!-- pane:p1 — the main pane. HOST-DRIVEN (`self-driven` absent): its rows
         come from `load()`, which also answers a search, the trash, the
         panel's virtual views and an encrypted folder. Those are states the
         WINDOW is in, so the three of them that have no listing behind them
         arrive through the `body` slot and the rest through `empty`. -->
    <FilePane
      pane-id="main"
      ref="mainPaneRef"
      :api="api"
      :locale="locale"
      :theme="themeMode"
      :focused="mainPaneFocus"
      :path="currentPath"
      :qualify="qualify"
      :to-user="paneToUser"
      :clamp="paneClamp"
      :root-path="rootPathProp"
      :floor="rootFloor"
      :multi-root="multiStorageRoot"
      :rows="files"
      :loading="loading"
      :error="loadError"
      :order="listingOrder"
      :view-mode="viewMode"
      :view-modes="allowedViewModes"
      :open-trigger="props.config.openTrigger"
      :show-crumbs="navView !== 'home' /* Home has no address: its cards come
             from every folder in every storage, so a trail would have to name
             one */"
      :filter-mode="filterRowMode /* `show-filter-bar` is NOT passed: the row is
             drawn in every view now, so the default (true) is the answer and a
             prop repeating it would be a second place to forget. */"
      :find-label="navView === 'home' ? t('filter.find.home') : ''"
      :show-view-switcher="navView !== 'home'"
      :folder-key="currentFolderKey"
      :show-parent-path="!!searchQuery || crossFolderView"
      :trash="trashActive"
      :clipped="clippedPaths"
      :extra-filters="advFilters"
      :can-write="canWriteHere && !trashActive"
      :can-paste="!!clipboard.mode"
      :selected="selection.selected.value"
      :filters="driveFilters"
      :thumb-src="thumbs.src"
      :keep-badge-for="desktopSync ? keepBadgeFor : undefined"
      :starred-ids="starredIds"
      :star-enabled="identitySurfaces"
      :api-base="props.config.apiBase ?? ''"
      :auth-headers="() => buildAuthHeaders()"
      :auth-credentials="api.credentialsMode()"
      :e2e-active="e2eActive"
      :body-override="hostBodyState !== ''"
      @activate="setPaneMain"
      @navigate="onNavigate"
      @open="openNode"
      @open-trash="() => loadTrash()"
      @click-row="(n, m) => onPaneClickRow('main', n, m)"
      @context="(n: FileNode | null, ev: MouseEvent) => onPaneMenu('main', n, ev)"
      @clear-selection="selection.clear()"
      @display-order="(nodes: FileNode[]) => (displayOrder = nodes)"
      @item-drag-start="(n: FileNode, ev: DragEvent) => onPaneItemDragStart('main', n, ev)"
      @item-drop-into="onItemDropInto"
      @transfer="onPaneTransfer"
      @update:view-mode="(v: ViewMode) => (viewMode = v)"
      @update:filters="setDriveFilters"
      @crumb-context="onCrumbContext"
      @copy-path="onCopyPath"
      @crumb-drop="onCrumbDropInto"
      @new-folder="showNewFolder = true"
      @upload="triggerUpload"
      @paste="onToolbarAction('paste') /* surucu:d1-actions — through the SAME
             handler the right-click menu and the toolbar use */"
      @select-all="selection.selectAll()"
      @clear-filters="clearDriveFilters"
      @star-change="onStarChange"
      @retry="retryLoad"
    >
      <!-- ⚠ NO `#heading` for Home, and the empty address row it used to live
           on is not drawn either (FilePane's own guard). Owner, 2026-09-13:
           "ver sayfa içinde salak bir Home yazısı var, onu kaldıralım."
           He is right and it is the same rule the tab strip already follows:
           the panel's Home row is highlighted, the tab says Home and the
           address bar says `#.home` — a fourth "Home", in 18px type, over
           three sections that name themselves, was the page introducing itself
           to somebody who had just clicked its name. The SECTION headings
           (Storages / Recent / Starred) stay: those name the blocks, not the
           page. `home.title` is still the tab's and the panel row's word. -->

      <!-- Strips that describe the WINDOW's state rather than the listing. -->
      <template #banners>
    <!-- A search that matched more than it returned: the index filled its page,
         or the index-less fallback filled its window of names. Without this a
         cut list reads as the whole answer. No count — the number that came
         back is the window's, not the matches'. -->
    <div
      v-if="searchQuery && searchTruncated && !navView && !trashActive"
      class="fe-search-cut"
      role="status"
    >{{ t('search.truncated') }}</div>

    <!-- The catalog does not cover all of this storage yet (a first sync, a
         lazily cataloged storage): search, folder sizes and usage leave part
         of it out, and this says so and why (lib/catalogCoverage). -->
    <div
      v-if="coverageShown"
      class="fe-coverage"
      role="status"
      data-testid="catalog-coverage"
    >
      <span class="fe-coverage__text">{{ t(coverageShown.key, coverageShown.vars) }}</span>
      <button
        v-if="coverageShown.offerCatalogAll"
        type="button"
        class="fe-coverage__action"
        data-testid="catalog-coverage-all"
        :disabled="catalogAllBusy"
        @click="catalogAll(coverageShown.storage)"
      >{{ t('coverage.catalog_all') }}</button>
    </div>

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
      <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
      <span class="fe-e2e-strip__icon" aria-hidden="true" v-html="actionIconSvg('lock')"></span>
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

    <!-- tablo:t1 — the trash banner: what the trash IS on the left, the one
         irreversible action on the right. OUTSIDE `fe__body` so it sits above
         the listing AND above the centred empty state, which is where the
         reference draws it and the only place it reads as a property of the
         view rather than of the rows. -->
    <div v-if="trashMode && !loading" class="fe-trashbar">
      <p class="fe-trashbar__text">{{ trashBannerText }}</p>
      <p v-if="trashEmptyProgress" class="fe-trashbar__progress" role="status" aria-live="polite">
        {{ trashEmptyProgress }}
      </p>
      <!-- Offered only when the SERVER has said this caller may purge. The
           backend refuses regardless of what we draw; this is so nobody is
           handed a button that always fails. -->
      <button
        v-if="trashCanEmpty"
        type="button"
        class="fe-btn fe-btn--danger fe-trashbar__action"
        :disabled="files.length === 0 || trashEmptying"
        data-testid="trash-empty"
        @click="showTrashConfirm = true"
      >
        {{ t('trash.empty_action') }}
      </button>
    </div>
      </template>

      <!-- The three states with no listing behind them. `hostBodyState` keeps
           them in the order the old chain had — and, crucially, behind the
           skeleton, so a folder that is still loading does not flash its lock
           screen. -->
      <template #body>
      <!-- gorunum:v3-shell — Home. FIRST in the chain, and it short-circuits
           every state below it on purpose: those all describe a LISTING (a
           dead deep link, a failed fetch, a locked folder, an empty folder)
           and Home has no listing behind it — `files` is deliberately empty
           while it is open, which every one of them would read as "nothing
           here". Its own three blocks each carry their own empty state. -->
      <HomeView
        v-if="hostBodyState === 'home'"
        :storages="homeStorages"
        :recent="homeRecent"
        :starred="homeStarred"
        :loading="homeLoading"
        :locale="locale"
        :name-filter="driveFilters.name ?? '' /* surucu:d1-scope — Home's filter
               row is the name box alone, and this is what it narrows. The same
               `DriveFilters.name` the listing's own box writes, so the value is
               reset on navigation by the one watcher that already does that and
               there is no second piece of filter state to keep in step. */"
        :thumb-src="thumbs.src"
        @open-storage="openNavStorage"
        @open-node="openNode"
        @context-node="onContextTarget"
      />
      <!-- Dead deep link (404) or RBAC-hidden dir (403, shown identically):
           a dedicated state instead of a misleading "this folder is empty". -->
      <div v-else-if="hostBodyState === 'notfound'" class="fe-state">
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
      <!-- wiring:e2 — encrypted-folder lock screen: the listing is not
           rendered until the correct password is entered. The password is
           verified against the marker in the browser; it never reaches the
           server. -->
      <div v-else class="fe-state fe-e2e-lock">
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
            :aria-label="t('e2e.locked.pw_placeholder') /* the title and hint above
              say what this screen is; the field still needs its own name */"
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
      </template>

      <!-- Loaded and empty: WHICH empty. The pane's own fallback ("this folder
           is empty") is the last branch here, and it is the one the split pane
           falls back to. -->
      <template #empty>
      <!-- Search with zero hits — its own message, not "folder is empty". -->
      <div v-if="searchQuery" class="fe-state">
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
        v-else-if="navView && navView !== 'trash'"
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
      <div v-else-if="trashMode" class="fe-state">
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
        <!-- tablo:t1 — the second line the reference has and we did not. The
             key did not exist in EITHER catalogue, so there was nothing to
             show even if something had asked for it. -->
        <p class="fe-state__hint">{{ t('empty.trash.hint') }}</p>
      </div>
      <!-- Loaded, zero files, no search: the real empty-folder state. The
           upload affordances follow write permission (RBAC viewers only get
           the title). -->
      <div v-else class="fe-state">
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
      </template>
    </FilePane>

    <!-- pane:p1 — the split pane. The SAME component, SELF-DRIVEN: it asks the
         backend for one folder through the same `api.index` + `lib/listing`
         helpers. `:key` is bound to the tab id, so a tab switch remounts it
         with its own location. -->
    <FilePane
      v-if="splitVisible && activeSplit"
      ref="splitPaneRef"
      :key="'split-' + tabsActiveId"
      pane-id="split"
      self-driven
      closable
      :api="api"
      :locale="locale"
      :theme="themeMode"
      :focused="paneIsActive"
      :path="activeSplit.path"
      :qualify="qualify"
      :to-user="paneToUser"
      :clamp="paneClamp"
      :root-path="rootPathProp"
      :floor="rootFloor"
      :multi-root="multiStorageRoot"
      :virtual-rows="virtualStorageRows"
      :trash-visible="config.trashVisible !== false"
      :nav-offers-trash="navOffersTrash"
      :view-mode="paneViewMode"
      :view-modes="allowedViewModes"
      :open-trigger="props.config.openTrigger"
      :clipped="clippedPaths"
      :can-write="canWriteHere"
      :can-paste="!!clipboard.mode"
      :selected="splitSelection.selected.value"
      :filters="splitFilters"
      :folder-key="splitFolderKey"
      :thumb-src="thumbs.src"
      :keep-badge-for="desktopSync ? keepBadgeFor : undefined"
      :starred-ids="starredIds"
      :star-enabled="identitySurfaces"
      :api-base="props.config.apiBase ?? ''"
      :auth-headers="() => buildAuthHeaders()"
      :auth-credentials="api.credentialsMode()"
      @activate="activePane = 'split'"
      @close="closeSplit"
      @navigate="onPaneNavigate"
      @open="openNode"
      @open-trash="onPaneOpenTrash"
      @click-row="(n, m) => onPaneClickRow('split', n, m)"
      @context="(n: FileNode | null, ev: MouseEvent) => onPaneMenu('split', n, ev)"
      @clear-selection="splitSelection.clear()"
      @display-order="(nodes: FileNode[]) => (splitDisplayOrder = nodes)"
      @item-drag-start="(n: FileNode, ev: DragEvent) => onPaneItemDragStart('split', n, ev)"
      @item-drop-into="onItemDropInto"
      @transfer="onPaneTransfer"
      @update:view-mode="setPaneViewMode"
      @update:filters="(v: DriveFilters) => (splitFilters = v)"
      @copy-path="onCopyPath"
      @crumb-drop="onCrumbDropInto"
      @new-folder="showNewFolder = true"
      @upload="triggerUpload"
      @paste="onToolbarAction('paste')"
      @select-all="splitSelection.selectAll()"
      @clear-filters="splitFilters = { ...EMPTY_FILTERS }"
      @star-change="onStarChange"
    />

    </div><!-- /fe__panes -->
    </div><!-- /fe__stack pane:p1 -->


    <!-- koru:k1 — inspector (details) panel; v-if keeps the closed state
         free of any DOM. Narrow mode renders it as a full-size overlay. -->
    <InspectorPanel
      v-if="showInspector"
      :api="api"
      :nodes="inspectorNodes /* pane:p1 — the FOCUSED pane's selection, through
             the same `activeTargets()` the selection bar reads. It was
             `selection.nodes` (the main pane's, always), which is why clicking
             in the split pane changed nothing here. */"
      :dir-label="inspectorDirLabel"
      :dir-count="inspectorDirCount"
      :dir-perm="inspectorDirPerm"
      :held-in="inspectorHeldIn /* non-empty ⇒ this is the LAST selected thing,
             not what is ticked in front of you, and it lives here. */"
      :locale="locale"
      :narrow="isNarrow"
      :caller-admin="callerAdmin /* the Node ID row is an administrator's (InspectorPanel) */"
      :thumb-src="thumbs.src"
      :api-base="props.config.apiBase ?? '' /* etiket:t1 — the details panel's
             Tags section mounts the same TagPicker the context menu opens, and
             that component talks to `/api/files/tags/*` itself. These three are
             the trio every self-fetching child in this package already takes
             (StarButton, TagPicker, GridView's star column): the base, the
             headers, and the credentials mode. ⚠ `authCredentials` is not
             decoration — a credentialed cross-origin request cannot be answered
             with `ACAO: *`, so an embed served from a different origin to the
             API breaks without it. */"
      :auth-headers="() => buildAuthHeaders()"
      :auth-credentials="api.credentialsMode()"
      :plugin-views="pluginInspectorViews /* App plugins — inspector sections */"
      :theme="themeMode"
      :storages="(props.config.storages ?? []).map((st) => st.name)"
      @plugin-op="(op) => onPluginOpQueued(op)"
      @plugin-open="(p) => onSurfaceOpen(p.plugin, p.req)"
      @open-tag="openTagView /* etiket:t1 — a tag chip in this panel is a door to
             that tag's view, the same door the navigation panel's Tags section
             opens. */"
      @close="closeInspector"
      @share-created="onInspectorShareCreated /* surucu:d1 */"
      @manage-permissions="onInspectorManage"
      @toast="flashToast"
      @changed="() => (paneIsActive ? void splitPaneRef?.reload() : void load()) /* pane:p1 —
             the panel now acts on the FOCUSED pane's selection, so the listing
             it refreshes afterwards has to be that pane's. Reloading the main
             one would leave a restored version, a deleted comment or a renamed
             file on screen unchanged in the half it happened in. */"
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
          closable
          @close="closeOverlays"
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
        <!-- ⚠ Was the `⬆` emoji: rendered by whatever emoji font the machine
             has, so the one mark on the drop overlay came out as a blue arrow
             on Windows, a grey one on Linux and nothing at all on a headless
             Chromium with no emoji font — on the single screen whose whole job
             is one glyph and one line. Same `upload` key the "+ New" menu and
             the toolbar draw, so the three now agree. -->
        <!-- eslint-disable-next-line vue/no-v-html — static markup from lib/actionIcons -->
        <span class="fe-icon" aria-hidden="true" v-html="actionIconSvg('upload')"></span>
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
      :output-mode-of="pluginOutputModeOf"
      :caller-admin="callerAdmin"
      @cancel="cancelPendingOp"
      @dismiss="(id) => pendingOps.dismiss(id)"
      @open="onOpenOpOutput"
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

    <!-- belge:n1 — New document: the types this deployment can actually create
         AND open, then a name, then where it goes. -->
    <NewDocumentModal
      :open="showNewDocument"
      :locale="locale"
      :theme="themeMode"
      :api="api"
      :types="newDocTypes"
      :current-path="qualify(currentPath)"
      :storages="(props.config.storages ?? []).map((st) => st.name) /* ⚠ the dialog
                 takes NAMES; `config.storages` is objects, and passing them
                 straight through (as the wiring note had it) does not
                 typecheck. */"
      :only-office-ready="!!effectiveOnlyOfficeBase"
      :drawio-ready="!!effectiveDrawioUrl"
      :can-configure="callerAdmin"
      @close="showNewDocument = false"
      @created="onDocumentCreated"
      @error="emit('error', { message: $event.message, context: { op: 'newdoc' } })"
    />
    <NewFolderModal
      :open="showNewFolder"
      :locale="locale"
      :encrypted-option="!e2eActive /* wiring:e2 — no nested encrypted folders */"
      :busy="newFolderBusy"
      :error="newFolderError"
      @close="showNewFolder = false"
      @submit="submitNewFolder"
      @encrypted="showNewFolder = false; showEncFolder = true /* wiring:e2 */"
    />
    <ArchiveCreateModal
      :open="showArchiveCreate"
      :locale="locale"
      :count="archiveTargets.length"
      :suggested-name="archiveSuggestedName"
      :default-format="archiveDefaultFormat"
      :allowed-formats="archiveAllowedFormats"
      :encryption="archiveEncryption"
      :busy="archiveBusy"
      :request-error="archiveError"
      @close="showArchiveCreate = false"
      @submit="submitArchiveCreate"
    />
    <ArchiveExtractModal
      :open="showArchiveExtract"
      :locale="locale"
      :archive-name="archiveTarget?.basename || ''"
      :suggested-folder="archiveSuggestedFolder"
      :busy="archiveBusy"
      :error="archiveError"
      @close="showArchiveExtract = false"
      @submit="submitArchiveExtract"
    />
    <ArchivePasswordModal
      :open="showArchivePassword"
      :locale="locale"
      :archive-name="archivePasswordAction?.target.basename || archiveTarget?.basename || ''"
      :busy="archiveBusy"
      :error="archivePasswordError"
      @close="closeArchivePassword"
      @submit="submitArchivePassword"
    />

    <!-- tasi:m1 — "Şuraya taşı…" / "Şuraya kopyala…". ONE dialog for both, and
         the same one anything else that has to ask for a folder mounts (the
         new-document flow does): a second private folder browser is how two
         choosers start disagreeing about what a writable folder is. -->
    <DestinationPickerModal
      :open="showDestPicker"
      :api="api"
      :locale="locale"
      :mode="destPickerMode"
      :busy="destPickerBusy"
      :storages="(props.config.storages ?? []).map((st) => st.name) /* NAMES, not
                 the objects — same shape NewDocumentModal takes */"
      :start-at="qualify(paneIsActive ? (splitPaneRef?.getPath() ?? '') : currentPath) /* open
                 where the selection lives, not at the drive list */"
      :moving="destPickerMode === 'move'
        ? destPickerTargets.filter((n) => n.type === 'dir').map((n) => n.path)
        : [] /* only a MOVE can eat itself; a copy into your own subfolder is legal */"
      @close="showDestPicker = false"
      @pick="onDestinationPicked"
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
      :error="renameError"
      :busy="renameBusy"
      @close="showRename = false"
      @submit="submitRename"
    />
    <!-- tablo:t1 — emptying the trash is irreversible and it is a BULK
         delete, so the question names what is about to go: how many things and
         how much space. "Empty the trash?" with no quantity is a question
         nobody can actually answer, and this is the last screen before the
         bytes are gone for good. -->
    <Modal
      :open="showTrashConfirm"
      :title="t('trash.empty_confirm_title')"
      size="sm"
      @close="showTrashConfirm = false"
    >
      <!-- ⚠ A singular form exists (`…_one`) and `t()` picks it from `count`:
           this is the last screen before an irreversible bulk delete, and
           "1 items" is not a sentence anybody should have to read there. -->
      <p>{{ t(trashConfirmKey, { count: files.length, size: formatSize(trashTotalBytes) }) }}</p>
      <template #actions>
        <button type="button" class="fe-btn" @click="showTrashConfirm = false">
          {{ t('modal.delete.cancel') }}
        </button>
        <button
          type="button"
          class="fe-btn fe-btn--danger"
          data-testid="trash-empty-confirm"
          @click="emptyTrash"
        >
          {{ t('trash.empty_action') }}
        </button>
      </template>
    </Modal>
    <DeleteConfirmModal
      :open="showDelete"
      :locale="locale"
      :count="selection.size.value"
      :busy="deleteBusy"
      :error="deleteError"
      @close="showDelete = false"
      @confirm="confirmDelete"
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
      :can-configure="callerAdmin"
      :new-tab-enabled="!e2eActive /* wiring:e2 — the standalone route pulls raw bytes */"
      :save-text-endpoint="e2eActive ? null : api.endpoints.saveText || null /* wiring:e2 — a plaintext save would be a leak */"
      :archive-list-endpoint="api.endpoints.archiveList || null"
      :open-mode="previewMode"
      :open-as="previewOpenAsExt /* #56 — a New document opens as its type */"
      :auth-headers="() => buildAuthHeaders({ 'Content-Type': 'application/json' })"
      :auth-credentials="api.credentialsMode()"
      :drawio-url="effectiveDrawioUrl"
      :pdf-worker-url="props.config.pdfWorkerUrl || null"
      :pdf-save-url="props.config.pdfSaveUrl || null"
      :viewer-base-url="effectiveViewerBaseUrl"
      :index="previewPosition.index /* gorunum:v1 — the 3-of-9 counter */"
      :total="previewPosition.total"
      :nav-enabled="previewPosition.total > 1"
      :share-enabled="!e2eActive /* gorunum:v2 — the viewer's share icon opens the
           SAME dialog the menu opens. It shipped disabled because nothing was
           listening; an icon that does nothing is worse than no icon. Off inside
           an encrypted folder, where a link would serve ciphertext. */"
      @share="() => {
        const n = previewTarget;
        if (n) { permTarget = n; permInitialTab = undefined; showPerm = true; }
      }"
      :api-base="props.config.apiBase ?? ''"
      @nav="onPreviewNav"
      @close="showPreview = false"
    />
    <!-- App plugins — a `modal` view, and the manifest's confirm question. -->
    <PluginViewModal
      v-if="pluginView"
      :open="!!pluginView"
      :api="api"
      :locale="locale"
      :theme="themeMode"
      :plugin="pluginView.plugin"
      :view="pluginView.view"
      :surface="pluginView.surface"
      :path="pluginView.path"
      :paths="pluginView.paths"
      :size="pluginView.size"
      :storages="(props.config.storages ?? []).map((st) => st.name)"
      :start-at="qualify(paneIsActive ? (splitPaneRef?.getPath() ?? '') : currentPath)"
      @close="pluginView = null"
      @op="(op) => onPluginOpQueued(op)"
      @toast="flashToast"
      @open="(req) => onSurfaceOpen(pluginView?.plugin ?? '', req)"
    />
    <PluginConfirmModal
      :open="!!pluginConfirm"
      :locale="locale"
      :theme="themeMode"
      :title="pluginConfirm ? pluginLabelOf(pluginConfirm.action.label, locale) : ''"
      :message="pluginConfirm ? pluginLabelOf(pluginConfirm.action.confirm, locale) : ''"
      :danger="pluginConfirm?.action.danger === true"
      @close="pluginConfirm = null"
      @confirm="onPluginConfirmed"
    />
    <ConvertModal
      v-if="showConvert && convertTarget && legacyConvertUrl"
      :convert-url="legacyConvertUrl"
      :admin-note="callerAdmin ? t('convert.legacy_admin') : ''"
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
      :locale="locale"
      :share-max-ttl-days="shareMaxTtlDays"
      :initial-tab="permInitialTab /* surucu:d1 */"
      :mail-ready="capabilitiesData?.mail?.ready"
      :can-configure="callerAdmin"
      :perm="permTarget.perm || dirPerm"
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
            <strong>{{ t('recents.title') }}</strong>
            <button class="fe-recents__close" :aria-label="t('recents.close')" @click="showRecents = false">×</button>
          </div>
          <RecentlyOpened
            :locale="locale"
            :api-base="props.config.apiBase ?? ''"
            :auth-headers="() => buildAuthHeaders()"
            :auth-credentials="api.credentialsMode()"
            :limit="20"
            :refresh-key="recentRefreshKey"
            @open="onRecentOpen"
            @context="onRecentContext"
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
              {{ t('tags.title') }} — {{ tagPickerNode.basename }}
            </h2>
            <button class="fe-modal__close" :aria-label="t('tags.close')" @click="showTagPicker = false">×</button>
          </header>
          <div class="fe-modal__body">
            <TagPicker
              :node-id="tagPickerNode.id"
              :locale="locale"
              :api-base="props.config.apiBase ?? ''"
              :auth-headers="() => buildAuthHeaders()"
              :auth-credentials="api.credentialsMode()"
              @open="openTagView /* etiket:t1 — the SAME door as in the details
                     panel; `openTagView` closes this dialog on the way, because
                     leaving a modal open over the view it just navigated to is
                     a dialog nobody asked to keep. */"
              @error="(msg: string) => emit('error', { message: msg, context: { op: 'tags' } })"
            />
          </div>
        </div>
      </div>
    </transition>

    <!-- gorunum:v1-advsearch — the advanced search dialog. Its result lands in
         `files` through the same load() a toolbar search lands in, so there is
         exactly one results surface and one empty state. -->
    <AdvancedSearch
      :open="showAdvSearch"
      :locale="locale"
      :theme="themeMode"
      :initial-query="advSearchSeed"
      :folder-label="driveScopeLabel"
      :path-base="advPathBase"
      :content-search="advContentAvailable"
      :count="advSearchCount"
      :known-tags="advKnownTags"
      @close="showAdvSearch = false"
      @submit="applyAdvancedSearch"
    />

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
      :hit-can="paletteHitCan /* #47 */"
      @close="showPalette = false"
      @open-hit="onPaletteOpenHit /* #47 — another account's hit goes to the host */"
      @download-hit="downloadSearchHit /* #47 */"
      @drag-hit="onPaletteDragHit /* #47 */"
      @warm-hit="onPaletteWarmHit /* #71 */"
      @drop-inside="onPaletteDropInside /* #47 */"
      @open-node="openNode"
      @navigate="(p: string) => load(p)"
      @new-folder="showNewFolder = true"
      @upload="triggerUpload"
      @toggle-view="setDisplayedViewMode(displayedViewMode === 'list' ? 'grid' : displayedViewMode === 'grid' ? 'gallery' : 'list') /* wiring:d2 + ui-fix — 3-mode cycle, to the active pane */"
      @open-trash="loadTrash"
      @refresh="refreshAll /* gorunum:v2-topbar */"
      @go-up="goUp"
      @open-theme="showThemeGallery = true /* wiring:int */"
      @open-shortcut-settings="showShortcutSettings = true /* wiring:int */"
      @start-tour="startTour() /* wiring:int */"
      :split-enabled="splitOffered /* wiring:d1 — see splitOffered */"
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

    <!-- zaman:z3 — the embed's own time-zone setting (kept in this browser) -->
    <TimeZoneDialog
      :open="showTimeZone"
      :locale="locale"
      :theme="themeMode"
      @close="showTimeZone = false"
    />

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
      :can-configure="callerAdmin"
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
<!-- The five signing faces, self-hosted. Declared here so the ONE rolled-up
     `style.css` every host already imports carries them; a browser fetches a
     `.woff2` only when text is actually drawn in that family, so a host that
     never opens a signing screen pays nothing. -->
<style src="./styles/sign-fonts.css"></style>
