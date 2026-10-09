import axios from 'axios';

import { api } from './client';
import type { FileTypeHandler } from './fileTypes';
import type { PluginLogLine } from './plugins';

// App plugins — WebAssembly modules that add rows to the file menu and run as
// ops jobs (/api/admin/app-plugins, backend/internal/wasmplugin,
// docs/APP-PLUGINS-API.md). Admin UI name: "Apps". Not storage plugins —
// those are ./plugins.ts.
//
// The wire shapes (Text, Applies, Action, Manifest…) are the core package's
// mirror of backend/pkg/pluginkit/wire/wire.go; they are re-exported here so
// the admin views import one name.
//
// ⚠⚠ A LOCALISED FIELD IS `PluginText`, NEVER `string`. The server sends every
// `wire.Text` — a label, a description, a permission's reason — as an object
// of languages (`{"en": "…", "tr": "…"}`) and resolves nothing, except where
// its own Go type says `string` (PermissionRow.Label, which it renders in the
// caller's language). Typed `string`, such a field compiles, reaches a
// template untouched and prints as raw JSON: that is how the install review
// came to show `{"en": …, "tr": …}` beside every permission (2026-09-21), and
// how a picture of it nearly put Turkish into the English README. Read one
// through `pluginLabelOf(text, locale)`, which also accepts a plain string.
//
// ⚠ The tests read the SERVER's own bytes for these answers
// (backend/internal/api/handlers/testdata/wire/*.json, written and checked by
// app_plugins_wire_test.go) — not a fixture typed from this file, which could
// only ever agree with it.

export type {
  PluginText,
  PluginApplies,
  PluginActionRow,
  PluginViewRow,
} from '@brftech/filex-core';
import type { PluginText, PluginApplies, PluginField } from '@brftech/filex-core';

/**
 * A settings/action form input (wire.Field).
 *
 * ⚠⚠ The core package's `PluginField` IS this shape — it is the browser's
 * mirror of `backend/pkg/pluginkit/wire.Field` — so this is an alias and not
 * a second declaration. The copy that used to live here had drifted: it was
 * missing `multi` and `style`, and it carried an `advanced` that v3 deleted
 * from the contract (`wire.Field` has no `Advanced`; the server drops it at
 * parse). A field a plugin declares must mean ONE thing, so there is one
 * type for it and one mapper (`storageFieldOf`) that draws it.
 */
export type AppPluginField = PluginField;

/** wire.Action as it appears in a manifest. */
export interface AppPluginManifestAction {
  id: string;
  label: PluginText;
  icon?: string;
  applies: PluginApplies;
  view?: string;
  confirm?: PluginText | null;
  min_role?: string;
  danger?: boolean;
  output?: { mode: string; name?: string };
  /**
   * The app's own machinery (the signer's `apply`, a scheduled `expire`):
   * never in a menu, and not an administrator's to switch — the server
   * neither lists nor stores an override for it (wasmplugin
   * hiddenIgnoresOverride).
   */
  hidden?: boolean;
}

export interface AppPluginManifestView {
  id: string;
  placement: 'modal' | 'inspector' | 'home' | string;
  label: PluginText;
  applies?: PluginApplies;
  size?: string;
}

export interface AppPluginManifestPublicPage {
  id: string;
  label: PluginText;
  pin?: string;
  default_ttl_days?: number;
  max_ttl_days?: number;
}

/** filex-app.json (wire.Manifest). */
export interface AppPluginManifest {
  manifest_version: number;
  name: string;
  version: string;
  label: PluginText;
  description?: PluginText;
  icon?: string;
  homepage?: string;
  /**
   * The filex versions the app works with — a range, `">=0.47.0 <0.60.0"`
   * (wasmplugin/compat.go). The older `min_filex` means `>=` that version.
   */
  filex?: string;
  min_filex?: string;
  permissions: string[];
  permission_reasons?: Record<string, PluginText>;
  settings?: AppPluginField[];
  actions?: AppPluginManifestAction[];
  views?: AppPluginManifestView[];
  public_pages?: AppPluginManifestPublicPage[];
  /** Languages for filex ITSELF: `{tag: {filex key: text}}`. */
  ui_locales?: Record<string, Record<string, string>>;
  wasm?: { url?: string; sha256?: string };
  /** The kinds of file the app draws thumbnails of (filex 0.50). */
  thumbnails?: { applies: PluginApplies };
}

export type AppPluginState = 'running' | 'disabled' | 'refused' | 'failed' | 'unlicensed' | string;

