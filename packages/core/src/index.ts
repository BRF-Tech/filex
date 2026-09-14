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
// belge:n1 — the "New document" picker. Exported because the entry belongs
// on every surface, not just the admin app: a host that draws its own
// "+ New" menu mounts this and gets the same dialog.
export { default as NewDocumentModal } from './modals/NewDocumentModal.vue';
/* tasi:m1 — the destination picker. Exported for the same reason and one
 * more: it is the folder chooser the product did not have, and it is meant to
 * be the ONLY one. "Move to", "Copy to" and (next) the new-document flow all
 * mount this rather than growing a private browser each. Its rules are pure
 * functions in lib/destinationTree so a host can reuse the decisions without
 * the dialog. */
export { default as DestinationPickerModal } from './modals/DestinationPickerModal.vue';
export {
  DRIVES,
  blockedReason,
  crumbsOfWire,
  destinationRows,
  driveRows,
  initialLocation,
  isAtOrInside,
  joinWire,
  labelOfWire,
  parentOfWire,
  permAllowsWrite,
  splitWire,
} from './lib/destinationTree';
export type { DestinationRow } from './lib/destinationTree';

/* tasi:m1 — "download the selection as one archive". Exported so a host that
 * draws its own selection bar gets the real two-step flow (authorized mint,
 * then a navigation that streams) instead of reaching for window.open per
 * file, which is what the explorer could not do and why Download used to
 * disappear the moment a second row was selected. */
export {
  DOWNLOAD_FRAME_TTL_MS,
  absoluteTicketUrl,
  archiveTicketUrl,
  downloadArchive,
  requestArchive,
  triggerFileNavigation,
} from './lib/downloadSelection';
export type { ArchiveTicket } from './lib/downloadSelection';

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
  NewDocType,
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
/* Browser-side reachability probe for external services (OnlyOffice, drawio).
   Lives here, not in the admin app, because the admin page and every embedder
   need the same answer to "can THIS browser reach the document server?" — see
   lib/externalReach.ts for why a plain fetch() is the wrong mechanism. */
export {
  probeExternalFromBrowser,
  browserProbeURL,
  DEFAULT_PROBE_TIMEOUT_MS,
} from './lib/externalReach';
export type {
  BrowserProbeState,
  BrowserProbeResult,
  BrowserProbeDeps,
} from './lib/externalReach';

/* gorunum:v4-hostmenu — the ACTION glyph vocabulary.
 *
 * Exported because a host draws rows that belong to this explorer: the web
 * app's account menu now carries the explorer's own settings rows (see
 * Toolbar.vue's `fe:header-menu` claim), and an "Restart the tour" row drawn
 * with somebody else's icon set is the emoji problem lib/actionIcons.ts was
 * written to end — a dozen sets, whatever weight and colour the OS shipped,
 * four pixels from a column of stroked grey ones. One vocabulary, one voice.
 * `actionIconSvg` returns '' for an unknown key, so a host can call it for
 * every row and draw nothing where there is nothing. */
export { actionIconSvg, actionIconKeys } from './lib/actionIcons';

export { snippetSegments, matchedInContent } from './lib/snippet';
export type { SnippetSegment, SearchMatched } from './lib/snippet';

export { useUploadChunked, isStagedUnsupported } from './composables/useUploadChunked';
export type {
  UploadJob,
  UploadOptions,
  UploadResult,
} from './composables/useUploadChunked';

/* Resumable-upload bookmarks — a host can list or discard unfinished uploads
   (e.g. an "unfinished uploads" panel) without reimplementing the store. */
export {
  uploadFingerprint,
  listResume,
  loadResume,
  clearResume,
  pruneResume,
  defaultResumeStorage,
  RESUME_TTL_MS,
} from './lib/uploadResume';
export type { ResumeRecord, ResumeStorage } from './lib/uploadResume';

export { useSelection } from './composables/useSelection';
export { useKeyboardShortcuts } from './composables/useKeyboardShortcuts';
export type { ShortcutHandlers } from './composables/useKeyboardShortcuts';
export { useLocale, localeTag, formatByteSize, formatInstant } from './composables/useLocale';
export type { ByteSizeOptions, ByteUnitKey } from './composables/useLocale';
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
export { messages, tr, en, resolveLocale, detectLocale } from './locales';

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
  /* The light/dark MODE half — a different question from which palette paints
   * (see themes.ts). Exported because the appearance controls now live in the
   * host's own settings surface, and a host that can pick a palette but cannot
   * un-pin the mode the explorer's old strip wrote would leave the user with a
   * switch that visibly does nothing. */
  THEME_MODE_LS_KEY,
  useThemeModeState,
  setThemeMode,
} from './lib/themes';
export type { ThemeDef, ThemeTokenMap, ThemeModePref } from './lib/themes';
export { default as ThemeGallery } from './components/ThemeGallery.vue';
/* The palette grid without the modal around it — for hosts that show
 * appearance settings in a pane of their own (the filex admin app does). */
