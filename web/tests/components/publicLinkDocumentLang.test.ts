/**
 * A public page owns `<html lang>` FROM ITS FIRST PAINT, not from the first
 * time somebody presses a language.
 *
 * ⚠⚠ Why this exists next to e2e/131. That spec measures the whole thing in a
 * browser — the picker, the document element, `dir` derived from it — but it
 * cannot measure the `immediate` on the watcher: in the WEB app the panel's
 * own boot (`applyStoredLocale`) has already stamped the very same value
 * before the page mounts, so removing `immediate` leaves that spec green
 * (measured 2026-09-24, mutation B3 survived). The flag is load-bearing for a
 * host that mounts this page with a language of its OWN — the desktop shell
 * and an embed — and that is exactly what is mounted here.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import PublicLinkPage from '@brftech/filex-core/src/components/public/PublicLinkPage.vue';

/* ⚠ The page asks for its instance's branding on mount. Left to happy-dom
   that request is still in flight at teardown and the run prints an
   AbortError over every assertion; answered here with an empty instance, the
   page draws its "not available" state and nothing is pending. */
beforeEach(() => {
  document.documentElement.lang = 'en';
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })),
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  document.documentElement.lang = 'en';
});

describe('PublicLinkPage and the document element', () => {
  it('stamps the host’s language on the first paint, with nothing pressed', async () => {
    // ⚠ An empty token: the page draws "not available" and asks the server
    // for nothing, which is all this needs. The language is the host's.
    const w = mount(PublicLinkPage, { props: { kind: 'share' as const, token: '', locale: 'tr' } });
    await nextTick();
    expect(
      document.documentElement.lang,
      'the page’s language has to reach the document before anybody clicks',
    ).toBe('tr');
    w.unmount();
  });

  it('follows the host when it changes its mind', async () => {
    const w = mount(PublicLinkPage, { props: { kind: 'share' as const, token: '', locale: 'en' } });
    await nextTick();
    expect(document.documentElement.lang).toBe('en');
    await w.setProps({ locale: 'tr' });
    await nextTick();
    expect(document.documentElement.lang).toBe('tr');
    w.unmount();
  });
});
