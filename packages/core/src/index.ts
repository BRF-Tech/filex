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
/* The filex mark — ONE copy, here, because the public shell draws it and
   `packages/core` cannot import out of `web/` (dup-scan: `brand-mark`). */
export { default as LogoMark } from './components/LogoMark.vue';
export { default as StarButton } from './components/StarButton.vue';
export { default as TagPicker } from './components/TagPicker.vue';
export { default as TagKindIcon } from './components/TagKindIcon.vue';
export { tagItemsOf, tagKey, isTagKind, type TagItem, type TagKind } from './lib/tags';
export {
  coverageByStorage,
  coverageMessage,
  coverageNotice,
  coveragePercent,
  incomplete as coverageIncomplete,
  type CatalogCoverage,
  type CoverageNotice,
  type CoverageReason,
  type StorageInfo,
} from './lib/catalogCoverage';
/* surucu:d1 — which number a storage line prints: the explorer panel's, and
 * the admin top bar's chip, by one rule. */
export {
  hasCeiling,
  needsMeasuredDrives,
  storageLine,
  type MeasuredDrive,
  type PanelDrive,
  type PersonUsage,
  type StorageLine,
} from './lib/storageLine';
// tablo:t3 — the row's ONE action control ("Actions" / "Aksiyon"). DataTable
// draws it for every table (`rowActions`); it stays exported for a host that
// needs the same control outside a table. A second menu built beside
// ContextMenu drifts from it the first time an action is added (lesson #67).
// ⚠ `useTableScroll`, `PIN_LEAD_ROOM` and `useScrolledX` are gone: they were
// the plumbing of the second table (ui/Table.vue) that imitated this one, and
// DataTable owns the scroll and the frozen edges itself.
export { default as RowActions } from './components/RowActions.vue';
export type { ContextAction } from './components/ContextMenu.vue';
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

/* App plugins (docs/APP-PLUGINS-API.md) — the menu rule, the label fallback
 * and the shared actions cache, so a host that draws its own menu offers the
 * same rows the explorer does. */
export { usePluginActions, invalidatePluginActions, PLUGIN_ACTIONS_TTL_MS } from './composables/usePluginActions';
export type { PluginActionsStore } from './composables/usePluginActions';
export { appliesMatches, appliesToNodes, appliesItemOf } from './lib/pluginApplies';
export { labelOf as pluginLabelOf, labelIn as pluginLabelIn, appTextOr as pluginTextOr } from './lib/pluginLabel';
/* The public page's three card widths, and which kind gets which. */
export { publicLayoutFor } from './lib/publicLayout';
export type { PublicLayout } from './lib/publicLayout';
export { pluginMenuRows, pluginActionsFor, pluginActionKey, isPluginActionKey } from './lib/pluginMenu';
export { normalizeOp } from './composables/usePendingOps';
export type { PendingOpType, PendingOpOutput } from './composables/usePendingOps';
/* M2 — the surface conversation (modal + inspector share it) and the value seeding. */
export { usePluginSurface, SURFACE_CHANGE_DEBOUNCE_MS } from './composables/usePluginSurface';
export type { PluginSurfaceStore, PluginSurfaceHost, PluginSurfaceEvents } from './composables/usePluginSurface';
export { initialValues as surfaceInitialValues, storageFieldOf, pinLength, looksLikeEmail } from './lib/surfaceValues';
/* v3 §2 — the two rules the RENDERER keeps so no plugin can break them:
 * a field that depends on another field, and a step with one way forward. */
export {
  conditionMet,
  fieldRequired,
  formFields,
  hasAnswer,
  hiddenKeys,
  missingRequired as surfaceMissingRequired,
  stripHiddenValues,
  visibleFields,
} from './lib/surfaceConditions';
export { hasSteps, isBackAction, stepFooter } from './lib/surfaceSteps';
/* v3 §3.0 — "go to this file, and start that screen on it". */
export { isOpenRequest, openHashFor, openTargetFor } from './lib/surfaceOpen';
export type { SurfaceOpenOptions, SurfaceOpenTarget } from './lib/surfaceOpen';
export type { StepFooter } from './lib/surfaceSteps';
/* v3 §2 — a choice you can read without clicking. The replacement for
 * `<select>` in every surface, and available to a host that draws its own. */
