// Tests for src/api/client.ts — mainly the interceptor behaviour
// (CSRF header injection, bearer fallback, 401 → onUnauthorized).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Router } from 'vue-router';

import { api, installAxiosInterceptors, extractError } from '@/api/client';

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

describe('api/client', () => {
  // Keep references so we can detach interceptors between tests if needed.
  const originalUseRequest = api.interceptors.request.use;
  const originalUseResponse = api.interceptors.response.use;

  beforeEach(() => {
    // Each test gets its own fresh interceptor set.
    sessionStorage.clear();
    document.cookie = 'filex_csrf=; Max-Age=0; path=/';
  });

  afterEach(() => {
    // Eject any installed interceptors. axios doesn't expose a direct way
    // to flush, so we restore the .use() reference (no-op for tests that
    // didn't install).
    api.interceptors.request.use = originalUseRequest;
    api.interceptors.response.use = originalUseResponse;
  });

  it('extractError returns a string from various error shapes', () => {
    expect(extractError(new Error('boom'))).toBe('boom');
    expect(
      extractError({ isAxiosError: true, response: { data: { error: 'pretty' } } } as never),
    ).toBe('pretty');
    expect(extractError({})).toBe('Unknown error');
    expect(extractError({}, 'fallback')).toBe('fallback');
  });

  it('request interceptor sets X-CSRF-Token from cookie on POST', async () => {
    document.cookie = 'filex_csrf=csrf-abc; path=/';
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
    const onUnauthorized = vi.fn();
    installAxiosInterceptors({ router: fakeRouter('home'), onUnauthorized });

    const handlers = (api.interceptors.response as unknown as { handlers: Array<{ rejected: (err: unknown) => Promise<unknown> }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const err = { response: { status: 401 }, message: '401' };

    await expect(handler.rejected(err)).rejects.toBe(err);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('response interceptor does NOT call onUnauthorized when on /admin/login', async () => {
    const onUnauthorized = vi.fn();
    installAxiosInterceptors({ router: fakeRouter('login'), onUnauthorized });

    const handlers = (api.interceptors.response as unknown as { handlers: Array<{ rejected: (err: unknown) => Promise<unknown> }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const err = { response: { status: 401 }, message: '401' };
    await expect(handler.rejected(err)).rejects.toBe(err);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('response interceptor calls onError for network errors (no response)', async () => {
    const onError = vi.fn();
    installAxiosInterceptors({ router: fakeRouter('home'), onError });

    const handlers = (api.interceptors.response as unknown as { handlers: Array<{ rejected: (err: unknown) => Promise<unknown> }> }).handlers;
    const handler = handlers[handlers.length - 1];
    const err = { response: undefined, message: 'Network Error' };
    await expect(handler.rejected(err)).rejects.toBe(err);
    expect(onError).toHaveBeenCalledWith('Network Error');
  });

  it('installAxiosInterceptors is idempotent', () => {
    installAxiosInterceptors({ router: fakeRouter() });
    const before = (api.interceptors.request as unknown as { handlers: unknown[] }).handlers.length;
    installAxiosInterceptors({ router: fakeRouter() });
    const after = (api.interceptors.request as unknown as { handlers: unknown[] }).handlers.length;
    expect(after).toBe(before);
  });
});