/**
 * `app` — a module that runs; `language_pack` — languages for filex itself
 * and nothing else: no module, nothing runs (wasmplugin.KindLanguagePack).
 */
export type AppPluginKind = 'app' | 'language_pack' | string;

/**
 * One language an app adds to filex itself (wasmplugin.LanguageRow), with its
 * coverage of the CURRENT catalogue — the one this binary's interface uses.
 * `total: 0` = the binary carries no catalogue; coverage unknown.
 */
export interface AppPluginLanguage {
  code: string;
  /** Strings the pack carries for it (empty ones not counted). */
  keys: number;
  /** …of which the current catalogue has. */
  translated: number;
  /** …of which it does not (typos, strings filex no longer has). */
  unknown: number;
  total: number;
  /** floor(100 × translated / total) — 100 only when nothing is missing. */
  percent: number;
  /** Written right to left: the interface is laid out right to left in it. */
  rtl: boolean;
}
export type AppPluginSource = 'upload' | 'url' | 'github' | 'bundle' | string;

/** Whether the runtime can run plugins at all, and which engines the host has. */
export interface AppPluginRuntime {
  enabled: boolean;
  arch_ok: boolean;
  disabled_reason: string;
  requires_signature: boolean;
  engines: Record<string, boolean>;
  /** Engine id → the name a person reads (`imagemagick` → `ImageMagick`,
   *  `office` and its old name `libreoffice` → `ONLYOFFICE`), the server's
   *  one spelling (enginebin.DisplayName). */
  engine_names: Record<string, string>;
  /** The filex apps' ranges are judged against (`0.47.0`, or a dev build's own string). */
  filex_version: string;
  /** False on a development build: ranges are not checked there. */
  compat_enforced: boolean;
  /** The daily update check runs (FILEX_APP_PLUGIN_UPDATE_CHECK). */
  update_check: boolean;
  /** RFC 3339 — when the last update check ran; absent: never. */
  updates_checked_at?: string;
  /**
   * The tab's header in the reader's language, on their clock (handlers
   * runtimeSaid, 0.55): the server picks and words each line; absent keys
   * say nothing.
   */
  said: AppPluginRuntimeSaid;
}

/** The Apps tab's header lines, as the server says them. */
export interface AppPluginRuntimeSaid {
  state?: string;
  arch?: string;
  signature?: string;
  dev_build?: string;
  update_check?: string;
}

/** An app's `filex` range against the running filex (wasmplugin.Compat). */
export interface AppPluginCompat {
  requires: string;
  ok: boolean;
  /** The running filex it was judged against. */
  filex: string;
  /**
   * An install review's sentence for a range that leaves this filex out, in
   * the reader's language (wasmplugin.Compat.Said, 0.55): shown as it came.
   * Absent on a list row and when the range holds.
   */
  message?: string;
}

/**
 * What the last update check found for one app (wasmplugin.UpdateInfo).
 *
 * ⚠⚠ Nothing updates itself (filex 0.48, owner's rule): the check only says
 * what it found, and an administrator's "Review update" installs it.
 *
 * `status`: `current` (nothing newer this filex can run) · `available` (a
 * newer version waits for the administrator) · `needs_approval` (it asks for
 * more: `added`, `adds_module`) · `incompatible` (only newer versions that
 * need a newer filex: `version`, `requires`) · `check_failed` (the source
 * could not be read: `refusal`) · `failed` (only on a row filex 0.47 wrote:
 * its automatic update was tried and undone). `notes` are the source's
 * release notes for `version`: a GitHub release's body, Markdown as text.
 */
export interface AppPluginUpdate {
  checked_at?: string;
  status?: 'current' | 'available' | 'needs_approval' | 'incompatible' | 'failed' | 'check_failed' | string;
  version?: string;
  ref?: string;
  requires?: string;
  added?: string[];
  adds_module?: boolean;
  /** The refusal, in the shape an install answers (read it with `refusalOf`). */
  refusal?: Record<string, unknown>;
  /** The source's notes for `version` (a GitHub release's body): Markdown, as text. */
  notes?: string;
}

/** The version an approval replaced, kept to go back to (wasmplugin.PreviousVersion). */
export interface AppPluginPrevious {
  version: string;
  replaced_at: string;
  /** Said by the server in the reader's language, on their clock (0.55). */
  message?: string;
  /** That version has an interface of its own. */
  ui?: boolean;
}

/** One address outside an interface's package, as the review shows it. */
export interface AppPluginUIExternal {
  url: string;
  as: 'style' | 'font' | 'img' | 'media' | string;
  /** `mirror`: filex serves its own checked copy · `live`: the reader's browser fetches it. */
  mode: 'mirror' | 'live' | string;
  sha256?: string;
  bytes?: number;
  reason?: PluginText;
  path?: string;
}