export { default as ChoiceButtons } from './components/ChoiceButtons.vue';
export type { ChoiceOption } from './components/ChoiceButtons.vue';
// M3 — public pages for outside participants + the sign track's components.
export { usePublicPage, publicPageClient, publicPageUrl, PublicPageError } from './composables/usePublicPage';
export type { PublicPageStore, PublicPageClient, PublicPageStatus, PinFailure } from './composables/usePublicPage';
/* v3 §1 — ONE public shell for every link a stranger can follow: a share
 * (`/s/`), a file request (`/d/`) and an app plugin's page (a share that
 * carries one). A host binds the address and mounts `PublicLinkPage`; it does
 * not own a public surface of its own. */
export {
  usePublicLink,
  usePublicShare,
  usePublicRequest,
  publicLinkClient,
  shareRoot,
  requestRoot,
  pageRoot,
  shareDownloadUrl,
  shareNoJsUrl,
  PublicLinkError,
} from './composables/usePublicLink';
export type {
  PublicLinkOptions,
  PublicLinkStore,
  PublicLinkClient,
  PublicRoot,
  PublicStatus,
  PublicUpload,
} from './composables/usePublicLink';
export { usePublicBranding, normalizeAccent, shade, inkOn, accentStyleOf, DEFAULT_BRAND_NAME } from './composables/usePublicBranding';
export { default as PublicLinkPage } from './components/public/PublicLinkPage.vue';
export { default as PublicShell } from './components/public/PublicShell.vue';
export { default as PublicLinkPreview } from './components/public/PublicLinkPreview.vue';
export { default as PublicPinGate } from './components/public/PublicPinGate.vue';
export { default as PublicShareBody } from './components/public/PublicShareBody.vue';
export { default as PublicRequestBody } from './components/public/PublicRequestBody.vue';
export { default as PublicLanguagePicker } from './components/public/PublicLanguagePicker.vue';
export type {
  PublicBranding,
  PublicEntry,
  PublicFailure,
  PublicLinkBase,
  PublicApp,
  PublicDropLimits,
  PublicLocaleOption,
  PublicNode,
  PublicRequestInfo,
  PublicShareInfo,
  PublicShareKind,
} from './types/Public';
/* v3 §5 — the language list is not a constant: an app plugin may add one. */
export {
  acceptableLocale,
  availableLocales,
  ensureLocaleStrings,
  hasLocale,
  isBuiltinLocale,
  loadLocales,
  localeLabel,
  localeOwnTable,
  localeStrings,
  localeTable,
  localesKnown,
  localesVersion,
  normalizeLocaleCode,
  plausibleLocale,
  registerLocale,
  resetLocales,
  setLocales,
  setLocalesFromBranding,
  unregisterPluginLocales,
  BUILTIN_LOCALES,
} from './lib/uiLocales';
export type { OwnLocaleTable } from './lib/uiLocales';
/* v0.43.0 — CLDR plural categories: a language pack writes as many forms as
 * its language has (Arabic six, Russian four), in both catalogues. */
export {
  CLDR_ORDER,
  pluralCategories,
  pluralCategory,
  pluralChoiceIndex,
} from './lib/plural';
export type { PluralCategory } from './lib/plural';
/* v3 §4-of-the-brief — theme, palette, density and language live on the
 * ACCOUNT, not in one browser's localStorage. */
export {
  configurePrefs,
  currentPrefs,
  flushPrefs,
  hydratePrefs,
  localPref,
  localPrefs,
  onPrefs,
  onPrefsSettled,
  prefsConfigured,
  prefsHydrated,
  prefsSettled,
  resetPrefs,
  savePref,
  setLocalPref,
  PREFS_PUT_DEBOUNCE_MS,
  PREF_KEYS,
  LOOK_KEYS,
  PREF_LS_KEYS,
  /* ⚠⚠ "Is anybody signed in?" — the synchronous, first-paint answer that
   * decides whether a PERSON's palette and light/dark mode may be read at all.
   * A host wires it in two places and no more: `sealSessionless()` on a public
   * link before the first paint, `rememberSession()` the moment
   * `/api/auth/me` answers. */
  SESSION_LS_KEY,
  hasSession,
  rememberSession,
  sealSessionless,
  /* ⚠ The session ENDED: drop every per-person mirror this browser holds.
   * An "and" beside `hasSession`, never an "instead of" — it covers only the
   * sign-outs the app is told about. */
  forgetPersonalPrefs,
  registerPersonalMirror,
} from './lib/prefs';
export type { LookKey, PrefKey, PrefsConfig, UiPrefs } from './lib/prefs';
/* The first-use tour is offered to a PERSON once — the account's answer or
 * this browser's, never once per mount. */
