// A store's install link and the router's guard (router/index.ts,
// lib/storeLink.ts; store fe review): what reaches the guard instead of a
// page load.
//
// ⚠ The router reads the address once, at module load, so each case puts the
// window on its address and imports the router (and the auth store it uses)
// fresh, as web/tests/api/homeLanding.test.ts does.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Router } from 'vue-router';

// The tab title's brand name (router afterEach → lib/documentTitle) asks the
// server; nothing here needs it.
vi.mock('@/api/branding', () => ({
  BrandingApi: { get: vi.fn().mockResolvedValue({}), boot: vi.fn().mockResolvedValue({}) },
}));

const LINK = '#store=https%3A%2F%2Ffapps.brfd.app&intent=tok_secondlink0001';

interface Fresh {
  router: Router;
  link: typeof import('@/lib/storeLink');
  signIn: (role: 'admin' | 'user' | null) => void;
}

async function freshAt(path: string): Promise<Fresh> {
  window.history.replaceState({}, '', path);
  vi.resetModules();
  const { createPinia, setActivePinia } = await import('pinia');
  setActivePinia(createPinia());
  const router = (await import('@/router')).default;
  const link = await import('@/lib/storeLink');
  const { useAuthStore } = await import('@/stores/auth');
  const signIn = (role: 'admin' | 'user' | null) => {
    const auth = useAuthStore();
    auth.user = role ? ({ id: 1, email: 'a@test.local', name: 'a', role } as unknown as typeof auth.user) : null;
    auth.ready = true;
  };
  return { router, link, signIn };
}

describe('a store link the router meets', () => {
  beforeEach(() => {
    sessionStorage.clear();
  });

  // store fe review #2: in a tab already on the store page, a second link is
  // a same-document fragment navigation - no page load, vue-router wrote the
  // token back into the address and the page never read it.
  it('a second link in a tab on the store page: taken off the address, kept for the page, the page told', async () => {
    const { router, link, signIn } = await freshAt('/admin/store-install');
    signIn('admin');
    await router.push('/store-install');
    const before = link.storeLinkArrivals.value;
    await router.push('/store-install' + LINK);
    expect(router.currentRoute.value.name).toBe('store-install');
    expect(router.currentRoute.value.hash).toBe('');
    expect(window.location.href).not.toContain('tok_secondlink0001');
    expect(link.storeLinkArrivals.value).toBe(before + 1);
    expect(link.takeStoreLink()).toEqual({ store: 'https://fapps.brfd.app', token: 'tok_secondlink0001' });
  }, 20_000);

  it('the same, when the browser made the move (popstate after a fragment navigation)', async () => {
    const { router, link, signIn } = await freshAt('/admin/store-install');
    signIn('admin');
    await router.push('/store-install');
    const before = link.storeLinkArrivals.value;
    window.history.pushState(null, '', '/admin/store-install' + LINK);
    window.dispatchEvent(new PopStateEvent('popstate', { state: null }));
    await vi.waitFor(() => expect(link.storeLinkArrivals.value).toBe(before + 1));
    await vi.waitFor(() => expect(window.location.href).not.toContain('tok_secondlink0001'));
    expect(window.location.pathname).toBe('/admin/store-install');
    expect(link.takeStoreLink()?.token).toBe('tok_secondlink0001');
  }, 20_000);

  it('signed out: the sign-in address carries no token, the link waits in the tab', async () => {
    const { router, link, signIn } = await freshAt('/admin/store-install');
    signIn(null);
    await router.push('/store-install' + LINK);
    expect(router.currentRoute.value.name).toBe('login');
    expect(router.currentRoute.value.fullPath).not.toContain('tok_secondlink0001');
    expect(String(router.currentRoute.value.query.redirect)).toBe('/store-install');
    expect(link.hasStoreLink()).toBe(true);
  }, 20_000);

  // e2e 202 on a slower machine (Firefox, WebKit): a 401 sent the panel to
  // the sign-in page while the session it still believed in bounced it off
  // to the start page; the session found over on the way, the sign-in address
  // named the sign-in address as the place to come back to
  // (`?redirect=/login?redirect=/store-install`).
  it('the session found over after the sign-in page bounced: the sign-in comes back to that page’s own return address', async () => {
    const { router, signIn } = await freshAt('/admin/store-install');
    signIn('admin');
    await router.push('/store-install');
    let ended = false;
    router.beforeEach((to) => {
      if (!ended && to.redirectedFrom?.name === 'login') {
        ended = true;
        signIn(null);
        return { path: to.path, query: to.query };
      }
      return true;
    });
    await router.push({ name: 'login', query: { redirect: '/store-install' } });
    expect(ended, 'the bounce happened').toBe(true);
    expect(router.currentRoute.value.name).toBe('login');
    expect(router.currentRoute.value.query.redirect).toBe('/store-install');
  }, 20_000);

  // store fe review #3: the token stayed in the tab of someone who is not an
  // administrator, alive for an administrator signing in later there.
  it('someone who is not an administrator keeps nothing of a waiting link', async () => {
    const { router, link, signIn } = await freshAt('/drive/');
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://fapps.brfd.app', token: 'tok_notadmin0001', at: Date.now() }));
    signIn('user');
    await router.push('/home');
    expect(sessionStorage.getItem('filex.storeLink')).toBeNull();
    expect(link.hasStoreLink()).toBe(false);
  }, 20_000);

  it('nor of a second link opened in their tab', async () => {
    const { router, link, signIn } = await freshAt('/drive/');
    signIn('user');
    await router.push('/home');
    await router.push('/store-install' + LINK);
    expect(window.location.href).not.toContain('tok_secondlink0001');
    expect(sessionStorage.getItem('filex.storeLink')).toBeNull();
    expect(link.hasStoreLink()).toBe(false);
  }, 20_000);

  // store fe review #5: an SSO sign-in came back to the panel's front door
  // (the server takes no return address), not to the link; the docs said it
  // came back. Now any sign-in does, by the router.
  it('an administrator signed in with a link waiting comes back to it from the front door, any page', async () => {
    const { router, link, signIn } = await freshAt('/admin/');
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://fapps.brfd.app', token: 'tok_backtoit0001', at: Date.now() }));
    signIn('admin');
    await router.push('/');
    expect(router.currentRoute.value.name).toBe('store-install');
    expect(link.hasStoreLink(), 'still waiting, for the page to read').toBe(true);
  }, 20_000);

  it('a link that waited over an hour does not bring the administrator anywhere', async () => {
    const { router, signIn } = await freshAt('/admin/');
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://fapps.brfd.app', token: 'tok_old0001', at: Date.now() - 2 * 3600_000 }));
    signIn('admin');
    await router.push('/dashboard');
    expect(router.currentRoute.value.name).toBe('dashboard');
  }, 20_000);

  it('a page load drops a link that waited over an hour, on any page', async () => {
    sessionStorage.setItem('filex.storeLink', JSON.stringify({ store: 'https://fapps.brfd.app', token: 'tok_stale0001', at: Date.now() - 2 * 3600_000 }));
    await freshAt('/admin/dashboard');
    expect(sessionStorage.getItem('filex.storeLink')).toBeNull();
  }, 20_000);
});