/** An app's own interface (wasmplugin.UIInfo). */
export interface AppPluginUI {
  sha256: string;
  files: number;
  bytes: number;
  unpacked: number;
  /** Script-policy exceptions it asks for (`ui:eval`, `ui:wasm-eval`). */
  csp?: string[];
  external?: AppPluginUIExternal[];
}

/** How an upgrade's interface files differ (wasmplugin.UIFileDiff). Lists are cut at 200. */
export interface AppPluginUIFileDiff {
  added?: string[];
  removed?: string[];
  changed?: string[];
  added_count: number;
  removed_count: number;
  changed_count: number;
}

/** An upgrade's review (wasmplugin.DryRunUpgrade). */
export interface AppPluginUpgradeReview {
  from: string;
  /** Permissions it adds to the grant — what is being approved. */
  added?: string[];
  removed?: string[];
  adds_module?: boolean;
  module_from?: string;
  module_to?: string;
  ui_from?: string;
  ui_to?: string;
  ui_files?: AppPluginUIFileDiff;
  filex_from?: string;
  filex_to?: string;
  signed_from?: boolean;
  signed_to?: boolean;
  notes?: string;
}

/** `POST /admin/app-plugins/updates/check` — one check, as it went. */
export interface AppPluginUpdateReport {
  checked_at: string;
  checked: number;
  /** Always empty since filex 0.48 (nothing updates itself); kept on the wire. */
  updated: string[];
  available: string[];
  needs_approval: string[];
  failed: string[];
}

/** An engine as a person reads it: the server's name for the id, else the id. */
export function engineName(id: string, names: Record<string, string> | undefined): string {
  return names?.[id] || id;
}

/** One row of `GET /api/admin/app-plugins` (wasmplugin.Status). */
export interface AppPlugin {
  id: number;
  name: string;
  version: string;
  label: PluginText;
  /** wire.Text, like `label` — the manifest's description, absent when it has none. */
  description?: PluginText;
  icon?: string;
  homepage?: string;
  enabled: boolean;
  state: AppPluginState;
  state_error?: string;
  source: AppPluginSource;
  source_url?: string;
  /** A URL install: where its manifest is read (the update check's address). */
  manifest_url?: string;
  sha256?: string;
  signed: boolean;
  permissions: string[];
  /**
   * uyan:s1 — this app is woken once an hour and asks for work of its own
   * (`wasmplugin.PermSchedule`). The panel says so on the row: an app that
   * acts while nobody is watching is a different kind of thing from one that
   * only answers a menu click, and the permission list alone does not say it
   * at a glance.
   */
  scheduled: boolean;
  actions: number;
  views: number;
  public_pages: number;
  kind?: AppPluginKind;
  /** The languages it adds to filex itself, with coverage. Absent when none. */
  languages?: AppPluginLanguage[];
  /**
   * Its `filex` range against this filex; absent when it declares none.
   * `ok: false` on an installed app is a warning — it keeps running.
   */
  compat?: AppPluginCompat;
  /** It has a module (false: an interface-only app or a language pack). */
  engine?: boolean;
  /** Its own interface, when it has one. */
  ui?: AppPluginUI;
  /** The version the last approval replaced, kept to go back to. */
  previous?: AppPluginPrevious;
  /** Where newer versions are looked for; absent = nowhere (an uploaded app). */
  update_source?: 'github' | 'url' | string;
  /** What the last update check found; absent before the first. */
  update?: AppPluginUpdate;
  /**
   * The line the list shows about its updates, in the reader's language
   * (handlers sayStatus, 0.55): a newer version that needs another filex,
   * what an approval adds, an update undone, a source that could not be
   * read, or no source at all. Absent where the status word says it all.
   */
  update_said?: string;
  created_at: string;
  updated_at: string;
}

export interface AppPluginList {
  runtime: AppPluginRuntime;
  plugins: AppPlugin[];
}

/** One action's admin override (`GET/PUT …/{id}/overrides`). `applies: null` = manifest default. */
export interface AppPluginActionOverride {
  id: string;
  enabled: boolean;
  applies: PluginApplies | null;
  admin_only: boolean;
}