export { markTourSeen, offerTourOnce, resetTourState, tourSeen, TOUR_LS_KEY } from './lib/tour';
/* #57 — the order a person put their storages in (the navigation panel and
 * Home draw it). Kept on the account beside the palette (`storageOrder`). */
export {
  defaultStorageOrder,
  dropStorageAt,
  moveStorage,
  orderStorages,
  parseStorageOrder,
  saveStorageOrder,
  storageOrderKey,
  useStorageOrder,
} from './lib/storageOrder';
export type { OrderableStorage, StorageOrderHandle } from './lib/storageOrder';
/* #57 — the one "drag a row to a new place" gesture: the panel's storages and
 * the admin Storages table. */
export { useReorderDrag } from './composables/useReorderDrag';
export type { ReorderDrag, ReorderDragOptions } from './composables/useReorderDrag';
export { default as SurfaceRenderer } from './components/plugin/SurfaceRenderer.vue';
/* v2 — the surface BODY and the footer row, shared by every frame that draws
 * one (the explorer's dialog, an inspector section, an app's full page, the
 * public card). A host drawing its own frame takes these two rather than
 * re-deriving what `submit` means. */
export { default as SurfaceConversation } from './components/plugin/SurfaceConversation.vue';
export { default as SurfaceFooterButtons } from './components/plugin/SurfaceFooterButtons.vue';
export { default as PluginPageView } from './components/plugin/PluginPageView.vue';
export { default as SurfaceSignaturePad } from './components/plugin/nodes/SurfaceSignaturePad.vue';
export { default as SurfacePdfFields } from './components/plugin/nodes/SurfacePdfFields.vue';
export { loadPdfjs, defaultPdfWorkerUrl } from './lib/pdfjsLoader';
export { fracToPdf, pdfToFrac, fracToPixel, pixelToFrac, clampFrac, fitPageWidth, normRotation } from './lib/pdfFieldsGeom';
export {
  defineField,
  fillValues,
  isFieldOf,
  isPlaced,
  normalizeFields,
  todayIso,
  unplacedFields,
  withFont,
  PDF_FIELD_TYPES,
} from './lib/pdfFields';
export type { PdfField, PdfFieldType, PdfSigner, PdfFillEntry } from './lib/pdfFields';
export { signatureModes, isSignatureValue, SIGNATURE_UPLOAD_MAX_BYTES } from './lib/signaturePad';
export type { SignatureValue, SignatureMode } from './lib/signaturePad';
/* v2 — the five faces a signature may be written in, and what a `text` field
 * on a PDF accepts. Both travel back to the plugin with the value, so a host
 * that renders a stored signature reads them from here too. */
export { SIGN_FONTS, DEFAULT_SIGN_FONT, signFont, isSignFontKey } from './lib/signFonts';
export type { SignFont, SignFontKey } from './lib/signFonts';
/* ⚠ v3 §3.3 — `isRealDate` and `DATE_MASK` are gone with the `date` text
 * rule. A date is asked for with the `date` FIELD TYPE; a host that was
 * parsing `GG/AA/YYYY` out of a text field is reading a shape this product
 * no longer produces. */
export {
  applyRule,
  normalizeRule,
  ruleError,
  ruleInputMode,
  ruleMaxLength,
  PDF_RULE_KINDS,
} from './lib/pdfFieldRules';
export type { PdfFieldRule, PdfRuleKind, PdfRuleError } from './lib/pdfFieldRules';
/* v2 — an app's hold on a file: the badge's words and the 423 refusal. */
export { lockOf, anyLocked, lockedRefusal, lockWords, lockUntilText, lockReasonText } from './lib/appLock';
/* How a failure is SAID — one table of words for every screen (lib/errorWords). */
export {
  jobFailure,
  looksTechnical,
  networkFailure,
  refusalCode,
  refusalWords,
  requestFailure,
  sayFailure,
  serverWords,
  statusIsTelling,
  statusWords,
} from './lib/errorWords';
export type { JobErrorCode, RequestFailure, SaidFailure } from './lib/errorWords';
export { legacyConvertGate, gateOnService } from './lib/serviceGate';
export type { AppLock, LockedRefusal, LockWordsHost } from './lib/appLock';
/* issue #34 — a symlink the server will NOT follow: what it is, why it will
   not open, and the rule that every surface refuses it out loud. */
