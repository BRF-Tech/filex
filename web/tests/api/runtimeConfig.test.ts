// Tests for src/api/runtimeConfig.ts — the runtime API-base + token seam that
// Dilim 2 (Electron) rides on. The load-bearing property here is REGRESSION
// SAFETY: with nothing injected, the web build must behave exactly as before
// (base '/api', sessionStorage bearer). The override paths exist only for the
// desktop shell.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// Each test re-imports the module so the module-level base/token singletons
// start from their defaults (they persist across calls by design).
async function freshConfig() {
  vi.resetModules();
  return await import('@/api/runtimeConfig');
}

describe('api/runtimeConfig', () => {
  beforeEach(() => {
    sessionStorage.clear();
    // Ensure no injected global leaks between tests.
    delete (window as unknown as { __FILEX_RUNTIME__?: unknown }).__FILEX_RUNTIME__;
  });

  afterEach(() => {
    delete (window as unknown as { __FILEX_RUNTIME__?: unknown }).__FILEX_RUNTIME__;
  });

  it('defaults to same-origin /api when nothing is injected or set', async () => {
    const cfg = await freshConfig();
    expect(cfg.getApiBaseUrl()).toBe('/api');
    expect(cfg.DEFAULT_API_BASE).toBe('/api');
  });

  it('setApiBaseUrl overrides the base and trims whitespace', async () => {
    const cfg = await freshConfig();
    cfg.setApiBaseUrl('  https://fm.brf.sh/api  ');
    expect(cfg.getApiBaseUrl()).toBe('https://fm.brf.sh/api');
  });

  it('setApiBaseUrl with empty/blank/null resets to the default (never bricks)', async () => {
    const cfg = await freshConfig();
    cfg.setApiBaseUrl('https://x/api');
    cfg.setApiBaseUrl('');
    expect(cfg.getApiBaseUrl()).toBe('/api');
    cfg.setApiBaseUrl('https://x/api');
    cfg.setApiBaseUrl('   ');
    expect(cfg.getApiBaseUrl()).toBe('/api');
    cfg.setApiBaseUrl('https://x/api');
    cfg.setApiBaseUrl(null);
    expect(cfg.getApiBaseUrl()).toBe('/api');
  });

  it('getBearerToken falls back to sessionStorage when no runtime token set', async () => {
    const cfg = await freshConfig();
    sessionStorage.setItem('filex.bearer', 'sess-tok');
    expect(cfg.getBearerToken()).toBe('sess-tok');
  });

  it('runtime token takes precedence over the sessionStorage token', async () => {
    const cfg = await freshConfig();
    sessionStorage.setItem('filex.bearer', 'sess-tok');
    cfg.setBearerToken('runtime-tok');
    expect(cfg.getBearerToken()).toBe('runtime-tok');
  });

  it('setBearerToken(null) clears the runtime token, falling back to session', async () => {
    const cfg = await freshConfig();
    sessionStorage.setItem('filex.bearer', 'sess-tok');
    cfg.setBearerToken('runtime-tok');
    cfg.setBearerToken(null);
    expect(cfg.getBearerToken()).toBe('sess-tok');
  });

  it('getBearerToken returns null when neither source has a token', async () => {
    const cfg = await freshConfig();
    expect(cfg.getBearerToken()).toBeNull();
  });

  it('initRuntimeConfig picks up an injected window.__FILEX_RUNTIME__', async () => {
    (window as unknown as { __FILEX_RUNTIME__?: unknown }).__FILEX_RUNTIME__ = {
      apiBaseUrl: 'https://remote.example/api',
      bearerToken: 'injected-tok',
    };
    const cfg = await freshConfig();
    cfg.initRuntimeConfig();
    expect(cfg.getApiBaseUrl()).toBe('https://remote.example/api');
    expect(cfg.getBearerToken()).toBe('injected-tok');
  });

  it('initRuntimeConfig is a no-op when the global is absent (web build)', async () => {
    const cfg = await freshConfig();
    cfg.initRuntimeConfig();
    expect(cfg.getApiBaseUrl()).toBe('/api');
    expect(cfg.getBearerToken()).toBeNull();
  });

  // The web app's identity IS the session cookie, so credentials must stay on
  // unless something explicitly turns them off.
  it('credentials default to on, and stay on when the global omits the field', async () => {
    const cfg = await freshConfig();
    expect(cfg.getUseCredentials()).toBe(true);

    (window as unknown as { __FILEX_RUNTIME__?: unknown }).__FILEX_RUNTIME__ = {
      apiBaseUrl: 'https://remote.example/api',
    };
    cfg.initRuntimeConfig();
    expect(cfg.getUseCredentials()).toBe(true);
  });

  // Electron injects false because a credentialed request cannot be answered
  // with `Access-Control-Allow-Origin: *` — filex's default — so every
  // cross-origin call from the app:// renderer would be rejected.
  it('an injected useCredentials:false turns credentials off', async () => {
    (window as unknown as { __FILEX_RUNTIME__?: unknown }).__FILEX_RUNTIME__ = {
      apiBaseUrl: 'https://remote.example/api',
      bearerToken: 'tok',
      useCredentials: false,
    };
    const cfg = await freshConfig();
    cfg.initRuntimeConfig();
    expect(cfg.getUseCredentials()).toBe(false);
  });
});
