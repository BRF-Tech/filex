/**
 * useFileApi — backend wrapper for the manager + ancillary endpoints.
 *
 * Two URL strategies, picked at construction time:
 *
 *   1. NEW (RESTful): caller passes `apiBase: 'https://files.example.com'`.
 *      The manager endpoint becomes `${apiBase}/api/files/manager` and
 *      every action uses `?action=…&path=…` as a query parameter (still
 *      Vuefinder-compatible — the same `?q=…` convention is accepted by
 *      the new Go backend, but `action` is the canonical name in v0.1+).
 *
 *   2. LEGACY (Vuefinder-compat): caller passes `endpoint:
 *      '/api/files/manager'` (and the rest of the per-route fields).
 *      Used by `@brftech/file-explorer` 0.1.0 embedders that have a
 *      Laravel/Filament backend already.
 *
 * Per-route fields (`uploadInit`, `shareCreate`, …) ALWAYS win over
 * the auto-derived `apiBase` URL — lets the caller mix and match.
 *
 * Auth normalisation is centralised here. The component code never
 * thinks about CSRF vs Bearer vs Basic — it just calls `index(path)`.
 */

import type { StorageInfo } from '../lib/catalogCoverage';
import type { MeasuredDrive } from '../lib/storageLine';
import type { ExplorerConfig, AuthConfig, EndpointMap, SearchAccount } from '../types/ExplorerConfig';
import { resolveLocale } from '../locales/resolve';
import { listingAddress } from '../lib/internalPaths';
import { draftsClient, type DraftDto } from '../lib/drafts';
import { localeTag } from './useLocale';
import { networkFailure, requestFailure } from '../lib/errorWords';
// ⚠ The same folding rule the web app's axios layer applies, applied by the
// request layer an EMBED uses — one notice for a connection that is down, and
// no surface-specific copy of the decision (lib/connection).
import { kindOfMethod, noteRequestFailed, noteRequestSucceeded } from '../lib/connection';
import type {
  FileNode,
  ShareInfo,
  UploadLimits,
  Capabilities,
  ArchiveEntry,
  ArchiveCreateFormat,
  TrashEntry,
} from '../types/FileNode';
import type {
  PluginActionsResponse,
  PluginRunResult,
  PluginSurface,
  PluginUsersResponse,
  PluginViewEventBody,
} from '../types/Plugins';

/** Server-side PendingOp DTO (mirror of Modules\FishApp\Models\PendingOp::toApiArray). */
export interface PendingOpDto {
  id: number;
  op_type: 'copy' | 'move' | 'delete' | 'rename' | 'restore' | 'archive-create' | 'archive-extract';
  status: 'pending' | 'running' | 'cancelling' | 'done' | 'error' | 'cancelled';
  progress_total: number;
  progress_done: number;
  target_path: string | null;
  source_dir: string | null;
  source_count: number;
  error_message: string | null;
  cancellable: boolean;
  started_at: string | null;
  finished_at: string | null;
  created_at: string | null;
}

/** Answer to `?action=newfile` — where the new document actually landed. */
export interface NewFileResponse {
  /** Adapter-qualified path, ready to hand to the viewer. */
  path: string;
  /** Made from an app's row (`new_documents`): the view that opens it. Set
   *  by the New document dialog, not by the server. */
  app?: { plugin: string; view: string };
  /** Final basename, which may have gained the extension server-side. */
  name: string;
  /** The TYPE the bytes were made from (a `newdoc_types` key) — not the
   *  extension of `name`, which since #56 may have none (`LICENSE`). The
   *  explorer opens the new file as this type. */
  ext: string;
  size: number;
  mime: string;
  /** Drafts (issue #71): set when the document was made as a DRAFT — then
   *  `path` is the draft's, in the person's drafts area, and nothing is at
   *  the destination yet (lib/drafts). */
  draft?: DraftDto;
}

export interface ManagerResponse {
  adapter: string;
  storages: string[];
  dirname: string;
  read_only: boolean;
  /** RBAC effective level for the current user on this directory ('' when ACL
   *  is not enforced on the storage). Gates the folder-level write actions. */
  perm?: 'none' | 'viewer' | 'editor' | 'owner';
  /* wiring:e2 — E2E-encrypted folder awareness: `e2e` is true when the listed
   * dir IS an encrypted root; `e2e_root` is the adapter-qualified path of the
   * nearest encrypted root covering this dir (set for the root itself AND for
   * every subfolder inside the subtree). Absent on plain folders / old
   * backends — consumers must stay undefined-safe. */
  e2e?: boolean;
  e2e_root?: string;
  files: FileNode[];
  /** `action=search` only: more rows matched than came back — the index
   *  filled its page, or the index-less fallback filled its window — so the
   *  list is not the whole answer. Absent on other actions and on servers
   *  older than the flag. */
  truncated?: boolean;
  /** Every storage the caller can open, with how much of it the catalog
   *  covers (`coverage`, absent when all of it — lib/catalogCoverage). Absent
   *  on servers older than the lazy catalog. */
  storage_info?: StorageInfo[];
}

/** A single ACL grant row (RBAC permissions panel): to one person, or — with
 *  `kind: 'group'` — to a group, every member of which holds it. A group's
 *  grant has its own id space (change it with the *GroupPermission calls). */
export interface Grant {
  id: number;
  storage_id: number;
  path_prefix: string;
  /** "user" or "group"; absent from servers older than groups (a person). */
  kind?: 'user' | 'group';
  user_id?: number;
  group_id?: number;
  group_name?: string;
  level: 'viewer' | 'editor' | 'owner';
  user_email?: string;
  user_display_name?: string;
  /** The person as every screen names them (server model.PersonLabel). */
  user_name?: string;
  inherited?: boolean;
}

/** surucu:d1 — `GET /api/files/quota/me` (quota.Snapshot). */
export interface QuotaSnapshot {
  used_bytes: number;
  quota_bytes: number;
  percent_used: number;
  unlimited: boolean;
}

export interface PermissionsResponse {
  path: string;
  storage_rbac: boolean;
  direct: Grant[];
  inherited: Grant[];
  effective: string;
}

export interface ResolveEmailResponse {
  found: boolean;
  user?: { id: number; email: string; display_name: string; username?: string; role: string };
}

export interface UserSuggestion {
  id: number;
  email: string;
  display_name: string;
  username?: string;
  role: string;
}

/* === koru:k1 — version history (inspector panel) === */

/** Mirrors backend `model.NodeVersion` (GET /api/files/versions?node_id=…). */
export interface NodeVersion {
  id: number;
  node_id: number;
  version_n: number;
  storage_key?: string;
  size: number;
  etag?: string;
  created_at: string;
}
export interface UserSearchResponse {
  users: UserSuggestion[];
}

/** A group the caller could share with (GET /api/files/permissions/groups). */
export interface GroupSuggestion {
  id: number;
  name: string;
  description?: string;
}

/* === calisma:d3 — node comments (inspector panel) === */

/** Mirrors backend `model.NodeComment` (GET /api/files/comments?node_id=…). */
export interface NodeComment {
  id: number;
  node_id: number;
  user_id: number;
  body: string;
  created_at: string;
  updated_at?: string;
  /** Joined author display name (email fallback), filled by the backend. */
  author_name?: string;
  /** Whether the CURRENT caller may delete this row (author or admin). */
  can_delete?: boolean;
}
/* === /calisma:d3 === */

export interface InviteResponse {
  mode: 'granted' | 'user_created' | 'shared';
  user_id?: number;
  url?: string;
  temp_password?: string;
  emailed: boolean;
}

/* === bul:s3 — global search (GET /api/files/search) === */

export type GlobalSearchScope = 'name' | 'content' | 'all';

/**
 * One hit from the dedicated files-search endpoint. The backend returns raw
 * node rows (`{results: [...]}`), so `path` is the IN-STORAGE relative path
 * (no `adapter://` prefix) and the storage comes back as a numeric
 * `storage_id`. `snippet`/`matched` are the v0.2 "Bul" contract additions —
 * older backends simply omit them, so every consumer must stay
 * undefined-safe.
 */
