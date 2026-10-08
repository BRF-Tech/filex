/**
 * e2evault/api — the vault API (`/api/files/e2e/vault/*`) and the byte-range
 * reads of packs and index files (docs/E2E-VAULT-FORMAT.md → "API").
 *
 * Every write carries the lock token in `X-Filex-Vault-Lock`. Reads need no
 * lock: index files and pack ranges come through the ordinary download
 * (`GET /api/files/manager?action=download&path=…` with `Range`), as
 * ciphertext.
 *
 * An error answer is `{"error": "<CODE>", "message": "...", ...}`; it comes
 * back as a VaultApiError carrying the code and the extra fields (`holder`,
 * `since`, `retry_after`, `reason`, `latest`).
 */
import { bytesToB64 } from '../e2ecrypto';
import { VAULT_DELETE_MAX, bufferView, generationHex, indexPath, packPath } from './layout';
import { VaultPackRetry } from './writer';

export const VAULT_LOCK_HEADER = 'X-Filex-Vault-Lock';

export type VaultClientKind = 'web' | 'desktop' | 'cli' | 'mount';

export interface VaultHolder {
  name: string;
  client: VaultClientKind | string;
  label?: string;
}

export interface VaultLockInfo {
  holder: VaultHolder;
  since: string;
  expires_at: string;
  mine: boolean;
}

export interface VaultState {
  vault_id: string;
  pack_log2: number;
  generation: number;
  lock: VaultLockInfo | null;
}

export interface VaultLockGrant {
  token: string;
  generation: number;
  lease_seconds: number;
  idle_seconds: number;
  expires_at: string;
}

export interface VaultListItem {
  generation?: number;
  id?: string;
  size: number;
  /** An RFC 3339 time in UTC (a number is read as seconds or ms since 1970). */
  mtime: string | number;
}

/** One page of a listing. `now` is the SERVER's clock: retention is
 *  measured against it, never against this machine's. */
export interface VaultListPage {
  items: VaultListItem[];
  next: string | null;
  now?: string;
}

export type VaultLostReason = 'expired' | 'idle' | 'broken' | 'released' | 'taken';

/** A refusal of the vault API, with its code and fields. */
export class VaultApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly fields: Record<string, unknown> = {},
  ) {
    super(message || code || `vault API: ${status}`);
    this.name = 'VaultApiError';
  }

  get holder(): VaultHolder | null {
    const h = this.fields.holder;
    return h && typeof h === 'object' ? (h as VaultHolder) : null;
  }

  get reason(): string {
    return typeof this.fields.reason === 'string' ? this.fields.reason : '';
  }

  get latest(): number | null {
    return typeof this.fields.latest === 'number' ? this.fields.latest : null;
  }
}

/** The explorer's client, as much of it as the vault needs (useFileApi). */
export interface VaultHttp {
  endpoints: { manager: string };
  authHeaders(extra?: Record<string, string>): Promise<Record<string, string>>;
  credentialsMode(): RequestCredentials;
  downloadUrl(path: string): string;
  /** The window's language, for the server's `message`. */
  acceptLanguage?(): string;
}

/** `/api/files/manager` → `/api/files/e2e/vault`, like permissions and drafts. */
export function vaultApiBase(manager: string): string {
  return manager.replace(/\/manager(\?.*)?$/, '/e2e/vault');
}

/** Join a vault root's wire path and a path inside it. */
export function vaultWire(root: string, rel: string): string {
  return `${root.replace(/\/+$/, '')}/${rel.replace(/^\/+/, '')}`;
}