/**
 * uyan:s1 — one row of a scheduled app's timetable
 * (`model.AppPluginScheduleItem`), soonest first.
 *
 * ⚠ TWO kinds of row in one list, told apart by `key`:
 *
 *   · `key === ''` is the WAKE-UP itself (the reserved
 *     `model.AppPluginScheduleWakeupKey`; an app's own keys are 1..64 chars,
 *     so nothing an app returns can land on it). `due_at` is the next
 *     wake-up, `error` is what the LAST one decided — the app's own note
 *     plus the host's tally, `"N scheduled, N beyond this window, N refused
 *     (why)"` — `attempts` is how many times it has been woken and
 *     `claimed_by` names the filex process that took it.
 *   · every other row is one piece of WORK the last wake-up asked for:
 *     `action_id` at `due_at`, and `job_id` once it reached the queue.
 *
 * ⚠ `error` on a wake-up row is NOT a failure. It is the decision line, and
 * a healthy hour reads "3 scheduled". Only a `status: 'failed'` row is a
 * failure.
 */
export interface AppPluginScheduleItem {
  plugin_id: number;
  /** `''` = the hourly wake-up; anything else = one piece of work. */
  key: string;
  /** RFC 3339. When this runs, to the second; nothing runs before it. */
  due_at: string;
  /** The action to run — empty on a wake-up row. */
  action_id?: string;
  storage_id?: number;
  /** `due` · `running` · `queued` · `failed` · `skipped`. */
  status: string;
  /** Claims, so a row that keeps coming back says so. */
  attempts: number;
  /** The filex process that took the row (`Registry.InstanceID`). */
  claimed_by?: string;
  claimed_at?: string | null;
  /** The `app_plugin_jobs` row this item became, once queued. */
  job_id?: string;
  /** Why the last attempt queued nothing — or, on a wake-up row, the decision. */
  error?: string;
  created_at: string;
  updated_at: string;
}

/**
 * `GET /api/admin/app-plugins/{id}`, flattened by `AppPluginsApi.get`.
 *
 * `permissions` stays what the list row says it is — the permission IDS —
 * while the answer's own top-level `permissions` (the reviewed rows, with a
 * label and a `{en, tr}` reason each) arrives as `permission_rows`. Both are
 * called `permissions` on the wire, one level apart; see `get`.
 */
export interface AppPluginDetail extends AppPlugin {
  manifest: AppPluginManifest;
  granted: string[];
  /** The review of every permission, as the install wizard shows it. */
  permission_rows?: AppPluginPermissionReview[];
  /** The manifest's setting fields (`wire.Field`), for the settings form. */
  setting_fields?: AppPluginField[];
  overrides: AppPluginActionOverride[];
  /**
   * uyan:s1 — the timetable, soonest first. Absent/empty for every app
   * without the `schedule` permission, and also when the read failed: the
   * server drops this one key rather than the whole page, so the drawer
   * shows the section without rows instead of an error screen.
   */
  schedule?: AppPluginScheduleItem[];
  /** Secret values come back as `"***"`. */
  settings: Record<string, string>;
  /** The last `describe` answer, as the plugin gave it. */
  describe?: unknown;
}

/**
 * One permission of a review (wasmplugin.PermissionRow).
 *
 * ⚠ The two text fields are NOT the same kind: `label` is filex's own words
 * for the permission, already rendered by the server in the caller's language
 * (a Go `string`); `reason` is the APP's words, sent as the manifest wrote
 * them — every language at once (a `wire.Text`), absent when the manifest
 * gives none. Resolve it with `pluginLabelOf(reason, locale)`.
 */
export interface AppPluginPermissionReview {
  id: string;
  label: string;
  reason?: PluginText;
}

/** `POST /api/admin/app-plugins?dry_run=1` (wasmplugin.DryRunAnswer). */
export interface AppPluginDryRun {
  manifest: AppPluginManifest;
  permissions: AppPluginPermissionReview[];
  /** Empty for a language pack — it has no module. */
  wasm_sha256: string;
  wasm_bytes?: number;
  signed?: boolean;
  /**
   * An app of the same name is already installed — said at the REVIEW, where
   * the plan can still change to "upgrade it". Absent on an upgrade's own dry
   * run and when the name is free.
   */
  installed?: { id: number; version: string };
  /** Engines the manifest asks for that this server does not have.
   *  `kind: "office"` (0.50) is the office engine: a document server to
   *  connect, not a program to install - the review says it apart. */
  engines_missing?: { id: string; name: string; kind?: 'office' }[];
  kind?: AppPluginKind;
  /** A language pack's integrity is its manifest's: this is what was verified. */
  manifest_sha256?: string;
  languages?: AppPluginLanguage[];
  /** The manifest's `filex` range against this filex; `ok: false` → it cannot be installed here. */
  compat?: AppPluginCompat;
  /**
   * An upgrade's review: the version it replaces, the permissions it adds to
   * the grant (what is being approved) and drops from it, and whether a
   * language pack now brings a module. Absent on an install.
   */
  upgrade?: AppPluginUpgradeReview;
  /** It has a module. */
  engine?: boolean;
  /** Its own interface: the "Interface" group of the review. */
  ui?: AppPluginUI;
  /**
   * The review's File types group (0.50, an install only): every kind the app
   * would open or draw thumbnails of, who handles it now, and where the app
   * lands when nothing is chosen (assoc.InstallKind).
   */
  file_types?: AppPluginInstallKind[];
}