export { default as ThemePalette } from './components/ThemePalette.vue';

/* zaman:z1 / z3 — whose clock an instant is read on. ONE resolver ranks the
 * tiers (`TIME_ZONE_TIERS`: the viewer's own pick in this browser, the host's
 * `config.timeZone`, the account behind a PERSON's credential, the device) and
 * every surface formats through it — the explorer, and a host's own dates
 * outside it (the filex admin app's `lib/format` reads `activeTimeZone()`), so
 * an admin page and an embed cannot disagree about one file's time again. */
export {
  TIME_ZONE_TIERS,
  resolveTimeZone,
  activeTimeZone,
  resolvedTimeZone,
  timeZoneSources,
  TIMEZONE_VIEWER_LS_KEY,
  viewerTimeZone,
  setViewerTimeZone,
  setHostTimeZone,
  TIMEZONE_ACCOUNT_LS_KEY,
  setAccountTimeZone,
  accountTimeZoneOf,
  rememberedAccountTimeZone,
  releaseTimeZoneOwner,
  deviceTimeZone,
  isValidTimeZone,
  supportedTimeZones,
  zonedDayNumber,
} from './lib/timezone';
export type { TimeZoneTier, TimeZoneSources, ResolvedTimeZone } from './lib/timezone';
/* The picker itself — a host with a settings surface of its own (the filex
 * admin app) mounts this rather than writing a second one. */
export { default as TimeZonePicker } from './components/TimeZonePicker.vue';
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
  /* Hint surfaces: the only sanctioned way to NAME a key on screen. A hint
   * written by hand is true until the user remaps that action, and then the
   * product is telling them to press something that does nothing. */
  shortcutHint,
  comboLabel,
  eventMatchesShortcut,
  isMacLike,
  /* tus:t1 — menu/toolbar rows name their key from the same registry, and the
   * combos a browser tab never receives are declared in one place. */
  menuShortcutHint,
  MENU_ACTION_SHORTCUTS,
  isReservedCombo,
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
export { default as FilePane } from './components/FilePane.vue';
/* /wiring:d1 */
/* wiring:e2 — end-to-end encrypted folders (WebCrypto; docs/E2E-ENCRYPTION.md) */
export {
  E2E_MARKER_NAME,
  E2E_MAGIC,
  E2E_VERSION,
  E2E_MARKER_VERSION,
  E2E_DEFAULT_ITERATIONS,
  E2E_MAX_FILE_BYTES,
  E2E_MIN_PASSWORD_LEN,
  E2E_RECOVERY_KEY_BYTES,
  E2E_ESCROW_ALG,
  E2eDecryptError,
  deriveKek,
  createMarker,
  createEncryptedFolder,
  upgradeMarkerV1,
  parseMarker,
  verifyPassword,
  unlockWithPassword,
  unlockWithRecoveryKey,
  unlockWithEscrowKey,
  markerHasRecovery,
  markerHasEscrow,
  escrowAvailability,
  escrowOfferState,
  addEscrowSlot,
  declineEscrowSlot,
  generateRecoveryKey,
  formatRecoveryKey,
  parseRecoveryKey,
  importEscrowPublicKey,
  importEscrowPrivateKey,
  escrowKeyId,
  hasMagic,
  encryptFile,
  decryptFile,
  createKeyRing,
} from './lib/e2ecrypto';
export type {
  E2eMarker,
  E2eKeyRing,
  E2eFmkMode,
  E2eRecoverySlot,
  E2eEscrowSlot,
  EscrowAvailability,
  EscrowOfferState,
  CreateFolderOptions,
  CreatedFolder,
} from './lib/e2ecrypto';
export { default as EncryptedFolderModal } from './components/EncryptedFolderModal.vue';
export { default as RecoveryKeyModal } from './components/RecoveryKeyModal.vue';
export { default as E2eRecoveryUnlockModal } from './components/E2eRecoveryUnlockModal.vue';
/* /wiring:e2 */

/* ── connections ────────────────────────────────────────────────────
   Storage connections and "how to connect", as ONE implementation for
   every surface. The desktop app mounts <filex-connections> (the web
   component wrapper around this), the admin SPA mounts the SFC, embeds
   can do either — and none of them owns a copy of the form. */
