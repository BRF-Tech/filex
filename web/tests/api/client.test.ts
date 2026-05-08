// Tests for src/api/client.ts — mainly the interceptor behaviour
// (CSRF header injection, bearer fallback, 401 → onUnauthorized).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Router } from 'vue-router';

// Helper to build a fake-router stub with the bare minimum surface
// installAxiosInterceptors touches.
function fakeRouter(name: string = 'home'): Router {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return {
    currentRoute: {
      value: { name },
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
    expect(extractError({})).toBe('Unknown error');
    expect(extractError({}, 'fallback')).toBe('fallback');
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

  it('response interceptor calls onError for network errors (no response)', async () => {
    const { api, installAxiosInterceptors } = await freshClient();
    const onError = vi.fn();
    installAxiosInterceptors({ router: fakeRouter('home'), onError });

    const handlers = (api.interceptors.response as unknown as { handlers: Array<{ rejected: (err: unknown) => Promise<unknown> }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const err = { response: undefined, message: 'Network Error' };
    await expect(handler.rejected(err)).rejects.toBe(err);
    expect(onError).toHaveBeenCalledWith('Network Error');
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