/** One kind the app would handle (assoc.InstallKind). */
export interface AppPluginInstallKind {
  capability: 'open' | 'thumbnail' | string;
  ext: string;
  mime?: string;
  /** The new app's handler for the kind. */
  handler: FileTypeHandler;
  /** The handlers on for the kind now, in order. */
  current: FileTypeHandler[];
  /** Where the app lands with no choice: first, or after the others. */
  default: AppPluginPlace;
}

/** Where an install puts the new app for one kind. */
export type AppPluginPlace = 'first' | 'last' | 'off';

/** One choice of the File types group, as the install body carries it. */
export interface AppPluginPlacement {
  capability: string;
  ext: string;
  handler: string;
  place: AppPluginPlace;
}

/** An app's thumbnail limits (wasmplugin.ThumbLimits). 0 = the default. */
export interface AppThumbLimits {
  max_input_mb: number;
  timeout_s: number;
  memory_mb: number;
  concurrency: number;
}

/** `GET/PUT …/{id}/thumbnails` (wasmplugin.ThumbLimitsAnswer). */
export interface AppThumbLimitsAnswer {
  /** In force. */
  values: AppThumbLimits;
  /** As the administrator stored them (0 = the default). */
  stored: AppThumbLimits;
  defaults: AppThumbLimits;
  min: AppThumbLimits;
  max: AppThumbLimits;
  /** The kinds the app draws: extensions and media types. */
  ext: string[];
  mime: string[];
}

/** An install's answer: the app, and the File types choices that could not be
 *  written (the app is installed either way). */
export type AppPluginInstalled = AppPlugin & { association_errors?: string[] };

/**
 * One live file lock (`GET /api/admin/app-plugins/locks`). An app froze the
 * file for EVERYONE — owner and administrators included — so the only way
 * back for a person who needs the file now is an administrator lifting it
 * here, which is audited as `app_plugin.unlock`.
 */
export interface AppPluginLock {
  storage_id: number;
  /** That storage's name; absent when the storage is gone (the panel then
   *  names it by id). */
  storage?: string;
  /** Relative to that storage's root; no `<storage>://` prefix. */
  path: string;
  /** The app's manifest name. */
  plugin: string;
  reason?: string;
  /** The reason in every language the app wrote it in (a manifest message). */
  reason_text?: PluginText;
  /** RFC 3339; absent = until the app lifts it. */
  until?: string | null;
  created_by?: number | null;
  created_at: string;
}

/** One line of an app's log - the shape every plugin log has (api/plugins
 *  PluginLogLine, backend internal/pluginlog). */
export type AppPluginLogLine = PluginLogLine;

export interface AppPluginLogs {
  lines: AppPluginLogLine[];
  next: number;
}

/**
 * The install bodies (install and upgrade take the same ones). `update` is an
 * upgrade from the app's OWN source: the server finds and fetches the newer
 * version exactly as the update check does (wasmplugin.FetchUpdate).
 */
export type AppPluginInstallSource =
  | { kind: 'update' }
  | { kind: 'github'; repo: string; ref?: string }
  // ⚠ `wasm` / `url` absent = a language pack, which has no module (the
  // server decides from the manifest and refuses the wrong combination).
  | { kind: 'upload'; wasm?: File | null; manifest: File; signature?: string; ui?: File | null }
  | { kind: 'url'; url?: string; manifest_url: string; sha256?: string };

/** Error codes the install endpoints answer with (400/409). */
export type AppPluginErrorCode =
  | 'permissions_incomplete'
  | 'permissions_changed'
  | 'sha256_mismatch'
  | 'manifest_invalid'
  | 'signature_required'
  | 'signature_invalid'
  | 'name_taken'
  | 'describe_mismatch';

