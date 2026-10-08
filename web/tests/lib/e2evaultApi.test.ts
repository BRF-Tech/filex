// The vault API client (docs/E2E-VAULT-FORMAT.md → "API"): the routes it
// calls, the lock token on every write and only there, ranges for packs, the
// refusals read into codes and fields, and which failures mean "send the pack
// again under a new id".

import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  VAULT_LOCK_HEADER,
  VaultApiError,
  createVaultApi,
  listedTime,
  vaultApiBase,
  vaultWire,
} from '../../../packages/core/src/lib/e2evault/api';
import { VaultPackRetry } from '../../../packages/core/src/lib/e2evault/writer';

interface Call {
  url: string;
  method: string;
  headers: Record<string, string>;
  body?: unknown;
  keepalive?: boolean;
}

function http() {
  return {
    endpoints: { manager: '/api/files/manager' },
    authHeaders: async (extra: Record<string, string> = {}) => ({ Accept: 'application/json', 'X-CSRF-TOKEN': 'csrf', ...extra }),
    credentialsMode: () => 'include' as RequestCredentials,
    downloadUrl: (p: string) => `/api/files/manager?action=download&path=${encodeURIComponent(p)}`,
    acceptLanguage: () => 'tr-TR',
  };
}

function answer(status: number, body?: unknown): Response {
  if (body instanceof Uint8Array) return new Response(body, { status });
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function stubFetch(reply: (c: Call) => Response | Promise<Response>) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit = {}) => {
      const c: Call = {
        url: String(url),
        method: init.method ?? 'GET',
        headers: (init.headers ?? {}) as Record<string, string>,
        body: init.body,
        keepalive: init.keepalive,
      };
      calls.push(c);
      return reply(c);
    }),
  );
  return calls;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('the vault API client', () => {
  it('lives beside the manager, like permissions and drafts', () => {
    expect(vaultApiBase('/api/files/manager')).toBe('/api/files/e2e/vault');
    expect(vaultApiBase('https://fm.example/api/files/manager?x=1')).toBe('https://fm.example/api/files/e2e/vault');
    expect(vaultWire('docs://Kasa/', '/v/idx/0000000000000001.fxi')).toBe('docs://Kasa/v/idx/0000000000000001.fxi');
  });

  it('state and lock: no token; renew, release, pack, index and delete: the token', async () => {
    const calls = stubFetch((c) => {
      if (c.url.includes('/state')) return answer(200, { vault_id: 'x', pack_log2: 22, generation: 4, lock: null });
      if (c.url.endsWith('/lock')) return answer(200, { token: 'T', generation: 4, lease_seconds: 60, idle_seconds: 180, expires_at: '' });
      if (c.url.includes('/lock/renew')) return answer(200, { expires_at: '', idle_until: '' });
      if (c.url.includes('/lock/release')) return answer(204);
      if (c.url.includes('/pack')) return answer(201);
      if (c.url.includes('/index')) return answer(201, { generation: 5 });
      if (c.url.includes('/delete')) return answer(200, { deleted: { packs: [], indexes: [] } });
      return answer(500);
    });
    const api = createVaultApi(http());
    expect((await api.state('docs://Kasa')).generation).toBe(4);
    expect((await api.lock('docs://Kasa', 'web', 'Firefox, Linux')).token).toBe('T');
    await api.renew('docs://Kasa', 'T', true);
    await api.release('docs://Kasa', 'T');
    await api.putPack('docs://Kasa', 'b067d7bcd62c9f817216a5ef1b1b653b', new Uint8Array(65536), 'T');
    expect(await api.putIndex('docs://Kasa', 5, new Uint8Array(65536), 'T')).toBe(5);
    await api.del('docs://Kasa', [], [1], 'T');

    expect(calls[0].url).toBe('/api/files/e2e/vault/state?path=docs%3A%2F%2FKasa');
    expect(calls[0].headers[VAULT_LOCK_HEADER]).toBeUndefined();
    expect(calls[0].headers['Accept-Language']).toBe('tr-TR');
    expect(calls[1].method).toBe('POST');
    expect(JSON.parse(String(calls[1].body))).toEqual({ path: 'docs://Kasa', client: 'web', label: 'Firefox, Linux' });
    expect(calls[1].headers[VAULT_LOCK_HEADER]).toBeUndefined();
    for (const c of calls.slice(2)) expect(c.headers[VAULT_LOCK_HEADER], c.url).toBe('T');
    expect(JSON.parse(String(calls[2].body))).toEqual({ path: 'docs://Kasa', active: true });
    const pack = calls.find((c) => c.url.includes('/pack'))!;
    expect(pack.method).toBe('PUT');
    expect(pack.url).toBe('/api/files/e2e/vault/pack?path=docs%3A%2F%2FKasa&id=b067d7bcd62c9f817216a5ef1b1b653b');
    expect(pack.headers['Content-Type']).toBe('application/octet-stream');
    expect(calls.find((c) => c.url.includes('/index'))!.url).toContain('generation=5');
  });

  it('a pack is read by an HTTP range of the ordinary download', async () => {
    const calls = stubFetch(() => answer(206, new Uint8Array([1, 2, 3])));
    const api = createVaultApi(http());
    const got = await api.fetchRange('docs://Kasa', 'b067d7bcd62c9f817216a5ef1b1b653b', 100, 3);
    expect([...got]).toEqual([1, 2, 3]);
    expect(calls[0].url).toBe(
      '/api/files/manager?action=download&path=' + encodeURIComponent('docs://Kasa/v/p/b0/b067d7bcd62c9f817216a5ef1b1b653b.fxp'),
    );
    expect(calls[0].headers.Range).toBe('bytes=100-102');
  });

  it('a server that ignores Range: the bytes asked for are cut out of the whole pack', async () => {
    const whole = new Uint8Array(200).map((_, i) => i);
    stubFetch(() => answer(200, whole));
    const got = await createVaultApi(http()).fetchRange('docs://Kasa', 'b067d7bcd62c9f817216a5ef1b1b653b', 10, 4);
    expect([...got]).toEqual([10, 11, 12, 13]);
  });

  it('an index file that is not there is null; a pack that is not there is VAULT_PACK_GONE', async () => {
    stubFetch(() => answer(404, { error: 'not_found' }));
    const api = createVaultApi(http());
    expect(await api.fetchIndex('docs://Kasa', 9)).toBeNull();
    await expect(api.fetchRange('docs://Kasa', 'b067d7bcd62c9f817216a5ef1b1b653b', 32, 10)).rejects.toMatchObject({ code: 'VAULT_PACK_GONE' });
  });

  it('VAULT_LOCKED carries who holds it, since when and when to try again', async () => {
    stubFetch(() =>
      answer(409, { error: 'VAULT_LOCKED', message: 'Kasaya Ayşe yazıyor', holder: { name: 'Ayşe', client: 'desktop', label: 'filex, macOS' }, since: '2026-10-06T10:00:00Z', retry_after: 30 }),
    );
    const err = await createVaultApi(http())
      .lock('docs://Kasa', 'web', '')
      .catch((e: unknown) => e as VaultApiError);
    expect(err).toBeInstanceOf(VaultApiError);
    expect(err.status).toBe(409);
    expect(err.code).toBe('VAULT_LOCKED');
    expect(err.message).toBe('Kasaya Ayşe yazıyor');
    expect(err.holder).toEqual({ name: 'Ayşe', client: 'desktop', label: 'filex, macOS' });
    expect(err.fields.retry_after).toBe(30);
  });

  it('VAULT_LOCK_LOST carries the reason; VAULT_GENERATION the latest', async () => {
    stubFetch((c) =>
      c.url.includes('/renew')
        ? answer(409, { error: 'VAULT_LOCK_LOST', message: '', reason: 'taken' })
        : answer(409, { error: 'VAULT_GENERATION', message: '', latest: 7 }),
    );
    const api = createVaultApi(http());
    await expect(api.renew('docs://Kasa', 'T', false)).rejects.toMatchObject({ code: 'VAULT_LOCK_LOST', reason: 'taken' });
    await expect(api.putIndex('docs://Kasa', 6, new Uint8Array(65536), 'T')).rejects.toMatchObject({ code: 'VAULT_GENERATION', latest: 7 });
  });

  it('a pack upload of unknown outcome, or an id that is taken: sent again under a new id', async () => {
    const api = createVaultApi(http());
    stubFetch(() => {
      throw new TypeError('Failed to fetch');
    });
    await expect(api.putPack('docs://Kasa', 'b067d7bcd62c9f817216a5ef1b1b653b', new Uint8Array(8), 'T')).rejects.toBeInstanceOf(VaultPackRetry);
    vi.unstubAllGlobals();
    stubFetch(() => answer(409, { error: 'VAULT_PACK_EXISTS', message: '' }));
    await expect(api.putPack('docs://Kasa', 'b067d7bcd62c9f817216a5ef1b1b653b', new Uint8Array(8), 'T')).rejects.toBeInstanceOf(VaultPackRetry);
    vi.unstubAllGlobals();
    stubFetch(() => answer(400, { error: 'VAULT_BAD_OBJECT', message: '' }));
    await expect(api.putPack('docs://Kasa', 'b067d7bcd62c9f817216a5ef1b1b653b', new Uint8Array(8), 'T')).rejects.toMatchObject({ code: 'VAULT_BAD_OBJECT' });
  });

  it('the page closes: release with keepalive, never awaited', () => {
    const calls = stubFetch(() => answer(204));
    createVaultApi(http()).releaseKeepalive('docs://Kasa', 'T', { 'X-CSRF-TOKEN': 'csrf' });
    expect(calls).toHaveLength(1);
    expect(calls[0].keepalive).toBe(true);
    expect(calls[0].headers[VAULT_LOCK_HEADER]).toBe('T');
    expect(calls[0].url).toBe('/api/files/e2e/vault/lock/release');
  });

  it('prefs: the idle time, in minutes', async () => {
    const calls = stubFetch((c) => answer(200, { idle_minutes: c.method === 'PUT' ? 7 : 3 }));
    const api = createVaultApi(http());
    expect((await api.getPrefs()).idle_minutes).toBe(3);
    expect((await api.putPrefs(7)).idle_minutes).toBe(7);
    expect(JSON.parse(String(calls[1].body))).toEqual({ idle_minutes: 7 });
  });

  it('a listing: every page, and the clock of the SERVER (retention is measured on it)', async () => {
    const calls = stubFetch((c) =>
      c.url.includes('after=p2')
        ? answer(200, { items: [{ generation: 3, size: 65536, mtime: '2026-10-06T19:00:00Z' }], next: null, now: '2026-10-06T19:30:00Z' })
        : answer(200, { items: [{ generation: 1, size: 65536, mtime: '2026-10-06T18:00:00Z' }], next: 'p2', now: '2026-10-06T19:29:59Z' }),
    );
    const got = await createVaultApi(http()).listAll('docs://Kasa', 'index');
    expect(got.items.map((i) => i.generation)).toEqual([1, 3]);
    expect(got.now).toBe(Date.parse('2026-10-06T19:29:59Z'));
    expect(calls[0].url).toContain('kind=index');
    expect(calls[1].url).toContain('after=p2');
  });

  it('a listing time, as ISO or as a number', () => {
    expect(listedTime('2026-10-06T10:00:00Z')).toBe(Date.parse('2026-10-06T10:00:00Z'));
    expect(listedTime(1_791_277_200)).toBe(1_791_277_200_000);
    expect(listedTime(1_791_277_200_000)).toBe(1_791_277_200_000);
  });
});
