// Tests for src/api/client.ts — mainly the interceptor behaviour
// (CSRF header injection, bearer fallback, 401 → onUnauthorized).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Router } from 'vue-router';
import fs from 'node:fs';
import path from 'node:path';
import en from '@/locales/en.json';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';

// Helper to build a fake-router stub with the bare minimum surface
// installAxiosInterceptors touches. A settled route always has a non-empty
// `matched`; pass matched: [] to simulate the cold-load START_LOCATION.
function fakeRouter(name: string | undefined = 'home', matched: unknown[] = [{}]): Router {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return {
    currentRoute: {
      value: { name, matched },
    },
  } as unknown as Router;
}

// Helper: reset module so installAxiosInterceptors' module-level singleton
// (interceptorsInstalled) is fresh for each test. Returns a fresh import.
async function freshClient() {
  vi.resetModules();
  return await import('@/api/client');
}

describe('api/client', () => {
  beforeEach(() => {
    // Each test gets its own fresh interceptor set.
    sessionStorage.clear();
    document.cookie = 'filex_csrf=; Max-Age=0; path=/';
  });

  afterEach(() => {
    // No-op: vi.resetModules() at the top of each test gives us a clean
    // axios instance, so leaked interceptors from prior tests can't bleed.
  });

  it('extractError returns a string from various error shapes', async () => {
    const { extractError } = await freshClient();
    expect(extractError(new Error('boom'))).toBe('boom');
    expect(
      extractError({ isAxiosError: true, response: { data: { error: 'pretty' } } } as never),
    ).toBe('pretty');
    expect(extractError({})).toBe(en.errors.generic);
    expect(extractError({}, 'fallback')).toBe('fallback');
    // No fallback given: the panel's own words, never "Unknown error".
    expect(extractError({})).not.toBe('Unknown error');
    expect(extractError({})).toBeTruthy();
    // axios's English is not shown: a response with no message → fallback,
    // no response at all → the network sentence.
    const noBody = { isAxiosError: true, message: 'Request failed with status code 502', response: { status: 502, data: '<html>' } };
    expect(extractError(noBody as never, 'fallback')).toBe('fallback');
    const offline = { isAxiosError: true, message: 'Network Error', response: undefined };
    expect(extractError(offline as never, 'fallback')).not.toContain('Network Error');
  });

  // ⚠ Wave-2 wording sweep: with no sentence from the server, extractError
  // printed axios's own English — "Network Error", "Request failed with
  // status code 500" — whatever the interface language, and ~50 callers
  // passed English fallbacks ("Failed to load users"). The words now come
  // from the catalogue: the network, the status's words when they tell the
  // person something, the caller's words for what it was doing otherwise.
  it('extractError never prints axios plumbing', async () => {
    const { extractError } = await freshClient();
    const net = { isAxiosError: true, message: 'Network Error' };
    expect(extractError(net as never, 'fallback')).toBe(en.errors.network);
    const bare = (status: number) => ({ isAxiosError: true, message: `Request failed with status code ${status}`, response: { status, data: {} } });
    // a 5xx says only "it failed" — the caller's words say what failed
    expect(extractError(bare(500) as never, en.errors.saveFailed)).toBe(en.errors.saveFailed);
    // a refusal the person can act on is said as itself
    expect(extractError(bare(403) as never, en.errors.saveFailed)).toBe(coreEn['err.status.403']);
    expect(extractError(bare(413) as never, en.errors.saveFailed)).toBe(coreEn['err.status.413']);
    // no caller words: the status's own
    expect(extractError(bare(500) as never)).toBe(coreEn['err.status.500']);
    for (const s of [400, 403, 404, 409, 500, 502]) {
      expect(extractError(bare(s) as never, 'x')).not.toMatch(/Request failed|status code/);
    }
  });

  it('no caller passes an English fallback any more', () => {
    const walk = (dir: string, out: string[] = []): string[] => {
      for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
        const p = path.join(dir, e.name);
        if (e.isDirectory()) walk(p, out);
        else if (/\.(ts|vue)$/.test(e.name)) out.push(p);
      }
      return out;
    };
    const english: string[] = [];
    for (const f of walk(path.resolve(__dirname, '../../src'))) {
      const src = fs.readFileSync(f, 'utf8');
      for (const m of src.matchAll(/extractError\([^,()]+,\s*'([^']+)'\)/g)) english.push(`${path.basename(f)}: '${m[1]}'`);
    }
    expect(walk(path.resolve(__dirname, '../../src')).length, 'a scan of nothing passes everything').toBeGreaterThan(100);
    expect(english).toEqual([]);
  });

  // A 423 is an app's freeze (handlers.lockedAnswer): the person reads which
  // app holds the file and why, not the server's English `message` with a path.
  it('extractError says which app froze the file on a 423', async () => {
    const { extractError } = await freshClient();
    const locked = (data: unknown) =>
      extractError({ isAxiosError: true, response: { status: 423, data } } as never, 'fallback');
    const said = locked({
      error: 'locked',
      message: 'locked by app sign: Sozlesmeler/NDA.docx',
      plugin: 'sign',
      reason: 'imzalar toplanıyor',
      path: 'Sozlesmeler/NDA.docx',
    });
    expect(said).toContain('sign');
    expect(said).toContain('imzalar toplanıyor');
    expect(said).not.toContain('locked by app');
    expect(locked({})).not.toBe('fallback');
  });

  it('request interceptor sets X-CSRF-Token from cookie on POST', async () => {
    document.cookie = 'filex_csrf=csrf-abc; path=/';
    const { api, installAxiosInterceptors } = await freshClient();
    installAxiosInterceptors({ router: fakeRouter() });

    const handlers = (api.interceptors.request as unknown as { handlers: Array<{ fulfilled: (cfg: { method?: string; headers?: Record<string, string> }) => unknown }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const out = (await handler.fulfilled({
      method: 'post',
      headers: {} as Record<string, string>,
    })) as { headers: Record<string, string> };
    expect(out.headers['X-CSRF-Token']).toBe('csrf-abc');
  });

  it('request interceptor does NOT add CSRF for GET', async () => {
    document.cookie = 'filex_csrf=csrf-abc; path=/';
    const { api, installAxiosInterceptors } = await freshClient();
    installAxiosInterceptors({ router: fakeRouter() });

    const handlers = (api.interceptors.request as unknown as { handlers: Array<{ fulfilled: (cfg: { method?: string; headers?: Record<string, string> }) => unknown }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const out = (await handler.fulfilled({
      method: 'get',
      headers: {} as Record<string, string>,
    })) as { headers: Record<string, string> };
    expect(out.headers['X-CSRF-Token']).toBeUndefined();
  });

  it('request interceptor adds Authorization bearer when present', async () => {
    sessionStorage.setItem('filex.bearer', 'tkn-1');
    const { api, installAxiosInterceptors } = await freshClient();
    installAxiosInterceptors({ router: fakeRouter() });

    const handlers = (api.interceptors.request as unknown as { handlers: Array<{ fulfilled: (cfg: { method?: string; headers?: Record<string, string> }) => unknown }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const out = (await handler.fulfilled({
      method: 'get',
      headers: {} as Record<string, string>,
    })) as { headers: Record<string, string> };
    expect(out.headers.Authorization).toBe('Bearer tkn-1');
  });

  it('request interceptor sets config.baseURL to /api by default (web unchanged)', async () => {
    const { api, installAxiosInterceptors } = await freshClient();
    installAxiosInterceptors({ router: fakeRouter() });

    const handlers = (api.interceptors.request as unknown as { handlers: Array<{ fulfilled: (cfg: { method?: string; headers?: Record<string, string>; baseURL?: string }) => unknown }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const out = (await handler.fulfilled({
      method: 'get',
      headers: {} as Record<string, string>,
    })) as { baseURL?: string };
    expect(out.baseURL).toBe('/api');
  });

  it('request interceptor honours a runtime API base override (Electron path)', async () => {
    vi.resetModules();
    const runtime = await import('@/api/runtimeConfig');
    runtime.setApiBaseUrl('https://fm.example.com/api');
    // client.ts must import the SAME runtimeConfig module instance so the
    // override is visible; freshClient()'s resetModules would give it a
    // different one, so import client without resetting here.
    const { api, installAxiosInterceptors } = await import('@/api/client');
    installAxiosInterceptors({ router: fakeRouter() });

    const handlers = (api.interceptors.request as unknown as { handlers: Array<{ fulfilled: (cfg: { method?: string; headers?: Record<string, string>; baseURL?: string }) => unknown }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const out = (await handler.fulfilled({
      method: 'get',
      headers: {} as Record<string, string>,
    })) as { baseURL?: string };
    expect(out.baseURL).toBe('https://fm.example.com/api');
    runtime.setApiBaseUrl(''); // reset so later tests see the default
  });

  it('response interceptor calls onUnauthorized for 401 outside login', async () => {
    const { api, installAxiosInterceptors } = await freshClient();
    const onUnauthorized = vi.fn();
    installAxiosInterceptors({ router: fakeRouter('home'), onUnauthorized });

    const handlers = (api.interceptors.response as unknown as { handlers: Array<{ rejected: (err: unknown) => Promise<unknown> }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const err = { response: { status: 401 }, message: '401' };

    await expect(handler.rejected(err)).rejects.toBe(err);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('response interceptor does NOT call onUnauthorized when on /admin/login', async () => {
    const { api, installAxiosInterceptors } = await freshClient();
    const onUnauthorized = vi.fn();
    installAxiosInterceptors({ router: fakeRouter('login'), onUnauthorized });

    const handlers = (api.interceptors.response as unknown as { handlers: Array<{ rejected: (err: unknown) => Promise<unknown> }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const err = { response: { status: 401 }, message: '401' };
    await expect(handler.rejected(err)).rejects.toBe(err);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('response interceptor does NOT call onUnauthorized during initial navigation', async () => {
    // Cold load: currentRoute is still the START_LOCATION (nothing matched).
    // The router guard owns routing here — a push would race the pending
    // navigation and strip the login page's query params (?local=1 etc.).
    const { api, installAxiosInterceptors } = await freshClient();
    const onUnauthorized = vi.fn();
    installAxiosInterceptors({ router: fakeRouter(undefined, []), onUnauthorized });

    const handlers = (api.interceptors.response as unknown as { handlers: Array<{ rejected: (err: unknown) => Promise<unknown> }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const err = { response: { status: 401 }, message: '401' };
    await expect(handler.rejected(err)).rejects.toBe(err);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  // ⚠⚠ ONE notice, not one per request. The owner, 2026-09-24: "attığı tüm
  // istekler için ayrı ayrı atıyor o yüzden çok fazla popover çıkıyor". Every
  // answerless refusal used to call `onError`, and the explorer alone fires
  // several calls a second — so a dropped connection filled the corner with
  // copies of one sentence. A read is folded into the shared notice (core
  // lib/connection); a write is something the person is waiting on and still
  // speaks for itself.
  //
  // ⚠ The core module is imported AFTER `freshClient()`: `vi.resetModules()`
  // hands `client.ts` a fresh copy of it, and a copy imported at the top of
  // this file would be a different instance holding different state.
  async function withClient() {
    const { api, installAxiosInterceptors } = await freshClient();
    const core = await import('@brftech/filex-core');
    core.resetConnectionNotice();
    const onError = vi.fn();
    installAxiosInterceptors({ router: fakeRouter('home'), onError });
    const handlers = (
      api.interceptors.response as unknown as {
        handlers: Array<{
          fulfilled: (r: unknown) => unknown;
          rejected: (err: unknown) => Promise<unknown>;
        }>;
      }
    ).handlers;
    return { core, onError, handler: handlers[handlers.length - 1] };
  }

  it('folds a storm of failed background calls into one notice', async () => {
    const { core, onError, handler } = await withClient();
    // A listing, its thumbnails and two polls, all answerless.
    for (let i = 0; i < 12; i++) {
      const err = { config: { method: 'get' }, response: undefined, message: 'Network Error' };
      await expect(handler.rejected(err)).rejects.toBe(err);
    }
    expect(onError, 'twelve failures, no toasts at all').not.toHaveBeenCalled();
    expect(core.connectionDown.value, 'one notice instead').toBe(true);
    expect(core.connectionFolded.value).toBe(12);
  });

  it('still reports a failed write the person started', async () => {
    const { core, onError, handler } = await withClient();
    const err = { config: { method: 'post' }, response: undefined, message: 'Network Error' };
    await expect(handler.rejected(err)).rejects.toBe(err);
    // ⚠ In the interface language — not axios's English "Network Error".
    expect(onError).toHaveBeenCalledTimes(1);
    expect(onError).toHaveBeenCalledWith(en.errors.network);
    expect(core.connectionDown.value, 'and the shared notice is up too').toBe(true);
  });

  it('takes the notice down again as soon as anything is answered', async () => {
    const { core, onError, handler } = await withClient();
    const err = { config: { method: 'get' }, response: undefined, message: 'Network Error' };
    await expect(handler.rejected(err)).rejects.toBe(err);
    expect(core.connectionDown.value).toBe(true);

    handler.fulfilled({ status: 200, data: {} });
    expect(core.connectionDown.value, 'nobody dismisses a stale notice').toBe(false);

    // A refusal is an answer too: the server spoke, so the connection is not
    // what is wrong and the strip must not keep saying it is.
    await expect(handler.rejected(err)).rejects.toBe(err);
    expect(core.connectionDown.value).toBe(true);
    const refused = { config: { method: 'get' }, response: { status: 500, data: {} } };
    await expect(handler.rejected(refused)).rejects.toBe(refused);
    expect(core.connectionDown.value).toBe(false);
    expect(onError, 'and a 500 is the caller’s to report, not the network’s').not.toHaveBeenCalled();
  });

  it('installAxiosInterceptors is idempotent', async () => {
    const { api, installAxiosInterceptors } = await freshClient();
    installAxiosInterceptors({ router: fakeRouter() });
    const before = (api.interceptors.request as unknown as { handlers: unknown[] }).handlers.length;
    installAxiosInterceptors({ router: fakeRouter() });
    const after = (api.interceptors.request as unknown as { handlers: unknown[] }).handlers.length;
    expect(after).toBe(before);
  });
});
