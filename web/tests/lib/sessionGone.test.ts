// A 401 and the sign-in page (lib/sessionGone, wired in main.ts; task #199).
//
// e2e 202 on GitHub's full matrix (Chromium; runs 37598598805, 37661356185,
// 37702037032): a store link opened in a tab whose session had ended came
// back to Home after the sign-in (`?redirect=/home`), not to the store page,
// and the link was lost. The panel pushed to the sign-in page the moment a 401
// arrived, while it still believed in the session; the sign-in page's guard
// sent that "signed-in" reader on to the start page, whose own 401 pushed a
// second sign-in naming Home over the one the store page had asked for.
//
// ⚠ The real router and its guard (router/index.ts), as in
// storeLinkRouter.test.ts: the window is put on its address and the router
// imported fresh. The server's answer to "who is signed in" is held by the
// test, so the order the race needs is the order every case runs in.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Router } from 'vue-router';

// The tab title's brand name (router afterEach → lib/documentTitle) asks the
// server; nothing here needs it.
vi.mock('@/api/branding', () => ({
  BrandingApi: { get: vi.fn().mockResolvedValue({}), boot: vi.fn().mockResolvedValue({}) },
}));

type Role = 'admin' | 'user' | null;

interface Fresh {
  router: Router;
  signIn: (role: Role) => void;
  handlerFor: (askSession: () => Promise<unknown>) => () => Promise<void>;
  /** Every route the router settled on after the start, by name. */
  settled: string[];
}

async function freshAt(path: string): Promise<Fresh> {
  window.history.replaceState({}, '', path);
  vi.resetModules();
  const { createPinia, setActivePinia } = await import('pinia');
  setActivePinia(createPinia());
  const router = (await import('@/router')).default;
  const { unauthorizedHandler } = await import('@/lib/sessionGone');
  const { useAuthStore } = await import('@/stores/auth');
  const signIn = (role: Role) => {
    const auth = useAuthStore();
    auth.user = role ? ({ id: 1, email: 'a@test.local', name: 'a', role } as unknown as typeof auth.user) : null;
    auth.ready = true;
  };
  const settled: string[] = [];
  return {
    router,
    signIn,
    settled,
    handlerFor: (askSession) => {
      router.afterEach((to, _from, failure) => {
        if (!failure) settled.push(String(to.name));
      });
      return unauthorizedHandler({ router, askSession });
    },
  };
}

/** The server's answer to "who is signed in", held until the test lets it go. */
function heldAnswer(signIn: (role: Role) => void) {
  let release: (role: Role) => void = () => undefined;
  let asked = 0;
  const ask = () => {
    asked++;
    return new Promise<unknown>((ok) => {
      release = (role) => {
        // What stores/auth fetchMe does with the answer: the panel believes it.
        signIn(role);
        ok(role ? { id: 1, role } : null);
      };
    });
  };
  return { ask, answer: (role: Role) => release(role), asked: () => asked };
}

describe('a 401 and the sign-in page', () => {
  beforeEach(() => {
    sessionStorage.clear();
  });

  // Why the panel must not go first: while it believes in the session, the
  // sign-in page sends the reader on to the start page.
  it('the sign-in page, asked for while the panel believes in the session, sends the reader to the start page', async () => {
    const { router, signIn } = await freshAt('/admin/store-install');
    signIn('admin');
    await router.push('/store-install');
    await router.push({ name: 'login', query: { redirect: '/store-install' } });
    expect(router.currentRoute.value.name).toBe('home');
  }, 20_000);

  it('the session over: nothing moves until the server says so; then the sign-in names the page, never the start page on the way', async () => {
    const { router, signIn, handlerFor, settled } = await freshAt('/admin/store-install');
    signIn('admin');
    await router.push('/store-install');
    const server = heldAnswer(signIn);
    const handle = handlerFor(server.ask);

    const done = handle();
    await vi.waitFor(() => expect(server.asked()).toBe(1));
    // The page's other requests answer 401 too, and so does the question
    // itself: they join it.
    void handle();
    void handle();
    expect(server.asked(), 'one question').toBe(1);
    expect(router.currentRoute.value.name, 'nothing moved before the answer').toBe('store-install');
    expect(settled).toEqual([]);

    server.answer(null);
    await done;
    expect(router.currentRoute.value.name).toBe('login');
    expect(router.currentRoute.value.query.redirect).toBe('/store-install');
    expect(settled, 'the start page was never on the way').toEqual(['login']);
  }, 20_000);

  it('a 401 with the session alive (a wrong current password, a one-time code) leaves the reader where they are', async () => {
    const { router, signIn, handlerFor, settled } = await freshAt('/admin/store-install');
    signIn('admin');
    await router.push('/store-install');
    const server = heldAnswer(signIn);
    const done = handlerFor(server.ask)();
    await vi.waitFor(() => expect(server.asked()).toBe(1));
    server.answer('admin');
    await done;
    expect(router.currentRoute.value.name).toBe('store-install');
    expect(settled).toEqual([]);
  }, 20_000);

  // The store page handles its own 401 (StoreInstall.vue sessionEnded): it
  // may have gone to the sign-in page by the time the answer comes.
  it('already on the sign-in page when the answer comes: its return address stands', async () => {
    const { router, signIn, handlerFor } = await freshAt('/admin/store-install');
    signIn('admin');
    await router.push('/store-install');
    const server = heldAnswer(signIn);
    const done = handlerFor(server.ask)();
    await vi.waitFor(() => expect(server.asked()).toBe(1));
    signIn(null);
    await router.replace({ name: 'login', query: { redirect: '/store-install' } });
    server.answer(null);
    await done;
    expect(router.currentRoute.value.name).toBe('login');
    expect(router.currentRoute.value.query.redirect).toBe('/store-install');
  }, 20_000);

  it('a new question once the last one was answered', async () => {
    const { router, signIn, handlerFor } = await freshAt('/admin/store-install');
    signIn('admin');
    await router.push('/store-install');
    const server = heldAnswer(signIn);
    const handle = handlerFor(server.ask);
    const first = handle();
    await vi.waitFor(() => expect(server.asked()).toBe(1));
    server.answer('admin');
    await first;
    const second = handle();
    await vi.waitFor(() => expect(server.asked()).toBe(2));
    server.answer(null);
    await second;
    expect(router.currentRoute.value.name).toBe('login');
  }, 20_000);
});
