// Global test setup. Runs once per test file before suites execute.
//
// - Provides matchMedia, ResizeObserver, IntersectionObserver stubs that
//   most components touch indirectly via Tailwind/Headless UI.
// - Resets sessionStorage / localStorage / document.cookie between tests
//   so Pinia stores backed by them don't leak state.
// - ⚠ Refuses the network (helpers/noNetwork): a request a test did not mock
//   fails that test, with the URL.
// - ⚠ Ends every test the one safe way (helpers/teardown): let what is in
//   flight land, unmount every mounted page, THEN empty <body>. Tests do not
//   do this themselves (task #127).
import { afterAll, afterEach, vi } from 'vitest';
import { failOnNetworkHits } from './helpers/noNetwork';
import { teardownDom } from './helpers/teardown';

// matchMedia stub — required by DarkModeToggle / theme.ts on cold boot.
if (!('matchMedia' in window)) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

// ResizeObserver stub — Headless UI Modal uses it for focus trap math.
if (!('ResizeObserver' in window)) {
  class StubResizeObserver {
    observe() {
      /* noop */
    }
    unobserve() {
      /* noop */
    }
    disconnect() {
      /* noop */
    }
  }
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (window as any).ResizeObserver = StubResizeObserver;
}

// IntersectionObserver stub — used by lazy-image components.
if (!('IntersectionObserver' in window)) {
  class StubIntersectionObserver {
    observe() {
      /* noop */
    }
    unobserve() {
      /* noop */
    }
    disconnect() {
      /* noop */
    }
    takeRecords() {
      return [];
    }
    root = null;
    rootMargin = '';
    thresholds = [];
  }
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (window as any).IntersectionObserver = StubIntersectionObserver;
}

// Reset DOM + browser stores between tests to avoid cross-test pollution.
// Registered here, it runs AFTER the test file's own afterEach hooks.
afterEach(async () => {
  await teardownDom();
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  if (typeof sessionStorage !== 'undefined') sessionStorage.clear();
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  if (typeof localStorage !== 'undefined') localStorage.clear();
  // Wipe cookies
  document.cookie.split(';').forEach((c) => {
    const eq = c.indexOf('=');
    const name = eq > -1 ? c.substring(0, eq).trim() : c.trim();
    if (name) document.cookie = `${name}=; Max-Age=0; path=/`;
  });
  vi.clearAllMocks();
  failOnNetworkHits('(this test)');
});

// A request that fires after the file's last test still fails the file.
afterAll(async () => {
  await teardownDom();
  failOnNetworkHits('after the last test of this file');
});
