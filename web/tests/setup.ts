// Global test setup. Runs once per test file before suites execute.
//
// - Provides matchMedia, ResizeObserver, IntersectionObserver stubs that
//   most components touch indirectly via Tailwind/Headless UI.
// - Resets sessionStorage / localStorage / document.cookie between tests
//   so Pinia stores backed by them don't leak state.
import { afterEach, vi } from 'vitest';

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
afterEach(() => {
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
});