export interface GlobalSearchHit {
  id?: number;
  storage_id?: number;
  name?: string;
  path?: string;
  /** `file` | `dir` (backend NodeType). */
  type?: string;
  size?: number;
  mime?: string;
  /** Plain-text content snippet; matches wrapped in «» (never HTML). */
  snippet?: string;
  /** Where the hit matched: name | content | both. */
  matched?: 'name' | 'content' | 'both';
  /** Drive NAME the hit lives on (the server fills it; older ones did not). */
  storage?: string;
  /**
   * #47 — which signed-in account the hit came from. Never on the wire: the
   * explorer stamps it when the host searches several accounts
   * (`ExplorerConfig.accountSearch`). Absent = this mount's own account, with
   * no other account in play.
   */
  account?: SearchAccount;
  [k: string]: unknown;
}

/**
 * Resolve a Vuefinder-compatible endpoint map from the user's config.
 * Either `apiBase` is set (auto-derive everything) or each route is
 * supplied explicitly (legacy). Mixed mode works too — explicit fields
 * trump the derived URL.
 */
export function resolveEndpoints(config: ExplorerConfig): EndpointMap {
  // `apiBase: ''` (empty string) is a *valid* relative-root prefix —
  // it produces URLs like `/api/files/copy`. Treat only `undefined`
  // as "no apiBase, legacy explicit-only mode". Falsy boolean checks
  // would silently drop relative-root callers and leave every derived
  // endpoint null → "endpoint not configured" UI dead-ends.
  const base =
    config.apiBase != null ? config.apiBase.replace(/\/+$/, '') : null;

  function derive(path: string | undefined, autoSegment: string): string | null {
    if (path) return path;
    if (base === null) return null;
    return `${base}${autoSegment}`;
  }

  // Manager URL is mandatory — pick the explicit `endpoint` first, then
  // fall back to `${apiBase}/api/files/manager`.
  const manager =
    config.endpoint ??
    (base !== null ? `${base}/api/files/manager` : null);

  if (!manager) {
    throw new Error(
      "[@brftech/filex-core] config requires either `apiBase` or `endpoint`",
    );
  }

  return {
    manager,
    // Staged (chunked + resumable) uploads — what useUploadChunked speaks on
    // every driver. The {id} routes are derived from this one.
    uploadBegin: derive(config.uploadBegin, '/api/files/upload/begin'),
    uploadInit: derive(config.uploadInit, '/api/files/upload/init'),
    uploadFinalize: derive(config.uploadFinalize, '/api/files/upload/finalize'),
    uploadAbort: derive(config.uploadAbort, '/api/files/upload/abort'),
    shareCreate: derive(config.shareCreate, '/api/files/share'),
    shareList: derive(config.shareList, '/api/files/share'),
    shareDelete: derive(config.shareDelete, '/api/files/share/{uuid}'),
    limits: derive(config.limits, '/api/files/limits'),
    capabilities: derive(config.capabilities, '/api/files/capabilities'),
    me: derive(config.me, '/api/auth/me'),
    archiveList: derive(config.archiveList, '/api/files/archive/list'),
    archiveExtract: derive(config.archiveExtract, '/api/files/archive/extract'),
    archiveCreate: derive(config.archiveCreate, '/api/files/archive/create'),
    archiveAdd: derive(config.archiveAdd, '/api/files/archive/add'),
    copy: derive(config.copy, '/api/files/copy'),
    moveAsync: derive(config.moveAsync, '/api/files/move'),
    deleteAsync: derive(config.deleteAsync, '/api/files/delete'),
    opsList: derive(config.opsList, '/api/files/ops'),
    opsShow: derive(config.opsShow, '/api/files/ops/{id}'),
    onlyOfficeConfig: derive(config.onlyOfficeConfig, '/api/files/onlyoffice/config'),
    saveText: derive(config.saveText, '/api/files/save-text'),
    restore: derive(config.restore, '/api/files/restore'),
    // filex trash: list soft-deleted nodes + restore one by node id.
    trashList: derive(config.trashList, '/api/files/manager/trash'),
    trashRestore: derive(config.trashRestore, '/api/files/manager/restore'),
    /* An operator's "Delete permanently" of one trash entry (`{id}`). */
    trashPurge: derive(config.trashPurge, '/api/admin/trash/{id}'),
    /* An operator's hard delete of one version (`{id}`). */
    versionPurge: derive(config.versionPurge, '/api/admin/versions/{id}'),
    /* wiring:e2 — escrow proof-of-possession, then the owner is told. */
    e2eEscrowChallenge: derive(config.e2eEscrowChallenge, '/api/files/e2e/escrow/challenge'),
    e2eEscrowUsed: derive(config.e2eEscrowUsed, '/api/files/e2e/escrow/used'),
    /* wiring:e2 password — a folder password was changed; its owner is told. */
    e2ePasswordChanged: derive(config.e2ePasswordChanged, '/api/files/e2e/password-changed'),
    /* wiring:e2 convert — after a folder is encrypted in place. */
    e2eCleanup: derive(config.e2eCleanup, '/api/files/e2e/cleanup'),
    /* App plugins — docs/APP-PLUGINS-API.md. */
    pluginActions: derive(config.pluginActions, '/api/files/plugins/actions'),
    pluginActionRun: derive(config.pluginActionRun, '/api/files/plugins/actions/{plugin}/{action}/run'),
    pluginView: derive(config.pluginView, '/api/files/plugins/views/{plugin}/{view}'),
    pluginViewEvent: derive(config.pluginViewEvent, '/api/files/plugins/views/{plugin}/{view}/event'),
    pluginUsers: derive(config.pluginUsers, '/api/files/plugins/users'),
    opsCancel: derive(config.opsCancel, '/api/files/ops/{id}/cancel'),
    /* v4 — an app's own interface (AppFrame): its module, and its saves. */
    pluginUICall: derive(config.pluginUICall, '/api/files/plugins/ui/{plugin}/{view}/call'),
    pluginUISave: derive(config.pluginUISave, '/api/files/plugins/ui/{plugin}/{view}/save'),
  };
}

/**
 * Resolve a possibly-async bearer token to a string. Caller passes
 * `auth.token` here so the auth header is fresh on every request.
 */
async function resolveToken(t: string | (() => string | Promise<string>)): Promise<string> {
  if (typeof t === 'function') {
    const out = t();
    return out instanceof Promise ? await out : out;
  }
  return t;
}

/**
 * Normalise the legacy `{type: 'bearer'}` shape to the modern
 * `{kind: 'bearer'}` discriminator. Lets us write a single auth-header
 * builder downstream.
 */
function normalizeAuth(auth: AuthConfig | undefined): { kind: 'bearer'; token: string | (() => string | Promise<string>) }
  | { kind: 'csrf'; csrf: string }
  | { kind: 'basic'; user: string; pass: string }
  | { kind: 'none' } {
  if (!auth) return { kind: 'none' };
  if ('kind' in auth) return auth;
  // Legacy shape — translate.
  if (auth.type === 'bearer') return { kind: 'bearer', token: auth.token };
  if (auth.type === 'csrf') return { kind: 'csrf', csrf: auth.csrf };
  return { kind: 'none' };
}

/** One chunk of an app interface's save (handlers/app_ui_chunks.go). */
export const UI_SAVE_CHUNK = 8 << 20;

