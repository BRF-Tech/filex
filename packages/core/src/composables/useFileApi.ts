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

import type { ExplorerConfig, AuthConfig, EndpointMap } from '../types/ExplorerConfig';
import type {
  FileNode,
  ShareInfo,
  UploadLimits,
  Capabilities,
  ArchiveEntry,
  TrashEntry,
} from '../types/FileNode';

/** Server-side PendingOp DTO (mirror of Modules\FishApp\Models\PendingOp::toApiArray). */
export interface PendingOpDto {
  id: number;
  op_type: 'copy' | 'move' | 'delete';
  status: 'pending' | 'running' | 'done' | 'error';
  progress_total: number;
  progress_done: number;
  target_path: string | null;
  source_dir: string | null;
  source_count: number;
  error_message: string | null;
  started_at: string | null;
  finished_at: string | null;
  created_at: string | null;
}

export interface ManagerResponse {
  adapter: string;
  storages: string[];
  dirname: string;
  read_only: boolean;
  /** RBAC effective level for the current user on this directory ('' when ACL
   *  is not enforced on the storage). Gates the folder-level write actions. */
  perm?: 'none' | 'viewer' | 'editor' | 'owner';
  files: FileNode[];
}

/** A single ACL grant row (RBAC permissions panel). */
export interface Grant {
  id: number;
  storage_id: number;
  path_prefix: string;
  user_id: number;
  level: 'viewer' | 'editor' | 'owner';
  user_email?: string;
  user_display_name?: string;
  inherited?: boolean;
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
  user?: { id: number; email: string; display_name: string; role: string };
}

export interface UserSuggestion {
  id: number;
  email: string;
  display_name: string;
  role: string;
}
export interface UserSearchResponse {
  users: UserSuggestion[];
}

