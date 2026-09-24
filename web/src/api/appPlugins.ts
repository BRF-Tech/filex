import axios from 'axios';

import { api } from './client';

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
}

export type AppPluginState = 'running' | 'disabled' | 'refused' | 'failed' | string;

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
  /** Engine id → the name a person reads (`libreoffice` → `LibreOffice`),
   *  the server's one spelling (enginebin.DisplayName). */
  engine_names: Record<string, string>;
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
  /** Engines the manifest asks for that this server does not have. */
  engines_missing?: { id: string; name: string }[];
  kind?: AppPluginKind;
  /** A language pack's integrity is its manifest's: this is what was verified. */
  manifest_sha256?: string;
  languages?: AppPluginLanguage[];
}

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

export interface AppPluginLogLine {
  seq: number;
  ts: string;
  level: string;
  msg: string;
}

export interface AppPluginLogs {
  lines: AppPluginLogLine[];
  next: number;
}

/** The three install bodies (install and upgrade take the same ones). */
export type AppPluginInstallSource =
  | { kind: 'github'; repo: string; ref?: string }
  // ⚠ `wasm` / `url` absent = a language pack, which has no module (the
  // server decides from the manifest and refuses the wrong combination).
  | { kind: 'upload'; wasm?: File | null; manifest: File; signature?: string }
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
  /** The server's own sentence — English, for the log; never shown as is. */
  message: string;
  /** fetch_failed: why (manifest_not_found, module_not_found, unreachable …). */
  reason: string;
  /** fetch_failed: the repository or the URL it was fetching. */
  where: string;
  /** fetch_failed on a repository: the refs it tried. */
  refs: string[];
  /** fetch_failed: the HTTP status that came back, 0 when none did. */
  status: number;
}

/** The machine code and the details of a refused install, if any. */
export function appPluginError(err: unknown): AppPluginInstallRefusal | null {
  if (!axios.isAxiosError(err) || !err.response?.data) return null;
  const data = err.response.data as {
    error?: string;
    message?: string;
    missing?: string[];
    reason?: string;
    where?: string;
    refs?: string[];
    status?: number;
  };
  if (!data.error) return null;
  return {
    code: data.error,
    missing: Array.isArray(data.missing) ? data.missing : [],
    message: data.message ?? '',
    reason: data.reason ?? '',
    where: data.where ?? '',
    refs: Array.isArray(data.refs) ? data.refs : [],
    status: typeof data.status === 'number' ? data.status : 0,
  };
}

/** Build the request body + config for one install source. */
function installPayload(
  source: AppPluginInstallSource,
  permissions: string[],
): { body: FormData | Record<string, unknown> } {
  if (source.kind === 'upload') {
    const form = new FormData();
    if (source.wasm) form.append('wasm', source.wasm);
    form.append('manifest', source.manifest);
    if (source.signature) form.append('signature', source.signature);
    form.append('grant', JSON.stringify({ permissions }));
    return { body: form };
  }
  if (source.kind === 'github') {
    return { body: { github_repo: source.repo, ref: source.ref ?? '', permissions } };
  }
  return {
    body: { url: source.url ?? '', manifest_url: source.manifest_url, sha256: source.sha256 ?? '', permissions },
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

export const AppPluginsApi = {
  async list(): Promise<AppPluginList> {
    const { data } = await api.get<Partial<AppPluginList>>(BASE);
    return {
      runtime: {
        enabled: data.runtime?.enabled ?? false,
        arch_ok: data.runtime?.arch_ok ?? false,
        disabled_reason: data.runtime?.disabled_reason ?? '',
        requires_signature: data.runtime?.requires_signature ?? false,
        engines: data.runtime?.engines ?? {},
        engine_names: data.runtime?.engine_names ?? {},
      },
      plugins: data.plugins ?? [],
    };
  },

  /** `?dry_run=1`: the manifest, the permission review and the wasm hash — nothing installed. */
  async dryRun(source: AppPluginInstallSource): Promise<AppPluginDryRun> {
    const { body } = installPayload(source, []);
    const { data } = await api.post<AppPluginDryRun>(BASE, body, { params: { dry_run: 1 } });
    return data;
  },

  /** Install. `permissions` must be exactly the manifest's list. */
  async install(source: AppPluginInstallSource, permissions: string[]): Promise<AppPlugin> {
    const { body } = installPayload(source, permissions);
    const { data } = await api.post<AppPlugin>(BASE, body, { timeout: INSTALL_TIMEOUT_MS });
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

  /** Same bodies as install; a manifest that asks for new permissions answers 409 `permissions_changed`. */
  async upgrade(id: number, source: AppPluginInstallSource, permissions: string[]): Promise<AppPlugin> {
    const { body } = installPayload(source, permissions);
    const { data } = await api.post<AppPlugin>(`${BASE}/${id}/upgrade`, body, { timeout: INSTALL_TIMEOUT_MS });
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
};