export function useFileApi(config: ExplorerConfig) {
  const endpoints = resolveEndpoints(config);
  const authConf = normalizeAuth(config.auth);

  /**
   * Last bearer value a function-token actually produced.
   *
   * ⚠ Exists for `authHeadersSync` only. A function token is resolved
   * asynchronously (the desktop app fetches it from the main process per
   * call), and a synchronous caller cannot wait for that — before this cache
   * it simply emitted NO Authorization header, so the request went out
   * anonymous and came back 401. Remembering the last value turns "no
   * credential at all" into "the credential we last held", which is the
   * difference between a dead feature and a stale-token retry.
   */
  let lastBearer: string | null = null;

  async function authHeaders(extra: Record<string, string> = {}): Promise<Record<string, string>> {
    const h: Record<string, string> = { Accept: 'application/json', ...extra };
    if (authConf.kind === 'bearer') {
      const token = await resolveToken(authConf.token);
      if (token) {
        h.Authorization = `Bearer ${token}`;
        lastBearer = token;
      }
    } else if (authConf.kind === 'csrf') {
      h['X-CSRF-TOKEN'] = authConf.csrf;
      h['X-Requested-With'] = 'XMLHttpRequest';
    } else if (authConf.kind === 'basic') {
      const creds = btoa(`${authConf.user}:${authConf.pass}`);
      h.Authorization = `Basic ${creds}`;
    }
    return h;
  }

  /**
   * Sync auth-header builder for the few callers that genuinely cannot await
   * (XMLHttpRequest's `setRequestHeader` loop).
   *
   * ⚠ Prefer `authHeaders()`. A function token can only be *resolved*
   * asynchronously, so this returns the last value one produced — which is
   * nothing at all until the first async call has run. Measured on 2026-08-10
   * in the desktop app: the OnlyOffice config POST, the starred list and the
   * recently-opened POST all went out with no Authorization header and came
   * back 401, because every one of them reached the API through this function.
   */
  function authHeadersSync(extra: Record<string, string> = {}): Record<string, string> {
    const h: Record<string, string> = { Accept: 'application/json', ...extra };
    if (authConf.kind === 'bearer') {
      const token = typeof authConf.token === 'string' ? authConf.token : lastBearer;
      if (token) h.Authorization = `Bearer ${token}`;
    } else if (authConf.kind === 'csrf') {
      h['X-CSRF-TOKEN'] = authConf.csrf;
      h['X-Requested-With'] = 'XMLHttpRequest';
    } else if (authConf.kind === 'basic') {
      const creds = btoa(`${authConf.user}:${authConf.pass}`);
      h.Authorization = `Basic ${creds}`;
    }
    return h;
  }

  function credentialsMode(): RequestCredentials {
    return authConf.kind === 'csrf' ? 'include' : 'same-origin';
  }

  /* ⚠⚠ Every refusal this client meets is said by lib/errorWords — ONE table
     of status words and refusal codes for every screen (a read-only drive,
     a missing encryption key, a quota, the statuses). It used to live here
     as an inline table and was copied, differently, into each component that
     called `fetch` itself; those printed "save failed: 500 {…}" and
     "Config fetch 503: {…}". The raw body rides along as `.detail`, for an
     administrator's second line and for `lockedRefusal`, never the sentence. */
  const lang = () => resolveLocale(config.locale);

  async function jsonFetch<T>(url: string, init: RequestInit = {}): Promise<T> {
    const res = await rawRequest(url, init);
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw requestFailure(res.status, text, lang());
    }
    // ⚠⚠ A 204 carries NO BODY, and several endpoints answer with one (every
    // delete does). Parsing it throws "Unexpected end of JSON input" AFTER the
    // server has already done the work, so the caller reports a failure for an
    // operation that succeeded — measured 2026-08-16 in a browser: revoking an
    // S3 access key deleted it on the server and left it on screen with an
    // error under it, which invites the user to trust a credential that is
    // gone. An empty success is a success.
    if (res.status === 204 || res.status === 205) return undefined as T;
    const body = await res.text();
    if (!body) return undefined as T;
    return JSON.parse(body) as T;
  }

  /**
   * One request with this client's credentials and language, answered as the
   * Response itself — for a caller that reads a refusal's body as an ANSWER
   * (a draft saved beside a taken name is a question, not a failure:
   * lib/drafts). Every other caller wants jsonFetch.
   */
  async function rawRequest(url: string, init: RequestInit = {}): Promise<Response> {
    const headers = {
      // ⚠⚠ The language on SCREEN, not the one the browser was installed in.
      // Everything the server writes for a person — a plugin's surface, the
      // labels inside it, the message on a queued job — is rendered with the
      // request's `Accept-Language`. Chrome sends `en-US` whatever filex is
      // set to, so a Turkish window asked a signing wizard for its screens
      // and got "Identity" and an English help line in the middle of Turkish
      // ones. The window's own language is the only right answer here.
      'Accept-Language': localeTag(resolveLocale(config.locale)),
      ...(await authHeaders()),
      ...((init.headers as Record<string, string> | undefined) ?? {}),
    };
    let res: Response;
    try {
      res = await fetch(url, {
        ...init,
        headers,
        credentials: credentialsMode(),
      });
    } catch (e) {
      // An abort is the caller's own decision — handed back untouched, callers
      // branch on `name === 'AbortError'`.
      if ((e as Error)?.name === 'AbortError') throw e;
      // No answer at all: said as a network failure, not as the browser's
      // "TypeError: Failed to fetch".
      // ⚠ Told to the shared notice as well, so an embedded explorer folds a
      // storm of failed listings and thumbnails the same way the panel does.
      noteRequestFailed(kindOfMethod(init.method));
      throw networkFailure(lang(), e);
    }
    // Any answer — a 404 and a 500 included — means the connection is not
    // what is wrong, and the shared notice must not keep saying it is.
    noteRequestSucceeded();
    return res;
  }

  // --------------------------------------------------------------------
  // Drafts (issue #71) — a new document until its first save. Derived from
  // the manager endpoint like permissions/versions, so an embed's proxy that
  // forwards /api/files/* reaches it. The calls are lib/drafts', which the
  // viewer uses too; this only hands it this client's credentials.
  // --------------------------------------------------------------------
  const draftsBase = endpoints.manager.replace(/\/manager(\?.*)?$/, '/drafts');
  const drafts = draftsClient(draftsBase, {
    request: rawRequest,
    get locale() {
      return lang();
    },
  });

  // --------------------------------------------------------------------
  // Permissions (RBAC) — derived from the manager endpoint by swapping the
  // trailing `/manager` for `/permissions`. Owner/admin only (backend gated).
  // --------------------------------------------------------------------
  function permissionsUrl(sub = ''): string {
    const base = endpoints.manager.replace(/\/manager(\?.*)?$/, '/permissions');
    return base + sub;
  }
  async function listPermissions(path: string): Promise<PermissionsResponse> {
    return jsonFetch<PermissionsResponse>(permissionsUrl() + '?path=' + encodeURIComponent(path));
  }
  async function resolveEmail(email: string): Promise<ResolveEmailResponse> {
    return jsonFetch<ResolveEmailResponse>(permissionsUrl('/resolve') + '?email=' + encodeURIComponent(email));
  }
  async function searchUsers(q: string): Promise<UserSearchResponse> {
    return jsonFetch<UserSearchResponse>(permissionsUrl('/users') + '?q=' + encodeURIComponent(q));
  }
  /** Groups matching q; an empty list from a server without groups. */
  async function searchGroups(q: string): Promise<{ groups: GroupSuggestion[] }> {
    try {
      return await jsonFetch<{ groups: GroupSuggestion[] }>(permissionsUrl('/groups') + '?q=' + encodeURIComponent(q));
    } catch {
      return { groups: [] };
    }
  }
  async function updateGroupPermission(id: number, level: string): Promise<unknown> {
    return jsonFetch(permissionsUrl('/groups/' + id), { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ level }) });
  }
  async function deleteGroupPermission(id: number): Promise<unknown> {
    return jsonFetch(permissionsUrl('/groups/' + id), { method: 'DELETE' });
  }
  async function addPermission(body: { path: string; user_id?: number; group_id?: number; level: string; is_dir?: boolean }): Promise<unknown> {
    return jsonFetch(permissionsUrl(), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  }
  async function updatePermission(id: number, level: string): Promise<unknown> {
    return jsonFetch(permissionsUrl('/' + id), { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ level }) });
  }
  async function deletePermission(id: number): Promise<unknown> {
    return jsonFetch(permissionsUrl('/' + id), { method: 'DELETE' });
  }
  async function invitePermission(body: { path: string; email: string; level: string; create_user?: boolean; role?: string; is_dir?: boolean; locale?: string }): Promise<InviteResponse> {
    return jsonFetch<InviteResponse>(permissionsUrl('/invite'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  }
  async function shareMail(body: { path: string; email?: string; emails?: string[]; url: string; pin?: string | null; expires_days?: number; locale?: string; is_dir?: boolean; size?: number; mode?: string }): Promise<{ emailed: boolean; sent?: string[]; failed?: string[] }> {
    return jsonFetch<{ emailed: boolean; sent?: string[]; failed?: string[] }>(permissionsUrl('/share-mail'), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  }

  // --------------------------------------------------------------------
  // Manager contract — supports both `?q=action` (legacy) and
  // `?action=action` (new) by emitting BOTH. Backends recognising one
  // ignore the other; new backends prefer `action`.
  // --------------------------------------------------------------------

  function qs(params: Record<string, string | number | boolean | undefined>): string {
    const sp = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) {
      if (v === undefined || v === null) continue;
      sp.set(k, String(v));
    }
    return sp.toString();
  }

  function managerUrl(action: string, params: Record<string, string | number | boolean | undefined> = {}): string {
    const sep = endpoints.manager.includes('?') ? '&' : '?';
    return `${endpoints.manager}${sep}${qs({ q: action, action, ...params })}`;
  }

  // ⚠⚠ A listing of one of filex's own directories is answered with the
  // storage's root instead (lib/internalPaths → listingAddress). The server
  // refuses the trash/versions/thumbs trees outright, but it has to keep
  // serving `.filex-open` by exact path — every desktop release since 0.29.0
  // reads its working copies that way — so the explorer is the only place
  // that can tell "a person navigated here" from "the desktop is checking
  // its copy". The explorer arrives wherever the response's `dirname` says,
  // so a stale hash, an old tab or a notification from before this release
  // lands in the storage instead of in the machinery (measured 2026-09-21:
  // `#docs/.filex-open` opened a folder of working copies).
  async function index(path: string): Promise<ManagerResponse> {
    return jsonFetch<ManagerResponse>(managerUrl('index', { path: listingAddress(path) }));
  }

  async function search(path: string, filter: string): Promise<ManagerResponse> {
    return jsonFetch<ManagerResponse>(managerUrl('search', { path: listingAddress(path), filter }));
  }

  /* === bul:s3 — global "search everywhere" ===
   * Derived from the manager endpoint by swapping `/manager` for `/search`
   * (same trick permissionsUrl uses), so embedded proxies that forward the
   * whole /api/files/* subtree keep working. Errors and legacy backends
   * degrade to an empty result list — the palette just shows nothing. */
  async function globalSearch(
    query: string,
    opts: { limit?: number; scope?: GlobalSearchScope } = {},
  ): Promise<GlobalSearchHit[]> {
    const base = endpoints.manager.replace(/\/manager(\?.*)?$/, '/search');
    const sep = base.includes('?') ? '&' : '?';
    const url = `${base}${sep}${qs({ q: query, limit: opts.limit, scope: opts.scope })}`;
    const data = await jsonFetch<{ results?: GlobalSearchHit[] | null }>(url);
    return Array.isArray(data?.results) ? data.results : [];
  }

  /* === surucu:d1 — the signed-in person's storage line ==================
   * `GET /api/files/quota/me` → `{used_bytes, quota_bytes, percent_used,
   * unlimited}` (internal/quota/service.go `Snapshot`). Derived from the
   * manager endpoint the same way permissions and search are, so a proxy that
   * forwards /api/files/* keeps working.
   *
   * ⚠ PER-USER, never per-storage: usage is `SUM(nodes.size) WHERE owner_id=me`
   * and there is no per-provider quota. Anything labelling this figure with a
   * drive's name is describing a number the server did not send.
   *
   * ⚠ Returns null rather than throwing on ANY failure — a server without the
   * route (404), an app token with no person behind it (403) or an older build
   * must leave the panel exactly as it was, not put an error where a status
   * line goes.
   */
  async function quotaMe(): Promise<QuotaSnapshot | null> {
    const base = endpoints.manager.replace(/\/manager(\?.*)?$/, '/quota/me');
    try {
      const q = await jsonFetch<QuotaSnapshot>(base);
      return q && typeof q.used_bytes === 'number' ? q : null;
    } catch {
      return null;
    }
  }

  /**
   * `GET /api/files/quota/storages` → `{storages: [{name, used_bytes,
   * file_count, coverage?}]}` — how full each drive the caller can open is
   * (handlers/quota_storages.go, RBAC-filtered server-side). The storage line
   * asks for it when the host handed over no size for some drive — the
   * desktop app hands over names only (lib/storageLine).
   *
   * ⚠ Null on ANY failure, for the reason `quotaMe()` is: a server without the
   * route must leave the panel as it was.
   */
  async function storageUsage(): Promise<MeasuredDrive[] | null> {
    const base = endpoints.manager.replace(/\/manager(\?.*)?$/, '/quota/storages');
    try {
      const body = await jsonFetch<{ storages?: unknown }>(base);
      return Array.isArray(body?.storages) ? (body.storages as MeasuredDrive[]) : null;
    } catch {
      return null;
    }
  }

  async function subfolders(path: string): Promise<{ folders: FileNode[] }> {
    return jsonFetch<{ folders: FileNode[] }>(managerUrl('subfolders', { path }));
  }

  async function newFolder(path: string, name: string): Promise<ManagerResponse> {
    return jsonFetch<ManagerResponse>(managerUrl('newfolder'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, name }),
    });
  }

  /**
   * Create an empty document of a known type in `path`.
   *
   * `type` is an extension from `capabilities.newdoc_types` — the registry the
   * SERVER compiled in, not a list the client keeps. That matters for the
   * office formats: a .docx is a ZIP of XML parts, so "create an empty file"
   * has to be answered by whoever holds the template bytes, and the client
   * cannot manufacture one.
   *
   * `name` may or may not already carry the extension; the server appends it
   * when it is missing. Throws on a name collision (409 NAME_TAKEN) — the
   * dialog warns first, but this is the check.
   *
   * `exactName` (#56): `name` is the WHOLE file name. A text type is then
   * created under exactly it — `LICENSE`, `Makefile`, `test.conf` — and only
   * a type whose editor needs its extension (`ext_required`: office,
   * diagrams) still gains it. A text type may not borrow such a type's
   * extension (`x.docx` as Plain text): 400 `EXT_NEEDS_TYPE`.
   */
  async function newFile(
    path: string,
    name: string,
    type: string,
    opts: { exactName?: boolean } = {},
  ): Promise<NewFileResponse> {
    return jsonFetch<NewFileResponse>(managerUrl('newfile'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(opts.exactName ? { path, name, type, exact_name: true } : { path, name, type }),
    });
  }

  async function rename(path: string, item: string, name: string): Promise<ManagerResponse> {
    return jsonFetch<ManagerResponse>(managerUrl('rename'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, item, name }),
    });
  }

  /**
   * Rename as a job of the operations queue (`queued=1`), for a folder: on an
   * object store that is one request per object, longer than any proxy waits.
   * `op` is the job when the server queued it. An older server ignores
   * `queued` and renames inside the request, and answers the listing instead.
   * A refusal throws, as `rename` does.
   */
  async function renameQueued(path: string, item: string, name: string): Promise<{ op?: PendingOpDto }> {
    return jsonFetch<{ op?: PendingOpDto }>(managerUrl('rename', { queued: 1 }), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, item, name }),
    });
  }

  async function move(path: string, items: string[], target: string): Promise<ManagerResponse> {
    return jsonFetch<ManagerResponse>(managerUrl('move'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, item: target, items: items.map((p) => ({ path: p })) }),
    });
  }

  async function deleteItems(path: string, items: string[]): Promise<ManagerResponse> {
    return jsonFetch<ManagerResponse>(managerUrl('delete'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, items: items.map((p) => ({ path: p })) }),
    });
  }

  /**
   * Which of `permissions` the account holds on each of `items` — the
   * server's per-path answer for actions a role allows or denies only in some
   * folders. One list per item, in the order asked. Changes nothing.
   */
  async function allowedAt(items: string[], permissions: string[]): Promise<string[][]> {
    const out: string[][] = [];
    // The server takes at most 1000 paths per question.
    for (let i = 0; i < items.length; i += 1000) {
      const part = items.slice(i, i + 1000);
      const data = await jsonFetch<{ allowed?: string[][] }>(managerUrl('allowed'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ permissions, items: part.map((p) => ({ path: p })) }),
      });
      part.forEach((_, j) => out.push(data.allowed?.[j] ?? []));
    }
    return out;
  }

  /** Server-side recursive copy (async — returns a PendingOp). */
  async function copy(source: string[], target: string): Promise<{ op: PendingOpDto }> {
    if (!endpoints.copy) throw new Error('copy endpoint not configured');
    return jsonFetch(endpoints.copy, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source, target }),
    });
  }

  /**
   * wiring:e2 names — copy or move ONE item into `target` under `name`, as one
   * step of the queue (the server makes the literal destination; a taken name
   * is refused, never suffixed). An encrypted-names folder needs it: a name is
   * sealed for the folder it is in, so an item crossing folders arrives under
   * a name sealed for its new folder, and nothing can stop between the move
   * and the rename.
   */
  async function transferNamed(
    kind: 'copy' | 'move',
    source: string,
    target: string,
    name: string,
    sourceDir?: string,
  ): Promise<{ op: PendingOpDto }> {
    const url = kind === 'copy' ? endpoints.copy : endpoints.moveAsync;
    if (!url) throw new Error(`${kind} endpoint not configured`);
    return jsonFetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source: [source], target, name, ...(sourceDir ? { sourceDir } : {}) }),
    });
  }
  async function moveAsync(source: string[], target: string, sourceDir?: string): Promise<{ op: PendingOpDto }> {
    if (!endpoints.moveAsync) throw new Error('moveAsync endpoint not configured');
    return jsonFetch(endpoints.moveAsync, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source, target, sourceDir }),
    });
  }

  async function deleteAsync(source: string[], sourceDir?: string): Promise<{ op: PendingOpDto }> {
    if (!endpoints.deleteAsync) throw new Error('deleteAsync endpoint not configured');
    return jsonFetch(endpoints.deleteAsync, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source, sourceDir }),
    });
  }

  async function restore(source: string[]): Promise<{ ok: boolean; restored: number }> {
    if (!endpoints.restore) throw new Error('restore endpoint not configured');
    return jsonFetch(endpoints.restore, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source }),
    });
  }

  /**
   * Restore trash entries by node id as jobs of the operations queue
   * (`queued=1`): one request for the whole selection, one job per storage.
   * Only for a server whose capabilities list `restore` under `queued`; an
   * older one restores one `node_id` per request (restoreIds).
   */
  async function restoreQueued(ids: number[]): Promise<{ ops: PendingOpDto[] }> {
    if (!endpoints.trashRestore) throw new Error('trashRestore endpoint not configured');
    const sep = endpoints.trashRestore.includes('?') ? '&' : '?';
    const res = await jsonFetch<{ ops?: PendingOpDto[] }>(`${endpoints.trashRestore}${sep}queued=1`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ node_ids: ids }),
    });
    return { ops: res.ops ?? [] };
  }

  /** filex trash listing — soft-deleted nodes across (or within) storages. */
  async function listTrash(storageName?: string): Promise<{ entries: TrashEntry[]; total: number }> {
    if (!endpoints.trashList) throw new Error('trashList endpoint not configured');
    const base = endpoints.trashList;
    const sep = base.includes('?') ? '&' : '?';
    const url = storageName ? `${base}${sep}storage=${encodeURIComponent(storageName)}` : base;
    return jsonFetch<{ entries: TrashEntry[]; total: number }>(url);
  }

  /**
   * Restore soft-deleted nodes by their node id. The filex backend restores
   * one node per call (`POST {node_id}`), so we fan out and tally successes.
   *
   * `taken` names the entries the server refused because something already
   * holds their original path (409 `EXISTS`). That refusal is the server
   * protecting the file that holds the name — a restore used to overwrite it —
   * so it is reported by name rather than folded into "0 items restored",
   * which would read as if nothing had been tried.
   *
   * `failed` counts every other item that did not come back, and `failure` is
   * the first of those errors, to be said. ⚠ They used to be skipped without a
   * word: a folder whose restore outran the proxy (every object inside is moved
   * back one by one on an object store) simply did not count, and the explorer
   * reported "2 items restored" over a selection of three.
   */
  async function restoreIds(
    ids: number[],
  ): Promise<{ restored: number; taken: string[]; failed: number; failure?: unknown }> {
    const url = endpoints.trashRestore;
    if (!url) throw new Error('trashRestore endpoint not configured');
    let restored = 0;
    let failed = 0;
    let failure: unknown;
    const taken: string[] = [];
    for (const id of ids) {
      try {
        await jsonFetch(url, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ node_id: id }),
        });
        restored++;
      } catch (err) {
        const e = err as { status?: number; detail?: string };
        if (e.status === 409) {
          try {
            const body = JSON.parse(e.detail ?? '') as { code?: string; name?: string };
            if (body.code === 'EXISTS') {
              taken.push(body.name || String(id));
              continue;
            }
          } catch {
            /* a 409 without the envelope is counted as a plain failure */
          }
        }
        failed++;
        if (failure === undefined) failure = err;
      }
    }
    return failure === undefined ? { restored, taken, failed } : { restored, taken, failed, failure };
  }

  /**
   * Deletes one trash entry for good (`DELETE /api/admin/trash/{id}`) — an
   * operator's "Delete permanently". With `queued` (a server whose
   * capabilities list `purge` under `queued`) it is a job of the operations
   * queue, handed back as `op`: a folder is purged one object and one row at a
   * time, which outlasts a request. A refusal throws, said in the server's
   * words (requestFailure).
   *
   * ⚠ Through jsonFetch like every other request: it was a raw `fetch` in the
   * explorer, and a refusal lost the server's reason there.
   */
  async function purgeTrash(id: number, opts: { queued?: boolean } = {}): Promise<{ op?: PendingOpDto }> {
    const tpl = endpoints.trashPurge;
    if (!tpl) throw new Error('trashPurge endpoint not configured');
    let url = tpl.replace('{id}', encodeURIComponent(String(id)));
    if (opts.queued) url += `${url.includes('?') ? '&' : '?'}queued=1`;
    const res = await jsonFetch<{ op?: PendingOpDto } | undefined>(url, { method: 'DELETE' });
    return res?.op ? { op: res.op } : {};
  }

  /**
   * Deletes one version for good (`DELETE /api/admin/versions/{id}`): the
   * row and the bytes it kept. An operator's action, like purgeTrash; used by
   * "Delete the original for good" when a file is encrypted (useE2eFiles).
   */
  async function purgeVersion(id: number): Promise<void> {
    const tpl = endpoints.versionPurge;
    if (!tpl) throw new Error('versionPurge endpoint not configured');
    await jsonFetch(tpl.replace('{id}', encodeURIComponent(String(id))), { method: 'DELETE' });
  }

  /**
   * Legacy in-band multipart upload (small files / chunked endpoint
   * absent). XMLHttpRequest because fetch doesn't expose upload
   * progress on most browsers.
   */
  async function uploadMultipart(
    path: string,
    files: File[],
    onProgress?: (p: number) => void,
    /** Extra form fields: `expect` (the precondition "<size>:<ms>" or
     *  "none"), `e2e_convert` (an in-place E2E conversion write). */
    fields?: Record<string, string>,
  ): Promise<ManagerResponse> {
    const fd = new FormData();
    fd.append('path', path);
    for (const [k, v] of Object.entries(fields ?? {})) fd.append(k, v);
    for (const f of files) {
      fd.append('file[]', f, f.name);
    }
    const headers = await authHeaders();
    return new Promise<ManagerResponse>((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open('POST', managerUrl('upload'));
      for (const [k, v] of Object.entries(headers)) {
        if (k === 'Content-Type') continue;
        xhr.setRequestHeader(k, v);
      }
      xhr.withCredentials = credentialsMode() === 'include';
      xhr.upload.onprogress = (ev) => {
        if (onProgress && ev.lengthComputable) {
          onProgress(Math.round((ev.loaded / ev.total) * 100));
        }
      };
      xhr.onload = () => {
        noteRequestSucceeded();
        if (xhr.status >= 200 && xhr.status < 300) {
          try {
            resolve(JSON.parse(xhr.responseText));
          } catch (e) {
            reject(e);
          }
        } else {
          reject(requestFailure(xhr.status, xhr.responseText || '', lang()));
        }
      };
      // ⚠ `action`, not `background`: an upload is something the person
      // started and is waiting on, so it is NEVER folded into the shared
      // notice — it says for itself that it did not go.
      xhr.onerror = () => {
        noteRequestFailed('action');
        reject(networkFailure(lang()));
      };
      xhr.send(fd);
    });
  }

  function downloadUrl(path: string): string {
    return managerUrl('download', { path });
  }

  function previewUrl(path: string): string {
    return managerUrl('preview', { path });
  }

  /**
   * Fetch a file body with the configured auth headers + credentials,
   * returning the raw blob plus a normalized object URL viewers can mount
   * directly. Used by the rich viewers (3D, EPUB, PDF, PSD, TIFF, …) which
   * need an `ArrayBuffer` or a `Blob`-backed `objectURL` rather than the
   * relative preview URL.
   *
   * Caller is responsible for revoking `url` (`URL.revokeObjectURL(url)`)
   * once the viewer unmounts to avoid leaking the blob.
   */
  async function fetchBlob(
    path: string,
    opts: { fresh?: boolean } = {},
  ): Promise<{ url: string; blob: Blob; mime: string }> {
    const headers = await authHeaders();
    const res = await fetch(previewUrl(path), {
      headers,
      credentials: credentialsMode(),
      // ⚠ The preview endpoint answers `Cache-Control: private, max-age=60`,
      // which is right for the thing it was built for — a viewer re-opening
      // an image — and wrong for anything the client treats as CONTROL data.
      // `.filex-e2e.json` is control data: after the client rewrites it
      // (recovery upgrade, escrow slot, escrow refusal) the next read inside
      // that minute came back PRE-write, so the folder looked un-upgraded
      // and the question filex had just been answered was asked again.
      // Measured 2026-09-05 in a real browser: decline the escrow offer,
      // reopen the folder, and the offer was back.
      cache: opts.fresh ? 'no-store' : 'default',
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw requestFailure(res.status, text, lang());
    }
    const blob = await res.blob();
    const mime = blob.type || res.headers.get('content-type') || '';
    const url = URL.createObjectURL(blob);
    return { url, blob, mime };
  }

  /**
   * Like `fetchBlob` but returns the raw bytes — viewers that need
   * binary parsing (utif, ag-psd, pdfjs-dist) get the buffer directly
   * without the extra `Blob → arrayBuffer` round trip.
   */
  async function fetchArrayBuffer(path: string): Promise<ArrayBuffer> {
    const headers = await authHeaders();
    const res = await fetch(previewUrl(path), {
      headers,
      credentials: credentialsMode(),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw requestFailure(res.status, text, lang());
    }
    return res.arrayBuffer();
  }

  // --------------------------------------------------------------------
  // Peripheral endpoints
  // --------------------------------------------------------------------

  async function limits(): Promise<UploadLimits> {
    if (!endpoints.limits) return { max_upload_mb: 1024 };
    return jsonFetch<UploadLimits>(endpoints.limits);
  }

  async function capabilities(): Promise<Capabilities> {
    if (!endpoints.capabilities) {
      return {
        ffmpeg: false,
        ghostscript: false,
        max_chunk_mb: 5,
        upload_limit_mb: 1024,
        onlyoffice_url: config.onlyOfficeBase ?? null,
        drawio_url: config.drawioBase ?? null,
      };
    }
    return jsonFetch<Capabilities>(endpoints.capabilities);
  }

  /** What the signed-in account may do (`/api/auth/me`, filex internal/perm):
   *  null when the endpoint is off or the answer carries no permissions. */
  async function myPermissions(): Promise<{
    admin: boolean;
    permissions: string[];
    byFolder: string[];
  } | null> {
    if (!endpoints.me) return null;
    const me = await jsonFetch<{
      user?: { role?: string };
      permissions?: string[];
      permissions_in_folders?: string[];
      permissions_by_folder?: string[];
    }>(endpoints.me);
    if (!me || !Array.isArray(me.permissions)) return null;
    return {
      admin: me.user?.role === 'admin',
      permissions: [...me.permissions, ...(me.permissions_in_folders ?? [])],
      byFolder: me.permissions_by_folder ?? [],
    };
  }

  /* ── App plugins (docs/APP-PLUGINS-API.md) ─────────────────────────── */

  /** Fill `{name}` placeholders of an endpoint template. */
  function fillTemplate(tpl: string, vars: Record<string, string | number>): string {
    return Object.entries(vars).reduce(
      (acc, [k, v]) => acc.replaceAll(`{${k}}`, encodeURIComponent(String(v))),
      tpl,
    );
  }

  /**
   * An app call's URL with the language on SCREEN named (`lang=`).
   *
   * ⚠⚠ Accept-Language already carries it (jsonFetch), but the server ranks
   * that header below the ACCOUNT's language on purpose — for any other
   * client it is only the language the browser was installed in. An embed
   * draws the language its host chose (`config.locale`), whatever the
   * account says, and an app picks its plain strings (a field's label and
   * help, a select's options) by the language it is told: a Turkish popup
   * over an English account asked "Identity" with an English help line
   * (the signing app, 2026-09-26). Named here, it is an explicit choice the
   * server puts first (backend pluginLang). A host's own endpoint template
   * may already carry a query, so the separator is chosen, not assumed.
   */
  function withScreenLang(url: string): string {
    return `${url}${url.includes('?') ? '&' : '?'}lang=${encodeURIComponent(lang())}`;
  }

  /**
   * The address an app's interface is loaded from (docs/APP-PLUGINS-API.md →
   * An app's own interface): the row's `ui.url` joined with this server's
   * root, like every other relative address filex answers — unless the server
   * named an absolute one (interfaces on an origin of their own).
   */
  function appUIUrl(url: string): string {
    if (/^https?:\/\//i.test(url)) return url;
    const root = endpoints.manager.replace(/\/api\/files\/manager(\?.*)?$/, '');
    return root.replace(/\/$/, '') + (url.startsWith('/') ? url : `/${url}`);
  }

  /** `POST …/plugins/ui/{plugin}/{view}/call` — the app's module (`ui_call`). */
  async function pluginUICall(
    plugin: string,
    view: string,
    body: { method: string; params?: unknown; paths: string[] },
  ): Promise<{ result: unknown }> {
    if (!endpoints.pluginUICall) throw new Error('pluginUICall endpoint not configured');
    return jsonFetch<{ result: unknown }>(withScreenLang(fillTemplate(endpoints.pluginUICall, { plugin, view })), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  }

  /**
   * `PUT …/plugins/ui/{plugin}/{view}/save?path=` — an interface's save: the
   * new content of a file it was opened with (a new version, or the draft it
   * is), or with `dir` + `name` a NEW file there.
   *
   * ⚠ Anything over one chunk (8 MiB) goes as a CHUNKED save — `chunk=start`,
   * then `session=&offset=`, the last with `final=1` — so every request stays
   * under a reverse proxy's body limit, and a stream the interface hands over
   * is sent as it is read, never held whole in this page. The server writes
   * the file once, when the last chunk arrives (handlers/app_ui_chunks.go).
   */
  async function pluginUISave(
    plugin: string,
    view: string,
    target: { path: string } | { dir: string; name: string },
    body: Blob | ReadableStream<Uint8Array>,
  ): Promise<{ saved: boolean; path: string; name: string; size: number }> {
    if (!endpoints.pluginUISave) throw new Error('pluginUISave endpoint not configured');
    const q = 'path' in target
      ? `path=${encodeURIComponent(target.path)}`
      : `dir=${encodeURIComponent(target.dir)}&name=${encodeURIComponent(target.name)}`;
    const url = fillTemplate(endpoints.pluginUISave, { plugin, view });
    const base = `${url}${url.includes('?') ? '&' : '?'}${q}`;
    const put = <T>(u: string, part: Blob) =>
      jsonFetch<T>(u, { method: 'PUT', headers: { 'Content-Type': 'application/octet-stream' }, body: part });
    if (body instanceof Blob && body.size <= UI_SAVE_CHUNK) {
      return jsonFetch(base, {
        method: 'PUT',
        headers: { 'Content-Type': body.type || 'application/octet-stream' },
        body,
      });
    }
    let session = '';
    let offset = 0;
    const next = async (part: Blob, final: boolean) => {
      const u = session
        ? `${base}&session=${encodeURIComponent(session)}&offset=${offset}${final ? '&final=1' : ''}`
        : `${base}&chunk=start${final ? '&final=1' : ''}`;
      const r = await put<{ session?: string; received?: number; saved?: boolean; path: string; name: string; size: number }>(u, part);
      if (!final) {
        session = r.session ?? session;
        offset = r.received ?? offset + part.size;
      }
      return r;
    };
    if (body instanceof Blob) {
      for (let at = 0; ; at += UI_SAVE_CHUNK) {
        const end = Math.min(at + UI_SAVE_CHUNK, body.size);
        const part = body.slice(at, end);
        if (end >= body.size) return (await next(part, true)) as { saved: boolean; path: string; name: string; size: number };
        await next(part, false);
      }
    }
    const reader = body.getReader();
    let held: Uint8Array[] = [];
    let heldBytes = 0;
    for (;;) {
      const { done, value } = await reader.read();
      if (value && value.byteLength) {
        held.push(value);
        heldBytes += value.byteLength;
      }
      while (heldBytes >= UI_SAVE_CHUNK && !done) {
        const all = new Blob(held as BlobPart[]);
        await next(all.slice(0, UI_SAVE_CHUNK), false);
        const rest = new Uint8Array(await all.slice(UI_SAVE_CHUNK).arrayBuffer());
        held = rest.byteLength ? [rest] : [];
        heldBytes = rest.byteLength;
      }
      if (done) {
        const last = new Blob(held as BlobPart[]);
        if (!session && last.size <= UI_SAVE_CHUNK) {
          return jsonFetch(base, { method: 'PUT', headers: { 'Content-Type': 'application/octet-stream' }, body: last });
        }
        return (await next(last, true)) as { saved: boolean; path: string; name: string; size: number };
      }
    }
  }

  /**
   * The file's bytes as a response to stream from — `file.read` of an app's
   * interface. The same preview address the viewers read, with this viewer's
   * credentials, never cached (a save a moment ago must be what is read).
   */
  async function fetchResponse(path: string): Promise<Response> {
    const headers = await authHeaders();
    const res = await fetch(previewUrl(path), { headers, credentials: credentialsMode(), cache: 'no-store' });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw requestFailure(res.status, text, lang());
    }
    return res;
  }

  /** `GET /api/files/plugins/actions` — what applies to the caller, and the
   *  administrator's open rules by kind (Default apps, 0.50).
   *
   *  ⚠ Every field the answer carries is passed on: this used to rebuild the
   *  answer from `actions` and `views` only, so `open_rules` never reached the
   *  explorer - a handler switched off for a kind was still offered in "Open
   *  with" and the administrator's order was not followed (e2e 185). */
  async function pluginActions(): Promise<PluginActionsResponse> {
    if (!endpoints.pluginActions) return { actions: [], views: [] };
    const res = await jsonFetch<Partial<PluginActionsResponse>>(endpoints.pluginActions);
    return {
      actions: res?.actions ?? [],
      views: res?.views ?? [],
      ...(res?.open_rules ? { open_rules: res.open_rules } : {}),
    };
  }

  /**
   * `POST …/actions/{plugin}/{action}/run`. `paths` are adapter-qualified
   * wire paths (`docs://reports/nda.pdf`) — the same form copy/move send.
   * Answers `{op}` (queued) or `{surface}` (the action opens a view first).
   */
  async function pluginActionRun(
    plugin: string,
    action: string,
    body: { paths: string[]; params?: Record<string, unknown> },
  ): Promise<PluginRunResult> {
    if (!endpoints.pluginActionRun) throw new Error('pluginActionRun endpoint not configured');
    return jsonFetch<PluginRunResult>(withScreenLang(fillTemplate(endpoints.pluginActionRun, { plugin, action })), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  }

  /** `GET …/views/{plugin}/{view}?path=` — the initial surface (event `open`). */
  /**
   * `GET …/views/{plugin}/{view}` — a view's opening surface. `section` opens
   * a home page at one of its sections (`surface.sections`): the frame keeps
   * it in its own address, so Back and a link land where they were.
   */
  async function pluginView(
    plugin: string,
    view: string,
    path?: string,
    section?: string,
  ): Promise<{ surface: PluginSurface }> {
    if (!endpoints.pluginView) throw new Error('pluginView endpoint not configured');
    const url = fillTemplate(endpoints.pluginView, { plugin, view });
    const q = [
      path ? `path=${encodeURIComponent(path)}` : '',
      section ? `section=${encodeURIComponent(section)}` : '',
    ].filter(Boolean);
    return jsonFetch<{ surface: PluginSurface }>(withScreenLang(url + (q.length ? `?${q.join('&')}` : '')));
  }

  /** `POST …/views/{plugin}/{view}/event` — answers a surface, or `{op}` when it enqueued a job. */
  async function pluginViewEvent(plugin: string, view: string, body: PluginViewEventBody): Promise<PluginRunResult> {
    if (!endpoints.pluginViewEvent) throw new Error('pluginViewEvent endpoint not configured');
    return jsonFetch<PluginRunResult>(withScreenLang(fillTemplate(endpoints.pluginViewEvent, { plugin, view })), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  }

  /**
   * `GET …/plugins/users?plugin=<name>&q=` — the people-picker's lookup of
   * internal users. Answers only for a plugin that holds `users:lookup`;
   * 403 (and 404 on an older server) mean "free e-mail entry only" and the
   * caller reads the status off the thrown error.
   */
  async function pluginUsers(plugin: string, q: string): Promise<PluginUsersResponse> {
    if (!endpoints.pluginUsers) throw Object.assign(new Error('pluginUsers endpoint not configured'), { status: 404 });
    const url = `${endpoints.pluginUsers}?plugin=${encodeURIComponent(plugin)}&q=${encodeURIComponent(q)}`;
    const res = await jsonFetch<Partial<PluginUsersResponse>>(url);
    return { users: res?.users ?? [] };
  }

  /** `POST /api/files/ops/{id}/cancel`. */
  async function opsCancel(id: number): Promise<void> {
    if (!endpoints.opsCancel) throw new Error('opsCancel endpoint not configured');
    await jsonFetch<unknown>(fillTemplate(endpoints.opsCancel, { id }), { method: 'POST' });
  }

  /* wiring:e2 — escrow use is announced, not merely performed.
   *
   * Ask the server for a nonce sealed to the escrow public key; only the
   * holder of the private half can read it back. Returning it is what earns
   * the notification to the folder's owner — a bare "I used escrow" POST
   * would be a string anyone could send.
   *
   * ⚠ This is an announcement, not a gate. An operator holding the private
   * key can decrypt the folder offline with a script and never come here.
   * docs/E2E-ENCRYPTION.md says so plainly and must keep saying so. */
  async function e2eEscrowChallenge(
    path: string,
  ): Promise<{ id: string; challenge: string; kid: string }> {
    if (!endpoints.e2eEscrowChallenge) throw new Error('e2e escrow endpoint not configured');
    return jsonFetch(endpoints.e2eEscrowChallenge, {
      method: 'POST',
      body: JSON.stringify({ path }),
    });
  }

  async function e2eEscrowUsed(payload: {
    path: string;
    id: string;
    nonce: string;
  }): Promise<{ ok: boolean; notified: boolean }> {
    if (!endpoints.e2eEscrowUsed) throw new Error('e2e escrow endpoint not configured');
    return jsonFetch(endpoints.e2eEscrowUsed, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  /**
   * wiring:e2 password — announce that an encrypted folder's password was
   * changed (or reset with its recovery key), AFTER the new key file is
   * written. The server records it in the audit log and tells the folder's
   * owner (`e2e.password_changed`). It carries no key material: the change
   * itself happened in this browser.
   */
  /**
   * wiring:e2 convert — the first `n` bytes of a file, without downloading
   * the rest: a range request, and the body cancelled after the first chunk
   * even where the range is not honoured. For the conversion's "is this one
   * already encrypted?" (the magic), on files that may be gigabytes.
   */
  async function fetchHead(path: string, n = 16): Promise<Uint8Array> {
    const headers = { ...(await authHeaders()), Range: `bytes=0-${n - 1}` };
    const res = await fetch(previewUrl(path), { headers, credentials: credentialsMode(), cache: 'no-store' });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw requestFailure(res.status, text, lang());
    }
    if (!res.body) return new Uint8Array(await res.arrayBuffer()).slice(0, n);
    const reader = res.body.getReader();
    const out = new Uint8Array(n);
    let got = 0;
    try {
      while (got < n) {
        const { done, value } = await reader.read();
        if (done || !value) break;
        const take = Math.min(value.length, n - got);
        out.set(value.subarray(0, take), got);
        got += take;
      }
    } finally {
      void reader.cancel().catch(() => undefined);
    }
    return out.slice(0, got);
  }

  /**
   * wiring:e2 convert — after a folder was encrypted in place: the server
   * drops the thumbnails and extracted search content it holds for it and,
   * when asked (owner or administrator), every version and every trash entry
   * that came from it.
   */
  async function e2eCleanup(payload: { path: string; versions: boolean; trash: boolean }): Promise<{
    versions_deleted: number;
    trash_purged: number;
    thumbnails_dropped: number;
    index_cleared: number;
  }> {
    if (!endpoints.e2eCleanup) throw new Error('e2e cleanup endpoint not configured');
    return jsonFetch(endpoints.e2eCleanup, { method: 'POST', body: JSON.stringify(payload) });
  }

  async function e2ePasswordChanged(payload: {
    path: string;
    via: 'password' | 'recovery_key';
    rekey: boolean;
  }): Promise<{ ok: boolean; notified: boolean }> {
    if (!endpoints.e2ePasswordChanged) throw new Error('e2e password endpoint not configured');
    return jsonFetch(endpoints.e2ePasswordChanged, {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  async function createShare(payload: {
    path: string;
    password?: boolean;
    expires_at?: string | null;
    max_downloads?: number | null;
    // File-drop (public upload link) — kind:'drop' mints an upload link into a
    // folder instead of a download link; drop_settings carries the caps.
    kind?: string;
    max_uploads?: number | null;
    drop_settings?: Record<string, unknown> | null;
  }): Promise<{ share: ShareInfo & { url: string; path: string; filename: string; kind?: string } }> {
    if (!endpoints.shareCreate) throw new Error('shareCreate endpoint not configured');
    return jsonFetch(endpoints.shareCreate, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
  }

  async function listShares(path: string): Promise<{ shares: ShareInfo[] }> {
    if (!endpoints.shareList) return { shares: [] };
    const sep = endpoints.shareList.includes('?') ? '&' : '?';
    return jsonFetch(`${endpoints.shareList}${sep}path=${encodeURIComponent(path)}`);
  }

  async function revokeShare(uuid: string): Promise<{ success: boolean }> {
    if (!endpoints.shareDelete) throw new Error('shareDelete endpoint not configured');
    const url = endpoints.shareDelete.replace('{uuid}', encodeURIComponent(uuid));
    return jsonFetch(url, { method: 'DELETE' });
  }

  async function archiveList(path: string, password?: string): Promise<{ entries: ArchiveEntry[] }> {
    if (!endpoints.archiveList) throw new Error('archiveList endpoint not configured');
    const raw = await jsonFetch<{ entries: Array<{ name: string; size: number; is_dir: boolean; mtime?: number }> }>(
      endpoints.archiveList,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path, password }),
      },
    );
    return {
      entries: raw.entries.map((e) => ({
        name: e.name,
        size: e.size,
        isDir: e.is_dir,
        lastModified: e.mtime,
      })),
    };
  }

  async function archiveExtract(
    path: string,
    options: { members?: string[]; password?: string; dest?: string } = {},
  ): Promise<{ op?: PendingOpDto; keys?: string[]; count?: number }> {
    if (!endpoints.archiveExtract) throw new Error('archiveExtract endpoint not configured');
    return jsonFetch(endpoints.archiveExtract, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, ...options }),
    });
  }

  async function archiveCreate(payload: {
    dest: string;
    sources: string[];
    format: ArchiveCreateFormat;
    password?: string;
    encrypt_filenames?: boolean;
    compression?: number;
    solid?: boolean;
    dictionary_size_mb?: number;
  }): Promise<{ op: PendingOpDto }> {
    if (!endpoints.archiveCreate) throw new Error('archiveCreate endpoint not configured');
    return jsonFetch(endpoints.archiveCreate, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
  }

  async function archiveAdd(path: string, files: Array<{ name: string; source: string }>): Promise<{ path: string }> {
    if (!endpoints.archiveAdd) throw new Error('archiveAdd endpoint not configured');
    return jsonFetch(endpoints.archiveAdd, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, files }),
    });
  }

  /* === koru:k1 — version history (inspector panel) ===
   * Derived from the manager endpoint by swapping `/manager` for `/versions`
   * (same trick permissionsUrl/globalSearch use) so embedded proxies that
   * forward the whole /api/files/* subtree keep working.
   *   GET  /api/files/versions?node_id=N       → {versions, node_id}
   *   POST /api/files/versions/restore         → {node_id, version_id, snapshot_current}
   *   POST /api/files/versions/snapshot        → {node_id}   (may not exist on older backends)
   */
  function versionsUrl(sub = ''): string {
    const base = endpoints.manager.replace(/\/manager(\?.*)?$/, '/versions');
    return base + sub;
  }
  async function listVersions(nodeId: number): Promise<NodeVersion[]> {
    const data = await jsonFetch<{ versions?: NodeVersion[] | null }>(
      versionsUrl() + '?node_id=' + encodeURIComponent(String(nodeId)),
    );
    return Array.isArray(data?.versions) ? data.versions : [];
  }
  async function restoreVersion(
    nodeId: number,
    versionId: number,
    snapshotCurrent = true,
  ): Promise<void> {
    await jsonFetch(versionsUrl('/restore'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        node_id: nodeId,
        version_id: versionId,
        snapshot_current: snapshotCurrent,
      }),
    });
  }
  async function snapshotVersion(nodeId: number): Promise<void> {
    await jsonFetch(versionsUrl('/snapshot'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ node_id: nodeId }),
    });
  }
  /* === /koru:k1 === */

  /* === calisma:d3 — node comments (inspector panel) ===
   * Same manager-URL derivation trick as versions/permissions so embedded
   * proxies forwarding the whole /api/files/* subtree keep working.
   *   GET    /api/files/comments?node_id=N → {comments, node_id}
   *   POST   /api/files/comments           → {node_id, body}
   *   DELETE /api/files/comments/{id}      → {ok}
   */
  function commentsUrl(sub = ''): string {
    const base = endpoints.manager.replace(/\/manager(\?.*)?$/, '/comments');
    return base + sub;
  }
  async function listComments(nodeId: number): Promise<NodeComment[]> {
    const data = await jsonFetch<{ comments?: NodeComment[] | null }>(
      commentsUrl() + '?node_id=' + encodeURIComponent(String(nodeId)),
    );
    return Array.isArray(data?.comments) ? data.comments : [];
  }
  async function addComment(nodeId: number, body: string): Promise<NodeComment> {
    const data = await jsonFetch<{ comment: NodeComment }>(commentsUrl(), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ node_id: nodeId, body }),
    });
    return data.comment;
  }
  async function deleteComment(id: number): Promise<void> {
    await jsonFetch(commentsUrl('/' + encodeURIComponent(String(id))), {
      method: 'DELETE',
    });
  }
  /* === /calisma:d3 === */

  // Mint a short-lived WebSocket auth ticket for the realtime layer. Derived
  // from the manager URL (so it flows through the same host proxy) and uses the
  // same auth/creds as every other call. Returns null on any failure (a backend
  // without the endpoint, a network error) so the caller falls back to polling.
  async function wsTicket(): Promise<{ ticket: string; ws_url: string } | null> {
    const url = endpoints.manager.replace(/\/manager(\?.*)?$/, '/ws-ticket');
    try {
      return await jsonFetch<{ ticket: string; ws_url: string }>(url, { method: 'POST' });
    } catch {
      return null;
    }
  }

  return {
    // Realtime
    wsTicket,
    // Manager
    index,
    search,
    globalSearch /* bul:s3 */,
    quotaMe /* surucu:d1 */,
    storageUsage /* surucu:d1 */,
    subfolders,
    newFolder,
    newFile,
    drafts,
    draftsBase,
    rename,
    renameQueued,
    move,
    copy,
    moveAsync,
    deleteAsync,
    deleteItems,
    allowedAt,
    myPermissions,
    restore,
    listTrash,
    restoreIds,
    restoreQueued,
    purgeTrash,
    purgeVersion,
    uploadMultipart,
    downloadUrl,
    previewUrl,
    fetchBlob,
    fetchArrayBuffer,
    // Peripheral
    limits,
    capabilities,
    /* wiring:e2 */
    e2eEscrowChallenge,
    e2eEscrowUsed,
    e2ePasswordChanged,
    e2eCleanup,
    fetchHead,
    transferNamed,
    /* App plugins */
    pluginActions,
    pluginActionRun,
    pluginView,
    pluginViewEvent,
    pluginUsers,
    opsCancel,
    appUIUrl,
    pluginUICall,
    pluginUISave,
    fetchResponse,
    createShare,
    listShares,
    revokeShare,
    archiveList,
    archiveExtract,
    archiveCreate,
    archiveAdd,
    // Version history (koru:k1 inspector)
    listVersions,
    restoreVersion,
    snapshotVersion,
    // Node comments (calisma:d3 inspector)
    listComments,
    addComment,
    deleteComment,
    // Permissions (RBAC panel)
    listPermissions,
    resolveEmail,
    searchUsers,
    searchGroups,
    addPermission,
    updatePermission,
    deletePermission,
    updateGroupPermission,
    deleteGroupPermission,
    invitePermission,
    shareMail,
    // Internals (exposed for useUploadChunked + PreviewModal)
    endpoints,
    authHeaders,
    authHeadersSync,
    credentialsMode,
    jsonFetch,
    withScreenLang,
  };
}

export type FileApi = ReturnType<typeof useFileApi>;