export interface InviteResponse {
  mode: 'granted' | 'user_created' | 'shared';
  user_id?: number;
  url?: string;
  temp_password?: string;
  emailed: boolean;
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
    uploadInit: derive(config.uploadInit, '/api/files/upload/init'),
    uploadFinalize: derive(config.uploadFinalize, '/api/files/upload/finalize'),
    uploadAbort: derive(config.uploadAbort, '/api/files/upload/abort'),
    shareCreate: derive(config.shareCreate, '/api/files/share'),
    shareList: derive(config.shareList, '/api/files/share'),
    shareDelete: derive(config.shareDelete, '/api/files/share/{uuid}'),
    limits: derive(config.limits, '/api/files/limits'),
    capabilities: derive(config.capabilities, '/api/files/capabilities'),
    archiveList: derive(config.archiveList, '/api/files/archive/list'),
    archiveExtract: derive(config.archiveExtract, '/api/files/archive/extract'),
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

export function useFileApi(config: ExplorerConfig) {
  const endpoints = resolveEndpoints(config);
  const authConf = normalizeAuth(config.auth);

  async function authHeaders(extra: Record<string, string> = {}): Promise<Record<string, string>> {
    const h: Record<string, string> = { Accept: 'application/json', ...extra };
    if (authConf.kind === 'bearer') {
      const token = await resolveToken(authConf.token);
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

  /**
   * Sync auth-header builder for callers that need headers in a
   * non-async context (XMLHttpRequest, OnlyOffice config endpoint
   * post-mounted PreviewModal). Function-token bearers are *resolved
   * lazily* via the async path; here we just include the cached value
   * if any. Component code should prefer the async `authHeaders()` —
   * this is a defensive escape hatch.
   */
  function authHeadersSync(extra: Record<string, string> = {}): Record<string, string> {
    const h: Record<string, string> = { Accept: 'application/json', ...extra };
    if (authConf.kind === 'bearer' && typeof authConf.token === 'string') {
      h.Authorization = `Bearer ${authConf.token}`;
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

  // Map an HTTP status to a short, human-readable message in the explorer's
  // locale. The raw JSON body is attached as `.detail` for debugging but never
  // shown in the toast (Burak: "404/403 falan verince ham json görüyorum").
  function statusMessage(status: number): string {
    const tr = (config.locale ?? 'tr') !== 'en';
    const m: Record<number, [string, string]> = {
      400: ['Geçersiz istek', 'Bad request'],
      401: ['Oturum gerekli, tekrar giriş yapın', 'Sign-in required'],
      403: ['Bu işlem için yetkiniz yok', 'You are not allowed to do this'],
      404: ['Bulunamadı', 'Not found'],
      409: ['Zaten var / çakışma', 'Already exists / conflict'],
      413: ['Dosya çok büyük', 'File too large'],
      415: ['Bu dosya türü desteklenmiyor', 'Unsupported file type'],
      422: ['Geçersiz veri', 'Invalid data'],
      429: ['Çok fazla istek, biraz bekleyin', 'Too many requests'],
      500: ['Sunucu hatası', 'Server error'],
      501: ['Bu işlem desteklenmiyor', 'Not supported'],
      503: ['Servis şu an kullanılamıyor', 'Service unavailable'],
    };
    const e = m[status];
    if (e) return tr ? e[0] : e[1];
    return tr ? `Hata (${status})` : `Error (${status})`;
  }

  async function jsonFetch<T>(url: string, init: RequestInit = {}): Promise<T> {
    const headers = {
      ...(await authHeaders()),
      ...((init.headers as Record<string, string> | undefined) ?? {}),
    };
    const res = await fetch(url, {
      ...init,
      headers,
      credentials: credentialsMode(),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      const err = new Error(statusMessage(res.status)) as Error & { status?: number; detail?: string };
      err.status = res.status;
      err.detail = text.slice(0, 300);
      throw err;
    }
    return res.json() as Promise<T>;
  }

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
  async function addPermission(body: { path: string; user_id: number; level: string; is_dir?: boolean }): Promise<unknown> {
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

  async function index(path: string): Promise<ManagerResponse> {
    return jsonFetch<ManagerResponse>(managerUrl('index', { path }));
  }

  async function search(path: string, filter: string): Promise<ManagerResponse> {
    return jsonFetch<ManagerResponse>(managerUrl('search', { path, filter }));
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

  async function rename(path: string, item: string, name: string): Promise<ManagerResponse> {
    return jsonFetch<ManagerResponse>(managerUrl('rename'), {
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

  /** Server-side recursive copy (async — returns a PendingOp). */
  async function copy(source: string[], target: string): Promise<{ op: PendingOpDto }> {
    if (!endpoints.copy) throw new Error('copy endpoint not configured');
    return jsonFetch(endpoints.copy, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source, target }),
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
   */
  async function restoreIds(ids: number[]): Promise<{ restored: number }> {
    const url = endpoints.trashRestore;
    if (!url) throw new Error('trashRestore endpoint not configured');
    let restored = 0;
    for (const id of ids) {
      try {
        await jsonFetch(url, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ node_id: id }),
        });
        restored++;
      } catch {
        /* skip individual failures; report the count that succeeded */
      }
    }
    return { restored };
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
  ): Promise<ManagerResponse> {
    const fd = new FormData();
    fd.append('path', path);
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
        if (xhr.status >= 200 && xhr.status < 300) {
          try {
            resolve(JSON.parse(xhr.responseText));
          } catch (e) {
            reject(e);
          }
        } else {
          reject(new Error(`${xhr.status} ${xhr.statusText}: ${xhr.responseText.slice(0, 200)}`));
        }
      };
      xhr.onerror = () => reject(new Error('Network error'));
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
  ): Promise<{ url: string; blob: Blob; mime: string }> {
    const headers = await authHeaders();
    const res = await fetch(previewUrl(path), {
      headers,
      credentials: credentialsMode(),
    });
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new Error(
        `${res.status} ${res.statusText}${text ? ' — ' + text.slice(0, 200) : ''}`,
      );
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
      throw new Error(
        `${res.status} ${res.statusText}${text ? ' — ' + text.slice(0, 200) : ''}`,
      );
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
        libreoffice: false,
        max_chunk_mb: 5,
        upload_limit_mb: 1024,
        onlyoffice_url: config.onlyOfficeBase ?? null,
        drawio_url: config.drawioBase ?? null,
        convert_url: config.convertBase ?? null,
      };
    }
    return jsonFetch<Capabilities>(endpoints.capabilities);
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

  async function archiveList(path: string): Promise<{ entries: ArchiveEntry[] }> {
    if (!endpoints.archiveList) throw new Error('archiveList endpoint not configured');
    const raw = await jsonFetch<{ entries: Array<{ name: string; size: number; is_dir: boolean; mtime?: number }> }>(
      endpoints.archiveList,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path }),
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

  async function archiveExtract(path: string, members?: string[]): Promise<{ keys: string[]; count: number }> {
    if (!endpoints.archiveExtract) throw new Error('archiveExtract endpoint not configured');
    return jsonFetch(endpoints.archiveExtract, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, members }),
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
    subfolders,
    newFolder,
    rename,
    move,
    copy,
    moveAsync,
    deleteAsync,
    deleteItems,
    restore,
    listTrash,
    restoreIds,
    uploadMultipart,
    downloadUrl,
    previewUrl,
    fetchBlob,
    fetchArrayBuffer,
    // Peripheral
    limits,
    capabilities,
    createShare,
    listShares,
    revokeShare,
    archiveList,
    archiveExtract,
    archiveAdd,
    // Permissions (RBAC panel)
    listPermissions,
    resolveEmail,
    searchUsers,
    addPermission,
    updatePermission,
    deletePermission,
    invitePermission,
    shareMail,
    // Internals (exposed for useUploadChunked + PreviewModal)
    endpoints,
    authHeaders,
    authHeadersSync,
    credentialsMode,
    jsonFetch,
  };
}

export type FileApi = ReturnType<typeof useFileApi>;
