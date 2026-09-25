/**
 * #57 — an open public page (a share, a file request, an app's signing page)
 * turns with the operating system when nobody forced a mode.
 *
 * Measured 2026-09-25 in a browser: opened dark, the OS turned light, and the
 * page stayed dark — `fe--theme-dark` pinned on its root, because the page
 * resolved `prefers-color-scheme` inside a computed Vue cannot re-run. The
 * other direction LOOKED right only by accident (the host's `.dark .fe` rule
 * leaked the dark tokens past a root that still said `fe--theme-light`), so a
 * signature pad drawn on the page sat in a card of the wrong mode's
 * derivations.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import PublicLinkPage from '@brftech/filex-core/src/components/public/PublicLinkPage.vue';

type Listener = (e: { matches: boolean }) => void;
let osDark = true;
const listeners = new Set<Listener>();

function osTurns(dark: boolean) {
  osDark = dark;
  for (const l of listeners) l({ matches: dark });
}

beforeEach(() => {
  osDark = true;
  listeners.clear();
  localStorage.clear();
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })),
  );
  vi.spyOn(window, 'matchMedia').mockImplementation(
    (q: string) =>
      ({
        get matches() {
          return q.includes('dark') ? osDark : false;
        },
        media: q,
        addEventListener: (_t: string, l: Listener) => listeners.add(l),
        removeEventListener: (_t: string, l: Listener) => listeners.delete(l),
      }) as unknown as MediaQueryList,
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('PublicLinkPage follows the operating system while it is open', () => {
  it('dark → light → dark, on the page’s own root', async () => {
    const w = mount(PublicLinkPage, { props: { kind: 'share' as const, token: '', locale: 'en' } });
    await nextTick();
    const root = () => w.find('[data-testid="public-page"]').classes();
    expect(root()).toContain('fe--theme-dark');

    osTurns(false);
    await nextTick();
    expect(root(), 'the OS turned light and the open page must turn with it').toContain('fe--theme-light');
    expect(root()).not.toContain('fe--theme-dark');

    osTurns(true);
    await nextTick();
    expect(root()).toContain('fe--theme-dark');
    w.unmount();
    expect(listeners.size, 'the listener leaves with the page').toBe(0);
  });

  it('a mode the host forced is not overruled by the OS', async () => {
    const w = mount(PublicLinkPage, { props: { kind: 'share' as const, token: '', locale: 'en', theme: 'light' } });
    await nextTick();
    osTurns(true);
    await nextTick();
    expect(w.find('[data-testid="public-page"]').classes()).toContain('fe--theme-light');
    w.unmount();
  });
});
