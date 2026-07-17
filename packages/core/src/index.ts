/**
 * @brftech/filex-core — public entry.
 *
 * Vue 3 source of truth for the filex file manager. Sibling packages
 * (`@brftech/filex` Web Component, `@brftech/filex-react` React adapter)
 * wrap this single FileExplorer SFC.
 *
 * Usage (Vue):
 *   import { FileExplorer } from '@brftech/filex-core';
 *   import '@brftech/filex-core/style.css';
 *
 *   <FileExplorer
 *     :config="{ apiBase: 'https://files.example.com',
 *                auth: { kind: 'bearer', token: '…' } }"
 *     @error="…"
 *   />
 */

export { default as FileExplorer } from './FileExplorer.vue';

// PreviewModal is exposed so embedders can mount their own
// fullscreen editor route (e.g. /files/edit) without re-implementing
// the viewer dispatch logic.
export { default as PreviewModal } from './modals/PreviewModal.vue';

// ——— Phase-2 standalone components (consumers can mount these
//      independently of the FileExplorer host, e.g. a sidebar tray).
export { default as StarButton } from './components/StarButton.vue';
export { default as TagPicker } from './components/TagPicker.vue';
export { default as RecentlyOpened } from './components/RecentlyOpened.vue';

// ——— Types ———
export type {
  ExplorerConfig,
  ExplorerEmits,
  AuthConfig,
  ThemeMode,
  LocaleCode,
  EndpointMap,
} from './types/ExplorerConfig';

export type {
  FileNode,
  ShareInfo,
  UploadLimits,
  Capabilities,
  ExternalServiceState,
  ExternalServiceStatus,
  UploadInitResponse,
  UploadFinalizeResponse,
  ArchiveEntry,
  ViewMode,
  ClipboardState,
} from './types/FileNode';
export { isExternalUsable } from './types/FileNode';

// ——— Composables (consumers can roll their own UI on top) ———
export { useFileApi, resolveEndpoints } from './composables/useFileApi';
export type { FileApi, ManagerResponse, PendingOpDto } from './composables/useFileApi';
/* bul:s3 — global-search contract types + snippet helpers */
export type { GlobalSearchHit, GlobalSearchScope } from './composables/useFileApi';
export { snippetSegments, matchedInContent } from './lib/snippet';
export type { SnippetSegment, SearchMatched } from './lib/snippet';

export { useUploadChunked } from './composables/useUploadChunked';
export type { UploadJob, UploadOptions } from './composables/useUploadChunked';

export { useSelection } from './composables/useSelection';
export { useKeyboardShortcuts } from './composables/useKeyboardShortcuts';
export type { ShortcutHandlers } from './composables/useKeyboardShortcuts';
export { useLocale } from './composables/useLocale';
export { usePendingOps } from './composables/usePendingOps';
export type { PendingOp, UsePendingOpsOptions } from './composables/usePendingOps';
export {
  useMonacoLoader,
  preloadEditor,
  ensureMonaco,
  ensureHighlight,
  getMonaco,
  getHighlight,
} from './composables/useMonacoLoader';

// ——— Locale catalogue (consumers may merge their own keys) ———
export { messages, tr, en } from './locales';

/* wiring:c1 — theme registry + gallery (hosts can list/apply themes programmatically) */
export {
  THEMES,
  THEME_LS_KEY,
  DEFAULT_THEME_ID,
  themeById,
  useThemeState,
  setTheme,
  applyThemeToEl,
  syncThemeStyle,
  generateThemeCss,
} from './lib/themes';
export type { ThemeDef, ThemeTokenMap } from './lib/themes';
export { default as ThemeGallery } from './components/ThemeGallery.vue';
/* wiring:c2 — customizable shortcut registry + settings/quick-look UI */
export {
  SHORTCUT_ACTIONS,
  useShortcutList,
  effectiveCombo,
  setShortcutOverride,
  resetShortcut,
  resetAllShortcuts,
  findShortcutConflict,
  comboFromEvent,
} from './composables/useKeyboardShortcuts';
export type {
  ShortcutActionDef,
  ShortcutView,
  ShortcutConflict,
} from './composables/useKeyboardShortcuts';
export { default as ShortcutSettings } from './components/ShortcutSettings.vue';
export { default as QuickLook } from './components/QuickLook.vue';
/* /wiring:c2 */
/* wiring:c3 — unified operations center (badge + panel + store) */
export { useOperations } from './composables/useOperations';
export type {
  Operation,
  OperationInput,
  OperationActions,
  OperationKind,
  OperationStatus,
  OperationsStore,
} from './composables/useOperations';
export { default as OperationsCenter } from './components/OperationsCenter.vue';
/* wiring:d1 — sekmeler + split panel */
export { useTabs } from './composables/useTabs';
export type { TabState, TabSplit, TabsApi } from './composables/useTabs';
export { default as TabBar } from './components/TabBar.vue';
export { default as SecondaryPane } from './components/SecondaryPane.vue';
/* /wiring:d1 */
/* wiring:e2 — uçtan uca şifreli klasörler (WebCrypto; docs/E2E-ENCRYPTION.md) */
export {
  E2E_MARKER_NAME,
  E2E_MAGIC,
  E2E_VERSION,
  E2E_DEFAULT_ITERATIONS,
  E2E_MAX_FILE_BYTES,
  E2E_MIN_PASSWORD_LEN,
  E2eDecryptError,
  deriveKek,
  createMarker,
  parseMarker,
  verifyPassword,
  hasMagic,
  encryptFile,
  decryptFile,
  createKeyRing,
} from './lib/e2ecrypto';
export type { E2eMarker, E2eKeyRing } from './lib/e2ecrypto';
export { default as EncryptedFolderModal } from './components/EncryptedFolderModal.vue';
/* /wiring:e2 */