export { linkStateOf, linkWords, linkWordsFor, isUnopenableLink } from './lib/symlink';
export type { LinkState, LinkWords, LinkWordsHost } from './lib/symlink';
/* v2 — where a `page` view lives and how a host opens it. */
export { pluginPagePath, pluginPageUrl, isPagePlacement, PLUGIN_PAGE_SEGMENT } from './lib/pluginPage';
export type { PluginPageTarget } from './lib/pluginPage';
export type {
  PluginText,
  PluginApplies,
  PluginActionRow,
  PluginGatedRule,
  PluginViewRow,
  PluginActionsResponse,
  PluginSurface,
  SurfaceNode,
  SurfaceAction,
  PluginRunResult,
  PluginViewEventBody,
  PluginViewEventData,
  SurfaceTone,
  TextNodeProps,
  PluginField,
  FormNodeProps,
  StepState,
  StepsNodeProps,
  ListRowAction,
  ListNodeProps,
  ProgressNodeProps,
  PluginPerson,
  PeoplePickerNodeProps,
  PinInputNodeProps,
  FileChooserNodeProps,
  PreviewNodeProps,
  PluginUsersResponse,
} from './types/Plugins';
export type { NavApp } from './components/SideNav.vue';

/* issue #27 — the one "how far along is this queued op" rule, shared by the
 * explorer's operations center and a host's own tray (the admin app's). */
export { opPercent } from './lib/opProgress';
export type { OpProgressLike } from './lib/opProgress';

/* #48 — how an archive format is written (ZIP, TAR.GZ, 7z), for the explorer's
 * create dialog and a host's own archive settings alike. */
export { archiveFormatLabel } from './lib/archiveFormats';

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
/* #47 — several accounts in one ⌘K (the desktop rail), and the one place a
   search hit becomes an address. */
export type { AccountSearchHook, SearchAccount, SearchHitItem } from './types/ExplorerConfig';
export { groupHitsByAccount, hitItem, hitStorageName, hitToNode } from './lib/searchHit';
export type { HitDriveContext, HitGroup } from './lib/searchHit';
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
export { useLocale, localeTag, formatByteSize, formatInstant, formatWhen, formatWhenFull, translate } from './composables/useLocale';
export type { WhenInput } from './composables/useLocale';
/* The Trash's "time left" sentence — the explorer's Trash view and the admin Trash page. */
export { trashTimeLeft } from './lib/trashTimeLeft';
/* A person, named one way on every surface (lib/personName; server twin model.PersonLabel). */
export { personName, personInitial } from './lib/personName';
export type { PersonLike } from './lib/personName';
/* RTL — the direction of a language (the server's `rtl` flag, never a second
 * list) and the page-level rule that derives `<html dir>` from `<html lang>`. */
export {
  clampAlongInline,
  dirOfElement,
  inlineEndX,
  inlineKeyStep,
  inlineSign,
  inlineStartX,
  foreignText,
  isolateLtrRuns,
  localeDir,
  openAlongInline,
  syncDocumentDir,
} from './lib/direction';
/* ONE notice for a connection that is down, however many requests fall over
   while it is (lib/connection): a background call is folded into the shared
   state, an action the person is waiting on still reports itself, and the
   notice clears the moment anything gets an answer. */
export {
  connectionDown,
  connectionDownSince,
  connectionFolded,
  kindOfMethod,
  noteRequestFailed,
  noteRequestSucceeded,
  resetConnectionNotice,
} from './lib/connection';
export type { FailureVerdict, RequestKind } from './lib/connection';
export { default as ConnectionNotice } from './components/ConnectionNotice.vue';
/* `filex 0.43.0` — which filex this is, in ONE spelling for every place that
   says it (the account menus, user settings, the sign-in page). No catalogue
   key: a name and a number need no translation (lib/productVersion). */
export { PRODUCT_NAME, parseServerVersion, productVersionLine, shortCommit } from './lib/productVersion';
export type { ServerVersion } from './lib/productVersion';
export { default as ProductVersion } from './components/ProductVersion.vue';
/* The one splitter for list fields — "،" "，" "、" are commas too. */
export { splitList, isListSeparatorKey } from './lib/listInput';
export type { SplitListOptions } from './lib/listInput';
export type { TextDirection } from './lib/direction';
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
  /* tema:v1 — operator-defined themes. The host fetches /api/appearance once
   * at boot and publishes the result through `setCustomThemes`; everything
   * else — the gallery, the palette cards, and the id resolution that makes a
   * deleted theme fall back to stock — reads it from here. */
  CUSTOM_THEME_PREFIX,
  setCustomThemes,
  useCustomThemes,
  allThemes,
  themeName,
  setTheme,
  /* v3 — paint the palette the ACCOUNT holds without writing it back. */
  applyStoredPalette,
  /* tema:v1 — paint the INSTANCE's default without recording it as a choice. */
  applyInstanceDefault,
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
  /* ⚠⚠ The session answer changed after the first paint (a public link, or
   * an `/api/auth/me` that says nobody) — re-decide whose look this window
   * wears. See `lib/prefs` → SESSION_LS_KEY. */
  resolveSessionLook,
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
export { default as StorageTags } from './components/StorageTags.vue';
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