export { default as ConnectionsPanel } from './components/ConnectionsPanel.vue';
export { default as StorageFields } from './components/StorageFields.vue';
export { default as ConnectionGuideView } from './components/ConnectionGuideView.vue';
export { default as S3KeysPanel } from './components/S3KeysPanel.vue';
export { default as SSHKeysPanel } from './components/SSHKeysPanel.vue';
export { default as NFSExportsPanel } from './components/NFSExportsPanel.vue';
export { default as TokensPanel } from './components/TokensPanel.vue';
export { useS3Keys } from './composables/useS3Keys';
export { useSSHKeys } from './composables/useSSHKeys';
export { useNFSExports } from './composables/useNFSExports';
export { useTokens } from './composables/useTokens';
export type { S3AccessKey, S3Connection, S3KeyCreated, S3KeyRequest } from './types/S3Keys';
export type { SSHPublicKey, SSHConnection } from './types/SSHKeys';
export type { NFSExport, NFSConnection, NFSExportCreated } from './types/NFSExports';
export type { ApiToken, ApiTokenCreated, ApiTokenRequest } from './types/Tokens';
export { useConnections, connectionsBase, connectionsOrigin } from './composables/useConnections';
export type { ConnectionsApi } from './composables/useConnections';
export {
  GUIDE_BUILDERS,
  buildGuide,
  buildWebdavGuide,
  buildS3Guide,
  buildSftpGuide,
  buildFtpsGuide,
  buildNfsGuide,
  guideProtocols,
  hostOf,
  isPlainHttp,
} from './lib/connectionGuides';
export type {
  GuideBlock,
  GuideBlockKind,
  GuideBuilder,
  GuideClient,
  GuideContext,
  GuideFact,
  ProtocolGuide,
  Translate,
} from './lib/connectionGuides';
export type {
  ConnectionsUser,
  ManageDenial,
  StorageDriverCapabilities,
  StorageDriverDescriptor,
  StorageField,
  StorageFieldOption,
  StorageFieldType,
  StorageRow,
  StorageTestResult,
  StorageWrite,
} from './types/Connections';
/* /connections */

/* tablo:t1 — per-folder view memory + the table configuration.
 *
 * Exported because the HOST owns two things this module cannot reach: the
 * settings control that turns the memory on (`folderMemoryEnabled` /
 * `setFolderMemoryEnabled`), and the transport that reads and writes the
 * document (`attachViewPrefsStore` — the explorer wires its own, but a host
 * embedding the views directly has to). The rest is exported so the gates in
 * `web/tests/lib/viewPrefs.test.ts` drive the real module rather than a copy. */
export {
  COLUMNS,
  FOLDER_CAP,
  NAME_AUTO,
  NAME_MIN,
  __flushViewPrefs,
  __resetViewPrefs,
  attachViewPrefsStore,
  canMoveColumn,
  columnHidden,
  columnOrder,
  columnWidth,
  columnsCustomised,
  folderIsRemembered,
  folderKey,
  folderMemoryEnabled,
  folderPrefs,
  forgetAllFolders,
  forgetFolder,
  freezeWidths,
  moveColumn,
  moveColumnBy,
  rememberFolder,
  rememberedCount,
  resetColumns,
  setColumnHidden,
  setColumnWidth,
  setFolderMemoryEnabled,
  tableLayout,
  touchFolder,
  viewPrefsSlot,
  widthsAreAuto,
} from './lib/viewPrefs';
export type {
  ColumnId,
  ColumnSpec,
  FolderPrefs,
  TableLayout,
  ViewPrefsSlot,
  ViewPrefsTransport,
} from './lib/viewPrefs';

/* gruplama — the date ladder every listing view draws its headings from.
 *
 * Exported so the gate in `web/tests/lib/dateGroups.test.ts` measures the real
 * rungs rather than a copy of them, and so a host that mounts ListView /
 * GridView / GalleryView itself can label a listing of its own with the same
 * words the explorer uses. */
export { dateBucketFor, groupByDate, groupingActive } from './lib/dateGroups';
export type {
  DateBucket,
  DateGroupLabels,
  DateGrouping,
  DateRun,
} from './lib/dateGroups';

/* uiProfile — the two profiles, and the rule for everything that is not one of
 * them. Exported because the web component resolves the `ui-profile` ATTRIBUTE
 * with it: a string off the DOM needs the same answer the `config` object gets,
 * and two copies of that answer is how the element and the SFC come to disagree
 * about what an unknown value means. */
export {
  DEFAULT_UI_PROFILE,
  UI_PROFILES,
  __resetUiProfileWarnings,
  resolveUiProfile,
} from './lib/uiProfile';
export type { UiProfile } from './lib/uiProfile';