export function createVaultApi(http: VaultHttp) {
  const base = vaultApiBase(http.endpoints.manager);

  async function headers(extra: Record<string, string> = {}, token?: string | null): Promise<Record<string, string>> {
    const h = await http.authHeaders(extra);
    if (http.acceptLanguage) h['Accept-Language'] = http.acceptLanguage();
    if (token) h[VAULT_LOCK_HEADER] = token;
    return h;
  }

  async function failure(res: Response): Promise<VaultApiError> {
    const text = await res.text().catch(() => '');
    let fields: Record<string, unknown> = {};
    try {
      const parsed = JSON.parse(text);
      if (parsed && typeof parsed === 'object') fields = parsed as Record<string, unknown>;
    } catch {
      /* not JSON */
    }
    const code = typeof fields.error === 'string' ? fields.error : '';
    const message = typeof fields.message === 'string' ? fields.message : '';
    return new VaultApiError(res.status, code, message, fields);
  }

  async function call<T>(method: string, sub: string, body?: unknown, token?: string | null, signal?: AbortSignal): Promise<T> {
    const res = await fetch(base + sub, {
      method,
      headers: await headers(body === undefined ? {} : { 'Content-Type': 'application/json' }, token),
      credentials: http.credentialsMode(),
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
    if (!res.ok) throw await failure(res);
    if (res.status === 204) return undefined as T;
    const text = await res.text();
    return (text ? JSON.parse(text) : undefined) as T;
  }

  const q = (params: Record<string, string | number>) =>
    '?' +
    Object.entries(params)
      .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`)
      .join('&');

  /** A body upload (pack, index). A network failure is an unknown outcome. */
  async function put(sub: string, bytes: Uint8Array, token: string, signal?: AbortSignal): Promise<Response> {
    let res: Response;
    try {
      res = await fetch(base + sub, {
        method: 'PUT',
        headers: await headers({ 'Content-Type': 'application/octet-stream' }, token),
        credentials: http.credentialsMode(),
        body: bufferView(bytes),
        signal,
      });
    } catch (err) {
      if ((err as Error)?.name === 'AbortError') throw err;
      throw new VaultPackRetry();
    }
    return res;
  }

  /** One page of `v/idx/` or `v/p/`. */
  function list(path: string, kind: 'index' | 'pack', after = '', limit = 1000): Promise<VaultListPage> {
    const params: Record<string, string | number> = { path, kind, limit };
    if (after) params.after = after;
    return call('GET', '/list' + q(params));
  }

  return {
    base,
    /** `POST /create` — a new, empty vault: its key file and generation 1. */
    create(path: string, marker: object, index: Uint8Array): Promise<{ generation: number }> {
      return call('POST', '/create', { path, marker, index: bytesToB64(index) });
    },
    /** `GET /state`. With the lock token, when this session holds the lock,
     *  so that `lock.mine` can mean "this session" and not "this person". */
    state(path: string, signal?: AbortSignal, token?: string | null): Promise<VaultState> {
      return call('GET', '/state' + q({ path }), undefined, token ?? null, signal);
    },
    list,
    /** Every page, and the server's clock of the first one (ms since 1970,
     *  0 when it sent none). */
    async listAll(path: string, kind: 'index' | 'pack'): Promise<{ items: VaultListItem[]; now: number }> {
      const out: VaultListItem[] = [];
      let now = 0;
      let after = '';
      for (let i = 0; i < 10_000; i++) {
        const page = await list(path, kind, after, 10_000);
        if (!now && page.now) now = listedTime(page.now);
        out.push(...(page.items ?? []));
        if (!page.next) break;
        after = page.next;
      }
      return { items: out, now };
    },
    lock(path: string, client: VaultClientKind, label: string): Promise<VaultLockGrant> {
      return call('POST', '/lock', { path, client, label });
    },
    renew(path: string, token: string, active: boolean): Promise<{ expires_at: string; idle_until: string }> {
      return call('POST', '/lock/renew', { path, active }, token);
    },
    release(path: string, token: string): Promise<void> {
      return call('POST', '/lock/release', { path }, token);
    },
    /**
     * The page is closing: `release` with `keepalive`, best effort (the lease
     * covers the rest). Synchronous on purpose - an unload handler cannot wait.
     */
    releaseKeepalive(path: string, token: string, headersNow: Record<string, string>): void {
      try {
        void fetch(base + '/lock/release', {
          method: 'POST',
          headers: { ...headersNow, 'Content-Type': 'application/json', [VAULT_LOCK_HEADER]: token },
          credentials: http.credentialsMode(),
          body: JSON.stringify({ path }),
          keepalive: true,
        }).catch(() => undefined);
      } catch {
        /* the page is going anyway */
      }
    },
    /** End someone else's lock (the vault folder's owner, or an administrator). */
    breakLock(path: string): Promise<void> {
      return call('POST', '/lock/break', { path });
    },
    /** `PUT /pack`: 201. A taken id and a lost answer are both a retry under a new id. */
    async putPack(path: string, id: string, bytes: Uint8Array, token: string, signal?: AbortSignal): Promise<void> {
      const res = await put('/pack' + q({ path, id }), bytes, token, signal);
      if (res.ok) return;
      const err = await failure(res);
      if (err.code === 'VAULT_PACK_EXISTS' || res.status >= 500 || res.status === 408) throw new VaultPackRetry(err.message);
      throw err;
    },
    /** `PUT /index`: 201 {generation}. */
    async putIndex(path: string, generation: number, bytes: Uint8Array, token: string, signal?: AbortSignal): Promise<number> {
      const res = await put('/index' + q({ path, generation }), bytes, token, signal);
      if (res.ok) {
        const body = (await res.json().catch(() => ({}))) as { generation?: number };
        return typeof body.generation === 'number' ? body.generation : generation;
      }
      const err = await failure(res);
      if (res.status >= 500 || res.status === 408) throw new VaultPackRetry(err.message);
      throw err;
    },
    /** `POST /delete`: at most 1 000 names, for good. Answers how many. */
    del(path: string, packs: string[], indexes: number[], token: string): Promise<{ deleted: { packs: number; indexes: number } }> {
      if (packs.length + indexes.length > VAULT_DELETE_MAX) throw new Error('vault: at most 1 000 deletions at a time');
      return call('POST', '/delete', { path, packs, indexes }, token);
    },
    async getPrefs(): Promise<{ idle_minutes: number }> {
      return call('GET', '/prefs');
    },
    async putPrefs(idleMinutes: number): Promise<{ idle_minutes: number }> {
      return call('PUT', '/prefs', { idle_minutes: idleMinutes });
    },

    /** The bytes of `v/idx/G.fxi`, whole; null when there is no such file. */
    async fetchIndex(root: string, gen: number, signal?: AbortSignal): Promise<Uint8Array | null> {
      const res = await fetch(http.downloadUrl(vaultWire(root, indexPath(gen))), {
        headers: await headers(),
        credentials: http.credentialsMode(),
        cache: 'no-store',
        signal,
      });
      if (res.status === 404) return null;
      if (!res.ok) throw await failure(res);
      return new Uint8Array(await res.arrayBuffer());
    },
    /** `length` bytes at `offset` of a pack, by an HTTP range request (206). */
    async fetchRange(root: string, pack: string, offset: number, length: number, signal?: AbortSignal): Promise<Uint8Array> {
      const res = await fetch(http.downloadUrl(vaultWire(root, packPath(pack))), {
        headers: await headers({ Range: `bytes=${offset}-${offset + length - 1}` }),
        credentials: http.credentialsMode(),
        // A pack never changes once written: its bytes may be cached.
        cache: 'default',
        signal,
      });
      if (res.status === 404) throw new VaultApiError(404, 'VAULT_PACK_GONE', '', { pack });
      if (!res.ok) throw await failure(res);
      const all = new Uint8Array(await res.arrayBuffer());
      // A server that ignores Range answers 200 with the whole pack.
      if (res.status === 200 && all.length > length) return all.slice(offset, offset + length);
      return all;
    },
  };
}

export type VaultApi = ReturnType<typeof createVaultApi>;

/** An index listing row's modification time, in ms since 1970. */
export function listedTime(m: string | number): number {
  if (typeof m === 'number') return m < 1e12 ? m * 1000 : m;
  const t = Date.parse(m);
  return Number.isFinite(t) ? t : 0;
}

/** For tests and logs. */
export function describeGeneration(gen: number): string {
  return generationHex(gen);
}