/* tablo:t1 — per-folder view memory, the defaults beneath it, and the
 * explorer's table configuration.
 *
 * Exported because the HOST owns things this module cannot reach: the
 * settings controls for the person's default folder view
 * (`personFolderDefault` / `setPersonFolderDefault`) and the instance's
 * (`readInstanceFolderDefault`, written by the admin settings page), and the
 * transport that reads and writes the document (`attachViewPrefsHttp` — the
 * explorer attaches it, and so does the admin app on sign-in, because its
 * tables keep their columns in the same document). The rest is exported so
 * the gates in `web/tests/lib/viewPrefs.test.ts` drive the real module rather
 * than a copy.
 *
 * ⚠ v0.43: the "global default" (`globalViewPrefs` / `setGlobalViewPrefs`) is
 * gone — it was written by every click and was the leak the owner reported
 * ("tüm klasörlerde görünüm değişikliği geçerli oluyor"). A change belongs to
 * the folder it was made in; the default is set on purpose. */
export {
  COLUMNS,
  EXPLORER_METRICS,
  FOLDER_CAP,
  NAME_AUTO,
  NAME_MIN,
  ROOT_FOLDER_KEY,
  __flushViewPrefs,
  __resetViewPrefs,
  applyToAllFolders,
  attachViewPrefsStore,
  canMoveColumn,
  columnDropBand,
  columnHidden,
  columnOrder,
  columnWidth,
  columnsCustomised,
  defaultFolderView,
  detachViewPrefsStore,
  folderColumnStore,
  folderIsRemembered,
  folderKey,
  folderMemoryEnabled,
  folderPrefs,
  forgetAllFolders,
  forgetFolder,
  freezeWidths,
  instanceFolderDefault,
  onViewPrefsApplied,
  personFolderDefault,
  readInstanceFolderDefault,
  refreshViewPrefs,
  moveColumn,
  moveColumnBy,
  rememberFolder,
  rememberedCount,
  reorderColumns,
  resetColumns,
  resolveFolderView,
  setColumnHidden,
  setColumnWidth,
  setFolderMemoryEnabled,
  setInstanceFolderDefault,
  setPersonFolderDefault,
  tableLayout,
  touchFolder,
  viewPrefsAttached,
  viewPrefsReady,
  viewPrefsSlot,
  widthsAreAuto,
} from './lib/viewPrefs';
export type {
  ColumnId,
  ColumnSpec,
  FolderPrefs,
  FolderViewDefault,
  InstanceFolderDefault,
  TableLayout,
  ViewPrefsSlot,
  ViewPrefsTransport,
} from './lib/viewPrefs';
export { attachViewPrefsHttp, viewPrefsHttpTransport } from './lib/viewPrefsHttp';
export type { ViewPrefsHttpOptions } from './lib/viewPrefsHttp';

/* ═══ THE ONE TABLE ═══════════════════════════════════════════════════════
 * `DataTable` is the explorer's list view with the files taken out of it, and
 * it is the ONLY table in the product: the admin panel, the connection
 * panels, My shares, the notifications list and an app's `list` node all
 * render through it. A raw `<table>` or a second table component fails
 * `web/tests/ui/tablePinnedActions.test.ts`; `docs/CONTRIBUTING.md` → "UI
 * rules" says why (owner, 2026-09-21: "bir yere tablo gerekiyorsa bu tabloyu
 * koymak zorundayız"). */
export { default as DataTable } from './components/DataTable.vue';
export type { DataColumn } from './components/DataTable.vue';
export {
  columnDropBand as tableColumnDropBand,
  createColumnStore,
  emptyCols,
  memoryBacking,
  readCols,
} from './lib/tableColumns';
export type {
  ColsState,
  ColumnBacking,
  ColumnStore,
  LayoutMetrics,
  TableColumnSpec,
  TableLayout as GenericTableLayout,
} from './lib/tableColumns';
export { setTableSort, tableColumnBacking, tableSort } from './lib/tablePrefs';
export { TABLE_ENV, provideTableEnv, useTableEnv } from './lib/tableEnv';
export type { TableEnv } from './lib/tableEnv';

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