/** A refused install, as the wizard needs it to say what happened. */
export interface AppPluginInstallRefusal {
  code: string;
  missing: string[];
  /**
   * The server's sentence for the refusal, in the reader's language
   * (wasmplugin.InstallRefusal.Said, `server.install.*`): shown as it is.
   * The panel keeps no sentence of its own for an install refusal (0.55).
   */
  message: string;
  /** The server's English detail behind `message`, for a log - never shown. */
  detail: string;
  /** fetch_failed: why (manifest_not_found, module_not_found, unreachable …). */
  reason: string;
  /** fetch_failed: the repository or the URL it was fetching. */
  where: string;
  /** fetch_failed on a repository: the refs it tried. */
  refs: string[];
  /** fetch_failed: the HTTP status that came back, 0 when none did. */
  status: number;
  /** incompatible: the range the app declares. */
  requires: string;
  /** incompatible: the filex it leaves out. */
  filex: string;
}

/** The machine code and the details of a refused install, if any. */
export function appPluginError(err: unknown): AppPluginInstallRefusal | null {
  if (!axios.isAxiosError(err) || !err.response?.data) return null;
  return refusalOf(err.response.data);
}

/**
 * A refusal as the wire carries it (wasmplugin.InstallRefusal) — the body of
 * a refused install AND what an update check stores when it could not read
 * the source or undid an automatic update (`update.refusal`). ONE reader, so
 * the Apps list says an update's failure with the wizard's own sentences.
 */
export function refusalOf(raw: unknown): AppPluginInstallRefusal | null {
  if (!raw || typeof raw !== 'object') return null;
  const data = raw as {
    error?: string;
    message?: string;
    detail?: string;
    missing?: string[];
    reason?: string;
    where?: string;
    refs?: string[];
    status?: number;
    requires?: string;
    filex?: string;
  };
  if (!data.error) return null;
  return {
    code: data.error,
    missing: Array.isArray(data.missing) ? data.missing : [],
    message: data.message ?? '',
    detail: data.detail ?? '',
    reason: data.reason ?? '',
    where: data.where ?? '',
    refs: Array.isArray(data.refs) ? data.refs : [],
    status: typeof data.status === 'number' ? data.status : 0,
    requires: data.requires ?? '',
    filex: data.filex ?? '',
  };
}

/**
 * Build the request body + config for one install source. `associations` are
 * the File types choices (an install only); left out of the body when there
 * are none, so a body without them is exactly what it always was.
 */
function installPayload(
  source: AppPluginInstallSource,
  permissions: string[],
  associations: AppPluginPlacement[] = [],
): { body: FormData | Record<string, unknown> } {
  const extra = associations.length ? { associations } : {};
  if (source.kind === 'upload') {
    const form = new FormData();
    if (source.wasm) form.append('wasm', source.wasm);
    form.append('manifest', source.manifest);
    if (source.ui) form.append('ui', source.ui);
    if (source.signature) form.append('signature', source.signature);
    form.append('grant', JSON.stringify({ permissions }));
    if (associations.length) form.append('associations', JSON.stringify(associations));
    return { body: form };
  }
  if (source.kind === 'update') {
    return { body: { from_source: true, permissions } };
  }
  if (source.kind === 'github') {
    return { body: { github_repo: source.repo, ref: source.ref ?? '', permissions, ...extra } };
  }
  return {
    body: { url: source.url ?? '', manifest_url: source.manifest_url, sha256: source.sha256 ?? '', permissions, ...extra },
  };
}

/* ⚠⚠ There was a `fieldToStorageField` here — a SECOND mapper from a
 * plugin's `Field` to the shape a form renderer reads, beside
 * `storageFieldOf` in @brftech/filex-core. Two mappers meant one manifest
 * field drew two different controls: a `select` was a row of buttons on a
 * plugin surface and a native dropdown on this screen, a `multi` select
 * silently kept one value here, and an `advanced` field folded itself into
 * a "Gelişmiş ayarlar" block the contract had abolished. Deleted, not
 * fixed: the admin settings screen calls `storageFieldOf` like every other
 * surface does (see components/plugins/AppPluginDetail.vue). */

const BASE = '/admin/app-plugins';

/**
 * How long an install or an upgrade may take: the server COMPILES the module
 * before it answers.
 *
 * ⚠⚠ Not the client's 30 s default. Measured 2026-09-21: the 20 MB signing
 * module compiled for 29 s on a busy machine, the request gave up at 30 s,
 * and the wizard said "timeout of 30000ms exceeded" about an install that
 * was working. (The server now finishes — or undoes — an install whatever
 * the client does; this is the half that lets the person see the answer.)
 */
export const INSTALL_TIMEOUT_MS = 180_000;

/** The list answer, defaults filled — for the list and for "Check now". */
function listOf(data: Partial<AppPluginList>): AppPluginList {
  return {
    runtime: {
      enabled: data.runtime?.enabled ?? false,
      arch_ok: data.runtime?.arch_ok ?? false,
      disabled_reason: data.runtime?.disabled_reason ?? '',
      requires_signature: data.runtime?.requires_signature ?? false,
      engines: data.runtime?.engines ?? {},
      engine_names: data.runtime?.engine_names ?? {},
      filex_version: data.runtime?.filex_version ?? '',
      // Absent = a server that checks no range: say nothing about it.
      compat_enforced: data.runtime?.compat_enforced ?? true,
      update_check: data.runtime?.update_check ?? false,
      updates_checked_at: data.runtime?.updates_checked_at,
      said: data.runtime?.said ?? {},
    },
    plugins: data.plugins ?? [],
  };
}

/**
 * How long "Check now" may take: every app's source is read, and each update
 * that may be applied is installed — a compile each (INSTALL_TIMEOUT_MS).
 */
export const UPDATE_CHECK_TIMEOUT_MS = 600_000;

export const AppPluginsApi = {
  async list(): Promise<AppPluginList> {
    const { data } = await api.get<Partial<AppPluginList>>(BASE);
    return listOf(data);
  },

  /**
   * Ask every app's source for a newer version now; what may be applied is
   * installed. Answers what happened, and the list redrawn. A check already
   * running (the daily one) is waited for, not doubled.
   */
  async checkUpdates(): Promise<AppPluginList & { report: AppPluginUpdateReport }> {
    const { data } = await api.post<Partial<AppPluginList> & { report: AppPluginUpdateReport }>(
      `${BASE}/updates/check`,
      {},
      { timeout: UPDATE_CHECK_TIMEOUT_MS },
    );
    return { ...listOf(data), report: data.report };
  },

  /**
   * Back to the version the last approval replaced (`previous`): no new
   * approval — that version's grant was approved when it was installed.
   */
  async rollback(id: number): Promise<AppPlugin> {
    const { data } = await api.post<AppPlugin>(`${BASE}/${id}/rollback`, {}, { timeout: INSTALL_TIMEOUT_MS });
    return data;
  },

  /** `?dry_run=1`: the manifest, the permission review and the wasm hash — nothing installed. */
  async dryRun(source: AppPluginInstallSource): Promise<AppPluginDryRun> {
    const { body } = installPayload(source, []);
    const { data } = await api.post<AppPluginDryRun>(BASE, body, { params: { dry_run: 1 } });
    return data;
  },

  /**
   * Install. `permissions` must be exactly the manifest's list; `associations`
   * are the review's File types choices (none: the default order).
   */
  async install(
    source: AppPluginInstallSource,
    permissions: string[],
    associations: AppPluginPlacement[] = [],
  ): Promise<AppPluginInstalled> {
    const { body } = installPayload(source, permissions, associations);
    const { data } = await api.post<AppPluginInstalled>(BASE, body, { timeout: INSTALL_TIMEOUT_MS });
    return data;
  },

  /**
   * ⚠⚠ The answer is an ENVELOPE, not the flat row this type describes: the
   * app's own fields arrive under `plugin` (`{ plugin, manifest, granted,
   * permissions, settings, setting_fields, overrides, schedule }`), while
   * everything else is top level. Handed back as-is, `detail.name`,
   * `.version`, `.source`, `.sha256`, `.created_at`, `.updated_at` and
   * `.signed` are all `undefined` — and `AppPluginDetail extends AppPlugin`,
   * so TypeScript is perfectly happy and nothing anywhere says a word.
   *
   * Measured 2026-09-20 in a browser against a real instance: the drawer's
   * facts list drew Name/Version/Source blank, the dates as `—`, and
   * "Unsigned" for an app that IS signed — a screen that does not merely omit
   * a fact but states the opposite of it.
   *
   * Flattened here, once, where the wire shape is already known. ⚠ `plugin`
   * first so the top-level keys keep winning exactly as they did before.
   * Tolerant of a flat answer too, so a server that ever stops nesting keeps
   * working.
   *
   * ⚠⚠ The two levels overlap on `permissions`, and they are different
   * things: under `plugin` it is the list of permission IDS (what the type
   * says), at the top it is the reviewed ROWS — objects carrying a `{en, tr}`
   * reason. Spread naively, the rows won and `detail.permissions` held
   * objects while TypeScript promised strings; the drawer's badges fall back
   * to it when `granted` is absent, and a badge of an object prints as JSON.
   * So the rows move to `permission_rows` and `permissions` keeps the ids.
   */
  async get(id: number): Promise<AppPluginDetail> {
    const { data } = await api.get<
      Omit<AppPluginDetail, 'permissions'> & { plugin?: AppPlugin; permissions?: unknown[] }
    >(`${BASE}/${id}`);
    const { plugin, permissions: top, ...rest } = data;
    const rows = (top ?? []).filter(
      (r): r is AppPluginPermissionReview => typeof r === 'object' && r !== null,
    );
    const ids = plugin?.permissions ?? (top ?? []).filter((r): r is string => typeof r === 'string');
    return {
      ...(plugin ?? ({} as AppPlugin)),
      ...rest,
      permissions: ids,
      ...(rows.length ? { permission_rows: rows } : {}),
    } as AppPluginDetail;
  },

  async setEnabled(id: number, enabled: boolean): Promise<AppPlugin> {
    const { data } = await api.patch<AppPlugin>(`${BASE}/${id}`, { enabled });
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`${BASE}/${id}`);
  },

  /**
   * Same bodies as install; a manifest that asks for new permissions answers
   * 409 `permissions_changed`. `associations` are the review's File types
   * choices, for the kinds the new version ADDS (the server refuses one for a
   * kind the app already handled: its order stays as it is).
   */
  async upgrade(
    id: number,
    source: AppPluginInstallSource,
    permissions: string[],
    associations: AppPluginPlacement[] = [],
  ): Promise<AppPluginInstalled> {
    const { body } = installPayload(source, permissions, associations);
    const { data } = await api.post<AppPluginInstalled>(`${BASE}/${id}/upgrade`, body, { timeout: INSTALL_TIMEOUT_MS });
    return data;
  },

  /** Dry-run of an upgrade: the new manifest and its permission review. */
  async upgradeDryRun(id: number, source: AppPluginInstallSource): Promise<AppPluginDryRun> {
    const { body } = installPayload(source, []);
    const { data } = await api.post<AppPluginDryRun>(`${BASE}/${id}/upgrade`, body, { params: { dry_run: 1 } });
    return data;
  },

  async getSettings(id: number): Promise<Record<string, string>> {
    const { data } = await api.get<{ values?: Record<string, string> }>(`${BASE}/${id}/settings`);
    return data.values ?? {};
  },

  /** A `"***"` value leaves that secret unchanged. */
  async putSettings(id: number, values: Record<string, string>): Promise<void> {
    await api.put(`${BASE}/${id}/settings`, { values });
  },

  async getOverrides(id: number): Promise<AppPluginActionOverride[]> {
    const { data } = await api.get<{ actions?: AppPluginActionOverride[] }>(`${BASE}/${id}/overrides`);
    return data.actions ?? [];
  },

  async putOverrides(id: number, actions: AppPluginActionOverride[]): Promise<void> {
    await api.put(`${BASE}/${id}/overrides`, { actions });
  },

  /**
   * Every live lock, or only one storage's. ⚠ Not scoped to a plugin: the
   * server answers instance-wide, so a caller that wants one app's locks
   * filters on `plugin` itself (the app detail drawer does).
   */
  async locks(storageId?: number): Promise<AppPluginLock[]> {
    const { data } = await api.get<{ locks?: AppPluginLock[] }>(`${BASE}/locks`, {
      params: storageId ? { storage_id: storageId } : undefined,
    });
    return data.locks ?? [];
  },

  /** Lift one lock by force. `404` when nothing is locked there. */
  async unlock(storageId: number, path: string): Promise<void> {
    await api.delete(`${BASE}/locks`, { data: { storage_id: storageId, path } });
  },

  async logs(id: number, after = 0): Promise<AppPluginLogs> {
    const { data } = await api.get<Partial<AppPluginLogs>>(`${BASE}/${id}/logs`, { params: { after } });
    return { lines: data.lines ?? [], next: data.next ?? after };
  },

  /** An app's thumbnail limits: in force, stored, the defaults, the bounds, its kinds. */
  async thumbLimits(id: number): Promise<AppThumbLimitsAnswer> {
    const { data } = await api.get<AppThumbLimitsAnswer>(`${BASE}/${id}/thumbnails`);
    return data;
  },

  /** Store an app's thumbnail limits (0 = the default). 400 `out_of_range`
   *  names the `field`; an API key gets 403 `session_required`. */
  async putThumbLimits(id: number, limits: AppThumbLimits): Promise<AppThumbLimitsAnswer> {
    const { data } = await api.put<AppThumbLimitsAnswer>(`${BASE}/${id}/thumbnails`, limits);
    return data;
  },
};
